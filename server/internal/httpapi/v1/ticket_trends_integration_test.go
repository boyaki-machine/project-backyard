package v1

import (
	"context"
	"fmt"
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
	otherKey := key + "-other"
	projectID := createDashProject(t, router, pool, session, key, "with_review")
	otherID := createDashProject(t, router, pool, session, otherKey, "simple")
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM project WHERE id=ANY($1)", []string{projectID, otherID})
		if err != nil {
			t.Error(err)
		}
	})
	ticket := func(project string, seq int, kind string, parent *string) string {
		id := ulidgen.New()
		_, err := pool.Exec(ctx, "INSERT INTO ticket(id,project_id,seq,type,title,status_key,parent_id) VALUES($1,$2,$3,$4,'trend fixture','todo',$5)", id, project, seq, kind, parent)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	root := ticket(projectID, 1, "story", nil)
	child := ticket(projectID, 2, "task", &root)
	epic := ticket(projectID, 3, "epic", nil)
	deleted := ulidgen.New()
	other := ticket(otherID, 1, "task", nil)
	// This ticket has no activity; created_at/closed_at must not invent events.
	missing := ticket(projectID, 4, "task", nil)
	if _, err := pool.Exec(ctx, "UPDATE ticket SET closed_at=now(),status_key='done' WHERE id=$1", missing); err != nil {
		t.Fatal(err)
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
		_, err := pool.Exec(ctx, "INSERT INTO activity(id,project_id,entity_type,entity_id,action,field,old_value,new_value,occurred_at) VALUES($1,$2,'ticket',$3,$4,$5,$6,$7,$8)", ulidgen.New(), project, id, action, field, old, next, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	event(projectID, root, "create", "", "", start)
	event(projectID, child, "create", "", "", start.Add(time.Hour))
	event(projectID, child, "transition", "todo", "done", start.Add(2*time.Hour))
	event(projectID, child, "transition", "done", "todo", start.Add(3*time.Hour))
	event(projectID, child, "transition", "todo", "done", start.Add(4*time.Hour))
	event(projectID, child, "transition", "done", "done", start.Add(5*time.Hour))
	event(projectID, child, "transition", "unknown", "done", start.Add(6*time.Hour))
	event(projectID, epic, "create", "", "", start.Add(time.Hour))
	event(projectID, deleted, "create", "", "", start.Add(time.Hour))
	event(otherID, other, "create", "", "", start.Add(time.Hour))
	event(projectID, child, "create", "", "", start.Add(-time.Millisecond))
	event(projectID, child, "create", "", "", asOf)
	event(projectID, root, "transition", "todo", "in_progress", asOf.Add(-time.Hour))
	get := func(days int32, at time.Time) []gen.GetProjectTicketTrendsRow {
		rows, err := q.GetProjectTicketTrends(ctx, gen.GetProjectTicketTrendsParams{ProjectID: projectID, AsOf: at, Days: days})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != int(days) {
			t.Fatalf("calendar rows=%d want %d", len(rows), days)
		}
		for i, row := range rows {
			if row.IsPartial != (i == len(rows)-1) {
				t.Fatalf("partial day=%+v", row)
			}
		}
		return rows
	}
	rows := get(7, asOf)
	if rows[0].Date != "2026-09-27" || rows[6].Date != "2026-10-03" || rows[0].Created != 2 || rows[0].Completed != 2 || rows[0].Reopened != 1 || rows[0].UnclassifiedTransitions != 1 {
		t.Fatalf("boundary/event counts=%+v", rows)
	}
	for _, row := range rows[1:] {
		if row.Created+row.Completed+row.Reopened+row.UnclassifiedTransitions != 0 {
			t.Fatalf("unexpected=%+v", row)
		}
	}
	if rows := get(28, asOf); rows[0].Date != "2026-09-06" {
		t.Fatalf("28-day start=%s", rows[0].Date)
	}
	// Deleting the child removes all its past recorded operations.
	if _, err := pool.Exec(ctx, "DELETE FROM ticket WHERE id=$1", child); err != nil {
		t.Fatal(err)
	}
	rows = get(7, asOf)
	if rows[0].Created != 1 || rows[0].Completed != 0 || rows[0].Reopened != 0 || rows[0].UnclassifiedTransitions != 0 {
		t.Fatalf("deleted scope=%+v", rows[0])
	}
	// A second done status is classified by category, not by a hard-coded key.
	if _, err := pool.Exec(ctx, "INSERT INTO workflow_status(id,workflow_id,key,name,category,sort_order) SELECT $1,workflow_id,'finished','Finished','done',99 FROM project WHERE id=$2", ulidgen.New(), projectID); err != nil {
		t.Fatal(err)
	}
	event(projectID, root, "transition", "todo", "finished", start.Add(7*time.Hour))
	rows = get(7, asOf)
	if rows[0].Completed != 1 {
		t.Fatalf("custom done=%+v", rows[0])
	}
	// Changing the workflow makes the earlier custom status unclassifiable.
	if _, err := pool.Exec(ctx, "DELETE FROM workflow_status WHERE key='finished' AND workflow_id=(SELECT workflow_id FROM project WHERE id=$1)", projectID); err != nil {
		t.Fatal(err)
	}
	rows = get(7, asOf)
	if rows[0].Completed != 0 || rows[0].UnclassifiedTransitions != 1 {
		t.Fatalf("workflow scope=%+v", rows[0])
	}
	// Real router response and its sum must agree with daily rows.
	rec := getWithCookie(router, "/api/v1/projects/"+key+"/ticket-trends?days=28", session)
	if rec.Code != 200 {
		t.Fatalf("API=%d %s", rec.Code, rec.Body.String())
	}
	var view ticketTrendsView
	if err := decodeTrendJSON(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Daily) != 28 || view.Days != 28 || view.Timezone != "Asia/Tokyo" {
		t.Fatalf("view=%+v", view)
	}
	var sum ticketTrendCounts
	for _, d := range view.Daily {
		sum.Created += d.Created
		sum.Completed += d.Completed
		sum.Reopened += d.Reopened
		sum.UnclassifiedTransitions += d.UnclassifiedTransitions
	}
	if sum != view.Totals {
		t.Fatalf("sum=%+v totals=%+v", sum, view.Totals)
	}
	// Calendar grouping on the spring/fall DST day must not use fixed 24-hour days.
	if _, err := pool.Exec(ctx, "UPDATE project SET timezone='America/New_York' WHERE id=$1", projectID); err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	for _, md := range [][2]int{{3, 8}, {11, 1}} {
		t.Run(fmt.Sprintf("DST-%d", md[0]), func(t *testing.T) {
			midnight := time.Date(2026, time.Month(md[0]), md[1], 0, 0, 0, 0, ny)
			next := midnight.AddDate(0, 0, 1)
			event(projectID, root, "create", "", "", midnight)
			event(projectID, root, "create", "", "", next.Add(-time.Millisecond))
			event(projectID, root, "create", "", "", next)
			r := get(7, next.Add(time.Hour))
			if r[5].Date != midnight.Format("2006-01-02") || r[5].Created != 2 || r[6].Created != 1 {
				t.Fatalf("DST=%+v", r)
			}
		})
	}
}
