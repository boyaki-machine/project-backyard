package v1

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// createFake は POST /projects を通せるフェイクとトランザクション実行口を返す。
func createFake(t *testing.T) (*fakeQuerier, *fakeTxRunner) {
	t.Helper()
	q := projectFake(t)
	q.templateStatuses = []gen.ListWorkflowStatusesRow{
		{Key: "todo", Name: "未着手", Category: "todo", SortOrder: 1, IsAgentReachable: true},
		{Key: "doing", Name: "進行中", Category: "in_progress", SortOrder: 2, IsAgentReachable: true},
		{Key: "done", Name: "完了", Category: "done", SortOrder: 3, IsAgentReachable: true},
	}
	q.templateTransitions = []gen.ListWorkflowTransitionsRow{
		{FromStatusKey: "todo", ToStatusKey: "doing", AllowedActorKinds: []byte(`["user","agent"]`)},
		{FromStatusKey: "doing", ToStatusKey: "done", AllowedActorKinds: []byte(`["user","agent"]`)},
	}
	q.detailRow = gen.GetProjectByKeyRow{
		ID:           "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
		Key:          "my-app",
		Name:         "社内タスク管理の刷新",
		Description:  txt("既存のExcel管理を置き換える"),
		Status:       "active",
		Settings:     []byte(`{"max_concurrent_agents":2}`),
		Version:      1,
		CreatedAt:    ts(time.Date(2026, 8, 15, 3, 4, 5, 0, time.UTC)),
		UpdatedAt:    ts(time.Date(2026, 8, 15, 3, 4, 5, 0, time.UTC)),
		WorkflowID:   txt("01K2F8QW3H7YRJ4M5N6P7Q8WFL"),
		WorkflowName: txt("シンプル"),
	}
	q.memberRows = []gen.ListProjectMembersRow{
		{
			ActorID: testActorID, Kind: auth.ActorKindUser, DisplayName: "田中",
			RoleKey: creatorRoleKey, JoinedAt: ts(time.Date(2026, 8, 15, 3, 4, 5, 0, time.UTC)),
		},
	}
	return q, &fakeTxRunner{q: q}
}

// postProject は Cookie 認証・CSRF つきで POST /projects を叩く。
func postProject(q *fakeQuerier, tx *fakeTxRunner, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	routerWithDeps(Deps{Queries: q, Tx: tx}).ServeHTTP(rec, req)
	return rec
}

const validCreateBody = `{"key":"my-app","name":"社内タスク管理の刷新",
	"description":"既存のExcel管理を置き換える","workflow_template":"simple"}`

// 5.3 の「サーバ側の処理」を順序ごと固定する。
func TestCreateProjectRunsFullSequenceInOneTransaction(t *testing.T) {
	q, tx := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := postProject(q, tx, token, validCreateBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/projects/my-app" {
		t.Errorf("Location = %q, want /api/v1/projects/my-app", got)
	}

	// **1トランザクションにまとめる**（5.3）。
	if tx.calls != 1 || !tx.committed {
		t.Errorf("トランザクション: calls=%d committed=%v, want 1/true", tx.calls, tx.committed)
	}

	// 手順の順序。ワークフローの複製は project より後で、紐づけはその後。
	want := []string{
		"CreateProject", "CreateProjectCounter",
		"FindWorkflowTemplate", "CreateProjectWorkflow",
		"ListWorkflowStatuses", "CreateWorkflowStatus", "CreateWorkflowStatus", "CreateWorkflowStatus",
		"ListWorkflowTransitions", "CreateWorkflowTransition", "CreateWorkflowTransition",
		"SetProjectWorkflow", "AddProjectMember", "InsertAuditLog",
	}
	if len(q.opLog) < len(want) {
		t.Fatalf("呼び出し順 = %v, want 先頭が %v", q.opLog, want)
	}
	for i, op := range want {
		if q.opLog[i] != op {
			t.Fatalf("呼び出し順[%d] = %q, want %q（全体=%v）", i, q.opLog[i], op, q.opLog)
		}
	}

	// project 本体。
	if len(q.createdProjects) != 1 {
		t.Fatalf("作成された project = %d件, want 1", len(q.createdProjects))
	}
	created := q.createdProjects[0]
	if created.Key != "my-app" || created.Name != "社内タスク管理の刷新" {
		t.Errorf("project = %+v", created)
	}
	if created.CreatedBy.String != testActorID {
		t.Errorf("created_by = %q, want %q", created.CreatedBy.String, testActorID)
	}
	if len(created.ID) != 26 {
		t.Errorf("id = %q, want ULID 26文字", created.ID)
	}
	if q.createdCounters[0] != created.ID {
		t.Errorf("project_counter の project_id = %q, want %q", q.createdCounters[0], created.ID)
	}

	// ワークフローはテンプレートから複製され、project へ紐づく。
	if q.createdWorkflows[0].ProjectID.String != created.ID {
		t.Errorf("workflow.project_id = %q, want %q", q.createdWorkflows[0].ProjectID.String, created.ID)
	}
	if len(q.createdStatuses) != 3 || len(q.createdTransitions) != 2 {
		t.Errorf("複製された statuses/transitions = %d/%d, want 3/2",
			len(q.createdStatuses), len(q.createdTransitions))
	}
	if q.linkedWorkflows[0].WorkflowID.String != q.createdWorkflows[0].ID {
		t.Errorf("project.workflow_id が複製したワークフローを指していない")
	}

	// 作成者は project_admin として登録される（5.3）。
	if len(q.addedMembers) != 1 {
		t.Fatalf("登録されたメンバー = %d件, want 1", len(q.addedMembers))
	}
	if m := q.addedMembers[0]; m.ActorID != testActorID || m.RoleKey != creatorRoleKey {
		t.Errorf("メンバー = %+v, want actor=%s role=%s", m, testActorID, creatorRoleKey)
	}

	// 監査（ApiDesign.md 2.10）。
	if got := q.auditActions(); len(got) != 1 || got[0] != "project.create" {
		t.Errorf("監査アクション = %v, want [project.create]", got)
	}
}

