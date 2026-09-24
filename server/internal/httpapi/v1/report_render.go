// 完了レポートを Markdown へ整形する（ApiDesign.md 9.15）。手順26c。
//
// **整形は REST 層が行う。** MCP 層に置くと、同じ整形の規則が2か所に生まれる
// （Design.md 8.1）。人が画面から提出する経路は無いが、**規則の置き場は経路の
// 数で決めない。**
//
// **これが人が完了レポートを読む面である**（GuiDesign.md 5.5）——チケット詳細の
// コメント欄は、遷移・作業中のノート・人の議論が時系列に並ぶ場所であり、完了の
// 報告もそこに並ぶのが読む順序として自然である。
//
// **空の節は出さない。** 報告しなかった項目に見出しだけが並ぶと、「報告したが
// 中身が無い」と読めてしまう。
package v1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// reportStatusLabels は見出しに出す日本語（9.15 の本文の形）。
//
// **画面の語と揃える必要は無い。** これはコメントの本文であって列ではなく、
// 出るのはこの1か所だけである。
var reportStatusLabels = map[string]string{
	"completed": "完了",
	"blocked":   "ブロック",
	"partial":   "一部完了",
}

// findingKindLabels は findings[].kind の日本語（Requirements.md 6.5 の情報類型）。
//
// **comment.kind と同じ語を当てる**（GuiDesign.md 5.5「kind の表示名」）。
// 同じ類型が場所によって違う名前で出ると、読む側が別物と受け取る。
var findingKindLabels = map[string]string{
	"discussion": "議論",
	"decision":   "決定",
	"artifact":   "成果物",
	"caveat":     "注意",
	"reference":  "参照",
}

// renderReportComment はレポートを完了レポートのコメント本文にする（9.15）。
func renderReportComment(req reportRequest, dod []gen.ListTicketDoDRow) string {
	var b strings.Builder

	label := reportStatusLabels[req.Status]
	if label == "" {
		label = req.Status
	}
	fmt.Fprintf(&b, "## 完了レポート（%s）\n", label)

	writeArtifacts(&b, req.Artifacts)
	writeDoDTable(&b, req, dod)
	writeFindings(&b, req.Findings)
	writeFailures(&b, req.Failures)
	writeProposedSubtasks(&b, req.ProposedSubtasks)
	writeFooter(&b, req)

	return strings.TrimRight(b.String(), "\n")
}

// writeArtifacts は成果物の節（10.6.1 の artifacts）。
func writeArtifacts(b *strings.Builder, raw json.RawMessage) {
	type artifact struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Path string `json:"path"`
	}
	var items []artifact
	if !decodeSection(raw, &items) {
		return
	}
	b.WriteString("\n**成果物**\n\n")
	for _, a := range items {
		where := a.URL
		if where == "" {
			where = a.Path
		}
		switch {
		case a.Type != "" && where != "":
			fmt.Fprintf(b, "- %s: %s\n", a.Type, where)
		case where != "":
			fmt.Fprintf(b, "- %s\n", where)
		default:
			fmt.Fprintf(b, "- %s\n", a.Type)
		}
	}
}

// writeDoDTable は完了条件の判定と証跡（10.6.1 の dod_results）。
//
// **チケットの完了条件を主にして並べる。** レポート側を主にすると、報告されな
// かった項目が表から消える——**「触れなかった」ことが読めなくなる**ので、
// 9.15 の unsatisfied_dod と同じ見え方にならない。
func writeDoDTable(b *strings.Builder, req reportRequest, dod []gen.ListTicketDoDRow) {
	if len(dod) == 0 {
		return
	}
	byID := make(map[string]reportDoD, len(req.DoDResults))
	for _, res := range req.DoDResults {
		byID[res.ID] = res
	}

	// **判定を完了条件のセルへ前置し、2列にする。** 判定を独立の列にすると、
	// 中身が記号1つなので列が最小幅まで縮み、**見出しの「判定」が折り返されて
	// 縦に潰れる**（詳細ペインは 750px 前後しかない。GuiDesign.md 5.5）。
	b.WriteString("\n**完了条件**\n\n")
	b.WriteString("| 完了条件 | 証跡 |\n|---|---|\n")
	for _, d := range dod {
		res, reported := byID[d.ID]
		mark := "—"
		switch {
		case reported && res.Passed != nil && *res.Passed:
			mark = "✅"
		case reported:
			mark = "❌"
		}
		note := res.Evidence
		if note == "" {
			note = res.Note
		}
		if !reported {
			note = "報告なし"
		}
		fmt.Fprintf(b, "| %s %s | %s |\n",
			mark, escapeCell(d.Body), escapeCell(note))
	}
}

