package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/go-chi/chi/v5"
)

// 外部参照API（ApiDesign.md 9.10.2）の単体テスト。手順17c。
//
// 認可（ticket.view / ticket.edit）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、入力の検証・
// 応答の形・404 の倒し方・kind ごとの必須・activity の粒度である。

const (
	testRefCodeID = "01K2REF00000000000000COD1"
	testRefDocID  = "01K2REF00000000000000DOC1"
)

// ── 素材 ────────────────────────────────────────────────────

// refReq は /tickets/{seq}/references 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうため（tags_test と同じ）。
func refReq(method, target, body, seq, id string) *http.Request {
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
	if id != "" {
		rc.URLParams.Add("id", id)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

// refFake は seq=31 のチケットが解決できる状態のフェイクを返す。
func refFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	return q
}

// sampleCodeRef / sampleDocRef は 9.10.2 の例に合わせた行。
func sampleCodeRef(id string, sortOrder int32) gen.ListTicketReferencesRow {
	now := time.Date(2026, 8, 27, 2, 10, 0, 0, time.UTC)
	return gen.ListTicketReferencesRow{
		ID: id, Kind: "code",
		Repository: txt("my-app"), Branch: txt("pb/31"), CommitSha: txt("a1b2c3d4e5"),
		Url:       txt("https://github.com/example/my-app/commit/a1b2c3d4e5"),
		Label:     txt("認証ハンドラを追加"),
		SortOrder: sortOrder,
		CreatedBy: txt(testActorID), CreatedByKind: txt("user"), CreatedByName: txt("田中"),
		CreatedAt: ts(now), UpdatedAt: ts(now),
	}
}

func sampleDocRef(id string, sortOrder int32) gen.ListTicketReferencesRow {
	now := time.Date(2026, 8, 27, 2, 20, 0, 0, time.UTC)
	return gen.ListTicketReferencesRow{
		ID: id, Kind: "doc",
		Url:       txt("https://example.com/auth-design.md"),
		Label:     txt("認証設計メモ"),
		SortOrder: sortOrder,
		CreatedBy: txt(testActorID), CreatedByKind: txt("user"), CreatedByName: txt("田中"),
		CreatedAt: ts(now), UpdatedAt: ts(now),
	}
}

// refJSON は応答を読むための型。
//
// **referenceView をそのまま Unmarshal に使えない**——v1.Time は MarshalJSON
// しか持たない（apitime.go）ので、文字列を読み戻せない。時刻は string で受ける。
type refJSON struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Repository *string   `json:"repository"`
	Branch     *string   `json:"branch"`
	CommitSha  *string   `json:"commit_sha"`
	URL        *string   `json:"url"`
	Label      *string   `json:"label"`
	Note       *string   `json:"note"`
	SortOrder  int32     `json:"sort_order"`
	CreatedBy  *actorRef `json:"created_by"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
}

type refListJSON struct {
	Items []refJSON `json:"items"`
}

func decodeRef(t *testing.T, rec *httptest.ResponseRecorder) refJSON {
	t.Helper()
	var v refJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

func decodeRefList(t *testing.T, rec *httptest.ResponseRecorder) refListJSON {
	t.Helper()
	var v refListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET（9.10.2）────────────────────────────────────────────

func TestListReferencesReturnsItems(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{
		sampleCodeRef(testRefCodeID, 10),
		sampleDocRef(testRefDocID, 20),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketReferences(rec,
		refReq(http.MethodGet, "/api/v1/projects/demo/tickets/31/references", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	list := decodeRefList(t, rec)
	if len(list.Items) != 2 {
		t.Fatalf("items = %d件, want 2", len(list.Items))
	}
	if list.Items[0].Kind != "code" || list.Items[1].Kind != "doc" {
		t.Errorf("kind の並び = %q, %q, want code, doc",
			list.Items[0].Kind, list.Items[1].Kind)
	}
	// **created_by は返す**（9.10.2）。画面は使わないが、データは残す。
	if list.Items[0].CreatedBy == nil || list.Items[0].CreatedBy.Kind != "user" {
		t.Errorf("created_by = %+v, want kind=user", list.Items[0].CreatedBy)
	}
	if list.Items[0].Repository == nil || *list.Items[0].Repository != "my-app" {
		t.Errorf("repository = %v, want my-app", list.Items[0].Repository)
	}
	// doc は repository を持たない（1つの表なので null になる）。
	if list.Items[1].Repository != nil {
		t.Errorf("doc の repository = %v, want null", *list.Items[1].Repository)
	}
}

// **0件でも items は空配列である**（null にしない）。画面が length を読むため。
func TestListReferencesReturnsEmptyArray(t *testing.T) {
	h, _ := ticketHandler(refFake())

	rec := httptest.NewRecorder()
	h.listTicketReferences(rec,
		refReq(http.MethodGet, "/api/v1/projects/demo/tickets/31/references", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"items":[]`) {
		t.Errorf("body = %s, want items が空配列", got)
	}
}

