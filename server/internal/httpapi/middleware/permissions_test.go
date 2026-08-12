package middleware

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// withCache は、実効権限をキャッシュ済みのプリンシパルを返す。
// age はキャッシュを書いてからの経過時間。
func withCache(p *auth.Principal, age time.Duration, permissions ...string) *auth.Principal {
	at := time.Now().Add(-age)
	// 可変長引数を渡さないと nil になる。空のキャッシュ（権限0件）と
	// キャッシュ不在は別の状態なので、必ず非 nil にしておく。
	p.CachedPermissions = append([]string{}, permissions...)
	p.PermissionsCachedAt = &at
	return p
}

// ── キャッシュを読む（Design.md 6.4.5） ────────────────────────

// キャッシュが有効なら role_permission を引かない。
// これが 6b の狙いそのものである（認可のためにクエリを足さない）。
func TestSystemPermissionsUsesFreshCache(t *testing.T) {
	q := seededQuerier()
	p := withCache(principal(auth.SystemRoleOperator), time.Second, "user.manage")

	w, reached := serveAuthz(p, "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusNoContent || !reached {
		t.Fatalf("code = %d, reached = %v。キャッシュした権限で通っていない", w.Code, reached)
	}
	if q.roleCalls != 0 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 0（キャッシュを使っていない）", q.roleCalls)
	}
	if len(q.cacheSaves) != 0 {
		t.Errorf("キャッシュを %d 回書き戻した, want 0（読めたものを書き直している）", len(q.cacheSaves))
	}
}

// キャッシュはシステムロールの割り当てより優先される。
// オペレータのロールに user.manage は無いが、キャッシュにあれば通る。
// これは「無効化を忘れると古い権限で通る」ことの裏返しであり、
// InvalidateActorPermissionCache が要る理由でもある。
func TestSystemPermissionsCacheWinsOverRole(t *testing.T) {
	q := seededQuerier()
	p := withCache(principal(auth.SystemRoleOperator), time.Second, "user.manage")

	if _, reached := serveAuthz(p, "/x", "/x", RequirePermission(q, "user.manage")); !reached {
		t.Error("キャッシュにある権限で通らなかった")
	}

	// 逆向きも同じ。ロールが持っていてもキャッシュに無ければ通らない。
	p2 := withCache(principal(auth.SystemRoleAdministrator), time.Second, "ticket.view")
	if _, reached := serveAuthz(p2, "/x", "/x", RequirePermission(q, "user.manage")); reached {
		t.Error("キャッシュに無い権限が通った")
	}
}

// TTL を過ぎたキャッシュは使わず、計算し直して書き戻す。
func TestSystemPermissionsRecomputesExpiredCache(t *testing.T) {
	q := seededQuerier()
	p := withCache(principal(auth.SystemRoleOperator), auth.PermissionCacheTTL+time.Second, "user.manage")

	w, reached := serveAuthz(p, "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	// オペレータは user.manage を持たない（DbDesign.md 7.3）。
	if w.Code != http.StatusForbidden || reached {
		t.Fatalf("code = %d, reached = %v, want 403。期限切れのキャッシュを使っている", w.Code, reached)
	}
	if q.roleCalls != 1 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 1", q.roleCalls)
	}
	if len(q.cacheSaves) != 1 {
		t.Fatalf("キャッシュの書き戻し = %d回, want 1", len(q.cacheSaves))
	}
}

// 権限0件のキャッシュは「キャッシュが無い」ではない。計算し直さない。
func TestSystemPermissionsEmptyCacheIsStillACache(t *testing.T) {
	q := seededQuerier()
	p := withCache(principal(auth.SystemRoleAdministrator), time.Second)

	w, _ := serveAuthz(p, "/x", "/x", RequirePermission(q, "user.manage"))

	if w.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", w.Code)
	}
	if q.roleCalls != 0 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 0（空のキャッシュを不在と誤認している）", q.roleCalls)
	}
}

// **読むときにもスコープとの積を取る。** キャッシュのほうが広くても、
// トークンのスコープを超えては通さない（Design.md 6.4.1 の「縮小のみ」）。
func TestSystemPermissionsCacheIsStillNarrowedByScopes(t *testing.T) {
	q := seededQuerier()
	p := withCache(principal(auth.SystemRoleAdministrator, "ticket.view"),
		time.Second, "user.manage", "ticket.view")

	if _, reached := serveAuthz(p, "/x", "/x", RequirePermission(q, "user.manage")); reached {
		t.Error("スコープ外の権限がキャッシュ経由で通った")
	}
	if _, reached := serveAuthz(p, "/x", "/x", RequirePermission(q, "ticket.view")); !reached {
		t.Error("スコープ内の権限が通らなかった")
	}
}

