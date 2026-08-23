package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ルート定義に CSRF とレート制限が並んでいることを、ハンドラではなく
// ルータ越しに確かめる（Design.md 6.4.4「必要権限はルート定義に宣言する」）。
// ミドルウェア単体の検証は middleware パッケージ側にある。

// **Cookie 認証の POST に X-PB-CSRF が無ければ 403**（ApiDesign.md 2.4）。
func TestLogoutRequiresCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	// pb_csrf も X-PB-CSRF も付けない。
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
	if len(q.revoked) != 0 {
		t.Error("CSRF に失敗したのにトークンを失効させた")
	}
}

// GET は状態を変えないので CSRF を要求しない。
func TestMeDoesNotRequireCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
}

// Bearer 認証には CSRF を要求しない（ApiDesign.md 2.4）。CLI から
// pb_csrf を用意させることになってしまうため。
func TestLogoutWithBearerSkipsCSRF(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
}

// ログインは IP あたり loginRateLimit 回/分（ApiDesign.md 2.9）。
//
// **成否によらず数える。** 本文を壊した 400 を並べているのは、ハンドラの
// 結果と無関係に「送った回数」で制限されることを固定するためである
// （ロックの副作用も入らない）。
func TestLoginIsRateLimitedPerIP(t *testing.T) {
	q := newFake(t)
	r := router(q)

	for i := 1; i <= loginRateLimit; i++ {
		rec := call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%d回目 status = %d, want 400（上限は %d）", i, rec.Code, loginRateLimit)
		}
	}

	over := call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("%d回目 status = %d, want 429（body=%s）",
			loginRateLimit+1, over.Code, over.Body.String())
	}

	got := errorOf(t, over)
	if got.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", got.Code)
	}
	if got.RetryAfterSec <= 0 {
		t.Errorf("retry_after_sec = %d, want 1以上", got.RetryAfterSec)
	}
	if h := over.Header().Get("Retry-After"); h == "" {
		t.Error("Retry-After ヘッダが無い（ApiDesign.md 2.9）")
	}
	if h := over.Header().Get("X-RateLimit-Limit"); h != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want 10", h)
	}
}

// ログインの制限は他のエンドポイントを巻き込まない（キーも上限も別）。
func TestLoginRateLimitDoesNotAffectMe(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	r := router(q)

	for range loginRateLimit + 1 {
		call(r, http.MethodPost, "/api/v1/auth/login", `{"email":`)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me の status = %d, want 200（ログインの制限に巻き込まれている）", rec.Code)
	}
}

// 認証済みリクエストには X-RateLimit ヘッダが付く（アクターあたり 600回/分）。
func TestAuthenticatedRequestsCarryRateLimitHeaders(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "600" {
		t.Errorf("X-RateLimit-Limit = %q, want 600", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "599" {
		t.Errorf("X-RateLimit-Remaining = %q, want 599", got)
	}
}

// ── タグ・スプリントの認可（手順16a。ApiDesign.md 9.11 / 9.12）─────
//
// **読みと書きで必要権限が違う**（一覧は ticket.view、定義の変更は project.edit）。
// ルータ越しに確かめるのは、この振り分けがルート定義に宣言されていること
// （Design.md 6.4.4）そのものが検証の対象だからである。ハンドラを直接呼ぶと
// 宣言を1本消しても気づけない。

// tagRouteFake は「プロジェクト demo のメンバーだが project.edit を持たない」
// 状態を作る。システムロールにも project.edit を入れない。
func tagRouteFake(t *testing.T, projectRole string) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	// システムロール側からは ticket.view を外す。残したままだと、
	// プロジェクトロールを持たない利用者にも権限が渡り、
	// 「メンバーかどうか」ではなく「システムロール」を測ることになる。
	q.permissions[auth.SystemRoleOperator] = []string{"project.view"}
	q.permissions["project_member"] = []string{"ticket.view", "ticket.create"}
	q.permissions["project_admin"] = []string{"ticket.view", "project.edit"}
	q.projectIDByKey = map[string]string{"demo": testProjectID}
	q.tagByID = map[string]gen.GetTagByIDRow{}
	q.sprintByID = map[string]gen.GetSprintByIDRow{}
	q.withProjectMember("demo", projectRole)
	return q
}

// callAsMember はオペレータとして叩く。
//
// **アドミニストレータでは非メンバーの経路を検証できない。** projectAuthz の
// Reachable は `(RoleKey != "" || IsAdministrator())` であり、アドミニストレータは
// メンバーでなくても全プロジェクトへ到達する（middleware/authz.go）。
// ここで見たいのはプロジェクトロール側の判定なので、システムロールは
// オペレータに落とす。
func callAsMember(q *fakeQuerier, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tokenAs(q, auth.SystemRoleOperator)})
	if method != http.MethodGet {
		addCSRF(req)
	}
	rec := httptest.NewRecorder()
	// **Tx を渡す。** チケットの作成・並べ替え（手順16b）は単一トランザクションで
	// 行うため、Deps.Tx が nil だとハンドラが nil を呼ぶ。タグ・スプリントは
	// 使わないので、渡しても振る舞いは変わらない。
	routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}}).ServeHTTP(rec, req)
	return rec
}

