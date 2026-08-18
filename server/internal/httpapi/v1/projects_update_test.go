package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// detailFake は /projects/{key} 配下を通せるフェイクと実行口を返す。
//
// 認可（RequireProjectPermission）とハンドラの両方が同じフェイクを引く。
// **プロジェクト層のロールは project_admin** とし、5.4〜5.6 が要求する
// project.view / project.edit / project.archive を持たせる。
func detailFake(t *testing.T) (*fakeQuerier, *fakeTxRunner) {
	t.Helper()
	q := projectFake(t)
	q.permissions["project_admin"] = []string{
		"project.view", "project.edit", "project.archive", "ticket.view",
	}
	q.withProjectMember("my-app", "project_admin")
	q.detailRow = gen.GetProjectByKeyRow{
		ID:           testProjectID,
		Key:          "my-app",
		Name:         "社内タスク管理の刷新",
		Description:  txt("既存のExcel管理を置き換える"),
		Status:       "active",
		Settings:     []byte(`{"max_concurrent_agents":2}`),
		Version:      3,
		CreatedAt:    ts(time.Date(2026, 8, 15, 3, 4, 5, 0, time.UTC)),
		UpdatedAt:    ts(time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC)),
		WorkflowID:   txt("01K2F8QW3H7YRJ4M5N6P7Q8WFL"),
		WorkflowName: txt("シンプル"),
	}
	q.memberRows = []gen.ListProjectMembersRow{{
		ActorID: testActorID, Kind: auth.ActorKindUser, DisplayName: "田中",
		RoleKey: "project_admin", JoinedAt: ts(time.Date(2026, 8, 15, 3, 4, 5, 0, time.UTC)),
	}}
	q.updateRows = 1
	q.statusRows = 1
	return q, &fakeTxRunner{q: q}
}

// callProject は Cookie 認証・CSRF つきで /projects/{key} 配下を叩く。
// ifMatch が空文字ならヘッダを付けない（2.8 の省略時の検証に使う）。
func callProject(
	q *fakeQuerier, tx *fakeTxRunner, method, path, token, ifMatch, body string,
) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	routerWithDeps(Deps{Queries: q, Tx: tx}).ServeHTTP(rec, req)
	return rec
}

// ── GET /projects/{key}（ApiDesign.md 5.4）────────────────────

// 5.4 の応答は workflow・members・my_permissions・version を持つ。
func TestGetProjectReturnsDetail(t *testing.T) {
	q, tx := detailFake(t)
	q.templateStatuses = []gen.ListWorkflowStatusesRow{
		{Key: "todo", Name: "未着手", Category: "todo", SortOrder: 1, IsAgentReachable: true},
	}
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodGet, "/api/v1/projects/my-app", token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	view := viewOf(t, rec)
	if view["key"] != "my-app" || view["status"] != "active" {
		t.Errorf("key/status = %v / %v", view["key"], view["status"])
	}
	if got, _ := view["version"].(float64); got != 3 {
		t.Errorf("version = %v, want 3", view["version"])
	}
	if view["my_role"] != "project_admin" {
		t.Errorf("my_role = %v, want project_admin", view["my_role"])
	}
	// settings は jsonb をそのまま返す（5.4）。
	settings, ok := view["settings"].(map[string]any)
	if !ok || settings["max_concurrent_agents"] == nil {
		t.Errorf("settings = %v, want jsonb をそのまま", view["settings"])
	}
	if _, ok := view["workflow"].(map[string]any); !ok {
		t.Errorf("workflow = %v, want オブジェクト", view["workflow"])
	}
	if members, ok := view["members"].([]any); !ok || len(members) != 1 {
		t.Errorf("members = %v, want 1件", view["members"])
	}
	// 実効権限は「システムロール ∪ プロジェクトロール」（Design.md 6.4.1）。
	perms, _ := view["my_permissions"].([]any)
	if !containsString(perms, "project.edit") || !containsString(perms, "project.create") {
		t.Errorf("my_permissions = %v, want システム側とプロジェクト側の和", perms)
	}
}

// **非メンバーには 404**（Design.md 6.4.5。403 だと存在が漏れる）。
func TestGetProjectHidesUnreachableProject(t *testing.T) {
	q, tx := detailFake(t)
	// プロジェクトは在るが、このアクターはメンバーではない。
	q.withProjectMember("secret-app", "")
	token := tokenAs(q, auth.SystemRoleOperator)

	rec := callProject(q, tx, http.MethodGet, "/api/v1/projects/secret-app", token, "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "not_found" {
		t.Errorf("code = %q, want not_found", got)
	}
}

