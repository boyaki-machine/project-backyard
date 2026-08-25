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
//
// **3〜5 が Requirements.md 10.10.4「承認ゲートをAPIレベルで強制する」の実体**
// であり、画面側の制御に依存しない。
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

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

// findTransition は from → to の定義を返す。無ければ nil（検証2 で使う）。
func (wf ticketWorkflow) findTransition(from, to string) *gen.ListWorkflowTransitionsRow {
	for i := range wf.transitions {
		if wf.transitions[i].FromStatusKey == from && wf.transitions[i].ToStatusKey == to {
			return &wf.transitions[i]
		}
	}
	return nil
}

// transitionActor は遷移を試みる側。検証3〜5 の材料になる。
type transitionActor struct {
	kind        string
	permissions []string
}

// denyTransition は検証2〜5 を順に見て、通らない理由を日本語で返す。
// 通るなら空文字。
//
// **検証1（to の存在）は呼び出し側で見る。** 9.6 は 422 unknown_status という
// 別の応答に倒す必要があり、9.7 では items[] をワークフローから組み立てるので
// そもそも起こらない。ここで混ぜると、両者の応答の違いが表せなくなる。
//
// **複数に当たる場合は先の検証の理由を返す**（9.7）。利用者が最初に取り除くべき
// 障害がそれだからである——権限を得ても遷移が定義されていなければ進めない。
func (wf ticketWorkflow) denyTransition(
	from string, to gen.ListWorkflowStatusesRow, actor transitionActor,
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

	return ""
}

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
