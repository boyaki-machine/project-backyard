package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type workPolicy struct {
	Version             string `json:"version"`
	PointFormula        string `json:"point_formula"`
	TimeSource          string `json:"time_source"`
	RequireRegistration bool   `json:"require_registration"`
	DocumentVersion     int32  `json:"document_version"`
	Status              string `json:"status"`
}

// The structured block in the actuals section is the sole editable policy source.
func parseWorkPolicy(body string, version int32) workPolicy {
	p := workPolicy{Status: "unset", DocumentVersion: version, TimeSource: "reported_or_pb"}
	section, ok := extractSection(body, "実績")
	if !ok {
		return p
	}
	marker := "```pb-effort-policy\n"
	if strings.Count(section, marker) == 0 {
		return p
	}
	p.Status = "invalid"
	if strings.Count(section, marker) != 1 || strings.Count(body, marker) != 1 {
		return p
	}
	rest := strings.SplitN(section, marker, 2)[1]
	end := strings.Index(rest, "\n```")
	if end < 0 {
		return p
	}
	var raw struct {
		Version             string `json:"version"`
		PointFormula        string `json:"point_formula"`
		TimeSource          string `json:"time_source"`
		RequireRegistration bool   `json:"require_registration"`
	}
	dec := json.NewDecoder(strings.NewReader(rest[:end]))
	dec.DisallowUnknownFields()
	if dec.Decode(&raw) != nil {
		return p
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return p
	}
	if raw.Version != "actual-v0" || raw.PointFormula != "change-volume-v0" || (raw.TimeSource != "reported_or_pb" && raw.TimeSource != "pb") {
		return p
	}
	return workPolicy{Version: raw.Version, PointFormula: raw.PointFormula, TimeSource: raw.TimeSource, RequireRegistration: raw.RequireRegistration, DocumentVersion: version, Status: "ready"}
}
func getWorkPolicy(ctx context.Context, q gen.Querier, projectID string) (workPolicy, error) {
	d, e := q.GetWorkPolicyDocument(ctx, usageText(projectID))
	if errors.Is(e, pgx.ErrNoRows) {
		return workPolicy{Status: "unset", TimeSource: "reported_or_pb"}, nil
	}
	if e != nil {
		return workPolicy{}, e
	}
	return parseWorkPolicy(d.BodyMd, d.Version), nil
}
func (h *handler) getWorkPolicy(w http.ResponseWriter, r *http.Request) {
	ctx, scope, _, ok := h.ticketScope(w, r, "GET work-policy")
	if !ok {
		return
	}
	p, e := getWorkPolicy(ctx, h.q, scope.projectID)
	if e != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	var openID *string
	var openSeq *int32
	open, e := h.q.GetOpenWorkSessionTicket(ctx, gen.GetOpenWorkSessionTicketParams{ProjectID: scope.projectID, ActorID: auth.PrincipalFromContext(ctx).ActorID})
	if e == nil {
		openID, openSeq = &open.ID, &open.Seq
	} else if !errors.Is(e, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	WriteJSON(w, 200, struct {
		Policy       workPolicy `json:"policy"`
		OpenWorkID   *string    `json:"open_work_id"`
		OpenWorkSeq  *int32     `json:"open_work_seq"`
		DocumentPath string     `json:"document_path"`
		Section      string     `json:"section"`
	}{p, openID, openSeq, "project-management-note", "実績"})
}

type changeMetrics struct {
	Seq            int32  `json:"seq"`
	CodeLines      *int64 `json:"code_lines"`
	TestLines      *int64 `json:"test_lines"`
	DocLines       *int64 `json:"doc_lines"`
	OtherLines     *int64 `json:"other_lines"`
	Files          *int64 `json:"files"`
	Branches       *int64 `json:"branches"`
	LearningEvents *int64 `json:"learning_events"`
	Source         string `json:"source"`
}

func (m changeMetrics) valid() bool {
	if m.Seq < 1 || strings.TrimSpace(m.Source) == "" || len([]rune(m.Source)) > 2000 {
		return false
	}
	for _, v := range []*int64{m.CodeLines, m.TestLines, m.DocLines, m.OtherLines, m.Files, m.Branches, m.LearningEvents} {
		if v == nil || *v < 0 || *v > 9007199254740991 {
			return false
		}
	}
	return *m.Branches >= 1
}
func changePoint(m changeMetrics) *float64 {
	if !m.valid() {
		return nil
	}
	size := float64(*m.CodeLines) + .5*float64(*m.TestLines) + .7*float64(*m.DocLines) + .3*float64(*m.OtherLines)
	if size == 0 || *m.Files == 0 {
		return nil
	}
	e := math.Log2(1+size) + .5*math.Log2(1+float64(*m.Files)) + .5*float64(*m.Branches-1) + .5*float64(*m.LearningEvents)
	limits := []float64{4.3, 7, 9.05, 10.85, 12.9, 15.9}
	points := []float64{1, 2, 3, 5, 8, 13, 21}
	for i, b := range limits {
		if e < b {
			return &points[i]
		}
	}
	return &points[6]
}

type workPointInput struct {
	Policy  workPolicy     `json:"policy"`
	Metrics *changeMetrics `json:"metrics"`
	EndedAt int64          `json:"ended_at"`
}
type workTotals struct {
	gen.GetWorkTotalsRow
	ArchivedCount int64
	Inputs        []workPointInput
}

func collectWorkTotals(ctx context.Context, q gen.Querier, ticketID string) (workTotals, error) {
	live, e := q.GetWorkTotals(ctx, ticketID)
	if e != nil {
		return workTotals{}, e
	}
	out := workTotals{GetWorkTotalsRow: live}
	b, e := q.GetWorkBase(ctx, ticketID)
	if e == nil {
		out.ArchivedCount = b.SessionsCount
		out.SessionsCount += b.SessionsCount
		out.IncompleteCount += b.IncompleteCount
		out.TokensMissing += b.TokensMissing
		out.TimeMissing += b.TimeMissing
		out.ActiveMs += b.ActiveMs
		out.ElapsedMs += b.ElapsedMs
		out.WaitingMs += b.WaitingMs
		out.Tokens += b.Tokens
		if e = json.Unmarshal(b.PointInputs, &out.Inputs); e != nil {
			return out, e
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	rows, e := q.GetLiveWorkPointInputs(ctx, ticketID)
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		var p workPolicy
		var m *changeMetrics
		if e = json.Unmarshal(row.Policy, &p); e != nil {
			return out, e
		}
		if len(row.ChangeMetrics) > 0 {
			if e = json.Unmarshal(row.ChangeMetrics, &m); e != nil {
				return out, e
			}
		}
		out.Inputs = append(out.Inputs, workPointInput{p, m, row.EndedAt.UnixNano()})
	}
	return out, nil
}
func pointFromInputs(inputs []workPointInput) (*float64, string, string) {
	if len(inputs) == 0 {
		return nil, "", "変更量が未登録です"
	}
	var latest *workPointInput
	key := ""
	for i := range inputs {
		x := &inputs[i]
		if x.Policy.Status != "ready" {
			return nil, "", "開始時の算出契約が未設定または不正です"
		}
		k := x.Policy.Version + ":" + x.Policy.PointFormula + ":" + x.Policy.TimeSource
		if key != "" && key != k {
			return nil, "", "異なる算出契約が混在しています"
		}
		key = k
		if latest == nil || x.EndedAt > latest.EndedAt {
			latest = x
		}
	}
	if latest.Metrics == nil {
		return nil, latest.Policy.Version, "チケット全体の累積変更量が未登録です"
	}
	point := changePoint(*latest.Metrics)
	if point == nil {
		return nil, latest.Policy.Version, "変更量0または必要な材料が不足しています"
	}
	return point, latest.Policy.Version, ""
}

// Preserve only the newest cumulative snapshot per computational contract.
func compactPointInputs(inputs []workPointInput) []workPointInput {
	out := []workPointInput{}
	indexes := map[string]int{}
	for _, x := range inputs {
		key := x.Policy.Status + ":" + x.Policy.Version + ":" + x.Policy.PointFormula + ":" + x.Policy.TimeSource
		if i, ok := indexes[key]; ok {
			if x.EndedAt >= out[i].EndedAt {
				out[i] = x
			}
		} else {
			indexes[key] = len(out)
			out = append(out, x)
		}
	}
	return out
}
func nullablePoint(p *float64) pgtype.Float8 {
	if p == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *p, Valid: true}
}
func requireWorkReport(ctx context.Context, q gen.Querier, projectID, ticketID string, p *auth.Principal, req reportRequest) *apierr.Error {
	if !p.IsAgent() {
		return nil
	}
	policy, e := getWorkPolicy(ctx, q, projectID)
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	open, e := q.HasTicketOpenWork(ctx, gen.HasTicketOpenWorkParams{TicketID: ticketID, ActorID: p.ActorID})
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	if open {
		return workError(apierr.Conflict, "未終了の作業があります。pb_record_workでfinishまたはabandonを記録してください")
	}
	pinned, e := q.WorkRequiresRegistration(ctx, gen.WorkRequiresRegistrationParams{TicketID: ticketID, ActorID: p.ActorID})
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	if (policy.RequireRegistration || pinned || policy.Status == "invalid") && req.WorkSessionID == "" && req.EffortUnavailable == nil {
		return usageInvalid("work_session_id", "pb_start_work→pb_record_work(action=finish)で保存結果を確認し、work_session_idを指定してください。計測不能ならeffort_unavailableに対象と理由を指定してください")
	}
	return nil
}
func archivedReportAllowed(a gen.WorkArchive, ticketID, actorID string, agent bool) bool {
	var ids []string
	if json.Unmarshal(a.TicketIds, &ids) != nil {
		return false
	}
	return contains(ids, ticketID) && !a.TimeMissing && (!agent || a.ActorID.Valid && a.ActorID.String == actorID)
}

func workMeasurementPermission(ctx context.Context, q gen.Querier, r *http.Request, projectID string, t gen.GetTicketBySeqRow) *apierr.Error {
	p := auth.PrincipalFromContext(ctx)
	if p == nil || !p.IsAgent() {
		return nil
	}
	d, e := q.GetPlanningDelegation(ctx, gen.GetPlanningDelegationParams{TicketID: t.ID, ProjectID: projectID})
	if errors.Is(e, pgx.ErrNoRows) {
		_, main, e := findActiveDelegationByMain(ctx, q, projectID, p.ActorID)
		if e != nil {
			return apierr.New(apierr.InternalError).WithCause(e)
		}
		if main {
			return workError(apierr.Forbidden, "委任の範囲外の作業は記録できません")
		}
		if !agentMayWorkOn(p, t.AssigneeID) {
			return workError(apierr.Forbidden, "所有者が担当するチケットだけ作業を記録できます")
		}
		return nil
	}
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	full, e := q.GetDelegationByID(ctx, d.ID)
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	if d.Status == "ended" {
		return workError(apierr.Forbidden, "終了した委任の作業は記録できません")
	}
	if p.ActorID == full.MainActorID || p.ActorID == full.EvaluatorActorID {
		return nil
	}
	// Sub work must be assigned to its owner, and occur in an approved delegation.
	if !agentMayWorkOn(p, t.AssigneeID) || !delegationGate(d.Status) {
		return workError(apierr.Forbidden, "委任で認可された担当作業だけ記録できます")
	}
	return nil
}

type workContextView struct {
	Policy     workPolicy `json:"policy"`
	OpenWorkID *string    `json:"open_work_id"`
	// OpenWorkSeq は開いている記録の開始元のチケット番号（pb-306）。
	OpenWorkSeq *int32 `json:"open_work_seq"`
}

func getWorkContext(ctx context.Context, q gen.Querier, projectID, actorID string) (*workContextView, error) {
	p, e := getWorkPolicy(ctx, q, projectID)
	if e != nil {
		return nil, e
	}
	out := &workContextView{Policy: p}
	open, e := q.GetOpenWorkSessionTicket(ctx, gen.GetOpenWorkSessionTicketParams{ProjectID: projectID, ActorID: actorID})
	if e == nil {
		out.OpenWorkID, out.OpenWorkSeq = &open.ID, &open.Seq
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	return out, nil
}
