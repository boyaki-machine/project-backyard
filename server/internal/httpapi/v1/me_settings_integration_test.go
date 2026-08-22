package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 自分自身に関するAPI（ApiDesign.md 4.2 / 4.3）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - メール変更に user_identity.subject が追随し、**新しいメールでログインできる**こと
//     （追随しなければ FindLocalLoginByEmail の結合が外れて弾かれる）
//   - app_user の UNIQUE 制約が本当に 409 already_exists に写ること
//   - パスワード変更で**他のセッションだけが 401 になり、現在のセッションは残る**こと
//   - must_change が false に落ちること（GET /me の must_change_password で見る）
//   - version が加算され、管理者側（6.4）の If-Match が壊れないこと
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestMeSettingsIntegration(t *testing.T) {
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
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	uniq := strings.ToLower(ulidgen.New())

	// newSelf は自分自身として使うユーザーを1人作り、ID とメールを返す。
	// **テストごとに作る。** 状態を変える検証が互いの前提を壊さないようにする。
	newSelf := func(t *testing.T, label string) (string, string) {
		t.Helper()
		id := ulidgen.New()
		email := "me-" + label + "-" + uniq + "@example.com"
		seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleOperator)
		return id, email
	}

	// ── PATCH /me（4.2）────────────────────────────────────────

	t.Run("プロフィールと見た目を更新すると応答が更新後の値になる", func(t *testing.T) {
		_, email := newSelf(t, "profile")
		session := loginAs(t, r, email)

		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"display_name":"更新後の名前","timezone":"UTC","theme":"dark","hue":"green"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}

		actor := actorOf(t, rec)
		if actor["display_name"] != "更新後の名前" {
			t.Errorf("display_name = %v", actor["display_name"])
		}
		if actor["timezone"] != "UTC" {
			t.Errorf("timezone = %v, want UTC", actor["timezone"])
		}
		if actor["theme"] != "dark" || actor["hue"] != "green" {
			t.Errorf("theme/hue = %v/%v, want dark/green", actor["theme"], actor["hue"])
		}

		// **GET /me でも同じ値が返ること。** 応答だけ書き換えて DB に
		// 入っていない、という取り違えを防ぐ。
		got := getWithCookie(r, "/api/v1/me", session)
		if got.Code != http.StatusOK {
			t.Fatalf("GET /me status = %d（body=%s）", got.Code, got.Body.String())
		}
		reread := actorOf(t, got)
		if reread["theme"] != "dark" || reread["hue"] != "green" {
			t.Errorf("読み直すと theme/hue = %v/%v", reread["theme"], reread["hue"])
		}
	})

	// **これが 4.2 の要点である。** subject を追随させなければ、
	// メールを変えた本人がログインできなくなる。
	t.Run("メールを変えると新しいメールでログインできる", func(t *testing.T) {
		_, email := newSelf(t, "email")
		session := loginAs(t, r, email)
		newEmail := "me-email-new-" + uniq + "@example.com"

		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"email":"`+newEmail+`"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if got := actorOf(t, rec)["email"]; got != newEmail {
			t.Errorf("email = %v, want %s", got, newEmail)
		}

		// 新しいメールで入れる。
		if token := loginAs(t, r, newEmail); token == "" {
			t.Fatal("新しいメールでログインできない")
		}
		// 古いメールでは入れない。
		old := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if old.Code != http.StatusUnauthorized {
			t.Errorf("古いメールでのログイン status = %d, want 401", old.Code)
		}
	})

	t.Run("他人が使っているメールは409", func(t *testing.T) {
		_, email := newSelf(t, "dup-self")
		_, otherEmail := newSelf(t, "dup-other")
		session := loginAs(t, r, email)

		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"email":"`+otherEmail+`"}`, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		if e := errorOf(t, rec); e.Code != "already_exists" {
			t.Errorf("error.code = %q, want already_exists", e.Code)
		}
	})

	// version を加算することで、管理者側（6.4）の If-Match が壊れないことを見る。
	t.Run("versionが加算される", func(t *testing.T) {
		id, email := newSelf(t, "version")
		session := loginAs(t, r, email)

		before := appUserVersion(t, pool, id)
		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"theme":"light"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if after := appUserVersion(t, pool, id); after != before+1 {
			t.Errorf("version = %d, want %d", after, before+1)
		}
	})

	t.Run("If-Matchを要求しない", func(t *testing.T) {
		_, email := newSelf(t, "nomatch")
		session := loginAs(t, r, email)

		// **ヘッダを付けずに 200 になること**（4.2）。6.4 は 422 になる。
		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"theme":"light"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// ── POST /me/password（4.3）────────────────────────────────

	// **これが 4.3 の要点である。** 現在のセッションを残しつつ他だけを切る。
	t.Run("パスワード変更で他のセッションだけが失効する", func(t *testing.T) {
		_, email := newSelf(t, "pw")

		// **2本のセッションを作ってから測る。** 「戻る」ことではなく
		// 「片方だけ切れる」ことを見るので、始点を自分で作る必要がある。
		keep := loginAs(t, r, email)
		other := loginAs(t, r, email)

		// 変更前はどちらも通ること（始点の確認）。
		for name, tok := range map[string]string{"keep": keep, "other": other} {
			if rec := getWithCookie(r, "/api/v1/me", tok); rec.Code != http.StatusOK {
				t.Fatalf("変更前の %s が通らない: status = %d", name, rec.Code)
			}
		}

		newPassword := "changed-passphrase-1"
		rec := bodyWithCookie(r, http.MethodPost, "/api/v1/me/password", keep,
			`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}

		// 操作したセッションは残る。
		if got := getWithCookie(r, "/api/v1/me", keep); got.Code != http.StatusOK {
			t.Errorf("操作したセッションが切れた: status = %d", got.Code)
		}
		// もう一方は 401 になる。
		if got := getWithCookie(r, "/api/v1/me", other); got.Code != http.StatusUnauthorized {
			t.Errorf("他のセッションが残っている: status = %d", got.Code)
		}
		// 新しいパスワードで入れる。
		if token := loginAsWith(t, r, email, newPassword); token == "" {
			t.Fatal("新しいパスワードでログインできない")
		}
		// 古いパスワードでは入れない。
		old := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if old.Code != http.StatusUnauthorized {
			t.Errorf("古いパスワードでのログイン status = %d, want 401", old.Code)
		}
	})

	t.Run("現在のパスワードが違うと401で何も変わらない", func(t *testing.T) {
		_, email := newSelf(t, "pw-wrong")
		session := loginAs(t, r, email)

		rec := bodyWithCookie(r, http.MethodPost, "/api/v1/me/password", session,
			`{"current_password":"totally-wrong-1","new_password":"changed-passphrase-1"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
		}
		if e := errorOf(t, rec); e.Code != "invalid_credentials" {
			t.Errorf("error.code = %q, want invalid_credentials", e.Code)
		}
		// **元のパスワードのまま入れること。** 失敗が副作用を残していない。
		if token := loginAsWith(t, r, email, testPassword); token == "" {
			t.Fatal("失敗後に元のパスワードで入れない")
		}
	})

	// **失敗を failed_attempts に数えない**（4.3）。数えると自分で自分を
	// 締め出せる。5回失敗させてから、元のパスワードで入れることを見る。
	t.Run("失敗を重ねてもアカウントがロックされない", func(t *testing.T) {
		_, email := newSelf(t, "pw-lock")
		session := loginAs(t, r, email)

		for i := 0; i < 5; i++ {
			rec := bodyWithCookie(r, http.MethodPost, "/api/v1/me/password", session,
				`{"current_password":"totally-wrong-1","new_password":"changed-passphrase-1"}`, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%d回目の status = %d, want 401（body=%s）", i+1, rec.Code, rec.Body.String())
			}
		}

		// Design.md 6.3 のロックは「5回連続で15分」。数えていれば
		// ここで 423 account_locked になる。
		rec := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+testPassword+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("5回失敗した後のログイン status = %d, want 200（body=%s）",
				rec.Code, rec.Body.String())
		}
	})

	// 要パスワード変更の解除（4.3）。GET /me の must_change_password で見る。
	t.Run("変更するとmust_change_passwordが下りる", func(t *testing.T) {
		_, email := newSelf(t, "mustchange")
		setMustChange(t, pool, email, true)

		session := loginAs(t, r, email)
		before := getWithCookie(r, "/api/v1/me", session)
		if before.Code != http.StatusOK {
			t.Fatalf("GET /me status = %d", before.Code)
		}
		// **始点を確かめる。** true になっていなければ、この検証は
		// 何もしなくても通ってしまう。
		if actorOf(t, before)["must_change_password"] != true {
			t.Fatalf("始点が must_change_password=true になっていない: %s", before.Body.String())
		}

		rec := bodyWithCookie(r, http.MethodPost, "/api/v1/me/password", session,
			`{"current_password":"`+testPassword+`","new_password":"changed-passphrase-1"}`, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}

		after := getWithCookie(r, "/api/v1/me", session)
		if after.Code != http.StatusOK {
			t.Fatalf("変更後の GET /me status = %d", after.Code)
		}
		if actorOf(t, after)["must_change_password"] != false {
			t.Errorf("must_change_password が下りていない: %s", after.Body.String())
		}
	})

	// ── 認証・CSRF（2.3 / 2.4）─────────────────────────────────

	t.Run("未認証は401", func(t *testing.T) {
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPatch, "/api/v1/me", `{"theme":"dark"}`},
			{http.MethodPost, "/api/v1/me/password", `{"current_password":"a","new_password":"b"}`},
		} {
			rec := call(r, c.method, c.path, c.body)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s status = %d, want 401", c.method, c.path, rec.Code)
			}
		}
	})

	t.Run("CSRFトークンが無いと403", func(t *testing.T) {
		_, email := newSelf(t, "csrf")
		session := loginAs(t, r, email)

		req := httptest.NewRequest(http.MethodPatch, "/api/v1/me",
			strings.NewReader(`{"theme":"dark"}`))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		if e := errorOf(t, rec); e.Code != "csrf_failed" {
			t.Errorf("error.code = %q, want csrf_failed", e.Code)
		}
	})

	// **オペレータでも通ること。** 4章は権限キーを要求しない（対象が常に
	// 自分自身なので、誰のアカウントを触るかで必要権限が変わらない）。
	t.Run("オペレータでも自分の設定は変えられる", func(t *testing.T) {
		_, email := newSelf(t, "operator")
		session := loginAs(t, r, email)

		rec := bodyWithCookie(r, http.MethodPatch, "/api/v1/me", session,
			`{"theme":"dark"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
	})
}

// ── 結合テストの小道具 ──────────────────────────────────────

// actorOf は sessionView の actor 部分を取り出す（4.1 / 4.2 の応答）。
func actorOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	actor, ok := viewOf(t, rec)["actor"].(map[string]any)
	if !ok {
		t.Fatalf("応答に actor が無い: %s", rec.Body.String())
	}
	return actor
}

