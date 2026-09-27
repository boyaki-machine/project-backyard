package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// パスキー（ApiDesign.md 3.5 / 3.6 / 4.7 / 6.10）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - **登録したパスキーが本当にログインを通すこと。** 公開鍵（COSE_Key）とフラグを
//     列に分けて保存し、ログインで組み直している（DbDesign.md 6.19）。往復で
//     1バイトでも崩れれば署名の照合が落ちる
//   - credential_id の一意制約の名前が、実装の判定（isUniqueViolation）と合っていること
//   - 他人のパスキーが 404 になること（WHERE の user_id）
//   - purpose と user_id の CHECK が効いていること
//   - 管理者の全削除で行が消え、6.3 の passkey_count が 0 になること
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMePasskeysIntegration(t *testing.T) {
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

	newSelf := func(t *testing.T, label, role string) (string, string) {
		t.Helper()
		id := ulidgen.New()
		email := "passkey-" + label + "-" + uniq + "@example.com"
		seedUserWithRole(t, ctx, pool, q, id, email, role)
		return id, email
	}

	// register は 4.7.2 → 4.7.3 を通す。
	register := func(t *testing.T, token string, a *softAuthenticator, name string) *httptest.ResponseRecorder {
		t.Helper()
		opts := pkReadOptions(t, postWithCookie(r, "/api/v1/me/passkeys/options", token, ""))
		return postWithCookie(r, "/api/v1/me/passkeys", token,
			fmt.Sprintf(`{"name":%q,"credential":%s}`, name, a.register(testRPID, opts.Options.PublicKey.Challenge)))
	}

	// ── 登録した鍵がログインを通す ─────────────────────────
	t.Run("登録したパスキーでパスワード無しにログインできる", func(t *testing.T) {
		id, email := newSelf(t, "login", auth.SystemRoleOperator)
		token := loginAs(t, r, email)
		a := newSoftAuthenticator(t, id)
		a.backupEligible, a.backupState = true, true

		if rec := register(t, token, a, "MacBook"); rec.Code != http.StatusCreated {
			t.Fatalf("登録できない: status=%d body=%s", rec.Code, rec.Body.String())
		}

		opts := pkReadOptions(t, call(r, http.MethodPost, "/api/v1/auth/passkey/options", ""))
		assertion := a.assert(testRPID, opts.Options.PublicKey.Challenge)
		rec := call(r, http.MethodPost, "/api/v1/auth/login/passkey", `{"credential":`+assertion+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("パスキーでログインできない: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if c := cookieOf(rec, auth.SessionCookieName); c == nil || c.Value == "" {
			t.Error("成功したのにセッションが発行されていない")
		}

		rows, err := q.ListPasskeys(ctx, id)
		if err != nil || len(rows) != 1 {
			t.Fatalf("パスキーを読めない: rows=%d err=%v", len(rows), err)
		}
		if rows[0].LastUsedAt == nil {
			t.Error("last_used_at が書き戻されていない")
		}
		if !rows[0].BackupState {
			t.Error("backup_state が保存されていない")
		}

		// **同じ応答はもう通らない**（consumed_at IS NULL の条件）
		if rec2 := call(r, http.MethodPost, "/api/v1/auth/login/passkey", `{"credential":`+assertion+`}`); rec2.Code != http.StatusUnauthorized {
			t.Errorf("消費済みの挑戦が %d で通った", rec2.Code)
		}
	})

	// ── 一意制約 ───────────────────────────────────────────
	t.Run("同じ認証器と同じ名前の二重登録は 409", func(t *testing.T) {
		id, email := newSelf(t, "dup", auth.SystemRoleOperator)
		token := loginAs(t, r, email)
		a := newSoftAuthenticator(t, id)

		if rec := register(t, token, a, "MacBook"); rec.Code != http.StatusCreated {
			t.Fatalf("1件目を登録できない: status=%d body=%s", rec.Code, rec.Body.String())
		}

		// **同じ credential_id**——ブラウザは excludeCredentials で断るが、サーバは
		// 一意制約で断る。制約名が実装の判定と合っていなければ 500 になる。
		rec := register(t, token, a, "別の名前")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "登録済み") {
			t.Errorf("同じ認証器の二重登録: status=%d body=%s（期待 409「登録済み」）", rec.Code, rec.Body.String())
		}

		// **同じ名前**は挑戦を消費する前に断る
		b := newSoftAuthenticator(t, id)
		rec = register(t, token, b, "MacBook")
		if rec.Code != http.StatusConflict || errorOf(t, rec).Code != "already_exists" {
			t.Errorf("同じ名前の登録: status=%d body=%s（期待 409 already_exists）", rec.Code, rec.Body.String())
		}

		n, err := q.CountPasskeys(ctx, id)
		if err != nil || n != 1 {
			t.Errorf("件数 = %d（期待 1）err=%v", n, err)
		}
	})

	// ── 他人のパスキーは触れない ─────────────────────────────
	t.Run("他人のパスキーは削除できない（404）", func(t *testing.T) {
		ownerID, ownerEmail := newSelf(t, "owner", auth.SystemRoleOperator)
		_, otherEmail := newSelf(t, "other", auth.SystemRoleOperator)
		ownerToken := loginAs(t, r, ownerEmail)
		otherToken := loginAs(t, r, otherEmail)

		if rec := register(t, ownerToken, newSoftAuthenticator(t, ownerID), "所有者の端末"); rec.Code != http.StatusCreated {
			t.Fatalf("登録できない: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows, err := q.ListPasskeys(ctx, ownerID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("パスキーを読めない: rows=%d err=%v", len(rows), err)
		}
		path := "/api/v1/me/passkeys/" + rows[0].ID

		if rec := bodyWithCookie(r, http.MethodDelete, path, otherToken, "", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("他人のパスキーが %d で消せた（期待 404）: %s", rec.Code, rec.Body.String())
		}
		if n, _ := q.CountPasskeys(ctx, ownerID); n != 1 {
			t.Errorf("所有者のパスキーが %d 件（期待 1）", n)
		}

		// **本人なら消せる**（404 が「何も消せない」実装でないことを確かめる）
		if rec := bodyWithCookie(r, http.MethodDelete, path, ownerToken, "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("本人が消せない: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if n, _ := q.CountPasskeys(ctx, ownerID); n != 0 {
			t.Errorf("消したあとの件数が %d（期待 0）", n)
		}
	})

	// ── CHECK ─────────────────────────────────────────────
	t.Run("登録の挑戦には利用者が要り、ログインの挑戦には要らない（CHECK）", func(t *testing.T) {
		userID, _ := newSelf(t, "check", auth.SystemRoleOperator)
		expires := ts(time.Now().Add(time.Minute))

		cases := []struct {
			name    string
			purpose string
			user    pgtype.Text
		}{
			{"利用者の無い登録", webauthnPurposeRegister, pgtype.Text{}},
			{"利用者のあるログイン", webauthnPurposeLogin, pgtype.Text{String: userID, Valid: true}},
		}
		for _, c := range cases {
			err := q.CreateWebauthnChallenge(ctx, gen.CreateWebauthnChallengeParams{
				ID: ulidgen.New(), Purpose: c.purpose, UserID: c.user,
				Challenge: "check-" + ulidgen.New(), Session: []byte(`{}`), ExpiresAt: expires,
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("%s: err=%v（期待 23514 check_violation）", c.name, err)
			}
		}
	})

	// ── 管理者の全削除 ─────────────────────────────────────
	t.Run("管理者の全削除でパスキーが消え、件数が 0 になる", func(t *testing.T) {
		targetID, targetEmail := newSelf(t, "target", auth.SystemRoleOperator)
		targetToken := loginAs(t, r, targetEmail)
		if rec := register(t, targetToken, newSoftAuthenticator(t, targetID), "MacBook"); rec.Code != http.StatusCreated {
			t.Fatalf("登録できない: status=%d body=%s", rec.Code, rec.Body.String())
		}

		// **始点を測る**（Development.md 8.5）。0件から0件では何も確かめていない
		before, err := q.GetAdminUser(ctx, targetID)
		if err != nil || before.PasskeyCount != 1 {
			t.Fatalf("全削除の前の passkey_count = %d（期待 1）err=%v", before.PasskeyCount, err)
		}

		_, adminEmail := newSelf(t, "admin", auth.SystemRoleAdministrator)
		adminToken := loginAs(t, r, adminEmail)
		rec := postWithCookie(r, "/api/v1/admin/users/"+targetID+"/passkeys/reset", adminToken, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("全削除が %d（期待 204）: %s", rec.Code, rec.Body.String())
		}

		after, err := q.GetAdminUser(ctx, targetID)
		if err != nil || after.PasskeyCount != 0 {
			t.Errorf("全削除の後の passkey_count = %d（期待 0）err=%v", after.PasskeyCount, err)
		}

		// **パスワードでは入れる**（6.10 はパスワードに触らない）
		loginAs(t, r, targetEmail)
	})
}
