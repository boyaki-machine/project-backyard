// スプリントの運用（ApiDesign.md 9.12.1 / 9.12.2）。
//
//	POST /api/v1/projects/{key}/sprints/start        project.edit
//	POST /api/v1/projects/{key}/sprints/{id}/finish  project.edit
//
// **定義（sprints.go の CRUD）と運用を分ける。** あちらはプロジェクト設定の
// スプリントタブ（GuiDesign.md 5.9.5）が使い、ここはバックログのオンステージ段
// （同 5.4）が使う。**対象は「オンステージに載っているもの全部」**であり、
// 1件ずつ選ぶものではない——だからチケット詳細のスプリント欄は読み取り専用に
// なった（9.5.2 の use_sprint_endpoint）。
//
// **どちらも1トランザクションで行う。** 開始が途中で落ちると「スプリントは
// 出来たが対象が入っていない」、終了が途中で落ちると「完了にしたが段が
// 降りていない」という、画面から直す手段の無い状態が残る。
//
// **activity には記録しない**（9.13.2）。1回の操作でオンステージの全チケットが
// 動くので、行ごとに1件記録すると、実際には1つである出来事が数十行に膨らむ。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// startSprintRequest は 9.12.1 のリクエスト。
//
// **status を受け取らない。** 開始は必ず active であり、選ばせる意味がない。
type startSprintRequest struct {
	Name    string          `json:"name"`
	Goal    *string         `json:"goal"`
	StartAt json.RawMessage `json:"start_at"`
	EndAt   json.RawMessage `json:"end_at"`
	AllDay  *bool           `json:"all_day"`
}

// ── POST /api/v1/projects/{key}/sprints/start ───────────────

