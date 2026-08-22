package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// アクセスログとエラーログがテスト出力を埋めないよう、既定ロガーを捨てる。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// testPassword は照合に成功する平文。Design.md 6.3 の最小長12を満たす。
const testPassword = "quiet-harbor-4172-mint"

const (
	testActorID  = "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
	testIdentity = "01K2F8QW3H7YRJ4M5N6P7Q8R9I"
	testEmail    = "tanaka@example.com"
	// testProjectID は認可ミドルウェアが返すプロジェクトの ULID（手順11）。
	testProjectID = "01K2F8QW3H7YRJ4M5N6P7Q8PRJ"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func txt(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

// fakeQuerier は手順5で使うクエリだけを実装した Querier。
//
// 埋め込んだ gen.Querier は nil のままなので、実装し忘れたメソッドを
// 呼べば panic して気づける。
type fakeQuerier struct {
	gen.Querier

	// login 経路
	loginRow  gen.FindLocalLoginByEmailRow
	loginErr  error // 未設定なら loginRow を返す。pgx.ErrNoRows で「未登録」
	loginedBy []string

	// 認証ミドルウェア
	tokenRow gen.FindAccessTokenByHashRow
	tokenErr error

	// プロフィールと権限
	profileRow  gen.GetActorProfileRow
	profileErr  error
	permissions map[string][]string
	memberships []gen.ListProjectMembershipsByActorRow
	roleCalls   int

	// ロール・権限カタログ（手順14。ApiDesign.md 7.1 / 7.2）
	//
	// catalogScopes に ListRoles / ListRolePermissionAssignments が受けた
	// scope を順に残す。**両者が同じ値で呼ばれること**を試験が確かめる
	// （片方だけ絞ると、返らないロールの権限が応答に混ざる）。
	catalogRoles       []gen.Role
	catalogAssignments []gen.RolePermission
	catalogPerms       []gen.Permission
	catalogScopes      []string
	catalogErr         error

	// プロジェクト（手順9）
	projectRows    []gen.ListProjectsRow
	projectSummary gen.SummarizeProjectsRow
	listParams     []gen.ListProjectsParams
	summaryParams  []gen.SummarizeProjectsParams
	keyExists      bool
	keyChecked     []string
	listErr        error

	// プロジェクトの作成（手順9）。opLog に呼び出し順を残し、
	// 5.3 の「単一トランザクションで行う手順」をテストから固定する。
	opLog               []string
	createdProjects     []gen.CreateProjectParams
	createdCounters     []string
	createdWorkflows    []gen.CreateProjectWorkflowParams
	createdStatuses     []gen.CreateWorkflowStatusParams
	createdTransitions  []gen.CreateWorkflowTransitionParams
	linkedWorkflows     []gen.SetProjectWorkflowParams
	addedMembers        []gen.AddProjectMemberParams
	templateRow         gen.FindWorkflowTemplateRow
	templateErr         error
	templateStatuses    []gen.ListWorkflowStatusesRow
	templateTransitions []gen.ListWorkflowTransitionsRow
	detailRow           gen.GetProjectByKeyRow
	detailErr           error
	memberRows          []gen.ListProjectMembersRow
	createProjectErr    error
	auditErr            error

	// プロジェクトの更新（手順11。ApiDesign.md 5.5 / 5.6）
	//
	// updateRows / statusRows は UPDATE の影響行数を決める。0 が
	// 「version 不一致 または 行が無い」と「既にその状態」を表し、
	// ハンドラがそれを 409 / 404 / 冪等な 200 に分ける様子を試せる。
	updateParams []gen.UpdateProjectParams
	updateRows   int64
	updateErr    error
	statusParams []gen.SetProjectStatusParams
	statusRows   int64
	statusErr    error

	// ユーザー管理（手順12a。ApiDesign.md 6.1 / 6.2）
	userRows      []gen.ListAdminUsersRow
	userSummary   gen.SummarizeAdminUsersRow
	userListParam []gen.ListAdminUsersParams
	userSumParam  []gen.SummarizeAdminUsersParams
	userListErr   error
	createdActors []gen.CreateUserActorParams
	createdUsers  []gen.CreateAppUserParams
	createdIdents []gen.CreateUserIdentityParams
	createdCreds  []gen.CreateLocalCredentialParams
	createUserErr error

	// ユーザー詳細・編集（手順13a。ApiDesign.md 6.3〜6.8）
	//
	// detailUser が GetAdminUser の戻り。detailUserErr に pgx.ErrNoRows を
	// 入れると「居ない（または kind が user でない）」を表せる。
	detailUser        gen.GetAdminUserRow
	detailUserErr     error
	identityRows      []gen.ListUserIdentitiesRow
	membershipRows    []gen.ListUserProjectMembershipsRow
	sessionRows       []gen.ListUserSessionsRow
	activeAdmins      int64
	updateUserParams  []gen.UpdateAdminUserProfileParams
	updateUserRows    int64
	updateUserErr     error
	updateActorParams []gen.UpdateAdminUserActorParams
	subjectUpdates    []gen.UpdateLocalIdentitySubjectParams
	appUserExists     bool
	commentCount      int64
	deletedActors     []string
	deleteUserRows    int64
	credentialID      string
	credentialErr     error
	credentialResets  []gen.ResetLocalCredentialParams
	revokedActors     []string
	revokedSessions   int64
	upsertedMembers   []gen.UpsertProjectMemberParams
	membershipRow     gen.GetProjectMembershipRow
	deletedMembers    []gen.DeleteProjectMemberParams
	deleteMemberRows  int64
	invalidatedCaches []string
	projectIDByKey    map[string]string
	projectRoles      map[string]bool

	// 認可ミドルウェア（RequireProjectPermission）が引く行。
	// key ごとに「プロジェクトの存在・自分のロール・その権限」を持つ。
	projectAuthzRows map[string][]gen.FindProjectAuthzByKeyRow

	// 書き込みの記録
	created      []gen.CreateAccessTokenParams
	failures     []gen.RecordLoginFailureParams
	resets       []string
	rehashes     []gen.RehashPasswordParams
	revoked      []string
	touchedLogin []string
	audits       []gen.InsertAuditLogParams
	cacheSaves   []gen.SaveTokenPermissionCacheParams

	// 故意に失敗させる
	createErr error
	resetErr  error
	failErr   error
	revokeErr error
	// cacheSaveErr は実効権限のキャッシュ書き戻しを失敗させる（手順6b）。
	cacheSaveErr error
}

func (q *fakeQuerier) FindLocalLoginByEmail(_ context.Context, email string) (gen.FindLocalLoginByEmailRow, error) {
	q.loginedBy = append(q.loginedBy, email)
	if q.loginErr != nil {
		return gen.FindLocalLoginByEmailRow{}, q.loginErr
	}
	return q.loginRow, nil
}

func (q *fakeQuerier) FindAccessTokenByHash(context.Context, string) (gen.FindAccessTokenByHashRow, error) {
	if q.tokenErr != nil {
		return gen.FindAccessTokenByHashRow{}, q.tokenErr
	}
	return q.tokenRow, nil
}

func (q *fakeQuerier) TouchAccessTokenLastUsed(context.Context, string) error { return nil }

func (q *fakeQuerier) GetActorProfile(_ context.Context, actorID string) (gen.GetActorProfileRow, error) {
	if q.profileErr != nil {
		return gen.GetActorProfileRow{}, q.profileErr
	}
	row := q.profileRow
	if row.ActorID == "" {
		row.ActorID = actorID
	}
	return row, nil
}

func (q *fakeQuerier) ListRolePermissions(_ context.Context, roleKey string) ([]string, error) {
	q.roleCalls++
	return q.permissions[roleKey], nil
}

func (q *fakeQuerier) ListProjectMembershipsByActor(context.Context, string) ([]gen.ListProjectMembershipsByActorRow, error) {
	return q.memberships, nil
}

func (q *fakeQuerier) CreateAccessToken(_ context.Context, arg gen.CreateAccessTokenParams) error {
	if q.createErr != nil {
		return q.createErr
	}
	q.created = append(q.created, arg)
	return nil
}

func (q *fakeQuerier) RecordLoginFailure(_ context.Context, arg gen.RecordLoginFailureParams) error {
	if q.failErr != nil {
		return q.failErr
	}
	q.failures = append(q.failures, arg)
	return nil
}

func (q *fakeQuerier) ResetLoginFailure(_ context.Context, identityID string) error {
	if q.resetErr != nil {
		return q.resetErr
	}
	q.resets = append(q.resets, identityID)
	return nil
}

func (q *fakeQuerier) RehashPassword(_ context.Context, arg gen.RehashPasswordParams) error {
	q.rehashes = append(q.rehashes, arg)
	return nil
}

func (q *fakeQuerier) RevokeAccessToken(_ context.Context, id string) error {
	if q.revokeErr != nil {
		return q.revokeErr
	}
	q.revoked = append(q.revoked, id)
	return nil
}

func (q *fakeQuerier) TouchLastLoginAt(_ context.Context, actorID string) error {
	q.touchedLogin = append(q.touchedLogin, actorID)
	return nil
}

func (q *fakeQuerier) SaveTokenPermissionCache(_ context.Context, arg gen.SaveTokenPermissionCacheParams) error {
	if q.cacheSaveErr != nil {
		return q.cacheSaveErr
	}
	q.cacheSaves = append(q.cacheSaves, arg)
	return nil
}

func (q *fakeQuerier) InsertAuditLog(_ context.Context, arg gen.InsertAuditLogParams) error {
	q.opLog = append(q.opLog, "InsertAuditLog")
	if q.auditErr != nil {
		return q.auditErr
	}
	q.audits = append(q.audits, arg)
	return nil
}

func (q *fakeQuerier) CreateProject(_ context.Context, arg gen.CreateProjectParams) error {
	q.opLog = append(q.opLog, "CreateProject")
	if q.createProjectErr != nil {
		return q.createProjectErr
	}
	q.createdProjects = append(q.createdProjects, arg)
	return nil
}

func (q *fakeQuerier) CreateProjectCounter(_ context.Context, projectID string) error {
	q.opLog = append(q.opLog, "CreateProjectCounter")
	q.createdCounters = append(q.createdCounters, projectID)
	return nil
}

func (q *fakeQuerier) FindWorkflowTemplate(_ context.Context, key pgtype.Text) (gen.FindWorkflowTemplateRow, error) {
	q.opLog = append(q.opLog, "FindWorkflowTemplate")
	if q.templateErr != nil {
		return gen.FindWorkflowTemplateRow{}, q.templateErr
	}
	row := q.templateRow
	if row.ID == "" {
		row.ID = "01K2F8QW3H7YRJ4M5N6P7Q8TPL"
		row.Name = "シンプル（" + key.String + "）"
		row.Definition = []byte(`{}`)
	}
	return row, nil
}

func (q *fakeQuerier) CreateProjectWorkflow(_ context.Context, arg gen.CreateProjectWorkflowParams) error {
	q.opLog = append(q.opLog, "CreateProjectWorkflow")
	q.createdWorkflows = append(q.createdWorkflows, arg)
	return nil
}

func (q *fakeQuerier) ListWorkflowStatuses(context.Context, string) ([]gen.ListWorkflowStatusesRow, error) {
	q.opLog = append(q.opLog, "ListWorkflowStatuses")
	return q.templateStatuses, nil
}

func (q *fakeQuerier) CreateWorkflowStatus(_ context.Context, arg gen.CreateWorkflowStatusParams) error {
	q.opLog = append(q.opLog, "CreateWorkflowStatus")
	q.createdStatuses = append(q.createdStatuses, arg)
	return nil
}

func (q *fakeQuerier) ListWorkflowTransitions(context.Context, string) ([]gen.ListWorkflowTransitionsRow, error) {
	q.opLog = append(q.opLog, "ListWorkflowTransitions")
	return q.templateTransitions, nil
}

func (q *fakeQuerier) CreateWorkflowTransition(_ context.Context, arg gen.CreateWorkflowTransitionParams) error {
	q.opLog = append(q.opLog, "CreateWorkflowTransition")
	q.createdTransitions = append(q.createdTransitions, arg)
	return nil
}

func (q *fakeQuerier) SetProjectWorkflow(_ context.Context, arg gen.SetProjectWorkflowParams) error {
	q.opLog = append(q.opLog, "SetProjectWorkflow")
	q.linkedWorkflows = append(q.linkedWorkflows, arg)
	return nil
}

func (q *fakeQuerier) AddProjectMember(_ context.Context, arg gen.AddProjectMemberParams) error {
	q.opLog = append(q.opLog, "AddProjectMember")
	q.addedMembers = append(q.addedMembers, arg)
	return nil
}

func (q *fakeQuerier) GetProjectByKey(_ context.Context, key string) (gen.GetProjectByKeyRow, error) {
	q.opLog = append(q.opLog, "GetProjectByKey")
	if q.detailErr != nil {
		return gen.GetProjectByKeyRow{}, q.detailErr
	}
	row := q.detailRow
	if row.Key == "" {
		row.Key = key
	}
	return row, nil
}

func (q *fakeQuerier) ListProjectMembers(context.Context, string) ([]gen.ListProjectMembersRow, error) {
	q.opLog = append(q.opLog, "ListProjectMembers")
	return q.memberRows, nil
}

// fakeTxRunner は fn をそのまま呼ぶ TxRunner。
//
// コミットの有無だけを記録する。**fn がエラーを返したらコミットしない**という
// 実装（store.TxRunner）の約束をテストから確かめるためである。
type fakeTxRunner struct {
	q         gen.Querier
	calls     int
	committed bool
}

func (t *fakeTxRunner) RunInTx(ctx context.Context, fn func(gen.Querier) error) error {
	t.calls++
	if err := fn(t.q); err != nil {
		return err
	}
	t.committed = true
	return nil
}

func (q *fakeQuerier) ListProjects(_ context.Context, arg gen.ListProjectsParams) ([]gen.ListProjectsRow, error) {
	q.listParams = append(q.listParams, arg)
	if q.listErr != nil {
		return nil, q.listErr
	}
	return q.projectRows, nil
}

func (q *fakeQuerier) SummarizeProjects(_ context.Context, arg gen.SummarizeProjectsParams) (gen.SummarizeProjectsRow, error) {
	q.summaryParams = append(q.summaryParams, arg)
	if q.listErr != nil {
		return gen.SummarizeProjectsRow{}, q.listErr
	}
	return q.projectSummary, nil
}

func (q *fakeQuerier) ProjectKeyExists(_ context.Context, key string) (bool, error) {
	q.opLog = append(q.opLog, "ProjectKeyExists")
	q.keyChecked = append(q.keyChecked, key)
	return q.keyExists, nil
}

func (q *fakeQuerier) UpdateProject(_ context.Context, arg gen.UpdateProjectParams) (int64, error) {
	q.opLog = append(q.opLog, "UpdateProject")
	q.updateParams = append(q.updateParams, arg)
	if q.updateErr != nil {
		return 0, q.updateErr
	}
	return q.updateRows, nil
}

func (q *fakeQuerier) SetProjectStatus(_ context.Context, arg gen.SetProjectStatusParams) (int64, error) {
	q.opLog = append(q.opLog, "SetProjectStatus")
	q.statusParams = append(q.statusParams, arg)
	if q.statusErr != nil {
		return 0, q.statusErr
	}
	return q.statusRows, nil
}

func (q *fakeQuerier) FindProjectAuthzByKey(
	_ context.Context, arg gen.FindProjectAuthzByKeyParams,
) ([]gen.FindProjectAuthzByKeyRow, error) {
	return q.projectAuthzRows[arg.ProjectKey], nil
}

// withProjectMember は、key のプロジェクトに role のメンバーとして
// 属している状態を作る（RequireProjectPermission が引く行）。
//
// role が空文字なら「プロジェクトは在るが非メンバー」（LEFT JOIN の NULL 行）。
// key ごと登録しなければ「プロジェクトが無い」＝ 0 行になる。
func (q *fakeQuerier) withProjectMember(key, role string) *fakeQuerier {
	if q.projectAuthzRows == nil {
		q.projectAuthzRows = map[string][]gen.FindProjectAuthzByKeyRow{}
	}
	if role == "" {
		q.projectAuthzRows[key] = []gen.FindProjectAuthzByKeyRow{{
			ProjectID: testProjectID, ProjectKey: key, ProjectStatus: "active",
		}}
		return q
	}
	rows := make([]gen.FindProjectAuthzByKeyRow, 0, len(q.permissions[role]))
	for _, perm := range q.permissions[role] {
		rows = append(rows, gen.FindProjectAuthzByKeyRow{
			ProjectID: testProjectID, ProjectKey: key, ProjectStatus: "active",
			RoleKey:       txt(role),
			PermissionKey: txt(perm),
		})
	}
	q.projectAuthzRows[key] = rows
	return q
}

// auditActions は記録された監査アクションを順に返す。
func (q *fakeQuerier) auditActions() []string {
	out := make([]string, 0, len(q.audits))
	for _, a := range q.audits {
		out = append(out, a.Action)
	}
	return out
}

// loginRow は照合に成功する既定の行を返す。
func loginRow(t *testing.T) gen.FindLocalLoginByEmailRow {
	t.Helper()
	phc, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return gen.FindLocalLoginByEmailRow{
		ActorID:      testActorID,
		DisplayName:  "田中",
		IsActive:     true,
		Email:        testEmail,
		SystemRole:   auth.SystemRoleAdministrator,
		Locale:       "ja",
		Timezone:     "Asia/Tokyo",
		IdentityID:   testIdentity,
		PasswordHash: phc,
	}
}

// newFake は既定の権限割り当てを持つフェイクを返す。
func newFake(t *testing.T) *fakeQuerier {
	t.Helper()
	return &fakeQuerier{
		loginRow: loginRow(t),
		permissions: map[string][]string{
			auth.SystemRoleAdministrator: {"project.view", "ticket.view", "user.manage"},
			auth.SystemRoleOperator:      {"project.view", "ticket.view"},
			"project_admin":              {"ticket.close"},
		},
	}
}

// routerWithDeps は v1.Mount を通した実際のルータを返す。
// ルート定義（Design.md 6.4.4）ごと検証したいので、ハンドラを直接呼ばない。
func routerWithDeps(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Mount(r, deps)
	})
	return r
}

