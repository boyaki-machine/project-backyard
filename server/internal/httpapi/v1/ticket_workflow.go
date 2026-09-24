// ワークフローの解決と遷移の判定（ApiDesign.md 9.6 / 9.7、DbDesign.md 6.5）。
//
// **遷移する側（9.6）と選択肢を並べる側（9.7）で、同じ判定を通す。** 9.7 の
// allowed:false は「9.6 を叩いたら断られる」を先に見せているだけであり、
// 2か所に別々の条件を書くと、画面が出した選択肢が押した瞬間に断られる。
//
// 判定の順序は 9.6 の検証表そのままである。
//
//	1  to がプロジェクトのワークフローに存在するか   422 unknown_status
//	2  その遷移が定義されているか                   409 invalid_transition
//	3  actor.kind が allowed_actor_kinds にあるか   403 forbidden
//	4  遷移先が is_agent_reachable=false でエージェント 403 forbidden
//	5  required_permission を持つか                 403 forbidden
//	6  エージェントなら、担当が自分の所有者か         403 forbidden
//	7  完了へ進むとき、未完了の子が残っていないか     409 children_not_closed
//
// **3〜6 が Requirements.md 10.10.4「承認ゲートをAPIレベルで強制する」の実体**
// であり、画面側の制御に依存しない。
//
// **検証7 は権限ではなく盤面の整合である**。人にもエージェントにも等しく
// 掛かる——「未完了の子を抱えた親が完了している」状態は、誰が作っても壊れている。
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// actorKindLabels は reason に出す種別の日本語（ApiDesign.md 9.7、DbDesign.md 6.1）。
var actorKindLabels = map[string]string{
	"user":   "ユーザー",
	"agent":  "エージェント",
	"system": "システム",
}

// actorKindAgent は検証4 が見る種別。
const actorKindAgent = "agent"

// ticketWorkflow はプロジェクトのワークフロー1つぶんの定義。
//
// **1リクエストで1回だけ読む。** 9.7 は全ステータスと全遷移を突き合わせるので、
// ステータスごとに引くと N+1 になる。Phase 1 のテンプレートは最大5ステータス・
// 7遷移（DbDesign.md 7.4）で、まとめて読んでも小さい。
type ticketWorkflow struct {
	statuses    []gen.ListWorkflowStatusesRow
	transitions []gen.ListWorkflowTransitionsRow
}

// loadTicketWorkflow はプロジェクトのワークフローを読む。
//
// **ワークフローを持たないプロジェクトでは空になる**（project.workflow_id は
// ON DELETE SET NULL。DbDesign.md 6.5）。そのとき 9.6 はすべて 422
// unknown_status に倒れ、9.7 は空の items[] を返す——どちらも「行ける先が無い」
// という同じ事実を表す。
func loadTicketWorkflow(
	ctx context.Context, q gen.Querier, projectID string,
) (ticketWorkflow, error) {
	var wf ticketWorkflow

	workflowID, err := q.FindProjectWorkflowID(ctx, projectID)
	if err != nil {
		return wf, fmt.Errorf("プロジェクトのワークフローを読めない: %w", err)
	}
	if !workflowID.Valid {
		return wf, nil
	}

	wf.statuses, err = q.ListWorkflowStatuses(ctx, workflowID.String)
	if err != nil {
		return wf, fmt.Errorf("ワークフローのステータスを読めない: %w", err)
	}
	wf.transitions, err = q.ListWorkflowTransitions(ctx, workflowID.String)
	if err != nil {
		return wf, fmt.Errorf("ワークフローの遷移を読めない: %w", err)
	}
	return wf, nil
}

// findStatus は key に一致するステータスを返す。無ければ nil（検証1 で使う）。
func (wf ticketWorkflow) findStatus(key string) *gen.ListWorkflowStatusesRow {
	for i := range wf.statuses {
		if wf.statuses[i].Key == key {
			return &wf.statuses[i]
		}
	}
	return nil
}

