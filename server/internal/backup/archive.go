// Package backup は PB 全体の書き出しと取り込みを行う（DbDesign.md 9.1.1）。pb-147。
//
// **書庫は tar.gz である。** zip は末尾の索引を読まないと中身を取り出せず、取り込みで
// 全体をメモリか一時ファイルに載せることになる。**K8s の Pod は readOnlyRootFilesystem
// で動く**ので一時ファイルを書けない。tar は先頭から順に流せる。
//
// **アプリのデータの読み書きは sqlc を通すが、ここは通さない。** 表と列はその時点の
// カタログから引くもので、生成時に決まらないためである。
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// FormatVersion は書庫の形式の版（DbDesign.md 9.1.1）。
//
// **形式を変えたら上げる。** 読めない版を黙って読もうとしないための番号であり、
// 上げ忘れると「後から形式を変えると既に取ったバックアップを戻せなくなる」が
// 静かに起きる。
const FormatVersion = 1

// MetaName と DataDir は書庫の中の位置。
const (
	MetaName = "meta.json"
	DataDir  = "data"
)

// GooseTable はマイグレーションの適用履歴。**書庫の data/ には入れない**
// （DbDesign.md 9.1.1）。番号は Meta.MigrationVersion が持つ。
const GooseTable = "goose_db_version"

// Meta は書庫の meta.json。
type Meta struct {
	FormatVersion int `json:"format_version"`
	// MigrationVersion は書き出した時点で適用済みだった最大の番号。
	MigrationVersion int64     `json:"migration_version"`
	CreatedAt        time.Time `json:"created_at"`
	PBVersion        string    `json:"pb_version"`
	// Tables は表ごとの件数。**取り込んだあとの突き合わせに使う**（ApiDesign.md 11.12）。
	Tables []TableCount `json:"tables"`
}

// TableCount は表1つの件数。
type TableCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// Count は表名から件数を引く。**無い表は 0 ではなく、見つからないことを返す。**
func (m Meta) Count(name string) (int64, bool) {
	for _, t := range m.Tables {
		if t.Name == name {
			return t.Rows, true
		}
	}
	return 0, false
}

// FileName は書き出しのファイル名（ApiDesign.md 11.11）。
//
// **時刻は UTC である。** 画面の日時は利用者のタイムゾーンで出すが、**ファイル名は
// 端末をまたいで並ぶ**ので、並べたときに時系列になる UTC で固定する。
func FileName(at time.Time) string {
	return "pb-backup-" + at.UTC().Format("20060102-150405") + ".tar.gz"
}

// Writer は書庫を書く。**流しながら書く**ので、応答全体をメモリに載せない。
type Writer struct {
	gz *gzip.Writer
	tw *tar.Writer
}

// NewWriter は w へ書く Writer を返す。使い終わったら Close を呼ぶこと。
func NewWriter(w io.Writer) *Writer {
	gz := gzip.NewWriter(w)
	return &Writer{gz: gz, tw: tar.NewWriter(gz)}
}

// WriteMeta は meta.json を書く。**data/ より先に書く**——取り込む側が、
// 行を読み始める前に版を確かめられるようにするためである。
func (w *Writer) WriteMeta(m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("meta.json を組み立てられない: %w", err)
	}
	b = append(b, '\n')
	if err := w.tw.WriteHeader(&tar.Header{
		Name: MetaName, Mode: 0o644, Size: int64(len(b)), ModTime: m.CreatedAt,
	}); err != nil {
		return fmt.Errorf("meta.json の見出しを書けない: %w", err)
	}
	_, err = w.tw.Write(b)
	return err
}

// WriteTable は表1つを data/<表名>.jsonl として書く。
//
// **tar は見出しに大きさを要る**ので、行を流しながらは書けない。表1つぶんを
// 組み立ててから渡す。**書庫全体を抱えないので、いちばん大きい表のぶんで足りる。**
func (w *Writer) WriteTable(table string, body []byte, modTime time.Time) error {
	if err := w.tw.WriteHeader(&tar.Header{
		Name: DataDir + "/" + table + ".jsonl", Mode: 0o644,
		Size: int64(len(body)), ModTime: modTime,
	}); err != nil {
		return fmt.Errorf("%s の見出しを書けない: %w", table, err)
	}
	_, err := w.tw.Write(body)
	return err
}

// Close は書庫を閉じる。
func (w *Writer) Close() error {
	if err := w.tw.Close(); err != nil {
		return err
	}
	return w.gz.Close()
}

// Reader は書庫を読む。**先頭から順に流す。** 戻って読み直せない。
type Reader struct {
	gz *gzip.Reader
	tr *tar.Reader
}

// NewReader は r から読む Reader を返す。
func NewReader(r io.Reader) (*Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("書庫を展開できない（tar.gz ではない）: %w", err)
	}
	return &Reader{gz: gz, tr: tar.NewReader(gz)}, nil
}

// ReadMeta は先頭の meta.json を読む。**書庫の1件目でなければ失敗する**——
// 行を読み始めてから版が違うと分かるのでは遅い。
func (r *Reader) ReadMeta() (Meta, error) {
	h, err := r.tr.Next()
	if err != nil {
		return Meta{}, fmt.Errorf("書庫が空である: %w", err)
	}
	if path.Clean(h.Name) != MetaName {
		return Meta{}, fmt.Errorf("書庫の1件目が %s ではない（%s）", MetaName, h.Name)
	}
	var m Meta
	if err := json.NewDecoder(r.tr).Decode(&m); err != nil {
		return Meta{}, fmt.Errorf("%s を読めない: %w", MetaName, err)
	}
	return m, nil
}

// NextTable は次の表へ進み、表名とその中身を読む io.Reader を返す。
// **書庫の終わりでは io.EOF を返す。**
//
// 返した io.Reader は、次に NextTable を呼ぶまでのあいだだけ有効である。
func (r *Reader) NextTable() (string, io.Reader, error) {
	for {
		h, err := r.tr.Next()
		if err != nil {
			return "", nil, err // io.EOF を含む
		}
		name := path.Clean(h.Name)
		// **ディレクトリの見出しは読み飛ばす。** tar を作る側によっては入る。
		if h.Typeflag == tar.TypeDir {
			continue
		}
		table, ok := tableNameOf(name)
		if !ok {
			return "", nil, fmt.Errorf("書庫に知らないファイルがある: %s", h.Name)
		}
		return table, r.tr, nil
	}
}

// Close は書庫を閉じる。
func (r *Reader) Close() error { return r.gz.Close() }

// tableNameOf は data/<表名>.jsonl から表名を取り出す。
//
// **表名を検査する。** 書庫の中のパスがそのまま SQL の識別子になるので、
// ここで弾けないものを後段へ渡さない（識別子は quoteIdent でも包むが、
// 二重に守る）。
func tableNameOf(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, DataDir+"/")
	if !ok {
		return "", false
	}
	table, ok := strings.CutSuffix(rest, ".jsonl")
	if !ok || !validIdent(table) {
		return "", false
	}
	return table, true
}

// validIdent は PostgreSQL の表名として受け入れる形かを見る。
//
// **PB が作る表は小文字・数字・下線だけである**（DbDesign.md 4.7）。
// それ以外を受けないことで、書庫の中身から識別子を組み立てる危うさを狭める。
func validIdent(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
