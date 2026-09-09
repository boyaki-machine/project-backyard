package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 文書API（ApiDesign.md 10章）の単体テスト。
//
// 認可（doc.view / doc.edit）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、木の組み立てと path、
// 入力の検証、応答の形、404 / 405 / 409 の倒し方、リビジョンを作る条件である。

const (
	testDocVisionID = "01K2DOC0000000000000VISION"
	testDocRulesID  = "01K2DOC00000000000000RULES"
	testDocNamingID = "01K2DOC0000000000000NAMING"
)

var testDocTime = time.Date(2026, 8, 29, 5, 0, 0, 0, time.UTC)

// docReq は /projects/{key}/docs 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを通らず、
// chi.URLParam が空を返してしまうためである（tags_test と同じ）。
// wildcard には * を入れる（ルート定義が /docs/* であるため）。
func docReq(method, target, body, wildcard string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	if wildcard != "" {
		rc.URLParams.Add("*", wildcard)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

// docFake はプロジェクト demo に vision / rules / rules>naming がある状態を作る。
//
// **rules を vision より後ろの sort_order に置く**（10 と 20）。目次の並びが
// sort_order で決まっていることを測るためで、ULID の順とは逆にしてある。
func docFake() *fakeQuerier {
	q := &fakeQuerier{projectIDByKey: map[string]string{"demo": testProjectID}}
	q.docs = docFakeState{
		tree: []gen.ListDocumentTreeRow{
			{ID: testDocRulesID, Slug: "rules", Title: "規約", SortOrder: 20, Version: 3,
				CreatedAt: ts(testDocTime), UpdatedAt: ts(testDocTime)},
			{ID: testDocVisionID, Slug: "vision", Title: "価値観・世界観", SortOrder: 10, Version: 1,
				CreatedAt: ts(testDocTime), UpdatedAt: ts(testDocTime)},
			{ID: testDocNamingID, ParentID: txt(testDocRulesID), Slug: "naming", Title: "命名",
				SortOrder: 10, Version: 1, CreatedAt: ts(testDocTime), UpdatedAt: ts(testDocTime)},
		},
		bodies: map[string]string{
			testDocVisionID: "価値観の本文。\n",
			testDocRulesID:  "本書は規約である。\n\n## 命名\n\n- 単数形\n\n## ブランチ\n\n- main へ直接コミットしない\n",
			testDocNamingID: "命名の本文。\n",
		},
		byID: map[string]gen.GetDocumentRow{
			testDocRulesID: {
				ID: testDocRulesID, Slug: "rules", Title: "規約",
				BodyMd:    "本書は規約である。\n\n## 命名\n\n- 単数形\n\n## ブランチ\n\n- main へ直接コミットしない\n",
				SortOrder: 20, Version: 3,
				CreatedBy: txt(testActorID), CreatedByKind: txt("user"), CreatedByName: txt("田中"),
				CreatedAt: ts(testDocTime), UpdatedAt: ts(testDocTime),
			},
			testDocNamingID: {
				ID: testDocNamingID, ParentID: txt(testDocRulesID), Slug: "naming", Title: "命名",
				BodyMd: "命名の本文。\n", SortOrder: 10, Version: 1,
				CreatedAt: ts(testDocTime), UpdatedAt: ts(testDocTime),
			},
		},
		nextSortOrder:  30,
		nextRevisionNo: 4,
		updateRows:     1,
		deleteRows:     1,
		revisionByNo:   map[int32]gen.GetDocumentRevisionRow{},
	}
	return q
}

// ── 応答を読むための写し ─────────────────────────────────────
//
// **Time は MarshalJSON だけを持つ**（apitime.go）ので、応答の型そのままでは
// 読み戻せない。時刻は文字列で受ける——2.2 の「ISO8601 UTC・秒精度」を測るには
// そのほうが直接である。

type docTreeItemJSON struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Slug      string            `json:"slug"`
	Title     string            `json:"title"`
	SortOrder int32             `json:"sort_order"`
	Version   int32             `json:"version"`
	UpdatedAt string            `json:"updated_at"`
	Outline   *[]docOutlineItem `json:"outline"`
	Children  []docTreeItemJSON `json:"children"`
}

type docTreeListJSON struct {
	Items []docTreeItemJSON `json:"items"`
}

type docViewJSON struct {
	ID         string           `json:"id"`
	Path       string           `json:"path"`
	Slug       string           `json:"slug"`
	ParentPath *string          `json:"parent_path"`
	Title      string           `json:"title"`
	BodyMd     string           `json:"body_md"`
	Outline    []docOutlineItem `json:"outline"`
	SortOrder  int32            `json:"sort_order"`
	Version    int32            `json:"version"`
	CreatedBy  *actorRef        `json:"created_by"`
	UpdatedBy  *actorRef        `json:"updated_by"`
	CreatedAt  string           `json:"created_at"`
	UpdatedAt  string           `json:"updated_at"`
}

type docSectionJSON struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Section   string `json:"section"`
	BodyMd    string `json:"body_md"`
	Version   int32  `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

type docRevisionItemJSON struct {
	RevisionNo   int32     `json:"revision_no"`
	Title        string    `json:"title"`
	ChangedBy    *actorRef `json:"changed_by"`
	ChangeReason *string   `json:"change_reason"`
	CreatedAt    string    `json:"created_at"`
}

type docRevisionJSON struct {
	RevisionNo   int32     `json:"revision_no"`
	Title        string    `json:"title"`
	BodyMd       string    `json:"body_md"`
	ChangedBy    *actorRef `json:"changed_by"`
	ChangeReason *string   `json:"change_reason"`
	CreatedAt    string    `json:"created_at"`
}

func docHandler(q *fakeQuerier) *handler {
	return &handler{q: q, tx: &fakeTxRunner{q: q}}
}

// ── 目次（10.2）────────────────────────────────────────────

func TestListDocsBuildsTree(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).listDocs(rec, docReq(http.MethodGet, "/projects/demo/docs", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got docTreeListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}

	// sort_order 昇順（vision=10 → rules=20）。ULID の順は逆である。
	if len(got.Items) != 2 {
		t.Fatalf("トップレベル = %d件, want 2", len(got.Items))
	}
	if got.Items[0].Path != "vision" || got.Items[1].Path != "rules" {
		t.Errorf("並び = %s, %s。want vision, rules（sort_order 昇順）",
			got.Items[0].Path, got.Items[1].Path)
	}
	if len(got.Items[0].Children) != 0 {
		t.Errorf("vision の子 = %d件, want 0", len(got.Items[0].Children))
	}
	// path は slug を根から連ねたもの（10.1）。
	if len(got.Items[1].Children) != 1 || got.Items[1].Children[0].Path != "rules/naming" {
		t.Errorf("rules の子 = %+v, want path=rules/naming", got.Items[1].Children)
	}
	// version は木のドラッグ&ドロップが If-Match に使う（10.2）。
	if got.Items[1].Version != 3 {
		t.Errorf("rules の version = %d, want 3", got.Items[1].Version)
	}
	// ?outline=1 を付けていないので outline は出ない。
	if got.Items[1].Outline != nil {
		t.Errorf("outline = %v, want 省略（?outline=1 が無い）", got.Items[1].Outline)
	}
	// 目次は本文を読まない（10.2）。
	for _, op := range q.opLog {
		if op == "ListDocumentBodies" {
			t.Error("?outline=1 が無いのに本文を読んでいる")
		}
	}
}

func TestListDocsWithOutline(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).listDocs(rec, docReq(http.MethodGet, "/projects/demo/docs?outline=1", "", ""))

	var got docTreeListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	rules := got.Items[1]
	want := []docOutlineItem{{"命名", 2}, {"ブランチ", 2}}
	if rules.Outline == nil || len(*rules.Outline) != len(want) {
		t.Fatalf("rules の outline = %v, want %v", rules.Outline, want)
	}
	for i := range want {
		if (*rules.Outline)[i] != want[i] {
			t.Errorf("outline[%d] = %+v, want %+v", i, (*rules.Outline)[i], want[i])
		}
	}
	// **見出しの無い文書でも outline: [] を返す**（「章が無い」と「調べていない」を
	// 分ける）。値型 + omitempty ではキーごと消えるため、ここは nil でないことまで見る。
	if got.Items[0].Outline == nil {
		t.Error("vision に outline のキーが無い。?outline=1 を付けたのに省略されている")
	} else if len(*got.Items[0].Outline) != 0 {
		t.Errorf("vision の outline = %v, want []", *got.Items[0].Outline)
	}
}

// ── 本文（10.3）────────────────────────────────────────────

func TestGetDocReturnsBodyAndOutline(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec, docReq(http.MethodGet, "/projects/demo/docs/rules", "", "rules"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got docViewJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	if got.Path != "rules" || got.Slug != "rules" {
		t.Errorf("path/slug = %s/%s, want rules/rules", got.Path, got.Slug)
	}
	// トップレベルの parent_path は null（10.3）。
	if got.ParentPath != nil {
		t.Errorf("parent_path = %v, want null", *got.ParentPath)
	}
	if len(got.Outline) != 2 {
		t.Errorf("outline = %v, want 2件", got.Outline)
	}
	if got.CreatedBy == nil || got.CreatedBy.DisplayName != "田中" {
		t.Errorf("created_by = %+v, want 田中", got.CreatedBy)
	}
	// updated_by は NULL になりうる（ON DELETE SET NULL。10.3）。
	if got.UpdatedBy != nil {
		t.Errorf("updated_by = %+v, want null", got.UpdatedBy)
	}
}

func TestGetDocNestedParentPath(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec,
		docReq(http.MethodGet, "/projects/demo/docs/rules/naming", "", "rules/naming"))

	var got docViewJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	if got.Path != "rules/naming" {
		t.Errorf("path = %s, want rules/naming", got.Path)
	}
	if got.ParentPath == nil || *got.ParentPath != "rules" {
		t.Errorf("parent_path = %v, want rules", got.ParentPath)
	}
}

func TestGetDocNotFound(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec, docReq(http.MethodGet, "/projects/demo/docs/nope", "", "nope"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ?section= が命中しないときは available_sections を添える（10.3）。
func TestGetDocSection(t *testing.T) {
	t.Run("命中", func(t *testing.T) {
		q := docFake()
		rec := httptest.NewRecorder()
		docHandler(q).getDoc(rec,
			docReq(http.MethodGet, "/projects/demo/docs/rules?section=%E5%91%BD%E5%90%8D", "", "rules"))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		var got docSectionJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
		}
		if got.Section != "命名" {
			t.Errorf("section = %q, want 命名", got.Section)
		}
		if want := "## 命名\n\n- 単数形"; got.BodyMd != want {
			t.Errorf("body_md = %q, want %q", got.BodyMd, want)
		}
	})

	t.Run("不命中", func(t *testing.T) {
		q := docFake()
		rec := httptest.NewRecorder()
		docHandler(q).getDoc(rec,
			docReq(http.MethodGet, "/projects/demo/docs/rules?section=nope", "", "rules"))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Error struct {
				Code              string   `json:"code"`
				AvailableSections []string `json:"available_sections"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
		}
		if body.Error.Code != "not_found" {
			t.Errorf("code = %s, want not_found", body.Error.Code)
		}
		want := []string{"命名", "ブランチ"}
		if len(body.Error.AvailableSections) != len(want) {
			t.Fatalf("available_sections = %v, want %v", body.Error.AvailableSections, want)
		}
		for i := range want {
			if body.Error.AvailableSections[i] != want[i] {
				t.Errorf("available_sections[%d] = %s, want %s",
					i, body.Error.AvailableSections[i], want[i])
			}
		}
	})
}

