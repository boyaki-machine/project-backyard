// 委任の取り下げ（PM）。Delegation.md 4.3「取り下げ」・5章。pb-281。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation/withdraw   delegation.manage
//
// **計画中・承認済・停止のどれからでも、1回で取り下げる**（停止を挟まない。PM の判断
// 2026-10-06）。取り下げた後の main・評価の書き込みは、終了の段階として5章のとおり拒まれる。
//
// 1つのトランザクションで次を行う。
//
//   - 委任を ended にする
//   - 根の execution_mode を、PM が選んだ従来のモードに戻す
//   - 開いている確認待ちを withdrawn にする（答える人がいない問いを残さない）
//
// **木の中の子チケットは残し、その execution_mode も変えない**（委任とは別の軸。4.1）。
// 判定を待つ計画の版も触らない（6.2 と同じく履歴）。**受け入れと取り下げは列で分けない**
// ——根の execution_mode が delegated のまま終わったのが受け入れである（DbDesign.md 6.30）。
//
// **MCP ツールにしない**（委任の作成・停止と同じ。PM が直接行う操作）。
package v1

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// withdrawDelegationRequest は withdraw の本体。
type withdrawDelegationRequest struct {
	ExecutionMode string `json:"execution_mode"`
	ReasonMd      string `json:"reason_md"`
}

// withdrawTargetModes は取り下げで戻せる従来の実行モード（delegated を除く）。
var withdrawTargetModes = map[string]string{
	"human_only":  "人が行う",
	"agent_draft": "エージェントが下書きし、人が仕上げる",
	"agent_only":  "エージェントに任せてよい",
}

func (h *handler) withdrawDelegation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/delegation/withdraw")
	if !ok {
		return
	}
	var req withdrawDelegationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.ReasonMd = strings.TrimSpace(req.ReasonMd)
	modeLabel, valid := withdrawTargetModes[req.ExecutionMode]
	if !valid {
		code, msg := "invalid", "戻す先の実行モードは human_only / agent_draft / agent_only のいずれかです"
		if req.ExecutionMode == "" {
			code, msg = "required", "戻す先の実行モードを選んでください"
		}
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "execution_mode", Code: code, Message: msg,
		}))
		return
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
		if deleg.Status == "ended" {
			rejected = apierr.New(apierr.Conflict).
				WithMessage("自律実行はすでに終了しています")
			return errTicketReference
		}

		if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
			ID: deleg.ID, Status: "ended",
		}); err != nil {
			return fmt.Errorf("委任を終了できない: %w", err)
		}
		if _, err := q.SetTicketExecutionMode(ctx, gen.SetTicketExecutionModeParams{
			ID: ticketID, ExecutionMode: req.ExecutionMode,
		}); err != nil {
			return fmt.Errorf("execution_mode を戻せない: %w", err)
		}
		withdrawn, err := q.WithdrawOpenConfirmations(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("確認待ちを取り下げられない: %w", err)
		}
		if err := withdrawConfirmationNotifications(ctx, q, withdrawn...); err != nil {
			return err
		}
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID: scope.projectID, EntityType: activity.EntityTicket,
			EntityID: ticketID, Action: activity.Update,
			Field: strPtr("execution_mode"), OldValue: strPtr("delegated"), NewValue: strPtr(req.ExecutionMode),
		}); err != nil {
			return err
		}

		body := fmt.Sprintf("**自律実行をやめた**（PM）。実行モードを「%s」に戻した。"+
			"子チケットは残っている。エージェントは書き込まずに止まること。", modeLabel)
		if len(withdrawn) > 0 {
			body += fmt.Sprintf("\n\n回答していないユーザ確認待ち %d 件を取り下げた。", len(withdrawn))
		}
		if req.ReasonMd != "" {
			body += "\n\n" + req.ReasonMd
		}
		return finishDelegationControl(ctx, q, rec, scope, ticketID, deleg.ID, deleg.Status, body, &view)
	})
	writeDelegationControl(w, r, view, rejected, err)
}
