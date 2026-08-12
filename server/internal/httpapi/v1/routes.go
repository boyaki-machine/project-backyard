package v1

import (
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// レート制限の上限と窓（ApiDesign.md 2.9）。
//
// 表のもう1行「アカウントあたり 5回/15分（超過で account_locked）」は
// Design.md 6.3 のロックそのもので、local_credential の failed_attempts /
// locked_until として login.go に実装済みである。
const (
	loginRateLimit  = 10
	loginRateWindow = time.Minute
	actorRateLimit  = 600
	actorRateWindow = time.Minute
)

// Deps は /api/v1 のハンドラが必要とする外部資源と設定。
type Deps struct {
	// Queries は sqlc の問い合わせ口。
	Queries gen.Querier

	// CookieSecure は pb_session / pb_csrf に Secure 属性を付けるか
	// （PB_COOKIE_SECURE、Design.md 6.2.1 手順7）。
	CookieSecure bool
}

// Mount は /api/v1 のルートを r に並べる。
//
// **必要権限はルート定義に宣言する**（Design.md 6.4.4）。本関数を眺めるだけで
// 全エンドポイントの認証・認可要件が読み取れる状態を保つこと。認可
// （RequirePermission）は手順6で、ここに .With(...) として足す。
func Mount(r chi.Router, deps Deps) {
	h := &handler{q: deps.Queries, cookieSecure: deps.CookieSecure}

	// ── 認証不要 ────────────────────────────────
	// ログインは認証を通れない状態で叩くもののため、認証必須グループの外に置く。
	// CSRF（2.4）の対象にもならない。Cookie 認証ではないためである。
	// IP 単位のレート制限だけを掛ける（2.9）。
	r.With(middleware.RateLimit(loginRateLimit, loginRateWindow, middleware.ClientIPKey)).
		Post("/auth/login", h.login)

	// ── 認証必須 ────────────────────────────────
	// Cookie か Bearer での認証を必須とする（Design.md 6.2.2）。
	//
	// 連鎖の順は 認証 → レート制限 → CSRF とする。レート制限を CSRF より
	// 前に置くのは、CSRF に失敗し続けるリクエストも「送られた回数」として
	// 数えるためである。どちらもアクターが確定していないと判定できないので、
	// 認証より前には置けない。
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(deps.Queries))
		r.Use(middleware.RateLimit(actorRateLimit, actorRateWindow, middleware.ActorKey))
		r.Use(middleware.RequireCSRF)

		r.Post("/auth/logout", h.logout)
		r.Get("/me", h.me)
	})
}

// handler は /api/v1 のハンドラが共有する依存。
type handler struct {
	q            gen.Querier
	cookieSecure bool
}
