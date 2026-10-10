package v1

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

func TestTicketTrendsIntegration(t *testing.T) {
	dbURL := os.Getenv("PB_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("PB_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	router := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	adminID := ulidgen.New()
	email := "trend-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, email, auth.SystemRoleAdministrator)
	session := loginAs(t, router, email)
	key := "tr-" + strings.ToLower(adminID[len(adminID)-8:])
	projectID := createDashProject(t, router, pool, session, key, "with_review")
	otherID := createDashProject(t, router, pool, session, key+"-other", "simple")
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM project WHERE id=ANY($1)", []string{projectID, otherID})
		if err != nil {
			t.Error(err)
		}
	})
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	loc, _ := time.LoadLocation("Asia/Tokyo")
	asOf := time.Date(2026, 10, 3, 12, 0, 0, 0, loc)
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, loc)
	event := func(project, id, action, old, next string, at time.Time) {
		var field *string
		if action == "transition" {
			v := "status_key"
			field = &v
		}
		exec("INSERT INTO activity(id,project_id,entity_type,entity_id,action,field,old_value,new_value,occurred_at) VALUES($1,$2,'ticket',$3,$4,$5,$6,$7,$8)", ulidgen.New(), project, id, action, field, old, next, at)
	}
	ticket := func(project string, seq int, kind, status string, parent *string, at time.Time, history bool) string {
		id := ulidgen.New()
		exec("INSERT INTO ticket(id,project_id,seq,type,title,status_key,parent_id,created_at) VALUES($1,$2,$3,$4,'trend fixture',$5,$6,$7)", id, project, seq, kind, status, parent, at)
		if history {
			event(project, id, "create", "", "", at)
		}
		return id
	}
	root := ticket(projectID, 1, "story", "in_progress", nil, start.AddDate(0, 0, -10), true)
	child := ticket(projectID, 2, "task", "done", &root, start, true)
	ticket(projectID, 3, "epic", "todo", nil, start, true)
	ticket(otherID, 1, "task", "todo", nil, start, true)
	missing := ticket(projectID, 4, "task", "done", nil, start.AddDate(0, 0, -1), false)
	ticket(projectID, 5, "task", "todo", nil, start.Add(2*time.Hour), true)
	ticket(projectID, 6, "task", "todo", nil, start.AddDate(0, 0, -1), true)
	ticket(projectID, 7, "task", "todo", nil, asOf, true)
	deleted := ticket(projectID, 8, "task", "todo", nil, start, true)
	exec("DELETE FROM ticket WHERE id=$1", deleted)
	event(projectID, root, "transition", "todo", "in_progress", start.AddDate(0, 0, 1))
	event(projectID, child, "transition", "todo", "done", start.Add(2*time.Hour))
	event(projectID, child, "transition", "done", "review", start.AddDate(0, 0, 1).Add(time.Hour))
	event(projectID, child, "transition", "review", "done", start.AddDate(0, 0, 2).Add(time.Hour))
	get := func(period string, at time.Time) []gen.GetProjectTicketTrendsRow {
		rows, err := q.GetProjectTicketTrends(ctx, gen.GetProjectTicketTrendsParams{ProjectID: projectID, Period: period, AsOf: at})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range rows {
			if r.IsPartial != (i == len(rows)-1) {
				t.Fatalf("partial=%+v", r)
			}
			if r.NewTodo > r.Todo {
				t.Fatalf("new counted twice=%+v", r)
			}
		}
		return rows
	}
	rows := get("7d", asOf)
	if len(rows) != 7 || rows[0].Date != "2026-09-27" || rows[6].Date != "2026-10-03" {
		t.Fatalf("calendar=%+v", rows)
	}
	counts := func(r gen.GetProjectTicketTrendsRow) ticketTrendCounts {
		return ticketTrendCounts{r.Done, r.InProgress, r.Todo, r.NewTodo, r.Unknown}
	}
	expected := []ticketTrendCounts{{1, 0, 3, 1, 1}, {0, 2, 2, 1, 1}, {1, 1, 2, 1, 1}, {1, 1, 2, 1, 1}, {1, 1, 2, 1, 1}, {1, 1, 2, 1, 1}, {2, 1, 2, 1, 0}}
	for i, want := range expected {
		if got := counts(rows[i]); got != want {
			t.Fatalf("day %s=%+v want %+v", rows[i].Date, got, want)
		}
	}
	// Report creation cannot establish a ticket's initial status.
	event(projectID, missing, "create", "", "", start.Add(-time.Hour))
	exec("UPDATE activity SET field='report' WHERE entity_id=$1 AND action='create'", missing)
	if r := get("7d", asOf)[0]; r.Unknown != 1 {
		t.Fatalf("report treated as ticket create=%+v", r)
	}
	event(projectID, missing, "transition", "gone", "done", start.AddDate(0, 0, 3))
	if r := get("7d", asOf)[0]; r.Unknown != 1 {
		t.Fatalf("unknown historical key=%+v", r)
	}
	exec("UPDATE activity SET old_value=NULL WHERE entity_id=$1 AND action='transition'", missing)
	if r := get("7d", asOf)[0]; r.Unknown != 1 {
		t.Fatalf("missing old value=%+v", r)
	}
	exec("INSERT INTO workflow_status(id,workflow_id,key,name,category,sort_order) SELECT $1,workflow_id,'finished','Finished','done',99 FROM project WHERE id=$2", ulidgen.New(), projectID)
	event(projectID, child, "transition", "done", "finished", start.AddDate(0, 0, 3))
	if r := get("7d", asOf)[3]; r.Done != 2 {
		t.Fatalf("custom done=%+v", r)
	}
	exec("DELETE FROM workflow_status WHERE key='finished' AND workflow_id=(SELECT workflow_id FROM project WHERE id=$1)", projectID)
	if r := get("7d", asOf)[3]; r.Unknown != 1 {
		t.Fatalf("removed category=%+v", r)
	}
	exec("DELETE FROM ticket WHERE id=$1", child)
	if r := get("7d", asOf)[0]; r.Done != 0 {
		t.Fatalf("deleted ticket still counted=%+v", r)
	}
	for _, tc := range []struct {
		period, start string
		days          int
	}{{"4w", "2026-09-06", 28}, {"3m", "2026-07-04", 92}, {"6m", "2026-04-04", 183}} {
		r := get(tc.period, asOf)
		if len(r) != tc.days || r[0].Date != tc.start {
			t.Fatalf("period %s=%d %s", tc.period, len(r), r[0].Date)
		}
	}
	for _, tc := range []struct {
		at            time.Time
		period, start string
	}{{time.Date(2026, 5, 31, 12, 0, 0, 0, loc), "3m", "2026-03-01"}, {time.Date(2024, 5, 31, 12, 0, 0, 0, loc), "3m", "2024-03-01"}, {time.Date(2026, 8, 31, 12, 0, 0, 0, loc), "6m", "2026-03-01"}} {
		if r := get(tc.period, tc.at); r[0].Date != tc.start {
			t.Fatalf("month-end=%s want %s", r[0].Date, tc.start)
		}
	}
	empty, err := q.GetProjectTicketTrends(ctx, gen.GetProjectTicketTrendsParams{ProjectID: otherID, Period: "7d", AsOf: start.AddDate(0, 0, -2)})
	if err != nil || len(empty) != 7 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	for _, r := range empty {
		if counts(r) != (ticketTrendCounts{}) {
			t.Fatalf("empty=%+v", r)
		}
	}
	rec := getWithCookie(router, "/api/v1/projects/"+key+"/ticket-trends?period=3m", session)
	if rec.Code != 200 {
		t.Fatalf("API=%d %s", rec.Code, rec.Body.String())
	}
	var view ticketTrendsView
	if err := decodeTrendJSON(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Period != "3m" || view.Days != len(view.Daily) || view.Latest != view.Daily[len(view.Daily)-1].ticketTrendCounts {
		t.Fatalf("view=%+v", view)
	}
	exec("UPDATE project SET timezone='America/New_York' WHERE id=$1", projectID)
	ny, _ := time.LoadLocation("America/New_York")
	for i, md := range [][2]int{{3, 8}, {11, 1}} {
		midnight := time.Date(2026, time.Month(md[0]), md[1], 0, 0, 0, 0, ny)
		next := midnight.AddDate(0, 0, 1)
		id := ticket(projectID, 20+i, "task", "done", nil, midnight, true)
		event(projectID, id, "transition", "todo", "done", next)
		exec("UPDATE ticket SET type='epic' WHERE project_id=$1 AND id<>$2", projectID, id)
		r := get("7d", next.Add(time.Hour))
		if r[5].Date != midnight.Format("2006-01-02") || r[5].Todo != 1 || r[5].Done != 0 || r[6].Done != 1 {
			t.Fatalf("DST boundary=%+v", r)
		}
	}
}
