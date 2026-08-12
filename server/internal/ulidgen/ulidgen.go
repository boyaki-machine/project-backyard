// Package ulidgen は ID を生成する。
//
// ID は ULID（Crockford Base32、26文字固定）とし、DB の自動採番を使わずに
// アプリ側で生成する（DbDesign.md 4.2）。
package ulidgen

import "github.com/oklog/ulid/v2"

// New は新しい ULID を26文字の文字列で返す。
//
// ulid.Make は同一ミリ秒内で単調増加するエントロピーを用い、並行呼び出しに対して安全。
// エントロピー源の枯渇時は panic するが、これは crypto/rand の失敗に相当し復旧手段がない。
func New() string {
	return ulid.Make().String()
}
