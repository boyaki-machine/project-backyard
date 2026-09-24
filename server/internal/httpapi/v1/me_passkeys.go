// 自分自身に関するAPI（ApiDesign.md 4章）のうち、パスキーの管理（4.7）。
//
//	GET    /api/v1/me/passkeys           4.7.1
//	POST   /api/v1/me/passkeys/options   4.7.2
//	POST   /api/v1/me/passkeys           4.7.3
//	DELETE /api/v1/me/passkeys/{id}      4.7.4
//
// **4.6（TOTP）と違い、登録の途中の行を作らない。** 途中の状態は挑戦
// （webauthn_challenge）だけにあり、user_passkey に入る行はすべて使えるパスキー
// である（DbDesign.md 6.19）。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
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

// user_passkey の一意制約の名前（DbDesign.md 6.19）。
//
// **credential_id の UNIQUE は列に書いてあるので、名前は PostgreSQL の既定である。**
const (
	uqPasskeyCredentialID = "user_passkey_credential_id_key"
	uqPasskeyName         = "uq_user_passkey_name"
)

// ── 応答の形（ApiDesign.md 4.7.1）──────────────────────────

// passkeyView はパスキー1件。**公開鍵も credential_id も持たない。**
type passkeyView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RPID       string `json:"rp_id"`
	BackedUp   bool   `json:"backed_up"`
	CreatedAt  Time   `json:"created_at"`
	LastUsedAt *Time  `json:"last_used_at"`
}

type passkeyListView struct {
	Items []passkeyView `json:"items"`
}

// ── GET /api/v1/me/passkeys（4.7.1）──────────────────────────

func (h *handler) listMyPasskeys(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "GET /me/passkeys")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rows, err := h.q.ListPasskeys(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスキーを読めない: %w", err)))
		return
	}

	items := make([]passkeyView, 0, len(rows))
	for _, row := range rows {
		items = append(items, passkeyView{
			ID:         row.ID,
			Name:       row.Name,
			RPID:       row.RpID,
			BackedUp:   row.BackupState,
			CreatedAt:  Time(row.CreatedAt.Time),
			LastUsedAt: apiTimestamptz(row.LastUsedAt),
		})
	}
	WriteJSON(w, http.StatusOK, passkeyListView{Items: items})
}

// ── POST /api/v1/me/passkeys/options（4.7.2）─────────────────

// startMyPasskeyRegistration は挑戦を作り、navigator.credentials.create() の
// options を返す。
//
// **excludeCredentials に、同じホスト名で登録済みのパスキーを入れる。** 同じ認証器を
// 二重に登録させない——ブラウザが登録の前に断る。
func (h *handler) startMyPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/passkeys/options")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	wa, rpID, e := h.relyingParty(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	if e := h.checkPasskeyLimit(ctx, p.ActorID); e != nil {
		apierr.Write(w, r, e)
		return
	}

	descriptors, err := h.q.ListPasskeyDescriptors(ctx, gen.ListPasskeyDescriptorsParams{
		UserID: p.ActorID, RpID: rpID,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("登録済みのパスキーを読めない: %w", err)))
		return
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(descriptors))
	for _, d := range descriptors {
		exclusions = append(exclusions, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: d.CredentialID,
			Transport:    decodeTransports(d.Transports),
		})
	}

	creation, session, err := wa.BeginRegistration(registeringUser(p), webauthn.WithExclusions(exclusions))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("登録の挑戦を作れない: %w", err)))
		return
	}

	expiresAt, err := h.saveWebauthnChallenge(ctx, webauthnPurposeRegister, p.ActorID, session)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// **監査に残さない**（4.7.2）。まだ何も登録されていない。
	WriteJSON(w, http.StatusOK, passkeyOptionsView{Options: creation, ExpiresAt: Time(expiresAt)})
}

// ── POST /api/v1/me/passkeys（4.7.3）─────────────────────────

type registerPasskeyRequest struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

// errPasskeyRegistrationExpired は挑戦が使えないときの応答（4.7.3）。
//
// **401 ではなく 422 である。** 既にセッションを持つ本人の操作であり、
// セッションを疑う場面ではない（4.6.3 と同じ理由）。
var errPasskeyRegistrationExpired = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
	Field: "credential", Code: "expired",
	Message: "登録の有効期限が切れました。もう一度やり直してください",
})

// errPasskeyRegistrationFailed は検証に失敗したときの応答（4.7.3）。
var errPasskeyRegistrationFailed = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
	Field: "credential", Code: "invalid", Message: "パスキーを確認できませんでした",
})

