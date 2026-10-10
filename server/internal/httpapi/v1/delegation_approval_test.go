package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 委任範囲の承認・却下（Delegation.md 4.3・5章・6.1）の単体テスト。pb-260。
//
// 認可（delegation.manage）はミドルウェアの責務なのでここでは通さない。
// ここで確かめるのは、計画中だけを受け付けること・最新の版と plan_version の
// 突き合わせ・承認は評価の合格の後だけ・承認で委任と計画の両方が進むこと・
// 却下では委任が計画中のまま残ること・判断が根のコメントに残ることである。

const testLatestPlanID = "01K2PLAN00000000000000002"

func approvalFake(delegStatus string, plan gen.GetLatestDelegationPlanWithVerdictRow) *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, delegStatus)
	q.latestPlanWithVerdict = plan
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "approved", RootSeq: 31}
	return q
}

func passedPlanV2() gen.GetLatestDelegationPlanWithVerdictRow {
	return gen.GetLatestDelegationPlanWithVerdictRow{
		ID: testLatestPlanID, Version: 2, Scope: []byte(`{"goal":"合格する","budget":10}`),
		Status: "proposed", LatestVerdict: "pass",
	}
}

func TestApproveDelegationHappyPath(t *testing.T) {
	q := approvalFake("planning", passedPlanV2())
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":2}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.decidedPlans) != 1 {
		t.Fatalf("DecideDelegationPlan の回数 = %d", len(q.decidedPlans))
	}
	d := q.decidedPlans[0]
	if d.ID != testLatestPlanID || d.Status != "accepted" || d.DecidedBy.String != testActorID {
		t.Errorf("decided = %+v, want 最新の版を PM が accepted に", d)
	}
	if len(q.supersededKeepIDs) != 1 || q.supersededKeepIDs[0] != testLatestPlanID {
		t.Errorf("supersede の keep = %v", q.supersededKeepIDs)
	}
	if len(q.approvedDelegationScopes) != 1 {
		t.Fatalf("ApproveDelegationScope の回数 = %d", len(q.approvedDelegationScopes))
	}
	if got := string(q.approvedDelegationScopes[0].Scope); got != `{"goal":"合格する","budget":10}` {
		t.Errorf("scope = %s, want 承認した版の写し", got)
	}
	if len(q.ticket.comments) != 1 || q.ticket.comments[0].Kind != "decision" ||
		!strings.Contains(q.ticket.comments[0].BodyMd, "承認") {
		t.Errorf("comments = %+v, want 承認を決定のコメントで1件", q.ticket.comments)
	}
}

func TestApproveDelegationRequiresPass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict string
		want    string
	}{
		{"評価が無い", "", "まだこの計画を評価していません"},
		{"不合格", "fail", "評価に合格していません"},
		{"判断できない", "unable", "評価に合格していません"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := passedPlanV2()
			plan.LatestVerdict = tc.verdict
			q := approvalFake("planning", plan)
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":2}`, "31"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
			}
			if msg := errorOf(t, rec).Message; !strings.Contains(msg, tc.want) {
				t.Errorf("message = %q, want %q を含む", msg, tc.want)
			}
			if len(q.decidedPlans)+len(q.approvedDelegationScopes) != 0 {
				t.Error("合格前に計画か委任が進んだ")
			}
		})
	}
}

func TestDecideDelegationRejectsWhenNotPlanning(t *testing.T) {
	for _, status := range []string{"approved", "paused", "ended"} {
		t.Run(status, func(t *testing.T) {
			q := approvalFake(status, passedPlanV2())
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":2}`, "31"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
			}
			if len(q.decidedPlans) != 0 {
				t.Error("計画中でないのに計画が確定した")
			}
		})
	}
}

func TestDecideDelegationRejectsStaleVersion(t *testing.T) {
	q := approvalFake("planning", passedPlanV2())
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":1}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "plan_version", "stale") {
		t.Errorf("details = %v, want plan_version/stale", errorOf(t, rec).Details)
	}
}

func TestDecideDelegationRejectsAlreadyDecidedPlan(t *testing.T) {
	plan := passedPlanV2()
	plan.Status = "rejected"
	q := approvalFake("planning", plan)
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.rejectDelegation(rec, ticketReq(http.MethodPost, "/delegation/reject",
		`{"plan_version":2,"reason_md":"予算が大きすぎる"}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestDecideDelegationNoPlanYet(t *testing.T) {
	q := approvalFake("planning", gen.GetLatestDelegationPlanWithVerdictRow{})
	q.latestPlanWithVerdictErr = pgx.ErrNoRows
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":1}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestDecideDelegationNotARoot(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":1}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// TestRejectDelegationKeepsPlanning は、却下が評価を待たずに行え、委任は
// 計画中のまま残ることを確かめる（main が出し直す）。
func TestRejectDelegationKeepsPlanning(t *testing.T) {
	plan := passedPlanV2()
	plan.LatestVerdict = ""
	q := approvalFake("planning", plan)
	q.delegationByIDRow.Status = "planning"
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.rejectDelegation(rec, ticketReq(http.MethodPost, "/delegation/reject",
		`{"plan_version":2,"reason_md":"予算が大きすぎる"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.decidedPlans) != 1 || q.decidedPlans[0].Status != "rejected" {
		t.Errorf("decided = %+v, want rejected を1件", q.decidedPlans)
	}
	if len(q.approvedDelegationScopes) != 0 || len(q.supersededKeepIDs) != 0 {
		t.Error("却下で委任が承認済へ進んだか、古い版を畳んだ")
	}
	if len(q.ticket.comments) != 1 || !strings.Contains(q.ticket.comments[0].BodyMd, "予算が大きすぎる") {
		t.Errorf("comments = %+v, want 理由を含む決定のコメント", q.ticket.comments)
	}
}

func TestDecideDelegationValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve bool
		body    string
		field   string
	}{
		{"承認：版が無い", true, `{}`, "plan_version"},
		{"承認：版が0", true, `{"plan_version":0}`, "plan_version"},
		{"却下：理由が無い", false, `{"plan_version":2}`, "reason_md"},
		{"却下：理由が空白だけ", false, `{"plan_version":2,"reason_md":"  "}`, "reason_md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := approvalFake("planning", passedPlanV2())
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			req := ticketReq(http.MethodPost, "/delegation/x", tc.body, "31")
			if tc.approve {
				h.approveDelegation(rec, req)
			} else {
				h.rejectDelegation(rec, req)
			}

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), tc.field, "required") {
				t.Errorf("details = %v, want %s/required", errorOf(t, rec).Details, tc.field)
			}
		})
	}
}

func TestDecideDelegationConcurrentDecision(t *testing.T) {
	q := approvalFake("planning", passedPlanV2())
	q.decidePlanRows = -1
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.approveDelegation(rec, ticketReq(http.MethodPost, "/delegation/approve", `{"plan_version":2}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.approvedDelegationScopes) != 0 {
		t.Error("計画を確定できなかったのに委任が進んだ")
	}
}
