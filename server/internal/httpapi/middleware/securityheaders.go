package middleware

import "net/http"

// contentSecurityPolicy は全応答に付ける CSP（ApiDesign.md 2.12、Design.md 6.6.2）。
//
// **値は実測で決めた**。ビルド後の index.html はインラインスクリプトを
// 持たず（Vite が外部ファイルを参照する）、CSS は外部リソースを読まず、client の
// ソースに外部ドメインの参照も無い。**だから `script-src 'self'` で足りる。**
//
// **`style-src` にだけ `'unsafe-inline'` を許す。** Vue の `:style` 束縛と
// CodeMirror が実行時にスタイルを当てるためである。**スクリプトの実行を止めるのは
// `script-src` であり、そちらは緩めていない。**
//
// **`img-src` の `data:` は TOTP の QR である**（QRCode.toDataURL が返す。
// ApiDesign.md 4.6.2）。これを落とすと第2要素の登録画面が空になる。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// SecurityHeaders は全応答にセキュリティヘッダを付ける（ApiDesign.md 2.12）。
//
// **いまの防御が崩れた日の受け皿である。** XSS のガードを誰かが外した日に、
// **止めるものがここ以外に無い**（Design.md 6.6.2）。
//
// **最も外側に積む。** 保守モードで止めた応答にも、エラー応答にも付くようにする。
//
// **`Strict-Transport-Security` は出さない**（6.6.2）。PB は http でも動く設計で、
// **一度ブラウザが受け取ると http で開けなくなる**——開発端末や、TLS を無効へ
// 戻した環境で締め出しが起きる。
//
// **API の応答にも付ける。** CSP は JSON には効かないが、**`nosniff` は効く**
// ——Content-Type を無視して別の型として解釈させる経路を塞ぐ。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		// **frame-ancestors と重ねて出す。** CSP を読まない古いブラウザ向けで、
		// 両方ある場合は frame-ancestors が優先される（仕様）。
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
