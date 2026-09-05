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
// **エージェントが遷移すると working_agent_id が自分になる**（9.6。手順26b）。
// 実行者の自己申告であり、担当（assignee_id）とは別の欄である（DbDesign.md 6.6）。
// **activity には記録しない**——遷移の行が「誰が進めたか」を actor_id で既に
// 持っており、同じ事実が2行になる。
//
// **エージェントは、担当が自分の所有者であるチケットしか進められない**（検証6）。
// 判定は ticket_workflow.go の agentMayWorkOn にあり、9.7 と共有する。
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
	"github.com/jackc/pgx/v5/pgtype"

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

		// **検証3〜6 の材料は、チケットを読んだ後でないと揃わない**（手順26b）。
		// assigneeIsOwner が行に依存するためで、9.7 が同じ関数を通す。
		actor := transitionActor{
			kind:            p.ActorKind,
			assigneeIsOwner: agentMayWorkOn(p, before.AssigneeID),
		}
		if a := auth.ProjectAuthzFromContext(ctx, key); a != nil {
			actor.permissions = a.Permissions
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

		// 検証2：定義が無ければ 409、検証3〜6 は 403。
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

		// **エージェントは、遷移に成功した時点で自分を実行者として立てる**（9.6）。
		// 「着手した」と「宣言した」が別々に起こる状態を作らないための副作用で、
		// 専用の操作を持たない（DbDesign.md 6.6）。**別のエージェントが入って
		// いれば上書きする**——途中で替えるのが通常の運用であり、排他ではない。
		//
		// **version を動かさない。** 直前の SetTicketStatus が既に +1 しており、
		// ここでもう一度上げると1回の遷移で2つ進む（2.8）。
		if p.ActorKind == actorKindAgent {
			if err := q.SetTicketWorkingAgent(ctx, gen.SetTicketWorkingAgentParams{
				WorkingAgentID: pgtype.Text{String: p.ActorID, Valid: true},
				ProjectID:      projectID,
				Seq:            seq,
			}); err != nil {
				return fmt.Errorf("実行者を記録できない: %w", err)
			}
		}

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
				// **遷移コメントは返信ではない**（手順18a で in_reply_to を足した）。
				InReplyTo: pgtype.Text{},
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
