package activity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

type activityQueryFake struct {
	gen.Querier
	entry gen.InsertActivityParams
}

func (q *activityQueryFake) InsertActivity(_ context.Context, entry gen.InsertActivityParams) error {
	q.entry = entry
	return nil
}

func TestRecorderSnapshotsUserAndAgent(t *testing.T) {
	for _, tc := range []struct{ kind, name, email string }{
		{auth.ActorKindUser, "担当者", "person@example.com"},
		{auth.ActorKindAgent, "AI担当", "owner@example.com"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			principal := &auth.Principal{ActorID: "01K00000000000000000000001", ActorKind: tc.kind, DisplayName: tc.name, Email: tc.email}
			req := httptest.NewRequest(http.MethodPost, "/projects/demo/tickets", nil)
			req = req.WithContext(auth.NewPrincipalContext(req.Context(), principal))
			q := &activityQueryFake{}
			if err := FromRequest(req).Record(req.Context(), q, Entry{ProjectID: "01K00000000000000000000002", EntityType: EntityTicket, EntityID: "01K00000000000000000000003", Action: Create}); err != nil {
				t.Fatal(err)
			}
			if q.entry.ActorKind.String != tc.kind || q.entry.ActorName.String != tc.name || q.entry.ActorID.String != principal.ActorID {
				t.Errorf("snapshot=%+v", q.entry)
			}
			if q.entry.ActorName.String == tc.email {
				t.Error("メールアドレスが名前に入った")
			}
		})
	}
}
