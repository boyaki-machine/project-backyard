package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ── GET /tickets?view=gantt（ApiDesign.md 9.2.6）───────────────────────

// ganttRespJSON は view=gantt の応答を読み戻す型。
type ganttRespJSON struct {
	Items   []ticketItemJSON `json:"items"`
	Links   []ganttLinkItem  `json:"links"`
	Page    int              `json:"page"`
	PerPage int              `json:"per_page"`
	Total   int              `json:"total"`
}

func getGantt(t *testing.T, h *handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.listTickets(rec, ticketReq(http.MethodGet, "/projects/demo/tickets?"+query, "", ""))
	return rec
}

func decodeGantt(t *testing.T, rec *httptest.ResponseRecorder) ganttRespJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var v ganttRespJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// view=gantt の既定：上限 5,000・棚に戻ったものを含む・links は空でも配列（9.2.6）。
func TestListTicketsGanttDefaults(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := getGantt(t, h, "view=gantt")
	v := decodeGantt(t, rec)

	p := q.ticket.listParams[0]
	if p.PageLimit != ganttMaxTickets || p.PageOffset != 0 {
		t.Errorf("LIMIT/OFFSET = %d/%d（9.2.6 は %d/0）", p.PageLimit, p.PageOffset, ganttMaxTickets)
	}
	if v.PerPage != ganttMaxTickets || v.Page != 1 {
		t.Errorf("page/per_page = %d/%d", v.Page, v.PerPage)
	}
	if !p.IncludeRetired {
		t.Error("view=gantt で棚に戻ったものを既定で含んでいない（9.2.6）")
	}
	if p.Sort != "sort_key" || p.SortOrder != "asc" {
		t.Errorf("並びの既定 = %s/%s（一覧と同じ sort_key/asc のはず）", p.Sort, p.SortOrder)
	}
	// **links は 0件でも null にしない。** 画面が配列として回す
	if !strings.Contains(rec.Body.String(), `"links":[]`) {
		t.Errorf("0件の links が [] になっていない: %s", rec.Body.String())
	}
	// チケットが0件なら依存を引きに行かない
	if slices.Contains(q.opLog, "ListLinksAmongTickets") {
		t.Error("チケットが0件なのに依存を引いた")
	}
}

// retired=false を明示すれば、view=gantt でも棚に戻ったものを外す（9.2.6）。
func TestListTicketsGanttRetiredFalse(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	decodeGantt(t, getGantt(t, h, "view=gantt&retired=false"))
	if q.ticket.listParams[0].IncludeRetired {
		t.Error("retired=false が効いていない")
	}
}

// view=gantt でも、通常の一覧の既定は変わらない（棚に戻ったものは外す・上限 200）。
func TestListTicketsWithoutViewKeepsListDefaults(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := getGantt(t, h, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	p := q.ticket.listParams[0]
	if p.IncludeRetired || p.PageLimit != 200 {
		t.Errorf("通常の一覧の既定が変わった: retired=%v limit=%d", p.IncludeRetired, p.PageLimit)
	}
	if strings.Contains(rec.Body.String(), `"links"`) {
		t.Errorf("view なしの応答に links が載っている: %s", rec.Body.String())
	}
}

// 受けない値は 422。page / per_page は黙って無視しない（9.2.6）。
func TestListTicketsGanttRejects(t *testing.T) {
	cases := []struct {
		name, query string
		fields      []string
	}{
		{"view の他の値", "view=board", []string{"view"}},
		{"page", "view=gantt&page=2", []string{"page"}},
		{"page=1 でも受けない", "view=gantt&page=1", []string{"page"}},
		{"per_page", "view=gantt&per_page=10", []string{"per_page"}},
		// 絞り込みの誤りと一緒でも、1つの 422 にまとめる（2.5）
		{"絞り込みの誤りとまとめる", "view=gantt&per_page=10&type=nope", []string{"per_page", "type"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := ticketFake()
			h, _ := ticketHandler(q)
			rec := getGantt(t, h, tc.query)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d（422 のはず）body = %s", rec.Code, rec.Body.String())
			}
			for _, f := range tc.fields {
				if !strings.Contains(rec.Body.String(), `"`+f+`"`) {
					t.Errorf("details に %s が無い: %s", f, rec.Body.String())
				}
			}
			if len(q.ticket.listParams) != 0 {
				t.Error("422 なのに一覧を引いた")
			}
		})
	}
}

