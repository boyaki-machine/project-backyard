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
	"context"
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

		// 検証7 の材料（9.6。pb-72）。**完了へ進むときだけ数える**——
		// それ以外の遷移では結果に効かないので、毎回1本増やす理由が無い。
		var hasOpenChildren bool
		if target.Category == statusCategoryDone {
			n, err := q.CountOpenChildren(ctx, pgtype.Text{String: before.ID, Valid: true})
			if err != nil {
				return fmt.Errorf("チケット %d の子を数えられない: %w", seq, err)
			}
			hasOpenChildren = n > 0
		}

		// 検証2：定義が無ければ 409、検証3〜6 は 403、検証7 は 409。
		if reason := wf.denyTransition(before.StatusKey, *target, actor, hasOpenChildren); reason != "" {
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
				// **遷移の時点で agent_run は存在しない**（手順26c）。run を作るのは
				// pb_submit_result だけである（DbDesign.md 8.2.4）。
				AgentRunID: pgtype.Text{},
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

		// **子が未着手を出たら、祖先を進行中にする**（9.6。pb-72）。
		//
		// **同じトランザクションで行う。** 連動が落ちて子だけ進むと、盤面が
		// 「子は動いているのに親は未着手」のまま残る——それを直す操作が画面に無い。
		if err := cascadeParentsToInProgress(ctx, q, rec, wf, projectID, before, *target); err != nil {
			return err
		}

		// **着手したら、オンステージへ上げる**（9.6。pb-5）。
		//
		// 「オンステージだけを見れば仕掛りが全部わかる」という段の約束を、
		// 手の操作に頼らずに保つ（GuiDesign.md 5.4）——上げ忘れた仕掛りが
		// バックログ段に埋もれると、上の段は仕掛りの一覧でなくなる。
		if err := stageDisplayRootOnStart(ctx, q, wf, before, *target); err != nil {
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

// statusCategoryTodo / statusCategoryInProgress は親子の連動が見るカテゴリ
// （9.6「子が動いたら、親を進行中にする」。pb-72）。
const (
	statusCategoryTodo       = "todo"
	statusCategoryInProgress = "in_progress"
)

// cascadeParentsToInProgress は、子が未着手カテゴリを出たときに祖先を進行中へ動かす
// （ApiDesign.md 9.6「子が動いたら、親を進行中にする」。pb-72）。
//
// **掛ける検証は2（順路の定義）だけである。** 検証3〜7 は掛けない——連動は
// **すでに認可された操作の帰結**であって、新しい操作ではない。ここで検証5（権限）や
// 検証6（担当が所有者か）を掛けると、**親の担当が別人であるという理由で子の着手が
// 失敗する**ことになる。
//
// **順路が定義されていなければ黙って飛ばす。** 親のワークフローに todo → in_progress
// が無いことを理由に子の遷移を 409 にすると、関係のないチケットが着手できなくなる。
// 連動は付随的な整合であって、子の遷移の成否を左右しない。
//
// **親に working_agent_id は立てない**（DbDesign.md 6.6）。あれは「自分がこの
// チケットを処理している」という自己申告で、エージェントは親を処理していない。
//
// **コメントは作らない**——添える本文が無い。activity には子を進めた本人の
// actor_id で1行残す（連動を起こした責任はそこにある）。
func cascadeParentsToInProgress(
	ctx context.Context, q gen.Querier, rec *activity.Recorder, wf ticketWorkflow,
	projectID string, before gen.GetTicketBySeqRow, target gen.ListWorkflowStatusesRow,
) error {
	// 未着手カテゴリを出たときだけ動く。**遷移前が todo でなければ、親は既に
	// 動いている**（この規則自身がそうしている）。
	fromStatus := wf.findStatus(before.StatusKey)
	if fromStatus == nil || fromStatus.Category != statusCategoryTodo ||
		target.Category == statusCategoryTodo {
		return nil
	}

	to := wf.firstStatusInCategory(statusCategoryInProgress)
	if to == nil {
		return nil
	}

	// **祖先をたどる。** 親が todo でなければそこで打ち切る——この規則自体が
	// 親を進めるとき同じ経路を通るので、todo でない親の上に todo の祖先は残らない。
	//
	// **深さに上限を置く。** parent_id の循環は 9.5.2 の parent_cycle が書き込み時に
	// 防いでいるが、認可を通らない経路で無限に回ると要求が返らなくなる。
	// 木の深さは epic → story → task の3段が想定で（DbDesign.md 6.6）、
	// 32 は実際の運用の遥か上にある。
	childID := before.ID
	for depth := 0; depth < 32; depth++ {
		parent, err := q.GetParentForCascade(ctx, childID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 親を持たない（根に着いた）
		}
		if err != nil {
			return fmt.Errorf("親チケットを読めない: %w", err)
		}
		if !parent.StatusCategory.Valid ||
			parent.StatusCategory.String != statusCategoryTodo {
			return nil
		}

		// 検証2 だけを掛ける。順路が無ければ、そこで静かにやめる。
		if wf.findTransition(parent.StatusKey, to.Key) == nil {
			return nil
		}

		if _, err := q.SetTicketStatus(ctx, gen.SetTicketStatusParams{
			StatusKey: to.Key,
			// **in_progress は done ではないので closed_at は立たない**（9.6 の表）。
			Closing:   false,
			ProjectID: projectID,
			Seq:       parent.Seq,
		}); err != nil {
			return fmt.Errorf("親チケット %d を進行中にできない: %w", parent.Seq, err)
		}

		field := "status_key"
		oldValue := parent.StatusKey
		newValue := to.Key
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  projectID,
			EntityType: activity.EntityTicket,
			EntityID:   parent.ID,
			Action:     activity.Transition,
			Field:      &field,
			OldValue:   &oldValue,
			NewValue:   &newValue,
		}); err != nil {
			return err
		}

		childID = parent.ID
	}
	return nil
}

