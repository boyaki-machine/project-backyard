package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// authzQuerier は認可経路の2クエリと監査ログの書き込みを持つ Querier。
//
// 権限の割り当ては DbDesign.md 7.3 のシードが正本なので、ここには
// **テストが必要とする分だけ**を置く。シードそのものの検証は
// 実DBに対する結合テスト（httpapi/authz_integration_test.go）で行う。
type authzQuerier struct {
	gen.Querier // 未使用のメソッドは nil のまま（呼べば panic して気づける）

	rolePermissions map[string][]string
	projectRows     map[string][]gen.FindProjectAuthzByKeyRow

	roleErr    error
	projectErr error

	roleCalls    int
	projectCalls int
	audits       []gen.InsertAuditLogParams

	// 実効権限のセッションキャッシュ（手順6b）。
	cacheSaves   []gen.SaveTokenPermissionCacheParams
	cacheSaveErr error
}

func (q *authzQuerier) ListRolePermissions(_ context.Context, roleKey string) ([]string, error) {
	q.roleCalls++
	if q.roleErr != nil {
		return nil, q.roleErr
	}
	return q.rolePermissions[roleKey], nil
}

func (q *authzQuerier) FindProjectAuthzByKey(
	_ context.Context, arg gen.FindProjectAuthzByKeyParams,
) ([]gen.FindProjectAuthzByKeyRow, error) {
	q.projectCalls++
	if q.projectErr != nil {
		return nil, q.projectErr
	}
	return q.projectRows[arg.ProjectKey], nil
}

func (q *authzQuerier) SaveTokenPermissionCache(
	_ context.Context, arg gen.SaveTokenPermissionCacheParams,
) error {
	if q.cacheSaveErr != nil {
		return q.cacheSaveErr
	}
	q.cacheSaves = append(q.cacheSaves, arg)
	return nil
}

func (q *authzQuerier) InsertAuditLog(_ context.Context, arg gen.InsertAuditLogParams) error {
	q.audits = append(q.audits, arg)
	return nil
}

const (
	testActorID   = "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
	testProjectID = "01K2F8QW3H7YRJ4M5N6P7Q8R9P"
)

// seededQuerier は DbDesign.md 7.3 の割り当てのうち、テストで使う分を持つ。
func seededQuerier() *authzQuerier {
	return &authzQuerier{
		rolePermissions: map[string][]string{
			auth.SystemRoleOperator: {
				"project.view", "ticket.view", "ticket.create", "ticket.close", "export.excel",
			},
			auth.SystemRoleAdministrator: {
				"project.view", "project.create", "project.edit", "project.archive",
				"ticket.view", "ticket.create", "ticket.close", "user.manage", "role.manage",
			},
			"project_viewer": {"project.view", "ticket.view", "knowledge.view"},
			"project_admin":  {"project.view", "project.edit", "ticket.view", "ticket.close"},
		},
		projectRows: map[string][]gen.FindProjectAuthzByKeyRow{},
	}
}

// withMember はプロジェクト my-app に role のメンバーがいる状態を作る。
// role が空文字なら「プロジェクトは在るが非メンバー」（LEFT JOIN で NULL の行）。
func (q *authzQuerier) withMember(key, role string) *authzQuerier {
	if role == "" {
		q.projectRows[key] = []gen.FindProjectAuthzByKeyRow{{
			ProjectID: testProjectID, ProjectKey: key, ProjectStatus: "active",
		}}
		return q
	}
	var rows []gen.FindProjectAuthzByKeyRow
	for _, perm := range q.rolePermissions[role] {
		rows = append(rows, gen.FindProjectAuthzByKeyRow{
			ProjectID: testProjectID, ProjectKey: key, ProjectStatus: "active",
			RoleKey:       pgtype.Text{String: role, Valid: true},
			PermissionKey: pgtype.Text{String: perm, Valid: true},
		})
	}
	q.projectRows[key] = rows
	return q
}

