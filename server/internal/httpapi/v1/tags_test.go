package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// タグAPI（ApiDesign.md 9.11）の単体テスト。
//
// 認可（ticket.view / project.edit）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、入力の検証・
// 応答の形・404 と 409 の倒し方・並べ替えの受け取り方である。

const testTagID = "01K2TAG00000000000000001"

// tagReq は /projects/{key}/tags 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうためである（me_tokens_test と同じ）。
func tagReq(method, target, body, tagID string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	if tagID != "" {
		rc.URLParams.Add("id", tagID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

// tagFake はプロジェクト demo が解決できる状態のフェイクを返す。
func tagFake() *fakeQuerier {
	return &fakeQuerier{
		projectIDByKey: map[string]string{"demo": testProjectID},
		tagByID:        map[string]gen.GetTagByIDRow{},
	}
}

func decodeTag(t *testing.T, rec *httptest.ResponseRecorder) tagView {
	t.Helper()
	var v tagView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET（9.11）──────────────────────────────────────────────

func TestListTagsReturnsItemsWithTicketCount(t *testing.T) {
	q := tagFake()
	q.tagRows = []gen.ListTagsByProjectRow{
		{ID: "01K2TAG00000000000000001", Name: "設計", SortOrder: 10, TicketCount: 12},
		{ID: "01K2TAG00000000000000002", Name: "GUI", SortOrder: 20, TicketCount: 8},
		{ID: "01K2TAG00000000000000003", Name: "MCP", SortOrder: 30, TicketCount: 0},
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listTags(rec, tagReq(http.MethodGet, "/api/v1/projects/demo/tags", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body tagListView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("items = %d件, want 3", len(body.Items))
	}
	// 並びはクエリ（sort_order, name）のまま。ハンドラが並べ替えない。
	if body.Items[0].Name != "設計" || body.Items[2].Name != "MCP" {
		t.Errorf("並びがクエリの順と違う: %+v", body.Items)
	}
	// 0件でも ticket_count を返す（削除確認が「0件」を出せる必要がある）。
	if body.Items[2].TicketCount != 0 {
		t.Errorf("ticket_count = %d, want 0", body.Items[2].TicketCount)
	}
	// プロジェクトで絞っていること。
	if len(q.tagListProjectIDs) != 1 || q.tagListProjectIDs[0] != testProjectID {
		t.Errorf("project_id で絞っていない: %v", q.tagListProjectIDs)
	}
}

// **ページャを持たない**（9.11）。items 以外のキーが応答に出ないことを見る。
func TestListTagsHasNoPagination(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.listTags(rec, tagReq(http.MethodGet, "/api/v1/projects/demo/tags", "", ""))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("応答のキー = %v, want items のみ", keysOf(raw))
	}
	if _, ok := raw["items"]; !ok {
		t.Errorf("items が無い: %v", keysOf(raw))
	}
	// ETag も付けない（2.7 を適用しない）。
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("ETag = %q, want 空", etag)
	}
}

// 0件でも items は null ではなく空配列（2.6 の一覧応答の形）。
func TestListTagsReturnsEmptyArrayNotNull(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.listTags(rec, tagReq(http.MethodGet, "/api/v1/projects/demo/tags", "", ""))

	if got := rec.Body.String(); !strings.Contains(got, `"items":[]`) {
		t.Errorf("items が空配列でない: %s", got)
	}
}

// ── POST（9.11）─────────────────────────────────────────────

func TestCreateTagUsesNextSortOrderWhenOmitted(t *testing.T) {
	q := tagFake()
	q.nextTagSortOrder = 40
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags", `{"name":"設計"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdTags) != 1 {
		t.Fatalf("CreateTag = %d回, want 1", len(q.createdTags))
	}
	if got := q.createdTags[0].SortOrder; got != 40 {
		t.Errorf("sort_order = %d, want 40（末尾＝最大値+10）", got)
	}
	if got := q.createdTags[0].ProjectID; got != testProjectID {
		t.Errorf("project_id = %q, want %q", got, testProjectID)
	}
	// Location は作成先を指す（B-2）。
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/projects/demo/tags/") {
		t.Errorf("Location = %q", loc)
	}
	if body := decodeTag(t, rec); body.Name != "設計" {
		t.Errorf("応答の name = %q, want 設計", body.Name)
	}
}

func TestCreateTagHonorsExplicitSortOrder(t *testing.T) {
	q := tagFake()
	q.nextTagSortOrder = 40
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags",
		`{"name":"設計","sort_order":5}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.createdTags[0].SortOrder; got != 5 {
		t.Errorf("sort_order = %d, want 5（明示値を尊重する）", got)
	}
	// 明示されたら既定値を引きに行かない。
	for _, op := range q.opLog {
		if op == "NextTagSortOrder" {
			t.Errorf("sort_order が明示されているのに既定値を引いた: %v", q.opLog)
		}
	}
}

// **前後の空白を取り除いてから検証する**（B-6）。
func TestCreateTagTrimsName(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags",
		`{"name":"  設計  "}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.createdTags[0].Name; got != "設計" {
		t.Errorf("name = %q, want %q（トリム後の値を保存する）", got, "設計")
	}
}

func TestCreateTagRejectsInvalidName(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"空", `{"name":""}`, "required"},
		{"空白のみ", `{"name":"   "}`, "required"},
		{"欠落", `{}`, "required"},
		{"31文字", `{"name":"` + strings.Repeat("あ", 31) + `"}`, "too_long"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := tagFake()
			h := &handler{q: q}
			rec := httptest.NewRecorder()
			h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags", c.body, ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.wantCode) {
				t.Errorf("details に %q が無い: %s", c.wantCode, rec.Body.String())
			}
			if len(q.createdTags) != 0 {
				t.Errorf("検証に失敗したのに INSERT した")
			}
		})
	}
}

