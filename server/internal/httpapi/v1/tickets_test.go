package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/lexorank"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// チケットAPI（ApiDesign.md 9.2 / 9.3 / 9.4）の単体テスト。
//
// 認可（ticket.view / ticket.create / ticket.edit）はミドルウェアの責務なので
// ここでは通さない（routes_test.go が宣言を見ている）。ここで確かめるのは、
// クエリの解析・入力の検証・応答の形・ETag・並べ替えの境界である。

const (
	testTicketID  = "01K2TKT00000000000000031"
	testTicketID2 = "01K2TKT00000000000000044"
	testTicketID3 = "01K2TKT00000000000000045"
	testTicketID4 = "01K2TKT00000000000000012"
)

// ticketReq は /projects/{key}/tickets 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうためである（tags_test と同じ）。
func ticketReq(method, target, body, seq string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	if seq != "" {
		rc.URLParams.Add("seq", seq)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

// ticketFake はプロジェクト demo が解決できる状態のフェイクを返す。
func ticketFake() *fakeQuerier {
	q := &fakeQuerier{projectIDByKey: map[string]string{"demo": testProjectID}}
	q.ticket.bySeq = map[int32]gen.GetTicketBySeqRow{}
	q.ticket.briefByID = map[string]gen.GetTicketBriefRow{}
	q.ticket.idBySeq = map[int32]string{}
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{}
	q.ticket.initialStatusKey = "todo"
	q.ticket.nextSeq = 31
	return q
}

func ticketHandler(q *fakeQuerier) (*handler, *fakeTxRunner) {
	tx := &fakeTxRunner{q: q}
	return &handler{q: q, tx: tx}, tx
}

// sampleTicketRow は一覧の1行ぶんの素材。
func sampleTicketRow(id string, seq int32, title string) gen.ListTicketsRow {
	now := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)
	return gen.ListTicketsRow{
		ID: id, Seq: seq, Type: "task", Title: title,
		StatusKey:      "in_progress",
		StatusName:     txt("進行中"),
		StatusCategory: txt("in_progress"),
		Priority:       txt("high"),
		AssigneeID:     txt(testActorID),
		AssigneeKind:   txt("user"),
		AssigneeName:   txt("田中"),
		SortKey:        txt("0|n:"),
		Version:        3,
		CreatedAt:      ts(now),
		UpdatedAt:      ts(now),
		Total:          1,
		LastUpdatedAt:  ts(now),
	}
}

// ticketItemJSON は応答を読み戻すための型。
//
// **ticketListItem をそのまま使えない。** 日時は v1.Time / v1.Date で、応答を
// 1つの表記に固定するための「書く側」の型であり UnmarshalJSON を持たない
// （apitime.go）。読み戻しのために production 側へ逆変換を足すと、書く型と
// 読む型の区別が消える。ここで確かめたいのは日時の表記ではなく本体の形なので、
// 検証側に必要なぶんだけの型を置く。
type ticketItemJSON struct {
	ID          string           `json:"id"`
	Seq         int32            `json:"seq"`
	Type        string           `json:"type"`
	Title       string           `json:"title"`
	Status      ticketStatusView `json:"status"`
	Priority    *string          `json:"priority"`
	Assignee    *actorRef        `json:"assignee"`
	Reporter    *actorRef        `json:"reporter"`
	ParentSeq   *int32           `json:"parent_seq"`
	HasChildren bool             `json:"has_children"`
	SortKey     *string          `json:"sort_key"`
	Tags        []ticketTagRef   `json:"tags"`
	Sprint      *sprintRef       `json:"sprint"`
	Version     int32            `json:"version"`
	CreatedAt   string           `json:"created_at"`
	DueDate     *string          `json:"due_date"`
}

func decodeTicketList(t *testing.T, rec *httptest.ResponseRecorder) List[ticketItemJSON] {
	t.Helper()
	var v List[ticketItemJSON]
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET /tickets（9.2）──────────────────────────────────────

func TestListTicketsReturnsItemsWithTagsAndHierarchy(t *testing.T) {
	q := ticketFake()
	row := sampleTicketRow(testTicketID, 31, "認証APIの実装")
	row.ParentSeq = pgtype.Int4{Int32: 12, Valid: true}
	row.HasChildren = true
	row.SprintID = txt("01K2SPR00000000000000001")
	row.SprintName = txt("Sprint 3")
	q.ticket.rows = []gen.ListTicketsRow{row}
	q.ticket.tagRows = []gen.ListTagsForTicketsRow{
		{TicketID: testTicketID, ID: "01K2TAG00000000000000001", Name: "設計"},
		{TicketID: testTicketID, ID: "01K2TAG00000000000000002", Name: "API"},
	}

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	list := decodeTicketList(t, rec)
	if len(list.Items) != 1 {
		t.Fatalf("items = %d件", len(list.Items))
	}
	item := list.Items[0]
	if item.Seq != 31 || item.Title != "認証APIの実装" {
		t.Errorf("本体が違う: %+v", item)
	}
	if item.Status.Key != "in_progress" || item.Status.Name != "進行中" ||
		item.Status.Category != "in_progress" {
		t.Errorf("status = %+v", item.Status)
	}
	if len(item.Tags) != 2 || item.Tags[0].Name != "設計" {
		t.Errorf("tags = %+v（1回の ListTagsForTickets で束ねること）", item.Tags)
	}
	if item.ParentSeq == nil || *item.ParentSeq != 12 || !item.HasChildren {
		t.Errorf("階層の情報が欠けている: parent_seq=%v has_children=%v",
			item.ParentSeq, item.HasChildren)
	}
	if item.Sprint == nil || item.Sprint.Name != "Sprint 3" {
		t.Errorf("sprint = %+v", item.Sprint)
	}
	if item.Assignee == nil || item.Assignee.Kind != "user" {
		t.Errorf("assignee = %+v（kind は 🤖 バッジの判定に要る）", item.Assignee)
	}
	// タグは1回だけ引く（行ごとに引かない。9.2.2 の要点）
	if got := countOps(q.opLog, "ListTagsForTickets"); got != 1 {
		t.Errorf("ListTagsForTickets の回数 = %d（1回で束ねること）", got)
	}
}

// 一覧に本文とエージェント連携の列を載せない（9.2.2）。
func TestListTicketsOmitsBodyAndPhase2Columns(t *testing.T) {
	q := ticketFake()
	q.ticket.rows = []gen.ListTicketsRow{sampleTicketRow(testTicketID, 31, "本文は出さない")}

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets", "", ""))

	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	for _, forbidden := range []string{
		"body_md", "execution_mode", "readiness", "readiness_note", "scope", "custom_fields",
	} {
		if _, ok := raw.Items[0][forbidden]; ok {
			t.Errorf("一覧に %s が載っている（9.2.2 は載せないと決めている）", forbidden)
		}
	}
}

// per_page の既定は 200、sort の既定は sort_key の昇順（9.2.1）。
func TestListTicketsDefaults(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	list := decodeTicketList(t, rec)
	if list.PerPage != 200 {
		t.Errorf("per_page の既定 = %d（9.2.1 は 200）", list.PerPage)
	}
	p := q.ticket.listParams[0]
	if p.Sort != "sort_key" || p.SortOrder != "asc" {
		t.Errorf("sort/order の既定 = %s/%s（9.2.1 は sort_key/asc）", p.Sort, p.SortOrder)
	}
	if p.OpenFilter != "all" || p.DueWithinDays != -1 || len(p.ParentSeqs) != 0 {
		t.Errorf("未指定のフィルタが効いている: %+v", p)
	}
	if p.PlannedFrom.Valid || p.PlannedTo.Valid {
		t.Errorf("未指定の予定期間が効いている: from=%+v to=%+v", p.PlannedFrom, p.PlannedTo)
	}
	// overdue / stale の「指定なし」（9.2.1。手順19b）。**stale は 0 ではなく負**
	// ——0 は「0日以上更新なし」＝全件になってしまい、指定なしと区別できない。
	if p.OverdueOnly || p.StaleDays != -1 {
		t.Errorf("overdue/stale の既定が効いている: overdue=%v stale=%d", p.OverdueOnly, p.StaleDays)
	}
	// **nil を送らない**（tickets.go の parseTicketFilters）。pgx は nil スライスを
	// SQL の NULL にするため cardinality(NULL) = NULL となり、「指定なし」の
	// 判定が偽になって1件も返らなくなる。
	if p.ParentSeqs == nil {
		t.Error("parent 未指定のとき ParentSeqs が nil（空スライスであること）")
	}
}

// 同じ条件の複数指定は OR、me / none は別扱い（9.2.1）。
func TestListTicketsFilters(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?type=story,task&priority=high,highest"+
			"&assignee=me,none&tag=01K2TAG00000000000000001,none&sprint=none"+
			"&open=true&due_within=7d&parent=12,30&status=todo,in_progress"+
			"&status_category=todo&planned_from=2026-09-01&planned_to=2026-09-30", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	p := q.ticket.listParams[0]
	if len(p.Types) != 2 || len(p.Priorities) != 2 || len(p.StatusKeys) != 2 {
		t.Errorf("カンマ区切りが分解されていない: %+v", p)
	}
	// me は自分の ULID へ、none はフラグへ振り分ける
	if len(p.AssigneeIds) != 1 || p.AssigneeIds[0] != testActorID || !p.AssigneeNone {
		t.Errorf("assignee=me,none の解釈が違う: ids=%v none=%v", p.AssigneeIds, p.AssigneeNone)
	}
	if len(p.TagIds) != 1 || !p.TagNone {
		t.Errorf("tag=<ULID>,none の解釈が違う: ids=%v none=%v", p.TagIds, p.TagNone)
	}
	if len(p.SprintIds) != 0 || !p.SprintNone {
		t.Errorf("sprint=none の解釈が違う: ids=%v none=%v", p.SprintIds, p.SprintNone)
	}
	if p.OpenFilter != "open" || p.DueWithinDays != 7 {
		t.Errorf("open/due_within の解釈が違う: %+v", p)
	}
	if !p.PlannedFrom.Valid || p.PlannedFrom.Time.Format(time.DateOnly) != "2026-09-01" ||
		!p.PlannedTo.Valid || p.PlannedTo.Time.Format(time.DateOnly) != "2026-09-30" {
		t.Errorf("予定期間の解釈が違う: from=%+v to=%+v", p.PlannedFrom, p.PlannedTo)
	}
	// parent は**カンマ区切りで複数指定できる**（9.2.1）。エピックフィルタが使う。
	if len(p.ParentSeqs) != 2 || p.ParentSeqs[0] != 12 || p.ParentSeqs[1] != 30 {
		t.Errorf("parent=12,30 の解釈が違う: %v", p.ParentSeqs)
	}
}

// 解釈できない値は既定へ丸めず 422（2.6）。**項目ごとに details を並べる。**
func TestListTicketsRejectsInvalidFilters(t *testing.T) {
	cases := []struct{ name, query, field string }{
		{"検索モード", "search_mode=unknown", "search_mode"},
		{"種別", "type=epic,unknown", "type"},
		{"優先度", "priority=urgent", "priority"},
		{"分類", "status_category=blocked", "status_category"},
		{"open", "open=yes", "open"},
		{"期限", "due_within=7days", "due_within"},
		{"期限超過", "overdue=false", "overdue"},
		{"予定開始日の書式", "planned_from=2026/09/01", "planned_from"},
		{"予定期間の向き", "planned_from=2026-09-30&planned_to=2026-09-01", "planned_to"},
		{"放置の書式", "stale=14days", "stale"},
		{"放置の上限", "stale=3651d", "stale"},
		{"親", "parent=0", "parent"},
		{"ソート", "sort=body_md", "sort"},
		{"件数", "per_page=201", "per_page"},
		// 検索の条件（9.2.1「検索の条件」）。**範囲が逆なら空の結果にせず 422**
		{"キーワードの長さ", "q=" + url.QueryEscape(strings.Repeat("あ", 201)), "q"},
		{"番号の下限", "seq_from=0", "seq_from"},
		{"番号の書式", "seq_to=abc", "seq_to"},
		{"番号の向き", "seq_from=20&seq_to=10", "seq_to"},
		{"着手日時は日付だけでは受けない", "started_since=2026-09-01", "started_since"},
		{"完了日時の向き", "closed_since=2026-09-16T00:00:00Z&closed_before=2026-09-01T00:00:00Z", "closed_before"},
		// 時差が違っても同じ瞬間なら空の範囲である
		{"同じ瞬間は空の範囲", "started_since=2026-09-01T00:00:00Z&started_before=2026-09-01T09:00:00%2B09:00", "started_before"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := ticketFake()
			h, _ := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.listTickets(rec, ticketReq(http.MethodGet,
				"/projects/demo/tickets?"+tc.query, "", ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d（422 のはず）body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Errorf("details に %s が無い: %s", tc.field, rec.Body.String())
			}
		})
	}
}

// 複数の誤りは1つの 422 にまとめる（2.5）。
func TestListTicketsMergesValidationErrors(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?type=nope&per_page=0", "", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type"`) || !strings.Contains(body, `"per_page"`) {
		t.Errorf("両方の項目が details に無い: %s", body)
	}
}

// ETag はフィルタ・並び・ページで変わる（9.2.5）。
func TestTicketsETagVariesByFilterAndPage(t *testing.T) {
	q := ticketFake()
	q.ticket.rows = []gen.ListTicketsRow{sampleTicketRow(testTicketID, 31, "A")}
	h, _ := ticketHandler(q)

	etag := func(query string) string {
		rec := httptest.NewRecorder()
		h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets?"+query, "", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("ETag")
	}

	base := etag("type=story")
	if !strings.HasPrefix(base, `W/"tkt-`) {
		t.Errorf("ETag が弱い検証子の形になっていない: %q", base)
	}
	if other := etag("type=task"); other == base {
		t.Errorf("フィルタが違うのに ETag が同じ: %q", base)
	}
	if other := etag("type=story&page=2"); other == base {
		t.Errorf("ページが違うのに ETag が同じ: %q", base)
	}
	if other := etag("type=story&order=desc"); other == base {
		t.Errorf("並びが違うのに ETag が同じ: %q", base)
	}
	// parent の複数指定も ETag に混ざる（9.2.5）。**数として並べ替えている**ので
	// 順番違いは同じ値になり、9 と 10 の前後が文字列比較で逆にならない。
	if a, b := etag("parent=9,10"), etag("parent=10,9"); a != b {
		t.Errorf("parent の順番違いで ETag が変わった: %q vs %q", a, b)
	}
	if a, b := etag("parent=9,10"), etag("parent=9"); a == b {
		t.Errorf("parent の件数が違うのに ETag が同じ: %q", a)
	}
	// 同じ意味の違う書き方は同じ ETag（正規化して混ぜているため）
	if a, b := etag("type=story,task"), etag("type=task,story"); a != b {
		t.Errorf("順序違いの同じ条件で ETag が変わった: %q vs %q", a, b)
	}
	// overdue / stale も ETag の材料に入る（9.2.5。手順19b）。**入っていないと、
	// ダッシュボードの「確認する →」から来た一覧が、素の一覧のキャッシュに
	// 当たって 304 で返りうる。**
	if other := etag("overdue=true"); other == etag("") {
		t.Errorf("overdue の有無で ETag が変わらない: %q", other)
	}
	if a, b := etag("stale=14d"), etag("stale=30d"); a == b {
		t.Errorf("stale の日数が違うのに ETag が同じ: %q", a)
	}
	// 検索の条件も ETag に混ざる（9.2.5）。**語の順番違いと、同じ瞬間の時差違いは
	// 同じ意味**なので同じ値になる。
	if a, b := etag("q="+url.QueryEscape("認証 API")), etag("q="+url.QueryEscape("API 認証")); a != b {
		t.Errorf("語の順番違いで ETag が変わった: %q vs %q", a, b)
	}
	if a, b := etag("q="+url.QueryEscape("認証")), etag("q=API"); a == b {
		t.Errorf("語が違うのに ETag が同じ: %q", a)
	}
	if a, b := etag("seq_from=10"), etag("seq_from=11"); a == b {
		t.Errorf("番号の範囲が違うのに ETag が同じ: %q", a)
	}
	if a, b := etag("planned_from=2026-09-01"), etag("planned_from=2026-09-02"); a == b {
		t.Errorf("予定期間が違うのに ETag が同じ: %q", a)
	}
	if a, b := etag("closed_since=2026-09-01T00:00:00Z"),
		etag("closed_since="+url.QueryEscape("2026-09-01T09:00:00+09:00")); a != b {
		t.Errorf("同じ瞬間の時差違いで ETag が変わった: %q vs %q", a, b)
	}
}

// 検索の条件（9.2.1「検索の条件」）がクエリの値どおりに渡る。
//
// **キーワードは store/search で ID に変えてから一覧へ渡す**（Design.md 4.6）。
// 検索へ渡ったパターンと、一覧へ渡った ID の両方を見る。
func TestListTicketsSearchConditions(t *testing.T) {
	q := ticketFake()
	q.ticket.searchIDs = []string{testTicketID}
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?q="+url.QueryEscape("認証　100% 認証")+
			"&seq_from=10&seq_to=20"+
			"&started_since="+url.QueryEscape("2026-09-01T00:00:00+09:00")+
			"&closed_before=2026-09-16T00:00:00Z&sort=closed_at&order=desc", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// 全角の空白で分け、重複を畳み、% を文字として扱う（store/search）
	if len(q.ticket.searchParams) != 1 {
		t.Fatalf("SearchTicketIDs の回数 = %d, want 1", len(q.ticket.searchParams))
	}
	sp := q.ticket.searchParams[0]
	if sp.ProjectID != testProjectID || !slices.Equal(sp.Patterns, []string{`%認証%`, `%100\%%`}) {
		t.Errorf("SearchTicketIDs の引数 = %+v", sp)
	}
	p := q.ticket.listParams[0]
	if !p.KeywordSet || !slices.Equal(p.KeywordIds, []string{testTicketID}) {
		t.Errorf("keyword = set:%v ids:%v, want 検索が返した ID", p.KeywordSet, p.KeywordIds)
	}
	if p.SeqFrom != 10 || p.SeqTo != 20 {
		t.Errorf("seq = %d〜%d, want 10〜20", p.SeqFrom, p.SeqTo)
	}
	// 時差付きの瞬間をそのまま受ける（日の境界はサーバが作らない）
	want := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	if !p.StartedSince.Valid || !p.StartedSince.Time.Equal(want) {
		t.Errorf("started_since = %+v, want %s", p.StartedSince, want)
	}
	if p.StartedBefore.Valid || p.ClosedSince.Valid || !p.ClosedBefore.Valid {
		t.Errorf("指定しなかった範囲が効いている、または指定した範囲が落ちた: %+v", p)
	}
	if p.Sort != "closed_at" || p.SortOrder != "desc" {
		t.Errorf("sort/order = %s/%s, want closed_at/desc", p.Sort, p.SortOrder)
	}
}

// 語がすべて trigram を作れるときだけ、trgm を使う形へ振り分ける（DbDesign.md 4.5）。
//
// **同じ語でも DB の LC_CTYPE で行き先が変わる。** C の DB では日本語から trigram を
// 取り出せないので、インデックスを使う形にすると遅くなる。
func TestListTicketsSearchChoosesQueryByCtype(t *testing.T) {
	cases := []struct {
		name, ctype, q string
		trigram        bool
	}{
		{"C.UTF-8 で3文字以上の日本語", "C.UTF-8", "ケルベロス　サーバ", true},
		{"C では日本語を trgm へ回さない", "C", "ケルベロス　サーバ", false},
		{"C でも英数字3文字以上なら回す", "C", "sqlc API", true},
		{"2文字の語が混じれば回さない", "C.UTF-8", "ケルベロス 認証", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ticketFake()
			q.ticket.searchCtype = c.ctype
			q.ticket.searchIDs = []string{testTicketID}
			h, _ := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.listTickets(rec, ticketReq(http.MethodGet,
				"/projects/demo/tickets?q="+url.QueryEscape(c.q), "", ""))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			gotTrigram := len(q.ticket.trigramSearchParams) == 1 && len(q.ticket.searchParams) == 0
			gotPlain := len(q.ticket.searchParams) == 1 && len(q.ticket.trigramSearchParams) == 0
			if c.trigram && !gotTrigram || !c.trigram && !gotPlain {
				t.Errorf("trgm の形 %d 回・今の形 %d 回, want trigram=%v",
					len(q.ticket.trigramSearchParams), len(q.ticket.searchParams), c.trigram)
			}
			// どちらへ行っても、一覧へ渡る ID は検索が返したもの
			if p := q.ticket.listParams[0]; !slices.Equal(p.KeywordIds, []string{testTicketID}) {
				t.Errorf("keyword ids = %v, want 検索が返した ID", p.KeywordIds)
			}
		})
	}
}

