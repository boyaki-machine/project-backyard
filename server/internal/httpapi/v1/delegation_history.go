// 計画の版と評価記録の一覧（読み取り）。Delegation.md 6.2・7.3。pb-274。
//
//	GET /api/v1/projects/{key}/tickets/{seq}/plans         ticket.view
//	GET /api/v1/projects/{key}/tickets/{seq}/evaluations   ticket.view
//
// **{seq} は委任の根**（根の最新の委任を読む。終了した委任も読める）。読むのは全員
// （5章「読む」）なので ticket.view で足りる。画面の「計画と評価の履歴」が使う
// （GuiDesign.md 5.5「自律実行」）。パックは delegation-status の要約を使い、ここは使わない。
package v1

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// planHistoryView は一覧の1版。objectable は PM が異議を出せるか（6.4。pb-275）。
type planHistoryView struct {
	delegationPlanView
	LatestVerdict  *string `json:"latest_verdict"`
	PassObjectedAt *Time   `json:"pass_objected_at"`
	Objectable     bool    `json:"objectable"`
}

// evaluationHistoryView は一覧の1件（7.3 の項目）。
type evaluationHistoryView struct {
	ID          string        `json:"id"`
	TargetKind  string        `json:"target_kind"`
	PlanVersion *int32        `json:"plan_version"`
	Round       int32         `json:"round"`
	Verdict     string        `json:"verdict"`
	Findings    any           `json:"findings"`
	ClassCheck  *string       `json:"class_check"`
	TokensUsed  *int64        `json:"tokens_used"`
	ObjectedAt  *Time         `json:"objected_at"`
	CreatedAt   Time          `json:"created_at"`
	Evaluator   actorRef      `json:"evaluator"`
	EvaluatorAt modelSnapshot `json:"evaluator_model"`
	MainAt      modelSnapshot `json:"main_model"`
}

// modelSnapshot は評価の時点の種類とモデル（4.4 の写し）。
type modelSnapshot struct {
	ClientKind   *string `json:"client_kind"`
	ModelName    *string `json:"model_name"`
	ModelVersion *string `json:"model_version"`
}

func (h *handler) listDelegationPlans(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r, "GET /projects/{key}/tickets/{seq}/plans")
	if !ok {
		return
	}
	delegationID, e := h.rootDelegationID(r, ticketID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rows, err := h.q.ListDelegationPlans(ctx, delegationID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	items := make([]planHistoryView, 0, len(rows))
	for _, row := range rows {
		v := planHistoryView{delegationPlanView: delegationPlanViewOf(gen.GetDelegationPlanByIDRow{
			ID: row.ID, Version: row.Version, Scope: row.Scope, Skeleton: row.Skeleton,
			BodyMd: row.BodyMd, ReasonMd: row.ReasonMd,
			DeclaredClass: row.DeclaredClass, MeasuredClass: row.MeasuredClass,
			Status: row.Status, RevertsTo: row.RevertsTo,
			SubmittedAt: row.SubmittedAt, DecidedAt: row.DecidedAt,
			SubmittedByID: row.SubmittedByID, SubmittedByKind: row.SubmittedByKind, SubmittedByName: row.SubmittedByName,
			DecidedByID: row.DecidedByID, DecidedByKind: row.DecidedByKind, DecidedByName: row.DecidedByName,
		})}
		if row.LatestVerdict != "" {
			v.LatestVerdict = &row.LatestVerdict
		}
		if row.PassObjectedAt != nil {
			t := Time(*row.PassObjectedAt)
			v.PassObjectedAt = &t
		}
		v.Objectable = objectionProblem(gen.GetPlanForObjectionRow{
			Status: row.Status, DeclaredClass: row.DeclaredClass, MeasuredClass: row.MeasuredClass,
			DecidedBy: row.DecidedByID, EvaluationID: row.PassEvaluationID, ObjectedAt: row.PassObjectedAt,
		}) == ""
		items = append(items, v)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) listDelegationEvaluations(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r, "GET /projects/{key}/tickets/{seq}/evaluations")
	if !ok {
		return
	}
	delegationID, e := h.rootDelegationID(r, ticketID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rows, err := h.q.ListDelegationEvaluations(ctx, delegationID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	items := make([]evaluationHistoryView, 0, len(rows))
	for _, row := range rows {
		v := evaluationHistoryView{
			ID: row.ID, TargetKind: row.TargetKind, Round: row.Round, Verdict: row.Verdict,
			ClassCheck: textPtr(row.ClassCheck), CreatedAt: Time(row.CreatedAt),
			Evaluator: actorRef{ID: row.EvaluatorActorID, Kind: row.EvaluatorActorKind, DisplayName: row.EvaluatorActorName},
			EvaluatorAt: modelSnapshot{
				ClientKind: textPtr(row.EvaluatorClientKind), ModelName: textPtr(row.EvaluatorModelName),
				ModelVersion: textPtr(row.EvaluatorModelVersion),
			},
			MainAt: modelSnapshot{
				ClientKind: textPtr(row.MainClientKind), ModelName: textPtr(row.MainModelName),
				ModelVersion: textPtr(row.MainModelVersion),
			},
		}
		_ = json.Unmarshal(row.Findings, &v.Findings)
		if row.PlanVersion.Valid {
			v.PlanVersion = &row.PlanVersion.Int32
		}
		if row.TokensUsed.Valid {
			v.TokensUsed = &row.TokensUsed.Int64
		}
		if row.ObjectedAt != nil {
			t := Time(*row.ObjectedAt)
			v.ObjectedAt = &t
		}
		items = append(items, v)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// rootDelegationID は根の最新の委任を引く。根でなければ 422。
func (h *handler) rootDelegationID(r *http.Request, ticketID string) (string, *apierr.Error) {
	deleg, err := h.q.GetDelegationByRootTicket(r.Context(), ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "seq", Code: "invalid", Message: "このチケットは自律実行の根ではありません",
			})
		}
		return "", apierr.New(apierr.InternalError).WithCause(err)
	}
	return deleg.ID, nil
}
