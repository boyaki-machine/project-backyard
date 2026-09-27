package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// チケット1件のAPI（ApiDesign.md 9.5 / 9.6 / 9.7）の単体テスト。手順17a。
//
// 認可（ticket.view / edit / delete / transition）はミドルウェアの責務なので
// ここでは通さない（routes_test.go が宣言を見ている）。ここで確かめるのは、
// 入力の検証・楽観ロック・オンステージの規則・遷移の5段階・activity の粒度である。
//
// **PATCH の assignee_id だけは例外で、ハンドラ内の権限判定をここで測る**
// （必要権限が本文の内容で変わり、ルート宣言では表せないため。9.5.2）。

// ── 素材 ────────────────────────────────────────────────────

// ticketDetailFake は seq=31 のチケット1件が読める状態のフェイクを返す。
func ticketDetailFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.bySeq[31] = ticketDetailRow()
	q.ticket.idBySeq[31] = testTicketID
	q.ticket.typeByID = map[string]string{}
	q.ticket.updateRows = 1
	q.ticket.deleteRows = 1
	q.ticket.projectTagCount = 2
	// sprint_id は 9.3 / 9.5.2 から外れたので、参照先の検証は無くなった。
	q.ticket.isMember = true
	return q
}

func ticketDetailRow() gen.GetTicketBySeqRow {
	now := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)
	return gen.GetTicketBySeqRow{
		ID: testTicketID, Seq: 31, Type: "task", Title: "認証APIの実装",
		BodyMd:         txt("# 概要\n\nローカルID/PW認証のAPIを実装する。"),
		StatusKey:      "in_progress",
		StatusName:     txt("進行中"),
		StatusCategory: txt("in_progress"),
		Priority:       txt("high"),
		AssigneeID:     txt(testActorID),
		AssigneeKind:   txt("user"),
		AssigneeName:   txt("田中"),
		ReporterID:     txt(testActorID),
		ReporterKind:   txt("user"),
		ReporterName:   txt("田中"),
		SortKey:        txt("0|n:"),
		// **DDL の既定と同じ値を置く**（DbDesign.md 6.6）。実物では
		// execution_mode が NOT NULL DEFAULT 'agent_draft'（0025）、scope が NOT NULL DEFAULT '{}'
		// であり、零値のフェイクだと 9.5.1 の応答が実サーバと違う形になる（手順27）。
		ExecutionMode: "agent_draft",
		Scope:         []byte(`{}`),
		Version:       3,
		CreatedAt:     ts(now),
		UpdatedAt:     ts(now),
	}
}

// withReviewWorkflow は DbDesign.md 7.4 の with_review テンプレートをフェイクへ入れる。
//
// **実物と同じ定義を使う。** 9.7 の「定義が無い先も返す」は in_progress → done の
// 定義が無いことで初めて測れるので、作り話の遷移表では意味が変わる。
func withReviewWorkflow(q *fakeQuerier) {
	q.ticket.workflowID = txt("01JZZZZZZZZZZZZZZZZZZZZZW2")
	q.ticket.workflowStatuses = []gen.ListWorkflowStatusesRow{
		{Key: "todo", Name: "未着手", Category: "todo", SortOrder: 1, IsAgentReachable: true},
		{Key: "in_progress", Name: "進行中", Category: "in_progress", SortOrder: 2, IsAgentReachable: true},
		{Key: "review", Name: "レビュー中", Category: "review", SortOrder: 3, IsAgentReachable: true},
		{Key: "done", Name: "完了", Category: "done", SortOrder: 4, IsAgentReachable: false},
	}
	tr := func(from, to, perm string, kinds string) gen.ListWorkflowTransitionsRow {
		return gen.ListWorkflowTransitionsRow{
			FromStatusKey: from, ToStatusKey: to,
			RequiredPermission: txt(perm), AllowedActorKinds: []byte(kinds),
		}
	}
	q.ticket.workflowTransitions = []gen.ListWorkflowTransitionsRow{
		tr("todo", "in_progress", "ticket.transition", `["user","agent"]`),
		tr("in_progress", "review", "ticket.transition", `["user","agent"]`),
		tr("review", "in_progress", "ticket.transition", `["user","agent"]`),
		tr("review", "done", "ticket.close", `["user"]`),
		tr("in_progress", "todo", "ticket.transition", `["user","agent"]`),
	}
}

// detailReq は 9.5 / 9.6 / 9.7 のリクエストを組み立てる。
//
// perms はプロジェクトの実効権限。**ミドルウェアが載せるものをここでも載せる**
// ——ハンドラは auth.ProjectAuthzFromContext から読むため、載せないと
// 「権限が無い」と同じ扱いになる（9.5.2 の ticket.assign、9.6 の検証5）。
func detailReq(method, target, body, seq string, perms ...string) *http.Request {
	return detailReqAs(auth.ActorKindUser, method, target, body, seq, perms...)
}

func detailReqAs(kind, method, target, body, seq string, perms ...string) *http.Request {
	req := ticketReq(method, target, body, seq)
	ctx := auth.NewPrincipalContext(req.Context(), &auth.Principal{
		ActorID: testActorID, ActorKind: kind,
	})
	ctx = auth.NewProjectAuthzContext(ctx, &auth.ProjectAuthz{
		Key: "demo", ProjectID: testProjectID, Reachable: true, Permissions: perms,
	})
	return req.WithContext(ctx)
}

// patchReq は If-Match 付きの PATCH。ifMatch が空ならヘッダを付けない（2.8）。
func patchReq(body, ifMatch string, perms ...string) *http.Request {
	req := detailReq(http.MethodPatch, "/api/v1/projects/demo/tickets/31", body, "31", perms...)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	return req
}

// callPatch は PATCH を1回叩く。既定で ticket.assign を持たせる——
// **担当者以外の項目を測るテストが、権限で落ちて意味を失わないようにする。**
func callPatch(q *fakeQuerier, body, ifMatch string, perms ...string) *httptest.ResponseRecorder {
	if len(perms) == 0 {
		perms = []string{"ticket.edit", permTicketAssign}
	}
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.updateTicket(rec, patchReq(body, ifMatch, perms...))
	return rec
}

// ── 9.5.1 GET ───────────────────────────────────────────────

func TestGetTicketReturnsDetailShape(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.ActualPoint = pgtype.Float8{Float64: 5, Valid: true}
	row.ActualPointVersion = txt("actual-v0")
	q.ticket.bySeq[31] = row
	q.ticket.commentNum = 4
	q.ticket.children = []gen.ListTicketChildrenBriefRow{
		{Seq: 44, Title: "ログイン", Type: "task", StatusKey: "todo",
			StatusName: txt("未着手"), StatusCategory: txt("todo")},
	}

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if view["actual_point"] != float64(5) || view["actual_point_version"] != "actual-v0" {
		t.Errorf("実績ポイントと版 = %v / %v", view["actual_point"], view["actual_point_version"])
	}

	// 9.5.1 は「9.2 の items[] に6項目を加えたもの」。**6項目すべてが在ること。**
	for _, key := range []string{"body_md", "parent", "children", "dod", "links", "comment_count"} {
		if _, ok := view[key]; !ok {
			t.Errorf("詳細に %q が無い（9.5.1 の追加項目）", key)
		}
	}
	// 一覧の項目も引き続き在る（型で埋め込んでいることの確認）。
	for _, key := range []string{"seq", "type", "title", "status", "version", "staged_at"} {
		if _, ok := view[key]; !ok {
			t.Errorf("詳細に %q が無い（9.2.2 の項目）", key)
		}
	}
	if got := view["comment_count"]; got != float64(4) {
		t.Errorf("comment_count = %v, want 4（手順17a から実数）", got)
	}
	children, _ := view["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("children = %v, want 1件", view["children"])
	}
	if view["parent"] != nil {
		t.Errorf("parent = %v, want null（親を持たない行）", view["parent"])
	}
	// epic は**キーごと在って null**。親が無ければ祖先もたどらない。
	if v, ok := view["epic"]; !ok || v != nil {
		t.Errorf("epic = %v（在る=%v）, want null（親を持たない行）", v, ok)
	}
	if slices.Contains(q.opLog, "GetTicketEpicAncestor") {
		t.Error("親が無いのに祖先のエピックを引いた")
	}
}

