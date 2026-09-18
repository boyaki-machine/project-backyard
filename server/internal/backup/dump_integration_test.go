package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store"
)

// TestDumpIntegration は実 DB（pb_app）から書庫を作れることを見る（ApiDesign.md 11.11）。
//
// **pb_app で書き出せることそのものが検証の中心である**（DbDesign.md 9.1.1）。
// オーナーで通っても、実行時のロールで同じ結果になる保証にならない。
func TestDumpIntegration(t *testing.T) {
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

	var buf bytes.Buffer
	if err := NewDumper(pool, "9.9.9").Dump(ctx, &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	meta, err := r.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.FormatVersion != FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", meta.FormatVersion, FormatVersion)
	}
	if meta.PBVersion != "9.9.9" {
		t.Errorf("PBVersion = %q, want 9.9.9", meta.PBVersion)
	}
	if meta.MigrationVersion <= 0 {
		t.Errorf("MigrationVersion = %d, want > 0", meta.MigrationVersion)
	}
	if len(meta.Tables) == 0 {
		t.Fatal("meta.json に表が1つも無い")
	}

	// **goose_db_version は書庫に入らない**（DbDesign.md 9.1.1）。
	for _, tc := range meta.Tables {
		if tc.Name == GooseTable {
			t.Errorf("%s が meta.json に入っている", GooseTable)
		}
	}

	// **meta.json の件数と、data/ の行数が一致すること。** 同じスナップショットで
	// 数えているので、必ず合う。ずれるなら読み方が違っている。
	seen := map[string]int64{}
	for {
		name, body, err := r.NextTable()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextTable: %v", err)
		}
		b, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("%s を読めない: %v", name, err)
		}
		var n int64
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row map[string]any
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatalf("%s の行が JSON ではない: %v\n%s", name, err, line)
			}
			n++
		}
		seen[name] = n
	}
	if len(seen) != len(meta.Tables) {
		t.Errorf("data/ の表 %d 件、meta.json の表 %d 件", len(seen), len(meta.Tables))
	}
	for _, tc := range meta.Tables {
		if got, ok := seen[tc.Name]; !ok {
			t.Errorf("data/%s.jsonl が無い", tc.Name)
		} else if got != tc.Rows {
			t.Errorf("%s: data の行数 %d、meta の件数 %d", tc.Name, got, tc.Rows)
		}
	}
	t.Logf("表 %d 件、書庫 %d バイト、版 %d", len(meta.Tables), buf.Len(), meta.MigrationVersion)
}

// TestValidateIntegration は、実 DB の全ての列を書き出し・取り込みできる形に
// 対応づけられていることを見る。
//
// **マイグレーションで新しい型の列が増えたとき、ここで落ちる。** これが
// 「マイグレーションを足すたびに追従させる」を守る仕掛けである（DbDesign.md 9.1.1）。
func TestValidateIntegration(t *testing.T) {
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

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("接続を借りられない: %v", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tables, err := ListTables(ctx, tx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if err := Validate(tables); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var cols int
	for _, tb := range tables {
		cols += len(tb.Columns)
	}
	t.Logf("表 %d 件・列 %d 本すべてに対応がある", len(tables), cols)
}
