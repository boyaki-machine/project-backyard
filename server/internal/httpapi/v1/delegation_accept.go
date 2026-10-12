// 委任の受け入れ（PM）。Delegation.md 4.3「受け入れ」・5章。
//
// **専用の操作を持たない。** 根を完了カテゴリへ遷移することが受け入れであり、
// 遷移（9.6）と同じトランザクションで委任を ended にする。受け入れと取り下げは
// 列で分けない——根の execution_mode が delegated のまま終わったのが受け入れである
// （DbDesign.md 6.30）。
package v1

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// rootAcceptance は、完了へ進む根について検証9 と受け入れの材料を持つ。
//
// **終了した委任の根は対象にしない**（delegationID が空）。取り下げた根や、
// 受け入れた後に開き直した根を閉じても、もう委任は動かない。
type rootAcceptance struct {
	delegationID string
	oldStatus    string
	// notAcceptable は検証9：承認済でないか、関門の確認待ちが開いている。
	notAcceptable bool
}

// readRootAcceptance は ticketID が終了していない委任の根なら、その状態を読む。
func readRootAcceptance(ctx context.Context, q gen.Querier, ticketID string) (rootAcceptance, error) {
	deleg, err := q.GetDelegationForControl(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return rootAcceptance{}, nil
		}
		return rootAcceptance{}, fmt.Errorf("委任を読めない: %w", err)
	}
	if deleg.Status == "ended" {
		return rootAcceptance{}, nil
	}
	acc := rootAcceptance{delegationID: deleg.ID, oldStatus: deleg.Status}
	if !delegationGate(deleg.Status) {
		acc.notAcceptable = true
		return acc, nil
	}
	open, err := q.HasOpenConfirmation(ctx, deleg.ID)
	if err != nil {
		return rootAcceptance{}, fmt.Errorf("確認待ちを読めない: %w", err)
	}
	acc.notAcceptable = open
	return acc, nil
}

// acceptDelegation は根の完了と同じトランザクションで委任を終了にする。
//
// 開いている確認待ちは、ヒアリングも含めて withdrawn にする（答える人が
// いない問いを画面に残さない。取り下げと同じ）。根へのコメントで pb_wait が
// 変化を返し、エージェントは「終了」を読んで止まる。
func acceptDelegation(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID, ticketID, actorID, origin string, acc rootAcceptance,
) error {
	if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
		ID: acc.delegationID, Status: "ended",
	}); err != nil {
		return fmt.Errorf("委任を終了できない: %w", err)
	}
	withdrawn, err := q.WithdrawOpenConfirmations(ctx, acc.delegationID)
	if err != nil {
		return fmt.Errorf("確認待ちを取り下げられない: %w", err)
	}
	body := "**自律実行を受け入れた**（根を完了にした）。実行モードは「自律実行」のまま残る。" +
		"エージェントは書き込まずに止まること。"
	if withdrawn > 0 {
		body += fmt.Sprintf("\n\n回答していない確認待ち %d 件を取り下げた。", withdrawn)
	}
	if err := q.CreateComment(ctx, gen.CreateCommentParams{
		ID: ulidgen.New(), TicketID: ticketID, AuthorID: actorID,
		BodyMd: body, Kind: commentKindDecision, Origin: origin,
		InReplyTo: pgtype.Text{}, AgentRunID: pgtype.Text{},
	}); err != nil {
		return fmt.Errorf("受け入れのコメントを作成できない: %w", err)
	}
	field, newStatus := "delegation", "ended"
	oldStatus := acc.oldStatus
	return rec.Record(ctx, q, activity.Entry{
		ProjectID: projectID, EntityType: activity.EntityTicket,
		EntityID: ticketID, Action: activity.Update,
		Field: &field, OldValue: &oldStatus, NewValue: &newStatus,
	})
}
