// 自分自身に関するAPI（ApiDesign.md 4章）のうち、第2要素の管理。
//
//	GET    /api/v1/me/mfa                     4.6.1
//	POST   /api/v1/me/mfa/totp                4.6.2
//	POST   /api/v1/me/mfa/totp/{id}/confirm   4.6.3
//	DELETE /api/v1/me/mfa/totp/{id}           4.6.4
//	POST   /api/v1/me/mfa/recovery-codes      4.6.5
//
// **確定していない登録（confirmed_at IS NULL）は認証の要素として数えない。**
// 一覧にも出さず、上限にも数えず、ログインの分岐にも効かない（DbDesign.md 6.18）。
// 条件はクエリ側に閉じ込めてあり、ハンドラが毎回書くことはしない。
//
// **共有秘密は app_secret の鍵で封じて保存する**（Design.md 6.7.3）。鍵の解決は
// TLS 証明書と共通で h.secretKey に委ねる——MFA のために別の鍵を持たない。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
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
	// mfaCredentialNameMaxLen は name の上限。
	// **user_mfa_credential.name の CHECK（1〜60）と同じ値**である。
	mfaCredentialNameMaxLen = 60

	// maxMFACredentialsPerUser は1人あたりの認証器の上限（ApiDesign.md 4.6.2）。
	//
	// **/me/tokens の5本に揃えた。** 端末を複数持つ人が登録できる必要がある一方、
	// 増えるほど「どの端末が自分のアカウントを開けるか」を本人が把握できなくなる。
	// **数えるのは確定済みだけ**——未確定は要素ではないので、上限を占める理由が無い。
	maxMFACredentialsPerUser = 5

	// maxConfirmAttempts は登録時の照合を何回まで許すか（ApiDesign.md 4.6.3）。
	//
	// **超えたら未確定の行を捨てる。** 6桁を総当たりする相手に、同じ共有秘密で
	// 何度も試させない。やり直しは新しい秘密から始まる。
	maxConfirmAttempts = 5

	// mfaKindTOTP は user_mfa_credential.kind の値（CHECK は 'totp' のみ）。
	mfaKindTOTP = "totp"
)

// ── 応答の形（ApiDesign.md 4.6.1）──────────────────────────

// totpCredentialView は確定済みの認証器1件。**共有秘密を持たない。**
type totpCredentialView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  Time   `json:"created_at"`
	LastUsedAt *Time  `json:"last_used_at"`
}

// recoveryCodeStatusView は残数。**平文を持たない。**
type recoveryCodeStatusView struct {
	Remaining   int  `json:"remaining"`
	GeneratedAt Time `json:"generated_at"`
}

// mfaOverviewView は 4.6.1 の応答。
//
// **RecoveryCodes はポインタである。** null（1本も作っていない）と
// remaining: 0（作って全部使った）は別の状態であり、画面が出す操作も違う。
type mfaOverviewView struct {
	TOTP          []totpCredentialView    `json:"totp"`
	RecoveryCodes *recoveryCodeStatusView `json:"recovery_codes"`
}

// startedTOTPView は 4.6.2 の応答。**secret と otpauth_uri が出る唯一の形**である。
type startedTOTPView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Secret     string `json:"secret"`
	OtpauthURI string `json:"otpauth_uri"`
	Digits     int    `json:"digits"`
	PeriodSec  int    `json:"period_sec"`
}

// confirmedTOTPView は 4.6.3 の応答。
//
// **RecoveryCodes は初回だけ入る。** 2件目以降で返すと、既に持っているコードが
// 無効になったと読めてしまう。
type confirmedTOTPView struct {
	Credential    totpCredentialView `json:"credential"`
	RecoveryCodes []string           `json:"recovery_codes,omitempty"`
}

// recoveryCodeListView は 4.6.5 の応答。
type recoveryCodeListView struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// ── GET /api/v1/me/mfa（4.6.1）─────────────────────────────

func (h *handler) getMyMfa(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "GET /me/mfa")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rows, err := h.q.ListConfirmedMfaCredentials(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器を読めない: %w", err)))
		return
	}

	items := make([]totpCredentialView, 0, len(rows))
	for _, row := range rows {
		items = append(items, totpCredentialView{
			ID:         row.ID,
			Name:       row.Name,
			CreatedAt:  Time(row.CreatedAt.Time),
			LastUsedAt: apiTimestamptz(row.LastUsedAt),
		})
	}

	status, e := h.recoveryCodeStatus(r.Context(), p.ActorID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	WriteJSON(w, http.StatusOK, mfaOverviewView{TOTP: items, RecoveryCodes: status})
}

