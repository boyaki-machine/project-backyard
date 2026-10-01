package mcp

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
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
	`"estimate_point":5,"estimate_hours":null,"actual_hours":3.5,"start_at":1786201200000,` +
	`"due_at":1786719600000,"all_day":false,"closed_at":null,"version":3,` +
	`"created_at":1786237200000,"updated_at":1786407164000}],` +
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
		"working_agent", "parent_seq", "staged_at", "due_at", "updated_at"}
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
	// **書いた内容を返さない**（Design.md 8.5.1）。要点は TestWriteToolsReturnOnlySummary で見る。
	if out.Content[0].Text != `{"seq":31}` {
		t.Errorf("応答の要点が違う: %s", out.Content[0].Text)
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

// ── pb_add_reference（Design.md 8.5.1）────────────────

// TestAddReferencePostsReference は 9.10.2 を叩くことを見る。
//
// **表は新設していない**——ticket_reference は 0016 から repository / branch /
// commit_sha を持ち、欠けていたのは MCP の口と権限だけだった（DbDesign.md 6.12）。
func TestAddReferencePostsReference(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01R1","kind":"code"}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_add_reference",
		`{"seq":31,"repository":"project-backyard","branch":"feature/pb-68",
		  "commit_sha":"a1b2c3d","label":"MCP の口を足した"}`))

	if rest.gotMethod != http.MethodPost {
		t.Errorf("メソッド = %q, want POST", rest.gotMethod)
	}
	if rest.gotPath != "/api/v1/projects/demo/tickets/31/references" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	for k, want := range map[string]string{
		"kind": "code", "repository": "project-backyard",
		"branch": "feature/pb-68", "commit_sha": "a1b2c3d", "label": "MCP の口を足した",
	} {
		if sent[k] != want {
			t.Errorf("%s = %v, want %q（本文: %s）", k, sent[k], want, rest.gotBody)
		}
	}
	// **送っていない欄は載せない**（callCreateTicket と同じ）。空文字を載せると、
	// 9.10.2 が任意と定める欄に空を明示したことになる。
	for _, k := range []string{"url", "note"} {
		if _, ok := sent[k]; ok {
			t.Errorf("%s を送っている（渡していない）: %s", k, rest.gotBody)
		}
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
}

// TestAddReferenceDefaultsKindToCode は kind の既定が code であることを見る。
//
// **ここだけは MCP 層が既定を置く**（Design.md 8.5.1）。9.10.2 は kind を必須と
// するので、pb_post_note のように「送らない」形にすると必ず 422 になる。
// **既定を置くことが隠れた規則にならないよう、8.5.1 に書いてある。**
func TestAddReferenceDefaultsKindToCode(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01R1"}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_add_reference", `{"seq":31,"repository":"my-app"}`))

	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	if sent["kind"] != "code" {
		t.Errorf("kind = %v, want code（本文: %s）", sent["kind"], rest.gotBody)
	}
}

// TestAddReferencePassesDocKindThrough は doc を塞いでいないことを見る。
//
// **塞ぐと 8.1 の「MCP 層に独自の規則を置かない」に反する。** 既定が code なのは
// 呼ぶ動機がほぼ code だからであって、doc を禁じたからではない。
func TestAddReferencePassesDocKindThrough(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01R2"}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_add_reference",
		`{"seq":31,"kind":"doc","url":"https://example.com/design.md"}`))

	var sent map[string]any
	_ = json.Unmarshal([]byte(rest.gotBody), &sent)
	if sent["kind"] != "doc" || sent["url"] != "https://example.com/design.md" {
		t.Errorf("doc の参照が素通りしていない: %s", rest.gotBody)
	}
}

