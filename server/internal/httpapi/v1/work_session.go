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
	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type workAllocation struct {
	Seq   int32   `json:"seq"`
	Share float64 `json:"share"`
}
type workRequest struct {
	Outcome          string           `json:"outcome"`
	RequestID        string           `json:"request_id"`
	Action           string           `json:"action"`
	Result           string           `json:"result"`
	Note             string           `json:"note"`
	ReportedActiveMS *int64           `json:"reported_active_ms"`
	Tokens           *int64           `json:"tokens"`
	Incomplete       bool             `json:"incomplete"`
	Allocations      []workAllocation `json:"allocations"`
	ChangeMetrics    []changeMetrics  `json:"change_metrics"`
}
type workView struct {
	Outcome          *string          `json:"outcome"`
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
	Hold             *string          `json:"hold"`
	HoldMS           int64            `json:"hold_ms"`
	ReportedActiveMS *int64           `json:"reported_active_ms"`
	Result           *string          `json:"result"`
	Tokens           *int64           `json:"tokens"`
	Note             string           `json:"note"`
	Incomplete       bool             `json:"incomplete"`
	Allocations      []workAllocation `json:"allocations"`
	Policy           workPolicy       `json:"policy"`
	TimeMissing      bool             `json:"time_missing"`
	Saved            []workSaved      `json:"saved,omitempty"`
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
	ArchivedCount      int64    `json:"archived_count"`
	TimeMissing        int64    `json:"time_missing"`
	PointReason        string   `json:"point_reason"`
	HoursStatus        string   `json:"hours_status"`
	PointStatus        string   `json:"point_status"`
}

