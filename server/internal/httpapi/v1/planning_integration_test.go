package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanningIntegration(t *testing.T) {
	f := newGanttFixture(t, "Planning regression")
	ctx := context.Background()
	q := gen.New(f.pool)
	root := f.create(t, "root", 0)
	a := f.create(t, "a", root)
	b := f.create(t, "b", root)
	outside := f.create(t, "outside", 0)
	register := func(name string) (agentJSON, string) {
		t.Helper()
		rec := postWithCookie(f.r, "/api/v1/me/agents", f.session, fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"other"}`, name, f.key))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var agent agentJSON
		decodeJSONBody(t, rec, &agent)
		t.Cleanup(func() {
			f.pool.Exec(context.Background(), `DELETE FROM project WHERE key=$1`, f.key)
			f.pool.Exec(context.Background(), `DELETE FROM audit_log WHERE actor_id=$1`, agent.ID)
			if _, e := f.pool.Exec(context.Background(), `DELETE FROM actor WHERE id=$1`, agent.ID); e != nil {
				t.Error(e)
			}
		})
		rec = postWithCookie(f.r, "/api/v1/me/agents/"+agent.ID+"/tokens", f.session, `{"expires_in_days":30,"scopes":["project.view","ticket.view","ticket.plan.edit","ticket.self_edit","ticket.create"]}`)
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var token issuedAgentTokenJSON
		decodeJSONBody(t, rec, &token)
		return agent, token.Token
	}
	main, token := register("main")
	eval, evalToken := register("eval")
	path := func(seq int, suffix string) string { return fmt.Sprintf("%s/tickets/%d%s", f.base, seq, suffix) }
	rec := postWithCookie(f.r, path(root, "/delegation"), f.session, fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{"goal":"planning"}}`, main.ID, eval.ID))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var d struct{ ID string }
	decodeJSONBody(t, rec, &d)
	state := func(s string) {
		t.Helper()
		if _, e := f.pool.Exec(ctx, `UPDATE delegation SET status=$1 WHERE id=$2`, s, d.ID); e != nil {
			t.Fatal(e)
		}
	}
	call := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		if method == http.MethodPatch && strings.HasSuffix(path, "/links") == false && !strings.Contains(path, "/links/") {
			cur := bearerGet(f.r, path, tok)
			var version struct{ Version int }
			decodeJSONBody(t, cur, &version)
			req.Header.Set("If-Match", fmt.Sprintf("%d", version.Version))
		}
		res := httptest.NewRecorder()
		f.r.ServeHTTP(res, req)
		return res
	}
	expect := func(res *httptest.ResponseRecorder, status int) {
		t.Helper()
		if res.Code != status {
			t.Fatalf("status %d want %d body %s", res.Code, status, res.Body.String())
		}
	}
	linkBody := fmt.Sprintf(`{"target_seq":%d,"link_type":"FS","lag_days":2}`, b)
	for _, s := range []string{"planning", "paused", "ended"} {
		state(s)
		expect(call("POST", path(a, "/links"), linkBody, token), 403)
		expect(call("POST", path(a, "/move"), `{"position":"first"}`, token), 403)
		expect(call("PATCH", path(a, ""), `{"start_at":1791212400000,"due_at":1791298800000,"all_day":true}`, token), 403)
	}
	state("approved")
	expect(call("POST", path(root, "/move"), fmt.Sprintf(`{"before_seq":%d}`, outside), token), 403)
	expect(call("POST", path(a, "/links"), linkBody, evalToken), 403)
	expect(call("POST", path(a, "/links"), fmt.Sprintf(`{"target_seq":%d,"link_type":"FS"}`, outside), token), 403)
	expect(call("POST", path(outside, "/links"), fmt.Sprintf(`{"target_seq":%d,"link_type":"FS"}`, a), token), 403)
	ids := []string{}
	for _, typ := range []string{"FS", "SS", "FF", "SF"} {
		rec := call("POST", path(a, "/links"), fmt.Sprintf(`{"target_seq":%d,"link_type":%q,"lag_days":-1}`, b, typ), token)
		expect(rec, 201)
		var link struct{ ID string }
		decodeJSONBody(t, rec, &link)
		ids = append(ids, link.ID)
	}
	expect(call("POST", path(a, "/links"), linkBody, token), 409)
	expect(call("POST", path(b, "/links"), fmt.Sprintf(`{"target_seq":%d,"link_type":"FS"}`, a), token), 422)
	expect(call("POST", path(a, "/links"), fmt.Sprintf(`{"target_seq":%d,"link_type":"FS"}`, a), token), 422)
	expect(call("PATCH", path(b, "/links/"+ids[0]), `{"lag_days":3}`, token), 200)
	expect(call("DELETE", path(b, "/links/"+ids[1]), "", evalToken), 403)
	state("paused")
	expect(call("PATCH", path(a, "/links/"+ids[0]), `{"lag_days":4}`, token), 403)
	expect(call("DELETE", path(a, "/links/"+ids[1]), "", token), 403)
	state("approved")
	before := map[string]any{}
	decodeJSONBody(t, bearerGet(f.r, path(b, ""), token), &before)
	expect(call("POST", path(b, "/move"), fmt.Sprintf(`{"before_seq":%d}`, a), token), 200)
	after := map[string]any{}
	decodeJSONBody(t, bearerGet(f.r, path(b, ""), token), &after)
	for _, k := range []string{"parent_seq", "staged_at", "assignee", "status", "sprint"} {
		x, _ := json.Marshal(before[k])
		y, _ := json.Marshal(after[k])
		if string(x) != string(y) {
			t.Fatalf("move changed %s", k)
		}
	}
	expect(call("POST", path(b, "/move"), `{"position":"last"}`, token), 200)
	expect(call("POST", path(b, "/move"), `{"position":"first","staged":true}`, token), 422)
	expect(call("POST", path(b, "/move"), `{"position":"first","parent_seq":null}`, token), 422)
	expect(call("POST", path(b, "/move"), fmt.Sprintf(`{"before_seq":%d}`, outside), token), 422)
	expect(call("PATCH", path(a, ""), `{"start_at":1791212400000,"due_at":1791298800000,"all_day":true,"estimate_point":5}`, token), 200)
	list, _ := f.gantt(t, "")
	if len(list.Links) != 4 {
		t.Fatalf("gantt links %d", len(list.Links))
	}
	rec = bearerPost(f.r, path(root, "/confirmation"), token, `{"reason":"scope_change","question_md":"Can the plan change?"}`)
	expect(rec, 201)
	expect(call("POST", path(a, "/move"), `{"position":"last"}`, token), 403)
	expect(call("DELETE", path(b, "/links/"+ids[1]), "", token), 403)
	// A refused later operation must preserve the earlier successful link and dates.
	if got := scalarInt(t, f.pool, `SELECT lag_days FROM ticket_link WHERE id=$1`, ids[0]); got != 3 {
		t.Fatalf("lag %d", got)
	}
	projectID, e := q.FindProjectIDByKey(ctx, f.key)
	if e != nil {
		t.Fatal(e)
	}
	if got := scalarInt(t, f.pool, `SELECT count(*) FROM ticket WHERE project_id=$1 AND estimate_point=5 AND start_at IS NOT NULL AND due_at IS NOT NULL`, projectID); got != 1 {
		t.Fatal("date/estimate update lost")
	}
	// Plan-only grants can write dates, but cannot reach general editing fields.
	rec = postWithCookie(f.r, "/api/v1/me/agents/"+main.ID+"/tokens", f.session, `{"expires_in_days":30,"scopes":["project.view","ticket.view","ticket.plan.edit"]}`)
	expect(rec, 201)
	var planOnly issuedAgentTokenJSON
	decodeJSONBody(t, rec, &planOnly)
	if _, e := f.pool.Exec(ctx, `DELETE FROM confirmation WHERE delegation_id=$1`, d.ID); e != nil {
		t.Fatal(e)
	}
	expect(call("PATCH", path(a, ""), `{"start_at":1791212400000,"due_at":1791298800000,"all_day":true}`, planOnly.Token), 200)
	for _, body := range []string{`{"title":"changed"}`, `{"execution_mode":"agent_draft"}`, `{"scope":{}}`, `{"parent_seq":null}`, `{"estimate_point":8}`} {
		expect(call("PATCH", path(a, ""), body, planOnly.Token), 403)
	}
	// Granting only read permissions cannot write even inside an approved tree.
	rec = postWithCookie(f.r, "/api/v1/me/agents/"+main.ID+"/tokens", f.session, `{"expires_in_days":30,"scopes":["project.view","ticket.view"]}`)
	expect(rec, 201)
	var read issuedAgentTokenJSON
	decodeJSONBody(t, rec, &read)
	expect(call("POST", path(a, "/links"), linkBody, read.Token), 403)
}