// firstStatusInCategory はカテゴリに属するステータスのうち、sort_order が最小の
// ものを返す。無ければ nil（親子の連動で使う。ApiDesign.md 9.6）。
//
// **statuses は sort_order 昇順で読んである**（ListWorkflowStatuses の ORDER BY）
// ので、最初に見つかったものがそれである。
//
// **カテゴリで引くのは、ステータスのキーがワークフローごとに違いうるためである。**
// 'in_progress' というキーを決め打ちすると、テンプレートを写して名前を変えた
// プロジェクトで連動が黙って効かなくなる（DbDesign.md 6.5）。
func (wf ticketWorkflow) firstStatusInCategory(category string) *gen.ListWorkflowStatusesRow {
	for i := range wf.statuses {
		if wf.statuses[i].Category == category {
			return &wf.statuses[i]
		}
	}
	return nil
}

// findTransition は from → to の定義を返す。無ければ nil（検証2 で使う）。
func (wf ticketWorkflow) findTransition(from, to string) *gen.ListWorkflowTransitionsRow {
	for i := range wf.transitions {
		if wf.transitions[i].FromStatusKey == from && wf.transitions[i].ToStatusKey == to {
			return &wf.transitions[i]
		}
	}
	return nil
}

// transitionActor は遷移を試みる側。検証3〜6 の材料になる。
type transitionActor struct {
	kind        string
	permissions []string
	// assigneeIsOwner は検証6 の材料（9.6。手順26b）。呼び出し元がエージェントの
	// とき、そのチケットの assignee_id が自分の所有者かどうか。
	//
	// **チケット単位の値なので、ここに持たせる。** 検証2〜5 が遷移ごとに変わるのに
	// 対し、これは1チケットに対して1つしかない。呼び出し側が
	// agentMayWorkOn で作る。
	assigneeIsOwner bool
}

// agentMayWorkOn は検証6 の判定を1か所に閉じる（9.6。手順26b）。
//
// **人（actor.kind='user'）には常に true を返す。** 全員に掛けると
// ticket.transition を持つ人が他人の担当を進められなくなり、GuiDesign.md 5.5 の
// 状態ドロップダウンが自分の担当でしか使えなくなる。**種別で分けることで、
// 検証3・4 と同じ土俵に乗る**（ApiDesign.md 9.6）。
//
// **担当が未割当（NULL）のチケットは、エージェントから進められない。**
// 「人が引き受けていないものをエージェントが動かさない」が規則であり、
// 誰の担当でもないものはその条件を満たさない。
//
// **working_agent_id は見ない。** あれは実行者の自己申告で、人がいつでも消せる
// （DbDesign.md 6.6）。消しただけで作業が止まる列を認可に使わない。
func agentMayWorkOn(p *auth.Principal, assigneeID pgtype.Text) bool {
	if p == nil || p.ActorKind != actorKindAgent {
		return true
	}
	return assigneeID.Valid && p.OwnerActorID != "" && assigneeID.String == p.OwnerActorID
}

