package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ユーザー詳細・編集（ApiDesign.md 6.3〜6.8、手順13a）のハンドラ単体テスト。
//
// 認証・認可はミドルウェアの責務なので通さない（routes_test.go が別に見ている）。
// ここで確かめるのは**応答の形・ガードの分岐・監査に何を書くか**である。
// 実DBでしか確かめられないもの（UNIQUE 制約・CASCADE・楽観ロックの実挙動）は
// users_integration_test.go にある。

// targetID は操作の対象になるユーザー。testActorID（＝操作する側）とは別人。
const targetID = "01K2F8QW3H7YRJ4M5N6P7Q8TGT"

const targetEmail = "yamada@example.com"

// adminPrincipal は user.manage を持つアドミニストレータ。
func adminPrincipal() *auth.Principal {
	return &auth.Principal{
		ActorID:     testActorID,
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		Email:       testEmail,
		SystemRole:  auth.SystemRoleAdministrator,
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8TOK",
	}
}

// detailUserRow は既定の対象ユーザー（有効なオペレータ）。
func detailUserRow() gen.GetAdminUserRow {
	return gen.GetAdminUserRow{
		ID:          targetID,
		Kind:        "user",
		DisplayName: "山田 太郎",
		Email:       targetEmail,
		SystemRole:  auth.SystemRoleOperator,
		IsActive:    true,
		CreatedAt:   ts(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		Version:     2,
	}
}

// userFake は 6.3〜6.8 のテストが使う既定のフェイク。
//
// **「通る」状態を既定にして、テストごとに1つだけ崩す。** 崩した点が
// そのテストの主題であると読めるようにするためである。
func userFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.detailUser = detailUserRow()
	q.updateUserRows = 1
	q.deleteUserRows = 1
	q.deleteMemberRows = 1
	q.activeAdmins = 2
	q.credentialID = testIdentity
	q.projectIDByKey = map[string]string{"my-app": testProjectID}
	q.projectRoles = map[string]bool{
		"project_admin": true, "project_member": true, "project_viewer": true,
	}
	q.membershipRow = gen.GetProjectMembershipRow{
		ProjectID:   testProjectID,
		ProjectKey:  "my-app",
		ProjectName: "社内タスク管理の刷新",
		RoleKey:     "project_admin",
		JoinedAt:    ts(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)),
	}
	return q
}

// adminUserReq は {id}（と {key}）を持つリクエストを組み立てる。
//
// ハンドラを直接呼ぶため、chi のルートコンテキストと認証済みプリンシパルを
// テスト側で用意する。ルータを通す経路は routes_test.go が見ている。
func adminUserReq(method, target, body string, p *auth.Principal, params ...string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}

	rctx := chi.NewRouteContext()
	for i := 0; i+1 < len(params); i += 2 {
		rctx.URLParams.Add(params[i], params[i+1])
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(auth.NewPrincipalContext(ctx, p))
}

// ── GET /admin/users/:id（ApiDesign.md 6.3）─────────────────────