// writeFindings は判明したこと（10.6.1 の findings。6.5 の情報類型に対応する）。
func writeFindings(b *strings.Builder, raw json.RawMessage) {
	type finding struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	}
	var items []finding
	if !decodeSection(raw, &items) {
		return
	}
	b.WriteString("\n**判明したこと**\n\n")
	for _, f := range items {
		if label := findingKindLabels[f.Kind]; label != "" {
			fmt.Fprintf(b, "- %s：%s\n", label, f.Body)
			continue
		}
		if f.Kind != "" {
			fmt.Fprintf(b, "- %s：%s\n", f.Kind, f.Body)
			continue
		}
		fmt.Fprintf(b, "- %s\n", f.Body)
	}
}

// writeFailures は試して駄目だったこと（10.6.1 の failures）。
//
// **10.6.2 が「失敗の記録を第一級の資産とする」と定めている。** 人間向けPMツール
// では軽視されがちだが、エージェントは同じ失敗を平然と繰り返すため効果が大きい。
func writeFailures(b *strings.Builder, raw json.RawMessage) {
	type failure struct {
		Approach string `json:"approach"`
		Reason   string `json:"reason"`
	}
	var items []failure
	if !decodeSection(raw, &items) {
		return
	}
	b.WriteString("\n**試して駄目だったこと**\n\n")
	for _, f := range items {
		if f.Reason != "" {
			fmt.Fprintf(b, "- %s — %s\n", f.Approach, f.Reason)
			continue
		}
		fmt.Fprintf(b, "- %s\n", f.Approach)
	}
}

// writeProposedSubtasks は分割の提案（10.6.1 の proposed_subtasks）。
//
// **ここに書くだけで、チケットは作らない**（9.15）。承認キュー（proposal）は
// Phase 3 であり、人が読んで要ると判断すれば pb_create_ticket を呼ばせれば済む。
// **承認なしに盤面が増える経路を作らない。**
func writeProposedSubtasks(b *strings.Builder, raw json.RawMessage) {
	type subtask struct {
		Title     string `json:"title"`
		Rationale string `json:"rationale"`
	}
	var items []subtask
	if !decodeSection(raw, &items) {
		return
	}
	b.WriteString("\n**分割の提案**\n\n")
	for _, s := range items {
		if s.Rationale != "" {
			fmt.Fprintf(b, "- %s — %s\n", s.Title, s.Rationale)
			continue
		}
		fmt.Fprintf(b, "- %s\n", s.Title)
	}
}

// writeFooter はコストと知識への影響（10.6.1 の cost / knowledge_impact）。
func writeFooter(b *strings.Builder, req reportRequest) {
	var parts []string
	if c := req.Cost; c != nil {
		var cost []string
		if c.Tokens != nil {
			cost = append(cost, fmt.Sprintf("%s トークン", withThousands(*c.Tokens)))
		}
		if c.Turns != nil {
			cost = append(cost, fmt.Sprintf("%d ターン", *c.Turns))
		}
		if c.WallClockMin != nil {
			cost = append(cost, fmt.Sprintf("%d 分", *c.WallClockMin))
		}
		if len(cost) > 0 {
			parts = append(parts, "**コスト** "+strings.Join(cost, " / "))
		}
	}
	if req.KnowledgeImpact != nil {
		parts = append(parts, "**知識への影響** "+*req.KnowledgeImpact)
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, "　") + "\n")
	}
}

// decodeSection は節の中身を読む。**読めない形は黙って捨てる。**
//
// 9.15 が「知らないキーも拒まず保存する」と定めているので、**保存は済んでいる**
// ——ここで落ちるのは描画だけである。モデルが artifacts に配列ではなく文字列を
// 入れてきたときに、提出そのものが失敗するほうが高くつく。
func decodeSection(raw json.RawMessage, dst any) bool {
	if len(raw) == 0 {
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false
	}
	// 空の配列は節ごと出さない（本節の冒頭）。
	switch v := dst.(type) {
	case *[]struct{}:
		return len(*v) > 0
	}
	return sectionHasItems(dst)
}

// sectionHasItems は json.Unmarshal 後の要素数を見る。
func sectionHasItems(dst any) bool {
	b, err := json.Marshal(dst)
	if err != nil {
		return false
	}
	var probe []json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return false
	}
	return len(probe) > 0
}

// escapeCell は Markdown の表のセルを壊さないようにする。
//
// **| と改行だけを潰す。** 本文はレポートの原文であり、余計に書き換えると
// 「エージェントが書いたもの」と食い違う。
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// withThousands は桁区切りを入れる（トークン数が6〜7桁になるため）。
func withThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	return sign + strings.Join(out, ",")
}
