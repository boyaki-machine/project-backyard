package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/boyaki-machine/project-backyard/server/migrations"
)

// Result は取り込みの結果（ApiDesign.md 11.12 の応答の中身）。
type Result struct {
	RestoredAt time.Time
	Backup     Meta
	// MigrationVersion は取り込みが終わったあとの版。**Backup.MigrationVersion より
	// 進んでいることがある**（古い書庫を取り込んでから最新まで進めるため）。
	MigrationVersion int64
	Tables           []TableResult
	// Mismatched は expected と rows が食い違った表の名前。**空なら全表が一致した。**
	Mismatched []string
}

// TableResult は表1つの突き合わせ。
type TableResult struct {
	Name string
	// Expected は書庫の meta.json が持つ件数。
	Expected int64
	// Rows は行を入れた直後に数えた count(*)。**最新の版へ進める前の数である**
	// （ApiDesign.md 11.12）。
	Rows int64
}

// ErrTooNew は書庫の版がいまの PB より新しいときに返る（409 backup_too_new）。
var ErrTooNew = errors.New("書庫のマイグレーション番号が、いまの PB より新しい")

// ErrBadArchive は書庫を読めないときに返る（422 validation_failed）。
var ErrBadArchive = errors.New("書庫を読めない")

// ErrBadOwnerCredentials は pb_owner の資格情報が違うときに返る（422）。
var ErrBadOwnerCredentials = errors.New("DB のオーナーの資格情報で接続できない")

// Restorer は書庫を取り込む（ApiDesign.md 11.12、DbDesign.md 9.1.1）。
//
// **オーナーの資格情報は呼び出しごとに受け取り、持ち続けない**（DbDesign.md 3.4）。
// Restore が終われば接続は閉じ、資格情報はどこにも残らない。
type Restorer struct {
	now func() time.Time
}

// NewRestorer は Restorer を返す。
func NewRestorer() *Restorer { return &Restorer{now: time.Now} }

// Restore は書庫を取り込む。**段取りは DbDesign.md 9.1.1 の①〜⑩。**
//
// ownerURL は pb_owner の接続文字列（画面で受け取った資格情報から組み立てる）。
// **保守モードへの出入りは呼び出し側が行う**——この関数は DB だけを見る。
//
// currentVersion はいまの PB が持つ最新のマイグレーション番号である。
func (rs *Restorer) Restore(ctx context.Context, ownerURL string, src io.Reader) (Result, error) {
	ar, err := NewReader(src)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	defer ar.Close()

	meta, err := ar.ReadMeta()
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	if meta.FormatVersion != FormatVersion {
		return Result{}, fmt.Errorf(
			"%w: 書庫の形式の版が %d で、この PB が読めるのは %d である",
			ErrBadArchive, meta.FormatVersion, FormatVersion)
	}

	// **goose は database/sql しか取らない**（Design.md 3.1）。マイグレーションを
	// 走らせる箇所だけで使い、行の読み書きは pgx で直に行う。
	db := stdlib.OpenDB(*mustParse(ownerURL))
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadOwnerCredentials, err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return Result{}, fmt.Errorf("マイグレーションを読めない: %w", err)
	}

	// **③より前に版を比べる。** 表を落としてから拒むのでは、拒んだ意味が無い。
	latest, err := latestSource(provider)
	if err != nil {
		return Result{}, err
	}
	if meta.MigrationVersion > latest {
		return Result{}, fmt.Errorf(
			"%w（書庫 %d / この PB %d）", ErrTooNew, meta.MigrationVersion, latest)
	}

	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadOwnerCredentials, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// ③ 表を落とす。**DROP SCHEMA はしない**（DbDesign.md 9.1.1）。
	if err := dropAll(ctx, conn); err != nil {
		return Result{}, err
	}
	// ④ 書庫の版までスキーマを進める。
	if _, err := provider.UpTo(ctx, meta.MigrationVersion); err != nil {
		return Result{}, fmt.Errorf("版 %d まで進められない: %w", meta.MigrationVersion, err)
	}
	// ⑤ マイグレーションが入れた行を払う。
	if err := truncateAll(ctx, conn); err != nil {
		return Result{}, err
	}

	// ⑥⑦ 外部キーを遅延させ、1つのトランザクションで入れて数える。
	res, err := rs.loadAll(ctx, conn, ar, meta)
	if err != nil {
		return Result{}, err
	}

	// ⑧ 最新の版まで進める。**データを入れるマイグレーションが本来の順で走る。**
	if _, err := provider.Up(ctx); err != nil {
		return Result{}, fmt.Errorf("最新の版まで進められない: %w", err)
	}
	if res.MigrationVersion, err = provider.GetDBVersion(ctx); err != nil {
		return Result{}, fmt.Errorf("取り込んだあとの版を読めない: %w", err)
	}
	res.RestoredAt = rs.now().UTC()
	return res, nil
}

