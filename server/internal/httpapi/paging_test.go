package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

var projectSort = SortSpec{
	Allowed:      []string{"updated_at", "name", "key"},
	DefaultSort:  "updated_at",
	DefaultOrder: OrderDesc,
}

func parse(t *testing.T, query string) (Page, *apierr.Error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects"+query, nil)
	return ParsePage(req, projectSort)
}

// ApiDesign.md 2.6 の既定：page=1、per_page=25。
func TestParsePageDefaults(t *testing.T) {
	p, e := parse(t, "")
	if e != nil {
		t.Fatalf("既定値でエラーになった: %v", e)
	}
	if p.Page != 1 || p.PerPage != 25 {
		t.Errorf("page/per_page = %d/%d, want 1/25", p.Page, p.PerPage)
	}
	if p.Sort != "updated_at" || p.Order != OrderDesc {
		t.Errorf("sort/order = %q/%q", p.Sort, p.Order)
	}
}

func TestParsePageAccepts(t *testing.T) {
	p, e := parse(t, "?page=3&per_page=200&sort=name&order=asc")
	if e != nil {
		t.Fatalf("正しい指定でエラーになった: %v", e)
	}
	if p.Page != 3 || p.PerPage != 200 {
		t.Errorf("page/per_page = %d/%d, want 3/200", p.Page, p.PerPage)
	}
	if p.Sort != "name" || p.Order != OrderAsc {
		t.Errorf("sort/order = %q/%q, want name/asc", p.Sort, p.Order)
	}
	if p.Offset() != 400 || p.Limit() != 200 {
		t.Errorf("offset/limit = %d/%d, want 400/200", p.Offset(), p.Limit())
	}
}

// 不正な指定は既定値へ丸めず 422 を返す。details にどの項目かを載せる。
func TestParsePageRejects(t *testing.T) {
	cases := []struct {
		name  string
		query string
		field string
	}{
		{"page が0", "?page=0", "page"},
		{"page が負", "?page=-1", "page"},
		{"page が数値でない", "?page=abc", "page"},
		{"per_page が0", "?per_page=0", "per_page"},
		{"per_page が上限超過", "?per_page=201", "per_page"},
		{"per_page が数値でない", "?per_page=2.5", "per_page"},
		{"許可外の sort", "?sort=password_hash", "sort"},
		{"不正な order", "?order=descending", "order"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, e := parse(t, tt.query)
			if e == nil {
				t.Fatal("エラーにならなかった")
			}
			if e.Code != apierr.ValidationFailed {
				t.Errorf("code = %q, want validation_failed", e.Code)
			}
			if e.Status() != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", e.Status())
			}
			if len(e.Details) == 0 || e.Details[0].Field != tt.field {
				t.Errorf("details = %+v, want field=%s", e.Details, tt.field)
			}
		})
	}
}

// 複数の項目が同時に不正なら、まとめて details に載せる（フォームの各欄に紐づけるため）。
func TestParsePageCollectsAllDetails(t *testing.T) {
	_, e := parse(t, "?page=0&per_page=999&order=up")
	if e == nil {
		t.Fatal("エラーにならなかった")
	}
	if len(e.Details) != 3 {
		t.Errorf("details = %d 件, want 3: %+v", len(e.Details), e.Details)
	}
}

func TestTotalPages(t *testing.T) {
	cases := []struct {
		total, perPage, want int
	}{
		{48, 25, 2}, // ApiDesign.md 2.6 の例
		{0, 25, 0},
		{1, 25, 1},
		{25, 25, 1},
		{26, 25, 2},
		{200, 200, 1},
	}
	for _, tt := range cases {
		p := Page{Page: 1, PerPage: tt.perPage}
		got := NewList([]string{}, p, tt.total).TotalPages
		if got != tt.want {
			t.Errorf("total=%d per_page=%d: total_pages = %d, want %d",
				tt.total, tt.perPage, got, tt.want)
		}
	}
}

// items が空でも JSON では [] にする（null にしない）。
func TestNewListEmptyItemsSerializeAsArray(t *testing.T) {
	var items []string // nil
	b, err := json.Marshal(NewList(items, Page{Page: 1, PerPage: 25}, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[],"page":1,"per_page":25,"total":0,"total_pages":0}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestOffsetOfFirstPage(t *testing.T) {
	p := Page{Page: 1, PerPage: 25}
	if p.Offset() != 0 {
		t.Errorf("offset = %d, want 0", p.Offset())
	}
}
