// Package store は PostgreSQL への接続を組み立てる。
//
// 実際のクエリは sqlc が gen/ に生成したものを使う（Design.md 3.2）。
// 接続は常に実行時ロール pb_app で行い、DDL 権限を持たない（DbDesign.md 3.4）。
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// プールの大きさは DbDesign.md 3.5 の表に合わせる。
// DB 側の max_connections は 50 で、単一プロセス・少人数利用を前提とする。
const (
	minConns = 2
	maxConns = 10

	// applicationName は pg_stat_activity での識別に使う（DbDesign.md 3.5）。
	applicationName = "pb"
)

// NewPool は接続プールを作り、疎通を確認してから返す。
//
// 接続文字列に application_name が含まれていなければ補う。DbDesign.md 3.5 が
// 求める値であり、接続文字列の書き方に依存させないため。
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("接続文字列を解釈できない: %w", err)
	}
	cfg.MinConns = minConns
	cfg.MaxConns = maxConns
	if cfg.ConnConfig.RuntimeParams["application_name"] == "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("接続プールを作成できない: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("DBに接続できない: %w", err)
	}
	return pool, nil
}
