package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/mfa"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
)

// 第2要素の管理（ApiDesign.md 4.6）のハンドラ単体テスト。
//
// 認証はミドルウェアの責務なので通さない（routes_test.go が別に見ている）。
// ここで確かめるのは**応答の形・検証の分岐・書き込みに何を渡すか**である。
// 実DBでしか確かめられないもの（部分 UNIQUE・他人の行・CHECK）は
// me_mfa_integration_test.go にある。

// ── 応答を読むための型 ──────────────────────────────────────
//
// **応答の型をそのまま使わない。** v1.Time は MarshalJSON だけを持つ
// （me_tokens_test.go と同じ理由）。
type mfaOverviewJSON struct {
	TOTP []struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	} `json:"totp"`
	RecoveryCodes *struct {
		Remaining   int    `json:"remaining"`
		GeneratedAt string `json:"generated_at"`
	} `json:"recovery_codes"`
}

type startedTOTPJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Secret     string `json:"secret"`
	OtpauthURI string `json:"otpauth_uri"`
	Digits     int    `json:"digits"`
	PeriodSec  int    `json:"period_sec"`
}

type confirmedTOTPJSON struct {
	Credential struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"credential"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// newMFAHandler は第2要素のハンドラを作る。
//
// **settings を入れる。** h.secretKey が第1層（PB_SECRET_KEY）を読むため、
// nil のままでは落ちる——Mount は LiveDefaults を入れるが、ここは
// ハンドラを直接組み立てている。
func newMFAHandler(q *fakeQuerier) (*handler, *fakeTxRunner) {
	tx := &fakeTxRunner{q: q}
	return &handler{q: q, tx: tx, settings: config.LiveDefaults()}, tx
}

// mfaFake は 4.6 のテストが使う既定のフェイク。
//
// **「通る」状態を既定にして、テストごとに1つだけ崩す**（tokenFake と同じ型）。
func mfaFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.profileRow = meProfileRow()
	q.mfa.deletedRows = 1
	return q
}

// sealedSecret は封じた共有秘密を作る。**テストも本番と同じ鍵と方式を通す。**
func sealedSecret(t *testing.T, secret string) (ciphertext, nonce []byte) {
	t.Helper()
	ct, n, err := tlscert.Seal(testSecretKey, secret)
	if err != nil {
		t.Fatalf("共有秘密を封じられない: %v", err)
	}
	return ct, n
}

// ── GET /me/mfa（4.6.1）────────────────────────────────────