// チケットが無ければ 404（参照の 404 ではない）。
func TestListReferencesReturnsNotFoundForUnknownTicket(t *testing.T) {
	h, _ := ticketHandler(refFake())

	rec := httptest.NewRecorder()
	h.listTicketReferences(rec,
		refReq(http.MethodGet, "/api/v1/projects/demo/tickets/99/references", "", "99", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "チケットが見つかりません") {
		t.Errorf("message = %s, want チケットの 404", got)
	}
}

// ── POST（9.10.2）───────────────────────────────────────────

func TestCreateReferenceCreatesCodeRow(t *testing.T) {
	q := refFake()
	h, tx := ticketHandler(q)

	body := `{"kind":"code","repository":"my-app","branch":"pb/31",
	          "commit_sha":"a1b2c3d","url":"https://example.com/c/a1b2c3d",
	          "label":"認証ハンドラを追加"}`
	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(
		loc, "/api/v1/projects/demo/tickets/31/references/") {
		t.Errorf("Location = %q, want /api/v1/projects/demo/tickets/31/references/<id>", loc)
	}
	view := decodeRef(t, rec)
	if view.Kind != "code" {
		t.Errorf("kind = %q, want code", view.Kind)
	}
	if view.Repository == nil || *view.Repository != "my-app" {
		t.Errorf("repository = %v, want my-app", view.Repository)
	}
	if len(q.ticket.refCreated) != 1 {
		t.Fatalf("CreateTicketReference = %d回, want 1", len(q.ticket.refCreated))
	}
	// **sort_order 省略時は末尾**（現在の最大値 + 10。9.10.2）。
	if got := q.ticket.refCreated[0].SortOrder; got != 10 {
		t.Errorf("sort_order = %d, want 10", got)
	}
	// **書き手はプリンシパル**（9.10.2）。Phase 1 は API トークンの持ち主。
	if got := q.ticket.refCreated[0].CreatedBy; got.String != testActorID {
		t.Errorf("created_by = %q, want %q", got.String, testActorID)
	}
}

// **activity を1行書く**（9.1.1 / 9.10.2）。action は update、field は kind 付き。
func TestCreateReferenceRecordsActivity(t *testing.T) {
	q := refFake()
	h, _ := ticketHandler(q)

	body := `{"kind":"code","repository":"my-app","branch":"pb/31","commit_sha":"a1b2c3d"}`
	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	// **create ではなく update。** ticket:31 の create は「チケットが作られた」を
	// 意味しており（9.5.3 / 手順19）、参照の追加に当てるとチケットが増えて見える。
	if a.Action != "update" {
		t.Errorf("action = %q, want update", a.Action)
	}
	if a.EntityType != "ticket" || a.EntityID != testTicketID {
		t.Errorf("entity = %s:%s, want ticket:%s", a.EntityType, a.EntityID, testTicketID)
	}
	if a.Field.String != "reference.code" {
		t.Errorf("field = %q, want reference.code", a.Field.String)
	}
	// 追加なので old_value は NULL、new_value は画面と同じ形の要約。
	if a.OldValue.Valid {
		t.Errorf("old_value = %q, want NULL", a.OldValue.String)
	}
	if a.NewValue.String != "my-app : pb/31 : a1b2c3d" {
		t.Errorf("new_value = %q, want %q", a.NewValue.String, "my-app : pb/31 : a1b2c3d")
	}
}

// doc の要約は label（無ければ url）。
func TestCreateReferenceSummaryForDocUsesLabel(t *testing.T) {
	q := refFake()
	h, _ := ticketHandler(q)

	body := `{"kind":"doc","url":"https://example.com/x.md","label":"認証設計メモ"}`
	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	a := q.ticket.activities[0]
	if a.Field.String != "reference.doc" {
		t.Errorf("field = %q, want reference.doc", a.Field.String)
	}
	if a.NewValue.String != "認証設計メモ" {
		t.Errorf("new_value = %q, want 認証設計メモ", a.NewValue.String)
	}
}

func TestCreateReferenceSummaryForDocFallsBackToURL(t *testing.T) {
	q := refFake()
	h, _ := ticketHandler(q)

	body := `{"kind":"doc","url":"https://example.com/x.md"}`
	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.ticket.activities[0].NewValue.String; got != "https://example.com/x.md" {
		t.Errorf("new_value = %q, want URL", got)
	}
}

// **kind ごとに必須が違う**（9.10.2）。code は repository、doc は url。
func TestCreateReferenceRejectsMissingRequiredField(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"code に repository が無い", `{"kind":"code","branch":"pb/31"}`, "repository"},
		{"doc に url が無い", `{"kind":"doc","label":"設計メモ"}`, "url"},
		{"kind が無い", `{"repository":"my-app"}`, "kind"},
		{"kind が未知", `{"kind":"spec","url":"https://example.com"}`, "kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := refFake()
			h, tx := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.createTicketReference(rec, refReq(http.MethodPost,
				"/api/v1/projects/demo/tickets/31/references", c.body, "31", ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"field":"`+c.field+`"`) {
				t.Errorf("details に %q が無い: %s", c.field, rec.Body.String())
			}
			// 検証で落ちたらトランザクションを開かない。
			if tx.calls != 0 {
				t.Errorf("RunInTx = %d回, want 0", tx.calls)
			}
		})
	}
}

// 上限（9.10.2 の表）。**空でないことしか見ない URL も長さだけは見る。**
func TestCreateReferenceRejectsTooLongFields(t *testing.T) {
	cases := []struct {
		field string
		body  string
	}{
		{"repository", `{"kind":"code","repository":"` + strings.Repeat("あ", 201) + `"}`},
		{"branch", `{"kind":"code","repository":"my-app","branch":"` + strings.Repeat("b", 256) + `"}`},
		{"commit_sha", `{"kind":"code","repository":"my-app","commit_sha":"` + strings.Repeat("0", 65) + `"}`},
		{"url", `{"kind":"doc","url":"` + strings.Repeat("u", 1001) + `"}`},
		{"label", `{"kind":"doc","url":"https://x","label":"` + strings.Repeat("ら", 201) + `"}`},
		{"note", `{"kind":"doc","url":"https://x","note":"` + strings.Repeat("め", 501) + `"}`},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			h, _ := ticketHandler(refFake())
			rec := httptest.NewRecorder()
			h.createTicketReference(rec, refReq(http.MethodPost,
				"/api/v1/projects/demo/tickets/31/references", c.body, "31", ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"too_long"`) {
				t.Errorf("code = %s, want too_long", rec.Body.String())
			}
		})
	}
}

