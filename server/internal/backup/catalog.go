package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Column は表の列1つ。
type Column struct {
	Name string
	// Type は宣言された型（format_type の出力。例 "character(26)" "text[]"）。
	// **取り込みで値を鋳造する先**である。
	Type string
	// BaseType は配列なら要素の型、そうでなければ Type と同じ。
	// **配列かどうかの判定にだけ使う。**
	IsArray bool
}

// Table は書き出す表1つ。
type Table struct {
	Name    string
	Columns []Column
}

// ListTables は public スキーマの表を、名前の昇順で返す。
//
// **goose_db_version を除く**（DbDesign.md 9.1.1）。適用履歴は行として運ばず、
// meta.json の番号が持つ。
//
// **区画（パーティション）の子も除く。** 親へ入れれば子へ振り分けられる。
func ListTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
		       (t.typcategory = 'A')
		  FROM pg_class c
		  JOIN pg_attribute a ON a.attrelid = c.oid
		  JOIN pg_type t ON t.oid = a.atttypid
		 WHERE c.relnamespace = 'public'::regnamespace
		   AND c.relkind IN ('r', 'p')
		   AND NOT c.relispartition
		   AND c.relname <> $1
		   AND a.attnum > 0
		   AND NOT a.attisdropped
		 ORDER BY c.relname, a.attnum`, GooseTable)
	if err != nil {
		return nil, fmt.Errorf("表と列の一覧を読めない: %w", err)
	}
	defer rows.Close()

	var (
		tables []Table
		cur    *Table
	)
	for rows.Next() {
		var relname string
		var col Column
		if err := rows.Scan(&relname, &col.Name, &col.Type, &col.IsArray); err != nil {
			return nil, fmt.Errorf("表と列の一覧を読めない: %w", err)
		}
		if !validIdent(relname) {
			return nil, fmt.Errorf("表名が想定の形でない: %q", relname)
		}
		if !validIdent(col.Name) {
			return nil, fmt.Errorf("列名が想定の形でない: %s.%q", relname, col.Name)
		}
		if cur == nil || cur.Name != relname {
			tables = append(tables, Table{Name: relname})
			cur = &tables[len(tables)-1]
		}
		cur.Columns = append(cur.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("表と列の一覧を読めない: %w", err)
	}
	return tables, nil
}

// ForeignKeys は public スキーマの外部キー制約を、表ごとに返す。
//
// **取り込みのあいだ遅延させるために引く**（DbDesign.md 9.1.1 の⑥）。
// 外部キーは行ごとにその場で検査されるので、自己参照を持つ表では
// 子を親より先に入れた時点で落ちる。
func ForeignKeys(ctx context.Context, tx pgx.Tx) ([]Constraint, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.relname, con.conname, con.condeferrable
		  FROM pg_constraint con
		  JOIN pg_class c ON c.oid = con.conrelid
		 WHERE con.contype = 'f'
		   AND c.relnamespace = 'public'::regnamespace
		 ORDER BY c.relname, con.conname`)
	if err != nil {
		return nil, fmt.Errorf("外部キーの一覧を読めない: %w", err)
	}
	defer rows.Close()

	var out []Constraint
	for rows.Next() {
		var k Constraint
		if err := rows.Scan(&k.Table, &k.Name, &k.Deferrable); err != nil {
			return nil, fmt.Errorf("外部キーの一覧を読めない: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Constraint は外部キー制約1つ。
type Constraint struct {
	Table string
	Name  string
	// Deferrable は今すでに遅延できる形か。**戻すときに元へ返すために持つ。**
	Deferrable bool
}

// quoteIdent は識別子を二重引用符で包む。
//
// **validIdent を通った名前しか渡らない**が、包むこと自体をやめない——
// 将来 validIdent が緩んだときに、ここが最後の壁になる。
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
