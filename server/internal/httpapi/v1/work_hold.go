package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/mcpbridge"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 承認待ちの保留（Design.md 8.5.7、DbDesign.md 6.34、ApiDesign.md「共通作業タイマー」）。
//
// エージェントのクライアントのフックが、承認を求める直前と次のツール実行の後に知らせてくる。
// **本文はフックの JSON そのままである**——Claude Code の HTTP フックは本文を選べない。

// workHoldEvents はフックのイベント名から、止めるか（true）再開するか（false）を引く。
// 載っていないイベントは何もしない。本文から拾うのは hook_event_name だけで、pb-wait hook と
// 同じ関数を使う（PostToolUse はツールの出力を丸ごと含み、全体を読むと大きい）。
var workHoldEvents = map[string]bool{
	"PermissionRequest":  true,
	"PostToolUse":        false,
	"PostToolUseFailure": false,
	"UserPromptSubmit":   false,
}

// releaseWorkHold はフックが止めた記録を実行中へ戻す（呼び出し側がロックを持つ）。
// 止めていた間は待機（waiting_ms）とその内訳の承認待ち（hold_ms）に数える。
func releaseWorkHold(ctx context.Context, q gen.Querier, s *gen.WorkSession, now time.Time) error {
	before := s.WaitingMs
	advanceWork(s, now)
	s.HoldMs += s.WaitingMs - before
	s.State = "running"
	s.Hold = usageText("")
	return q.SetWorkHold(ctx, gen.SetWorkHoldParams{ID: s.ID, State: s.State, ChangedAt: s.ChangedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, Hold: s.Hold, HoldMs: s.HoldMs})
}

// changeWorkHold は POST /api/v1/projects/{key}/work-hold。
//
// **常に 204 を返し、本文を返さない。** Claude Code の HTTP フックは 2xx の JSON 本文を
// 判定（許可・拒否）として読むので、本文があると承認の流れを変えてしまう。人のトークンでは何もしない。
func (h *handler) changeWorkHold(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "POST work-hold")
	if !ok {
		return
	}
	hold, known := workHoldEvents[mcpbridge.HookEventName(r.Body)]
	if !known || !p.IsAgent() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.LockWorkProject(ctx, projectID); err != nil {
			return err
		}
		now := time.Now().UTC()
		if !hold {
			rows, err := q.ListHeldWork(ctx, gen.ListHeldWorkParams{ProjectID: projectID, ActorID: p.ActorID})
			if err != nil {
				return err
			}
			for i := range rows {
				if err = releaseWorkHold(ctx, q, &rows[i], now); err != nil {
					return err
				}
			}
			return nil
		}
		rows, err := q.ListRunningWorkForHold(ctx, gen.ListRunningWorkForHoldParams{ProjectID: projectID, ActorID: p.ActorID})
		if err != nil {
			return err
		}
		for _, s := range rows {
			advanceWork(&s, now)
			if err = q.SetWorkHold(ctx, gen.SetWorkHoldParams{ID: s.ID, State: "waiting", ChangedAt: s.ChangedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, Hold: usageText("approval"), HoldMs: s.HoldMs}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