// 応答は 5.4 と同形式（5.3）。
func TestCreateProjectReturnsDetailView(t *testing.T) {
	q, tx := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	view := viewOf(t, postProject(q, tx, token, validCreateBody))

	for key, want := range map[string]any{
		"id":     "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
		"key":    "my-app",
		"status": "active",
	} {
		if view[key] != want {
			t.Errorf("%s = %v, want %v", key, view[key], want)
		}
	}
	if got, _ := view["version"].(float64); got != 1 {
		t.Errorf("version = %v, want 1", view["version"])
	}
	if view["created_at"] != "2026-08-15T03:04:05Z" {
		t.Errorf("created_at = %v, want ISO8601 UTC", view["created_at"])
	}

	wf, ok := view["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("workflow = %v, want オブジェクト", view["workflow"])
	}
	statuses, _ := wf["statuses"].([]any)
	if len(statuses) != 3 {
		t.Fatalf("workflow.statuses = %v, want 3件", wf["statuses"])
	}
	first, _ := statuses[0].(map[string]any)
	if first["key"] != "todo" || first["category"] != "todo" {
		t.Errorf("statuses[0] = %v", first)
	}
	if v, ok := first["requires_human_approval"].(bool); !ok || v {
		t.Errorf("statuses[0].requires_human_approval = %v, want false", first["requires_human_approval"])
	}

	members, _ := view["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("members = %v, want 1件", view["members"])
	}
	if m, _ := members[0].(map[string]any); m["role"] != creatorRoleKey {
		t.Errorf("members[0].role = %v, want %s", m["role"], creatorRoleKey)
	}
	if view["my_role"] != creatorRoleKey {
		t.Errorf("my_role = %v, want %s", view["my_role"], creatorRoleKey)
	}

	// my_permissions = システムロール ∪ プロジェクトロール（Design.md 6.4.1）。
	perms, _ := view["my_permissions"].([]any)
	got := map[string]bool{}
	for _, p := range perms {
		got[p.(string)] = true
	}
	for _, want := range []string{"project.view", "project.create", "ticket.close"} {
		if !got[want] {
			t.Errorf("my_permissions に %q が無い（= %v）", want, perms)
		}
	}

	// settings は jsonb をそのまま返す。
	settings, ok := view["settings"].(map[string]any)
	if !ok || settings["max_concurrent_agents"] != float64(2) {
		t.Errorf("settings = %v", view["settings"])
	}
}

