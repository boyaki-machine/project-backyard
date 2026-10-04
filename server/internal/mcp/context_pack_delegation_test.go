package mcp

import (
	"encoding/json"
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
		// 同じ文書の「共通」の見出し（pb-278）。
		{status: http.StatusOK, body: `{"body_md":"ローカルの引き継ぎファイルに書く。"}`},
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
		"ローカルの引き継ぎファイルに書く。",
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
		{status: http.StatusOK, body: `{"body_md":"呼び直されたら状況を取り直す。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text
	if got := rest.gotQuery.Get("section"); got != "共通" {
		t.Errorf("最後に読んだ見出し = %q, want 共通", got)
	}

	for _, s := range []string{
		"**役割**：評価（独立した評価者）",
		"あなたは独立した評価者である。",
		"呼び直されたら状況を取り直す。",
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

// TestRenderBudgetLines は「予算の消費」の行（12.1・9.1。pb-253）。上限のある
// 項目だけを出し、トークンを申告しなかった件数は上限の有無によらず出す。
func TestRenderBudgetLines(t *testing.T) {
	if got := renderBudgetLines(nil); got != "" {
		t.Errorf("nil = %q, want 空", got)
	}
	var none packDelegationBudget
	if got := renderBudgetLines(&none); got != "" {
		t.Errorf("上限なし・漏れなし = %q, want 空", got)
	}

	var b packDelegationBudget
	if err := json.Unmarshal([]byte(`{"limits":{"tokens":1000,"tickets":300,"due_on":"2027-02-28"},
		"tickets":12,"max_depth":2,"tokens_total":420,"tokens_this_month":100,"tokens_missing":2,"today":"2026-10-04"}`), &b); err != nil {
		t.Fatal(err)
	}
	got := renderBudgetLines(&b)
	for _, want := range []string{"トークン 420 / 1000", "起票数 12 / 300", "期日 2027-02-28（今日 2026-10-04）", "申告しなかった報告・評価 2 件"} {
		if !strings.Contains(got, want) {
			t.Errorf("got = %q, want %q を含む", got, want)
		}
	}
	for _, unwanted := range []string{"今月", "深さ", "往復"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("got = %q, 上限の無い %q を出している", got, unwanted)
		}
	}
}

// TestRenderStopConditions は止まる条件（4.2。pb-252）を箇条書きで出す。
func TestRenderStopConditions(t *testing.T) {
	if got := renderStopConditions(nil); got != "" {
		t.Errorf("空 = %q, want 空", got)
	}
	got := renderStopConditions([]string{"模試の判定が2回続けて下がったとき", "計画より2週間遅れたとき"})
	for _, want := range []string{"**止まる条件**", "- 模試の判定が2回続けて下がったとき\n", "- 計画より2週間遅れたとき\n", "pb_raise_confirmation"} {
		if !strings.Contains(got, want) {
			t.Errorf("got = %q, want %q を含む", got, want)
		}
	}
}

// TestGetContextDelegationSubSkipsCommon は、sub には「共通」の見出しを載せない
// ことを確かめる（pb-278）。sub は main の内側で動き、手元の状態は main が持つ。
func TestGetContextDelegationSubSkipsCommon(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"sub","root_seq":31,"status":"approved"}`},
		{status: http.StatusOK, body: `{"items":[]}`},
		{status: http.StatusOK, body: `{"body_md":"このチケットの完了条件だけを満たす。"}`},
		{status: http.StatusOK, body: `{"body_md":"共通の本文（載ってはいけない）"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":32}`)).Content[0].Text

	if !strings.Contains(text, "このチケットの完了条件だけを満たす。") {
		t.Errorf("sub の指示が無い:\n%s", text)
	}
	if strings.Contains(text, "共通の本文") {
		t.Errorf("sub に共通の見出しが載っている:\n%s", text)
	}
	if rest.calls != 4 {
		t.Errorf("REST の呼び出し = %d 回, want 4（共通を読まない）", rest.calls)
	}
}

// TestRenderRecentAnswers は「最近の回答」（9.3。pb-278）。main だけに載せ、答えは全文、
// 問いは先頭だけ。
func TestRenderRecentAnswers(t *testing.T) {
	var ds packDelegationStatus
	if err := json.Unmarshal([]byte(`{"role":"main","recent_answers":[
		{"reason":"hearing","question_md":"計画を立てる前に、目的を掘り下げさせてください。\n\n1. **何を良くしたいですか**","answer_md":"１について、aが近いです。\n２について、提案で良いです。","answered_at":"2026-10-04T09:42:55Z"}]}`), &ds); err != nil {
		t.Fatal(err)
	}
	got := renderRecentAnswers(ds)
	for _, want := range []string{"**最近の回答**", "- `hearing`（2026-10-04T09:42:55Z）問い：計画を立てる前に、目的を掘り下げさせてください。 1. 何を良くしたいですか\n",
		"  > １について、aが近いです。\n", "  > ２について、提案で良いです。\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("got = %q, want %q を含む", got, want)
		}
	}
	ds.Role = "evaluator"
	if got := renderRecentAnswers(ds); got != "" {
		t.Errorf("評価 = %q, want 載せない", got)
	}
}

// TestGetContextDelegationWithHandoffAndAnswers は、引き継ぎノートと最近の回答がある
// 委任でパックが組み立てられることを確かめる（pb-278）。
//
// **REST は日時をエポックミリ秒で返し、getREST が ISO8601 の文字列へ直す**（isoTimes）。
// パックの側が日時を int64 で受けていると、引き継ぎノートが1件でもある委任で
// pb_get_context が丸ごと失敗する（pb-251 の不具合）。ここでは REST と同じ数値で渡す。
func TestGetContextDelegationWithHandoffAndAnswers(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "delegated", `null`)},
		{status: http.StatusOK, body: `{"in_delegation":true,"role":"main","root_seq":31,"status":"planning",
			"latest_handoff":{"version":1,"body":{"次の一手":"評価を待つ"},"trigger":"plan_version","written_at":1791106416000},
			"recent_answers":[{"reason":"hearing","question_md":"何を良くしたいか","answer_md":"a が近い","answered_at":1791106375000}],
			"next_move":"評価を待つ"}`},
		{status: http.StatusOK, body: `{"items":[]}`},
		{status: http.StatusOK, body: `{"body_md":"計画を出す。"}`},
		{status: http.StatusOK, body: `{"body_md":"呼び直されたら状況を取り直す。"}`},
	}}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`))
	if out.IsError {
		t.Fatalf("成功のはずが isError: %s", out.Content[0].Text)
	}
	text := out.Content[0].Text
	for _, s := range []string{"**最近の回答**", "  > a が近い\n", "問い：何を良くしたいか"} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}
