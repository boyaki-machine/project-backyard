package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// callTool1 はツールを1回呼び、ツール結果を返す。
func callTool1(t *testing.T, h *Handler, body string) toolResult {
	t.Helper()
	return resultAsTool(t, decodeRPC(t, callMCP(t, h, agentPrincipal(), body)))
}

// toolCallBody は tools/call のリクエストを組み立てる。
func toolCallBody(name, args string) string {
	if args == "" {
		args = "{}"
	}
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name +
		`","arguments":` + args + `}}`
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	h := New(&fakeREST{}, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_delete_everything", "")))

	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidParams)
	}
}

func TestGetProjectCallsREST(t *testing.T) {
	rest := &fakeREST{body: `{"key":"demo","name":"デモ"}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_project", ""))

	if rest.gotPath != "/api/v1/projects/demo" {
		t.Errorf("叩いた REST = %q, want /api/v1/projects/demo", rest.gotPath)
	}
	if rest.gotAuth != "Bearer pb_agt_dummy" {
		t.Errorf("Authorization を引き継いでいない: %q", rest.gotAuth)
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
	if out.Content[0].Text != rest.body {
		t.Errorf("応答をそのまま返していない: %s", out.Content[0].Text)
	}
}

func TestListDocsAlwaysAsksOutline(t *testing.T) {
	// ApiDesign.md 10.2：見出しが無いと、読む章を決められない。
	rest := &fakeREST{body: `{"items":[]}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_docs", ""))

	if rest.gotPath != "/api/v1/projects/demo/docs" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
	if got := rest.gotQuery.Get("outline"); got != "1" {
		t.Errorf("outline = %q, want 1", got)
	}
}

func TestGetDocReturnsMarkdownOnly(t *testing.T) {
	// Design.md 8.5：本文だけを返す（JSON でくるまない）。
	rest := &fakeREST{body: `{"path":"rules","title":"規約","body_md":"# 規約\n\n## 命名\n","version":3}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_doc", `{"path":"rules"}`))

	if out.Content[0].Text != "# 規約\n\n## 命名\n" {
		t.Errorf("本文だけを返していない: %q", out.Content[0].Text)
	}
	if rest.gotPath != "/api/v1/projects/demo/docs/rules" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
	if rest.gotQuery.Has("section") {
		t.Errorf("section を渡していないのにクエリに出ている: %v", rest.gotQuery)
	}
}

func TestGetDocPassesSection(t *testing.T) {
	rest := &fakeREST{body: `{"body_md":"## 命名\n"}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_get_doc", `{"path":"rules/naming","section":"命名"}`))

	if rest.gotPath != "/api/v1/projects/demo/docs/rules/naming" {
		t.Errorf("階層のパスを保っていない: %q", rest.gotPath)
	}
	if got := rest.gotQuery.Get("section"); got != "命名" {
		t.Errorf("section = %q, want 命名", got)
	}
}

func TestGetDocRequiresPath(t *testing.T) {
	rest := &fakeREST{}
	h := New(rest, "v0")

	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_get_doc", `{"section":"命名"}`)))
	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidParams)
	}
	if rest.calls != 0 {
		t.Errorf("引数が足りないのに REST を叩いている（%d 回）", rest.calls)
	}
}

func TestRESTErrorBecomesToolError(t *testing.T) {
	// Design.md 8.4：403 / 404 はプロトコルのエラーではなく、呼び出しの結果である。
	// 章が見つからないときの available_sections（10.3）を落とさないこと。
	rest := &fakeREST{
		status: http.StatusNotFound,
		body: `{"error":{"code":"not_found","message":"指定された章が見つかりません",` +
			`"available_sections":["命名","ブランチ"],"request_id":"01K2"}}`,
	}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_doc", `{"path":"rules","section":"存在しない章"}`))

	if !out.IsError {
		t.Fatal("isError が立っていない")
	}
	text := out.Content[0].Text
	if !strings.Contains(text, "HTTP 404 not_found") {
		t.Errorf("状態を伝えていない: %s", text)
	}
	if !strings.Contains(text, "available_sections") || !strings.Contains(text, "ブランチ") {
		t.Errorf("次の一手の材料が落ちている: %s", text)
	}
}

