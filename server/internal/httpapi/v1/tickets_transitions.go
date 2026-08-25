// GET /api/v1/projects/{key}/tickets/{seq}/transitions（ApiDesign.md 9.7）。
//
// チケット詳細画面のステータスドロップダウン（GuiDesign.md 5.5）に出す選択肢。
//
// **items[] はワークフローの全ステータス（現在を除く）である**（9.7）。遷移が
// 定義されている先だけに絞らない。設計原則4「権限で見えないを作る」は
// **メニュー項目**についての規則であり、ステータスは業務上の到達点なので、
// 存在ごと隠すと「なぜ完了にできないのか」が分からなくなる。**この理由は
// 定義が無い先にこそ強く効く**——with_review では in_progress から done への
// 定義が無く、隠すとレビューを通す必要があること自体が読めない。
//
// **このエンドポイントを 9.5.1 に埋めないのは、PATCH のたびに再計算が要るため**
// である（9.7）。ドロップダウンを開いたときにだけ呼べばよい。
package v1

import (
	"fmt"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// transitionOptionView は 9.7 の items[] の1件。
//
// **Reason はポインタである。** allowed=true の行に "reason": "" を出すと、
// 画面が空文字を理由として描きうる。通る先には項目ごと出さない。
type transitionOptionView struct {
	Key      string  `json:"key"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Allowed  bool    `json:"allowed"`
	Reason   *string `json:"reason,omitempty"`
}

// transitionsView は 9.7 の応答。
type transitionsView struct {
	Current *ticketStatusView      `json:"current"`
	Items   []transitionOptionView `json:"items"`
}

// listTicketTransitions は遷移先の一覧を返す（9.7）。
func (h *handler) listTicketTransitions(w http.ResponseWriter, r *http.Request) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q,
		"GET /projects/{key}/tickets/{seq}/transitions")
	if !ok {
		return
	}

	seq, seqErr := ticketSeqParam(r)
	if seqErr != nil {
		apierr.Write(w, r, seqErr)
		return
	}

	ctx := r.Context()
	row, err := h.q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{
		ProjectID: projectID, Seq: seq,
	})
	if err != nil {
		writeTicketReadError(w, r, seq, err)
		return
	}

	wf, err := loadTicketWorkflow(ctx, h.q, projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット %d の遷移先を読めない: %w", seq, err)))
		return
	}

	actor := transitionActor{kind: p.ActorKind}
	if a := auth.ProjectAuthzFromContext(ctx, key); a != nil {
		actor.permissions = a.Permissions
	}

	view := transitionsView{Items: []transitionOptionView{}}

	// current は 9.2.2 の status と同じ3点。**ワークフローに無いキーでも返す**
	// ——ワークフローを差し替えた後の孤立した status_key がありうるためで、
	// statusView が fallback を当てる（ticket_view.go）。
	current := statusView(row.StatusKey, row.StatusName, row.StatusCategory)
	view.Current = &current

	for _, s := range wf.statuses {
		// **現在のステータスは選択肢に出さない**（9.7 が「遷移可能な先」を
		// 並べるものであり、ck_workflow_transition_diff も同じ状態への遷移を
		// 禁じている。DbDesign.md 6.5）。
		if s.Key == row.StatusKey {
			continue
		}
		option := transitionOptionView{
			Key: s.Key, Name: s.Name, Category: s.Category, Allowed: true,
		}
		if reason := wf.denyTransition(row.StatusKey, s, actor); reason != "" {
			option.Allowed = false
			option.Reason = &reason
		}
		view.Items = append(view.Items, option)
	}

	WriteJSON(w, http.StatusOK, view)
}
