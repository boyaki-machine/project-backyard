package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/backup"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/dbstat"
	"github.com/boyaki-machine/project-backyard/server/internal/holiday"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	v1 "github.com/boyaki-machine/project-backyard/server/internal/httpapi/v1"
	"github.com/boyaki-machine/project-backyard/server/internal/maintenance"
	"github.com/boyaki-machine/project-backyard/server/internal/mcp"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
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

	// Settings は実行中の設定（Design.md 10.3 の第2層）。
	//
	// **HealthShowVersion と CookieSecure を bool で持たない。**
	// どちらも画面から変えられるので、組み立て時の値を
	// 畳み込むと変更が効かない。**nil なら既定値だけの Live を組む。**
	Settings *config.Live

	// OnSettingsChanged は設定が変わったときに呼ばれる（任意）。
	// ロガーの入れ替えを cmd 側で行うための口である。
	OnSettingsChanged func(*config.Set)

	// Certs は出す TLS 証明書の入れ物（Design.md 6.6.1）。**nil なら
	// TLS で待ち受けていない**（証明書の登録はできる）。
	Certs *tlscert.Holder
	// TLSListening は実際に TLS で待ち受けているか。**設定の実効値ではない**
	// ——設定を変えても再起動までは待受が変わらないためである。
	TLSListening bool
	// ListenURL は実際に待ち受けているスキームとアドレス（ApiDesign.md 11.4）。
	ListenURL string

	// DBStats は DB の接続状態と統計を読む口（ApiDesign.md 11.10）。
	// **nil なら Pool から作る。** Pool も nil なら nil のまま渡る。
	DBStats v1.DatabaseStats

	// Backups は PB 全体の書き出しと取り込みの口（ApiDesign.md 11.11〜11.12）。
	// **nil なら Pool と DatabaseURL から作る。**
	Backups v1.Backups

	// Holidays は祝日カレンダーの取得口（ApiDesign.md 5.8.3）。**nil なら
	// 実際に Google へ取りに行く口を組む。** テストは偽物を渡す。
	Holidays holiday.Fetcher
	// DatabaseURL は PB がいま繋いでいる接続文字列（pb_app）。**取り込みで
	// 接続先だけを取り出すために要る**——ロールとパスワードは画面から受け取る。
	DatabaseURL string

	// Maintenance は保守モードの旗（Design.md 10.4）。**nil なら作る。**
	Maintenance *maintenance.Flag
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
	// **Pool が nil のときに dbstat.New(nil) を入れない。** 中身が nil の
	// ポインタをインターフェースに入れると nil と判定されず、ハンドラが
	// 500 ではなく panic する。
	dbStats := deps.DBStats
	if dbStats == nil && deps.Pool != nil {
		dbStats = dbstat.New(deps.Pool)
	}

	backups := deps.Backups
	if backups == nil && deps.Pool != nil {
		backups = backup.NewService(deps.Pool, deps.DatabaseURL, deps.Version)
	}
	flag := deps.Maintenance
	if flag == nil {
		flag = maintenance.New(func(on bool) {
			if on {
				slog.Warn("保守モードに入った。取り込みが終わるまで要求を受け付けない")
			} else {
				slog.Info("保守モードを出た")
			}
		})
	}

	r := chi.NewRouter()

	// **最も外側に積む**（ApiDesign.md 2.12、Design.md 6.6.2）。
	// 保守モードで止めた応答にも、エラー応答にも付くようにするため。
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.RequestID)
	// /healthcheck は probe が短間隔で叩くため DEBUG に落とす（Design.md 10.1）。
	r.Use(middleware.AccessLog(HealthPath))
	// **保守モード中は要求を止める**（ApiDesign.md 11.13、Design.md 10.4）。
	// **RequestID とアクセスログの内側に置く**——止めた要求もログに残す。
	r.Use(middleware.Maintenance(flag, maintenanceExempt))

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
	settings := deps.Settings
	if settings == nil {
		settings = config.LiveDefaults()
	}
	r.Get(HealthPath, health(deps.Version, settings.HealthShowVersion))

	v1Deps := v1.Deps{
		Queries:           q,
		Tx:                tx,
		Settings:          settings,
		OnSettingsChanged: deps.OnSettingsChanged,
		Certs:             deps.Certs,
		TLSListening:      deps.TLSListening,
		ListenURL:         deps.ListenURL,
		DBStats:           dbStats,
		Backups:           backups,
		Maintenance:       flag,
		// 祝日カレンダーの取得口（ApiDesign.md 5.8.3）。相手先が送り主を
		// 見分けられるよう、User-Agent に PB と版を入れる。
		Holidays: deps.Holidays,
	}
	if v1Deps.Holidays == nil {
		v1Deps.Holidays = holiday.NewHTTPFetcher("ProjectBackyard/" + deps.Version)
	}

	r.Route(BasePath, func(r chi.Router) {
		v1.Mount(r, v1Deps)
	})

	mountMCP(r, q, v1Deps, deps.Version)

	return r
}

// maintenanceExempt は保守モードでも素通しする要求を選ぶ（ApiDesign.md 11.13）。
//
//	/healthcheck        監視が落ちたと読まないように 200 のまま（2.11）
//	POST /admin/restore 取り込みの口そのもの。止めると自分を止めることになる
func maintenanceExempt(r *http.Request) bool {
	if r.URL.Path == HealthPath {
		return true
	}
	return r.Method == http.MethodPost && r.URL.Path == BasePath+"/admin/restore"
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
// 対象は REST（/api）と MCP（/mcp）の2つ。ここに該当するパスは、
// 未定義であっても index.html ではなく 2.5 形式の 404 を返す。
func isAPIPath(p string) bool {
	for _, prefix := range []string{"/api", "/mcp"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