// 依存は返したチケットの ID で1回だけ引き、形を変えずに links へ載せる（9.2.6）。
func TestListTicketsGanttLinks(t *testing.T) {
	q := ticketFake()
	a := sampleTicketRow("01K2TICKET0000000000000218", 218, "デザイン案を作る")
	b := sampleTicketRow("01K2TICKET0000000000000220", 220, "ガント画面（閲覧）")
	q.ticket.rows = []gen.ListTicketsRow{a, b}
	q.ticket.ganttLinks = []gen.ListLinksAmongTicketsRow{
		{ID: "01K2LINK00000000000000000A", SourceSeq: 218, TargetSeq: 220, LinkType: "FS",
			LagDays: 2, Origin: "ai_suggested", CreatedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
	}
	h, _ := ticketHandler(q)
	v := decodeGantt(t, getGantt(t, h, "view=gantt"))

	if n := strings.Count(strings.Join(q.opLog, ","), "ListLinksAmongTickets"); n != 1 {
		t.Errorf("依存を %d 回引いた（1回のはず）", n)
	}
	if got := q.ticket.ganttLinkIDsIn[0]; !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Errorf("依存を引いた ID = %v（返したチケットの ID のはず）", got)
	}
	want := ganttLinkItem{ID: "01K2LINK00000000000000000A", SourceSeq: 218, TargetSeq: 220,
		LinkType: "FS", LagDays: 2, Origin: "ai_suggested"}
	if len(v.Links) != 1 || v.Links[0] != want {
		t.Errorf("links = %+v、want %+v", v.Links, want)
	}
	if len(v.Items) != 2 {
		t.Errorf("items = %d 件", len(v.Items))
	}
}

// ETag は依存の件数と MAX(created_at) で変わる（9.2.5）。依存の増減は
// チケットの updated_at を動かさないため。
func TestListTicketsGanttETagFollowsLinks(t *testing.T) {
	q := ticketFake()
	q.ticket.rows = []gen.ListTicketsRow{sampleTicketRow(testTicketID, 31, "A")}
	h, _ := ticketHandler(q)
	etag := func() string {
		rec := getGantt(t, h, "view=gantt")
		decodeGantt(t, rec)
		return rec.Header().Get("ETag")
	}

	none := etag()
	q.ticket.ganttLinks = []gen.ListLinksAmongTicketsRow{
		{ID: "L1", SourceSeq: 31, TargetSeq: 31, LinkType: "FS", CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}
	one := etag()
	q.ticket.ganttLinks[0].CreatedAt = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	later := etag()

	if none == one || one == later {
		t.Errorf("依存が変わっても ETag が変わらない: %s / %s / %s", none, one, later)
	}
	if !strings.HasPrefix(one, `W/"tkt-`) || !strings.HasSuffix(one, `"`) || strings.Count(one, `"`) != 2 {
		t.Errorf("弱い検証子の形が崩れた: %s", one)
	}
	// 同じチケットでも view の有無で ETag が違う（本文が違うため）
	rec := getGantt(t, h, "")
	if rec.Header().Get("ETag") == none {
		t.Error("view の有無で ETag が同じ")
	}
}

// sprint=active はフラグへ振り分け、ULID の配列に混ぜない（9.2.1）。
func TestListTicketsSprintActive(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := getGantt(t, h, "sprint=active,none,01K2SPRINT0000000000000001")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	p := q.ticket.listParams[0]
	if !p.SprintActive || !p.SprintNone || !slices.Equal(p.SprintIds, []string{"01K2SPRINT0000000000000001"}) {
		t.Errorf("sprint=active,none,<ULID> の解釈が違う: active=%v none=%v ids=%v",
			p.SprintActive, p.SprintNone, p.SprintIds)
	}

	// 指定しなければ効かない
	q2 := ticketFake()
	h2, _ := ticketHandler(q2)
	getGantt(t, h2, "")
	if q2.ticket.listParams[0].SprintActive {
		t.Error("sprint 未指定で SprintActive が立っている")
	}

	// ETag は sprint=active の有無で変わる
	q3 := ticketFake()
	q3.ticket.rows = []gen.ListTicketsRow{sampleTicketRow(testTicketID, 31, "A")}
	h3, _ := ticketHandler(q3)
	if getGantt(t, h3, "sprint=active").Header().Get("ETag") == getGantt(t, h3, "").Header().Get("ETag") {
		t.Error("sprint=active の有無で ETag が同じ")
	}
}

// ganttLinkTypes と SQL の IN 句が同じ種別を並べている（GuiDesign.md 5.14）。
// **期待値を試験に書かず、定義（link.sql）から読んで突き合わせる。**
func TestGanttLinkTypesMatchQuery(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "store", "queries", "link.sql"))
	if err != nil {
		t.Fatalf("link.sql を読めない: %v", err)
	}
	quoted := make([]string, 0, len(ganttLinkTypes))
	for _, lt := range ganttLinkTypes {
		quoted = append(quoted, "'"+lt+"'")
	}
	want := "l.link_type IN (" + strings.Join(quoted, ", ") + ")"
	if !strings.Contains(string(raw), want) {
		t.Errorf("link.sql の ListLinksAmongTickets に %q が無い", want)
	}
}
