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

// TicketIDs は、すべてのパターンを含むチケットの ID を返す（語ごとに、タイトル・本文・
// 削除されていないコメントのどれかに当たればよい）。
//
// **patterns が空なら呼ばないこと**——当たらない語が1つも無いので、全件が返る。
func TicketIDs(ctx context.Context, q gen.Querier, projectID string, patterns []string) ([]string, error) {
	return q.SearchTicketIDs(ctx, gen.SearchTicketIDsParams{ProjectID: projectID, Patterns: patterns})
}
