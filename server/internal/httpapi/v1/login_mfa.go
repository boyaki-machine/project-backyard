// ログインの第2要素（ApiDesign.md 3.1 の後半・3.4、Design.md 6.7.4）。
//
//	POST /api/v1/auth/login       → 確定済みの認証器があれば挑戦を返す
//	POST /api/v1/auth/login/mfa   → コードを照合してセッションを発行する
//
// **挑戦を access_token に置かない。** 認証ミドルウェアは token_hash で引いた行を
// token_type で絞らずアクターを載せるため（middleware/auth.go）、access_token に
// 中間状態を置くと挑戦トークンがそのまま API 全体を通る資格情報になる。
// 置き場は mfa_login_challenge であり、**その経路が構造上存在しない。**
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/mfa"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

const (
	// mfaChallengeTTL は挑戦の有効期間（Design.md 6.7.4）。
	//
	// **認証アプリを開いて6桁を写す時間である。** 長くすると、パスワードだけが
	// 通った状態がそれだけ残る。
	mfaChallengeTTL = 5 * time.Minute

	// maxMFAChallengeAttempts は1つの挑戦で許す試行回数。
	//
	// **アカウントをロックしない代わりの抑止である**（Design.md 6.7.4）。
	// 超えたら挑戦を捨て、メールアドレスからやり直させる。
	maxMFAChallengeAttempts = 5
)

// mfaMethod は 3.1 の methods に載る値。
const (
	mfaMethodTOTP         = "totp"
	mfaMethodRecoveryCode = "recovery_code"
)

// mfaChallengeView は 3.1 が返す挑戦。**Session とは mfa_required の有無で
// 見分ける**（こちらに actor は無い）。
type mfaChallengeView struct {
	MFARequired bool     `json:"mfa_required"`
	MFAToken    string   `json:"mfa_token"`
	Methods     []string `json:"methods"`
	ExpiresAt   Time     `json:"expires_at"`
}

// startMFAChallenge は挑戦を1件作って応答する（Design.md 6.7.4 の手順3）。
//
// **セッションは発行しない。** ここで Cookie を出すと第2要素が飾りになる。
func (h *handler) startMFAChallenge(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder, userID string,
) {
	ctx := r.Context()

	plaintext, err := auth.NewToken(auth.MFAChallengePrefix)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	expiresAt := time.Now().Add(mfaChallengeTTL)
	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **古い挑戦を先に片付ける。** 期限切れの行が積むのを、専用のバッチでは
		// なく次のログインに相乗りさせて防ぐ（1人あたり数行で済む）。
		if err := q.DeleteMfaLoginChallengesForUser(ctx, userID); err != nil {
			return fmt.Errorf("古い挑戦を片付けられない: %w", err)
		}
		return q.CreateMfaLoginChallenge(ctx, gen.CreateMfaLoginChallengeParams{
			ID:        ulidgen.New(),
			UserID:    userID,
			TokenHash: auth.HashToken(plaintext),
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
		})
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("第2要素の挑戦を作れない: %w", err)))
		return
	}

	methods, err := h.mfaMethods(ctx, userID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// **パスワードが通ったことは記録に残す。** ここで止まった要求が
	// login.success として数えられないので、成否の勘定が合うようにする。
	rec.RecordOrLog(ctx, h.q, audit.Entry{
		Action:     audit.LoginSuccess,
		Result:     audit.Success,
		TargetType: "app_user",
		TargetID:   userID,
		Detail:     map[string]any{"provider_key": "local", "mfa_required": true},
	})

	WriteJSON(w, http.StatusOK, mfaChallengeView{
		MFARequired: true,
		MFAToken:    plaintext,
		Methods:     methods,
		ExpiresAt:   Time(expiresAt),
	})
}

// mfaMethods は「この挑戦で使える手段」を組む（ApiDesign.md 3.1）。
//
// **未使用のリカバリコードが1本も無ければ載せない。** 出せない選択肢を画面に
// 出さないためである。
func (h *handler) mfaMethods(ctx context.Context, userID string) ([]string, error) {
	methods := []string{mfaMethodTOTP}

	remaining, err := h.q.CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("リカバリコードの残数を読めない: %w", err)
	}
	if remaining > 0 {
		methods = append(methods, mfaMethodRecoveryCode)
	}
	return methods, nil
}

// ── POST /api/v1/auth/login/mfa（3.4）──────────────────────

