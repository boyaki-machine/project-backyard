package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 計画の版（Delegation.md 6章・12.2）の単体テスト。pb-247。
//
// 認可（ticket.create / delegation.manage）はミドルウェアの責務なので
// ここでは通さない。ここで確かめるのは、main の身元確認・6.3 の3段階の
// 判定（固定閾値）・軽微の即時反映・範囲変更での計画中への差し戻し・
// 差し戻し（revert）の組み立てである。

const testDelegationID = "01K2DELEGATION00000000PLN1"

func planRevertReq(version, actorKind string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/plan/"+version+"/revert", strings.NewReader(`{}`))
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	rc.URLParams.Add("seq", "31")
	rc.URLParams.Add("version", version)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID, ActorKind: actorKind})
	return req.WithContext(ctx)
}

func withMainDelegation(q *fakeQuerier, status string) {
	q.delegationByRootTicket = map[string]gen.GetDelegationByRootTicketRow{
		testTicketID: {
			ID: testDelegationID, Status: status, Scope: []byte(`{"goal":"合格する"}`),
			MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
		},
	}
}

// ── POST /tickets/{seq}/plan ───────────────────────────────────

func TestSubmitPlanFirstVersionStaysProposed(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.latestAcceptedPlanErr = pgx.ErrNoRows // まだ承認済みの版が無い（初版）
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[]},"body_md":"初版","reason_md":"開始"}`, "31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdPlans) != 1 {
		t.Fatalf("CreateDelegationPlan の回数 = %d", len(q.createdPlans))
	}
	if q.createdPlans[0].Status != "proposed" {
		t.Errorf("status = %q, want proposed（初版は評価待ち。6.1）", q.createdPlans[0].Status)
	}
	if q.createdPlans[0].MeasuredClass.Valid {
		t.Errorf("measured_class = %v, want 無効（初版は判定規則を適用しない）", q.createdPlans[0].MeasuredClass)
	}
	if len(q.acceptedPlanIDs) != 0 {
		t.Error("初版が即座に accepted になっている")
	}
}

func TestSubmitPlanMinorChangeAutoAccepted(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`),
		Skeleton: []byte(`{"branches":[{"name":"演習","budget_share":0.5}]}`),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[{"name":"演習","budget_share":0.55}]},"body_md":"微修正","reason_md":"誤差の調整"}`,
		"31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.createdPlans[0].MeasuredClass.String != planClassMinor {
		t.Errorf("measured_class = %q, want minor", q.createdPlans[0].MeasuredClass.String)
	}
	if len(q.acceptedPlanIDs) != 1 {
		t.Error("軽微な変更が即座に反映（accepted）されていない")
	}
	if len(q.delegationStatusSetTo) != 0 {
		t.Error("軽微な変更で委任の状態が動いている")
	}
}

func TestSubmitPlanScopeChangeReturnsDelegationToPlanning(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`),
		Skeleton: []byte(`{"branches":[]}`),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"scope":{"goal":"第一志望に合格する"},"skeleton":{"branches":[]},"body_md":"目的を変える","reason_md":"方針変更"}`,
		"31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.createdPlans[0].MeasuredClass.String != planClassScope {
		t.Errorf("measured_class = %q, want scope", q.createdPlans[0].MeasuredClass.String)
	}
	if q.createdPlans[0].Status != "proposed" {
		t.Errorf("status = %q, want proposed（PM の事前承認を待つ）", q.createdPlans[0].Status)
	}
	if got := q.delegationStatusSetTo[testDelegationID]; got != "planning" {
		t.Errorf("委任の状態 = %q, want planning", got)
	}
}

func TestSubmitPlanBranchAddedIsStructural(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`),
		Skeleton: []byte(`{"branches":[{"name":"演習","budget_share":0.5}]}`),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[{"name":"演習","budget_share":0.5},{"name":"模試","budget_share":0.2}]},"body_md":"模試を追加","reason_md":"中間評価が要る"}`,
		"31", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.createdPlans[0].MeasuredClass.String != planClassStructural {
		t.Errorf("measured_class = %q, want structural（枝が増えた）", q.createdPlans[0].MeasuredClass.String)
	}
	if len(q.acceptedPlanIDs) != 0 {
		t.Error("構造の変更が即座に反映（accepted）されている——評価待ちのはず")
	}
}

func TestSubmitPlanBudgetShiftOver20PercentIsStructural(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`),
		Skeleton: []byte(`{"branches":[{"name":"演習","budget_share":0.3}]}`),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[{"name":"演習","budget_share":0.6}]},"body_md":"配分を変える","reason_md":"遅れを取り戻す"}`,
		"31", testMainActorID))

	if q.createdPlans[0].MeasuredClass.String != planClassStructural {
		t.Errorf("measured_class = %q, want structural（30%%超の予算配分の変動）", q.createdPlans[0].MeasuredClass.String)
	}
}

func TestSubmitPlanTooManyNewTicketsIsStructural(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`), Skeleton: []byte(`{"branches":[]}`),
	}
	q.ticketsCreatedSince = 6 // > 5 件（6.3）
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[]},"body_md":"同じ骨格","reason_md":"軽微のつもり"}`,
		"31", testMainActorID))

	if q.createdPlans[0].MeasuredClass.String != planClassStructural {
		t.Errorf("measured_class = %q, want structural（新規5件超）", q.createdPlans[0].MeasuredClass.String)
	}
}

func TestSubmitPlanDeclaredClassEscalatesOverMeasured(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 2
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{
		Version: 1, Scope: []byte(`{"goal":"合格する"}`), Skeleton: []byte(`{"branches":[]}`),
	}
	h, _ := ticketHandler(q)

	// 測定は minor（骨格もスコープも変わらない）だが、main 自身が structural だと申告。
	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{"branches":[]},"body_md":"同じ骨格だが重要な変更","reason_md":"申告を厳しめに","declared_class":"structural"}`,
		"31", testMainActorID))

	if q.createdPlans[0].DeclaredClass.String != "structural" {
		t.Fatalf("declared_class が保存されていない: %+v", q.createdPlans[0])
	}
	if len(q.acceptedPlanIDs) != 0 {
		t.Error("main の申告（structural）より PB の判定（minor）を優先して即時反映してしまっている")
	}
}

