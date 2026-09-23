// POST /api/v1/auth/login（ApiDesign.md 3.1）。
//
// 実装は Design.md 6.2.1 の8手順をそのまま順に行う。
//
//  1. { email, password } を受ける
//  2. app_user を email で検索（citext のため大文字小文字を区別しない）
//  3. user_identity を (provider_key='local', subject=email) で検索
//  4. local_credential.locked_until を確認 → ロック中なら 423
//  5. Argon2id で password_hash を検証
//  6. access_token を発行（token_type='session'）
//  7. Set-Cookie
//  8. audit_log('login.success') を記録
//
// 手順2〜3は FindLocalLoginByEmail が1文で行う（queries/auth.sql）。
//
// **手順5と6のあいだに第2要素の分岐がある**（pb-103。Design.md 6.7.4）。
// 確定済みの認証器があれば、セッションを出さずに挑戦を返し、手順6〜8 を
// 次の要求（POST /auth/login/mfa。login_mfa.go）へ持ち越す。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ログイン失敗によるロック（Design.md 6.3「5回連続で15分ロック」）。
const (
	maxFailedAttempts = 5
	lockDuration      = 15 * time.Minute
)

// maxEmailLength は受け付けるメールアドレスの最大長（RFC 5321 §4.5.3.1.3）。
// 監査ログの detail にそのまま載せるため、上限を設けてから記録する。
const maxEmailLength = 254

// maxRequestBodyBytes は JSON 本文の読み取り上限。
//
// 設計文書に規定は無い。認証前に叩けるエンドポイントで無制限に読むと、
// 1本のリクエストでメモリを食い潰せてしまうため実装側の安全弁として置く。
const maxRequestBodyBytes = 1 << 20 // 1MiB

// loginRequest は 3.1 のリクエスト本体。
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login は POST /api/v1/auth/login を処理する。
func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	email, e := validateLoginRequest(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := audit.FromRequest(r)

	// 手順2〜3。
	row, err := h.q.FindLocalLoginByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 応答時間を揃えてから 401（Design.md 6.2.1 末尾）。
			auth.VerifyAgainstDummy(req.Password)
			h.recordLoginFailure(r.Context(), rec, email, "unknown_email")
			apierr.WriteCode(w, r, apierr.InvalidCredentials)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// 誰の操作かが判明したので、以降の監査記録にアクターを載せる。
	rec = rec.WithActor(row.ActorID, auth.ActorKindUser,
		(&auth.Principal{DisplayName: row.DisplayName, Email: row.Email}).AuditLabel())

	now := time.Now()

	// 手順4。ロック中は照合すらしない。
	if row.LockedUntil.Valid && row.LockedUntil.Time.After(now) {
		h.recordLoginFailure(r.Context(), rec, email, "locked")
		apierr.Write(w, r, apierr.New(apierr.AccountLocked).
			WithRetryAfter(retryAfterSec(row.LockedUntil.Time, now)))
		return
	}

	// ロック期限が切れていたら失敗回数を 0 起点に戻す。
	// Design.md 6.3 の「5回**連続**で」を満たすため。戻さないと、ロックが
	// 明けた直後の1回の失敗で再びロックされ続ける。
	attempts := int(row.FailedAttempts)
	if row.LockedUntil.Valid && !row.LockedUntil.Time.After(now) {
		attempts = 0
	}

	// 手順5。
	ok, err := auth.VerifyPassword(req.Password, row.PasswordHash)
	if err != nil {
		// PHC 文字列が壊れている。401 に倒すと運用者が原因に気づけない。
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if !ok {
		h.handleWrongPassword(w, r, rec, row, attempts, now, email)
		return
	}

	// アカウント無効。**認証失敗と区別しない**（ApiDesign.md 3.1）。
	// 照合の後に見るのは、有効なアカウントと応答時間を揃えるためである。
	if !row.IsActive {
		h.recordLoginFailure(r.Context(), rec, email, "inactive_actor")
		apierr.WriteCode(w, r, apierr.InvalidCredentials)
		return
	}

	// 手順5.5。**第2要素が登録されていれば、ここで止める**（Design.md 6.7.4）。
	// セッションを出してから確認する形にすると、第2要素が飾りになる。
	count, err := h.q.CountConfirmedMfaCredentials(r.Context(), row.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("第2要素の件数を読めない: %w", err)))
		return
	}
	if count > 0 {
		// **失敗回数のリセットはここでも行う**（手順5「成功 → failed_attempts=0」）。
		// パスワードは正しかったので、第2要素で止まってもロックの数は戻す。
		if err := h.q.ResetLoginFailure(r.Context(), row.IdentityID); err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
		h.rehashIfStale(r.Context(), row, req.Password)
		h.startMFAChallenge(w, r, rec, row.ActorID)
		return
	}

	h.completeLogin(w, r, rec, row, req.Password)
}

