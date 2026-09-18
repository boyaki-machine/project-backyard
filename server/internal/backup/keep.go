package backup

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeptRow は、取り込みをまたいで持ち越す行1つ（ApiDesign.md 11.12）。
//
// **操作者のセッションを維持するために使う。** 取り込みは access_token ごと
// 入れ替えるので、何もしないと操作した本人まで締め出され、戻ったかどうかを
// 確かめに行けなくなる。
type KeptRow struct {
	Table string
	// Line は行1つの JSON（書庫の data/*.jsonl と同じ形）。
	Line []byte
}

// CaptureRow は表の1行を、書庫と同じ JSON の形で控える。
//
// **列を手で並べない。** 書き出しと同じ仕組みで作るので、マイグレーションで
// access_token の列が増えても追従する必要が無い。
//
// 行が無ければ nil を返す（誤りではない）。
func CaptureRow(ctx context.Context, pool *pgxpool.Pool, table, col, val string) (*KeptRow, error) {
	if !validIdent(table) || !validIdent(col) {
		return nil, fmt.Errorf("表名または列名が想定の形でない: %q.%q", table, col)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("接続を借りられない: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("トランザクションを始められない: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tables, err := ListTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	var t *Table
	for i := range tables {
		if tables[i].Name == table {
			t = &tables[i]
			break
		}
	}
	if t == nil {
		return nil, fmt.Errorf("表 %q が無い", table)
	}

	var line []byte
	err = tx.QueryRow(ctx,
		selectJSON(*t)+" WHERE r."+quoteIdent(col)+" = $1", val,
	).Scan(&line)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s の行を控えられない: %w", table, err)
	}
	return &KeptRow{Table: table, Line: line}, nil
}

// insertKept は控えた行を入れ直す。
//
// **入れ直せる条件は、参照先の行が復元後のデータに在ることである。** 別の PB の
// 書庫を入れたときなど、在らなければ外部キーが通らない。**そのときは誤りにせず
// 「維持できなかった」として返す**（ApiDesign.md 11.12 の session_kept）。
func insertKept(ctx context.Context, conn *pgx.Conn, kept *KeptRow) (bool, error) {
	if kept == nil {
		return false, nil
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("トランザクションを始められない: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tables, err := ListTables(ctx, tx)
	if err != nil {
		return false, err
	}
	var t *Table
	for i := range tables {
		if tables[i].Name == kept.Table {
			t = &tables[i]
			break
		}
	}
	if t == nil {
		return false, nil
	}
	args, err := rowArgs(*t, kept.Line)
	if err != nil {
		// **控えた行が、戻したスキーマに合わない。** 版をまたいだときに起こる。
		// 誤りにはせず、維持できなかったとして返す。
		return false, nil
	}
	if _, err := tx.Exec(ctx, insertStmt(*t), args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				// **一意制約違反＝書庫に同じ行が入っていた。** 自分で取った書庫を
				// 戻したときの、いちばん普通の場合である。**行は既に在るので
				// 「維持できた」**——入れ直す必要が無かっただけである。
				//
				// **ここを「維持できなかった」と返すと、画面が不要にログイン画面へ
				// 飛ばす**（pb-147 で実サーバを叩いて分かった）。
				return true, nil
			case "23503":
				// **外部キー違反＝参照先の行が復元後のデータに無い。** 別の PB の
				// 書庫を入れたときである。**維持できない。**
				return false, nil
			}
		}
		return false, fmt.Errorf("控えた行を入れ直せない: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, nil
	}
	return true, nil
}