// TestAddReferenceRequiresSeq は seq だけを必須にしていることを見る。
//
// **条件付き必須（code なら repository、doc なら url）はスキーマで組まない**
// ——判定が REST と MCP の2か所に分かれる。9.10.2 の検証がそのまま返る。
func TestAddReferenceRequiresSeq(t *testing.T) {
	h := New(&fakeREST{}, "v0")

	for _, args := range []string{`{"repository":"my-app"}`, `{"seq":0,"repository":"my-app"}`} {
		res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_add_reference", args)))
		if res.Error == nil || res.Error.Code != codeInvalidParams {
			t.Errorf("args=%s のエラー = %+v, want %d", args, res.Error, codeInvalidParams)
		}
	}

	// **repository が無いことは MCP では弾かない**（REST が返す 422 を通す）。
	rest := &fakeREST{status: http.StatusUnprocessableEntity,
		body: `{"error":{"code":"validation_failed"}}`}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_add_reference", `{"seq":31}`))
	if !out.IsError {
		t.Errorf("REST の 422 が isError で返っていない: %+v", out)
	}
	if rest.calls != 1 {
		t.Errorf("REST の呼び出し = %d回, want 1（MCP で先に弾いていない）", rest.calls)
	}
}

// TestPutDocReadsVersionThenPatches は 2往復して If-Match を付けることを見る
// （Design.md 8.5.1）。
//
// **エージェントは version を持てない**（pb_get_doc は Markdown しか返さない）ので、
// MCP 層が GET で読んで載せる。
func TestCreateDocUsesRESTAndCanBeUpdated(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusCreated, body: `{"path":"project-management-note","version":1,"updated_at":"2026-09-26T00:00:00Z","body_md":"初稿"}`},
		{status: http.StatusOK, body: `{"path":"project-management-note","version":1}`},
		{status: http.StatusOK, body: `{"path":"project-management-note","version":2,"updated_at":"2026-09-26T00:01:00Z"}`},
	}}
	h := New(rest, "v0")
	out := callTool1(t, h, toolCallBody("pb_create_doc", `{"slug":"project-management-note","title":"運営ノート","body_md":"初稿"}`))
	if out.IsError || strings.Contains(out.Content[0].Text, "初稿") || !strings.Contains(out.Content[0].Text, `"path":"project-management-note"`) {
		t.Fatalf("作成結果 = %+v", out)
	}
	updated := callTool1(t, h, toolCallBody("pb_put_doc", `{"path":"project-management-note","body_md":"改稿"}`))
	if updated.IsError || rest.calls != 3 || strings.Join(rest.gotMethods, ",") != "POST,GET,PATCH" {
		t.Fatalf("作成後の更新 = %+v, REST = %v", updated, rest.gotMethods)
	}
	if rest.gotPaths[0] != "/api/v1/projects/demo/docs" || rest.gotPaths[1] != "/api/v1/projects/demo/docs/project-management-note" {
		t.Errorf("REST のパス = %v", rest.gotPaths)
	}
}

func TestCreateDocPassesThroughConflictAndForbidden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"duplicate", http.StatusConflict, "already_exists"},
		{"no doc.edit", http.StatusForbidden, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rest := &fakeREST{status: tc.status, body: `{"error":{"code":"` + tc.code + `","message":"拒否しました"}}`}
			out := callTool1(t, New(rest, "v0"), toolCallBody("pb_create_doc", `{"slug":"rules","title":"規約"}`))
			if !out.IsError || !strings.Contains(out.Content[0].Text, tc.code) || rest.calls != 1 {
				t.Fatalf("REST の拒否が戻らない: %+v, calls=%d", out, rest.calls)
			}
		})
	}
}

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

func TestUpdateTicketPassesActualPointAndVersion(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: `{"seq":31,"version":3}`},
		{status: http.StatusOK, body: `{"seq":31,"version":4}`},
	}}
	h := New(rest, "v0")
	out := callTool1(t, h, toolCallBody("pb_update_ticket", `{"seq":31,"actual_point":5,"actual_point_version":"actual-v0"}`))
	if out.IsError {
		t.Fatalf("更新に失敗: %s", out.Content[0].Text)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["actual_point"] != float64(5) || sent["actual_point_version"] != "actual-v0" {
		t.Errorf("REST へ送った値 = %v", sent)
	}
}