// handleWrongPassword は照合に失敗したときの後始末を行う（Design.md 6.2.1 手順5）。
//
//	失敗 → failed_attempts++ 、閾値超過で locked_until を設定
//	       audit_log('login.failure') を記録し 401 を返す
//
// **ロックを設定した回の応答は 423 にする。** その時点で既にロック済みであり、
// 401 を返すと利用者は理由が分からないまま次の試行でロックに当たる。
func (h *handler) handleWrongPassword(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder,
	row gen.FindLocalLoginByEmailRow, attempts int, now time.Time, email string,
) {
	attempts++

	var lockedUntil pgtype.Timestamptz
	if attempts >= maxFailedAttempts {
		lockedUntil = pgtype.Timestamptz{Time: now.Add(lockDuration), Valid: true}
	}

	if err := h.q.RecordLoginFailure(r.Context(), gen.RecordLoginFailureParams{
		FailedAttempts: int32(attempts),
		LockedUntil:    lockedUntil,
		IdentityID:     row.IdentityID,
	}); err != nil {
		// 記録できないまま通すと総当たりが無制限になる。落とす側に倒す。
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	reason := "wrong_password"
	if lockedUntil.Valid {
		reason = "wrong_password_locked"
	}
	h.recordLoginFailure(r.Context(), rec, email, reason)

	if lockedUntil.Valid {
		apierr.Write(w, r, apierr.New(apierr.AccountLocked).
			WithRetryAfter(retryAfterSec(lockedUntil.Time, now)))
		return
	}
	apierr.WriteCode(w, r, apierr.InvalidCredentials)
}

// completeLogin は照合に成功したあとの手順5後半〜8を行う。
//
// password は再ハッシュにのみ使う。**この値をログにも監査記録にも渡さない。**
func (h *handler) completeLogin(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder,
	row gen.FindLocalLoginByEmailRow, password string,
) {
	ctx := r.Context()

	// 手順5「成功 → failed_attempts=0」。
	if err := h.q.ResetLoginFailure(ctx, row.IdentityID); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	h.rehashIfStale(ctx, row, password)

	h.finishLogin(w, r, rec, profile{
		ActorID:            row.ActorID,
		DisplayName:        row.DisplayName,
		Email:              row.Email,
		SystemRole:         row.SystemRole,
		Locale:             row.Locale,
		Timezone:           row.Timezone,
		Theme:              row.Theme,
		Hue:                row.Hue,
		MustChangePassword: row.MustChange,
	}, map[string]any{"provider_key": "local"})
}

// finishLogin は 3.1 の手順6〜8 を行う（pb-115）。
//
// **照合を終えた3経路が、すべてここを通る**——パスワード（completeLogin）・
// 第2要素（completeMFALogin）・パスキー（completePasskeyLogin）。並びは
// セッションの発行 → last_login_at → login.success の監査 → 実効権限の計算と
// キャッシュ → 応答と Cookie で、**経路ごとに違うのは利用者の属性（p）と監査の
// detail だけ**である。以前は3か所に写っており（pb-104 で3か所目）、監査や
// セッションの発行を変えるたびに直し忘れた経路だけ記録が食い違いうる形だった。
//
// **p.Kind は見ない。** ログインするのは常に人なので、ここで user に決める。
func (h *handler) finishLogin(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder,
	p profile, detail map[string]any,
) {
	ctx := r.Context()
	p.Kind = auth.ActorKindUser

	// 手順6。
	session, err := h.issueSession(ctx, p.ActorID, r.UserAgent())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// last_login_at は付随情報のため、失敗しても認証は通す。
	if err := h.q.TouchLastLoginAt(ctx, p.ActorID); err != nil {
		slog.WarnContext(ctx, "last_login_at を更新できなかった",
			slog.String("request_id", apierr.RequestIDFromContext(ctx)),
			slog.String("actor_id", p.ActorID),
			slog.String("cause", err.Error()))
	}

	// 手順8。発行したトークンを token_id に載せる。
	rec.WithToken(session.TokenID).RecordOrLog(ctx, h.q, audit.Entry{
		Action:     audit.LoginSuccess,
		Result:     audit.Success,
		TargetType: "access_token",
		TargetID:   session.TokenID,
		Detail:     detail,
	})

	// Design.md 6.4.5「ログインごとに実効権限を計算し、セッションにキャッシュする」。
	// 発行したばかりのトークンにキャッシュは無いので、必ずDBから計算する。
	systemPerms, err := middleware.ComputeSystemPermissions(ctx, h.q, p.SystemRole, nil)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	middleware.SaveSystemPermissionCache(ctx, h.q, session.TokenID, p.SystemRole, systemPerms)

	// ログイン応答は GET /me と同じ内容を返す（ApiDesign.md 3.1）。
	// 新しいセッションは scopes を持たないため、縮小は起きない。
	view, err := h.buildSessionView(ctx, h.q, p, systemPerms, nil, &session.ExpiresAt)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// 手順7。本文より先に Set-Cookie を積む。
	h.setSessionCookies(w, session)
	WriteJSON(w, http.StatusOK, view)
}

// rehashIfStale は保存済みハッシュが旧世代なら作り直す
// （Design.md 6.2.1 手順5「ハッシュパラメータが旧世代なら再ハッシュして更新」）。
//
// 認証は既に成立しているため、失敗してもログインは通す。次回のログインで
// もう一度試みられる。
func (h *handler) rehashIfStale(ctx context.Context, row gen.FindLocalLoginByEmailRow, password string) {
	if !auth.NeedsRehash(row.PasswordHash) {
		return
	}

	phc, err := auth.HashPassword(password)
	if err != nil {
		slog.WarnContext(ctx, "パスワードを再ハッシュできなかった",
			slog.String("request_id", apierr.RequestIDFromContext(ctx)),
			slog.String("actor_id", row.ActorID),
			slog.String("cause", err.Error()))
		return
	}
	if err := h.q.RehashPassword(ctx, gen.RehashPasswordParams{
		PasswordHash: phc, IdentityID: row.IdentityID,
	}); err != nil {
		slog.WarnContext(ctx, "再ハッシュしたパスワードを保存できなかった",
			slog.String("request_id", apierr.RequestIDFromContext(ctx)),
			slog.String("actor_id", row.ActorID),
			slog.String("cause", err.Error()))
	}
}

// recordLoginFailure は login.failure を1件残す。
//
// 業務トランザクションを持たない認証イベントのため RecordOrLog を使う
// （記録に失敗してもログインの応答は返す。audit パッケージの説明を参照）。
// detail に試行されたメールを残すのは、総当たりの調査に要るためである。
// audit_log は管理者だけが読める（DbDesign.md 6.8）。
func (h *handler) recordLoginFailure(ctx context.Context, rec *audit.Recorder, email, reason string) {
	rec.RecordOrLog(ctx, h.q, audit.Entry{
		Action: audit.LoginFailure,
		Result: audit.Failure,
		Detail: map[string]any{"email": email, "reason": reason},
	})
}

// retryAfterSec はロック解除までの秒数を返す（ApiDesign.md 3.1 の retry_after_sec）。
// 端数は切り上げる。0 を返すと「今すぐ再試行してよい」と読めてしまうため。
func retryAfterSec(until, now time.Time) int {
	d := until.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// validateLoginRequest は 3.1 の入力を検査し、検索に使うメールアドレスを返す。
//
// 形式の誤りは 422 validation_failed とし、資格情報の誤り（401）と区別する。
// **アカウントの存在は漏らさない。** ここで見るのは入力の形だけである。
//
// 返すのは mail.ParseAddress が取り出したアドレス部である。pb admin create も
// 同じ関数を通した値を保存しているため（cmd/pb/admin_create.go）、
// 表示名つきの入力（"田中" <tanaka@example.com>）でも同じ表記で突き合わせられる。
// 大文字小文字は変換しない。照合は app_user.email の citext に委ねる
// （Design.md 6.2.1）。
func validateLoginRequest(req loginRequest) (string, *apierr.Error) {
	var details []apierr.Detail
	var email string

	switch {
	case req.Email == "":
		details = append(details, apierr.Detail{
			Field: "email", Code: "required", Message: "メールアドレスを入力してください",
		})
	case len(req.Email) > maxEmailLength:
		details = append(details, apierr.Detail{
			Field: "email", Code: "too_long", Message: "メールアドレスが長すぎます",
		})
	default:
		addr, err := mail.ParseAddress(req.Email)
		if err != nil {
			details = append(details, apierr.Detail{
				Field: "email", Code: "invalid", Message: "メールアドレスの形式が正しくありません",
			})
		} else {
			email = addr.Address
		}
	}

	if req.Password == "" {
		details = append(details, apierr.Detail{
			Field: "password", Code: "required", Message: "パスワードを入力してください",
		})
	}

	if len(details) > 0 {
		return "", apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return email, nil
}

// decodeJSON は JSON 本文を読む。形式の誤りは 400 bad_request とする。
func decodeJSON(r *http.Request, dst any) *apierr.Error {
	body := io.LimitReader(r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		return apierr.New(apierr.BadRequest).
			WithCause(fmt.Errorf("リクエスト本文を JSON として読めない: %w", err))
	}
	return nil
}
