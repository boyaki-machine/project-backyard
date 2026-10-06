package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type workAllocation struct {
	Seq   int32   `json:"seq"`
	Share float64 `json:"share"`
}
type workRequest struct {
	RequestID        string           `json:"request_id"`
	Action           string           `json:"action"`
	Result           string           `json:"result"`
	Note             string           `json:"note"`
	ReportedActiveMS *int64           `json:"reported_active_ms"`
	Tokens           *int64           `json:"tokens"`
	Incomplete       bool             `json:"incomplete"`
	Allocations      []workAllocation `json:"allocations"`
}
type workView struct {
	TicketSeq        int32            `json:"ticket_seq"`
	ID               string           `json:"id"`
	ActorID          string           `json:"actor_id"`
	ClientKind       *string          `json:"client_kind"`
	ModelName        *string          `json:"model_name"`
	ModelVersion     *string          `json:"model_version"`
	State            string           `json:"state"`
	StartedAt        Time             `json:"started_at"`
	ChangedAt        Time             `json:"changed_at"`
	EndedAt          *Time            `json:"ended_at"`
	ActiveMS         int64            `json:"active_ms"`
	WaitingMS        int64            `json:"waiting_ms"`
	ReportedActiveMS *int64           `json:"reported_active_ms"`
	Result           *string          `json:"result"`
	Tokens           *int64           `json:"tokens"`
	Note             string           `json:"note"`
	Incomplete       bool             `json:"incomplete"`
	Allocations      []workAllocation `json:"allocations"`
}
type effortSummary struct {
	SessionsCount      int64    `json:"sessions_count"`
	OpenCount          int64    `json:"open_count"`
	IncompleteCount    int64    `json:"incomplete_count"`
	TokensMissing      int64    `json:"tokens_missing"`
	ActiveMS           float64  `json:"active_ms"`
	ElapsedMS          float64  `json:"elapsed_ms"`
	WaitingMS          float64  `json:"waiting_ms"`
	Tokens             float64  `json:"tokens"`
	SuggestedHours     *float64 `json:"suggested_hours"`
	SuggestedPoint     *float64 `json:"suggested_point"`
	PointVersion       string   `json:"point_version"`
	ActualHours        *float64 `json:"actual_hours"`
	ActualPoint        *float64 `json:"actual_point"`
	ActualPointVersion *string  `json:"actual_point_version"`
	ManualHours        bool     `json:"manual_hours"`
	ManualPoint        bool     `json:"manual_point"`
}

func effortPoint(ms float64) float64 {
	if ms == 0 {
		return 0
	}
	limits := []float64{30, 60, 120, 240, 480, 960}
	points := []float64{1, 2, 3, 5, 8, 13}
	for i, m := range limits {
		if ms <= m*60000 {
			return points[i]
		}
	}
	return 21
}
func workError(code apierr.Code, message string) *apierr.Error {
	return apierr.New(code).WithMessage(message)
}

