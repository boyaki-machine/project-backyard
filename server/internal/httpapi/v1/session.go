// セッションの発行と Cookie の付け外し（Design.md 6.2.1 手順6〜7、
// ApiDesign.md 2.3 / 2.4 / 3.1）。
package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// SessionMaxAge はセッションの寿命。
// ApiDesign.md 3.1 の Set-Cookie が定める Max-Age=1209600（14日）と一致させる。
const SessionMaxAge = 14 * 24 * time.Hour

// sessionCookiePath は 2つの Cookie の Path（ApiDesign.md 3.1）。
const sessionCookiePath = "/"

// issuedSession は発行結果。平文は呼び出し側が Cookie に載せるためだけに使い、
// DB にもログにも渡さない（Design.md 6.2.1）。
type issuedSession struct {
	TokenID    string
	Plaintext  string
	CSRFToken  string
	ExpiresAt  time.Time
	ClientInfo string
}

// issueSession は access_token を1行作り、平文トークンと CSRF トークンを返す。
//
// scopes は空配列にする。ブラウザのセッションに縮小は不要であり、空は
// 「絞り込みなし」を意味する（Design.md 6.4.1）。
func (h *handler) issueSession(ctx context.Context, actorID, clientInfo string) (issuedSession, error) {
	plaintext, err := auth.NewToken(auth.SessionTokenPrefix)
	if err != nil {
		return issuedSession{}, err
	}
	csrfToken, err := auth.NewCSRFToken()
	if err != nil {
		return issuedSession{}, err
	}
	scopes, err := auth.EncodeScopes(nil)
	if err != nil {
		return issuedSession{}, err
	}

	s := issuedSession{
		TokenID:    ulidgen.New(),
		Plaintext:  plaintext,
		CSRFToken:  csrfToken,
		ExpiresAt:  time.Now().Add(SessionMaxAge),
		ClientInfo: clientInfo,
	}

	err = h.q.CreateAccessToken(ctx, gen.CreateAccessTokenParams{
		ID:        s.TokenID,
		ActorID:   actorID,
		TokenType: auth.TokenTypeSession,
		TokenHash: auth.HashToken(plaintext),
		// 一覧表示用（DbDesign.md 6.2）。接頭辞8文字なので pb_sess_ になる。
		TokenPrefix: pgtype.Text{String: auth.TokenPrefix(plaintext), Valid: true},
		Scopes:      scopes,
		ExpiresAt:   pgtype.Timestamptz{Time: s.ExpiresAt, Valid: true},
		// GET /me/sessions（手順12）が「Chrome / macOS」を出すための素材。
		// ここで残さないと後から取れないため、User-Agent をそのまま入れる。
		ClientInfo: text(clientInfo),
	})
	if err != nil {
		return issuedSession{}, fmt.Errorf("セッションを発行できない: %w", err)
	}
	return s, nil
}

// setSessionCookies は pb_session と pb_csrf を付ける（ApiDesign.md 3.1）。
//
//	Set-Cookie: pb_session=pb_sess_...; HttpOnly; SameSite=Lax; Path=/; Max-Age=1209600
//	Set-Cookie: pb_csrf=...;            SameSite=Lax; Path=/; Max-Age=1209600
//
// pb_csrf に HttpOnly を付けないのは、JS が読んで X-PB-CSRF に載せるため
// （ApiDesign.md 2.4 の double-submit）。**両者の Max-Age を揃える。**
// pb_csrf だけをセッションCookieにすると、ブラウザを閉じた後も 14日残る
// pb_session に対して CSRF トークンだけが消え、状態変更系が必ず失敗する。
func (h *handler) setSessionCookies(w http.ResponseWriter, s issuedSession) {
	maxAge := int(SessionMaxAge / time.Second)

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    s.Plaintext,
		Path:     sessionCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    s.CSRFToken,
		Path:     sessionCookiePath,
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookies は 2つの Cookie を削除する（ApiDesign.md 3.2）。
//
// 属性は発行時と揃える。Path や Secure が違うと同名の別Cookieとして扱われ、
// 元のCookieがブラウザに残る。
func (h *handler) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     sessionCookiePath,
			MaxAge:   -1, // 即時削除
			HttpOnly: name == auth.SessionCookieName,
			Secure:   h.cookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

// text は空文字を NULL に写す。
func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
