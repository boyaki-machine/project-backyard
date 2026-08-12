package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// safeMethods は状態を変えないメソッド（RFC 9110 §9.2.1）。
//
// ApiDesign.md 2.4 は対象を「POST/PATCH/PUT/DELETE」と列挙しているが、
// ここでは「安全なもの以外を検証する」と裏返して持つ。列挙side で持つと、
// 将来ハンドラを増やしたときに列挙から漏れたメソッドが素通りするため。
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
}

// RequireCSRF は Cookie 認証の状態変更系リクエストに CSRF トークンを要求する
// （ApiDesign.md 2.4）。
//
//	Cookie:  pb_csrf=<random>   ← HttpOnly ではない（JS から読む）
//	Header:  X-PB-CSRF: <同じ値>
//
// 一致しなければ 403 csrf_failed。トークンは 3.1 のログイン応答で発行する。
//
// **Bearer 認証では要求しない。** Authorization ヘッダは他サイトからの
// リクエストに自動では付かないため、CSRF が成立しない。
//
// 判定に使うのは Principal.Source であって token_type ではない。api トークンを
// Cookie に載せて送ることも技術的には可能で、その場合はブラウザが自動送信する
// 以上 CSRF の対象になる（auth.CredentialSource の説明）。
//
// **Authenticate の後に置くこと。** Principal が無いと Cookie 認証かどうかを
// 判定できず、すべて素通りする。
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethods[r.Method] {
			next.ServeHTTP(w, r)
			return
		}

		p := auth.PrincipalFromContext(r.Context())
		if p == nil || p.Source != auth.SourceCookie {
			next.ServeHTTP(w, r)
			return
		}

		if reason := csrfFailure(r); reason != "" {
			// 理由は応答に出さない。どこまで合っていたかを攻撃側に教えないため、
			// 「Cookie が無い」「不一致」をすべて同じ 403 に倒し、区別が要る
			// 運用者向けの情報はサーバログにのみ出す（ApiDesign.md 2.5）。
			apierr.Write(w, r, apierr.New(apierr.CSRFFailed).WithCause(errors.New(reason)))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// csrfFailure は検証に失敗した理由を返す。通してよいなら空文字。
//
// 応答には出さない。サーバログへ出す文言である。
func csrfFailure(r *http.Request) string {
	c, err := r.Cookie(auth.CSRFCookieName)
	if err != nil || c.Value == "" {
		return "pb_csrf Cookie が無い"
	}

	header := r.Header.Get(auth.CSRFHeaderName)
	if header == "" {
		return "X-PB-CSRF ヘッダが無い"
	}

	// 一致判定は定数時間で行う。長さが違えば ConstantTimeCompare は 0 を返す。
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
		return "pb_csrf Cookie と X-PB-CSRF ヘッダが一致しない"
	}
	return ""
}