func validateWork(req workRequest) *apierr.Error {
	if strings.TrimSpace(req.RequestID) == "" || len([]rune(req.RequestID)) > 128 {
		return usageInvalid("request_id", "再送を識別するrequest_idを128文字以内で指定してください")
	}
	if len([]rune(req.Note)) > 2000 {
		return usageInvalid("note", "記録は2000文字以内で指定してください")
	}
	if req.Tokens != nil && *req.Tokens < 0 || req.ReportedActiveMS != nil && *req.ReportedActiveMS < 0 {
		return usageInvalid("cost", "時間とトークンは0以上にしてください")
	}
	switch req.Action {
	case "start", "pause", "resume", "finish", "correct", "restore_auto":
	default:
		return usageInvalid("action", "作業の操作が正しくありません")
	}
	if req.Action == "pause" || req.Action == "correct" {
		if strings.TrimSpace(req.Note) == "" {
			return usageInvalid("note", "待機または訂正の理由を指定してください")
		}
	}
	if req.Action == "finish" && !contains(reportStatuses, req.Result) {
		return usageInvalid("result", "完了・一部完了・進行不可の結果を指定してください")
	}
	if req.Action != "finish" && req.Action != "correct" && (req.Tokens != nil || req.ReportedActiveMS != nil || req.Incomplete || req.Result != "") {
		return usageInvalid("action", "実績値は終了または訂正時だけ指定できます")
	}
	if len(req.Allocations) > 0 {
		if req.Action != "start" && req.Action != "correct" {
			return usageInvalid("allocations", "配分は開始または訂正時だけ指定できます")
		}
		if len(req.Allocations) > 50 {
			return usageInvalid("allocations", "配分先は50件以内にしてください")
		}
		seen := map[int32]bool{}
		sum := 0.0
		for _, a := range req.Allocations {
			if a.Seq < 1 || seen[a.Seq] || math.IsNaN(a.Share) || math.IsInf(a.Share, 0) || a.Share <= 0 || a.Share > 1 {
				return usageInvalid("allocations", "配分先を重複させず、正の比率を指定してください")
			}
			seen[a.Seq] = true
			sum += a.Share
		}
		if math.Abs(sum-1) > 1e-9 {
			return usageInvalid("allocations", "配分の合計を1にしてください")
		}
		if len(req.Allocations) > 1 && strings.TrimSpace(req.Note) == "" {
			return usageInvalid("note", "共通作業の配分理由を指定してください")
		}
	}
	return nil
}

// Advance only declared work/wait intervals. A disconnected client is never assumed idle.
func advanceWork(s *gen.WorkSession, now time.Time) {
	delta := now.Sub(s.ChangedAt).Milliseconds()
	if delta < 0 {
		delta = 0
	}
	if s.State == "running" {
		s.ActiveMs += delta
	} else if s.State == "waiting" {
		s.WaitingMs += delta
	}
	s.ChangedAt = now
}

func workViewOf(s gen.WorkSession, allocations []gen.ListWorkAllocationsRow) workView {
	v := workView{ID: s.ID, ActorID: s.ActorID, ClientKind: usageString(s.ClientKind), ModelName: usageString(s.ModelName), ModelVersion: usageString(s.ModelVersion), State: s.State, StartedAt: Time(s.StartedAt), ChangedAt: Time(s.ChangedAt), ActiveMS: s.ActiveMs, WaitingMS: s.WaitingMs, ReportedActiveMS: usageInt(s.ReportedActiveMs), Result: usageString(s.Result), Tokens: usageInt(s.Tokens), Note: s.Note, Incomplete: s.Incomplete, Allocations: []workAllocation{}}
	if s.EndedAt != nil {
		x := Time(*s.EndedAt)
		v.EndedAt = &x
	}
	for _, a := range allocations {
		if a.TicketID == s.TicketID {
			v.TicketSeq = a.Seq
		}
		v.Allocations = append(v.Allocations, workAllocation{a.Seq, a.Share})
	}
	return v
}

func workSummary(ctx context.Context, q gen.Querier, ticketID string) (effortSummary, error) {
	t, err := q.GetWorkTotals(ctx, ticketID)
	if err != nil {
		return effortSummary{}, err
	}
	e, err := q.GetTicketEffort(ctx, ticketID)
	if err != nil {
		return effortSummary{}, err
	}
	out := effortSummary{SessionsCount: t.SessionsCount, OpenCount: t.OpenCount, IncompleteCount: t.IncompleteCount, TokensMissing: t.TokensMissing, ActiveMS: t.ActiveMs, ElapsedMS: t.ElapsedMs, WaitingMS: t.WaitingMs, Tokens: t.Tokens, PointVersion: "actual-v1", ActualHours: float8Ptr(e.ActualHours), ActualPoint: float8Ptr(e.ActualPoint), ActualPointVersion: usageString(e.ActualPointVersion), ManualHours: e.ManualHours, ManualPoint: e.ManualPoint}
	if t.SessionsCount > t.OpenCount {
		hours := t.ActiveMs / 3600000
		point := effortPoint(t.ActiveMs)
		out.SuggestedHours = &hours
		out.SuggestedPoint = &point
	}
	return out, nil
}
func recalculateEffort(ctx context.Context, q gen.Querier, ticketID string) error {
	if err := q.EnsureTicketEffort(ctx, ticketID); err != nil {
		return err
	}
	t, err := q.GetWorkTotals(ctx, ticketID)
	if err != nil {
		return err
	}
	if t.SessionsCount == t.OpenCount {
		return q.ClearTicketEffortAuto(ctx, ticketID)
	}
	return q.ApplyTicketEffort(ctx, gen.ApplyTicketEffortParams{TicketID: ticketID, Hours: t.ActiveMs / 3600000, Point: effortPoint(t.ActiveMs)})
}