// 検索の条件が無ければ SearchTicketIDs を呼ばず、一覧の引数も「指定なし」になる。
// **空白だけの q も指定なし**——語が無いまま呼ぶと、当たらない語が無いので全件が一致する。
func TestListTicketsWithoutSearchConditions(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?q="+url.QueryEscape(" 　 "), "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if slices.Contains(q.opLog, "SearchTicketIDs") {
		t.Error("語が無いのに SearchTicketIDs を呼んだ")
	}
	p := q.ticket.listParams[0]
	if p.KeywordSet || p.KeywordIds == nil || p.SeqFrom != 0 || p.SeqTo != 0 ||
		p.StartedSince.Valid || p.StartedBefore.Valid || p.ClosedSince.Valid || p.ClosedBefore.Valid {
		t.Errorf("未指定の検索の条件が効いている: %+v", p)
	}
}

// overdue / stale はクエリの値どおりに解釈される（9.2.1。手順19b）。
//
// **どちらも stats（9.13.1）と同じ条件を意図しており、SQL 側の条件は
// stats.sql の写しである。** ここではハンドラが値を落とさず渡すことだけを見る。
func TestListTicketsOverdueAndStale(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?overdue=true&stale=14d", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	p := q.ticket.listParams[0]
	if !p.OverdueOnly {
		t.Error("overdue=true が渡っていない")
	}
	if p.StaleDays != 14 {
		t.Errorf("stale=14d の解釈が違う: %d", p.StaleDays)
	}
}

