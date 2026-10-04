package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 委任の予算の計測と自動停止、停止と再開（Delegation.md 4.2・8.2・12.1）の
// 単体テスト。pb-253。
//
// 消費を測る SQL（GetDelegationBudgetUsage）はフェイクで差し替えるので、ここで
// 確かめるのは、予算の形の検証・上限との比べ方・超えたときに拒むか受け付けるか・
// 停止と確認待ちを立てること・再開の戻り先と予算の差し替えである。SQL そのものは
// delegation_budget_integration_test.go が実 DB で通す。

func i64(n int64) *int64   { return &n }
func i32(n int32) *int32   { return &n }
func str(s string) *string { return &s }

func TestValidateBudgetJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		field string // 空なら合格
	}{
		{"全項目", `{"tokens":1000000,"tokens_per_month":200000,"due_on":"2027-02-28","tickets":300,"depth":4,"rounds":3}`, ""},
		{"空", `{}`, ""},
		{"オブジェクトでない", `[1]`, "budget"},
		{"null", `null`, "budget"},
		{"知らないキー（綴りの誤り）", `{"ticket":300}`, "budget.ticket"},
		{"0", `{"tickets":0}`, "budget.tickets"},
		{"負", `{"depth":-1}`, "budget.depth"},
		{"小数", `{"tokens":1.5}`, "budget.tokens"},
		{"文字列の数", `{"tokens":"100"}`, "budget.tokens"},
		{"日付の形", `{"due_on":"2027/02/28"}`, "budget.due_on"},
		{"ありえない日付", `{"due_on":"2027-02-30"}`, "budget.due_on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			details := validateBudgetJSON(json.RawMessage(tc.raw), "budget")
			if tc.field == "" {
				if len(details) != 0 {
					t.Errorf("details = %v, want なし", details)
				}
				return
			}
			if len(details) != 1 || details[0].Field != tc.field {
				t.Errorf("details = %v, want %s の1件", details, tc.field)
			}
		})
	}
}