// setMustChange は local_credential.must_change を直接書き換える。
//
// **API では true にできない**（6.2 の作成と 6.6 のリセットだけが立てる）。
// 「要パスワード変更が解除されること」を測るには始点が要るので、ここだけ
// SQL で作る。#35 の「戻ることを確かめる検証は、戻る前の状態を自分で作る」。
func setMustChange(t *testing.T, pool *pgxpool.Pool, email string, v bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE local_credential c SET must_change = $2
		FROM user_identity i, app_user u
		WHERE c.identity_id = i.id
		  AND i.user_id = u.actor_id
		  AND i.provider_key = 'local'
		  AND u.email = $1`, email, v); err != nil {
		t.Fatalf("must_change を設定できない: %v", err)
	}
}

// appUserVersion は app_user.version を DB から直接読む。
//
// **既存の userVersion（GET /admin/users/:id 経由）は使えない。** ここの
// 自分自身はオペレータであり、6章のエンドポイントは user.manage を要して
// 403 になる。測りたいのは列の値そのものなので、SQL で読む。
func appUserVersion(t *testing.T, pool *pgxpool.Pool, actorID string) int {
	t.Helper()
	var v int
	if err := pool.QueryRow(context.Background(),
		`SELECT version FROM app_user WHERE actor_id = $1`, actorID).Scan(&v); err != nil {
		t.Fatalf("version を読めない（actor_id=%s）: %v", actorID, err)
	}
	return v
}