// ── 9.5.1 epic──────────────────────────────────────

// detailWithParent は seq=31 の親を seq=12 にしたフェイクを返す。parentType が親の種別。
func detailWithParent(parentType string) *fakeQuerier {
	q := ticketDetailFake()
	row := ticketDetailRow()
	row.ParentSeq = pgtype.Int4{Int32: 12, Valid: true}
	q.ticket.bySeq[31] = row
	q.ticket.idBySeq[12] = testTicketID4
	q.ticket.briefByID[testTicketID4] = gen.GetTicketBriefRow{
		Seq: 12, Title: "認証", Type: parentType, StatusKey: "todo",
		StatusName: txt("未着手"), StatusCategory: txt("todo"),
	}
	return q
}

func epicSeqOf(t *testing.T, view map[string]any) any {
	t.Helper()
	e, ok := view["epic"].(map[string]any)
	if !ok {
		return view["epic"]
	}
	return e["seq"]
}

// 親がエピックなら、それが答えである。**祖先をたどる往復を足さない。**
func TestGetTicketEpicIsParentWhenParentIsEpic(t *testing.T) {
	q := detailWithParent("epic")
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := epicSeqOf(t, viewOf(t, rec)); got != float64(12) {
		t.Errorf("epic.seq = %v, want 12（親そのもの）", got)
	}
	if slices.Contains(q.opLog, "GetTicketEpicAncestor") {
		t.Error("親がエピックなのに祖先をたどった")
	}
}

// 親がエピックでなければ、祖先をたどった結果が入る。
func TestGetTicketEpicWalksAncestorsWhenParentIsNotEpic(t *testing.T) {
	q := detailWithParent("story")
	q.ticket.epicAncestorByID = map[string]gen.GetTicketEpicAncestorRow{
		testTicketID: {Seq: 9, Title: "認証基盤", Type: "epic", StatusKey: "todo",
			StatusName: txt("未着手"), StatusCategory: txt("todo")},
	}
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := epicSeqOf(t, viewOf(t, rec)); got != float64(9) {
		t.Errorf("epic.seq = %v, want 9（祖先のエピック）", got)
	}
}

// 祖先にエピックが無いのは誤りではない。**0行を 500 にしない。**
func TestGetTicketEpicIsNullWhenNoAncestorEpic(t *testing.T) {
	q := detailWithParent("story")
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := epicSeqOf(t, viewOf(t, rec)); got != nil {
		t.Errorf("epic = %v, want null", got)
	}
	if !slices.Contains(q.opLog, "GetTicketEpicAncestor") {
		t.Error("祖先をたどっていない（null の理由が検証になっていない）")
	}
}

// **comment_count は 0 固定ではない。** 遷移がコメントを作る以上、
// 固定値のままだと事実と食い違う（A-6 の判断）。
func TestGetTicketCommentCountIsReal(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.commentNum = 0

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))
	if got := viewOf(t, rec)["comment_count"]; got != float64(0) {
		t.Fatalf("comment_count = %v, want 0", got)
	}
	if countOps(q.opLog, "CountTicketComments") != 1 {
		t.Errorf("CountTicketComments が呼ばれていない（0 を直書きしている）: %v", q.opLog)
	}
}

func TestGetTicketNotFound(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/999", "", "999"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 9.5.2 PATCH：楽観ロック（2.8）──────────────────────────────

// **まず「通る」ことを確かめてから、落ちる側を測る**（LEARNINGS #35）。
func TestPatchTicketSucceedsWithMatchingVersion(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"title":"新しいタイトル"}`, `"3"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := viewOf(t, rec)["title"]; got != "新しいタイトル" {
		t.Errorf("title = %v, want 新しいタイトル（応答は更新後の値）", got)
	}
	if len(q.ticket.updated) != 1 || q.ticket.updated[0].Version != 3 {
		t.Errorf("UpdateTicket の version = %v, want 3", q.ticket.updated)
	}
}

func TestPatchTicketRequiresIfMatch(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"title":"x"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "If-Match", "required") {
		t.Errorf("details = %v, want If-Match/required", errorOf(t, rec).Details)
	}
}

func TestPatchTicketConflictOnStaleVersion(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.updateRows = 0 // WHERE version = ... に当たらなかった
	rec := callPatch(q, `{"title":"x"}`, `"2"`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchTicketNotFound(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	req := detailReq(http.MethodPatch, "/api/v1/projects/demo/tickets/999",
		`{"title":"x"}`, "999", "ticket.edit")
	req.Header.Set("If-Match", `"1"`)
	h.updateTicket(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 9.5.2 PATCH：受け付けない項目（3系統）──────────────────────

func TestPatchTicketRejectsServerOwnedFields(t *testing.T) {
	cases := []struct {
		field string
		body  string
		code  string
	}{
		{"id", `{"id":"01K2"}`, "immutable_field"},
		{"seq", `{"seq":99}`, "immutable_field"},
		{"version", `{"version":9}`, "immutable_field"},
		{"created_at", `{"created_at":"2026-01-01T00:00:00Z"}`, "immutable_field"},
		{"updated_at", `{"updated_at":"2026-01-01T00:00:00Z"}`, "immutable_field"},
		{"reporter_id", `{"reporter_id":"01K2"}`, "immutable_field"},
		{"sort_key", `{"sort_key":"0|x:"}`, "use_move_endpoint"},
		{"staged_at", `{"staged_at":"2026-01-01T00:00:00Z"}`, "use_move_endpoint"},
		{"status_key", `{"status_key":"done"}`, "use_transition_endpoint"},
		{"closed_at", `{"closed_at":"2026-01-01T00:00:00Z"}`, "use_transition_endpoint"},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			q := ticketDetailFake()
			rec := callPatch(q, c.body, `"3"`)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), c.field, c.code) {
				t.Errorf("details = %v, want %s/%s", errorOf(t, rec).Details, c.field, c.code)
			}
			if len(q.ticket.updated) != 0 {
				t.Errorf("弾いたのに UPDATE が走っている: %v", q.ticket.updated)
			}
		})
	}
}

// **値が現在と同じでも弾く**（9.5.2 の方針。「送れば通ることがある」にしない）。
func TestPatchTicketRejectsServerOwnedFieldEvenWhenUnchanged(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"status_key":"in_progress"}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 9.5.2 PATCH：形の検証 ────────────────────────────────────

func TestPatchTicketValidation(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{"タイトルが空", `{"title":"   "}`, "title", "required"},
		{"タイトルが長い", fmt.Sprintf(`{"title":%q}`, strings.Repeat("あ", 201)), "title", "too_long"},
		{"タイトルが null", `{"title":null}`, "title", "required"},
		{"種別が不正", `{"type":"bug"}`, "type", "invalid"},
		{"種別が null", `{"type":null}`, "type", "invalid"},
		{"優先度が不正", `{"priority":"urgent"}`, "priority", "invalid"},
		{"見積が負", `{"estimate_point":-1}`, "estimate_point", "out_of_range"},
		{"実績が負", `{"actual_hours":-0.5}`, "actual_hours", "out_of_range"},
		{"実績ポイントが負", `{"actual_point":-1,"actual_point_version":"actual-v0"}`, "actual_point", "out_of_range"},
		{"算出式の版が無い", `{"actual_point":3}`, "actual_point_version", "required"},
		{"算出式の版が不正", `{"actual_point":3,"actual_point_version":"old"}`, "actual_point_version", "invalid"},
		{"日付の形式", `{"due_date":"2026/08/14"}`, "due_date", "invalid"},
		{"日付に時刻", `{"due_date":"2026-08-14T00:00:00Z"}`, "due_date", "invalid"},
		{"親の番号が0", `{"parent_seq":0}`, "parent_seq", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ticketDetailFake()
			rec := callPatch(q, c.body, `"3"`)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), c.field, c.code) {
				t.Errorf("details = %v, want %s/%s", errorOf(t, rec).Details, c.field, c.code)
			}
		})
	}
}

