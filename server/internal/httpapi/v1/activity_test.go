package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 業務履歴（ApiDesign.md 9.13.2）の単体テスト。手順19a。
//
// ここで確かめるのは、絞り込みの受け取り方・並び順・ページャと ETag・
// null になる3項目（entity_seq / entity_title / actor）である。

// dashReq は /projects/{key}/... のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうためである（tags_test と同じ）。
func dashReq(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

// activityItem は応答の1件を読むためのテスト側の型。
//
// **応答の型（activityView）をそのまま json.Unmarshal へ渡せない。** v1.Time は
// MarshalJSON しか持たず（apitime.go）、書き出し専用だからである。occurred_at を
// string で受けることで、**2.2 の書式（UTC・秒精度）もそのまま確かめられる。**
type activityItem struct {
	ID          string  `json:"id"`
	EntityType  string  `json:"entity_type"`
	EntityID    string  `json:"entity_id"`
	EntitySeq   *int32  `json:"entity_seq"`
	EntityTitle *string `json:"entity_title"`
	Actor       *struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	} `json:"actor"`
	Action     string  `json:"action"`
	Field      *string `json:"field"`
	OldValue   *string `json:"old_value"`
	NewValue   *string `json:"new_value"`
	OccurredAt string  `json:"occurred_at"`
}

