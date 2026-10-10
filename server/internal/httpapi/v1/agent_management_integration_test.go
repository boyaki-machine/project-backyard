package v1

import (
	"context"
	"fmt"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

type agentControlFixture struct {
	pool                                             *pgxpool.Pool
	r                                                http.Handler
	key, base, ownerID, ownerSession, managerSession string
	register                                         func(string) agentJSON
}

func newAgentControlFixture(t *testing.T) agentControlFixture {
	t.Helper()
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
	owner := ulidgen.New()
	manager := ulidgen.New()
	ownerEmail := "agent-owner-" + owner + "@example.com"
	managerEmail := "agent-manager-" + manager + "@example.com"
	seedUserWithRole(t, ctx, pool, q, owner, ownerEmail, auth.SystemRoleAdministrator)
	seedUserWithRole(t, ctx, pool, q, manager, managerEmail, auth.SystemRoleAdministrator)
	osession := loginAs(t, r, ownerEmail)
	msession := loginAs(t, r, managerEmail)
	key := "act-" + strings.ToLower(owner[len(owner)-8:])
	base := "/api/v1/projects/" + key
	agents := []string{}
	t.Cleanup(func() {
		if _, e := pool.Exec(ctx, `DELETE FROM project WHERE key=$1`, key); e != nil {
			t.Error(e)
		}
		for _, id := range agents {
			pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id=$1 OR target_id=$1`, id)
			if _, e := pool.Exec(ctx, `DELETE FROM actor WHERE id=$1`, id); e != nil {
				t.Error(e)
			}
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", osession, fmt.Sprintf(`{"key":%q,"name":"Agent control fixture","workflow_template":"simple"}`, key))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	register := func(name string) agentJSON {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/agents", osession, fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var a agentJSON
		decodeJSONBody(t, rec, &a)
		agents = append(agents, a.ID)
		return a
	}
	return agentControlFixture{pool, r, key, base, owner, osession, msession, register}
}
func bearerRequest(r http.Handler, method, path, token, body, version string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set("If-Match", version)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
func TestManagedAgentIntegration(t *testing.T) {
	f := newAgentControlFixture(t)
	ctx := context.Background()
	a := f.register("Managed target")
	var token issuedAgentTokenJSON
	rec := postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/tokens", f.ownerSession, `{"expires_in_days":30}`)
	decodeJSONBody(t, rec, &token)
	historyTicket := postWithCookie(f.r, f.base+"/tickets", f.ownerSession, `{"type":"task","title":"Retained comment history"}`)
	if historyTicket.Code != 201 {
		t.Fatal(historyTicket.Body.String())
	}
	var ticket struct{ Seq int32 }
	decodeJSONBody(t, historyTicket, &ticket)
	rec = bearerPost(f.r, fmt.Sprintf("%s/tickets/%d/comments", f.base, ticket.Seq), token.Token, `{"kind":"discussion","body_md":"Keep after manager stop"}`)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	rec = getWithCookie(f.r, f.base+"/agents", f.managerSession)
	if rec.Code != 200 {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list List[struct {
		managedAgent
		CreatedAt int64 `json:"created_at"`
	}]
	decodeJSONBody(t, rec, &list)
	if list.Total != 1 || list.Items[0].Owner.ID != f.ownerID {
		t.Fatalf("list=%+v", list)
	}
	rec = bearerRequest(f.r, "GET", f.base+"/agents", token.Token, "", "")
	if rec.Code != 403 {
		t.Fatalf("agent may manage: %d", rec.Code)
	}
	rec = bearerPost(f.r, f.base+"/agents/"+a.ID+"/disable", token.Token, `{}`)
	if rec.Code != 403 {
		t.Fatalf("agent stop status=%d", rec.Code)
	}
	rec = postWithCookie(f.r, f.base+"/agents/"+ulidgen.New()+"/disable", f.managerSession, `{}`)
	if rec.Code != 404 {
		t.Fatalf("missing status=%d", rec.Code)
	}

	memberID := ulidgen.New()
	memberEmail := "control-member-" + memberID + "@example.com"
	q := gen.New(f.pool)
	seedUserWithRole(t, ctx, f.pool, q, memberID, memberEmail, auth.SystemRoleOperator)
	if _, e := f.pool.Exec(ctx, `INSERT INTO project_member(project_id,actor_id,role_key) SELECT id,$1,'project_member' FROM project WHERE key=$2`, memberID, f.key); e != nil {
		t.Fatal(e)
	}
	memberSession := loginAs(t, f.r, memberEmail)
	rec = getWithCookie(f.r, f.base+"/agents", memberSession)
	if rec.Code != 403 {
		t.Fatalf("member list status=%d", rec.Code)
	}
	rec = postWithCookie(f.r, f.base+"/agents/"+a.ID+"/disable", memberSession, `{}`)
	if rec.Code != 403 {
		t.Fatalf("member stop status=%d", rec.Code)
	}
	otherKey := f.key + "-x"
	rec = postWithCookie(f.r, "/api/v1/projects", f.ownerSession, fmt.Sprintf(`{"key":%q,"name":"Other fixture","workflow_template":"simple"}`, otherKey))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	t.Cleanup(func() {
		if _, e := f.pool.Exec(ctx, `DELETE FROM project WHERE key=$1`, otherKey); e != nil {
			t.Error(e)
		}
	})
	rec = postWithCookie(f.r, "/api/v1/projects/"+otherKey+"/agents/"+a.ID+"/disable", f.managerSession, `{}`)
	if rec.Code != 404 {
		t.Fatalf("cross project stop status=%d", rec.Code)
	}
	// A project admin with an ordinary system role can manage the same target.
	if _, e := f.pool.Exec(ctx, `UPDATE project_member SET role_key='project_admin' WHERE actor_id=$1`, memberID); e != nil {
		t.Fatal(e)
	}
	rec = getWithCookie(f.r, f.base+"/agents", memberSession)
	if rec.Code != 200 {
		t.Fatalf("project admin list: %d %s", rec.Code, rec.Body.String())
	}
	// The owner no longer needs to belong to the project for a manager to stop the agent.
	if _, e := f.pool.Exec(ctx, `DELETE FROM project_member WHERE actor_id=$1`, f.ownerID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, session := range []string{f.managerSession, memberSession} {
		wg.Add(1)
		go func(session string) {
			defer wg.Done()
			statuses <- postWithCookie(f.r, f.base+"/agents/"+a.ID+"/disable", session, `{}`).Code
		}(session)
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 204 {
			t.Fatalf("concurrent stop status=%d", status)
		}
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM audit_log WHERE target_id=$1 AND action='agent.update'`, a.ID) != 1 {
		t.Fatal("concurrent stop duplicated audit")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM actor WHERE id=$1 AND is_active`, a.ID) != 0 {
		t.Fatal("actor still active")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM access_token WHERE actor_id=$1 AND revoked_at IS NULL`, a.ID) != 0 {
		t.Fatal("token not revoked")
	}
	rec = bearerRequest(f.r, "GET", f.base, token.Token, "", "")
	if rec.Code != 401 {
		t.Fatalf("token status=%d", rec.Code)
	}
	auditBefore := scalarInt(t, f.pool, `SELECT count(*) FROM audit_log WHERE target_id=$1 AND action='agent.update'`, a.ID)
	rec = postWithCookie(f.r, f.base+"/agents/"+a.ID+"/disable", f.managerSession, `{}`)
	if rec.Code != 204 {
		t.Fatalf("repeat status=%d", rec.Code)
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM audit_log WHERE target_id=$1 AND action='agent.update'`, a.ID) != auditBefore {
		t.Fatal("repeat duplicated audit")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM comment WHERE author_id=$1 AND body_md='Keep after manager stop'`, a.ID) != 1 {
		t.Fatal("comment history removed")
	}
	if scalarInt(t, f.pool, `SELECT count(*) FROM agent WHERE actor_id=$1`, a.ID) != 1 {
		t.Fatal("agent history removed")
	}
}