// ── 完了レポート系（手順26c。Design.md 8.5.4）───────────────

// TestSubmitResultPostsReport は seq を URL へ、残りを本文へ写すことを見る。
func TestSubmitResultPostsReport(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{"id":"01K5","unsatisfied_dod":[]}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_submit_result", `{
		"seq":31,"status":"completed",
		"artifacts":[{"type":"pull_request","url":"https://example.com/pr/45"}],
		"dod_results":[{"id":"01K2DOD","passed":true,"evidence":"go test → ok"}],
		"failures":[{"approach":"ライブラリZ","reason":"版が競合"}],
		"cost":{"tokens":128000,"turns":34}}`))

	if rest.gotMethod != http.MethodPost {
		t.Errorf("メソッド = %q, want POST", rest.gotMethod)
	}
	if rest.gotPath != "/api/v1/projects/demo/tickets/31/reports" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &sent); err != nil {
		t.Fatalf("送った本文が JSON でない: %v（%s）", err, rest.gotBody)
	}
	// **seq は URL へ移したので本文に残らない**（Design.md 8.5.4）。
	if _, ok := sent["seq"]; ok {
		t.Errorf("本文に seq が残っている: %s", rest.gotBody)
	}
	for _, k := range []string{"status", "artifacts", "dod_results", "failures", "cost"} {
		if _, ok := sent[k]; !ok {
			t.Errorf("本文に %s が無い: %s", k, rest.gotBody)
		}
	}
	if out.IsError {
		t.Errorf("成功のはずが isError: %s", out.Content[0].Text)
	}
	if out.Content[0].Text != rest.body {
		t.Errorf("応答をそのまま返していない（8.5）: %s", out.Content[0].Text)
	}
}

// **知らないキーも REST へ渡す**（ApiDesign.md 9.15）。
//
// MCP 層で構造体に受け直すと、9.15 が「拒まず保存する」と定めているのに
// **ここで落ちる**ことになり、同じ規則が2か所で食い違う（Design.md 8.1）。
func TestSubmitResultPassesUnknownKeys(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_submit_result",
		`{"seq":31,"status":"completed","weather":"晴れ"}`))

	var sent map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &sent); err != nil {
		t.Fatalf("送った本文が JSON でない: %v", err)
	}
	if _, ok := sent["weather"]; !ok {
		t.Errorf("知らないキーが落ちている: %s", rest.gotBody)
	}
}

// seq が無い・0 以下なら -32602（呼び出し側の不具合。Design.md 8.4）。
func TestSubmitResultRequiresSeq(t *testing.T) {
	for _, args := range []string{`{"status":"completed"}`, `{"seq":0,"status":"completed"}`} {
		h := New(&fakeREST{status: http.StatusCreated, body: `{}`}, "v0")
		res := decodeRPC(t, callMCP(t, h, agentPrincipal(),
			toolCallBody("pb_submit_result", args)))
		if res.Error == nil || res.Error.Code != codeInvalidParams {
			t.Errorf("args=%s のエラー = %+v, want %d", args, res.Error, codeInvalidParams)
		}
	}
}

// **seq を文字列で書いてきても受ける**（flexInt。Design.md 8.4）。
func TestSubmitResultAcceptsSeqAsString(t *testing.T) {
	rest := &fakeREST{status: http.StatusCreated, body: `{}`}
	h := New(rest, "v0")

	callTool1(t, h, toolCallBody("pb_submit_result", `{"seq":"31","status":"blocked"}`))

	if rest.gotPath != "/api/v1/projects/demo/tickets/31/reports" {
		t.Errorf("叩いた REST = %q", rest.gotPath)
	}
}

// **status の値域は REST 層が判定する**（8.1）。MCP は素通しする。
func TestSubmitResultLeavesStatusValidationToREST(t *testing.T) {
	rest := &fakeREST{
		status: http.StatusUnprocessableEntity,
		body:   `{"error":{"code":"validation_failed","details":[{"field":"status","code":"invalid"}]}}`,
	}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_submit_result", `{"seq":31,"status":"finished"}`))

	if !out.IsError {
		t.Errorf("422 は isError のツール結果のはず: %+v", out)
	}
	if !strings.Contains(out.Content[0].Text, "validation_failed") {
		t.Errorf("本文をそのまま添えていない: %s", out.Content[0].Text)
	}
}

// ── 遷移系の応答（Design.md 8.5.3）──────────────────────

// fullTicketJSON は 9.5.1 の応答（本文・完了条件・関連リンクつき）の1件。
const fullTicketJSON = `{"id":"01K2","seq":31,"type":"task","title":"認証APIの実装",
	"status":{"key":"in_progress","name":"進行中","category":"in_progress"},
	"priority":"high","assignee":{"id":"01U","kind":"user","display_name":"田中"},
	"working_agent":{"id":"01A","kind":"agent","display_name":"Claude Code"},
	"parent_seq":12,"version":4,"created_at":"2026-09-01T00:00:00Z",
	"updated_at":"2026-09-17T01:00:00Z","closed_at":null,
	"body_md":"ここに長い本文がある","dod":[{"id":"01D","body":"テストが通ること"}],
	"links":[],"references":[],"children":[],"comment_count":2}`

func TestTransitionTaskReturnsOnlyStatusSummary(t *testing.T) {
	rest := &fakeREST{status: http.StatusOK, body: fullTicketJSON}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_transition_task", `{"seq":31,"to":"in_progress"}`))
	if out.IsError {
		t.Fatalf("成功のはずが isError: %s", out.Content[0].Text)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.Content[0].Text), &got); err != nil {
		t.Fatalf("応答が JSON でない: %v（%s）", err, out.Content[0].Text)
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := append([]string(nil), transitionResultFields...)
	sort.Strings(want)
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("応答の項目 = %v, want %v", keys, want)
	}
	// **値は REST の JSON をそのまま写す**（null も null のまま）。
	if string(got["version"]) != "4" || string(got["closed_at"]) != "null" {
		t.Errorf("値が写っていない: %s", out.Content[0].Text)
	}
	if strings.Contains(out.Content[0].Text, "ここに長い本文がある") {
		t.Errorf("本文を返している: %s", out.Content[0].Text)
	}
}

func TestTransitionTaskKeepsFailureBody(t *testing.T) {
	// **失敗は今までどおり本文ごと返す**——理由の文が次の一手を決める。
	rest := &fakeREST{status: http.StatusForbidden,
		body: `{"error":{"code":"forbidden","message":"担当が所有者でないチケットは進められません"}}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_transition_task", `{"seq":31,"to":"in_progress"}`))

	if !out.IsError {
		t.Fatalf("失敗のはずが成功している: %s", out.Content[0].Text)
	}
	if !strings.Contains(out.Content[0].Text, "担当が所有者でないチケットは進められません") {
		t.Errorf("理由の文が落ちている: %s", out.Content[0].Text)
	}
}

