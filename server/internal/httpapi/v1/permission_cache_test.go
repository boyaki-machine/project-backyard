// 実効権限のセッションキャッシュ（Design.md 6.4.5、手順6b）が
// ログインと GET /me でどう扱われるかの検証。
package v1

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// cachedIn は仕込んだトークン行にキャッシュを載せる。
// age はキャッシュを書いてからの経過時間。
func cachedIn(q *fakeQuerier, age time.Duration, permissions ...string) {
	raw, err := auth.EncodeCachedPermissions(append([]string{}, permissions...))
	if err != nil {
		panic(err)
	}
	q.tokenRow.CachedPermissions = raw
	q.tokenRow.PermissionsCachedAt = tsp(time.Now().Add(-age))
}

// ── ログイン（6.4.5「ログインごとに実効権限を計算し、セッションにキャッシュする」） ──

func TestLoginCachesEffectivePermissions(t *testing.T) {
	q := newFake(t)

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	if len(q.cacheSaves) != 1 {
		t.Fatalf("キャッシュの書き込み = %d回, want 1", len(q.cacheSaves))
	}
	save := q.cacheSaves[0]
	if len(q.created) != 1 || save.ID != q.created[0].ID {
		t.Errorf("書き込み先 = %q。発行したセッションのトークンIDと一致しない", save.ID)
	}

	got, err := auth.DecodeCachedPermissions(save.CachedPermissions)
	if err != nil {
		t.Fatalf("書き込んだ値を読めない: %v", err)
	}
	// newFake のアドミニストレータは3件を持つ。
	want := q.permissions[auth.SystemRoleAdministrator]
	if len(got) != len(want) {
		t.Errorf("キャッシュした権限 = %v, want %v と同数", got, want)
	}
	// 応答の permissions と同じ集合であること。片方だけ変わることを防ぐ。
	perms := viewOf(t, rec)["permissions"].([]any)
	if len(perms) != len(got) {
		t.Errorf("応答 = %v, キャッシュ = %v。両者が食い違っている", perms, got)
	}
}

// キャッシュを書けなくてもログインは成立する。
func TestLoginSucceedsWhenCacheWriteFails(t *testing.T) {
	q := newFake(t)
	q.cacheSaveErr = errors.New("書き込みできない")

	rec := postLogin(q, `{"email":"tanaka@example.com","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(viewOf(t, rec)["permissions"].([]any)) == 0 {
		t.Error("応答の permissions が空になった。キャッシュの失敗が本体に波及している")
	}
}

// ── GET /me ────────────────────────────────

// 有効なキャッシュがあれば role_permission を引かない。
// **ミドルウェアと同じ解決経路を使う**ことの確認でもある。
func TestMeUsesFreshCache(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	cachedIn(q, time.Second, "ticket.view")

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	if q.roleCalls != 0 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 0", q.roleCalls)
	}
	perms := viewOf(t, rec)["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "ticket.view" {
		t.Errorf("permissions = %v, want [ticket.view]（キャッシュの値）", perms)
	}
}

// TTL を過ぎたキャッシュは使わず、計算し直して書き戻す。
func TestMeRecomputesExpiredCache(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	cachedIn(q, auth.PermissionCacheTTL+time.Second, "ticket.view")

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	if q.roleCalls != 1 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 1", q.roleCalls)
	}
	if len(q.cacheSaves) != 1 {
		t.Errorf("キャッシュの書き戻し = %d回, want 1", len(q.cacheSaves))
	}
	perms := viewOf(t, rec)["permissions"].([]any)
	if len(perms) != len(q.permissions[auth.SystemRoleAdministrator]) {
		t.Errorf("permissions = %v。ロールから計算し直していない", perms)
	}
}

// キャッシュはスコープを超えない（Design.md 6.4.1「縮小のみ」）。
func TestMeCacheIsNarrowedByScopes(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `["ticket.view"]`)
	cachedIn(q, time.Second, "user.manage", "ticket.view")

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	perms := viewOf(t, rec)["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "ticket.view" {
		t.Errorf("permissions = %v, want [ticket.view]。スコープ外がキャッシュ経由で漏れている", perms)
	}
}

// プロジェクト層はキャッシュしない。projects[] は毎回引いて組み立てる。
func TestMeProjectPermissionsAreNotCached(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	cachedIn(q, time.Second, "ticket.view")
	q.memberships = []gen.ListProjectMembershipsByActorRow{
		{ProjectID: "01P1", ProjectKey: "my-app", ProjectName: "社内タスク", Status: "active",
			RoleKey: "project_admin", PermissionKey: txt("ticket.close")},
	}

	rec := authed(q, http.MethodGet, "/api/v1/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	projects := viewOf(t, rec)["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("projects = %v, want 1件", projects)
	}
	// ( キャッシュしたシステム層 ∪ プロジェクト層 ) ∩ スコープ。
	perms := projects[0].(map[string]any)["permissions"].([]any)
	if len(perms) != 2 {
		t.Errorf("projects[0].permissions = %v, want 2件（ticket.view ∪ ticket.close）", perms)
	}
}
