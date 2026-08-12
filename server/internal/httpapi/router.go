package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// Deps はルータが必要とする外部資源と設定。
type Deps struct {
	Pool *pgxpool.Pool

	// Queries は sqlc の問い合わせ口。nil なら Pool から作る。
	// テストが DB を立てずに差し替えられるよう、インターフェースで受ける。
	Queries gen.Querier

	// Version は GET /healthcheck が返すバージョン（ApiDesign.md 2.11）。
	Version string
	// HealthShowVersion が false なら version を返さない。既定は false。
	HealthShowVersion bool
}

// BasePath は API のベースパス（ApiDesign.md 2.1）。
const BasePath = "/api/v1"

// NewRouter はルータを組み立てる。
//
// 権限は chi のミドルウェアとしてルート定義に宣言する規約のため、
// エンドポイントは本関数の Route ブロックに並べる（Design.md 6.4.4）。
// 手順4b の時点では /healthcheck 以外のエンドポイントを定義していない。
func NewRouter(deps Deps) http.Handler {
	return newRouter(deps, nil)
}

// newRouter は認証必須グループへ追加のルートを差し込めるようにした本体。
//
// extra は同一パッケージのテストが「グループに属するルートが実際に
// Authenticate を通るか」を確かめるためだけに使う。手順5以降の実運用の
// ルートは、テストからの注入ではなく下の Group ブロックに直接並べること
// （routes を1か所に集めるのが Design.md 6.4.4 の狙いのため）。
func newRouter(deps Deps, extra func(chi.Router)) http.Handler {
	q := deps.Queries
	if q == nil {
		q = gen.New(deps.Pool)
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// /healthcheck は probe が短間隔で叩くため DEBUG に落とす（Design.md 10.1）。
	r.Use(middleware.AccessLog(HealthPath))

	// chi の既定は本文なしの 404 / 405 を返すため、2.5 の形式に置き換える。
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteCode(w, r, apierr.NotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteCode(w, r, apierr.MethodNotAllowed)
	})

	// 認証不要・副作用なし。認証／CSRF／レート制限の対象外に置く（ApiDesign.md 2.11）。
	r.Get(HealthPath, health(deps.Version, deps.HealthShowVersion))

	r.Route(BasePath, func(r chi.Router) {
		// ── 認証不要 ────────────────────────────────
		// POST /auth/login、GET /auth/providers は手順5でここに置く
		// （ApiDesign.md 3.1 / 3.3）。CSRF とレート制限も手順5で入る。

		// ── 認証必須 ────────────────────────────────
		// 以降のエンドポイントは Cookie か Bearer での認証を必須とする
		// （Design.md 6.2.2）。認可（RequirePermission）は手順6で、
		// ルートごとの宣言として個別に足す（Design.md 6.4.4）。
		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(q))

			// 手順5以降でエンドポイントを追加する。
			// 現時点で所属するルートは無く、外形上の挙動は 404 のままである。
			if extra != nil {
				extra(r)
			}
		})
	})

	return r
}