type loginMFARequest struct {
	MFAToken     string `json:"mfa_token"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

// loginMFA は第2要素を照合し、成功すれば 3.1 の手順6〜8 を行う。
func (h *handler) loginMFA(w http.ResponseWriter, r *http.Request) {
	var req loginMFARequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateLoginMFARequest(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	row, err := h.q.FindMfaLoginChallenge(ctx, auth.HashToken(req.MFAToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.recordMFAFailure(ctx, rec, "", "unknown_challenge")
			apierr.Write(w, r, errMFAChallengeGone.WithCause(
				errors.New("該当する挑戦が無い")))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	rec = rec.WithActor(row.UserID, auth.ActorKindUser,
		(&auth.Principal{DisplayName: row.DisplayName, Email: row.Email}).AuditLabel())

	// **有効性はここで判定する。** クエリで絞らないのは、「無い」と「期限切れ」を
	// サーバログで区別するためである（access_token と同じ考え方）。
	if reason := challengeInvalidReason(row, time.Now()); reason != "" {
		h.recordMFAFailure(ctx, rec, row.UserID, reason)
		apierr.Write(w, r, errMFAChallengeGone.WithCause(errors.New(reason)))
		return
	}

	ok, credentialID, e := h.verifySecondFactor(ctx, row.UserID, req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if !ok {
		h.handleMFAMismatch(w, r, rec, row)
		return
	}

	// **挑戦を消費する。** consumed_at IS NULL を条件にしてあるので、
	// 同じ挑戦から2本のセッションは出ない。
	n, err := h.q.ConsumeMfaLoginChallenge(ctx, row.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("挑戦を消費できない: %w", err)))
		return
	}
	if n == 0 {
		h.recordMFAFailure(ctx, rec, row.UserID, "already_consumed")
		apierr.Write(w, r, errMFAChallengeGone.WithCause(
			errors.New("挑戦が既に消費されている")))
		return
	}

	h.completeMFALogin(w, r, rec, row, credentialID)
}

// errMFAChallengeGone は挑戦が使えないときの応答。
//
// **新しいエラーコードを作らない**（ApiDesign.md 3.4）。利用者が取る行動は
// 「もう一度ログインする」で共通するため、invalid_credentials に文言を被せる。
var errMFAChallengeGone = apierr.New(apierr.InvalidCredentials).
	WithMessage("確認の有効期限が切れました。もう一度ログインしてください")

// challengeInvalidReason は挑戦が使えない理由を返す。使えるなら空文字。
// **応答には出さない。** サーバログへ出す文言である。
func challengeInvalidReason(row gen.FindMfaLoginChallengeRow, now time.Time) string {
	if row.ConsumedAt.Valid {
		return "挑戦が既に消費されている（consumed_at）"
	}
	if !row.ExpiresAt.Time.After(now) {
		return "挑戦の有効期限が切れている（expires_at）"
	}
	if int(row.Attempts) >= maxMFAChallengeAttempts {
		return "挑戦の試行回数が上限に達している（attempts）"
	}
	if !row.IsActive {
		// ApiDesign.md 3.1 に揃え、アカウント無効も認証失敗と区別しない。
		return "アクターが無効化されている（actor.is_active=false）"
	}
	return ""
}

// verifySecondFactor は TOTP かリカバリコードを照合する。
//
// 戻り値の credentialID は、通った認証器の id（リカバリコードなら空）。
func (h *handler) verifySecondFactor(
	ctx context.Context, userID string, req loginMFARequest,
) (bool, string, *apierr.Error) {
	if req.RecoveryCode != "" {
		n, err := h.q.ConsumeRecoveryCode(ctx, gen.ConsumeRecoveryCodeParams{
			UserID:   userID,
			CodeHash: auth.HashToken(mfa.NormalizeRecoveryCode(req.RecoveryCode)),
		})
		if err != nil {
			return false, "", apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("リカバリコードを消費できない: %w", err))
		}
		// **0行は「無い」か「既に使った」。** どちらも応答は同じである。
		return n > 0, "", nil
	}

	rows, err := h.q.ListConfirmedMfaSecrets(ctx, userID)
	if err != nil {
		return false, "", apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器を読めない: %w", err))
	}
	key, _, e := h.secretKey(ctx)
	if e != nil {
		return false, "", e
	}

	now := time.Now()
	// **確定済みを順に試す。** どの認証器で通るかは分からず、1人5件までなので
	// 総当たりの費用は小さい（Design.md 6.7.4）。
	for _, row := range rows {
		secret, err := tlscert.Open(key, row.Secret, row.SecretNonce)
		if err != nil {
			// **1件が壊れていても他を試す。** 鍵を入れ替えた直後などに
			// 復号できない行が混ざりうるが、それで全部を通さなくする必要は無い。
			continue
		}
		step, err := mfa.Verify(secret, req.Code, now, row.LastUsedStep.Int64)
		if err != nil {
			continue
		}
		if err := h.q.TouchMfaCredentialUsed(ctx, gen.TouchMfaCredentialUsedParams{
			ID: row.ID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true},
		}); err != nil {
			return false, "", apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("使った刻みを保存できない: %w", err))
		}
		return true, row.ID, nil
	}
	return false, "", nil
}

// handleMFAMismatch は照合に失敗したときの後始末（Design.md 6.7.4）。
//
// **local_credential.failed_attempts は増やさない。** パスワードは既に正しく、
// ここで数えるとコードを打ち間違えた本人がアカウントごとロックされる。
func (h *handler) handleMFAMismatch(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder, row gen.FindMfaLoginChallengeRow,
) {
	ctx := r.Context()

	next, err := h.q.RecordMfaChallengeFailure(ctx, row.ID)
	if err != nil {
		// 数えられないまま通さない（login.go と同じ判断）。
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("第2要素の失敗を記録できない: %w", err)))
		return
	}

	if int(next) >= maxMFAChallengeAttempts {
		h.recordMFAFailure(ctx, rec, row.UserID, "attempts_exceeded")
		apierr.Write(w, r, errMFAChallengeGone.WithCause(
			errors.New("試行回数が上限に達した")))
		return
	}

	h.recordMFAFailure(ctx, rec, row.UserID, "wrong_code")
	apierr.Write(w, r, apierr.New(apierr.InvalidCredentials).
		WithMessage("確認コードが正しくありません"))
}

// completeMFALogin は 3.1 の手順6〜8 を行う。
//
// **パスワードだけで通ったときと同じ関数を通る**（finishLogin）。
// ここで決めるのは、利用者の属性と監査の detail だけである。
func (h *handler) completeMFALogin(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder,
	row gen.FindMfaLoginChallengeRow, credentialID string,
) {
	method := mfaMethodTOTP
	if credentialID == "" {
		method = mfaMethodRecoveryCode
	}
	h.finishLogin(w, r, rec, profile{
		ActorID:            row.UserID,
		DisplayName:        row.DisplayName,
		Email:              row.Email,
		SystemRole:         row.SystemRole,
		Locale:             row.Locale,
		Timezone:           row.Timezone,
		Theme:              row.Theme,
		Hue:                row.Hue,
		MustChangePassword: row.MustChange.Bool,
	},
		// **第2要素で通ったことを残す**（ApiDesign.md 3.4）。
		map[string]any{"provider_key": "local", "mfa": method})
}

// recordMFAFailure は login.mfa_failure を1件残す（ApiDesign.md 4.6.6）。
//
// **login.failure と分けてある。** パスワードは通っているので、総当たりの調査で
// 見る対象が違う。
func (h *handler) recordMFAFailure(ctx context.Context, rec *audit.Recorder, userID, reason string) {
	detail := map[string]any{"reason": reason}
	rec.RecordOrLog(ctx, h.q, audit.Entry{
		Action:     audit.LoginMFAFailure,
		Result:     audit.Failure,
		TargetType: targetTypeOrEmpty(userID),
		TargetID:   userID,
		Detail:     detail,
	})
}

func targetTypeOrEmpty(userID string) string {
	if userID == "" {
		return ""
	}
	return "app_user"
}

// validateLoginMFARequest は 3.4 の入力を検査する。
//
// **code と recovery_code はどちらか一方だけ。** 両方来たときにどちらを見るかを
// 決めずに通すと、片方が空文字のときの挙動が呼び出し側ごとに変わる。
func validateLoginMFARequest(req loginMFARequest) *apierr.Error {
	var details []apierr.Detail

	if req.MFAToken == "" {
		details = append(details, apierr.Detail{
			Field: "mfa_token", Code: "required",
			Message: "確認の情報が失われました。もう一度ログインしてください",
		})
	}
	switch {
	case req.Code == "" && req.RecoveryCode == "":
		details = append(details, apierr.Detail{
			Field: "code", Code: "required", Message: "確認コードを入力してください",
		})
	case req.Code != "" && req.RecoveryCode != "":
		details = append(details, apierr.Detail{
			Field: "code", Code: "invalid",
			Message: "確認コードとリカバリコードは、どちらか一方を入力してください",
		})
	}

	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}
