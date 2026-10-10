package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 委任の状態の集約読み取り（Delegation.md 9.1）の単体テスト。pb-251。

func TestGetDelegationStatusNotInDelegation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, ticketReq(http.MethodGet, "/delegation-status", "", "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := viewOf(t, rec)
	if v["in_delegation"] != false {
		t.Errorf("in_delegation = %v, want false", v["in_delegation"])
	}
}

func TestGetDelegationStatusRoleMainAtRoot(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{
		ID: testDelegationID, Status: "approved", Scope: []byte(`{"goal":"合格する"}`),
		RootSeq: 31, MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
	}
	q.latestDelegationPlanErr = pgx.ErrNoRows

	h, _ := ticketHandler(q)
	req := ticketReqAsAgent(http.MethodGet, "/delegation-status", "", "31", testMainActorID)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := viewOf(t, rec)
	if v["role"] != "main" {
		t.Errorf("role = %v, want main", v["role"])
	}
	if v["status"] != "approved" {
		t.Errorf("status = %v, want approved", v["status"])
	}
}

func TestGetDelegationStatusRoleSubAtDescendant(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID2 // 根ではない子孫
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testDelegationRootID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{
		ID: testDelegationID, Status: "approved", Scope: []byte(`{}`),
		RootSeq: 31, MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
	}
	q.latestDelegationPlanErr = pgx.ErrNoRows

	h, _ := ticketHandler(q)
	req := ticketReqAsAgent(http.MethodGet, "/delegation-status", "", "31", testMainActorID)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := viewOf(t, rec)
	if v["role"] != "sub" {
		t.Errorf("role = %v, want sub", v["role"])
	}
}

func TestGetDelegationStatusRoleReadonlyForOthers(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{
		ID: testDelegationID, Status: "approved", Scope: []byte(`{}`),
		RootSeq: 31, MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
	}
	q.latestDelegationPlanErr = pgx.ErrNoRows

	h, _ := ticketHandler(q)
	req := ticketReqAsAgent(http.MethodGet, "/delegation-status", "", "31", testOtherAgentID)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := viewOf(t, rec)
	if v["role"] != "readonly" {
		t.Errorf("role = %v, want readonly", v["role"])
	}
}

// TestGetDelegationStatusIncludesRecentAnswers は「最近の回答」（9.3。pb-278）を返すこと。
func TestGetDelegationStatusIncludesRecentAnswers(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testTicketID, Status: "planning"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "planning", Scope: []byte(`{}`), RootSeq: 31,
		MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID}
	q.latestDelegationPlanErr = pgx.ErrNoRows
	at := time.Date(2026, 10, 4, 9, 42, 55, 0, time.UTC)
	q.recentAnswered = []gen.ListRecentAnsweredConfirmationsRow{
		{Reason: "hearing", QuestionMd: "何を良くしたいか", AnswerMd: pgtype.Text{String: "a が近い", Valid: true}, AnsweredAt: &at},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, ticketReqAsAgent(http.MethodGet, "/delegation-status", "", "31", testMainActorID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	answers, _ := viewOf(t, rec)["recent_answers"].([]any)
	if len(answers) != 1 {
		t.Fatalf("recent_answers = %v, want 1件", answers)
	}
	a := answers[0].(map[string]any)
	if a["reason"] != "hearing" || a["answer_md"] != "a が近い" || a["answered_at"] == nil {
		t.Errorf("recent_answers[0] = %v", a)
	}
}
