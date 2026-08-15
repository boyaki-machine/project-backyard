package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// TxRunner は複数のクエリを1トランザクションで実行する。
//
// **HTTP ハンドラにプールを直接持たせないための入口である。** ApiDesign.md 5.3 の
// 「project 作成・project_counter 初期化・ワークフローの複製・作成者の登録を
// 単一トランザクションで行う」のように、複数の書き込みが不可分であることを
// 求める操作がハンドラ側にあり、そこから tx を開始できる必要がある。
//
// インターフェースにしてあるのは、v1 パッケージのテストが実DBを立てずに
// 差し替えられるようにするためである（gen.Querier と同じ方針）。
type TxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner はプールを使う実装を返す。
func NewTxRunner(pool *pgxpool.Pool) *TxRunner { return &TxRunner{pool: pool} }

// RunInTx は fn をトランザクションの中で実行する。
//
// fn がエラーを返せばロールバックし、そのエラーをそのまま返す（呼び出し側が
// pgconn.PgError を見て一意制約違反を判別できるよう、包み直さない）。
// nil を返せばコミットする。
//
// ロールバックは context.WithoutCancel で行う。クライアントが接続を切った
// だけでロールバックを送れなくなると、トランザクションが開いたまま残る。
func (t *TxRunner) RunInTx(ctx context.Context, fn func(gen.Querier) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("トランザクションを開始できない: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // Commit 済みなら no-op

	if err := fn(gen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("コミットに失敗した: %w", err)
	}
	return nil
}