// ── POST /tickets（9.3）─────────────────────────────────────

func TestCreateTicketServerDecidesSeqStatusSortKeyAndReporter(t *testing.T) {
	q := ticketFake()
	q.ticket.sortRowBySeq[31] = gen.GetTicketSortRowRow{ID: testTicketID, SortKey: txt("0|n:")}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"認証APIの実装","body_md":"本文"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/projects/demo/tickets/31" {
		t.Errorf("Location = %q", got)
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}
	if len(q.ticket.created) != 1 {
		t.Fatalf("CreateTicket の回数 = %d", len(q.ticket.created))
	}
	c := q.ticket.created[0]
	if c.Seq != 31 {
		t.Errorf("seq = %d（project_counter の採番を使うこと）", c.Seq)
	}
	if c.StatusKey != "todo" {
		t.Errorf("status_key = %q（ワークフローの入口を使うこと）", c.StatusKey)
	}
	if c.ReporterID.String != testActorID {
		t.Errorf("reporter_id = %q（呼び出し元のアクター）", c.ReporterID.String)
	}
	if !lexorank.Valid(c.SortKey.String) {
		t.Errorf("sort_key = %q（LexoRank の形式でない）", c.SortKey.String)
	}
	if c.SortKey.String <= "0|n:" {
		t.Errorf("sort_key = %q（末尾の次にならない）", c.SortKey.String)
	}
}