// latestSource は埋め込んだマイグレーションの最大の番号を返す。
func latestSource(p *goose.Provider) (int64, error) {
	var max int64
	for _, s := range p.ListSources() {
		if s.Version > max {
			max = s.Version
		}
	}
	if max == 0 {
		return 0, errors.New("埋め込んだマイグレーションが1件も無い")
	}
	return max, nil
}

// mustParse は接続文字列を pgx の設定にする。**呼ぶ前に Ping で確かめる**ので、
// ここで失敗しても資格情報の誤りとして扱える。
func mustParse(url string) *pgx.ConnConfig {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		// **組み立てるのは PB 自身である**（画面が渡すのはロール名とパスワードだけ）。
		// ここが壊れるのは PB の不具合なので、空の設定で先の Ping に落とす。
		return &pgx.ConnConfig{}
	}
	return cfg
}

// dropAll は public にある、拡張が持っていないものを落とす（DbDesign.md 9.1.1 の③）。
//
// **表だけでは足りない。** 0001 は set_updated_at() という関数を作っており、表を落としても
// 残る。残ったまま④を走らせると function … already exists で止まる（pb-147 で実機を見た）。
//
// **拡張が持つものは落とさない。** citext と pg_trgm は public にたくさんの関数と型を作るが、
// CREATE EXTENSION IF NOT EXISTS で作られるので残っていて構わない。
//
// **DROP SCHEMA public はしない。** ALTER DEFAULT PRIVILEGES … IN SCHEMA public の
// 登録がスキーマに紐づいており、作り直すと一緒に消える。initdb は pgdata が空の
// 初回しか走らないので、そのあと pb_owner が作った表を pb_app が読めなくなる。
func dropAll(ctx context.Context, conn *pgx.Conn) error {
	// **順に落とす。** CASCADE を付けるので依存は解けるが、ビュー→表→関数→型の順に
	// すると、CASCADE で消える範囲が狭くなり、何が落ちたかが追いやすい。
	for _, step := range []struct {
		what string
		sql  string
	}{
		{"ビュー", `SELECT 'DROP VIEW IF EXISTS ' || quote_ident(c.relname) || ' CASCADE'
		              FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace
		                AND c.relkind = 'v' AND NOT ` + notExtension("c.oid", "pg_class")},
		{"実体化ビュー", `SELECT 'DROP MATERIALIZED VIEW IF EXISTS ' || quote_ident(c.relname) || ' CASCADE'
		              FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace
		                AND c.relkind = 'm' AND NOT ` + notExtension("c.oid", "pg_class")},
		{"表", `SELECT 'DROP TABLE IF EXISTS ' || quote_ident(c.relname) || ' CASCADE'
		              FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace
		                AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		                AND NOT ` + notExtension("c.oid", "pg_class")},
		{"順序", `SELECT 'DROP SEQUENCE IF EXISTS ' || quote_ident(c.relname) || ' CASCADE'
		              FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace
		                AND c.relkind = 'S' AND NOT ` + notExtension("c.oid", "pg_class")},
		{"関数", `SELECT 'DROP ROUTINE IF EXISTS ' || p.oid::regprocedure::text || ' CASCADE'
		              FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace
		                AND NOT ` + notExtension("p.oid", "pg_proc")},
		{"型", `SELECT 'DROP TYPE IF EXISTS ' || quote_ident(t.typname) || ' CASCADE'
		              FROM pg_type t WHERE t.typnamespace = 'public'::regnamespace
		                AND t.typtype IN ('e', 'd', 'r')
		                AND NOT ` + notExtension("t.oid", "pg_type")},
	} {
		if err := execAll(ctx, conn, step.sql); err != nil {
			return fmt.Errorf("%sを落とせない: %w", step.what, err)
		}
	}
	return nil
}