// principal は認証済みプリンシパルを作る。
func principal(systemRole string, scopes ...string) *auth.Principal {
	return &auth.Principal{
		ActorID:     testActorID,
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		Email:       "tanaka@example.com",
		SystemRole:  systemRole,
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9T",
		TokenType:   auth.TokenTypeSession,
		Scopes:      scopes,
		Source:      auth.SourceCookie,
	}
}

// serveAuthz は mw を通したリクエストを実行し、応答とハンドラ到達の有無を返す。
//
// chi のルータ越しに実行するのは、RequireProjectPermission が URL パラメータを
// chi.URLParam で読むためである。素の httptest.NewRequest では {key} が取れない。
func serveAuthz(p *auth.Principal, pattern, target string,
	mw func(http.Handler) http.Handler) (*httptest.ResponseRecorder, bool) {

	reached := false
	r := chi.NewRouter()
	r.With(mw).Get(pattern, func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest("GET", target, nil)
	if p != nil {
		req = req.WithContext(auth.NewPrincipalContext(req.Context(), p))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, reached
}

// auditDetail は記録された監査ログの detail を読む。
func auditDetail(t *testing.T, arg gen.InsertAuditLogParams) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(arg.Detail, &d); err != nil {
		t.Fatalf("detail が JSON でない: %v", err)
	}
	return d
}

// ── RequirePermission ────────────────────────────────

func TestRequirePermissionAllowsAdministrator(t *testing.T) {
	q := seededQuerier()

	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if !reached {
		t.Error("ハンドラに到達していない")
	}
	if len(q.audits) != 0 {
		t.Errorf("許可したのに監査ログが %d 件書かれている", len(q.audits))
	}
}

func TestRequirePermissionDeniesOperator(t *testing.T) {
	q := seededQuerier()

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "forbidden" {
		t.Errorf("code = %q, want forbidden", got)
	}
	if reached {
		t.Error("403 なのにハンドラへ到達している")
	}
}

func TestRequirePermissionRecordsPermissionDenied(t *testing.T) {
	q := seededQuerier()

	serveAuthz(principal(auth.SystemRoleOperator), "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if len(q.audits) != 1 {
		t.Fatalf("監査ログが %d 件。Design.md 6.4.5 は permission.denied の記録を求める", len(q.audits))
	}
	a := q.audits[0]
	if a.Action != "permission.denied" {
		t.Errorf("action = %q, want permission.denied", a.Action)
	}
	if a.Result != "failure" {
		t.Errorf("result = %q, want failure", a.Result)
	}
	if a.ActorID.String != testActorID {
		t.Errorf("actor_id = %q, want %q", a.ActorID.String, testActorID)
	}
	d := auditDetail(t, a)
	if d["required_permission"] != "user.manage" {
		t.Errorf("detail.required_permission = %v, want user.manage", d["required_permission"])
	}
	if d["path"] != "/admin/users" {
		t.Errorf("detail.path = %v, want /admin/users", d["path"])
	}
}

func TestRequirePermissionIntersectsScopes(t *testing.T) {
	q := seededQuerier()

	// 管理者のトークンでも、スコープが user.manage を含まなければ通さない。
	// 「トークンスコープは権限の上限であり縮小のみ可能」（Design.md 6.4.1）。
	w, _ := serveAuthz(principal(auth.SystemRoleAdministrator, "ticket.view"), "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403。スコープで絞られていない（body=%s）", w.Code, w.Body.String())
	}
}

