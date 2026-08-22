// Package webui は client のビルド成果物を埋め込んで配信する（Design.md 3.4）。
//
// //go:embed は自パッケージのディレクトリ配下しか参照できないため、
// client/dist を直接は指せない。make sync-webui が dist/ へコピーする（Design.md 4.2）。
//
// dist/placeholder.html をコミットしてある。//go:embed は対象ディレクトリが
// 空だとコンパイルが通らないため、最低1つのファイルが要る。
//
// **index.html という名前にしない。** 実ビルドが同じ名前を出力するので、
// make build のたびに追跡対象が書き換わり作業ツリーが汚れる（2026-08-22 に
// 名前を分けた）。index.html が無いときは handler が placeholder.html を
// 返す（handler.go の serveIndex）。
package webui

import "embed"

// all: を付けるのは、Vite が出力しうる _ 始まりのファイルも取り込むため。
//
//go:embed all:dist
var embedded embed.FS
