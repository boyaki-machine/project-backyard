package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// チケット間リンクAPI（ApiDesign.md 9.10.1）の単体テスト。手順18a。
//
// 認可（ticket.view / ticket.edit）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、双方向の返し方・
// 重複の 409・自己リンクの 422・direction を問わない削除、である。

const (
	testLinkID  = "01K2LNK0000000000000000001"
	testLinkID2 = "01K2LNK0000000000000000002"
)

// linkFake は seq=31（自分）と seq=12 / 45（相手）が解決できる状態を返す。
func linkFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticket.idBySeq[12] = testTicketID4
	q.ticket.idBySeq[45] = testTicketID3
	q.ticket.briefByID[testTicketID4] = gen.GetTicketBriefRow{
		Seq: 12, Title: "DB設計", Type: "story", StatusKey: "done",
	}
	q.ticket.briefByID[testTicketID3] = gen.GetTicketBriefRow{
		Seq: 45, Title: "ticketテーブル定義", Type: "task", StatusKey: "todo",
	}
	return q
}

// sampleLink は 9.10.1 の応答例に合わせた行。
func sampleLink(id, direction, linkType string, rank int32, seq int32, title, tp string) gen.ListTicketLinksRow {
	return gen.ListTicketLinksRow{
		ID: id, DirectionRank: rank, Direction: direction,
		LinkType: linkType, LagDays: 0, Origin: linkOriginHuman,
		CreatedAt: ts(baseTime),
		TicketSeq: seq, TicketTitle: title, TicketType: tp,
		TicketStatusKey: "todo", TicketStatusName: txt("未着手"),
		TicketStatusCategory: txt("todo"),
	}
}

// linkJSON は応答を読むための型（v1.Time は読み戻せないので時刻は string）。
type linkJSON struct {
	ID        string          `json:"id"`
	Direction string          `json:"direction"`
	LinkType  string          `json:"link_type"`
	Ticket    linkTicketBrief `json:"ticket"`
	LagDays   int32           `json:"lag_days"`
	Origin    string          `json:"origin"`
	CreatedAt string          `json:"created_at"`
}

type linkListJSON struct {
	Items []linkJSON `json:"items"`
}

func decodeLink(t *testing.T, rec *httptest.ResponseRecorder) linkJSON {
	t.Helper()
	var v linkJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

func decodeLinkList(t *testing.T, rec *httptest.ResponseRecorder) linkListJSON {
	t.Helper()
	var v linkListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET（9.10.1）────────────────────────────────────────────

// **双方向を1本で返し、相手が ticket に入る**（9.10.1）。
func TestListLinksReturnsBothDirections(t *testing.T) {
	q := linkFake()
	q.ticket.linkRows = []gen.ListTicketLinksRow{
		sampleLink(testLinkID, "outgoing", "blocks", 0, 45, "ticketテーブル定義", "task"),
		sampleLink(testLinkID2, "incoming", "blocks", 1, 12, "DB設計", "story"),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketLinks(rec, cmtReq(http.MethodGet, "/links", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeLinkList(t, rec)
	if len(got.Items) != 2 {
		t.Fatalf("items = %d件, want 2（双方向）", len(got.Items))
	}
	if got.Items[0].Direction != "outgoing" || got.Items[1].Direction != "incoming" {
		t.Errorf("direction の並び = %q, %q, want outgoing, incoming",
			got.Items[0].Direction, got.Items[1].Direction)
	}
	// **ticket に入るのは相手であって自分ではない**（seq=31 は出ない）。
	for _, it := range got.Items {
		if it.Ticket.Seq == 31 {
			t.Errorf("ticket に自分（seq=31）が入っている: %+v", it)
		}
	}
	// type を持つ（画面が種別アイコンを出すため。9.10.1）。
	if got.Items[0].Ticket.Type != "task" || got.Items[1].Ticket.Type != "story" {
		t.Errorf("ticket.type = %q, %q", got.Items[0].Ticket.Type, got.Items[1].Ticket.Type)
	}
	// status は key / name / category の3つ（9.5.1 の parent と同じ形）。
	if got.Items[0].Ticket.Status.Key != "todo" || got.Items[0].Ticket.Status.Name != "未着手" {
		t.Errorf("status = %+v", got.Items[0].Ticket.Status)
	}
}

func TestListLinksEmpty(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketLinks(rec, cmtReq(http.MethodGet, "/links", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// items は null ではなく空配列。
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("items が空配列でない: %s", rec.Body.String())
	}
}

// ── POST（9.10.1）───────────────────────────────────────────

func TestCreateLink(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":12,"link_type":"blocks"}`, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.linkCreated) != 1 {
		t.Fatalf("CreateTicketLink = %d回, want 1", len(q.ticket.linkCreated))
	}
	arg := q.ticket.linkCreated[0]
	// **当該チケットが常に source になる**（9.10.1）。
	if arg.SourceTicketID != testTicketID || arg.TargetTicketID != testTicketID4 {
		t.Errorf("source/target = %q/%q", arg.SourceTicketID, arg.TargetTicketID)
	}
	if arg.Origin != linkOriginHuman {
		t.Errorf("origin = %q, want human（Phase 1）", arg.Origin)
	}
	got := decodeLink(t, rec)
	if got.Direction != "outgoing" || got.Ticket.Seq != 12 {
		t.Errorf("応答 = %+v, want outgoing/seq=12", got)
	}
	if rec.Header().Get("Location") == "" {
		t.Error("Location が空")
	}
}

// **API は7種すべて受ける**（画面が3種に絞るのは 5.5 の話。9.10.1）。
func TestCreateLinkAcceptsAllTypes(t *testing.T) {
	for _, lt := range linkTypes {
		q := linkFake()
		h, _ := ticketHandler(q)
		rec := httptest.NewRecorder()
		h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
			`{"target_seq":12,"link_type":"`+lt+`","lag_days":3}`, "31", ""))

		if rec.Code != http.StatusCreated {
			t.Fatalf("link_type=%s: status = %d, want 201 (%s)", lt, rec.Code, rec.Body.String())
		}
		if got := q.ticket.linkCreated[0].LagDays; got != 3 {
			t.Errorf("link_type=%s: lag_days = %d, want 3", lt, got)
		}
	}
}

// **自分自身は self_link で 422**（9.10.1 / 9.14）。
func TestCreateLinkRejectsSelf(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":31,"link_type":"relates"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "self_link") {
		t.Errorf("details[].code に self_link が無い: %s", rec.Body.String())
	}
	if len(q.ticket.linkCreated) != 0 {
		t.Errorf("作られてしまった")
	}
}

// **同一プロジェクトに無い相手は not_found で 422**（9.10.1 / 9.14）。
func TestCreateLinkRejectsUnknownTarget(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":999,"link_type":"relates"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("details[].code に not_found が無い: %s", rec.Body.String())
	}
}

func TestCreateLinkRejectsUnknownType(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":12,"link_type":"depends"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateLinkRequiresFields(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links", `{}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	// **2件まとめて返す**（1件ずつだと直すたびに往復が要る。2.5）。
	body := rec.Body.String()
	if !strings.Contains(body, `"target_seq"`) || !strings.Contains(body, `"link_type"`) {
		t.Errorf("details に両方が並んでいない: %s", body)
	}
}

// **重複は 409 already_exists**（9.10.1）。
func TestCreateLinkDuplicateIsConflict(t *testing.T) {
	q := linkFake()
	q.ticket.linkExists = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":12,"link_type":"blocks"}`, "31", ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already_exists") {
		t.Errorf("error.code = %s, want already_exists", rec.Body.String())
	}
	if len(q.ticket.linkCreated) != 0 {
		t.Errorf("作られてしまった")
	}
}