// **長さの上限（422 too_long）も本文ごと返す**（ApiDesign.md 9.6 / 9.15）。
// エージェントは details の文を読んで、短くして出し直す。
func TestTooLongIsReturnedToAgent(t *testing.T) {
	cases := []struct {
		tool, args, field, message string
	}{
		{"pb_transition_task", `{"seq":31,"to":"review","comment":"x"}`,
			"comment", "コメントは20000文字以内で入力してください"},
		{"pb_submit_result", `{"seq":31,"status":"completed"}`,
			"report", "完了レポートが長すぎます（整形後20500文字、上限20000文字）。findings や failures を短くして出し直してください"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			rest := &fakeREST{status: http.StatusUnprocessableEntity,
				body: `{"error":{"code":"validation_failed","message":"入力内容に誤りがあります",` +
					`"details":[{"field":"` + c.field + `","code":"too_long","message":"` + c.message + `"}]}}`}
			out := callTool1(t, New(rest, "v0"), toolCallBody(c.tool, c.args))
			if !out.IsError {
				t.Fatalf("失敗のはずが成功している: %s", out.Content[0].Text)
			}
			if !strings.Contains(out.Content[0].Text, c.message) {
				t.Errorf("details の文が落ちている: %s", out.Content[0].Text)
			}
		})
	}
}

