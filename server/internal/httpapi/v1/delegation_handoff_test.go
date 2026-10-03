package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 引き継ぎノートと次の一手（Delegation.md 9.3・11章・12.2）の単体テスト。pb-250。
//
// 認可（ticket.create）はミドルウェアの責務なのでここでは通さない。ここで
// 確かめるのは、main の身元確認・ノートの追記（版の採番）・次の一手の
// 代表的な分岐である。

// ── POST /tickets/{seq}/handoff ────────────────────────────────

func TestUpdateHandoffHappyPath(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.latestDelegationPlanErr = pgx.ErrNoRows
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved"}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.updateHandoff(rec, ticketReqAsAgent(http.MethodPost, "/handoff",
		`{"body":{"purpose":"合格する"},"trigger":"session_end"}`, "31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdHandoffs) != 1 {
		t.Fatalf("CreateDelegationHandoff の回数 = %d", len(q.createdHandoffs))
	}
	if q.createdHandoffs[0].Trigger != "session_end" {
		t.Errorf("trigger = %q, want session_end", q.createdHandoffs[0].Trigger)
	}
}

func TestUpdateHandoffRejectsNonMain(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.updateHandoff(rec, ticketReqAsAgent(http.MethodPost, "/handoff",
		`{"body":{"purpose":"合格する"},"trigger":"session_end"}`, "31", testEvaluatorActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestUpdateHandoffRejectsInvalidTrigger(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.updateHandoff(rec, ticketReqAsAgent(http.MethodPost, "/handoff",
		`{"body":{"purpose":"合格する"},"trigger":"made_up"}`, "31", testMainActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── 次の一手（9.3）────────────────────────────────────────────

func TestComputeNextMoveMainPlanningNoPlanNoHearing(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "planning"}
	q.latestDelegationPlanErr = pgx.ErrNoRows

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleMain)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "目的を掘り下げる問いを立てる（ヒアリング）" {
		t.Errorf("move = %q", move)
	}
}

func TestComputeNextMoveMainApprovedRejectedPlan(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved"}
	q.latestDelegationPlan = gen.GetLatestDelegationPlanRow{Version: 2, Status: "rejected"}

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleMain)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "指摘（一覧）を直して出し直す" {
		t.Errorf("move = %q", move)
	}
}

func TestComputeNextMoveMainApprovedNoGate(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved"}
	q.latestDelegationPlan = gen.GetLatestDelegationPlanRow{Version: 1, Status: "accepted"}

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleMain)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "引き継ぎノートの「次の一手」を続ける" {
		t.Errorf("move = %q", move)
	}
}

func TestComputeNextMoveEvaluatorPendingPlan(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved"}
	q.latestDelegationPlan = gen.GetLatestDelegationPlanRow{Version: 2, Status: "proposed"}

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleEvaluator)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "対象（計画の版）を評価する" {
		t.Errorf("move = %q", move)
	}
}

func TestComputeNextMoveStoppedDelegation(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "paused"}

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleMain)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "何もしない（委任は停止）" {
		t.Errorf("move = %q", move)
	}
}

func TestComputeNextMoveOpenConfirmationBlocksMove(t *testing.T) {
	q := ticketFake()
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved"}
	q.openConfirmation = true

	move, err := computeNextMove(context.Background(), q, testDelegationID, roleMain)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if move != "何もしない（確認待ちが開いている）" {
		t.Errorf("move = %q", move)
	}
}
