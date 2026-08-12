package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// validToken は認証を通るトークン1件を fake に仕込み、平文を返す。
func validToken(q *fakeQuerier, scopes string) string {
	const plaintext = "pb_sess_dummy-session-token"
	q.tokenRow = gen.FindAccessTokenByHashRow{
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9T",
		TokenType:   auth.TokenTypeSession,
		Scopes:      []byte(scopes),
		ExpiresAt:   ts(time.Now().Add(SessionMaxAge)),
		ActorID:     testActorID,
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		IsActive:    true,
		SystemRole:  txt(auth.SystemRoleAdministrator),
		Email:       txt(testEmail),
	}
	q.profileRow = gen.GetActorProfileRow{
		ActorID:     testActorID,
		Kind:        auth.ActorKindUser,
		DisplayName: "田中",
		Email:       txt(testEmail),
		SystemRole:  txt(auth.SystemRoleAdministrator),
		Locale:      txt("ja"),
		Timezone:    txt("Asia/Tokyo"),
		MustChange:  pgBool(false),
	}
	return plaintext
}

// authed は Cookie 認証で1リクエスト投げる。
func authed(q gen.Querier, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)
	return rec
}

// GET /me は 3.1 と同一構造を返す（ApiDesign.md 4.1）。
func TestMeReturnsSessionView(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	view := viewOf(t, rec)
	actor := view["actor"].(map[string]any)
	if actor["id"] != testActorID || actor["email"] != testEmail {
		t.Errorf("actor = %v", actor)
	}
	if actor["locale"] != "ja" || actor["timezone"] != "Asia/Tokyo" {
		t.Errorf("locale/timezone = %v / %v", actor["locale"], actor["timezone"])
	}
	if view["expires_at"] == nil {
		t.Error("expires_at が null（セッションには期限がある）")
	}
	perms := view["permissions"].([]any)
	if len(perms) != 3 {
		t.Errorf("permissions = %v, want 3件", perms)
	}
}

// ログイン応答と GET /me のキー構成が一致する（ApiDesign.md 4.1「3.1 と同一構造」）。
func TestMeAndLoginShareShape(t *testing.T) {
	loginQ := newFake(t)
	loginView := viewOf(t, postLogin(loginQ,
		`{"email":"tanaka@example.com","password":"`+testPassword+`"}`))

	meQ := newFake(t)
	token := validToken(meQ, `[]`)
	meView := viewOf(t, authed(meQ, http.MethodGet, "/api/v1/me", token))

	for key := range loginView {
		if _, ok := meView[key]; !ok {
			t.Errorf("GET /me に %q が無い", key)
		}
	}
	for key := range meView {
		if _, ok := loginView[key]; !ok {
			t.Errorf("ログイン応答に %q が無い", key)
		}
	}

	loginActor := loginView["actor"].(map[string]any)
	meActor := meView["actor"].(map[string]any)
	for key := range loginActor {
		if _, ok := meActor[key]; !ok {
			t.Errorf("GET /me の actor に %q が無い", key)
		}
	}
}

// projects[] は所属プロジェクトごとに畳まれ、実効権限が付く（Design.md 6.4.1）。
func TestMeFoldsProjectMemberships(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	q.memberships = []gen.ListProjectMembershipsByActorRow{
		{ProjectID: "01P1", ProjectKey: "my-app", ProjectName: "社内タスク", Status: "active",
			RoleKey: "project_admin", PermissionKey: txt("ticket.close")},
		{ProjectID: "01P1", ProjectKey: "my-app", ProjectName: "社内タスク", Status: "active",
			RoleKey: "project_admin", PermissionKey: txt("ticket.view")},
		{ProjectID: "01P2", ProjectKey: "other", ProjectName: "別件", Status: "archived",
			RoleKey: "project_viewer", PermissionKey: txt("project.view")},
	}

	view := viewOf(t, authed(q, http.MethodGet, "/api/v1/me", token))
	projects := view["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("projects = %d件, want 2（プロジェクトごとに畳めていない）", len(projects))
	}

	first := projects[0].(map[string]any)
	if first["key"] != "my-app" || first["role"] != "project_admin" {
		t.Errorf("projects[0] = %v", first)
	}
	// システムロール（3件）にプロジェクトロールの ticket.close が合流する。
	perms := first["permissions"].([]any)
	if len(perms) != 4 {
		t.Errorf("projects[0].permissions = %v, want 4件（システム3 ∪ プロジェクト1）", perms)
	}
}

// トークンスコープは実効権限を縮小する（Design.md 6.4.1）。
func TestMeAppliesTokenScopes(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `["ticket.view"]`)

	view := viewOf(t, authed(q, http.MethodGet, "/api/v1/me", token))
	perms := view["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "ticket.view" {
		t.Errorf("permissions = %v, want [ticket.view]", perms)
	}
}

// 資格情報が無ければ 401（認証必須グループにある）。
func TestMeRequiresAuthentication(t *testing.T) {
	q := newFake(t)
	q.tokenErr = notFoundErr

	rec := call(router(q), http.MethodGet, "/api/v1/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// 期限のないトークン（APIトークン）では expires_at が null になる。
func TestMeNullExpiresAtForTokenWithoutExpiry(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	q.tokenRow.ExpiresAt.Valid = false

	view := viewOf(t, authed(q, http.MethodGet, "/api/v1/me", token))
	if view["expires_at"] != nil {
		t.Errorf("expires_at = %v, want null", view["expires_at"])
	}
}

// app_user を持たないアクターでは email / system_role が null になる
// （ApiDesign.md 6.1「意味を持たないフィールドは null」）。
func TestMeNullFieldsForNonUserActor(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	q.tokenRow.ActorKind = auth.ActorKindAgent
	q.tokenRow.SystemRole = pgNull()
	q.tokenRow.Email = pgNull()
	q.profileRow = gen.GetActorProfileRow{
		ActorID: testActorID, Kind: auth.ActorKindAgent, DisplayName: "claude-code",
	}

	view := viewOf(t, authed(q, http.MethodGet, "/api/v1/me", token))
	actor := view["actor"].(map[string]any)
	for _, key := range []string{"email", "system_role", "locale", "timezone"} {
		if v, ok := actor[key]; !ok {
			t.Errorf("actor に %q が無い（省略してはならない）", key)
		} else if v != nil {
			t.Errorf("actor[%q] = %v, want null", key, v)
		}
	}
	if len(view["permissions"].([]any)) != 0 {
		t.Errorf("permissions = %v, want []（システムロールが無い）", view["permissions"])
	}
}

// 日時は ISO8601 UTC・秒精度で出る（ApiDesign.md 2.2）。
func TestExpiresAtFormat(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	q.tokenRow.ExpiresAt = ts(time.Date(2026, 8, 25, 9, 3, 12, 123456000, time.FixedZone("JST", 9*3600)))

	view := viewOf(t, authed(q, http.MethodGet, "/api/v1/me", token))
	if got := view["expires_at"]; got != "2026-08-25T00:03:12Z" {
		t.Errorf("expires_at = %v, want 2026-08-25T00:03:12Z", got)
	}
}
