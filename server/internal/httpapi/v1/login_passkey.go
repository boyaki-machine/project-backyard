// パスキーでのログイン（ApiDesign.md 3.5・3.6、Design.md 6.8.2。pb-104）。
//
//	POST /api/v1/auth/passkey/options   → 挑戦を作り、navigator.credentials.get() の options を返す
//	POST /api/v1/auth/login/passkey     → 応答を検証し、セッションを発行する
//
// **パスキーはパスワードの代わりであって、第2要素ではない**（Design.md 6.8.1）。
// ロック（local_credential.locked_until）も第2要素の挑戦（login_mfa.go）も通らない。
// UV（生体認証か PIN）をライブラリの検証で必須にしてあり、それがパスキー1回で
// 多要素を満たす根拠である（passkey.New）。
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/passkey"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// webauthn_challenge.purpose の値（DbDesign.md 6.19）。
const (
	webauthnPurposeLogin    = "login"
	webauthnPurposeRegister = "register"
)

// passkeyOptionsView は 3.5 と 4.7.2 の応答。
//
// **Options は go-webauthn の型をそのまま JSON にする。** WebAuthn の JSON 表現で
// あり、PB の命名規約（snake_case）に変換しない（ApiDesign.md 3.5）。
type passkeyOptionsView struct {
	Options   any  `json:"options"`
	ExpiresAt Time `json:"expires_at"`
}

// errPasskeyLoginFailed は 3.6 の失敗の応答。
//
// **理由を分けない**（ApiDesign.md 3.6）。誰でも叩ける口なので、「そのパスキーは
// 登録されていない」と「署名が合わない」を区別して見せない。理由は監査に残す。
var errPasskeyLoginFailed = apierr.New(apierr.InvalidCredentials).
	WithMessage("パスキーを確認できませんでした。もう一度お試しください")

// errPasskeyUnknown はハンドラの中から「登録されていない」を伝える番兵。
var errPasskeyUnknown = errors.New("登録されていないパスキー")

// relyingParty は要求の Host から RP を組む（Design.md 6.8.3）。
//
// **origin は publicBaseURL と同じ組み立てにする**（ApiDesign.md 5.7.1）。
// IP アドレスで開いているなら 409 を返す——IP アドレスは RP ID になれない。
func (h *handler) relyingParty(r *http.Request) (*webauthn.WebAuthn, string, *apierr.Error) {
	rpID, err := passkey.RPID(r.Host)
	if err != nil {
		msg := "このアドレスで開いた画面ではパスキーを使えません。ホスト名で開いてください"
		if errors.Is(err, passkey.ErrIPAddress) {
			msg = "IP アドレスで開いた画面ではパスキーを使えません。ホスト名で開いてください"
		}
		return nil, "", apierr.New(apierr.Conflict).WithMessage(msg).WithCause(err)
	}
	wa, e := h.relyingPartyFor(r, rpID)
	if e != nil {
		return nil, "", e
	}
	return wa, rpID, nil
}

// relyingPartyFor は RP ID を決めた上で RP を組む。
//
// **確定の要求では、挑戦を作ったときの RP ID を使う**（SessionData に残してある）。
// origin だけはいまの要求から組む——clientDataJSON の origin は、いま開いている
// ページのものだからである。
func (h *handler) relyingPartyFor(r *http.Request, rpID string) (*webauthn.WebAuthn, *apierr.Error) {
	wa, err := passkey.New(rpID, h.publicBaseURL(r))
	if err != nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("WebAuthn の RP を組めない: %w", err))
	}
	return wa, nil
}

// saveWebauthnChallenge は挑戦を DB に置き、期限を返す（DbDesign.md 6.19）。
//
// **期限切れを先に全部消す。** 専用のバッチを持たず、挑戦を作るたびに片付ける。
// 登録（userID あり）なら、同じ利用者の古い挑戦も消す——1人1件（ApiDesign.md 4.7.2）。
func (h *handler) saveWebauthnChallenge(
	ctx context.Context, purpose, userID string, session *webauthn.SessionData,
) (time.Time, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return time.Time{}, fmt.Errorf("SessionData を JSON にできない: %w", err)
	}
	expiresAt := session.Expires
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(passkey.ChallengeTTL)
	}

	var owner pgtype.Text
	if userID != "" {
		owner = pgtype.Text{String: userID, Valid: true}
	}

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.DeleteExpiredWebauthnChallenges(ctx); err != nil {
			return fmt.Errorf("期限切れの挑戦を片付けられない: %w", err)
		}
		if owner.Valid {
			if _, err := q.DeleteWebauthnChallengesForUser(ctx, owner); err != nil {
				return fmt.Errorf("古い登録の挑戦を片付けられない: %w", err)
			}
		}
		return q.CreateWebauthnChallenge(ctx, gen.CreateWebauthnChallengeParams{
			ID:        ulidgen.New(),
			Purpose:   purpose,
			UserID:    owner,
			Challenge: session.Challenge,
			Session:   payload,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
		})
	})
	if err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

