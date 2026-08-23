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

	// チケット（手順16b）
	ticket ticketFakeState

	// タグ・スプリント（手順16a）
	tagRows              []gen.ListTagsByProjectRow
	tagByID              map[string]gen.GetTagByIDRow
	tagErr               error
	tagListProjectIDs    []string
	nextTagSortOrder     int32
	createdTags          []gen.CreateTagParams
	createTagErr         error
	updatedTags          []gen.UpdateTagParams
	updateTagErr         error
	deletedTags          []gen.DeleteTagParams
	sprintRows           []gen.ListSprintsByProjectRow
	sprintByID           map[string]gen.GetSprintByIDRow
	sprintErr            error
	sprintListProjectIDs []string
	createdSprints       []gen.CreateSprintParams
	createSprintErr      error
	updatedSprints       []gen.UpdateSprintParams
	updateSprintErr      error
	deletedSprints       []gen.DeleteSprintParams

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

	// 自分自身（手順15。ApiDesign.md 4.2 / 4.3）
	//
	// **6章のフィールドと分けて持つ。** 同じ名前を使い回すと、/me の
	// テストが admin 側の既定値に引きずられて「なぜ通ったか」が読めなくなる。
	myProfileParams  []gen.UpdateMyProfileParams
	myProfileRows    int64
	myProfileErr     error
	myNameParams     []gen.UpdateMyDisplayNameParams
	myCredential     gen.FindMyLocalCredentialRow
	myCredentialErr  error
	myPasswordParams []gen.ChangeMyPasswordParams
	myRevokeParams   []gen.RevokeMyOtherSessionsParams
	myRevokedCount   int64

	// アクセストークン（手順15b。ApiDesign.md 4.4）
	//
	// myTokenCount は「いま何本あるか」で、上限5本の判定に効く。
	// myTokenFindErr に pgx.ErrNoRows を入れると「他人のトークン・
	// セッション・存在しない ID」のいずれも表せる（4.4.3 はこの3つを
	// 区別せず 404 に寄せる）。
	myTokenRows        []gen.ListMyAPITokensRow
	myTokenListErr     error
	myTokenCount       int64
	myTokenCountErr    error
	myTokenRow         gen.FindMyAPITokenRow
	myTokenFindErr     error
	myTokenRevokes     []gen.RevokeMyAPITokenParams
	myTokenRevokedRows int64

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

// ── 自分自身（手順15。ApiDesign.md 4.2 / 4.3）─────────────────

func (q *fakeQuerier) UpdateMyProfile(_ context.Context, arg gen.UpdateMyProfileParams) (int64, error) {
	q.opLog = append(q.opLog, "UpdateMyProfile")
	q.myProfileParams = append(q.myProfileParams, arg)
	if q.myProfileErr != nil {
		return 0, q.myProfileErr
	}
	return q.myProfileRows, nil
}

func (q *fakeQuerier) UpdateMyDisplayName(_ context.Context, arg gen.UpdateMyDisplayNameParams) error {
	q.opLog = append(q.opLog, "UpdateMyDisplayName")
	q.myNameParams = append(q.myNameParams, arg)
	return nil
}

func (q *fakeQuerier) FindMyLocalCredential(context.Context, string) (gen.FindMyLocalCredentialRow, error) {
	if q.myCredentialErr != nil {
		return gen.FindMyLocalCredentialRow{}, q.myCredentialErr
	}
	return q.myCredential, nil
}

func (q *fakeQuerier) ChangeMyPassword(_ context.Context, arg gen.ChangeMyPasswordParams) error {
	q.opLog = append(q.opLog, "ChangeMyPassword")
	q.myPasswordParams = append(q.myPasswordParams, arg)
	return nil
}

func (q *fakeQuerier) RevokeMyOtherSessions(_ context.Context, arg gen.RevokeMyOtherSessionsParams) (int64, error) {
	q.opLog = append(q.opLog, "RevokeMyOtherSessions")
	q.myRevokeParams = append(q.myRevokeParams, arg)
	return q.myRevokedCount, nil
}