func TestBudgetExceeded(t *testing.T) {
	b := delegationBudget{
		Tokens: i64(1000), TokensPerMonth: i64(300), DueOn: str("2026-10-31"),
		Tickets: i64(10), Depth: i32(3),
	}
	base := gen.GetDelegationBudgetUsageRow{Tickets: 9, TokensTotal: 1000, TokensThisMonth: 300, Today: "2026-10-31"}
	for _, tc := range []struct {
		name string
		u    func(gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow
		c    budgetCheck
		want []string // 理由に含まれる語
	}{
		{"ちょうど上限は超えていない", nil, budgetCheck{newTickets: 1, parentDepth: 2, checkTokens: true}, nil},
		{"期日の翌日", func(u gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow {
			u.Today = "2026-11-01"
			return u
		}, budgetCheck{}, []string{"期日"}},
		{"起票数", func(u gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow {
			u.Tickets = 10
			return u
		}, budgetCheck{newTickets: 1}, []string{"起票数"}},
		{"深さ", nil, budgetCheck{newTickets: 1, parentDepth: 3}, []string{"深さ"}},
		{"起票しないなら起票数を見ない", func(u gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow {
			u.Tickets = 99
			return u
		}, budgetCheck{}, nil},
		{"トークンの総予算と今月", func(u gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow {
			u.TokensTotal, u.TokensThisMonth = 1001, 301
			return u
		}, budgetCheck{checkTokens: true}, []string{"総予算", "今月"}},
		{"トークンは checkTokens のときだけ見る", func(u gen.GetDelegationBudgetUsageRow) gen.GetDelegationBudgetUsageRow {
			u.TokensTotal = 5000
			return u
		}, budgetCheck{newTickets: 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := base
			if tc.u != nil {
				u = tc.u(u)
			}
			got := b.exceeded(u, tc.c)
			if len(got) != len(tc.want) {
				t.Fatalf("got = %v, want %v を含む %d 件", got, tc.want, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("got[%d] = %q, want %q を含む", i, got[i], w)
				}
			}
		})
	}
}

func TestBudgetOfScopeWithoutBudgetHasNoLimits(t *testing.T) {
	if b := budgetOfScope([]byte(`{"goal":"合格する"}`)); b != (delegationBudget{}) {
		t.Errorf("budget = %+v, want 上限なし", b)
	}
	if got := roundLimitOf([]byte(`{}`)); got != evaluationRoundLimit {
		t.Errorf("rounds = %d, want 既定 %d", got, evaluationRoundLimit)
	}
	if got := roundLimitOf([]byte(`{"budget":{"rounds":5}}`)); got != 5 {
		t.Errorf("rounds = %d, want 5", got)
	}
}

// ── 起票（main）───────────────────────────────────────────────

func mainCreateFake(scope string, usage gen.GetDelegationBudgetUsageRow) *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testDelegationRootID
	q.ticket.sortRowBySeq[31] = gen.GetTicketSortRowRow{ID: testDelegationRootID, SortKey: txt("0|n:")}
	q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{
		testMainActorID: {ID: "01K2DELEGATION00000000001", RootTicketID: testDelegationRootID, RootSeq: 31, Status: "approved"},
	}
	q.ticket.descendant = true
	withRootDelegation(q, testDelegationRootID, scope)
	q.budgetUsage, q.budgetUsageSet = usage, true
	return q
}

func TestCreateTicketOverBudgetIsRefusedAndPauses(t *testing.T) {
	q := mainCreateFake(`{"budget":{"tickets":3}}`,
		gen.GetDelegationBudgetUsageRow{Tickets: 3, ProbeDepth: 0, Today: "2026-10-04"})
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習4","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "parent_seq", "delegation_budget") {
		t.Errorf("details = %v, want parent_seq/delegation_budget", errorOf(t, rec).Details)
	}
	if q.delegationStatusSetTo["01K2DELEGATION00000000001"] != "paused" {
		t.Errorf("委任の状態 = %v, want paused", q.delegationStatusSetTo)
	}
	if len(q.createdConfirmations) != 1 || q.createdConfirmations[0].Reason != "budget_exceeded" ||
		!strings.Contains(q.createdConfirmations[0].QuestionMd, "起票数") {
		t.Errorf("確認待ち = %+v, want budget_exceeded（起票数）を1件", q.createdConfirmations)
	}
	if len(q.budgetUsageProbes) != 1 || q.budgetUsageProbes[0] != testDelegationRootID {
		t.Errorf("probe = %v, want 親（根）の深さを測る", q.budgetUsageProbes)
	}
	for _, op := range q.opLog {
		if op == "CreateTicket" {
			t.Fatal("予算を超える起票が作られた")
		}
	}
}

func TestCreateTicketDepthLimit(t *testing.T) {
	q := mainCreateFake(`{"budget":{"depth":2}}`,
		gen.GetDelegationBudgetUsageRow{Tickets: 5, ProbeDepth: 2, Today: "2026-10-04"})
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"孫の子","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(errorOf(t, rec).Message, "深さ") {
		t.Errorf("message = %q, want 深さ を含む", errorOf(t, rec).Message)
	}
}

func TestCreateTicketWithinBudget(t *testing.T) {
	q := mainCreateFake(`{"budget":{"tickets":3,"depth":2,"due_on":"2026-10-04"}}`,
		gen.GetDelegationBudgetUsageRow{Tickets: 2, ProbeDepth: 1, Today: "2026-10-04"})
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/projects/demo/tickets",
		`{"type":"task","title":"演習3","parent_seq":31}`, "", testMainActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.createdConfirmations) != 0 || q.delegationStatusSetTo != nil {
		t.Error("予算の内側なのに停止した")
	}
}

// ── 完了レポート ──────────────────────────────────────────────

func reportBudgetFake(scope string, usage gen.GetDelegationBudgetUsageRow) *fakeQuerier {
	q := reportFake()
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: "01K2DELEGATION00000000001", RootTicketID: testTicketID, Status: "approved"},
	}
	q.ticket.descendant = true
	withRootDelegation(q, testTicketID, scope)
	q.budgetUsage, q.budgetUsageSet = usage, true
	return q
}

func TestSubmitReportAfterDueIsRefusedAndPauses(t *testing.T) {
	q := reportBudgetFake(`{"budget":{"due_on":"2026-10-03"}}`,
		gen.GetDelegationBudgetUsageRow{Today: "2026-10-04"})
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo["01K2DELEGATION00000000001"] != "paused" || len(q.createdConfirmations) != 1 {
		t.Errorf("状態 = %v / 確認待ち %d 件, want paused と1件", q.delegationStatusSetTo, len(q.createdConfirmations))
	}
	for _, op := range q.opLog {
		if op == "CreateAgentReport" {
			t.Fatal("期日を過ぎた完了レポートが保存された")
		}
	}
}

