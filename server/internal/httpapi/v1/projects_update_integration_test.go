package v1

import (
	"context"
	"fmt"
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

// GET/PATCH /projects/:key と archive/unarchive（ApiDesign.md 5.4〜5.6）を
// **実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - UpdateProject の COALESCE と sqlc.narg が「送られなかった項目を据え置く」
//     という意味で本当に動くこと（部分更新の要）
//   - WHERE の version 照合が楽観ロックとして機能し、+1 されること
//   - SetProjectStatus の CASE が archived_at を入れ／NULL に戻すこと
//   - status <> @status による冪等が本当に 0 行になること
//   - trg_project_updated（0004）が updated_at を進めること
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestProjectUpdateIntegration(t *testing.T) {
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
	adminEmail := "upd-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	operatorID := ulidgen.New()
	operatorEmail := "upd-op-" + operatorID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, operatorID, operatorEmail, auth.SystemRoleOperator)

	adminSession := loginAs(t, r, adminEmail)
	operatorSession := loginAs(t, r, operatorEmail)

	key := "up-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})

	created := viewOf(t, postWithCookie(r, "/api/v1/projects", adminSession,
		fmt.Sprintf(`{"key":%q,"name":"更新の結合テスト","description":"最初の説明"}`, key)))
	projectID, _ := created["id"].(string)
	if len(projectID) != 26 {
		t.Fatalf("作成に失敗した: %v", created)
	}

	// ① GET /projects/:key（5.4）。作成の応答と同じ形が返る。
	got := viewOf(t, getWithCookie(r, "/api/v1/projects/"+key, adminSession))
	if got["key"] != key || got["name"] != "更新の結合テスト" {
		t.Errorf("GET の応答 = %v", got)
	}
	if v, _ := got["version"].(float64); v != 1 {
		t.Fatalf("作成直後の version = %v, want 1", got["version"])
	}
	if _, ok := got["workflow"].(map[string]any); !ok {
		t.Errorf("workflow = %v, want オブジェクト", got["workflow"])
	}

	// ② PATCH。**name だけ送り、description が据え置かれること**（COALESCE と
	//    sqlc.narg が意図どおりに効くのは、実際に SQL を投げないと分からない）。
	rec := patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, `"1"`,
		`{"name":"名前だけ変えた"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH の status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	updated := viewOf(t, rec)
	if updated["name"] != "名前だけ変えた" {
		t.Errorf("name = %v, want 名前だけ変えた", updated["name"])
	}
	if updated["description"] != "最初の説明" {
		t.Errorf("description = %v, want 最初の説明（送っていないので据え置き）", updated["description"])
	}
	if v, _ := updated["version"].(float64); v != 2 {
		t.Errorf("version = %v, want 2（+1 される）", updated["version"])
	}

	// ③ 古い version は 409（2.8 の楽観ロック）。
	stale := patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, `"1"`,
		`{"name":"競合するはず"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("古い If-Match の status = %d, want 409（body=%s）", stale.Code, stale.Body.String())
	}
	if got := errorOf(t, stale).Code; got != "conflict" {
		t.Errorf("code = %q, want conflict", got)
	}
	// 競合した更新は反映されていない。
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM project WHERE id = $1 AND name = '名前だけ変えた'`, projectID); n != 1 {
		t.Error("409 を返したのに name が書き換わっている")
	}

	// ④ `"description": null` は説明を消す（未送信との区別）。
	cleared := viewOf(t, patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, `"2"`,
		`{"description":null}`))
	if cleared["description"] != nil {
		t.Errorf("description = %v, want null", cleared["description"])
	}
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM project WHERE id = $1 AND description IS NULL`, projectID); n != 1 {
		t.Error("description が NULL になっていない")
	}

	// ⑤ settings は jsonb をそのまま置き換える。
	withSettings := viewOf(t, patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, `"3"`,
		`{"settings":{"max_concurrent_agents":3}}`))
	settings, _ := withSettings["settings"].(map[string]any)
	if v, _ := settings["max_concurrent_agents"].(float64); v != 3 {
		t.Errorf("settings = %v, want max_concurrent_agents=3", withSettings["settings"])
	}
	// name は据え置き（settings だけを送った）。
	if withSettings["name"] != "名前だけ変えた" {
		t.Errorf("name = %v, want 据え置き", withSettings["name"])
	}

	// ⑥ key を送ると 422 immutable_field（5.5）。
	immutable := patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, `"4"`,
		`{"key":"renamed-key"}`)
	if immutable.Code != http.StatusUnprocessableEntity {
		t.Fatalf("key 送信の status = %d, want 422（body=%s）", immutable.Code, immutable.Body.String())
	}
	if e := errorOf(t, immutable); e.Code != "validation_failed" || !hasDetail(e, "key", "immutable_field") {
		t.Errorf("エラー = %+v, want validation_failed ＋ key/immutable_field", e)
	}

	// ⑦ If-Match 省略は 422（2.8）。
	noMatch := patchProjectWithCookie(r, "/api/v1/projects/"+key, adminSession, "",
		`{"name":"ヘッダ無し"}`)
	if noMatch.Code != http.StatusUnprocessableEntity {
		t.Fatalf("If-Match 無しの status = %d, want 422（body=%s）", noMatch.Code, noMatch.Body.String())
	}
	if !hasDetail(errorOf(t, noMatch), "If-Match", "required") {
		t.Errorf("details = %v, want If-Match/required", errorOf(t, noMatch).Details)
	}

	// ⑧ archive（5.6）。status・archived_at・version・監査を確かめる。
	beforeUpdatedAt := scalarString(t, pool, `SELECT updated_at::text FROM project WHERE id = $1`, projectID)

	archived := viewOf(t, postWithCookie(r, "/api/v1/projects/"+key+"/archive", adminSession, ""))
	if archived["status"] != "archived" {
		t.Errorf("status = %v, want archived", archived["status"])
	}
	if v, _ := archived["version"].(float64); v != 5 {
		t.Errorf("version = %v, want 5（4 から +1）", archived["version"])
	}
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM project WHERE id = $1 AND archived_at IS NOT NULL`, projectID); n != 1 {
		t.Error("archived_at が入っていない（5.6）")
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'project.archive' AND target_id = $1
		   AND detail->>'status' = 'archived'`, projectID); n != 1 {
		t.Error("audit_log に project.archive（status=archived）が無い")
	}
	// trg_project_updated（0004）が updated_at を進めること。
	if after := scalarString(t, pool, `SELECT updated_at::text FROM project WHERE id = $1`, projectID); after == beforeUpdatedAt {
		t.Errorf("updated_at が進んでいない（%s のまま）", after)
	}

	// ⑨ 二重の archive は冪等。version は進まず、監査も増えない（5.6）。
	again := viewOf(t, postWithCookie(r, "/api/v1/projects/"+key+"/archive", adminSession, ""))
	if v, _ := again["version"].(float64); v != 5 {
		t.Errorf("2回目の archive で version = %v, want 5 のまま", again["version"])
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'project.archive' AND target_id = $1`, projectID); n != 1 {
		t.Errorf("監査が %d件, want 1件（冪等な空振りは記録しない）", n)
	}

	// ⑩ unarchive で戻る。archived_at は NULL に戻る（5.6）。
	restored := viewOf(t, postWithCookie(r, "/api/v1/projects/"+key+"/unarchive", adminSession, ""))
	if restored["status"] != "active" {
		t.Errorf("status = %v, want active", restored["status"])
	}
	if v, _ := restored["version"].(float64); v != 6 {
		t.Errorf("version = %v, want 6", restored["version"])
	}
	if n := scalarInt(t, pool,
		`SELECT count(*) FROM project WHERE id = $1 AND archived_at IS NULL`, projectID); n != 1 {
		t.Error("archived_at が NULL に戻っていない（5.6）")
	}

	// ⑪ 非メンバーのオペレータには 404（Design.md 6.4.5。403 だと存在が漏れる）。
	for _, tc := range []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"GET", getWithCookie(r, "/api/v1/projects/"+key, operatorSession)},
		{"PATCH", patchProjectWithCookie(r, "/api/v1/projects/"+key, operatorSession, `"6"`, `{"name":"x"}`)},
		{"archive", postWithCookie(r, "/api/v1/projects/"+key+"/archive", operatorSession, "")},
	} {
		if tc.rec.Code != http.StatusNotFound {
			t.Errorf("非メンバーの %s = %d, want 404（body=%s）", tc.name, tc.rec.Code, tc.rec.Body.String())
		}
	}

	// ⑫ 存在しないキーも 404（同じ応答であること。存在の有無が漏れない）。
	missing := getWithCookie(r, "/api/v1/projects/no-such-project", adminSession)
	if missing.Code != http.StatusNotFound {
		t.Errorf("存在しないキーの status = %d, want 404", missing.Code)
	}
}

// patchProjectWithCookie は Cookie 認証・CSRF つきで PATCH する。
// ifMatch が空文字ならヘッダを付けない（2.8 の省略時の検証に使う）。
func patchProjectWithCookie(
	r http.Handler, path, token, ifMatch, body string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// scalarString は scalarInt の文字列版。timestamptz を ::text で比べるのに使う。
func scalarString(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("クエリに失敗した（%s）: %v", sql, err)
	}
	return s
}