// ── 作成（10.4）────────────────────────────────────────────

// POST は revision_no = 1 を同時に作る（10.4）。
func TestCreateDocMakesFirstRevision(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	body := `{"slug":"decisions","title":"判断の記録","body_md":"本文"}`
	docHandler(q).createDoc(rec, docReq(http.MethodPost, "/projects/demo/docs", body, ""))

	if len(q.docs.created) != 1 {
		t.Fatalf("作成 = %d件, want 1 (%s)", len(q.docs.created), rec.Body.String())
	}
	created := q.docs.created[0]
	if created.Slug != "decisions" || created.Title != "判断の記録" {
		t.Errorf("作成内容 = %+v", created)
	}
	// sort_order 省略時は同じ親の中の末尾（10.4）。
	if created.SortOrder != 30 {
		t.Errorf("sort_order = %d, want 30（最大値 + 10）", created.SortOrder)
	}
	if created.ParentID.Valid {
		t.Errorf("parent_id = %v, want NULL（parent_path 省略）", created.ParentID)
	}

	if len(q.docs.revisionsMade) != 1 {
		t.Fatalf("リビジョン = %d件, want 1", len(q.docs.revisionsMade))
	}
	rev := q.docs.revisionsMade[0]
	if rev.RevisionNo != 1 {
		t.Errorf("revision_no = %d, want 1", rev.RevisionNo)
	}
	if rev.BodyMd != "本文" || rev.Title != "判断の記録" {
		t.Errorf("リビジョンの中身 = %+v, want 作成時の本文", rev)
	}
	if rev.ChangeReason.Valid {
		t.Errorf("change_reason = %v, want NULL", rev.ChangeReason)
	}
	if rev.DocumentID != created.ID {
		t.Errorf("リビジョンが別の文書を指している: %s != %s", rev.DocumentID, created.ID)
	}
}

