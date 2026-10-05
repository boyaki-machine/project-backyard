package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 自律実行の画面（pb-252）が使うサーバ側の拡張の単体テスト：止まる条件
// （scope.stop_conditions）の検証、再開での差し替え、delegation-status の
// 計画の承認のカードの中身。

func TestValidateStopConditions(t *testing.T) {
	long := strings.Repeat("あ", stopConditionMaxLen+1)
	many, _ := json.Marshal(make([]string, stopConditionsMax+1))
	for _, tc := range []struct {
		name  string
		scope string
		ok    bool
	}{
		{"無し", `{}`, true},
		{"2件", `{"stop_conditions":["模試の判定が2回続けて下がったとき","計画より2週間遅れたとき"]}`, true},
		{"空配列", `{"stop_conditions":[]}`, true},
		{"文字列", `{"stop_conditions":"模試"}`, false},
		{"null", `{"stop_conditions":null}`, false},
		{"数の要素", `{"stop_conditions":[1]}`, false},
		{"空白だけ", `{"stop_conditions":["  "]}`, false},
		{"長すぎる", `{"stop_conditions":["` + long + `"]}`, false},
		{"多すぎる", `{"stop_conditions":` + string(many) + `}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			details := validateScopeConstraints([]byte(tc.scope), "scope")
			if tc.ok && len(details) != 0 {
				t.Errorf("details = %v, want なし", details)
			}
			if !tc.ok && (len(details) != 1 || details[0].Field != "scope.stop_conditions") {
				t.Errorf("details = %v, want scope.stop_conditions の1件", details)
			}
		})
	}
}

func TestCreateDelegationValidatesStopConditions(t *testing.T) {
	e := validateCreateDelegation(createDelegationRequest{
		MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
		Scope: json.RawMessage(`{"stop_conditions":[""]}`),
	})
	if e == nil || e.Details[0].Field != "scope.stop_conditions" {
		t.Errorf("err = %+v, want scope.stop_conditions", e)
	}
}

func TestResumeDelegationReplacesStopConditionsOnly(t *testing.T) {
	q := controlFake("paused", 1)
	q.latestDelegationPlan, q.latestDelegationPlanErr = gen.GetLatestDelegationPlanRow{Version: 2, Status: "accepted"}, nil
	q.latestAcceptedPlan = gen.GetLatestAcceptedDelegationPlanRow{Version: 2}
	q.planByVersion = map[int32]gen.GetDelegationPlanByVersionRow{
		2: {ID: "01K2PLANV2", Skeleton: []byte(`{}`), BodyMd: "v2", Status: "accepted"},
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume",
		`{"stop_conditions":["計画より1週間遅れたとき"]}`, "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.updatedScopes) != 1 {
		t.Fatalf("UpdateDelegationScope の回数 = %d", len(q.updatedScopes))
	}
	var scope map[string]any
	_ = json.Unmarshal(q.updatedScopes[0].Scope, &scope)
	if b, _ := scope["budget"].(map[string]any); b["tickets"] != float64(3) {
		t.Errorf("budget = %v, want 元のまま（tickets 3）", scope["budget"])
	}
	if sc, _ := scope["stop_conditions"].([]any); len(sc) != 1 || sc[0] != "計画より1週間遅れたとき" {
		t.Errorf("stop_conditions = %v", scope["stop_conditions"])
	}
	if len(q.ticket.comments) != 1 || !strings.Contains(q.ticket.comments[0].BodyMd, "止まる条件を直した") {
		t.Errorf("comments = %+v", q.ticket.comments)
	}
}

func TestResumeDelegationRejectsBadStopConditions(t *testing.T) {
	q := controlFake("paused", 1)
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.resumeDelegation(rec, ticketReq(http.MethodPost, "/delegation/resume", `{"stop_conditions":"遅れたら"}`, "31"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "stop_conditions", "invalid") {
		t.Errorf("details = %v", errorOf(t, rec).Details)
	}
}

func TestGetDelegationStatusIncludesLatestPlanForApproval(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testTicketID, Status: "planning"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{
		ID: testDelegationID, Status: "planning", Scope: []byte(`{}`), RootSeq: 31,
		MainActorID: testMainActorID, EvaluatorActorID: testEvaluatorActorID,
	}
	q.latestDelegationPlan = gen.GetLatestDelegationPlanRow{Version: 3, Status: "proposed"}
	q.latestPlanForStatus = gen.GetLatestPlanForStatusRow{
		Version: 3, Status: "proposed", Scope: []byte(`{"budget":{"tickets":300}}`),
		Skeleton: []byte(`{"branches":[{"name":"確認","budget_share":1}]}`),
		BodyMd:   "計画の説明", ReasonMd: "出し直し", EvalVerdict: "pass", EvalRound: 1,
		EvalFindings: []byte(`[{"aspect":"values","verdict":"pass","note_md":""}]`),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, ticketReq(http.MethodGet, "/delegation-status", "", "31"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	plan, _ := viewOf(t, rec)["latest_plan"].(map[string]any)
	if plan["body_md"] != "計画の説明" || plan["reason_md"] != "出し直し" {
		t.Errorf("latest_plan = %v", plan)
	}
	skeleton, _ := plan["skeleton"].(map[string]any)
	branches, _ := skeleton["branches"].([]any)
	if len(branches) != 1 {
		t.Errorf("skeleton = %v, want 計画の骨格の枝1件", plan["skeleton"])
	}
	ev, _ := plan["evaluation"].(map[string]any)
	if ev["verdict"] != "pass" || ev["round"] != float64(1) {
		t.Errorf("evaluation = %v, want この版への pass", plan["evaluation"])
	}
}

func TestGetDelegationStatusPlanWithoutEvaluation(t *testing.T) {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.delegationList = []gen.ListActiveDelegationsForProjectRow{
		{ID: testDelegationID, RootTicketID: testTicketID, Status: "planning"},
	}
	q.ticket.descendant = true
	q.delegationByIDRow = gen.GetDelegationByIDRow{ID: testDelegationID, Status: "planning", Scope: []byte(`{}`), RootSeq: 31}
	q.latestDelegationPlan = gen.GetLatestDelegationPlanRow{Version: 1, Status: "proposed"}
	q.latestPlanForStatus = gen.GetLatestPlanForStatusRow{Version: 1, Status: "proposed", Scope: []byte(`{}`), BodyMd: "初版"}
	q.latestDelegationPlanErr = nil
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.getDelegationStatus(rec, ticketReq(http.MethodGet, "/delegation-status", "", "31"))

	plan, _ := viewOf(t, rec)["latest_plan"].(map[string]any)
	if _, has := plan["evaluation"]; has {
		t.Errorf("evaluation = %v, want 出さない（未評価）", plan["evaluation"])
	}
}