// stageDisplayRootOnStart は、未着手カテゴリを出たチケットの「表示上の
// トップレベルの祖先」をオンステージへ上げる（ApiDesign.md 9.6
// 「着手したら、オンステージへ上げる」。pb-5。利用者の判断、2026-09-08）。
//
// **上げるのは自分ではなく祖先である。** 段に置けるのは表示上のトップレベル
// だけで（9.4.1 の not_stageable）、配下は親と一緒に運ばれる。子タスクに
// 着手したとき、動かすべきなのは**その子を含む部分木の根**である。
//
// **sort_key を動かさない。** 二段は同じ順序キーを1本共有しており（9.4）、
// 段が変わっても位置は保たれる。**着手のたびに末尾へ飛ぶと、人が手で組んだ
// 消化順が壊れる。**
//
// **version を動かさない。** staged_at は 9.5.2 で PATCH できない欄なので、
// 開いている詳細ペインの If-Match が古くなっても失われる編集が無い。
// 逆に上げると、着手のたびに祖先を開いている画面が 409 になる。
//
// **activity には書かない**（9.6）。遷移の行が「誰が進めたか」を既に持って
// おり、手で動かす経路（9.4）と混ざることもない。
//
// **逆向きの連動は持たない**——未着手へ戻してもオンステージから降ろさない。
// 「未着手だがオンステージ」は段が表せなければならない状態であり
// （DbDesign.md 6.6）、やり直しのために状態を戻した行が仕掛りから消えるのは
// 誤りである。降ろすのは手で戻すか、スプリントを終えるかの2つだけである。
func stageDisplayRootOnStart(
	ctx context.Context, q gen.Querier, wf ticketWorkflow,
	before gen.GetTicketBySeqRow, target gen.ListWorkflowStatusesRow,
) error {
	// 未着手カテゴリを出たときだけ動く。遷移前が todo でなければ、
	// 既に一度は着手されている（この規則自身がそのとき上げている）。
	fromStatus := wf.findStatus(before.StatusKey)
	if fromStatus == nil || fromStatus.Category != statusCategoryTodo ||
		target.Category == statusCategoryTodo {
		return nil
	}

	root, err := q.GetDisplayRootForStaging(ctx, before.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("段に置く祖先を読めない: %w", err)
	}
	// **エピックは段に置けない**（9.4.1）。親を持たないエピックを着手させると
	// ここへ来るので、黙って何もしない。
	if root.Staged || root.Type == ticketTypeEpic {
		return nil
	}

	if err := q.SetTicketStagedAt(ctx, gen.SetTicketStagedAtParams{
		ID:       root.ID,
		StagedAt: nowTimestamptz(),
	}); err != nil {
		return fmt.Errorf("チケットをオンステージへ上げられない: %w", err)
	}
	return nil
}

// transitionDenied は denyTransition の理由を 9.6 の応答へ翻訳する。
//
// **定義が無い（検証2）だけが 409 invalid_transition で、検証3〜6 は 403**
// （9.6 の表）。前者は「このワークフローではその順路が存在しない」、後者は
// 「順路はあるがあなたには通れない」であり、利用者が次に取る行動が違う。
//
// **検証7 は 409 children_not_closed**（pb-72）。403 に混ぜないのは、これが
// 権限の問題ではないためである——**同じ人が、子を完了させたあとなら通る。**
//
// **理由の文字列で見分ける。** denyTransition が返すのは日本語1本なので、
// どの検証で落ちたかは呼び出し側からは文字列でしか分からない。検証7 の文言だけ
// 定数（childrenNotClosedReason）にしてあるのはこのためで、**9.7 の reason と
// 同じ文字列を使う必要もここで満たされる。**
func transitionDenied(wf ticketWorkflow, from, to, reason string) *apierr.Error {
	if reason == childrenNotClosedReason {
		return apierr.New(apierr.ChildrenNotClosed).WithMessage(reason)
	}
	if wf.findTransition(from, to) == nil {
		return apierr.New(apierr.InvalidTransition).WithMessage(reason)
	}
	return apierr.New(apierr.Forbidden).WithMessage(reason)
}
