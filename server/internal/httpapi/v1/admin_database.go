// DB の接続状態と統計（ApiDesign.md 11.10）。pb-110。
//
//	GET /api/v1/admin/database   11.10
//
// **必要権限は system.settings**（11章と同じ）。**読み取り専用で、変更の口は持たない。**
//
// **パスワードはどこにも入らない。** 接続文字列そのものは 11.1 の database_url で、
// これまでどおり値を返さない。
package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/dbstat"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// DatabaseStats は DB の接続状態と統計を読む口。
//
// 実装は dbstat.Reader（プール）。**インターフェースで受けるのは、ハンドラの
// テストが実 DB を立てずに差し替えられるようにするため**（TxRunner と同じ理由）。
type DatabaseStats interface {
	Snapshot(ctx context.Context) (dbstat.Snapshot, error)
}

// databaseStatusResponse は 11.10 の応答。
type databaseStatusResponse struct {
	FetchedAt        time.Time           `json:"fetched_at"`
	Connection       databaseConnection  `json:"connection"`
	Server           databaseServer      `json:"server"`
	MigrationVersion int64               `json:"migration_version"`
	Sessions         databaseSessions    `json:"sessions"`
	Pool             databasePool        `json:"pool"`
	SizeBytes        int64               `json:"size_bytes"`
	Tables           []databaseTableView `json:"tables"`
}

type databaseConnection struct {
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	TLS      bool   `json:"tls"`
}

type databaseServer struct {
	Version        string    `json:"version"`
	StartedAt      time.Time `json:"started_at"`
	MaxConnections int       `json:"max_connections"`
}

type databaseSessions struct {
	Database int64 `json:"database"`
	PB       int64 `json:"pb"`
}

type databasePool struct {
	Total    int32 `json:"total"`
	Acquired int32 `json:"acquired"`
	Idle     int32 `json:"idle"`
	Max      int32 `json:"max"`
}

type databaseTableView struct {
	Name string `json:"name"`
	// Rows は pb_app が SELECT できない表で null（11.10）。**一覧から落とさない。**
	Rows      *int64 `json:"rows"`
	SizeBytes int64  `json:"size_bytes"`
}

// getDatabaseStatus は GET /api/v1/admin/database を処理する（11.10）。
func (h *handler) getDatabaseStatus(w http.ResponseWriter, r *http.Request) {
	if h.dbStats == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /admin/database に DB の統計の口が渡されていない")))
		return
	}
	s, err := h.dbStats.Snapshot(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("DB の統計を読めない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, buildDatabaseStatus(s))
}

// buildDatabaseStatus は dbstat の値を 11.10 の応答の形にする。
func buildDatabaseStatus(s dbstat.Snapshot) databaseStatusResponse {
	// **表が0件でも [] で返す**（null にしない）。画面が配列として回せるように。
	tables := make([]databaseTableView, 0, len(s.Tables))
	for _, t := range s.Tables {
		tables = append(tables, databaseTableView{Name: t.Name, Rows: t.Rows, SizeBytes: t.SizeBytes})
	}
	return databaseStatusResponse{
		FetchedAt: s.FetchedAt.UTC(),
		Connection: databaseConnection{
			Host: s.Connection.Host, Port: s.Connection.Port,
			Database: s.Connection.Database, User: s.Connection.User, TLS: s.Connection.TLS,
		},
		Server: databaseServer{
			Version: s.Server.Version, StartedAt: s.Server.StartedAt.UTC(),
			MaxConnections: s.Server.MaxConnections,
		},
		MigrationVersion: s.MigrationVersion,
		Sessions:         databaseSessions{Database: s.Sessions.Database, PB: s.Sessions.PB},
		Pool: databasePool{
			Total: s.Pool.Total, Acquired: s.Pool.Acquired, Idle: s.Pool.Idle, Max: s.Pool.Max,
		},
		SizeBytes: s.SizeBytes,
		Tables:    tables,
	}
}