// denyTransition は検証2〜7 を順に見て、通らない理由を日本語で返す。
// 通るなら空文字。
//
// **検証1（to の存在）は呼び出し側で見る。** 9.6 は 422 unknown_status という
// 別の応答に倒す必要があり、9.7 では items[] をワークフローから組み立てるので
// そもそも起こらない。ここで混ぜると、両者の応答の違いが表せなくなる。
//
// **複数に当たる場合は先の検証の理由を返す**（9.7）。利用者が最初に取り除くべき
// 障害がそれだからである——権限を得ても遷移が定義されていなければ進めない。
//
// **検証6 はチケット単位の条件なので、9.7 では全行が同時に allowed:false になる。**
// 遷移先ごとに違う理由が並ぶ他の検証とは性質が異なるが、行ごとに理由を付ける形は
// 変えない（9.7）——エージェントは「この1件はどうか」を見て次の一手を決める。
// **hasOpenChildren を actor に持たせない。** transitionActor は「遷移を試みる側」
// であり、子が残っているかはチケットの側の事実である（検証6 の assigneeIsOwner が
// あそこに居るのは、あれが「その人にとってのチケット」を表すためである）。
func (wf ticketWorkflow) denyTransition(
	from string, to gen.ListWorkflowStatusesRow, actor transitionActor,
	hasOpenChildren bool,
) string {
	tr := wf.findTransition(from, to.Key)

	// 検証2：遷移が定義されていない。
	if tr == nil {
		fromName := to.Key
		if s := wf.findStatus(from); s != nil {
			fromName = s.Name
		}
		return fmt.Sprintf("%sから%sへは直接進められません", fromName, to.Name)
	}

	// 検証3：呼び出し元の種別が許されていない。
	kinds, err := decodeActorKinds(tr.AllowedActorKinds)
	if err == nil && len(kinds) > 0 && !slices.Contains(kinds, actor.kind) {
		label := actorKindLabels[actor.kind]
		if label == "" {
			label = actor.kind
		}
		return fmt.Sprintf("この状態への変更は%sからは行えません", label)
	}

	// 検証4：エージェントが到達できないステータス。
	if !to.IsAgentReachable && actor.kind == actorKindAgent {
		return "この状態へはエージェントから変更できません"
	}

	// 検証5：必要権限。
	if tr.RequiredPermission.Valid &&
		!slices.Contains(actor.permissions, tr.RequiredPermission.String) {
		return fmt.Sprintf("%s 権限が必要です", tr.RequiredPermission.String)
	}

	// 検証6：エージェントは所有者の担当だけを進められる（手順26b）。
	//
	// **最後に置くのは、これが行に依存する唯一の検証だからである**（9.6）。
	// 行を読まずに決まる障害（順路が無い・種別が違う・権限が無い）を先に
	// 返したほうが、利用者が取り除く順序と一致する。
	if actor.kind == actorKindAgent && !actor.assigneeIsOwner {
		return "このチケットの担当者があなたの所有者ではないため、エージェントからは変更できません"
	}

	// 検証7：未完了の子が残っている親は完了にできない。
	//
	// **人にもエージェントにも等しく掛ける。** 検証6 と違って種別で分けないのは、
	// これが**盤面の整合**についての規則だからである——「未完了の子を抱えた親が
	// 完了している」状態は、誰が作っても同じように壊れている。
	//
	// **検証6 の後に置く。** どちらも行を読むが、6 は「あなたが動かしてよいか」、
	// 7 は「いま動かしてよいか」であり、前者を先に返すほうが利用者が取り除く
	// 順序と一致する（9.6）。
	if to.Category == statusCategoryDone && hasOpenChildren {
		return childrenNotClosedReason
	}

	return ""
}

// childrenNotClosedReason は検証7 の文言（ApiDesign.md 9.6 / 9.7）。
//
// **9.7 の reason と 9.6 の message を同じ文字列にする。** 画面は reason を
// そのまま出し（GuiDesign.md 5.5）、押したときは 9.6 の message が出る——
// 別々の文言を持つと、同じ理由が2通りの日本語で見える。
const childrenNotClosedReason = "未完了の子チケットが残っているため完了にできません"

// decodeActorKinds は allowed_actor_kinds（jsonb の配列）を解く。
//
// **解けなかったときは空を返す。** 呼び出し側は「制限なし」として扱う——
// 列の既定値は '["user","agent"]' であり（DbDesign.md 6.5）、壊れた値で
// 全員を締め出すより、検証4・5 に判断を委ねるほうが害が小さい。
// 値を書くのはマイグレーションだけで、API から変える経路は Phase 1 に無い。
func decodeActorKinds(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var kinds []string
	if err := json.Unmarshal(raw, &kinds); err != nil {
		return nil, fmt.Errorf("allowed_actor_kinds を読めない: %w", err)
	}
	return kinds, nil
}
