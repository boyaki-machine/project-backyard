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
