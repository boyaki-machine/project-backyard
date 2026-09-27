package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/dbstat"
)

// fakeDBStats は DatabaseStats の差し替え。呼ばれた回数を数える。
type fakeDBStats struct {
	snap  dbstat.Snapshot
	err   error
	calls int
}

func (f *fakeDBStats) Snapshot(context.Context) (dbstat.Snapshot, error) {
	f.calls++
	return f.snap, f.err
}

// databaseRouter は 11.10 を叩けるルータと、system.settings を持つトークンを返す。
func databaseRouter(t *testing.T, stats DatabaseStats) (http.Handler, *fakeQuerier, string) {
	t.Helper()
	q := newFake(t)
	grantSystemSettings(q)
	token := validToken(q, "[]")
	r := routerWithDeps(Deps{
		Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults(), DBStats: stats,
	})
	return r, q, token
}

// 11.10 の応答の形。**読めない表の rows は null で、一覧から落とさない。**
func TestGetDatabaseStatus(t *testing.T) {
	rows := int64(12345)
	jst := time.FixedZone("JST", 9*60*60)
	stats := &fakeDBStats{snap: dbstat.Snapshot{
		FetchedAt: time.Date(2026, 9, 17, 14, 3, 12, 0, jst),
		Connection: dbstat.Connection{
			Host: "127.0.0.1", Port: 5432, Database: "pb", User: "pb_app", TLS: false,
		},
		Server: dbstat.Server{
			Version: "17.10", StartedAt: time.Date(2026, 9, 17, 11, 17, 59, 0, jst), MaxConnections: 50,
		},
		MigrationVersion: 38,
		Sessions:         dbstat.Sessions{Database: 7, PB: 5},
		Pool:             dbstat.Pool{Total: 5, Acquired: 1, Idle: 4, Max: 10},
		SizeBytes:        79712256,
		Tables: []dbstat.Table{
			{Name: "comment", Rows: &rows, SizeBytes: 43778048},
			{Name: "secret_table", Rows: nil, SizeBytes: 8192},
		},
	}}
	r, _, token := databaseRouter(t, stats)

	rec := getWithCookie(r, "/api/v1/admin/database", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/database = %d, body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	// **日時はエポックミリ秒で返す**（ApiDesign.md 2.2、pb-224）。JST で渡しても同じ瞬間になる。
	if got := body["fetched_at"]; got != float64(1789621392000) { // 2026-09-17T05:03:12Z
		t.Errorf("fetched_at = %v", got)
	}
	conn := body["connection"].(map[string]any)
	if conn["host"] != "127.0.0.1" || conn["port"] != float64(5432) ||
		conn["user"] != "pb_app" || conn["tls"] != false {
		t.Errorf("connection = %v", conn)
	}
	server := body["server"].(map[string]any)
	if server["started_at"] != float64(1789611479000) || server["max_connections"] != float64(50) { // 2026-09-17T02:17:59Z
		t.Errorf("server = %v", server)
	}
	if body["migration_version"] != float64(38) || body["size_bytes"] != float64(79712256) {
		t.Errorf("migration_version / size_bytes = %v / %v", body["migration_version"], body["size_bytes"])
	}
	if s := body["sessions"].(map[string]any); s["database"] != float64(7) || s["pb"] != float64(5) {
		t.Errorf("sessions = %v", s)
	}
	if p := body["pool"].(map[string]any); p["total"] != float64(5) || p["acquired"] != float64(1) ||
		p["idle"] != float64(4) || p["max"] != float64(10) {
		t.Errorf("pool = %v", p)
	}

	tables := body["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("tables = %v", tables)
	}
	first := tables[0].(map[string]any)
	if first["name"] != "comment" || first["rows"] != float64(12345) {
		t.Errorf("tables[0] = %v", first)
	}
	second := tables[1].(map[string]any)
	v, present := second["rows"]
	if !present || v != nil {
		t.Errorf("読めない表の rows は null で返す: %v（キーあり=%v）", second, present)
	}

	// **パスワードはどこにも入らない。** 鍵の名前ごと現れないことを見る。
	if strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Errorf("応答に password が現れた: %s", rec.Body.String())
	}
}

// 表が0件でも tables は [] で返す（null にしない）。
func TestGetDatabaseStatusEmptyTables(t *testing.T) {
	r, _, token := databaseRouter(t, &fakeDBStats{})
	rec := getWithCookie(r, "/api/v1/admin/database", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tables":[]`) {
		t.Errorf("tables が [] でない: %s", rec.Body.String())
	}
}

// system.settings を持たなければ 403 で、**統計を読みにいかない。**
func TestGetDatabaseStatusRequiresPermission(t *testing.T) {
	stats := &fakeDBStats{}
	r, q, token := databaseRouter(t, stats)
	// オペレータは system.settings を持たない（administrator にだけ足した）。
	q.tokenRow.SystemRole = txt(auth.SystemRoleOperator)

	if rec := getWithCookie(r, "/api/v1/admin/database", token); rec.Code != http.StatusForbidden {
		t.Errorf("GET = %d, want 403", rec.Code)
	}
	if stats.calls != 0 {
		t.Errorf("権限の無い要求で統計を %d 回読んだ", stats.calls)
	}
}

// 読めなければ 500。**口が渡されていなくても panic せず 500 にする。**
func TestGetDatabaseStatusFails(t *testing.T) {
	cases := []struct {
		name  string
		stats DatabaseStats
	}{
		{"読み取りの失敗", &fakeDBStats{err: errors.New("statement timeout")}},
		{"口が無い", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _, token := databaseRouter(t, c.stats)
			rec := getWithCookie(r, "/api/v1/admin/database", token)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("GET = %d, want 500 (%s)", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "statement timeout") {
				t.Errorf("内部の原因を応答に出した: %s", rec.Body.String())
			}
		})
	}
}
