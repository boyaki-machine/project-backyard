package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 完了条件（DoD）API（ApiDesign.md 9.9）の単体テスト。手順18a。
//
// 認可（ticket.view / ticket.edit）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、type の絞り込み・
// is_satisfied と satisfied_at / satisfied_by が同時に動くこと・
// 並び順だけの変更を activity に書かないこと、である。

const (
	testDoDID  = "01K2DOD0000000000000000001"
	testDoDID2 = "01K2DOD0000000000000000002"
)

// dodFake は seq=31 のチケットが解決できる状態のフェイクを返す。
func dodFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	return q
}

// sampleDoD は 9.9 の応答例に合わせた行。
func sampleDoD(id, body string, satisfied bool, sortOrder int32) gen.GetTicketDoDItemRow {
	row := gen.GetTicketDoDItemRow{
		ID: id, Type: dodTypeManual, Body: body,
		IsSatisfied: satisfied, SortOrder: sortOrder,
		CreatedAt: ts(baseTime), UpdatedAt: ts(baseTime),
	}
	if satisfied {
		row.SatisfiedAt = tsp(baseTime)
		row.SatisfiedBy = txt(testActorID)
		row.SatisfiedByKind = txt("user")
		row.SatisfiedByName = txt("田中")
	}
	return row
}

// dodJSON は応答を読むための型（v1.Time は読み戻せないので時刻は string）。
type dodJSON struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Body        string    `json:"body"`
	IsSatisfied bool      `json:"is_satisfied"`
	SatisfiedAt *int64    `json:"satisfied_at"`
	SatisfiedBy *actorRef `json:"satisfied_by"`
	SortOrder   int32     `json:"sort_order"`
	CreatedAt   int64     `json:"created_at"`
	UpdatedAt   int64     `json:"updated_at"`
}

type dodListJSON struct {
	Items []dodJSON `json:"items"`
}

