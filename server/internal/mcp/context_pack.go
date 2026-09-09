// コンテキストパック（Design.md 8.5.5、Requirements.md 10.4）。手順27。
//
// **チケット1件について、着手前に押し付ける前提を1枚の Markdown にして返す。**
// エージェントは自分が何を知らないかを知らないので、検索させるのではなく
// あらかじめ詰めて渡す（10.4.1）。
//
// **合成は MCP 層で行い、REST に専用のエンドポイントを作らない**（8.5.5）。
// 8.1 の表が両方向からこの置き場を指している——MCP が持つものは「応答の整形、
// トークン予算」であり、REST が**持たない**ものは「エージェント向けの言い換え」
// である。パックはその3つそのものである。
//
// **本文・完了条件・コメントを入れない。** 10.4.2 の優先度表が構成要素に
// 挙げておらず、/pb-implement は手順1 で pb_get_task を先に呼んでいる
// （Requirements.md 10.8.6）。**同じ本文を2回運ぶと、押し付けたいものが薄まる。**
package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// contextTools は手順27 で実装する1件を返す。
//
// **read 系の最後に置く**——/pb-implement の流れが「契約を読む（pb_get_task）→
// 前提を読む（pb_get_context）」だからである（Requirements.md 10.8.6 の手順1・2）。
func contextTools() []tool {
	return []tool{
		{
			Name: "pb_get_context",
			Description: "チケット1件に着手する前の前提一式を Markdown で返す。" +
				"スコープ境界（触ってよい範囲・いけない範囲）、実行モードと Readiness、" +
				"プロジェクトの憲章、依存・関連するチケットが入る。" +
				"**実装に着手する前に必ず呼び、書かれた前提に従うこと。**" +
				"チケットの本文と完了条件は含まないので、それらは pb_get_task で読む。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq": {Type: "integer", Description: "チケット番号（プロジェクト内で一意の連番）", Minimum: intPtr(1)},
				},
				Required: []string{"seq"},
			},
			call: callGetContext,
		},
	}
}

// contextArgs は pb_get_context の引数。**pb_get_task と同じ seq である**
// （Design.md 8.5.5。Requirements.md 10.3.2 の task_id は改訂した）。
type contextArgs struct {
	Seq flexInt `json:"seq"`
}

// ── 内部呼び出しの応答から読む形 ────────────────────────────
//
// **必要な項目だけを宣言する。** 9.5.1 / 10.2 / 10.3 の応答は他にも項目を
// 持つが、パックが描くのはここに挙げたものだけである。

type packActor struct {
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

type packStatus struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type packBrief struct {
	Seq    int32      `json:"seq"`
	Title  string     `json:"title"`
	Type   string     `json:"type"`
	Status packStatus `json:"status"`
}

type packLink struct {
	Direction string    `json:"direction"`
	LinkType  string    `json:"link_type"`
	Ticket    packBrief `json:"ticket"`
}

// packTicket は 9.5.1 の応答のうちパックが使う部分。
//
// **execution_mode / readiness / readiness_note / scope は手順27 で 9.5.1 に
// 足したものである。** それまでどの API も返しておらず、パックの優先度1 の
// 項目（10.4.2）を埋める材料が無かった。
type packTicket struct {
	Seq          int32      `json:"seq"`
	Type         string     `json:"type"`
	Title        string     `json:"title"`
	Status       packStatus `json:"status"`
	Priority     *string    `json:"priority"`
	Assignee     *packActor `json:"assignee"`
	WorkingAgent *packActor `json:"working_agent"`

	ExecutionMode string          `json:"execution_mode"`
	Readiness     *string         `json:"readiness"`
	ReadinessNote *string         `json:"readiness_note"`
	Scope         json.RawMessage `json:"scope"`

	Parent   *packBrief  `json:"parent"`
	Children []packBrief `json:"children"`
	Links    []packLink  `json:"links"`
}

// packDocNode は 10.2 の目次の1件。**本文は含まない**ので、後から 10.3 で引く。
type packDocNode struct {
	Path     string        `json:"path"`
	Title    string        `json:"title"`
	Children []packDocNode `json:"children"`
}

// packDoc は本文を積んだ文書1件。
type packDoc struct {
	Path   string
	Title  string
	BodyMD string
}

func callGetContext(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in contextArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !in.Seq.set || in.Seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}
	seq := strconv.FormatInt(in.Seq.value, 10)
	base := "/projects/" + url.PathEscape(key)

	// ── チケット（9.5.1）────────────────────────────────────
	//
	// **これが読めなければパックは成り立たない**ので、失敗はそのまま
	// isError のツール結果にする（Design.md 8.4 / 8.5.5）。
	res, err := h.getREST(r, base+"/tickets/"+seq, nil)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return failed(r, res), nil
	}
	var ticket packTicket
	if err := json.Unmarshal(res.body, &ticket); err != nil {
		return toolResult{}, newError(codeInternalError, "チケットの応答を解釈できない: "+err.Error())
	}

	// ── 憲章（10.2 の目次 → 10.3 の本文）──────────────────────
	ch, rpcErr := h.fetchCharter(r, base)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}

	return textResult(renderContextPack(key, ticket, ch)), nil
}