func TestPatchActualPointRequiresPMGrantAndStoresPair(t *testing.T) {
	body := `{"actual_point":5,"actual_point_version":"actual-v0"}`
	for _, perms := range [][]string{{"ticket.edit"}, {"ticket.self_edit"}} {
		q := ticketDetailFake()
		rec := callPatch(q, body, `"3"`, perms...)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%v: status=%d, want 403 (%s)", perms, rec.Code, rec.Body.String())
		}
	}
	q := ticketDetailFake()
	rec := callPatch(q, body, `"3"`, "ticket.self_edit", permTicketActualPointEdit)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.updated) != 1 || !q.ticket.updated[0].ActualPoint.Valid || q.ticket.updated[0].ActualPoint.Float64 != 5 || q.ticket.updated[0].ActualPointVersion.String != "actual-v0" {
		t.Errorf("更新内容=%+v", q.ticket.updated)
	}
}

// 開始日と期限の前後（DbDesign.md 6.6 の ck_ticket_dates）。
// **片方だけ送られたときは現在値と比べる。**
func TestPatchTicketRejectsDueDateBeforeStartDate(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.StartDate = pgtype.Date{Time: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), Valid: true}
	q.ticket.bySeq[31] = row

	rec := callPatch(q, `{"due_date":"2026-08-14"}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "due_date", "invalid") {
		t.Errorf("details = %v, want due_date/invalid", errorOf(t, rec).Details)
	}
}

// ── 9.5.2 PATCH：null で空にする ──────────────────────────────

// **「送られていない」と「null が送られた」を区別する。**
// ポインタだけの実装ではここが通らない。
func TestPatchTicketNullClearsField(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"assignee_id":null,"due_date":null,"estimate_point":null}`, `"3"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	for _, c := range []struct {
		name  string
		set   bool
		valid bool
	}{
		{"assignee_id", arg.AssigneeIDSet, arg.AssigneeID.Valid},
		{"due_date", arg.DueDateSet, arg.DueDate.Valid},
		{"estimate_point", arg.EstimatePointSet, arg.EstimatePoint.Valid},
	} {
		if !c.set {
			t.Errorf("%s の Set = false, want true（null は「空にする」）", c.name)
		}
		if c.valid {
			t.Errorf("%s が NULL になっていない", c.name)
		}
	}
}

// 送られていない項目は触らない。
func TestPatchTicketLeavesUnsentFieldsAlone(t *testing.T) {
	q := ticketDetailFake()
	if rec := callPatch(q, `{"title":"x"}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	if arg.AssigneeIDSet || arg.DueDateSet || arg.BodyMdSet || arg.PrioritySet {
		t.Errorf("送っていない項目の Set が立っている: %+v", arg)
	}
	if arg.Type.Valid {
		t.Errorf("送っていない type に値が入っている: %v", arg.Type)
	}
}

// ── 9.5.2 PATCH：タグの置き換え ───────────────────────────────

// **丸ごと置き換える**（部分更新ではない。9.5.2）。
func TestPatchTicketReplacesTags(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"tag_ids":["01TAG0000000000000000001","01TAG0000000000000000002"]}`, `"3"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.detached) != 1 {
		t.Errorf("DetachTicketTags が呼ばれていない: %v", q.ticket.detached)
	}
	if len(q.ticket.attached) != 2 {
		t.Errorf("付け直したタグ = %d件, want 2", len(q.ticket.attached))
	}
}

// 空配列でタグを全て外す（9.5.2）。
func TestPatchTicketClearsTagsWithEmptyArray(t *testing.T) {
	q := ticketDetailFake()
	if rec := callPatch(q, `{"tag_ids":[]}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.detached) != 1 || len(q.ticket.attached) != 0 {
		t.Errorf("外す=%v 付ける=%v, want 外す1件・付ける0件", q.ticket.detached, q.ticket.attached)
	}
}

// **タグだけ変えても UPDATE が走る**（9.2.5 の ETag が変わるため。引き継ぎの要件）。
func TestPatchTicketTouchesRowWhenOnlyTagsChange(t *testing.T) {
	q := ticketDetailFake()
	// **当該プロジェクトに在るタグの件数は、送る件数と一致させる**——
	// CountProjectTagsByIDs は「渡した ID がすべて自プロジェクトのものか」を
	// 数えるので（9.3）、ずれていると 422 not_found になる。
	q.ticket.projectTagCount = 1
	if rec := callPatch(q, `{"tag_ids":["01TAG0000000000000000001"]}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.updated) != 1 {
		t.Fatalf("UpdateTicket が走っていない（updated_at が動かず ETag が変わらない）")
	}
}

