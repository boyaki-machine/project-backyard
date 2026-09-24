// Command notices は、配布物に入る第三者のソフトウェアのライセンス表示
// （THIRD_PARTY_NOTICES.txt）を生成し、または古くなっていないかを確かめる。
// `make licenses` / `make licenses-check` から呼ぶ（Design.md 4.5）。
//
//	go -C server/tools run ./notices -npm <npm.json> -goose-tags "<タグ>" -o <出力> [-check]
//
// **対象は配布物に入る実行ファイルの依存である。** pb・pb-mcp-bridge・goose の3つで、
// go.mod の require ではなく `go list -deps` が返すもの（＝リンクされるもの）を拾う。
// 行き先の OS によって依存が変わりうるので、配布する3つの OS の和を取る。
// 画面（npm）の分は client/scripts/npm-licenses.mjs が出す JSON を受け取る。
//
// **出力は同じ依存からは毎回同じになる。** 時刻もツールチェーンの版も入れず、並びも固定する。
// -check はこの性質を使い、作り直した結果とファイルを比べる。
//
// 標準ライブラリだけで書く。tools モジュールに置くのは、配布する実行ファイル（cmd/）と
// 分けるためである。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// binary は配布物に入る実行ファイル1つ。dir は go -C server/tools から見たモジュールの位置。
type binary struct {
	name  string
	dir   string
	pkg   string
	goose bool // goose のビルドタグを付けるか
}

var binaries = []binary{
	{name: "pb", dir: "..", pkg: "./cmd/pb"},
	{name: "pb-mcp-bridge", dir: "..", pkg: "./cmd/pb-mcp-bridge"},
	{name: "goose", dir: ".", pkg: "github.com/pressly/goose/v3/cmd/goose", goose: true},
}

// 配布する OS（deploy/prod/build-release.sh の OS）。CPU では依存が変わらないので回さない。
var targetOSes = []string{"linux", "darwin", "windows"}