func TestSubmitPlanRejectsNonMain(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{},"body_md":"x","reason_md":"y"}`, "31", testOtherAgentID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitPlanRejectsWhenPaused(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "paused")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{},"body_md":"x","reason_md":"y"}`, "31", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitPlanRejectsNonRootTicket(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	// delegationByRootTicket を設定しない = 根ではない。
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan",
		`{"skeleton":{},"body_md":"x","reason_md":"y"}`, "31", testMainActorID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitPlanValidation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	for _, body := range []string{
		`{"body_md":"x","reason_md":"y"}`,                  // skeleton 無し
		`{"skeleton":{},"reason_md":"y"}`,                  // body_md 無し
		`{"skeleton":{},"body_md":"x"}`,                    // reason_md 無し
		`{"skeleton":[1,2],"body_md":"x","reason_md":"y"}`, // skeleton がオブジェクトでない
	} {
		rec := httptest.NewRecorder()
		h.submitDelegationPlan(rec, ticketReqAsAgent(http.MethodPost, "/plan", body, "31", testMainActorID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("body=%s: status = %d, want 422 (%s)", body, rec.Code, rec.Body.String())
		}
	}
}

// ── POST /tickets/{seq}/plan/{version}/revert ──────────────────

func TestRevertPlanCreatesAcceptedCopy(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	q.nextPlanVersion = 4
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLANV2", Scope: []byte(`{"goal":"合格する"}`), Skeleton: []byte(`{"branches":[]}`), BodyMd: "v2の説明", Status: "superseded"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.revertDelegationPlan(rec, planRevertReq("2", auth.ActorKindUser))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdPlans) != 1 {
		t.Fatalf("CreateDelegationPlan の回数 = %d", len(q.createdPlans))
	}
	p := q.createdPlans[0]
	if p.Version != 4 {
		t.Errorf("version = %d, want 4（次の番号）", p.Version)
	}
	if !p.RevertsTo.Valid || p.RevertsTo.Int32 != 2 {
		t.Errorf("reverts_to = %+v, want 2", p.RevertsTo)
	}
	if p.Status != "accepted" {
		t.Errorf("status = %q, want accepted（PM の操作は評価を挟まない）", p.Status)
	}
	if len(q.acceptedPlanIDs) != 1 {
		t.Error("AcceptDelegationPlan が呼ばれていない")
	}
	if string(p.Scope) != `{"goal":"合格する"}` {
		t.Errorf("scope = %s, want 元の版の写し", p.Scope)
	}
}

func TestRevertPlanRejectsUnknownVersion(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.revertDelegationPlan(rec, planRevertReq("99", auth.ActorKindUser))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

func TestRevertPlanRejectsInvalidVersionParam(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.revertDelegationPlan(rec, planRevertReq("abc", auth.ActorKindUser))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}