func decodeDoD(t *testing.T, rec *httptest.ResponseRecorder) dodJSON {
	t.Helper()
	var v dodJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

func decodeDoDList(t *testing.T, rec *httptest.ResponseRecorder) dodListJSON {
	t.Helper()
	var v dodListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET（9.9）───────────────────────────────────────────────

func TestListDoDReturnsItems(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "ユニットテストが通ること", true, 10),
		sampleDoD(testDoDID2, "設計文書を更新すること", false, 20),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketDoD(rec, cmtReq(http.MethodGet, "/dod", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeDoDList(t, rec)
	if len(got.Items) != 2 {
		t.Fatalf("items = %d件, want 2", len(got.Items))
	}
	// 満たした行は satisfied_at と satisfied_by を持つ（9.9）。
	if got.Items[0].SatisfiedAt == nil || got.Items[0].SatisfiedBy == nil {
		t.Errorf("満たした行に satisfied_at/by が無い: %+v", got.Items[0])
	}
	// 満たしていない行はどちらも null。
	if got.Items[1].SatisfiedAt != nil || got.Items[1].SatisfiedBy != nil {
		t.Errorf("未充足の行に satisfied_at/by が入っている: %+v", got.Items[1])
	}
}

// **config / evidence / origin は応答に出さない**（9.9）。
func TestDoDResponseOmitsUnsupportedFields(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{sampleDoD(testDoDID, "x", false, 10)}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketDoD(rec, cmtReq(http.MethodGet, "/dod", "", "31", ""))

	for _, field := range []string{`"config"`, `"evidence"`, `"origin"`} {
		if strings.Contains(rec.Body.String(), field) {
			t.Errorf("応答に %s が出ている（返さない項目）: %s", field, rec.Body.String())
		}
	}
}

// ── POST（9.9）──────────────────────────────────────────────

func TestCreateDoDDefaultsToManual(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, cmtReq(http.MethodPost, "/dod",
		`{"body":"ユニットテストが通ること"}`, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.dodCreated) != 1 {
		t.Fatalf("CreateDoDItem = %d回, want 1", len(q.ticket.dodCreated))
	}
	if got := q.ticket.dodCreated[0].Type; got != dodTypeManual {
		t.Errorf("type = %q, want manual", got)
	}
	// sort_order 省略時は末尾（現在の最大値 + 10。9.9）。
	if got := q.ticket.dodCreated[0].SortOrder; got != 10 {
		t.Errorf("sort_order = %d, want 10（NextDoDSortOrder）", got)
	}
	if rec.Header().Get("Location") == "" {
		t.Error("Location が空")
	}
}

// **manual 以外の型は unsupported_type で弾く**（9.9 / 9.14）。
func TestCreateDoDRejectsUnsupportedType(t *testing.T) {
	for _, tp := range dodUnsupportedTypes {
		rec := httptest.NewRecorder()
		q := dodFake()
		h, _ := ticketHandler(q)
		h.createDoDItem(rec, cmtReq(http.MethodPost, "/dod",
			`{"type":"`+tp+`","body":"x"}`, "31", ""))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("type=%s: status = %d, want 422 (%s)", tp, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "unsupported_type") {
			t.Errorf("type=%s: details[].code に unsupported_type が無い: %s", tp, rec.Body.String())
		}
		if len(q.ticket.dodCreated) != 0 {
			t.Errorf("type=%s: 作られてしまった", tp)
		}
	}
}

// **綴り違いは invalid**（unsupported_type と区別する）。
func TestCreateDoDRejectsUnknownType(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, cmtReq(http.MethodPost, "/dod",
		`{"type":"asertion","body":"x"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "unsupported_type") {
		t.Errorf("綴り違いに unsupported_type を返している: %s", rec.Body.String())
	}
}

func TestCreateDoDRejectsEmptyBody(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, cmtReq(http.MethodPost, "/dod", `{"body":"  "}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// withTicketEdit は ticket.edit を持つ認可結果を文脈に載せる。
//
// **本番ではミドルウェアが必ず載せる**（RequireAnyProjectPermission）。
// 0029 で is_satisfied の可否がハンドラの判定になったので、単体テストでも
// 同じ入れ物へ入れる——載せなければ「ticket.self_edit しか持たない」に倒れ、
// チェックの付け外しが 403 になる。
func withTicketEdit(req *http.Request) *http.Request {
	return withDeleteAny(req, permTicketEdit)
}

// withSelfEditOnly は ticket.self_edit だけを持つ認可結果を載せる。
func withSelfEditOnly(req *http.Request) *http.Request {
	return withDeleteAny(req, permTicketSelfEdit)
}

// **作成時に is_satisfied=true なら満たした人も入る。**
func TestCreateDoDSatisfiedRecordsActor(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, withTicketEdit(cmtReq(http.MethodPost, "/dod",
		`{"body":"すでに満たしている","is_satisfied":true}`, "31", "")))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeDoD(t, rec)
	if !got.IsSatisfied {
		t.Fatal("is_satisfied = false, want true")
	}
	if got.SatisfiedBy == nil || got.SatisfiedBy.ID != testActorID {
		t.Errorf("satisfied_by = %+v, want 呼び出し元", got.SatisfiedBy)
	}
	if got.SatisfiedAt == nil {
		t.Error("satisfied_at が null")
	}
}

func TestCreateDoDRecordsActivity(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, cmtReq(http.MethodPost, "/dod", `{"body":"テストが通ること"}`, "31", ""))

	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "update" || !a.Field.Valid || a.Field.String != "dod" {
		t.Errorf("action/field = %q/%+v, want update/dod", a.Action, a.Field)
	}
	if a.OldValue.Valid {
		t.Errorf("old_value = %q, want NULL（追加）", a.OldValue.String)
	}
	if got := a.NewValue.String; got != "未: テストが通ること" {
		t.Errorf("new_value = %q, want %q", got, "未: テストが通ること")
	}
}

// ── PATCH（9.9）─────────────────────────────────────────────

// **is_satisfied / satisfied_at / satisfied_by は同時に動く**（9.9）。
func TestPatchDoDCheckSetsSatisfiedBy(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "テストが通ること", false, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, withTicketEdit(cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"is_satisfied":true}`, "31", testDoDID)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeDoD(t, rec)
	if !got.IsSatisfied || got.SatisfiedAt == nil || got.SatisfiedBy == nil {
		t.Fatalf("3つが揃っていない: %+v", got)
	}
	if got.SatisfiedBy.ID != testActorID {
		t.Errorf("satisfied_by = %q, want %q", got.SatisfiedBy.ID, testActorID)
	}
	// 履歴は 未 → 済（9.9）。
	a := q.ticket.activities[0]
	if a.OldValue.String != "未: テストが通ること" || a.NewValue.String != "済: テストが通ること" {
		t.Errorf("activity = %q -> %q", a.OldValue.String, a.NewValue.String)
	}
}

// **false に戻すと satisfied_at と satisfied_by は NULL へ戻る**（9.9）。
func TestPatchDoDUncheckClearsSatisfied(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "テストが通ること", true, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, withTicketEdit(cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"is_satisfied":false}`, "31", testDoDID)))

	got := decodeDoD(t, rec)
	if got.IsSatisfied || got.SatisfiedAt != nil || got.SatisfiedBy != nil {
		t.Fatalf("戻っていない: %+v", got)
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "済: テストが通ること" || a.NewValue.String != "未: テストが通ること" {
		t.Errorf("activity = %q -> %q", a.OldValue.String, a.NewValue.String)
	}
}

// **並び順だけの変更は activity に書かない**（9.9）。
func TestPatchDoDSortOrderOnlySkipsActivity(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "テストが通ること", false, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"sort_order":50}`, "31", testDoDID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// **並び替え自体は効いている**（効かないまま「記録しない」が通っては困る）。
	if got := decodeDoD(t, rec); got.SortOrder != 50 {
		t.Errorf("sort_order = %d, want 50", got.SortOrder)
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0（並び順だけ）", len(q.ticket.activities))
	}
}

// **type は immutable_field**（9.9）。
func TestPatchDoDRejectsType(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{sampleDoD(testDoDID, "x", false, 10)}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"type":"assertion"}`, "31", testDoDID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "immutable_field") {
		t.Errorf("details[].code に immutable_field が無い: %s", rec.Body.String())
	}
}

