package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// GET/POST /admin/users（ApiDesign.md 6.1 / 6.2）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - actor → app_user → user_identity → local_credential の4表が
//     1つのトランザクションで入り、途中で失敗すれば1行も残らないこと
//   - app_user.email の UNIQUE が本当に 23505 を返し、409 に写ること
//   - citext の email が大文字小文字を区別せずに衝突すること
//   - ListAdminUsers の CASE 式による並び替えと ILIKE の絞り込み
//   - 作成したユーザーが、返された初期パスワードで実際にログインできること
//   - project_count が project_member の行数を数えていること
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestAdminUsersIntegration(t *testing.T) {
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

	adminID := ulidgen.New()
	adminEmail := "usr-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	operatorID := ulidgen.New()
	operatorEmail := "usr-op-" + operatorID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, operatorID, operatorEmail, auth.SystemRoleOperator)

	adminSession := loginAs(t, r, adminEmail)
	operatorSession := loginAs(t, r, operatorEmail)

	// 作成したユーザーは actor ごと消す。**t.Cleanup は後入れ先出しで走る**ので、
	// ここで登録した後始末は seedUserWithRole のものより先に実行される。
	cleanupActor := func(actorID string) {
		t.Cleanup(func() {
			bg := context.Background()
			if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1 OR target_id = $1`, actorID); err != nil {
				t.Errorf("監査ログの後始末に失敗した: %v", err)
			}
			if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, actorID); err != nil {
				t.Errorf("後始末に失敗した: %v", err)
			}
		})
	}

	uniq := strings.ToLower(ulidgen.New())

	t.Run("作成した4表が単一トランザクションで入る", func(t *testing.T) {
		email := "created-" + uniq + "@example.com"
		rec := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"結合テスト 太郎","email":"`+email+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var body struct {
			ID                string `json:"id"`
			SystemRole        string `json:"system_role"`
			GeneratedPassword string `json:"generated_password"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が JSON でない: %v", err)
		}
		cleanupActor(body.ID)

		if body.GeneratedPassword == "" {
			t.Fatalf("generated_password が空。6.2 は generate を既定とする")
		}
		if body.SystemRole != "operator" {
			t.Errorf("system_role = %q, want operator（省略時の既定）", body.SystemRole)
		}

		// 4表すべてに行が入っていること。
		var actorCount, userCount, identCount, credCount int
		row := pool.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM actor WHERE id = $1),
			  (SELECT count(*) FROM app_user WHERE actor_id = $1),
			  (SELECT count(*) FROM user_identity WHERE user_id = $1),
			  (SELECT count(*) FROM local_credential lc
			     JOIN user_identity ui ON ui.id = lc.identity_id WHERE ui.user_id = $1)`, body.ID)
		if err := row.Scan(&actorCount, &userCount, &identCount, &credCount); err != nil {
			t.Fatalf("行数を数えられない: %v", err)
		}
		if actorCount != 1 || userCount != 1 || identCount != 1 || credCount != 1 {
			t.Errorf("行数 actor=%d app_user=%d user_identity=%d local_credential=%d, want すべて1",
				actorCount, userCount, identCount, credCount)
		}

		// must_change は省略時 true（GuiDesign.md 5.6.1）。
		var mustChange bool
		if err := pool.QueryRow(ctx, `
			SELECT lc.must_change FROM local_credential lc
			JOIN user_identity ui ON ui.id = lc.identity_id WHERE ui.user_id = $1`,
			body.ID).Scan(&mustChange); err != nil {
			t.Fatalf("must_change を読めない: %v", err)
		}
		if !mustChange {
			t.Errorf("must_change = false, want true")
		}

		// **返された初期パスワードで実際にログインできること。**
		// ハッシュ化と subject の突き合わせ（Design.md 6.2.1 手順3）が
		// 通っていることを、curl と同じ経路で確かめる。
		loginRec := call(r, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"`+body.GeneratedPassword+`"}`)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("生成パスワードでログインできない status = %d（body=%s）",
				loginRec.Code, loginRec.Body.String())
		}
		newSession := cookieOf(loginRec, auth.SessionCookieName)
		if newSession == nil {
			t.Fatalf("pb_session が返らない")
		}
		// /me が must_change_password: true を出すこと（手順15 の誘導の材料）。
		// **actor の下にある**（ApiDesign.md 3.1 の応答構造。4.1 は同一構造）。
		meRec := getWithCookie(r, "/api/v1/me", newSession.Value)
		if meRec.Code != http.StatusOK {
			t.Fatalf("GET /me status = %d", meRec.Code)
		}
		actor, ok := viewOf(t, meRec)["actor"].(map[string]any)
		if !ok {
			t.Fatalf("GET /me に actor が無い: %s", meRec.Body.String())
		}
		if v := actor["must_change_password"]; v != true {
			t.Errorf("actor.must_change_password = %v, want true", v)
		}

		// 監査ログが同じトランザクションで入り、平文を含まないこと。
		var action, detail string
		if err := pool.QueryRow(ctx,
			`SELECT action, detail::text FROM audit_log WHERE target_id = $1 AND action = 'user.create'`,
			body.ID).Scan(&action, &detail); err != nil {
			t.Fatalf("監査ログが無い: %v", err)
		}
		if strings.Contains(detail, body.GeneratedPassword) {
			t.Errorf("audit_log.detail に平文が入っている: %s", detail)
		}
	})

	t.Run("メール重複は409で1行も残さない", func(t *testing.T) {
		email := "dup-" + uniq + "@example.com"
		first := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"最初","email":"`+email+`"}`)
		if first.Code != http.StatusCreated {
			t.Fatalf("1回目 status = %d, want 201（body=%s）", first.Code, first.Body.String())
		}
		cleanupActor(viewOf(t, first)["id"].(string))

		// **citext なので大文字でも衝突する**（DbDesign.md 6.2）。
		second := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"二人目","email":"`+strings.ToUpper(email)+`"}`)
		if second.Code != http.StatusConflict {
			t.Fatalf("2回目 status = %d, want 409（body=%s）", second.Code, second.Body.String())
		}
		if e := errorOf(t, second); e.Code != "already_exists" {
			t.Errorf("code = %q, want already_exists", e.Code)
		}

		// 落ちた側の actor が残っていないこと（単一トランザクションの要）。
		var strays int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM actor WHERE display_name = '二人目'`).Scan(&strays); err != nil {
			t.Fatalf("残骸を数えられない: %v", err)
		}
		if strays != 0 {
			t.Errorf("409 で失敗した作成の actor が %d 行残っている", strays)
		}
	})

	t.Run("一覧の絞り込みと並び替え", func(t *testing.T) {
		// 検索で確実に当たる表示名を持つユーザーを作る。
		marker := "zzsearch" + uniq
		rec := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"`+marker+`","email":"search-`+uniq+`@example.com"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		targetID := viewOf(t, rec)["id"].(string)
		cleanupActor(targetID)

		// q は表示名に部分一致する。
		items := listUsersAs(t, r, adminSession, "?q="+marker)
		if len(items) != 1 {
			t.Fatalf("q=%s の件数 = %d, want 1", marker, len(items))
		}
		if items[0].(map[string]any)["id"] != targetID {
			t.Errorf("別のユーザーが当たった: %v", items[0])
		}

		// メールでも当たる。
		if got := listUsersAs(t, r, adminSession, "?q=search-"+uniq); len(got) != 1 {
			t.Errorf("メールでの検索の件数 = %d, want 1", len(got))
		}

		// **ILIKE のメタ文字がワイルドカードとして働かないこと。**
		// `_` を素通しすると1文字にマッチしてしまい、別のユーザーが当たる。
		if got := listUsersAs(t, r, adminSession, "?q="+marker[:4]+"_"+marker[5:]); len(got) != 0 {
			t.Errorf("`_` がワイルドカードとして働いている: %d 件当たった", len(got))
		}

		// kind=agent の絞り込みが効くこと（0019 でエージェントが実在する）。
		//
		// **件数を決め打ちしない。** dev seed も他のテストもエージェントを作りうる
		// ので、「0件」や「N件」と書くと他人のデータで落ちる。ここで測るのは
		// **絞り込みが種別で効いているか**——返った行がすべて agent であること、
		// および kind=user の結果と重ならないことを見る。
		agents := listUsersAs(t, r, adminSession, "?kind=agent&per_page=200")
		inAgents := map[string]bool{}
		for _, raw := range agents {
			a, _ := raw.(map[string]any)
			if a["kind"] != "agent" {
				t.Errorf("kind=agent に %v の行が混ざっている", a["kind"])
			}
			// **エージェントには email も system_role も無い**（6.1）。
			if a["email"] != nil || a["system_role"] != nil {
				t.Errorf("エージェントに email/system_role が入っている: %v", a)
			}
			id, _ := a["id"].(string)
			inAgents[id] = true
		}
		for _, raw := range listUsersAs(t, r, adminSession, "?kind=user&per_page=200") {
			h, _ := raw.(map[string]any)
			id, _ := h["id"].(string)
			if inAgents[id] {
				t.Errorf("同じ行が kind=user と kind=agent の両方に出ている: %s", id)
			}
		}

		// 並び替えの両方向が効くこと（CASE 式の確認）。
		asc := listUsersAs(t, r, adminSession, "?sort=display_name&order=asc&per_page=200")
		desc := listUsersAs(t, r, adminSession, "?sort=display_name&order=desc&per_page=200")
		if len(asc) < 2 || len(asc) != len(desc) {
			t.Fatalf("並び替えの比較に足りる件数が無い: asc=%d desc=%d", len(asc), len(desc))
		}
		firstAsc := asc[0].(map[string]any)["display_name"]
		lastDesc := desc[len(desc)-1].(map[string]any)["display_name"]
		if firstAsc != lastDesc {
			t.Errorf("asc の先頭 %v と desc の末尾 %v が違う。並び替えが効いていない", firstAsc, lastDesc)
		}

		// is_active=false は現時点で0件（無効化は手順13）。
		if got := listUsersAs(t, r, adminSession, "?is_active=false"); len(got) != 0 {
			t.Errorf("is_active=false の件数 = %d, want 0", len(got))
		}
	})

	// 手順12c で足したソートと検索（ApiDesign.md 6.1）。
	//
	// **行の順そのものを確かめる。** CASE 式は「クエリ層へ値が渡ったか」では
	// 検証できず、実際にDBが並べた結果でしか分からない。全体の並びは他の
	// テストデータに左右されるので、**自分で作った行どうしの前後関係**を見る。
	t.Run("ロールと状態での並び替え・ロール名での検索", func(t *testing.T) {
		// 位置を引くための索引。per_page=200 で全件を採る。
		indexOf := func(query string) map[string]int {
			items := listUsersAs(t, r, adminSession, query+"&per_page=200")
			at := make(map[string]int, len(items))
			for i, it := range items {
				at[it.(map[string]any)["id"].(string)] = i
			}
			return at
		}

		// ── sort=system_role（role.sort_order の順。オペレータ 10 → アドミン 20）──
		asc := indexOf("?sort=system_role&order=asc")
		if asc[operatorID] > asc[adminID] {
			t.Errorf("昇順でオペレータ(%d)がアドミニストレータ(%d)より後ろにある。"+
				"role.sort_order の順（10→20）になっていない", asc[operatorID], asc[adminID])
		}
		desc := indexOf("?sort=system_role&order=desc")
		if desc[adminID] > desc[operatorID] {
			t.Errorf("降順でアドミニストレータ(%d)がオペレータ(%d)より後ろにある",
				desc[adminID], desc[operatorID])
		}

		// ── sort=is_active（昇順は無効が先）─────────────────────
		// 無効化のAPIは手順13 なので、ここだけDBを直接倒す。
		rec := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"zzinactive`+uniq+`","email":"inactive-`+uniq+`@example.com"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		inactiveID := viewOf(t, rec)["id"].(string)
		cleanupActor(inactiveID)
		if _, err := pool.Exec(ctx, `UPDATE actor SET is_active = false WHERE id = $1`, inactiveID); err != nil {
			t.Fatalf("is_active を倒せない: %v", err)
		}

		activeAsc := indexOf("?sort=is_active&order=asc")
		if activeAsc[inactiveID] > activeAsc[adminID] {
			t.Errorf("昇順で無効(%d)が有効(%d)より後ろにある。false < true になっていない",
				activeAsc[inactiveID], activeAsc[adminID])
		}
		activeDesc := indexOf("?sort=is_active&order=desc")
		if activeDesc[adminID] > activeDesc[inactiveID] {
			t.Errorf("降順で有効(%d)が無効(%d)より後ろにある",
				activeDesc[adminID], activeDesc[inactiveID])
		}

		// ── q がロールの表示名に当たる（画面に出ている文字列で探せる）──
		hit := listUsersAs(t, r, adminSession, "?q=アドミニストレータ&per_page=200")
		var foundAdmin, foundOperator bool
		for _, it := range hit {
			switch it.(map[string]any)["id"] {
			case adminID:
				foundAdmin = true
			case operatorID:
				foundOperator = true
			}
		}
		if !foundAdmin {
			t.Errorf("q=アドミニストレータ でアドミニストレータが当たらない（%d件）", len(hit))
		}
		if foundOperator {
			t.Error("q=アドミニストレータ でオペレータまで当たっている")
		}

		// **キーでは当てない**（画面に出ない文字列。6.1）。
		for _, it := range listUsersAs(t, r, adminSession, "?q=administrator&per_page=200") {
			if it.(map[string]any)["id"] == adminID {
				t.Error("q=administrator（キー）で当たっている。6.1 は表示名だけを対象と定める")
			}
		}
	})

	t.Run("ETagは総件数と更新で変わる", func(t *testing.T) {
		before := listRecorderUsers(r, adminSession, "").Header().Get("ETag")
		if before == "" {
			t.Fatalf("ETag が空")
		}
		if !strings.HasPrefix(before, `W/"user-`) {
			t.Errorf("ETag = %q, want W/\"user-… （RFC 9110 8.8.3 の弱い検証子）", before)
		}
		rec := postWithCookie(r, "/api/v1/admin/users", adminSession,
			`{"display_name":"ETag 確認","email":"etag-`+uniq+`@example.com"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		cleanupActor(viewOf(t, rec)["id"].(string))

		if after := listRecorderUsers(r, adminSession, "").Header().Get("ETag"); after == before {
			t.Errorf("ユーザーを増やしても ETag が %q のまま", after)
		}
	})

	t.Run("project_countはメンバーシップの件数", func(t *testing.T) {
		items := listUsersAs(t, r, adminSession, "?q=usr-admin-"+strings.ToLower(adminID))
		if len(items) != 1 {
			t.Fatalf("管理者が引けない: %d 件", len(items))
		}
		// seedUserWithRole はプロジェクトに入れないので0件。
		if got := items[0].(map[string]any)["project_count"]; got != float64(0) {
			t.Errorf("project_count = %v, want 0", got)
		}
	})

	t.Run("operatorは403", func(t *testing.T) {
		rec := listRecorderUsers(r, operatorSession, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		post := postWithCookie(r, "/api/v1/admin/users", operatorSession,
			`{"display_name":"作れないはず","email":"forbidden-`+uniq+`@example.com"}`)
		if post.Code != http.StatusForbidden {
			t.Fatalf("POST status = %d, want 403（body=%s）", post.Code, post.Body.String())
		}
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM actor WHERE display_name = '作れないはず'`).Scan(&count); err != nil {
			t.Fatalf("残骸を数えられない: %v", err)
		}
		if count != 0 {
			t.Errorf("403 なのに actor が %d 行できている", count)
		}
	})
}

func listRecorderUsers(r http.Handler, token, query string) *httptest.ResponseRecorder {
	return getWithCookie(r, "/api/v1/admin/users"+query, token)
}

func listUsersAs(t *testing.T, r http.Handler, token, query string) []any {
	t.Helper()
	rec := listRecorderUsers(r, token, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users%s status = %d（body=%s）", query, rec.Code, rec.Body.String())
	}
	items, ok := viewOf(t, rec)["items"].([]any)
	if !ok {
		t.Fatalf("items が配列でない: %s", rec.Body.String())
	}
	return items
}