func TestGetUserReturnsDetail(t *testing.T) {
	q := userFake(t)
	q.identityRows = []gen.ListUserIdentitiesRow{{
		ID: testIdentity, ProviderKey: "local", ProviderType: "local",
		Subject:           targetEmail,
		LinkedAt:          ts(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		PasswordUpdatedAt: ts(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
	}}
	q.membershipRows = []gen.ListUserProjectMembershipsRow{{
		ProjectID: testProjectID, ProjectKey: "my-app", ProjectName: "社内タスク管理の刷新",
		RoleKey: "project_admin", JoinedAt: ts(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)),
	}}
	q.sessionRows = []gen.ListUserSessionsRow{{
		ID: "01K2F8QW3H7YRJ4M5N6P7Q8SES", ClientInfo: txt("Chrome / macOS"),
		IssuedAt:  ts(time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)),
		ExpiresAt: ts(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)),
	}}

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.getUser(rec, adminUserReq(http.MethodGet, "/api/v1/admin/users/"+targetID, "",
		adminPrincipal(), "id", targetID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)

	// 6.3 のキーが揃っていること。**配列は空でもキーを省略しない。**
	for _, key := range []string{
		"id", "kind", "display_name", "email", "system_role", "is_active",
		"last_login_at", "created_at", "version", "identities",
		"project_memberships", "sessions",
	} {
		if _, ok := view[key]; !ok {
			t.Errorf("応答に %q が無い: %s", key, rec.Body.String())
		}
	}
	if view["version"] != float64(2) {
		t.Errorf("version = %v, want 2", view["version"])
	}
	// 一度もログインしていなければ null（6.1 と同じ扱い）。
	if view["last_login_at"] != nil {
		t.Errorf("last_login_at = %v, want null", view["last_login_at"])
	}

	// メンバーシップの JSON キーは role_key ではなく **role**（6.3 の例）。
	m := view["project_memberships"].([]any)[0].(map[string]any)
	if m["role"] != "project_admin" {
		t.Errorf("project_memberships[0].role = %v, want project_admin", m["role"])
	}
	if _, ok := m["role_key"]; ok {
		t.Errorf("project_memberships[0] に role_key が出ている（6.3 のキーは role）")
	}

	// セッションに平文やハッシュが漏れていないこと。
	if strings.Contains(rec.Body.String(), "token_hash") {
		t.Errorf("応答に token_hash が含まれている: %s", rec.Body.String())
	}
}

func TestGetUserEmptyArraysAreNotNull(t *testing.T) {
	q := userFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.getUser(rec, adminUserReq(http.MethodGet, "/api/v1/admin/users/"+targetID, "",
		adminPrincipal(), "id", targetID))

	view := viewOf(t, rec)
	for _, key := range []string{"identities", "project_memberships", "sessions"} {
		v, ok := view[key].([]any)
		if !ok {
			t.Errorf("%s = %v, want []（null にしない）", key, view[key])
			continue
		}
		if len(v) != 0 {
			t.Errorf("%s の要素数 = %d, want 0", key, len(v))
		}
	}
}

// **エージェント・システムアクターは 404**（手順13a の判断。GetAdminUser が
// kind='user' に限るため 0 行になる）。
func TestGetUserNotFound(t *testing.T) {
	q := userFake(t)
	q.detailUserErr = pgx.ErrNoRows

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.getUser(rec, adminUserReq(http.MethodGet, "/api/v1/admin/users/nope", "",
		adminPrincipal(), "id", "nope"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "not_found" {
		t.Errorf("code = %q, want not_found", e.Code)
	}
}

// ── PATCH /admin/users/:id（ApiDesign.md 6.4）───────────────────

// patchUser はテストのたびに同じ4行を書くので、呼び出しをまとめる。
func callPatchUser(q *fakeQuerier, ifMatch, body string, p *auth.Principal) *httptest.ResponseRecorder {
	h, _ := newUserHandler(q)
	req := adminUserReq(http.MethodPatch, "/api/v1/admin/users/"+targetID, body, p, "id", targetID)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	h.patchUser(rec, req)
	return rec
}

func TestPatchUserUpdatesBothTablesAndReturnsDetail(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, `"2"`, `{"display_name":"山田 花子","system_role":"administrator"}`, adminPrincipal())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	// 応答は 6.3 と同形式。
	if _, ok := viewOf(t, rec)["identities"]; !ok {
		t.Errorf("応答が 6.3 の形式でない: %s", rec.Body.String())
	}

	// **app_user 側は version を照合して更新する**（2.8）。
	if len(q.updateUserParams) != 1 {
		t.Fatalf("UpdateAdminUserProfile の呼び出し = %d回, want 1", len(q.updateUserParams))
	}
	arg := q.updateUserParams[0]
	if arg.Version != 2 || arg.ActorID != targetID {
		t.Errorf("UpdateAdminUserProfile = %+v, want version=2 actor=%s", arg, targetID)
	}
	if arg.Email.Valid {
		t.Errorf("email を送っていないのに %q で更新しようとしている", arg.Email.String)
	}
	if arg.SystemRole.String != auth.SystemRoleAdministrator {
		t.Errorf("system_role = %q, want administrator", arg.SystemRole.String)
	}

	// **actor 側も同じトランザクションで更新される。**
	if len(q.updateActorParams) != 1 {
		t.Fatalf("UpdateAdminUserActor の呼び出し = %d回, want 1", len(q.updateActorParams))
	}
	if got := q.updateActorParams[0].DisplayName.String; got != "山田 花子" {
		t.Errorf("display_name = %q, want 山田 花子", got)
	}
	if q.updateActorParams[0].IsActive.Valid {
		t.Errorf("is_active を送っていないのに更新しようとしている")
	}
}

// **actor の列だけを変えても version は進む**（app_user を必ず通す）。
// これが崩れると、直後に同じ version でもう一度更新が通ってしまう。
func TestPatchUserBumpsVersionEvenForActorOnlyChange(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, `"2"`, `{"display_name":"表示名だけ"}`, adminPrincipal())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.updateUserParams) != 1 {
		t.Fatalf("app_user を更新していない（version が進まない）")
	}
}

