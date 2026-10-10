package v1

import (
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentAssignmentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, status string
		main, inTree, open bool
		want               int
	}{
		{name: "ordinary agent cannot clear", body: `{"assignee_id":null}`, want: 403},
		{name: "ordinary agent cannot take", body: `{"assignee_id":"01OTHER0000000000000000000"}`, want: 403},
		{name: "same assignee", body: `{"assignee_id":"` + testActorID + `"}`, want: 200},
		{name: "worker change", body: `{"working_agent_id":"` + testOtherAgentID + `"}`, want: 403},
		{name: "approved main", body: `{"assignee_id":null}`, status: "approved", main: true, inTree: true, want: 200},
		{name: "planning main", body: `{"assignee_id":null}`, status: "planning", main: true, inTree: true, want: 403},
		{name: "paused main", body: `{"assignee_id":null}`, status: "paused", main: true, inTree: true, want: 403},
		{name: "outside tree", body: `{"assignee_id":null}`, status: "approved", main: true, want: 403},
		{name: "confirmation", body: `{"assignee_id":null}`, status: "approved", main: true, inTree: true, open: true, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := ticketDetailFake()
			q.ticket.descendant = tc.inTree
			q.openConfirmation = tc.open
			if tc.main {
				q.mainDelegation = map[string]gen.GetActiveDelegationByMainActorRow{testMainActorID: {ID: "deleg", RootTicketID: testTicketID, Status: tc.status}}
			}
			req := patchReq(tc.body, `"3"`, "ticket.edit", "ticket.assign")
			req = req.WithContext(auth.NewPrincipalContext(req.Context(), &auth.Principal{ActorID: testMainActorID, ActorKind: auth.ActorKindAgent}))
			rec := httptest.NewRecorder()
			h, _ := ticketHandler(q)
			h.updateTicket(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden && len(q.ticket.updated) > 0 {
				t.Fatal("forbidden mutation persisted")
			}
		})
	}
}
func TestAgentCannotAssignOnCreation(t *testing.T) {
	q := ticketFake()
	h, _ := ticketHandler(q)
	rec := httptest.NewRecorder()
	h.createTicket(rec, ticketReqAsAgent(http.MethodPost, "/tickets", `{"type":"task","title":"bypass","assignee_id":"`+testActorID+`"}`, "", testOtherAgentID))
	if rec.Code != 403 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
