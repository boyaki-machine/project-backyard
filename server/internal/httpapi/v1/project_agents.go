package v1

import (
	"encoding/json"
	"errors"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"net/http"
)

type managedAgent struct {
	Capabilities []string `json:"capabilities"`
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	IsActive     bool     `json:"is_active"`
	ClientKind   string   `json:"client_kind"`
	ModelName    *string  `json:"model_name"`
	ModelVersion *string  `json:"model_version"`
	TrustLevel   int32    `json:"trust_level"`
	Owner        ownerRef `json:"owner"`
	CreatedAt    Time     `json:"created_at"`
}

func (h *handler) listProjectAgents(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "GET project agents")
	if !ok {
		return
	}
	if p.IsAgent() {
		apierr.Write(w, r, apierr.New(apierr.Forbidden))
		return
	}
	page, e := ParsePage(r, SortSpec{Allowed: []string{"display_name"}, DefaultSort: "display_name", DefaultOrder: OrderAsc})
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rows, err := h.q.ListProjectAgents(r.Context(), gen.ListProjectAgentsParams{ProjectID: text(projectID), PageLimit: int32(page.Limit()), PageOffset: int32(page.Offset())})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	total, err := h.q.CountProjectAgents(r.Context(), text(projectID))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	items := []managedAgent{}
	for _, a := range rows {
		items = append(items, managedAgent{ID: a.ActorID, DisplayName: a.DisplayName, IsActive: a.IsActive, ClientKind: a.ClientKind, ModelName: textPtr(a.ModelName), ModelVersion: textPtr(a.ModelVersion), TrustLevel: a.TrustLevel, Capabilities: decodeCapabilities(a.Capabilities), Owner: ownerRef{ID: a.OwnerActorID, DisplayName: a.OwnerName}, CreatedAt: Time(a.CreatedAt)})
	}
	WriteJSON(w, 200, NewList(items, page, int(total)))
}
func (h *handler) disableProjectAgent(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "POST disable agent")
	if !ok {
		return
	}
	if p.IsAgent() {
		apierr.Write(w, r, apierr.New(apierr.Forbidden))
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	rec := audit.FromRequest(r)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		a, err := q.LockProjectAgent(ctx, gen.LockProjectAgentParams{ProjectID: text(projectID), ActorID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(apierr.NotFound)
		}
		if err != nil {
			return err
		}
		if err = q.DisableProjectAgent(ctx, id); err != nil {
			return err
		}
		n, err := q.RevokeAllAgentTokens(ctx, id)
		if err != nil {
			return err
		}
		if !a.IsActive && n == 0 {
			return nil
		}
		return rec.Record(ctx, q, audit.Entry{Action: audit.AgentUpdate, Result: audit.Success, TargetType: "agent", TargetID: id, Detail: map[string]any{"is_active": false, "project_id": projectID, "revoked_tokens": n}})
	})
	if err != nil {
		var e *apierr.Error
		if errors.As(err, &e) {
			apierr.Write(w, r, e)
		} else {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		}
		return
	}
	w.WriteHeader(204)
}

func decodeCapabilities(data []byte) []string {
	labels := []string{}
	_ = json.Unmarshal(data, &labels)
	if labels == nil {
		return []string{}
	}
	return labels
}