// 本文とチェックを同時に変えても activity は1行（9.9）。
func TestPatchDoDBodyAndCheckIsOneActivity(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "もとの条件", false, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, withTicketEdit(cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"body":"直した条件","is_satisfied":true}`, "31", testDoDID)))

	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "未: もとの条件" || a.NewValue.String != "済: 直した条件" {
		t.Errorf("activity = %q -> %q", a.OldValue.String, a.NewValue.String)
	}
}

func TestPatchDoDNotFound(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"body":"x"}`, "31", testDoDID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── DELETE（9.9）────────────────────────────────────────────

func TestDeleteDoD(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "消される条件", false, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteDoDItem(rec, cmtReq(http.MethodDelete, "/dod/"+testDoDID, "", "31", testDoDID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.dodRows) != 0 {
		t.Errorf("残っている: %+v", q.ticket.dodRows)
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "未: 消される条件" {
		t.Errorf("old_value = %q", a.OldValue.String)
	}
	if a.NewValue.Valid {
		t.Errorf("new_value = %q, want NULL（削除）", a.NewValue.String)
	}
}

func TestDeleteDoDNotFound(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteDoDItem(rec, cmtReq(http.MethodDelete, "/dod/"+testDoDID, "", "31", testDoDID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0", len(q.ticket.activities))
	}
}

// ── 詳細応答への差し込み（9.5.1）──────────────────────────────

// **dod / links は手順18a から実数を返す**（9.5.1）。それまでは空配列だった。
//
// **別の GET に切っていないことも同時に測る**——画面は詳細1本で3セクションぶんの
// データを得る（GuiDesign.md 5.5、ApiDesign.md 8章の「起動時1〜2本」）。
func TestTicketDetailIncludesDoDAndLinks(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[12] = testTicketID4
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "ユニットテストが通ること", true, 10),
		sampleDoD(testDoDID2, "設計文書を更新すること", false, 20),
	}
	q.ticket.linkRows = []gen.ListTicketLinksRow{
		sampleLink(testLinkID, "outgoing", "blocks", 0, 12, "DB設計", "story"),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31", "", "31", "ticket.view"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)

	dod, _ := view["dod"].([]any)
	if len(dod) != 2 {
		t.Fatalf("dod = %d件, want 2 (%s)", len(dod), rec.Body.String())
	}
	first, _ := dod[0].(map[string]any)
	if first["body"] != "ユニットテストが通ること" || first["is_satisfied"] != true {
		t.Errorf("dod[0] = %+v", first)
	}

	links, _ := view["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("links = %d件, want 1 (%s)", len(links), rec.Body.String())
	}
	link, _ := links[0].(map[string]any)
	if link["direction"] != "outgoing" || link["link_type"] != "blocks" {
		t.Errorf("links[0] = %+v", link)
	}
	ticket, _ := link["ticket"].(map[string]any)
	if ticket["seq"] != float64(12) || ticket["type"] != "story" {
		t.Errorf("links[0].ticket = %+v", ticket)
	}
}

// ── ticket.self_edit では is_satisfied を書けない（9.9。0029）──
//
// **まず通る側を確かめてから、断られる側を測る**（憲章「動いたことを先に
// 確かめてから、動かないことを確かめる」）。上の4件が通る側である。

func TestCreateDoDRejectsSatisfiedForSelfEdit(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDoDItem(rec, withSelfEditOnly(cmtReq(http.MethodPost, "/dod",
		`{"body":"満たしたことにする","is_satisfied":true}`, "31", "")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "is_satisfied") {
		t.Errorf("どの項目で断ったかが本文に無い: %s", rec.Body.String())
	}
}

func TestPatchDoDRejectsSatisfiedForSelfEdit(t *testing.T) {
	q := dodFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, withSelfEditOnly(cmtReq(http.MethodPatch, "/dod",
		`{"is_satisfied":true}`, "31", testDoDID)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// **body だけなら ticket.self_edit で通る。** 開けているのはここまでである。
func TestPatchDoDAllowsBodyForSelfEdit(t *testing.T) {
	q := dodFake()
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		sampleDoD(testDoDID, "テストが通ること", false, 10),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchDoDItem(rec, withSelfEditOnly(cmtReq(http.MethodPatch, "/dod/"+testDoDID,
		`{"body":"記述を整えた"}`, "31", testDoDID)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
