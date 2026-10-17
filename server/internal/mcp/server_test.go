package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// fakeREST は内部呼び出しの宛先を差し替える（Design.md 8.4 の rest）。
//
// **受け取った要求をそのまま覚える。** MCP 層の仕事は「どの REST を、どんな
// クエリで叩くか」に尽きるので、検証のほとんどがここを見ることになる。
type fakeREST struct {
	status int
	body   string

	// steps は呼び出しごとに違う応答を返すための台本（手順26a）。
	// **pb_put_doc は GET してから PATCH する**ので、1回ぶんの status/body では
	// 足りない。空なら status/body を毎回返す。
	steps []fakeStep

	gotPath  string
	gotQuery url.Values
	gotAuth  string
	calls    int

	// 手順26a：write 系が何をどう送ったかを見る。**最後の1回ぶん**を持つ。
	gotMethod      string
	gotBody        string
	gotIfMatch     string
	gotContentType string
	// gotPaths / gotMethods は複数回の呼び出しを順に見るため。
	gotPaths   []string
	gotMethods []string
}

// fakeStep は台本の1手。
type fakeStep struct {
	status int
	body   string
}

func (f *fakeREST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.gotPath = r.URL.Path
	f.gotQuery = r.URL.Query()
	f.gotAuth = r.Header.Get("Authorization")
	f.gotMethod = r.Method
	f.gotIfMatch = r.Header.Get("If-Match")
	f.gotContentType = r.Header.Get("Content-Type")
	f.gotPaths = append(f.gotPaths, r.URL.Path)
	f.gotMethods = append(f.gotMethods, r.Method)
	f.gotBody = ""
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		f.gotBody = string(b)
	}

	status, body := f.status, f.body
	if len(f.steps) > 0 {
		st := f.steps[min(f.calls, len(f.steps)-1)]
		status, body = st.status, st.body
	}
	f.calls++
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// agentPrincipal は所有者つきのエージェント（Design.md 6.5 の委譲）。
func agentPrincipal() *auth.Principal {
	return &auth.Principal{
		ActorID:      "01AGENT000000000000000000",
		ActorKind:    auth.ActorKindAgent,
		OwnerActorID: "01OWNER000000000000000000",
		TokenType:    auth.TokenTypeAgent,
		Source:       auth.SourceBearer,
	}
}

// userPrincipal は人のトークン。
func userPrincipal() *auth.Principal {
	return &auth.Principal{
		ActorID:   "01USER0000000000000000000",
		ActorKind: auth.ActorKindUser,
		TokenType: auth.TokenTypeAPI,
		Source:    auth.SourceBearer,
	}
}

// callMCP は /mcp/{key} を1回叩く。
func callMCP(t *testing.T, h *Handler, p *auth.Principal, body string) *httptest.ResponseRecorder {
	t.Helper()
	return callMCPMethod(t, h, p, http.MethodPost, body)
}

// callMCPMethod は HTTP() の口（公式 SDK。Design.md 8.4）を1回叩く。
//
// **Accept に両方を入れる。** 実際のクライアントは4つとも送り（pb-158 のプローブ）、
// SDK は欠けた要求を 400 で拒む。**SDK は応答を SSE で返すことがある**ので、
// 応答（id を持つ data 行）だけを取り出して JSON の本文として読めるようにする。
func callMCPMethod(t *testing.T, h *Handler, p *auth.Principal, method, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, "/mcp/demo", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer pb_agt_dummy")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")

	// ルータを通さないので、chi の URL パラメータは自分で載せる
	// （v1 のハンドラ単体テストと同じ作り）。
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	if p != nil {
		ctx = auth.NewPrincipalContext(ctx, p)
	}

	w := httptest.NewRecorder()
	h.HTTP().ServeHTTP(w, r.WithContext(ctx))
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		var last string
		for _, line := range strings.Split(w.Body.String(), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok && strings.Contains(data, `"id"`) {
				last = data
			}
		}
		w.Body = bytes.NewBufferString(last)
	}
	return w
}

// rpcResponse は試験で読む JSON-RPC の応答。
type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeRPC は応答を JSON-RPC として読む。
func decodeRPC(t *testing.T, w *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var res rpcResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("応答が JSON-RPC として読めない: %v（本文 %s）", err, w.Body.String())
	}
	return res
}

// resultAsTool は result をツール結果として読む。
func resultAsTool(t *testing.T, res rpcResponse) toolResult {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("JSON-RPC エラーが返った: %+v", res.Error)
	}
	b, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatalf("result を組み直せない: %v", err)
	}
	var out toolResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("result がツール結果ではない: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "text" {
		t.Fatalf("content はテキスト1件であること: %+v", out.Content)
	}
	return out
}