// workflow_template 未指定は simple（ApiDesign.md 5.3）。
func TestCreateProjectDefaultsWorkflowTemplate(t *testing.T) {
	q, tx := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := postProject(q, tx, token, `{"key":"my-app","name":"テスト"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.audits) != 1 {
		t.Fatalf("監査が %d 件", len(q.audits))
	}
	// detail には既定が入る。監査の detail から確かめる。
	if !strings.Contains(string(q.audits[0].Detail), `"workflow_template":"simple"`) {
		t.Errorf("監査の detail = %s, want workflow_template=simple", q.audits[0].Detail)
	}
}

// 検証表（ApiDesign.md 5.3）。誤りは details に項目ごとに並べる。
func TestCreateProjectValidation(t *testing.T) {
	long := strings.Repeat("あ", 101)
	for _, tc := range []struct {
		name       string
		body       string
		wantFields []string
	}{
		{"キーが無い", `{"name":"テスト"}`, []string{"key"}},
		{"キーの形式", `{"key":"My_App","name":"テスト"}`, []string{"key"}},
		{"キーが1文字", `{"key":"a","name":"テスト"}`, []string{"key"}},
		{"予約語", `{"key":"admin","name":"テスト"}`, []string{"key"}},
		{"名前が無い", `{"key":"my-app"}`, []string{"name"}},
		{"名前が長い", `{"key":"my-app","name":"` + long + `"}`, []string{"name"}},
		{"説明が長い", `{"key":"my-app","name":"テスト","description":"` +
			strings.Repeat("あ", 1001) + `"}`, []string{"description"}},
		{"未知のテンプレート", `{"key":"my-app","name":"テスト","workflow_template":"kanban"}`,
			[]string{"workflow_template"}},
		{"複数の誤り", `{"key":"ADMIN","name":""}`, []string{"key", "name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, tx := createFake(t)
			token := tokenAs(q, auth.SystemRoleAdministrator)

			rec := postProject(q, tx, token, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			fields := map[string]bool{}
			for _, d := range errorOf(t, rec).Details {
				fields[d.Field] = true
			}
			for _, want := range tc.wantFields {
				if !fields[want] {
					t.Errorf("details のフィールド = %v, want %q を含む", fields, want)
				}
			}
			if tx.calls != 0 {
				t.Error("入力が不正なのにトランザクションを開始した")
			}
		})
	}
}

// 本文が JSON でなければ 400（2.5.1）。
func TestCreateProjectRejectsBrokenBody(t *testing.T) {
	q, tx := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := postProject(q, tx, token, `{"key":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "bad_request" {
		t.Errorf("code = %q, want bad_request", got)
	}
}

// キー重複は 409 already_exists（ApiDesign.md 5.3 / 2.5.1）。
//
// **検出はDBの UNIQUE 制約に委ねる。** check-key の結果は信頼しない。
func TestCreateProjectKeyConflict(t *testing.T) {
	q, tx := createFake(t)
	q.createProjectErr = &pgconn.PgError{
		Code: "23505", TableName: "project", ConstraintName: "project_key_key",
	}
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := postProject(q, tx, token, validCreateBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "already_exists" {
		t.Errorf("code = %q, want already_exists", e.Code)
	}
	if len(e.Details) != 1 || e.Details[0].Field != "key" {
		t.Errorf("details = %+v, want key の1件", e.Details)
	}
	if tx.committed {
		t.Error("重複で失敗したのにコミットした")
	}
	// **check-key を先に引かない。** 引いてしまうと TOCTOU の窓が広がるうえ、
	// 「確認では空きだったのに作成で失敗する」経路が2種類になる。
	if len(q.keyChecked) != 0 {
		t.Error("作成時に check-key のクエリを引いた")
	}
}

// 一意制約違反でも project 以外の表なら 500 に倒す（想定外の不具合を隠さない）。
func TestCreateProjectOtherUniqueViolationIsInternalError(t *testing.T) {
	q, tx := createFake(t)
	q.createProjectErr = &pgconn.PgError{Code: "23505", TableName: "workflow"}
	token := tokenAs(q, auth.SystemRoleAdministrator)

	if rec := postProject(q, tx, token, validCreateBody); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", rec.Code, rec.Body.String())
	}
}

// 監査の書き込みが失敗したら作成ごと失敗させる（手順4b の方針）。
func TestCreateProjectRollsBackWhenAuditFails(t *testing.T) {
	q, tx := createFake(t)
	q.auditErr = errors.New("audit_log への書き込みに失敗")
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := postProject(q, tx, token, validCreateBody)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", rec.Code, rec.Body.String())
	}
	if tx.committed {
		t.Error("監査に失敗したのにコミットした")
	}
}

// project.create を持たなければ 403（Design.md 6.4.4）。
func TestCreateProjectRequiresPermission(t *testing.T) {
	q, tx := createFake(t)
	q.permissions[auth.SystemRoleOperator] = []string{"project.view"}
	token := tokenAs(q, auth.SystemRoleOperator)

	rec := postProject(q, tx, token, validCreateBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "forbidden" {
		t.Errorf("code = %q, want forbidden", got)
	}
	if tx.calls != 0 {
		t.Error("認可で拒否したのにトランザクションを開始した")
	}
}

// Cookie 認証の状態変更系は CSRF を要求する（ApiDesign.md 2.4）。
func TestCreateProjectRequiresCSRF(t *testing.T) {
	q, tx := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(validCreateBody))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	routerWithDeps(Deps{Queries: q, Tx: tx}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "csrf_failed" {
		t.Errorf("code = %q, want csrf_failed", got)
	}
}

// トランザクション実行口が渡っていなければ 500（ルータの組み立て漏れ）。
func TestCreateProjectWithoutTxRunner(t *testing.T) {
	q, _ := createFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(validCreateBody))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	router(q).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500（body=%s）", rec.Code, rec.Body.String())
	}
}