func TestSubmitReportOverTokensIsAcceptedThenPauses(t *testing.T) {
	q := reportBudgetFake(`{"budget":{"tokens":1000}}`,
		gen.GetDelegationBudgetUsageRow{TokensTotal: 1200, Today: "2026-10-04"})
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed","cost":{"tokens":500}}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)（報告は受け付ける）", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo["01K2DELEGATION00000000001"] != "paused" {
		t.Errorf("状態 = %v, want paused", q.delegationStatusSetTo)
	}
	if len(q.createdConfirmations) != 1 || !strings.Contains(q.createdConfirmations[0].QuestionMd, "総予算") {
		t.Errorf("確認待ち = %+v", q.createdConfirmations)
	}
}

// ── 評価 ──────────────────────────────────────────────────────

func TestSubmitEvaluationRoundLimitFromBudget(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	row := q.delegationByRootTicket[testTicketID]
	row.Scope = []byte(`{"budget":{"rounds":5}}`)
	q.delegationByRootTicket[testTicketID] = row
	withEvaluatorAgentInfo(q)
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLAN00000000000000002", Status: "proposed"},
	}
	q.evaluationsSinceLastPass = 2 // round 3：既定の上限なら止まる
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"plan","plan_version":2,"verdict":"fail","findings":`+failFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo != nil {
		t.Errorf("状態 = %v, want 止まらない（rounds=5 の3回目）", q.delegationStatusSetTo)
	}
}

func TestSubmitEvaluationTokensOverBudgetPauses(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	withMainDelegation(q, "approved")
	row := q.delegationByRootTicket[testTicketID]
	row.Scope = []byte(`{"budget":{"tokens_per_month":100}}`)
	q.delegationByRootTicket[testTicketID] = row
	withEvaluatorAgentInfo(q)
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLAN00000000000000002", Status: "proposed"},
	}
	q.budgetUsage, q.budgetUsageSet = gen.GetDelegationBudgetUsageRow{TokensThisMonth: 150, Today: "2026-10-04"}, true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitEvaluation(rec, ticketReqAsAgent(http.MethodPost, "/evaluation",
		`{"target_kind":"plan","plan_version":2,"verdict":"pass","tokens_used":150,"findings":`+passFindings()+`}`,
		"31", testEvaluatorActorID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo[testDelegationID] != "paused" {
		t.Errorf("状態 = %v, want paused", q.delegationStatusSetTo)
	}
}

// ── 委任の作成・計画の提出での予算の検証 ──────────────────────────

func TestCreateDelegationRejectsBadBudget(t *testing.T) {
	e := validateCreateDelegation(createDelegationRequest{
		MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
		Scope: json.RawMessage(`{"goal":"合格する","budget":{"ticket":300}}`),
	})
	if e == nil || len(e.Details) != 1 || e.Details[0].Field != "scope.budget.ticket" {
		t.Errorf("err = %+v, want scope.budget.ticket の1件", e)
	}
}

func TestSubmitPlanRejectsBadBudget(t *testing.T) {
	e := validateSubmitPlan(submitPlanRequest{
		Scope: json.RawMessage(`{"budget":{"depth":0}}`), Skeleton: json.RawMessage(`{}`),
		BodyMd: "案", ReasonMd: "理由",
	})
	if e == nil || len(e.Details) != 1 || e.Details[0].Field != "scope.budget.depth" {
		t.Errorf("err = %+v, want scope.budget.depth の1件", e)
	}
}

// ── 停止と再開 ────────────────────────────────────────────────

func controlFake(status string, scopeVersion int32) *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationForControl = map[string]gen.GetDelegationForControlRow{
		testTicketID: {ID: testDelegationID, Status: status, Scope: []byte(`{"goal":"合格する","budget":{"tickets":3}}`), ScopeVersion: scopeVersion},
	}
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, RootSeq: 31, Status: "approved"}
	q.latestDelegationPlanErr = pgx.ErrNoRows
	return q
}

func TestPauseDelegation(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   int
	}{{"planning", 200}, {"approved", 200}, {"paused", 409}, {"ended", 409}} {
		t.Run(tc.status, func(t *testing.T) {
			q := controlFake(tc.status, 1)
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.pauseDelegation(rec, ticketReq(http.MethodPost, "/delegation/pause", `{"reason_md":"方針を見直す"}`, "31"))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 200 {
				if q.delegationStatusSetTo[testDelegationID] != "paused" {
					t.Errorf("状態 = %v, want paused", q.delegationStatusSetTo)
				}
				if len(q.ticket.comments) != 1 || !strings.Contains(q.ticket.comments[0].BodyMd, "方針を見直す") {
					t.Errorf("comments = %+v", q.ticket.comments)
				}
			}
		})
	}
}

func TestResumeDelegationRequiresAnsweredConfirmation(t *testing.T) {
	q := controlFake("paused", 1)
	q.openConfirmation = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if q.delegationStatusSetTo != nil {
		t.Error("確認待ちが開いたまま再開した")
	}
}

func TestResumeDelegationTargetStatus(t *testing.T) {
	scope := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
	for _, tc := range []struct {
		name         string
		scopeVersion int32
		plan         *gen.GetLatestDelegationPlanRow
		want         string
	}{
		{"一度も承認されていない", 0, nil, "planning"},
		{"承認済みの版がある", 1, &gen.GetLatestDelegationPlanRow{Version: 2, Status: "accepted"}, "approved"},
		{"構造の変更が評価待ち", 1, &gen.GetLatestDelegationPlanRow{Version: 3, Status: "proposed", MeasuredClass: scope("structural")}, "approved"},
		{"範囲の変更が承認待ち（PB の判定）", 1, &gen.GetLatestDelegationPlanRow{Version: 3, Status: "proposed", MeasuredClass: scope("scope")}, "planning"},
		{"範囲の変更が承認待ち（main の申告）", 1, &gen.GetLatestDelegationPlanRow{Version: 3, Status: "proposed", DeclaredClass: scope("scope")}, "planning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := controlFake("paused", tc.scopeVersion)
			if tc.plan != nil {
				q.latestDelegationPlan, q.latestDelegationPlanErr = *tc.plan, nil
			}
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{}`, "31"))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			if got := q.delegationStatusSetTo[testDelegationID]; got != tc.want {
				t.Errorf("戻り先 = %q, want %q", got, tc.want)
			}
			if len(q.updatedScopes) != 0 {
				t.Error("予算を渡していないのに範囲が変わった")
			}
		})
	}
}

