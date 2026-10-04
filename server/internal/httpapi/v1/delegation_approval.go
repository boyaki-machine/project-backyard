// 委任範囲の承認・却下（PM）。Delegation.md 4.3・5章・6.1。pb-260。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation/approve   delegation.manage
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation/reject    delegation.manage
//
// **委任を計画中から承認済へ進める唯一の口である。** 6.1「計画の初版は、評価が先、
// PM が後」——評価が合格させた計画を、PM が範囲ごと承認する。範囲の変更（6.3）で
// 計画中へ戻った委任も、同じ口で承認済へ戻す。
//
// **対象は最新の計画の版だけで、本体に plan_version を必須とする。** PM が画面で
// 見た版と、押した時点の最新の版が食い違う（その間に main が出し直した）なら
// 409 で拒む。見ていない計画を承認させない。
//
// **却下は計画の版を rejected にするだけで、委任は計画中のまま残す。** main は
// 却下の理由を読んで出し直す。委任の取り下げ（終了）は別の操作である（4.3）。
//
// **MCP ツールにしない**（委任の作成と同じ。delegation.go の冒頭）。
package v1

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// commentKindDecision は PM の承認・却下を根に残すコメントの種類（9.8）。
const commentKindDecision = "decision"

// decideDelegationRequest は approve / reject の本体。
//
// approve では reason_md は任意（承認に添える一言）、reject では必須である。
type decideDelegationRequest struct {
	PlanVersion *int32 `json:"plan_version"`
	ReasonMd    string `json:"reason_md"`
}

// approveDelegation は委任範囲を承認する。
func (h *handler) approveDelegation(w http.ResponseWriter, r *http.Request) {
	h.decideDelegation(w, r, true)
}

// rejectDelegation は委任範囲（最新の計画の版）を却下する。
func (h *handler) rejectDelegation(w http.ResponseWriter, r *http.Request) {
	h.decideDelegation(w, r, false)
}

