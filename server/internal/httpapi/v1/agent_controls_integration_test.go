package v1

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

func TestAgentMonitorIntegration(t *testing.T) {
	f := newAgentControlFixture(t)
	ctx := context.Background()
	a := f.register("Observed worker")
	var token issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/tokens", f.ownerSession, `{"expires_in_days":30}`), &token)
	rec := postWithCookie(f.r, f.base+"/tickets", f.ownerSession, fmt.Sprintf(`{"type":"task","title":"Observed work","assignee_id":%q}`, f.ownerID))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var ticket struct {
		ID  string
		Seq int32
	}
	decodeJSONBody(t, rec, &ticket)
	path := fmt.Sprintf("%s/tickets/%d/work-sessions", f.base, ticket.Seq)
	// Configure through the human API, preserving unrelated project settings.
	rec = putWithCookie(f.r, f.base+"/agent-monitor", f.managerSession, `{"max_consecutive_failures":2}`)
	if rec.Code != 200 {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body.String())
	}
	rec = bodyWithCookie(f.r, "PATCH", "/api/v1/me/agents/"+a.ID, f.ownerSession, `{"model_name":"Before","model_version":"v1"}`, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var originalScopes []byte
	if e := f.pool.QueryRow(ctx, `SELECT scopes FROM access_token WHERE actor_id=$1 AND revoked_at IS NULL`, a.ID).Scan(&originalScopes); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(ctx, `UPDATE access_token SET scopes='[]'::jsonb WHERE actor_id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	rec = bearerRequest(f.r, "PATCH", f.base, token.Token, `{"settings":{}}`, fmt.Sprintf(`"%d"`, scalarInt(t, f.pool, `SELECT version FROM project WHERE key=$1`, f.key)))
	if rec.Code != 403 {
		t.Fatalf("generic settings bypass: %d %s", rec.Code, rec.Body.String())
	}
	rec = bearerRequest(f.r, "PUT", f.base+"/agent-monitor", token.Token, `{"max_consecutive_failures":null}`, "")
	if rec.Code != 403 {
		t.Fatalf("agent policy change: %d %s", rec.Code, rec.Body.String())
	}
	if _, e := f.pool.Exec(ctx, `UPDATE access_token SET scopes=$2 WHERE actor_id=$1`, a.ID, originalScopes); e != nil {
		t.Fatal(e)
	}
	rec = getWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.managerSession)
	if rec.Code != 404 {
		t.Fatalf("nonowner reset access: %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{"request_id":"start-%d"}`, i)
		rec = bearerPost(f.r, path, token.Token, body)
		if rec.Code != 200 {
			t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
		}
		var work testWorkView
		decodeJSONBody(t, rec, &work)
		replay := bearerPost(f.r, path, token.Token, body)
		var replayWork testWorkView
		decodeJSONBody(t, replay, &replayWork)
		if replay.Code != 200 || replayWork.ID != work.ID {
			t.Fatalf("start replay changed: %d %s", replay.Code, replay.Body.String())
		}
		if i == 0 {
			rec = bodyWithCookie(f.r, "PATCH", "/api/v1/me/agents/"+a.ID, f.ownerSession, `{"model_name":"After","model_version":"v2"}`, "")
			if rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
		}
		finish := fmt.Sprintf(`{"request_id":"fail-%d","action":"finish","result":"blocked","outcome":"failed","note":"explicit failure","tokens":7}`, i)
		rec = bearerPost(f.r, path+"/"+work.ID+"/events", token.Token, finish)
		if rec.Code != 200 {
			t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
		}
		rec = bearerPost(f.r, path+"/"+work.ID+"/events", token.Token, finish)
		if rec.Code != 200 {
			t.Fatal("finish replay refused")
		}
		if i == 0 {
			reportPath := fmt.Sprintf("%s/tickets/%d/reports", f.base, ticket.Seq)
			body := fmt.Sprintf(`{"status":"blocked","work_session_id":%q,"cost":{"tokens":11,"turns":3,"wall_clock_min":1}}`, work.ID)
			rec = bearerPost(f.r, reportPath, token.Token, body)
			if rec.Code != 201 {
				t.Fatalf("failed execution report: %d %s", rec.Code, rec.Body.String())
			}
			rec = bearerPost(f.r, reportPath, token.Token, body)
			if rec.Code != 409 {
				t.Fatalf("duplicate failed report: %d %s", rec.Code, rec.Body.String())
			}
		}
	}
	// A later report must not move the observed failure into another reporting period.
	if _, e := f.pool.Exec(ctx, `UPDATE agent_report SET submitted_at=now()+interval '40 days' WHERE agent_run_id IN (SELECT id FROM agent_run WHERE actor_id=$1)`, a.ID); e != nil {
		t.Fatal(e)
	}
	latest, e := gen.New(f.pool).GetLatestAgentReportForTicket(ctx, ticket.ID)
	if e != nil {
		t.Fatal(e)
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE id=$1 AND model_name='Before'`, latest.AgentRunID) != 1 {
		t.Fatal("latest report used execution end instead of submission time")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_attempt WHERE actor_id=$1`, a.ID) != 2 {
		t.Fatal("replay duplicated attempts")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM audit_log WHERE target_id=$1 AND detail ? 'monitor_stop_reasons'`, a.ID) != 1 {
		t.Fatal("monitor stop audit missing or duplicated")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE actor_id=$1 AND status='failed'`, a.ID) != 2 {
		t.Fatal("failures not observed")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE actor_id=$1 AND model_name='Before' AND model_version='v1'`, a.ID) != 1 {
		t.Fatal("started model snapshot changed")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE actor_id=$1 AND model_name='After' AND model_version='v2'`, a.ID) != 1 {
		t.Fatal("new model snapshot missing")
	}
	rec = getWithCookie(f.r, f.base+"/agent-usage", f.ownerSession)
	var earlyUsage agentUsageView
	decodeJSONBody(t, rec, &earlyUsage)
	if earlyUsage.Totals.RunsCount != 2 || earlyUsage.Totals.RunTokens != 14 || earlyUsage.Totals.Turns != 3 || earlyUsage.Totals.TurnsMissing != 1 || earlyUsage.Totals.WallClockMin != 1 || earlyUsage.Totals.WallClockMinMissing != 1 {
		t.Fatalf("failed report cost/missing changed: %+v", earlyUsage.Totals)
	}
	rec = bearerPost(f.r, path, token.Token, `{"request_id":"stopped-start"}`)
	if rec.Code != 403 {
		t.Fatalf("stopped start: %d %s", rec.Code, rec.Body.String())
	}
	rec = bearerRequest(f.r, "PATCH", fmt.Sprintf("%s/tickets/%d", f.base, ticket.Seq), token.Token, `{"title":"must reject"}`, fmt.Sprintf(`"%d"`, scalarInt(t, f.pool, `SELECT version FROM ticket WHERE id=$1`, ticket.ID)))
	if rec.Code != 403 {
		t.Fatalf("stopped write: %d %s", rec.Code, rec.Body.String())
	}
	rec = getWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.ownerSession)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var state monitorState
	decodeJSONBody(t, rec, &state)
	if !state.Stopped || state.ConsecutiveFailures != 2 || state.Attempts != 2 {
		t.Fatalf("state=%+v", state)
	}
	rec = bearerRequest(f.r, "POST", "/api/v1/me/agents/"+a.ID+"/monitor", token.Token, `{"reason":"self reset"}`, "")
	if rec.Code != 403 {
		t.Fatalf("self reset status=%d", rec.Code)
	}
	rec = postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.ownerSession, `{"reason":" "}`)
	if rec.Code != 422 {
		t.Fatalf("empty reset reason: %d", rec.Code)
	}
	beforeReset := time.Now().Truncate(time.Microsecond)
	rec = postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.ownerSession, `{"reason":"reviewed failures"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	// The reset is compared with attempt starts, which carry the server clock, so it must carry that clock too.
	var resetAt time.Time
	if e := f.pool.QueryRow(ctx, `SELECT reset_at FROM agent_monitor_reset WHERE actor_id=$1`, a.ID).Scan(&resetAt); e != nil {
		t.Fatal(e)
	}
	if resetAt.Before(beforeReset) || resetAt.After(time.Now()) {
		t.Fatalf("reset_at=%s is not on the server clock (request started %s)", resetAt, beforeReset)
	}
	rec = bearerPost(f.r, path, token.Token, `{"request_id":"after-reset"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var work testWorkView
	decodeJSONBody(t, rec, &work)
	rec = bearerPost(f.r, path+"/"+work.ID+"/events", token.Token, `{"request_id":"abandon","action":"abandon","note":"human confirmed abandoned"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE actor_id=$1 AND status='abandoned'`, a.ID) != 1 {
		t.Fatal("abandon not observed")
	}

	rec = bearerPost(f.r, path, token.Token, `{"request_id":"partial-start"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var partial testWorkView
	decodeJSONBody(t, rec, &partial)
	rec = bearerPost(f.r, path+"/"+partial.ID+"/events", token.Token, `{"request_id":"partial-finish","action":"finish","result":"partial"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	for i := 0; i < 2; i++ {
		rec = bearerPost(f.r, fmt.Sprintf("%s/tickets/%d/reports", f.base, ticket.Seq), token.Token, fmt.Sprintf(`{"status":"partial","work_session_id":%q}`, partial.ID))
		if rec.Code != 201 {
			t.Fatalf("normal resubmission: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec = getWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.ownerSession)
	decodeJSONBody(t, rec, &state)
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE actor_id=$1 AND status='completed' AND retry_count IN (1,2)`, a.ID) != 2 {
		t.Fatal("unreported failures changed normal resubmission count")
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("partial/report changed failure count: %+v", state)
	}
	rec = bearerPost(f.r, path, token.Token, `{"request_id":"successful-start"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var success testWorkView
	decodeJSONBody(t, rec, &success)
	rec = bearerPost(f.r, path+"/"+success.ID+"/events", token.Token, `{"request_id":"successful-finish","action":"finish","result":"completed"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = getWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/monitor", f.ownerSession)
	decodeJSONBody(t, rec, &state)
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("success did not reset: %+v", state)
	}
	// Attributes are human evaluations, never token scopes.
	req := `{"trust_level":3,"capabilities":["review","review"," tests "]}`
	rec = bodyWithCookie(f.r, http.MethodPatch, "/api/v1/me/agents/"+a.ID, f.ownerSession, req, "")
	if rec.Code != 200 {
		t.Fatalf("owner attributes: %d %s", rec.Code, rec.Body.String())
	}
	if scalarInt(t, f.pool, `SELECT trust_level FROM agent WHERE actor_id=$1`, a.ID) != 3 {
		t.Fatal("trust not updated")
	}
	var count int
	if e := f.pool.QueryRow(ctx, `SELECT jsonb_array_length(capabilities) FROM agent WHERE actor_id=$1`, a.ID).Scan(&count); e != nil || count != 2 {
		t.Fatalf("labels count=%d err=%v", count, e)
	}
	rec = bearerRequest(f.r, "PATCH", "/api/v1/me/agents/"+a.ID, token.Token, `{"trust_level":3}`, "")
	if rec.Code != 403 {
		t.Fatalf("self promotion status=%d %s", rec.Code, rec.Body.String())
	}
	rec = bodyWithCookie(f.r, http.MethodPatch, f.base+"/agents/"+a.ID+"/attributes", f.managerSession, `{"trust_level":0,"capabilities":[]}`, "")
	if rec.Code != 204 {
		t.Fatalf("manager attributes: %d %s", rec.Code, rec.Body.String())
	}
	// A day either side covers the project time zone without depending on the month the test runs in.
	now := time.Now()
	rec = getWithCookie(f.r, f.base+"/agent-usage?from_date="+now.AddDate(0, 0, -1).Format(time.DateOnly)+"&to_date="+now.AddDate(0, 0, 1).Format(time.DateOnly), f.ownerSession)
	if rec.Code != 200 {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"failed_count":2`) || !strings.Contains(rec.Body.String(), `"abandoned_count":1`) {
		t.Fatalf("usage missing outcomes: %s", rec.Body.String())
	}
	rec = postWithCookie(f.r, f.base+"/agents/"+a.ID+"/monitor/reset", f.managerSession, `{"reason":"new attempt interval"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = putWithCookie(f.r, f.base+"/agent-monitor", f.managerSession, `{"max_attempts":1}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = bearerPost(f.r, path, token.Token, `{"request_id":"last-permitted-start"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var last testWorkView
	decodeJSONBody(t, rec, &last)
	rec = bearerRequest(f.r, "PATCH", fmt.Sprintf("%s/tickets/%d", f.base, ticket.Seq), token.Token, `{"title":"active allowed attempt"}`, fmt.Sprintf(`"%d"`, scalarInt(t, f.pool, `SELECT version FROM ticket WHERE id=$1`, ticket.ID)))
	if rec.Code != 200 {
		t.Fatalf("active attempt blocked: %d %s", rec.Code, rec.Body.String())
	}
	rec = bearerPost(f.r, path+"/"+last.ID+"/events", token.Token, `{"request_id":"last-finish","action":"finish","result":"completed"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = bearerPost(f.r, path, token.Token, `{"request_id":"above-limit"}`)
	if rec.Code != 403 {
		t.Fatalf("attempt limit not enforced: %d", rec.Code)
	}
	rec = bearerPost(f.r, path+"/"+last.ID+"/events", token.Token, `{"request_id":"last-finish","action":"finish","result":"completed"}`)
	if rec.Code != 200 {
		t.Fatal("monitor blocked terminal replay")
	}
	rec = deleteWithCookie(f.r, "/api/v1/me/agents/"+a.ID, f.ownerSession)
	if rec.Code != 204 {
		t.Fatalf("deletion regressed: %d %s", rec.Code, rec.Body.String())
	}

}