func decodeActivity(t *testing.T, rec *httptest.ResponseRecorder) List[activityItem] {
	t.Helper()
	var v List[activityItem]
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// 履歴のフェイクが使うチケットの ULID。**2件用意する**——entity で絞れている
// ことを測るには、絞られて落ちる側が要る。
const (
	actTicketA = "01K2ACT00000000000000TKTA"
	actTicketB = "01K2ACT00000000000000TKTB"
)

func actAt(min int) pgtype.Timestamptz {
	return ts(time.Date(2026, 8, 27, 10, min, 0, 0, time.UTC))
}

// sampleActivity は5行の履歴。**期待値を先に紙の上で作れるように、
// 時刻と ID を明示して並べてある**（LEARNINGS の「期待値は対象を数え上げてから」）。
//
//	id  entity  action      occurred  備考
//	A1  TKTA    create      10:00
//	A2  TKTA    update      10:05     field=title
//	A3  TKTA    update      10:05     field=due_date。**A2 と同じ時刻**
//	B1  TKTB    transition  10:10
//	B2  TKTB    delete      10:15     チケットは物理削除済み（seq/title が NULL）
//
// occurred_at DESC, id DESC の期待順は B2, B1, A3, A2, A1 になる。
func sampleActivity() []gen.ListActivityRow {
	actor := func() (pgtype.Text, pgtype.Text, pgtype.Text) {
		return txt(testActorID), txt("user"), txt("田中")
	}
	id, kind, name := actor()
	return []gen.ListActivityRow{
		{
			ID: "01K2ACT0000000000000000A1", EntityType: "ticket", EntityID: actTicketA,
			EntitySeq: pgtype.Int4{Int32: 31, Valid: true}, EntityTitle: txt("認証APIの実装"),
			ActorID: id, ActorKind: kind, ActorDisplayName: name,
			Action: "create", OccurredAt: actAt(0),
		},
		{
			ID: "01K2ACT0000000000000000A2", EntityType: "ticket", EntityID: actTicketA,
			EntitySeq: pgtype.Int4{Int32: 31, Valid: true}, EntityTitle: txt("認証APIの実装"),
			ActorID: id, ActorKind: kind, ActorDisplayName: name,
			Action: "update", Field: txt("title"),
			OldValue: txt("旧タイトル"), NewValue: txt("認証APIの実装"),
			OccurredAt: actAt(5),
		},
		{
			ID: "01K2ACT0000000000000000A3", EntityType: "ticket", EntityID: actTicketA,
			EntitySeq: pgtype.Int4{Int32: 31, Valid: true}, EntityTitle: txt("認証APIの実装"),
			ActorID: id, ActorKind: kind, ActorDisplayName: name,
			Action: "update", Field: txt("due_date"), NewValue: txt("2026-08-14"),
			OccurredAt: actAt(5),
		},
		{
			ID: "01K2ACT0000000000000000B1", EntityType: "ticket", EntityID: actTicketB,
			EntitySeq: pgtype.Int4{Int32: 44, Valid: true}, EntityTitle: txt("ログイン画面"),
			ActorID: id, ActorKind: kind, ActorDisplayName: name,
			Action: "transition", Field: txt("status_key"),
			OldValue: txt("todo"), NewValue: txt("in_progress"),
			OccurredAt: actAt(10),
		},
		{
			// **削除されたチケットの行**（9.13.2）。seq / title は結合できない。
			// アクターも消えている（ON DELETE SET NULL）。
			ID: "01K2ACT0000000000000000B2", EntityType: "ticket", EntityID: actTicketB,
			Action: "delete", OccurredAt: actAt(15),
		},
	}
}

func activityFake() *fakeQuerier {
	q := dashFake()
	q.ticket.activityAll = sampleActivity()
	q.ticket.idBySeq = map[int32]string{31: actTicketA, 44: actTicketB}
	return q
}

// ── 並び順（9.13.2）──────────────────────────────────────────

// **occurred_at DESC, id DESC で固定**（9.13.2）。A2 と A3 は同じ時刻なので、
// tie-break が無いと並びが実行ごとに変わる。
func TestListActivityOrdersNewestFirstWithIDTieBreak(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeActivity(t, rec)
	want := []string{
		"01K2ACT0000000000000000B2",
		"01K2ACT0000000000000000B1",
		"01K2ACT0000000000000000A3",
		"01K2ACT0000000000000000A2",
		"01K2ACT0000000000000000A1",
	}
	if len(got.Items) != len(want) {
		t.Fatalf("件数 = %d, want %d", len(got.Items), len(want))
	}
	for i, id := range want {
		if got.Items[i].ID != id {
			t.Errorf("items[%d].id = %s, want %s", i, got.Items[i].ID, id)
		}
	}
}

// ── ページャ（2.6 / 9.13.2）──────────────────────────────────

func TestListActivityDefaultPerPageIs20(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	got := decodeActivity(t, rec)
	if got.PerPage != 20 {
		t.Errorf("per_page = %d, want 20（9.13.2）", got.PerPage)
	}
	if got.Total != 5 || got.TotalPages != 1 {
		t.Errorf("total = %d / total_pages = %d, want 5 / 1", got.Total, got.TotalPages)
	}
}

// **範囲外のページでも total が返る**（2.6）。ウィンドウ関数は行が無いと
// 1行も返らないので、Summarize 側から採れていないと 0 に落ちる。
func TestListActivityOutOfRangePageKeepsTotal(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet,
		"/api/v1/projects/demo/activity?page=9&per_page=2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeActivity(t, rec)
	if len(got.Items) != 0 {
		t.Errorf("items = %d件, want 0", len(got.Items))
	}
	if got.Total != 5 || got.TotalPages != 3 {
		t.Errorf("total = %d / total_pages = %d, want 5 / 3", got.Total, got.TotalPages)
	}
	if !slices.Contains(q.opLog, "SummarizeActivity") {
		t.Errorf("SummarizeActivity が呼ばれていない: %v", q.opLog)
	}
}

// ── entity フィルタ（9.13.2）─────────────────────────────────

func TestListActivityFiltersByEntity(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet,
		"/api/v1/projects/demo/activity?entity=ticket:31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeActivity(t, rec)
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3（TKTA の3件）", got.Total)
	}
	for _, item := range got.Items {
		if item.EntityID != actTicketA {
			t.Errorf("entity_id = %s, want %s", item.EntityID, actTicketA)
		}
	}
}

// **存在しない seq は空の一覧を 200 で返す**（9.13.2）。404 にしない。
//
// **entity_id を空文字のまま SQL へ渡すと全件が返る**ので、その取り違えも
// ここで捕まる（total が 5 になる）。
func TestListActivityUnknownEntitySeqReturnsEmptyList(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet,
		"/api/v1/projects/demo/activity?entity=ticket:9999"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeActivity(t, rec)
	if got.Total != 0 || len(got.Items) != 0 {
		t.Errorf("total = %d / items = %d件, want 0 / 0", got.Total, len(got.Items))
	}
	if slices.Contains(q.opLog, "ListActivity") {
		t.Errorf("解決できないのに ListActivity を呼んでいる: %v", q.opLog)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("空でも ETag は返す（2.7）")
	}
}