// 作成は 9.5 形式で返す。**作りたては dod / links が空、comment_count が0**
// ——手順18a で実数を返すようになった後も、作成直後の正しい値は空である。
func TestCreateTicketRespondsWithDetailShape(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"落ちる"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	for _, key := range []string{"body_md", "parent", "children", "dod", "links", "comment_count"} {
		if _, ok := body[key]; !ok {
			t.Errorf("9.5 形式に %s が無い", key)
		}
	}
	if arr, ok := body["dod"].([]any); !ok || len(arr) != 0 {
		t.Errorf("dod = %v（作りたては空配列）", body["dod"])
	}
	if arr, ok := body["links"].([]any); !ok || len(arr) != 0 {
		t.Errorf("links = %v（作りたては空配列）", body["links"])
	}
	if n, ok := body["comment_count"].(float64); !ok || n != 0 {
		t.Errorf("comment_count = %v（作りたては0）", body["comment_count"])
	}
	if body["version"].(float64) != 1 {
		t.Errorf("version = %v（作成時は1）", body["version"])
	}
}

func TestCreateTicketAttachesTags(t *testing.T) {
	q := ticketFake()
	q.ticket.projectTagCount = 2
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"タグ付き","tag_ids":["01K2TAG00000000000000001","01K2TAG00000000000000002","01K2TAG00000000000000001"]}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// 重複は畳んでから付ける
	if len(q.ticket.attached) != 2 {
		t.Errorf("AttachTicketTag の回数 = %d（重複を畳んで2回）", len(q.ticket.attached))
	}
}

