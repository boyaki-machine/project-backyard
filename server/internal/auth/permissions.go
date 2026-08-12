// 実効権限の計算（Design.md 6.4.1）。
//
//	実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ トークンのスコープ
//
// 権限の割り当てそのものは持たない。権限は「コードのif文ではなくデータとして
// 定義する」（Design.md 6.4.2、原則5）ため、割り当ては DbDesign.md 7.2 / 7.3 の
// シードが正本であり、本ファイルはDBから読んだ集合を組み合わせるだけである。
package auth

import (
	"context"
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
// RequirePermission（Design.md 6.4.4）が使う判定である。呼び出し側は
// EffectivePermissions の結果（昇順・重複なし）を渡す。
func HasPermission(effective []string, key string) bool {
	return slices.Contains(effective, key)
}

// ProjectAuthz は、あるアクターの1プロジェクトに対する認可の結果。
//
// RequireProjectPermission が組み立て、同じリクエストの後続（別の
// RequireProjectPermission やハンドラ）が読む。
type ProjectAuthz struct {
	// ProjectID は project.id。監査ログの target_id になる。
	ProjectID string
	// Key は project.key。URL の :key（ApiDesign.md 5.4）と一致する。
	Key string
	// Status は project.status（active / archived）。
	Status string
	// RoleKey は project_member.role_key。**空文字は非メンバー**。
	RoleKey string
	// Reachable はこのプロジェクトの存在を当人に見せてよいか。
	//
	// メンバーであるか、アドミニストレータであるときに真。偽なら 403 ではなく
	// 404 を返す（Design.md 6.4.5「存在を隠したい資源（他プロジェクト）は 404」）。
	// アドミニストレータを例外にするのは、ApiDesign.md 5.1 が一覧について
	// 「管理者は全件」と定めており、一覧に出たものを開けないのは矛盾するため。
	//
	// **`project.view` の有無では判定しない。** オペレータもシステムロールとして
	// project.view を持つ（DbDesign.md 7.3）ため、それで判定すると全プロジェクトが
	// 見えてしまい、5.1 の「自分がメンバーであるプロジェクトのみ」と食い違う。
	// project.view は「プロジェクトを閲覧する能力」であって「どのプロジェクトか」を
	// 決めるものではない。
	Reachable bool
	// Permissions は当該プロジェクトでの実効権限（Design.md 6.4.1）。
	//
	//	( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ スコープ
	Permissions []string
}

type systemPermissionsContextKey struct{}

// NewSystemPermissionsContext はシステムロール由来の実効権限を載せた
// コンテキストを返す。
//
// 同一リクエスト内で権限を2度計算しないための入れ物である。ルートに
// ミドルウェアが複数並んでも（.With(A).With(B)）、DBを引くのは1回で済む。
//
// **跨リクエストのキャッシュではない。** Design.md 6.4.5 が求める
// セッションへのキャッシュは手順6b で access_token 側に持たせる。
func NewSystemPermissionsContext(ctx context.Context, permissions []string) context.Context {
	return context.WithValue(ctx, systemPermissionsContextKey{}, permissions)
}

// SystemPermissionsFromContext はシステムロール由来の実効権限を取り出す。
// 2つ目の戻り値は「計算済みかどうか」であり、権限0件と未計算を区別する。
func SystemPermissionsFromContext(ctx context.Context) ([]string, bool) {
	p, ok := ctx.Value(systemPermissionsContextKey{}).([]string)
	return p, ok
}

type projectAuthzContextKey struct{}

// NewProjectAuthzContext はプロジェクトの認可結果を載せたコンテキストを返す。
func NewProjectAuthzContext(ctx context.Context, a *ProjectAuthz) context.Context {
	return context.WithValue(ctx, projectAuthzContextKey{}, a)
}

// ProjectAuthzFromContext は key に対する認可結果を取り出す。未計算なら nil。
//
// key を照合するのは、1リクエストが2つのプロジェクトに触れる経路（将来の
// チケット移動など）で、別のプロジェクトの判定結果を誤って使わないため。
func ProjectAuthzFromContext(ctx context.Context, key string) *ProjectAuthz {
	a, _ := ctx.Value(projectAuthzContextKey{}).(*ProjectAuthz)
	if a == nil || a.Key != key {
		return nil
	}
	return a
}
