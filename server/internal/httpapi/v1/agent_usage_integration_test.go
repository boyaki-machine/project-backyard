package v1

import (
	"context"
	"fmt"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentUsageIntegration(t *testing.T) {
	dbURL := os.Getenv("PB_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("development DB not configured")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	id := ulidgen.New()
	email := "usage-" + id + "@example.com"
	seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleAdministrator)
	session := loginAs(t, r, email)
	key := "usage-" + strings.ToLower(id[len(id)-8:])
	base := "/api/v1/projects/" + key
	agents := []string{}
	t.Cleanup(func() {
		bg := context.Background()
		if _, e := pool.Exec(bg, `DELETE FROM project WHERE key=$1`, key); e != nil {
			t.Error(e)
		}
		for _, a := range agents {
			pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id=$1`, a)
			if _, e := pool.Exec(bg, `DELETE FROM actor WHERE id=$1`, a); e != nil {
				t.Error(e)
			}
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session, fmt.Sprintf(`{"key":%q,"name":"Usage fixture","workflow_template":"simple"}`, key))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	rec = postWithCookie(r, base+"/tickets", session, `{"type":"task","title":"Usage ticket"}`)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var ticket struct {
		ID  string
		Seq int32
	}
	decodeJSONBody(t, rec, &ticket)
	report := func(body string) reportJSON {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/reports", base, ticket.Seq), session, body)
		if rec.Code != 201 {
			t.Fatalf("report %d %s", rec.Code, rec.Body.String())
		}
		var v reportJSON
		decodeJSONBody(t, rec, &v)
		return v
	}
	first := report(`{"status":"partial","cost":{"tokens":0,"turns":0,"wall_clock_min":0}}`)
	report(`{"status":"completed"}`)
	if _, e := pool.Exec(ctx, `UPDATE agent_run SET client_kind='codex', model_name='snapshot', model_version='v1' WHERE id=$1`, first.AgentRunID); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"main", "eval"} {
		rec = postWithCookie(r, "/api/v1/me/agents", session, fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"other"}`, name, key))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var a agentJSON
		decodeJSONBody(t, rec, &a)
		agents = append(agents, a.ID)
	}
	projectID, e := q.FindProjectIDByKey(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	delegID := ulidgen.New()
	if _, e = pool.Exec(ctx, `INSERT INTO delegation(id,project_id,root_ticket_id,main_actor_id,evaluator_actor_id,scope,created_by) VALUES($1,$2,$3,$4,$5,'{}',$6)`, delegID, projectID, ticket.ID, agents[0], agents[1], id); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO evaluation(id,delegation_id,target_kind,agent_report_id,round,verdict,findings,evaluator_actor_id,evaluator_model_name,tokens_used) VALUES($1,$2,'deliverable',$3,1,'pass','[]',$4,'review-snapshot',7)`, ulidgen.New(), delegID, first.ID, agents[1]); e != nil {
		t.Fatal(e)
	}
	rec = getWithCookie(r, base+"/agent-usage", session)
	if rec.Code != 200 {
		t.Fatalf("usage %d %s", rec.Code, rec.Body.String())
	}
	var v agentUsageView
	decodeJSONBody(t, rec, &v)
	if c := v.Totals; c.RunsCount != 2 || c.TicketsCount != 1 || c.RunTokens != 0 || c.RunTokensMissing != 1 || c.WallClockMinMissing != 1 || c.EvaluationTokens != 7 || c.EvaluationCount != 1 || len(v.Models) != 3 {
		t.Fatalf("usage %+v", v)
	}
	rec = getWithCookie(r, base+"/agent-usage/runs?per_page=1&page=3", session)
	var list List[struct {
		Tokens       *int64  `json:"tokens"`
		WallClockMin *int64  `json:"wall_clock_min"`
		ModelVersion *string `json:"model_version"`
	}]
	decodeJSONBody(t, rec, &list)
	if list.Total != 2 || len(list.Items) != 0 || list.TotalPages != 2 {
		t.Fatalf("paging %+v", list)
	}
	rec = getWithCookie(r, base+"/agent-usage/runs?model_name=snapshot", session)
	decodeJSONBody(t, rec, &list)
	if list.Total != 1 || list.Items[0].Tokens == nil || *list.Items[0].Tokens != 0 || list.Items[0].WallClockMin == nil || list.Items[0].ModelVersion == nil || *list.Items[0].ModelVersion != "v1" {
		t.Fatalf("filtered %+v", list)
	}
	date := time.Now().In(time.FixedZone("Tokyo", 9*3600)).Format(time.DateOnly)
	rec = getWithCookie(r, base+"/agent-usage?from_date="+date+"&to_date="+date, session)
	decodeJSONBody(t, rec, &v)
	if len(v.Daily) != 1 || v.Daily[0].RunsCount != 2 {
		t.Fatalf("period %+v", v)
	}
	for _, query := range []string{"from_date=2026-02-30&to_date=2026-03-01", "from_date=2026-03-01", "from_date=2026-03-02&to_date=2026-03-01"} {
		if got := getWithCookie(r, base+"/agent-usage?"+query, session); got.Code != 422 {
			t.Fatalf("accepted %s %d", query, got.Code)
		}
	}
	if got := getWithCookie(r, base+"/agent-usage/runs?sort=submitted_at", session); got.Code != 422 {
		t.Fatal("sort should be fixed")
	}
	rec = getWithCookie(r, base+"/agent-usage", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated %d", rec.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE ticket SET status_key='removed-status' WHERE id=$1`, ticket.ID); err != nil {
		t.Fatal(err)
	}
	rec = getWithCookie(r, base+"/agent-usage", session)
	decodeJSONBody(t, rec, &v)
	if v.Totals.RunsCount != 2 {
		t.Fatal("unknown current status erased historical usage")
	}
	// An authenticated outsider still needs project and ticket permission.
	outsiderID := ulidgen.New()
	outsiderEmail := "usage-outsider-" + outsiderID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, outsiderID, outsiderEmail, auth.SystemRoleOperator)
	outsiderSession := loginAs(t, r, outsiderEmail)
	if got := getWithCookie(r, base+"/agent-usage", outsiderSession); got.Code != 404 {
		t.Fatalf("outsider usage = %d", got.Code)
	}
	// Another project cannot see these events.
	other := key + "-other"
	rec = postWithCookie(r, "/api/v1/projects", session, fmt.Sprintf(`{"key":%q,"name":"Other","workflow_template":"simple"}`, other))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM project WHERE key=$1`, other) })
	rec = getWithCookie(r, "/api/v1/projects/"+other+"/agent-usage", session)
	decodeJSONBody(t, rec, &v)
	if v.Totals.RunsCount != 0 || v.Totals.EvaluationCount != 0 {
		t.Fatal("cross-project usage leaked")
	}
}
