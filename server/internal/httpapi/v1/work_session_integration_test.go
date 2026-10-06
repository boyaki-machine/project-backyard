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
)

type testWorkView struct {
	workView
	StartedAt int64  `json:"started_at"`
	ChangedAt int64  `json:"changed_at"`
	EndedAt   *int64 `json:"ended_at"`
}

func TestWorkSessionIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("development DB not configured")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	id := ulidgen.New()
	email := "effort-" + id + "@example.com"
	seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleAdministrator)
	session := loginAs(t, r, email)
	key := "effort-" + strings.ToLower(id[len(id)-8:])
	base := "/api/v1/projects/" + key
	agentIDs := []string{}
	t.Cleanup(func() {
		if _, e := pool.Exec(context.Background(), `DELETE FROM project WHERE key=$1`, key); e != nil {
			t.Error(e)
		}
		for _, actorID := range agentIDs {
			pool.Exec(context.Background(), `DELETE FROM audit_log WHERE actor_id=$1`, actorID)
			if _, e := pool.Exec(context.Background(), `DELETE FROM actor WHERE id=$1`, actorID); e != nil {
				t.Error(e)
			}
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session, fmt.Sprintf(`{"key":%q,"name":"Effort fixture","workflow_template":"simple"}`, key))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	tickets := []struct {
		ID  string
		Seq int32
	}{}
	for i := 0; i < 2; i++ {
		rec = postWithCookie(r, base+"/tickets", session, `{"type":"task","title":"Effort fixture"}`)
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var v struct {
			ID  string
			Seq int32
		}
		decodeJSONBody(t, rec, &v)
		tickets = append(tickets, v)
	}
	path := fmt.Sprintf("%s/tickets/%d", base, tickets[0].Seq)
	post := func(path, body string, status int) testWorkView {
		t.Helper()
		rec := postWithCookie(r, path, session, body)
		if rec.Code != status {
			t.Fatalf("%d want %d %s", rec.Code, status, rec.Body.String())
		}
		var v testWorkView
		if status == 200 {
			decodeJSONBody(t, rec, &v)
		}
		return v
	}
	startBody := fmt.Sprintf(`{"request_id":"start","note":"shared research","allocations":[{"seq":%d,"share":0.75},{"seq":%d,"share":0.25}]}`, tickets[0].Seq, tickets[1].Seq)
	work := post(path+"/work-sessions", startBody, 200)
	replay := post(path+"/work-sessions", startBody, 200)
	if work.ID != replay.ID {
		t.Fatal("duplicate start")
	}
	post(path+"/work-sessions", `{"request_id":"other"}`, 409)
	post(path+"/work-sessions", `{"request_id":"start","note":"different"}`, 409)
	events := path + "/work-sessions/" + work.ID + "/events"
	// Simulate precise server intervals without sleeping or editing measured values.
	setTime := func() {
		t.Helper()
		_, e := pool.Exec(ctx, `UPDATE work_session SET started_at=now()-interval '1 hour',changed_at=now()-interval '40 minutes' WHERE id=$1`, work.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	setTime()
	post(events, `{"request_id":"pause","action":"pause","note":"human question"}`, 200)
	_, err = pool.Exec(ctx, `UPDATE work_session SET changed_at=now()-interval '20 minutes' WHERE id=$1`, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	post(events, `{"request_id":"resume","action":"resume"}`, 200)
	finish := `{"request_id":"finish","action":"finish","result":"completed","tokens":0}`
	done := post(events, finish, 200)
	post(events, finish, 200)
	if done.ActiveMS < 2400000 || done.ActiveMS > 2405000 || done.WaitingMS < 1200000 || done.Tokens == nil || *done.Tokens != 0 {
		t.Fatalf("intervals %+v", done)
	}
	check := func(index int, hours, point float64) {
		t.Helper()
		v, e := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{ProjectID: workProject(t, ctx, q, key), Seq: tickets[index].Seq})
		if e != nil {
			t.Fatal(e)
		}
		if !v.ActualHours.Valid || v.ActualHours.Float64 < hours-.001 || v.ActualHours.Float64 > hours+.001 || v.ActualPoint.Float64 != point || v.ActualPointVersion.String != "actual-v1" {
			t.Fatalf("actuals %v %v %v", v.ActualHours, v.ActualPoint, v.ActualPointVersion)
		}
	}
	check(0, .5, effortPoint(float64(done.ActiveMS)*.75))
	check(1, 1.0/6, 1) // First allocation lies just above 30 minutes with API processing time.
	// Repeated reports reference the same measured work; they never change actuals.
	for i := 0; i < 2; i++ {
		rec = postWithCookie(r, path+"/reports", session, fmt.Sprintf(`{"status":"completed","work_session_id":%q}`, work.ID))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
	}
	check(0, .5, effortPoint(float64(done.ActiveMS)*.75))
	rec = postWithCookie(r, path+"/reports", session, `{"status":"completed","work_session_id":"missing"}`)
	if rec.Code != 404 {
		t.Fatal(rec.Body.String())
	}
	// Manual zero is preserved. An explicit null also stays manual until restore_auto.
	ticket, e := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{ProjectID: workProject(t, ctx, q, key), Seq: tickets[0].Seq})
	if e != nil {
		t.Fatal(e)
	}
	rec = bodyWithCookie(r, http.MethodPatch, path, session, `{"actual_hours":0,"actual_point":0,"actual_point_version":"actual-v0"}`, fmt.Sprintf("\"%d\"", ticket.Version))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	post(events, `{"request_id":"correction","action":"correct","reported_active_ms":1200000,"note":"runtime measurement","allocations":[{"seq":1,"share":1}]}`, 200)
	auto, e := q.GetTicketEffort(ctx, tickets[0].ID)
	if e != nil || !auto.ManualHours || auto.ActualHours.Float64 != 0 || auto.ActualPoint.Float64 != 0 {
		t.Fatalf("manual %+v %v", auto, e)
	}
	old, e := q.GetTicketEffort(ctx, tickets[1].ID)
	if e != nil || old.ActualHours.Valid || old.ActualPoint.Valid {
		t.Fatalf("removed allocation stale %+v %v", old, e)
	}
	post(events, `{"request_id":"restore","action":"restore_auto"}`, 200)
	check(0, 1.0/3, 1)

	// Explicit clearing is a manual decision; restoring re-enables aggregation.
	ticket, e = q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{ProjectID: workProject(t, ctx, q, key), Seq: tickets[0].Seq})
	if e != nil {
		t.Fatal(e)
	}
	rec = bodyWithCookie(r, http.MethodPatch, path, session, `{"actual_hours":null,"actual_point":null,"actual_point_version":null}`, fmt.Sprintf("\"%d\"", ticket.Version))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	post(events, `{"request_id":"after-clear","action":"correct","tokens":0,"note":"verified zero tokens"}`, 200)
	cleared, e := q.GetTicketEffort(ctx, tickets[0].ID)
	if e != nil || cleared.ActualHours.Valid || cleared.ActualPoint.Valid || !cleared.ManualHours {
		t.Fatalf("cleared %+v %v", cleared, e)
	}
	post(events, `{"request_id":"restore-clear","action":"restore_auto"}`, 200)
	// Closing disallows new timers; reopening retains actuals and allows additional work.
	if _, e = pool.Exec(ctx, `UPDATE ticket SET closed_at=now() WHERE id=$1`, tickets[0].ID); e != nil {
		t.Fatal(e)
	}
	post(path+"/work-sessions", `{"request_id":"closed-start"}`, 409)
	if _, e = pool.Exec(ctx, `UPDATE ticket SET closed_at=NULL WHERE id=$1`, tickets[0].ID); e != nil {
		t.Fatal(e)
	}
	// Restart adds a distinct session; missing tokens remain unknown and zero remains known.
	next := post(path+"/work-sessions", `{"request_id":"new-start"}`, 200)
	post(path+"/work-sessions/"+next.ID+"/events", `{"request_id":"new-finish","action":"finish","result":"partial","incomplete":true,"note":"interrupted"}`, 200)
	rec = getWithCookie(r, path+"/work-sessions?per_page=1&page=2", session)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var list struct {
		Items   []testWorkView
		Total   int
		Summary effortSummary
	}
	decodeJSONBody(t, rec, &list)
	if list.Total != 2 || len(list.Items) != 1 || list.Summary.TokensMissing != 1 || list.Summary.IncompleteCount != 1 || list.Items[0].TicketSeq != tickets[0].Seq {
		t.Fatalf("list %+v", list)
	}

	// Runtime identity is snapshotted and Agents cannot edit others or use correction rights.
	agentTokens := []string{}
	for _, client := range []string{"codex", "claude_code"} {
		rec = postWithCookie(r, "/api/v1/me/agents", session, fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":%q}`, client, key, client))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var actor agentJSON
		decodeJSONBody(t, rec, &actor)
		agentIDs = append(agentIDs, actor.ID)
		rec = postWithCookie(r, "/api/v1/me/agents/"+actor.ID+"/tokens", session, `{"expires_in_days":30}`)
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var token issuedAgentTokenJSON
		decodeJSONBody(t, rec, &token)
		agentTokens = append(agentTokens, token.Token)
	}
	rec = bearerPost(r, path+"/work-sessions", agentTokens[0], `{"request_id":"unassigned"}`)
	if rec.Code != 403 {
		t.Fatalf("assignment %d %s", rec.Code, rec.Body.String())
	}
	if _, e = pool.Exec(ctx, `UPDATE ticket SET assignee_id=$1 WHERE id=$2`, id, tickets[0].ID); e != nil {
		t.Fatal(e)
	}
	rec = bearerPost(r, path+"/work-sessions", agentTokens[0], `{"request_id":"agent-start"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var agentWork testWorkView
	decodeJSONBody(t, rec, &agentWork)
	if agentWork.ClientKind == nil || *agentWork.ClientKind != "codex" {
		t.Fatalf("snapshot %+v", agentWork)
	}
	agentEvents := path + "/work-sessions/" + agentWork.ID + "/events"
	rec = bearerPost(r, agentEvents, agentTokens[1], `{"request_id":"foreign","action":"finish","result":"completed"}`)
	if rec.Code != 403 {
		t.Fatal(rec.Body.String())
	}
	rec = bearerPost(r, agentEvents, agentTokens[0], `{"request_id":"forbidden-correction","action":"correct","note":"test"}`)
	if rec.Code != 403 {
		t.Fatal(rec.Body.String())
	}
	rec = bearerPost(r, agentEvents, agentTokens[0], `{"request_id":"agent-finish","action":"finish","result":"completed"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	// Deleting the actor preserves both its work and retry journal.
	actor := ulidgen.New()
	if e = q.CreateDeletedAgentActor(ctx, gen.CreateDeletedAgentActorParams{ID: actor, DisplayName: "fixture actor"}); e != nil {
		t.Fatal(e)
	}
	_, err = pool.Exec(ctx, `UPDATE work_session SET actor_id=$1 WHERE id=$2`, actor, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE work_event SET actor_id=$1 WHERE session_id=$2`, actor, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, e = reassignAgentRecords(ctx, q, actor); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `DELETE FROM actor WHERE id=$1`, actor); e != nil {
		t.Fatal(e)
	}

	// Human deletion preserves its event journal even if it owns no work session.
	humanID := ulidgen.New()
	seedUserWithRole(t, ctx, pool, q, humanID, "timer-human-"+humanID+"@example.com", auth.SystemRoleOperator)
	if _, e = pool.Exec(ctx, `UPDATE work_event SET actor_id=$1 WHERE session_id=$2`, humanID, next.ID); e != nil {
		t.Fatal(e)
	}
	rec = deleteWithCookie(r, "/api/v1/admin/users/"+humanID, session)
	if rec.Code != 204 {
		t.Fatalf("human deletion %d %s", rec.Code, rec.Body.String())
	}

	// Historical manual values and an explicit unknown survive the very first timer.
	rec = postWithCookie(r, base+"/tickets", session, `{"type":"task","title":"Historical actuals"}`)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var historical struct {
		ID      string
		Seq     int32
		Version int32
	}
	decodeJSONBody(t, rec, &historical)
	historicalPath := fmt.Sprintf("%s/tickets/%d", base, historical.Seq)
	rec = bodyWithCookie(r, http.MethodPatch, historicalPath, session, `{"actual_hours":null,"actual_point":0,"actual_point_version":"actual-v0"}`, fmt.Sprintf("\"%d\"", historical.Version))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	historyWork := post(historicalPath+"/work-sessions", `{"request_id":"historical-start"}`, 200)
	historyEvents := historicalPath + "/work-sessions/" + historyWork.ID + "/events"
	post(historyEvents, `{"request_id":"historical-finish","action":"finish","result":"completed","reported_active_ms":0,"note":"verified zero measurement"}`, 200)
	historicalEffort, e := q.GetTicketEffort(ctx, historical.ID)
	if e != nil || historicalEffort.ActualHours.Valid || historicalEffort.ActualPoint.Float64 != 0 || historicalEffort.ActualPointVersion.String != "actual-v0" || !historicalEffort.ManualHours || !historicalEffort.ManualPoint {
		t.Fatalf("historical actuals %+v %v", historicalEffort, e)
	}
	post(historyEvents, `{"request_id":"historical-restore","action":"restore_auto"}`, 200)
	historicalEffort, e = q.GetTicketEffort(ctx, historical.ID)
	if e != nil || !historicalEffort.ActualHours.Valid || historicalEffort.ActualHours.Float64 != 0 || historicalEffort.ActualPoint.Float64 != 0 || historicalEffort.ActualPointVersion.String != "actual-v1" {
		t.Fatalf("historical restored %+v %v", historicalEffort, e)
	}
	// Deleting the primary ticket clears derived actuals in surviving shared targets.
	shared := post(path+"/work-sessions", fmt.Sprintf(`{"request_id":"delete-start","note":"shared","allocations":[{"seq":%d,"share":0.5},{"seq":%d,"share":0.5}]}`, tickets[0].Seq, tickets[1].Seq), 200)
	if _, e = pool.Exec(ctx, `UPDATE work_session SET started_at=now()-interval '20 minutes',changed_at=now()-interval '20 minutes' WHERE id=$1`, shared.ID); e != nil {
		t.Fatal(e)
	}
	post(path+"/work-sessions/"+shared.ID+"/events", `{"request_id":"delete-finish","action":"finish","result":"completed"}`, 200)
	old, e = q.GetTicketEffort(ctx, tickets[1].ID)
	if e != nil || !old.ActualHours.Valid {
		t.Fatalf("shared effort %+v %v", old, e)
	}
	rec = deleteWithCookie(r, path, session)
	if rec.Code != 204 {
		t.Fatal(rec.Body.String())
	}
	old, e = q.GetTicketEffort(ctx, tickets[1].ID)
	if e != nil || old.ActualHours.Valid || old.ActualPoint.Valid {
		t.Fatalf("deleted shared effort %+v %v", old, e)
	}

}
func workProject(t *testing.T, ctx context.Context, q gen.Querier, key string) string {
	t.Helper()
	id, e := q.FindProjectIDByKey(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
