package v1

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// tokenAs は指定したシステムロールで認証を通るトークンを仕込む。
func tokenAs(q *fakeQuerier, role string) string {
	token := validToken(q, `[]`)
	q.tokenRow.SystemRole = txt(role)
	q.profileRow.SystemRole = txt(role)
	return token
}

// projectFake は /projects を叩ける権限を持つ管理者のフェイクを返す。
//
// newFake の既定の権限割り当てを直接いじらないのは、そちらを変えると
// 権限の集合を検証している me_test の期待値まで動いてしまうためである。
func projectFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.permissions[auth.SystemRoleAdministrator] = []string{"project.view", "project.create"}
	q.permissions[auth.SystemRoleOperator] = []string{"project.view"}
	return q
}

var sampleProjectRows = []gen.ListProjectsRow{
	{
		ID:          "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
		Key:         "my-app",
		Name:        "社内タスク管理の刷新",
		Description: txt("既存のExcel管理を置き換える"),
		Status:      "active",
		UpdatedAt:   ts(time.Date(2026, 8, 11, 9, 12, 44, 0, time.UTC)),
		MyRole:      txt("project_admin"),
		TicketCount: 48,
		ClosedCount: 36,
		Progress:    0.75,
	},
	{
		// アドミニストレータには非メンバーのプロジェクトも返る（ApiDesign.md 5.1）。
		ID:          "01K2F8QW3H7YRJ4M5N6P7Q8R9T",
		Key:         "pb-server",
		Name:        "PB本体の実装",
		Description: pgNull(),
		Status:      "active",
		UpdatedAt:   ts(time.Date(2026, 8, 11, 8, 40, 0, 0, time.UTC)),
		MyRole:      pgNull(),
		TicketCount: 0,
		ClosedCount: 0,
		Progress:    0,
	},
}

// 一覧は 2.6 のエンベロープと 5.1 の items[] を返す。
func TestListProjectsReturnsEnvelope(t *testing.T) {
	q := projectFake(t)
	q.projectRows = sampleProjectRows
	q.projectSummary = gen.SummarizeProjectsRow{
		Total:         2,
		LastUpdatedAt: ts(time.Date(2026, 8, 11, 9, 12, 44, 0, time.UTC)),
	}
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet, "/api/v1/projects", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	view := viewOf(t, rec)
	for key, want := range map[string]float64{
		"page": 1, "per_page": 25, "total": 2, "total_pages": 1,
	} {
		if got, _ := view[key].(float64); got != want {
			t.Errorf("%s = %v, want %v", key, view[key], want)
		}
	}

	items, ok := view["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2件", view["items"])
	}

	first, _ := items[0].(map[string]any)
	if first["key"] != "my-app" {
		t.Errorf("items[0].key = %v, want my-app", first["key"])
	}
	if got, _ := first["progress"].(float64); got != 0.75 {
		t.Errorf("items[0].progress = %v, want 0.75", first["progress"])
	}
	if got, _ := first["ticket_count"].(float64); got != 48 {
		t.Errorf("items[0].ticket_count = %v, want 48", first["ticket_count"])
	}
	if first["my_role"] != "project_admin" {
		t.Errorf("items[0].my_role = %v, want project_admin", first["my_role"])
	}
	if first["updated_at"] != "2026-08-11T09:12:44Z" {
		t.Errorf("items[0].updated_at = %v, want ISO8601 UTC", first["updated_at"])
	}

	// 非メンバーのアドミニストレータには my_role が無い。**フィールドは省略しない。**
	second, _ := items[1].(map[string]any)
	if v, ok := second["my_role"]; !ok || v != nil {
		t.Errorf("items[1].my_role = %v（present=%v）, want null", v, ok)
	}
	if v, ok := second["description"]; !ok || v != nil {
		t.Errorf("items[1].description = %v（present=%v）, want null", v, ok)
	}
	// ticket_count = 0 のとき progress は null ではなく 0（ApiDesign.md 5.1）。
	if v, ok := second["progress"].(float64); !ok || v != 0 {
		t.Errorf("items[1].progress = %v, want 0", second["progress"])
	}
}