func router(q gen.Querier) http.Handler { return routerWithDeps(Deps{Queries: q}) }

// testCSRFToken はテストで使う CSRF トークン。値そのものに意味は無く、
// Cookie とヘッダで同じ値を送れば 2.4 の double-submit を満たす。
const testCSRFToken = "test-csrf-token-01K2F8QW"

// addCSRF は pb_csrf Cookie と X-PB-CSRF ヘッダを同じ値で付ける（ApiDesign.md 2.4）。
//
// Cookie 認証の状態変更系は CSRF ミドルウェアを通るため、ブラウザが行うのと
// 同じことをテスト側でも行う。安全なメソッドでは検証されないので害は無い。
func addCSRF(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: testCSRFToken})
	req.Header.Set(auth.CSRFHeaderName, testCSRFToken)
}

// call はルータへ1リクエスト投げる。body が空なら本文なし。
func call(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// postLogin は JSON 本文でログインを叩く。
func postLogin(q gen.Querier, body string) *httptest.ResponseRecorder {
	return call(router(q), http.MethodPost, "/api/v1/auth/login", body)
}

// errorOf は 2.5 形式の応答からエラー部を取り出す。
type apiError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	RetryAfterSec int    `json:"retry_after_sec"`
	Details       []struct {
		Field string `json:"field"`
		Code  string `json:"code"`
	} `json:"details"`
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	var body struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が 2.5 の JSON 形式でない: %v (%s)", err, rec.Body.String())
	}
	return body.Error
}