func TestPatchTicketRejectsForeignTag(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.projectTagCount = 0 // 当該プロジェクトに無い
	rec := callPatch(q, `{"tag_ids":["01TAGFOREIGN000000000001"]}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "tag_ids", "not_found") {
		t.Errorf("details = %v, want tag_ids/not_found", errorOf(t, rec).Details)
	}
}

// ── 9.5.2 PATCH：親（parent_cycle）────────────────────────────

// 先に「親を付けられる」ことを確かめる（LEARNINGS #35）。
func TestPatchTicketSetsParent(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[12] = testTicketID4
	q.ticket.typeByID[testTicketID4] = "epic"

	rec := callPatch(q, `{"parent_seq":12}`, `"3"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	if !arg.ParentIDSet || arg.ParentID.String != testTicketID4 {
		t.Errorf("parent_id = %+v, want %s", arg.ParentID, testTicketID4)
	}
}

func TestPatchTicketRejectsParentCycle(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[44] = testTicketID2
	q.ticket.descendant = true // 44 は 31 の子孫

	rec := callPatch(q, `{"parent_seq":44}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "parent_seq", "parent_cycle") {
		t.Errorf("details = %v, want parent_seq/parent_cycle", errorOf(t, rec).Details)
	}
}

func TestPatchTicketDetachesParentWithNull(t *testing.T) {
	q := ticketDetailFake()
	if rec := callPatch(q, `{"parent_seq":null}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	if !arg.ParentIDSet || arg.ParentID.Valid {
		t.Errorf("parent_id = %+v, want NULL（親を外す）", arg.ParentID)
	}
}

// ── 9.5.2 PATCH：オンステージの規則（A-4／D-2）──────────────────

// **まず「バックログにいる行なら通る」ことを確かめる。**
// これを先に測らないと、下の2件は「常に弾いている」実装でも緑になる。
func TestPatchTicketAllowsEpicWhenNotStaged(t *testing.T) {
	q := ticketDetailFake() // staged_at は NULL
	if rec := callPatch(q, `{"type":"epic"}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchTicketRejectsEpicWhileStaged(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.StagedAt = tsp(time.Now())
	q.ticket.bySeq[31] = row

	rec := callPatch(q, `{"type":"epic"}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "type", "not_stageable") {
		t.Errorf("details = %v, want type/not_stageable", errorOf(t, rec).Details)
	}
	if len(q.ticket.updated) != 0 {
		t.Errorf("弾いたのに UPDATE が走っている")
	}
}

func TestPatchTicketRejectsNonEpicParentWhileStaged(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.StagedAt = tsp(time.Now())
	q.ticket.bySeq[31] = row
	q.ticket.idBySeq[44] = testTicketID2
	q.ticket.typeByID[testTicketID2] = "story" // エピックではない

	rec := callPatch(q, `{"parent_seq":44}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "parent_seq", "not_stageable") {
		t.Errorf("details = %v, want parent_seq/not_stageable", errorOf(t, rec).Details)
	}
}

// オンステージでも、親がエピックなら通る（9.4.1 と同じ条件）。
func TestPatchTicketAllowsEpicParentWhileStaged(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.StagedAt = tsp(time.Now())
	q.ticket.bySeq[31] = row
	q.ticket.idBySeq[12] = testTicketID4
	q.ticket.typeByID[testTicketID4] = "epic"

	if rec := callPatch(q, `{"parent_seq":12}`, `"3"`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 9.5.2 PATCH：ticket.assign（本文で必要権限が変わる）──────────

// 先に「持っていれば変えられる」ことを確かめる。
func TestPatchTicketAssigneeAllowedWithAssignPermission(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"assignee_id":"`+testActorID+`"}`, `"3"`,
		"ticket.edit", permTicketAssign)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchTicketAssigneeForbiddenWithoutAssignPermission(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"assignee_id":"`+testActorID+`"}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.updated) != 0 {
		t.Errorf("403 なのに UPDATE が走っている")
	}
}

// **ticket.assign が無くても、担当以外は編集できる。**
// これを測らないと「常に 403」の実装でも上の1件は通る。
func TestPatchTicketOtherFieldsAllowedWithoutAssignPermission(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"title":"担当は触らない"}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// 担当を外す（null）のも「変える」に含む。
func TestPatchTicketClearingAssigneeNeedsAssignPermission(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"assignee_id":null}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPatchTicketRejectsNonMemberAssignee(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.isMember = false
	rec := callPatch(q, `{"assignee_id":"`+testActorID+`"}`, `"3"`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "assignee_id", "not_a_member") {
		t.Errorf("details = %v, want assignee_id/not_a_member", errorOf(t, rec).Details)
	}
}

// ── 9.5.1 / 9.5.2：エージェントの契約4項目（手順27）──────────────
//
// **期待値は ApiDesign.md 9.5.1 / 9.5.2 から取る。** 4項目を足した理由は
// pb_get_task が Requirements.md 10.3.2 の約束（スコープ境界・実行主体属性・
// readiness を返す）を果たせていなかったことで、pb_get_task は 9.5.1 を
// そのまま返す（Design.md 8.5.2）ため、ここが返さない限り届かない。

func TestGetTicketReturnsAgentContractFields(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.getTicket(rec, detailReq(http.MethodGet, "/api/v1/projects/demo/tickets/31", "", "31"))

	view := viewOf(t, rec)
	for _, key := range []string{"execution_mode", "readiness", "readiness_note", "scope"} {
		if _, ok := view[key]; !ok {
			t.Errorf("詳細に %q が無い（9.5.1。手順27 で追加）", key)
		}
	}
	if got := view["execution_mode"]; got != "agent_draft" {
		t.Errorf("execution_mode = %v, want agent_draft（DDL の既定。0025）", got)
	}
	// **未設定でも null にせず {} を返す**（9.5.1）。「境界が無い」と
	// 「項目が無い」は違うもので、パックが前者に文を当てる。
	scope, ok := view["scope"].(map[string]any)
	if !ok || len(scope) != 0 {
		t.Errorf("scope = %v, want {}", view["scope"])
	}
	// custom_fields は足していない（9.5.1。読む相手がまだ居ない）。
	if _, ok := view["custom_fields"]; ok {
		t.Errorf("custom_fields が載っている（9.5.1 は足さないと決めている）")
	}
}

func TestPatchTicketWritesScopeAndExecutionMode(t *testing.T) {
	q := ticketDetailFake()
	body := `{"execution_mode":"agent_draft","readiness":"yellow",` +
		`"readiness_note":"認証方式が未決","scope":{"allow":["src/auth/**"],"deny":["migrations/**"]}}`
	rec := callPatch(q, body, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	if arg.ExecutionMode.String != "agent_draft" {
		t.Errorf("execution_mode = %q, want agent_draft", arg.ExecutionMode.String)
	}
	if !arg.ReadinessSet || arg.Readiness.String != "yellow" {
		t.Errorf("readiness = %+v, want yellow", arg.Readiness)
	}
	if !arg.ReadinessNoteSet || arg.ReadinessNote.String != "認証方式が未決" {
		t.Errorf("readiness_note = %+v", arg.ReadinessNote)
	}
	if !strings.Contains(string(arg.Scope), "src/auth/**") {
		t.Errorf("scope = %s", arg.Scope)
	}
}

// **未知のキーは拒まずそのまま保存する**（9.5.2。9.15 と同じ判断）。
func TestPatchTicketKeepsUnknownScopeKeys(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"scope":{"approval":["本番デプロイ"]}}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(q.ticket.updated[0].Scope), "approval") {
		t.Errorf("未知のキーが落ちている: %s", q.ticket.updated[0].Scope)
	}
}

func TestPatchTicketRejectsInvalidAgentContractFields(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"実行モードが不正", `{"execution_mode":"robot"}`, "execution_mode"},
		{"実行モードが null", `{"execution_mode":null}`, "execution_mode"},
		{"Readiness が不正", `{"readiness":"orange"}`, "readiness"},
		{"scope が null", `{"scope":null}`, "scope"},
		{"scope が配列", `{"scope":["src/**"]}`, "scope"},
		{"allow が配列でない", `{"scope":{"allow":"src/**"}}`, "scope"},
		{"allow の要素が文字列でない", `{"scope":{"allow":[1,2]}}`, "scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ticketDetailFake()
			rec := callPatch(q, c.body, `"3"`, "ticket.edit")
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			// **新しい details[].code を発明しない**（9.5.2）。既存の invalid で表す。
			if !hasDetail(errorOf(t, rec), c.field, "invalid") {
				t.Errorf("details = %v, want %s/invalid", errorOf(t, rec).Details, c.field)
			}
		})
	}
}

// null は「未判定へ戻す」（9.5.2）。execution_mode / scope と扱いが違う。
func TestPatchTicketClearsReadinessWithNull(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"readiness":null,"readiness_note":null}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	arg := q.ticket.updated[0]
	if !arg.ReadinessSet || arg.Readiness.Valid {
		t.Errorf("readiness = %+v, want NULL を書く", arg.Readiness)
	}
}

// **ticket.assign は要らない**（9.5.2）。追加の権限が要るのは「誰がやるか」を
// 決める操作だけで、境界と実行モードは「何をしてよいか」である。
func TestPatchTicketScopeDoesNotNeedAssignPermission(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"scope":{"allow":["src/**"]},"execution_mode":"agent_only"}`,
		`"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// **scope は old/new を残す**（9.5.2。body_md のように落とさない）。
func TestPatchTicketRecordsScopeChange(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"scope":{"allow":["src/**"]}}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d件, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Field.String != "scope" {
		t.Fatalf("field = %q, want scope", a.Field.String)
	}
	if a.OldValue.String != "{}" || !strings.Contains(a.NewValue.String, "src/**") {
		t.Errorf("old/new = %q/%q（境界の変更は履歴に残す）", a.OldValue.String, a.NewValue.String)
	}
}

