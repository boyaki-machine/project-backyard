package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ユーザー詳細・編集（ApiDesign.md 6.3〜6.8）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - app_user.version による楽観ロックが本当に0行を返すこと
//   - メール変更に user_identity.subject が追随し、**新しいメールでログインできる**こと
//     （追随しなければ FindLocalLoginByEmail の結合が外れて弾かれる）
//   - パスワードリセット後、**古いセッションが 401 になり、新しいパスワードで入れる**こと
//   - actor の削除で app_user / user_identity / local_credential /
//     access_token / project_member が CASCADE で消えること
//   - PUT のメンバーシップが冪等で、joined_at が動かないこと
//   - 「最後の有効なアドミニストレータ」が実データの数え方で守られること
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestAdminUserDetailIntegration(t *testing.T) {
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

	// **操作する側の管理者を2人用意する。** 1人だと自分自身のガードと
	// 「最後のアドミニストレータ」のガードが重なり、どちらで止まったのか
	// 分からなくなる。
	adminID := ulidgen.New()
	adminEmail := "dtl-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	admin2ID := ulidgen.New()
	admin2Email := "dtl-admin2-" + admin2ID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, admin2ID, admin2Email, auth.SystemRoleAdministrator)
	operatorID := ulidgen.New()
	operatorEmail := "dtl-op-" + operatorID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, operatorID, operatorEmail, auth.SystemRoleOperator)

	adminSession := loginAs(t, r, adminEmail)
	operatorSession := loginAs(t, r, operatorEmail)

	uniq := strings.ToLower(ulidgen.New())

	// createTarget は操作対象のユーザーを1人作り、その ID と初期パスワードを返す。
	// **テストごとに作る。** 状態を変える検証が互いの前提を壊さないようにする。
	createTarget := func(t *testing.T, label string) (string, string, string) {
		t.Helper()
		email := label + "-" + uniq + "@example.com"
		rec := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"詳細テスト `+label+`","email":"`+email+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("対象を作れない status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var body struct {
			ID                string `json:"id"`
			GeneratedPassword string `json:"generated_password"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が JSON でない: %v", err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			if _, err := pool.Exec(bg,
				`DELETE FROM audit_log WHERE actor_id = $1 OR target_id = $1`, body.ID); err != nil {
				t.Errorf("監査ログの後始末に失敗した: %v", err)
			}
			if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, body.ID); err != nil {
				t.Errorf("後始末に失敗した: %v", err)
			}
		})
		return body.ID, email, body.GeneratedPassword
	}

	t.Run("詳細が5ブロックぶんの材料を1回で返す", func(t *testing.T) {
		id, email, password := createTarget(t, "detail")
		// セッションを1本作って sessions[] に出ることを確かめる。
		targetSession := loginAsWith(t, r, email, password)
		_ = targetSession

		view := userDetail(t, r, adminSession, id)
		if view["email"] != email {
			t.Errorf("email = %v, want %s", view["email"], email)
		}
		if view["version"] != float64(1) {
			t.Errorf("version = %v, want 1（作成直後）", view["version"])
		}

		identities, _ := view["identities"].([]any)
		if len(identities) != 1 {
			t.Fatalf("identities の件数 = %d, want 1", len(identities))
		}
		ident := identities[0].(map[string]any)
		if ident["provider_key"] != "local" || ident["provider_type"] != "local" {
			t.Errorf("identities[0] = %v, want local/local", ident)
		}
		// **subject は app_user.email と同じ**（Design.md 6.2.1 手順3）。
		if ident["subject"] != email {
			t.Errorf("subject = %v, want %s", ident["subject"], email)
		}
		if ident["password_updated_at"] == nil {
			t.Errorf("password_updated_at が null（local_credential の LEFT JOIN が外れている）")
		}

		sessions, _ := view["sessions"].([]any)
		if len(sessions) != 1 {
			t.Errorf("sessions の件数 = %d, want 1（ログインした1本）", len(sessions))
		}
	})

	t.Run("メール変更に user_identity.subject が追随する", func(t *testing.T) {
		id, _, password := createTarget(t, "mailchg")
		newEmail := "mailchg-new-" + uniq + "@example.com"

		version := userVersion(t, r, adminSession, id)
		rec := patchUserAs(r, adminSession, id, version, `{"email":"`+newEmail+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if got := viewOf(t, rec)["version"]; got != float64(version+1) {
			t.Errorf("version = %v, want %d", got, version+1)
		}

		// **DB の subject が変わっていること。**
		var subject string
		if err := pool.QueryRow(ctx,
			`SELECT subject FROM user_identity WHERE user_id = $1 AND provider_key = 'local'`,
			id).Scan(&subject); err != nil {
			t.Fatalf("subject を読めない: %v", err)
		}
		if subject != newEmail {
			t.Errorf("user_identity.subject = %q, want %q", subject, newEmail)
		}

		// **新しいメールで実際にログインできること。** ここが本題で、
		// subject を放置すると FindLocalLoginByEmail の結合が外れて 401 になる。
		login := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+newEmail+`","password":"`+password+`"}`)
		if login.Code != http.StatusOK {
			t.Fatalf("新しいメールでログインできない status = %d（body=%s）",
				login.Code, login.Body.String())
		}
	})

	t.Run("楽観ロックが古いversionを弾く", func(t *testing.T) {
		id, _, _ := createTarget(t, "optlock")
		version := userVersion(t, r, adminSession, id)

		first := patchUserAs(r, adminSession, id, version, `{"display_name":"1回目"}`)
		if first.Code != http.StatusOK {
			t.Fatalf("1回目 status = %d, want 200（body=%s）", first.Code, first.Body.String())
		}
		// **同じ version をもう一度使う。** 表示名は actor の列だが、
		// version は app_user 側で進んでいるので弾かれるはず。
		second := patchUserAs(r, adminSession, id, version, `{"display_name":"2回目"}`)
		if second.Code != http.StatusConflict {
			t.Fatalf("2回目 status = %d, want 409（body=%s）", second.Code, second.Body.String())
		}
		if e := errorOf(t, second); e.Code != "conflict" {
			t.Errorf("code = %q, want conflict", e.Code)
		}
	})

	t.Run("自分自身のロール変更と無効化は409", func(t *testing.T) {
		version := userVersion(t, r, adminSession, adminID)

		role := patchUserAs(r, adminSession, adminID, version, `{"system_role":"operator"}`)
		if role.Code != http.StatusConflict {
			t.Fatalf("ロール変更 status = %d, want 409（body=%s）", role.Code, role.Body.String())
		}
		if e := errorOf(t, role); e.Code != "self_modification_forbidden" {
			t.Errorf("code = %q, want self_modification_forbidden", e.Code)
		}

		deact := patchUserAs(r, adminSession, adminID, version, `{"is_active":false}`)
		if deact.Code != http.StatusConflict {
			t.Fatalf("無効化 status = %d, want 409（body=%s）", deact.Code, deact.Body.String())
		}

		// **弾かれた後も version が進んでいないこと**（ガードが更新より前にある）。
		if after := userVersion(t, r, adminSession, adminID); after != version {
			t.Errorf("version = %d, want %d（409 なのに更新されている）", after, version)
		}

		// 削除も同じ。
		del := callWithCookie(r, http.MethodDelete, "/api/v1/admin/users/"+adminID, adminSession)
		if del.Code != http.StatusConflict {
			t.Fatalf("自分の削除 status = %d, want 409（body=%s）", del.Code, del.Body.String())
		}
	})

	t.Run("有効なアドミニストレータの数え方", func(t *testing.T) {
		// **409 そのものは再現できない。** last_administrator が出るのは有効な
		// 管理者が1人だけのときで、同じDBには利用者自身のアカウントが居る。
		// 1人にするには他人の管理者を無効化することになり、**利用者のデータを
		// 壊す**（make dev-reset を反射で打たないのと同じ理由）。
		//
		// 代わりに、ガードの土台である **CountActiveAdministrators の数え方**を
		// 実データで確かめる。「無効な管理者を数えない」「降格した管理者を
		// 数えない」の2点が満たされていれば、409 の判定は正しい材料の上に立つ。
		// 409 に倒す分岐そのものは users_detail_test.go が見ている。
		before, err := q.CountActiveAdministrators(ctx)
		if err != nil {
			t.Fatalf("管理者を数えられない: %v", err)
		}

		// **自前で作った admin2 だけを動かす。** 他人のアカウントは触らない。
		v := userVersion(t, r, adminSession, admin2ID)
		if rec := patchUserAs(r, adminSession, admin2ID, v, `{"is_active":false}`); rec.Code != http.StatusOK {
			t.Fatalf("無効化 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		deactivated, err := q.CountActiveAdministrators(ctx)
		if err != nil {
			t.Fatalf("管理者を数えられない: %v", err)
		}
		if deactivated != before-1 {
			t.Errorf("無効化後の人数 = %d, want %d（無効な管理者を数えている）", deactivated, before-1)
		}

		// 有効に戻すと元の人数に戻る。
		v = userVersion(t, r, adminSession, admin2ID)
		if rec := patchUserAs(r, adminSession, admin2ID, v, `{"is_active":true}`); rec.Code != http.StatusOK {
			t.Fatalf("有効化 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if restored, _ := q.CountActiveAdministrators(ctx); restored != before {
			t.Errorf("有効化後の人数 = %d, want %d", restored, before)
		}

		// 降格でも減る。**現在値は毎回読み直す**（前の操作の結果を固定値で書かない）。
		v = userVersion(t, r, adminSession, admin2ID)
		if rec := patchUserAs(r, adminSession, admin2ID, v, `{"system_role":"operator"}`); rec.Code != http.StatusOK {
			t.Fatalf("降格 status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		t.Cleanup(func() {
			// **途中で落ちても admin2 を管理者へ戻す。**
			cv := userVersion(t, r, adminSession, admin2ID)
			if rec := patchUserAs(r, adminSession, admin2ID, cv, `{"system_role":"administrator"}`); rec.Code != http.StatusOK {
				t.Errorf("admin2 を戻せない status = %d（body=%s）", rec.Code, rec.Body.String())
			}
		})
		demoted, err := q.CountActiveAdministrators(ctx)
		if err != nil {
			t.Fatalf("管理者を数えられない: %v", err)
		}
		if demoted != before-1 {
			t.Errorf("降格後の人数 = %d, want %d", demoted, before-1)
		}
	})

	t.Run("パスワードリセットが旧セッションを切り新パスワードで入れる", func(t *testing.T) {
		id, email, password := createTarget(t, "pwreset")
		oldSession := loginAsWith(t, r, email, password)

		// 旧セッションが生きていることを先に確かめる（この後の 401 が
		// 「元から入れなかった」ではないと言えるようにする）。
		if rec := getWithCookie(r, "/api/v1/me", oldSession); rec.Code != http.StatusOK {
			t.Fatalf("リセット前の GET /me status = %d, want 200", rec.Code)
		}

		// **ロックされた状態を先に作る。** 6.6 の主な用途は「ログイン失敗が
		// 続いてロックされた利用者を救う」ことなので、リセット前が 0 のままでは
		// 「戻った」ことを確かめられない。**ログインを失敗させて作らない**——
		// ログインは IP 単位のレート制限（2.9、10回/分）を消費するため、
		// 検証の本筋でない試行で枠を使うと後続が 429 で落ちる。
		if _, err := pool.Exec(ctx, `
			UPDATE local_credential SET failed_attempts = 3, locked_until = now() + interval '15 minutes'
			WHERE identity_id = (
			  SELECT id FROM user_identity WHERE user_id = $1 AND provider_key = 'local')`,
			id); err != nil {
			t.Fatalf("ロック状態を作れない: %v", err)
		}

		rec := postWithCookie(r, "/api/v1/admin/users/"+id+"/password-reset", adminSession, `{}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		newPassword, _ := viewOf(t, rec)["generated_password"].(string)
		if newPassword == "" || newPassword == password {
			t.Fatalf("generated_password = %q（生成されていない）", newPassword)
		}

		// **failed_attempts と locked_until が戻っていること**（6.6）。
		// **ログインを試す前に読む。** 後で読むと、この下で故意に失敗させる
		// 旧パスワードのログインが failed_attempts を 1 に戻してしまい、
		// リセットの効果ではなくその失敗を測ることになる。
		var failed int
		var locked *string
		if err := pool.QueryRow(ctx, `
			SELECT c.failed_attempts, c.locked_until::text FROM local_credential c
			JOIN user_identity i ON i.id = c.identity_id WHERE i.user_id = $1`,
			id).Scan(&failed, &locked); err != nil {
			t.Fatalf("資格情報を読めない: %v", err)
		}
		if failed != 0 || locked != nil {
			t.Errorf("failed_attempts = %d locked_until = %v, want 0 / NULL", failed, locked)
		}

		// **旧セッションが 401 になる。**
		if after := getWithCookie(r, "/api/v1/me", oldSession); after.Code != http.StatusUnauthorized {
			t.Errorf("リセット後の GET /me status = %d, want 401", after.Code)
		}
		// **新しいパスワードで入れる。**
		login := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+newPassword+`"}`)
		if login.Code != http.StatusOK {
			t.Fatalf("新パスワードでログインできない status = %d（body=%s）",
				login.Code, login.Body.String())
		}
		// 旧パスワードでは入れない。
		old := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+password+`"}`)
		if old.Code == http.StatusOK {
			t.Errorf("旧パスワードでログインできてしまう")
		}

		// 監査に平文が残っていないこと。
		var detail string
		if err := pool.QueryRow(ctx,
			`SELECT detail::text FROM audit_log WHERE target_id = $1 AND action = 'password.reset'`,
			id).Scan(&detail); err != nil {
			t.Fatalf("監査ログが無い: %v", err)
		}
		if strings.Contains(detail, newPassword) {
			t.Errorf("audit_log.detail に平文が入っている: %s", detail)
		}
	})

	t.Run("全セッション失効で入れなくなる", func(t *testing.T) {
		id, email, password := createTarget(t, "revoke")
		session := loginAsWith(t, r, email, password)

		rec := postWithCookie(r, "/api/v1/admin/users/"+id+"/sessions/revoke", adminSession, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		if after := getWithCookie(r, "/api/v1/me", session); after.Code != http.StatusUnauthorized {
			t.Errorf("失効後の GET /me status = %d, want 401", after.Code)
		}
		// **冪等。** もう一度呼んでも 204。
		again := postWithCookie(r, "/api/v1/admin/users/"+id+"/sessions/revoke", adminSession, "")
		if again.Code != http.StatusNoContent {
			t.Errorf("2回目 status = %d, want 204", again.Code)
		}
	})

	t.Run("無効化すると次のリクエストから401", func(t *testing.T) {
		id, email, password := createTarget(t, "deact")
		session := loginAsWith(t, r, email, password)

		version := userVersion(t, r, adminSession, id)
		rec := patchUserAs(r, adminSession, id, version, `{"is_active":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if viewOf(t, rec)["is_active"] != false {
			t.Errorf("is_active = %v, want false", viewOf(t, rec)["is_active"])
		}
		// **セッションは失効させないが、認証が actor.is_active を見るので通らない。**
		if after := getWithCookie(r, "/api/v1/me", session); after.Code == http.StatusOK {
			t.Errorf("無効化したのに GET /me が 200 を返す")
		}
	})

	t.Run("メンバーシップの付与は冪等でjoined_atが動かない", func(t *testing.T) {
		id, _, _ := createTarget(t, "member")
		key := "dtl-" + uniq[:8]
		created := postWithCookie(r, "/api/v1/projects", adminSession,
			`{"key":"`+key+`","name":"メンバーシップ検証","template":"minimal"}`)
		if created.Code != http.StatusCreated {
			t.Fatalf("プロジェクトを作れない status = %d（body=%s）", created.Code, created.Body.String())
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM project WHERE key = $1`, key); err != nil {
				t.Errorf("プロジェクトの後始末に失敗した: %v", err)
			}
		})

		path := "/api/v1/admin/users/" + id + "/memberships/" + key
		first := putWithCookie(r, path, adminSession, `{"role":"project_member"}`)
		if first.Code != http.StatusOK {
			t.Fatalf("1回目 status = %d, want 200（body=%s）", first.Code, first.Body.String())
		}
		joined := viewOf(t, first)["joined_at"]

		// **ロールを変えても joined_at は動かない**（6.3 の joined_at が意味を失う）。
		second := putWithCookie(r, path, adminSession, `{"role":"project_admin"}`)
		if second.Code != http.StatusOK {
			t.Fatalf("2回目 status = %d, want 200（body=%s）", second.Code, second.Body.String())
		}
		view := viewOf(t, second)
		if view["role"] != "project_admin" {
			t.Errorf("role = %v, want project_admin", view["role"])
		}
		if view["joined_at"] != joined {
			t.Errorf("joined_at = %v, want %v（ロール変更で動かさない）", view["joined_at"], joined)
		}

		// 詳細にも1件だけ出ること（重複して入っていない）。
		memberships, _ := userDetail(t, r, adminSession, id)["project_memberships"].([]any)
		if len(memberships) != 1 {
			t.Errorf("project_memberships の件数 = %d, want 1", len(memberships))
		}

		// 剥奪。**元から居なくても 204**（冪等）。
		if rec := callWithCookie(r, http.MethodDelete, path, adminSession); rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		if rec := callWithCookie(r, http.MethodDelete, path, adminSession); rec.Code != http.StatusNoContent {
			t.Errorf("2回目の DELETE status = %d, want 204", rec.Code)
		}
	})

	t.Run("存在しないプロジェクトへの付与は404", func(t *testing.T) {
		id, _, _ := createTarget(t, "nomember")
		rec := putWithCookie(r, "/api/v1/admin/users/"+id+"/memberships/no-such-"+uniq[:6],
			adminSession, `{"role":"project_member"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("削除で関連する行がCASCADEで消える", func(t *testing.T) {
		id, email, password := createTarget(t, "delete")
		loginAsWith(t, r, email, password) // access_token を1行作る

		rec := callWithCookie(r, http.MethodDelete, "/api/v1/admin/users/"+id, adminSession)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}

		var actors, users, idents, creds, tokens int
		if err := pool.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM actor WHERE id = $1),
			  (SELECT count(*) FROM app_user WHERE actor_id = $1),
			  (SELECT count(*) FROM user_identity WHERE user_id = $1),
			  (SELECT count(*) FROM local_credential c
			     JOIN user_identity i ON i.id = c.identity_id WHERE i.user_id = $1),
			  (SELECT count(*) FROM access_token WHERE actor_id = $1)`,
			id).Scan(&actors, &users, &idents, &creds, &tokens); err != nil {
			t.Fatalf("行数を数えられない: %v", err)
		}
		if actors+users+idents+creds+tokens != 0 {
			t.Errorf("残った行 actor=%d app_user=%d user_identity=%d local_credential=%d access_token=%d",
				actors, users, idents, creds, tokens)
		}

		// **監査は残る**（誰を消したかを追えるように。6.5）。
		var detail string
		if err := pool.QueryRow(ctx,
			`SELECT detail::text FROM audit_log WHERE target_id = $1 AND action = 'user.delete'`,
			id).Scan(&detail); err != nil {
			t.Fatalf("user.delete の監査が無い: %v", err)
		}
		if !strings.Contains(detail, email) {
			t.Errorf("detail = %s, want %s を含む", detail, email)
		}

		// 消えた後は 404。
		if after := getWithCookie(r, "/api/v1/admin/users/"+id, adminSession); after.Code != http.StatusNotFound {
			t.Errorf("削除後の GET status = %d, want 404", after.Code)
		}
	})

	t.Run("operatorは6.3〜6.9のすべてで403", func(t *testing.T) {
		id, _, _ := createTarget(t, "forbidden")
		path := "/api/v1/admin/users/" + id

		cases := []struct {
			method, path, body string
		}{
			{http.MethodGet, path, ""},
			{http.MethodPatch, path, `{"display_name":"変えられないはず"}`},
			{http.MethodDelete, path, ""},
			{http.MethodPost, path + "/password-reset", `{}`},
			{http.MethodPost, path + "/sessions/revoke", ""},
			{http.MethodPost, path + "/mfa/reset", ""},
			{http.MethodPut, path + "/memberships/whatever", `{"role":"project_member"}`},
			{http.MethodDelete, path + "/memberships/whatever", ""},
		}
		for _, c := range cases {
			rec := bodyWithCookie(r, c.method, c.path, operatorSession, c.body, "")
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s status = %d, want 403（body=%s）",
					c.method, c.path, rec.Code, rec.Body.String())
			}
		}

		// **operator が触れていないこと**を実データで確かめる。
		if got := userDetail(t, r, adminSession, id)["display_name"]; got == "変えられないはず" {
			t.Errorf("403 なのに display_name が変わっている")
		}
	})

	t.Run("CSRFトークンが無いと403", func(t *testing.T) {
		id, _, _ := createTarget(t, "csrf")
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/"+id,
			strings.NewReader(`{"display_name":"CSRFなし"}`))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: adminSession})
		req.Header.Set("If-Match", `"1"`)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		if e := errorOf(t, rec); e.Code != "csrf_failed" {
			t.Errorf("code = %q, want csrf_failed", e.Code)
		}
	})
}