func TestForbiddenAddsScopeHintForAgent(t *testing.T) {
	// エージェントの 403 は「所有者のロール」と「トークンのスコープ」の
	// どちらが原因かを区別できない。どこを見ればよいかだけを添える。
	rest := &fakeREST{status: http.StatusForbidden,
		body: `{"error":{"code":"forbidden","message":"この操作を行う権限がありません"}}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_project", ""))
	if !strings.Contains(out.Content[0].Text, "/me/agents") {
		t.Errorf("エージェントに手がかりを添えていない: %s", out.Content[0].Text)
	}

	// 人のトークンでは添えない（所有者もスコープの発行画面も無い話になる）。
	res := decodeRPC(t, callMCP(t, h, userPrincipal(), toolCallBody("pb_get_project", "")))
	if strings.Contains(resultAsTool(t, res).Content[0].Text, "/me/agents") {
		t.Error("人のトークンにエージェント向けの手がかりを添えている")
	}
}

func TestGetTaskAcceptsSeqAsNumberOrString(t *testing.T) {
	// flexInt：モデルは "31" と書いてくることがある。
	for _, args := range []string{`{"seq":31}`, `{"seq":"31"}`} {
		rest := &fakeREST{body: `{"seq":31}`}
		h := New(rest, "v0")

		callTool1(t, h, toolCallBody("pb_get_task", args))

		if rest.gotPath != "/api/v1/projects/demo/tickets/31" {
			t.Errorf("%s → 叩いた REST = %q, want /api/v1/projects/demo/tickets/31", args, rest.gotPath)
		}
	}
}

func TestGetTaskRejectsMissingSeq(t *testing.T) {
	rest := &fakeREST{}
	h := New(rest, "v0")

	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_get_task", `{}`)))
	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidParams)
	}
	if rest.calls != 0 {
		t.Errorf("seq が無いのに REST を叩いている（%d 回）", rest.calls)
	}
}

// listBody は 9.2.2 の応答（1件）。落とす項目も入れてある。
const listBody = `{"items":[{"id":"01TICKET00000000000000000","seq":31,"type":"task",` +
	`"title":"認証APIの実装","status":{"key":"in_progress","name":"進行中","category":"in_progress"},` +
	`"priority":"high","assignee":{"id":"01USER0000000000000000000","kind":"user","display_name":"田中"},` +
	`"reporter":{"id":"01USER0000000000000000000","kind":"user","display_name":"田中"},` +
	`"working_agent":{"id":"01AGENT000000000000000000","kind":"agent","display_name":"claude-code"},` +
	`"parent_seq":null,"has_children":true,"sort_key":"0|hzzzzz:","staged_at":null,` +
	`"tags":[{"id":"01TAG00000000000000000000","name":"設計"}],"sprint":null,` +
	`"estimate_point":5,"estimate_hours":null,"actual_hours":3.5,"start_date":"2026-08-09",` +
	`"due_date":"2026-08-14","closed_at":null,"version":3,` +
	`"created_at":"2026-08-09T01:00:00Z","updated_at":"2026-08-11T00:12:44Z"}],` +
	`"page":1,"per_page":200,"total":48,"total_pages":1}`

func TestListTasksKeepsExactlyElevenFields(t *testing.T) {
	// Design.md 8.5：ボードの状況把握に要らない項目を件数ぶん掛け算しない。
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_list_tasks", `{}`))

	var got struct {
		Items      []map[string]json.RawMessage `json:"items"`
		Page       int                          `json:"page"`
		PerPage    int                          `json:"per_page"`
		Total      int                          `json:"total"`
		TotalPages int                          `json:"total_pages"`
	}
	if err := json.Unmarshal([]byte(out.Content[0].Text), &got); err != nil {
		t.Fatalf("軽量化した一覧を読めない: %v（%s）", err, out.Content[0].Text)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items の件数 = %d, want 1", len(got.Items))
	}

	want := []string{"seq", "type", "title", "status", "priority", "assignee",
		"working_agent", "parent_seq", "staged_at", "due_date", "updated_at"}
	for _, k := range want {
		if _, ok := got.Items[0][k]; !ok {
			t.Errorf("%s が落ちている", k)
		}
	}
	if len(got.Items[0]) != len(want) {
		var extra []string
		for k := range got.Items[0] {
			extra = append(extra, k)
		}
		t.Errorf("項目数 = %d（%v）, want %d", len(got.Items[0]), extra, len(want))
	}
	for _, k := range []string{"id", "sort_key", "tags", "estimate_point", "version", "created_at"} {
		if _, ok := got.Items[0][k]; ok {
			t.Errorf("%s は落とすはずの項目である", k)
		}
	}

	// ページングの4項目は 2.6 のまま残す。
	if got.Page != 1 || got.PerPage != 200 || got.Total != 48 || got.TotalPages != 1 {
		t.Errorf("ページングが保たれていない: %+v", got)
	}

	// 値そのものも保つ（選別であって作り直しではない）。
	if string(got.Items[0]["title"]) != `"認証APIの実装"` {
		t.Errorf("title = %s", got.Items[0]["title"])
	}
	if string(got.Items[0]["parent_seq"]) != "null" {
		t.Errorf("null の項目が落ちている: parent_seq = %s", got.Items[0]["parent_seq"])
	}
}

func TestListTasksMapsMeToOwnerForAgent(t *testing.T) {
	// Design.md 8.5：エージェントのアクターに担当は付かない。
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_tasks", `{"assignee":"me","open":true}`))

	if got := rest.gotQuery.Get("assignee"); got != "01OWNER000000000000000000" {
		t.Errorf("assignee = %q, want 所有者のアクターID", got)
	}
	if got := rest.gotQuery.Get("open"); got != "true" {
		t.Errorf("open = %q, want true", got)
	}
}

func TestListTasksKeepsMeForHumanToken(t *testing.T) {
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	res := decodeRPC(t, callMCP(t, h, userPrincipal(), toolCallBody("pb_list_tasks", `{"assignee":"me"}`)))
	resultAsTool(t, res)

	if got := rest.gotQuery.Get("assignee"); got != "01USER0000000000000000000" {
		t.Errorf("assignee = %q, want 本人のアクターID", got)
	}
}

func TestListTasksMapsMeInsideCommaList(t *testing.T) {
	// 9.2.1 のカンマ区切り（OR）を保つ。
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_tasks", `{"assignee":"me,none"}`))

	if got := rest.gotQuery.Get("assignee"); got != "01OWNER000000000000000000,none" {
		t.Errorf("assignee = %q, want 01OWNER000000000000000000,none", got)
	}
}

func TestListTasksRejectsPerPageOutOfRange(t *testing.T) {
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_list_tasks", `{"per_page":500}`)))
	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラーコード = %+v, want %d", res.Error, codeInvalidParams)
	}
	if rest.calls != 0 {
		t.Errorf("範囲外なのに REST を叩いている（%d 回）", rest.calls)
	}
}

func TestListTasksOmitsEmptyFilters(t *testing.T) {
	// 空の値をクエリに載せると、9.2.1 の解析で 422 になる。
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_tasks", `{}`))

	if len(rest.gotQuery) != 0 {
		t.Errorf("引数なしの呼び出しでクエリが付いている: %v", rest.gotQuery)
	}
}

func TestListTasksPassesParentAsGiven(t *testing.T) {
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_tasks", `{"parent":"12,30"}`))

	if got := rest.gotQuery.Get("parent"); got != "12,30" {
		t.Errorf("parent = %q, want 12,30", got)
	}
}

func TestListTasksAcceptsParentAsNumber(t *testing.T) {
	// flexString：1件だけ指定するとき、モデルは数値で書くことがある。
	rest := &fakeREST{body: listBody}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_list_tasks", `{"parent":12}`))

	if got := rest.gotQuery.Get("parent"); got != "12" {
		t.Errorf("parent = %q, want 12", got)
	}
}

// ── write 系（手順26a。Design.md 8.5.1）─────────────────────────

func TestCreateTicketPostsToREST(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"seq":31,"title":"認証APIの実装"}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_create_ticket",
		`{"type":"task","title":"認証APIの実装","body_md":"本文","priority":"high","parent_seq":12}`))

	if rest.gotMethod != http.MethodPost {
		t.Errorf("メソッド = %q, want POST", rest.gotMethod)
	}
	if rest.gotPath != "/api/v1/projects/demo/tickets" {
		t.Errorf("叩いた REST = %q, want /api/v1/projects/demo/tickets", rest.gotPath)
	}
	if rest.gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rest.gotContentType)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &sent); err != nil {
		t.Fatalf("送った本文が JSON でない: %v（%s）", err, rest.gotBody)
	}
	// **9.3 のフィールド名で送っていること**（Design.md 8.5.1）。
	for _, k := range []string{"type", "title", "body_md", "priority", "parent_seq"} {
		if _, ok := sent[k]; !ok {
			t.Errorf("本文に %s が無い: %s", k, rest.gotBody)
		}
	}
	if sent["parent_seq"] != float64(12) {
		t.Errorf("parent_seq = %v, want 12", sent["parent_seq"])
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
	if out.Content[0].Text != rest.body {
		t.Errorf("応答をそのまま返していない（8.5）: %s", out.Content[0].Text)
	}
}

// TestCreateTicketOmitsUnsetFields は、送られなかった欄を本文に載せないことを見る。
//
// **空文字を載せると、9.3 が任意と定める欄に空を明示したことになる**（既定の
// 解釈が変わりうる）。
func TestCreateTicketOmitsUnsetFields(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"seq":31}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_create_ticket", `{"type":"task","title":"最小"}`))

	var sent map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &sent); err != nil {
		t.Fatalf("送った本文が JSON でない: %v", err)
	}
	if len(sent) != 2 {
		t.Errorf("本文の項目数 = %d, want 2（type と title だけ）: %s", len(sent), rest.gotBody)
	}
}

// TestCreateTicketResolvesAssigneeMe は me を所有者へ写すことを見る（Design.md 8.5）。
//
// **エージェントのアクターに担当は付かない**（担当を持つのは人である）。
func TestCreateTicketResolvesAssigneeMe(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"seq":31}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_create_ticket",
		`{"type":"task","title":"担当つき","assignee_id":"me"}`))

	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	if sent["assignee_id"] != "01OWNER000000000000000000" {
		t.Errorf("assignee_id = %v, want 所有者の ULID（8.5 の委譲）", sent["assignee_id"])
	}
}

func TestCreateTicketRequiresTypeAndTitle(t *testing.T) {
	h := New(&fakeREST{}, "v0")

	for _, args := range []string{`{"title":"種別が無い"}`, `{"type":"task"}`, `{"type":"task","title":"  "}`} {
		res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_create_ticket", args)))
		if res.Error == nil || res.Error.Code != codeInvalidParams {
			t.Errorf("args=%s のエラー = %+v, want %d", args, res.Error, codeInvalidParams)
		}
	}
}

func TestPostNotePostsComment(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01K2","kind":"caveat"}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_post_note",
		`{"seq":31,"body_md":"並列実行で落ちる","kind":"caveat"}`))

	if rest.gotMethod != http.MethodPost {
		t.Errorf("メソッド = %q, want POST", rest.gotMethod)
	}
	if rest.gotPath != "/api/v1/projects/demo/tickets/31/comments" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	if sent["body_md"] != "並列実行で落ちる" || sent["kind"] != "caveat" {
		t.Errorf("送った本文が違う: %s", rest.gotBody)
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
}

// TestPostNoteOmitsKindWhenUnset は kind を送らないことを見る。
//
// **既定は REST 側が持つ**（9.8 の discussion）。MCP が既定値を書くと、
// 同じ既定が2か所に生まれる（Design.md 8.1）。
func TestPostNoteOmitsKindWhenUnset(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01K2"}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_post_note", `{"seq":31,"body_md":"本文"}`))

	if strings.Contains(rest.gotBody, "kind") {
		t.Errorf("kind を送っている（既定は REST 側が持つ）: %s", rest.gotBody)
	}
}

func TestPostNoteRequiresSeqAndBody(t *testing.T) {
	h := New(&fakeREST{}, "v0")

	for _, args := range []string{`{"body_md":"seq が無い"}`, `{"seq":31}`, `{"seq":0,"body_md":"x"}`} {
		res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_post_note", args)))
		if res.Error == nil || res.Error.Code != codeInvalidParams {
			t.Errorf("args=%s のエラー = %+v, want %d", args, res.Error, codeInvalidParams)
		}
	}
}

// TestPutDocReadsVersionThenPatches は 2往復して If-Match を付けることを見る
// （Design.md 8.5.1）。
//
// **エージェントは version を持てない**（pb_get_doc は Markdown しか返さない）ので、
// MCP 層が GET で読んで載せる。
func TestPutDocReadsVersionThenPatches(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: `{"path":"rules","version":3}`},
		{status: http.StatusOK, body: `{"path":"rules","version":4}`},
	}}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_put_doc",
		`{"path":"rules","body_md":"# 規約\n新しい本文","change_reason":"命名を足した"}`))

	if rest.calls != 2 {
		t.Fatalf("REST の呼び出し = %d回, want 2（GET してから PATCH）", rest.calls)
	}
	if got := strings.Join(rest.gotMethods, ","); got != "GET,PATCH" {
		t.Errorf("メソッドの順 = %s, want GET,PATCH", got)
	}
	for _, p := range rest.gotPaths {
		if p != "/api/v1/projects/demo/docs/rules" {
			t.Errorf("叩いた REST = %q", p)
		}
	}
	// 2.8 / 5.5 の例と同じ、引用符付きの entity-tag。
	if rest.gotIfMatch != `"3"` {
		t.Errorf(`If-Match = %q, want "3"`, rest.gotIfMatch)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	if sent["body_md"] != "# 規約\n新しい本文" || sent["change_reason"] != "命名を足した" {
		t.Errorf("送った本文が違う: %s", rest.gotBody)
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
	if out.Content[0].Text != `{"path":"rules","version":4}` {
		t.Errorf("PATCH の応答をそのまま返していない: %s", out.Content[0].Text)
	}
}

// TestPutDocConflictIsToolError は 409 が isError で返ることを見る（Design.md 8.4）。
//
// **読み直してやり直すのはモデルが判断できること**であって、プロトコルの誤りではない。
func TestPutDocConflictIsToolError(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: `{"version":3}`},
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"競合しました"}}`},
	}}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_put_doc", `{"path":"rules","body_md":"本文"}`))

	if !out.IsError {
		t.Fatalf("409 が isError になっていない: %+v", out)
	}
	if !strings.Contains(out.Content[0].Text, "pb_get_doc") {
		t.Errorf("次の一手（読み直し）を案内していない: %s", out.Content[0].Text)
	}
}