// ── 書いた内容を応答で返さない（Design.md 8.5.1）──────────────

// responseKeys は応答 JSON のキーを名前順に返す。
func responseKeys(t *testing.T, text string) []string {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("応答が JSON でない: %v（%s）", err, text)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestWriteToolsReturnOnlySummary(t *testing.T) {
	const docJSON = `{"id":"01DOC","path":"rules","title":"規約","body_md":"ここに長い本文がある",
		"outline":[{"section":"命名","level":2}],"version":4,"updated_at":"2026-09-17T01:00:00Z"}`
	const commentJSON = `{"id":"01C","body_md":"ここに長い本文がある","kind":"caveat","in_reply_to":null,
		"origin":"agent","author":{"id":"01A","kind":"agent","display_name":"Claude Code"},
		"created_at":"2026-09-17T01:00:00Z","updated_at":"2026-09-17T01:00:00Z"}`

	cases := []struct {
		name  string
		tool  string
		args  string
		steps []fakeStep
		want  []string
	}{
		{"起票", "pb_create_ticket", `{"type":"task","title":"認証APIの実装","body_md":"ここに長い本文がある"}`,
			[]fakeStep{{status: http.StatusCreated, body: fullTicketJSON}}, createTicketResultFields},
		{"更新", "pb_update_ticket", `{"seq":31,"body_md":"ここに長い本文がある"}`,
			[]fakeStep{{status: http.StatusOK, body: fullTicketJSON}, {status: http.StatusOK, body: fullTicketJSON}},
			updateTicketResultFields},
		{"文書", "pb_put_doc", `{"path":"rules","body_md":"ここに長い本文がある"}`,
			[]fakeStep{{status: http.StatusOK, body: docJSON}, {status: http.StatusOK, body: docJSON}}, putDocResultFields},
		{"コメント", "pb_post_note", `{"seq":31,"body_md":"ここに長い本文がある"}`,
			[]fakeStep{{status: http.StatusCreated, body: commentJSON}}, postNoteResultFields},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := New(&fakeREST{steps: c.steps}, "v0")

			out := callTool1(t, h, toolCallBody(c.tool, c.args))
			if out.IsError {
				t.Fatalf("成功のはずが isError: %s", out.Content[0].Text)
			}
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if got := responseKeys(t, out.Content[0].Text); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("応答の項目 = %v, want %v", got, want)
			}
			if strings.Contains(out.Content[0].Text, "ここに長い本文がある") {
				t.Errorf("書いた本文を返している: %s", out.Content[0].Text)
			}
		})
	}
}