// **メールを変えたら user_identity.subject も追随する**（設計文書に無い操作。
// 追随しないと当人がログインできなくなる）。
func TestPatchUserSyncsIdentitySubjectOnEmailChange(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, `"2"`, `{"email":"new@example.com"}`, adminPrincipal())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.subjectUpdates) != 1 {
		t.Fatalf("UpdateLocalIdentitySubject の呼び出し = %d回, want 1", len(q.subjectUpdates))
	}
	if got := q.subjectUpdates[0].Subject; got != "new@example.com" {
		t.Errorf("subject = %q, want new@example.com", got)
	}
}

func TestPatchUserDoesNotTouchIdentityWhenEmailUnchanged(t *testing.T) {
	q := userFake(t)
	callPatchUser(q, `"2"`, `{"display_name":"表示名だけ"}`, adminPrincipal())

	if len(q.subjectUpdates) != 0 {
		t.Errorf("メールを送っていないのに subject を更新している")
	}
}

// **ロール変更は権限キャッシュを捨て、role.change も記録する**（Design.md 6.4.5、
// ApiDesign.md 2.10）。
func TestPatchUserRoleChangeInvalidatesCacheAndAudits(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, `"2"`, `{"system_role":"administrator"}`, adminPrincipal())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if !slices.Contains(q.invalidatedCaches, targetID) {
		t.Errorf("権限キャッシュを無効化していない: %v", q.invalidatedCaches)
	}
	actions := q.auditActions()
	if !slices.Contains(actions, "user.update") || !slices.Contains(actions, "role.change") {
		t.Errorf("監査 = %v, want user.update と role.change の両方", actions)
	}
}

// ロールを変えていないときは role.change を書かず、キャッシュも捨てない。
func TestPatchUserWithoutRoleChangeSkipsCacheAndRoleAudit(t *testing.T) {
	q := userFake(t)
	// 現在と同じ値を送る。**「送られた」だけでは変更にしない。**
	callPatchUser(q, `"2"`, `{"system_role":"operator"}`, adminPrincipal())

	if len(q.invalidatedCaches) != 0 {
		t.Errorf("ロールが変わっていないのにキャッシュを捨てている: %v", q.invalidatedCaches)
	}
	if slices.Contains(q.auditActions(), "role.change") {
		t.Errorf("ロールが変わっていないのに role.change を書いている: %v", q.auditActions())
	}
}

func TestPatchUserRequiresIfMatch(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, "", `{"display_name":"山田"}`, adminPrincipal())

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); !hasDetail(e, "If-Match", "required") {
		t.Errorf("details に If-Match/required が無い: %+v", e.Details)
	}
	if len(q.updateUserParams) != 0 {
		t.Errorf("If-Match が無いのに更新している")
	}
}

