package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
	"math"
	"os"
	"strings"
	"testing"
)

func TestWorkCleanupIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("development DB not configured")
	}
	ctx := context.Background()
	pool, e := store.NewPool(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	user := ulidgen.New()
	email := "cleanup-" + user + "@example.com"
	seedUserWithRole(t, ctx, pool, q, user, email, auth.SystemRoleAdministrator)
	cookie := loginAs(t, r, email)
	key := "cleanup-" + strings.ToLower(user[len(user)-8:])
	base := "/api/v1/projects/" + key
	rec := postWithCookie(r, "/api/v1/projects", cookie, fmt.Sprintf(`{"key":%q,"name":"cleanup fixture","workflow_template":"simple"}`, key))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	agentIDs := []string{}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM project WHERE key=$1`, key)
		for _, id := range agentIDs {
			pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id=$1`, id)
			pool.Exec(ctx, `DELETE FROM actor WHERE id=$1`, id)
		}
	})
	ids := []string{}
	seqs := []int32{}
	for i := 0; i < 3; i++ {
		rec = postWithCookie(r, base+"/tickets", cookie, `{"type":"task","title":"cleanup fixture"}`)
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var x struct {
			ID  string
			Seq int32
		}
		decodeJSONBody(t, rec, &x)
		ids = append(ids, x.ID)
		seqs = append(seqs, x.Seq)
	}
	path := fmt.Sprintf("%s/tickets/%d", base, seqs[0])
	other := fmt.Sprintf("%s/tickets/%d", base, seqs[2])
	post := func(path, body string, want int) testWorkView {
		t.Helper()
		rec := postWithCookie(r, path, cookie, body)
		if rec.Code != want {
			t.Fatalf("%s: %d want %d: %s", path, rec.Code, want, rec.Body.String())
		}
		var v testWorkView
		if want == 200 {
			decodeJSONBody(t, rec, &v)
		}
		return v
	}
	cleanup := func(dry bool) workCleanupResult {
		t.Helper()
		rec := postWithCookie(r, path+"/work-cleanup", cookie, fmt.Sprintf(`{"dry_run":%t}`, dry))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var x workCleanupResult
		decodeJSONBody(t, rec, &x)
		return x
	}
	startBody := fmt.Sprintf(`{"request_id":"shared-start","note":"shared","allocations":[{"seq":%d,"share":0.75},{"seq":%d,"share":0.25}]}`, seqs[0], seqs[1])
	w := post(path+"/work-sessions", startBody, 200)
	if w.Policy.Status != "ready" || w.Policy.Version != "actual-v0" {
		t.Fatalf("policy %+v", w.Policy)
	}
	if _, e = pool.Exec(ctx, `UPDATE work_session SET started_at=now()-interval '2 hours',changed_at=now()-interval '2 hours' WHERE id=$1`, w.ID); e != nil {
		t.Fatal(e)
	}
	// Mutating the document cannot change the already pinned contract.
	if _, e = pool.Exec(ctx, `UPDATE document SET body_md='### 実績\nNo configured contract',version=version+1 WHERE project_id=$1 AND slug='project-management-note'`, workProject(t, ctx, q, key)); e != nil {
		t.Fatal(e)
	}
	m1, m2 := metric(100), metric(1)
	m1.Seq = seqs[0]
	m2.Seq = seqs[1]
	changes, _ := json.Marshal([]changeMetrics{m1, m2})
	finishBody := fmt.Sprintf(`{"request_id":"shared-finish","action":"finish","result":"completed","reported_active_ms":3600000,"note":"runtime fixture","tokens":0,"change_metrics":%s}`, changes)
	done := post(path+"/work-sessions/"+w.ID+"/events", finishBody, 200)
	if len(done.Saved) != 2 || done.Saved[0].Summary.ActualPoint == nil || *done.Saved[0].Summary.ActualPoint != 3 {
		t.Fatalf("saved %+v", done.Saved)
	}
	before, e := q.GetTicketEffort(ctx, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	if math.Abs(before.ActualHours.Float64-.75) > .001 {
		t.Fatal(before)
	}
	// Active ticket cannot be cleaned; closure of only one shared target skips.
	post(path+"/work-cleanup", `{"dry_run":false}`, 409)
	pool.Exec(ctx, `UPDATE ticket SET closed_at=now() WHERE id=$1`, ids[0])
	if x := cleanup(true); len(x.WorkIDs) != 0 || x.SkippedCount != 1 {
		t.Fatal(x)
	}
	pool.Exec(ctx, `UPDATE ticket SET closed_at=now() WHERE id=$1`, ids[1])
	if x := cleanup(true); len(x.WorkIDs) != 1 {
		t.Fatal(x)
	}
	if _, e = q.GetWorkSession(ctx, gen.GetWorkSessionParams{ID: w.ID, ProjectID: workProject(t, ctx, q, key)}); e != nil {
		t.Fatal("dry run mutated record", e)
	}
	if x := cleanup(false); len(x.WorkIDs) != 1 {
		t.Fatal(x)
	}
	if x := cleanup(false); len(x.WorkIDs) != 0 {
		t.Fatal("duplicate cleanup", x)
	}
	after, e := q.GetTicketEffort(ctx, ids[0])
	if e != nil || before != after {
		t.Fatalf("actual values changed: %+v %+v %v", before, after, e)
	}
	totals, e := collectWorkTotals(ctx, q, ids[0])
	if e != nil || math.Abs(totals.ActiveMs-2700000) > .01 || totals.ArchivedCount != 1 {
		t.Fatalf("totals %+v %v", totals, e)
	}
	post(path+"/work-sessions", startBody, 409)
	post(path+"/work-sessions/"+w.ID+"/events", finishBody, 409)
	rec = postWithCookie(r, path+"/reports", cookie, fmt.Sprintf(`{"status":"completed","work_session_id":%q}`, w.ID))
	if rec.Code != 201 {
		t.Fatal("archived report reference", rec.Body.String())
	}
	// Reopen and restore policy, then add work without losing the archived base.
	pool.Exec(ctx, `UPDATE ticket SET closed_at=NULL WHERE id=$1`, ids[0])
	pool.Exec(ctx, `UPDATE document SET body_md=$1,version=version+1 WHERE project_id=$2 AND slug='project-management-note'`, policyBody(), workProject(t, ctx, q, key))
	next := post(path+"/work-sessions", `{"request_id":"new-start"}`, 200)
	pool.Exec(ctx, `UPDATE work_session SET started_at=now()-interval '1 hour',changed_at=now()-interval '1 hour' WHERE id=$1`, next.ID)
	cumulative := metric(1000)
	cumulative.Seq = seqs[0]
	changes, _ = json.Marshal([]changeMetrics{cumulative})
	done = post(path+"/work-sessions/"+next.ID+"/events", fmt.Sprintf(`{"request_id":"new-finish","action":"finish","result":"completed","reported_active_ms":1800000,"note":"runtime","change_metrics":%s}`, changes), 200)
	if *done.Saved[0].Summary.ActualHours != 1.25 || *done.Saved[0].Summary.ActualPoint != 5 {
		t.Fatalf("reopened %+v", done.Saved)
	}
	// Abandonment never counts the idle tail and never overwrites known actuals.
	abandoned := post(path+"/work-sessions", `{"request_id":"abandoned-start"}`, 200)
	pool.Exec(ctx, `UPDATE work_session SET started_at=now()-interval '3 days',changed_at=now()-interval '3 days',active_ms=600000,waiting_ms=120000 WHERE id=$1`, abandoned.ID)
	done = post(path+"/work-sessions/"+abandoned.ID+"/events", `{"request_id":"abandon","action":"abandon","note":"interrupted"}`, 200)
	if done.ActiveMS != 600000 || !done.TimeMissing || math.Abs(*done.Saved[0].Summary.ActualHours-17.0/12) > .0001 || done.Saved[0].Summary.ActualPoint != nil {
		t.Fatalf("abandon %+v", done)
	}
	// Concurrent repeated cleanup preserves the confirmed portion and only compacts once.
	beforePartial, e := q.GetTicketEffort(ctx, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	pool.Exec(ctx, `UPDATE ticket SET closed_at=now() WHERE id=$1`, ids[0])
	type response struct {
		code int
		body []byte
	}
	results := make(chan response, 2)
	for i := 0; i < 2; i++ {
		go func() {
			rec := postWithCookie(r, path+"/work-cleanup", cookie, `{"dry_run":false}`)
			results <- response{rec.Code, rec.Body.Bytes()}
		}()
	}
	total := 0
	for i := 0; i < 2; i++ {
		v := <-results
		if v.code != 200 {
			t.Fatalf("concurrent cleanup %d %s", v.code, v.body)
		}
		var x workCleanupResult
		if e = json.Unmarshal(v.body, &x); e != nil {
			t.Fatal(e)
		}
		total += len(x.WorkIDs)
	}
	if total != 2 {
		t.Fatalf("duplicated cleanup: %d", total)
	}
	afterPartial, e := q.GetTicketEffort(ctx, ids[0])
	if e != nil || beforePartial != afterPartial {
		t.Fatalf("partial actuals changed %+v %+v %v", beforePartial, afterPartial, e)
	}
	totals, e = collectWorkTotals(ctx, q, ids[0])
	if e != nil || math.Abs(totals.ActiveMs-5100000) > .01 || totals.ArchivedCount != 3 || totals.TimeMissing != 1 {
		t.Fatalf("partial base %+v %v", totals, e)
	}
	pool.Exec(ctx, `UPDATE ticket SET closed_at=NULL WHERE id=$1`, ids[0])
	// Unconfigured point keeps measured time, and reports enforce configured agents.
	rec = postWithCookie(r, "/api/v1/me/agents", cookie, fmt.Sprintf(`{"display_name":"fixture codex","project_key":%q,"client_kind":"codex"}`, key))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var actor agentJSON
	decodeJSONBody(t, rec, &actor)
	agentIDs = append(agentIDs, actor.ID)
	rec = postWithCookie(r, "/api/v1/me/agents/"+actor.ID+"/tokens", cookie, `{"expires_in_days":30}`)
	var token issuedAgentTokenJSON
	decodeJSONBody(t, rec, &token)
	pool.Exec(ctx, `UPDATE ticket SET assignee_id=$1 WHERE id=$2`, user, ids[2])
	rec = bearerPost(r, other+"/reports", token.Token, `{"status":"completed"}`)
	if rec.Code != 422 {
		t.Fatalf("missing registration %d %s", rec.Code, rec.Body.String())
	}
	rec = bearerPost(r, other+"/reports", token.Token, `{"status":"completed","effort_unavailable":{"target":"both","reason":"work happened before measurement"}}`)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	rec = bearerPost(r, other+"/work-sessions", token.Token, `{"request_id":"agent-start"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var aw testWorkView
	decodeJSONBody(t, rec, &aw)
	rec = bearerPost(r, other+"/reports", token.Token, `{"status":"completed","effort_unavailable":{"target":"both","reason":"cannot measure"}}`)
	if rec.Code != 409 {
		t.Fatalf("open bypass %d %s", rec.Code, rec.Body.String())
	}
	rec = bearerPost(r, other+"/work-cleanup", token.Token, `{"dry_run":false}`)
	if rec.Code != 403 {
		t.Fatalf("agent cleanup %d", rec.Code)
	}
	// Closed ticket with an open record is cleaned without calculating abandoned time.
	pool.Exec(ctx, `UPDATE ticket SET closed_at=now() WHERE id=$1`, ids[2])
	rec = postWithCookie(r, other+"/work-cleanup", cookie, `{"dry_run":false}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var cleaned workCleanupResult
	decodeJSONBody(t, rec, &cleaned)
	if cleaned.MissingCount != 1 {
		t.Fatal(cleaned)
	}
	value, e := q.GetTicketEffort(ctx, ids[2])
	if e != nil || value.ActualHours.Valid || value.ActualPoint.Valid {
		t.Fatalf("invented actual %+v %v", value, e)
	}
	rec = bearerPost(r, other+"/reports", token.Token, fmt.Sprintf(`{"status":"completed","work_session_id":%q}`, aw.ID))
	if rec.Code != 403 {
		t.Fatalf("missing receipt %d %s", rec.Code, rec.Body.String())
	}
	// Same actor may now start work on a new open ticket in this project.
	pool.Exec(ctx, `UPDATE ticket SET assignee_id=$1 WHERE id=$2`, user, ids[0])
	rec = bearerPost(r, path+"/work-sessions", token.Token, `{"request_id":"after-cleanup"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
}