// startSprint はスプリントを新しく作り、active にし、オンステージに載っている
// ものを対象に入れる（9.12.1）。
//
// **既にある planned のスプリントを開始する形にはしない。**
// 始めるたびに名前と期間を決めるほうが、先に作った定義を探して選ぶより短い。
func (h *handler) startSprint(w http.ResponseWriter, r *http.Request) {
	_, key, projectID, ok := projectScopeContext(w, r, h.q,
		"POST /projects/{key}/sprints/start")
	if !ok {
		return
	}

	var req startSprintRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	loc, err := projectLocation(r.Context(), h.q, projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	var details []apierr.Detail
	name := validateSprintName(req.Name, &details)
	start, end, allDay := parseCreateSchedule(req.StartAt, req.EndAt, req.AllDay, &details)
	details = validateSchedule(start, end, allDay, loc, "end_at", details)
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	goal := pgtype.Text{}
	if req.Goal != nil {
		goal = optionalText(strings.TrimSpace(*req.Goal))
	}

	ctx := r.Context()
	id := ulidgen.New()
	var (
		view     sprintView
		writeErr *apierr.Error
	)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **進行中のスプリントは同時に1本だけである**（9.12.1）。
		// オンステージは1つしかなく、「いまどの期間で消化しようと
		// しているか」の答えが2つあると、開始のたびにどちらへ入れるかを選ぶ
		// ことになる。**複数チームの並行はプロジェクトを分ける形で表す。**
		active, err := q.GetActiveSprint(ctx, projectID)
		switch {
		case err == nil:
			writeErr = apierr.New(apierr.Conflict).
				WithMessage(fmt.Sprintf(
					"スプリント「%s」が進行中です。先に終了してください", active.Name)).
				WithCause(fmt.Errorf("進行中のスプリント %q がある", active.ID))
			return errSprintConflict
		case errors.Is(err, pgx.ErrNoRows):
			// 進行中が無い。これが通常の経路である。
		default:
			return fmt.Errorf("進行中のスプリントを読めない: %w", err)
		}

		if err := q.CreateSprint(ctx, gen.CreateSprintParams{
			ID:        id,
			ProjectID: projectID,
			Name:      name,
			Goal:      goal,
			StartAt:   start,
			EndAt:     end,
			AllDay:    allDay,
			Status:    sprintStatusActive,
		}); err != nil {
			return fmt.Errorf("スプリントを作成できない: %w", err)
		}

		// **対象はオンステージ段に出ている行すべてである。**
		// staged_at だけで決めてはならない——段を決めるのは親であり、子は
		// staged_at が NULL のまま親と一緒に出る（GuiDesign.md 5.4）。
		ids, err := q.ListOnstageTicketIDs(ctx, projectID)
		if err != nil {
			return fmt.Errorf("オンステージのチケットを読めない: %w", err)
		}

		// **空でも開始できる**（9.12.1）。期間を先に切ってから積む進め方が
		// あるためで、開始できない理由にしない。
		if len(ids) > 0 {
			if err := q.AddTicketsToSprint(ctx, gen.AddTicketsToSprintParams{
				TicketIds: ids,
				SprintID:  id,
			}); err != nil {
				return fmt.Errorf("スプリントの対象を記録できない: %w", err)
			}
			if err := q.SetTicketsSprintID(ctx, gen.SetTicketsSprintIDParams{
				SprintID:  pgtype.Text{String: id, Valid: true},
				ProjectID: projectID,
				TicketIds: ids,
			}); err != nil {
				return fmt.Errorf("チケットのスプリントを更新できない: %w", err)
			}
		}

		v, err := sprintByIDWith(ctx, q, projectID, id)
		if err != nil {
			return fmt.Errorf("開始したスプリントを読めない: %w", err)
		}
		view = v
		return nil
	})

	switch {
	case writeErr != nil:
		apierr.Write(w, r, writeErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリントを開始できない: %w", err)))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/sprints/%s", key, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── POST /api/v1/projects/{key}/sprints/{id}/finish ─────────

// finishSprint はスプリントを終える（9.12.2）。
//
// **完了しているオンステージの根だけを段から降ろす。** 未完了のものは
// staged_at を触らず、オンステージに残って次の start でそちらへ入る。
//
// **ticket.sprint_id は消さない。** 終わったあとも「最後に属したスプリント」を
// 指し続ける——9.2.1 の「棚に戻ったか」の判定がこれを読む。
func (h *handler) finishSprint(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q,
		"POST /projects/{key}/sprints/{id}/finish")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	ctx := r.Context()
	var (
		view     sprintView
		writeErr *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		current, err := q.GetSprintByID(ctx, gen.GetSprintByIDParams{
			ProjectID: projectID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeErr = apierr.New(apierr.NotFound).
					WithMessage("スプリントが見つかりません").
					WithCause(fmt.Errorf("スプリント %q が見つからない", id))
				return errSprintConflict
			}
			return fmt.Errorf("スプリント %q を読めない: %w", id, err)
		}

		// **進行中でなければ終えられない。** 404 ではなく 409 にするのは、
		// 権限でも不在でもなく**盤面がその操作を許さない**ためである
		// （9.6 の検証2・検証7 と同じ置き方）。
		if current.Status != sprintStatusActive {
			writeErr = apierr.New(apierr.Conflict).
				WithMessage("進行中のスプリントではありません").
				WithCause(fmt.Errorf("スプリント %q の状態は %q", id, current.Status))
			return errSprintConflict
		}

		rows, err := q.FinishSprint(ctx, gen.FinishSprintParams{ProjectID: projectID, ID: id})
		if err != nil {
			return fmt.Errorf("スプリント %q を終了できない: %w", id, err)
		}
		if rows == 0 {
			// 読んだ直後に他者が状態を変えた場合だけここへ来る。
			writeErr = apierr.New(apierr.Conflict).
				WithMessage("進行中のスプリントではありません").
				WithCause(fmt.Errorf("スプリント %q の更新が0行", id))
			return errSprintConflict
		}

		// 所属の履歴を閉じる。**完了・未完了を問わず立てる**（DbDesign.md 6.9.1）。
		if err := q.MarkSprintMembershipRemoved(ctx, id); err != nil {
			return fmt.Errorf("スプリントの所属を閉じられない: %w", err)
		}

		// 完了している根を段から降ろす。**画面からはこれで消える**
		// ——降ろした行は 9.2.1 の3条件を満たすので一覧から外れる。
		if _, err := q.UnstageClosedTicketsInSprint(ctx, gen.UnstageClosedTicketsInSprintParams{
			ProjectID: projectID,
			SprintID:  pgtype.Text{String: id, Valid: true},
		}); err != nil {
			return fmt.Errorf("完了したチケットを段から降ろせない: %w", err)
		}

		v, err := sprintByIDWith(ctx, q, projectID, id)
		if err != nil {
			return fmt.Errorf("終了したスプリントを読めない: %w", err)
		}
		view = v
		return nil
	})

	switch {
	case writeErr != nil:
		apierr.Write(w, r, writeErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリント %q を終了できない: %w", id, err)))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// errSprintConflict はトランザクションを巻き戻すための番兵。
//
// **writeErr に応答を積んでからこれを返す**——RunInTx は error が返ると
// ロールバックするので、9.6 の errTicketReference と同じ形である。
var errSprintConflict = errors.New("sprint conflict")

// joinActiveSprint は、スプリント中にオンステージへ入った部分木を、その場で進行中の
// スプリントへ所属させる（9.12.3）。
//
// **所属を書くのが開始だけだと、途中で加わった配下が棚に戻らない。** 9.2.1 の
// 条件2 は「最後に属したスプリントが completed」を見るので、所属を持たない配下は
// 根と一緒に完了しても、終了後にバックログへ残る（stg で #81 の配下5件）。
//
// 呼ぶのは入口4つ——作成（9.3）・親の付け替え（9.5.2）・段の移動（9.4.1）・
// 着手による段上げ（9.6）。**ticketID を根とする部分木**を所属させるので、段へ
// 上げた根を渡せば配下ごと入る。進行中のスプリントが無いか、表示上の根が
// オンステージに居なければ何もしない。**書き方は開始（startSprint）と同じ**で、
// 既に所属していれば AddTicketsToSprint の ON CONFLICT で何も起きない。
func joinActiveSprint(ctx context.Context, q gen.Querier, projectID, ticketID string) error {
	active, err := q.GetActiveSprint(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("進行中のスプリントを読めない: %w", err)
	}
	ids, err := q.ListSubtreeIDsJoiningSprint(ctx, gen.ListSubtreeIDsJoiningSprintParams{
		ProjectID: projectID, TicketID: ticketID,
	})
	if err != nil {
		return fmt.Errorf("スプリントへ入れる部分木を読めない: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := q.AddTicketsToSprint(ctx, gen.AddTicketsToSprintParams{
		TicketIds: ids,
		SprintID:  active.ID,
	}); err != nil {
		return fmt.Errorf("スプリントの対象を記録できない: %w", err)
	}
	if err := q.SetTicketsSprintID(ctx, gen.SetTicketsSprintIDParams{
		SprintID:  pgtype.Text{String: active.ID, Valid: true},
		ProjectID: projectID,
		TicketIds: ids,
	}); err != nil {
		return fmt.Errorf("チケットのスプリントを更新できない: %w", err)
	}
	return nil
}