// ── アクセストークン（手順15b。ApiDesign.md 4.4）──────────────────

func (q *fakeQuerier) ListMyAPITokens(_ context.Context, actorID string) ([]gen.ListMyAPITokensRow, error) {
	q.opLog = append(q.opLog, "ListMyAPITokens")
	if q.myTokenListErr != nil {
		return nil, q.myTokenListErr
	}
	_ = actorID
	return q.myTokenRows, nil
}

func (q *fakeQuerier) CountMyAPITokens(context.Context, string) (int64, error) {
	q.opLog = append(q.opLog, "CountMyAPITokens")
	if q.myTokenCountErr != nil {
		return 0, q.myTokenCountErr
	}
	return q.myTokenCount, nil
}

func (q *fakeQuerier) FindMyAPIToken(_ context.Context, arg gen.FindMyAPITokenParams) (gen.FindMyAPITokenRow, error) {
	q.opLog = append(q.opLog, "FindMyAPIToken")
	if q.myTokenFindErr != nil {
		return gen.FindMyAPITokenRow{}, q.myTokenFindErr
	}
	row := q.myTokenRow
	if row.ID == "" {
		row.ID = arg.ID
	}
	return row, nil
}

func (q *fakeQuerier) RevokeMyAPIToken(_ context.Context, arg gen.RevokeMyAPITokenParams) (int64, error) {
	q.opLog = append(q.opLog, "RevokeMyAPIToken")
	q.myTokenRevokes = append(q.myTokenRevokes, arg)
	return q.myTokenRevokedRows, nil
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

// ── タグ・スプリント（手順16a。ApiDesign.md 9.11 / 9.12）──────────
//
// **1件取得は ID をキーにした map で持つ。** ハンドラが「書いてから読み直す」
// 形（ticket_count を応答に載せるため）なので、書き込みの結果が読み取りに
// 反映されないと、作成・更新の応答を検証できない。

func (q *fakeQuerier) ListTagsByProject(_ context.Context, projectID string) ([]gen.ListTagsByProjectRow, error) {
	q.opLog = append(q.opLog, "ListTagsByProject")
	q.tagListProjectIDs = append(q.tagListProjectIDs, projectID)
	if q.tagErr != nil {
		return nil, q.tagErr
	}
	return q.tagRows, nil
}

func (q *fakeQuerier) GetTagByID(_ context.Context, arg gen.GetTagByIDParams) (gen.GetTagByIDRow, error) {
	q.opLog = append(q.opLog, "GetTagByID")
	row, ok := q.tagByID[arg.ID]
	if !ok {
		return gen.GetTagByIDRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) NextTagSortOrder(_ context.Context, _ string) (int32, error) {
	q.opLog = append(q.opLog, "NextTagSortOrder")
	return q.nextTagSortOrder, nil
}

func (q *fakeQuerier) CreateTag(_ context.Context, arg gen.CreateTagParams) error {
	q.opLog = append(q.opLog, "CreateTag")
	q.createdTags = append(q.createdTags, arg)
	if q.createTagErr != nil {
		return q.createTagErr
	}
	if q.tagByID == nil {
		q.tagByID = map[string]gen.GetTagByIDRow{}
	}
	q.tagByID[arg.ID] = gen.GetTagByIDRow{
		ID: arg.ID, Name: arg.Name, SortOrder: arg.SortOrder,
	}
	return nil
}

func (q *fakeQuerier) UpdateTag(_ context.Context, arg gen.UpdateTagParams) (int64, error) {
	q.opLog = append(q.opLog, "UpdateTag")
	q.updatedTags = append(q.updatedTags, arg)
	if q.updateTagErr != nil {
		return 0, q.updateTagErr
	}
	row, ok := q.tagByID[arg.ID]
	if !ok {
		return 0, nil
	}
	if arg.Name.Valid {
		row.Name = arg.Name.String
	}
	if arg.SortOrder.Valid {
		row.SortOrder = arg.SortOrder.Int32
	}
	q.tagByID[arg.ID] = row
	return 1, nil
}

func (q *fakeQuerier) DeleteTag(_ context.Context, arg gen.DeleteTagParams) (int64, error) {
	q.opLog = append(q.opLog, "DeleteTag")
	q.deletedTags = append(q.deletedTags, arg)
	if _, ok := q.tagByID[arg.ID]; !ok {
		return 0, nil
	}
	delete(q.tagByID, arg.ID)
	return 1, nil
}

func (q *fakeQuerier) ListSprintsByProject(_ context.Context, projectID string) ([]gen.ListSprintsByProjectRow, error) {
	q.opLog = append(q.opLog, "ListSprintsByProject")
	q.sprintListProjectIDs = append(q.sprintListProjectIDs, projectID)
	if q.sprintErr != nil {
		return nil, q.sprintErr
	}
	return q.sprintRows, nil
}

func (q *fakeQuerier) GetSprintByID(_ context.Context, arg gen.GetSprintByIDParams) (gen.GetSprintByIDRow, error) {
	q.opLog = append(q.opLog, "GetSprintByID")
	row, ok := q.sprintByID[arg.ID]
	if !ok {
		return gen.GetSprintByIDRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) CreateSprint(_ context.Context, arg gen.CreateSprintParams) error {
	q.opLog = append(q.opLog, "CreateSprint")
	q.createdSprints = append(q.createdSprints, arg)
	if q.createSprintErr != nil {
		return q.createSprintErr
	}
	if q.sprintByID == nil {
		q.sprintByID = map[string]gen.GetSprintByIDRow{}
	}
	q.sprintByID[arg.ID] = gen.GetSprintByIDRow{
		ID: arg.ID, Name: arg.Name, Goal: arg.Goal,
		StartDate: arg.StartDate, EndDate: arg.EndDate, Status: arg.Status,
	}
	return nil
}

func (q *fakeQuerier) UpdateSprint(_ context.Context, arg gen.UpdateSprintParams) (int64, error) {
	q.opLog = append(q.opLog, "UpdateSprint")
	q.updatedSprints = append(q.updatedSprints, arg)
	if q.updateSprintErr != nil {
		return 0, q.updateSprintErr
	}
	row, ok := q.sprintByID[arg.ID]
	if !ok {
		return 0, nil
	}
	if arg.Name.Valid {
		row.Name = arg.Name.String
	}
	if arg.Status.Valid {
		row.Status = arg.Status.String
	}
	if arg.SetGoal {
		row.Goal = arg.Goal
	}
	if arg.SetStartDate {
		row.StartDate = arg.StartDate
	}
	if arg.SetEndDate {
		row.EndDate = arg.EndDate
	}
	q.sprintByID[arg.ID] = row
	return 1, nil
}

func (q *fakeQuerier) DeleteSprint(_ context.Context, arg gen.DeleteSprintParams) (int64, error) {
	q.opLog = append(q.opLog, "DeleteSprint")
	q.deletedSprints = append(q.deletedSprints, arg)
	if _, ok := q.sprintByID[arg.ID]; !ok {
		return 0, nil
	}
	delete(q.sprintByID, arg.ID)
	return 1, nil
}

// ── チケット（手順16b。ApiDesign.md 9.2 / 9.3 / 9.4）────────────────
//
// **書いてから読み直す形をフェイクでも保つ。** POST /tickets の応答は
// 9.5 形式であり、ハンドラは作成後に GetTicketBySeq で読み直す（9.3）。
// 書き込みが読み取りに反映されないと、作成の応答を検証できない。

// ticketFakeState はチケット系のフェイクが持つ状態。
// fakeQuerier の項目が増えすぎるのを避けてひとまとめにしてある。
type ticketFakeState struct {
	rows       []gen.ListTicketsRow
	listParams []gen.ListTicketsParams
	listErr    error

	tagRows  []gen.ListTagsForTicketsRow
	tagIDsIn [][]string

	bySeq     map[int32]gen.GetTicketBySeqRow
	briefByID map[string]gen.GetTicketBriefRow
	children  []gen.ListTicketChildrenBriefRow
	idBySeq   map[int32]string

	nextSeq          int32
	initialStatusKey string
	created          []gen.CreateTicketParams
	createErr        error
	attached         []gen.AttachTicketTagParams

	projectTagCount int64
	sprintExists    bool
	isMember        bool

	// 並び順は sortRowBySeq から計算する（固定値を持たない）。
	sortRowBySeq map[int32]gen.GetTicketSortRowRow
	idsInOrder   []string

	moved      []gen.MoveTicketParams
	setSortKey []gen.SetTicketSortKeyParams

	activities []gen.InsertActivityParams
}

func (q *fakeQuerier) ListTickets(_ context.Context, arg gen.ListTicketsParams) ([]gen.ListTicketsRow, error) {
	q.opLog = append(q.opLog, "ListTickets")
	q.ticket.listParams = append(q.ticket.listParams, arg)
	if q.ticket.listErr != nil {
		return nil, q.ticket.listErr
	}
	return q.ticket.rows, nil
}

func (q *fakeQuerier) ListTagsForTickets(_ context.Context, ids []string) ([]gen.ListTagsForTicketsRow, error) {
	q.opLog = append(q.opLog, "ListTagsForTickets")
	q.ticket.tagIDsIn = append(q.ticket.tagIDsIn, ids)
	return q.ticket.tagRows, nil
}

func (q *fakeQuerier) GetTicketBySeq(_ context.Context, arg gen.GetTicketBySeqParams) (gen.GetTicketBySeqRow, error) {
	q.opLog = append(q.opLog, "GetTicketBySeq")
	row, ok := q.ticket.bySeq[arg.Seq]
	if !ok {
		return gen.GetTicketBySeqRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) GetTicketBrief(_ context.Context, id string) (gen.GetTicketBriefRow, error) {
	q.opLog = append(q.opLog, "GetTicketBrief")
	row, ok := q.ticket.briefByID[id]
	if !ok {
		return gen.GetTicketBriefRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) ListTicketChildrenBrief(context.Context, pgtype.Text) ([]gen.ListTicketChildrenBriefRow, error) {
	q.opLog = append(q.opLog, "ListTicketChildrenBrief")
	return q.ticket.children, nil
}

func (q *fakeQuerier) FindTicketIDBySeq(_ context.Context, arg gen.FindTicketIDBySeqParams) (string, error) {
	q.opLog = append(q.opLog, "FindTicketIDBySeq")
	id, ok := q.ticket.idBySeq[arg.Seq]
	if !ok {
		return "", pgx.ErrNoRows
	}
	return id, nil
}

func (q *fakeQuerier) NextTicketSeq(context.Context, string) (int32, error) {
	q.opLog = append(q.opLog, "NextTicketSeq")
	return q.ticket.nextSeq, nil
}

func (q *fakeQuerier) ResolveInitialStatusKey(context.Context, string) (string, error) {
	q.opLog = append(q.opLog, "ResolveInitialStatusKey")
	return q.ticket.initialStatusKey, nil
}

func (q *fakeQuerier) CreateTicket(_ context.Context, arg gen.CreateTicketParams) error {
	q.opLog = append(q.opLog, "CreateTicket")
	q.ticket.created = append(q.ticket.created, arg)
	if q.ticket.createErr != nil {
		return q.ticket.createErr
	}
	if q.ticket.bySeq == nil {
		q.ticket.bySeq = map[int32]gen.GetTicketBySeqRow{}
	}
	q.ticket.bySeq[arg.Seq] = gen.GetTicketBySeqRow{
		ID: arg.ID, Seq: arg.Seq, Type: arg.Type, Title: arg.Title,
		BodyMd: arg.BodyMd, StatusKey: arg.StatusKey, Priority: arg.Priority,
		AssigneeID: arg.AssigneeID, ReporterID: arg.ReporterID,
		EstimatePoint: arg.EstimatePoint, EstimateHours: arg.EstimateHours,
		StartDate: arg.StartDate, DueDate: arg.DueDate,
		SprintID: arg.SprintID, SortKey: arg.SortKey,
		Version: 1, CreatedAt: ts(time.Now()), UpdatedAt: ts(time.Now()),
	}
	return nil
}

func (q *fakeQuerier) AttachTicketTag(_ context.Context, arg gen.AttachTicketTagParams) error {
	q.opLog = append(q.opLog, "AttachTicketTag")
	q.ticket.attached = append(q.ticket.attached, arg)
	return nil
}

func (q *fakeQuerier) CountProjectTagsByIDs(_ context.Context, _ gen.CountProjectTagsByIDsParams) (int64, error) {
	q.opLog = append(q.opLog, "CountProjectTagsByIDs")
	return q.ticket.projectTagCount, nil
}

func (q *fakeQuerier) SprintExistsInProject(context.Context, gen.SprintExistsInProjectParams) (bool, error) {
	q.opLog = append(q.opLog, "SprintExistsInProject")
	return q.ticket.sprintExists, nil
}

func (q *fakeQuerier) IsProjectMember(context.Context, gen.IsProjectMemberParams) (bool, error) {
	q.opLog = append(q.opLog, "IsProjectMember")
	return q.ticket.isMember, nil
}

// 並び順を引く4本は、固定値ではなく sortRowBySeq から計算する。
//
// **振り直し（rebalance）の後にキーが変わることを再現するため**である。固定値を
// 返すフェイクだと、振り直してももう一度同じ隣が返り、実装が回復するかどうかを
// 測れない（LEARNINGS #14「検証が意図と違うものを見ていた」）。
// 空文字は SQL 側の COALESCE(..., '') と同じく「該当なし」を表す。

func (q *fakeQuerier) MinTicketSortKey(context.Context, string) (string, error) {
	q.opLog = append(q.opLog, "MinTicketSortKey")
	min := ""
	for _, key := range q.ticketSortKeys() {
		if min == "" || key < min {
			min = key
		}
	}
	return min, nil
}

func (q *fakeQuerier) MaxTicketSortKey(context.Context, string) (string, error) {
	q.opLog = append(q.opLog, "MaxTicketSortKey")
	max := ""
	for _, key := range q.ticketSortKeys() {
		if key > max {
			max = key
		}
	}
	return max, nil
}

// MinTicketSortKeyInStage / MaxTicketSortKeyInStage は position の解決に使う
// （9.4.1）。**段で絞る**——"first" は「移動先の段の先頭」であって
// 「プロジェクト全体の先頭」ではない。
func (q *fakeQuerier) MinTicketSortKeyInStage(
	_ context.Context, arg gen.MinTicketSortKeyInStageParams,
) (string, error) {
	q.opLog = append(q.opLog, "MinTicketSortKeyInStage")
	min := ""
	for _, key := range q.ticketSortKeysInStage(arg.Staged) {
		if min == "" || key < min {
			min = key
		}
	}
	return min, nil
}

func (q *fakeQuerier) MaxTicketSortKeyInStage(
	_ context.Context, arg gen.MaxTicketSortKeyInStageParams,
) (string, error) {
	q.opLog = append(q.opLog, "MaxTicketSortKeyInStage")
	max := ""
	for _, key := range q.ticketSortKeysInStage(arg.Staged) {
		if key > max {
			max = key
		}
	}
	return max, nil
}

func (q *fakeQuerier) TicketSortKeyAfter(_ context.Context, arg gen.TicketSortKeyAfterParams) (string, error) {
	q.opLog = append(q.opLog, "TicketSortKeyAfter")
	out := ""
	for _, key := range q.ticketSortKeys() {
		if key > arg.After && (out == "" || key < out) {
			out = key
		}
	}
	return out, nil
}

func (q *fakeQuerier) TicketSortKeyBefore(_ context.Context, arg gen.TicketSortKeyBeforeParams) (string, error) {
	q.opLog = append(q.opLog, "TicketSortKeyBefore")
	out := ""
	for _, key := range q.ticketSortKeys() {
		if key < arg.Before && key > out {
			out = key
		}
	}
	return out, nil
}

// ticketSortKeys は登録済みの行が持つ空でない sort_key を返す。
func (q *fakeQuerier) ticketSortKeys() []string {
	keys := make([]string, 0, len(q.ticket.sortRowBySeq))
	for _, row := range q.ticket.sortRowBySeq {
		if row.SortKey.Valid && row.SortKey.String != "" {
			keys = append(keys, row.SortKey.String)
		}
	}
	return keys
}

// ticketSortKeysInStage は片方の段に属する行の sort_key だけを返す。
// SQL 側の `(staged_at IS NOT NULL) = @staged` と同じ判定である。
func (q *fakeQuerier) ticketSortKeysInStage(staged bool) []string {
	keys := make([]string, 0, len(q.ticket.sortRowBySeq))
	for _, row := range q.ticket.sortRowBySeq {
		if row.StagedAt.Valid != staged {
			continue
		}
		if row.SortKey.Valid && row.SortKey.String != "" {
			keys = append(keys, row.SortKey.String)
		}
	}
	return keys
}

func (q *fakeQuerier) GetTicketSortRow(_ context.Context, arg gen.GetTicketSortRowParams) (gen.GetTicketSortRowRow, error) {
	q.opLog = append(q.opLog, "GetTicketSortRow")
	row, ok := q.ticket.sortRowBySeq[arg.Seq]
	if !ok {
		return gen.GetTicketSortRowRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) ListTicketIDsInSortOrder(context.Context, string) ([]string, error) {
	q.opLog = append(q.opLog, "ListTicketIDsInSortOrder")
	return q.ticket.idsInOrder, nil
}

func (q *fakeQuerier) SetTicketSortKey(_ context.Context, arg gen.SetTicketSortKeyParams) error {
	q.opLog = append(q.opLog, "SetTicketSortKey")
	q.ticket.setSortKey = append(q.ticket.setSortKey, arg)
	// 振り直しの結果を読み取りへ反映する（上の4本が新しいキーを見るため）。
	for seq, row := range q.ticket.sortRowBySeq {
		if row.ID == arg.ID {
			row.SortKey = arg.SortKey
			q.ticket.sortRowBySeq[seq] = row
		}
	}
	return nil
}

func (q *fakeQuerier) MoveTicket(_ context.Context, arg gen.MoveTicketParams) (gen.MoveTicketRow, error) {
	q.opLog = append(q.opLog, "MoveTicket")
	q.ticket.moved = append(q.ticket.moved, arg)
	// 実装は RETURNING で seq / version を返す。フェイクでは登録済みの行から引く。
	// **version は +1 する**（9.4。振り直しの SetTicketSortKey とはここが違う）。
	for seq, r := range q.ticket.sortRowBySeq {
		if r.ID == arg.ID {
			r.SortKey, r.Version = arg.SortKey, r.Version+1
			// change_stage が false のとき staged_at は現在値のまま（9.4.1）。
			if arg.ChangeStage {
				r.StagedAt = arg.StagedAt
			}
			q.ticket.sortRowBySeq[seq] = r
			return gen.MoveTicketRow{
				Seq: seq, SortKey: arg.SortKey, StagedAt: r.StagedAt, Version: r.Version,
			}, nil
		}
	}
	return gen.MoveTicketRow{SortKey: arg.SortKey, Version: 1}, nil
}

func (q *fakeQuerier) InsertActivity(_ context.Context, arg gen.InsertActivityParams) error {
	q.opLog = append(q.opLog, "InsertActivity")
	q.ticket.activities = append(q.ticket.activities, arg)
	return nil
}