// notExtension は「拡張が持っている」という条件を返す（pg_depend の deptype = 'e'）。
func notExtension(oid, catalog string) string {
	return `EXISTS (SELECT 1 FROM pg_depend d
	                 WHERE d.objid = ` + oid + ` AND d.classid = '` + catalog + `'::regclass
	                   AND d.deptype = 'e')`
}

// execAll は「文を返す問い合わせ」を実行し、返った文を順に流す。
//
// **落とす対象と DROP 文の組み立てを DB の側に置く。** 識別子の引用も
// quote_ident に任せられる。
func execAll(ctx context.Context, conn *pgx.Conn, query string) error {
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return err
	}
	var stmts []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		stmts = append(stmts, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// truncateAll は goose_db_version を除く全表を空にする（⑤）。
//
// **マイグレーションはスキーマだけでなく行も入れる**（0010 の権限カタログ、
// 0037・0038 のテンプレート本文）。払わないと、書庫の同じ行と主キーで衝突する。
//
// **全表を1つの文に並べる**ので、外部キーの順序を気にしなくてよい。
// **goose_db_version を除く**——空にすると④で進めた版が無かったことになる。
func truncateAll(ctx context.Context, conn *pgx.Conn) error {
	names, err := allTableNames(ctx, conn, false)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	list := make([]string, 0, len(names))
	for _, n := range names {
		list = append(list, quoteIdent(n))
	}
	_, err = conn.Exec(ctx, "TRUNCATE "+joinComma(list)+" CASCADE")
	if err != nil {
		return fmt.Errorf("表を空にできない: %w", err)
	}
	return nil
}

// allTableNames は public スキーマの表名を返す。includeGoose が false なら
// goose_db_version を除く。
func allTableNames(ctx context.Context, conn *pgx.Conn, includeGoose bool) ([]string, error) {
	sql := `SELECT c.relname FROM pg_class c
	         WHERE c.relnamespace = 'public'::regnamespace
	           AND c.relkind IN ('r', 'p') AND NOT c.relispartition`
	if !includeGoose {
		sql += ` AND c.relname <> '` + GooseTable + `'`
	}
	rows, err := conn.Query(ctx, sql+` ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("表の一覧を読めない: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("表の一覧を読めない: %w", err)
		}
		if !validIdent(n) {
			return nil, fmt.Errorf("表名が想定の形でない: %q", n)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// loadAll は書庫の行を入れ、入れた直後の件数を数える（⑥⑦）。
//
// **外部キーを遅延させる**（DbDesign.md 9.1.1 の⑥）。外部キーは行ごとにその場で
// 検査されるので、ticket.parent_id のような自己参照では子を親より先に入れた時点で
// 落ちる。**検査が消えるわけではない**——遅延した検査は COMMIT でまとめて走り、
// 整合していない書庫はそこで弾かれる。
func (rs *Restorer) loadAll(ctx context.Context, conn *pgx.Conn, ar *Reader, meta Meta) (Result, error) {
	// **遅延できる形にするのは、入れるトランザクションの外で行う。** 同じ
	// トランザクションの中で戻すと、戻した時点で遅延中の検査が走ってしまう。
	fks, err := setDeferrable(ctx, conn)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		// **元へ戻す。** 途中で落ちたときは③からやり直すので消えるが、
		// やり直さずに使い続けると本来と違う形のスキーマになる。
		_ = restoreDeferrable(context.WithoutCancel(ctx), conn, fks)
	}()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("トランザクションを始められない: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return Result{}, fmt.Errorf("外部キーを遅延させられない: %w", err)
	}
	// **時間の上限を外す。** 取り込みは行数に比例して長くなる。
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 0`); err != nil {
		return Result{}, fmt.Errorf("statement_timeout を外せない: %w", err)
	}

	tables, err := ListTables(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if err := Validate(tables); err != nil {
		return Result{}, err
	}
	byName := make(map[string]Table, len(tables))
	for _, t := range tables {
		byName[t.Name] = t
	}

	loaded := map[string]int64{}
	for {
		name, body, err := ar.NextTable()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
		}
		t, ok := byName[name]
		if !ok {
			// **知らない表は黙って捨てない。** 版が揃っていない書庫を
			// 受け入れたことになる。
			return Result{}, fmt.Errorf(
				"%w: 書庫にある表 %q が、この版のスキーマに無い", ErrBadArchive, name)
		}
		n, err := loadTable(ctx, tx, t, body)
		if err != nil {
			return Result{}, err
		}
		loaded[name] = n
	}

	// ⑦ 数えて突き合わせる。**最新の版へ進める前である**（ApiDesign.md 11.12）。
	counts, err := countAll(ctx, tx, tables)
	if err != nil {
		return Result{}, err
	}

	res := Result{Backup: meta, Tables: make([]TableResult, 0, len(counts))}
	for _, c := range counts {
		expected, ok := meta.Count(c.Name)
		if !ok {
			return Result{}, fmt.Errorf(
				"%w: この版にある表 %q が書庫に無い", ErrBadArchive, c.Name)
		}
		res.Tables = append(res.Tables, TableResult{
			Name: c.Name, Expected: expected, Rows: c.Rows,
		})
		if c.Rows != expected {
			res.Mismatched = append(res.Mismatched, c.Name)
		}
	}

	// **COMMIT で遅延した検査がまとめて走る。** ここで落ちるのは、書庫の行が
	// 互いに整合していないということである。
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("取り込みを確定できない（書庫の行が整合していない可能性がある）: %w", err)
	}
	committed = true
	return res, nil
}

