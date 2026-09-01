// 実効権限（Design.md 6.4.1）のうちシステムロール層の解決と、
// そのセッションキャッシュ（6.4.5）。
//
// 認可ミドルウェア（authz.go）と GET /me（httpapi/v1）の双方が使う。
// **1か所に置く。** 「キャッシュがあれば使い、無ければ計算して書き戻す」を
// 呼び出し側ごとに書くと、片方だけキャッシュを見る状態になりやすい。
// そうなると画面が出したボタンが 403 になる（/me が新しい権限を返すのに
// ミドルウェアは古い権限で拒む、あるいはその逆）。
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// permissionCacheWriteTimeout はキャッシュの書き戻しに許す時間。
// 認可の判定は既に済んでいるため、待たせない。
const permissionCacheWriteTimeout = 3 * time.Second

// SystemPermissions はシステムロール由来の実効権限を返す（Design.md 6.4.1）。
//
//	実効権限 = システムロールの権限 ∩ トークンのスコープ
//
// 参照する順は3段ある。
//
//  1. リクエストのコンテキスト  ミドルウェアを重ねても計算は1回で済む（手順6a）
//  2. セッションのキャッシュ    access_token の列。認証時に読み込み済みで、DBを引かない（手順6b）
//  3. DB                        ロールから計算し、2 へ書き戻す
//
// 返すコンテキストには 1 の結果が載っている。呼び出し側は後続へ渡すこと。
func SystemPermissions(ctx context.Context, q gen.Querier, p *auth.Principal) ([]string, context.Context, error) {
	if cached, ok := auth.SystemPermissionsFromContext(ctx); ok {
		return cached, ctx, nil
	}
	if cached, ok := p.FreshPermissions(time.Now()); ok {
		// **読むときにもスコープとの積を取る。** キャッシュには積を取った後の
		// 値が入っているので通常は何も変わらないが、こうしておくと
		// 「トークンのスコープは発行後に変わらない」という前提が崩れても
		// 権限が広がる側には倒れない。積は縮小しかしない（Design.md 6.4.1）。
		cached = auth.EffectivePermissions(cached, nil, p.Scopes)
		return cached, auth.NewSystemPermissionsContext(ctx, cached), nil
	}

	permissions, err := ComputeSystemPermissions(ctx, q, p.SystemRole, p.Scopes)
	if err != nil {
		return nil, ctx, err
	}

	// **システムロールを持たないアクターには書かない。**
	// 計算にDBを引いていないので、キャッシュしても次回に節約できるものが無い。
	// 書けば TTL ごとに空配列を UPDATE し続けるだけになる。
	//
	// **エージェントにも書かない**（Design.md 6.5 の委譲、0019）。エージェントの
	// SystemRole には**所有者のロール**が入っているのでキャッシュは効きうるが、
	// **無効化が届かない**。6.4.5 の無効化はアクター単位（当人の全トークン）で
	// 行われるため、所有者のロールを変えても、**別アクターであるエージェントの
	// トークンに載ったキャッシュは残る**。TTL 5分の安全弁はあるが、権限を
	// 絞る変更が最大5分効かないことになる。
	//
	// 書かなければ毎回 ListRolePermissions を1本引くだけで、常に正本と一致する。
	// エージェントの往復は MCP 経由であり、画面のように全ページで呼ばれるもの
	// ではないので、この1本を惜しむ理由がない。
	if p.SystemRole != "" && !p.IsAgent() {
		SaveSystemPermissionCache(ctx, q, p.TokenID, p.SystemRole, permissions)
	}

	return permissions, auth.NewSystemPermissionsContext(ctx, permissions), nil
}

// ComputeSystemPermissions はロールの割り当てをDBから読んで実効権限を計算する。
//
// **キャッシュを見ない。** ログイン直後のようにキャッシュがまだ無い経路と、
// SystemPermissions の 3段目が使う。
func ComputeSystemPermissions(
	ctx context.Context, q gen.Querier, systemRole string, scopes []string,
) ([]string, error) {
	var rolePermissions []string
	if systemRole != "" {
		// システムロールを持たないアクター（エージェント）は空のまま。
		// 権限0件になり、RequirePermission はすべて 403 になる。
		var err error
		rolePermissions, err = q.ListRolePermissions(ctx, systemRole)
		if err != nil {
			return nil, fmt.Errorf("システムロール %q の権限を読めない: %w", systemRole, err)
		}
	}
	return auth.EffectivePermissions(rolePermissions, nil, scopes), nil
}

// SaveSystemPermissionCache は実効権限をセッションへ書き戻す（Design.md 6.4.5）。
//
// systemRole は permissions を計算したときのロールである。**書き込みの条件になる**
// （queries/authz.sql を参照）。この間にロールが変わっていれば0行更新で落ち、
// 無効化を追い越して旧権限を復活させることがない。
//
// **失敗しても処理は続ける。** 書けなければ次のリクエストで計算し直すだけで、
// 応答の中身は変わらない。キャッシュが1回書けなかったためにリクエストを
// 落とす価値はない（last_used_at と同じ扱い）。0行更新も失敗と区別しない。
//
// **本関数はミドルウェアの中で同期的に走る**（認可の判定より前）。応答後の
// 後片付けではない。それでもリクエストのキャンセルを引き継がないのは、
// 計算が既に済んでおり、捨てると次のリクエストでもう一度DBを引くことになる
// ためである。同期的である以上は長く待てないので、上限を短く切る。
func SaveSystemPermissionCache(
	ctx context.Context, q gen.Querier, tokenID, systemRole string, permissions []string,
) {
	raw, err := auth.EncodeCachedPermissions(permissions)
	if err != nil {
		warnPermissionCache(ctx, tokenID, err)
		return
	}

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), permissionCacheWriteTimeout)
	defer cancel()

	if err := q.SaveTokenPermissionCache(saveCtx, gen.SaveTokenPermissionCacheParams{
		ID:                tokenID,
		SystemRole:        systemRole,
		CachedPermissions: raw,
	}); err != nil {
		warnPermissionCache(ctx, tokenID, err)
	}
}

func warnPermissionCache(ctx context.Context, tokenID string, err error) {
	slog.WarnContext(ctx, "実効権限をセッションへ保存できなかった",
		slog.String("request_id", apierr.RequestIDFromContext(ctx)),
		slog.String("token_id", tokenID),
		slog.String("cause", err.Error()),
	)
}

// requirePrincipal は認可の判定に要るプリンシパルを取り出す。
//
// 無いのは、認証必須グループの外に認可ミドルウェアを置いた場合だけである。
// **401 ではなく 500 にする。** ルート定義の誤りが「未認証」に見えると
// 発見が遅れる。
func requirePrincipal(w http.ResponseWriter, r *http.Request, what string) *auth.Principal {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
			fmt.Errorf("%s が Authenticate より前に置かれている", what)))
		return nil
	}
	return p
}
