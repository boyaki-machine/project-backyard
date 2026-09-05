package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func callMCPMethod(t *testing.T, h *Handler, p *auth.Principal, method, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, "/mcp/demo", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer pb_agt_dummy")

	// ルータを通さないので、chi の URL パラメータは自分で載せる
	// （v1 のハンドラ単体テストと同じ作り）。
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	if p != nil {
		ctx = auth.NewPrincipalContext(ctx, p)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(ctx))
	return w
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

func TestGETIsMethodNotAllowed(t *testing.T) {
	// Design.md 8.4：SSE ストリームを持たないので GET は 405。
	h := New(&fakeREST{}, "v0")
	w := callMCPMethod(t, h, agentPrincipal(), http.MethodGet, "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET のステータス = %d, want 405", w.Code)
	}
	if !strings.Contains(w.Body.String(), "method_not_allowed") {
		t.Errorf("2.5 の形式で返っていない: %s", w.Body.String())
	}
}

func TestCookieIsRejected(t *testing.T) {
	// Design.md 8.3：MCP は Bearer だけを受ける（CSRF の面を作らない）。
	h := New(&fakeREST{}, "v0")
	p := userPrincipal()
	p.Source = auth.SourceCookie

	w := callMCP(t, h, p, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Cookie 認証のステータス = %d, want 401", w.Code)
	}
}

func TestBrokenJSONIsParseError(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	w := callMCP(t, h, agentPrincipal(), `{"jsonrpc":`)

	if w.Code != http.StatusOK {
		t.Fatalf("JSON-RPC のエラーは HTTP 200 で返す。got %d", w.Code)
	}
	res := decodeRPC(t, w)
	if res.Error == nil || res.Error.Code != codeParseError {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeParseError)
	}
	if string(res.ID) != "null" {
		t.Errorf("解釈できない要求の id = %s, want null", res.ID)
	}
}

func TestBatchIsInvalidRequest(t *testing.T) {
	// MCP は 2025-06-18 で JSON-RPC のバッチを外した（Design.md 8.4）。
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`))

	if res.Error == nil || res.Error.Code != codeInvalidRequest {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidRequest)
	}
}

func TestWrongJSONRPCVersionIsInvalidRequest(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`))

	if res.Error == nil || res.Error.Code != codeInvalidRequest {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidRequest)
	}
}

func TestNotificationGets202WithoutBody(t *testing.T) {
	// Design.md 8.4：通知には応答を返さない。
	h := New(&fakeREST{}, "v0")
	w := callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	if w.Code != http.StatusAccepted {
		t.Errorf("通知のステータス = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("通知に本文を返している: %s", w.Body.String())
	}
}

func TestUnknownNotificationIsIgnored(t *testing.T) {
	// 知らない通知は黙って捨てる（method not found を返さない）。
	h := New(&fakeREST{}, "v0")
	w := callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","method":"notifications/cancelled"}`)

	if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Errorf("知らない通知の応答 = %d %q, want 202 かつ本文なし", w.Code, w.Body.String())
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`{"jsonrpc":"2.0","id":9,"method":"resources/list"}`))

	if res.Error == nil || res.Error.Code != codeMethodNotFound {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeMethodNotFound)
	}
	if string(res.ID) != "9" {
		t.Errorf("id を返していない: %s", res.ID)
	}
}

func TestPingReturnsEmptyResult(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), `{"jsonrpc":"2.0","id":"a","method":"ping"}`))

	if res.Error != nil {
		t.Fatalf("ping が失敗した: %+v", res.Error)
	}
	if string(res.ID) != `"a"` {
		t.Errorf("文字列の id を返せていない: %s", res.ID)
	}
}

func TestInitializeEchoesKnownProtocolVersion(t *testing.T) {
	// Design.md 8.4：知っている版はそのまま返す。
	h := New(&fakeREST{}, "v1.2.3")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize",`+
			`"params":{"protocolVersion":"2024-11-05","capabilities":{},`+
			`"clientInfo":{"name":"test","version":"0"}}}`))

	got := initializeOf(t, res)
	if got.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05（クライアントの版をそのまま）", got.ProtocolVersion)
	}
	if got.ServerInfo.Name != ServerName || got.ServerInfo.Version != "v1.2.3" {
		t.Errorf("serverInfo = %+v, want %s / v1.2.3", got.ServerInfo, ServerName)
	}
	if got.Capabilities.Tools.ListChanged {
		t.Error("listChanged は false であること（サーバ発の通知を持たない）")
	}
}

func TestInitializeFallsBackToLatestVersion(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`))

	if got := initializeOf(t, res); got.ProtocolVersion != protocolVersions[0] {
		t.Errorf("protocolVersion = %q, want %q（PB の最新）", got.ProtocolVersion, protocolVersions[0])
	}
}

func initializeOf(t *testing.T, res rpcResponse) initializeResult {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("initialize が失敗した: %+v", res.Error)
	}
	b, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatalf("result を組み直せない: %v", err)
	}
	var out initializeResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("result が initialize の応答ではない: %v", err)
	}
	return out
}

func TestToolsListReturnsReadAndWriteTools(t *testing.T) {
	// Design.md 8.2 の read 行から pb_get_context（手順27）を除いた5件。
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

	// read 5件（/pb-onboard が呼ぶ順）＋ write 3件（10.7.1 の開発フローの順）
	// ＋ 遷移2件（見てから動かす順。手順26b）＋ 完了レポート1件（手順26c。
	// /pb-implement の流れの終端）。
	//
	// **pb_claim_task / pb_release_task は Phase 3 へ送った**
	// （Requirements.md 10.3.3——排他が実際に要るのは自律取得 pb_next_task から
	// である）。**pb_get_context は手順27。**
	want := []string{
		"pb_get_project", "pb_list_docs", "pb_get_doc", "pb_list_tasks", "pb_get_task",
		"pb_create_ticket", "pb_post_note", "pb_put_doc",
		"pb_list_transitions", "pb_transition_task",
		"pb_submit_result",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("ツール = %v, want %v（read → write → 遷移 → 報告 の順）", names, want)
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
