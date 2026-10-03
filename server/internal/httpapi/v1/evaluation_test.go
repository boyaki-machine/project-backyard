package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 評価（Delegation.md 7章・12.2）の単体テスト。pb-248。
//
// 認可（delegation.evaluate）はミドルウェアの責務なのでここでは通さない。
// ここで確かめるのは、評価エージェントの身元確認・観点の検証（最低2観点・
// 不合格には指摘が要る）・round の算定・plan 対象の accept/reject・
// unable と往復の上限超過での paused 化・根の受け入れゲート（検証8）である。

func withEvaluatorAgentInfo(q *fakeQuerier) {
	q.ticket.agentInfo = &gen.GetAgentRuntimeInfoRow{ClientKind: "code"}
}

func passFindings() string {
	return `[{"aspect":"purpose_and_dod","verdict":"pass","note_md":""},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
}

func failFindings() string {
	return `[{"aspect":"purpose_and_dod","verdict":"fail","note_md":"完了条件を満たしていない"},` +
		`{"aspect":"values","verdict":"pass","note_md":""}]`
}

// ── POST /tickets/{seq}/evaluation ─────────────────────────────

func TestSubmitEvaluationDeliverablePassHappyPath(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"pass","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdEvaluations) != 1 {
		t.Fatalf("CreateEvaluation の回数 = %d", len(q.createdEvaluations))
	}
	got := q.createdEvaluations[0]
	if got.Round != 1 {
		t.Errorf("round = %d, want 1", got.Round)
	}
	if got.AgentReportID.String != "01K2REPORT0000000000001" {
		t.Errorf("agent_report_id = %q", got.AgentReportID.String)
	}
	if len(q.delegationStatusSetTo) != 0 {
		t.Error("合格なのに委任の状態が動いている")
	}
}

func TestSubmitEvaluationRejectsNonEvaluator(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"pass","findings":`+passFindings()+`}`,
		"31", testMainActorID)) // main が評価を書こうとする

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitEvaluationRequiresBothAspects(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"pass",`+
			`"findings":[{"aspect":"purpose_and_dod","verdict":"pass"}]}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "findings", "invalid") {
		t.Errorf("details = %v, want findings/invalid", errorOf(t, rec).Details)
	}
}

func TestSubmitEvaluationFailRequiresNote(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"fail",`+
			`"findings":[{"aspect":"purpose_and_dod","verdict":"fail","note_md":""},`+
			`{"aspect":"values","verdict":"pass","note_md":""}]}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "findings", "required") {
		t.Errorf("details = %v, want findings/required（不合格には指摘が要る）", errorOf(t, rec).Details)
	}
}

func TestSubmitEvaluationPlanVersionOneDoesNotAccept(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		1: {ID: "01K2PLAN00000000000000001", Status: "proposed"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"plan","plan_version":1,"verdict":"pass","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.acceptedPlanIDs) != 0 {
		t.Error("初版（version=1）が accept されている。6.1 により PM の承認待ちになるだけのはず")
	}
}

func TestSubmitEvaluationPlanVersionTwoFailRejects(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLAN00000000000000002", Status: "proposed"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"plan","plan_version":2,"verdict":"fail","findings":`+failFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.rejectedPlanIDs) != 1 || q.rejectedPlanIDs[0] != "01K2PLAN00000000000000002" {
		t.Errorf("rejectedPlanIDs = %v", q.rejectedPlanIDs)
	}
}

func TestSubmitEvaluationUnablePausesDelegation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"unable","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo[testDelegationID] != "paused" {
		t.Errorf("委任の状態 = %q, want paused（confirmation 表が無いあいだの代替）",
			q.delegationStatusSetTo[testDelegationID])
	}
	if len(q.ticket.comments) == 0 {
		t.Error("停止の通知コメントが作られていない")
	}
}

func TestSubmitEvaluationRoundLimitPausesDelegation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	q.evaluationsSinceLastPass = evaluationRoundLimit - 1 // この評価が3回目になる
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"fail","findings":`+failFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.createdEvaluations[0].Round != evaluationRoundLimit {
		t.Fatalf("round = %d, want %d", q.createdEvaluations[0].Round, evaluationRoundLimit)
	}
	if q.delegationStatusSetTo[testDelegationID] != "paused" {
		t.Errorf("委任の状態 = %q, want paused（往復の上限超過）", q.delegationStatusSetTo[testDelegationID])
	}
}

func TestSubmitEvaluationAlreadyEvaluatedReportRejected(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	q.evaluationVerdictByReport = map[string]string{"01K2REPORT0000000000001": "pass"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"pass","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 検証8：根の受け入れを評価の合格に掛ける（ticket_workflow.go）─────

func TestRootHasUnpassedEvaluationNotADelegationRoot(t *testing.T) {
	q := ticketFake()
	pending, err := rootHasUnpassedEvaluation(context.Background(), q, testTicketID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if pending {
		t.Error("委任の根でないチケットが pending=true を返している")
	}
}

func TestRootHasUnpassedEvaluationNoReportYet(t *testing.T) {
	q := ticketFake()
	withMainDelegation(q, "approved")
	pending, err := rootHasUnpassedEvaluation(context.Background(), q, testTicketID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !pending {
		t.Error("完了レポートが無いのに pending=false になっている")
	}
}

func TestRootHasUnpassedEvaluationPassed(t *testing.T) {
	q := ticketFake()
	withMainDelegation(q, "approved")
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	q.evaluationVerdictByReport = map[string]string{"01K2REPORT0000000000001": "pass"}
	pending, err := rootHasUnpassedEvaluation(context.Background(), q, testTicketID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if pending {
		t.Error("合格しているのに pending=true になっている")
	}
}