// **プロトコルそのもの（壊れた JSON・バッチ・通知の 202・ping・版の交渉）は
// 公式 SDK の持ち物である**（Design.md 8.4。pb-258）。ここでは PB が決めた約束だけを見る。
// 新方式（2026-07-28）の確かめは結合試験（httpapi の TestMCPIntegration）にある。

func TestGETIsMethodNotAllowed(t *testing.T) {
	// Design.md 8.4：サーバ発の通知を持たないので GET は 405（Stateless）。
	h := New(&fakeREST{}, "v0")
	w := callMCPMethod(t, h, agentPrincipal(), http.MethodGet, "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET のステータス = %d, want 405", w.Code)
	}
}

func TestCookieIsRejected(t *testing.T) {
	// Design.md 8.3：MCP は Bearer だけを受ける（CSRF の面を作らない）。
	h := New(&fakeREST{}, "v0")
	p := userPrincipal()
	p.Source = auth.SourceCookie

	w := callMCP(t, h, p, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Cookie 認証のステータス = %d, want 401", w.Code)
	}
}

func TestInitializeNamesPB(t *testing.T) {
	// 旧方式（initialize）のクライアント（Desktop・Codex・Copilot。pb-158）にも応じる。
	h := New(&fakeREST{}, "v1.2.3")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize",`+
			`"params":{"protocolVersion":"2025-06-18","capabilities":{},`+
			`"clientInfo":{"name":"test","version":"0"}}}`))
	if res.Error != nil {
		t.Fatalf("initialize が失敗した: %+v", res.Error)
	}
	b, _ := json.Marshal(res.Result)
	var got struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("result が initialize の応答ではない: %v", err)
	}
	if got.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want 2025-06-18（クライアントの版をそのまま）", got.ProtocolVersion)
	}
	if got.ServerInfo.Name != ServerName || got.ServerInfo.Version != "v1.2.3" {
		t.Errorf("serverInfo = %+v, want %s / v1.2.3", got.ServerInfo, ServerName)
	}
}

func TestToolsListReturnsReadAndWriteTools(t *testing.T) {
	// Design.md 8.2 のツール16件。
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if res.Error != nil {
		t.Fatalf("tools/list が失敗した: %+v", res.Error)
	}

	b, _ := json.Marshal(res.Result)
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("tools/list の応答を読めない: %v", err)
	}

	var names []string
	for _, tl := range out.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" {
			t.Errorf("%s に description が無い（8.6：ツールの説明がエージェントの行動を規定する）", tl.Name)
		}
		if tl.InputSchema.Type != "object" {
			t.Errorf("%s の inputSchema.type = %q, want object", tl.Name, tl.InputSchema.Type)
		}
		if tl.InputSchema.Properties == nil {
			t.Errorf("%s に properties が無い（引数を取らないツールでも {} を出す）", tl.Name)
		}
	}

	// read 7件（/pb-onboard が呼ぶ順。pb_list_tags はタグの語彙で pb_get_doc の隣。
	// pb_list_comments は既存コメントを読む口で、pb_get_task の隣＝9.8 の子資源）
	// ＋ コンテキストパック1件（手順27。/pb-implement が「契約を読む → 前提を読む」の
	// 順で呼ぶので pb_get_task の直後）＋ write 7件（10.7.1 の開発フローの順。
	// pb_update_ticket と pb_put_dod は起票したあと直すので pb_create_ticket の隣、
	// pb_add_reference は「実装中に分かったことを書く」の隣＝作業の跡を積む位置。
	// pb_submit_plan は自律実行の main が計画の版を出す口、pb_submit_evaluation は
	// 評価エージェントが計画・成果を評価する口、pb_raise_confirmation は main が
	// 人への確認待ちを立てる口、pb_update_handoff は main が引き継ぎノートを更新する口
	// （Delegation.md 15章 D7 の新設5ツールのうちMCPに開けるのはこの4つ。
	// pb-247・pb-248・pb-249・pb-250）。pb_record_metric は main が指標の値を入れる口（pb-277）＋ 遷移2件（見てから動かす順。
	// 手順26b）＋ 完了レポート1件（手順26c。/pb-implement の流れの終端）。
	//
	// **pb_claim_task / pb_release_task は持たない（構想）**
	// （Requirements.md 10.3.3——排他が実際に要るのは自律取得 pb_next_task から
	// である）。
	want := []string{
		"pb_get_agent_usage", "pb_list_agent_runs", "pb_start_work", "pb_record_work", "pb_list_work",
		"pb_get_planning_data", "pb_add_link", "pb_update_link", "pb_delete_link", "pb_move_task", "pb_reschedule_task",
		"pb_get_project", "pb_list_docs", "pb_get_doc", "pb_list_tags",
		"pb_list_tasks", "pb_get_task", "pb_list_comments", "pb_get_context",
		"pb_create_ticket", "pb_update_ticket", "pb_put_dod",
		"pb_post_note", "pb_add_reference", "pb_submit_plan", "pb_submit_evaluation",
		"pb_raise_confirmation", "pb_update_handoff", "pb_record_metric",
		"pb_create_doc", "pb_put_doc",
		"pb_list_transitions", "pb_transition_task",
		"pb_submit_result",
		"pb_wait",
	}
	// **並び順は見ない。** 公式 SDK は名前の順に並べ替えて返す（仕様 2026-07-28 が
	// キャッシュのために決まった順を求める。pb-258）。呼ぶ順は /pb-implement の手順が教える。
	slices.Sort(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("ツール = %v, want %v（名前の順）", names, want)
	}

	// 必須の引数が宣言されていること。
	for _, tl := range out.Tools {
		switch tl.Name {
		case "pb_get_doc":
			if strings.Join(tl.InputSchema.Required, ",") != "path" {
				t.Errorf("pb_get_doc の required = %v, want [path]", tl.InputSchema.Required)
			}
		case "pb_get_task":
			if strings.Join(tl.InputSchema.Required, ",") != "seq" {
				t.Errorf("pb_get_task の required = %v, want [seq]", tl.InputSchema.Required)
			}
		case "pb_submit_result":
			if strings.Join(tl.InputSchema.Required, ",") != "seq,status" {
				t.Errorf("pb_submit_result の required = %v, want [seq status]",
					tl.InputSchema.Required)
			}
		}
	}
}