// onboardingDocPath は憲章から外す1件のパス（Design.md 8.5.5、DbDesign.md 8.1.2）。
//
// **「エージェントの参画情報」は参画のときに一度読む手順であって、判断の
// 拠りどころではない**（Requirements.md 10.6.2）。**チケットごとのパックに毎回
// 運ぶと、押し付けたいもの（スコープ境界・規約）が薄まる**——本文と完了条件を
// 入れない理由と同じである。
//
// **完全一致で見る。** 木のどこにあっても効く規則にすると、**たまたま同じ slug を
// 付けた別の文書まで黙って落ちる。** 他の文書の下へ移されると憲章に戻るが、
// **落としたことは応答に1行出る**ので、移した人が気づける。
const onboardingDocPath = "agent-onboarding"

// charter は憲章の取り込み結果（Design.md 8.5.5）。
type charter struct {
	// docs は目次の順に本文を積んだ文書。
	docs []packDoc
	// note は憲章を丸ごと省いたときの理由（省いていなければ空）。
	note string
	// excludedOnboarding は onboardingDocPath を落としたかどうか。
	excludedOnboarding bool
}

// fetchCharter は憲章を全文で集める（Design.md 8.5.5）。
//
// **doc.view が無いときは憲章を落とし、成功として続ける。** これは切り詰めの
// 一種であり、10.4.3 の 4「切り詰めた事実を応答に明記する」がそのまま当たる。
// **403 で全体を落とすと、読めるはずのもの（スコープ境界・依存関係）まで
// 届かない**——閲覧用に絞ったトークン（Requirements.md 10.9.1 の系統B）でも
// パックは役に立つ。
//
// 返す note は、憲章を省いたときにその理由を書いた1行である（省いていなければ空）。
func (h *Handler) fetchCharter(r *http.Request, base string) (charter, *rpcError) {
	q := url.Values{}
	q.Set("outline", "1")
	res, err := h.getREST(r, base+"/docs", q)
	if err != nil {
		return charter{}, newError(codeInternalError, err.Error())
	}
	if res.status == http.StatusForbidden {
		return charter{note: "このトークンは憲章を読む権限（doc.view）を**持たない**ため、**憲章を省いた**。" +
			"プロジェクトの規約・価値観・判断の記録を参照せずに進めることになるので、" +
			"**判断に迷ったら実装せず利用者に相談すること**。"}, nil
	}
	if !res.ok() {
		return charter{}, newError(codeInternalError,
			fmt.Sprintf("憲章の目次を読めない（HTTP %d）: %s", res.status, string(res.body)))
	}

	var outline struct {
		Items []packDocNode `json:"items"`
	}
	if err := json.Unmarshal(res.body, &outline); err != nil {
		return charter{}, newError(codeInternalError, "憲章の目次を解釈できない: "+err.Error())
	}

	// **本文を引く前に落とす。** 目次の段階で外しておかないと、捨てる文書のために
	// 10.3 を1往復ぶん余計に叩くことになる。
	items, excluded := dropOnboardingDoc(outline.Items)

	ch := charter{excludedOnboarding: excluded}
	for _, node := range flattenDocTree(items) {
		body, rpcErr := h.fetchDocBody(r, base, node.Path)
		if rpcErr != nil {
			return charter{}, rpcErr
		}
		ch.docs = append(ch.docs, packDoc{Path: node.Path, Title: node.Title, BodyMD: body})
	}
	return ch, nil
}

