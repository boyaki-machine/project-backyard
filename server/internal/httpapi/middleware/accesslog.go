package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// AccessLog は1リクエストにつき1行を、応答を返し終えた時点で出す（Design.md 10.1）。
//
// クエリ文字列は記録しない。検索語やメールアドレスがログに残るため。
// エラーの内部原因は apierr が別行で出す。1行の意味を「1リクエストの結果」に
// 保ち、収集基盤側のスキーマを安定させるため。
//
// quietPaths に挙げたパスは、**成功した場合に限り** DEBUG で出す。probe が
// 既定レベルの出力を埋め尽くさないようにするためだが、そのパスへの 4xx / 5xx
// （メソッド違いや probe の設定誤り）まで隠れると気づけなくなるため。
func AccessLog(quietPaths ...string) func(http.Handler) http.Handler {
	quiet := make(map[string]bool, len(quietPaths))
	for _, p := range quietPaths {
		quiet[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status < http.StatusBadRequest && quiet[r.URL.Path]:
				level = slog.LevelDebug
			}

			slog.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", apierr.RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.Int("bytes", rec.bytes),
				slog.String("ip", clientIP(r)),
			)
		})
	}
}

// recorder は応答のステータスと本文長を記録する ResponseWriter。
type recorder struct {
	http.ResponseWriter
	status  int
	bytes   int
	written bool
}

func (rec *recorder) WriteHeader(status int) {
	if rec.written {
		return
	}
	rec.status = status
	rec.written = true
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *recorder) Write(b []byte) (int, error) {
	// WriteHeader を呼ばずに Write した場合、net/http は 200 を送る。
	rec.written = true
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// clientIP は接続元アドレスからポートを落とす。
//
// audit_log.ip（inet 型）と同じ値にする。プロキシ経由の実IP解決
// （X-Forwarded-For 等）は Phase 1 では行わない。詐称可能なヘッダを
// 検証なしに信じると、監査ログの発信元が偽装できてしまうため。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
