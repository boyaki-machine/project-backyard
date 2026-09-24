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

// 完了レポートAPI（ApiDesign.md 9.15）の単体テスト。手順26c。
//
// 認可（ticket.transition）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、**盤面を動かさない
// こと**（dod_item を書かない・状態を進めない）、**unsatisfied_dod が
// is_satisfied ではなく自己申告との突き合わせであること**、列に出す値だけを
// 検証していること、そして完了レポートのコメントが1件作られることである。

const (
	testRunActorID = "01K2AGENT000000000000000001"
	testDoDIDA     = "01K2DODR00000000000000000A"
	testDoDIDB     = "01K2DODR00000000000000000B"
)

// reportReq は /tickets/{seq}/reports のリクエストを組み立てる。
//
// **DisplayName と TokenID を載せる**——9.15 の応答が submitted_by に表示名を
// 出し、agent_run.token_id に実行時のトークンを写すためである。
func reportReq(body, actorID, actorKind string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	rc.URLParams.Add("seq", "31")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{
		ActorID: actorID, ActorKind: actorKind,
		DisplayName: "claude-code", TokenID: "01K2TOKEN0000000000000001",
	})
	return req.WithContext(ctx)
}

// agentReportReq はエージェントとして叩く既定の形。
func agentReportReq(body string) *http.Request {
	return reportReq(body, testRunActorID, "agent")
}

// reportFake は seq=31 のチケットに完了条件が2件ある状態のフェイクを返す。
func reportFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticket.dodRows = []gen.GetTicketDoDItemRow{
		{ID: testDoDIDA, Type: dodTypeManual, Body: "ユニットテストが通ること", SortOrder: 10},
		{ID: testDoDIDB, Type: dodTypeManual, Body: "設計文書を更新すること", SortOrder: 20},
	}
	q.ticket.agentInfo = &gen.GetAgentRuntimeInfoRow{
		ClientKind: "claude_code",
		ModelName:  txt("claude-opus-5"),
	}
	return q
}

type reportJSON struct {
	ID              string  `json:"id"`
	AgentRunID      string  `json:"agent_run_id"`
	Seq             int32   `json:"seq"`
	Status          string  `json:"status"`
	KnowledgeImpact *string `json:"knowledge_impact"`
	SubmittedAt     string  `json:"submitted_at"`
	SubmittedBy     struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	} `json:"submitted_by"`
	CommentID      string `json:"comment_id"`
	UnsatisfiedDoD []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Body string `json:"body"`
	} `json:"unsatisfied_dod"`
}

// rawOf は本文を map へ読む（reportOf と renderReportComment の入口を揃える）。
func rawOf(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("本文を読めない: %v", err)
	}
	return raw
}