// dropOnboardingDoc は onboardingDocPath の節点を目次から外す。
//
// **トップレベルだけを見る**（テンプレートが置く位置。上の const を参照）。
// 節点ごと外すので、**その下に置かれた文書も一緒に落ちる**——参画の細目を
// ぶら下げた人の意図に沿う。
func dropOnboardingDoc(nodes []packDocNode) ([]packDocNode, bool) {
	for i, n := range nodes {
		if n.Path != onboardingDocPath {
			continue
		}
		out := make([]packDocNode, 0, len(nodes)-1)
		out = append(out, nodes[:i]...)
		out = append(out, nodes[i+1:]...)
		return out, true
	}
	return nodes, false
}

// fetchDocBody は文書1件の本文を引く（10.3）。
func (h *Handler) fetchDocBody(r *http.Request, base, path string) (string, *rpcError) {
	res, err := h.getREST(r, base+"/docs/"+escapePath(path), nil)
	if err != nil {
		return "", newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return "", newError(codeInternalError,
			fmt.Sprintf("文書 %s を読めない（HTTP %d）: %s", path, res.status, string(res.body)))
	}
	var doc struct {
		BodyMD string `json:"body_md"`
	}
	if err := json.Unmarshal(res.body, &doc); err != nil {
		return "", newError(codeInternalError, "文書の応答を解釈できない: "+err.Error())
	}
	return doc.BodyMD, nil
}

// flattenDocTree は木を目次の順（親→子）で1本に並べる。
//
// **子も載せる。** テンプレートの4文書はいずれもトップレベルだが
// （DbDesign.md 8.1.2）、**後から足す文書は階層を持ってよい**と同節が定めて
// おり、トップレベルだけを写す実装は子が1件足された日に無言で落とす。
func flattenDocTree(nodes []packDocNode) []packDocNode {
	var out []packDocNode
	for _, n := range nodes {
		out = append(out, n)
		out = append(out, flattenDocTree(n.Children)...)
	}
	return out
}

// ── 描画 ────────────────────────────────────────────────

// 実行モードの日本語（Requirements.md 10.5.4）。
var executionModeLabels = map[string]string{
	"human_only":  "人が行う（human_only）",
	"agent_only":  "エージェントに任せてよい（agent_only）",
	"agent_draft": "エージェントが下書きし、人が仕上げる（agent_draft）",
}

// Readiness の日本語（Requirements.md 10.5.1）。
var readinessLabels = map[string]string{
	"red":    "赤（前提が足りていない）",
	"yellow": "黄（不明点が残っている）",
	"green":  "緑（着手してよい）",
}

// scopeKeyLabels は scope の既知のキーの日本語（Requirements.md 10.5.3）。
//
// **順序を持たせるため配列で持つ。** map で回すと実行のたびに並びが変わり、
// 同じチケットのパックが呼ぶたびに違う文面になる。
var scopeKeyLabels = []struct{ key, label string }{
	{"allow", "触ってよい範囲"},
	{"deny", "触ってはいけない範囲"},
	{"repositories", "リポジトリ"},
	{"external_apis", "外部API"},
}

