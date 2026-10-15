// 確認待ちの通知（Delegation.md 8.2、DbDesign.md 6.33.1）。
//
// 確認待ちが立ったら、委任を作った人（PM）に通知を1件作る。確認待ちが閉じたら
// （回答・委任の取り下げ・受け入れ）、その通知を取り下げる。**どちらも確認待ちを
// 立てる・閉じる処理と同じトランザクションで呼ぶ**（6.33.1 の5）。
package v1

import (
	"context"
	"fmt"

	"github.com/boyaki-machine/project-backyard/server/internal/notify"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// confirmationNotificationKind は確認待ちの通知の種類（DbDesign.md 6.33.1 の表）。
const confirmationNotificationKind = "delegation.confirmation"

// confirmationNotificationBodyMax は通知の本文に写す問いの長さ。全文は根の詳細で読む。
const confirmationNotificationBodyMax = 300

// confirmationReasonTitles は通知の題名に出す理由の語。画面の語
// （client/src/api/delegation.ts の confirmationReasonLabels）と揃える。
var confirmationReasonTitles = map[string]string{
	confirmationReasonScopeChange:          "範囲の外への変更",
	confirmationReasonIrreversibleExternal: "後戻りできない操作",
	confirmationReasonCharterChange:        "憲章の変更",
	confirmationReasonSensitiveArea:        "機微な領域",
	confirmationReasonBudgetExceeded:       "制約を超えたため一時停止しました",
	confirmationReasonEvaluationUnable:     "評価エージェントが判断できませんでした",
	confirmationReasonCheckpoint:           "チェックポイント",
	confirmationReasonHearing:              "ヒアリング",
}

// confirmationSubjectKey は確認待ち1件の通知を取り下げる単位。
func confirmationSubjectKey(confirmationID string) string {
	return "delegation_confirmation:" + confirmationID
}

// notifyConfirmation は確認待ち1件について、委任を作った人へ通知を作る。
//
// fromPB は PB 自身が立てた確認待ち（予算・往復の超過、評価できない、周期の要約）
// であることを表す。そのとき通知元は PB になる——raised_by は呼び出しの文脈の
// actor（評価エージェント、要約を読んだ人）であり、問いを出した主ではないため。
func notifyConfirmation(ctx context.Context, q gen.Querier, confirmationID string, fromPB bool) error {
	c, err := q.GetConfirmationNotice(ctx, confirmationID)
	if err != nil {
		return fmt.Errorf("確認待ちの通知の材料を読めない: %w", err)
	}
	reason := confirmationReasonTitles[c.Reason]
	if reason == "" {
		reason = c.Reason
	}
	n := notify.Notification{
		RecipientID: c.RecipientID,
		Kind:        confirmationNotificationKind,
		Title:       fmt.Sprintf("自律実行（#%d）の確認待ち：%s", c.RootSeq, reason),
		Body:        truncateRunes(c.QuestionMd, confirmationNotificationBodyMax),
		Link:        fmt.Sprintf("/p/%s/tickets/%d#confirmations", c.ProjectKey, c.RootSeq),
		LinkLabel:   truncateRunes(fmt.Sprintf("%s-%d %s", c.ProjectKey, c.RootSeq, c.RootTitle), 100),
		SubjectKey:  confirmationSubjectKey(c.ID),
		ProjectID:   c.ProjectID,
	}
	if !fromPB {
		n.SourceID = c.RaisedByID
		n.SourceName = truncateRunes(c.RaisedByName, 100)
	}
	return notify.Create(ctx, q, n)
}

// withdrawConfirmationNotifications は閉じた確認待ちの通知を取り下げる。
func withdrawConfirmationNotifications(ctx context.Context, q gen.Querier, confirmationIDs ...string) error {
	for _, id := range confirmationIDs {
		if _, err := notify.Withdraw(ctx, q, confirmationSubjectKey(id)); err != nil {
			return err
		}
	}
	return nil
}

// truncateRunes は s を max 文字までに切り、切ったら末尾を「…」にする。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