// viewOf は成功応答（3.1 / 4.1）を読む。
func viewOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が JSON でない: %v (%s)", err, rec.Body.String())
	}
	return body
}

// cookieOf は Set-Cookie から目的の Cookie を取り出す。無ければ nil。
func cookieOf(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// notFoundErr は「該当なし」を表す。
var notFoundErr = pgx.ErrNoRows

func pgBool(b bool) pgtype.Bool { return pgtype.Bool{Bool: b, Valid: true} }

// pgNull は NULL を表す pgtype.Text。
func pgNull() pgtype.Text { return pgtype.Text{} }

// ── ユーザー管理（手順12a）────────────────────────────────

func (q *fakeQuerier) ListAdminUsers(_ context.Context, arg gen.ListAdminUsersParams) ([]gen.ListAdminUsersRow, error) {
	q.userListParam = append(q.userListParam, arg)
	if q.userListErr != nil {
		return nil, q.userListErr
	}
	return q.userRows, nil
}

func (q *fakeQuerier) SummarizeAdminUsers(_ context.Context, arg gen.SummarizeAdminUsersParams) (gen.SummarizeAdminUsersRow, error) {
	q.userSumParam = append(q.userSumParam, arg)
	if q.userListErr != nil {
		return gen.SummarizeAdminUsersRow{}, q.userListErr
	}
	return q.userSummary, nil
}

// 以下の4本は opLog に順を残す。6.2 の「単一トランザクションで
// actor → app_user → user_identity → local_credential」をテストから固定するため。

func (q *fakeQuerier) CreateUserActor(_ context.Context, arg gen.CreateUserActorParams) error {
	q.opLog = append(q.opLog, "CreateUserActor")
	q.createdActors = append(q.createdActors, arg)
	return nil
}

func (q *fakeQuerier) CreateAppUser(_ context.Context, arg gen.CreateAppUserParams) error {
	q.opLog = append(q.opLog, "CreateAppUser")
	if q.createUserErr != nil {
		return q.createUserErr
	}
	q.createdUsers = append(q.createdUsers, arg)
	return nil
}

func (q *fakeQuerier) CreateUserIdentity(_ context.Context, arg gen.CreateUserIdentityParams) error {
	q.opLog = append(q.opLog, "CreateUserIdentity")
	q.createdIdents = append(q.createdIdents, arg)
	return nil
}

func (q *fakeQuerier) CreateLocalCredential(_ context.Context, arg gen.CreateLocalCredentialParams) error {
	q.opLog = append(q.opLog, "CreateLocalCredential")
	q.createdCreds = append(q.createdCreds, arg)
	return nil
}

// ── ユーザー詳細・編集（手順13a）──────────────────────────────

func (q *fakeQuerier) GetAdminUser(_ context.Context, actorID string) (gen.GetAdminUserRow, error) {
	q.opLog = append(q.opLog, "GetAdminUser")
	if q.detailUserErr != nil {
		return gen.GetAdminUserRow{}, q.detailUserErr
	}
	row := q.detailUser
	if row.ID == "" {
		row.ID = actorID
	}
	return row, nil
}

func (q *fakeQuerier) ListUserIdentities(context.Context, string) ([]gen.ListUserIdentitiesRow, error) {
	return q.identityRows, nil
}

func (q *fakeQuerier) ListUserProjectMemberships(context.Context, string) ([]gen.ListUserProjectMembershipsRow, error) {
	return q.membershipRows, nil
}

func (q *fakeQuerier) ListUserSessions(context.Context, string) ([]gen.ListUserSessionsRow, error) {
	return q.sessionRows, nil
}

func (q *fakeQuerier) CountActiveAdministrators(context.Context) (int64, error) {
	return q.activeAdmins, nil
}

func (q *fakeQuerier) UpdateAdminUserProfile(_ context.Context, arg gen.UpdateAdminUserProfileParams) (int64, error) {
	q.opLog = append(q.opLog, "UpdateAdminUserProfile")
	q.updateUserParams = append(q.updateUserParams, arg)
	if q.updateUserErr != nil {
		return 0, q.updateUserErr
	}
	return q.updateUserRows, nil
}

func (q *fakeQuerier) UpdateAdminUserActor(_ context.Context, arg gen.UpdateAdminUserActorParams) error {
	q.opLog = append(q.opLog, "UpdateAdminUserActor")
	q.updateActorParams = append(q.updateActorParams, arg)
	return nil
}

func (q *fakeQuerier) UpdateLocalIdentitySubject(_ context.Context, arg gen.UpdateLocalIdentitySubjectParams) error {
	q.opLog = append(q.opLog, "UpdateLocalIdentitySubject")
	q.subjectUpdates = append(q.subjectUpdates, arg)
	return nil
}

func (q *fakeQuerier) AppUserExists(context.Context, string) (bool, error) {
	return q.appUserExists, nil
}

func (q *fakeQuerier) InvalidateActorPermissionCache(_ context.Context, actorID string) error {
	q.opLog = append(q.opLog, "InvalidateActorPermissionCache")
	q.invalidatedCaches = append(q.invalidatedCaches, actorID)
	return nil
}

func (q *fakeQuerier) CountCommentsByAuthor(context.Context, string) (int64, error) {
	return q.commentCount, nil
}

func (q *fakeQuerier) DeleteActorByID(_ context.Context, actorID string) (int64, error) {
	q.opLog = append(q.opLog, "DeleteActorByID")
	q.deletedActors = append(q.deletedActors, actorID)
	return q.deleteUserRows, nil
}

func (q *fakeQuerier) FindLocalCredentialByActor(context.Context, string) (string, error) {
	if q.credentialErr != nil {
		return "", q.credentialErr
	}
	return q.credentialID, nil
}

func (q *fakeQuerier) ResetLocalCredential(_ context.Context, arg gen.ResetLocalCredentialParams) error {
	q.opLog = append(q.opLog, "ResetLocalCredential")
	q.credentialResets = append(q.credentialResets, arg)
	return nil
}

func (q *fakeQuerier) RevokeActorSessions(_ context.Context, actorID string) (int64, error) {
	q.opLog = append(q.opLog, "RevokeActorSessions")
	q.revokedActors = append(q.revokedActors, actorID)
	return q.revokedSessions, nil
}

func (q *fakeQuerier) FindProjectIDByKey(_ context.Context, key string) (string, error) {
	id, ok := q.projectIDByKey[key]
	if !ok {
		return "", pgx.ErrNoRows
	}
	return id, nil
}

func (q *fakeQuerier) IsProjectScopedRole(_ context.Context, key string) (bool, error) {
	return q.projectRoles[key], nil
}

func (q *fakeQuerier) UpsertProjectMember(_ context.Context, arg gen.UpsertProjectMemberParams) error {
	q.opLog = append(q.opLog, "UpsertProjectMember")
	q.upsertedMembers = append(q.upsertedMembers, arg)
	return nil
}

func (q *fakeQuerier) GetProjectMembership(_ context.Context, arg gen.GetProjectMembershipParams) (gen.GetProjectMembershipRow, error) {
	row := q.membershipRow
	if row.ProjectID == "" {
		row.ProjectID = arg.ProjectID
	}
	return row, nil
}

func (q *fakeQuerier) DeleteProjectMember(_ context.Context, arg gen.DeleteProjectMemberParams) (int64, error) {
	q.opLog = append(q.opLog, "DeleteProjectMember")
	q.deletedMembers = append(q.deletedMembers, arg)
	return q.deleteMemberRows, nil
}

// ── ロール・権限カタログ（手順14。ApiDesign.md 7.1 / 7.2）─────────

func (q *fakeQuerier) ListRoles(_ context.Context, scope string) ([]gen.Role, error) {
	q.catalogScopes = append(q.catalogScopes, scope)
	if q.catalogErr != nil {
		return nil, q.catalogErr
	}
	return q.catalogRoles, nil
}

func (q *fakeQuerier) ListRolePermissionAssignments(_ context.Context, scope string) ([]gen.RolePermission, error) {
	q.catalogScopes = append(q.catalogScopes, scope)
	if q.catalogErr != nil {
		return nil, q.catalogErr
	}
	return q.catalogAssignments, nil
}

func (q *fakeQuerier) ListPermissions(context.Context) ([]gen.Permission, error) {
	if q.catalogErr != nil {
		return nil, q.catalogErr
	}
	return q.catalogPerms, nil
}
