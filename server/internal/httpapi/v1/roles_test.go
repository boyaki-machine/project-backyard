package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ── GET /roles（ApiDesign.md 7.1）───────────────────────────────
//
// 認可は middleware.RequirePermissionUnlessQuery の責務なのでここでは通さない
// （middleware/authz_test.go と routes_test.go が別に見ている）。ここで確かめる
// のは scope の解釈・応答の形・畳み方・並びである。

func catalogHandler(q *fakeQuerier) *handler { return &handler{q: q} }

// seedRoles は DbDesign.md 7.3 のシードを縮めたもの。sort_order は実物と同じ。
func seedRoles() []gen.Role {
	return []gen.Role{
		{Key: "operator", Scope: "system", DisplayName: "オペレータ",
			Description: txt("プロジェクトとチケットの閲覧・編集ができます"), IsBuiltin: true, SortOrder: 10},
		{Key: "administrator", Scope: "system", DisplayName: "アドミニストレータ",
			Description: txt("ユーザー管理・システム設定を含む全操作ができます"), IsBuiltin: true, SortOrder: 20},
		{Key: "project_admin", Scope: "project", DisplayName: "プロジェクト管理者",
			Description: txt("当該プロジェクトの全操作と承認ができます"), IsBuiltin: true, SortOrder: 30},
	}
}

type roleBody struct {
	Items []struct {
		Key         string   `json:"key"`
		Scope       string   `json:"scope"`
		DisplayName string   `json:"display_name"`
		Description *string  `json:"description"`
		IsBuiltin   bool     `json:"is_builtin"`
		SortOrder   int32    `json:"sort_order"`
		Permissions []string `json:"permissions"`
	} `json:"items"`
}

func getRoles(t *testing.T, q *fakeQuerier, query string) (*httptest.ResponseRecorder, roleBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	catalogHandler(q).listRoles(rec, httptest.NewRequest(http.MethodGet, "/api/v1/roles"+query, nil))
	var body roleBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, body
}

func TestListRolesFoldsPermissionsByRole(t *testing.T) {
	q := &fakeQuerier{
		catalogRoles: seedRoles(),
		// クエリは permission.sort_order の順で返す。ハンドラは順序を保って畳む。
		catalogAssignments: []gen.RolePermission{
			{RoleKey: "operator", PermissionKey: "project.view"},
			{RoleKey: "operator", PermissionKey: "ticket.view"},
			{RoleKey: "administrator", PermissionKey: "project.view"},
			{RoleKey: "administrator", PermissionKey: "user.manage"},
		},
	}
	rec, body := getRoles(t, q, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状態コード = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(body.Items) != 3 {
		t.Fatalf("items = %d件, want 3", len(body.Items))
	}

	// 並びはクエリ（role.sort_order）のまま。ハンドラが並べ替えない。
	wantOrder := []string{"operator", "administrator", "project_admin"}
	for i, want := range wantOrder {
		if body.Items[i].Key != want {
			t.Errorf("items[%d].key = %q, want %q", i, body.Items[i].Key, want)
		}
	}

	// permissions は role_key で畳まれ、クエリの順序が保たれる。
	got := body.Items[0].Permissions
	if len(got) != 2 || got[0] != "project.view" || got[1] != "ticket.view" {
		t.Errorf("operator の permissions = %v, want [project.view ticket.view]", got)
	}

	// **権限を1件も持たないロールは [] であって null ではない。**
	// null にすると、画面が「未取得」と「0件」を区別できない。
	if body.Items[2].Permissions == nil {
		t.Error("project_admin の permissions が null。0件でも [] を返すこと")
	}
	if len(body.Items[2].Permissions) != 0 {
		t.Errorf("project_admin の permissions = %v, want []", body.Items[2].Permissions)
	}
}

func TestListRolesReturnsCatalogFields(t *testing.T) {
	q := &fakeQuerier{catalogRoles: seedRoles()}
	_, body := getRoles(t, q, "")

	it := body.Items[0]
	if it.Scope != "system" || it.DisplayName != "オペレータ" || !it.IsBuiltin || it.SortOrder != 10 {
		t.Errorf("operator = %+v, want scope=system display_name=オペレータ is_builtin=true sort_order=10", it)
	}
	// description は NULL 許容だが、キー自体は常に返す（6.1 と同じ方針）。
	if it.Description == nil {
		t.Fatal("description が null。シードは説明を持つ")
	}
	if *it.Description != "プロジェクトとチケットの閲覧・編集ができます" {
		t.Errorf("description = %q", *it.Description)
	}
}

// **ページネーションも ETag も持たない**（7.1）。件数がシードで固定のため。
func TestListRolesHasNoPagingEnvelopeAndNoETag(t *testing.T) {
	q := &fakeQuerier{catalogRoles: seedRoles()}
	rec, _ := getRoles(t, q, "")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(raw) != 1 {
		t.Errorf("応答のキー = %v, want items のみ", keysOf(raw))
	}
	if _, ok := raw["items"]; !ok {
		t.Errorf("items が無い: %v", keysOf(raw))
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("ETag = %q, want 無し", etag)
	}
}

// scope はクエリへそのまま渡り、**2本のクエリが同じ値で呼ばれる**。
func TestListRolesPassesScopeToBothQueries(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"", "all"},
		{"?scope=system", "system"},
		{"?scope=project", "project"},
	} {
		q := &fakeQuerier{catalogRoles: seedRoles()}
		rec, _ := getRoles(t, q, tc.query)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: 状態コード = %d, want 200", tc.query, rec.Code)
		}
		if len(q.catalogScopes) != 2 {
			t.Fatalf("%q: クエリの呼び出し = %d回, want 2回", tc.query, len(q.catalogScopes))
		}
		for i, got := range q.catalogScopes {
			if got != tc.want {
				t.Errorf("%q: %d本目の scope = %q, want %q", tc.query, i+1, got, tc.want)
			}
		}
	}
}

