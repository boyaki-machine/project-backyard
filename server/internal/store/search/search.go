// Package search はチケットのキーワード検索を隔離する（Design.md 4.6、DbDesign.md 4.5）。
//
// **日本語検索の方式を pg_trgm から pg_bigm へ替えるとき、変わるのはこの層と
// queries/search.sql とインデックス定義だけにする。** 一覧のクエリ（queries/ticket.sql
// の ListTickets）は語もパターンも知らず、ここが返したチケットの ID だけを受け取る。
//
// 規則の正本は ApiDesign.md 9.2.1「検索の条件」（pb-66）。
package search

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// MaxQueryRunes は q 全体の上限（文字数）。超えたら 422（9.2.1）。
const MaxQueryRunes = 200

// TooLong は q が上限を超えているか。**バイトではなく文字で数える**——日本語は
// 1文字3バイトなので、バイトで数えると67文字で上限に達する。
func TooLong(q string) bool {
	return utf8.RuneCountInString(q) > MaxQueryRunes
}

// Terms は q を語に分ける。**全角の空白でも区切る**（unicode.IsSpace は U+3000 を含む）。
//
// 空の語は落とし、同じ語は1つに畳む——語どうしは AND なので、重複しても意味は
// 変わらない。語が無ければ空のスライスを返す（nil にしない）。
func Terms(q string) []string {
	terms := []string{}
	for _, t := range strings.FieldsFunc(q, unicode.IsSpace) {
		if !slices.Contains(terms, t) {
			terms = append(terms, t)
		}
	}
	return terms
}

// likeEscaper は ILIKE の特殊文字を文字として扱わせる（9.2.1「`%` と `_` は文字として
// 扱う」）。ESCAPE は PostgreSQL の既定のバックスラッシュである。
//
// **strings.Replacer は1回の走査で置き換える**ので、`%` の前に足したバックスラッシュが
// もう一度置き換えられて二重になることはない。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Patterns は語を部分一致の ILIKE パターンにする。
func Patterns(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		out = append(out, "%"+likeEscaper.Replace(t)+"%")
	}
	return out
}

// TicketIDs は、すべての語を含むチケットの ID を返す（語ごとに、タイトル・本文・
// 削除されていないコメントのどれかに当たればよい）。
//
// **問い合わせを2つの形で切り替える**（DbDesign.md 4.5。pb-143）。どの語からも
// trigram を取り出せるなら SearchTicketIDsByTrigram（GIN インデックスを使う）、
// 1つでも取り出せない語があれば SearchTicketIDs（全件に ILIKE を当てる）。
// 取り出せない語でインデックスを使うと、インデックスが全件を返して遅くなるためである。
// **どちらも同じ集合を返す。**
//
// **取り出せるかは DB の LC_CTYPE で変わる**ので、毎回引く（カタログを1行読むだけ）。
//
// **terms が空なら呼ばないこと**——当たらない語が1つも無いので、全件が返る。
func TicketIDs(ctx context.Context, q gen.Querier, projectID string, terms []string) ([]string, error) {
	ctype, err := q.CurrentDatabaseCtype(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB の LC_CTYPE を読めない: %w", err)
	}
	patterns := Patterns(terms)
	if AllTrigramUsable(terms, UnicodeCtype(ctype)) {
		return q.SearchTicketIDsByTrigram(ctx, gen.SearchTicketIDsByTrigramParams{
			ProjectID: projectID, Patterns: patterns,
		})
	}
	return q.SearchTicketIDs(ctx, gen.SearchTicketIDsParams{ProjectID: projectID, Patterns: patterns})
}

// UnicodeCtype は、DB の LC_CTYPE で pg_trgm が英数字以外（日本語など）も語の文字として
// 数えるかを返す（pb-143）。**C と POSIX だけが英数字に限られる。**
func UnicodeCtype(ctype string) bool {
	return ctype != "C" && ctype != "POSIX"
}

// AllTrigramUsable は、すべての語から trigram を取り出せるかを返す。
func AllTrigramUsable(terms []string, unicodeCtype bool) bool {
	if len(terms) == 0 {
		return false
	}
	for _, t := range terms {
		if !TrigramUsable(t, unicodeCtype) {
			return false
		}
	}
	return true
}

// TrigramUsable は、語から pg_trgm が trigram を1つ以上取り出せるかを返す。
//
// **pg_trgm は語の文字（英数字）が3つ以上続く部分からしか trigram を作らない。**
// 記号や空白は区切りになるので、`pb-66` は `pb` と `66` に分かれて取り出せない。
// 語の文字は DB の LC_CTYPE で決まり、C では ASCII の英数字だけである——
// **C の DB では日本語の語からは1つも取り出せない**（show_trgm('ログイン') が空）。
func TrigramUsable(term string, unicodeCtype bool) bool {
	run := 0
	for _, r := range term {
		if isTrigramRune(r, unicodeCtype) {
			run++
			if run >= 3 {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

func isTrigramRune(r rune, unicodeCtype bool) bool {
	if r < utf8.RuneSelf {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
	}
	return unicodeCtype && (unicode.IsLetter(r) || unicode.IsDigit(r))
}
