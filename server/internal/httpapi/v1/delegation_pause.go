// 委任の停止と再開（PM）。Delegation.md 4.3・5章・8.2。pb-253。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation/pause    delegation.manage
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation/resume   delegation.manage
//
// **停止は計画中・承認済から行える**（5章「PM：停止・再開」）。PB が予算の超過で
// 止める経路（delegation_budget.go）とは別に、PM がいつでも止められる口である。
//
// **再開は確認待ちに答えた後だけ受け付ける**（8.2「予算の超過は、答えるだけで
// 続けてよいものではない」——答えて、さらに PM が再開する）。戻す先は、止める前の
// 主状態を列に持たないので、計画の状態から導く。
//
//   - 委任範囲がまだ一度も承認されていない（scope_version=0）→ 計画中
//   - 最新の計画の版が、範囲の変更として承認を待っている → 計画中
//   - それ以外 → 承認済
//
// **再開のときに予算を直せる**（承認済へ戻すときだけ）。停止中は main が範囲の
// 変更を出せないので、PM が予算を直す口はここにしかない。直したら scope_version を
// 進め、直前の承認済みの版を写して範囲だけ差し替えた版を、PM の承認済みとして作る
// ——作らないと、次に main が出す版が「範囲が違う」と判定され、委任が計画中へ戻る。
//
// **MCP ツールにしない**（委任の作成・承認と同じ。PM が直接行う操作）。
package v1

import (
	"context"
	"encoding/json"
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

// pauseDelegationRequest は pause の本体（理由は任意）。
type pauseDelegationRequest struct {
	ReasonMd string `json:"reason_md"`
}

// resumeDelegationRequest は resume の本体。budget / stop_conditions を渡すと
// その制約を置き換える（渡さなかった方はそのまま）。
type resumeDelegationRequest struct {
	ReasonMd       string          `json:"reason_md"`
	Budget         json.RawMessage `json:"budget"`
	StopConditions json.RawMessage `json:"stop_conditions"`
}

func (h *handler) pauseDelegation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/delegation/pause")
	if !ok {
		return
	}
	var req pauseDelegationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.ReasonMd = strings.TrimSpace(req.ReasonMd)

	rec := activity.FromRequest(r)
	var (
		view     delegationView
		rejected *apierr.Error
	)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, e := readDelegationForControl(ctx, q, ticketID)
		if e != nil {
			rejected = e
			return errTicketReference
		}
		if deleg.Status != "planning" && deleg.Status != "approved" {
			rejected = apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
				"自律実行が%sのため一時停止できません。計画中・実行中のときだけ止められます",
				delegationScreenLabel(deleg.Status)))
			return errTicketReference
		}
		if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
			ID: deleg.ID, Status: "paused",
		}); err != nil {
			return fmt.Errorf("委任を停止できない: %w", err)
		}
		body := "**自律実行を一時停止した**（PM）。再開は PM が行う。"
		if req.ReasonMd != "" {
			body += "\n\n" + req.ReasonMd
		}
		return finishDelegationControl(ctx, q, rec, scope, ticketID, deleg.ID, deleg.Status, body, &view)
	})
	writeDelegationControl(w, r, view, rejected, err)
}

