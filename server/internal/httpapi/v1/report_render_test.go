package v1

import (
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 完了レポートの Markdown 整形（ApiDesign.md 9.15）の単体テスト。手順26c。
//
// **人がレポートを読む面がこの本文である**（GuiDesign.md 5.5）。ここで測るのは、
// 空の節を出さないこと・完了条件の表がチケット側を主にして並ぶこと
// （報告されなかった項目が消えないこと）・表のセルが壊れないことである。

// renderFor は reportRequest を組み立てて整形する。
func renderFor(t *testing.T, body string, dod []gen.ListTicketDoDRow) string {
	t.Helper()
	raw := rawOf(t, body)
	req, e := reportOf(raw)
	if e != nil {
		t.Fatalf("本文を読めない: %v", e)
	}
	return renderReportComment(req, dod)
}

func sampleReportDoD() []gen.ListTicketDoDRow {
	return []gen.ListTicketDoDRow{
		{ID: testDoDIDA, Type: dodTypeManual, Body: "ユニットテストが通ること"},
		{ID: testDoDIDB, Type: dodTypeManual, Body: "設計文書を更新すること"},
	}
}

// **空の節は出さない**（9.15）。報告しなかった項目に見出しだけが並ぶと、
// 「報告したが中身が無い」と読めてしまう。
func TestRenderReportOmitsEmptySections(t *testing.T) {
	got := renderFor(t, `{"status":"completed"}`, nil)

	if !strings.HasPrefix(got, "## 完了レポート（完了）") {
		t.Errorf("見出しが違う: %q", got)
	}
	for _, heading := range []string{
		"成果物", "完了条件", "判明したこと", "試して駄目だったこと", "分割の提案", "コスト",
	} {
		if strings.Contains(got, heading) {
			t.Errorf("空の節 %q が出ている:\n%s", heading, got)
		}
	}
}

// **完了条件の表はチケット側を主にして並ぶ**（9.15）。
//
// レポート側を主にすると、報告されなかった項目が表から消え、**「触れなかった」
// ことが読めなくなる**——unsatisfied_dod と見え方が食い違う。
func TestRenderReportDoDTableKeepsUnreported(t *testing.T) {
	got := renderFor(t, `{
		"status":"partial",
		"dod_results":[{"id":"`+testDoDIDA+`","passed":true,"evidence":"go test → ok"}]}`,
		sampleReportDoD())

	if !strings.Contains(got, "| ✅ ユニットテストが通ること | go test → ok |") {
		t.Errorf("満たした行が出ていない:\n%s", got)
	}
	// 報告しなかった行も消えない。
	if !strings.Contains(got, "| — 設計文書を更新すること | 報告なし |") {
		t.Errorf("報告しなかった行が消えている:\n%s", got)
	}
}

// passed: false は ❌ で、note が証跡欄に出る。
func TestRenderReportShowsFailedDoD(t *testing.T) {
	got := renderFor(t, `{
		"status":"partial",
		"dod_results":[{"id":"`+testDoDIDB+`","passed":false,"note":"レビュー待ち"}]}`,
		sampleReportDoD())

	if !strings.Contains(got, "| ❌ 設計文書を更新すること | レビュー待ち |") {
		t.Errorf("未達の行が出ていない:\n%s", got)
	}
}

// 成果物・判明したこと・失敗・分割の提案・コストが節として出る。
func TestRenderReportWritesAllSections(t *testing.T) {
	got := renderFor(t, `{
		"status":"completed",
		"artifacts":[{"type":"pull_request","url":"https://example.com/pr/45"}],
		"findings":[{"kind":"decision","body":"保存先を Y にした"}],
		"failures":[{"approach":"ライブラリZ","reason":"版が競合"}],
		"proposed_subtasks":[{"title":"ローテーション","rationale":"スコープ外だが必要"}],
		"cost":{"tokens":128000,"turns":34,"wall_clock_min":42},
		"knowledge_impact":"minor"}`, nil)

	for _, want := range []string{
		"- pull_request: https://example.com/pr/45",
		"- 決定：保存先を Y にした",
		"- ライブラリZ — 版が競合",
		"- ローテーション — スコープ外だが必要",
		"**コスト** 128,000 トークン / 34 ターン / 42 分",
		"**知識への影響** minor",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が出ていない:\n%s", want, got)
		}
	}
}

// **表のセルを壊さない**（9.15）。本文に | や改行が入っても表が崩れない。
func TestRenderReportEscapesTableCells(t *testing.T) {
	dod := []gen.ListTicketDoDRow{
		{ID: testDoDIDA, Type: dodTypeManual, Body: "a | b"},
	}
	got := renderFor(t, `{
		"status":"completed",
		"dod_results":[{"id":"`+testDoDIDA+`","passed":true,"evidence":"1行目\n2行目"}]}`, dod)

	if !strings.Contains(got, `| ✅ a \| b | 1行目 2行目 |`) {
		t.Errorf("セルが壊れている:\n%s", got)
	}
}

// **読めない形の節は描かない**（9.15）。保存は済んでいるので、落ちるのは描画だけ。
func TestRenderReportSkipsMalformedSections(t *testing.T) {
	got := renderFor(t, `{"status":"completed","artifacts":"pr を出した"}`, nil)

	if strings.Contains(got, "成果物") {
		t.Errorf("読めない節を描いている:\n%s", got)
	}
}