func TestListActivityRejectsMalformedEntity(t *testing.T) {
	for _, v := range []string{"ticket:abc", "foo:1", "ticket:", "31", "ticket:0", "ticket:-1"} {
		t.Run(v, func(t *testing.T) {
			q := activityFake()
			h := &handler{q: q}

			rec := httptest.NewRecorder()
			h.listProjectActivity(rec, dashReq(http.MethodGet,
				"/api/v1/projects/demo/activity?entity="+v))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			assertDetail(t, rec, "entity", "invalid")
			// **422 になるリクエストで DB を触らない。**
			if slices.Contains(q.opLog, "FindTicketIDBySeq") {
				t.Errorf("書式が誤っているのにチケットを引いている: %v", q.opLog)
			}
		})
	}
}

// ── action フィルタ（9.13.2）─────────────────────────────────

func TestListActivityFiltersByAction(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet,
		"/api/v1/projects/demo/activity?action=update"))

	got := decodeActivity(t, rec)
	if got.Total != 2 {
		t.Fatalf("total = %d, want 2（A2 と A3）", got.Total)
	}
	for _, item := range got.Items {
		if item.Action != "update" {
			t.Errorf("action = %s, want update", item.Action)
		}
	}
}

// **カンマ区切りの OR は受け付けない**（9.13.2）。
func TestListActivityRejectsUnknownOrMultipleAction(t *testing.T) {
	for _, v := range []string{"created", "update,create", "UPDATE", ""} {
		if v == "" {
			continue // 空は「絞らない」で正当（下の既定のテストが見ている）
		}
		t.Run(v, func(t *testing.T) {
			q := activityFake()
			h := &handler{q: q}

			rec := httptest.NewRecorder()
			h.listProjectActivity(rec, dashReq(http.MethodGet,
				"/api/v1/projects/demo/activity?action="+v))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			assertDetail(t, rec, "action", "invalid")
		})
	}
}

// ── sort / order は指定できない（9.13.2）───────────────────────

func TestListActivityRejectsSortAndOrder(t *testing.T) {
	cases := []struct{ query, field string }{
		{"sort=occurred_at", "sort"},
		{"sort=id", "sort"},
		{"order=asc", "order"},
		{"order=desc", "order"},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			q := activityFake()
			h := &handler{q: q}

			rec := httptest.NewRecorder()
			h.listProjectActivity(rec, dashReq(http.MethodGet,
				"/api/v1/projects/demo/activity?"+c.query))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			assertDetail(t, rec, c.field, "invalid")
		})
	}
}

// ── null になる3項目（9.13.2）────────────────────────────────

func TestListActivityDeletedTicketHasNullSeqTitleAndActor(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	got := decodeActivity(t, rec)
	del := got.Items[0] // B2。並び順のテストで先頭であることを確かめてある
	if del.Action != "delete" {
		t.Fatalf("先頭の action = %s, want delete", del.Action)
	}
	if del.EntitySeq != nil {
		t.Errorf("entity_seq = %v, want null（9.13.2 の物理削除）", *del.EntitySeq)
	}
	if del.EntityTitle != nil {
		t.Errorf("entity_title = %v, want null", *del.EntityTitle)
	}
	if del.Actor != nil {
		t.Errorf("actor = %+v, want null（ON DELETE SET NULL）", *del.Actor)
	}
	// **entity_id は残る。** 行そのものは消えていない。
	if del.EntityID != actTicketB {
		t.Errorf("entity_id = %s, want %s", del.EntityID, actTicketB)
	}
}

// **create の行は field / old_value / new_value がすべて null**（activity.go）。
func TestListActivityCreateRowHasNullFieldAndValues(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	got := decodeActivity(t, rec)
	create := got.Items[4] // A1
	if create.Action != "create" {
		t.Fatalf("items[4].action = %s, want create", create.Action)
	}
	if create.Field != nil || create.OldValue != nil || create.NewValue != nil {
		t.Errorf("create の field/old/new = %v/%v/%v, want すべて null",
			create.Field, create.OldValue, create.NewValue)
	}
}

