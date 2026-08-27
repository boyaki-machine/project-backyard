package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// プロジェクトの集計（ApiDesign.md 9.13.1）の単体テスト。手順19a。
//
// 認可（project.view）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは応答の形と、
// **9.13.1 が固定した閾値がクエリまで届いているか**である。

// dashFake はプロジェクト demo が解決できる状態のフェイクを返す。
//
// 9.13 の2本（stats / activity）で共有する。
func dashFake() *fakeQuerier {
	return &fakeQuerier{projectIDByKey: map[string]string{"demo": testProjectID}}
}

func decodeStats(t *testing.T, rec *httptest.ResponseRecorder) projectStatsView {
	t.Helper()
	var v projectStatsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// sampleStatsRow は 9.13.1 の応答例と同じ値。**by_category の合計 66 と
// total 66 が一致する**、分類できないチケットが無い場合である。
func sampleStatsRow() gen.GetProjectTicketStatsRow {
	return gen.GetProjectTicketStatsRow{
		Todo: 18, InProgress: 8, Review: 4, Done: 36,
		Total: 66, OpenCount: 30, Overdue: 2, Stale: 3, Unassigned: 5,
	}
}

func TestGetProjectStatsReturnsAllFields(t *testing.T) {
	q := dashFake()
	q.ticket.statsRow = sampleStatsRow()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeStats(t, rec)

	// 9.13.1 の応答例をそのまま突き合わせる。
	want := projectStatsView{
		ByCategory: statsByCategoryView{Todo: 18, InProgress: 8, Review: 4, Done: 36},
		Total:      66,
		Open:       30,
		Overdue:    2,
		Stale:      statsStaleView{Count: 3, ThresholdDays: 14},
		Unassigned: 5,
	}
	if got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

// **閾値は応答だけでなくクエリにも渡る**（9.13.1）。応答の threshold_days だけを
// 見ても、SQL へ何日が渡ったかは分からない——どちらも定数から書けてしまう。
func TestGetProjectStatsPassesFixedThresholdToQuery(t *testing.T) {
	q := dashFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if q.ticket.statsStaleIn != staleThresholdDays {
		t.Errorf("stale_days = %d, want %d", q.ticket.statsStaleIn, staleThresholdDays)
	}
	if staleThresholdDays != 14 {
		t.Errorf("staleThresholdDays = %d, want 14（ApiDesign.md 9.13.1）", staleThresholdDays)
	}
}

// **by_category は常に4つのキーを持つ**（9.13.1）。simple テンプレートは review を
// 持たないが（DbDesign.md 7.4）、キーが消えると画面のカードが3枚になる。
func TestGetProjectStatsKeepsAllFourCategoryKeys(t *testing.T) {
	q := dashFake()
	// review を持たないワークフローのプロジェクトを模す。
	q.ticket.statsRow = gen.GetProjectTicketStatsRow{
		Todo: 4, InProgress: 1, Review: 0, Done: 10, Total: 15, OpenCount: 5,
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	// **生の JSON を見る。** 構造体へ解いてから 0 を確かめると、キーが
	// 応答に無くてもゼロ値で通ってしまう。
	body := rec.Body.String()
	for _, key := range []string{`"todo":`, `"in_progress":`, `"review":`, `"done":`} {
		if !strings.Contains(body, key) {
			t.Errorf("by_category に %s が無い: %s", key, body)
		}
	}
	if !strings.Contains(body, `"review":0`) {
		t.Errorf(`"review":0 が無い: %s`, body)
	}
}

// **by_category の合計が total と一致しないことがある**（stats.sql）。
// ステータスキーがワークフローに解決できないチケットはどのカテゴリにも
// 数えられない。サーバは合わせに行かず、そのまま返す。
func TestGetProjectStatsDoesNotReconcileCategorySumWithTotal(t *testing.T) {
	q := dashFake()
	q.ticket.statsRow = gen.GetProjectTicketStatsRow{
		Todo: 1, InProgress: 1, Review: 0, Done: 0, Total: 3, OpenCount: 3,
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	got := decodeStats(t, rec)
	sum := got.ByCategory.Todo + got.ByCategory.InProgress + got.ByCategory.Review + got.ByCategory.Done
	if sum != 2 || got.Total != 3 {
		t.Errorf("by_category の合計 = %d / total = %d, want 2 / 3", sum, got.Total)
	}
}

// **ETag を付けない**（9.13.1）。一覧ではなく、材料を採る走査が本体と同じである。
func TestGetProjectStatsHasNoETag(t *testing.T) {
	q := dashFake()
	q.ticket.statsRow = sampleStatsRow()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	if v := rec.Header().Get("ETag"); v != "" {
		t.Errorf("ETag = %q, want 空（9.13.1）", v)
	}
}

func TestGetProjectStatsFailsWithInternalError(t *testing.T) {
	q := dashFake()
	q.ticket.statsErr = errors.New("boom")
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.getProjectStats(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/stats"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
