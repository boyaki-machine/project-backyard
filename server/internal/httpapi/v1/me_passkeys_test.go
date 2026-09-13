package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// パスキーの管理（ApiDesign.md 4.7。pb-104）のハンドラ単体テスト。
//
// 認証はミドルウェアの責務なので通さない（me_mfa_test.go と同じ）。
// **登録の応答はソフトウェアの認証器で作り、go-webauthn の検証を本当に通す。**

func pkHandler(q *fakeQuerier) *handler {
	return &handler{q: q, tx: &fakeTxRunner{q: q}, settings: config.LiveDefaults()}
}

// pkStartRegistration は 4.7.2 を叩き、options を返す。
func pkStartRegistration(t *testing.T, h *handler, p *auth.Principal) pkOptions {
	t.Helper()
	rec := httptest.NewRecorder()
	h.startMyPasskeyRegistration(rec, tokenReq(http.MethodPost, "/api/v1/me/passkeys/options", "", "", p))
	return pkReadOptions(t, rec)
}

// pkRegister は 4.7.3 を叩く。
func pkRegister(h *handler, p *auth.Principal, name, credential string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"name":%q,"credential":%s}`, name, credential)
	h.registerMyPasskey(rec, tokenReq(http.MethodPost, "/api/v1/me/passkeys", body, "", p))
	return rec
}

// ── GET /me/passkeys（4.7.1）─────────────────────────────────

func TestListMyPasskeysOmitsKeyMaterial(t *testing.T) {
	q := newFake(t)
	q.passkey.list = []gen.ListPasskeysRow{
		{ID: "01K2PASSKEY00000000000001", Name: "MacBook", RpID: "localhost", BackupState: true,
			CreatedAt: ts(time.Now().Add(-time.Hour))},
	}

	rec := httptest.NewRecorder()
	pkHandler(q).listMyPasskeys(rec, tokenReq(http.MethodGet, "/api/v1/me/passkeys", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d 件, want 1", len(body.Items))
	}
	item := body.Items[0]
	if item["rp_id"] != "localhost" || item["backed_up"] != true || item["last_used_at"] != nil {
		t.Errorf("item = %v", item)
	}
	// **公開鍵も credential_id も返さない**（4.7.1）
	for _, key := range []string{"public_key", "credential_id", "publicKey", "id_bytes"} {
		if _, ok := item[key]; ok {
			t.Errorf("一覧に %s が入っている", key)
		}
	}
}

// ── POST /me/passkeys/options（4.7.2）────────────────────────

func TestStartMyPasskeyRegistrationSetsPolicy(t *testing.T) {
	q := newFake(t)
	q.passkey.count = 1
	q.passkey.descriptors = []gen.ListPasskeyDescriptorsRow{
		{CredentialID: []byte("existing-credential"), Transports: []byte(`["internal"]`)},
	}

	opts := pkStartRegistration(t, pkHandler(q), selfPrincipal())
	pk := opts.Options.PublicKey

	if pk.RP.ID != testRPID {
		t.Errorf("rp.id = %q, want %q", pk.RP.ID, testRPID)
	}
	// **user handle は actor.id**（Design.md 6.8.4）
	if pk.User.ID != b64url([]byte(testActorID)) {
		t.Errorf("user.id = %q, want actor.id の base64url", pk.User.ID)
	}
	if pk.User.Name != testEmail {
		t.Errorf("user.name = %q, want メールアドレス", pk.User.Name)
	}
	if pk.AuthenticatorSelection.ResidentKey != "required" ||
		pk.AuthenticatorSelection.UserVerification != "required" {
		t.Errorf("authenticatorSelection = %+v, want residentKey/userVerification とも required",
			pk.AuthenticatorSelection)
	}
	if pk.Attestation != "none" {
		t.Errorf("attestation = %q, want none", pk.Attestation)
	}
	// **同じ認証器を二重に登録させない**——同じホスト名の登録済みを除外に入れる
	if len(pk.ExcludeCredentials) != 1 || pk.ExcludeCredentials[0].ID != b64url([]byte("existing-credential")) {
		t.Errorf("excludeCredentials = %+v, want 登録済みの1件", pk.ExcludeCredentials)
	}
	if len(q.passkey.descriptorCalls) != 1 || q.passkey.descriptorCalls[0].RpID != testRPID {
		t.Errorf("除外の材料を %+v で引いた, want rp_id=%s", q.passkey.descriptorCalls, testRPID)
	}

	// **登録の挑戦は1人1件**——作る前に同じ利用者の挑戦を消す
	if len(q.passkey.challengesDeletedFor) != 1 || q.passkey.challengesDeletedFor[0].String != testActorID {
		t.Errorf("古い挑戦の削除 = %+v, want %s を1回", q.passkey.challengesDeletedFor, testActorID)
	}
	if len(q.passkey.challengesCreated) != 1 {
		t.Fatalf("挑戦の作成が %d 回, want 1", len(q.passkey.challengesCreated))
	}
	c := q.passkey.challengesCreated[0]
	if c.Purpose != webauthnPurposeRegister || c.UserID.String != testActorID {
		t.Errorf("挑戦 = purpose %q / user_id %v, want register / 本人", c.Purpose, c.UserID)
	}
	if len(q.audits) != 0 {
		t.Errorf("監査が %d 件残った, want 0（まだ何も登録されていない）", len(q.audits))
	}
}

func TestStartMyPasskeyRegistrationRefuses(t *testing.T) {
	t.Run("5件ある", func(t *testing.T) {
		q := newFake(t)
		q.passkey.count = 5
		rec := httptest.NewRecorder()
		pkHandler(q).startMyPasskeyRegistration(rec,
			tokenReq(http.MethodPost, "/api/v1/me/passkeys/options", "", "", selfPrincipal()))

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		if len(q.passkey.challengesCreated) != 0 {
			t.Error("上限なのに挑戦を作った")
		}
	})

	t.Run("IP アドレスで開いている", func(t *testing.T) {
		q := newFake(t)
		req := tokenReq(http.MethodPost, "/api/v1/me/passkeys/options", "", "", selfPrincipal())
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		pkHandler(q).startMyPasskeyRegistration(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(errorOf(t, rec).Message, "IP アドレス") {
			t.Errorf("message = %q, want IP アドレスである理由", errorOf(t, rec).Message)
		}
	})
}

// ── POST /me/passkeys（4.7.3）────────────────────────────────

func TestRegisterMyPasskeyStoresVerifiedCredential(t *testing.T) {
	q := newFake(t)
	h := pkHandler(q)
	a := newSoftAuthenticator(t, testActorID)
	a.backupEligible, a.backupState = true, true

	opts := pkStartRegistration(t, h, selfPrincipal())
	rec := pkRegister(h, selfPrincipal(), "MacBook", a.register(testRPID, opts.Options.PublicKey.Challenge))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.passkey.created) != 1 {
		t.Fatalf("保存が %d 回, want 1", len(q.passkey.created))
	}
	got := q.passkey.created[0]
	if got.UserID != testActorID || got.Name != "MacBook" || got.RpID != testRPID {
		t.Errorf("保存 = user %s / name %s / rp_id %s", got.UserID, got.Name, got.RpID)
	}
	if string(got.CredentialID) != string(a.credentialID) {
		t.Error("credential_id が認証器の値と違う")
	}
	// **公開鍵は COSE_Key のまま**（DbDesign.md 6.19）
	if string(got.PublicKey) != string(a.coseKey()) {
		t.Error("public_key が認証器の COSE_Key と違う")
	}
	if !got.UserVerified || !got.BackupEligible || !got.BackupState {
		t.Errorf("フラグ = uv %v / be %v / bs %v, want すべて真", got.UserVerified, got.BackupEligible, got.BackupState)
	}
	if string(got.Transports) != `["internal"]` {
		t.Errorf("transports = %s", got.Transports)
	}

	view := viewOf(t, rec)
	if view["name"] != "MacBook" || view["rp_id"] != testRPID || view["backed_up"] != true {
		t.Errorf("応答 = %v", view)
	}

	reg := pkAudits(q, "passkey.register")
	if len(reg) != 1 {
		t.Fatalf("passkey.register が %d 件, want 1", len(reg))
	}
	d := pkAuditDetail(t, reg[0])
	for _, key := range []string{"public_key", "credential_id", "publicKey"} {
		if _, ok := d[key]; ok {
			t.Errorf("監査の detail に %s を入れた", key)
		}
	}
}

func TestRegisterMyPasskeyRejectsUnverifiedUser(t *testing.T) {
	q := newFake(t)
	h := pkHandler(q)
	a := newSoftAuthenticator(t, testActorID)
	a.userVerified = false // 生体認証も PIN も通っていない

	opts := pkStartRegistration(t, h, selfPrincipal())
	rec := pkRegister(h, selfPrincipal(), "YubiKey", a.register(testRPID, opts.Options.PublicKey.Challenge))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); len(e.Details) != 1 || e.Details[0].Field != "credential" {
		t.Errorf("details = %+v, want credential", e.Details)
	}
	if len(q.passkey.created) != 0 {
		t.Error("UV の無いパスキーを保存した")
	}
}

// TestRegisterMyPasskeyNameTakenKeepsChallenge は、名前の重複では挑戦を消費せず、
// **名前だけを直して同じ応答を送り直せる**ことを見る（4.7.3）。
func TestRegisterMyPasskeyNameTakenKeepsChallenge(t *testing.T) {
	q := newFake(t)
	h := pkHandler(q)
	a := newSoftAuthenticator(t, testActorID)
	opts := pkStartRegistration(t, h, selfPrincipal())
	credential := a.register(testRPID, opts.Options.PublicKey.Challenge)

	q.passkey.nameTaken = true
	rec := pkRegister(h, selfPrincipal(), "MacBook", credential)
	if rec.Code != http.StatusConflict || errorOf(t, rec).Code != "already_exists" {
		t.Fatalf("status = %d / %s, want 409 already_exists", rec.Code, rec.Body.String())
	}
	if len(q.passkey.consumed) != 0 {
		t.Fatalf("名前の重複で挑戦を消費した: %v", q.passkey.consumed)
	}

	q.passkey.nameTaken = false
	rec = pkRegister(h, selfPrincipal(), "MacBook (2)", credential)
	if rec.Code != http.StatusCreated {
		t.Fatalf("送り直しの status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
}

func TestRegisterMyPasskeyRefusesUnusableChallenge(t *testing.T) {
	t.Run("他人の挑戦", func(t *testing.T) {
		q := newFake(t)
		h := pkHandler(q)
		a := newSoftAuthenticator(t, testActorID)
		opts := pkStartRegistration(t, h, selfPrincipal())

		other := selfPrincipal()
		other.ActorID = "01K2OTHER0000000000000001"
		rec := pkRegister(h, other, "MacBook", a.register(testRPID, opts.Options.PublicKey.Challenge))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		// **持ち主の登録を邪魔させない**——他人の挑戦は消費しない
		if len(q.passkey.consumed) != 0 {
			t.Errorf("他人の挑戦を消費した: %v", q.passkey.consumed)
		}
	})

	t.Run("期限切れ", func(t *testing.T) {
		q := newFake(t)
		h := pkHandler(q)
		a := newSoftAuthenticator(t, testActorID)
		opts := pkStartRegistration(t, h, selfPrincipal())
		created := q.passkey.challengesCreated[0]
		q.passkey.challenge = &gen.FindWebauthnChallengeRow{
			ID: created.ID, Purpose: created.Purpose, UserID: created.UserID,
			Session: created.Session, ExpiresAt: ts(time.Now().Add(-time.Second)),
		}

		rec := pkRegister(h, selfPrincipal(), "MacBook", a.register(testRPID, opts.Options.PublicKey.Challenge))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if e := errorOf(t, rec); len(e.Details) != 1 || e.Details[0].Code != "expired" {
			t.Errorf("details = %+v, want credential / expired", e.Details)
		}
	})
}

func TestRegisterMyPasskeyDuplicateCredential(t *testing.T) {
	q := newFake(t)
	h := pkHandler(q)
	a := newSoftAuthenticator(t, testActorID)
	q.passkey.createErr = &pgconn.PgError{Code: "23505", ConstraintName: uqPasskeyCredentialID}

	opts := pkStartRegistration(t, h, selfPrincipal())
	rec := pkRegister(h, selfPrincipal(), "MacBook", a.register(testRPID, opts.Options.PublicKey.Challenge))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "already_exists" || !strings.Contains(e.Message, "登録済み") {
		t.Errorf("error = %s / %q, want already_exists と「登録済み」", e.Code, e.Message)
	}
}

func TestRegisterMyPasskeyValidatesBody(t *testing.T) {
	cases := []struct {
		name, body, field string
	}{
		{"名前が空", `{"name":"  ","credential":{}}`, "name"},
		{"credential が無い", `{"name":"MacBook"}`, "credential"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newFake(t)
			rec := httptest.NewRecorder()
			pkHandler(q).registerMyPasskey(rec,
				tokenReq(http.MethodPost, "/api/v1/me/passkeys", c.body, "", selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			if e := errorOf(t, rec); len(e.Details) != 1 || e.Details[0].Field != c.field {
				t.Errorf("details = %+v, want %s", e.Details, c.field)
			}
		})
	}
}

// ── DELETE /me/passkeys/{id}（4.7.4）─────────────────────────

func TestDeleteMyPasskey(t *testing.T) {
	const id = "01K2PASSKEY00000000000001"

	t.Run("消す", func(t *testing.T) {
		q := newFake(t)
		q.passkey.deleteName = "MacBook"
		rec := httptest.NewRecorder()
		pkHandler(q).deleteMyPasskey(rec,
			tokenReq(http.MethodDelete, "/api/v1/me/passkeys/"+id, "", id, selfPrincipal()))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		if len(q.passkey.deleted) != 1 || q.passkey.deleted[0].UserID != testActorID {
			t.Errorf("削除 = %+v, want 本人の条件で1回", q.passkey.deleted)
		}
		if got := pkAudits(q, "passkey.unregister"); len(got) != 1 {
			t.Errorf("passkey.unregister が %d 件, want 1", len(got))
		}
		// **最後の1件でも他に何も消さない**（4.7.4。TOTP とは違う）
		if len(q.mfa.codesDeletedFor) != 0 {
			t.Error("パスキーの削除でリカバリコードを消した")
		}
	})

	t.Run("他人のもの・存在しない", func(t *testing.T) {
		q := newFake(t) // deleteName が空 = 行が返らない
		rec := httptest.NewRecorder()
		pkHandler(q).deleteMyPasskey(rec,
			tokenReq(http.MethodDelete, "/api/v1/me/passkeys/"+id, "", id, selfPrincipal()))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if len(q.audits) != 0 {
			t.Error("消していないのに監査に残した")
		}
	})
}