func TestCreateDocValidatesSlugAndTitle(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{"slug が空", `{"slug":"","title":"題"}`, "slug", "required"},
		{"slug に _ は使えない", `{"slug":"_revisions","title":"題"}`, "slug", "invalid"},
		{"slug が大文字", `{"slug":"Rules","title":"題"}`, "slug", "invalid"},
		{"title が空", `{"slug":"ok","title":"   "}`, "title", "required"},
		{"title が201文字", `{"slug":"ok","title":"` + strings.Repeat("あ", 201) + `"}`, "title", "too_long"},
		{"parent_path が無い", `{"slug":"ok","title":"題","parent_path":"nope"}`, "parent_path", "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := docFake()
			rec := httptest.NewRecorder()
			docHandler(q).createDoc(rec, docReq(http.MethodPost, "/projects/demo/docs", tt.body, ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), tt.field, tt.code) {
				t.Errorf("details に %s/%s が無い: %s", tt.field, tt.code, rec.Body.String())
			}
			if len(q.docs.created) != 0 {
				t.Error("検証に落ちたのに INSERT している")
			}
		})
	}
}

// 同じ親の下に同じ slug は 409 already_exists（conflict ではない。2.5.1 / 10.6）。
func TestCreateDocDuplicateSlug(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	body := `{"slug":"rules","title":"別の規約"}`
	docHandler(q).createDoc(rec, docReq(http.MethodPost, "/projects/demo/docs", body, ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if code := errorOf(t, rec).Code; code != "already_exists" {
		t.Errorf("code = %s, want already_exists", code)
	}
}

// ── 更新（10.4）────────────────────────────────────────────

func TestPatchDocRequiresIfMatch(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules", `{"title":"新"}`, "rules")
	docHandler(q).patchDoc(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "If-Match", "required") {
		t.Errorf("details に If-Match/required が無い: %s", rec.Body.String())
	}
}

func TestPatchDocVersionConflict(t *testing.T) {
	q := docFake()
	q.docs.updateRows = 0 // WHERE version = ? に当たらなかった
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules", `{"title":"新"}`, "rules")
	req.Header.Set("If-Match", `"2"`)
	docHandler(q).patchDoc(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if code := errorOf(t, rec).Code; code != "conflict" {
		t.Errorf("code = %s, want conflict", code)
	}
	if len(q.docs.revisionsMade) != 0 {
		t.Error("競合したのにリビジョンを積んでいる")
	}
}

// リビジョンを作るのは title か body_md が実際に変わったときだけ（10.4）。
func TestPatchDocRevisionCondition(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantRev bool
	}{
		{"本文が変わった", `{"body_md":"新しい本文"}`, true},
		{"タイトルが変わった", `{"title":"新しい題"}`, true},
		{"同じ本文の送り直し", `{"body_md":"本書は規約である。\n\n## 命名\n\n- 単数形\n\n## ブランチ\n\n- main へ直接コミットしない\n"}`, false},
		{"sort_order だけ", `{"sort_order":50}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := docFake()
			rec := httptest.NewRecorder()
			req := docReq(http.MethodPatch, "/projects/demo/docs/rules", tt.body, "rules")
			req.Header.Set("If-Match", `"3"`)
			docHandler(q).patchDoc(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			got := len(q.docs.revisionsMade) > 0
			if got != tt.wantRev {
				t.Errorf("リビジョンを作った = %v, want %v", got, tt.wantRev)
			}
			// version はどの更新でも +1 する（クエリが version + 1 を書く。10.4）。
			if len(q.docs.updated) != 1 {
				t.Fatalf("UPDATE = %d件, want 1", len(q.docs.updated))
			}
			if q.docs.updated[0].Version != 3 {
				t.Errorf("If-Match の version = %d, want 3", q.docs.updated[0].Version)
			}
		})
	}
}

// リビジョンを作らない更新で change_reason を送っても捨てる（422 にはしない。10.4）。
func TestPatchDocDropsChangeReasonWithoutRevision(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules",
		`{"sort_order":50,"change_reason":"並べ替えただけ"}`, "rules")
	req.Header.Set("If-Match", `"3"`)
	docHandler(q).patchDoc(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.docs.revisionsMade) != 0 {
		t.Errorf("リビジョン = %d件, want 0", len(q.docs.revisionsMade))
	}
}

func TestPatchDocChangeReasonRecorded(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules",
		`{"body_md":"新","change_reason":"ブランチ命名にチケット番号を入れる"}`, "rules")
	req.Header.Set("If-Match", `"3"`)
	docHandler(q).patchDoc(rec, req)

	if len(q.docs.revisionsMade) != 1 {
		t.Fatalf("リビジョン = %d件, want 1 (%s)", len(q.docs.revisionsMade), rec.Body.String())
	}
	rev := q.docs.revisionsMade[0]
	if rev.RevisionNo != 4 {
		t.Errorf("revision_no = %d, want 4", rev.RevisionNo)
	}
	if rev.ChangeReason.String != "ブランチ命名にチケット番号を入れる" {
		t.Errorf("change_reason = %q", rev.ChangeReason.String)
	}
	if rev.BodyMd != "新" {
		t.Errorf("リビジョンの本文 = %q, want 新（変更のあとの本文）", rev.BodyMd)
	}
}

// 自分自身または自分の子孫へは移動できない（10.4 の cycle）。
func TestPatchDocCycle(t *testing.T) {
	for _, target := range []string{"rules", "rules/naming"} {
		t.Run("移動先="+target, func(t *testing.T) {
			q := docFake()
			rec := httptest.NewRecorder()
			req := docReq(http.MethodPatch, "/projects/demo/docs/rules",
				`{"parent_path":"`+target+`"}`, "rules")
			req.Header.Set("If-Match", `"3"`)
			docHandler(q).patchDoc(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), "parent_path", "cycle") {
				t.Errorf("details に parent_path/cycle が無い: %s", rec.Body.String())
			}
			if len(q.docs.updated) != 0 {
				t.Error("循環なのに UPDATE している")
			}
		})
	}
}

// parent_path に null を送るとトップレベルへ移す。送らなければ親は変わらない（10.4）。
func TestPatchDocParentPathNullMovesToTop(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules/naming",
		`{"parent_path":null}`, "rules/naming")
	req.Header.Set("If-Match", `"1"`)
	docHandler(q).patchDoc(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.docs.updated) != 1 {
		t.Fatalf("UPDATE = %d件, want 1", len(q.docs.updated))
	}
	arg := q.docs.updated[0]
	if !arg.ParentIDSet {
		t.Error("parent_id_set が false。null を送っても親を外せない")
	}
	if arg.ParentID.Valid {
		t.Errorf("parent_id = %v, want NULL", arg.ParentID)
	}
}

func TestPatchDocKeepsParentWhenNotSent(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules/naming", `{"title":"新"}`, "rules/naming")
	req.Header.Set("If-Match", `"1"`)
	docHandler(q).patchDoc(rec, req)

	if len(q.docs.updated) != 1 {
		t.Fatalf("UPDATE = %d件, want 1 (%s)", len(q.docs.updated), rec.Body.String())
	}
	if q.docs.updated[0].ParentIDSet {
		t.Error("parent_path を送っていないのに parent_id_set が true")
	}
}

func TestPatchDocRejectsUnknownField(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	req := docReq(http.MethodPatch, "/projects/demo/docs/rules", `{"version":9}`, "rules")
	req.Header.Set("If-Match", `"3"`)
	docHandler(q).patchDoc(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "version", "unknown_field") {
		t.Errorf("details に version/unknown_field が無い: %s", rec.Body.String())
	}
}

// ── 削除（10.4）────────────────────────────────────────────

func TestDeleteDoc(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).deleteDoc(rec, docReq(http.MethodDelete, "/projects/demo/docs/rules", "", "rules"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	// 子を持っていても API は止めない（10.4。件数を出すのは画面の仕事）。
	if len(q.docs.deleted) != 1 || q.docs.deleted[0] != testDocRulesID {
		t.Errorf("削除 = %v, want [%s]", q.docs.deleted, testDocRulesID)
	}
}

// ── 履歴（10.5）────────────────────────────────────────────

func TestListDocRevisions(t *testing.T) {
	q := docFake()
	q.docs.revisionRows = []gen.ListDocumentRevisionsRow{
		{RevisionNo: 3, Title: "規約", ChangedBy: txt(testActorID), ChangedByKind: txt("agent"),
			ChangedByName: txt("claude-code"), ChangeReason: txt("ブランチ命名にチケット番号を入れる"),
			CreatedAt: ts(testDocTime), Total: 3},
		{RevisionNo: 2, Title: "規約", CreatedAt: ts(testDocTime), Total: 3},
		{RevisionNo: 1, Title: "規約", CreatedAt: ts(testDocTime), Total: 3},
	}
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec,
		docReq(http.MethodGet, "/projects/demo/docs/rules/_revisions", "", "rules/_revisions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got List[docRevisionItemJSON]
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	if got.Total != 3 || len(got.Items) != 3 {
		t.Fatalf("total/件数 = %d/%d, want 3/3", got.Total, len(got.Items))
	}
	if got.PerPage != 20 {
		t.Errorf("per_page = %d, want 20（10.5 の既定）", got.PerPage)
	}
	if got.Items[0].RevisionNo != 3 {
		t.Errorf("先頭 = %d, want 3（revision_no の降順）", got.Items[0].RevisionNo)
	}
	if got.Items[0].ChangedBy == nil || got.Items[0].ChangedBy.Kind != "agent" {
		t.Errorf("changed_by = %+v, want kind=agent", got.Items[0].ChangedBy)
	}
	if got.Items[1].ChangeReason != nil {
		t.Errorf("change_reason = %v, want null", *got.Items[1].ChangeReason)
	}
}

func TestGetDocRevision(t *testing.T) {
	q := docFake()
	q.docs.revisionByNo[2] = gen.GetDocumentRevisionRow{
		RevisionNo: 2, Title: "規約", BodyMd: "古い本文", CreatedAt: ts(testDocTime),
	}
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec,
		docReq(http.MethodGet, "/projects/demo/docs/rules/_revisions/2", "", "rules/_revisions/2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got docRevisionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	if got.RevisionNo != 2 || got.BodyMd != "古い本文" {
		t.Errorf("応答 = %+v", got)
	}
}

func TestGetDocRevisionNotFound(t *testing.T) {
	q := docFake()
	rec := httptest.NewRecorder()
	docHandler(q).getDoc(rec,
		docReq(http.MethodGet, "/projects/demo/docs/rules/_revisions/9", "", "rules/_revisions/9"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// _revisions を GET 以外で叩くと 405（10.1 / 10.6）。
func TestDocRevisionsMethodNotAllowed(t *testing.T) {
	t.Run("PATCH", func(t *testing.T) {
		q := docFake()
		rec := httptest.NewRecorder()
		req := docReq(http.MethodPatch, "/projects/demo/docs/rules/_revisions", `{"title":"新"}`,
			"rules/_revisions")
		req.Header.Set("If-Match", `"3"`)
		docHandler(q).patchDoc(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405 (%s)", rec.Code, rec.Body.String())
		}
		if code := errorOf(t, rec).Code; code != "method_not_allowed" {
			t.Errorf("code = %s, want method_not_allowed", code)
		}
		if len(q.docs.updated) != 0 {
			t.Error("405 なのに UPDATE している")
		}
	})

	t.Run("DELETE", func(t *testing.T) {
		q := docFake()
		rec := httptest.NewRecorder()
		docHandler(q).deleteDoc(rec, docReq(http.MethodDelete,
			"/projects/demo/docs/rules/_revisions", "", "rules/_revisions"))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405 (%s)", rec.Code, rec.Body.String())
		}
		if len(q.docs.deleted) != 0 {
			t.Error("405 なのに DELETE している")
		}
	})
}