// **URL の形式は検証しない**（9.10.2）。SSH 形式でも受ける。
func TestCreateReferenceAcceptsNonWebURL(t *testing.T) {
	h, _ := ticketHandler(refFake())
	body := `{"kind":"doc","url":"git@github.com:example/my-app.git"}`

	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// **プロジェクト設定の repositories と突き合わせない**（DbDesign.md 6.12）。
func TestCreateReferenceAcceptsUnknownRepository(t *testing.T) {
	h, _ := ticketHandler(refFake())
	body := `{"kind":"code","repository":"設定に無いリポジトリ"}`

	rec := httptest.NewRecorder()
	h.createTicketReference(rec,
		refReq(http.MethodPost, "/api/v1/projects/demo/tickets/31/references", body, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// ── PATCH（9.10.2）──────────────────────────────────────────

func TestPatchReferenceUpdatesSentFieldsOnly(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleDocRef(testRefDocID, 10)}
	h, _ := ticketHandler(q)

	body := `{"label":"認証設計メモ（改訂）"}`
	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefDocID, body, "31", testRefDocID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	view := decodeRef(t, rec)
	if view.Label == nil || *view.Label != "認証設計メモ（改訂）" {
		t.Errorf("label = %v, want 認証設計メモ（改訂）", view.Label)
	}
	// 送っていない url は触らない。
	if view.URL == nil || *view.URL != "https://example.com/auth-design.md" {
		t.Errorf("url = %v, want 変わっていないこと", view.URL)
	}
	if len(q.ticket.refUpdated) != 1 {
		t.Fatalf("UpdateTicketReference = %d回, want 1", len(q.ticket.refUpdated))
	}
	arg := q.ticket.refUpdated[0]
	if !arg.LabelSet {
		t.Error("label_set = false, want true")
	}
	if arg.UrlSet {
		t.Error("url_set = true, want false（送っていない項目は触らない）")
	}
}

// **null は「その項目を空にする」**（9.10.2）。必須でない項目に限る。
func TestPatchReferenceClearsFieldWithNull(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleCodeRef(testRefCodeID, 10)}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefCodeID,
		`{"branch":null}`, "31", testRefCodeID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if view := decodeRef(t, rec); view.Branch != nil {
		t.Errorf("branch = %q, want null", *view.Branch)
	}
	arg := q.ticket.refUpdated[0]
	if !arg.BranchSet || arg.Branch.Valid {
		t.Errorf("branch_set = %v / branch.Valid = %v, want true / false",
			arg.BranchSet, arg.Branch.Valid)
	}
}

// **kind は作成後に変えられない**（9.10.2、details[].code = immutable_field）。
func TestPatchReferenceRejectsKind(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleDocRef(testRefDocID, 10)}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefDocID,
		`{"kind":"code","repository":"my-app"}`, "31", testRefDocID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"immutable_field"`) {
		t.Errorf("code = %s, want immutable_field", rec.Body.String())
	}
	if tx.calls != 0 {
		t.Errorf("RunInTx = %d回, want 0", tx.calls)
	}
}

// **更新後の行が CHECK を満たすことを、UPDATE の前に見る**（9.10.2）。
func TestPatchReferenceRejectsClearingRequiredField(t *testing.T) {
	cases := []struct {
		name  string
		row   gen.ListTicketReferencesRow
		id    string
		body  string
		field string
	}{
		{"code の repository を消す", sampleCodeRef(testRefCodeID, 10), testRefCodeID,
			`{"repository":null}`, "repository"},
		{"code の repository を空文字にする", sampleCodeRef(testRefCodeID, 10), testRefCodeID,
			`{"repository":"  "}`, "repository"},
		{"doc の url を消す", sampleDocRef(testRefDocID, 10), testRefDocID,
			`{"url":null}`, "url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := refFake()
			q.ticket.references = []gen.ListTicketReferencesRow{c.row}
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.patchTicketReference(rec, refReq(http.MethodPatch,
				"/api/v1/projects/demo/tickets/31/references/"+c.id, c.body, "31", c.id))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"field":"`+c.field+`"`) {
				t.Errorf("details に %q が無い: %s", c.field, rec.Body.String())
			}
			// UPDATE まで行かせない（DB の CHECK に落とすと 500 になる）。
			if len(q.ticket.refUpdated) != 0 {
				t.Errorf("UpdateTicketReference = %d回, want 0", len(q.ticket.refUpdated))
			}
		})
	}
}

