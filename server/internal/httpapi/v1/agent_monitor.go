package v1

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
)

type monitorLimits struct {
	MaxAttempts            *int32 `json:"max_attempts"`
	MaxConsecutiveFailures *int32 `json:"max_consecutive_failures"`
}
type monitorState struct {
	monitorLimits
	Attempts            int      `json:"attempts"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	Stopped             bool     `json:"stopped"`
	Reasons             []string `json:"reasons"`
	LegacyUnknown       bool     `json:"legacy_unknown"`
}

func monitorPolicy(ctx context.Context, q gen.Querier, id string) (monitorLimits, error) {
	data, e := q.GetAgentMonitorSettings(ctx, id)
	if e != nil {
		return monitorLimits{}, e
	}
	var p monitorLimits
	e = json.Unmarshal(data, &p)
	return p, e
}
func summarizeAttempts(limits monitorLimits, rows []gen.AgentAttempt, ticketID string, starting bool) monitorState {
	s := monitorState{monitorLimits: limits, Reasons: []string{}, LegacyUnknown: true}
	successful := false
	open := false
	for _, a := range rows {
		if a.TicketID == ticketID {
			s.Attempts++
			if a.EndedAt == nil {
				open = true
			}
		}
		if a.EndedAt == nil || successful {
			continue
		}
		switch a.Outcome.String {
		case "completed":
			successful = true
		case "failed", "abandoned":
			s.ConsecutiveFailures++
		}
	}
	if limits.MaxAttempts != nil && ticketID != "" && s.Attempts >= int(*limits.MaxAttempts) && (starting || !open) {
		s.Reasons = append(s.Reasons, "attempt_limit")
	}
	if limits.MaxConsecutiveFailures != nil && s.ConsecutiveFailures >= int(*limits.MaxConsecutiveFailures) {
		s.Reasons = append(s.Reasons, "consecutive_failures")
	}
	s.Stopped = len(s.Reasons) > 0
	return s
}
func agentMonitorState(ctx context.Context, q gen.Querier, projectID, actorID, ticketID string, starting bool) (monitorState, error) {
	p, e := monitorPolicy(ctx, q, projectID)
	if e != nil {
		return monitorState{}, e
	}
	rows, e := q.ListAgentAttempts(ctx, gen.ListAgentAttemptsParams{ProjectID: projectID, ActorID: actorID})
	if e != nil {
		return monitorState{}, e
	}
	s := summarizeAttempts(p, rows, ticketID, starting)
	if ticketID == "" {
		seen := map[string]bool{}
		for _, a := range rows {
			if !seen[a.TicketID] {
				seen[a.TicketID] = true
				t := summarizeAttempts(p, rows, a.TicketID, false)
				if contains(t.Reasons, "attempt_limit") && !contains(s.Reasons, "attempt_limit") {
					s.Stopped = true
					s.Reasons = append(s.Reasons, "attempt_limit")
				}
				if t.Attempts > s.Attempts {
					s.Attempts = t.Attempts
				}
			}
		}
	}
	return s, nil
}
func monitorWriteProblem(ctx context.Context, q gen.Querier, p *auth.Principal, projectID, ticketID string, starting bool) *apierr.Error {
	if !p.IsAgent() {
		return nil
	}
	limits, e := monitorPolicy(ctx, q, projectID)
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	if limits.MaxAttempts == nil && limits.MaxConsecutiveFailures == nil {
		return nil
	}
	rows, e := q.ListAgentAttempts(ctx, gen.ListAgentAttemptsParams{ProjectID: projectID, ActorID: p.ActorID})
	if e != nil {
		return apierr.New(apierr.InternalError).WithCause(e)
	}
	s := summarizeAttempts(limits, rows, ticketID, starting)
	if !s.Stopped {
		return nil
	}
	return apierr.New(apierr.Forbidden).WithMessage("実行の停止条件に達しました。所有者またはPJ管理者が理由付きで解除してください").WithDetails(apierr.Detail{Field: "agent", Code: "agent_monitor_stopped", Message: strings.Join(s.Reasons, ",")})
}

// Project-bound credentials cannot bypass a tripped monitor by using another business endpoint.
func (h *handler) agentMonitorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.PrincipalFromContext(r.Context())
		path := r.URL.Path
		if p == nil || !p.IsAgent() || p.ProjectID == "" || r.Method == "GET" || r.Method == "HEAD" || !strings.HasPrefix(path, "/api/v1/projects/") || strings.HasSuffix(path, "/reports") || strings.Contains(path, "/work-sessions") || strings.HasSuffix(path, "/evaluations") {
			next.ServeHTTP(w, r)
			return
		}
		ticketID := ""
		parts := strings.Split(path, "/")
		if len(parts) > 6 && parts[5] == "tickets" {
			if seq, e := strconv.ParseInt(parts[6], 10, 32); e == nil {
				t, e := h.q.GetTicketBySeq(r.Context(), gen.GetTicketBySeqParams{ProjectID: p.ProjectID, Seq: int32(seq)})
				if e == nil {
					ticketID = t.ID
				} else if !errors.Is(e, pgx.ErrNoRows) {
					apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
					return
				}
			}
		}
		if e := monitorWriteProblem(r.Context(), h.q, p, p.ProjectID, ticketID, false); e != nil {
			apierr.Write(w, r, e)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h *handler) getAgentMonitorPolicy(w http.ResponseWriter, r *http.Request) {
	_, _, id, ok := projectScopeContext(w, r, h.q, "GET agent monitor")
	if !ok {
		return
	}
	p, e := monitorPolicy(r.Context(), h.q, id)
	if e != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	WriteJSON(w, 200, p)
}
func (h *handler) putAgentMonitorPolicy(w http.ResponseWriter, r *http.Request) {
	p, _, id, ok := projectScopeContext(w, r, h.q, "PUT agent monitor")
	if !ok {
		return
	}
	if p.IsAgent() {
		apierr.Write(w, r, apierr.New(apierr.Forbidden))
		return
	}
	var req monitorLimits
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.MaxAttempts != nil && *req.MaxAttempts < 1 || req.MaxConsecutiveFailures != nil && *req.MaxConsecutiveFailures < 1 {
		apierr.Write(w, r, usageInvalid("limits", "停止上限は1以上、または未設定を指定してください"))
		return
	}
	data, _ := json.Marshal(req)
	ctx := r.Context()
	e := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if e := q.LockWorkProject(ctx, id); e != nil {
			return e
		}
		if e := q.SetAgentMonitorSettings(ctx, gen.SetAgentMonitorSettingsParams{ProjectID: id, Policy: data}); e != nil {
			return e
		}
		return audit.FromRequest(r).Record(ctx, q, audit.Entry{Action: audit.ProjectUpdate, Result: audit.Success, TargetType: "project", TargetID: id, Detail: map[string]any{"agent_monitor": req}})
	})
	if e != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	WriteJSON(w, 200, req)
}
func (h *handler) agentMonitor(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil || p.IsAgent() {
		apierr.Write(w, r, apierr.New(apierr.Forbidden))
		return
	}
	id := chi.URLParam(r, "id")
	projectID := ""
	ctx := r.Context()
	if chi.URLParam(r, "key") != "" {
		_, _, pid, ok := projectScopeContext(w, r, h.q, "agent monitor")
		if !ok {
			return
		}
		projectID = pid
	} else {
		a, e := h.q.FindMyAgent(ctx, gen.FindMyAgentParams{ActorID: id, OwnerActorID: p.ActorID})
		if errors.Is(e, pgx.ErrNoRows) {
			apierr.Write(w, r, apierr.New(apierr.NotFound))
			return
		}
		if e != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
			return
		}
		projectID = a.ProjectID.String
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Method == "POST" {
		if e := decodeJSON(r, &req); e != nil {
			apierr.Write(w, r, e)
			return
		}
		req.Reason = strings.TrimSpace(req.Reason)
		if req.Reason == "" || len([]rune(req.Reason)) > 2000 {
			apierr.Write(w, r, usageInvalid("reason", "解除理由を2000文字以内で指定してください"))
			return
		}
	}
	var state monitorState
	e := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if e := q.LockWorkProject(ctx, projectID); e != nil {
			return e
		}
		if _, e := q.LockProjectAgent(ctx, gen.LockProjectAgentParams{ProjectID: text(projectID), ActorID: id}); e != nil {
			return e
		}
		if r.Method == "POST" {
			if e := q.ResetAgentMonitor(ctx, gen.ResetAgentMonitorParams{ProjectID: projectID, ActorID: id, Reason: req.Reason}); e != nil {
				return e
			}
			if e := audit.FromRequest(r).Record(ctx, q, audit.Entry{Action: audit.AgentUpdate, Result: audit.Success, TargetType: "agent", TargetID: id, Detail: map[string]any{"monitor_reset_reason": req.Reason, "project_id": projectID}}); e != nil {
				return e
			}
		}
		var e error
		state, e = agentMonitorState(ctx, q, projectID, id, "", false)
		return e
	})
	if errors.Is(e, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound))
		return
	}
	if e != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	WriteJSON(w, 200, state)
}

type attributeRequest struct {
	TrustLevel   *int32    `json:"trust_level"`
	Capabilities *[]string `json:"capabilities"`
}

func validateAttributes(a *attributeRequest) *apierr.Error {
	if a.TrustLevel != nil && (*a.TrustLevel < 0 || *a.TrustLevel > 3) {
		return usageInvalid("trust_level", "信頼度は0〜3で指定してください")
	}
	if a.Capabilities != nil {
		if len(*a.Capabilities) > 32 {
			return usageInvalid("capabilities", "能力ラベルは32個以内で指定してください")
		}
		labels := []string{}
		seen := map[string]bool{}
		for _, x := range *a.Capabilities {
			x = strings.TrimSpace(x)
			if x == "" || len([]rune(x)) > 64 {
				return usageInvalid("capabilities", "能力ラベルは1〜64文字で指定してください")
			}
			if !seen[x] {
				seen[x] = true
				labels = append(labels, x)
			}
		}
		a.Capabilities = &labels
	}
	return nil
}
func saveAttributes(ctx context.Context, q gen.Querier, id string, a attributeRequest) error {
	var caps []byte
	if a.Capabilities != nil {
		caps, _ = json.Marshal(a.Capabilities)
	}
	return q.UpdateAgentAttributes(ctx, gen.UpdateAgentAttributesParams{ActorID: id, TrustLevel: int4OrNull(a.TrustLevel), Capabilities: caps})
}
func (h *handler) patchAgentAttributes(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "PATCH agent attributes")
	if !ok {
		return
	}
	if p.IsAgent() {
		apierr.Write(w, r, apierr.New(apierr.Forbidden))
		return
	}
	var req attributeRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateAttributes(&req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	e := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if _, e := q.LockProjectAgent(ctx, gen.LockProjectAgentParams{ProjectID: text(projectID), ActorID: id}); e != nil {
			return e
		}
		if e := saveAttributes(ctx, q, id, req); e != nil {
			return e
		}
		return audit.FromRequest(r).Record(ctx, q, audit.Entry{Action: audit.AgentUpdate, Result: audit.Success, TargetType: "agent", TargetID: id, Detail: map[string]any{"attributes": req, "project_id": projectID}})
	})
	if errors.Is(e, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound))
		return
	}
	if e != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		return
	}
	w.WriteHeader(204)
}