// **同じ内容の送り直しは記録しない**（9.5.2）。DB の jsonb と送られた JSON は
// 並びが違うので、均さないと「変わっていない」を判定できない。
func TestPatchTicketDoesNotRecordUnchangedScope(t *testing.T) {
	q := ticketDetailFake()
	row := q.ticket.bySeq[31]
	row.Scope = []byte(`{"deny": ["migrations/**"], "allow": ["src/**"]}`)
	q.ticket.bySeq[31] = row

	rec := callPatch(q, `{"scope":{"allow":["src/**"],"deny":["migrations/**"]}}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %v, want 0件（内容が同じ）", q.ticket.activities)
	}
}

// ── 9.5.2 PATCH：activity の粒度（A-2 / A-3／D-4）────────────────

func TestPatchTicketRecordsOneActivityPerChangedField(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"title":"新題","priority":"low"}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 2 {
		t.Fatalf("activity = %d件, want 2（変更した項目ごとに1行）", len(q.ticket.activities))
	}
	byField := map[string]gen.InsertActivityParams{}
	for _, a := range q.ticket.activities {
		if a.Action != "update" {
			t.Errorf("action = %q, want update", a.Action)
		}
		byField[a.Field.String] = a
	}
	title, ok := byField["title"]
	if !ok {
		t.Fatalf("title の行が無い: %v", byField)
	}
	if title.OldValue.String != "認証APIの実装" || title.NewValue.String != "新題" {
		t.Errorf("title の old/new = %q/%q, want 認証APIの実装/新題",
			title.OldValue.String, title.NewValue.String)
	}
	pri, ok := byField["priority"]
	if !ok {
		t.Fatalf("priority の行が無い: %v", byField)
	}
	if pri.OldValue.String != "high" || pri.NewValue.String != "low" {
		t.Errorf("priority の old/new = %q/%q, want high/low",
			pri.OldValue.String, pri.NewValue.String)
	}
}

// **body_md は field だけ残し、値は載せない**（9.5.2。9.13.2 の応答を軽く保つ）。
func TestPatchTicketDoesNotRecordBodyValues(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"body_md":"まったく別の長い本文"}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d件, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Field.String != "body_md" {
		t.Errorf("field = %q, want body_md", a.Field.String)
	}
	if a.OldValue.Valid || a.NewValue.Valid {
		t.Errorf("old/new = %v/%v, want どちらも NULL（本文は載せない）", a.OldValue, a.NewValue)
	}
}

// **同じ値を送っても記録しない**（変更が生じていないため。9.5.2）。
func TestPatchTicketDoesNotRecordUnchangedValues(t *testing.T) {
	q := ticketDetailFake()
	rec := callPatch(q, `{"title":"認証APIの実装","priority":"high"}`, `"3"`, "ticket.edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %v, want 0件（値が変わっていない）", q.ticket.activities)
	}
	// ただし version は上がる（2.8 の規約）。
	if len(q.ticket.updated) != 1 {
		t.Errorf("UPDATE は走るべき（version を +1 する）")
	}
}

// ── 9.5.3 DELETE ────────────────────────────────────────────

func TestDeleteTicketReturns204AndRecordsActivity(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.deleteTicket(rec, detailReq(http.MethodDelete,
		"/api/v1/projects/demo/tickets/31", "", "31", "ticket.delete"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 に本文がある: %q", rec.Body.String())
	}
	if len(q.ticket.deleted) != 1 || q.ticket.deleted[0].Seq != 31 {
		t.Errorf("DeleteTicket = %v, want seq=31", q.ticket.deleted)
	}
	// **activity は消さず delete を1行足す**（A-1／D-3）。
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d件, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "delete" {
		t.Errorf("action = %q, want delete", a.Action)
	}
	if a.EntityID != testTicketID {
		t.Errorf("entity_id = %q, want %q（seq ではなく ULID）", a.EntityID, testTicketID)
	}
	if a.Field.Valid || a.OldValue.Valid || a.NewValue.Valid {
		t.Errorf("削除の行に field/old/new が入っている: %+v", a)
	}
}

func TestDeleteTicketNotFound(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.deleteTicket(rec, detailReq(http.MethodDelete,
		"/api/v1/projects/demo/tickets/999", "", "999", "ticket.delete"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("404 なのに activity を書いている: %v", q.ticket.activities)
	}
}

// ── 9.6 transition ──────────────────────────────────────────

func callTransition(q *fakeQuerier, body string, perms ...string) *httptest.ResponseRecorder {
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.transitionTicket(rec, detailReq(http.MethodPost,
		"/api/v1/projects/demo/tickets/31/transition", body, "31", perms...))
	return rec
}

// **まず通る遷移を確かめてから、断られる側を測る**（LEARNINGS #35）。
func TestTransitionTicketSucceeds(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)

	rec := callTransition(q, `{"to":"review"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	status, _ := view["status"].(map[string]any)
	if status["key"] != "review" {
		t.Errorf("status.key = %v, want review（応答は 9.5.1 形式）", status)
	}
	if len(q.ticket.statusSet) != 1 || q.ticket.statusSet[0].StatusKey != "review" {
		t.Errorf("SetTicketStatus = %v, want review", q.ticket.statusSet)
	}
	// activity は action='transition'、field は status_key（キーのまま）。
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d件, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "transition" || a.Field.String != "status_key" {
		t.Errorf("activity = %+v, want transition/status_key", a)
	}
	if a.OldValue.String != "in_progress" || a.NewValue.String != "review" {
		t.Errorf("old/new = %q/%q, want in_progress/review", a.OldValue.String, a.NewValue.String)
	}
}

// closed_at は category='done' のときだけ立つ（9.6 の表）。
func TestTransitionTicketSetsClosedAtOnDone(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row

	rec := callTransition(q, `{"to":"done"}`, "ticket.transition", "ticket.close")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !q.ticket.statusSet[0].Closing {
		t.Errorf("closing = false, want true（category=done なら closed_at を立てる）")
	}
	if viewOf(t, rec)["closed_at"] == nil {
		t.Errorf("closed_at が null のまま")
	}
}

// done から戻すと closed_at は NULL へ戻る（9.6 の表）。
func TestTransitionTicketClearsClosedAtLeavingDone(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	row.ClosedAt = tsp(time.Now())
	q.ticket.bySeq[31] = row

	rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if q.ticket.statusSet[0].Closing {
		t.Errorf("closing = true, want false（done 以外は NULL へ戻す）")
	}
	if viewOf(t, rec)["closed_at"] != nil {
		t.Errorf("closed_at = %v, want null", viewOf(t, rec)["closed_at"])
	}
}

// 検証1：ワークフローに無いステータス → 422 unknown_status。
func TestTransitionTicketRejectsUnknownStatus(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	rec := callTransition(q, `{"to":"archived"}`, "ticket.transition")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "to", "unknown_status") {
		t.Errorf("details = %v, want to/unknown_status", errorOf(t, rec).Details)
	}
}

// 検証2：定義が無い遷移 → 409 invalid_transition。
func TestTransitionTicketRejectsUndefinedTransition(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q) // in_progress → done の定義は無い
	rec := callTransition(q, `{"to":"done"}`, "ticket.transition", "ticket.close")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "invalid_transition" {
		t.Errorf("code = %q, want invalid_transition", e.Code)
	}
	if !strings.Contains(e.Message, "進行中") || !strings.Contains(e.Message, "完了") {
		t.Errorf("message = %q, want ステータスの表示名を含む", e.Message)
	}
}

// 検証5：required_permission を持たない → 403。
func TestTransitionTicketRejectsMissingRequiredPermission(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row

	// review → done は ticket.close を要する（DbDesign.md 7.4）。
	rec := callTransition(q, `{"to":"done"}`, "ticket.transition")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if msg := errorOf(t, rec).Message; !strings.Contains(msg, "ticket.close") {
		t.Errorf("message = %q, want ticket.close を含む", msg)
	}
	if len(q.ticket.statusSet) != 0 {
		t.Errorf("403 なのにステータスを書いている")
	}
}

