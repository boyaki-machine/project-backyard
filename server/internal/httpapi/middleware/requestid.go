// Package middleware は chi のミドルウェアを提供する（Design.md 4.1）。
//
// 認証・認可・CSRF・レート制限もここに置く。手順4a の時点では request_id のみ。
package middleware

import (
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// RequestID はリクエストごとに ULID を発行してコンテキストへ載せる。
//
// ULID にするのは、audit_log.id・activity.request_id が char(26) であり、
// エラー応答の request_id と突き合わせられるようにするため
// （ApiDesign.md 2.5、DbDesign.md 6.8）。
//
// クライアントから渡された ID は採用しない。監査ログの識別子になる値を
// リクエスト側に決めさせないため。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := apierr.NewRequestIDContext(r.Context(), ulidgen.New())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