// ── 結合テストの小道具 ──────────────────────────────────────

// bodyWithCookie は本文と If-Match を任意に付けられる汎用の呼び出し。
func bodyWithCookie(
	r http.Handler, method, path, token, body, ifMatch string,
) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func putWithCookie(r http.Handler, path, token, body string) *httptest.ResponseRecorder {
	return bodyWithCookie(r, http.MethodPut, path, token, body, "")
}

func patchUserAs(
	r http.Handler, token, id string, version int, body string,
) *httptest.ResponseRecorder {
	return bodyWithCookie(r, http.MethodPatch, "/api/v1/admin/users/"+id, token, body,
		strconv.Quote(strconv.Itoa(version)))
}

// userDetail は 6.3 を引いて JSON を返す。
func userDetail(t *testing.T, r http.Handler, token, id string) map[string]any {
	t.Helper()
	rec := getWithCookie(r, "/api/v1/admin/users/"+id, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users/%s status = %d（body=%s）", id, rec.Code, rec.Body.String())
	}
	return viewOf(t, rec)
}

// userVersion は**現在の version を毎回読み直す**。
//
// 前の操作の結果を前提に固定値を書くと、途中で落ちたときに続きから
// 再開できなくなる（pb-step.md 手順6）。
func userVersion(t *testing.T, r http.Handler, token, id string) int {
	t.Helper()
	v, ok := userDetail(t, r, token, id)["version"].(float64)
	if !ok {
		t.Fatalf("version を読めない（id=%s）", id)
	}
	return int(v)
}

