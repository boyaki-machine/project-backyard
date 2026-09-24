package backup

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// loadExpr は、text で渡した値を列の型へ戻す式を返す（DbDesign.md 9.1.1）。
//
// **値は全て text で渡し、SQL の側で鋳造する。** pgx に列の型を推させると、
// citext のような拡張の型で詰まる。**text からの鋳造は、その型の入力関数を
// 通る**ので、PostgreSQL 自身が書いたものをそのまま読み戻せる。
//
// **知らない型では空を返す。** Validate がそれを見て、書き出す前に止める。
func loadExpr(c Column, n int) string {
	p := "$" + strconv.Itoa(n)
	switch {
	case c.IsArray:
		// **JSON の配列を配列にする。** `::text::text[]` は配列リテラルを要求するので通らない。
		// **CASE が要る**——無いと NULL が空の配列になる（実機で確かめた）。
		return "CASE WHEN " + p + "::text IS NULL THEN NULL ELSE ARRAY(SELECT jsonb_array_elements_text(" +
			p + "::text::jsonb)) END::" + c.Type
	case c.Type == "bytea":
		// **base64 を解く。** `::text::bytea` は base64 を解釈せず、文字列をそのまま
		// バイト列にしてしまう（実機で確かめた）。
		return "decode(" + p + "::text, 'base64')"
	case dumpExpr(c) == "":
		return ""
	default:
		return p + "::text::" + c.Type
	}
}

// insertStmt は表1つへ1行入れる文を組み立てる。
func insertStmt(t Table) string {
	cols := make([]string, 0, len(t.Columns))
	vals := make([]string, 0, len(t.Columns))
	for i, c := range t.Columns {
		cols = append(cols, quoteIdent(c.Name))
		vals = append(vals, loadExpr(c, i+1))
	}
	return "INSERT INTO " + quoteIdent(t.Name) + " (" + strings.Join(cols, ", ") +
		") VALUES (" + strings.Join(vals, ", ") + ")"
}

// loadTable は data/<表名>.jsonl の中身を表へ入れ、入れた件数を返す。
//
// **呼び出し側が外部キーを遅延させている前提である**（DbDesign.md 9.1.1 の⑥）。
// 行の順序は書庫のままで、親子を並べ替えない。
func loadTable(ctx context.Context, tx pgx.Tx, t Table, r io.Reader) (int64, error) {
	stmt := insertStmt(t)
	batch := &pgx.Batch{}
	var n int64

	sc := bufio.NewScanner(r)
	// **1行が長くなりうる。** 文書の本文やコメントが1行に入る。
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		args, err := rowArgs(t, line)
		if err != nil {
			return 0, fmt.Errorf("%s の %d 行目を読めない: %w", t.Name, n+1, err)
		}
		batch.Queue(stmt, args...)
		n++
		if batch.Len() >= batchRows {
			if err := sendBatch(ctx, tx, batch, t.Name); err != nil {
				return 0, err
			}
			batch = &pgx.Batch{}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("%s を読めない: %w", t.Name, err)
	}
	if batch.Len() > 0 {
		if err := sendBatch(ctx, tx, batch, t.Name); err != nil {
			return 0, err
		}
	}
	return n, nil
}

const (
	// batchRows は1回にまとめて送る行数。**往復の回数を減らすためだけの値**で、
	// 大きくすると組み立て中の memory が増える。
	batchRows = 500
	// maxLineBytes は1行の上限。**文書の本文が入る**ので広く取る。
	maxLineBytes = 64 * 1024 * 1024
)

func sendBatch(ctx context.Context, tx pgx.Tx, b *pgx.Batch, table string) error {
	res := tx.SendBatch(ctx, b)
	defer func() { _ = res.Close() }()
	for i := 0; i < b.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("%s へ入れられない: %w", table, err)
		}
	}
	return res.Close()
}

// rowArgs は JSON の1行を、列の順に並べた text の引数にする。
//
// **書庫に無い列は NULL で入れない。** 列の顔ぶれが違うのは版が違うということで、
// 取り込みは版を揃えてから行う（DbDesign.md 9.1.1）。**気づかずに埋めると、
// 揃っていない書庫を黙って受け入れる。**
func rowArgs(t Table, line []byte) ([]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	if len(raw) != len(t.Columns) {
		return nil, fmt.Errorf("列の数が合わない（書庫 %d / 表 %d）", len(raw), len(t.Columns))
	}
	args := make([]any, 0, len(t.Columns))
	for _, c := range t.Columns {
		v, ok := raw[c.Name]
		if !ok {
			return nil, fmt.Errorf("書庫に列 %q が無い", c.Name)
		}
		s, err := textOf(c, v)
		if err != nil {
			return nil, fmt.Errorf("列 %q: %w", c.Name, err)
		}
		args = append(args, s)
	}
	return args, nil
}

// textOf は JSON の値を、SQL へ渡す text にする。NULL は nil を返す。
func textOf(c Column, v json.RawMessage) (*string, error) {
	if string(v) == "null" {
		return nil, nil
	}
	switch {
	case c.IsArray, c.Type == "json", c.Type == "jsonb":
		// **JSON のまま渡す。** 受け側が ::jsonb で読む。
		s := string(v)
		return &s, nil
	case c.Type == "bytea":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("base64 の文字列ではない: %w", err)
		}
		if _, err := base64.StdEncoding.DecodeString(s); err != nil {
			return nil, fmt.Errorf("base64 として読めない: %w", err)
		}
		return &s, nil
	case c.Type == "boolean", isNumeric(c.Type):
		// **JSON の値をそのまま文字列にする。** true / 42 / 1.5 はどれも
		// PostgreSQL の入力関数が読める形である。
		s := string(v)
		return &s, nil
	default:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("文字列ではない: %w", err)
		}
		return &s, nil
	}
}
