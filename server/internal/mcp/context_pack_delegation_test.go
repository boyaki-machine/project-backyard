package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// 「6. 自律実行」節（Delegation.md 9.1）の単体テスト。pb-251。

func TestGetContextAddsDelegationSectionForMainPlanning(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "delegated", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"main","root_seq":31,"status":"planning",
			"main":{"kind":"agent","display_name":"main-agent"},
			"evaluator":{"kind":"agent","display_name":"eval-agent"},
			"next_move":"目的を掘り下げる問いを立てる（ヒアリング）"}`},
		{status: http.StatusOK, body: `{"items":[
			{"path":"vision","pack_mode":"outline","title":"価値観・世界観","version":1,"outline":[{"section":"PB とは何か","level":2}],"children":[]}]}`},
		// ensureFullVision が outline を全文へ戻すために1回多く叩く。
		{status: http.StatusOK, body: `{"body_md":"## PB とは何か\n\n人とエージェントの器である。","version":1}`},
		// delegation-instructions の該当節。
		{status: http.StatusOK, body: `{"body_md":"目的・指標・予算・止まる条件を読み、計画を提出する。"}`},
	}}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`))
	if out.IsError {
		t.Fatalf("成功のはずが isError: %s", out.Content[0].Text)
	}
	text := out.Content[0].Text

	for _, s := range []string{
		"## 6. 自律実行",
		"**役割**：main（委任の根：#31／主状態：計画中）",
		"**main**：main-agent",
		"**評価**：eval-agent",
		"**次の一手**：目的を掘り下げる問いを立てる（ヒアリング）",
		"目的・指標・予算・止まる条件を読み、計画を提出する。",
		// **outline だった vision が全文で載る**（9.1 の例外）。
		"#### PB とは何か",
		"人とエージェントの器である。",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
	// **目次だけ載せる文書として断りが出ない**——全文へ戻したので目次扱いの文言は残らない。
	if strings.Contains(text, "目次だけ載せる文書") {
		t.Errorf("vision が目次扱いのまま断りを出している:\n%s", text)
	}
}

func TestGetContextAddsDelegationSectionForEvaluator(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "delegated", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"evaluator","root_seq":31,"status":"approved",
			"latest_plan":{"version":2,"status":"proposed"},
			"next_move":"対象（計画の版）を評価する"}`},
		{status: http.StatusOK, body: `{"items":[]}`},
		{status: http.StatusOK, body: `{"body_md":"あなたは独立した評価者である。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"**役割**：評価（独立した評価者）",
		"あなたは独立した評価者である。",
		"**次の一手**：対象（計画の版）を評価する",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextDelegationSectionShowsOpenConfirmation(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "delegated", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"main","root_seq":31,"status":"approved",
			"open_confirmations":[{"reason":"scope_change","question_md":"範囲を広げてよいですか"}],
			"next_move":"何もしない（確認待ちが開いている）"}`},
		{status: http.StatusOK, body: `{"items":[]}`},
		{status: http.StatusOK, body: `{"body_md":"PM の回答を待っている。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"**開いている確認待ち**",
		"`scope_change`：範囲を広げてよいですか",
		"PM の回答を待っている。",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextDelegationSectionReadonly(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "delegated", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"readonly","root_seq":31,"status":"approved"}`},
		{status: http.StatusOK, body: `{"items":[]}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "**この木は自律実行中であり、書き込めない。**") {
		t.Errorf("読むだけの案内が無い:\n%s", text)
	}
	// **指示文の節を叩かない**（readonly には9.2の文面が無い）。
	if len(rest.gotPaths) != 3 {
		t.Errorf("呼び出し = %v, want チケット・委任の状態・目次の3本", rest.gotPaths)
	}
}

func TestGetContextWithoutDelegationOmitsSection(t *testing.T) {
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if strings.Contains(text, "## 6. 自律実行") {
		t.Errorf("委任の木に属さないのに節6が出ている:\n%s", text)
	}
}