// 作成は activity に記録する（9.3 / 9.1.1）。監査ログには書かない。
func TestCreateTicketRecordsActivity(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"履歴に残る"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity の件数 = %d", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.EntityType != "ticket" || a.Action != "create" {
		t.Errorf("activity = %+v", a)
	}
	if a.ProjectID != testProjectID || a.ActorID.String != testActorID {
		t.Errorf("activity の帰属が違う: %+v", a)
	}
	if a.Field.Valid || a.OldValue.Valid || a.NewValue.Valid {
		t.Errorf("作成の activity に field/old/new を入れない: %+v", a)
	}
	if countOps(q.opLog, "InsertAuditLog") != 0 {
		t.Error("チケットの作成を audit_log に書いている（9.1.1 は activity だけ）")
	}
}

func TestCreateTicketValidation(t *testing.T) {
	cases := []struct{ name, body, field string }{
		{"種別が無い", `{"title":"x"}`, "type"},
		{"種別が値域外", `{"type":"idea","title":"x"}`, "type"},
		{"タイトルが空", `{"type":"task","title":"   "}`, "title"},
		{"タイトルが長い", `{"type":"task","title":"` + strings.Repeat("あ", 201) + `"}`, "title"},
		{"優先度が値域外", `{"type":"task","title":"x","priority":"urgent"}`, "priority"},
		{"見積が負", `{"type":"task","title":"x","estimate_point":-1}`, "estimate_point"},
		{"日付の形式", `{"type":"task","title":"x","due_date":"2026/08/14"}`, "due_date"},
		{"日付に時刻", `{"type":"task","title":"x","due_date":"2026-08-14T00:00:00Z"}`, "due_date"},
		{"期限が開始より前", `{"type":"task","title":"x","start_date":"2026-08-14","due_date":"2026-08-09"}`, "due_date"},
		{"親の番号が0", `{"type":"task","title":"x","parent_seq":0}`, "parent_seq"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := ticketFake()
			h, _ := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets", tc.body, ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d（422 のはず）body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Errorf("details に %s が無い: %s", tc.field, rec.Body.String())
			}
			if len(q.ticket.created) != 0 {
				t.Error("検証に失敗したのに作成している")
			}
		})
	}
}

// 担当者がメンバーでなければ not_a_member（9.14）。
func TestCreateTicketRejectsNonMemberAssignee(t *testing.T) {
	q := ticketFake()
	q.ticket.isMember = false
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"x","assignee_id":"01K2OTH00000000000000001"}`, ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not_a_member") {
		t.Errorf("details[].code が not_a_member でない: %s", rec.Body.String())
	}
	if tx.committed {
		t.Error("検証に失敗したのにコミットしている")
	}
}

// 参照先がこのプロジェクトに無ければ 422（作成は行われない）。
func TestCreateTicketRejectsForeignReferences(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		setup func(*fakeQuerier)
		field string
	}{
		{
			name:  "親が無い",
			body:  `{"type":"task","title":"x","parent_seq":999}`,
			setup: func(q *fakeQuerier) {},
			field: "parent_seq",
		},
		{
			name:  "タグが他プロジェクト",
			body:  `{"type":"task","title":"x","tag_ids":["01K2TAG00000000000000009"]}`,
			setup: func(q *fakeQuerier) { q.ticket.projectTagCount = 0 },
			field: "tag_ids",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := ticketFake()
			tc.setup(q)
			h, tx := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets", tc.body, ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Errorf("details に %s が無い: %s", tc.field, rec.Body.String())
			}
			if tx.committed {
				t.Error("参照先が不正なのにコミットしている")
			}
		})
	}
}

// sprint_id は 9.3 が受け付けない。**黙って捨てず 422 に倒す**
// ——decodeJSON は未知のキーを無視するので、struct から落とすだけだと
// 送った側は設定できたつもりでスプリント無しのチケットが出来る。
func TestCreateTicketRejectsSprintID(t *testing.T) {
	q := ticketFake()
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"x","sprint_id":"01K2SPR00000000000000009"}`, ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "use_sprint_endpoint") {
		t.Errorf("details[].code が use_sprint_endpoint でない: %s", rec.Body.String())
	}
	if tx.committed {
		t.Error("受け付けない項目が来たのにコミットしている")
	}
}

// ── POST /tickets/{seq}/move（9.4）──────────────────────────

func moveFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{
		31: {ID: testTicketID, SortKey: txt("0|n:"), Version: 3},
		44: {ID: testTicketID2, SortKey: txt("0|u:"), Version: 1},
	}
	return q
}

