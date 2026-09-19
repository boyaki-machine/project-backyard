package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dumper は PB 全体を書庫へ書き出す（ApiDesign.md 11.11）。
//
// **読むだけなので pb_app で足りる**（DbDesign.md 3.4 の ALTER DEFAULT PRIVILEGES）。
// オーナーの資格情報は要らない。
type Dumper struct {
	pool      *pgxpool.Pool
	pbVersion string
	now       func() time.Time
}

// NewDumper は pool から書き出す Dumper を返す。
func NewDumper(pool *pgxpool.Pool, pbVersion string) *Dumper {
	return &Dumper{pool: pool, pbVersion: pbVersion, now: time.Now}
}

// Dump は書庫を w へ流す。
//
// **全表を1つの REPEATABLE READ のトランザクションで読む**（ApiDesign.md 11.11）。
// 表ごとに別のトランザクションにすると、読んでいる途中の書き込みが表のあいだで
// 食い違い、戻したときに外部キーが通らない書庫ができる。
//
// **meta.json を先に書く必要がある**が、件数は数えないと分からない。**同じ
// スナップショットの中で先に数える**ので、行を流したあとの数と必ず一致する。
func (d *Dumper) Dump(ctx context.Context, w io.Writer) error {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("接続を借りられない: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("トランザクションを始められない: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// **このトランザクションのあいだだけ時間の上限を外す**（ApiDesign.md 11.11）。
	// pb_app の既定は 15 秒で、書き出しは行数に比例して長くなる。**SET LOCAL に
	// するので、接続がプールへ戻っても他の要求へ持ち越さない。**
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 0`); err != nil {
		return fmt.Errorf("statement_timeout を外せない: %w", err)
	}

	tables, err := ListTables(ctx, tx)
	if err != nil {
		return err
	}

	meta := Meta{
		FormatVersion: FormatVersion,
		CreatedAt:     d.now().UTC(),
		PBVersion:     d.pbVersion,
	}
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(version_id), 0) FROM `+quoteIdent(GooseTable)+` WHERE is_applied`,
	).Scan(&meta.MigrationVersion); err != nil {
		return fmt.Errorf("マイグレーション番号を読めない: %w", err)
	}
	if meta.Tables, err = countAll(ctx, tx, tables); err != nil {
		return err
	}

	aw := NewWriter(w)
	if err := aw.WriteMeta(meta); err != nil {
		return err
	}
	for _, t := range tables {
		if err := d.dumpTable(ctx, tx, aw, t, meta.CreatedAt); err != nil {
			return fmt.Errorf("%s を書き出せない: %w", t.Name, err)
		}
	}
	return aw.Close()
}

// countAll は全表の件数を1つの文（UNION ALL）で数える。
//
// **1つの文は1つのスナップショットで読む**ので、表どうしの件数が同じ時点のものになる。
func countAll(ctx context.Context, tx pgx.Tx, tables []Table) ([]TableCount, error) {
	if len(tables) == 0 {
		return nil, nil
	}
	parts := make([]string, 0, len(tables))
	for i, t := range tables {
		parts = append(parts, fmt.Sprintf(
			"SELECT %d AS i, count(*) AS n FROM %s", i, quoteIdent(t.Name)))
	}
	rows, err := tx.Query(ctx, strings.Join(parts, " UNION ALL ")+" ORDER BY i")
	if err != nil {
		return nil, fmt.Errorf("件数を数えられない: %w", err)
	}
	defer rows.Close()

	out := make([]TableCount, 0, len(tables))
	for rows.Next() {
		var i int
		var n int64
		if err := rows.Scan(&i, &n); err != nil {
			return nil, fmt.Errorf("件数を数えられない: %w", err)
		}
		out = append(out, TableCount{Name: tables[i].Name, Rows: n})
	}
	return out, rows.Err()
}

