package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// Deps はルータが必要とする外部資源と設定。
type Deps struct {
	Pool *pgxpool.Pool

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
// 手順4a の時点では /healthcheck 以外のエンドポイントを定義していない。
func NewRouter(deps Deps) http.Handler {
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
		// 手順5以降でエンドポイントを追加する。
		// 認証・認可ミドルウェアは手順4b・6でこの階層に入る。
		_ = deps
	})

	return r
}