// ちょうど30文字は通る（境界の内側。DbDesign.md 6.10 の CHECK と同じ）。
func TestCreateTagAcceptsMaxLengthName(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags",
		`{"name":"`+strings.Repeat("あ", 30)+`"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（30文字は上限の内側）(%s)", rec.Code, rec.Body.String())
	}
}

// 一意制約違反は 409 already_exists（9.11）。
func TestCreateTagConflictOnDuplicateName(t *testing.T) {
	q := tagFake()
	q.createTagErr = &pgconn.PgError{Code: "23505", TableName: "tag",
		ConstraintName: "uq_tag_project_name"}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createTag(rec, tagReq(http.MethodPost, "/api/v1/projects/demo/tags", `{"name":"設計"}`, ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already_exists") {
		t.Errorf("code が already_exists でない: %s", rec.Body.String())
	}
}

// ── PATCH（9.11 / 9.11.1）───────────────────────────────────

func TestPatchTagRenames(t *testing.T) {
	q := tagFake()
	q.tagByID[testTagID] = gen.GetTagByIDRow{ID: testTagID, Name: "設計", SortOrder: 10, TicketCount: 12}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchTag(rec, tagReq(http.MethodPatch, "/api/v1/projects/demo/tags/"+testTagID,
		`{"name":"アーキテクチャ"}`, testTagID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := decodeTag(t, rec)
	if body.Name != "アーキテクチャ" {
		t.Errorf("name = %q, want アーキテクチャ", body.Name)
	}
	// 送っていない sort_order は据え置く。
	if !q.updatedTags[0].Name.Valid || q.updatedTags[0].SortOrder.Valid {
		t.Errorf("送られた項目だけを更新していない: %+v", q.updatedTags[0])
	}
}

// **並べ替えは sort_order の PATCH で表現する**（9.11.1）。専用の move を持たない。
func TestPatchTagReordersBySortOrder(t *testing.T) {
	q := tagFake()
	q.tagByID[testTagID] = gen.GetTagByIDRow{ID: testTagID, Name: "設計", SortOrder: 30}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchTag(rec, tagReq(http.MethodPatch, "/api/v1/projects/demo/tags/"+testTagID,
		`{"sort_order":10}`, testTagID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeTag(t, rec).SortOrder; got != 10 {
		t.Errorf("sort_order = %d, want 10", got)
	}
	if q.updatedTags[0].Name.Valid {
		t.Errorf("name を送っていないのに更新した: %+v", q.updatedTags[0])
	}
}

// 何も送られていない PATCH は現在の値をそのまま返す（users_update と同じ扱い）。
func TestPatchTagWithNoFieldsReturnsCurrent(t *testing.T) {
	q := tagFake()
	q.tagByID[testTagID] = gen.GetTagByIDRow{ID: testTagID, Name: "設計", SortOrder: 10, TicketCount: 3}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchTag(rec, tagReq(http.MethodPatch, "/api/v1/projects/demo/tags/"+testTagID, `{}`, testTagID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := decodeTag(t, rec)
	if body.Name != "設計" || body.SortOrder != 10 || body.TicketCount != 3 {
		t.Errorf("現在の値が返っていない: %+v", body)
	}
}

// 存在しない ID・他プロジェクトの ID はどちらも 404（Design.md 6.4.5）。
func TestPatchTagNotFound(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.patchTag(rec, tagReq(http.MethodPatch, "/api/v1/projects/demo/tags/01K2TAG00000000000000009",
		`{"name":"x"}`, "01K2TAG00000000000000009"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchTagRejectsEmptyName(t *testing.T) {
	q := tagFake()
	q.tagByID[testTagID] = gen.GetTagByIDRow{ID: testTagID, Name: "設計"}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchTag(rec, tagReq(http.MethodPatch, "/api/v1/projects/demo/tags/"+testTagID,
		`{"name":"  "}`, testTagID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedTags) != 0 {
		t.Errorf("検証に失敗したのに UPDATE した")
	}
}

// ── DELETE（9.11）───────────────────────────────────────────

// **使用中でも削除できる**（9.11）。件数は画面が確認ダイアログに出すだけ。
func TestDeleteTagSucceedsEvenWhenInUse(t *testing.T) {
	q := tagFake()
	q.tagByID[testTagID] = gen.GetTagByIDRow{ID: testTagID, Name: "設計", TicketCount: 12}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.deleteTag(rec, tagReq(http.MethodDelete, "/api/v1/projects/demo/tags/"+testTagID, "", testTagID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 に本文がある: %s", rec.Body.String())
	}
	if len(q.deletedTags) != 1 || q.deletedTags[0].ProjectID != testProjectID {
		t.Errorf("project_id で絞って削除していない: %+v", q.deletedTags)
	}
}

func TestDeleteTagNotFound(t *testing.T) {
	q := tagFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.deleteTag(rec, tagReq(http.MethodDelete, "/api/v1/projects/demo/tags/01K2TAG00000000000000009",
		"", "01K2TAG00000000000000009"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── プロジェクトの解決 ──────────────────────────────────────

// 認可の直後に消されたプロジェクトは 404 に倒す（projectScopeContext）。
func TestTagsProjectNotFound(t *testing.T) {
	q := &fakeQuerier{projectIDByKey: map[string]string{}}
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.listTags(rec, tagReq(http.MethodGet, "/api/v1/projects/demo/tags", "", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}