// consumeWebauthnChallenge は挑戦を引いて消費し、SessionData を返す（Design.md 6.8.2）。
//
// **検証より先に消費する。** 同じ応答を並行して2回送られても、通るのは1回だけになる。
// 使えない挑戦なら reason に理由を入れて返す（応答には出さず、監査に残す）。
//
// userID は登録のときの本人である（ログインでは空）。**他人の挑戦は消費しない**
// ——「無い」と同じに扱い、持ち主の登録を邪魔させない。
func (h *handler) consumeWebauthnChallenge(
	ctx context.Context, purpose, userID, challenge string,
) (*webauthn.SessionData, string, error) {
	row, err := h.q.FindWebauthnChallenge(ctx, gen.FindWebauthnChallengeParams{
		Challenge: challenge, Purpose: purpose,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "unknown_challenge", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("挑戦を引けない: %w", err)
	}
	if row.UserID.Valid != (userID != "") || row.UserID.String != userID {
		return nil, "unknown_challenge", nil
	}
	if row.ConsumedAt.Valid {
		return nil, "consumed", nil
	}
	if !row.ExpiresAt.Time.After(time.Now()) {
		return nil, "expired", nil
	}

	n, err := h.q.ConsumeWebauthnChallenge(ctx, row.ID)
	if err != nil {
		return nil, "", fmt.Errorf("挑戦を消費できない: %w", err)
	}
	if n == 0 {
		return nil, "consumed", nil
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(row.Session, &session); err != nil {
		return nil, "", fmt.Errorf("SessionData を読めない: %w", err)
	}
	return &session, "", nil
}

// ── POST /api/v1/auth/passkey/options（3.5）─────────────────

// startPasskeyLogin は挑戦を作り、navigator.credentials.get() の options を返す。
//
// **allowCredentials を持たない**（ApiDesign.md 3.5）。誰がログインしようと
// しているかを知らないまま挑戦を作るので、アカウントの有無が応答に現れない。
func (h *handler) startPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	wa, _, e := h.relyingParty(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	assertion, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("ログインの挑戦を作れない: %w", err)))
		return
	}

	expiresAt, err := h.saveWebauthnChallenge(r.Context(), webauthnPurposeLogin, "", session)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// **監査に残さない。** 挑戦を作っただけでは、まだ誰も何もしていない。
	WriteJSON(w, http.StatusOK, passkeyOptionsView{Options: assertion, ExpiresAt: Time(expiresAt)})
}

// ── POST /api/v1/auth/login/passkey（3.6）───────────────────

type loginPasskeyRequest struct {
	Credential json.RawMessage `json:"credential"`
}