// TestPutDocStopsWhenReadFails は、読めない文書には書きに行かないことを見る。
func TestPutDocStopsWhenReadFails(t *testing.T) {
	rest := &fakeREST{status: http.StatusForbidden,
		body: `{"error":{"code":"forbidden","message":"権限がありません"}}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_put_doc", `{"path":"rules","body_md":"本文"}`))

	if rest.calls != 1 {
		t.Errorf("REST の呼び出し = %d回, want 1（読めなければ書かない）", rest.calls)
	}
	if !out.IsError {
		t.Errorf("403 が isError になっていない: %+v", out)
	}
}

// TestPutDocRejectsEmptyBody は、空の本文で憲章を消せないことを見る。
//
// **10.4 の PATCH は body_md を任意とする**ので、素通しすると引数を省いた
// 呼び出しが本文を空にできてしまう。全置換のツールでは事故として重い。
func TestPutDocRejectsEmptyBody(t *testing.T) {
	rest := &fakeREST{}
	h := New(rest, "v0")

	res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
		toolCallBody("pb_put_doc", `{"path":"rules","body_md":""}`)))

	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラー = %+v, want %d", res.Error, codeInvalidParams)
	}
	if rest.calls != 0 {
		t.Errorf("REST を叩いている: %d回", rest.calls)
	}
}