// **If-Match の欠落と本文の誤りを1つの 422 にまとめる**（2.5）。
func TestPatchUserMergesValidationErrors(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, "", `{"system_role":"root"}`, adminPrincipal())

	e := errorOf(t, rec)
	if !hasDetail(e, "If-Match", "required") || !hasDetail(e, "system_role", "invalid") {
		t.Errorf("details = %+v, want If-Match と system_role の両方", e.Details)
	}
}

func TestPatchUserRejectsInvalidEmail(t *testing.T) {
	q := userFake(t)
	rec := callPatchUser(q, `"2"`, `{"email":"山田 <a@example.com>"}`, adminPrincipal())

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); !hasDetail(e, "email", "invalid") {
		t.Errorf("details に email/invalid が無い: %+v", e.Details)
	}
}

func TestPatchUserConflictsOnStaleVersion(t *testing.T) {
	q := userFake(t)
	q.updateUserRows = 0
	q.appUserExists = true // 行は在る＝version 不一致

	rec := callPatchUser(q, `"1"`, `{"display_name":"山田"}`, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "conflict" {
		t.Errorf("code = %q, want conflict", e.Code)
	}
}

func TestPatchUserReturns404WhenUserVanished(t *testing.T) {
	q := userFake(t)
	q.updateUserRows = 0
	q.appUserExists = false // 行が無い＝削除された

	rec := callPatchUser(q, `"2"`, `{"display_name":"山田"}`, adminPrincipal())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// **メールの重複は 6.2 と同じ already_exists**（2.5.1 の定義に従う）。
func TestPatchUserEmailConflict(t *testing.T) {
	q := userFake(t)
	q.updateUserErr = &pgconn.PgError{Code: "23505", TableName: "app_user"}

	rec := callPatchUser(q, `"2"`, `{"email":"taken@example.com"}`, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "already_exists" {
		t.Errorf("code = %q, want already_exists", e.Code)
	}
	if !hasDetail(e, "email", "already_exists") {
		t.Errorf("details に email/already_exists が無い: %+v", e.Details)
	}
}

// ── ガード（ApiDesign.md 6.4 / 6.5）───────────────────────────

// **自分自身のロール変更は 409。** UIだけで防ぐと、直接APIを叩いて
// 誰もログインできないインスタンスを作れてしまう。
func TestPatchUserRejectsSelfRoleChange(t *testing.T) {
	q := userFake(t)
	self := detailUserRow()
	self.ID = testActorID
	self.SystemRole = auth.SystemRoleAdministrator
	q.detailUser = self

	h, _ := newUserHandler(q)
	req := adminUserReq(http.MethodPatch, "/api/v1/admin/users/"+testActorID,
		`{"system_role":"operator"}`, adminPrincipal(), "id", testActorID)
	req.Header.Set("If-Match", `"2"`)
	rec := httptest.NewRecorder()
	h.patchUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "self_modification_forbidden" {
		t.Errorf("code = %q, want self_modification_forbidden", e.Code)
	}
	if len(q.updateUserParams) != 0 {
		t.Errorf("ガードを通り抜けて更新している")
	}
}

func TestPatchUserRejectsSelfDeactivation(t *testing.T) {
	q := userFake(t)
	self := detailUserRow()
	self.ID = testActorID
	q.detailUser = self

	h, _ := newUserHandler(q)
	req := adminUserReq(http.MethodPatch, "/api/v1/admin/users/"+testActorID,
		`{"is_active":false}`, adminPrincipal(), "id", testActorID)
	req.Header.Set("If-Match", `"2"`)
	rec := httptest.NewRecorder()
	h.patchUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "self_modification_forbidden" {
		t.Errorf("code = %q, want self_modification_forbidden", e.Code)
	}
}

// **自分自身でも表示名とメールは変えてよい。** 禁止の目的は
// 「管理者が自分の権限を失う」ことの防止であり、表示名はそれに当たらない。
func TestPatchUserAllowsSelfDisplayNameChange(t *testing.T) {
	q := userFake(t)
	self := detailUserRow()
	self.ID = testActorID
	q.detailUser = self

	h, _ := newUserHandler(q)
	req := adminUserReq(http.MethodPatch, "/api/v1/admin/users/"+testActorID,
		`{"display_name":"田中 一郎"}`, adminPrincipal(), "id", testActorID)
	req.Header.Set("If-Match", `"2"`)
	rec := httptest.NewRecorder()
	h.patchUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
}

func TestPatchUserRejectsLastAdministratorDemotion(t *testing.T) {
	q := userFake(t)
	target := detailUserRow()
	target.SystemRole = auth.SystemRoleAdministrator
	q.detailUser = target
	q.activeAdmins = 1

	rec := callPatchUser(q, `"2"`, `{"system_role":"operator"}`, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "last_administrator" {
		t.Errorf("code = %q, want last_administrator", e.Code)
	}
}

func TestPatchUserRejectsLastAdministratorDeactivation(t *testing.T) {
	q := userFake(t)
	target := detailUserRow()
	target.SystemRole = auth.SystemRoleAdministrator
	q.detailUser = target
	q.activeAdmins = 1

	rec := callPatchUser(q, `"2"`, `{"is_active":false}`, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "last_administrator" {
		t.Errorf("code = %q, want last_administrator", e.Code)
	}
}

// **無効なアドミニストレータは数に入らない**ので、降格しても止めない。
func TestPatchUserAllowsDemotingInactiveAdministrator(t *testing.T) {
	q := userFake(t)
	target := detailUserRow()
	target.SystemRole = auth.SystemRoleAdministrator
	target.IsActive = false
	q.detailUser = target
	q.activeAdmins = 1

	rec := callPatchUser(q, `"2"`, `{"system_role":"operator"}`, adminPrincipal())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── DELETE /admin/users/:id（ApiDesign.md 6.5）──────────────────

func callDeleteUser(q *fakeQuerier, id string, p *auth.Principal) *httptest.ResponseRecorder {
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteUser(rec, adminUserReq(http.MethodDelete, "/api/v1/admin/users/"+id, "", p, "id", id))
	return rec
}

func TestDeleteUserRemovesActorAndAuditsFirst(t *testing.T) {
	q := userFake(t)
	rec := callDeleteUser(q, targetID, adminPrincipal())

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 に本文がある: %s", rec.Body.String())
	}
	if !slices.Contains(q.deletedActors, targetID) {
		t.Errorf("actor を削除していない: %v", q.deletedActors)
	}

	// **監査は削除より先。** 順序が逆だと、削除に成功して記録に失敗した
	// ときに「誰を消したか」が残らない。
	insert := slices.Index(q.opLog, "InsertAuditLog")
	del := slices.Index(q.opLog, "DeleteActorByID")
	if insert < 0 || del < 0 || insert > del {
		t.Errorf("opLog = %v, want InsertAuditLog が DeleteActorByID より前", q.opLog)
	}

	// detail に削除時点の表示名とメールが残ること（6.5）。
	if len(q.audits) == 0 {
		t.Fatalf("監査ログが無い")
	}
	if d := string(q.audits[0].Detail); !strings.Contains(d, targetEmail) {
		t.Errorf("detail = %s, want メールを含む", d)
	}
}

// **コメントが1件も無ければ付け替えない**（Phase 1 は常にこちら）。
func TestDeleteUserSkipsReassignWhenNoComments(t *testing.T) {
	q := userFake(t)
	q.commentCount = 0
	callDeleteUser(q, targetID, adminPrincipal())

	if slices.Contains(q.opLog, "ReassignComments") {
		t.Errorf("コメントが無いのに付け替えている: %v", q.opLog)
	}
}

func TestDeleteUserRejectsSelf(t *testing.T) {
	q := userFake(t)
	self := detailUserRow()
	self.ID = testActorID
	q.detailUser = self

	rec := callDeleteUser(q, testActorID, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "self_modification_forbidden" {
		t.Errorf("code = %q, want self_modification_forbidden", e.Code)
	}
	if len(q.deletedActors) != 0 {
		t.Errorf("ガードを通り抜けて削除している")
	}
}

func TestDeleteUserRejectsLastAdministrator(t *testing.T) {
	q := userFake(t)
	target := detailUserRow()
	target.SystemRole = auth.SystemRoleAdministrator
	q.detailUser = target
	q.activeAdmins = 1

	rec := callDeleteUser(q, targetID, adminPrincipal())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "last_administrator" {
		t.Errorf("code = %q, want last_administrator", e.Code)
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	q := userFake(t)
	q.detailUserErr = pgx.ErrNoRows

	rec := callDeleteUser(q, "nope", adminPrincipal())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── POST /admin/users/:id/password-reset（ApiDesign.md 6.6）─────

func callPasswordReset(q *fakeQuerier, body string) *httptest.ResponseRecorder {
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.resetUserPassword(rec, adminUserReq(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/password-reset", body, adminPrincipal(), "id", targetID))
	return rec
}

// TestResetPasswordOnSelfKeepsCurrentSession は 6.6 の改訂を見る（pb-82）。
//
// **自分自身へのリセットで全セッションを切ると、押した本人が自分を締め出す。**
// 画面は generated_password を表示する前に 401 を受けてログイン画面へ飛び、
// **その値はこの応答でしか手に入らないので永久に失われる**（実測、2026-09-12）。
func TestResetPasswordOnSelfKeepsCurrentSession(t *testing.T) {
	q := userFake(t)
	q.revokedSessions = 2
	// **対象を自分自身にする。** 行の ID も principal に合わせる。
	q.detailUser.ID = testActorID

	p := adminPrincipal()
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.resetUserPassword(rec, adminUserReq(http.MethodPost,
		"/api/v1/admin/users/"+testActorID+"/password-reset", "", p, "id", testActorID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if pw, _ := viewOf(t, rec)["generated_password"].(string); pw == "" {
		t.Fatalf("generated_password が空: %s", rec.Body.String())
	}

	// **いま操作しているセッションを外して切る**（4.3 と同じ扱い）。
	if len(q.myRevokeParams) != 1 {
		t.Fatalf("RevokeMyOtherSessions の呼び出し = %d回, want 1", len(q.myRevokeParams))
	}
	if got := q.myRevokeParams[0].CurrentTokenID; got != p.TokenID {
		t.Errorf("残すトークン = %q, want %q", got, p.TokenID)
	}
	// **全失効のほうを呼んでいないこと。** 呼ぶと自分のセッションまで切れる。
	if slices.Contains(q.revokedActors, testActorID) {
		t.Errorf("自分自身に全セッション失効を掛けている: %v", q.revokedActors)
	}
}

// TestResetPasswordOnOtherRevokesAllSessions は他人へのリセットが従来どおりで
// あることを見る。**自分自身の例外が他人へ漏れていないこと。**
func TestResetPasswordOnOtherRevokesAllSessions(t *testing.T) {
	q := userFake(t)
	q.revokedSessions = 3
	if rec := callPasswordReset(q, ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if !slices.Contains(q.revokedActors, targetID) {
		t.Errorf("他人には全セッション失効を掛けること: %v", q.revokedActors)
	}
	if len(q.myRevokeParams) != 0 {
		t.Errorf("他人に RevokeMyOtherSessions を使っている: %+v", q.myRevokeParams)
	}
}

func TestResetPasswordReturnsGeneratedPasswordAndRevokesSessions(t *testing.T) {
	q := userFake(t)
	q.revokedSessions = 3
	rec := callPasswordReset(q, `{"mode":"generate","must_change_password":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	pw, _ := viewOf(t, rec)["generated_password"].(string)
	if pw == "" {
		t.Fatalf("generated_password が空: %s", rec.Body.String())
	}
	// 6.2.1 の形式（<形容詞>-<名詞>-<4桁数字>-<名詞>）。**6.6 の応答例とも揃った**
	// （pb-40 で強度を 2^21.3 から 2^31.3 へ上げたときに、例のほうも直した）。
	if strings.Count(pw, "-") != 3 {
		t.Errorf("generated_password = %q, want <形容詞>-<名詞>-<4桁数字>-<名詞>", pw)
	}

	if len(q.credentialResets) != 1 {
		t.Fatalf("ResetLocalCredential の呼び出し = %d回, want 1", len(q.credentialResets))
	}
	if !q.credentialResets[0].MustChange {
		t.Errorf("must_change = false, want true")
	}
	if !slices.Contains(q.revokedActors, targetID) {
		t.Errorf("セッションを失効していない: %v", q.revokedActors)
	}

	// **password.reset だけを記録する**（session.revoke は書かない）。
	actions := q.auditActions()
	if !slices.Contains(actions, "password.reset") {
		t.Errorf("監査 = %v, want password.reset", actions)
	}
	if slices.Contains(actions, "session.revoke") {
		t.Errorf("監査 = %v, 1操作を2行にしている", actions)
	}
	// **平文を監査に残さない**（6.6 の「この応答でのみ返る」を守る）。
	for _, a := range q.audits {
		if strings.Contains(string(a.Detail), pw) {
			t.Errorf("audit_log.detail に平文が入っている: %s", a.Detail)
		}
	}
}

// **本文そのものを省略できる**（2項目とも既定を持つ）。
func TestResetPasswordAcceptsEmptyBody(t *testing.T) {
	q := userFake(t)
	rec := callPasswordReset(q, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if !q.credentialResets[0].MustChange {
		t.Errorf("must_change = false, want true（省略時の既定）")
	}
}

func TestResetPasswordRejectsOtherModes(t *testing.T) {
	q := userFake(t)
	rec := callPasswordReset(q, `{"mode":"manual","password":"hunter2hunter2"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); !hasDetail(e, "mode", "invalid") {
		t.Errorf("details に mode/invalid が無い: %+v", e.Details)
	}
}

// **local_credential を持たないユーザーは 409**（IdP のみ、Phase 3）。
func TestResetPasswordConflictsWithoutLocalCredential(t *testing.T) {
	q := userFake(t)
	q.credentialErr = pgx.ErrNoRows

	rec := callPasswordReset(q, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "conflict" {
		t.Errorf("code = %q, want conflict", e.Code)
	}
}

// **居ないユーザーは 409 ではなく 404。** 順序を逆にすると、存在しない ID に
// 「資格情報が無い」と返してしまい、ID の総当たりに手がかりを与える。
func TestResetPasswordNotFoundBeforeConflict(t *testing.T) {
	q := userFake(t)
	q.detailUserErr = pgx.ErrNoRows
	q.credentialErr = pgx.ErrNoRows

	rec := callPasswordReset(q, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── POST /admin/users/:id/sessions/revoke（ApiDesign.md 6.7）────

func TestRevokeUserSessions(t *testing.T) {
	q := userFake(t)
	q.revokedSessions = 2

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.revokeUserSessions(rec, adminUserReq(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/sessions/revoke", "", adminPrincipal(), "id", targetID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if !slices.Contains(q.auditActions(), "session.revoke") {
		t.Errorf("監査 = %v, want session.revoke", q.auditActions())
	}
}

// **1本も無くても 204**（冪等）。ただし記録は残す。
func TestRevokeUserSessionsIsIdempotent(t *testing.T) {
	q := userFake(t)
	q.revokedSessions = 0

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.revokeUserSessions(rec, adminUserReq(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/sessions/revoke", "", adminPrincipal(), "id", targetID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if !slices.Contains(q.auditActions(), "session.revoke") {
		t.Errorf("0本でも記録する: %v", q.auditActions())
	}
}

// ── メンバーシップ（ApiDesign.md 6.8）───────────────────────────

func callPutMembership(q *fakeQuerier, key, body string) *httptest.ResponseRecorder {
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.putUserMembership(rec, adminUserReq(http.MethodPut,
		"/api/v1/admin/users/"+targetID+"/memberships/"+key, body, adminPrincipal(),
		"id", targetID, middleware.ProjectKeyURLParam, key))
	return rec
}

func TestPutMembershipReturnsMembershipAndInvalidatesCache(t *testing.T) {
	q := userFake(t)
	rec := callPutMembership(q, "my-app", `{"role":"project_admin"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if view["role"] != "project_admin" || view["project_key"] != "my-app" {
		t.Errorf("応答 = %v, want role=project_admin key=my-app", view)
	}
	if len(q.upsertedMembers) != 1 || q.upsertedMembers[0].RoleKey != "project_admin" {
		t.Errorf("UpsertProjectMember = %+v", q.upsertedMembers)
	}

	// **メンバーシップは実効権限の第2層**（Design.md 6.4.1）。変えたら捨てる。
	if !slices.Contains(q.invalidatedCaches, targetID) {
		t.Errorf("権限キャッシュを無効化していない: %v", q.invalidatedCaches)
	}
	if !slices.Contains(q.auditActions(), "role.change") {
		t.Errorf("監査 = %v, want role.change", q.auditActions())
	}
	if q.audits[0].TargetType.String != "project_member" {
		t.Errorf("target_type = %q, want project_member", q.audits[0].TargetType.String)
	}
}

// **ロールの妥当性はDBに問い合わせる**（Go 側に書き写さない）。
func TestPutMembershipRejectsUnknownRole(t *testing.T) {
	q := userFake(t)
	rec := callPutMembership(q, "my-app", `{"role":"owner"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); !hasDetail(e, "role", "invalid") {
		t.Errorf("details に role/invalid が無い: %+v", e.Details)
	}
	if len(q.upsertedMembers) != 0 {
		t.Errorf("不正なロールで書き込んでいる")
	}
}

func TestPutMembershipRequiresRole(t *testing.T) {
	q := userFake(t)
	rec := callPutMembership(q, "my-app", `{}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); !hasDetail(e, "role", "required") {
		t.Errorf("details に role/required が無い: %+v", e.Details)
	}
}

func TestPutMembershipUnknownProjectIs404(t *testing.T) {
	q := userFake(t)
	rec := callPutMembership(q, "no-such", `{"role":"project_member"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e.Code != "not_found" {
		t.Errorf("code = %q, want not_found", e.Code)
	}
}

func TestDeleteMembershipRemovesAndAudits(t *testing.T) {
	q := userFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteUserMembership(rec, adminUserReq(http.MethodDelete,
		"/api/v1/admin/users/"+targetID+"/memberships/my-app", "", adminPrincipal(),
		"id", targetID, middleware.ProjectKeyURLParam, "my-app"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.deletedMembers) != 1 {
		t.Fatalf("DeleteProjectMember の呼び出し = %d回, want 1", len(q.deletedMembers))
	}
	if !slices.Contains(q.auditActions(), "role.change") {
		t.Errorf("監査 = %v, want role.change", q.auditActions())
	}
}

// **元から居なくても 204。** ただし空振りは監査に残さない
// （記録すると本当の剥奪が埋もれる）。
func TestDeleteMembershipIsIdempotentAndSkipsAudit(t *testing.T) {
	q := userFake(t)
	q.deleteMemberRows = 0

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteUserMembership(rec, adminUserReq(http.MethodDelete,
		"/api/v1/admin/users/"+targetID+"/memberships/my-app", "", adminPrincipal(),
		"id", targetID, middleware.ProjectKeyURLParam, "my-app"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if slices.Contains(q.auditActions(), "role.change") {
		t.Errorf("空振りを監査に残している: %v", q.auditActions())
	}
	if len(q.invalidatedCaches) != 0 {
		t.Errorf("空振りでキャッシュを捨てている: %v", q.invalidatedCaches)
	}
}
