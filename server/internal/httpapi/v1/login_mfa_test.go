package v1

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/mfa"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ログインの第2要素（ApiDesign.md 3.1 の後半・3.4）のテスト。
//
// **ルータを通して叩く。** ルート定義（認証不要のまま置く・CSRF の対象外）
// ごと確かめたいためで、login_test.go と同じ形である。

// mfaRouter は第2要素を通る経路のルータを作る。
//
// **settings を渡す。** 照合が h.secretKey を通るので、Deps.Settings が
// nil だと LiveDefaults が入る（Mount がそうしている）が、明示しておく。
func mfaRouter(q *fakeQuerier) http.Handler {
	return routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults()})
}

// loginWithMFA はパスワードのログインを叩く。
func loginWithMFA(q *fakeQuerier) *httptest.ResponseRecorder {
	return call(mfaRouter(q), http.MethodPost, "/api/v1/auth/login",
		`{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
}

// postLoginMFA は第2要素の確認を叩く。
func postLoginMFA(q *fakeQuerier, body string) *httptest.ResponseRecorder {
	return call(mfaRouter(q), http.MethodPost, "/api/v1/auth/login/mfa", body)
}

// challengeRow は「パスワードは通った」状態の挑戦を組む。
func challengeRow(expiresIn time.Duration) *gen.FindMfaLoginChallengeRow {
	return &gen.FindMfaLoginChallengeRow{
		ID:          "01K2CHAL0000000000000001",
		UserID:      testActorID,
		ExpiresAt:   ts(time.Now().Add(expiresIn)),
		DisplayName: "田中",
		IsActive:    true,
		Email:       testEmail,
		SystemRole:  auth.SystemRoleAdministrator,
		Locale:      "ja",
		Timezone:    "Asia/Tokyo",
		Theme:       "system",
		Hue:         "blue",
	}
}

// withConfirmedTOTP は確定済みの認証器を1件持たせ、その共有秘密を返す。
func withConfirmedTOTP(t *testing.T, q *fakeQuerier) string {
	t.Helper()
	secret, err := mfa.NewSecret()
	if err != nil {
		t.Fatalf("共有秘密を作れない: %v", err)
	}
	ct, nonce := sealedSecret(t, secret)
	q.mfa.confirmed = []gen.ListConfirmedMfaCredentialsRow{
		{ID: "01K2MFA00000000000000001", Name: "iPhone", Kind: "totp", CreatedAt: ts(time.Now())},
	}
	q.mfa.secrets = []gen.ListConfirmedMfaSecretsRow{
		{ID: "01K2MFA00000000000000001", Name: "iPhone", Secret: ct, SecretNonce: nonce},
	}
	return secret
}

// ── POST /auth/login の分岐（ApiDesign.md 3.1）──────────────

func TestLoginReturnsChallengeWhenMFAEnabled(t *testing.T) {
	q := newFake(t)
	withConfirmedTOTP(t, q)
	q.mfa.unusedCode = 10

	rec := loginWithMFA(q)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	view := viewOf(t, rec)
	if view["mfa_required"] != true {
		t.Fatalf("mfa_required = %v, want true（body=%s）", view["mfa_required"], rec.Body.String())
	}
	// **Session とは actor の有無で見分ける。**
	if _, ok := view["actor"]; ok {
		t.Error("挑戦の応答に actor が入っている")
	}
	token, _ := view["mfa_token"].(string)
	if !strings.HasPrefix(token, auth.MFAChallengePrefix) {
		t.Errorf("mfa_token = %q, want %q で始まる", token, auth.MFAChallengePrefix)
	}
	if view["expires_at"] == nil {
		t.Error("expires_at が無い（挑戦の期限である）")
	}

	// **セッション Cookie を出さない。** 出すと第2要素が飾りになる。
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			t.Errorf("挑戦なのに %s を発行した", auth.SessionCookieName)
		}
	}

	// **DB には平文を残さない。**
	if len(q.mfa.challengesCreated) != 1 {
		t.Fatalf("挑戦の作成が %d 回, want 1", len(q.mfa.challengesCreated))
	}
	if q.mfa.challengesCreated[0].TokenHash == token {
		t.Error("挑戦トークンが平文で保存されようとしている")
	}
	// **古い挑戦を先に片付ける。**
	if len(q.mfa.challengesDeleted) != 1 {
		t.Errorf("古い挑戦の削除が %d 回, want 1", len(q.mfa.challengesDeleted))
	}

	// パスワードは正しかったので、失敗回数は戻す。
	if len(q.resets) != 1 {
		t.Errorf("failed_attempts のリセットが %d 回, want 1", len(q.resets))
	}
}

// TestLoginChallengeMethodsDependOnRecoveryCodes は
// 出せない選択肢を出さないことを確かめる（ApiDesign.md 3.1）。
func TestLoginChallengeMethodsDependOnRecoveryCodes(t *testing.T) {
	t.Run("リカバリコードが残っている", func(t *testing.T) {
		q := newFake(t)
		withConfirmedTOTP(t, q)
		q.mfa.unusedCode = 3

		methods := methodsOf(t, loginWithMFA(q))
		if len(methods) != 2 || methods[0] != "totp" || methods[1] != "recovery_code" {
			t.Errorf("methods = %v, want [totp recovery_code]", methods)
		}
	})

	t.Run("1本も残っていない", func(t *testing.T) {
		q := newFake(t)
		withConfirmedTOTP(t, q)
		q.mfa.unusedCode = 0

		methods := methodsOf(t, loginWithMFA(q))
		if len(methods) != 1 || methods[0] != "totp" {
			t.Errorf("methods = %v, want [totp]", methods)
		}
	})
}

func methodsOf(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	view := viewOf(t, rec)
	raw, ok := view["methods"].([]any)
	if !ok {
		t.Fatalf("methods が無い: %v", view)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// TestLoginIssuesSessionWhenNoMFA は既存の経路が変わらないことを確かめる。
func TestLoginIssuesSessionWhenNoMFA(t *testing.T) {
	q := newFake(t) // mfa.confirmed は空

	rec := loginWithMFA(q)

	view := viewOf(t, rec)
	if _, ok := view["mfa_required"]; ok {
		t.Error("第2要素が無いのに挑戦を返した")
	}
	if _, ok := view["actor"]; !ok {
		t.Errorf("actor が無い: %v", view)
	}
	if len(q.mfa.challengesCreated) != 0 {
		t.Errorf("挑戦を %d 件作った", len(q.mfa.challengesCreated))
	}
}

// ── POST /auth/login/mfa（3.4）─────────────────────────────

func TestLoginMFASucceedsWithTOTP(t *testing.T) {
	q := newFake(t)
	secret := withConfirmedTOTP(t, q)
	q.mfa.challenge = challengeRow(mfaChallengeTTL)
	q.mfa.consumeChalRows = 1

	code, err := mfa.Code(secret, mfa.Step(time.Now()))
	if err != nil {
		t.Fatalf("コードを作れない: %v", err)
	}

	rec := postLoginMFA(q, fmt.Sprintf(`{"mfa_token":"pb_mfa_x","code":%q}`, code))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	// **応答は 3.1 の成功と同一構造である。**
	view := viewOf(t, rec)
	actor, ok := view["actor"].(map[string]any)
	if !ok {
		t.Fatalf("actor が無い: %v", view)
	}
	if actor["id"] != testActorID || actor["email"] != testEmail {
		t.Errorf("actor = %v", actor)
	}
	if view["expires_at"] == nil {
		t.Error("expires_at が null（セッションには必ず期限を設定する）")
	}

	// ここで初めて Cookie が出る。
	var session, csrf bool
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookieName:
			session = c.Value != ""
		case auth.CSRFCookieName:
			csrf = c.Value != ""
		}
	}
	if !session || !csrf {
		t.Errorf("Cookie が揃っていない（session=%v csrf=%v）", session, csrf)
	}

	// **挑戦を消費する**（同じ挑戦から2本のセッションを出さない）。
	if len(q.mfa.consumedChallenge) != 1 {
		t.Errorf("挑戦の消費が %d 回, want 1", len(q.mfa.consumedChallenge))
	}
	// **通った刻みを保存する**（再利用を拒むため）。
	if len(q.mfa.touchedUsed) != 1 {
		t.Errorf("刻みの保存が %d 回, want 1", len(q.mfa.touchedUsed))
	}
}

func TestLoginMFASucceedsWithRecoveryCode(t *testing.T) {
	q := newFake(t)
	withConfirmedTOTP(t, q)
	q.mfa.challenge = challengeRow(mfaChallengeTTL)
	q.mfa.consumeChalRows = 1
	q.mfa.consumeCodeRows = 1 // 未使用の1本が見つかった

	rec := postLoginMFA(q, `{"mfa_token":"pb_mfa_x","recovery_code":"k7m2q-x9b4t"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.consumedCodes) != 1 {
		t.Fatalf("コードの消費が %d 回, want 1", len(q.mfa.consumedCodes))
	}
	// **正規化してから照合する**（紙から写す値なので、見た目の差で落とさない）。
	want := auth.HashToken("K7M2QX9B4T")
	if got := q.mfa.consumedCodes[0].CodeHash; got != want {
		t.Errorf("小文字とハイフンが正規化されていない（hash が一致しない）")
	}
	// リカバリコードで通ったときは TOTP の刻みを触らない。
	if len(q.mfa.touchedUsed) != 0 {
		t.Errorf("リカバリコードなのに刻みを保存した")
	}
}

// TestLoginMFARejectsUsedRecoveryCode は同じコードを2回使えないことを確かめる。
func TestLoginMFARejectsUsedRecoveryCode(t *testing.T) {
	q := newFake(t)
	withConfirmedTOTP(t, q)
	q.mfa.challenge = challengeRow(mfaChallengeTTL)
	q.mfa.consumeCodeRows = 0 // 0行＝無い、または既に使った

	rec := postLoginMFA(q, `{"mfa_token":"pb_mfa_x","recovery_code":"K7M2QX9B4T"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.consumedChallenge) != 0 {
		t.Error("照合に失敗したのに挑戦を消費した")
	}
}

func TestLoginMFARejectsWrongCode(t *testing.T) {
	q := newFake(t)
	withConfirmedTOTP(t, q)
	q.mfa.challenge = challengeRow(mfaChallengeTTL)

	rec := postLoginMFA(q, `{"mfa_token":"pb_mfa_x","code":"000000"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "invalid_credentials" {
		t.Errorf("code = %q, want invalid_credentials", e.Code)
	}
	// **文言でコードの誤りだと分かること**（期限切れとは別の文面である）。
	if !strings.Contains(e.Message, "確認コード") {
		t.Errorf("message = %q", e.Message)
	}
	// **retry_after_sec を返さない**（アカウントをロックしない）。
	if e.RetryAfterSec != 0 {
		t.Errorf("retry_after_sec = %d, want 0", e.RetryAfterSec)
	}
	// 試行は数える。
	if q.mfa.challengeAttempts != 1 {
		t.Errorf("attempts = %d, want 1", q.mfa.challengeAttempts)
	}
	// **パスワード側の失敗回数は増やさない**（自分で自分を締め出さない）。
	if len(q.failures) != 0 {
		t.Errorf("local_credential の失敗が %d 回記録された", len(q.failures))
	}
	// セッションは出ない。
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			t.Error("照合に失敗したのにセッションを発行した")
		}
	}
}

// TestLoginMFARejectsExpiredChallenge は期限切れを確かめる（負の側）。
func TestLoginMFARejectsExpiredChallenge(t *testing.T) {
	q := newFake(t)
	secret := withConfirmedTOTP(t, q)
	q.mfa.challenge = challengeRow(-time.Second) // 既に切れている
	code, _ := mfa.Code(secret, mfa.Step(time.Now()))

	rec := postLoginMFA(q, fmt.Sprintf(`{"mfa_token":"pb_mfa_x","code":%q}`, code))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	// **正しいコードでも通らない。**
	if len(q.mfa.consumedChallenge) != 0 {
		t.Error("期限切れの挑戦を消費した")
	}
	if !strings.Contains(errorOf(t, rec).Message, "もう一度ログイン") {
		t.Errorf("やり直しの導線が文言に無い: %q", errorOf(t, rec).Message)
	}
}

func TestLoginMFARejectsConsumedChallenge(t *testing.T) {
	q := newFake(t)
	secret := withConfirmedTOTP(t, q)
	row := challengeRow(mfaChallengeTTL)
	row.ConsumedAt = ts(time.Now().Add(-time.Minute))
	q.mfa.challenge = row
	code, _ := mfa.Code(secret, mfa.Step(time.Now()))

	rec := postLoginMFA(q, fmt.Sprintf(`{"mfa_token":"pb_mfa_x","code":%q}`, code))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
}

// TestLoginMFARejectsAfterTooManyAttempts は試行上限を確かめる。
func TestLoginMFARejectsAfterTooManyAttempts(t *testing.T) {
	q := newFake(t)
	secret := withConfirmedTOTP(t, q)
	row := challengeRow(mfaChallengeTTL)
	row.Attempts = maxMFAChallengeAttempts
	q.mfa.challenge = row
	code, _ := mfa.Code(secret, mfa.Step(time.Now()))

	rec := postLoginMFA(q, fmt.Sprintf(`{"mfa_token":"pb_mfa_x","code":%q}`, code))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	// **正しいコードでも通らない**（挑戦そのものが死んでいる）。
	if len(q.mfa.consumedChallenge) != 0 {
		t.Error("試行上限に達した挑戦を消費した")
	}
}

func TestLoginMFARejectsUnknownChallenge(t *testing.T) {
	q := newFake(t)
	withConfirmedTOTP(t, q)
	q.mfa.challenge = nil // 該当する挑戦が無い

	rec := postLoginMFA(q, `{"mfa_token":"pb_mfa_nope","code":"123456"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
}

func TestLoginMFAValidatesInput(t *testing.T) {
	cases := map[string]string{
		"トークンが無い":    `{"code":"123456"}`,
		"どちらのコードも無い": `{"mfa_token":"pb_mfa_x"}`,
		"両方のコードが来た":  `{"mfa_token":"pb_mfa_x","code":"123456","recovery_code":"K7M2QX9B4T"}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			q := newFake(t)
			withConfirmedTOTP(t, q)
			q.mfa.challenge = challengeRow(mfaChallengeTTL)

			rec := postLoginMFA(q, body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			// **DB を触る前に落ちること。**
			if len(q.mfa.consumedCodes) != 0 || q.mfa.challengeAttempts != 0 {
				t.Error("検証に落ちたのに照合へ進んだ")
			}
		})
	}
}

// TestLoginMFARejectsInactiveActor は無効化されたアカウントを確かめる。
//
// **認証失敗と区別しない**（ApiDesign.md 3.1 に揃える）。
func TestLoginMFARejectsInactiveActor(t *testing.T) {
	q := newFake(t)
	secret := withConfirmedTOTP(t, q)
	row := challengeRow(mfaChallengeTTL)
	row.IsActive = false
	q.mfa.challenge = row
	code, _ := mfa.Code(secret, mfa.Step(time.Now()))

	rec := postLoginMFA(q, fmt.Sprintf(`{"mfa_token":"pb_mfa_x","code":%q}`, code))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	if errorOf(t, rec).Code != "invalid_credentials" {
		t.Errorf("code = %q, want invalid_credentials", errorOf(t, rec).Code)
	}
}