// renderContextPack は 8.5.5 の5節を組み立てる。
func renderContextPack(projectKey string, t packTicket, ch charter) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# コンテキストパック — %s-%d「%s」\n\n", projectKey, t.Seq, t.Title)
	b.WriteString("**着手する前に、この文書の全体に目を通すこと。** " +
		"ここに書かれた前提は、チケットの指示より先に効く。\n" +
		"チケットの本文と完了条件は含まれていない（pb_get_task で読む）。\n\n")

	writeScopeSection(&b, t.Scope)
	writeExecutionSection(&b, t)
	writeCharterSection(&b, ch)
	writeRelatedSection(&b, projectKey, t)
	writeNextStepsSection(&b, t.Seq)

	return b.String()
}

// writeScopeSection は 10.4.2 の優先度1（スコープ境界と制約）。
func writeScopeSection(b *strings.Builder, raw json.RawMessage) {
	b.WriteString("## 1. スコープ境界と制約\n\n")

	fields := parseScope(raw)
	if len(fields) == 0 {
		// **空欄にせず文を出す**（Design.md 8.5.5）。空欄を「制約が無い」と
		// 読まれるのは、境界が無いことより悪い。
		b.WriteString("**このチケットにスコープ境界は設定されていない。**\n" +
			"触ってよい範囲が決まっていないということなので、" +
			"**境界の外かもしれない変更が要ると判断したら、実装せず利用者に相談すること。**\n\n")
		return
	}
	for _, f := range fields {
		fmt.Fprintf(b, "- **%s**：%s\n", f.label, f.value)
	}
	b.WriteString("\n**この境界の外にあるものへ変更が要ると判断したら、実装せず利用者に相談すること。**\n\n")
}

// scopeField は描画するスコープ境界の1行。
type scopeField struct{ label, value string }

// parseScope は scope を描画する行に開く。
//
// **未知のキーも落とさない**（9.5.2 が拒まず保存する以上、読む側が捨てると
// 書いた人の意図が届かない）。既知の4つを先に、残りをキー名の順で続ける。
func parseScope(raw json.RawMessage) []scopeField {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return nil
	}

	var out []scopeField
	seen := map[string]bool{}
	for _, kl := range scopeKeyLabels {
		v, ok := obj[kl.key]
		if !ok {
			continue
		}
		seen[kl.key] = true
		if s := renderScopeValue(v); s != "" {
			out = append(out, scopeField{label: kl.label, value: s})
		}
	}
	var rest []string
	for k := range obj {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest) // 未知のキーの出る順を実行ごとに揺らさない
	for _, k := range rest {
		if s := renderScopeValue(obj[k]); s != "" {
			out = append(out, scopeField{label: k, value: s})
		}
	}
	return out
}

