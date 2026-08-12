// POST /api/v1/auth/logout（ApiDesign.md 3.2）。
//
//	現在のセッショントークンを失効（revoked_at を設定）し、Cookie を削除する。
//	204 No Content。
package v1

import (
	"fmt"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// logout は POST /api/v1/auth/logout を処理する。
//
// 失効させるのは**このリクエストが使ったトークンだけ**である。他の端末の
// セッションは残る（全失効は DELETE /me/sessions、手順12）。
func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /auth/logout が認証ミドルウェアを通っていない")))
		return
	}

	if err := h.q.RevokeAccessToken(r.Context(), p.TokenID); err != nil {
		// 失効できていないのに Cookie だけ消すと、トークンは有効なまま
		// 利用者からは「ログアウトできた」ように見える。失敗として返す。
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	audit.FromRequest(r).RecordOrLog(r.Context(), h.q, audit.Entry{
		Action:     audit.Logout,
		Result:     audit.Success,
		TargetType: "access_token",
		TargetID:   p.TokenID,
	})

	// Bearer 認証でも Cookie の削除指示を返して差し支えない（送っていなければ
	// 消すものが無いだけ）。経路で分岐させると、片方の削除漏れを生みやすい。
	h.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}
