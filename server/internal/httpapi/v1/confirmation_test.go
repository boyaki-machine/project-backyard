package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// confirmationAnswerReq は PM（人）が確認待ちに答えるリクエストを組み立てる。
func confirmationAnswerReq(body, seq, id string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/confirmation/"+id+"/answer", strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	rc.URLParams.Add("seq", seq)
	rc.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID, ActorKind: auth.ActorKindUser})
	return req.WithContext(ctx)
}

// 確認待ち（Delegation.md 8章・12.2）の単体テスト。pb-249。
//
// 認可（ticket.create / delegation.manage）はミドルウェアの責務なのでここでは
// 通さない。ここで確かめるのは、main の身元確認・理由ごとの値域・回答の反映・
// 他の書き込み口（起票・計画・評価・完了レポート）への関門の波及である。

// ── POST /tickets/{seq}/confirmation ───────────────────────────

func TestRaiseConfirmationHappyPath(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.raiseConfirmation(rec, ticketReqAsAgent(http.MethodPost, "/confirmation",
		`{"reason":"hearing","question_md":"目的をもう少し教えてください"}`,
		"31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdConfirmations) != 1 {
		t.Fatalf("CreateConfirmation の回数 = %d", len(q.createdConfirmations))
	}
	if q.createdConfirmations[0].Reason != "hearing" {
		t.Errorf("reason = %q, want hearing", q.createdConfirmations[0].Reason)
	}
}

func TestRaiseConfirmationRejectsNonMain(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.raiseConfirmation(rec, ticketReqAsAgent(http.MethodPost, "/confirmation",
		`{"reason":"hearing","question_md":"目的をもう少し教えてください"}`,
		"31", testEvaluatorActorID)) // main ではない

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestRaiseConfirmationRejectsEvaluatorOnlyReason(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.raiseConfirmation(rec, ticketReqAsAgent(http.MethodPost, "/confirmation",
		`{"reason":"evaluation_unable","question_md":"判断できません"}`,
		"31", testMainActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（main からは evaluation_unable を立てられない） (%s)",
			rec.Code, rec.Body.String())
	}
}

// ── POST /tickets/{seq}/confirmation/{id}/answer ───────────────

func TestAnswerConfirmationHappyPath(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.confirmationByIDRow = gen.GetConfirmationByIDRow{
		ID: "01K2CONFIRM0000000000001", Status: "open", Reason: "hearing",
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.answerConfirmation(rec, confirmationAnswerReq(
		`{"answer_md":"目的はXです"}`, "31", "01K2CONFIRM0000000000001"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.answeredConfirmationIDs) != 1 {
		t.Fatalf("AnswerConfirmation の回数 = %d", len(q.answeredConfirmationIDs))
	}
}

func TestAnswerConfirmationRejectsAlreadyAnswered(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.confirmationByIDRow = gen.GetConfirmationByIDRow{
		ID: "01K2CONFIRM0000000000001", Status: "answered", Reason: "hearing",
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.answerConfirmation(rec, confirmationAnswerReq(
		`{"answer_md":"もう一度答える"}`, "31", "01K2CONFIRM0000000000001"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 確認待ちの関門（5章・8.2）が他の書き込み口へ波及すること ──────

func TestCreateTicketBlockedByOpenConfirmation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testDelegationRootID
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "approved"},
	}
	q.openConfirmation = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習1","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitPlanBlockedByOpenConfirmation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.openConfirmation = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[]},"body_md":"版","reason_md":"理由"}`, "31", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitEvaluationBlockedByOpenConfirmation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	withEvaluatorAgentInfo(q)
	q.latestAgentReport = gen.GetLatestAgentReportForTicketRow{ID: "01K2REPORT0000000000001"}
	q.openConfirmation = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"deliverable","verdict":"pass","findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitReportBlockedByOpenConfirmation(t *testing.T) {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.openConfirmation = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}
