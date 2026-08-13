// Package webui は client のビルド成果物を埋め込んで配信する（Design.md 3.4）。
//
// //go:embed は自パッケージのディレクトリ配下しか参照できないため、
// client/dist を直接は指せない。make sync-webui が dist/ へコピーする（Design.md 4.2）。
//
// dist/index.html はコンパイルを通すためのプレースホルダとしてコミットしてある。
// 実ビルドでは上書きされる。コミット前に戻すには make clean-webui を使う。
package webui

import "embed"

// all: を付けるのは、Vite が出力しうる _ 始まりのファイルも取り込むため。
//
//go:embed all:dist
var embedded embed.FS