// recoveryCodeStatus は残数を引く。**1本も無ければ nil を返す**（4.6.1）。
func (h *handler) recoveryCodeStatus(ctx context.Context, userID string) (*recoveryCodeStatusView, *apierr.Error) {
	row, err := h.q.GetRecoveryCodeStatus(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 行が返らないのは「1本も作っていない」であり、誤りではない。
		return nil, nil
	}
	if err != nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("リカバリコードの残数を読めない: %w", err))
	}
	return &recoveryCodeStatusView{
		Remaining:   int(row.Remaining),
		GeneratedAt: Time(row.GeneratedAt.Time),
	}, nil
}

// ── POST /api/v1/me/mfa/totp（4.6.2）───────────────────────

type startTOTPRequest struct {
	Name string `json:"name"`
}

// startMyTotp は共有秘密を作り、QR の材料を返す。
//
// **この時点では登録されていない。** confirmed_at は NULL のままで、
// 4.6.3 が通るまで認証に一切影響しない——照合しないまま確定させると、
// 利用者が自分を締め出せる（DbDesign.md 6.18）。
func (h *handler) startMyTotp(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/mfa/totp")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	var req startTOTPRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	name, e := validateMFAName(req.Name)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()

	// 上限（確定済み5件）。**未確定は数えない。**
	count, err := h.q.CountConfirmedMfaCredentials(ctx, p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器の件数を読めない: %w", err)))
		return
	}
	if count >= maxMFACredentialsPerUser {
		apierr.Write(w, r, apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
			"登録できるのは%d件までです。いずれかを削除してください", maxMFACredentialsPerUser)))
		return
	}

	// 同じ名前の確定済みがあるか。**部分 UNIQUE と同じ条件を先に見る**ので、
	// DB の一意制約違反ではなく 409 already_exists として返せる。
	if _, err := h.q.FindMfaCredentialByName(ctx, gen.FindMfaCredentialByNameParams{
		UserID: p.ActorID, Name: name,
	}); err == nil {
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage("その名前は既に使われています。別の名前を指定してください"))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("同名の認証器を確かめられない: %w", err)))
		return
	}

	key, _, e := h.secretKey(ctx)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	secret, err := mfa.NewSecret()
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	sealed, nonce, err := tlscert.Seal(key, secret)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("共有秘密を暗号化できない: %w", err)))
		return
	}

	id := ulidgen.New()
	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **途中の行は1人1件まで。** 始め直したら古い QR で確定できないようにする
		// （DbDesign.md 6.18。DB では縛らず、ここで守る）。
		if err := q.DeletePendingMfaCredentials(ctx, p.ActorID); err != nil {
			return fmt.Errorf("古い登録を片付けられない: %w", err)
		}
		return q.CreateMfaCredential(ctx, gen.CreateMfaCredentialParams{
			ID: id, UserID: p.ActorID, Kind: mfaKindTOTP, Name: name,
			Secret: sealed, SecretNonce: nonce,
		})
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器を作れない: %w", err)))
		return
	}

	// **監査に残さない**（4.6.2）。確定していない登録は認証に影響せず、
	// 記録すると共有秘密を作った回数だけ行が増える。
	WriteJSON(w, http.StatusCreated, startedTOTPView{
		ID:         id,
		Name:       name,
		Secret:     secret,
		OtpauthURI: mfa.OtpauthURI(accountLabel(p), secret),
		Digits:     mfa.Digits,
		PeriodSec:  mfa.PeriodSec,
	})
}

// accountLabel は otpauth URI のアカウント名。
//
// **メールアドレスを使う。** 認証アプリの一覧に出る文字列であり、
// 同じ発行者の行が複数あるとき本人が見分ける手がかりになる。
// **空のときは表示名に落とす**——システムのアクターには app_user が無い。
func accountLabel(p *auth.Principal) string {
	if p.Email != "" {
		return p.Email
	}
	return p.DisplayName
}

// ── POST /api/v1/me/mfa/totp/{id}/confirm（4.6.3）──────────

type confirmTOTPRequest struct {
	Code string `json:"code"`
}