// **値を持たない側だけが null になる。** A3 は old_value を持たず new_value を持つ。
func TestListActivityKeepsOneSidedValues(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	got := decodeActivity(t, rec)
	a3 := got.Items[2]
	if a3.ID != "01K2ACT0000000000000000A3" {
		t.Fatalf("items[2].id = %s, want ...A3", a3.ID)
	}
	if a3.OldValue != nil {
		t.Errorf("old_value = %v, want null", *a3.OldValue)
	}
	if a3.NewValue == nil || *a3.NewValue != "2026-08-14" {
		t.Errorf("new_value = %v, want 2026-08-14", a3.NewValue)
	}
}

// **status_key はキーのまま返す**（9.13.2）。表示名への変換は画面が行う。
func TestListActivityReturnsStatusKeyRaw(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet,
		"/api/v1/projects/demo/activity?action=transition"))

	got := decodeActivity(t, rec)
	if len(got.Items) != 1 {
		t.Fatalf("件数 = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.Field == nil || *item.Field != "status_key" {
		t.Fatalf("field = %v, want status_key", item.Field)
	}
	if *item.OldValue != "todo" || *item.NewValue != "in_progress" {
		t.Errorf("old/new = %s/%s, want todo/in_progress（表示名にしない）",
			*item.OldValue, *item.NewValue)
	}
}

// ── ETag（2.7 / 9.13.2）─────────────────────────────────────

// **ページとフィルタで ETag が変わる。** 同じ値だと 2ページ目に 304 が返る
// 実装を後から足したときに、1ページ目の本文が返る。
func TestListActivityETagVariesWithPageAndFilter(t *testing.T) {
	etagOfQuery := func(query string) string {
		q := activityFake()
		h := &handler{q: q}
		rec := httptest.NewRecorder()
		h.listProjectActivity(rec, dashReq(http.MethodGet,
			"/api/v1/projects/demo/activity?"+query))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (%s)", query, rec.Code, rec.Body.String())
		}
		return rec.Header().Get("ETag")
	}

	base := etagOfQuery("per_page=2")
	seen := map[string]string{base: "per_page=2"}
	for _, query := range []string{
		"per_page=2&page=2",
		"per_page=3",
		"entity=ticket:31",
		"action=update",
	} {
		got := etagOfQuery(query)
		if got == "" {
			t.Fatalf("%s: ETag が空", query)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%s の ETag が %s と同じ: %s", query, prev, got)
		}
		seen[got] = query
	}
	// 同じ条件なら同じ値であること（安定していないと差分取得に使えない）。
	if again := etagOfQuery("per_page=2"); again != base {
		t.Errorf("同じ条件で ETag が変わった: %s → %s", base, again)
	}
}

func TestListActivityFailsWithInternalError(t *testing.T) {
	q := activityFake()
	q.ticket.activityErr = errors.New("boom")
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// parseEntityTicketSeq は正しい形だけを通す（9.13.2）。
func TestParseEntityTicketSeqAcceptsOnlyTicketForm(t *testing.T) {
	if got, err := parseEntityTicketSeq("ticket:31"); err != nil || got != 31 {
		t.Errorf("ticket:31 → %d, %v; want 31, nil", got, err)
	}
	for _, v := range []string{"", "ticket", "ticket:", "ticket:0", "ticket:x", "comment:1", "31"} {
		if _, err := parseEntityTicketSeq(v); err == nil {
			t.Errorf("%q が通ってしまった", v)
		}
	}
}

// assertDetail は 2.5.1 の details[] に該当する1件があることを確かめる。
func assertDetail(t *testing.T, rec *httptest.ResponseRecorder, field, code string) {
	t.Helper()
	e := errorOf(t, rec)
	if e.Code != "validation_failed" {
		t.Errorf("error.code = %s, want validation_failed", e.Code)
	}
	if !hasDetail(e, field, code) {
		t.Errorf("details に %s/%s が無い: %+v", field, code, e.Details)
	}
}

// **occurred_at は 2.2 の ISO8601 UTC・秒精度**（apitime.go の Time）。
func TestListActivityFormatsOccurredAtAsUTCSeconds(t *testing.T) {
	q := activityFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listProjectActivity(rec, dashReq(http.MethodGet, "/api/v1/projects/demo/activity"))

	got := decodeActivity(t, rec)
	if want := "2026-08-27T10:15:00Z"; got.Items[0].OccurredAt != want {
		t.Errorf("occurred_at = %q, want %q", got.Items[0].OccurredAt, want)
	}
}