// loginAsWith は任意のパスワードでログインする（loginAs は testPassword 固定）。
//
// **呼び出しごとに違う RemoteAddr を使う。** ログインは IP 単位のレート制限を
// 受け（ApiDesign.md 2.9、10回/分）、httptest.NewRequest は固定のアドレスを
// 入れるため、同じ窓の中で11回目が 429 になる。1つのテストが増えただけで
// 無関係な検証が落ちる状態にしない。**別々の利用者の端末を模す**という点でも、
// 同じアドレスを共有するほうが実態から遠い。
func loginAsWith(t *testing.T, r http.Handler, email, password string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	req.RemoteAddr = nextTestClientIP()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s のログイン status = %d（body=%s）", email, rec.Code, rec.Body.String())
	}
	c := cookieOf(rec, auth.SessionCookieName)
	if c == nil {
		t.Fatalf("%s のログインで pb_session が返らない", email)
	}
	return c.Value
}

// testClientIP は loginAsWith が使う擬似クライアントの通し番号。
//
// 198.51.100.0/24 は RFC 5737 が文書用に予約したアドレスで、実在しない。
var testClientIP atomic.Uint32

func nextTestClientIP() string {
	// .0 と .255 を避けたいわけではない（到達させないので害は無い）が、
	// 1 から始めておくと読みやすい。
	return fmt.Sprintf("198.51.100.%d:51234", testClientIP.Add(1)%250+1)
}