// 解釈できない値は既定へ丸めず 422（2.6 の方針。parseUserKindFilter と同じ）。
func TestListRolesRejectsUnknownScope(t *testing.T) {
	// プロジェクトIDやユーザIDを scope に入れる案は 7.1 で明示的に退けている。
	// 値は URL へ埋める前にエスケープする。生の空白を httptest.NewRequest へ
	// 渡すと URL の解析に失敗して panic する（測りたいのは 422 であってそこではない）。
	// **`all` も弾く。** 7.1 の値域は system / project の2つで、全件は「未指定」で
	// 表す。6.1 の kind が all を明示的な値として受けるのとは違う。
	for _, bad := range []string{"all", "SYSTEM", "projects", "01K2F8QW3H7YRJ4M5N6P7Q8PRJ", " "} {
		q := &fakeQuerier{catalogRoles: seedRoles()}
		rec, _ := getRoles(t, q, "?scope="+url.QueryEscape(bad))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("scope=%q: 状態コード = %d, want 422", bad, rec.Code)
		}
		if len(q.catalogScopes) != 0 {
			t.Errorf("scope=%q: 検証に落ちたのにクエリを呼んでいる", bad)
		}
	}
}

func TestListRolesSurfacesQueryFailure(t *testing.T) {
	q := &fakeQuerier{catalogErr: errors.New("接続断")}
	rec, _ := getRoles(t, q, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("状態コード = %d, want 500", rec.Code)
	}
}

// ── GET /permissions（ApiDesign.md 7.2）─────────────────────────

func TestListPermissionsReturnsCatalog(t *testing.T) {
	q := &fakeQuerier{catalogPerms: []gen.Permission{
		{Key: "project.view", Category: "project", Description: "プロジェクトの閲覧", SortOrder: 10},
		{Key: "user.manage", Category: "admin", Description: "ユーザーの管理", SortOrder: 70},
	}}
	rec := httptest.NewRecorder()
	catalogHandler(q).listPermissions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/permissions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状態コード = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []permissionItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d件, want 2", len(body.Items))
	}
	// category は 5.6.3 の行の区切りに使う。description は権限列の副見出し。
	if body.Items[1].Category != "admin" || body.Items[1].Description != "ユーザーの管理" {
		t.Errorf("items[1] = %+v", body.Items[1])
	}
	if body.Items[1].SortOrder != 70 {
		t.Errorf("sort_order = %d, want 70", body.Items[1].SortOrder)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("ETag = %q, want 無し", etag)
	}
}

// 空でも null ではなく [] を返す（items が nil になると画面が落ちる）。
func TestListPermissionsReturnsEmptyArrayNotNull(t *testing.T) {
	rec := httptest.NewRecorder()
	catalogHandler(&fakeQuerier{}).listPermissions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/permissions", nil))
	if body := rec.Body.String(); !jsonHasEmptyItems(body) {
		t.Errorf("応答 = %s, want {\"items\":[]}", body)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func jsonHasEmptyItems(s string) bool {
	var body struct {
		Items *[]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(s), &body); err != nil {
		return false
	}
	return body.Items != nil && len(*body.Items) == 0
}