// moveRespJSON は move の応答を読み戻すための型。
//
// **moveTicketResponse をそのまま使えない。** staged_at は v1.Time で、応答を
// 1つの表記に固定するための「書く側」の型であり UnmarshalJSON を持たない
// （apitime.go）。ticketItemJSON と同じ理由・同じ扱いである。
type moveRespJSON struct {
	Seq        int32   `json:"seq"`
	SortKey    string  `json:"sort_key"`
	StagedAt   *string `json:"staged_at"`
	Version    int32   `json:"version"`
	Rebalanced bool    `json:"rebalanced"`
}

func decodeMove(t *testing.T, rec *httptest.ResponseRecorder) moveRespJSON {
	t.Helper()
	var v moveRespJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

func TestMoveTicketAfterSeq(t *testing.T) {
	q := moveFake() // 44（0|u:）が末尾。31 を 44 の後ろへ動かす
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"after_seq":44}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeMove(t, rec)
	if resp.Seq != 31 {
		t.Errorf("seq = %d", resp.Seq)
	}
	if resp.SortKey <= "0|u:" {
		t.Errorf("sort_key = %q（44 の後ろへ入らない）", resp.SortKey)
	}
	if resp.Version != 4 {
		t.Errorf("version = %d（+1 されること。9.4）", resp.Version)
	}
	if resp.Rebalanced {
		t.Error("振り直していないのに rebalanced=true")
	}
	if !tx.committed {
		t.Error("コミットされていない")
	}
}

func TestMoveTicketPositionFirstAndLast(t *testing.T) {
	t.Run("先頭へ", func(t *testing.T) {
		q := moveFake() // 先頭は 31（0|n:）
		h, _ := ticketHandler(q)
		rec := httptest.NewRecorder()
		h.moveTicket(rec, ticketReq(http.MethodPost,
			"/projects/demo/tickets/44/move", `{"position":"first"}`, "44"))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if got := decodeMove(t, rec).SortKey; got >= "0|n:" {
			t.Errorf("sort_key = %q（先頭より前にならない）", got)
		}
	})
	t.Run("末尾へ", func(t *testing.T) {
		q := moveFake() // 末尾は 44（0|u:）
		h, _ := ticketHandler(q)
		rec := httptest.NewRecorder()
		h.moveTicket(rec, ticketReq(http.MethodPost,
			"/projects/demo/tickets/31/move", `{"position":"last"}`, "31"))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if got := decodeMove(t, rec).SortKey; got <= "0|u:" {
			t.Errorf("sort_key = %q（末尾より後ろにならない）", got)
		}
	})
}

// 間が詰まったら全体を振り直し、rebalanced=true を返す（9.4）。
//
// **「間が作れない」を本当に作る。** 同じ2つのキーを並べるだけでは、片側が
// 末尾なら Between が普通に成功してしまう（前の版はそれで筋書きが成立して
// いなかった）。上限（MaxBodyLen）ちょうどの深さで隣り合う2つを用意する。
func TestMoveTicketRebalancesWhenExhausted(t *testing.T) {
	deep := "0|" + strings.Repeat("b", lexorank.MaxBodyLen) + ":"
	deepNext := "0|" + strings.Repeat("b", lexorank.MaxBodyLen-1) + "c:"
	if _, ok := lexorank.Between(deep, deepNext); ok {
		t.Fatalf("この2つの間はまだ作れる。筋書きが成立していない: %q %q", deep, deepNext)
	}

	q := moveFake()
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{
		31: {ID: testTicketID, SortKey: txt("0|zz:"), Version: 3},
		44: {ID: testTicketID2, SortKey: txt(deep), Version: 1},
		55: {ID: "01K2TKT00000000000000055", SortKey: txt(deepNext), Version: 1},
	}
	q.ticket.idsInOrder = []string{testTicketID2, "01K2TKT00000000000000055", testTicketID}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"after_seq":44}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeMove(t, rec)
	if !resp.Rebalanced {
		t.Error("rebalanced=false（振り直したなら true を返すこと）")
	}
	if len(q.ticket.setSortKey) != 3 {
		t.Errorf("SetTicketSortKey の回数 = %d（全行に振り直すこと）", len(q.ticket.setSortKey))
	}
	// 動かすのは1件だけ（振り直しは version を上げない。9.4）
	if countOps(q.opLog, "MoveTicket") != 1 {
		t.Errorf("MoveTicket の回数 = %d（動かした1件だけ）", countOps(q.opLog, "MoveTicket"))
	}
	if resp.Version != 4 {
		t.Errorf("version = %d（動かした1件だけ +1）", resp.Version)
	}
}

// sort_key を持たない行（手で入れた行・16a 以前の行）でも回復する。
func TestMoveTicketRecoversFromMissingSortKey(t *testing.T) {
	q := moveFake()
	q.ticket.sortRowBySeq[31] = gen.GetTicketSortRowRow{ID: testTicketID, Version: 1}
	q.ticket.sortRowBySeq[44] = gen.GetTicketSortRowRow{ID: testTicketID2, Version: 1}
	q.ticket.idsInOrder = []string{testTicketID2, testTicketID}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"after_seq":44}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !decodeMove(t, rec).Rebalanced {
		t.Error("sort_key が空の行を振り直していない")
	}
}

func TestMoveTicketValidation(t *testing.T) {
	cases := []struct{ name, body, field string }{
		{"指定が無い", `{}`, "position"},
		{"同時指定", `{"position":"first","after_seq":44}`, "position"},
		{"position が値域外", `{"position":"middle"}`, "position"},
		{"自分自身が基準", `{"after_seq":31}`, "after_seq"},
		{"基準が無い", `{"after_seq":999}`, "after_seq"},
		// **parent_seq は null しか受け取らない**（9.4.2）。数値を黙って捨てると、
		// 送った側は「親を変えたつもり」のまま位置だけ動いた結果を受け取る
		{"parent_seq に数値", `{"parent_seq":44,"after_seq":44}`, "parent_seq"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := moveFake()
			h, tx := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.moveTicket(rec, ticketReq(http.MethodPost,
				"/projects/demo/tickets/31/move", tc.body, "31"))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d（422 のはず）body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Errorf("details に %s が無い: %s", tc.field, rec.Body.String())
			}
			if tx.committed {
				t.Error("検証に失敗したのにコミットしている")
			}
		})
	}
}

