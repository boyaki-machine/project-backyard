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

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 委任（Delegation.md 4章・5章・12.2）の単体テスト。pb-246。
//
// 認可（delegation.manage / ticket.create / ticket.transition / ticket.edit）は
// ミドルウェアの責務なのでここでは通さない。ここで確かめるのは、
// delegation.status だけで判定できる範囲の強制（計画中・停止では書かせない、
// 木の外を指させない、木の境界をエージェントにまたがせない）と、
// 委任の作成そのものの検証である。

const (
	testMainActorID      = "01K2AGENT0000000000MAIN001"
	testEvaluatorActorID = "01K2AGENT0000000000EVAL001"
	testOtherAgentID     = "01K2AGENT0000000000OTHR001"
	testDelegationRootID = testTicketID // seq 31 を根に使う
	testOutsideTicketID  = "01K2TKT00000000000OUTSIDE1"
)

func delegationReq(method, target, body, seq string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	rc.URLParams.Add("seq", seq)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID, ActorKind: auth.ActorKindUser})
	return req.WithContext(ctx)
}

// ── POST /tickets/{seq}/delegation ────────────────────────────

func TestCreateDelegationHappyPath(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticketForDelegation = map[string]gen.GetTicketForDelegationRow{
		testTicketID: {ExecutionMode: "agent_draft"}, // ParentID 無効 = 根
	}
	q.delegationByIDRow = gen.GetDelegationByIDRow{
		ID: "01K2DELEGATION00000000001", Status: "planning",
		RootSeq:     31,
		MainActorID: testMainActorID, MainActorKind: "agent", MainActorName: "main-bot",
		EvaluatorActorID: testEvaluatorActorID, EvaluatorActorKind: "agent", EvaluatorActorName: "eval-bot",
		CreatedByID: testActorID, CreatedByKind: "user", CreatedByName: "田中",
		Scope:        []byte(`{"goal":"合格する"}`),
		ScopeVersion: 0,
		CreatedAt:    time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
	}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDelegation(rec, delegationReq(http.MethodPost, "/delegation",
		`{"main_actor_id":"`+testMainActorID+`","evaluator_actor_id":"`+testEvaluatorActorID+`","scope":{"goal":"合格する"}}`,
		"31"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}
	if len(q.createdDelegations) != 1 {
		t.Fatalf("CreateDelegation の回数 = %d", len(q.createdDelegations))
	}
	c := q.createdDelegations[0]
	if c.MainActorID != testMainActorID || c.EvaluatorActorID != testEvaluatorActorID {
		t.Errorf("main/evaluator = %q/%q", c.MainActorID, c.EvaluatorActorID)
	}
	if q.setExecutionModeTo[testTicketID] != "delegated" {
		t.Errorf("execution_mode = %q, want delegated", q.setExecutionModeTo[testTicketID])
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if body["status"] != "planning" {
		t.Errorf("status = %v, want planning（作った直後は常に計画中）", body["status"])
	}
}

func TestCreateDelegationRejectsNonRootTicket(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticketForDelegation = map[string]gen.GetTicketForDelegationRow{
		testTicketID: {ParentID: txt("01K2TKT00000000000PARENT1"), ExecutionMode: "agent_draft"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDelegation(rec, delegationReq(http.MethodPost, "/delegation",
		`{"main_actor_id":"`+testMainActorID+`","evaluator_actor_id":"`+testEvaluatorActorID+`","scope":{}}`,
		"31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "seq", "invalid") {
		t.Errorf("details = %v, want seq/invalid", errorOf(t, rec).Details)
	}
}

func TestCreateDelegationRejectsSameMainAndEvaluator(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDelegation(rec, delegationReq(http.MethodPost, "/delegation",
		`{"main_actor_id":"`+testMainActorID+`","evaluator_actor_id":"`+testMainActorID+`","scope":{}}`,
		"31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "evaluator_actor_id", "invalid") {
		t.Errorf("details = %v, want evaluator_actor_id/invalid（ck_delegation_independent と同じ条件）",
			errorOf(t, rec).Details)
	}
}

func TestCreateDelegationRejectsUnusableAgent(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticketForDelegation = map[string]gen.GetTicketForDelegationRow{
		testTicketID: {ExecutionMode: "agent_draft"},
	}
	q.agentUnusable = map[string]bool{testMainActorID: true}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDelegation(rec, delegationReq(http.MethodPost, "/delegation",
		`{"main_actor_id":"`+testMainActorID+`","evaluator_actor_id":"`+testEvaluatorActorID+`","scope":{}}`,
		"31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "main_actor_id", "not_found") {
		t.Errorf("details = %v, want main_actor_id/not_found", errorOf(t, rec).Details)
	}
}

func TestCreateDelegationRejectsNonObjectScope(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createDelegation(rec, delegationReq(http.MethodPost, "/delegation",
		`{"main_actor_id":"`+testMainActorID+`","evaluator_actor_id":"`+testEvaluatorActorID+`","scope":[1,2]}`,
		"31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "scope", "invalid") {
		t.Errorf("details = %v, want scope/invalid", errorOf(t, rec).Details)
	}
}

// ── 12.2：ticket.create の強制 ──────────────────────────────────

func ticketReqAsAgent(method, target, body, seq, actorID string) *http.Request {
	req := ticketReq(method, target, body, seq)
	ctx := auth.NewPrincipalContext(req.Context(), &auth.Principal{
		ActorID: actorID, ActorKind: auth.ActorKindAgent,
	})
	return req.WithContext(ctx)
}

func TestCreateTicketBlocksMainDuringPlanning(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testDelegationRootID
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "planning"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習1","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateTicketRequiresParentSeqForMain(t *testing.T) {
	q := ticketFake()
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "approved"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習1"}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateTicketRejectsParentOutsideTree(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[99] = testOutsideTicketID
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "approved"},
	}
	q.ticket.descendant = false // 99 は木の外
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習1","parent_seq":99}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateTicketAllowsMainInsideApprovedTree(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testDelegationRootID
	q.ticket.sortRowBySeq[31] = gen.GetTicketSortRowRow{ID: testDelegationRootID, SortKey: txt("0|n:")}
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "approved"},
	}
	q.ticket.descendant = true // 31（根）自身は木の中
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習1","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateTicketUnaffectedForNonMainAgent(t *testing.T) {
	q := ticketFake()
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "planning"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	// testOtherAgentID はどの委任の main でもない。委任があっても影響されない。
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"普段の仕事"}`, "", testOtherAgentID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 完了レポートの提出ゲート ────────────────────────────────────

func TestSubmitReportBlockedDuringPlanning(t *testing.T) {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "planning"},
	}
	q.ticket.descendant = true // testTicketID 自身が根
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitReportAllowedWhenApproved(t *testing.T) {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// TestSubmitReportBlockedWhileEvaluationPending は5章「main：成果の提出
// （根の完了レポート）」の「承認済＋評価待ち」=不可を確かめる（pb-248）。
func TestSubmitReportBlockedWhileEvaluationPending(t *testing.T) {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	// evaluationVerdictByReport は未設定＝まだ評価されていない＝評価待ち。
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// TestSubmitReportAllowedAfterRejection は「承認済＋差し戻し」=可（出し直し）
// を確かめる（pb-248）。
func TestSubmitReportAllowedAfterRejection(t *testing.T) {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	q.evaluationVerdictByReport = map[string]string{"01K2REPORT0000000000001": "fail"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitReportUnaffectedWithoutDelegation(t *testing.T) {
	q := reportFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 木の境界をまたぐ移動 ────────────────────────────────────────

// testDelegationTreeRootID は移動テスト専用の委任の根。
//
// **testTicketID 自身を根にしない。** 委任の根は execution_mode='delegated' の
// あいだ親を持てない（ck_ticket_delegated_no_parent）ので、PATCH の対象
// （testTicketID、seq 31）は根の**子孫**として置く。こうしないと
// 「自分自身を親にする循環」の判定（ancestor=自分）と「委任の木に属するか」
// の判定（ancestor=根）が同じ組になり、フェイクで区別できなくなる。
const testDelegationTreeRootID = "01K2TKT00000000000DELROOT1"

func TestUpdateTicketBlocksAgentCrossingDelegationBoundary(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[12] = testOutsideTicketID
	q.ticket.typeByID[testOutsideTicketID] = "task"
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationTreeRootID, Status: "approved"},
	}
	// testTicketID（自分）は木の中、新しい親（12 = testOutsideTicketID）は木の外。
	q.ticket.descendantPairs = map[[2]string]bool{
		{testDelegationTreeRootID, testTicketID}:        true,
		{testDelegationTreeRootID, testOutsideTicketID}: false,
	}
	h, _ := ticketHandler(q)

	req := detailReqAs(auth.ActorKindAgent, http.MethodPatch,
		"/api/v1/projects/demo/tickets/31", `{"parent_seq":12}`, "31",
		"ticket.edit", permTicketAssign)
	req.Header.Set("If-Match", `"3"`)
	rec := httptest.NewRecorder()
	h.updateTicket(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestUpdateTicketAllowsHumanCrossingDelegationBoundary(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[12] = testOutsideTicketID
	q.ticket.typeByID[testOutsideTicketID] = "task"
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationTreeRootID, Status: "approved"},
	}
	q.ticket.descendantPairs = map[[2]string]bool{
		{testDelegationTreeRootID, testTicketID}:        true,
		{testDelegationTreeRootID, testOutsideTicketID}: false,
	}
	h, _ := ticketHandler(q)

	req := detailReqAs(auth.ActorKindUser, http.MethodPatch,
		"/api/v1/projects/demo/tickets/31", `{"parent_seq":12}`, "31",
		"ticket.edit", permTicketAssign)
	req.Header.Set("If-Match", `"3"`)
	rec := httptest.NewRecorder()
	h.updateTicket(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（人には効かない） (%s)", rec.Code, rec.Body.String())
	}
}

func TestUpdateTicketAllowsAgentReparentWithinSameTree(t *testing.T) {
	q := ticketDetailFake()
	q.ticket.idBySeq[12] = testOutsideTicketID
	q.ticket.typeByID[testOutsideTicketID] = "task"
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationTreeRootID, Status: "approved"},
	}
	// 現在の親も新しい親も、同じ委任の木の中（境界をまたがない）。
	// **自分自身を親にする循環の判定（ancestor=testTicketID）は別の組**なので、
	// ここを true にしても cycle には化けない（descendantPairs の鍵が違う）。
	q.ticket.descendantPairs = map[[2]string]bool{
		{testDelegationTreeRootID, testTicketID}:        true,
		{testDelegationTreeRootID, testOutsideTicketID}: true,
	}
	h, _ := ticketHandler(q)

	req := detailReqAs(auth.ActorKindAgent, http.MethodPatch,
		"/api/v1/projects/demo/tickets/31", `{"parent_seq":12}`, "31",
		"ticket.edit", permTicketAssign)
	req.Header.Set("If-Match", `"3"`)
	rec := httptest.NewRecorder()
	h.updateTicket(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（同じ木の中の並べ替え） (%s)", rec.Code, rec.Body.String())
	}
}
