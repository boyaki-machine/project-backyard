// POST /api/v1/projects/{key}/tickets/{seq}/transition（ApiDesign.md 9.6）。
//
// ステータスを1つ進める。**検証の順序は 9.6 の表そのもの**で、判定は
// ticket_workflow.go が 9.7 と共有する。
//
// **closed_at はこの経路だけが動かす**（9.6）。遷移先の category が done なら
// now()、それ以外なら NULL へ戻す。PATCH で直接書けないようにしてあるので
// （9.5.2）、9.2.1 の ?open=true（closed_at IS NULL）が「完了していないもの」と
// 一致することが保証される。
//
// **If-Match を要求しない**（9.6）。遷移そのものが競合を検出する——2人が同時に
// 「進行中 → レビュー」を実行すると、後発は「レビュー → レビュー」を要求する
// ことになり、workflow_transition に定義が無いため検証2 で 409 になる。
// ただし version は他の更新と同じく +1 する。
package v1

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// statusCategoryDone は closed_at を立てる category（9.6 の表、DbDesign.md 6.5）。
const statusCategoryDone = "done"

// commentKindProgress は遷移に添えたコメントの kind（DbDesign.md 6.7）。
//
// **既定値に頼らず明示する。** comment.kind の DEFAULT は 'discussion' で、
// 遷移コメントはそれとは別の種類である（9.6）。
const commentKindProgress = "progress"

// transitionRequest は 9.6 のリクエスト。
//
// comment は任意。**長さの上限を置いていない**——コメントAPI（9.8）は手順18 で
// あり、上限を決めるならそちらと同じ値にする必要がある。先にここだけ決めると
// 2か所で食い違う。
type transitionRequest struct {
	To      string `json:"to"`
	Comment string `json:"comment"`
}

// transitionTicket はステータスを遷移させる（9.6）。応答は 9.5.1 と同形式。
func (h *handler) transitionTicket(w http.ResponseWriter, r *http.Request) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q,
		"POST /projects/{key}/tickets/{seq}/transition")
	if !ok {
		return
	}

	seq, seqErr := ticketSeqParam(r)
	if seqErr != nil {
		apierr.Write(w, r, seqErr)
		return
	}

	var req transitionRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.To = strings.TrimSpace(req.To)
	if req.To == "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "to", Code: "required", Message: "遷移先のステータスを指定してください",
		}))
		return
	}

	actor := transitionActor{kind: p.ActorKind}
	if a := auth.ProjectAuthzFromContext(r.Context(), key); a != nil {
		actor.permissions = a.Permissions
	}

	ctx := r.Context()
	rec := activity.FromRequest(r)
	var (
		view     ticketDetailView
		writeErr *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		before, err := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{
			ProjectID: projectID, Seq: seq,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeErr = ticketNotFound(seq)
				return errTicketReference
			}
			return fmt.Errorf("チケット %d を読めない: %w", seq, err)
		}

		wf, err := loadTicketWorkflow(ctx, q, projectID)
		if err != nil {
			return err
		}

		// 検証1：遷移先がワークフローに存在するか（422 unknown_status）。
		target := wf.findStatus(req.To)
		if target == nil {
			writeErr = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "to", Code: "unknown_status",
				Message: "このプロジェクトのワークフローに無いステータスです",
			})
			return errTicketReference
		}

		// **同じステータスへの遷移は定義されえない**（ck_workflow_transition_diff）。
		// 検証2 が 409 に倒すので、ここで特別扱いはしない。

		// 検証2：定義が無ければ 409、検証3〜5 は 403。
		if reason := wf.denyTransition(before.StatusKey, *target, actor); reason != "" {
			writeErr = transitionDenied(wf, before.StatusKey, req.To, reason)
			return errTicketReference
		}

		// **closed_at は category から決める**（9.6 の表）。
		newVersion, err := q.SetTicketStatus(ctx, gen.SetTicketStatusParams{
			StatusKey: req.To,
			Closing:   target.Category == statusCategoryDone,
			ProjectID: projectID,
			Seq:       seq,
		})
		if err != nil {
			return fmt.Errorf("チケット %d のステータスを変えられない: %w", seq, err)
		}
		_ = newVersion

		// **コメントは同じトランザクションで作る**（9.6）。遷移だけ通って
		// 経緯が残らない状態を作らない。
		if body := strings.TrimSpace(req.Comment); body != "" {
			origin := "human"
			if p.ActorKind == actorKindAgent {
				origin = "agent"
			}
			if err := q.CreateComment(ctx, gen.CreateCommentParams{
				ID:       ulidgen.New(),
				TicketID: before.ID,
				AuthorID: p.ActorID,
				BodyMd:   body,
				Kind:     commentKindProgress,
				Origin:   origin,
			}); err != nil {
				return fmt.Errorf("遷移コメントを作成できない: %w", err)
			}
		}

		// **activity は action='transition'**（9.6）。field は status_key で、
		// 値はキーのまま入れる（9.13.2 が「表示名への変換は画面が行う」と定める）。
		field := "status_key"
		oldValue := before.StatusKey
		newValue := req.To
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  projectID,
			EntityType: activity.EntityTicket,
			EntityID:   before.ID,
			Action:     activity.Transition,
			Field:      &field,
			OldValue:   &oldValue,
			NewValue:   &newValue,
		}); err != nil {
			return err
		}

		v, err := buildTicketDetail(ctx, q, projectID, seq)
		if err != nil {
			return fmt.Errorf("遷移したチケットを読めない: %w", err)
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
			WithCause(fmt.Errorf("チケット %d を遷移できない: %w", seq, err)))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// transitionDenied は denyTransition の理由を 9.6 の応答へ翻訳する。
//
// **定義が無い（検証2）だけが 409 invalid_transition で、残りは 403**（9.6 の表）。
// 前者は「このワークフローではその順路が存在しない」、後者は「順路はあるが
// あなたには通れない」であり、利用者が次に取る行動が違う。
func transitionDenied(wf ticketWorkflow, from, to, reason string) *apierr.Error {
	if wf.findTransition(from, to) == nil {
		return apierr.New(apierr.InvalidTransition).WithMessage(reason)
	}
	return apierr.New(apierr.Forbidden).WithMessage(reason)
}