// 値が実際に変わったときだけ記録する（9.5.2 と同じ扱い）。
func TestPatchReferenceRecordsActivityWithBothValues(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleDocRef(testRefDocID, 10)}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefDocID,
		`{"label":"認証設計メモ（改訂）"}`, "31", testRefDocID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "update" || a.Field.String != "reference.doc" {
		t.Errorf("action/field = %s/%s, want update/reference.doc", a.Action, a.Field.String)
	}
	if a.OldValue.String != "認証設計メモ" {
		t.Errorf("old_value = %q, want 認証設計メモ", a.OldValue.String)
	}
	if a.NewValue.String != "認証設計メモ（改訂）" {
		t.Errorf("new_value = %q, want 認証設計メモ（改訂）", a.NewValue.String)
	}
}

// **要約が変わらない変更は記録しない。** note だけを直しても履歴は増えない。
func TestPatchReferenceSkipsActivityWhenSummaryUnchanged(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleDocRef(testRefDocID, 10)}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefDocID,
		`{"note":"あとで読む"}`, "31", testRefDocID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0", len(q.ticket.activities))
	}
}

func TestPatchReferenceReturnsNotFoundForUnknownID(t *testing.T) {
	q := refFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketReference(rec, refReq(http.MethodPatch,
		"/api/v1/projects/demo/tickets/31/references/"+testRefDocID,
		`{"label":"x"}`, "31", testRefDocID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "参照が見つかりません") {
		t.Errorf("message = %s, want 参照の 404", rec.Body.String())
	}
}

// ── DELETE（9.10.2）─────────────────────────────────────────

func TestDeleteReferenceReturnsNoContent(t *testing.T) {
	q := refFake()
	q.ticket.references = []gen.ListTicketReferencesRow{sampleCodeRef(testRefCodeID, 10)}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketReference(rec, refReq(http.MethodDelete,
		"/api/v1/projects/demo/tickets/31/references/"+testRefCodeID, "", "31", testRefCodeID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}
	if len(q.ticket.references) != 0 {
		t.Errorf("残った行 = %d件, want 0", len(q.ticket.references))
	}
	// 削除は old_value に要約、new_value は NULL。
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	// **delete ではなく update。** ticket:31 の delete は「チケットが消された」
	// を意味しており（9.5.3）、手順19 のダッシュボードがそう読む。
	if a.Action != "update" {
		t.Errorf("action = %q, want update", a.Action)
	}
	if a.OldValue.String != "my-app : pb/31 : a1b2c3d4e5" {
		t.Errorf("old_value = %q, want 要約", a.OldValue.String)
	}
	if a.NewValue.Valid {
		t.Errorf("new_value = %q, want NULL", a.NewValue.String)
	}
}

func TestDeleteReferenceReturnsNotFoundForUnknownID(t *testing.T) {
	h, _ := ticketHandler(refFake())

	rec := httptest.NewRecorder()
	h.deleteTicketReference(rec, refReq(http.MethodDelete,
		"/api/v1/projects/demo/tickets/31/references/"+testRefCodeID, "", "31", testRefCodeID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 詳細応答への同梱（9.5.1）─────────────────────────────────

// **references は詳細応答に入る**（9.5.1）。画面は別の GET を呼ばない。
func TestTicketDetailIncludesReferences(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.references = []gen.ListTicketReferencesRow{
		sampleCodeRef(testRefCodeID, 10),
		sampleDocRef(testRefDocID, 20),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31", "", "31", "ticket.view"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	refs, _ := view["references"].([]any)
	if len(refs) != 2 {
		t.Fatalf("references = %d件, want 2 (%s)", len(refs), rec.Body.String())
	}
	first, _ := refs[0].(map[string]any)
	second, _ := refs[1].(map[string]any)
	if first["kind"] != "code" || second["kind"] != "doc" {
		t.Errorf("kind の並び = %v, %v, want code, doc", first["kind"], second["kind"])
	}
	// **dod / links は手順18a から実数になった。** この素材では入れていないので
	// 空配列だが、null ではない（消費者にとって形が安定する）。
	dod, _ := view["dod"].([]any)
	links, _ := view["links"].([]any)
	if len(dod) != 0 || len(links) != 0 {
		t.Errorf("dod = %d件 / links = %d件, want 0 / 0（素材に入れていない）", len(dod), len(links))
	}
}

// 参照が無いチケットでも references は空配列（null にしない）。
func TestTicketDetailReferencesIsEmptyArray(t *testing.T) {
	h, _ := ticketHandler(ticketDetailFake())

	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31", "", "31", "ticket.view"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"references":[]`) {
		t.Errorf("body に references の空配列が無い: %s", got)
	}
}

// ── 要約の組み立て（9.10.2）─────────────────────────────────

// **欠けている要素は詰める**（9.10.2）。ブランチだけのコミット無しでも読める。
func TestReferenceSummaryOmitsMissingParts(t *testing.T) {
	cases := []struct {
		name string
		view referenceView
		want string
	}{
		{"3つそろう", referenceView{Kind: "code",
			Repository: strPtr("my-app"), Branch: strPtr("pb/31"), CommitSha: strPtr("a1b2c3d")},
			"my-app : pb/31 : a1b2c3d"},
		{"コミットが無い", referenceView{Kind: "code",
			Repository: strPtr("my-app"), Branch: strPtr("pb/31")},
			"my-app : pb/31"},
		{"リポジトリだけ", referenceView{Kind: "code", Repository: strPtr("my-app")},
			"my-app"},
		{"doc はラベル", referenceView{Kind: "doc",
			Label: strPtr("設計メモ"), URL: strPtr("https://x")}, "設計メモ"},
		{"doc はラベルが無ければURL", referenceView{Kind: "doc", URL: strPtr("https://x")},
			"https://x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := referenceSummaryOf(c.view); got != c.want {
				t.Errorf("summary = %q, want %q", got, c.want)
			}
		})
	}
}