// ライセンスの本文として同梱するファイル。LICENSE.md・LICENCE・COPYING も拾う。
// PATENTS は golang.org/x と標準ライブラリが持つ特許の許諾で、LICENSE と対になる。
var noticeFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([.-].*)?$`)

const stdlibName = "Go standard library"

// goModule は Go の依存1件。同じモジュールでも版が違えば別に数える（本文が変わりうる）。
type goModule struct {
	path    string
	version string
	dir     string
	usedBy  []string
}

// npmPackage は client/scripts/npm-licenses.mjs が出す1件（Vite の build.license の JSON）。
type npmPackage struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Identifier string `json:"identifier"`
	Text       string `json:"text"`
}

func main() {
	npmPath := flag.String("npm", "", "npm-licenses.mjs が出した JSON")
	gooseTags := flag.String("goose-tags", "", "goose のビルドタグ（正本は deploy/prod/build-release.sh）")
	out := flag.String("o", "", "書き出す（-check なら比べる）THIRD_PARTY_NOTICES.txt")
	check := flag.Bool("check", false, "書き出さず、ファイルが作り直した結果と一致するかを見る")
	flag.Parse()
	if *npmPath == "" || *out == "" || *gooseTags == "" {
		fmt.Fprintln(os.Stderr, "使い方: notices -npm <npm.json> -goose-tags \"<タグ>\" -o <出力> [-check]")
		os.Exit(2)
	}

	text, err := generate(*npmPath, *gooseTags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(1)
	}

	if !*check {
		if err := os.WriteFile(*out, text, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "エラー:", err)
			os.Exit(1)
		}
		fmt.Printf("OK: %s を書いた\n", *out)
		return
	}

	current, err := os.ReadFile(*out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(1)
	}
	if bytes.Equal(current, text) {
		fmt.Printf("OK: %s は依存と一致している\n", filepath.Base(*out))
		return
	}
	fmt.Printf("NG: %s が依存と一致しない（依存を足した・外した・上げた）\n", filepath.Base(*out))
	added, removed := diffIndex(current, text)
	for _, l := range added {
		fmt.Println("    足りない:", strings.Join(strings.Fields(l), " "))
	}
	for _, l := range removed {
		fmt.Println("    余分:    ", strings.Join(strings.Fields(l), " "))
	}
	if len(added) == 0 && len(removed) == 0 {
		fmt.Println("    一覧は同じで、ライセンスの本文が違う")
	}
	fmt.Println("    直す: make licenses を打ち、THIRD_PARTY_NOTICES.txt をコミットする")
	os.Exit(1)
}

func generate(npmPath, gooseTags string) ([]byte, error) {
	mods, stdUsers, err := listGoModules(gooseTags)
	if err != nil {
		return nil, err
	}
	goroot, err := goOutput(".", nil, "env", "GOROOT")
	if err != nil {
		return nil, err
	}
	stdFiles, err := noticeFiles(strings.TrimSpace(goroot))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", stdlibName, err)
	}

	raw, err := os.ReadFile(npmPath)
	if err != nil {
		return nil, err
	}
	var pkgs []npmPackage
	if err := json.Unmarshal(raw, &pkgs); err != nil {
		return nil, fmt.Errorf("%s: %w", npmPath, err)
	}
	if len(pkgs) == 0 {
		// Vite が1件も拾えないのは、ビルドの仕方が変わったときである。空の表示を黙って書かない。
		return nil, fmt.Errorf("%s: npm のパッケージが1件も無い", npmPath)
	}
	for _, p := range pkgs {
		if strings.TrimSpace(p.Text) == "" {
			return nil, fmt.Errorf("npm の %s@%s に LICENSE のファイルが無い。本文を配布元で確かめ、扱いを決めること", p.Name, p.Version)
		}
	}
	slices.SortFunc(pkgs, func(a, b npmPackage) int {
		return strings.Compare(a.Name+"@"+a.Version, b.Name+"@"+b.Version)
	})

	var modFiles [][]file
	for _, m := range mods {
		fs, err := noticeFiles(m.dir)
		if err != nil {
			return nil, fmt.Errorf("%s@%s: %w", m.path, m.version, err)
		}
		modFiles = append(modFiles, fs)
	}

	return render(mods, modFiles, stdUsers, stdFiles, pkgs), nil
}

// listGoModules は、配布する実行ファイルにリンクされるモジュールを OS の和で返す。
// 本体（PB 自身のモジュール）は除き、標準ライブラリを使う実行ファイルは別に返す。
func listGoModules(gooseTags string) ([]*goModule, []string, error) {
	byKey := map[string]*goModule{}
	var stdUsers []string
	for _, b := range binaries {
		usesStd := false
		for _, goos := range targetOSes {
			args := []string{"list", "-deps", "-f",
				"{{if .Standard}}std{{else}}{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}{{end}}"}
			if b.goose {
				args = append(args, "-tags", gooseTags)
			}
			args = append(args, b.pkg)
			outText, err := goOutput(b.dir, []string{"GOOS=" + goos, "CGO_ENABLED=0"}, args...)
			if err != nil {
				return nil, nil, err
			}
			for _, line := range strings.Split(outText, "\n") {
				if line == "std" {
					usesStd = true
					continue
				}
				f := strings.Split(line, "\t")
				if len(f) != 3 {
					continue
				}
				if f[2] == "" {
					return nil, nil, fmt.Errorf("%s@%s がモジュールキャッシュに無い。go mod download を打つこと", f[0], f[1])
				}
				key := f[0] + "@" + f[1]
				m := byKey[key]
				if m == nil {
					m = &goModule{path: f[0], version: f[1], dir: f[2]}
					byKey[key] = m
				}
				if !slices.Contains(m.usedBy, b.name) {
					m.usedBy = append(m.usedBy, b.name)
				}
			}
		}
		if usesStd {
			stdUsers = append(stdUsers, b.name)
		}
	}
	mods := make([]*goModule, 0, len(byKey))
	for _, m := range byKey {
		mods = append(mods, m)
	}
	slices.SortFunc(mods, func(a, b *goModule) int {
		return strings.Compare(a.path+"@"+a.version, b.path+"@"+b.version)
	})
	return mods, stdUsers, nil
}

func goOutput(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go %s（%s）: %w\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return string(out), nil
}

type file struct {
	name string
	text string
}

// noticeFiles は、モジュールの直下にあるライセンス・NOTICE・PATENTS を名前の順で返す。
// **1つも無ければ失敗する。** 本文の無い表示は義務を果たさないので、黙って空で出さない。
func noticeFiles(dir string) ([]file, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !noticeFile.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file{name: e.Name(), text: strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s にライセンスのファイルが無い。本文を配布元で確かめ、扱いを決めること", dir)
	}
	return files, nil
}

const (
	heavyRule = "================================================================================"
	lightRule = "--------------------------------------------------------------------------------"
	indexMark = "■ 一覧"
)

func render(mods []*goModule, modFiles [][]file, stdUsers []string, stdFiles []file, pkgs []npmPackage) []byte {
	var b strings.Builder
	b.WriteString(`Project Backyard — 第三者のソフトウェアのライセンス表示