func TestRequirePermissionWithoutSystemRole(t *testing.T) {
	q := seededQuerier()

	// エージェント（app_user の行が無く system_role を持たない）。
	p := principal("")
	p.ActorKind = auth.ActorKindAgent

	w, _ := serveAuthz(p, "/admin/users", "/admin/users", RequirePermission(q, "user.manage"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", w.Code, w.Body.String())
	}
	if q.roleCalls != 0 {
		t.Errorf("system_role が空なのに ListRolePermissions を %d 回呼んでいる", q.roleCalls)
	}
}

func TestRequirePermissionWithoutPrincipal(t *testing.T) {
	q := seededQuerier()

	// Authenticate より前に置いた場合。401 ではなく 500 にして
	// ルート定義の誤りとして表に出す。
	w, reached := serveAuthz(nil, "/admin/users", "/admin/users", RequirePermission(q, "user.manage"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", w.Code, w.Body.String())
	}
	if reached {
		t.Error("ハンドラへ到達している")
	}
}

func TestRequirePermissionDatabaseFailure(t *testing.T) {
	q := seededQuerier()
	q.roleErr = errors.New("接続できない")

	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/admin/users", "/admin/users",
		RequirePermission(q, "user.manage"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500。権限を読めないときに通してはならない（body=%s）",
			w.Code, w.Body.String())
	}
	if reached {
		t.Error("権限を読めないのにハンドラへ到達している")
	}
}

func TestRequirePermissionCachesWithinRequest(t *testing.T) {
	q := seededQuerier()

	// 2つ重ねても DB は1回しか引かない。
	chained := func(next http.Handler) http.Handler {
		return RequirePermission(q, "user.manage")(RequirePermission(q, "role.manage")(next))
	}
	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/admin/users", "/admin/users", chained)

	if w.Code != http.StatusNoContent || !reached {
		t.Fatalf("status = %d, reached = %v, want 204 / true（body=%s）", w.Code, reached, w.Body.String())
	}
	if q.roleCalls != 1 {
		t.Errorf("ListRolePermissions を %d 回呼んでいる。同一リクエスト内は1回であること", q.roleCalls)
	}
}

// ── RequireProjectPermission ────────────────────────────────

const projectPattern = "/projects/{" + ProjectKeyURLParam + "}"

func TestRequireProjectPermissionAllowsMember(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_admin")

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.edit"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if !reached {
		t.Error("ハンドラに到達していない")
	}
}

func TestRequireProjectPermissionUnionsSystemAndProjectRoles(t *testing.T) {
	// project_viewer は ticket.close を持たないが、オペレータの
	// システムロールが持つ。6.4.1 の式は和集合なので通る。
	q := seededQuerier().withMember("my-app", "project_viewer")

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "ticket.close"))

	if w.Code != http.StatusNoContent || !reached {
		t.Fatalf("status = %d, reached = %v, want 204 / true（body=%s）", w.Code, reached, w.Body.String())
	}
}

func TestRequireProjectPermissionDeniesMemberWithoutPermission(t *testing.T) {
	// 閲覧者としてもオペレータとしても project.edit を持たない。
	// 到達はできるので 404 ではなく 403。
	q := seededQuerier().withMember("my-app", "project_viewer")

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.edit"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "forbidden" {
		t.Errorf("code = %q, want forbidden", got)
	}
	if reached {
		t.Error("403 なのにハンドラへ到達している")
	}
}

func TestRequireProjectPermissionHidesProjectFromNonMember(t *testing.T) {
	// 非メンバーのオペレータ。システムロールとして project.view を持つが、
	// それは「どのプロジェクトか」を決めない（DbDesign.md 7.3）。
	q := seededQuerier().withMember("my-app", "")

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404。他プロジェクトの存在を隠す（Design.md 6.4.5）（body=%s）",
			w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "not_found" {
		t.Errorf("code = %q, want not_found", got)
	}
	if reached {
		t.Error("ハンドラへ到達している")
	}
}

func TestRequireProjectPermissionAllowsAdministratorWhoIsNotMember(t *testing.T) {
	// ApiDesign.md 5.1 が一覧について「管理者は全件」と定めており、
	// 一覧に出たものを開けないのは矛盾する。
	q := seededQuerier().withMember("my-app", "")

	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusNoContent || !reached {
		t.Fatalf("status = %d, reached = %v, want 204 / true（body=%s）", w.Code, reached, w.Body.String())
	}
}

