// DELETE /api/v1/projects/{key}/tickets/{seq}（ApiDesign.md 9.5.3）。
//
// **物理削除である**（DbDesign.md 4.6 の既定）。204 No Content。
//
// **子チケットは消えない。** ticket.parent_id は ON DELETE SET NULL なので、
// 子は親を失って表示上のトップレベルへ上がる。確認ダイアログが件数を出すのは
// この挙動を利用者に見せるためで（GuiDesign.md 6.3）、件数は 9.5.1 の
// children から数える——そのための API は別に置かない。
//
// **activity は消さず、action='delete' を1行足す**（9.5.3）。activity.entity_id は
// 多相参照で FK を持てず（DbDesign.md 6.8）、9.13.2 は「削除されたチケットの行は
// entity_seq / entity_title が null になる」と定めている。**残る前提の設計**であり、
// 消すと手順19 のダッシュボード「最近の動き」から削除が読めなくなる。
//
// **If-Match を要求しない。** 2.8 が求めるのは「編集内容が失われる更新」であり、
// 削除には失われる編集内容が無い。9.5.3 も要求していない。
package v1

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// deleteTicket はチケット1件を物理削除する（9.5.3）。
func (h *handler) deleteTicket(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q,
		"DELETE /projects/{key}/tickets/{seq}")
	if !ok {
		return
	}

	seq, seqErr := ticketSeqParam(r)
	if seqErr != nil {
		apierr.Write(w, r, seqErr)
		return
	}

	ctx := r.Context()
	rec := activity.FromRequest(r)
	var notFound *apierr.Error

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **削除の前に id を引く。** activity.entity_id はチケットの ULID で
		// あり（seq ではない）、行が消えた後では引けない。
		ticketID, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
			ProjectID: projectID, Seq: seq,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = ticketNotFound(seq)
				return errTicketReference
			}
			return fmt.Errorf("チケット %d を読めない: %w", seq, err)
		}

		// **履歴を先に書く。** activity.actor_id は actor への FK を持つが
		// entity_id は持たないので順序の制約は無い。ただし削除を先に行うと、
		// 記録に失敗したときロールバックで戻すものが増える。
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  projectID,
			EntityType: activity.EntityTicket,
			EntityID:   ticketID,
			Action:     activity.Delete,
		}); err != nil {
			return err
		}

		rows, err := q.DeleteTicket(ctx, gen.DeleteTicketParams{
			ProjectID: projectID, Seq: seq,
		})
		if err != nil {
			return fmt.Errorf("チケット %d を削除できない: %w", seq, err)
		}
		if rows == 0 {
			// 直前に id を引けているので、ここへ来るのは同時削除だけである。
			notFound = ticketNotFound(seq)
			return errTicketReference
		}
		return nil
	})

	switch {
	case notFound != nil:
		apierr.Write(w, r, notFound)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット %d を削除できない: %w", seq, err)))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
