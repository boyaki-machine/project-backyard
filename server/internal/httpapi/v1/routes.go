package v1

import (
	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
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
	// CSRF とレート制限（ApiDesign.md 2.4 / 2.9）は手順5b でここに足す。
	r.Post("/auth/login", h.login)

	// ── 認証必須 ────────────────────────────────
	// Cookie か Bearer での認証を必須とする（Design.md 6.2.2）。
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(deps.Queries))

		r.Post("/auth/logout", h.logout)
		r.Get("/me", h.me)
	})
}

// handler は /api/v1 のハンドラが共有する依存。
type handler struct {
	q            gen.Querier
	cookieSecure bool
}