func TestGetMyMfaReturnsCredentialsWithoutSecret(t *testing.T) {
	now := time.Now()
	q := mfaFake(t)
	q.mfa.confirmed = []gen.ListConfirmedMfaCredentialsRow{
		{ID: "01K2MFA00000000000000001", Name: "iPhone", Kind: "totp",
			CreatedAt: ts(now.Add(-time.Hour)), LastUsedAt: ts(now.Add(-time.Minute))},
	}
	q.mfa.recovery = &gen.GetRecoveryCodeStatusRow{
		Remaining: 8, GeneratedAt: ts(now.Add(-time.Hour)),
	}

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.getMyMfa(rec, tokenReq(http.MethodGet, "/api/v1/me/mfa", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	var got mfaOverviewJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(got.TOTP) != 1 || got.TOTP[0].Name != "iPhone" {
		t.Fatalf("totp = %+v", got.TOTP)
	}
	if got.RecoveryCodes == nil || got.RecoveryCodes.Remaining != 8 {
		t.Errorf("recovery_codes = %+v, want remaining=8", got.RecoveryCodes)
	}

	// **共有秘密が応答に現れないこと。** 4.6.1 が返すのは名前と日時だけである。
	for _, word := range []string{"secret", "otpauth"} {
		if strings.Contains(rec.Body.String(), word) {
			t.Errorf("一覧に %q が含まれている: %s", word, rec.Body.String())
		}
	}
}

// TestGetMyMfaDistinguishesNoCodesFromZeroRemaining は
// 「1本も作っていない」と「作って全部使った」を区別することを確かめる（4.6.1）。
func TestGetMyMfaDistinguishesNoCodesFromZeroRemaining(t *testing.T) {
	t.Run("1本も作っていない → null", func(t *testing.T) {
		q := mfaFake(t)
		q.mfa.recovery = nil // GetRecoveryCodeStatus が行を返さない

		h, _ := newMFAHandler(q)
		rec := httptest.NewRecorder()
		h.getMyMfa(rec, tokenReq(http.MethodGet, "/api/v1/me/mfa", "", "", selfPrincipal()))

		var got mfaOverviewJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if got.RecoveryCodes != nil {
			t.Errorf("recovery_codes = %+v, want null", got.RecoveryCodes)
		}
	})

	t.Run("作って全部使った → remaining 0", func(t *testing.T) {
		q := mfaFake(t)
		q.mfa.recovery = &gen.GetRecoveryCodeStatusRow{Remaining: 0, GeneratedAt: ts(time.Now())}

		h, _ := newMFAHandler(q)
		rec := httptest.NewRecorder()
		h.getMyMfa(rec, tokenReq(http.MethodGet, "/api/v1/me/mfa", "", "", selfPrincipal()))

		var got mfaOverviewJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if got.RecoveryCodes == nil {
			t.Fatal("recovery_codes = null, want remaining=0（別の状態である）")
		}
		if got.RecoveryCodes.Remaining != 0 {
			t.Errorf("remaining = %d, want 0", got.RecoveryCodes.Remaining)
		}
	})
}

// ── POST /me/mfa/totp（4.6.2）──────────────────────────────

func TestStartMyTotpReturnsSecretAndURI(t *testing.T) {
	q := mfaFake(t)

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.startMyTotp(rec, tokenReq(http.MethodPost, "/api/v1/me/mfa/totp",
		`{"name":"iPhone"}`, "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}

	var got startedTOTPJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if got.Digits != mfa.Digits || got.PeriodSec != mfa.PeriodSec {
		t.Errorf("digits/period = %d/%d, want %d/%d",
			got.Digits, got.PeriodSec, mfa.Digits, mfa.PeriodSec)
	}
	// **secret と otpauth_uri は同じ値の2つの表現である**（4.6.2）。
	if !strings.Contains(got.OtpauthURI, got.Secret) {
		t.Errorf("otpauth_uri に secret が入っていない\nuri=%s\nsecret=%s",
			got.OtpauthURI, got.Secret)
	}
	if !strings.HasPrefix(got.OtpauthURI, "otpauth://totp/") {
		t.Errorf("otpauth_uri = %q", got.OtpauthURI)
	}
	// 認証アプリの一覧で本人が見分けるので、メールアドレスが載る。
	if !strings.Contains(got.OtpauthURI, "tanaka") {
		t.Errorf("otpauth_uri にアカウント名が無い: %q", got.OtpauthURI)
	}

	// **未確定の行を先に捨てる**（途中の行は1人1件まで）。
	if len(q.mfa.pendingDeleted) != 1 {
		t.Errorf("未確定の削除が %d 回, want 1", len(q.mfa.pendingDeleted))
	}
	if len(q.mfa.created) != 1 {
		t.Fatalf("作成が %d 回, want 1", len(q.mfa.created))
	}
	created := q.mfa.created[0]
	if created.Kind != mfaKindTOTP || created.Name != "iPhone" {
		t.Errorf("created = %+v", created)
	}
	// **平文を DB へ渡していないこと。** 封じた値と nonce が入る。
	if strings.Contains(string(created.Secret), got.Secret) {
		t.Error("共有秘密が平文のまま保存されようとしている")
	}
	if len(created.SecretNonce) == 0 {
		t.Error("nonce が空である")
	}
	// 封じた値が復号できること（鍵と方式が揃っている）。
	plain, err := tlscert.Open(testSecretKey, created.Secret, created.SecretNonce)
	if err != nil {
		t.Fatalf("保存しようとした値を復号できない: %v", err)
	}
	if plain != got.Secret {
		t.Errorf("復号した値が応答と違う（%q と %q）", plain, got.Secret)
	}
}

func TestStartMyTotpRejectsBadName(t *testing.T) {
	cases := map[string]string{
		"空":     `{"name":""}`,
		"空白のみ":  `{"name":"   "}`,
		"61文字":  fmt.Sprintf(`{"name":%q}`, strings.Repeat("あ", 61)),
		"項目が無い": `{}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			q := mfaFake(t)
			h, _ := newMFAHandler(q)
			rec := httptest.NewRecorder()
			h.startMyTotp(rec, tokenReq(http.MethodPost, "/api/v1/me/mfa/totp",
				body, "", selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			// **作りかけの行を残さない。**
			if len(q.mfa.created) != 0 {
				t.Errorf("検証に落ちたのに %d 件作られた", len(q.mfa.created))
			}
		})
	}
}

// TestStartMyTotpRejectsOverLimit は上限（確定済み5件）を確かめる。
//
// **負の側である。** 通る側だけでは上限が効いている証拠にならない。
func TestStartMyTotpRejectsOverLimit(t *testing.T) {
	q := mfaFake(t)
	for i := range maxMFACredentialsPerUser {
		q.mfa.confirmed = append(q.mfa.confirmed, gen.ListConfirmedMfaCredentialsRow{
			ID: fmt.Sprintf("01K2MFA0000000000000000%d", i), Name: fmt.Sprintf("端末%d", i),
			Kind: "totp", CreatedAt: ts(time.Now()),
		})
	}

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.startMyTotp(rec, tokenReq(http.MethodPost, "/api/v1/me/mfa/totp",
		`{"name":"6件目"}`, "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	// **押せない理由が読めること**（画面がそのまま出す文言である）。
	if !strings.Contains(rec.Body.String(), "5件") {
		t.Errorf("上限の本数が文言に無い: %s", rec.Body.String())
	}
	if len(q.mfa.created) != 0 {
		t.Errorf("上限を超えて %d 件作られた", len(q.mfa.created))
	}
}

func TestStartMyTotpRejectsDuplicateName(t *testing.T) {
	q := mfaFake(t)
	q.mfa.nameTaken = true

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.startMyTotp(rec, tokenReq(http.MethodPost, "/api/v1/me/mfa/totp",
		`{"name":"iPhone"}`, "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if code := errorOf(t, rec).Code; code != "already_exists" {
		t.Errorf("code = %q, want already_exists", code)
	}
}

// ── POST /me/mfa/totp/{id}/confirm（4.6.3）─────────────────

// pendingWithSecret は「QR を出した直後」のフェイクを組む。
func pendingWithSecret(t *testing.T, q *fakeQuerier, id, secret string) {
	t.Helper()
	ct, nonce := sealedSecret(t, secret)
	q.mfa.pending = &gen.FindPendingMfaCredentialRow{
		ID: id, Name: "iPhone", Secret: ct, SecretNonce: nonce,
	}
}

func TestConfirmMyTotpIssuesRecoveryCodesOnFirstCredential(t *testing.T) {
	const id = "01K2MFA00000000000000001"
	secret, err := mfa.NewSecret()
	if err != nil {
		t.Fatalf("共有秘密を作れない: %v", err)
	}
	code, err := mfa.Code(secret, mfa.Step(time.Now()))
	if err != nil {
		t.Fatalf("コードを作れない: %v", err)
	}

	q := mfaFake(t)
	pendingWithSecret(t, q, id, secret)

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.confirmMyTotp(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/totp/"+id+"/confirm",
		fmt.Sprintf(`{"code":%q}`, code), id, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	var got confirmedTOTPJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if got.Credential.ID != id {
		t.Errorf("credential.id = %q, want %q", got.Credential.ID, id)
	}
	if len(got.RecoveryCodes) != mfa.RecoveryCodeCount {
		t.Fatalf("recovery_codes = %d本, want %d", len(got.RecoveryCodes), mfa.RecoveryCodeCount)
	}
	// **保存されるのはハッシュだけである**（平文は応答にしかない）。
	if len(q.mfa.createdCodes) != mfa.RecoveryCodeCount {
		t.Fatalf("保存が %d 件, want %d", len(q.mfa.createdCodes), mfa.RecoveryCodeCount)
	}
	for _, saved := range q.mfa.createdCodes {
		for _, plain := range got.RecoveryCodes {
			if saved.CodeHash == plain {
				t.Fatalf("リカバリコードが平文で保存されようとしている: %q", plain)
			}
		}
	}

	// **確定したときに、通った刻みを保存する**（再利用を拒むため）。
	if len(q.mfa.confirmCalls) != 1 {
		t.Fatalf("確定が %d 回, want 1", len(q.mfa.confirmCalls))
	}
	if !q.mfa.confirmCalls[0].LastUsedStep.Valid {
		t.Error("last_used_step が入っていない")
	}
}

// TestConfirmMyTotpSkipsRecoveryCodesForSecondCredential は
// 2件目で返さないことを確かめる（4.6.3）。
func TestConfirmMyTotpSkipsRecoveryCodesForSecondCredential(t *testing.T) {
	const id = "01K2MFA00000000000000002"
	secret, _ := mfa.NewSecret()
	code, _ := mfa.Code(secret, mfa.Step(time.Now()))

	q := mfaFake(t)
	pendingWithSecret(t, q, id, secret)
	// 既に1件が確定済み（＝初回ではない）。
	q.mfa.confirmed = []gen.ListConfirmedMfaCredentialsRow{
		{ID: "01K2MFA00000000000000001", Name: "iPad", Kind: "totp", CreatedAt: ts(time.Now())},
	}

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.confirmMyTotp(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/totp/"+id+"/confirm",
		fmt.Sprintf(`{"code":%q}`, code), id, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var got confirmedTOTPJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(got.RecoveryCodes) != 0 {
		t.Errorf("2件目で recovery_codes が %d本 返った", len(got.RecoveryCodes))
	}
	if len(q.mfa.createdCodes) != 0 {
		t.Errorf("2件目でコードが %d件 作られた（既存が無効になる）", len(q.mfa.createdCodes))
	}
}

func TestConfirmMyTotpRejectsWrongCode(t *testing.T) {
	const id = "01K2MFA00000000000000001"
	secret, _ := mfa.NewSecret()

	q := mfaFake(t)
	pendingWithSecret(t, q, id, secret)

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.confirmMyTotp(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/totp/"+id+"/confirm", `{"code":"000000"}`, id, selfPrincipal()))

	// **401 ではなく 422**（既にセッションを持つ本人の入力誤りである）。
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	details := errorOf(t, rec).Details
	if len(details) == 0 || details[0].Field != "code" {
		t.Errorf("details = %+v, want field=code", details)
	}
	if len(q.mfa.confirmCalls) != 0 {
		t.Error("誤ったコードで確定された")
	}
}

// TestConfirmMyTotpDiscardsPendingAfterTooManyFailures は5回で捨てることを
// 確かめる（総当たりに同じ共有秘密を何度も使わせない）。
func TestConfirmMyTotpDiscardsPendingAfterTooManyFailures(t *testing.T) {
	const id = "01K2MFA00000000000000001"
	secret, _ := mfa.NewSecret()

	q := mfaFake(t)
	pendingWithSecret(t, q, id, secret)
	h, _ := newMFAHandler(q)

	var last *httptest.ResponseRecorder
	for range maxConfirmAttempts {
		last = httptest.NewRecorder()
		h.confirmMyTotp(last, tokenReq(http.MethodPost,
			"/api/v1/me/mfa/totp/"+id+"/confirm", `{"code":"000000"}`, id, selfPrincipal()))
	}

	if last.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", last.Code, last.Body.String())
	}
	if !strings.Contains(last.Body.String(), "やり直して") {
		t.Errorf("やり直しを促す文言が無い: %s", last.Body.String())
	}
	// **未確定の行を捨てている。**
	if len(q.mfa.pendingDeleted) != 1 {
		t.Errorf("未確定の削除が %d 回, want 1", len(q.mfa.pendingDeleted))
	}
}

func TestConfirmMyTotpReturns404ForUnknownPending(t *testing.T) {
	q := mfaFake(t)
	q.mfa.pending = nil // 他人のもの・存在しない・既に確定済み

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.confirmMyTotp(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/totp/01K2NOPE00000000000000001/confirm",
		`{"code":"123456"}`, "01K2NOPE00000000000000001", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── DELETE /me/mfa/totp/{id}（4.6.4）───────────────────────

func TestDeleteMyTotpRemovesRecoveryCodesWithLastCredential(t *testing.T) {
	const id = "01K2MFA00000000000000001"
	q := mfaFake(t)
	q.mfa.codesDeletedRows = 8
	// 削除後に確定済みが0件（confirmed は空のまま）。

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyTotp(rec, tokenReq(http.MethodDelete,
		"/api/v1/me/mfa/totp/"+id, "", id, selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.codesDeletedFor) != 1 {
		t.Errorf("リカバリコードの削除が %d 回, want 1（最後の1件だった）",
			len(q.mfa.codesDeletedFor))
	}
}

func TestDeleteMyTotpKeepsRecoveryCodesWhenOthersRemain(t *testing.T) {
	const id = "01K2MFA00000000000000001"
	q := mfaFake(t)
	// 削除後も1件残る。
	q.mfa.confirmed = []gen.ListConfirmedMfaCredentialsRow{
		{ID: "01K2MFA00000000000000002", Name: "iPad", Kind: "totp", CreatedAt: ts(time.Now())},
	}

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyTotp(rec, tokenReq(http.MethodDelete,
		"/api/v1/me/mfa/totp/"+id, "", id, selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.codesDeletedFor) != 0 {
		t.Errorf("まだ認証器が残っているのにリカバリコードを消した")
	}
}

func TestDeleteMyTotpReturns404WhenNoRowDeleted(t *testing.T) {
	q := mfaFake(t)
	q.mfa.deletedRows = 0 // 他人のもの・存在しない・未確定

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyTotp(rec, tokenReq(http.MethodDelete,
		"/api/v1/me/mfa/totp/01K2NOPE00000000000000001", "",
		"01K2NOPE00000000000000001", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.codesDeletedFor) != 0 {
		t.Error("消せていないのにリカバリコードを消した")
	}
}

// ── POST /me/mfa/recovery-codes（4.6.5）────────────────────

func TestRegenerateRecoveryCodesReplacesAll(t *testing.T) {
	q := mfaFake(t)
	q.mfa.confirmed = []gen.ListConfirmedMfaCredentialsRow{
		{ID: "01K2MFA00000000000000001", Name: "iPhone", Kind: "totp", CreatedAt: ts(time.Now())},
	}
	q.mfa.codesDeletedRows = 3

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.regenerateMyRecoveryCodes(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/recovery-codes", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var got struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(got.RecoveryCodes) != mfa.RecoveryCodeCount {
		t.Errorf("recovery_codes = %d本, want %d", len(got.RecoveryCodes), mfa.RecoveryCodeCount)
	}
	// **古いものを先に消す**（未使用も含めて全部無効になる）。
	if len(q.mfa.codesDeletedFor) != 1 {
		t.Errorf("古いコードの削除が %d 回, want 1", len(q.mfa.codesDeletedFor))
	}
	if len(q.mfa.createdCodes) != mfa.RecoveryCodeCount {
		t.Errorf("保存が %d 件, want %d", len(q.mfa.createdCodes), mfa.RecoveryCodeCount)
	}
}

// TestRegenerateRecoveryCodesRejectsWithoutCredential は
// 認証器が無いときに 409 を返すことを確かめる（4.6.5）。
func TestRegenerateRecoveryCodesRejectsWithoutCredential(t *testing.T) {
	q := mfaFake(t) // confirmed は空

	h, _ := newMFAHandler(q)
	rec := httptest.NewRecorder()
	h.regenerateMyRecoveryCodes(rec, tokenReq(http.MethodPost,
		"/api/v1/me/mfa/recovery-codes", "", "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.mfa.createdCodes) != 0 {
		t.Errorf("認証器が無いのに %d 件作られた", len(q.mfa.createdCodes))
	}
}
