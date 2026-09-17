// Package dbstat は PB が繋いでいる DB の接続状態と統計を読む（ApiDesign.md 11.10）。pb-110。
//
// **アプリのデータの読み書きは sqlc を通すが、ここは通さない。** 表ごとの件数を
// 数える文を、その時点にある表の名前から組み立てる必要があり、sqlc では書けない。
// 読むのはカタログと統計の view だけで、**書き込みは一切しない。**
//
// 接続は他と同じ実行時ロール pb_app である（DbDesign.md 3.4）。**pb_app から見えない
// ものは返さない**——他のロールのセッションの state や query は隠される。
package dbstat

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Snapshot はある時点の DB の接続状態と統計（ApiDesign.md 11.10 の応答の中身）。
type Snapshot struct {
	FetchedAt        time.Time
	Connection       Connection
	Server           Server
	MigrationVersion int64
	Sessions         Sessions
	Pool             Pool
	SizeBytes        int64
	// Tables は大きさの降順、同じなら名前の昇順。
	Tables []Table
}

// Connection は接続先。**パスワードを持たない。**
type Connection struct {
	// Host と Port は PB に与えられた接続先（PB_DATABASE_URL）。
	Host string
	Port uint16
	// Database と User は実際に繋いでいるもの（current_database() / current_user）。
	Database string
	User     string
	// TLS はこの読み取りに使った接続が TLS か。**接続そのもの（ドライバが握っている
	// ソケット）を見て決める**——問い合わせが要らず、view の見え方にも依らない。
	TLS bool
}

// Server は PostgreSQL のサーバの情報。
type Server struct {
	Version   string
	StartedAt time.Time
	// MaxConnections はサーバ全体の上限（DB ごとではない）。
	MaxConnections int
}

// Sessions は pg_stat_activity から数えたセッション数。
type Sessions struct {
	// Database はこの DB に繋いでいるセッション。
	Database int64
	// PB はそのうち application_name が PB の値のもの。**PB のプロセスが
	// 複数あれば全部を含む。**
	PB int64
}

// Pool はこのプロセスの接続プール。**問い合わせを始める前の値である。**
type Pool struct {
	Total    int32
	Acquired int32
	Idle     int32
	Max      int32
}

// Table は表1つの件数と大きさ。
type Table struct {
	Name string
	// Rows は count(*) の正確な数。**pb_app が SELECT できない表では nil。**
	Rows *int64
	// SizeBytes は索引と TOAST を含む大きさ（pg_total_relation_size）。
	SizeBytes int64
}

// Reader は接続プールから統計を読む。
type Reader struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New は pool から読む Reader を返す。
func New(pool *pgxpool.Pool) *Reader {
	return &Reader{pool: pool, now: time.Now}
}

// Snapshot はいまの接続状態と統計を読む。
//
// **1つの読み取り専用のトランザクション（REPEATABLE READ）で読む。** 件数と大きさと
// マイグレーション番号が同じ時点のものになる。件数は全表を1つの文で数える。
func (r *Reader) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot

	// **接続を借りる前にプールを見る。** 借りたあとだと、この読み取り自身の
	// 接続が「使用中」に入る（ApiDesign.md 11.10）。
	st := r.pool.Stat()
	s.Pool = Pool{
		Total: st.TotalConns(), Acquired: st.AcquiredConns(),
		Idle: st.IdleConns(), Max: st.MaxConns(),
	}

	cfg := r.pool.Config().ConnConfig
	s.Connection.Host = cfg.Host
	s.Connection.Port = cfg.Port
	appName := cfg.RuntimeParams["application_name"]

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("接続を借りられない: %w", err)
	}
	defer conn.Release()

	_, s.Connection.TLS = conn.Conn().PgConn().Conn().(*tls.Conn)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("トランザクションを始められない: %w", err)
	}
	// 読み取り専用なので、成否によらず巻き戻せばよい。
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		SELECT current_database(), current_user,
		       current_setting('server_version'), pg_postmaster_start_time(),
		       current_setting('max_connections')::int,
		       pg_database_size(current_database()),
		       (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
		       (SELECT count(*) FROM pg_stat_activity
		         WHERE datname = current_database() AND application_name = $1),
		       (SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied)`,
		appName,
	).Scan(
		&s.Connection.Database, &s.Connection.User,
		&s.Server.Version, &s.Server.StartedAt, &s.Server.MaxConnections,
		&s.SizeBytes, &s.Sessions.Database, &s.Sessions.PB, &s.MigrationVersion,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("DB の状態を読めない: %w", err)
	}

	tables, err := listTables(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	if err := countRows(ctx, tx, tables); err != nil {
		return Snapshot{}, err
	}
	s.Tables = tables
	s.FetchedAt = r.now()
	return s, nil
}

// listTables は public スキーマの表を、大きさの降順・名前の昇順で返す。
// Rows は SELECT できる表だけ 0 で埋め、できない表は nil のままにする。
//
// **区画（パーティション）の子は数えない。** 親の count(*) が子を含むので、
// 並べると二重に数えることになる。
func listTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.relname, has_table_privilege(c.oid, 'SELECT'), pg_total_relation_size(c.oid)
		  FROM pg_class c
		 WHERE c.relnamespace = 'public'::regnamespace
		   AND c.relkind IN ('r', 'p')
		   AND NOT c.relispartition
		 ORDER BY 3 DESC, 1`)
	if err != nil {
		return nil, fmt.Errorf("表の一覧を読めない: %w", err)
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var (
			t        Table
			readable bool
		)
		if err := rows.Scan(&t.Name, &readable, &t.SizeBytes); err != nil {
			return nil, fmt.Errorf("表の一覧を読めない: %w", err)
		}
		if readable {
			var zero int64
			t.Rows = &zero
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("表の一覧を読めない: %w", err)
	}
	return tables, nil
}

// countRows は読める表の件数を、1つの文（UNION ALL）で数えて埋める。
//
// **表の名前は pgx.Identifier で引用する。** 名前はカタログから来るもので利用者の
// 入力ではないが、大文字や記号を含む名前でも文が壊れないようにする。
func countRows(ctx context.Context, tx pgx.Tx, tables []Table) error {
	var (
		parts []string
		index []int
	)
	for i, t := range tables {
		if t.Rows == nil {
			continue
		}
		ident := pgx.Identifier{"public", t.Name}.Sanitize()
		parts = append(parts, fmt.Sprintf("SELECT %d, count(*) FROM %s", len(index), ident))
		index = append(index, i)
	}
	if len(parts) == 0 {
		return nil
	}

	rows, err := tx.Query(ctx, strings.Join(parts, " UNION ALL "))
	if err != nil {
		return fmt.Errorf("件数を数えられない: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			k int
			n int64
		)
		if err := rows.Scan(&k, &n); err != nil {
			return fmt.Errorf("件数を数えられない: %w", err)
		}
		if k < 0 || k >= len(index) {
			return fmt.Errorf("件数の行が表と対応しない: %d", k)
		}
		*tables[index[k]].Rows = n
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("件数を数えられない: %w", err)
	}
	return nil
}
