// 委任の状態の集約読み取り。Delegation.md 9.1。pb-251。
//
//	GET /api/v1/projects/{key}/tickets/{seq}/delegation-status   ticket.view
//
// **委任・計画・評価・確認待ち・引き継ぎノートのどれも GET を持たない**
// （各ファイルの冒頭コメントが明記するとおり、書き込みの口しか無い）。
// コンテキストパックの「6. 自律実行」節（pb_get_context。`server/internal/mcp`）
// がこの口を1回叩いて、節の描画に要る材料をまとめて受け取る——MCP 層は
// 内部で REST を叩く設計（`Design.md` 8.1）であり、5本の表を1本ずつ読みに
// 行かせると節の描画が5往復になる。
//
// **{seq} はどのチケットでもよい**（根・子孫・木の外のどれでも受け付ける）。
// 木の外なら `in_delegation: false` だけを返す。
package v1

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// delegationStatusPlanView はパック向けの計画の要約（全フィールドではない）。
type delegationStatusPlanView struct {
	Version       int32   `json:"version"`
	Status        string  `json:"status"`
	DeclaredClass *string `json:"declared_class"`
	MeasuredClass *string `json:"measured_class"`
}

// delegationStatusEvaluationView はパック向けの評価の要約。
type delegationStatusEvaluationView struct {
	TargetKind string `json:"target_kind"`
	Round      int32  `json:"round"`
	Verdict    string `json:"verdict"`
	Findings   any    `json:"findings"`
}

// delegationStatusView は GET の応答全体。
type delegationStatusView struct {
	InDelegation      bool                            `json:"in_delegation"`
	Role              string                          `json:"role,omitempty"`
	RootSeq           int32                           `json:"root_seq,omitempty"`
	Status            string                          `json:"status,omitempty"`
	Scope             any                             `json:"scope,omitempty"`
	Main              *actorRef                       `json:"main,omitempty"`
	Evaluator         *actorRef                       `json:"evaluator,omitempty"`
	LatestPlan        *delegationStatusPlanView       `json:"latest_plan,omitempty"`
	LatestEvaluation  *delegationStatusEvaluationView `json:"latest_evaluation,omitempty"`
	OpenConfirmations []confirmationView              `json:"open_confirmations,omitempty"`
	LatestHandoff     *handoffView                    `json:"latest_handoff,omitempty"`
	Budget            *budgetStatusView               `json:"budget,omitempty"`
	NextMove          string                          `json:"next_move,omitempty"`
}

func (h *handler) getDelegationStatus(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "GET /projects/{key}/tickets/{seq}/delegation-status")
	if !ok {
		return
	}

	d, in, err := findDelegationContainingTicket(ctx, h.q, scope.projectID, ticketID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if !in {
		WriteJSON(w, http.StatusOK, delegationStatusView{InDelegation: false})
		return
	}

	deleg, err := h.q.GetDelegationByID(ctx, d.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	role := delegationRoleOf(scope.actorID, deleg, ticketID == d.RootTicketID)

	view := delegationStatusView{
		InDelegation: true, Role: string(role), RootSeq: deleg.RootSeq, Status: deleg.Status,
		Main:      &actorRef{ID: deleg.MainActorID, Kind: deleg.MainActorKind, DisplayName: deleg.MainActorName},
		Evaluator: &actorRef{ID: deleg.EvaluatorActorID, Kind: deleg.EvaluatorActorKind, DisplayName: deleg.EvaluatorActorName},
	}
	var scopeAny any
	_ = json.Unmarshal(deleg.Scope, &scopeAny)
	view.Scope = scopeAny

	if plan, perr := h.q.GetLatestDelegationPlan(ctx, deleg.ID); perr == nil {
		pv := delegationStatusPlanView{Version: plan.Version, Status: plan.Status}
		if plan.DeclaredClass.Valid {
			pv.DeclaredClass = &plan.DeclaredClass.String
		}
		if plan.MeasuredClass.Valid {
			pv.MeasuredClass = &plan.MeasuredClass.String
		}
		view.LatestPlan = &pv
	} else if perr != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(perr))
		return
	}

	if ev, everr := h.q.GetLatestEvaluationForDelegation(ctx, deleg.ID); everr == nil {
		var findings any
		_ = json.Unmarshal(ev.Findings, &findings)
		view.LatestEvaluation = &delegationStatusEvaluationView{
			TargetKind: ev.TargetKind, Round: ev.Round, Verdict: ev.Verdict, Findings: findings,
		}
	} else if everr != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(everr))
		return
	}

	if rows, cerr := h.q.ListOpenConfirmations(ctx, deleg.ID); cerr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(cerr))
		return
	} else if len(rows) > 0 {
		views := make([]confirmationView, 0, len(rows))
		for _, row := range rows {
			views = append(views, openConfirmationViewOf(row))
		}
		view.OpenConfirmations = views
	}

	if handoff, herr := h.q.GetLatestDelegationHandoff(ctx, deleg.ID); herr == nil {
		hv := handoffViewOf(handoff)
		view.LatestHandoff = &hv
	} else if herr != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(herr))
		return
	}

	// 予算と消費（12.1・9.1「残り予算」。pb-253）。トークンを申告しなかった
	// 件数も載せる——自己申告の合計が漏れていることを読み手に知らせる。
	if usage, uerr := h.q.GetDelegationBudgetUsage(ctx, gen.GetDelegationBudgetUsageParams{
		DelegationID: deleg.ID, ProbeTicketID: "",
	}); uerr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(uerr))
		return
	} else {
		bv := budgetStatusOf(deleg.Scope, usage)
		view.Budget = &bv
	}

	move, merr := computeNextMove(ctx, h.q, deleg.ID, role)
	if merr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(merr))
		return
	}
	view.NextMove = move

	WriteJSON(w, http.StatusOK, view)
}

// delegationRoleOf は9.1の表を判定する。
func delegationRoleOf(actorID string, deleg gen.GetDelegationByIDRow, isRoot bool) delegationRole {
	switch {
	case actorID == deleg.EvaluatorActorID:
		return roleEvaluator
	case actorID == deleg.MainActorID && isRoot:
		return roleMain
	case actorID == deleg.MainActorID:
		return roleSub
	default:
		return roleReadonly
	}
}

func openConfirmationViewOf(row gen.ListOpenConfirmationsRow) confirmationView {
	var options any
	_ = json.Unmarshal(row.Options, &options)
	return confirmationView{
		ID: row.ID, TicketSeq: row.TicketSeq, Reason: row.Reason,
		QuestionMd: row.QuestionMd, Options: options, Status: "open",
		RaisedBy:  actorRef{ID: row.RaisedByID, Kind: row.RaisedByKind, DisplayName: row.RaisedByName},
		CreatedAt: Time(row.CreatedAt),
	}
}