func TestCreateLinkRecordsActivity(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketLink(rec, cmtReq(http.MethodPost, "/links",
		`{"target_seq":12,"link_type":"blocks"}`, "31", ""))

	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "update" || !a.Field.Valid || a.Field.String != "link" {
		t.Errorf("action/field = %q/%+v, want update/link", a.Action, a.Field)
	}
	// 要約は「<link_type> <相手の完全形ID>」（9.10.1）。
	if got := a.NewValue.String; got != "blocks demo-12" {
		t.Errorf("new_value = %q, want %q", got, "blocks demo-12")
	}
	if a.OldValue.Valid {
		t.Errorf("old_value = %q, want NULL（追加）", a.OldValue.String)
	}
	// **相手のチケットの履歴には書かない**（1行だけ。9.10.1）。
	if a.EntityID != testTicketID {
		t.Errorf("entity_id = %q, want 操作したチケット %q", a.EntityID, testTicketID)
	}
}

// ── DELETE（9.10.1）─────────────────────────────────────────

func TestDeleteOutgoingLink(t *testing.T) {
	q := linkFake()
	q.ticket.linkRows = []gen.ListTicketLinksRow{
		sampleLink(testLinkID, "outgoing", "blocks", 0, 45, "ticketテーブル定義", "task"),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketLink(rec, cmtReq(http.MethodDelete, "/links/"+testLinkID, "", "31", testLinkID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "blocks demo-45" {
		t.Errorf("old_value = %q, want %q", a.OldValue.String, "blocks demo-45")
	}
	if a.NewValue.Valid {
		t.Errorf("new_value = %q, want NULL（削除）", a.NewValue.String)
	}
}

// **incoming の行も同じエンドポイントから消せる**（9.10.1）。
func TestDeleteIncomingLink(t *testing.T) {
	q := linkFake()
	q.ticket.linkRows = []gen.ListTicketLinksRow{
		sampleLink(testLinkID2, "incoming", "blocks", 1, 12, "DB設計", "story"),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketLink(rec, cmtReq(http.MethodDelete, "/links/"+testLinkID2, "", "31", testLinkID2))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（incoming も消せる） (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.linkRows) != 0 {
		t.Errorf("残っている: %+v", q.ticket.linkRows)
	}
}

func TestDeleteLinkNotFound(t *testing.T) {
	q := linkFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketLink(rec, cmtReq(http.MethodDelete, "/links/"+testLinkID, "", "31", testLinkID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0", len(q.ticket.activities))
	}
}
