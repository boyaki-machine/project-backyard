package dbstat

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store"
)

// TestSnapshotIntegration は実 DB（pb_app）で統計を読めることを見る（ApiDesign.md 11.10）。
//
// **pb_app で読めることそのものが検証の中心である。** カタログや統計の view には、
// 見る側のロールによって行や列が隠れるものがある（pg_stat_activity の state / query）。
// オーナーで確かめても、実行時のロールで同じ値が返る保証にならない。**SET ROLE でも
// 足りない**——行を見せるかの判定が接続したロールで行われる view がある（pg_stat_ssl）。
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db が与える）。
func TestSnapshotIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("プールを作れない: %v", err)
	}
	defer pool.Close()

	before := time.Now()
	s, err := New(pool).Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if s.Connection.User != "pb_app" {
		t.Errorf("User = %q, want pb_app（実行時のロールで読んでいない）", s.Connection.User)
	}
	if s.Connection.Host == "" || s.Connection.Port == 0 {
		t.Errorf("接続先が空: %+v", s.Connection)
	}
	if strings.Contains(url, "sslmode=disable") && s.Connection.TLS {
		t.Errorf("sslmode=disable なのに TLS と判定した")
	}
	if s.Server.Version == "" || s.Server.MaxConnections <= 0 || !s.Server.StartedAt.Before(before) {
		t.Errorf("サーバの情報がおかしい: %+v", s.Server)
	}
	if s.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d", s.SizeBytes)
	}
	// **自分の接続が application_name=pb で数えられているはずである**（store.NewPool が補う）。
	if s.Sessions.PB < 1 || s.Sessions.Database < s.Sessions.PB {
		t.Errorf("セッション数がおかしい: %+v", s.Sessions)
	}
	if s.Pool.Max <= 0 || s.Pool.Acquired > s.Pool.Total {
		t.Errorf("プールの値がおかしい: %+v", s.Pool)
	}

	var wantVersion int64
	if err := pool.QueryRow(ctx,
		`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&wantVersion); err != nil {
		t.Fatal(err)
	}
	if s.MigrationVersion != wantVersion || wantVersion <= 0 {
		t.Errorf("MigrationVersion = %d, want %d", s.MigrationVersion, wantVersion)
	}

	byName := make(map[string]Table, len(s.Tables))
	for i, tb := range s.Tables {
		byName[tb.Name] = tb
		if i == 0 {
			continue
		}
		prev := s.Tables[i-1]
		if prev.SizeBytes < tb.SizeBytes ||
			(prev.SizeBytes == tb.SizeBytes && prev.Name > tb.Name) {
			t.Errorf("並びが大きさの降順・名前の昇順でない: %s(%d) → %s(%d)",
				prev.Name, prev.SizeBytes, tb.Name, tb.SizeBytes)
		}
	}
	for _, name := range []string{"ticket", "goose_db_version"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("表 %s が一覧に無い", name)
		}
	}

	// **件数は推定値ではなく count(*) と一致する。** 実行中に書き込まれうる表と
	// 比べると揺れるので、**マイグレーションでしか変わらない表**で突き合わせる。
	for _, name := range []string{"permission", "role_permission"} {
		tb, ok := byName[name]
		if !ok || tb.Rows == nil {
			t.Errorf("表 %s の件数が無い: %+v", name, tb)
			continue
		}
		var want int64
		q := "SELECT count(*) FROM " + pgx.Identifier{"public", name}.Sanitize()
		if err := pool.QueryRow(ctx, q).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if *tb.Rows != want || want == 0 {
			t.Errorf("%s の件数 = %d, want %d", name, *tb.Rows, want)
		}
	}
}