// loginPasskey はパスキーの応答を検証し、成功すれば 3.1 の手順6〜8 を行う。
func (h *handler) loginPasskey(w http.ResponseWriter, r *http.Request) {
	var req loginPasskeyRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if isEmptyJSON(req.Credential) {
		apierr.Write(w, r, errCredentialRequired)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		apierr.Write(w, r, errCredentialUnreadable.WithCause(err))
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	session, reason, err := h.consumeWebauthnChallenge(ctx, webauthnPurposeLogin, "",
		parsed.Response.CollectedClientData.Challenge)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if reason != "" {
		h.recordPasskeyFailure(ctx, rec, "", reason)
		apierr.Write(w, r, errPasskeyLoginFailed.WithCause(errors.New(reason)))
		return
	}

	wa, e := h.relyingPartyFor(r, session.RelyingPartyID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **利用者は credential_id で引く。** ライブラリは、引いた利用者の user handle と
	// 応答の userHandle が一致するかも確かめる（validateLogin の手順2）。
	var (
		row    gen.FindPasskeyLoginRow
		found  bool
		lookup error
	)
	handler := func(rawID, _ []byte) (webauthn.User, error) {
		got, err := h.q.FindPasskeyLogin(ctx, rawID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errPasskeyUnknown
		}
		if err != nil {
			lookup = fmt.Errorf("パスキーを引けない: %w", err)
			return nil, lookup
		}
		row, found = got, true
		return passkeyLoginUser(got), nil
	}

	_, credential, err := wa.ValidatePasskeyLogin(handler, *session, parsed)
	if lookup != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(lookup))
		return
	}
	if found {
		rec = rec.WithActor(row.UserID, auth.ActorKindUser,
			(&auth.Principal{DisplayName: row.DisplayName, Email: row.Email}).AuditLabel())
	}
	if err != nil {
		reason := "verification_failed"
		if errors.Is(err, errPasskeyUnknown) {
			reason = "unknown_credential"
		}
		h.recordPasskeyFailure(ctx, rec, row.UserID, reason)
		apierr.Write(w, r, errPasskeyLoginFailed.WithCause(err))
		return
	}

	// **無効化は照合の後に見る**（login.go と同じ）。応答は区別しない。
	if !row.IsActive {
		h.recordPasskeyFailure(ctx, rec, row.UserID, "inactive")
		apierr.Write(w, r, errPasskeyLoginFailed.WithCause(errors.New("アクターが無効化されている")))
		return
	}

	// **sign count の逆行は拒否する**（Design.md 6.8.2）。同期されるパスキーは常に 0 を
	// 返すので、ここに掛かるのは物理的な認証器だけである。
	if credential.Authenticator.CloneWarning {
		h.recordPasskeyFailure(ctx, rec, row.UserID, "clone_warning")
		apierr.Write(w, r, errPasskeyLoginFailed.WithCause(errors.New("sign count が逆行した")))
		return
	}

	// **書き戻せないまま通さない。** sign count は複製の検出に使う値である。
	if err := h.q.TouchPasskeyUsed(ctx, gen.TouchPasskeyUsedParams{
		ID:           row.ID,
		SignCount:    int64(credential.Authenticator.SignCount),
		UserVerified: credential.Flags.UserVerified,
		BackupState:  credential.Flags.BackupState,
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスキーの使用を記録できない: %w", err)))
		return
	}

	h.completePasskeyLogin(w, r, rec, row)
}

// passkeyLoginUser は照合のための利用者を組む。**持たせる鍵は引いた1件だけ**である。
func passkeyLoginUser(row gen.FindPasskeyLoginRow) *passkey.User {
	return &passkey.User{
		ID:          row.UserID,
		Name:        row.Email,
		DisplayName: row.DisplayName,
		Credentials: []webauthn.Credential{{
			ID:                row.CredentialID,
			PublicKey:         row.PublicKey,
			AttestationType:   row.AttestationType,
			AttestationFormat: row.AttestationFormat,
			Transport:         decodeTransports(row.Transports),
			Flags: webauthn.CredentialFlags{
				UserPresent:    true,
				UserVerified:   row.UserVerified,
				BackupEligible: row.BackupEligible,
				BackupState:    row.BackupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:     row.Aaguid,
				SignCount:  uint32(row.SignCount),
				Attachment: protocol.AuthenticatorAttachment(row.Attachment.String),
			},
		}},
	}
}

// completePasskeyLogin は 3.1 の手順6〜8 を行う（ApiDesign.md 3.6）。
//
// **パスワードで通ったときと同じ関数を通る**（finishLogin。pb-115）。
// ここで決めるのは、利用者の属性と監査の detail だけである。
func (h *handler) completePasskeyLogin(
	w http.ResponseWriter, r *http.Request, rec *audit.Recorder, row gen.FindPasskeyLoginRow,
) {
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
		// **パスキーで通ったことを残す**（ApiDesign.md 3.6）。
		map[string]any{"method": "passkey", "passkey_id": row.ID})
}

// recordPasskeyFailure は login.passkey_failure を1件残す（ApiDesign.md 4.7.5）。
//
// **公開鍵も credential_id も detail に入れない。** 利用者が分かっていれば対象に載せる。
func (h *handler) recordPasskeyFailure(ctx context.Context, rec *audit.Recorder, userID, reason string) {
	rec.RecordOrLog(ctx, h.q, audit.Entry{
		Action:     audit.LoginPasskeyFailure,
		Result:     audit.Failure,
		TargetType: targetTypeOrEmpty(userID),
		TargetID:   userID,
		Detail:     map[string]any{"reason": reason},
	})
}

// ── 小さな助け ─────────────────────────────────────────────

// errCredentialRequired / errCredentialUnreadable は credential の形の誤り（422）。
var (
	errCredentialRequired = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "credential", Code: "required", Message: "パスキーの応答がありません",
	})
	errCredentialUnreadable = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "credential", Code: "invalid", Message: "パスキーの応答を読み取れませんでした",
	})
)

// isEmptyJSON は本文に credential が無いか（キーが無い・null）を見る。
func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// decodeTransports は user_passkey.transports（jsonb の配列）を読む。
//
// **読めなければ空にする。** transports はブラウザへの手がかりにすぎず、
// 無くても照合は通る。
func decodeTransports(raw []byte) []protocol.AuthenticatorTransport {
	var out []protocol.AuthenticatorTransport
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
