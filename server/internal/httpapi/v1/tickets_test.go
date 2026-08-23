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
	if p.OpenFilter != "all" || p.DueWithinDays != -1 || p.ParentSeq != 0 {
		t.Errorf("未指定のフィルタが効いている: %+v", p)
	}
}

// 同じ条件の複数指定は OR、me / none は別扱い（9.2.1）。
func TestListTicketsFilters(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet,
		"/projects/demo/tickets?type=bug,task&priority=high,highest"+
			"&assignee=me,none&tag=01K2TAG00000000000000001,none&sprint=none"+
			"&open=true&due_within=7d&parent=12&status=todo,in_progress"+
			"&status_category=todo", "", ""))

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
	if p.OpenFilter != "open" || p.DueWithinDays != 7 || p.ParentSeq != 12 {
		t.Errorf("open/due_within/parent の解釈が違う: %+v", p)
	}
}

// 解釈できない値は既定へ丸めず 422（2.6）。**項目ごとに details を並べる。**
func TestListTicketsRejectsInvalidFilters(t *testing.T) {
	cases := []struct{ name, query, field string }{
		{"種別", "type=epic,unknown", "type"},
		{"優先度", "priority=urgent", "priority"},
		{"分類", "status_category=blocked", "status_category"},
		{"open", "open=yes", "open"},
		{"期限", "due_within=7days", "due_within"},
		{"親", "parent=0", "parent"},
		{"ソート", "sort=body_md", "sort"},
		{"件数", "per_page=201", "per_page"},
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

	base := etag("type=bug")
	if !strings.HasPrefix(base, `W/"tkt-`) {
		t.Errorf("ETag が弱い検証子の形になっていない: %q", base)
	}
	if other := etag("type=task"); other == base {
		t.Errorf("フィルタが違うのに ETag が同じ: %q", base)
	}
	if other := etag("type=bug&page=2"); other == base {
		t.Errorf("ページが違うのに ETag が同じ: %q", base)
	}
	if other := etag("type=bug&order=desc"); other == base {
		t.Errorf("並びが違うのに ETag が同じ: %q", base)
	}
	// 同じ意味の違う書き方は同じ ETag（正規化して混ぜているため）
	if a, b := etag("type=bug,task"), etag("type=task,bug"); a != b {
		t.Errorf("順序違いの同じ条件で ETag が変わった: %q vs %q", a, b)
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

// 作成は 9.5 形式で返す。dod / links / comment_count は空・0（手順18 まで）。
func TestCreateTicketRespondsWithDetailShape(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReq(http.MethodPost, "/projects/demo/tickets",
		`{"type":"bug","title":"落ちる"}`, ""))

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
		t.Errorf("dod = %v（手順18 までは空配列）", body["dod"])
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
		{
			name:  "スプリントが他プロジェクト",
			body:  `{"type":"task","title":"x","sprint_id":"01K2SPR00000000000000009"}`,
			setup: func(q *fakeQuerier) { q.ticket.sprintExists = false },
			field: "sprint_id",
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

// ── POST /tickets/{seq}/move（9.4）──────────────────────────

func moveFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.sortRowBySeq = map[int32]gen.GetTicketSortRowRow{
		31: {ID: testTicketID, SortKey: txt("0|n:"), Version: 3},
		44: {ID: testTicketID2, SortKey: txt("0|u:"), Version: 1},
	}
	return q
}

func decodeMove(t *testing.T, rec *httptest.ResponseRecorder) moveTicketResponse {
	t.Helper()
	var v moveTicketResponse
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

// 並べ替えは activity に記録しない（利用者の判断、2026-08-23）。
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
