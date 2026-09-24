// Package migrations は goose のマイグレーションを埋め込む（DbDesign.md 9.1.1）。
//
// **書庫の取り込みが版を動かすために要る。** 取り込みは表を落として作り直すので、
// 書庫の版までスキーマを進め、行を入れてから最新まで進める。
//
// **子プロセスとして goose のコマンドを起こす案は採らない**——配置ごとに在処が違い
// （コンテナは /goose、ネイティブ一式は同梱の goose、開発端末は go tool）、distroless に
// 「外のコマンドを起こす」前提を作ることになる。
//
// **このディレクトリは sqlc のスキーマ源でもある**（server/sqlc.yaml）。sqlc は .sql しか
// 読まないので、この .go は無視される。**goose のコマンドも .sql しか見ない。**
package migrations

import "embed"

// FS は 0001 からの .sql を全て含む。**goose へはこれを渡す。**
//
//go:embed *.sql
var FS embed.FS
