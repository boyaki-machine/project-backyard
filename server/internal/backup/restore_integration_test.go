package backup

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store"
)

// ownerURL は pb_owner の接続文字列。無ければスキップする（make test-db が与える）。
func ownerURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("PB_TEST_DATABASE_URL_OWNER")
	if u == "" {
		t.Skip("PB_TEST_DATABASE_URL_OWNER が未設定のためスキップする")
	}
	return u
}

// scratchDB は使い捨ての DB を作り、その pb_owner 接続文字列を返す。
//
// **取り込みは開発用の DB を壊す。** 表を落として作り直すので、dev をそのまま
// 相手にすると、途中で落ちたときに開発が止まる（pb-147 で実際に起きた）。
// **#147 の完了の見分け方も「別の DB の PB へ取り込む」と言っている。**
//
// 後始末は t.Cleanup で行う（Testing.md 7「状態を変える検証とあとしまつ」）。
func scratchDB(t *testing.T, ctx context.Context) string {
	t.Helper()
	base := ownerURL(t)

	cfg, err := pgx.ParseConfig(base)
	if err != nil {
		t.Fatalf("接続文字列を読めない: %v", err)
	}
	src := cfg.Database
	name := "pb_backup_test_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
	if len(name) > 60 {
		name = name[:60]
	}

	admin := func(sql string) {
		t.Helper()
		c := cfg.Copy()
		c.Database = "postgres"
		conn, err := pgx.ConnectConfig(ctx, c)
		if err != nil {
			t.Fatalf("postgres へ接続できない: %v", err)
		}
		defer conn.Close(context.WithoutCancel(ctx))
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	admin(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	admin(`CREATE DATABASE "` + name + `"`)
	t.Cleanup(func() {
		admin(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	})

	return strings.Replace(base, "/"+src+"?", "/"+name+"?", 1)
}

// dumpNow は pb_app で書庫を1つ作って返す。
func dumpNow(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("プールを作れない: %v", err)
	}
	defer pool.Close()
	var buf bytes.Buffer
	if err := NewDumper(pool, "test").Dump(ctx, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	return buf.Bytes()
}

// TestRestoreRoundTripIntegration は書き出し→取り込みで全表の件数が一致することを見る
// （#147 の完了の見分け方／ApiDesign.md 11.12）。
//
// **この DB そのものを戻す。** 別の DB を用意しなくても、往復が壊れていれば
// mismatched に出るか、取り込みの途中で落ちる。**dev の DB を作り直すので、
// make dev-reset と同じ扱いである。**
func TestRestoreRoundTripIntegration(t *testing.T) {
	ctx := context.Background()
	archive := dumpNow(t, ctx)
	owner := scratchDB(t, ctx)

	res, err := NewRestorer().Restore(ctx, owner, bytes.NewReader(archive), nil)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(res.Mismatched) != 0 {
		t.Errorf("件数が食い違った表: %v", res.Mismatched)
	}
	if len(res.Tables) == 0 {
		t.Fatal("突き合わせの結果が空")
	}
	if res.Backup.MigrationVersion != res.MigrationVersion {
		t.Errorf("書庫の版 %d と取り込み後の版 %d が違う（同じ版を戻したので一致するはず）",
			res.Backup.MigrationVersion, res.MigrationVersion)
	}
	var total int64
	for _, tr := range res.Tables {
		if tr.Rows != tr.Expected {
			t.Errorf("%s: 取り込み後 %d、書庫 %d", tr.Name, tr.Rows, tr.Expected)
		}
		total += tr.Rows
	}
	t.Logf("表 %d 件・行 %d 件が一致した（版 %d）", len(res.Tables), total, res.MigrationVersion)

	// **外部キーが元の形へ戻っていること。** 遅延できる形のまま残すと、
	// 本来と違うスキーマになる（DbDesign.md 9.1.1 の⑥）。
	conn, err := pgx.Connect(ctx, owner)
	if err != nil {
		t.Fatalf("接続できない: %v", err)
	}
	defer conn.Close(ctx)
	var deferrable int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint con
		  JOIN pg_class c ON c.oid = con.conrelid
		 WHERE con.contype = 'f' AND c.relnamespace = 'public'::regnamespace
		   AND con.condeferrable`).Scan(&deferrable); err != nil {
		t.Fatalf("外部キーの状態を読めない: %v", err)
	}
	if deferrable != 0 {
		t.Errorf("遅延できる形のまま残った外部キーが %d 本ある", deferrable)
	}
}

// TestRestoreTooNewIntegration は、いまの PB より新しい書庫を拒むことを見る
// （ApiDesign.md 11.12 の 409 backup_too_new）。
func TestRestoreTooNewIntegration(t *testing.T) {
	ctx := context.Background()
	archive := dumpNow(t, ctx)
	owner := scratchDB(t, ctx)

	// **meta.json の版だけを差し替えた書庫を作る。**
	tooNew := rewriteMetaVersion(t, archive, 99999)

	_, err := NewRestorer().Restore(ctx, owner, bytes.NewReader(tooNew), nil)
	if err == nil {
		t.Fatal("新しい版の書庫が拒まれなかった")
	}
	if !strings.Contains(err.Error(), ErrTooNew.Error()) {
		t.Errorf("ErrTooNew ではない: %v", err)
	}
}

// TestRestoreBadArchiveIntegration は、書庫でないものを拒むことを見る（422）。
//
// **表を落とす前に拒むこと**も併せて見る——落としてから拒むのでは、拒んだ意味が無い。
func TestRestoreBadArchiveIntegration(t *testing.T) {
	ctx := context.Background()
	owner := scratchDB(t, ctx)

	// **先に1回入れて、表がある状態にする。**
	if _, err := NewRestorer().Restore(ctx, owner, bytes.NewReader(dumpNow(t, ctx)), nil); err != nil {
		t.Fatalf("下ごしらえの取り込みに失敗: %v", err)
	}

	_, err := NewRestorer().Restore(ctx, owner, strings.NewReader("これは tar.gz ではない"), nil)
	if err == nil {
		t.Fatal("壊れた書庫が拒まれなかった")
	}
	if !strings.Contains(err.Error(), ErrBadArchive.Error()) {
		t.Errorf("ErrBadArchive ではない: %v", err)
	}

	// **表が残っていること。** 拒む前に落としていたら、ここで 0 になる。
	conn, err := pgx.Connect(ctx, owner)
	if err != nil {
		t.Fatalf("接続できない: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_class
		 WHERE relnamespace = 'public'::regnamespace AND relkind = 'r'`).Scan(&n); err != nil {
		t.Fatalf("表を数えられない: %v", err)
	}
	if n == 0 {
		t.Error("壊れた書庫で表が落とされた（検査は表を落とす前に行うこと）")
	}
}

// TestRestoreBadOwnerCredentialsIntegration は、資格情報が違えば拒むことを見る（422）。
func TestRestoreBadOwnerCredentialsIntegration(t *testing.T) {
	ctx := context.Background()
	archive := dumpNow(t, ctx)
	owner := scratchDB(t, ctx)

	bad := strings.Replace(owner, "pb_owner:", "pb_owner:wrong-", 1)
	if bad == owner {
		t.Skip("接続文字列の形が想定と違うためスキップする")
	}
	_, err := NewRestorer().Restore(ctx, bad, bytes.NewReader(archive), nil)
	if err == nil {
		t.Fatal("誤った資格情報が拒まれなかった")
	}
	if !strings.Contains(err.Error(), ErrBadOwnerCredentials.Error()) {
		t.Errorf("ErrBadOwnerCredentials ではない: %v", err)
	}
}

// rewriteMetaVersion は書庫の meta.json の migration_version を書き換えた書庫を返す。
func rewriteMetaVersion(t *testing.T, archive []byte, version int64) []byte {
	t.Helper()
	r, err := NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()
	meta, err := r.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	meta.MigrationVersion = version

	var out bytes.Buffer
	w := NewWriter(&out)
	if err := w.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}
