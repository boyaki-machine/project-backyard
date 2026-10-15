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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// delegationStatusPlanView はパック向けの計画の要約（全フィールドではない）。
type delegationStatusPlanView struct {
	Version       int32   `json:"version"`
	Status        string  `json:"status"`
	DeclaredClass *string `json:"declared_class"`
	MeasuredClass *string `json:"measured_class"`
	// 画面の「計画の承認」とエージェントの評価に要る、この版の中身。
	Scope      any                       `json:"scope,omitempty"`
	Skeleton   any                       `json:"skeleton,omitempty"`
	BodyMd     string                    `json:"body_md,omitempty"`
	ReasonMd   string                    `json:"reason_md,omitempty"`
	Evaluation *delegationStatusPlanEval `json:"evaluation,omitempty"`
}

// delegationStatusPlanEval は最新の計画の版への直近の評価（pb-252）。
// latest_evaluation は対象を問わない直近の評価で、版と結び付かない。
type delegationStatusPlanEval struct {
	Round    int32  `json:"round"`
	Verdict  string `json:"verdict"`
	Findings any    `json:"findings"`
}

// delegationStatusReportView は根の最新の完了レポート（pb-305）。
type delegationStatusReportView struct {
	ID          string `json:"id"`
	SubmittedAt Time   `json:"submitted_at"`
	Evaluated   bool   `json:"evaluated"`
}

// delegationStatusEvaluationView はパック向けの評価の要約。
type delegationStatusEvaluationView struct {
	TargetKind string `json:"target_kind"`
	Round      int32  `json:"round"`
	Verdict    string `json:"verdict"`
	Findings   any    `json:"findings"`
}

// answeredConfirmationView は PM が答えた確認待ち1件（9.3。pb-278）。
type answeredConfirmationView struct {
	Reason     string `json:"reason"`
	QuestionMd string `json:"question_md"`
	AnswerMd   string `json:"answer_md"`
	AnsweredAt *Time  `json:"answered_at"`
}

// recentAnswersMax はパックに載せる「最近の回答」の件数（pb-278）。ヒアリングは
// 何回かに分けて重ねる（9.2）ので、直近の数回ぶんが読めれば足りる。
const recentAnswersMax = 5

