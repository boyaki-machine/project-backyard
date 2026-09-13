package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// ── 期待値の作り方（Testing.md 3章）─────────────────────────────
//
// 本ファイルの期待値は Design.md 8.5.5 と Requirements.md 10.4.2 から取る。
// **5節の見出し・スコープ境界が未設定のときの文・doc.view が無いときの扱い**は
// いずれも設計文書が明示しているものである。

// ticketJSON は 9.5.1 の応答のうち、パックが読む項目を持つ最小の1件。
//
// **scope と execution_mode を差し替えられるようにしてある**——この2つが
// パックの優先度1（10.4.2）を作るためである。
func ticketJSON(scope, executionMode, readiness string) string {
	return `{"seq":31,"type":"task","title":"認証APIの実装",
		"status":{"key":"in_progress","name":"進行中","category":"in_progress"},
		"priority":"high",
		"assignee":{"id":"01U","kind":"user","display_name":"田中"},
		"working_agent":null,
		"body_md":"ここに本文がある",
		"dod":[{"id":"01D","body":"テストが通ること"}],
		"parent":{"seq":12,"title":"DB設計","type":"story",
		          "status":{"key":"done","name":"完了","category":"done"}},
		"children":[],
		"links":[{"direction":"outgoing","link_type":"blocks",
		          "ticket":{"seq":45,"title":"ticketテーブル定義","type":"task",
		                    "status":{"key":"todo","name":"未着手","category":"todo"}}}],
		"execution_mode":"` + executionMode + `",
		"readiness":` + readiness + `,
		"readiness_note":null,
		"scope":` + scope + `}`
}

// charterSteps は「チケット → 目次 → 本文×2」の台本を作る。
func charterSteps(ticket string) []fakeStep {
	return []fakeStep{
		{status: http.StatusOK, body: ticket},
		{status: http.StatusOK, body: `{"items":[
			{"path":"vision","title":"価値観・世界観","children":[]},
			{"path":"rules","title":"規約","children":[
				{"path":"rules/naming","title":"命名","children":[]}]}]}`},
		{status: http.StatusOK, body: `{"body_md":"## PB とは何か\n\n人とエージェントの器である。"}`},
		{status: http.StatusOK, body: `{"body_md":"## 実装の前に\n\n推測で実装しない。"}`},
		{status: http.StatusOK, body: `{"body_md":"## 命名\n\nテーブルは単数形。"}`},
	}
}

