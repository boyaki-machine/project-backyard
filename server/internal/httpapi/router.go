package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// Deps はルータが必要とする外部資源。
type Deps struct {
	Pool *pgxpool.Pool
}

// BasePath は API のベースパス（ApiDesign.md 2.1）。
const BasePath = "/api/v1"

// NewRouter はルータを組み立てる。
//
// 権限は chi のミドルウェアとしてルート定義に宣言する規約のため、
// エンドポイントは本関数の Route ブロックに並べる（Design.md 6.4.4）。
// 手順4a の時点ではエンドポイントを1つも定義していない。
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)

	// chi の既定は本文なしの 404 / 405 を返すため、2.5 の形式に置き換える。
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

	r.Route(BasePath, func(r chi.Router) {
		// 手順5以降でエンドポイントを追加する。
		// 認証・認可ミドルウェアは手順4b・6でこの階層に入る。
		_ = deps
	})

	return r
}

// notFound は未知のパスと、許可されていないメソッドの両方に使う。
//
// ApiDesign.md 2.5.1 に 405 の行が無いため、表にあるコードだけで応答する。
// 未定義のコードを実装側で作らないことを優先した（PROGRESS.md に記録）。
func notFound(w http.ResponseWriter, r *http.Request) {
	apierr.WriteCode(w, r, apierr.NotFound)
}
