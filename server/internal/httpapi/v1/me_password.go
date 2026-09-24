// 自分自身に関するAPI（ApiDesign.md 4章）のうち、パスワードの変更。
//
//	POST /api/v1/me/password  4.3
//
// **6.6（管理者によるリセット）と役割が違う。** あちらは当人を締め出して
// 新しい平文を管理者に渡すもので、こちらは当人が自分で決めた値に差し替える。
// 共通するのは「他のセッションを失効させる」という帰結だけで、現在のパスワード
// 検証・must_change のクリア・現在のセッションを残す点はこちらにしかない。
package v1

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// changePasswordRequest は 4.3 のリクエスト本体。
//
// **どちらも必須なので値型で受ける。** 6.6 の passwordResetRequest が
// ポインタなのは全項目に既定があるためで、こちらは既定を持てない。
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changeMyPassword は POST /api/v1/me/password を処理する（ApiDesign.md 4.3）。
//
// **現在のセッションだけを残して失効させる**（Design.md 6.3）。パスワードを
// 変えた本人が、その操作の直後に締め出されることを避けるためである。
// 6.6（管理者によるリセット）が全失効なのは、そちらの目的が「乗っ取られた
// 疑いのある端末を切る」ことにあり、操作者と対象者が別人だからである。
func (h *handler) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /me/password が認証ミドルウェアを通っていない")))
		return
	}

	var req changePasswordRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateChangePassword(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **平文のハッシュ化は DB を触る前に済ませる**（6.6 と同じ）。Argon2id は
	// 意図的に重く、トランザクションの中で回すと行ロックを掴んだまま待たせる。
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスワードをハッシュ化できない: %w", err)))
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		cred, err := q.FindMyLocalCredential(ctx, p.ActorID)
		if errors.Is(err, pgx.ErrNoRows) {
			// パスワード認証を使っていない（IdP のみ。構想）。6.6 と同じ扱い。
			return apierr.New(apierr.Conflict).
				WithMessage("パスワード認証を使っていないアカウントのため、変更できません")
		} else if err != nil {
			return fmt.Errorf("資格情報を引けない: %w", err)
		}

		// **現在のパスワードを検証する**（4.3）。失敗は 401 invalid_credentials。
		//
		// **failed_attempts は増やさない**（4.3）。既にセッションを持つ本人の
		// 操作であり、ここで数えると自分で自分を締め出せる。連打はアクター単位の
		// レート制限（2.9、600回/分）が抑える。
		ok, err := auth.VerifyPassword(req.CurrentPassword, cred.PasswordHash)
		if err != nil {
			return fmt.Errorf("パスワードを検証できない: %w", err)
		}
		if !ok {
			return apierr.New(apierr.InvalidCredentials).
				WithMessage("現在のパスワードが正しくありません").
				WithDetails(apierr.Detail{
					Field: "current_password", Code: "invalid",
					Message: "現在のパスワードを確認してください",
				})
		}

		// **must_change もここで false になる**（ChangeMyPassword の中）。
		// これをしないと、要パスワード変更で入った利用者が変更しても誘導が
		// 消えず、変更画面へ戻され続ける。
		if err := q.ChangeMyPassword(ctx, gen.ChangeMyPasswordParams{
			PasswordHash: hash,
			IdentityID:   cred.IdentityID,
		}); err != nil {
			return fmt.Errorf("パスワードを更新できない: %w", err)
		}

		revoked, err := q.RevokeMyOtherSessions(ctx, gen.RevokeMyOtherSessionsParams{
			ActorID:        p.ActorID,
			CurrentTokenID: p.TokenID,
		})
		if err != nil {
			return fmt.Errorf("他のセッションを失効できない: %w", err)
		}

		// **password.change だけを記録し、session.revoke を足さない**
		// （6.6 と同じ判断）。1つの操作が2行になると、監査ログの読み手が
		// 二重に数える。失効した本数は detail に入れる。
		//
		// **平文は detail に入れない。** audit_log は長期保存される記録である。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.PasswordChange,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   p.ActorID,
			Detail: map[string]any{
				"revoked_sessions": revoked,
			},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// validateChangePassword は 4.3 の入力を検証する。
//
// **新しいパスワードのポリシーは auth.ValidatePassword が正本**（Design.md 6.3
// の12文字）。6.2 の作成時と同じ関数を通し、経路によって通る値が変わらない
// ようにする。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateChangePassword(req changePasswordRequest) *apierr.Error {
	var details []apierr.Detail

	if req.CurrentPassword == "" {
		details = append(details, apierr.Detail{
			Field: "current_password", Code: "required",
			Message: "現在のパスワードを入力してください",
		})
	}

	switch {
	case req.NewPassword == "":
		details = append(details, apierr.Detail{
			Field: "new_password", Code: "required",
			Message: "新しいパスワードを入力してください",
		})
	default:
		if err := auth.ValidatePassword(req.NewPassword); err != nil {
			details = append(details, apierr.Detail{
				Field: "new_password", Code: "too_short",
				Message: fmt.Sprintf("パスワードは%d文字以上で入力してください", auth.MinPasswordLength),
			})
		}
	}

	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}