func (h *handler) decideDelegation(w http.ResponseWriter, r *http.Request, approve bool) {
	op := "reject"
	if approve {
		op = "approve"
	}
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/delegation/"+op)
	if !ok {
		return
	}

	var req decideDelegationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.ReasonMd = strings.TrimSpace(req.ReasonMd)
	if e := validateDecideDelegation(req, approve); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	var (
		view     delegationView
		rejected *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, err := q.GetDelegationByRootTicket(ctx, ticketID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "seq", Code: "invalid", Message: "このチケットは委任の根ではありません",
				})
				return errTicketReference
			}
			return fmt.Errorf("委任を読めない: %w", err)
		}
		// **計画中のときだけ受け付ける**（5章「PM：委任範囲を承認・却下する」の行）。
		// 承認済で範囲を変えるときは、main が範囲の変更を出して計画中へ戻る（6.3）。
		if deleg.Status != "planning" {
			rejected = apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
				"委任が%sのため、委任範囲を承認・却下できません。計画中のときだけ行えます",
				delegationStatusLabel(deleg.Status)))
			return errTicketReference
		}

		plan, err := q.GetLatestDelegationPlanWithVerdict(ctx, deleg.ID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.Conflict).
					WithMessage("main がまだ計画を提出していません")
				return errTicketReference
			}
			return fmt.Errorf("計画を読めない: %w", err)
		}
		if plan.Version != *req.PlanVersion {
			rejected = apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
				"最新の計画は版 %d です。その版を確かめてから操作してください",
				plan.Version)).
				WithDetails(apierr.Detail{Field: "plan_version", Code: "stale"})
			return errTicketReference
		}
		if plan.Status != "proposed" {
			rejected = apierr.New(apierr.Conflict).WithMessage(
				"最新の計画はすでに判定されています。main の出し直しを待ってください")
			return errTicketReference
		}
		// **承認は評価の合格の後だけ**（5章・6.1）。却下は評価を待たずに行える——
		// PM が範囲に同意しないなら、評価の結果を待つ理由が無い。
		if approve && plan.LatestVerdict != evaluationVerdictPass {
			msg := "評価エージェントがまだこの計画を評価していません"
			if plan.LatestVerdict != "" {
				msg = "この計画は評価に合格していません"
			}
			rejected = apierr.New(apierr.Conflict).WithMessage(msg + "。評価の合格の後に承認できます")
			return errTicketReference
		}

		status := "rejected"
		if approve {
			status = "accepted"
		}
		if n, err := q.DecideDelegationPlan(ctx, gen.DecideDelegationPlanParams{
			ID: plan.ID, Status: status,
			DecidedBy: pgtype.Text{String: scope.actorID, Valid: true},
		}); err != nil {
			return fmt.Errorf("計画を確定できない: %w", err)
		} else if n == 0 {
			// 同じ版への同時の操作。先に通った方を正とする。
			rejected = apierr.New(apierr.Conflict).WithMessage(
				"最新の計画はすでに判定されています。画面を読み直してください")
			return errTicketReference
		}

		if approve {
			if _, err := q.SupersedeProposedDelegationPlans(ctx, gen.SupersedeProposedDelegationPlansParams{
				DelegationID: deleg.ID, KeepID: plan.ID,
			}); err != nil {
				return fmt.Errorf("古い計画を畳めない: %w", err)
			}
			if n, err := q.ApproveDelegationScope(ctx, gen.ApproveDelegationScopeParams{
				ID: deleg.ID, Scope: plan.Scope,
			}); err != nil {
				return fmt.Errorf("委任を承認できない: %w", err)
			} else if n == 0 {
				return fmt.Errorf("委任が計画中ではなくなった: %s", deleg.ID)
			}
		}

		// **判断を根のコメントに残す**（9.8）。main は pb_list_comments で
		// 却下の理由を読み、出し直しの材料にする。activity だけでは届かない。
		body := delegationDecisionComment(approve, plan.Version, req.ReasonMd)
		if err := q.CreateComment(ctx, gen.CreateCommentParams{
			ID: ulidgen.New(), TicketID: ticketID, AuthorID: scope.actorID,
			BodyMd: body, Kind: commentKindDecision, Origin: scope.origin(),
			InReplyTo: pgtype.Text{}, AgentRunID: pgtype.Text{},
		}); err != nil {
			return fmt.Errorf("判断のコメントを作成できない: %w", err)
		}

		row, err := q.GetDelegationByID(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("委任を読み直せない: %w", err)
		}
		view = delegationViewOf(row)

		field := "delegation"
		oldVal := fmt.Sprintf("planning（計画 v%d）", plan.Version)
		newVal := fmt.Sprintf("%s（計画 v%d %s）", row.Status, plan.Version, status)
		return rec.Record(ctx, q, activity.Entry{
			ProjectID: scope.projectID, EntityType: activity.EntityTicket,
			EntityID: ticketID, Action: activity.Update,
			Field: &field, OldValue: &oldVal, NewValue: &newVal,
		})
	})
	switch {
	case rejected != nil:
		apierr.Write(w, r, rejected)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

func validateDecideDelegation(req decideDelegationRequest, approve bool) *apierr.Error {
	var details []apierr.Detail
	if req.PlanVersion == nil || *req.PlanVersion < 1 {
		details = append(details, apierr.Detail{
			Field: "plan_version", Code: "required",
			Message: "plan_version に、確認した計画の版を1以上の整数で指定してください",
		})
	}
	if !approve && req.ReasonMd == "" {
		details = append(details, apierr.Detail{
			Field: "reason_md", Code: "required",
			Message: "却下の理由を書いてください。main が計画を出し直す材料になります",
		})
	}
	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

// delegationDecisionComment は根に残すコメントの本文を作る。
func delegationDecisionComment(approve bool, version int32, reason string) string {
	head := fmt.Sprintf("**委任範囲を承認した**（計画 v%d）。委任は承認済になった。", version)
	if !approve {
		head = fmt.Sprintf("**委任範囲を却下した**（計画 v%d）。委任は計画中のまま。理由を読んで計画を出し直すこと。", version)
	}
	if reason == "" {
		return head
	}
	return head + "\n\n" + reason
}