// parent_seq: null は「ルートにする」を同じ文で送る（9.4.2）。
//
// **位置と一緒に決まるものを1本で送る**——2本に分けると「ルートにはなったが
// 位置は元のまま」が残りうる（9.4.1 の staged と同じ理由）。
func TestMoveTicketUnparent(t *testing.T) {
	q := moveFake()
	h, tx := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"parent_seq":null,"after_seq":44}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（200 のはず）body = %s", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Fatal("コミットしていない")
	}
	if len(q.ticket.moved) != 1 {
		t.Fatalf("MoveTicket = %d回, want 1", len(q.ticket.moved))
	}
	if !q.ticket.moved[0].Unparent {
		t.Error("Unparent が false（親を外す指定が SQL へ渡っていない）")
	}
	// **位置も同じ文で決まる。** sort_key が動いていることまで見る
	if q.ticket.moved[0].SortKey.String == "" {
		t.Error("sort_key が空（位置が決まっていない）")
	}
}

// parent_seq を省いたときは親を触らない（9.4.2）。
//
// **省略と null の区別が効いていることの実測である。** 区別を落とすと、
// ただの並べ替えが親まで外してしまう。
func TestMoveTicketWithoutParentSeqKeepsParent(t *testing.T) {
	q := moveFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"after_seq":44}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（200 のはず）body = %s", rec.Code, rec.Body.String())
	}
	if q.ticket.moved[0].Unparent {
		t.Error("parent_seq を送っていないのに Unparent が true")
	}
}

// **段に置けるかは、親を外した後の状態で判定する**（9.4.2）。
//
// 親を持つ行は本来オンステージへ上げられない（not_stageable）が、**同じ
// リクエストで親を外すなら上げてよい**——同じトランザクションで両方が確定する
// ので、途中の状態は存在しない。**外す前の親を見て弾いてはならない。**
func TestMoveTicketUnparentAllowsStaging(t *testing.T) {
	q := moveFake()
	// 31 は「タスクで、親がストーリー」＝そのままでは段に置けない行にする
	row := q.ticket.sortRowBySeq[31]
	row.Type = "task"
	row.ParentType = txt("story")
	q.ticket.sortRowBySeq[31] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move",
		`{"parent_seq":null,"staged":true,"position":"first"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（200 のはず）body = %s", rec.Code, rec.Body.String())
	}
	if !q.ticket.moved[0].Unparent {
		t.Error("Unparent が false")
	}
}

// 親を外さずに段へ上げようとすると、従来どおり not_stageable（9.4.1）。
func TestMoveTicketStagingChildStillRejected(t *testing.T) {
	q := moveFake()
	row := q.ticket.sortRowBySeq[31]
	row.Type = "task"
	row.ParentType = txt("story")
	q.ticket.sortRowBySeq[31] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"staged":true,"position":"first"}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d（422 のはず）body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not_stageable") {
		t.Errorf("not_stageable が返っていない: %s", rec.Body.String())
	}
}

// 無いチケットは 404（他プロジェクトの番号も同じ結果へ寄せる）。
func TestMoveTicketNotFound(t *testing.T) {
	q := moveFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/999/move", `{"position":"last"}`, "999"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d（404 のはず）body = %s", rec.Code, rec.Body.String())
	}
}

// 並べ替えは activity に記録しない。
func TestMoveTicketDoesNotRecordActivity(t *testing.T) {
	q := moveFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"position":"last"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("並べ替えを activity に記録している: %+v", q.ticket.activities)
	}
}

// ── オンステージ（手順16d。ApiDesign.md 9.4.1）────────────────

// stageFake は段の検証用。
//
//   - 31 … task。親を持たない（段に置ける）。バックログ
//   - 44 … task。親がエピック（段に置ける）。**既にオンステージ**
//   - 45 … task。親がタスク（段に置けない）。バックログ
//   - 12 … epic。親を持たないが**エピック自身なので置けない**
func stageFake() *fakeQuerier {
	q := ticketFake()
	staged := ts(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{
		31: {ID: testTicketID, Type: "task", SortKey: txt("0|n:"), Version: 3},
		44: {ID: testTicketID2, Type: "task", SortKey: txt("0|u:"), Version: 1,
			StagedAt: staged, ParentType: txt("epic")},
		45: {ID: testTicketID3, Type: "task", SortKey: txt("0|w:"), Version: 1,
			ParentType: txt("task")},
		12: {ID: testTicketID4, Type: "epic", SortKey: txt("0|g:"), Version: 1},
	}
	return q
}

// **エピック自身は段に置けない**（9.4.1）。親を持たないので親の種別だけでは
// 通ってしまうが、どちらの段にも行として出ないため上げても見えない
// （GuiDesign.md 5.4）。dev seed の検証と同じ規則である。
func TestMoveTicketRejectsStagingEpic(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/12/move", `{"staged":true,"position":"last"}`, "12"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d（422 であること）, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not_stageable") {
		t.Errorf("details に not_stageable が無い: %s", body)
	}
	if !strings.Contains(body, "エピック") {
		t.Errorf("エピック固有の理由になっていない: %s", body)
	}
	if len(q.ticket.moved) != 0 {
		t.Errorf("弾いたのに更新している: %+v", q.ticket.moved)
	}
}

// staged:true でオンステージへ上がり、staged_at が入る（9.4.1）。
func TestMoveTicketStagesTicket(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"staged":true,"position":"last"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeMove(t, rec)
	if resp.StagedAt == nil {
		t.Fatal("staged_at が null（オンステージへ上げたのに時刻が入っていない）")
	}
	if len(q.ticket.moved) != 1 || !q.ticket.moved[0].ChangeStage {
		t.Errorf("change_stage が立っていない: %+v", q.ticket.moved)
	}
	if !q.ticket.moved[0].StagedAt.Valid {
		t.Error("staged_at に値を書いていない")
	}
}

// staged:false でバックログへ戻り、staged_at が NULL になる（9.4.1）。
func TestMoveTicketUnstagesTicket(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/44/move", `{"staged":false,"position":"first"}`, "44"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeMove(t, rec); resp.StagedAt != nil {
		t.Errorf("staged_at = %v（バックログへ戻したら null）", resp.StagedAt)
	}
	if len(q.ticket.moved) != 1 || !q.ticket.moved[0].ChangeStage {
		t.Fatalf("change_stage が立っていない: %+v", q.ticket.moved)
	}
	if q.ticket.moved[0].StagedAt.Valid {
		t.Error("staged_at を NULL にしていない")
	}
}

// staged を省略すると段は変わらない（9.4.1）。並べ替えだけを行う。
func TestMoveTicketWithoutStagedKeepsStage(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/44/move", `{"position":"first"}`, "44"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(q.ticket.moved) != 1 || q.ticket.moved[0].ChangeStage {
		t.Errorf("staged を省略したのに段を書き換えている: %+v", q.ticket.moved)
	}
	// 元がオンステージなので、応答も入ったままであること
	if decodeMove(t, rec).StagedAt == nil {
		t.Error("staged_at が消えた（省略時は現在値のまま。9.4.1）")
	}
}

// position は段の中で解釈する（9.4.1）。
//
// **「効く」ことを先に確かめてから「段で絞れている」ことを見る**
// （LEARNINGS #35）。31 をオンステージの先頭へ入れると、オンステージに居る
// 44（0|u:）より前でありながら、バックログの 45（0|w:）は基準にならない。
func TestMoveTicketPositionIsScopedToStage(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/31/move", `{"staged":true,"position":"last"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := decodeMove(t, rec).SortKey
	// オンステージの末尾は 44（0|u:）なので、その後ろに入る。
	if got <= "0|u:" {
		t.Errorf("sort_key = %q（オンステージの末尾 0|u: より後ろに入らない）", got)
	}
	// **バックログの 45（0|w:）を末尾として使っていないこと。** 段で絞らずに
	// プロジェクト全体の max を取ると 0|w: より後ろへ入ってしまう。
	if got >= "0|w:" {
		t.Errorf("sort_key = %q（バックログの行を基準にしている。段で絞れていない）", got)
	}
	if countOps(q.opLog, "MaxTicketSortKeyInStage") != 1 {
		t.Errorf("段で絞った max を引いていない: %v", q.opLog)
	}
}

// 表示上のトップレベルでないものは段に置けない（9.4.1 の not_stageable）。
func TestMoveTicketRejectsStagingChild(t *testing.T) {
	q := stageFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/45/move", `{"staged":true,"position":"last"}`, "45"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d（422 であること）, body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "not_stageable") {
		t.Errorf("details に not_stageable が無い: %s", body)
	}
	if len(q.ticket.moved) != 0 {
		t.Errorf("弾いたのに更新している: %+v", q.ticket.moved)
	}
}

