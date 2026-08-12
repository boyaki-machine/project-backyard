// 実効権限の計算（Design.md 6.4.1）。
//
//	実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ トークンのスコープ
//
// 権限の割り当てそのものは持たない。権限は「コードのif文ではなくデータとして
// 定義する」（Design.md 6.4.2、原則5）ため、割り当ては DbDesign.md 7.2 / 7.3 の
// シードが正本であり、本ファイルはDBから読んだ集合を組み合わせるだけである。
package auth

import (
	"slices"
	"strings"
)

// EffectivePermissions は 6.4.1 の式を計算し、重複を除いて昇順で返す。
//
// scopes が空のときは縮小しない。ブラウザのセッション（token_type='session'）は
// scopes を持たず、ロールの権限をそのまま使うためである（Design.md 6.4.1 の
// 「空スライスは絞り込みなし」）。
//
// **scopes が空でないときは権限キーとの完全一致で絞る。** Design.md 6.5 が
// エージェントトークンの既定スコープとして挙げる ticket:read / note:write 等の
// 語彙と、権限カタログのキー（ticket.view 等）との対応表は設計文書にまだ無い。
// 対応が定義されるまでは一致しない語彙が権限を与えないほうへ倒す。スコープは
// 「権限の上限」であり縮小しかできない（6.4.1）以上、解釈できない語彙を
// 通してしまうと、絞ったはずのトークンが広い権限で通ることになるためである。
func EffectivePermissions(rolePermissions, projectPermissions, scopes []string) []string {
	granted := make(map[string]struct{}, len(rolePermissions)+len(projectPermissions))
	for _, p := range rolePermissions {
		granted[p] = struct{}{}
	}
	for _, p := range projectPermissions {
		granted[p] = struct{}{}
	}

	if len(scopes) > 0 {
		allowed := make(map[string]struct{}, len(scopes))
		for _, s := range scopes {
			allowed[s] = struct{}{}
		}
		for p := range granted {
			if _, ok := allowed[p]; !ok {
				delete(granted, p)
			}
		}
	}

	out := make([]string, 0, len(granted))
	for p := range granted {
		out = append(out, p)
	}
	slices.SortFunc(out, strings.Compare)
	return out
}

// HasPermission は実効権限の集合に key が含まれるかを返す。
//
// 手順6の RequirePermission が使う判定である。呼び出し側は
// EffectivePermissions の結果（昇順・重複なし）を渡す。
func HasPermission(effective []string, key string) bool {
	return slices.Contains(effective, key)
}