// dumpTable は表1つを data/<表名>.jsonl として書く。
//
// **tar は見出しに大きさを要る**ので、行を流しながら書けない。表1つぶんを
// 組み立ててから書く。**書庫全体を抱えないので、いちばん大きい表のぶんで足りる。**
func (d *Dumper) dumpTable(ctx context.Context, tx pgx.Tx, aw *Writer, t Table, modTime time.Time) error {
	rows, err := tx.Query(ctx, selectJSON(t))
	if err != nil {
		return err
	}
	defer rows.Close()

	var buf bytes.Buffer
	for rows.Next() {
		var line []byte
		if err := rows.Scan(&line); err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	return aw.WriteTable(t.Name, buf.Bytes(), modTime)
}

// selectJSON は、表の1行を1つの JSON にする SELECT を組み立てる。
//
// **row_to_json を使う**（json であって jsonb ではない）。jsonb は鍵を並べ替え、
// 数を正規化するので、**書いたままの形で残らない。** row_to_json は列の順を保つ。
func selectJSON(t Table) string {
	exprs := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		exprs = append(exprs, dumpExpr(c)+" AS "+quoteIdent(c.Name))
	}
	return "SELECT row_to_json(r)::text FROM (SELECT " +
		strings.Join(exprs, ", ") + " FROM " + quoteIdent(t.Name) + ") r"
}

// dumpExpr は列1つを JSON の値にする式を返す（DbDesign.md 9.1.1 の型の対応）。
//
// **知らない型は文字列にしない。** 落としたほうが、静かに壊れた書庫を作るより早く気づける
// ——マイグレーションで新しい型の列が増えたとき、ここで止まるのが狙いである。
func dumpExpr(c Column) string {
	q := quoteIdent(c.Name)
	switch {
	case c.IsArray:
		// **配列は JSON の配列にする。** 要素の型は text[] しか無い（pb-147 で数えた）。
		return "to_jsonb(" + q + ")"
	case c.Type == "bytea":
		// **base64 にする**（DbDesign.md 9.1.1）。
		return "encode(" + q + ", 'base64')"
	case strings.HasPrefix(c.Type, "timestamp with time zone"):
		// **ISO8601 UTC の文字列**（ApiDesign.md 2.2 と同じ形）。
		return `to_char(` + q + ` AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`
	case strings.HasPrefix(c.Type, "timestamp without time zone"):
		return `to_char(` + q + `, 'YYYY-MM-DD"T"HH24:MI:SS.US')`
	case c.Type == "date":
		return `to_char(` + q + `, 'YYYY-MM-DD')`
	case c.Type == "json", c.Type == "jsonb":
		// **そのまま JSON の値として埋める。** 文字列に包まない。
		return q
	case c.Type == "boolean":
		return q
	case isNumeric(c.Type):
		return q
	case isTextLike(c.Type):
		return q + "::text"
	default:
		// **組み立てずに、式そのものを壊して落とす**のではなく、呼び出し側が
		// 検査できるよう空を返す。selectJSON は使う前に Validate を通す。
		return ""
	}
}

// Validate は、全ての列を JSON にできるかを見る。**書き出す前に呼ぶ。**
func Validate(tables []Table) error {
	for _, t := range tables {
		for _, c := range t.Columns {
			if dumpExpr(c) == "" {
				return fmt.Errorf(
					"%s.%s の型 %q を書き出せない。backup パッケージに対応を足すこと（DbDesign.md 9.1.1）",
					t.Name, c.Name, c.Type)
			}
			if loadExpr(c, 1) == "" {
				return fmt.Errorf(
					"%s.%s の型 %q を取り込めない。backup パッケージに対応を足すこと（DbDesign.md 9.1.1）",
					t.Name, c.Name, c.Type)
			}
		}
	}
	return nil
}

func isNumeric(t string) bool {
	switch t {
	case "smallint", "integer", "bigint", "real", "double precision":
		return true
	}
	return strings.HasPrefix(t, "numeric")
}

func isTextLike(t string) bool {
	switch t {
	case "text", "citext", "inet", "uuid", "cidr", "macaddr":
		return true
	}
	return strings.HasPrefix(t, "character") || strings.HasPrefix(t, "character varying")
}