func TestRequireProjectPermissionUnknownProject(t *testing.T) {
	q := seededQuerier()

	w, _ := serveAuthz(principal(auth.SystemRoleAdministrator), projectPattern, "/projects/nope",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "not_found" {
		t.Errorf("code = %q, want not_found", got)
	}
}

func TestRequireProjectPermissionUnknownProjectAndHiddenLookAlike(t *testing.T) {
	// 「無い」と「在るが見えない」が応答で区別できないこと。
	// 区別できると、キーを変えながら叩いてプロジェクト一覧を復元できる。
	q := seededQuerier().withMember("secret", "")

	hidden, _ := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/secret",
		RequireProjectPermission(q, "project.view"))
	missing, _ := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/nope",
		RequireProjectPermission(q, "project.view"))

	if hidden.Code != missing.Code {
		t.Errorf("status が違う: 在るが見えない=%d, 無い=%d", hidden.Code, missing.Code)
	}
	if errorCode(t, hidden) != errorCode(t, missing) {
		t.Error("エラーコードが違う。存在の有無が漏れる")
	}
}

func TestRequireProjectPermissionRecordsDenialWithTarget(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_viewer")

	serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.edit"))

	if len(q.audits) != 1 {
		t.Fatalf("監査ログが %d 件、want 1", len(q.audits))
	}
	a := q.audits[0]
	if a.Action != "permission.denied" {
		t.Errorf("action = %q, want permission.denied", a.Action)
	}
	if a.TargetType.String != "project" || a.TargetID.String != testProjectID {
		t.Errorf("target = %q/%q, want project/%s", a.TargetType.String, a.TargetID.String, testProjectID)
	}
	if d := auditDetail(t, a); d["project_key"] != "my-app" {
		t.Errorf("detail.project_key = %v, want my-app", d["project_key"])
	}
}

func TestRequireProjectPermissionRecordsHiddenDenial(t *testing.T) {
	// 応答は「見つからない」でも、監査には permission.denied を残す。
	// 存在を隠すのは呼び出し元に対してであって、運用者に対してではない。
	q := seededQuerier().withMember("secret", "")

	serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/secret",
		RequireProjectPermission(q, "project.view"))

	if len(q.audits) != 1 {
		t.Fatalf("監査ログが %d 件、want 1", len(q.audits))
	}
	if q.audits[0].Action != "permission.denied" {
		t.Errorf("action = %q, want permission.denied", q.audits[0].Action)
	}
}

func TestRequireProjectPermissionIntersectsScopes(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_admin")

	w, _ := serveAuthz(principal(auth.SystemRoleOperator, "ticket.view"), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.edit"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403。スコープで絞られていない（body=%s）", w.Code, w.Body.String())
	}
}

func TestRequireProjectPermissionDatabaseFailure(t *testing.T) {
	q := seededQuerier()
	q.projectErr = errors.New("接続できない")

	w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500。404 に倒すと存在しないと誤って伝わる（body=%s）",
			w.Code, w.Body.String())
	}
	if reached {
		t.Error("ハンドラへ到達している")
	}
}

func TestRequireProjectPermissionWithoutKeyParam(t *testing.T) {
	q := seededQuerier()

	// {key} を含まないルートに置いた場合。ルート定義の誤りとして 500。
	w, _ := serveAuthz(principal(auth.SystemRoleAdministrator), "/projects", "/projects",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", w.Code, w.Body.String())
	}
}

func TestRequireProjectPermissionCachesWithinRequest(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_admin")

	chained := func(next http.Handler) http.Handler {
		return RequireProjectPermission(q, "project.view")(
			RequireProjectPermission(q, "project.edit")(next))
	}
	w, reached := serveAuthz(principal(auth.SystemRoleOperator), projectPattern, "/projects/my-app", chained)

	if w.Code != http.StatusNoContent || !reached {
		t.Fatalf("status = %d, reached = %v, want 204 / true（body=%s）", w.Code, reached, w.Body.String())
	}
	if q.projectCalls != 1 {
		t.Errorf("FindProjectAuthzByKey を %d 回呼んでいる。同一リクエスト内は1回であること", q.projectCalls)
	}
	if q.roleCalls != 1 {
		t.Errorf("ListRolePermissions を %d 回呼んでいる。同一リクエスト内は1回であること", q.roleCalls)
	}
}