// 検証3：allowed_actor_kinds に含まれない種別 → 403。
//
// **フェイクで負の側を作る。**
func TestTransitionTicketRejectsDisallowedActorKind(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	// review → done は allowed_actor_kinds が ["user"]。
	h.transitionTicket(rec, detailReqAs(auth.ActorKindAgent, http.MethodPost,
		"/api/v1/projects/demo/tickets/31/transition", `{"to":"done"}`, "31",
		"ticket.transition", "ticket.close"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if msg := errorOf(t, rec).Message; !strings.Contains(msg, "エージェント") {
		t.Errorf("message = %q, want エージェント を含む", msg)
	}
}

// コメントを添えると kind='progress' のコメントが同じ流れで作られる（9.6）。
func TestTransitionTicketCreatesProgressComment(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)

	rec := callTransition(q,
		`{"to":"review","comment":"レビューをお願いします"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.comments) != 1 {
		t.Fatalf("コメント = %d件, want 1", len(q.ticket.comments))
	}
	c := q.ticket.comments[0]
	if c.Kind != "progress" {
		t.Errorf("kind = %q, want progress", c.Kind)
	}
	if c.Origin != "human" {
		t.Errorf("origin = %q, want human", c.Origin)
	}
	if c.BodyMd != "レビューをお願いします" || c.TicketID != testTicketID {
		t.Errorf("コメント = %+v", c)
	}
	// **comment_count に反映される**（9.5.1 が実数を返すため）。
	if got := viewOf(t, rec)["comment_count"]; got != float64(1) {
		t.Errorf("comment_count = %v, want 1", got)
	}
}

func TestTransitionTicketWithoutCommentCreatesNone(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	if rec := callTransition(q, `{"to":"review"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.comments) != 0 {
		t.Errorf("コメント = %v, want 0件", q.ticket.comments)
	}
}

func TestTransitionTicketRequiresTo(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	rec := callTransition(q, `{"comment":"x"}`, "ticket.transition")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "to", "required") {
		t.Errorf("details = %v, want to/required", errorOf(t, rec).Details)
	}
}

// **コメントの上限は 9.8 と同じ20000字**（9.6）。ちょうどは通る。
// 多バイト文字で数える——バイト数で数えると日本語が3分の1で弾かれる。
func TestTransitionTicketCommentAtLimit(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	comment := strings.Repeat("あ", commentBodyMaxLen)
	rec := callTransition(q, `{"to":"review","comment":"`+comment+`"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String()[:200])
	}
	if len(q.ticket.comments) != 1 || q.ticket.comments[0].BodyMd != comment {
		t.Errorf("コメント = %d件, want 上限ちょうどの1件", len(q.ticket.comments))
	}
}

// **上限を超えたら 422 で、遷移もコメントも履歴も起きない**（9.6「本体の検査を先に行う」）。
func TestTransitionTicketRejectsLongComment(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	comment := strings.Repeat("あ", commentBodyMaxLen+1)
	rec := callTransition(q, `{"to":"review","comment":"`+comment+`"}`, "ticket.transition")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "comment", "too_long") {
		t.Errorf("details = %v, want comment/too_long", errorOf(t, rec).Details)
	}
	if len(q.ticket.statusSet) != 0 {
		t.Errorf("SetTicketStatus = %v, want 呼ばれない（状態は変わらない）", q.ticket.statusSet)
	}
	if len(q.ticket.comments) != 0 || len(q.ticket.activities) != 0 {
		t.Errorf("コメント %d件・activity %d件, want どちらも0件",
			len(q.ticket.comments), len(q.ticket.activities))
	}
}

// **前後の空白は数えない**（9.8 と同じ数え方）。作るコメントも空白を除いた本文である。
func TestTransitionTicketCountsCommentWithoutSpaces(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	comment := "  " + strings.Repeat("あ", commentBodyMaxLen) + "  "
	rec := callTransition(q, `{"to":"review","comment":"`+comment+`"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// **本体の誤りはまとめて返す。** to の欠けと comment の長すぎを1回で知らせる。
func TestTransitionTicketReportsAllBodyErrors(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	comment := strings.Repeat("あ", commentBodyMaxLen+1)
	rec := callTransition(q, `{"comment":"`+comment+`"}`, "ticket.transition")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if !hasDetail(e, "to", "required") || !hasDetail(e, "comment", "too_long") {
		t.Errorf("details = %v, want to/required と comment/too_long", e.Details)
	}
}

// ワークフローを持たないプロジェクトでは、どの遷移も 422 unknown_status（検証1）。
func TestTransitionTicketWithoutWorkflow(t *testing.T) {
	q := ticketDetailFake() // workflowID は無効のまま
	rec := callTransition(q, `{"to":"done"}`, "ticket.transition")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 9.7 transitions ─────────────────────────────────────────

func decodeTransitions(t *testing.T, rec *httptest.ResponseRecorder) transitionsView {
	t.Helper()
	var v transitionsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// **items[] はワークフローの全ステータス（現在を除く）**（A-5／D-1）。
// ── 検証7：未完了の子が残っている親は完了にできない（9.6）──────

// **まず通る側を測る。** これが無いと、下の 409 は「完了へは常に 409」の実装でも
// 緑になる（LEARNINGS #139）。
func TestTransitionTicketAllowsDoneWhenNoOpenChildren(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row
	q.ticket.openChildren = 0

	rec := callTransition(q, `{"to":"done"}`, "ticket.transition", "ticket.close")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestTransitionTicketRejectsDoneWithOpenChildren(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row
	q.ticket.openChildren = 1

	rec := callTransition(q, `{"to":"done"}`, "ticket.transition", "ticket.close")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	// **403 ではなく 409。** 権限の問題ではない——同じ人が、子を完了させたあとなら通る。
	if e.Code != "children_not_closed" {
		t.Errorf("code = %q, want children_not_closed", e.Code)
	}
	if e.Message != "未完了の子チケットが残っているため完了にできません" {
		t.Errorf("message = %q（9.7 の reason と同じ文字列であること）", e.Message)
	}
	if len(q.ticket.statusSet) != 0 {
		t.Errorf("拒んだのにステータスを書いている: %v", q.ticket.statusSet)
	}
}

// **完了へ進むときだけ数える**（9.6）。他の遷移で結果に効かないクエリを毎回
// 増やさない。
func TestTransitionTicketCountsChildrenOnlyForDone(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	q.ticket.openChildren = 3 // 効かないはず

	rec := callTransition(q, `{"to":"review"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if slices.Contains(q.opLog, "CountOpenChildren") {
		t.Errorf("完了以外の遷移で子を数えている: %v", q.opLog)
	}
}

// 9.7 も同じ関数を通る——**画面が「押せる完了」を出したあとで 409 にならない。**
func TestListTransitionsMarksOpenChildren(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row
	q.ticket.openChildren = 2

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31/transitions", "", "31",
		"ticket.transition", "ticket.close"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var got transitionsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	var done *transitionOptionView
	for i := range got.Items {
		if got.Items[i].Key == "done" {
			done = &got.Items[i]
		}
	}
	if done == nil {
		t.Fatalf("items に done が無い: %+v", got.Items)
	}
	if done.Allowed {
		t.Errorf("done が allowed:true（未完了の子が2件ある）")
	}
	if done.Reason == nil ||
		*done.Reason != "未完了の子チケットが残っているため完了にできません" {
		t.Errorf("reason = %v（9.6 の message と同じ文字列であること）", done.Reason)
	}
}

// ── 子が動いたら親を進行中にする（9.6）──────────────────

// cascadeFake は seq=31 が未着手で、親（seq=44）を持つ状態を作る。
func cascadeFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "todo"
	q.ticket.bySeq[31] = row
	q.ticket.parentForCascade = map[string]gen.GetParentForCascadeRow{
		testTicketID: {ID: testTicketID2, Seq: 44, StatusKey: "todo",
			StatusCategory: txt("todo")},
	}
	// **祖先の行も bySeq に要る。** SetTicketStatus は seq で引いて書き戻すので、
	// 行が無いと ErrNoRows になる（実物では、親は GetParentForCascade が
	// 返した時点で必ず在る）。
	for _, seq := range []int32{44, 55} {
		row := ticketDetailRow()
		row.Seq = seq
		row.StatusKey = "todo"
		q.ticket.bySeq[seq] = row
	}
	return q
}

func TestTransitionFromTodoAdvancesParent(t *testing.T) {
	q := cascadeFake(t)

	rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 2 {
		t.Fatalf("SetTicketStatus = %d回, want 2（子と親）: %+v", len(q.ticket.statusSet), q.ticket.statusSet)
	}
	parent := q.ticket.statusSet[1]
	if parent.Seq != 44 || parent.StatusKey != "in_progress" {
		t.Errorf("親の更新 = %+v, want seq=44 / in_progress", parent)
	}
	// **closed_at は動かない**（in_progress は done ではない）。
	if parent.Closing {
		t.Errorf("親で closing = true, want false")
	}
	// **activity は子と親で1行ずつ。** コメントは作らない（本文が無い）。
	if len(q.ticket.activities) != 2 {
		t.Fatalf("activity = %d件, want 2: %+v", len(q.ticket.activities), q.ticket.activities)
	}
	a := q.ticket.activities[1]
	if a.Action != "transition" || a.OldValue.String != "todo" || a.NewValue.String != "in_progress" {
		t.Errorf("親の activity = %+v, want transition todo→in_progress", a)
	}
	// **親に working_agent_id は立てない**（人が呼んだので子にも立たない）。
	if slices.Contains(q.opLog, "SetTicketWorkingAgent") {
		t.Errorf("working_agent_id を立てている: %v", q.opLog)
	}
}

// 祖先まで連鎖する。
func TestTransitionCascadesThroughAncestors(t *testing.T) {
	q := cascadeFake(t)
	q.ticket.parentForCascade[testTicketID2] = gen.GetParentForCascadeRow{
		ID: "01TICKET0000000000000GRAND", Seq: 55, StatusKey: "todo",
		StatusCategory: txt("todo"),
	}

	if rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 3 {
		t.Fatalf("SetTicketStatus = %d回, want 3（子・親・祖父）: %+v",
			len(q.ticket.statusSet), q.ticket.statusSet)
	}
	if q.ticket.statusSet[2].Seq != 55 {
		t.Errorf("3件目 = seq %d, want 55", q.ticket.statusSet[2].Seq)
	}
}

// **親が既に動いていれば打ち切る。** その上の祖先も見ない——この規則自体が
// 親を進めるとき同じ経路を通るので、todo でない親の上に todo の祖先は残らない。
func TestTransitionStopsAtStartedAncestor(t *testing.T) {
	q := cascadeFake(t)
	q.ticket.parentForCascade[testTicketID] = gen.GetParentForCascadeRow{
		ID: testTicketID2, Seq: 44, StatusKey: "in_progress",
		StatusCategory: txt("in_progress"),
	}
	q.ticket.parentForCascade[testTicketID2] = gen.GetParentForCascadeRow{
		ID: "01TICKET0000000000000GRAND", Seq: 55, StatusKey: "todo",
		StatusCategory: txt("todo"),
	}

	if rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 1 {
		t.Errorf("SetTicketStatus = %d回, want 1（子だけ）: %+v",
			len(q.ticket.statusSet), q.ticket.statusSet)
	}
}

// 未着手を出ていない遷移では連動しない（in_progress → review）。
func TestTransitionDoesNotCascadeWhenAlreadyStarted(t *testing.T) {
	q := cascadeFake(t)
	row := q.ticket.bySeq[31]
	row.StatusKey = "in_progress"
	q.ticket.bySeq[31] = row

	if rec := callTransition(q, `{"to":"review"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 1 {
		t.Errorf("SetTicketStatus = %d回, want 1（子だけ）: %+v",
			len(q.ticket.statusSet), q.ticket.statusSet)
	}
	if slices.Contains(q.opLog, "GetParentForCascade") {
		t.Errorf("未着手を出ていないのに親をたどっている: %v", q.opLog)
	}
}

// 親を持たなければ何も起きない（GetParentForCascade が 0件）。
func TestTransitionWithoutParentDoesNothing(t *testing.T) {
	q := cascadeFake(t)
	q.ticket.parentForCascade = map[string]gen.GetParentForCascadeRow{}

	if rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 1 {
		t.Errorf("SetTicketStatus = %d回, want 1: %+v", len(q.ticket.statusSet), q.ticket.statusSet)
	}
}

// **順路が定義されていなければ黙って飛ばす。** 親のワークフローの都合で子の遷移を
// 失敗させると、関係のないチケットが着手できなくなる（9.6）。
func TestCascadeSkipsWhenRouteUndefined(t *testing.T) {
	q := cascadeFake(t)
	// todo → in_progress の定義を落とす（子の遷移も同じ順路を使うので、
	// 子は review へ動かして測る）。
	q.ticket.workflowTransitions = []gen.ListWorkflowTransitionsRow{
		{FromStatusKey: "todo", ToStatusKey: "review",
			RequiredPermission: txt("ticket.transition"), AllowedActorKinds: []byte(`["user"]`)},
	}

	rec := callTransition(q, `{"to":"review"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（連動の失敗で子を落とさない） (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.statusSet) != 1 {
		t.Errorf("SetTicketStatus = %d回, want 1（子だけ）: %+v",
			len(q.ticket.statusSet), q.ticket.statusSet)
	}
}

func TestListTransitionsIncludesUndefinedTargets(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31/transitions", "", "31", "ticket.transition"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeTransitions(t, rec)

	if v.Current == nil || v.Current.Key != "in_progress" {
		t.Fatalf("current = %+v, want in_progress", v.Current)
	}
	// with_review は4ステータス。現在（in_progress）を除いた3件が出る。
	if len(v.Items) != 3 {
		t.Fatalf("items = %d件, want 3（現在を除く全ステータス）: %+v", len(v.Items), v.Items)
	}
	byKey := map[string]transitionOptionView{}
	for _, it := range v.Items {
		byKey[it.Key] = it
	}
	if _, ok := byKey["in_progress"]; ok {
		t.Errorf("現在のステータスが items に混ざっている")
	}

	// 定義がある先は allowed:true で reason を持たない。
	for _, key := range []string{"todo", "review"} {
		it, ok := byKey[key]
		if !ok {
			t.Fatalf("%s が items に無い", key)
		}
		if !it.Allowed {
			t.Errorf("%s の allowed = false, want true（定義があり権限もある）", key)
		}
		if it.Reason != nil {
			t.Errorf("%s に reason が付いている: %v", key, *it.Reason)
		}
	}

	// **定義が無い done も返る。** 隠すとレビューを通す必要が読めなくなる。
	done, ok := byKey["done"]
	if !ok {
		t.Fatalf("done が items に無い（定義が無い先も返すこと。9.7）")
	}
	if done.Allowed {
		t.Errorf("done の allowed = true, want false（in_progress → done の定義は無い）")
	}
	if done.Reason == nil || !strings.Contains(*done.Reason, "直接進められません") {
		t.Errorf("done の reason = %v, want 定義が無い旨", done.Reason)
	}
	if done.Name != "完了" || done.Category != "done" {
		t.Errorf("done = %+v, want name=完了 category=done", done)
	}
}

// 権限が足りない先は allowed:false ＋ 権限キーの reason（検証5）。
func TestListTransitionsMarksMissingPermission(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	// ticket.close を持たない。
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31/transitions", "", "31", "ticket.transition"))

	v := decodeTransitions(t, rec)
	for _, it := range v.Items {
		if it.Key != "done" {
			continue
		}
		if it.Allowed {
			t.Fatalf("done の allowed = true, want false（ticket.close が無い）")
		}
		if it.Reason == nil || !strings.Contains(*it.Reason, "ticket.close") {
			t.Fatalf("reason = %v, want ticket.close 権限が必要です", it.Reason)
		}
		return
	}
	t.Fatalf("done が items に無い: %+v", v.Items)
}

// **持っていれば allowed:true になる。** これを測らないと、
// 「常に false」の実装でも上の1件は通る。
func TestListTransitionsAllowsWithPermission(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "review"
	q.ticket.bySeq[31] = row

	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31/transitions", "", "31",
		"ticket.transition", "ticket.close"))

	v := decodeTransitions(t, rec)
	for _, it := range v.Items {
		if it.Key == "done" && !it.Allowed {
			t.Fatalf("done の allowed = false, want true（ticket.close を持つ）: %v", it.Reason)
		}
	}
}

// ワークフローを持たないプロジェクトでは items は空（current だけ返る）。
func TestListTransitionsWithoutWorkflow(t *testing.T) {
	q := ticketDetailFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/31/transitions", "", "31", "ticket.transition"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeTransitions(t, rec)
	if len(v.Items) != 0 {
		t.Errorf("items = %+v, want 0件", v.Items)
	}
	if v.Current == nil {
		t.Errorf("current が無い")
	}
}

func TestListTransitionsNotFound(t *testing.T) {
	q := ticketDetailFake()
	withReviewWorkflow(q)
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.listTicketTransitions(rec, detailReq(http.MethodGet,
		"/api/v1/projects/demo/tickets/999/transitions", "", "999", "ticket.transition"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 着手したらオンステージへ上げる（9.6）────────────────

// stagingFake は seq=31 が未着手で、表示上のトップレベルの祖先を持つ状態を作る。
//
// **祖先は自分ではない。** 段に置けるのは表示上のトップレベルだけで（9.4.1）、
// 配下は親と一緒に運ばれる——子タスクに着手したとき動くのは部分木の根である。
func stagingFake(t *testing.T, root gen.GetDisplayRootForStagingRow) *fakeQuerier {
	t.Helper()
	q := ticketDetailFake()
	withReviewWorkflow(q)
	row := q.ticket.bySeq[31]
	row.StatusKey = "todo"
	q.ticket.bySeq[31] = row
	q.ticket.displayRoot = map[string]gen.GetDisplayRootForStagingRow{testTicketID: root}
	return q
}

// 未着手を出たら、表示上のトップレベルの祖先が段へ上がる。
func TestTransitionFromTodoStagesDisplayRoot(t *testing.T) {
	q := stagingFake(t, gen.GetDisplayRootForStagingRow{
		ID: testTicketID2, Type: "story", Staged: false,
	})

	rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.stagedSet) != 1 {
		t.Fatalf("SetTicketStagedAt = %d回, want 1: %+v", len(q.ticket.stagedSet), q.ticket.stagedSet)
	}
	// **上げるのは自分ではなく祖先である。**
	if got := q.ticket.stagedSet[0].ID; got != testTicketID2 {
		t.Errorf("段へ上げた相手 = %q, want %q（表示上のトップレベルの祖先）", got, testTicketID2)
	}
	if q.ticket.stagedSet[0].StagedAt == nil {
		t.Error("staged_at に値が入っていない（NULL のままではバックログ段に残る）")
	}
	// **sort_key を動かさない**（9.4。二段は順序キーを1本共有する）。
	if len(q.ticket.setSortKey) != 0 {
		t.Errorf("sort_key を動かしている: %+v（着手のたびに消化順が壊れる）", q.ticket.setSortKey)
	}
}

// 既にオンステージなら何もしない（二重に上げない）。
func TestTransitionDoesNotRestageWhenAlreadyStaged(t *testing.T) {
	q := stagingFake(t, gen.GetDisplayRootForStagingRow{
		ID: testTicketID2, Type: "story", Staged: true,
	})

	if rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.stagedSet) != 0 {
		t.Errorf("既にオンステージなのに上げ直している: %+v", q.ticket.stagedSet)
	}
}

// **エピックは段に置けない**（9.4.1）。親を持たないエピックを着手させても上げない。
func TestTransitionDoesNotStageEpic(t *testing.T) {
	q := stagingFake(t, gen.GetDisplayRootForStagingRow{
		ID: testTicketID2, Type: "epic", Staged: false,
	})

	if rec := callTransition(q, `{"to":"in_progress"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.stagedSet) != 0 {
		t.Errorf("エピックを段へ上げている: %+v（どちらの段にも行として出ない）", q.ticket.stagedSet)
	}
}

// **未着手カテゴリの中の遷移では上げない。** 「未着手だがオンステージ」を
// 手で作れることは変わらず、着手していないものを勝手に仕掛りへ混ぜない。
func TestTransitionWithinTodoDoesNotStage(t *testing.T) {
	q := stagingFake(t, gen.GetDisplayRootForStagingRow{
		ID: testTicketID2, Type: "story", Staged: false,
	})
	// todo → todo は順路が無いので、逆に「進行中から未着手へ戻す」を測る。
	row := q.ticket.bySeq[31]
	row.StatusKey = "in_progress"
	q.ticket.bySeq[31] = row

	if rec := callTransition(q, `{"to":"todo"}`, "ticket.transition"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// **逆向きの連動は持たない**——降ろしもしないし、上げもしない。
	if len(q.ticket.stagedSet) != 0 {
		t.Errorf("未着手へ戻したのに段を触っている: %+v", q.ticket.stagedSet)
	}
}

// ── ticket.self_edit の絞り込み（9.5.2。0029）────────────
//
// **まず通る側を確かめてから、断られる側を測る**（憲章）。狭い権限でも
// 記述の修正は通ること、縛りの側の項目だけが 403 になることの両方を見る。

// selfEditPerms は ticket.self_edit だけを持つ呼び出し元（エージェント）。
//
// **ticket.assign は既定スコープに入っている**ので一緒に持たせる
// （Design.md 6.5）。これが無いと assignee_id の検証で落ち、
// **何を測っているのか分からなくなる。**
var selfEditPerms = []string{permTicketSelfEdit, permTicketAssign}

func TestPatchAllowsDescriptionForSelfEdit(t *testing.T) {
	q := ticketDetailFake()

	rec := callPatch(q, `{"title":"直したタイトル","body_md":"直した本文"}`, `"3"`,
		selfEditPerms...)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// **縛る側の項目は断る。** エージェントが自分の実行モードやスコープ境界を
// 緩められては、自己編集の制約条件が成り立たない。
func TestPatchRejectsGuardFieldsForSelfEdit(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"実行モード", `{"execution_mode":"agent_only"}`, "execution_mode"},
		{"readiness", `{"readiness":"green"}`, "readiness"},
		{"readiness_note", `{"readiness_note":"よい"}`, "readiness_note"},
		{"スコープ境界", `{"scope":{"allow":["**"]}}`, "scope"},
		{"種別", `{"type":"epic"}`, "type"},
		{"実行者", `{"working_agent_id":null}`, "working_agent_id"},
		{"実績時間", `{"actual_hours":3}`, "actual_hours"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ticketDetailFake()
			rec := callPatch(q, c.body, `"3"`, selfEditPerms...)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
			}
			// **どの項目で断ったかを本文に出す**——「権限がありません」だけでは、
			// どれを外せば通るのかが分からない。
			if !strings.Contains(rec.Body.String(), c.field) {
				t.Errorf("断った項目名が本文に無い: %s", rec.Body.String())
			}
			if len(q.ticket.updated) != 0 {
				t.Error("403 なのに更新している")
			}
		})
	}
}

// **ticket.edit を持つ人は従来どおり全部変えられる。** self_edit は部分集合で
// あって、画面の振る舞いを変えるものではない。
func TestPatchAllowsGuardFieldsForTicketEdit(t *testing.T) {
	q := ticketDetailFake()

	rec := callPatch(q, `{"execution_mode":"agent_only","type":"story"}`, `"3"`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