func (h *handler) resumeDelegation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/delegation/resume")
	if !ok {
		return
	}
	var req resumeDelegationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.ReasonMd = strings.TrimSpace(req.ReasonMd)
	hasBudget := len(req.Budget) > 0 && string(req.Budget) != "null"
	hasStop := len(req.StopConditions) > 0 && string(req.StopConditions) != "null"
	var details []apierr.Detail
	if hasBudget {
		details = append(details, validateBudgetJSON(req.Budget, "budget")...)
	}
	if hasStop {
		details = append(details, validateStopConditionsJSON(req.StopConditions, "stop_conditions")...)
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}
	changes := map[string]json.RawMessage{}
	if hasBudget {
		changes["budget"] = req.Budget
	}
	if hasStop {
		changes["stop_conditions"] = req.StopConditions
	}

	rec := activity.FromRequest(r)
	var (
		view     delegationView
		rejected *apierr.Error
	)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, e := readDelegationForControl(ctx, q, ticketID)
		if e != nil {
			rejected = e
			return errTicketReference
		}
		if deleg.Status != "paused" {
			rejected = apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
				"自律実行が%sのため再開できません。一時停止しているときだけ再開できます",
				delegationScreenLabel(deleg.Status)))
			return errTicketReference
		}
		if open, err := q.HasOpenConfirmation(ctx, deleg.ID); err != nil {
			return fmt.Errorf("確認待ちを確認できない: %w", err)
		} else if open {
			rejected = apierr.New(apierr.Conflict).
				WithMessage("開いている確認待ちがあります。答えてから再開してください")
			return errTicketReference
		}

		target, err := resumeTargetStatus(ctx, q, deleg)
		if err != nil {
			return err
		}
		if len(changes) > 0 && target != "approved" {
			field := "budget"
			if !hasBudget {
				field = "stop_conditions"
			}
			rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: field, Code: "invalid",
				Message: "計画中の自律実行の制約は、ここでは直せません。計画を却下し、制約を直した案を出し直させてください",
			})
			return errTicketReference
		}

		body := fmt.Sprintf("**自律実行を再開した**（PM）。状態は%sに戻った。", delegationScreenLabel(target))
		if len(changes) > 0 {
			if err := replaceDelegationConstraints(ctx, q, deleg, changes, scope.actorID); err != nil {
				return err
			}
			for _, k := range []string{"budget", "stop_conditions"} {
				if v, ok := changes[k]; ok {
					body += fmt.Sprintf("\n\n%sを直した：`%s`", constraintLabels[k], compactJSON(v))
				}
			}
		}
		if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
			ID: deleg.ID, Status: target,
		}); err != nil {
			return fmt.Errorf("委任を再開できない: %w", err)
		}
		if req.ReasonMd != "" {
			body += "\n\n" + req.ReasonMd
		}
		return finishDelegationControl(ctx, q, rec, scope, ticketID, deleg.ID, deleg.Status, body, &view)
	})
	writeDelegationControl(w, r, view, rejected, err)
}

// readDelegationForControl は根のチケットから委任を読む。根でなければ 422。
func readDelegationForControl(
	ctx context.Context, q gen.Querier, ticketID string,
) (gen.GetDelegationForControlRow, *apierr.Error) {
	deleg, err := q.GetDelegationForControl(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return deleg, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "seq", Code: "invalid", Message: "このチケットは自律実行ではありません",
			})
		}
		return deleg, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("委任を読めない: %w", err))
	}
	return deleg, nil
}

// resumeTargetStatus は再開の戻り先を決める（冒頭のコメント）。
func resumeTargetStatus(ctx context.Context, q gen.Querier, deleg gen.GetDelegationForControlRow) (string, error) {
	if deleg.ScopeVersion == 0 {
		return "planning", nil
	}
	plan, err := q.GetLatestDelegationPlan(ctx, deleg.ID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "approved", nil
		}
		return "", fmt.Errorf("計画を読めない: %w", err)
	}
	if plan.Status == "proposed" &&
		((plan.DeclaredClass.Valid && plan.DeclaredClass.String == planClassScope) ||
			(plan.MeasuredClass.Valid && plan.MeasuredClass.String == planClassScope)) {
		return "planning", nil
	}
	return "approved", nil
}

// constraintLabels は根のコメントに書く制約の名前。
var constraintLabels = map[string]string{"budget": "制約の上限", "stop_conditions": "止まる条件"}

