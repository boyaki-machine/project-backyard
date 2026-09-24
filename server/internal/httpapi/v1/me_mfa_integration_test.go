package v1

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/mfa"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 第2要素（ApiDesign.md 4.6 / 3.4）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - **登録した共有秘密が本当にログインを通すこと。** ハンドラ単体では
//     「封じて保存した」ところまでしか見えず、照合側が同じ鍵で開けるかは
//     実DBを通さないと分からない（アクセストークンの結合テストと同じ形）
//   - 未確定の行が一覧にも上限にもログインの分岐にも効かないこと
//     （部分索引と WHERE の条件が実際に効いているか）
//   - 他人の認証器が 404 になること（WHERE の user_id が効いているか）
//   - リカバリコードを2回は使えないこと（used_at IS NULL の条件）
//   - 最後の1件を消すとリカバリコードの行も実際に消えること
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMeMfaIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{
		Queries: q, Tx: store.NewTxRunner(pool), Settings: config.LiveDefaults(),
	})

	uniq := strings.ToLower(ulidgen.New())

	newSelf := func(t *testing.T, label string) (string, string) {
		t.Helper()
		id := ulidgen.New()
		email := "mfa-" + label + "-" + uniq + "@example.com"
		seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleOperator)
		return id, email
	}

	// startTOTP は登録を始め、共有秘密と id を返す。
	startTOTP := func(t *testing.T, token, name string) (string, string) {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/mfa/totp", token, `{"name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("登録を始められない: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		return got.ID, got.Secret
	}

	// confirmTOTP は確定させ、返ったリカバリコードを返す（初回以外は空）。
	confirmTOTP := func(t *testing.T, token, id, secret string) []string {
		t.Helper()
		code, err := mfa.Code(secret, mfa.Step(time.Now()))
		if err != nil {
			t.Fatalf("コードを作れない: %v", err)
		}
		rec := postWithCookie(r,
			"/api/v1/me/mfa/totp/"+id+"/confirm", token, `{"code":"`+code+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("確定できない: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got struct {
			RecoveryCodes []string `json:"recovery_codes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		return got.RecoveryCodes
	}

	// ── 登録した秘密がログインを通す ─────────────────────────
	t.Run("登録した共有秘密でログインの第2要素を通せる", func(t *testing.T) {
		_, email := newSelf(t, "login")
		token := loginAs(t, r, email)

		id, secret := startTOTP(t, token, "iPhone")
		codes := confirmTOTP(t, token, id, secret)
		if len(codes) != mfa.RecoveryCodeCount {
			t.Fatalf("リカバリコードが %d本（期待 %d）", len(codes), mfa.RecoveryCodeCount)
		}

		// **パスワードだけでは通らなくなる。**
		rec := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("ログインの1段目が %d（期待 200）: %s", rec.Code, rec.Body.String())
		}
		var challenge struct {
			MFARequired bool     `json:"mfa_required"`
			MFAToken    string   `json:"mfa_token"`
			Methods     []string `json:"methods"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &challenge); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if !challenge.MFARequired || challenge.MFAToken == "" {
			t.Fatalf("挑戦が返らなかった: %s", rec.Body.String())
		}
		// **セッション Cookie が出ていないこと。**
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookieName && c.Value != "" {
				t.Error("挑戦なのにセッションを発行した")
			}
		}

		// **同じ秘密から作ったコードで通る**（封じた値を開けている）。
		//
		// **次の刻みのコードを使う。** 確定（4.6.3）が現在の刻みを
		// last_used_step に保存しており、同じ刻みは再利用として拒まれる
		// （Design.md 6.7.2）。**+1 は許容窓（±1刻み）の内側**である。
		// **この足し算を省くとここで 401 になる**——実装ではなく検証の誤りである。
		code, err := mfa.Code(secret, mfa.Step(time.Now())+1)
		if err != nil {
			t.Fatalf("コードを作れない: %v", err)
		}
		rec2 := call(r, http.MethodPost, "/api/v1/auth/login/mfa",
			`{"mfa_token":"`+challenge.MFAToken+`","code":"`+code+`"}`)
		if rec2.Code != http.StatusOK {
			t.Fatalf("第2要素が通らない: status=%d body=%s", rec2.Code, rec2.Body.String())
		}
		var issued bool
		for _, c := range rec2.Result().Cookies() {
			if c.Name == auth.SessionCookieName && c.Value != "" {
				issued = true
			}
		}
		if !issued {
			t.Error("成功したのにセッションが発行されていない")
		}

		// **同じ挑戦は2回使えない**（consumed_at）。
		rec3 := call(r, http.MethodPost, "/api/v1/auth/login/mfa",
			`{"mfa_token":"`+challenge.MFAToken+`","code":"`+code+`"}`)
		if rec3.Code != http.StatusUnauthorized {
			t.Errorf("消費済みの挑戦が %d で通った", rec3.Code)
		}
	})

	// ── 未確定の行は何にも効かない ───────────────────────────
	t.Run("未確定の登録は一覧にもログインの分岐にも効かない", func(t *testing.T) {
		_, email := newSelf(t, "pending")
		token := loginAs(t, r, email)

		startTOTP(t, token, "確定しない端末")

		var overview struct {
			TOTP []struct {
				Name string `json:"name"`
			} `json:"totp"`
			RecoveryCodes *struct{} `json:"recovery_codes"`
		}
		rec := getWithCookie(r, "/api/v1/me/mfa", token)
		if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if len(overview.TOTP) != 0 {
			t.Errorf("未確定の登録が一覧に出た: %+v", overview.TOTP)
		}

		// **パスワードだけで入れるまま**（ログインの分岐に効かない）。
		rec2 := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if strings.Contains(rec2.Body.String(), "mfa_required") {
			t.Errorf("未確定の登録でログインが第2要素を求めた: %s", rec2.Body.String())
		}

		// **同じ名前で始め直せる**（部分 UNIQUE が確定済みだけに掛かっている）。
		startTOTP(t, token, "確定しない端末")
	})

	// ── 他人の認証器は触れない ───────────────────────────────
	t.Run("他人の認証器は削除できない（404）", func(t *testing.T) {
		_, ownerEmail := newSelf(t, "owner")
		_, otherEmail := newSelf(t, "other")
		ownerToken := loginAs(t, r, ownerEmail)
		otherToken := loginAs(t, r, otherEmail)

		id, secret := startTOTP(t, ownerToken, "所有者の端末")
		confirmTOTP(t, ownerToken, id, secret)

		rec := bodyWithCookie(r, http.MethodDelete, "/api/v1/me/mfa/totp/"+id, otherToken, "", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("他人の認証器が %d で消せた（期待 404）: %s", rec.Code, rec.Body.String())
		}

		// **実際に残っていること**（404 を返しつつ消していないか）。
		n, err := q.CountConfirmedMfaCredentials(ctx, mfaOwnerID(t, ctx, pool, ownerEmail))
		if err != nil {
			t.Fatalf("件数を読めない: %v", err)
		}
		if n != 1 {
			t.Errorf("所有者の認証器が %d 件（期待 1）", n)
		}
	})

	// ── リカバリコードは1本1回 ──────────────────────────────
	t.Run("リカバリコードは2回使えない", func(t *testing.T) {
		_, email := newSelf(t, "recovery")
		token := loginAs(t, r, email)

		id, secret := startTOTP(t, token, "端末")
		codes := confirmTOTP(t, token, id, secret)

		use := func(code string) int {
			rec := call(r, http.MethodPost, "/api/v1/auth/login",
				`{"email":"`+email+`","password":"`+testPassword+`"}`)
			var ch struct {
				MFAToken string `json:"mfa_token"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
				t.Fatalf("応答を読めない: %v", err)
			}
			return call(r, http.MethodPost, "/api/v1/auth/login/mfa",
				`{"mfa_token":"`+ch.MFAToken+`","recovery_code":"`+code+`"}`).Code
		}

		// **通る側を先に見る**（Development.md 8.5）。
		if got := use(codes[0]); got != http.StatusOK {
			t.Fatalf("1回目が %d（期待 200）", got)
		}
		if got := use(codes[0]); got != http.StatusUnauthorized {
			t.Errorf("2回目が %d（期待 401）", got)
		}
		// 別の1本はまだ使える。
		if got := use(codes[1]); got != http.StatusOK {
			t.Errorf("別のコードが %d（期待 200）", got)
		}
	})

	// ── 最後の1件を消すとコードも消える ─────────────────────
	t.Run("最後の認証器を消すとリカバリコードの行も消える", func(t *testing.T) {
		actorID, email := newSelf(t, "last")
		token := loginAs(t, r, email)

		id1, secret1 := startTOTP(t, token, "1台目")
		confirmTOTP(t, token, id1, secret1)
		id2, secret2 := startTOTP(t, token, "2台目")
		confirmTOTP(t, token, id2, secret2)

		remaining, err := q.CountUnusedRecoveryCodes(ctx, actorID)
		if err != nil {
			t.Fatalf("残数を読めない: %v", err)
		}
		if remaining != int64(mfa.RecoveryCodeCount) {
			t.Fatalf("残数が %d（期待 %d）", remaining, mfa.RecoveryCodeCount)
		}

		// 1台目を消しても残る。
		if rec := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/mfa/totp/"+id1, token, "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("削除が %d: %s", rec.Code, rec.Body.String())
		}
		if n, _ := q.CountUnusedRecoveryCodes(ctx, actorID); n != int64(mfa.RecoveryCodeCount) {
			t.Errorf("まだ認証器が残っているのに残数が %d へ減った", n)
		}

		// 最後の1件を消すと消える。
		if rec := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/mfa/totp/"+id2, token, "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("削除が %d: %s", rec.Code, rec.Body.String())
		}
		if n, _ := q.CountUnusedRecoveryCodes(ctx, actorID); n != 0 {
			t.Errorf("最後の1件を消したのにリカバリコードが %d 本残っている", n)
		}

		// **パスワードだけで入れる状態に戻る。**
		rec := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if strings.Contains(rec.Body.String(), "mfa_required") {
			t.Errorf("解除後もログインが第2要素を求めた: %s", rec.Body.String())
		}
	})

	// ── 共有秘密が平文で保存されていない ────────────────────
	t.Run("共有秘密は平文で保存されない", func(t *testing.T) {
		_, email := newSelf(t, "sealed")
		token := loginAs(t, r, email)
		id, secret := startTOTP(t, token, "端末")

		var stored []byte
		if err := pool.QueryRow(ctx,
			`SELECT secret FROM user_mfa_credential WHERE id = $1`, id).Scan(&stored); err != nil {
			t.Fatalf("行を読めない: %v", err)
		}
		if strings.Contains(string(stored), secret) {
			t.Error("共有秘密が平文で保存されている")
		}
		// Base32 の素の値でもない（別の符号で入っていないか）。
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
		if err == nil && strings.Contains(string(stored), string(raw)) {
			t.Error("共有秘密が復号鍵なしで読める形で保存されている")
		}
	})
}

// mfaOwnerID はメールアドレスから actor_id を引く（検算に使う）。
func mfaOwnerID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT actor_id FROM app_user WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("actor_id を引けない: %v", err)
	}
	return id
}
