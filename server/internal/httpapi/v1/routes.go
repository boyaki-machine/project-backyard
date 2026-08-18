package v1

import (
	"context"
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

// TxRunner は複数の書き込みを1トランザクションで実行する。
//
// 実装は store.NewTxRunner（プール）。**インターフェースとして受け取るのは、
// ハンドラのテストが実DBを立てずに差し替えられるようにするため**であり、
// Queries を gen.Querier で受けているのと同じ理由による。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(gen.Querier) error) error
}

// Deps は /api/v1 のハンドラが必要とする外部資源と設定。
type Deps struct {
	// Queries は sqlc の問い合わせ口。
	Queries gen.Querier

	// Tx はトランザクションの実行口。POST /projects（ApiDesign.md 5.3）のように
	// 複数の書き込みが不可分である操作が使う。
	Tx TxRunner

	// CookieSecure は pb_session / pb_csrf に Secure 属性を付けるか
	// （PB_COOKIE_SECURE、Design.md 6.2.1 手順7）。
	CookieSecure bool
}

// Mount は /api/v1 のルートを r に並べる。
//
// **必要権限はルート定義に宣言する**（Design.md 6.4.4）。本関数を眺めるだけで
// 全エンドポイントの認証・認可要件が読み取れる状態を保つこと。
//
//	middleware.RequirePermission(deps.Queries, "user.manage")        システムロール層
//	middleware.RequireProjectPermission(deps.Queries, "ticket.close") プロジェクト層（{key} が要る）
//
// 認証・自分自身の3本（ApiDesign.md 3.1 / 3.2 / 4.1）に .With(...) が
// 付いていないのは、いずれも「必要権限：不要」または「認証済み・本人」で
// あり、権限キーを要求しないためである。
func Mount(r chi.Router, deps Deps) {
	h := &handler{q: deps.Queries, tx: deps.Tx, cookieSecure: deps.CookieSecure}

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

		// ── プロジェクト（ApiDesign.md 5章）──────────────────
		//
		// 一覧が要求するのは project.view であり、**どのプロジェクトが見えるかは
		// 決めない**（オペレータもシステムロールとして project.view を持つ）。
		// 可視範囲の絞り込みはハンドラ側のクエリが行う（5.1、手順6a の判断）。
		r.With(middleware.RequirePermission(deps.Queries, "project.view")).
			Get("/projects", h.listProjects)
		// check-key は作成前の確認であり、作成と同じ権限を要求する（5.2）。
		// 誰でも叩けると、キーの当たりを付けてプロジェクトの存在を探れる。
		r.With(middleware.RequirePermission(deps.Queries, "project.create")).
			Get("/projects/check-key", h.checkProjectKey)
		// Phase 1 では project.create を持つのはアドミニストレータのみ（5.3、
		// DbDesign.md 7.3）。ここで役割を名指ししないのは、権限の割り当てが
		// role_permission のデータ側で決まるためである（Design.md 6.4.2）。
		r.With(middleware.RequirePermission(deps.Queries, "project.create")).
			Post("/projects", h.createProject)

		// ── プロジェクト個別（5.4〜5.6）────────────────────────
		//
		// **ここから先は RequireProjectPermission を使う。** システムロールだけを
		// 見る RequirePermission では、非メンバーのオペレータ（system_role として
		// project.view を持つ）が他人のプロジェクトを読めてしまう。到達できない
		// プロジェクトは 404 に倒す（Design.md 6.4.5）。
		//
		// **{key} は check-key より後に並べても効く。** chi は静的なセグメントを
		// パラメータより優先するため、/projects/check-key は必ず上の行に届く。
		// 逆に check-key というキーのプロジェクトは到達不能になるので、
		// 5.2 の予約語に入れてある（reservedProjectKeys）。
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.view")).
			Get("/projects/{key}", h.getProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Patch("/projects/{key}", h.patchProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.archive")).
			Post("/projects/{key}/archive", h.archiveProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.archive")).
			Post("/projects/{key}/unarchive", h.unarchiveProject)
	})
}

// handler は /api/v1 のハンドラが共有する依存。
type handler struct {
	q            gen.Querier
	tx           TxRunner
	cookieSecure bool
}
