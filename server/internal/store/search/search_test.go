package search

import (
	"slices"
	"strings"
	"testing"
)

// 全角の空白でも区切り、空の語を落とし、同じ語を畳む（ApiDesign.md 9.2.1「検索の条件」）。
func TestTermsSplitsOnFullWidthSpaceAndDedupes(t *testing.T) {
	got := Terms("  認証　API  認証\tログイン ")
	want := []string{"認証", "API", "ログイン"}
	if !slices.Equal(got, want) {
		t.Errorf("Terms = %q, want %q", got, want)
	}
}

// 空白だけなら語は無い。**nil ではなく空のスライス**を返す。
func TestTermsOfBlankIsEmpty(t *testing.T) {
	got := Terms(" 　\t ")
	if got == nil || len(got) != 0 {
		t.Errorf("Terms = %#v, want 空のスライス", got)
	}
}

// % と _ とバックスラッシュは文字として扱う。**二重にエスケープしない。**
func TestPatternsEscapeLikeWildcards(t *testing.T) {
	got := Patterns([]string{`100%`, `a_b`, `c\d`, `\%`})
	want := []string{`%100\%%`, `%a\_b%`, `%c\\d%`, `%\\\%%`}
	if !slices.Equal(got, want) {
		t.Errorf("Patterns = %q, want %q", got, want)
	}
}

// 上限は文字で数える。日本語200文字（600バイト）は通り、201文字で超える。
func TestTooLongCountsRunesNotBytes(t *testing.T) {
	if TooLong(strings.Repeat("あ", MaxQueryRunes)) {
		t.Errorf("%d文字で上限を超えたと判定した（バイトで数えている）", MaxQueryRunes)
	}
	if !TooLong(strings.Repeat("あ", MaxQueryRunes+1)) {
		t.Errorf("%d文字で上限を超えたと判定しない", MaxQueryRunes+1)
	}
}

// trigram を取り出せるかは「語の文字が3つ以上続くか」と DB の LC_CTYPE で決まる。
//
// **C の DB では日本語が語の文字にならない**——show_trgm('ログイン') は空である。
// 記号は区切りになるので、pb-66 は pb と 66 に分かれて取り出せない。
func TestTrigramUsable(t *testing.T) {
	cases := []struct {
		term    string
		unicode bool
		want    bool
	}{
		{"ログイン", false, false},
		{"ログイン", true, true},
		{"認証", true, false},
		{"API", false, true},
		{"AP", false, false},
		{"pb-66", true, false},
		{"100%", false, true},
		{"率1_0", true, false},
		{"sqlcの設定", false, true},
		{"コメントで", true, true},
	}
	for _, c := range cases {
		if got := TrigramUsable(c.term, c.unicode); got != c.want {
			t.Errorf("TrigramUsable(%q, unicode=%v) = %v, want %v", c.term, c.unicode, got, c.want)
		}
	}
}

// 1つでも取り出せない語があれば、インデックスを使う形にしない。語が無いときも使わない。
func TestAllTrigramUsable(t *testing.T) {
	if !AllTrigramUsable([]string{"ケルベロス", "サーバ"}, true) {
		t.Error("どの語も3文字以上の日本語で、C.UTF-8 なのに使えないと判定した")
	}
	if AllTrigramUsable([]string{"ケルベロス", "認証"}, true) {
		t.Error("2文字の語が混じっているのに使えると判定した")
	}
	if AllTrigramUsable([]string{"ケルベロス"}, false) {
		t.Error("C の DB で日本語の語を使えると判定した")
	}
	if AllTrigramUsable(nil, true) {
		t.Error("語が無いのに使えると判定した")
	}
}

// C と POSIX だけが英数字に限られる。
func TestUnicodeCtype(t *testing.T) {
	for ctype, want := range map[string]bool{"C": false, "POSIX": false, "C.UTF-8": true, "ja_JP.UTF-8": true} {
		if got := UnicodeCtype(ctype); got != want {
			t.Errorf("UnicodeCtype(%q) = %v, want %v", ctype, got, want)
		}
	}
}