func TestToolsListDoesNotLeakCallField(t *testing.T) {
	// call は関数なので JSON へ出ない（出ると tools/list が壊れる）。
	h := New(&fakeREST{}, "v0")
	w := callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if strings.Contains(w.Body.String(), `"call"`) {
		t.Errorf("tools/list に call が出ている: %s", w.Body.String())
	}
}

// TestToolsListDeclaresNestedSchema は、入れ子のスキーマが tools/list に出ることを見る。
//
// **手順26c で property に items / properties を足した。** pb_submit_result の
// 引数（Requirements.md 10.6.1）が配列とオブジェクトの入れ子を持つためで、
// **ここが表せないとモデルは中身の形を知らないまま埋める。**
func TestToolsListDeclaresNestedSchema(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))

	b, _ := json.Marshal(res.Result)
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]struct {
					Type  string `json:"type"`
					Items *struct {
						Type       string                     `json:"type"`
						Properties map[string]json.RawMessage `json:"properties"`
						Required   []string                   `json:"required"`
					} `json:"items"`
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("tools/list の応答を読めない: %v", err)
	}

	for _, tl := range out.Tools {
		if tl.Name != "pb_submit_result" {
			continue
		}
		dod, ok := tl.InputSchema.Properties["dod_results"]
		if !ok || dod.Items == nil {
			t.Fatalf("dod_results に items が無い: %+v", tl.InputSchema.Properties)
		}
		if strings.Join(dod.Items.Required, ",") != "id,passed" {
			t.Errorf("dod_results.items.required = %v, want [id passed]", dod.Items.Required)
		}
		if _, ok := dod.Items.Properties["evidence"]; !ok {
			t.Errorf("dod_results.items に evidence が無い: %+v", dod.Items.Properties)
		}
		cost := tl.InputSchema.Properties["cost"]
		if cost.Type != "object" || len(cost.Properties) != 3 {
			t.Errorf("cost = %+v, want object（tokens / turns / wall_clock_min）", cost)
		}
		return
	}
	t.Fatal("pb_submit_result が tools/list に無い")
}

// findings の要素型を省くと、Codex は文字列配列として公開し、
// REST が受けるオブジェクト配列を提出できなくなる。
func TestToolsListDeclaresEvaluationFindings(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	b, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema schema `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for _, tool := range out.Tools {
		if tool.Name != "pb_submit_evaluation" {
			continue
		}
		findings := tool.InputSchema.Properties["findings"]
		if findings.Type != "array" || findings.Items == nil || findings.Items.Type != "object" {
			t.Fatalf("findings はオブジェクト配列でなければならない: %+v", findings)
		}
		for _, key := range []string{"aspect", "verdict", "note_md"} {
			if findings.Items.Properties[key].Type != "string" || !slices.Contains(findings.Items.Required, key) {
				t.Errorf("findings.items の %s は必須の文字列でなければならない: %+v", key, findings.Items)
			}
		}
		if got := findings.Items.Properties["verdict"].Enum; !slices.Equal(got, []string{"pass", "fail"}) {
			t.Errorf("観点ごとの判定 = %v, want pass / fail", got)
		}
		return
	}
	t.Fatal("pb_submit_evaluation が tools/list に無い")
}
