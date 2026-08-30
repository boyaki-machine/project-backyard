package v1

import (
	"reflect"
	"strings"
	"testing"
)

// 見出しの抽出と章の切り出し（ApiDesign.md 10.2 / 10.3）の単体テスト。
//
// **CommonMark の ATX 見出しに合わせてあることを測る。** 画面の描画は markdown-it
// （CommonMark 準拠）が行うため、ここがずれると「目次にある章が本文では見出しに
// 見えない」という食い違いになる。

func TestParseHeadingsATX(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []docOutlineItem
	}{
		{
			name: "レベルは # の数",
			body: "# 一\n\n## 二\n\n### 三\n",
			want: []docOutlineItem{{"一", 1}, {"二", 2}, {"三", 3}},
		},
		{
			name: "# の直後に空白が無ければ見出しではない",
			body: "#hashtag\n\n## 命名\n",
			want: []docOutlineItem{{"命名", 2}},
		},
		{
			name: "先頭の空白は3つまで",
			body: "   ## 三つ\n\n    ## 四つ\n",
			want: []docOutlineItem{{"三つ", 2}},
		},
		{
			name: "閉じの # 列は落とす",
			body: "## 命名 ##\n",
			want: []docOutlineItem{{"命名", 2}},
		},
		{
			name: "7個以上の # は見出しではない",
			body: "####### 七つ\n\n###### 六つ\n",
			want: []docOutlineItem{{"六つ", 6}},
		},
		{
			name: "# だけの行は拾わない（?section= で指せないため）",
			body: "#\n\n## 命名\n",
			want: []docOutlineItem{{"命名", 2}},
		},
		{
			name: "Setext 見出しは拾わない",
			body: "見出しのつもり\n===\n\n## 命名\n",
			want: []docOutlineItem{{"命名", 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outlineItems(tt.body)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("outlineItems = %v, want %v", got, tt.want)
			}
		})
	}
}

// フェンスの中の # を見出しにしない。**コード例に混ざる # を目次へ出さないため**で、
// これが効かないと「シェルのコメント」や「Markdown の説明」が章として並ぶ。
func TestParseHeadingsIgnoresFences(t *testing.T) {
	body := strings.Join([]string{
		"## 命名",
		"",
		"```sh",
		"# これはシェルのコメント",
		"make test",
		"```",
		"",
		"~~~md",
		"## これは Markdown の例",
		"~~~",
		"",
		"## ブランチ",
	}, "\n")

	want := []docOutlineItem{{"命名", 2}, {"ブランチ", 2}}
	if got := outlineItems(body); !reflect.DeepEqual(got, want) {
		t.Errorf("outlineItems = %v, want %v", got, want)
	}
}

// 種類の違う印ではフェンスを閉じられない（CommonMark）。
func TestParseHeadingsFenceNeedsSameMarker(t *testing.T) {
	body := "```\n~~~\n## 中の見出し\n```\n\n## 外の見出し\n"
	want := []docOutlineItem{{"外の見出し", 2}}
	if got := outlineItems(body); !reflect.DeepEqual(got, want) {
		t.Errorf("outlineItems = %v, want %v", got, want)
	}
}

// 同名の見出しは2つ目以降に #2 を付ける（ApiDesign.md 10.2）。
func TestParseHeadingsDuplicates(t *testing.T) {
	body := "## 命名\n\n### 命名\n\n## 命名\n"
	want := []docOutlineItem{{"命名", 2}, {"命名#2", 3}, {"命名#3", 2}}
	if got := outlineItems(body); !reflect.DeepEqual(got, want) {
		t.Errorf("outlineItems = %v, want %v", got, want)
	}
}

// 見出しが1つも無い本文でも空スライスを返す（JSON の null にしない）。
func TestOutlineItemsEmpty(t *testing.T) {
	got := outlineItems("見出しのない本文。\n")
	if got == nil {
		t.Fatal("nil を返した。JSON が null になる")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// 章の切り出し（ApiDesign.md 10.3）。
//
// **見出し行から、同じか上のレベルの次の見出しの直前までを含む。**
// 下位の見出しは章の一部として付いてくる。
func TestExtractSection(t *testing.T) {
	body := strings.Join([]string{
		"本書はこのプロジェクトの規約である。",
		"",
		"## 命名",
		"",
		"- テーブルは単数形",
		"",
		"### 接頭辞",
		"",
		"- fix/ は修正",
		"",
		"## ブランチ",
		"",
		"- main へ直接コミットしない",
		"",
	}, "\n")

	tests := []struct {
		name    string
		section string
		want    string
	}{
		{
			name:    "下位の見出しを含む",
			section: "命名",
			want:    "## 命名\n\n- テーブルは単数形\n\n### 接頭辞\n\n- fix/ は修正",
		},
		{
			name:    "下位の見出しは同じレベルの次まで",
			section: "接頭辞",
			want:    "### 接頭辞\n\n- fix/ は修正",
		},
		{
			name:    "最後の章は本文の末尾まで（末尾の空行は落とす）",
			section: "ブランチ",
			want:    "## ブランチ\n\n- main へ直接コミットしない",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractSection(body, tt.section)
			if !ok {
				t.Fatalf("章 %s が見つからなかった", tt.section)
			}
			if got != tt.want {
				t.Errorf("extractSection =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestExtractSectionNotFound(t *testing.T) {
	body := "## 命名\n\n本文\n"
	if _, ok := extractSection(body, "ブランチ"); ok {
		t.Error("無い章が見つかったことになっている")
	}
	// 404 に添える available_sections（10.3）。
	want := []string{"命名"}
	if got := sectionNames(body); !reflect.DeepEqual(got, want) {
		t.Errorf("sectionNames = %v, want %v", got, want)
	}
}

// #2 で2つ目の同名見出しを指せる（10.2 / 10.3）。
func TestExtractSectionDuplicate(t *testing.T) {
	body := "## 命名\n\n一つ目\n\n## 命名\n\n二つ目\n"
	got, ok := extractSection(body, "命名#2")
	if !ok {
		t.Fatal("命名#2 が見つからない")
	}
	if want := "## 命名\n\n二つ目"; got != want {
		t.Errorf("extractSection = %q, want %q", got, want)
	}
}