// ── キャッシュを書く ────────────────────────────────

// キャッシュが無ければ計算し、その結果を書き戻す。
func TestSystemPermissionsSavesComputedCache(t *testing.T) {
	q := seededQuerier()
	p := principal(auth.SystemRoleAdministrator)

	if _, reached := serveAuthz(p, "/x", "/x", RequirePermission(q, "user.manage")); !reached {
		t.Fatal("アドミニストレータが user.manage で拒否された")
	}

	if len(q.cacheSaves) != 1 {
		t.Fatalf("キャッシュの書き戻し = %d回, want 1", len(q.cacheSaves))
	}
	save := q.cacheSaves[0]
	if save.ID != p.TokenID {
		t.Errorf("書き戻し先 = %q, want %q（トークン単位で書く）", save.ID, p.TokenID)
	}
	got, err := auth.DecodeCachedPermissions(save.CachedPermissions)
	if err != nil {
		t.Fatalf("書き戻した値を読めない: %v", err)
	}
	if !auth.HasPermission(got, "user.manage") {
		t.Errorf("書き戻した値 = %v。計算結果が入っていない", got)
	}
}

// 同じリクエスト内でミドルウェアを重ねても、書き戻しは1回だけ。
// 2段目はコンテキストの結果を使う（手順6a）。
func TestSystemPermissionsSavesOncePerRequest(t *testing.T) {
	q := seededQuerier()

	_, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/x", "/x",
		func(next http.Handler) http.Handler {
			return RequirePermission(q, "user.manage")(RequirePermission(q, "role.manage")(next))
		})

	if !reached {
		t.Fatal("ハンドラに到達しなかった")
	}
	if q.roleCalls != 1 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 1", q.roleCalls)
	}
	if len(q.cacheSaves) != 1 {
		t.Errorf("キャッシュの書き戻し = %d回, want 1", len(q.cacheSaves))
	}
}

// **書き戻しに失敗しても判定は変えない。** 次のリクエストで計算し直すだけで、
// 応答の中身は変わらない（last_used_at と同じ扱い）。
func TestSystemPermissionsSurvivesCacheWriteFailure(t *testing.T) {
	q := seededQuerier()
	q.cacheSaveErr = errors.New("書き込みできない")

	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/x", "/x",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusNoContent || !reached {
		t.Errorf("code = %d, reached = %v。キャッシュを書けないだけで拒否している", w.Code, reached)
	}
}

// システムロールを持たないアクター（エージェント）は DB を引かない。
// 空の結果をキャッシュへ書きに行くこともしない。
func TestSystemPermissionsNoRoleDoesNotQuery(t *testing.T) {
	q := seededQuerier()

	w, _ := serveAuthz(principal(""), "/x", "/x", RequirePermission(q, "ticket.view"))

	if w.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", w.Code)
	}
	if q.roleCalls != 0 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 0", q.roleCalls)
	}
}

// プロジェクト層はキャッシュしない。到達可否の判定に同じクエリが要るためである
// （0012 の説明）。システムロール層のキャッシュがあっても毎回引く。
func TestProjectAuthzIsNotCachedAcrossRequests(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_viewer")
	p := withCache(principal(auth.SystemRoleOperator), time.Second, "project.view")

	for i := range 2 {
		w, _ := serveAuthz(p, "/projects/{key}", "/projects/my-app",
			RequireProjectPermission(q, "project.view"))
		if w.Code != http.StatusNoContent {
			t.Fatalf("%d回目: code = %d, want 204", i+1, w.Code)
		}
	}

	if q.projectCalls != 2 {
		t.Errorf("FindProjectAuthzByKey の呼び出し = %d回, want 2（プロジェクト層を跨いでキャッシュしている）", q.projectCalls)
	}
	if q.roleCalls != 0 {
		t.Errorf("ListRolePermissions の呼び出し = %d回, want 0（システムロール層はキャッシュが効くはず）", q.roleCalls)
	}
}