func (h *handler) listWorkSessions(w http.ResponseWriter, r *http.Request) {
	ctx, _, id, ok := h.ticketScope(w, r, "GET work-sessions")
	if !ok {
		return
	}
	page, e := ParsePage(r, SortSpec{Allowed: []string{"started_at"}, DefaultSort: "started_at", DefaultPerPage: 50})
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var out struct {
		Items   []workView    `json:"items"`
		Total   int64         `json:"total"`
		Page    int           `json:"page"`
		PerPage int           `json:"per_page"`
		Summary effortSummary `json:"summary"`
	}
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		var err error
		out.Summary, err = workSummary(ctx, q, id)
		if err != nil {
			return err
		}
		out.Total, err = q.CountWorkSessions(ctx, id)
		if err != nil {
			return err
		}
		rows, err := q.ListWorkSessions(ctx, gen.ListWorkSessionsParams{TicketID: id, PageLimit: int32(page.Limit()), PageOffset: int32(page.Offset())})
		if err != nil {
			return err
		}
		out.Items = []workView{}
		out.Page = int(page.Page)
		out.PerPage = page.Limit()
		for _, row := range rows {
			a, err := q.ListWorkAllocations(ctx, row.ID)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(row)
			var s gen.WorkSession
			if err = json.Unmarshal(b, &s); err != nil {
				return err
			}
			out.Items = append(out.Items, workViewOf(s, a))
		}
		return nil
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, 200, out)
}

func workPermission(ctx context.Context, q gen.Querier, r *http.Request, projectID string, seq int32, allowClosed bool) (string, *apierr.Error) {
	t, err := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{ProjectID: projectID, Seq: seq})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ticketNotFound(seq)
	}
	if err != nil {
		return "", apierr.New(apierr.InternalError).WithCause(err)
	}
	p := auth.PrincipalFromContext(ctx)
	if !agentMayWorkOn(p, t.AssigneeID) {
		return "", workError(apierr.Forbidden, "所有者が担当するチケットだけ作業を記録できます")
	}
	if !allowClosed && t.ClosedAt != nil {
		return "", workError(apierr.Conflict, "完了したチケットは再オープンしてから作業を開始してください")
	}
	if e := planningWriteProblem(ctx, q, r, projectID, t.ID); e != nil {
		return "", e
	}
	return t.ID, nil
}