// ── PATCH /projects/{key}（ApiDesign.md 5.5）───────────────────

// 正しい If-Match なら 200 で、更新後の 5.4 形式が返る。
func TestPatchProjectUpdatesAndReturnsDetail(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"name":"新しい名前","description":"新しい説明"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}

	if len(q.updateParams) != 1 {
		t.Fatalf("UpdateProject の呼び出し = %d回, want 1", len(q.updateParams))
	}
	got := q.updateParams[0]
	if got.Key != "my-app" || got.Version != 3 {
		t.Errorf("key/version = %q / %d, want my-app / 3", got.Key, got.Version)
	}
	if !got.Name.Valid || got.Name.String != "新しい名前" {
		t.Errorf("name = %v, want 新しい名前", got.Name)
	}
	if !got.DescriptionSet || got.Description.String != "新しい説明" {
		t.Errorf("description = %v (set=%v)", got.Description, got.DescriptionSet)
	}
	// settings を送っていないので据え置き（COALESCE が効く）。
	if got.Settings != nil {
		t.Errorf("settings = %v, want nil（送っていないので据え置き）", got.Settings)
	}
}

// **送られたフィールドだけを更新する**（5.5 の部分更新）。
func TestPatchProjectLeavesOmittedFieldsAlone(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"name":"名前だけ"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	got := q.updateParams[0]
	if got.DescriptionSet {
		t.Error("description を送っていないのに DescriptionSet = true（据え置きにならない）")
	}
	if got.Settings != nil {
		t.Errorf("settings = %v, want nil", got.Settings)
	}
}

// `"description": null` は「説明を消す」であり、未送信とは区別する。
func TestPatchProjectClearsDescriptionWithExplicitNull(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"description":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	got := q.updateParams[0]
	if !got.DescriptionSet {
		t.Fatal("DescriptionSet = false, want true（null は「消す」という指示）")
	}
	if got.Description.Valid {
		t.Errorf("description = %v, want NULL", got.Description)
	}
}

// **If-Match が無ければ 422**（2.8。ヘッダの付け忘れで黙って上書きさせない）。
func TestPatchProjectRequiresIfMatch(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, "", `{"name":"x"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	got := errorOf(t, rec)
	if got.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", got.Code)
	}
	if !hasDetail(got, "If-Match", "required") {
		t.Errorf("details = %v, want If-Match/required", got.Details)
	}
	if len(q.updateParams) != 0 {
		t.Error("検証に失敗したのに UPDATE を実行している")
	}
}

// `key` は変更できない（5.5）。**`error.code` は validation_failed** であり、
// `immutable_field` は `details[].code` の値である。
func TestPatchProjectRejectsKeyChange(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"key":"renamed","name":"新しい名前"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	got := errorOf(t, rec)
	if got.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed（immutable_field は details 側）", got.Code)
	}
	if !hasDetail(got, "key", "immutable_field") {
		t.Errorf("details = %v, want key/immutable_field", got.Details)
	}
}

// **If-Match と本文の誤りは1つの 422 にまとめる**（2.5 の details）。
func TestPatchProjectMergesValidationErrors(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, "",
		`{"name":""}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	got := errorOf(t, rec)
	if !hasDetail(got, "If-Match", "required") || !hasDetail(got, "name", "required") {
		t.Errorf("details = %v, want If-Match と name の両方", got.Details)
	}
}