// confirmMyTotp はコードを照合し、認証器を確定させる。
//
// **初回だけリカバリコードを返す**（Design.md 6.7.5）。締め出しの手当てを
// 「有効にした瞬間」に渡すためであり、後から作らせる形にすると渡し損ねる。
func (h *handler) confirmMyTotp(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/mfa/totp/{id}/confirm")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	var req confirmTOTPRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "code", Code: "required", Message: "確認コードを入力してください",
		}))
		return
	}

	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// **user_id を条件に含むクエリで引く。** 他人の id では行が返らず 404 になる
	// （存在を漏らさない。Design.md 6.4.5）。
	row, err := h.q.FindPendingMfaCredential(ctx, gen.FindPendingMfaCredentialParams{
		ID: id, UserID: p.ActorID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.WriteCode(w, r, apierr.NotFound)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("登録中の認証器を読めない: %w", err)))
		return
	}

	key, _, e := h.secretKey(ctx)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	secret, err := tlscert.Open(key, row.Secret, row.SecretNonce)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("共有秘密を復号できない: %w", err)))
		return
	}

	// **lastUsedStep に 0 を渡す。** 未確定の行はまだ一度も使われていない。
	step, err := mfa.Verify(secret, req.Code, time.Now(), 0)
	if err != nil {
		h.handleConfirmFailure(w, r, p.ActorID, id, err)
		return
	}

	// ここから確定。**リカバリコードは MFA が0件のときだけ作る。**
	existing, err := h.q.CountConfirmedMfaCredentials(ctx, p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器の件数を読めない: %w", err)))
		return
	}
	first := existing == 0

	var codes []string
	if first {
		codes, err = mfa.NewRecoveryCodes()
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
	}

	var confirmed gen.ConfirmMfaCredentialRow
	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		var err error
		confirmed, err = q.ConfirmMfaCredential(ctx, gen.ConfirmMfaCredentialParams{
			ID: id, UserID: p.ActorID,
			LastUsedStep: pgtype.Int8{Int64: step, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("認証器を確定できない: %w", err)
		}
		if !first {
			return nil
		}
		// **作り直しではなく初回なので、既存は無い**が、念のため消してから入れる
		// ——「認証器0件でコードだけ残っている」状態は 4.6.4 が消しているが、
		// 途中で落ちた履歴があれば残りうる。
		if _, err := q.DeleteRecoveryCodes(ctx, p.ActorID); err != nil {
			return fmt.Errorf("古いリカバリコードを消せない: %w", err)
		}
		return insertRecoveryCodes(ctx, q, p.ActorID, codes)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 二重に送られ、1回目で確定済みになっている（4.6.3 の 404）。
			apierr.WriteCode(w, r, apierr.NotFound)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(ctx, h.q, audit.Entry{
			Action:     audit.MFARegister,
			Result:     audit.Success,
			TargetType: "user_mfa_credential",
			TargetID:   id,
			// **共有秘密もリカバリコードも入れない**（4.6.6）。
			Detail: map[string]any{"kind": mfaKindTOTP, "recovery_codes_issued": first},
		})

	WriteJSON(w, http.StatusOK, confirmedTOTPView{
		Credential: totpCredentialView{
			ID:         confirmed.ID,
			Name:       confirmed.Name,
			CreatedAt:  Time(confirmed.CreatedAt.Time),
			LastUsedAt: apiTimestamptz(confirmed.LastUsedAt),
		},
		RecoveryCodes: codes,
	})
}

// handleConfirmFailure は登録時の照合失敗を数え、応答を返す（4.6.3）。
//
// **5回で未確定の行を捨てる。** 同じ共有秘密で6桁を何度も試させない。
func (h *handler) handleConfirmFailure(
	w http.ResponseWriter, r *http.Request, userID, id string, cause error,
) {
	ctx := r.Context()

	next, err := h.q.RecordMfaCredentialFailure(ctx, gen.RecordMfaCredentialFailureParams{
		ID: id, UserID: userID,
	})
	if err != nil {
		// **数えられないまま通さない。** 落とす側に倒す（login.go と同じ判断）。
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("照合の失敗を記録できない: %w", err)))
		return
	}

	if int(next) >= maxConfirmAttempts {
		if err := h.q.DeletePendingMfaCredentials(ctx, userID); err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("登録中の認証器を捨てられない: %w", err)))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "code", Code: "attempts_exceeded",
			Message: "確認コードの誤りが続いたため、登録をやり直してください",
		}).WithCause(cause))
		return
	}

	apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "code", Code: "mismatch",
		Message: "確認コードが正しくありません。アプリに表示されている6桁を入力してください",
	}).WithCause(cause))
}

// ── DELETE /api/v1/me/mfa/totp/{id}（4.6.4）────────────────

