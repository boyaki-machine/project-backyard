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
	withMainDelegation(q, "planning")
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

// TestSubmitEvaluationScopeChangePassWaitsForPM は pb-260 で直した不具合の回帰。
// 範囲の変更（6.3）は提出の時点で委任を計画中へ戻す。その版が評価に合格しても
// accepted にせず、PM の承認（delegation/approve）を待たせる。
func TestSubmitEvaluationScopeChangePassWaitsForPM(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "planning")
	withEvaluatorAgentInfo(q)
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		3: {ID: "01K2PLAN00000000000000003", Status: "proposed"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"plan","plan_version":3,"verdict":"pass","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.acceptedPlanIDs) != 0 {
		t.Errorf("acceptedPlanIDs = %v。計画中の版は PM の承認を待つはず（6.1）", q.acceptedPlanIDs)
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

// TestSubmitEvaluationUnableRaisesConfirmation は pb-249 で置き換わった挙動。
// unable は確認待ち（evaluation_unable）を立てるだけで、主状態は動かさない
// （8.2「評価が立てる確認待ちは、関門（開いているか）だけで main・評価の
// 書き込みを止める」）。
func TestSubmitEvaluationUnableRaisesConfirmation(t *testing.T) {
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
	if len(q.createdConfirmations) != 1 {
		t.Fatalf("CreateConfirmation の回数 = %d", len(q.createdConfirmations))
	}
	if q.createdConfirmations[0].Reason != confirmationReasonEvaluationUnable {
		t.Errorf("reason = %q, want %q", q.createdConfirmations[0].Reason, confirmationReasonEvaluationUnable)
	}
	if _, ok := q.delegationStatusSetTo[testDelegationID]; ok {
		t.Error("unable で主状態が動いている")
	}
}

// TestSubmitEvaluationRoundLimitRaisesConfirmationAndPauses は pb-249 で
// 置き換わった挙動。往復の上限超過は確認待ち（budget_exceeded）を立て、
// かつ主状態を paused にする（8.2「PB が立てた確認待ちは、主状態を停止にする」）。
func TestSubmitEvaluationRoundLimitRaisesConfirmationAndPauses(t *testing.T) {
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
	if len(q.createdConfirmations) != 1 || q.createdConfirmations[0].Reason != confirmationReasonBudgetExceeded {
		t.Fatalf("createdConfirmations = %v", q.createdConfirmations)
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

// 取り下げた（ended の）根は、成果の評価が無くても完了できる（Delegation.md 4.3「取り下げ」）。
func TestRootHasUnpassedEvaluationEndedDelegation(t *testing.T) {
	q := ticketFake()
	withMainDelegation(q, "ended")
	pending, err := rootHasUnpassedEvaluation(context.Background(), q, testTicketID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if pending {
		t.Error("終了した委任の根が pending=true を返している")
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