// registerMyPasskey は認証器の応答を検証し、パスキーを保存する。
//
// **名前の重複と件数は、挑戦を消費する前に確かめる**（4.7.3）。端末にはもう
// パスキーができているので、名前だけを直して同じ応答を送り直せるようにする。
func (h *handler) registerMyPasskey(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/passkeys")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	var req registerPasskeyRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	// 名前の規則は TOTP の認証器と同じ（1〜60文字。CHECK も同じ）。
	name, e := validateMFAName(req.Name)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if isEmptyJSON(req.Credential) {
		apierr.Write(w, r, errCredentialRequired)
		return
	}

	ctx := r.Context()
	if e := h.checkPasskeyLimit(ctx, p.ActorID); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if _, err := h.q.FindPasskeyByName(ctx, gen.FindPasskeyByNameParams{
		UserID: p.ActorID, Name: name,
	}); err == nil {
		apierr.Write(w, r, errPasskeyNameTaken)
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("同名のパスキーを確かめられない: %w", err)))
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		apierr.Write(w, r, errCredentialUnreadable.WithCause(err))
		return
	}

	session, reason, err := h.consumeWebauthnChallenge(ctx, webauthnPurposeRegister, p.ActorID,
		parsed.Response.CollectedClientData.Challenge)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if reason != "" {
		apierr.Write(w, r, errPasskeyRegistrationExpired.WithCause(errors.New(reason)))
		return
	}

	wa, e := h.relyingPartyFor(r, session.RelyingPartyID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	credential, err := wa.CreateCredential(registeringUser(p), *session, parsed)
	if err != nil {
		apierr.Write(w, r, errPasskeyRegistrationFailed.WithCause(err))
		return
	}

	transports := credential.Transport
	if transports == nil {
		transports = []protocol.AuthenticatorTransport{}
	}
	transportsJSON, err := json.Marshal(transports)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	id := ulidgen.New()
	createdAt, err := h.q.CreatePasskey(ctx, gen.CreatePasskeyParams{
		ID:                id,
		UserID:            p.ActorID,
		Name:              name,
		CredentialID:      credential.ID,
		PublicKey:         credential.PublicKey,
		RpID:              session.RelyingPartyID,
		AttestationType:   credential.AttestationType,
		AttestationFormat: credential.AttestationFormat,
		Aaguid:            credential.Authenticator.AAGUID,
		Attachment:        nonEmptyText(string(credential.Authenticator.Attachment)),
		Transports:        transportsJSON,
		SignCount:         int64(credential.Authenticator.SignCount),
		UserVerified:      credential.Flags.UserVerified,
		BackupEligible:    credential.Flags.BackupEligible,
		BackupState:       credential.Flags.BackupState,
	})
	switch {
	case isUniqueViolation(err, uqPasskeyCredentialID):
		// excludeCredentials で断られるはずだが、別のホスト名で登録済みの同じ
		// 認証器や、並行した2回の登録はここに来る。
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage("このパスキーは登録済みです").WithCause(err))
		return
	case isUniqueViolation(err, uqPasskeyName):
		apierr.Write(w, r, errPasskeyNameTaken.WithCause(err))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスキーを保存できない: %w", err)))
		return
	}

	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(ctx, h.q, audit.Entry{
			Action:     audit.PasskeyRegister,
			Result:     audit.Success,
			TargetType: "user_passkey",
			TargetID:   id,
			// **公開鍵も credential_id も入れない**（4.7.5）。
			Detail: map[string]any{"name": name, "backed_up": credential.Flags.BackupState},
		})

	WriteJSON(w, http.StatusCreated, passkeyView{
		ID:        id,
		Name:      name,
		RPID:      session.RelyingPartyID,
		BackedUp:  credential.Flags.BackupState,
		CreatedAt: Time(createdAt.Time),
	})
}

// errPasskeyNameTaken は同じ名前のパスキーがあるときの応答（4.7.3）。
var errPasskeyNameTaken = apierr.New(apierr.AlreadyExists).
	WithMessage("その名前は既に使われています。別の名前を指定してください")

// ── DELETE /api/v1/me/passkeys/{id}（4.7.4）──────────────────

// deleteMyPasskey はパスキーを1件削除する。
//
// **最後の1件を消しても、他に何も消さない**（4.7.4）。パスワードで入れる状態は
// 変わらない。**端末の中のパスキーは消えない**——PB が消せるのは自分の記録だけである。
func (h *handler) deleteMyPasskey(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "DELETE /me/passkeys/{id}")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// **user_id を条件に含むクエリで消す。** 他人の id では行が返らず 404 になる。
	name, err := h.q.DeletePasskey(ctx, gen.DeletePasskeyParams{ID: id, UserID: p.ActorID})
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスキーを消せない: %w", err)))
		return
	}

	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(ctx, h.q, audit.Entry{
			Action:     audit.PasskeyUnregister,
			Result:     audit.Success,
			TargetType: "user_passkey",
			TargetID:   id,
			Detail:     map[string]any{"name": name},
		})

	w.WriteHeader(http.StatusNoContent)
}

// ── 小さな助け ─────────────────────────────────────────────

// checkPasskeyLimit は上限（5件）を確かめる。
func (h *handler) checkPasskeyLimit(ctx context.Context, userID string) *apierr.Error {
	count, err := h.q.CountPasskeys(ctx, userID)
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("パスキーの件数を読めない: %w", err))
	}
	if count >= passkey.MaxPerUser {
		return apierr.New(apierr.Conflict).WithMessage(fmt.Sprintf(
			"登録できるのは%d件までです。いずれかを削除してください", passkey.MaxPerUser))
	}
	return nil
}

// registeringUser は登録する本人を go-webauthn の利用者にする。
//
// **Name にはメールアドレスを入れる**（accountLabel）。認証器のパスキー一覧に出る
// 文字列であり、同じ RP の行が複数あるとき本人が見分ける手がかりになる。
func registeringUser(p *auth.Principal) *passkey.User {
	return &passkey.User{ID: p.ActorID, Name: accountLabel(p), DisplayName: p.DisplayName}
}

// nonEmptyText は空文字を NULL にする。
func nonEmptyText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