type workSaved struct {
	Seq     int32         `json:"seq"`
	Summary effortSummary `json:"summary"`
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
	case "start", "pause", "resume", "finish", "abandon", "correct", "restore_auto":
	default:
		return usageInvalid("action", "作業の操作が正しくありません")
	}
	if req.Action == "pause" || req.Action == "correct" || req.Action == "abandon" {
		if strings.TrimSpace(req.Note) == "" {
			return usageInvalid("note", "待機または訂正の理由を指定してください")
		}
	}
	if req.Outcome != "" && (req.Action != "finish" || !contains([]string{"completed", "failed"}, req.Outcome)) {
		return usageInvalid("outcome", "実行結果は終了時にcompleted/failedを指定してください")
	}
	if req.Outcome == "completed" && req.Result != "completed" {
		return usageInvalid("outcome", "成功はcompletedの作業結果と組み合わせてください")
	}
	if req.Outcome == "failed" && (strings.TrimSpace(req.Note) == "" || req.Result == "completed") {
		return usageInvalid("outcome", "失敗には理由とpartial/blockedの作業結果が必要です")
	}
	if req.Action == "finish" && !contains(reportStatuses, req.Result) {
		return usageInvalid("result", "完了・一部完了・進行不可の結果を指定してください")
	}
	if req.Action != "finish" && req.Action != "correct" && req.Action != "abandon" && (req.Tokens != nil || req.ReportedActiveMS != nil || req.Incomplete || req.Result != "") {
		return usageInvalid("action", "実績値は終了または訂正時だけ指定できます")
	}
	if len(req.ChangeMetrics) > 0 {
		if req.Action != "finish" && req.Action != "correct" {
			return usageInvalid("change_metrics", "累積変更量は終了または訂正時だけ報告できます")
		}
		if len(req.ChangeMetrics) > 50 {
			return usageInvalid("change_metrics", "累積変更量は50件以内にしてください")
		}
		seen := map[int32]bool{}
		for _, m := range req.ChangeMetrics {
			if !m.valid() || seen[m.Seq] {
				return usageInvalid("change_metrics", "全材料・取得元・重複のないチケット番号を指定してください")
			}
			seen[m.Seq] = true
		}
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
	v := workView{ID: s.ID, ActorID: s.ActorID, ClientKind: usageString(s.ClientKind), ModelName: usageString(s.ModelName), ModelVersion: usageString(s.ModelVersion), State: s.State, Outcome: usageString(s.Outcome), StartedAt: Time(s.StartedAt), ChangedAt: Time(s.ChangedAt), ActiveMS: s.ActiveMs, WaitingMS: s.WaitingMs, ReportedActiveMS: usageInt(s.ReportedActiveMs), Result: usageString(s.Result), Tokens: usageInt(s.Tokens), Note: s.Note, Incomplete: s.Incomplete, Allocations: []workAllocation{}}
	_ = json.Unmarshal(s.Policy, &v.Policy)
	v.TimeMissing = s.TimeMissing
	v.Hold = usageString(s.Hold)
	v.HoldMS = s.HoldMs
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
	t, err := collectWorkTotals(ctx, q, ticketID)
	if err != nil {
		return effortSummary{}, err
	}
	e, err := q.GetTicketEffort(ctx, ticketID)
	if err != nil {
		return effortSummary{}, err
	}
	out := effortSummary{SessionsCount: t.SessionsCount, OpenCount: t.OpenCount, IncompleteCount: t.IncompleteCount, TokensMissing: t.TokensMissing, ActiveMS: t.ActiveMs, ElapsedMS: t.ElapsedMs, WaitingMS: t.WaitingMs, Tokens: t.Tokens, ActualHours: float8Ptr(e.ActualHours), ActualPoint: float8Ptr(e.ActualPoint), ActualPointVersion: usageString(e.ActualPointVersion), ManualHours: e.ManualHours, ManualPoint: e.ManualPoint}
	out.ArchivedCount = t.ArchivedCount
	out.TimeMissing = t.TimeMissing
	out.HoursStatus = "unmeasured"
	out.PointStatus = "unmeasured"
	if t.ActiveMs > 0 || t.SessionsCount > t.OpenCount+t.TimeMissing {
		hours := t.ActiveMs / 3600000
		out.SuggestedHours = &hours
		out.HoursStatus = "updated"
	}
	out.SuggestedPoint, out.PointVersion, out.PointReason = pointFromInputs(t.Inputs)
	if out.SuggestedPoint != nil {
		out.PointStatus = "updated"
	}
	if out.ManualHours {
		out.HoursStatus = "manual_preserved"
	}
	if out.ManualPoint {
		out.PointStatus = "manual_preserved"
	}
	return out, nil
}
func recalculateEffort(ctx context.Context, q gen.Querier, ticketID string) error {
	if err := q.EnsureTicketEffort(ctx, ticketID); err != nil {
		return err
	}
	t, err := collectWorkTotals(ctx, q, ticketID)
	if err != nil {
		return err
	}
	if t.ActiveMs == 0 && t.SessionsCount == t.OpenCount+t.TimeMissing {
		if t.TimeMissing > 0 {
			return nil
		}
		return q.ClearTicketEffortAuto(ctx, ticketID)
	}
	point, version, _ := pointFromInputs(t.Inputs)
	var pv pgtype.Text
	if point != nil {
		pv = usageText(version)
	}
	return q.ApplyTicketEffort(ctx, gen.ApplyTicketEffortParams{TicketID: ticketID, Hours: t.ActiveMs / 3600000, Point: nullablePoint(point), PointVersion: pv})
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
	if !allowClosed && t.ClosedAt != nil {
		return "", workError(apierr.Conflict, "完了したチケットは再オープンしてから作業を開始してください")
	}
	if e := workMeasurementPermission(ctx, q, r, projectID, t); e != nil {
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
		replay, e := q.GetWorkReplay(ctx, gen.GetWorkReplayParams{ProjectID: scope.projectID, ActorID: p.ActorID, RequestID: req.RequestID})
		if e == nil {
			problem = workError(apierr.Conflict, "この要求は整理済みです。実績の再計上は行いません").WithDetails(apierr.Detail{Field: "work_id", Code: "work_cleaned", Message: replay.SessionID})
			return errTicketReference
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		now := time.Now().UTC()
		var s gen.WorkSession
		affected := map[string]bool{ticketID: true}
		previousChanges := map[string][]byte{}
		if req.Action == "start" || req.Action == "resume" {
			if problem = monitorWriteProblem(ctx, q, p, scope.projectID, ticketID, req.Action == "start"); problem != nil {
				return errTicketReference
			}
		}
		if req.Action == "start" {
			if _, problem = workPermission(ctx, q, r, scope.projectID, scope.seq, false); problem != nil {
				return errTicketReference
			}
			// **委任の main は、木の中ならチケットごとに1件ずつ同時に開ける**（Design.md 8.5.7。pb-344）。
			// サブエージェントは main のトークンで動くので、並行して子を消化すると記録が重なる。
			// 同じチケットに2件は開かせない（二重計上を防ぐ）。それ以外は PJ 内1件のまま。
			parallel := false
			if p.IsAgent() {
				if parallel, err = parallelWorkAllowed(ctx, q, scope.projectID, p.ActorID, ticketID); err != nil {
					return err
				}
			}
			if parallel {
				open, err := q.HasTicketOpenWork(ctx, gen.HasTicketOpenWorkParams{TicketID: ticketID, ActorID: p.ActorID})
				if err != nil {
					return err
				}
				if open {
					problem = workError(apierr.Conflict, "このチケットで開いている作業を終了してから開始してください")
					return errTicketReference
				}
			} else if _, err := q.GetOpenWorkSession(ctx, gen.GetOpenWorkSessionParams{ProjectID: scope.projectID, ActorID: p.ActorID}); err == nil {
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
			if p.IsAgent() {
				if err = q.CreateAgentAttempt(ctx, gen.CreateAgentAttemptParams{ID: s.ID, ProjectID: s.ProjectID, TicketID: s.TicketID, ActorID: s.ActorID, StartedAt: s.StartedAt}); err != nil {
					return err
				}
			}

			policy, e := getWorkPolicy(ctx, q, scope.projectID)
			if e != nil {
				return e
			}
			s.Policy = workJSON(policy)
			if e = q.SetWorkPolicy(ctx, gen.SetWorkPolicyParams{ID: s.ID, Policy: s.Policy}); e != nil {
				return e
			}
			if len(req.Allocations) == 0 {
				req.Allocations = []workAllocation{{scope.seq, 1}}
			}
		} else {
			s, err = q.GetWorkSession(ctx, gen.GetWorkSessionParams{ID: workID, ProjectID: scope.projectID})
			if errors.Is(err, pgx.ErrNoRows) {
				archived, e := q.GetArchivedWork(ctx, gen.GetArchivedWorkParams{ID: workID, ProjectID: scope.projectID})
				if e == nil && archivedReportAllowed(archived, ticketID, p.ActorID, p.IsAgent()) {
					problem = workError(apierr.Conflict, "作業情報は整理済みで変更できません")
					return errTicketReference
				}
				if e != nil && !errors.Is(e, pgx.ErrNoRows) {
					return e
				}
			}
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
				if (req.Action != "finish" && req.Action != "abandon") || !req.Incomplete || strings.TrimSpace(req.Note) == "" {
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
			// **自分の記録を閉じるときは、配分先の範囲を検査しない**（ApiDesign.md「共通作業タイマー」。pb-306）。
			// 閉じる操作は範囲を広げない。委任の外で開いた記録を、委任の main になった後でも閉じられる
			// ようにする——検査すると 403 で閉じられず、パックの「終了せよ」と食い違う。
			// 配分を変える要求（req.Allocations）は、下でこれまでどおり検査する。
			closingOwn := (req.Action == "finish" || req.Action == "abandon") && s.ActorID == p.ActorID
			for _, a := range old {
				previousChanges[a.TicketID] = a.ChangeMetrics
				affected[a.TicketID] = true
				if req.Action != "restore_auto" && req.Action != "correct" && !closingOwn {
					if _, problem = workPermission(ctx, q, r, scope.projectID, a.Seq, true); problem != nil {
						return errTicketReference
					}
				}
			}
			// **フックが止めた記録に明示的な操作が来たら、先に保留を解く**（DbDesign.md 6.34）。
			// 承認を求めていたのがこの操作そのものである場合があり、保留のままだと待機や終了が落ちる。
			released := false
			if s.Hold.Valid && s.State == "waiting" {
				if err = releaseWorkHold(ctx, q, &s, now); err != nil {
					return err
				}
				released = true
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
				if released {
					break
				}
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
				if s.ActorID != p.ActorID && req.ReportedActiveMS == nil {
					s.TimeMissing = true
					s.ChangedAt = now
				} else {
					advanceWork(&s, now)
				}
				s.State = "finished"
				s.EndedAt = &now
				s.Result = usageText(req.Result)
				outcome := req.Outcome
				if outcome == "" && req.Result == "completed" {
					outcome = "completed"
				}
				s.Outcome = usageText(outcome)
				s.Tokens = int8OrNull(req.Tokens)
				s.ReportedActiveMs = int8OrNull(req.ReportedActiveMS)
				s.Incomplete = req.Incomplete
				if req.Note != "" {
					s.Note = req.Note
				}
			case "abandon":
				if s.State == "finished" {
					problem = workError(apierr.Conflict, "終了済みの作業です")
					return errTicketReference
				}
				s.State = "finished"
				s.ChangedAt = now
				s.EndedAt = &now
				s.Result = usageText("blocked")
				s.Outcome = usageText("abandoned")
				s.Incomplete = true
				s.TimeMissing = true
				s.Note = req.Note
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
					s.TimeMissing = false
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
			if s.ReportedActiveMs.Valid && s.ReportedActiveMs.Int64 > s.ChangedAt.Sub(s.StartedAt).Milliseconds() {
				problem = usageInvalid("reported_active_ms", "稼働時間はセッションの経過時間以下にしてください")
				return errTicketReference
			}
			if err = q.UpdateWorkSession(ctx, gen.UpdateWorkSessionParams{Outcome: s.Outcome, ID: s.ID, State: s.State, ChangedAt: s.ChangedAt, EndedAt: s.EndedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, ReportedActiveMs: s.ReportedActiveMs, Result: s.Result, Tokens: s.Tokens, Note: s.Note, Incomplete: s.Incomplete}); err != nil {
				return err
			}
			if err = q.SetWorkMissingTime(ctx, gen.SetWorkMissingTimeParams{ID: s.ID, TimeMissing: s.TimeMissing}); err != nil {
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
				if previous := previousChanges[id]; len(previous) > 0 {
					if err = q.SetWorkChanges(ctx, gen.SetWorkChangesParams{SessionID: s.ID, TicketID: id, ChangeMetrics: previous}); err != nil {
						return err
					}
				}
			}
			if !hasPrimary {
				problem = usageInvalid("allocations", "作業を開始したチケットも配分に含めてください")
				return errTicketReference
			}
		}
		if len(req.ChangeMetrics) > 0 {
			allocations, e := q.ListWorkAllocations(ctx, s.ID)
			if e != nil {
				return e
			}
			for _, m := range req.ChangeMetrics {
				allocated := ""
				for _, a := range allocations {
					if a.Seq == m.Seq {
						allocated = a.TicketID
					}
				}
				if allocated == "" {
					problem = usageInvalid("change_metrics", "配分先の累積変更量だけ報告できます")
					return errTicketReference
				}
				if e = q.SetWorkChanges(ctx, gen.SetWorkChangesParams{SessionID: s.ID, TicketID: allocated, ChangeMetrics: workJSON(m)}); e != nil {
					return e
				}
			}
		}
		if s.ClientKind.Valid && (req.Action == "finish" || req.Action == "abandon") {
			before, e := agentMonitorState(ctx, q, s.ProjectID, s.ActorID, s.TicketID, false)
			if e != nil {
				return e
			}
			if err = q.FinishAgentAttempt(ctx, gen.FinishAgentAttemptParams{ID: s.ID, EndedAt: s.EndedAt, Outcome: s.Outcome}); err != nil {
				return err
			}
			after, e := agentMonitorState(ctx, q, s.ProjectID, s.ActorID, s.TicketID, false)
			if e != nil {
				return e
			}
			if after.Stopped && !before.Stopped {
				if e = audit.FromRequest(r).Record(ctx, q, audit.Entry{Action: audit.AgentUpdate, Result: audit.Success, TargetType: "agent", TargetID: s.ActorID, Detail: map[string]any{"monitor_stop_reasons": after.Reasons, "project_id": s.ProjectID, "ticket_id": s.TicketID, "work_session_id": s.ID}}); e != nil {
					return e
				}
			}
			if s.Outcome.String == "failed" || s.Outcome.String == "abandoned" {
				principal := &auth.Principal{ActorID: s.ActorID, ActorKind: auth.ActorKindAgent}
				runID := ulidgen.New()
				run, e := buildAgentRun(ctx, q, runID, s.TicketID, principal, reportRequest{})
				if e != nil {
					return e
				}
				run.StartedAt = s.StartedAt
				run.EndedAt = s.EndedAt
				run.Status = s.Outcome.String
				run.ClientKind = s.ClientKind
				run.ModelName = s.ModelName
				run.ModelVersion = s.ModelVersion
				run.TokensUsed = s.Tokens
				if e = q.CreateAgentRun(ctx, run); e != nil {
					return e
				}
				if e = q.LinkFailedAgentRun(ctx, gen.LinkFailedAgentRunParams{ID: runID, WorkSessionID: usageText(s.ID)}); e != nil {
					return e
				}
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
		view := workViewOf(s, a)
		if s.State == "finished" {
			for _, x := range a {
				summary, e := workSummary(ctx, q, x.TicketID)
				if e != nil {
					return e
				}
				view.Saved = append(view.Saved, workSaved{x.Seq, summary})
			}
		}
		response, err = json.Marshal(view)
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