func TestRequireProjectPermissionWithoutPrincipal(t *testing.T) {
	q := seededQuerier().withMember("my-app", "project_admin")

	w, _ := serveAuthz(nil, projectPattern, "/projects/my-app",
		RequireProjectPermission(q, "project.view"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", w.Code, w.Body.String())
	}
}

// ── RequirePermissionUnlessQuery（ApiDesign.md 7.1 の GET /roles）────
//
// scope によって必要権限が変わることを、ルート定義の側で表せているかを見る。

// exempt に一致する要求は、権限を持たない利用者でも素通しする。
func TestRequirePermissionUnlessQueryExemptsMatchingValue(t *testing.T) {
	q := seededQuerier()

	w, reached := serveAuthz(principal(auth.SystemRoleOperator), "/roles", "/roles?scope=project",
		RequirePermissionUnlessQuery(q, "user.manage", "scope", "project"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if !reached {
		t.Error("?scope=project はオペレータでも通ること（ApiDesign.md 7.1）")
	}
	if len(q.audits) != 0 {
		t.Errorf("素通ししたのに監査ログが %d 件書かれている", len(q.audits))
	}
}

// exempt 以外は permission を要求する。未指定・別の値・**解釈できない値**も含む。
func TestRequirePermissionUnlessQueryGuardsOtherValues(t *testing.T) {
	// 最後の2つは v1.parseRoleScopeFilter が 422 にする値だが、**認可のほうが
	// 先に走る**（7.1 の但し書き）。権限を持たない呼び出し元は project 以外を
	// 要求できないため、値の正しさは判定の後で足りる。
	for _, target := range []string{
		"/roles",
		"/roles?scope=system",
		"/roles?scope=all",
		"/roles?scope=SYSTEM",
		"/roles?scope=projects",
	} {
		q := seededQuerier()
		w, reached := serveAuthz(principal(auth.SystemRoleOperator), "/roles", target,
			RequirePermissionUnlessQuery(q, "user.manage", "scope", "project"))

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403（body=%s）", target, w.Code, w.Body.String())
		}
		if reached {
			t.Errorf("%s: 403 なのにハンドラへ到達している", target)
		}
	}
}

// 権限を持つ利用者は exempt 以外でも通る。
func TestRequirePermissionUnlessQueryAllowsPermittedActor(t *testing.T) {
	for _, target := range []string{"/roles", "/roles?scope=system", "/roles?scope=project"} {
		q := seededQuerier()
		w, reached := serveAuthz(principal(auth.SystemRoleAdministrator), "/roles", target,
			RequirePermissionUnlessQuery(q, "user.manage", "scope", "project"))

		if w.Code != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204（body=%s）", target, w.Code, w.Body.String())
		}
		if !reached {
			t.Errorf("%s: ハンドラに到達していない", target)
		}
	}
}

// **素通しは値の完全一致に限る。** 前方一致や大文字小文字の揺れで通ると、
// 認可がクエリの綴りしだいで外れることになる。
func TestRequirePermissionUnlessQueryMatchesExactly(t *testing.T) {
	for _, target := range []string{
		"/roles?scope=Project",
		"/roles?scope=project2",
		"/roles?scope=%20project",
		"/roles?other=project",
	} {
		q := seededQuerier()
		w, _ := serveAuthz(principal(auth.SystemRoleOperator), "/roles", target,
			RequirePermissionUnlessQuery(q, "user.manage", "scope", "project"))

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403（完全一致のみ素通しすること）", target, w.Code)
		}
	}
}