// 可視範囲はメンバーシップで絞る。管理者だけが全件を見る（ApiDesign.md 5.1）。
func TestListProjectsPassesVisibilityToQuery(t *testing.T) {
	for _, tc := range []struct {
		role      string
		wantAdmin bool
	}{
		{auth.SystemRoleAdministrator, true},
		{auth.SystemRoleOperator, false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			q := projectFake(t)
			token := tokenAs(q, tc.role)

			if rec := authed(q, http.MethodGet, "/api/v1/projects", token); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
			}
			if len(q.listParams) != 1 {
				t.Fatalf("ListProjects の呼び出し回数 = %d, want 1", len(q.listParams))
			}
			got := q.listParams[0]
			if got.IsAdministrator != tc.wantAdmin {
				t.Errorf("is_administrator = %v, want %v", got.IsAdministrator, tc.wantAdmin)
			}
			if got.ActorID != testActorID {
				t.Errorf("actor_id = %q, want %q", got.ActorID, testActorID)
			}
		})
	}
}

// 未指定時の既定は status=active・updated_at の降順（ApiDesign.md 5.1 / 2.6）。
func TestListProjectsDefaults(t *testing.T) {
	q := projectFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	if rec := authed(q, http.MethodGet, "/api/v1/projects", token); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := q.listParams[0]
	if got.StatusFilter != projectStatusActive {
		t.Errorf("status = %q, want active", got.StatusFilter)
	}
	if got.Sort != "updated_at" || got.SortOrder != OrderDesc {
		t.Errorf("sort/order = %q/%q, want updated_at/desc", got.Sort, got.SortOrder)
	}
	if got.PageLimit != DefaultPerPage || got.PageOffset != 0 {
		t.Errorf("limit/offset = %d/%d, want %d/0", got.PageLimit, got.PageOffset, DefaultPerPage)
	}
	// 総件数と一覧は同じ絞り込みで引く。片方だけ条件が違うと total が合わない。
	if q.summaryParams[0].StatusFilter != got.StatusFilter ||
		q.summaryParams[0].IsAdministrator != got.IsAdministrator {
		t.Errorf("総件数の絞り込みが一覧と違う: %+v / %+v", q.summaryParams[0], got)
	}
}

// クエリはそのままSQLへ渡る。ページは LIMIT / OFFSET に写る（2.6）。
func TestListProjectsAppliesQueryParameters(t *testing.T) {
	q := projectFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet,
		"/api/v1/projects?status=all&sort=ticket_count&order=asc&page=3&per_page=10", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	got := q.listParams[0]
	if got.StatusFilter != projectStatusAll {
		t.Errorf("status = %q, want all", got.StatusFilter)
	}
	if got.Sort != "ticket_count" || got.SortOrder != OrderAsc {
		t.Errorf("sort/order = %q/%q, want ticket_count/asc", got.Sort, got.SortOrder)
	}
	if got.PageLimit != 10 || got.PageOffset != 20 {
		t.Errorf("limit/offset = %d/%d, want 10/20", got.PageLimit, got.PageOffset)
	}
}

// **範囲外・解釈不能な値は既定へ丸めず 422**（ApiDesign.md 2.6）。
// status も同じ方針で扱い、details は項目ごとに並べる。
func TestListProjectsRejectsInvalidQuery(t *testing.T) {
	q := projectFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet, "/api/v1/projects?status=deleted&per_page=201", token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", e.Code)
	}

	fields := map[string]bool{}
	for _, d := range e.Details {
		fields[d.Field] = true
	}
	// **先に見つかったほうだけを返さない。** フォームの各欄に紐づけるため
	// 両方の誤りが details に載る必要がある（2.5）。
	if !fields["status"] || !fields["per_page"] {
		t.Errorf("details のフィールド = %v, want status と per_page の両方", fields)
	}
	if len(q.listParams) != 0 {
		t.Error("入力が不正なのにDBを引いた")
	}
}

// 一覧系 GET は ETag を返す（ApiDesign.md 2.7）。
func TestListProjectsSetsETag(t *testing.T) {
	q := projectFake(t)
	last := time.Date(2026, 8, 11, 9, 12, 44, 0, time.UTC)
	q.projectSummary = gen.SummarizeProjectsRow{Total: 3, LastUpdatedAt: ts(last)}
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet, "/api/v1/projects", token)
	got := rec.Header().Get("ETag")
	want := fmt.Sprintf(`W/"proj-3-%d"`, last.UnixNano())
	if got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}

	// 件数か最終更新のどちらかが動けば値が変わること（2.7 の材料はこの2つ）。
	if same := projectsETag(3, ts(last.Add(time.Millisecond))); same == got {
		t.Error("最終更新が変わっても ETag が同じ値になった")
	}
	if same := projectsETag(4, ts(last)); same == got {
		t.Error("件数が変わっても ETag が同じ値になった")
	}
}

