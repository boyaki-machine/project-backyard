package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	v1 "github.com/boyaki-machine/project-backyard/server/internal/httpapi/v1"
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

	// CookieSecure は pb_session / pb_csrf に Secure を付けるか
	// （PB_COOKIE_SECURE、Design.md 6.2.1 手順7）。
	CookieSecure bool
}

// BasePath は API のベースパス（ApiDesign.md 2.1）。
const BasePath = "/api/v1"

// NewRouter はルータを組み立てる。
//
// /api/v1 のエンドポイントは v1 パッケージが持つ（Design.md 4.1）。
// 本関数が並べるのは、バージョンの外にある /healthcheck と、
// 全リクエストに共通のミドルウェア連鎖・エラー形式だけである。
func NewRouter(deps Deps) http.Handler {
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
		v1.Mount(r, v1.Deps{
			Queries:      q,
			CookieSecure: deps.CookieSecure,
		})
	})

	return r
}
