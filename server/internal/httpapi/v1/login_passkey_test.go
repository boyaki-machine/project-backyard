package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// パスキーでのログイン（ApiDesign.md 3.5 / 3.6、Design.md 6.8.2）のテスト。
//
// **ルータを通して叩く。** ルート定義（認証不要のまま置く・CSRF の対象外）ごと
// 確かめたいためで、login_mfa_test.go と同じ形である。
//
// **応答はソフトウェアの認証器で作る**（passkey_authenticator_test.go）。
// go-webauthn の検証を本当に通すので、「UV が無い」「origin が違う」などの
// 負の側が、検証が効いていることの証拠になる。

func pkRouter(q *fakeQuerier) http.Handler {
	return routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults()})
}

// pkOptions は 3.5 / 4.7.2 の応答を読む型。**WebAuthn の JSON 表現のまま**（camelCase）。
type pkOptions struct {
	Options struct {
		PublicKey struct {
			Challenge        string `json:"challenge"`
			RPID             string `json:"rpId"`
			UserVerification string `json:"userVerification"`
			Timeout          int    `json:"timeout"`
			AllowCredentials []any  `json:"allowCredentials"`
			RP               struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"rp"`
			User struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
			ExcludeCredentials []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			Attestation string `json:"attestation"`
		} `json:"publicKey"`
	} `json:"options"`
	ExpiresAt int64 `json:"expires_at"`
}

func pkReadOptions(t *testing.T, rec *httptest.ResponseRecorder) pkOptions {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("options の status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var out pkOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("options を読めない: %v（%s）", err, rec.Body.String())
	}
	return out
}

// pkStartLogin は 3.5 を叩き、挑戦を返す。
func pkStartLogin(t *testing.T, q *fakeQuerier) string {
	t.Helper()
	opts := pkReadOptions(t, call(pkRouter(q), http.MethodPost, "/api/v1/auth/passkey/options", ""))
	return opts.Options.PublicKey.Challenge
}

// pkPostLogin は 3.6 を叩く。
func pkPostLogin(q *fakeQuerier, credential string) *httptest.ResponseRecorder {
	return call(pkRouter(q), http.MethodPost, "/api/v1/auth/login/passkey",
		fmt.Sprintf(`{"credential":%s}`, credential))
}

// pkRegistered は認証器の鍵を、登録済みのパスキーとしてフェイクに置く。
func pkRegistered(q *fakeQuerier, a *softAuthenticator) *gen.FindPasskeyLoginRow {
	row := &gen.FindPasskeyLoginRow{
		ID:                "01K2PASSKEY00000000000001",
		UserID:            testActorID,
		Name:              "MacBook",
		CredentialID:      a.credentialID,
		PublicKey:         a.coseKey(),
		AttestationType:   "none",
		AttestationFormat: "none",
		Transports:        []byte(`["internal"]`),
		SignCount:         int64(a.signCount),
		UserVerified:      true,
		BackupEligible:    a.backupEligible,
		BackupState:       a.backupState,
		DisplayName:       "田中",
		IsActive:          true,
		Email:             testEmail,
		SystemRole:        auth.SystemRoleAdministrator,
		Locale:            "ja",
		Timezone:          "Asia/Tokyo",
		Theme:             "system",
		Hue:               "blue",
	}
	q.passkey.login = row
	return row
}

// pkAudits は action の監査記録を返す。
func pkAudits(q *fakeQuerier, action string) []gen.InsertAuditLogParams {
	var out []gen.InsertAuditLogParams
	for _, a := range q.audits {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

func pkAuditDetail(t *testing.T, a gen.InsertAuditLogParams) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(a.Detail, &d); err != nil {
		t.Fatalf("監査の detail を読めない: %v（%s）", err, a.Detail)
	}
	return d
}

// ── POST /auth/passkey/options（3.5）─────────────────────────

func TestPasskeyLoginOptionsDoNotRevealAccounts(t *testing.T) {
	q := newFake(t)

	opts := pkReadOptions(t, call(pkRouter(q), http.MethodPost, "/api/v1/auth/passkey/options", ""))
	pk := opts.Options.PublicKey

	if pk.Challenge == "" {
		t.Fatal("challenge が無い")
	}
	if pk.RPID != testRPID {
		t.Errorf("rpId = %q, want %q（Host から導く。Design.md 6.8.3）", pk.RPID, testRPID)
	}
	if pk.UserVerification != "required" {
		t.Errorf("userVerification = %q, want required（Design.md 6.8.1）", pk.UserVerification)
	}
	// **allowCredentials を持たない**——アカウントの有無を応答に出さない（3.5）
	if len(pk.AllowCredentials) != 0 {
		t.Errorf("allowCredentials = %v, want 空", pk.AllowCredentials)
	}
	if opts.ExpiresAt == 0 {
		t.Error("expires_at が無い")
	}

	if len(q.passkey.challengesCreated) != 1 {
		t.Fatalf("挑戦の作成が %d 回, want 1", len(q.passkey.challengesCreated))
	}
	c := q.passkey.challengesCreated[0]
	if c.Purpose != webauthnPurposeLogin || c.UserID.Valid {
		t.Errorf("挑戦 = purpose %q / user_id %v, want login / NULL", c.Purpose, c.UserID)
	}
	if c.Challenge != pk.Challenge {
		t.Errorf("保存した challenge = %q, want 応答と同じ %q", c.Challenge, pk.Challenge)
	}
	// **期限切れを先に片付ける**（DbDesign.md 6.19）
	if q.passkey.expiredDeleted != 1 {
		t.Errorf("期限切れの削除が %d 回, want 1", q.passkey.expiredDeleted)
	}
	// **挑戦を作っただけでは監査に残さない**
	if len(q.audits) != 0 {
		t.Errorf("監査が %d 件残った, want 0", len(q.audits))
	}
}

// TestPasskeyLoginOptionsRejectsIPAddress は IP アドレスで開いた画面を 409 にする（Design.md 6.8.3）。
func TestPasskeyLoginOptionsRejectsIPAddress(t *testing.T) {
	q := newFake(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/options", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	pkRouter(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "conflict" {
		t.Errorf("code = %q, want conflict", got)
	}
	if len(q.passkey.challengesCreated) != 0 {
		t.Errorf("挑戦を %d 件作った, want 0", len(q.passkey.challengesCreated))
	}
}

// ── POST /auth/login/passkey（3.6）───────────────────────────

func TestPasskeyLoginIssuesSession(t *testing.T) {
	q := newFake(t)
	// **TOTP を登録していても第2要素を求めない**（Design.md 6.8.2）
	withConfirmedTOTP(t, q)

	a := newSoftAuthenticator(t, testActorID)
	row := pkRegistered(q, a)

	challenge := pkStartLogin(t, q)
	rec := pkPostLogin(q, a.assert(testRPID, challenge))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if _, ok := view["mfa_required"]; ok {
		t.Error("パスキーで入ったのに第2要素の挑戦を返した")
	}
	if _, ok := view["actor"]; !ok {
		t.Errorf("3.1 と同一構造の応答でない（actor が無い）: %s", rec.Body.String())
	}
	if c := cookieOf(rec, auth.SessionCookieName); c == nil || c.Value == "" {
		t.Errorf("%s が発行されていない", auth.SessionCookieName)
	}

	// **挑戦は消費済み**、sign count と最終利用を書き戻す
	if len(q.passkey.consumed) != 1 {
		t.Errorf("挑戦の消費が %d 回, want 1", len(q.passkey.consumed))
	}
	if len(q.passkey.touched) != 1 || q.passkey.touched[0].ID != row.ID {
		t.Fatalf("TouchPasskeyUsed = %+v, want %s を1回", q.passkey.touched, row.ID)
	}
	if !q.passkey.touched[0].UserVerified {
		t.Error("user_verified を真で書き戻していない")
	}

	succ := pkAudits(q, "login.success")
	if len(succ) != 1 {
		t.Fatalf("login.success が %d 件, want 1", len(succ))
	}
	d := pkAuditDetail(t, succ[0])
	if d["method"] != "passkey" || d["passkey_id"] != row.ID {
		t.Errorf("login.success の detail = %v, want method=passkey / passkey_id=%s", d, row.ID)
	}
	// **パスワードの経路を通っていない**
	if len(q.loginedBy) != 0 {
		t.Errorf("FindLocalLoginByEmail が呼ばれた: %v", q.loginedBy)
	}
}

// TestPasskeyLoginRejects は検証の負の側を並べる。
//
// **どれも同じ 401 と同じ文言にする**（3.6）。理由は監査の detail.reason にだけ残す。
func TestPasskeyLoginRejects(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, q *fakeQuerier, a *softAuthenticator)
		rpID   string
		reason string
	}{
		{
			name:   "UV が無い",
			setup:  func(_ *testing.T, _ *fakeQuerier, a *softAuthenticator) { a.userVerified = false },
			reason: "verification_failed",
		},
		{
			name:   "origin が違う",
			setup:  func(_ *testing.T, _ *fakeQuerier, a *softAuthenticator) { a.origin = "http://evil.example" },
			reason: "verification_failed",
		},
		{
			name:   "RP ID が違う",
			rpID:   "evil.example",
			reason: "verification_failed",
		},
		{
			name: "別の鍵で署名した",
			setup: func(t *testing.T, q *fakeQuerier, a *softAuthenticator) {
				other := newSoftAuthenticator(t, testActorID)
				other.credentialID = a.credentialID
				pkRegistered(q, other)
			},
			reason: "verification_failed",
		},
		{
			name:   "登録されていない",
			setup:  func(_ *testing.T, q *fakeQuerier, _ *softAuthenticator) { q.passkey.login = nil },
			reason: "unknown_credential",
		},
		{
			name:   "利用者が無効化されている",
			setup:  func(_ *testing.T, q *fakeQuerier, _ *softAuthenticator) { q.passkey.login.IsActive = false },
			reason: "inactive",
		},
		{
			name: "sign count が逆行した",
			setup: func(_ *testing.T, q *fakeQuerier, a *softAuthenticator) {
				q.passkey.login.SignCount = 10
				a.signCount = 5
			},
			reason: "clone_warning",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newFake(t)
			a := newSoftAuthenticator(t, testActorID)
			pkRegistered(q, a)
			if c.setup != nil {
				c.setup(t, q, a)
			}
			rpID := testRPID
			if c.rpID != "" {
				rpID = c.rpID
			}

			challenge := pkStartLogin(t, q)
			rec := pkPostLogin(q, a.assert(rpID, challenge))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
			}
			e := errorOf(t, rec)
			if e.Code != "invalid_credentials" || e.Message != "パスキーを確認できませんでした。もう一度お試しください" {
				t.Errorf("error = %s / %q, want invalid_credentials と共通の文言", e.Code, e.Message)
			}
			if c := cookieOf(rec, auth.SessionCookieName); c != nil && c.Value != "" {
				t.Error("失敗なのにセッション Cookie を発行した")
			}
			if len(q.passkey.touched) != 0 {
				t.Error("失敗なのに sign count を書き戻した")
			}

			fails := pkAudits(q, "login.passkey_failure")
			if len(fails) != 1 {
				t.Fatalf("login.passkey_failure が %d 件, want 1", len(fails))
			}
			if got := pkAuditDetail(t, fails[0])["reason"]; got != c.reason {
				t.Errorf("reason = %v, want %s", got, c.reason)
			}
		})
	}
}

// TestPasskeyLoginChallengeIsSingleUse は同じ応答を2回送っても2回目は通らないことを見る。
//
// **先に1回目が通ることを確かめる**（Development.md 8.5「動いたことを先に」）。
func TestPasskeyLoginChallengeIsSingleUse(t *testing.T) {
	q := newFake(t)
	a := newSoftAuthenticator(t, testActorID)
	pkRegistered(q, a)

	assertion := a.assert(testRPID, pkStartLogin(t, q))
	if rec := pkPostLogin(q, assertion); rec.Code != http.StatusOK {
		t.Fatalf("1回目の status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	rec := pkPostLogin(q, assertion)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("2回目の status = %d, want 401", rec.Code)
	}
	fails := pkAudits(q, "login.passkey_failure")
	if len(fails) != 1 || pkAuditDetail(t, fails[0])["reason"] != "consumed" {
		t.Errorf("2回目の監査 = %v, want reason=consumed を1件", fails)
	}
}

func TestPasskeyLoginRejectsUnusableChallenge(t *testing.T) {
	t.Run("期限切れ", func(t *testing.T) {
		q := newFake(t)
		a := newSoftAuthenticator(t, testActorID)
		pkRegistered(q, a)
		challenge := pkStartLogin(t, q)
		created := q.passkey.challengesCreated[0]
		q.passkey.challenge = &gen.FindWebauthnChallengeRow{
			ID: created.ID, Purpose: created.Purpose, Session: created.Session,
			ExpiresAt: ts(time.Now().Add(-time.Second)),
		}

		rec := pkPostLogin(q, a.assert(testRPID, challenge))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		// **使えない挑戦は消費しない**（消費は検証の直前）
		if len(q.passkey.consumed) != 0 {
			t.Errorf("期限切れの挑戦を消費した: %v", q.passkey.consumed)
		}
		fails := pkAudits(q, "login.passkey_failure")
		if len(fails) != 1 || pkAuditDetail(t, fails[0])["reason"] != "expired" {
			t.Errorf("監査 = %v, want reason=expired", fails)
		}
	})

	t.Run("作っていない挑戦", func(t *testing.T) {
		q := newFake(t)
		a := newSoftAuthenticator(t, testActorID)
		pkRegistered(q, a)

		rec := pkPostLogin(q, a.assert(testRPID, "bm90LWlzc3VlZC1ieS1wYi0xMjM0NTY3ODk"))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		fails := pkAudits(q, "login.passkey_failure")
		if len(fails) != 1 || pkAuditDetail(t, fails[0])["reason"] != "unknown_challenge" {
			t.Errorf("監査 = %v, want reason=unknown_challenge", fails)
		}
	})
}

func TestPasskeyLoginValidatesBody(t *testing.T) {
	cases := []struct {
		name, body, code string
	}{
		{"credential が無い", `{}`, "required"},
		{"credential が null", `{"credential":null}`, "required"},
		{"credential を読めない", `{"credential":{"id":"x"}}`, "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newFake(t)
			rec := call(pkRouter(q), http.MethodPost, "/api/v1/auth/login/passkey", c.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			e := errorOf(t, rec)
			if len(e.Details) != 1 || e.Details[0].Field != "credential" || e.Details[0].Code != c.code {
				t.Errorf("details = %+v, want credential / %s", e.Details, c.code)
			}
		})
	}
}