// **親がエピックなら段に置ける**（9.4.1）。
//
// 上の「置けない」を測る前提として、**置ける側が通ることを先に確かめる**
// ——これが無いと、実装が常に弾いていても not_stageable の検証は通る
// （LEARNINGS #35）。
func TestMoveTicketAllowsStagingChildOfEpic(t *testing.T) {
	q := stageFake()
	// 44 をいったんバックログへ落としてから、上げ直せることを見る
	row := q.ticket.sortRowBySeq[44]
	row.StagedAt = pgtype.Timestamptz{}
	q.ticket.sortRowBySeq[44] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/44/move", `{"staged":true,"position":"last"}`, "44"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（親がエピックなら置ける）, body = %s", rec.Code, rec.Body.String())
	}
	if decodeMove(t, rec).StagedAt == nil {
		t.Error("staged_at が入っていない")
	}
}

// **バックログへ戻すのは常に許す**（9.4.1）。段から降ろすだけなので、
// 置ける条件を問う理由がない。
func TestMoveTicketUnstageIsAlwaysAllowed(t *testing.T) {
	q := stageFake()
	// 45（親がタスク）が何らかの理由でオンステージに居る状態を作る
	row := q.ticket.sortRowBySeq[45]
	row.StagedAt = ts(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	q.ticket.sortRowBySeq[45] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.moveTicket(rec, ticketReq(http.MethodPost,
		"/projects/demo/tickets/45/move", `{"staged":false,"position":"last"}`, "45"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（降ろすのは常に許す）, body = %s", rec.Code, rec.Body.String())
	}
}

// countOps は opLog に name が何回現れたかを数える。
func countOps(opLog []string, name string) int {
	n := 0
	for _, op := range opLog {
		if op == name {
			n++
		}
	}
	return n
}

// バックログ検索は専用の検索対象と祖先補完を選び、空白なら検索しない。
func TestListTicketsBacklogSearch(t *testing.T) {
	for _, keyword := range []string{"認証　100%", "　 "} {
		t.Run(keyword, func(t *testing.T) {
			q := ticketFake()
			q.ticket.searchIDs = []string{testTicketID}
			h, _ := ticketHandler(q)
			rec := httptest.NewRecorder()
			h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets?search_mode=backlog&q="+url.QueryEscape(keyword), "", ""))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			p := q.ticket.listParams[0]
			if strings.TrimSpace(keyword) == "" {
				if p.BacklogSearch || p.KeywordSet || len(q.ticket.backlogSearchParams) != 0 {
					t.Fatal("空白で検索した")
				}
				return
			}
			if !p.BacklogSearch || !p.KeywordSet || !slices.Equal(p.KeywordIds, q.ticket.searchIDs) {
				t.Fatalf("検索の引数=%+v", p)
			}
			if len(q.ticket.backlogSearchParams) != 1 {
				t.Fatal("バックログ検索へ渡らない")
			}
			got := q.ticket.backlogSearchParams[0]
			if got.ProjectID != testProjectID || !slices.Equal(got.Patterns, []string{`%認証%`, `%100\%%`}) {
				t.Fatalf("パターン=%+v", got)
			}
			if len(q.ticket.searchParams) != 0 || len(q.ticket.trigramSearchParams) != 0 {
				t.Fatal("通常検索を呼んだ")
			}
		})
	}
}