func TestGetContextComposesTicketAndCharter(t *testing.T) {
	// Design.md 8.5.5：チケット1 ＋ 目次1 ＋ 本文の数、の内部呼び出しで組む。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`))
	if out.IsError {
		t.Fatalf("成功のはずが isError: %s", out.Content[0].Text)
	}

	want := []string{
		"/api/v1/projects/demo/tickets/31",
		"/api/v1/projects/demo/docs",
		"/api/v1/projects/demo/docs/vision",
		"/api/v1/projects/demo/docs/rules",
		"/api/v1/projects/demo/docs/rules/naming",
	}
	if strings.Join(rest.gotPaths, ",") != strings.Join(want, ",") {
		t.Errorf("叩いた REST = %v, want %v", rest.gotPaths, want)
	}

	// **子の文書も載る**（DbDesign.md 8.1.2「後から足す文書は階層を持ってよい」）。
	text := out.Content[0].Text
	for _, s := range []string{
		"# コンテキストパック — demo-31「認証APIの実装」",
		"## 1. スコープ境界と制約",
		"## 2. 実行の前提",
		"## 3. 憲章",
		"## 4. 依存・関連するチケット",
		"## 5. 足りないときの調べ方",
		"### 命名（`rules/naming`）",
		"テーブルは単数形。",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextDoesNotRepeatTicketBodyOrDoD(t *testing.T) {
	// Design.md 8.5.5：10.4.2 の優先度表が構成要素に挙げていない。
	// /pb-implement は手順1 で pb_get_task を先に呼んでいる（10.8.6）。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_only", `null`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if strings.Contains(text, "ここに本文がある") {
		t.Errorf("チケット本文を重ねて運んでいる:\n%s", text)
	}
	if strings.Contains(text, "テストが通ること") {
		t.Errorf("完了条件を重ねて運んでいる:\n%s", text)
	}
}

func TestGetContextRendersScopeBoundaries(t *testing.T) {
	// Requirements.md 10.5.3 の例。**未知のキーも落とさない**（9.5.2 が
	// 拒まず保存する以上、読む側が捨てると書いた人の意図が届かない）。
	scope := `{"allow":["src/auth/**"],"deny":["migrations/**"],
	           "repositories":["my-app"],"external_apis":[],"approval":["本番デプロイ"]}`
	rest := &fakeREST{steps: charterSteps(ticketJSON(scope, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"**触ってよい範囲**：`src/auth/**`",
		"**触ってはいけない範囲**：`migrations/**`",
		"**リポジトリ**：`my-app`",
		"**外部API**：（なし）",
		"**approval**：`本番デプロイ`",
		"実装せず利用者に相談すること",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("スコープ境界に %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextSaysBoundaryIsUnset(t *testing.T) {
	// Design.md 8.5.5：**空欄にせず文を出す。** 空欄を「制約が無い」と
	// 読まれるのは、境界が無いことより悪い。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "**このチケットにスコープ境界は設定されていない。**") {
		t.Errorf("未設定であることを述べていない:\n%s", text)
	}
}

func TestGetContextStopsHumanOnlyAndRedReadiness(t *testing.T) {
	// Requirements.md 10.8.6 の /pb-implement 手順1。**押し付ける側がもう一度言う**
	// ——手順ファイルは利用者のリポジトリ側にあり、古い版が置かれていることがある。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "human_only", `"red"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "実装せず、その旨を利用者へ報告して終了すること") {
		t.Errorf("human_only で止めていない:\n%s", text)
	}
	if !strings.Contains(text, "Readiness が赤である") {
		t.Errorf("赤の Readiness に触れていない:\n%s", text)
	}
}

func TestGetContextOmitsCharterWithoutDocView(t *testing.T) {
	// Design.md 8.5.5：**403 で全体を落とすと、読めるはずのものまで届かない。**
	// 閲覧用に絞ったトークン（Requirements.md 10.9.1 系統B）でもパックは役に立つ。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{"allow":["src/**"]}`, "agent_draft", `"green"`)},
		{status: http.StatusForbidden, body: `{"error":{"code":"forbidden","message":"権限がありません"}}`},
	}}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`))
	if out.IsError {
		t.Fatalf("憲章の欠落は切り詰めであって失敗ではない: %s", out.Content[0].Text)
	}
	text := out.Content[0].Text
	if !strings.Contains(text, "doc.view") {
		t.Errorf("省いた理由を書いていない:\n%s", text)
	}
	if !strings.Contains(text, "`src/**`") {
		t.Errorf("読めるはずのスコープ境界まで落ちている:\n%s", text)
	}
	// **本文を取りに行かない**（目次が取れていないので行き先が無い）。
	if len(rest.gotPaths) != 2 {
		t.Errorf("呼び出し = %v, want チケットと目次の2本", rest.gotPaths)
	}
}

func TestGetContextFailsWhenTicketIsUnreachable(t *testing.T) {
	// Design.md 8.5.5：チケットが読めなければパックは成り立たない。
	rest := &fakeREST{status: http.StatusForbidden,
		body: `{"error":{"code":"forbidden","message":"権限がありません"}}`}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`))

	if !out.IsError {
		t.Errorf("チケットが読めないのに成功している: %s", out.Content[0].Text)
	}
}

func TestGetContextRequiresSeq(t *testing.T) {
	h := New(&fakeREST{}, "v0")

	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_get_context", `{}`)))

	if res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Errorf("エラー = %+v, want %d", res.Error, codeInvalidParams)
	}
}

func TestGetContextAcceptsSeqAsString(t *testing.T) {
	// flexInt と同じ扱い（Design.md 8.5.2 の「モデルは型を取り違える」）。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	out := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":"31"}`))

	if out.IsError {
		t.Fatalf("文字列の seq を受けていない: %s", out.Content[0].Text)
	}
	if rest.gotPaths[0] != "/api/v1/projects/demo/tickets/31" {
		t.Errorf("叩いた REST = %q", rest.gotPaths[0])
	}
}

func TestGetContextListsRelatedTickets(t *testing.T) {
	// Requirements.md 10.4.2 の優先度5。9.5.1 の parent / children / links から作る。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"**親**：demo-12「DB設計」 完了",
		"**blocks →**：demo-45「ticketテーブル定義」 未着手",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("依存・関連に %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextShowsHowToDigDeeper(t *testing.T) {
	// Requirements.md 10.4.3 の 4：深掘り用のクエリ例を応答に明記する。
	// **いまは切り詰めが起きないが、入口だけは先に出す**（Design.md 8.5.5）。
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"`pb_get_task(seq=31)`",
		"`pb_list_transitions(seq=31)`",
		"`pb_get_doc(path, section)`",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("深掘りの入口に %q が無い:\n%s", s, text)
		}
	}
}

// **憲章の見出しはパックの節より深くする**（Design.md 8.5.5 の5節構成）。
//
// 実サーバで描画して初めて見えた問題である——本文が `##` で始まると
// 「## 3. 憲章」と同じ高さに並び、**後続の節が憲章の中か外か読めなくなる。**
func TestGetContextDemotesCharterHeadings(t *testing.T) {
	rest := &fakeREST{steps: charterSteps(ticketJSON(`{}`, "agent_draft", `"green"`))}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "#### PB とは何か") {
		t.Errorf("憲章の章が4段目に落ちていない:\n%s", text)
	}
	if strings.Contains(text, "\n## PB とは何か") {
		t.Errorf("憲章の章がパックの節と同じ高さにある:\n%s", text)
	}
	// パック自身の節は動かない。
	if !strings.Contains(text, "\n## 4. 依存・関連するチケット") {
		t.Errorf("パックの節まで下がっている:\n%s", text)
	}
}

func TestShiftHeadingsLeavesCodeAndCaps(t *testing.T) {
	in := "## 見出し\n\n```sh\n# これはコメント\n```\n\n###### 6段目\n\n#見出しではない\n"
	got := shiftHeadings(in, 2)

	for _, want := range []string{
		"#### 見出し",   // 2段下がる
		"# これはコメント",  // 囲みの中は触らない
		"###### 6段目", // 上限で止まる（7段目は見出しにならない）
		"#見出しではない",   // 空白が無いものは見出しではない
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
}

func TestGetContextSaysWhenCharterIsEmpty(t *testing.T) {
	// DbDesign.md 8.1.2：テンプレート導入前に作られたプロジェクトは文書0件。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[]}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "文書が1件も無い") {
		t.Errorf("憲章が空であることを述べていない:\n%s", text)
	}
}

// ── 「エージェントの参画情報」を憲章から外す（手順28c）──────────
//
// 期待値は Design.md 8.5.5「ただし『エージェントの参画情報』だけを除く」と
// DbDesign.md 8.1.2 から取る。**参画時に一度読む手順であって、判断の
// 拠りどころではない**ためである。

func TestGetContextExcludesOnboardingDoc(t *testing.T) {
	// **節点ごと落ちる**ので、その下にぶら下げた文書も憲章に来ない。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"vision","title":"価値観・世界観","children":[]},
			{"path":"agent-onboarding","title":"エージェントの参画情報","children":[
				{"path":"agent-onboarding/creds","title":"資格情報","children":[]}]}]}`},
		{status: http.StatusOK, body: `{"body_md":"人とエージェントの器である。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	// **本文を引く前に落とす**——捨てる文書のために 10.3 を叩かない。
	want := []string{
		"/api/v1/projects/demo/tickets/31",
		"/api/v1/projects/demo/docs",
		"/api/v1/projects/demo/docs/vision",
	}
	if strings.Join(rest.gotPaths, ",") != strings.Join(want, ",") {
		t.Errorf("叩いた REST = %v, want %v", rest.gotPaths, want)
	}
	if strings.Contains(text, "### エージェントの参画情報") {
		t.Errorf("参画情報が憲章に載っている:\n%s", text)
	}
	// **落としたことを1行書く**（Requirements.md 10.4.3 の 4）。
	for _, s := range []string{
		"「エージェントの参画情報」（`agent-onboarding`）は**含めていない**",
		`pb_get_doc(path="agent-onboarding")`,
		"### 価値観・世界観（`vision`）",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextKeepsOnboardingDocWhenMoved(t *testing.T) {
	// **除外は path の完全一致で見る**（Design.md 8.5.5）。木のどこでも効く
	// 規則にすると、同じ slug を付けた別の文書まで黙って落ちる。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"rules","title":"規約","children":[
				{"path":"rules/agent-onboarding","title":"参画","children":[]}]}]}`},
		{status: http.StatusOK, body: `{"body_md":"推測で実装しない。"}`},
		{status: http.StatusOK, body: `{"body_md":"clone してから始める。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "### 参画（`rules/agent-onboarding`）") {
		t.Errorf("移された文書は憲章に載るはず:\n%s", text)
	}
	if strings.Contains(text, "は含めていない") {
		t.Errorf("落としていないのに除外の断りが出ている:\n%s", text)
	}
}

func TestGetContextSaysCharterIsEmptyWhenOnlyOnboardingExists(t *testing.T) {
	// **件数を数え上げない。** 参画情報を落とした後で「1件も無い」と言い切ると
	// 嘘になるので、「判断の拠りどころになる文書が」と限定して述べる。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"agent-onboarding","title":"エージェントの参画情報","children":[]}]}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	for _, s := range []string{
		"判断の拠りどころになる文書が1件も無い",
		"「エージェントの参画情報」（`agent-onboarding`）は**含めていない**",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

// ── 「判断の記録」は目次だけを載せる（pb-119）──────────────────
//
// 期待値は Design.md 8.5.5「判断の記録は本文を載せず、目次と引き方だけを載せる」から取る。
// **追記で一方的に増える文書**なので、全チケットに全文を運ばない。

func TestGetContextListsDecisionsAsOutline(t *testing.T) {
	// **本文を引かない**——目次は 10.2 の ?outline=1 がすでに返している。
	// **節点ごと目次だけになる**ので、その下に置いた文書も本文を引かない。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"rules","title":"規約","outline":[{"section":"実装の前に","level":2}],"children":[]},
			{"path":"decisions","title":"判断の記録","outline":[
				{"section":"技術選定","level":2},
				{"section":"2026-08 / サーバは Go とする","level":3}],"children":[
				{"path":"decisions/archive","title":"古い判断","outline":[
					{"section":"2026-07 / 最初の判断","level":3}],"children":[]}]},
			{"path":"learnings","title":"学びと知見","outline":[],"children":[]}]}`},
		{status: http.StatusOK, body: `{"body_md":"## 実装の前に\n\n推測で実装しない。"}`},
		{status: http.StatusOK, body: `{"body_md":"失敗も成功も残す。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	want := []string{
		"/api/v1/projects/demo/tickets/31",
		"/api/v1/projects/demo/docs",
		"/api/v1/projects/demo/docs/rules",
		"/api/v1/projects/demo/docs/learnings",
	}
	if strings.Join(rest.gotPaths, ",") != strings.Join(want, ",") {
		t.Errorf("叩いた REST = %v, want %v", rest.gotPaths, want)
	}
	for _, s := range []string{
		"判断の記録（`decisions`）は**目次だけ**を載せている",
		"### 判断の記録（`decisions`）",
		"- 技術選定\n  - 2026-08 / サーバは Go とする\n",
		`pb_get_doc(path="decisions", section="<見出し>")`,
		"### 古い判断（`decisions/archive`）",
		"- 2026-07 / 最初の判断\n",
		`pb_get_doc(path="decisions/archive", section="<見出し>")`,
		// 目次だけの文書の前後で、他の文書は全文のまま載る。
		"推測で実装しない。",
		"失敗も成功も残す。",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
}

func TestGetContextKeepsDecisionsDocWhenMoved(t *testing.T) {
	// **見分けは path の完全一致**（Design.md 8.5.5）。他の文書の下へ移すと全文に戻り、
	// 目次だけにした旨の1行も出ない。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"rules","title":"規約","outline":[],"children":[
				{"path":"rules/decisions","title":"判断","outline":[
					{"section":"2026-08 / ID は ULID","level":3}],"children":[]}]}]}`},
		{status: http.StatusOK, body: `{"body_md":"推測で実装しない。"}`},
		{status: http.StatusOK, body: `{"body_md":"### 2026-08 / ID は ULID\n\nアプリ側で生成する。"}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if !strings.Contains(text, "アプリ側で生成する。") {
		t.Errorf("移された判断の記録は全文で載るはず:\n%s", text)
	}
	if strings.Contains(text, "目次だけ") {
		t.Errorf("目次にしていないのに断りが出ている:\n%s", text)
	}
}

func TestGetContextSaysDecisionsHasNoHeadings(t *testing.T) {
	// **見出しが1つも無いときも本文を載せない**（Design.md 8.5.5。利用者の判断、2026-09-13）。
	// 新規プロジェクトのテンプレート本文には見出しが無い（PB #121）。
	rest := &fakeREST{steps: []fakeStep{
		{status: http.StatusOK, body: ticketJSON(`{}`, "agent_draft", `"green"`)},
		{status: http.StatusOK, body: `{"items":[
			{"path":"decisions","title":"判断の記録","outline":[],"children":[]}]}`},
	}}
	h := New(rest, "v0")

	text := callTool1(t, h, toolCallBody("pb_get_context", `{"seq":31}`)).Content[0].Text

	if len(rest.gotPaths) != 2 {
		t.Errorf("呼び出し = %v, want チケットと目次の2本（本文を引かない）", rest.gotPaths)
	}
	for _, s := range []string{
		"見出しが1つも無い",
		`pb_get_doc(path="decisions")`,
	} {
		if !strings.Contains(text, s) {
			t.Errorf("パックに %q が無い:\n%s", s, text)
		}
	}
	// **判断の記録は目次の形で載っている**ので、「1件も無い」とは言わない。
	if strings.Contains(text, "1件も無い") {
		t.Errorf("判断の記録があるのに「1件も無い」と言っている:\n%s", text)
	}
}