func TestWriteToolsKeepFailureBody(t *testing.T) {
	// **失敗は本文ごと返す**——422 の details[] が次の一手を決める。
	rest := &fakeREST{status: http.StatusUnprocessableEntity,
		body: `{"error":{"code":"validation_failed","message":"入力に誤りがあります","details":[{"field":"title","code":"too_long"}]}}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_create_ticket", `{"type":"task","title":"長すぎる表題"}`))

	if !out.IsError || !strings.Contains(out.Content[0].Text, `"too_long"`) {
		t.Errorf("失敗の details が落ちている: %+v", out)
	}
}

// ── オンステージで絞る（Design.md 8.5.2）──────────────────

func TestListTasksPassesStagedOnlyWhenTrue(t *testing.T) {
	// **REST は true しか受けない**（ApiDesign.md 9.2.1。overdue と同じ）ので、false は送らない。
	for _, c := range []struct {
		args string
		want string
	}{
		{`{"staged":true}`, "true"},
		{`{"staged":false}`, ""},
		{`{}`, ""},
	} {
		rest := &fakeREST{body: listBody}
		h := New(rest, "v0")

		out := callTool1(t, h, toolCallBody("pb_list_tasks", c.args))

		if out.IsError {
			t.Fatalf("%s: 成功のはずが isError: %s", c.args, out.Content[0].Text)
		}
		if got := rest.gotQuery.Get("staged"); got != c.want {
			t.Errorf("%s: staged = %q, want %q", c.args, got, c.want)
		}
	}
}

// ── 着手では pb_list_transitions を省ける（Design.md 8.5.3）──────────

// TestStartConditionIsWrittenTheSameEverywhere は、ツールの説明文2つと /pb-implement の
// 手順4 が、同じ条件（着手（未着手→進行中））を書いていることを見る。
//
// **手順書だけに書くと、種別の違うエージェントには届かない**（CLAUDE.md「規約の正本は PB にある」）。
// 説明文だけに書くと、手順4 の「先に確かめる」習慣が残る。片方だけ直した状態を機械で拾う。
func TestStartConditionIsWrittenTheSameEverywhere(t *testing.T) {
	const cond = "着手（未着手→進行中）"

	for _, tl := range transitionTools() {
		if !strings.Contains(tl.Description, cond) {
			t.Errorf("%s の説明文に %q が無い", tl.Name, cond)
		}
		if !strings.Contains(tl.Description, "pb_list_transitions を") && tl.Name == "pb_transition_task" {
			t.Errorf("pb_transition_task の説明文が、省ける相手（pb_list_transitions）を名指ししていない")
		}
	}

	body, err := os.ReadFile("../agentsetup/templates/body/pb-implement.md")
	if err != nil {
		t.Fatalf("手順書の本文を読めない: %v", err)
	}
	want := cond + "では、先に `pb_list_transitions` を呼ばなくてよい。"
	if !strings.Contains(string(body), want) {
		t.Errorf("/pb-implement の本文に %q が無い", want)
	}
}

// 終日のチケットは、一覧に締切日を含む due_date を添える（Design.md 8.5.1。pb-217）。
// due_at は締切日の翌日の0時なので、それだけを見ると締切が1日後に読める。
// **基準タイムゾーンは REST の GET /projects で引く**（2手目）。
func TestListTasksAddsDueDateForAllDay(t *testing.T) {
	allDay := strings.Replace(listBody, `"all_day":false`, `"all_day":true`, 1)
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: allDay},
		{status: http.StatusOK, body: `{"key":"demo","timezone":"Asia/Tokyo"}`},
	}}
	h := New(rest, "v0")
	out := callTool1(t, h, toolCallBody("pb_list_tasks", `{}`))

	var got struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.Content[0].Text), &got); err != nil {
		t.Fatalf("一覧を読めない: %v（%s）", err, out.Content[0].Text)
	}
	if d := string(got.Items[0]["due_date"]); d != `"2026-08-14"` {
		t.Errorf("due_date = %s, want \"2026-08-14\"（due_at は 8/15 0:00 JST）", d)
	}
	if d := string(got.Items[0]["due_at"]); d != `"2026-08-14T15:00:00Z"` {
		t.Errorf("due_at = %s, want ISO8601 UTC", d)
	}
	if _, ok := got.Items[0]["all_day"]; ok {
		t.Error("all_day は出さない（11項目＋due_date）")
	}
}

// 日付の引数は基準タイムゾーンでエポックへ直し、締切は翌日の0時にする（8.5.1。pb-217）。
func TestCreateTicketConvertsDatesToEpoch(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: `{"key":"demo","timezone":"Asia/Tokyo"}`},
		{status: http.StatusCreated, body: `{"id":"01T","seq":5,"status":{"key":"todo"},"version":1,"parent_seq":null}`},
	}}
	h := New(rest, "v0")
	callTool1(t, h, toolCallBody("pb_create_ticket",
		`{"type":"task","title":"x","start_date":"2026-08-09","due_date":"2026-08-14"}`))

	var body map[string]any
	if err := json.Unmarshal([]byte(rest.gotBody), &body); err != nil {
		t.Fatalf("送った本文を読めない: %v（%s）", err, rest.gotBody)
	}
	if body["start_at"] != float64(1786201200000) || body["due_at"] != float64(1786719600000) || body["all_day"] != true {
		t.Errorf("本文 = %v, want start_at=8/9 0:00 JST, due_at=8/15 0:00 JST, all_day=true", body)
	}
	if _, ok := body["start_date"]; ok {
		t.Error("start_date を REST へそのまま送っている")
	}
}

// 終日と時刻付きを同じ呼び出しで混ぜると -32602（8.5.1）。
func TestCreateTicketRejectsMixedSchedule(t *testing.T) {
	h := New(&fakeREST{body: `{}`}, "v0")
	resp := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_create_ticket",
		`{"type":"task","title":"x","start_date":"2026-08-09","due_at":"2026-08-14T17:00:00+09:00"}`)))
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want -32602", resp.Error)
	}
}

func TestListTasksPassesKeywordQuery(t *testing.T) {
	for _, tool := range readTools() {
		if tool.Name == "pb_list_tasks" && tool.InputSchema.Properties["q"].Type != "string" {
			t.Fatal("q must be advertised as a string")
		}
	}
	for _, query := range []string{"認証 日本語", "API & 100%_", ""} {
		t.Run(query, func(t *testing.T) {
			rest := &fakeREST{body: listBody}
			h := New(rest, "v0")
			args, err := json.Marshal(map[string]any{"q": query, "staged": true, "status_category": "todo"})
			if err != nil {
				t.Fatal(err)
			}
			out := callTool1(t, h, toolCallBody("pb_list_tasks", string(args)))
			if out.IsError {
				t.Fatal(out.Content[0].Text)
			}
			if rest.gotQuery.Get("q") != query {
				t.Errorf("q = %q, want %q", rest.gotQuery.Get("q"), query)
			}
			if query == "" && rest.gotQuery.Has("q") {
				t.Error("empty q must be omitted")
			}
			if rest.gotQuery.Get("staged") != "true" || rest.gotQuery.Get("status_category") != "todo" {
				t.Errorf("other filters lost: %v", rest.gotQuery)
			}
		})
	}
}

func TestGetProjectWorkflowVersion(t *testing.T) {
	for _, tc := range []struct{ args, want string }{{`{}`, ""}, {`{"workflow_version":1}`, "1"}, {`{"workflow_version":"6"}`, "6"}} {
		rest := &fakeREST{body: `{"workflow_version":6,"warning":"再取得してください"}`}
		out := callTool1(t, New(rest, "v0"), toolCallBody("pb_get_project", tc.args))
		if out.IsError || out.Content[0].Text != rest.body {
			t.Fatalf("response lost: %+v", out)
		}
		if rest.gotQuery.Get("workflow_version") != tc.want {
			t.Errorf("query=%v, want %q", rest.gotQuery, tc.want)
		}
	}
	for _, raw := range []string{"0", "-1", "2147483648", "1.5", `"abc"`, "null"} {
		rest := &fakeREST{}
		res := decodeRPC(t, callMCP(t, New(rest, "v0"), agentPrincipal(), toolCallBody("pb_get_project", `{"workflow_version":`+raw+`}`)))
		if res.Error == nil || res.Error.Code != codeInvalidParams {
			t.Errorf("%s: error=%+v, want invalid params", raw, res.Error)
		}
		if rest.gotPath != "" {
			t.Errorf("%s reached REST", raw)
		}
	}
	for _, tool := range readTools() {
		if tool.Name == "pb_get_project" && tool.InputSchema.Properties["workflow_version"].Type != "integer" {
			t.Fatal("workflow_version must be advertised")
		}
	}
}
