package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	v1 "github.com/boyaki-machine/project-backyard/server/internal/httpapi/v1"
	"github.com/boyaki-machine/project-backyard/server/internal/mcp"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/webui"
)

// Deps はルータが必要とする外部資源と設定。
type Deps struct {
	Pool *pgxpool.Pool

	// Queries は sqlc の問い合わせ口。nil なら Pool から作る。
	// テストが DB を立てずに差し替えられるよう、インターフェースで受ける。
	Queries gen.Querier

	// Tx はトランザクションの実行口。nil なら Pool から作る。
	// Pool も nil の場合（ルート一覧を取るだけのテストなど）は nil のまま渡る。
	Tx v1.TxRunner

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

// MCPPath は MCP サーバのベースパス（Design.md 8.3）。
//
// この下に /{key} が付き、**URL パスにプロジェクトキーを含む**形になる
// （Requirements.md 10.8.8）。手順ファイルをプロジェクト非依存に保つための
// 決めごとであり、トークンのスコープとの整合はサーバが検証する。
const MCPPath = "/mcp"

// MCP のレート制限（ApiDesign.md 2.9 の「アクターあたり 600回/分」に合わせる）。
//
// **REST 側とは別の窓になる**（カウンタはミドルウェアの実体ごと）。ツールを
// 1回呼ぶと、外側の /mcp で1回、内側の REST 呼び出しで1回を数える。
const (
	mcpRateLimit  = 600
	mcpRateWindow = time.Minute
)

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
	tx := deps.Tx
	if tx == nil && deps.Pool != nil {
		tx = store.NewTxRunner(deps.Pool)
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// /healthcheck は probe が短間隔で叩くため DEBUG に落とす（Design.md 10.1）。
	r.Use(middleware.AccessLog(HealthPath))

	// chi の既定は本文なしの 404 / 405 を返すため、2.5 の形式に置き換える。
	//
	// ただし API 以外の未知パスは SPA のフォールバック先になる（Design.md 3.4）。
	// /healthcheck は下で登録済みのためここへは来ない（ApiDesign.md 2.11 の例外）。
	spa := webui.Handler()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			apierr.WriteCode(w, r, apierr.NotFound)
			return
		}
		// 画面の取得は GET / HEAD に限る。POST に index.html を返すと、
		// 綴りを誤った API 呼び出しが 200 で返ってきて発見が遅れる。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apierr.WriteCode(w, r, apierr.MethodNotAllowed)
			return
		}
		spa.ServeHTTP(w, r)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteCode(w, r, apierr.MethodNotAllowed)
	})

	// 認証不要・副作用なし。認証／CSRF／レート制限の対象外に置く（ApiDesign.md 2.11）。
	r.Get(HealthPath, health(deps.Version, deps.HealthShowVersion))

	v1Deps := v1.Deps{
		Queries:      q,
		Tx:           tx,
		CookieSecure: deps.CookieSecure,
	}

	r.Route(BasePath, func(r chi.Router) {
		v1.Mount(r, v1Deps)
	})

	mountMCP(r, q, v1Deps, deps.Version)

	return r
}

// mountMCP は MCP サーバを /mcp/{key} に並べる（Design.md 8.3）。
//
// **必要権限は agent.run である。** 0019 がこのキーの意味を「自分に紐づく
// エージェントを MCP から走らせてよい」と定め、operator / project_member /
// project_viewer へ配り直した（DbDesign.md 8.2.6）。個々のツールの権限
// （Design.md 8.2）は、この内側で REST 層が判定する。
//
// **RequireProjectPermission を通すことが要点である。** トークンが別の
// プロジェクトに紐づいていれば 404、非メンバーなら 404、権限が無ければ 403 が
// ここで返る（Design.md 6.4.5 / 8.3）——MCP 層に同じ判定を書かずに済む。
//
// **CSRF は掛けない。** ハンドラが Bearer 以外を弾くため（Design.md 8.3）、
// Cookie を自動送信するブラウザからの POST は認証の時点で 401 になる。
func mountMCP(r chi.Router, q gen.Querier, v1Deps v1.Deps, version string) {
	// **REST は別のルータ経由で叩く**（Design.md 8.4）。同じ木に置くと
	// httpapi → mcp → httpapi の循環になるため、内部呼び出し用に同じ
	// ルートをもう1本組む。**ミドルウェアの連鎖は v1.Mount が持っている**
	// ので、認証・認可・レート制限は同じものが同じ順で効く。
	//
	// レート制限のカウンタはミドルウェアの実体ごとに持たれる（ratelimit.go）
	// ため、こちらは外側とは別の窓になる。ツール1回につき、外側で1・
	// 内側で1を数える。
	internal := chi.NewRouter()
	internal.NotFound(func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteCode(w, r, apierr.NotFound)
	})
	internal.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteCode(w, r, apierr.MethodNotAllowed)
	})
	internal.Route(BasePath, func(r chi.Router) {
		v1.Mount(r, v1Deps)
	})

	h := mcp.New(internal, version)

	// **ミドルウェアはこの1本にだけ掛ける**（r.Route で /mcp の木ごと包まない）。
	// 包むと、/mcp 配下の**未知のパスまで認証を要求して 401 になる**——
	// 未知のパスは 404 であるべきで、/api/v1 側もそうなっている（v1.Mount は
	// 認証を Group の中だけに掛けている）。手順25 の検証で実際に踏んだ。
	//
	// **Handle はすべてのメソッドを受ける。** GET / DELETE をここで受けて
	// ハンドラが 405 を返す形にしてある（Design.md 8.4）——chi の 405 に
	// 任せると、SSE ストリームを持たないことが応答から読めない。
	r.With(
		middleware.Authenticate(q),
		middleware.RateLimit(mcpRateLimit, mcpRateWindow, middleware.ActorKey),
		middleware.RequireProjectPermission(q, "agent.run"),
	).Handle(MCPPath+"/{"+middleware.ProjectKeyURLParam+"}", h)
}

// isAPIPath は SPA のフォールバック対象外とするパスかを返す。
//
// 対象は REST（/api）と MCP（/mcp、Phase 2）の2つ。ここに該当するパスは、
// 未定義であっても index.html ではなく 2.5 形式の 404 を返す。
func isAPIPath(p string) bool {
	for _, prefix := range []string{"/api", "/mcp"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
