// ユーザー管理API（ApiDesign.md 6章）のうち、資格情報とセッション。
//
//	POST /api/v1/admin/users/:id/password-reset    6.6
//	POST /api/v1/admin/users/:id/sessions/revoke   6.7
//
// **どちらも当人を締め出す操作である。** 6.6 はパスワードを差し替えたうえで
// 全セッションを失効し、6.7 は失効だけを行う。
package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// passwordResetModeGenerate は 6.6 の mode。
//
// **Phase 1 は generate だけを受ける**（手順13a の判断）。6.6 の応答は
// generated_password しか持たず、管理者が手で決めた値を返す意味が無い。
// 画面（GuiDesign.md 5.6.2）の導線も [リセット] の1つだけである。
// 必要になれば 6.2 と同じ password_mode / password を足す。
const passwordResetModeGenerate = "generate"

// passwordResetRequest は 6.6 のリクエスト本体。
//
// **本文なしの POST も受ける。** どちらの項目にも既定があり
// （mode=generate / must_change_password=true）、`{}` と本文なしを
// 区別する理由が無いためである。
type passwordResetRequest struct {
	Mode               *string `json:"mode"`
	MustChangePassword *bool   `json:"must_change_password"`
}

// passwordResetResponse は 6.6 の 200 応答。
//
// **generated_password はこの応答でのみ返る**（6.2 の作成時と同じ扱い）。
// 監査ログにも残さない。再表示はできない。
type passwordResetResponse struct {
	GeneratedPassword string `json:"generated_password"`
}

// resetUserPassword は POST /api/v1/admin/users/:id/password-reset を
// 処理する（ApiDesign.md 6.6）。
//
// **local_credential を持たないユーザー（IdP のみ、Phase 3）は 409 conflict**。
func (h *handler) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.adminUserContext(w, r, "POST /admin/users/{id}/password-reset")
	if !ok {
		return
	}

	var req passwordResetRequest
	if e := decodeOptionalJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validatePasswordReset(req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	mustChange := true
	if req.MustChangePassword != nil {
		mustChange = *req.MustChangePassword
	}

	// **平文はここでしか作らない。** 生成に失敗したら何も変えずに 500 で
	// 返す（ハッシュ化まで済ませてから DB を触る）。
	password, err := auth.GeneratePassword()
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスワードをハッシュ化できない: %w", err)))
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **対象が 6章の扱うユーザーであることを先に確かめる。**
		// これを飛ばすと、存在しない ID に対して「資格情報が無い」という
		// 409 を返してしまい、404 と区別がつく（ID の総当たりの手がかりになる）。
		cur, err := q.GetAdminUser(ctx, id)
		if err != nil {
			return err
		}

		identityID, err := q.FindLocalCredentialByActor(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(apierr.Conflict).
				WithMessage("このユーザーはパスワード認証を使っていないため、リセットできません")
		} else if err != nil {
			return fmt.Errorf("資格情報を引けない: %w", err)
		}

		if err := q.ResetLocalCredential(ctx, gen.ResetLocalCredentialParams{
			PasswordHash: hash,
			MustChange:   mustChange,
			IdentityID:   identityID,
		}); err != nil {
			return fmt.Errorf("資格情報を更新できない: %w", err)
		}

		// **全セッションを失効する**（6.6）。パスワードを変えても、既に
		// 入っている端末が残っていては「乗っ取られた疑いがあるので直す」
		// という用途を果たさない。
		revoked, err := q.RevokeActorSessions(ctx, id)
		if err != nil {
			return fmt.Errorf("セッションを失効できない: %w", err)
		}

		// **password.reset だけを記録し、session.revoke は書かない**
		// （手順13a の判断）。1つの操作が2行になると、監査ログの読み手が
		// 二重に数える。失効した本数は detail に入れる。
		//
		// **平文は detail に入れない**（6.2 の作成と同じ。audit_log は
		// 長期保存される記録であり、残ると「この応答でのみ返る」が崩れる）。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.PasswordReset,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   id,
			Detail: map[string]any{
				"email":                cur.Email,
				"must_change_password": mustChange,
				"revoked_sessions":     revoked,
			},
		})
	})
	if err != nil {
		writeUserUpdateError(w, r, id, nil, err)
		return
	}

	WriteJSON(w, http.StatusOK, passwordResetResponse{GeneratedPassword: password})
}

// revokeUserSessions は POST /api/v1/admin/users/:id/sessions/revoke を
// 処理する（ApiDesign.md 6.7）。
//
// **エージェントのトークンにも適用される**（6.7）ため、token_type で
// 絞らずアクターの全トークンを失効させる（RevokeActorSessions）。
// ただし Phase 1 に到達できるのは kind='user' だけである（6.3 の判断）。
//
// **冪等である。** 既に1本も無くても 204 を返す。何度呼んでも
// 「入れない」状態に収束する。
func (h *handler) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.adminUserContext(w, r, "POST /admin/users/{id}/sessions/revoke")
	if !ok {
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		cur, err := q.GetAdminUser(ctx, id)
		if err != nil {
			return err
		}

		revoked, err := q.RevokeActorSessions(ctx, id)
		if err != nil {
			return fmt.Errorf("セッションを失効できない: %w", err)
		}

		// 0本でも記録する。**「切ろうとした」こと自体が監査の対象**であり、
		// 結果が0本かどうかは detail で読めばよい（archive の冪等な空振りとは
		// 違い、こちらは管理者が明示的に押した操作である）。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.SessionRevoke,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   id,
			Detail: map[string]any{
				"email":            cur.Email,
				"revoked_sessions": revoked,
			},
		})
	})
	if err != nil {
		writeUserUpdateError(w, r, id, nil, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// decodeOptionalJSON は本文を読むが、**空の本文を誤りとしない**。
//
// 6.6 は2項目とも既定を持つため（mode=generate / must_change_password=true）、
// `{}` と本文なしを区別する理由が無い。decodeJSON はどちらも通せない——
// 本文が無いと Decode が io.EOF を返し、400 になってしまう。
//
// **Content-Length では判定しない。** chunked 転送では -1 になり、
// 「本文が無い」と「長さが分からない」を取り違える。
func decodeOptionalJSON(r *http.Request, dst any) *apierr.Error {
	body := io.LimitReader(r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return apierr.New(apierr.BadRequest).
			WithCause(fmt.Errorf("リクエスト本文を JSON として読めない: %w", err))
	}
	return nil
}

// validatePasswordReset は 6.6 の mode を検証する。
func validatePasswordReset(req passwordResetRequest) *apierr.Error {
	if req.Mode == nil || *req.Mode == passwordResetModeGenerate {
		return nil
	}
	return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "mode", Code: "invalid",
		Message: "mode は generate で指定してください",
	})
}