// setDeferrable は全ての外部キーを遅延できる形に変え、元の状態を返す。
func setDeferrable(ctx context.Context, conn *pgx.Conn) ([]Constraint, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("トランザクションを始められない: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	fks, err := ForeignKeys(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, k := range fks {
		if k.Deferrable {
			continue
		}
		_, err := tx.Exec(ctx, "ALTER TABLE "+quoteIdent(k.Table)+
			" ALTER CONSTRAINT "+quoteIdent(k.Name)+" DEFERRABLE INITIALLY IMMEDIATE")
		if err != nil {
			return nil, fmt.Errorf("%s の %s を遅延できる形にできない: %w", k.Table, k.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("外部キーの形を変えられない: %w", err)
	}
	return fks, nil
}

// restoreDeferrable は、元は遅延できなかった外部キーを元へ戻す。
func restoreDeferrable(ctx context.Context, conn *pgx.Conn, fks []Constraint) error {
	for _, k := range fks {
		if k.Deferrable {
			continue
		}
		_, err := conn.Exec(ctx, "ALTER TABLE "+quoteIdent(k.Table)+
			" ALTER CONSTRAINT "+quoteIdent(k.Name)+" NOT DEFERRABLE")
		if err != nil {
			return fmt.Errorf("%s の %s を元へ戻せない: %w", k.Table, k.Name, err)
		}
	}
	return nil
}