func (h *handler) changeWorkSession(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST work-sessions")
	if !ok {
		return
	}
	p := auth.PrincipalFromContext(ctx)
	var req workRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	workID := chi.URLParam(r, "work_id")
	if workID == "" {
		req.Action = "start"
	}
	if e := validateWork(req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.Action == "correct" || req.Action == "restore_auto" || (req.ReportedActiveMS != nil && strings.TrimSpace(req.Note) == "") {
		if req.Action == "correct" || req.Action == "restore_auto" {
			a := auth.ProjectAuthzFromContext(ctx, scope.key)
			if p.IsAgent() || a == nil || !auth.HasPermission(a.Permissions, "ticket.edit") || !auth.HasPermission(a.Permissions, "ticket.actual_point.edit") {
				apierr.Write(w, r, workError(apierr.Forbidden, "実績の訂正には人のチケット編集・実績Point編集権限が必要です"))
				return
			}
		} else {
			apierr.Write(w, r, usageInvalid("note", "稼働時間の取得元を記録してください"))
			return
		}
	}
	encoded, _ := json.Marshal(req)
	sum := sha256.Sum256(append([]byte(r.URL.Path+":"), encoded...))
	digest := hex.EncodeToString(sum[:])
	var response json.RawMessage
	var problem *apierr.Error
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.LockWorkProject(ctx, scope.projectID); err != nil {
			return err
		}
		cached, err := q.GetWorkEvent(ctx, gen.GetWorkEventParams{ProjectID: scope.projectID, ActorID: p.ActorID, RequestID: req.RequestID})
		if err == nil {
			if cached.Digest != digest {
				problem = workError(apierr.Conflict, "同じrequest_idを異なる操作に再利用できません")
				return errTicketReference
			}
			response = cached.Response
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		var s gen.WorkSession
		affected := map[string]bool{ticketID: true}
		if req.Action == "start" {
			if _, problem = workPermission(ctx, q, r, scope.projectID, scope.seq, false); problem != nil {
				return errTicketReference
			}
			if _, err := q.GetOpenWorkSession(ctx, gen.GetOpenWorkSessionParams{ProjectID: scope.projectID, ActorID: p.ActorID}); err == nil {
				problem = workError(apierr.Conflict, "このPJで開いている作業を終了してから開始してください")
				return errTicketReference
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			s = gen.WorkSession{ID: ulidgen.New(), ProjectID: scope.projectID, TicketID: ticketID, ActorID: p.ActorID, State: "running", StartedAt: now, ChangedAt: now, Note: req.Note}
			info, err := q.GetAgentRuntimeInfo(ctx, p.ActorID)
			if err == nil {
				s.ClientKind = usageText(info.ClientKind)
				s.ModelName = info.ModelName
				s.ModelVersion = info.ModelVersion
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err = q.CreateWorkSession(ctx, gen.CreateWorkSessionParams{ID: s.ID, ProjectID: s.ProjectID, TicketID: s.TicketID, ActorID: s.ActorID, ClientKind: s.ClientKind, ModelName: s.ModelName, ModelVersion: s.ModelVersion, StartedAt: now, Note: s.Note}); err != nil {
				return err
			}
			if len(req.Allocations) == 0 {
				req.Allocations = []workAllocation{{scope.seq, 1}}
			}
		} else {
			s, err = q.GetWorkSession(ctx, gen.GetWorkSessionParams{ID: workID, ProjectID: scope.projectID})
			if errors.Is(err, pgx.ErrNoRows) || err == nil && s.TicketID != ticketID && req.Action != "restore_auto" {
				problem = workError(apierr.NotFound, "作業記録が見つかりません")
				return errTicketReference
			}
			if err != nil {
				return err
			}
			if s.ActorID != p.ActorID && p.IsAgent() {
				problem = workError(apierr.Forbidden, "他のAgentの作業記録は変更できません")
				return errTicketReference
			}
			if s.ActorID != p.ActorID && req.Action != "correct" && req.Action != "restore_auto" {
				a := auth.ProjectAuthzFromContext(ctx, scope.key)
				if a == nil || !auth.HasPermission(a.Permissions, "ticket.edit") {
					problem = workError(apierr.Forbidden, "他の作業記録を変更する権限がありません")
					return errTicketReference
				}
				if req.Action != "finish" || !req.Incomplete || strings.TrimSpace(req.Note) == "" {
					problem = usageInvalid("incomplete", "他の人の開いた作業は理由付きで不完全として終了してください")
					return errTicketReference
				}
			}
			old, err := q.ListWorkAllocations(ctx, s.ID)
			if err != nil {
				return err
			}
			if req.Action == "restore_auto" {
				allocated := false
				for _, a := range old {
					if a.TicketID == ticketID {
						allocated = true
					}
				}
				if !allocated {
					problem = workError(apierr.NotFound, "作業記録が見つかりません")
					return errTicketReference
				}
			}
			for _, a := range old {
				affected[a.TicketID] = true
				if req.Action != "restore_auto" && req.Action != "correct" {
					if _, problem = workPermission(ctx, q, r, scope.projectID, a.Seq, true); problem != nil {
						return errTicketReference
					}
				}
			}
			switch req.Action {
			case "pause":
				if s.State != "running" {
					problem = workError(apierr.Conflict, "実行中の作業だけ待機できます")
					return errTicketReference
				}
				advanceWork(&s, now)
				s.State = "waiting"
				s.Note = req.Note
			case "resume":
				if s.State != "waiting" {
					problem = workError(apierr.Conflict, "待機中の作業だけ再開できます")
					return errTicketReference
				}
				advanceWork(&s, now)
				s.State = "running"
			case "finish":
				if s.State == "finished" {
					problem = workError(apierr.Conflict, "終了済み作業の追加実行は新しい作業記録を開始してください")
					return errTicketReference
				}
				advanceWork(&s, now)
				s.State = "finished"
				s.EndedAt = &now
				s.Result = usageText(req.Result)
				s.Tokens = int8OrNull(req.Tokens)
				s.ReportedActiveMs = int8OrNull(req.ReportedActiveMS)
				s.Incomplete = req.Incomplete
				if req.Note != "" {
					s.Note = req.Note
				}
			case "correct":
				if s.State != "finished" {
					problem = workError(apierr.Conflict, "終了した作業だけ訂正できます")
					return errTicketReference
				}
				s.Note = req.Note
				s.Incomplete = req.Incomplete
				if req.Tokens != nil {
					s.Tokens = int8OrNull(req.Tokens)
				}
				if req.ReportedActiveMS != nil {
					s.ReportedActiveMs = int8OrNull(req.ReportedActiveMS)
				}
			case "restore_auto":
				if err := q.EnsureTicketEffort(ctx, ticketID); err != nil {
					return err
				}
				if err := q.RestoreTicketEffortAuto(ctx, ticketID); err != nil {
					return err
				}
			default:
				problem = usageInvalid("action", "この操作は作業記録へ適用できません")
				return errTicketReference
			}
			if s.ReportedActiveMs.Valid && s.ReportedActiveMs.Int64 > s.ActiveMs+s.WaitingMs {
				problem = usageInvalid("reported_active_ms", "稼働時間はセッションの経過時間以下にしてください")
				return errTicketReference
			}
			if err = q.UpdateWorkSession(ctx, gen.UpdateWorkSessionParams{ID: s.ID, State: s.State, ChangedAt: s.ChangedAt, EndedAt: s.EndedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, ReportedActiveMs: s.ReportedActiveMs, Result: s.Result, Tokens: s.Tokens, Note: s.Note, Incomplete: s.Incomplete}); err != nil {
				return err
			}
		}
		if len(req.Allocations) > 0 {
			if err = q.DeleteWorkAllocations(ctx, s.ID); err != nil {
				return err
			}
			hasPrimary := false
			for _, a := range req.Allocations {
				id, e := workPermission(ctx, q, r, scope.projectID, a.Seq, req.Action == "correct")
				if e != nil {
					problem = e
					return errTicketReference
				}
				if id == ticketID {
					hasPrimary = true
				}
				affected[id] = true
				if err = q.EnsureTicketEffort(ctx, id); err != nil {
					return err
				}
				if err = q.AddWorkAllocation(ctx, gen.AddWorkAllocationParams{SessionID: s.ID, TicketID: id, Share: a.Share}); err != nil {
					return err
				}
			}
			if !hasPrimary {
				problem = usageInvalid("allocations", "作業を開始したチケットも配分に含めてください")
				return errTicketReference
			}
		}
		ids := []string{}
		for id := range affected {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if s.State == "finished" {
			for _, id := range ids {
				if err = recalculateEffort(ctx, q, id); err != nil {
					return err
				}
			}
		}
		a, err := q.ListWorkAllocations(ctx, s.ID)
		if err != nil {
			return err
		}
		response, err = json.Marshal(workViewOf(s, a))
		if err != nil {
			return err
		}
		if err = q.CreateWorkEvent(ctx, gen.CreateWorkEventParams{ProjectID: scope.projectID, ActorID: p.ActorID, RequestID: req.RequestID, SessionID: s.ID, Digest: digest, Response: response}); err != nil {
			return err
		}
		field, newValue := "work_session", req.Action+":"+s.ID
		return activity.FromRequest(r).Record(ctx, q, activity.Entry{ProjectID: scope.projectID, EntityType: activity.EntityTicket, EntityID: ticketID, Action: activity.Update, Field: &field, NewValue: &newValue})
	})
	if problem != nil {
		apierr.Write(w, r, problem)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, 200, response)
}