// replaceDelegationConstraints は scope の制約（budget / stop_conditions）を置き換え、
// その範囲を前提とする計画の版を PM の承認済みとして作る（冒頭のコメント）。
func replaceDelegationConstraints(
	ctx context.Context, q gen.Querier, deleg gen.GetDelegationForControlRow,
	changes map[string]json.RawMessage, pmID string,
) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(deleg.Scope, &m); err != nil || m == nil {
		m = map[string]json.RawMessage{}
	}
	for k, v := range changes {
		m[k] = compactJSON(v)
	}
	newScope, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("範囲を組み立てられない: %w", err)
	}
	if _, err := q.UpdateDelegationScope(ctx, gen.UpdateDelegationScopeParams{
		ID: deleg.ID, Scope: newScope,
	}); err != nil {
		return fmt.Errorf("範囲を更新できない: %w", err)
	}

	base, err := q.GetLatestAcceptedDelegationPlan(ctx, deleg.ID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil // 承認済みの版が無い（scope_version>0 なら起きない）。範囲だけ直す
		}
		return fmt.Errorf("承認済みの計画を読めない: %w", err)
	}
	old, err := q.GetDelegationPlanByVersion(ctx, gen.GetDelegationPlanByVersionParams{
		DelegationID: deleg.ID, Version: base.Version,
	})
	if err != nil {
		return fmt.Errorf("承認済みの計画を読めない: %w", err)
	}
	version, err := q.NextDelegationPlanVersion(ctx, deleg.ID)
	if err != nil {
		return fmt.Errorf("計画の版を採番できない: %w", err)
	}
	id := ulidgen.New()
	if err := q.CreateDelegationPlan(ctx, gen.CreateDelegationPlanParams{
		ID: id, DelegationID: deleg.ID, Version: version,
		Scope: newScope, Skeleton: old.Skeleton, BodyMd: old.BodyMd,
		ReasonMd:      fmt.Sprintf("PM が再開のときに制約を直した（v%d を写した）", base.Version),
		DeclaredClass: pgtype.Text{}, MeasuredClass: pgtype.Text{String: planClassScope, Valid: true},
		Status: "proposed", RevertsTo: pgtype.Int4{}, SubmittedBy: pmID,
	}); err != nil {
		return fmt.Errorf("制約を直した版を作成できない: %w", err)
	}
	if _, err := q.DecideDelegationPlan(ctx, gen.DecideDelegationPlanParams{
		ID: id, Status: "accepted", DecidedBy: pgtype.Text{String: pmID, Valid: true},
	}); err != nil {
		return fmt.Errorf("制約を直した版を確定できない: %w", err)
	}
	// main が出したまま判定を待っていた版は、前提の範囲が変わったので畳む。
	if _, err := q.SupersedeProposedDelegationPlans(ctx, gen.SupersedeProposedDelegationPlansParams{
		DelegationID: deleg.ID, KeepID: id,
	}); err != nil {
		return fmt.Errorf("古い計画を畳めない: %w", err)
	}
	return nil
}

// finishDelegationControl は根へのコメント・応答の組み立て・活動の記録を行う。
func finishDelegationControl(
	ctx context.Context, q gen.Querier, rec *activity.Recorder, scope ticketScopeInfo,
	ticketID, delegationID, oldStatus, body string, view *delegationView,
) error {
	if err := q.CreateComment(ctx, gen.CreateCommentParams{
		ID: ulidgen.New(), TicketID: ticketID, AuthorID: scope.actorID,
		BodyMd: body, Kind: commentKindDecision, Origin: scope.origin(),
		InReplyTo: pgtype.Text{}, AgentRunID: pgtype.Text{},
	}); err != nil {
		return fmt.Errorf("判断のコメントを作成できない: %w", err)
	}
	row, err := q.GetDelegationByID(ctx, delegationID)
	if err != nil {
		return fmt.Errorf("委任を読み直せない: %w", err)
	}
	*view = delegationViewOf(row)
	field := "delegation"
	return rec.Record(ctx, q, activity.Entry{
		ProjectID: scope.projectID, EntityType: activity.EntityTicket,
		EntityID: ticketID, Action: activity.Update,
		Field: &field, OldValue: &oldStatus, NewValue: &row.Status,
	})
}

func writeDelegationControl(w http.ResponseWriter, r *http.Request, view delegationView, rejected *apierr.Error, err error) {
	switch {
	case rejected != nil:
		apierr.Write(w, r, rejected)
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
	default:
		WriteJSON(w, http.StatusOK, view)
	}
}

// compactJSON は表示と保存のために空白を詰める（失敗したら元のまま）。
func compactJSON(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

// delegationScreenLabel は画面の語での状態（GuiDesign.md 5.5「自律実行」。pb-252）。
// PM が読む応答とコメントに使う。エージェント向けの文は delegationStatusLabel のまま。
func delegationScreenLabel(status string) string {
	switch status {
	case "planning":
		return "計画中"
	case "approved":
		return "実行中"
	case "paused":
		return "一時停止"
	case "ended":
		return "終了"
	default:
		return status
	}
}