// delegationStatusView は GET の応答全体。
type delegationStatusView struct {
	InDelegation      bool                            `json:"in_delegation"`
	Role              string                          `json:"role,omitempty"`
	RootSeq           int32                           `json:"root_seq,omitempty"`
	Status            string                          `json:"status,omitempty"`
	Scope             any                             `json:"scope,omitempty"`
	ScopeVersion      int32                           `json:"scope_version,omitempty"`
	Main              *actorRef                       `json:"main,omitempty"`
	Evaluator         *actorRef                       `json:"evaluator,omitempty"`
	LatestPlan        *delegationStatusPlanView       `json:"latest_plan,omitempty"`
	LatestReport      *delegationStatusReportView     `json:"latest_report,omitempty"`
	LatestEvaluation  *delegationStatusEvaluationView `json:"latest_evaluation,omitempty"`
	OpenConfirmations []confirmationView              `json:"open_confirmations,omitempty"`
	LatestHandoff     *handoffView                    `json:"latest_handoff,omitempty"`
	Budget            *budgetStatusView               `json:"budget,omitempty"`
	RecentAnswers     []answeredConfirmationView      `json:"recent_answers,omitempty"`
	NextMove          string                          `json:"next_move,omitempty"`
	// MainNextMove は呼び出し元の役割によらず、main の次の一手（pb-252）。
	// 画面の「エージェントステイタス」に出す——PM が開くと役割は読むだけになり、
	// next_move は「次の一手が無い」になるため。
	MainNextMove string `json:"main_next_move,omitempty"`
	// MainLastSeenAt / MainStopped は main が止まったかの判定（10.4。pb-310）。
	MainLastSeenAt *Time `json:"main_last_seen_at"`
	MainStopped    bool  `json:"main_stopped"`
	// DeliverablePassed は根の最新の完了レポートが成果の評価に合格しているか（7.2）。
	// 検証8 と同じ材料で、画面は「成果の受け入れ」を出す合図に使う（GuiDesign.md 5.5。pb-340）。
	DeliverablePassed bool `json:"deliverable_passed"`
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
		// **最新の委任が終了した根なら、終了を添える**（4.3「取り下げ」。pb-281）。
		// エージェントが「委任の外」ではなく「終了した」と読んで止まれるように。
		view := delegationStatusView{InDelegation: false}
		if ended, err := h.q.GetDelegationByRootTicket(ctx, ticketID); err == nil && ended.Status == "ended" {
			view.Status, view.RootSeq = ended.Status, scope.seq
		} else if err != nil && err != pgx.ErrNoRows {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
		WriteJSON(w, http.StatusOK, view)
		return
	}

	// **周期が過ぎていれば、checkpoint の確認待ちを立ててから読む**（8.1 の 7・8.3。pb-277）。
	// PB は定期実行を持たないので、状態が読まれたときに区切りを判定する。
	if cerr := h.ensureCheckpoint(ctx, scope.projectID, d.ID, d.Status, d.RootTicketID,
		h.scopeOfDelegation(ctx, d.ID), scope.actorID); cerr != nil && cerr != errCheckpointExists {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(cerr))
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
	view.ScopeVersion = deleg.ScopeVersion

	if plan, perr := h.q.GetLatestDelegationPlan(ctx, deleg.ID); perr == nil {
		pv := delegationStatusPlanView{Version: plan.Version, Status: plan.Status}
		if plan.DeclaredClass.Valid {
			pv.DeclaredClass = &plan.DeclaredClass.String
		}
		if plan.MeasuredClass.Valid {
			pv.MeasuredClass = &plan.MeasuredClass.String
		}
		detail, derr := h.q.GetLatestPlanForStatus(ctx, deleg.ID)
		if derr != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(derr))
			return
		}
		_ = json.Unmarshal(detail.Scope, &pv.Scope)
		_ = json.Unmarshal(detail.Skeleton, &pv.Skeleton)
		pv.BodyMd, pv.ReasonMd = detail.BodyMd, detail.ReasonMd
		if detail.EvalVerdict != "" {
			ev := delegationStatusPlanEval{Round: detail.EvalRound, Verdict: detail.EvalVerdict}
			_ = json.Unmarshal(detail.EvalFindings, &ev.Findings)
			pv.Evaluation = &ev
		}
		view.LatestPlan = &pv
	} else if perr != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(perr))
		return
	}

	// 評価役が agent_report_id に渡す、根の最新の完了レポート（pb-305）。
	rep, rerr := latestRootReport(ctx, h.q, d.RootTicketID)
	if rerr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(rerr))
		return
	}
	view.LatestReport = rep
	if rep != nil && rep.Evaluated {
		verdict, verr := h.q.GetEvaluationVerdictForReport(ctx, pgtype.Text{String: rep.ID, Valid: true})
		if verr != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("評価を読めない: %w", verr)))
			return
		}
		view.DeliverablePassed = verdict == evaluationVerdictPass
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

	// 最近の回答（9.3「答えの本文は main のパックに載る」。pb-278）。止められていた
	// main が、その間に届いた答えを読む口になる。
	if rows, aerr := h.q.ListRecentAnsweredConfirmations(ctx, gen.ListRecentAnsweredConfirmationsParams{
		DelegationID: deleg.ID, MaxRows: recentAnswersMax,
	}); aerr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(aerr))
		return
	} else {
		for _, row := range rows {
			v := answeredConfirmationView{Reason: row.Reason, QuestionMd: row.QuestionMd, AnswerMd: row.AnswerMd.String}
			if row.AnsweredAt != nil {
				at := Time(*row.AnsweredAt)
				v.AnsweredAt = &at
			}
			view.RecentAnswers = append(view.RecentAnswers, v)
		}
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
	if role == roleMain {
		view.MainNextMove = move
	} else if mm, merr := computeNextMove(ctx, h.q, deleg.ID, roleMain); merr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(merr))
		return
	} else {
		view.MainNextMove = mm
	}

	// **main が止まったか**（10.4。pb-310）。評価役の待ち受けがこれを見て、来ない成果を待ち続けない。
	seen, serr := h.q.GetActorLastSeenAt(ctx, deleg.MainActorID)
	if serr != nil && serr != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(serr))
		return
	}
	if seen != nil {
		view.MainLastSeenAt = apiTime(seen)
		view.MainStopped = mainStopped(deleg.Status, deleg.Scope, seen, time.Now())
	}

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

// scopeOfDelegation は委任の scope を読む（読めなければ nil。周期を持たない扱いになる）。
func (h *handler) scopeOfDelegation(ctx context.Context, delegationID string) []byte {
	row, err := h.q.GetDelegationByID(ctx, delegationID)
	if err != nil {
		return nil
	}
	return row.Scope
}