// version が食い違えば 409 conflict（2.8）。
func TestPatchProjectConflictsOnStaleVersion(t *testing.T) {
	q, tx := detailFake(t)
	q.updateRows = 0 // WHERE の version 照合に外れた
	q.keyExists = true
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"1"`,
		`{"name":"新しい名前"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "conflict" {
		t.Errorf("code = %q, want conflict", got)
	}
	if tx.committed {
		t.Error("競合したのにコミットしている")
	}
}

// **0 行でもプロジェクトが消えていたなら 404**（409 と読み分ける）。
func TestPatchProjectReturns404WhenProjectVanished(t *testing.T) {
	q, tx := detailFake(t)
	q.updateRows = 0
	q.keyExists = false
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"name":"新しい名前"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// project.edit を持たないメンバーは 403（メンバーなので 404 にはしない）。
func TestPatchProjectForbiddenWithoutEditPermission(t *testing.T) {
	q, tx := detailFake(t)
	q.permissions["project_viewer"] = []string{"project.view"}
	q.withProjectMember("my-app", "project_viewer")
	token := tokenAs(q, auth.SystemRoleOperator)

	rec := callProject(q, tx, http.MethodPatch, "/api/v1/projects/my-app", token, `"3"`,
		`{"name":"新しい名前"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── archive / unarchive（ApiDesign.md 5.6）─────────────────────

// archive は status を切り替え、5.4 形式を返し、監査に残す。
func TestArchiveProjectSwitchesStatusAndAudits(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPost, "/api/v1/projects/my-app/archive", token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.statusParams) != 1 || q.statusParams[0].Status != "archived" {
		t.Fatalf("SetProjectStatus = %v, want status=archived", q.statusParams)
	}
	// 応答は 5.4 形式（`204` ではない）。
	if view := viewOf(t, rec); view["key"] != "my-app" {
		t.Errorf("応答が 5.4 形式でない: %v", view)
	}

	if got := q.auditActions(); len(got) != 1 || got[0] != "project.archive" {
		t.Fatalf("監査 = %v, want [project.archive]", got)
	}
	var detail map[string]any
	if err := json.Unmarshal(q.audits[0].Detail, &detail); err != nil {
		t.Fatalf("監査の detail が JSON でない: %v", err)
	}
	if detail["status"] != "archived" {
		t.Errorf("detail.status = %v, want archived（unarchive と区別する材料）", detail["status"])
	}
}

// unarchive も同じ経路を通り、status だけが違う。
func TestUnarchiveProjectSwitchesStatusBack(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPost, "/api/v1/projects/my-app/unarchive", token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.statusParams) != 1 || q.statusParams[0].Status != "active" {
		t.Fatalf("SetProjectStatus = %v, want status=active", q.statusParams)
	}
	var detail map[string]any
	if err := json.Unmarshal(q.audits[0].Detail, &detail); err != nil {
		t.Fatalf("監査の detail が JSON でない: %v", err)
	}
	if detail["status"] != "active" {
		t.Errorf("detail.status = %v, want active", detail["status"])
	}
}

// **既にその状態なら 200 のまま、監査には残さない**（5.6 の冪等）。
func TestArchiveProjectIsIdempotent(t *testing.T) {
	q, tx := detailFake(t)
	q.statusRows = 0 // WHERE の status <> 'archived' に外れた
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPost, "/api/v1/projects/my-app/archive", token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if got := q.auditActions(); len(got) != 0 {
		t.Errorf("監査 = %v, want なし（空振りを記録すると本当の切り替えが埋もれる）", got)
	}
}

// **archive は If-Match を要求しない**（2.8）。ヘッダ無しで通ること。
func TestArchiveProjectDoesNotRequireIfMatch(t *testing.T) {
	q, tx := detailFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := callProject(q, tx, http.MethodPost, "/api/v1/projects/my-app/archive", token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（If-Match は 5.6 の要件ではない）", rec.Code)
	}
}

// project.archive を持たないメンバーは 403。
func TestArchiveProjectForbiddenWithoutPermission(t *testing.T) {
	q, tx := detailFake(t)
	q.permissions["project_member"] = []string{"project.view", "project.edit"}
	q.withProjectMember("my-app", "project_member")
	token := tokenAs(q, auth.SystemRoleOperator)

	rec := callProject(q, tx, http.MethodPost, "/api/v1/projects/my-app/archive", token, "", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}
}

// ── 予約語（ApiDesign.md 5.2）─────────────────────────────────

// **check-key はプロジェクトキーとして使えない**（手順11で追加）。
// 使えると GET /projects/check-key が check-key エンドポイントに吸われ、
// そのプロジェクトへ到達できなくなる。
func TestCheckKeyIsReserved(t *testing.T) {
	q := projectFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet, "/api/v1/projects/check-key?key=check-key", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if view["available"] != false || view["reason"] != "reserved" {
		t.Errorf("available/reason = %v / %v, want false / reserved", view["available"], view["reason"])
	}
}

// ── 小さな道具 ──────────────────────────────────────────────

func hasDetail(e apiError, field, code string) bool {
	for _, d := range e.Details {
		if d.Field == field && d.Code == code {
			return true
		}
	}
	return false
}

func containsString(xs []any, want string) bool {
	for _, x := range xs {
		if s, ok := x.(string); ok && s == want {
			return true
		}
	}
	return false
}