func TestResumeDelegationReplacesBudget(t *testing.T) {
	q := controlFake("paused", 1)
	q.latestDelegationPlan, q.latestDelegationPlanErr = gen.GetLatestDelegationPlanRow{Version: 2, Status: "accepted"}, nil
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{Version: 2}
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLANV2", Skeleton: []byte(`{"branches":[]}`), BodyMd: "v2の説明", Status: "accepted"},
	}
	q.nextPlanVersion = 3
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume",
		`{"budget":{"tickets":10, "depth":4},"reason_md":"演習を増やす"}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedScopes) != 1 {
		t.Fatalf("UpdateDelegationScope の回数 = %d", len(q.updatedScopes))
	}
	var scope map[string]any
	_ = json.Unmarshal(q.updatedScopes[0].Scope, &scope)
	if scope["goal"] != "合格する" {
		t.Errorf("scope = %v, want 予算以外はそのまま", scope)
	}
	if b, _ := scope["budget"].(map[string]any); b["tickets"] != float64(10) || b["depth"] != float64(4) {
		t.Errorf("budget = %v, want 置き換え", scope["budget"])
	}
	if len(q.createdPlans) != 1 || q.createdPlans[0].Version != 3 ||
		string(q.createdPlans[0].Skeleton) != `{"branches":[]}` ||
		string(q.createdPlans[0].Scope) != string(q.updatedScopes[0].Scope) {
		t.Errorf("作った版 = %+v, want v2 を写し範囲だけ差し替えた v3", q.createdPlans)
	}
	if len(q.decidedPlans) != 1 || q.decidedPlans[0].Status != "accepted" || q.decidedPlans[0].DecidedBy.String != testActorID {
		t.Errorf("decided = %+v, want PM の承認済み", q.decidedPlans)
	}
	if q.delegationStatusSetTo[testDelegationID] != "approved" {
		t.Errorf("状態 = %v, want approved", q.delegationStatusSetTo)
	}
}

func TestResumeDelegationBudgetRejectedWhilePlanning(t *testing.T) {
	q := controlFake("paused", 0)
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{"budget":{"tickets":10}}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedScopes) != 0 || q.delegationStatusSetTo != nil {
		t.Error("拒んだのに範囲か状態が変わった")
	}
}

func TestResumeDelegationValidatesBudget(t *testing.T) {
	q := controlFake("paused", 1)
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{"budget":{"tokens":-5}}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "budget.tokens", "invalid") {
		t.Errorf("details = %v", errorOf(t, rec).Details)
	}
}

func TestResumeDelegationNotPaused(t *testing.T) {
	q := controlFake("approved", 1)
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{}`, "31"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestDelegationControlNotARoot(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.pauseDelegation(rec, ticketReq(http.MethodPost, "/delegation/pause", `{}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}
