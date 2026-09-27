package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// スプリントAPI（ApiDesign.md 9.12）の単体テスト。
//
// タグと違って**日付が2つあり、その関係（ck_sprint_dates）を PATCH でも
// 保たなければならない**。片方だけ送られたときに現在値と突き合わせているかを
// 重点的に見る。

const testSprintID = "01K2SPRINT000000000000001"

func sprintReq(method, target, body, sprintID string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	if sprintID != "" {
		rc.URLParams.Add("id", sprintID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

func sprintFake() *fakeQuerier {
	return &fakeQuerier{
		projectIDByKey: map[string]string{"demo": testProjectID},
		sprintByID:     map[string]gen.GetSprintByIDRow{},
	}
}

// sprintJSON は応答を読むための型。
//
// **sprintView をそのまま使わない。** Date / Time は表記を固定するための
// MarshalJSON しか持たず（apitime.go）、読み戻す側は文字列で受ける。
// 既存のテスト（me_tokens_test など）が Time を文字列で読んでいるのと同じ。
type sprintJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Goal        *string `json:"goal"`
	StartAt     *int64  `json:"start_at"`
	EndAt       *int64  `json:"end_at"`
	AllDay      bool    `json:"all_day"`
	Status      string  `json:"status"`
	TicketCount int64   `json:"ticket_count"`
	ClosedCount int64   `json:"closed_count"`
}

type sprintListJSON struct {
	Items []sprintJSON `json:"items"`
}

func decodeSprint(t *testing.T, rec *httptest.ResponseRecorder) sprintJSON {
	t.Helper()
	var v sprintJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── GET（9.12）──────────────────────────────────────────────

func TestListSprintsReturnsCounts(t *testing.T) {
	q := sprintFake()
	q.sprintRows = []gen.ListSprintsByProjectRow{
		{ID: "01K2SPRINT000000000000003", Name: "Sprint 3", Goal: txt("認証を通す"),
			StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true,
			Status: "active", TicketCount: 12, ClosedCount: 5},
		{ID: "01K2SPRINT000000000000002", Name: "Sprint 2",
			StartAt: jstAt("2026-07-22"), EndAt: jstEnd("2026-08-04"), AllDay: true,
			Status: "completed", TicketCount: 14, ClosedCount: 14},
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.listSprints(rec, sprintReq(http.MethodGet, "/api/v1/projects/demo/sprints", "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body sprintListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d件, want 2", len(body.Items))
	}
	// 並びはクエリのまま（新しいものが上）。
	if body.Items[0].Name != "Sprint 3" {
		t.Errorf("並びがクエリの順と違う: %+v", body.Items)
	}
	if body.Items[0].TicketCount != 12 || body.Items[0].ClosedCount != 5 {
		t.Errorf("件数 = %d/%d, want 5/12", body.Items[0].ClosedCount, body.Items[0].TicketCount)
	}
	// goal 未設定は null（空文字にしない）。
	if body.Items[1].Goal != nil {
		t.Errorf("goal = %v, want null", *body.Items[1].Goal)
	}
}

// **日付は YYYY-MM-DD で出す**（時刻とタイムゾーンを持たない。apitime.Date）。
func TestListSprintsFormatsDatesAsPlainDate(t *testing.T) {
	q := sprintFake()
	q.sprintRows = []gen.ListSprintsByProjectRow{
		{ID: testSprintID, Name: "Sprint 3",
			StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true, Status: "active"},
	}
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.listSprints(rec, sprintReq(http.MethodGet, "/api/v1/projects/demo/sprints", "", ""))

	got := rec.Body.String()
	// 予定はエポックミリ秒の半開区間（9.3.1）。終わりは締切日の翌日の0時。
	if !strings.Contains(got, `"start_at":`+msOf(jstAt("2026-08-05"))) {
		t.Errorf("start_at がエポックミリ秒でない: %s", got)
	}
	if !strings.Contains(got, `"end_at":`+msOf(jstEnd("2026-08-18"))) || !strings.Contains(got, `"all_day":true`) {
		t.Errorf("end_at / all_day が違う: %s", got)
	}
}

func TestListSprintsHasNoPagination(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.listSprints(rec, sprintReq(http.MethodGet, "/api/v1/projects/demo/sprints", "", ""))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("応答のキー = %v, want items のみ", keysOf(raw))
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("ETag = %q, want 空", etag)
	}
}

// ── POST（9.12）─────────────────────────────────────────────

func TestCreateSprintDefaultsToPlanned(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints",
		`{"name":"Sprint 4"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.createdSprints[0].Status; got != sprintStatusPlanned {
		t.Errorf("status = %q, want planned（既定）", got)
	}
	if got := decodeSprint(t, rec); got.Status != "planned" {
		t.Errorf("応答の status = %q", got.Status)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/projects/demo/sprints/") {
		t.Errorf("Location = %q", loc)
	}
}

func TestCreateSprintStoresDatesAndGoal(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints",
		`{"name":"Sprint 4","goal":"  バックログを作る  ","start_at":`+msOf(jstAt("2026-08-19"))+`,"end_at":`+msOf(jstEnd("2026-09-01"))+`,"status":"active"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	got := q.createdSprints[0]
	if got.Goal.String != "バックログを作る" {
		t.Errorf("goal = %q（トリムしていない）", got.Goal.String)
	}
	if got.StartAt == nil || !got.StartAt.Equal(*jstAt("2026-08-19")) || !got.AllDay {
		t.Errorf("start_at = %v all_day = %v", got.StartAt, got.AllDay)
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}
}

// **一意制約が無い**（B-7）。同名でも作れる。
func TestCreateSprintAllowsDuplicateName(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints",
			`{"name":"Sprint 4"}`, ""))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%d回目: status = %d, want 201 (%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if len(q.createdSprints) != 2 {
		t.Errorf("CreateSprint = %d回, want 2", len(q.createdSprints))
	}
}

func TestCreateSprintRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{"名前が空", `{"name":"  "}`, "name"},
		{"51文字", `{"name":"` + strings.Repeat("あ", 51) + `"}`, "name"},
		{"状態が値域外", `{"name":"S","status":"done"}`, "status"},
		{"日時が文字列", `{"name":"S","start_at":"2026-08-05"}`, "start_at"},
		{"終日なのに0時でない", `{"name":"S","start_at":` + msOf(plusMS(jstAt("2026-08-05"), 1)) + `}`, "start_at"},
		{"終了が開始より前", `{"name":"S","start_at":` + msOf(jstAt("2026-08-18")) + `,"end_at":` + msOf(jstEnd("2026-08-05")) + `}`, "end_at"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := sprintFake()
			h := &handler{q: q}
			rec := httptest.NewRecorder()
			h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints", c.body, ""))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+c.wantField+`"`) {
				t.Errorf("details の field が %q でない: %s", c.wantField, rec.Body.String())
			}
			if len(q.createdSprints) != 0 {
				t.Errorf("検証に失敗したのに INSERT した")
			}
		})
	}
}

// 開始 == 終了は通る（ck_sprint_dates は <= である）。
func TestCreateSprintAllowsSameDay(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints",
		`{"name":"S","start_at":`+msOf(jstAt("2026-08-05"))+`,"end_at":`+msOf(jstEnd("2026-08-05"))+`}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（開始と終了が同日は許す）(%s)", rec.Code, rec.Body.String())
	}
}

// 片方だけの日付は関係の検証をすり抜けてよい（CHECK も NULL を許す）。
func TestCreateSprintAllowsOnlyOneDate(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.createSprint(rec, sprintReq(http.MethodPost, "/api/v1/projects/demo/sprints",
		`{"name":"S","end_at":`+msOf(jstEnd("2026-08-05"))+`}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.createdSprints[0].StartAt != nil {
		t.Errorf("start_at が NULL でない: %v", q.createdSprints[0].StartAt)
	}
}

// ── PATCH（9.12）────────────────────────────────────────────

// **片方だけ送ったときに現在値と突き合わせる**（ck_sprint_dates）。
// これを怠ると DB の CHECK 違反になり 500 が返る。
func TestPatchSprintValidatesDatesAgainstCurrentValues(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 3",
		StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true, Status: "active",
	}
	h := &handler{q: q}

	// 終了日だけを開始日より前へ動かす。
	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"end_at":`+msOf(jstEnd("2026-08-01"))+`}`, testSprintID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（現在の start_date と矛盾する）(%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedSprints) != 0 {
		t.Errorf("検証に失敗したのに UPDATE した")
	}
}

// 同じ PATCH で両方送って辻褄が合うなら通る。
func TestPatchSprintAcceptsBothDatesTogether(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 3",
		StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true, Status: "active",
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"start_at":`+msOf(jstAt("2026-07-01"))+`,"end_at":`+msOf(jstEnd("2026-07-14"))+`}`, testSprintID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeSprint(t, rec)
	if got.StartAt == nil || *got.StartAt != jstAt("2026-07-01").UnixMilli() {
		t.Errorf("start_at が更新されていない: %v", got.StartAt)
	}
}

// **null は「消す」、キーが無いのは「据え置き」**（9.12）。
func TestPatchSprintNullClearsAndAbsentKeeps(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 3", Goal: txt("認証を通す"),
		StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true, Status: "active",
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"goal":null}`, testSprintID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.updatedSprints[0]
	if !arg.SetGoal || arg.Goal.Valid {
		t.Errorf("goal を NULL にしていない: %+v", arg)
	}
	// 送っていない日付は触らない。
	if arg.SetStartAt || arg.SetEndAt {
		t.Errorf("送っていない日付を更新した: %+v", arg)
	}
	if got := decodeSprint(t, rec); got.Goal != nil {
		t.Errorf("goal = %q, want null", *got.Goal)
	}
	// 据え置いた日付は残っている。
	if got := decodeSprint(t, rec); got.StartAt == nil {
		t.Errorf("据え置くはずの start_at が消えた")
	}
}

// 日付を null で消す。
func TestPatchSprintClearsDate(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 3",
		StartAt: jstAt("2026-08-05"), EndAt: jstEnd("2026-08-18"), AllDay: true, Status: "active",
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"start_at":null}`, testSprintID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeSprint(t, rec); got.StartAt != nil {
		t.Errorf("start_at が消えていない: %v", *got.StartAt)
	}
}

func TestPatchSprintChangesStatus(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 3", Status: "active",
	}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"status":"completed"}`, testSprintID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeSprint(t, rec); got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

func TestPatchSprintRejectsInvalidStatus(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{ID: testSprintID, Name: "S", Status: "active"}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/"+testSprintID,
		`{"status":"archived"}`, testSprintID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedSprints) != 0 {
		t.Errorf("検証に失敗したのに UPDATE した")
	}
}

func TestPatchSprintNotFound(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.patchSprint(rec, sprintReq(http.MethodPatch, "/api/v1/projects/demo/sprints/01K2SPRINT000000000000009",
		`{"name":"x"}`, "01K2SPRINT000000000000009"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── DELETE（9.12）───────────────────────────────────────────

func TestDeleteSprintReturns204(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{ID: testSprintID, Name: "Sprint 3", TicketCount: 12}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	h.deleteSprint(rec, sprintReq(http.MethodDelete, "/api/v1/projects/demo/sprints/"+testSprintID, "", testSprintID))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.deletedSprints) != 1 || q.deletedSprints[0].ProjectID != testProjectID {
		t.Errorf("project_id で絞って削除していない: %+v", q.deletedSprints)
	}
}

func TestDeleteSprintNotFound(t *testing.T) {
	q := sprintFake()
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.deleteSprint(rec, sprintReq(http.MethodDelete, "/api/v1/projects/demo/sprints/01K2SPRINT000000000000009",
		"", "01K2SPRINT000000000000009"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}