// ticket.view だけを持つメンバーは、一覧は読めるが定義は変えられない。
func TestTagRoutesSplitReadAndWritePermissions(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"タグ一覧は読める", http.MethodGet, "/api/v1/projects/demo/tags", "", http.StatusOK},
		{"スプリント一覧は読める", http.MethodGet, "/api/v1/projects/demo/sprints", "", http.StatusOK},
		{"タグは作れない", http.MethodPost, "/api/v1/projects/demo/tags", `{"name":"設計"}`, http.StatusForbidden},
		{"タグは変えられない", http.MethodPatch, "/api/v1/projects/demo/tags/" + testTagID, `{"name":"x"}`, http.StatusForbidden},
		{"タグは消せない", http.MethodDelete, "/api/v1/projects/demo/tags/" + testTagID, "", http.StatusForbidden},
		{"スプリントは作れない", http.MethodPost, "/api/v1/projects/demo/sprints", `{"name":"S"}`, http.StatusForbidden},
		{"スプリントは変えられない", http.MethodPatch, "/api/v1/projects/demo/sprints/" + testSprintID, `{"name":"S"}`, http.StatusForbidden},
		{"スプリントは消せない", http.MethodDelete, "/api/v1/projects/demo/sprints/" + testSprintID, "", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := tagRouteFake(t, "project_member")
			rec := callAsMember(q, c.method, c.path, c.body)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

// project.edit を持つプロジェクト管理者は定義を変えられる。
func TestTagRoutesAllowProjectAdmin(t *testing.T) {
	q := tagRouteFake(t, "project_admin")
	rec := callAsMember(q, http.MethodPost, "/api/v1/projects/demo/tags", `{"name":"設計"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	q2 := tagRouteFake(t, "project_admin")
	rec2 := callAsMember(q2, http.MethodPost, "/api/v1/projects/demo/sprints", `{"name":"Sprint 4"}`)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec2.Code, rec2.Body.String())
	}
}

// **非メンバーには 404**（Design.md 6.4.5「存在を隠す」）。403 ではない。
func TestTagRoutesHideProjectFromNonMember(t *testing.T) {
	q := tagRouteFake(t, "") // プロジェクトは在るが非メンバー
	for _, path := range []string{
		"/api/v1/projects/demo/tags",
		"/api/v1/projects/demo/sprints",
	} {
		rec := callAsMember(q, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// 状態変更系は CSRF を要求する（2.4）。
func TestTagWriteRequiresCSRF(t *testing.T) {
	q := tagRouteFake(t, "project_admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/demo/tags",
		strings.NewReader(`{"name":"設計"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tokenAs(q, auth.SystemRoleOperator)})
	// CSRF を付けない。
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
	if len(q.createdTags) != 0 {
		t.Error("CSRF に失敗したのにタグを作った")
	}
}

// ── チケットの認可（手順16b。ApiDesign.md 9.2 / 9.3 / 9.4）─────────
//
// **3本とも必要権限が違う**（読み ticket.view / 作成 ticket.create /
// 並べ替え ticket.edit）。タグ・スプリントと同じく、ルータ越しに確かめるのは
// 振り分けがルート定義に宣言されていること（Design.md 6.4.4）そのものが
// 検証の対象だからである。

// ticketRouteFake は tagRouteFake と同じ土台に、チケットの読み書きに要る
// 最小限の状態を足す。**project_viewer は ticket.view だけ**を持つ。
func ticketRouteFake(t *testing.T, projectRole string, perms ...string) *fakeQuerier {
	t.Helper()
	q := tagRouteFake(t, projectRole)
	// **権限を差し替えたら withProjectMember を呼び直す。** あの補助は呼んだ時点の
	// q.permissions を読んで projectAuthzRows を組み立てるので、後から書き換えても
	// 反映されない（そのまま進めると「非メンバー」の 404 になる）。
	if len(perms) > 0 && projectRole != "" {
		q.permissions[projectRole] = perms
		q.withProjectMember("demo", projectRole)
	}
	q.ticket.bySeq = map[int32]gen.GetTicketBySeqRow{}
	q.ticket.briefByID = map[string]gen.GetTicketBriefRow{}
	q.ticket.idBySeq = map[int32]string{}
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{
		31: {ID: testTicketID, SortKey: txt("0|n:"), Version: 1},
		44: {ID: testTicketID2, SortKey: txt("0|u:"), Version: 1},
	}
	q.ticket.initialStatusKey = "todo"
	q.ticket.nextSeq = 31
	return q
}

// ticket.view しか持たない閲覧者は、一覧は読めるが作成も並べ替えもできない。
func TestTicketRoutesSplitPermissions(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"一覧は読める", http.MethodGet, "/api/v1/projects/demo/tickets", "", http.StatusOK},
		{"作れない", http.MethodPost, "/api/v1/projects/demo/tickets",
			`{"type":"task","title":"x"}`, http.StatusForbidden},
		{"並べ替えられない", http.MethodPost, "/api/v1/projects/demo/tickets/31/move",
			`{"position":"last"}`, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ticketRouteFake(t, "project_viewer", "ticket.view")
			rec := callAsMember(q, c.method, c.path, c.body)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

// ticket.create を持つメンバーは作れる。並べ替えは ticket.edit が要る。
func TestTicketRoutesAllowCreateForMember(t *testing.T) {
	q := ticketRouteFake(t, "project_member", "ticket.view", "ticket.create")
	rec := callAsMember(q, http.MethodPost, "/api/v1/projects/demo/tickets",
		`{"type":"task","title":"作れる"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ticket.edit を持たないので並べ替えは 403
	q2 := ticketRouteFake(t, "project_member", "ticket.view", "ticket.create")
	rec2 := callAsMember(q2, http.MethodPost, "/api/v1/projects/demo/tickets/31/move",
		`{"position":"last"}`)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec2.Code, rec2.Body.String())
	}
}

func TestTicketRoutesAllowMoveWithTicketEdit(t *testing.T) {
	q := ticketRouteFake(t, "project_admin", "ticket.view", "ticket.create", "ticket.edit")
	rec := callAsMember(q, http.MethodPost, "/api/v1/projects/demo/tickets/31/move",
		`{"position":"last"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// **非メンバーには 404**（Design.md 6.4.5「存在を隠す」）。403 ではない。
func TestTicketRoutesHideProjectFromNonMember(t *testing.T) {
	q := ticketRouteFake(t, "") // プロジェクトは在るが非メンバー
	rec := callAsMember(q, http.MethodGet, "/api/v1/projects/demo/tickets", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// 状態変更系は CSRF を要求する（2.4）。
func TestTicketWriteRequiresCSRF(t *testing.T) {
	q := ticketRouteFake(t, "project_member", "ticket.view", "ticket.create")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/demo/tickets",
		strings.NewReader(`{"type":"task","title":"x"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tokenAs(q, auth.SystemRoleOperator)})
	// CSRF を付けない。
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "csrf_failed" {
		t.Errorf("error.code = %q, want csrf_failed", got)
	}
}