// 0件でも ETag を返す（MAX(updated_at) が NULL になる）。
func TestProjectsETagWithNoRows(t *testing.T) {
	if got := projectsETag(0, pgtype.Timestamptz{}); got != `W/"proj-0-0"` {
		t.Errorf("ETag = %q, want W/\"proj-0-0\"", got)
	}
}

// 必要権限はルート定義で宣言する（Design.md 6.4.4）。
func TestProjectRoutesRequirePermissions(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		// grant は当該ロールに与える権限。要求される権限を外して 403 を確かめる。
		grant []string
	}{
		{"一覧は project.view", "/api/v1/projects", []string{"project.create"}},
		{"check-key は project.create", "/api/v1/projects/check-key?key=my-app", []string{"project.view"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := projectFake(t)
			q.permissions[auth.SystemRoleOperator] = tc.grant
			token := tokenAs(q, auth.SystemRoleOperator)

			rec := authed(q, http.MethodGet, tc.path, token)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
			}
			if got := errorOf(t, rec).Code; got != "forbidden" {
				t.Errorf("code = %q, want forbidden", got)
			}
			// 拒否は監査に残す（Design.md 6.4.5）。
			if got := q.auditActions(); len(got) == 0 || got[len(got)-1] != "permission.denied" {
				t.Errorf("監査アクション = %v, want 末尾が permission.denied", got)
			}
		})
	}
}

// check-key の判定（ApiDesign.md 5.2）。**使えない場合も 200 で返す。**
func TestCheckProjectKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key       string
		exists    bool
		available bool
		reason    string
		// queried は DB を引いたかどうか。形式・予約語で落ちる場合は引かない。
		queried bool
	}{
		{"使用可能", "my-app", false, true, "", true},
		{"既に存在する", "my-app", true, false, keyReasonAlreadyExists, true},
		{"予約語", "admin", false, false, keyReasonReserved, false},
		{"大文字とアンダースコア", "My_App", false, false, keyReasonInvalidFormat, false},
		{"1文字は短すぎる", "a", false, false, keyReasonInvalidFormat, false},
		{"21文字は長すぎる", "abcdefghijklmnopqrstu", false, false, keyReasonInvalidFormat, false},
		{"ハイフン始まりは不可", "-app", false, false, keyReasonInvalidFormat, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := projectFake(t)
			q.keyExists = tc.exists
			token := tokenAs(q, auth.SystemRoleAdministrator)

			rec := authed(q, http.MethodGet, "/api/v1/projects/check-key?key="+tc.key, token)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
			}

			view := viewOf(t, rec)
			if view["key"] != tc.key {
				t.Errorf("key = %v, want %q", view["key"], tc.key)
			}
			if got, _ := view["available"].(bool); got != tc.available {
				t.Errorf("available = %v, want %v", view["available"], tc.available)
			}
			if tc.reason == "" {
				if _, ok := view["reason"]; ok {
					t.Errorf("available なのに reason = %v が出た", view["reason"])
				}
			} else if view["reason"] != tc.reason {
				t.Errorf("reason = %v, want %q", view["reason"], tc.reason)
			}
			if got := len(q.keyChecked) > 0; got != tc.queried {
				t.Errorf("DBを引いたか = %v, want %v", got, tc.queried)
			}
		})
	}
}

// key が無ければ 422（判定のしようがないため）。
func TestCheckProjectKeyRequiresKey(t *testing.T) {
	q := projectFake(t)
	token := tokenAs(q, auth.SystemRoleAdministrator)

	rec := authed(q, http.MethodGet, "/api/v1/projects/check-key", token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if len(e.Details) != 1 || e.Details[0].Field != "key" {
		t.Errorf("details = %+v, want key の1件", e.Details)
	}
}

// 未認証では 401（認証必須グループの中に置いてあること）。
func TestProjectRoutesRequireAuthentication(t *testing.T) {
	for _, path := range []string{"/api/v1/projects", "/api/v1/projects/check-key?key=my-app"} {
		q := projectFake(t)
		rec := call(router(q), http.MethodGet, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401（body=%s）", path, rec.Code, rec.Body.String())
		}
	}
}