PB の配布物に含まれる第三者のソフトウェアについて、著作権表示とライセンスの本文を載せます。
PB 自身のライセンスは、同梱のファイル LICENSE（Apache License 2.0）に記載されています。

`)
	b.WriteString(indexMark + "\n\n")
	b.WriteString("Go（右は、そのモジュールが入っている実行ファイル）\n")
	fmt.Fprintf(&b, "  %-68s %s\n", stdlibName, strings.Join(stdUsers, ", "))
	for _, m := range mods {
		fmt.Fprintf(&b, "  %-68s %s\n", m.path+" "+m.version, strings.Join(m.usedBy, ", "))
	}
	b.WriteString("\nnpm（画面。実行ファイル pb に埋め込まれる。右は package.json が示すライセンス）\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  %-68s %s\n", p.Name+" "+p.Version, p.Identifier)
	}

	section := func(title, sub string, files []file) {
		fmt.Fprintf(&b, "\n%s\n%s\n%s\n", heavyRule, title, sub)
		for _, f := range files {
			fmt.Fprintf(&b, "%s\n%s\n\n%s\n", lightRule, f.name, f.text)
		}
	}
	section(stdlibName, "実行ファイル: "+strings.Join(stdUsers, ", "), stdFiles)
	for i, m := range mods {
		section(m.path+" "+m.version, "実行ファイル: "+strings.Join(m.usedBy, ", "), modFiles[i])
	}
	for _, p := range pkgs {
		sub := "画面（pb に埋め込み）"
		if p.Identifier != "" {
			sub += "。ライセンス: " + p.Identifier
		}
		section(p.Name+" "+p.Version, sub, []file{{name: "LICENSE", text: strings.TrimSpace(p.Text)}})
	}
	return []byte(b.String())
}

// diffIndex は、一覧の行のうち新しい側にだけある行（足りない）と古い側にだけある行（余分）を返す。
// 本文を丸ごと diff すると数千行になりうるので、何が変わったかは一覧で示す。
func diffIndex(old, cur []byte) (added, removed []string) {
	o, c := indexLines(old), indexLines(cur)
	for _, l := range c {
		if !slices.Contains(o, l) {
			added = append(added, l)
		}
	}
	for _, l := range o {
		if !slices.Contains(c, l) {
			removed = append(removed, l)
		}
	}
	return added, removed
}

func indexLines(text []byte) []string {
	var lines []string
	in := false
	for _, l := range strings.Split(string(text), "\n") {
		switch {
		case l == indexMark:
			in = true
		case l == heavyRule:
			return lines
		case in && strings.HasPrefix(l, "  "):
			lines = append(lines, l)
		}
	}
	return lines
}