// renderScopeValue は値を1行に描く。文字列の配列は読点でつなぎ、
// それ以外は JSON のまま出す（形を決めていないキーが来ても落とさない）。
func renderScopeValue(raw json.RawMessage) string {
	var items []string
	if err := json.Unmarshal(raw, &items); err == nil {
		if len(items) == 0 {
			return "（なし）"
		}
		return "`" + strings.Join(items, "`, `") + "`"
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// writeExecutionSection は 10.4.2 の優先度1 のうち実行の前提。
func writeExecutionSection(b *strings.Builder, t packTicket) {
	b.WriteString("## 2. 実行の前提\n\n")

	mode := executionModeLabels[t.ExecutionMode]
	if mode == "" {
		mode = t.ExecutionMode
	}
	fmt.Fprintf(b, "- **実行モード**：%s\n", mode)

	if t.Readiness != nil {
		label := readinessLabels[*t.Readiness]
		if label == "" {
			label = *t.Readiness
		}
		if t.ReadinessNote != nil && *t.ReadinessNote != "" {
			fmt.Fprintf(b, "- **Readiness**：%s — %s\n", label, *t.ReadinessNote)
		} else {
			fmt.Fprintf(b, "- **Readiness**：%s\n", label)
		}
	} else {
		b.WriteString("- **Readiness**：未判定\n")
	}
	fmt.Fprintf(b, "- **状態**：%s ／ **担当**：%s ／ **実行者**：%s\n",
		t.Status.Name, actorLabel(t.Assignee), actorLabel(t.WorkingAgent))

	// **モードと Readiness は、読ませるだけでは足りない。**
	// Requirements.md 10.8.6 の /pb-implement 手順1 が求める行動をここに書く
	// ——手順ファイルは利用者のリポジトリ側にあり、古い版が置かれていることが
	// ありうる。**押し付ける側がもう一度言う。**
	if t.ExecutionMode == "human_only" {
		b.WriteString("\n**このチケットは人が行うものである。実装せず、その旨を利用者へ報告して終了すること。**\n")
	}
	if t.Readiness != nil && *t.Readiness == "red" {
		b.WriteString("\n**Readiness が赤である。何が足りないかを提示し、進めてよいかを利用者に確認してから着手すること。**\n")
	}
	b.WriteString("\n")
}

// writeCharterSection は 10.4.2 の優先度2。**Phase 2 は文書がメモリの代わりである**
// （DbDesign.md 8.3 が knowledge を Phase 3 へ送っているため）。
func writeCharterSection(b *strings.Builder, ch charter) {
	b.WriteString("## 3. 憲章\n\n")
	if ch.note != "" {
		b.WriteString(ch.note + "\n\n")
		return
	}
	if len(ch.docs) == 0 {
		// **件数を数え上げない。** 参画情報を落とした後で「1件も無い」と
		// 言い切ると、実際には1件ある場合に嘘になる。
		b.WriteString("**このプロジェクトには、判断の拠りどころになる文書が1件も無い。**\n" +
			"規約・価値観・判断の記録が書かれていないということなので、" +
			"**判断が要る場面では推測せず利用者に確認すること。**\n\n")
	} else {
		b.WriteString("**プロジェクトの規約・価値観・判断の記録である。全参加者を縛る。**\n" +
			"以下は全文であり、切り詰めていない。\n\n")
	}
	// **落としたことを1行書く**（10.4.3 の 4）。実際に落ちたときだけ出す。
	if ch.excludedOnboarding {
		// **強調は文ではなく句を囲む**（DbDesign.md 8.1.2）。閉じる ** が句点に続き
		// 直後が全角文字だと、CommonMark の right-flanking にならず ** が地の文に残る。
		b.WriteString("ただし「エージェントの参画情報」（`" + onboardingDocPath + "`）は**含めていない**。" +
			"参画のときに一度読む手順であって、判断の拠りどころではないためである。" +
			"作業材料の取り方や参画の合図が要るなら `pb_get_doc(path=\"" + onboardingDocPath + "\")` で読む。\n\n")
	}
	for _, d := range ch.docs {
		fmt.Fprintf(b, "### %s（`%s`）\n\n", d.Title, d.Path)
		body := strings.TrimSpace(d.BodyMD)
		if body == "" {
			b.WriteString("（本文は空である）\n\n")
			continue
		}
		b.WriteString(shiftHeadings(body, docHeadingShift) + "\n\n")
	}
}

// docHeadingShift は、憲章の本文を埋め込むときに下げる見出しの段数。
//
// パックは `#`（表題）→ `##`（節）→ `###`（文書）の3段を使うので、
// 文書の中の見出しは4段目から始まる。
const docHeadingShift = 2

// maxHeadingLevel は ATX 見出しの上限（CommonMark）。
const maxHeadingLevel = 6

// shiftHeadings は本文中の見出しを n 段下げる。
//
// **これが無いと憲章の章がパックの節と同じ高さに並ぶ。** 実サーバで描画して
// 初めて見えた——stg の憲章は本文が `##` で始まるため、「## 3. 憲章」の次に
// 「## PB とは何か」が来て、**後続の「## 4. 依存・関連するチケット」が憲章の
// 中にあるのか外にあるのかが読めなくなっていた。** 木の形が壊れると、
// モデルは「どこまでが押し付けか」を取り違える。
//
// **本文そのものは書き換えない。** 変えるのは `#` の数だけで、上限（6段）を
// 超える分は 6 に留める——CommonMark は7段目を見出しとして認めず、
// 落とすと文が消える。
//
// **コードブロックの中は触らない。** ``` や ~~~ で囲まれた中の `#` は
// シェルのコメントであることが多く、見出しではない。
func shiftHeadings(body string, n int) string {
	lines := strings.Split(body, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		// 囲みの開始と終了。**開いた記号と同じ記号でしか閉じない**（CommonMark）。
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			switch {
			case fence == "":
				fence = marker
			case fence == marker:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		// 見出しは `#` の並びの後ろに空白が要る（CommonMark）。`#hashtag` は見出しではない。
		if level == 0 || level > maxHeadingLevel || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		shifted := min(level+n, maxHeadingLevel)
		lines[i] = strings.Repeat("#", shifted) + trimmed[level:]
	}
	return strings.Join(lines, "\n")
}

// writeRelatedSection は 10.4.2 の優先度5（依存・関連タスクの状態）。
//
// **9.5.1 の parent / children / links から作る。** 相手の状態を含んだ形で
// 返ってくるので、追加の往復が要らない（Design.md 8.5.5）。
func writeRelatedSection(b *strings.Builder, projectKey string, t packTicket) {
	b.WriteString("## 4. 依存・関連するチケット\n\n")

	empty := t.Parent == nil && len(t.Children) == 0 && len(t.Links) == 0
	if empty {
		b.WriteString("登録されていない。\n\n")
		return
	}
	if t.Parent != nil {
		fmt.Fprintf(b, "- **親**：%s\n", briefLabel(projectKey, *t.Parent))
	}
	for _, c := range t.Children {
		fmt.Fprintf(b, "- **子**：%s\n", briefLabel(projectKey, c))
	}
	for _, l := range t.Links {
		arrow := "→"
		if l.Direction == "incoming" {
			arrow = "←"
		}
		fmt.Fprintf(b, "- **%s %s**：%s\n", l.LinkType, arrow, briefLabel(projectKey, l.Ticket))
	}
	b.WriteString("\n**完了していない前提のチケットがあるなら、着手前に利用者へ伝えること。**\n\n")
}

// writeNextStepsSection は 10.4.3 の 4（深掘り用のクエリ例を明記する）。
//
// **いまは切り詰めが起きないが、入口だけは先に出す**（Design.md 8.5.5）
// ——パックに無いものを探す手段が書かれていないと、モデルは推測で埋める。
func writeNextStepsSection(b *strings.Builder, seq int32) {
	b.WriteString("## 5. 足りないときの調べ方\n\n")
	fmt.Fprintf(b, "- チケットの本文・完了条件・関連リンク：`pb_get_task(seq=%d)`\n", seq)
	fmt.Fprintf(b, "- いまどの状態へ進めるか（進めない理由も返る）：`pb_list_transitions(seq=%d)`\n", seq)
	b.WriteString("- 憲章の章をもう一度読む：`pb_get_doc(path, section)`（目次は `pb_list_docs`）\n")
	b.WriteString("- ボードの状況・自分の担当：`pb_list_tasks(assignee=\"me\")`\n")
}

// actorLabel は担当・実行者の1語。**エージェントには印を付ける**
// （GuiDesign.md 5.4 が画面で 🤖 を出しているのと同じ区別を、文でも残す）。
func actorLabel(a *packActor) string {
	if a == nil {
		return "未設定"
	}
	if a.Kind == "agent" {
		return a.DisplayName + "（エージェント）"
	}
	return a.DisplayName
}

// briefLabel は相手チケットの1行。**9.1 の完全形 ID を使う**（<key>-<seq>）。
func briefLabel(projectKey string, b packBrief) string {
	return fmt.Sprintf("%s-%d「%s」 %s", projectKey, b.Seq, b.Title, b.Status.Name)
}
