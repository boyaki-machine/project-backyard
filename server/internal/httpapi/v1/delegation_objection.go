// PM の事後の異議（Delegation.md 6.4・6.5）。pb-275。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/plan/{version}/object   delegation.manage
//
// **異議を出せるのは、評価が合格させて PB が反映した構造の変更だけ**である。PM が
// 自分で承認した版（初版・範囲の変更）と、main の判断で反映された軽微な変更は対象にしない。
//
// 異議は2つのことを行う。
//
//   - **評価記録に objected_at を立てる**（6.5 の割合の材料。#254 が数える）
//   - **対象が最新の反映済みの版なら、その前の反映済みの版を写した新しい版を作る**
//     （6.2 の差し戻し）。**後に別の版が反映されていれば、差し戻しはしない**——後の変更を
//     黙って消さないため。どの版へ戻すかは、PM が差し戻し（.../revert）で選ぶ
//     （PM の判断 2026-10-06）
//
// **異議に期限を設けない**（6.4）。根に kind=decision のコメントを残し、main は pb_wait で知る。
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

type objectPlanRequest struct {
	ReasonMd string `json:"reason_md"`
}

// objectionView は異議の応答。reverted_plan は差し戻した版（差し戻さなければ null）。
type objectionView struct {
	ObjectedVersion int32               `json:"objected_version"`
	EvaluationID    string              `json:"evaluation_id"`
	Reverted        bool                `json:"reverted"`
	RevertedPlan    *delegationPlanView `json:"reverted_plan"`
	// LaterVersion は、差し戻さなかったときの、後に反映された最新の版。
	LaterVersion *int32 `json:"later_version,omitempty"`
}

func (h *handler) objectDelegationPlan(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/plan/{version}/object")
	if !ok {
		return
	}
	targetVersion, verErr := planVersionParam(r)
	if verErr != nil {
		apierr.Write(w, r, verErr)
		return
	}
	var req objectPlanRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.ReasonMd = strings.TrimSpace(req.ReasonMd)

	rec := activity.FromRequest(r)
	var (
		view     objectionView
		rejected *apierr.Error
	)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, err := q.GetDelegationByRootTicket(ctx, ticketID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "seq", Code: "invalid", Message: "このチケットは自律実行ではありません",
				})
				return errTicketReference
			}
			return fmt.Errorf("委任を読めない: %w", err)
		}
		if deleg.Status == "ended" {
			rejected = apierr.New(apierr.Conflict).WithMessage("自律実行は終了しています")
			return errTicketReference
		}

		plan, err := q.GetPlanForObjection(ctx, gen.GetPlanForObjectionParams{
			DelegationID: deleg.ID, Version: targetVersion,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "version", Code: "not_found", Message: "指定した版が見つかりません",
				})
				return errTicketReference
			}
			return fmt.Errorf("計画を読めない: %w", err)
		}
		if msg := objectionProblem(plan); msg != "" {
			rejected = apierr.New(apierr.Conflict).WithMessage(msg)
			return errTicketReference
		}
		if n, err := q.MarkEvaluationObjected(ctx, plan.EvaluationID); err != nil {
			return fmt.Errorf("異議を記録できない: %w", err)
		} else if n == 0 {
			rejected = apierr.New(apierr.Conflict).WithMessage("この版には既に異議を出しています")
			return errTicketReference
		}
		view = objectionView{ObjectedVersion: targetVersion, EvaluationID: plan.EvaluationID}

		latest, err := q.GetLatestAcceptedDelegationPlan(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("反映済みの計画を読めない: %w", err)
		}
		body := fmt.Sprintf("**PM が計画 第%d版（評価が合格させた構造の変更）に異議を出した。**", targetVersion)
		if latest.Version == targetVersion {
			prev, err := q.GetPreviousAcceptedDelegationPlan(ctx, gen.GetPreviousAcceptedDelegationPlanParams{
				DelegationID: deleg.ID, Version: targetVersion,
			})
			if err != nil {
				return fmt.Errorf("前の版を読めない: %w", err)
			}
			old, err := q.GetDelegationPlanByVersion(ctx, gen.GetDelegationPlanByVersionParams{
				DelegationID: deleg.ID, Version: prev,
			})
			if err != nil {
				return fmt.Errorf("前の版を読めない: %w", err)
			}
			reason := fmt.Sprintf("第%d版への異議による、第%d版への差し戻し", targetVersion, prev)
			if req.ReasonMd != "" {
				reason += "\n\n" + req.ReasonMd
			}
			pv, err := createRevertPlan(ctx, q, rec, scope, ticketID, deleg.ID, ulidgen.New(), prev, old, reason)
			if err != nil {
				return err
			}
			view.Reverted, view.RevertedPlan = true, &pv
			body += fmt.Sprintf("第%d版を写した第%d版に差し戻した。骨格に合わないチケットを畳むこと。", prev, pv.Version)
		} else {
			later := latest.Version
			view.LaterVersion = &later
			body += fmt.Sprintf("後に第%d版が反映されているため、差し戻しはしていない（戻す版は PM が選ぶ）。", later)
		}
		if req.ReasonMd != "" {
			body += "\n\n" + req.ReasonMd
		}
		if err := q.CreateComment(ctx, gen.CreateCommentParams{
			ID: ulidgen.New(), TicketID: ticketID, AuthorID: scope.actorID,
			BodyMd: body, Kind: commentKindDecision, Origin: scope.origin(),
			InReplyTo: pgtype.Text{}, AgentRunID: pgtype.Text{},
		}); err != nil {
			return fmt.Errorf("判断のコメントを作成できない: %w", err)
		}
		field := "delegation_plan_objection"
		newVal := fmt.Sprintf("v%d", targetVersion)
		return rec.Record(ctx, q, activity.Entry{
			ProjectID: scope.projectID, EntityType: activity.EntityTicket,
			EntityID: ticketID, Action: activity.Update,
			Field: &field, NewValue: &newVal,
		})
	})
	switch {
	case rejected != nil:
		apierr.Write(w, r, rejected)
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
	default:
		WriteJSON(w, http.StatusOK, view)
	}
}

// objectionProblem は、版が異議の対象でなければ理由を返す（冒頭のコメント）。
func objectionProblem(p gen.GetPlanForObjectionRow) string {
	structural := (p.DeclaredClass.Valid && p.DeclaredClass.String == planClassStructural) ||
		(p.MeasuredClass.Valid && p.MeasuredClass.String == planClassStructural)
	switch {
	case p.Status != "accepted":
		return "反映されていない版には異議を出せません"
	case p.DecidedBy.Valid:
		return "あなたが承認した版です。前の版へ戻すときは差し戻しを使ってください"
	case !structural || p.EvaluationID == "":
		return "異議を出せるのは、評価が合格させた構造の変更だけです"
	case p.ObjectedAt != nil:
		return "この版には既に異議を出しています"
	}
	return ""
}
