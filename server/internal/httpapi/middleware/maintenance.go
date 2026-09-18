package middleware

import (
	"net/http"
	"strings"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/maintenance"
)

// maintenanceHTML は保守モード中にブラウザへ返す頁（ApiDesign.md 11.13）。
//
// **SPA を返さない。** 返すと画面は動き出してから API で失敗し、何が起きているかは
// その API のエラーからしか読めない。**Vue を積まない単一の HTML** を返す。
//
// **再読み込みのボタンを置かない**（GuiDesign.md 5.12.2）——押せる時刻が分からない。
//
// **配色は GuiDesign.md 8章のトークンに寄せるが、CSS は持ち込まない。** この頁は
// client のビルド成果物を読まずに返るものであり、外部の資源を1つも要らない形にする。
const maintenanceHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>メンテナンス中 — Project Backyard</title>
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    font-family: system-ui, -apple-system, "Hiragino Sans", "Noto Sans JP", sans-serif;
    background: #f4f4f5; color: #27272a;
  }
  @media (prefers-color-scheme: dark) { body { background: #18181b; color: #e4e4e7; } }
  main { max-width: 32rem; padding: 2rem; text-align: center; line-height: 1.8; }
  h1 { font-size: 1.25rem; font-weight: 600; margin: 0 0 1rem; }
  p { margin: 0; font-size: 0.9375rem; }
</style>
</head>
<body>
<main>
  <h1>メンテナンス中です</h1>
  <p>バックアップの取り込みを行っています。<br>しばらくしてから開き直してください。</p>
</main>
</body>
</html>
`

// Maintenance は保守モード中の要求を止める（ApiDesign.md 11.13、Design.md 10.4）。
//
// **人には読める画面を、機械には読める形を返す。**
//
//	/api と /mcp          → 503 と 2.5 のエラー形式（code は maintenance）
//	それ以外のパス        → 503 と、単一の静的な HTML
//	exempt が真のパス     → 素通し（/healthcheck と、取り込みの口そのもの）
//
// **ステータスはどちらも 503 にする。** ブラウザはステータスに関係なく本文を描くので
// 画面は成立し、200 にするとエージェントと監視が成功と読む。
func Maintenance(flag *maintenance.Flag, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !flag.On() || (exempt != nil && exempt(r)) {
				next.ServeHTTP(w, r)
				return
			}
			if wantsJSON(r) {
				apierr.WriteCode(w, r, apierr.Maintenance)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// **溜め込ませない。** 保守モードが終わったあとに、この頁が
			// 返り続けることのないようにする。
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(maintenanceHTML))
		})
	}
}

// wantsJSON は、機械向けの応答を返す相手かを見る。
//
// **パスで決める。** Accept ヘッダで決めると、ブラウザの fetch と画面遷移で
// 同じパスの扱いが割れる。
func wantsJSON(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api" || strings.HasPrefix(p, "/api/") ||
		p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}