func decodeReport(t *testing.T, rec *httptest.ResponseRecorder) reportJSON {
	t.Helper()
	var v reportJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// ── 提出（9.15）─────────────────────────────────────────────

// 1回の提出が agent_run 1行・agent_report 1行・コメント1件を作る（9.15）。
func TestSubmitReportWritesRunReportAndComment(t *testing.T) {
	q := reportFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{
		"status":"completed",
		"dod_results":[{"id":"`+testDoDIDA+`","passed":true,"evidence":"go test → ok"}],
		"cost":{"tokens":128000,"turns":34,"wall_clock_min":42},
		"knowledge_impact":"minor"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.agentRuns) != 1 {
		t.Fatalf("agent_run = %d件, want 1", len(q.ticket.agentRuns))
	}
	if len(q.ticket.agentReports) != 1 {
		t.Fatalf("agent_report = %d件, want 1", len(q.ticket.agentReports))
	}
	if len(q.ticket.comments) != 1 {
		t.Fatalf("コメント = %d件, want 1", len(q.ticket.comments))
	}

	run := q.ticket.agentRuns[0]
	// **status に立つのは completed だけである**（DbDesign.md 8.2.4）。
	if run.Status != runStatusCompleted {
		t.Errorf("agent_run.status = %q, want %q", run.Status, runStatusCompleted)
	}
	// **client_kind / model_name は実行時点の値を写す**（同上）。
	if run.ClientKind.String != "claude_code" || run.ModelName.String != "claude-opus-5" {
		t.Errorf("実行時の値が写っていない: %+v", run)
	}
	if run.TokensUsed.Int64 != 128000 || run.Turns.Int32 != 34 {
		t.Errorf("コストが列に出ていない: %+v", run)
	}
	// **started_at は wall_clock_min から逆算する**（同上）。
	if gap := run.EndedAt.Time.Sub(run.StartedAt.Time); gap != 42*time.Minute {
		t.Errorf("started_at の逆算 = %v, want 42m", gap)
	}
	// **workflow_version は埋めない**（workflow に版の列が無い）。
	if run.ID == "" || run.RetryCount != 0 {
		t.Errorf("retry_count は初回 0 のはず: %+v", run)
	}

	rep := q.ticket.agentReports[0]
	if rep.AgentRunID != run.ID {
		t.Errorf("report が run を指していない: %q vs %q", rep.AgentRunID, run.ID)
	}
	if rep.KnowledgeImpact.String != "minor" {
		t.Errorf("knowledge_impact が列に出ていない: %+v", rep)
	}

	// **コメントは kind='progress' で、agent_run を指す**（9.15）。
	c := q.ticket.comments[0]
	if c.Kind != commentKindProgress {
		t.Errorf("コメントの kind = %q, want %q", c.Kind, commentKindProgress)
	}
	if c.AgentRunID.String != run.ID {
		t.Errorf("コメントが agent_run を指していない: %+v", c)
	}
	if c.Origin != originAgent {
		t.Errorf("コメントの origin = %q, want %q", c.Origin, originAgent)
	}

	got := decodeReport(t, rec)
	if got.CommentID != c.ID || got.AgentRunID != run.ID {
		t.Errorf("応答が書いた行を指していない: %+v", got)
	}
	if got.SubmittedBy.DisplayName != "claude-code" || got.SubmittedBy.Kind != "agent" {
		t.Errorf("submitted_by = %+v", got.SubmittedBy)
	}
}

// **unsatisfied_dod は「passed: true として現れなかったもの」である**（9.15）。
//
// is_satisfied を返すと作業直後は必ず全件になり、/pb-implement の手順7 が
// 終わらなくなる。**満たしていない扱いの行でも、passed: true と申告されれば
// 返らない**ことをここで測る。
func TestSubmitReportUnsatisfiedIsSelfReportDiff(t *testing.T) {
	q := reportFake()
	// A は is_satisfied=false のままだが、レポートは passed: true と申告する。
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{
		"status":"partial",
		"dod_results":[
			{"id":"`+testDoDIDA+`","passed":true},
			{"id":"`+testDoDIDB+`","passed":false,"note":"レビュー待ち"}]}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeReport(t, rec)
	if len(got.UnsatisfiedDoD) != 1 {
		t.Fatalf("unsatisfied_dod = %d件, want 1: %+v", len(got.UnsatisfiedDoD), got.UnsatisfiedDoD)
	}
	if got.UnsatisfiedDoD[0].ID != testDoDIDB {
		t.Errorf("unsatisfied_dod = %q, want %q", got.UnsatisfiedDoD[0].ID, testDoDIDB)
	}
}

// **触れなかった完了条件も未充足として返る**（9.15）。
func TestSubmitReportUnsatisfiedIncludesUnreported(t *testing.T) {
	q := reportFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"completed"}`))

	got := decodeReport(t, rec)
	if len(got.UnsatisfiedDoD) != 2 {
		t.Fatalf("unsatisfied_dod = %d件, want 2（1件も報告していない）", len(got.UnsatisfiedDoD))
	}
}

// **完了条件のチェックを書き換えない**（9.15）。
//
// manual の定義は「人間がチェックを入れる」であり（Requirements.md 10.5.2）、
// エージェントが立てると型の定義に反する。
func TestSubmitReportDoesNotTouchDoDItems(t *testing.T) {
	q := reportFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{
		"status":"completed",
		"dod_results":[{"id":"`+testDoDIDA+`","passed":true}]}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	for _, op := range q.opLog {
		if op == "UpdateDoDItem" || op == "SetTicketStatus" {
			t.Fatalf("盤面を動かしている: %q が呼ばれた（opLog=%v）", op, q.opLog)
		}
	}
	if len(q.ticket.dodCreated) != 0 || len(q.ticket.statusSet) != 0 {
		t.Errorf("完了条件・状態に書き込んでいる")
	}
}

// **retry_count は同じ（チケット × アクター）の既存の run 数である**（8.2.4）。
func TestSubmitReportCountsRetries(t *testing.T) {
	q := reportFake()
	q.ticket.priorRunCount = 2
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{"status":"blocked"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.ticket.agentRuns[0].RetryCount; got != 2 {
		t.Errorf("retry_count = %d, want 2", got)
	}
}

// **人のトークンで叩かれたときは client_kind / model が空になる**（8.2.4）。
func TestSubmitReportFromHumanHasNoAgentInfo(t *testing.T) {
	q := reportFake()
	q.ticket.agentInfo = nil // agent の行が無い
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, reportReq(`{"status":"completed"}`, testActorID, "user"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	run := q.ticket.agentRuns[0]
	if run.ClientKind.Valid || run.ModelName.Valid {
		t.Errorf("人の提出に実行時の値が入っている: %+v", run)
	}
	if q.ticket.comments[0].Origin != originHuman {
		t.Errorf("origin = %q, want %q", q.ticket.comments[0].Origin, originHuman)
	}
}

// **知らないキーは拒まず report jsonb へ入れる**（9.15）。
func TestSubmitReportKeepsUnknownKeys(t *testing.T) {
	q := reportFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.submitTicketReport(rec, agentReportReq(`{
		"status":"completed",
		"artifacts":[{"type":"pull_request","url":"https://example.com/pr/1"}],
		"weather":"晴れ"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	var saved map[string]any
	if err := json.Unmarshal(q.ticket.agentReports[0].Report, &saved); err != nil {
		t.Fatalf("report を読めない: %v", err)
	}
	if _, ok := saved["artifacts"]; !ok {
		t.Errorf("artifacts が保存されていない: %v", saved)
	}
	// **知らないキーも保存する**（Design.md 8.4 と同じ判断）。
	if _, ok := saved["weather"]; !ok {
		t.Errorf("知らないキーが落ちている: %v", saved)
	}
	// **省略したキーは書かない**——空の配列を並べると「報告した」と読めてしまう。
	if _, ok := saved["findings"]; ok {
		t.Errorf("省略したキーが入っている: %v", saved)
	}
}

// ── 検証（9.15）─────────────────────────────────────────────

func TestSubmitReportValidation(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{"status が無い", `{}`, "status", "required"},
		{"status が値域外", `{"status":"finished"}`, "status", "invalid"},
		{"knowledge_impact が値域外",
			`{"status":"completed","knowledge_impact":"huge"}`, "knowledge_impact", "invalid"},
		{"cost が負",
			`{"status":"completed","cost":{"tokens":-1}}`, "cost.tokens", "out_of_range"},
		{"知らない完了条件",
			`{"status":"completed","dod_results":[{"id":"01K2NOPE0000000000000000X","passed":true}]}`,
			"dod_results[0].id", "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := reportFake()
			h, _ := ticketHandler(q)

			rec := httptest.NewRecorder()
			h.submitTicketReport(rec, agentReportReq(tc.body))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Details []struct {
						Field string `json:"field"`
						Code  string `json:"code"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("応答を読めない: %v", err)
			}
			found := false
			for _, d := range body.Error.Details {
				if d.Field == tc.field && d.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Errorf("details に %s/%s が無い: %s", tc.field, tc.code, rec.Body.String())
			}
			// **検証で落ちたら1行も書かない。**
			if len(q.ticket.agentRuns) != 0 || len(q.ticket.comments) != 0 {
				t.Errorf("422 なのに書き込んでいる")
			}
		})
	}
}