// deleteMyTotp は認証器を削除する。
//
// **現在のパスワードを求めない**（4.2 の扱いに揃える）。
// **最後の1件ならリカバリコードも消す**（Design.md 6.7.5）。
func (h *handler) deleteMyTotp(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "DELETE /me/mfa/totp/{id}")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	id := chi.URLParam(r, "id")

	var removedCodes int64
	var remaining int64
	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		n, err := q.DeleteMfaCredential(ctx, gen.DeleteMfaCredentialParams{
			ID: id, UserID: p.ActorID,
		})
		if err != nil {
			return fmt.Errorf("認証器を消せない: %w", err)
		}
		if n == 0 {
			return errMFANotFound
		}

		remaining, err = q.CountConfirmedMfaCredentials(ctx, p.ActorID)
		if err != nil {
			return fmt.Errorf("残りの認証器を数えられない: %w", err)
		}
		if remaining > 0 {
			return nil
		}
		// **最後の1件だった。** MFA が無効になるので、リカバリコードを残さない。
		removedCodes, err = q.DeleteRecoveryCodes(ctx, p.ActorID)
		if err != nil {
			return fmt.Errorf("リカバリコードを消せない: %w", err)
		}
		return nil
	}); err != nil {
		if errors.Is(err, errMFANotFound) {
			apierr.WriteCode(w, r, apierr.NotFound)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(ctx, h.q, audit.Entry{
			Action:     audit.MFAUnregister,
			Result:     audit.Success,
			TargetType: "user_mfa_credential",
			TargetID:   id,
			// **1つの操作を2行にしない**（6.6 と同じ扱い）。消えたコードの本数は
			// この detail に入れる。
			Detail: map[string]any{
				"remaining_credentials":  remaining,
				"recovery_codes_removed": removedCodes,
			},
		})

	w.WriteHeader(http.StatusNoContent)
}

// errMFANotFound はトランザクション内から 404 を伝えるための番兵。
var errMFANotFound = errors.New("認証器が見つからない")

// ── POST /api/v1/me/mfa/recovery-codes（4.6.5）─────────────

// regenerateMyRecoveryCodes は10本を作り直す。
//
// **既存は未使用のものも含めて全部無効になる。** 本文は受けない——本数も形式も
// 選ばせない（4.4.2 のスコープと同じ判断）。
func (h *handler) regenerateMyRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/mfa/recovery-codes")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()

	count, err := h.q.CountConfirmedMfaCredentials(ctx, p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("認証器の件数を読めない: %w", err)))
		return
	}
	if count == 0 {
		apierr.Write(w, r, apierr.New(apierr.Conflict).
			WithMessage("先に認証アプリを登録してください"))
		return
	}

	codes, err := mfa.NewRecoveryCodes()
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if _, err := q.DeleteRecoveryCodes(ctx, p.ActorID); err != nil {
			return fmt.Errorf("古いリカバリコードを消せない: %w", err)
		}
		return insertRecoveryCodes(ctx, q, p.ActorID, codes)
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(ctx, h.q, audit.Entry{
			Action:     audit.MFARecoveryCodeRegenerate,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   p.ActorID,
			Detail:     map[string]any{"count": len(codes)},
		})

	WriteJSON(w, http.StatusOK, recoveryCodeListView{RecoveryCodes: codes})
}

// ── 小さな助け ─────────────────────────────────────────────

// insertRecoveryCodes は平文の10本をハッシュにして保存する。
//
// **平文を DB に残さない**（Design.md 6.7.5）。SHA-256 で足りるのは、
// コードが利用者の記憶に由来せず50ビットの乱数であるためである。
func insertRecoveryCodes(ctx context.Context, q gen.Querier, userID string, codes []string) error {
	for _, code := range codes {
		if err := q.CreateRecoveryCode(ctx, gen.CreateRecoveryCodeParams{
			ID: ulidgen.New(), UserID: userID, CodeHash: auth.HashToken(code),
		}); err != nil {
			return fmt.Errorf("リカバリコードを保存できない: %w", err)
		}
	}
	return nil
}

// validateMFAName は name を検証する（ApiDesign.md 4.6.2）。
func validateMFAName(raw string) (string, *apierr.Error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "name", Code: "required", Message: "名前を入力してください",
		})
	case utf8.RuneCountInString(name) > mfaCredentialNameMaxLen:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "name", Code: "too_long",
			Message: fmt.Sprintf("名前は%d文字以内で入力してください", mfaCredentialNameMaxLen),
		})
	}
	return name, nil
}

// requirePrincipal は認証ミドルウェアが載せたアクターを取り出す。
//
// **nil は実装の誤りである**（ルート定義が認証必須グループの外にある）。
// 500 に倒し、route を cause に載せて気づけるようにする。
func requirePrincipal(r *http.Request, route string) (*auth.Principal, *apierr.Error) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s が認証ミドルウェアを通っていない", route))
	}
	return p, nil
}
