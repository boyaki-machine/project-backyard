package v1

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"net/http"
)

type workCleanupResult struct {
	WorkIDs      []string `json:"work_ids"`
	SkippedCount int      `json:"skipped_count"`
	MissingCount int      `json:"missing_count"`
	DryRun       bool     `json:"dry_run"`
	Detail       string   `json:"detail"`
}

// Caller provides a transaction. This service can also be called by server maintenance.
func cleanupTicketWork(ctx context.Context, q gen.Querier, projectID, ticketID string, dryRun bool) (workCleanupResult, error) {
	out := workCleanupResult{WorkIDs: []string{}, DryRun: dryRun, Detail: "実績属性は保持します。整理した計測明細は復元できません。"}
	if e := q.LockWorkProject(ctx, projectID); e != nil {
		return out, e
	}
	tickets, e := q.LockWorkTickets(ctx, gen.LockWorkTicketsParams{ProjectID: projectID, TargetTicketID: ticketID})
	if e != nil {
		return out, e
	}
	closed := map[string]bool{}
	for _, t := range tickets {
		closed[t.ID] = t.ClosedAt != nil
	}
	if !closed[ticketID] {
		return out, workError(apierr.Conflict, "終了済みチケットの作業情報だけ整理できます")
	}
	sessions, e := q.ListTicketCleanupWork(ctx, ticketID)
	if e != nil {
		return out, e
	}
	for _, s := range sessions {
		a, e := q.ListWorkAllocations(ctx, s.ID)
		if e != nil {
			return out, e
		}
		eligible := len(a) > 0 && closed[s.TicketID]
		ids := []string{}
		for _, x := range a {
			ids = append(ids, x.TicketID)
			eligible = eligible && closed[x.TicketID]
		}
		if !eligible {
			out.SkippedCount++
			continue
		}
		missing := s.State != "finished" || s.TimeMissing
		out.WorkIDs = append(out.WorkIDs, s.ID)
		if missing {
			out.MissingCount++
		}
		if dryRun {
			continue
		}
		for _, x := range a {
			base := gen.AddWorkBaseParams{TicketID: x.TicketID, SessionsCount: 1}
			if s.Incomplete || missing {
				base.IncompleteCount = 1
			}
			if !s.Tokens.Valid {
				base.TokensMissing = 1
			} else {
				base.Tokens = float64(s.Tokens.Int64) * x.Share
			}
			if missing {
				base.TimeMissing = 1
				base.ActiveMs = float64(s.ActiveMs) * x.Share
				base.ElapsedMs = float64(s.ActiveMs+s.WaitingMs) * x.Share
				base.WaitingMs = float64(s.WaitingMs) * x.Share
			} else {
				active := s.ActiveMs
				var p workPolicy
				if e = json.Unmarshal(s.Policy, &p); e != nil {
					return out, e
				}
				if p.TimeSource != "pb" && s.ReportedActiveMs.Valid {
					active = s.ReportedActiveMs.Int64
				}
				base.ActiveMs = float64(active) * x.Share
				base.ElapsedMs = float64(s.ActiveMs+s.WaitingMs) * x.Share
				base.WaitingMs = float64(s.WaitingMs) * x.Share
			}
			inputs := []workPointInput{}
			old, e := q.GetWorkBase(ctx, x.TicketID)
			if e == nil {
				if e = json.Unmarshal(old.PointInputs, &inputs); e != nil {
					return out, e
				}
			} else if !errors.Is(e, pgx.ErrNoRows) {
				return out, e
			}
			if s.EndedAt != nil {
				rows, e := q.GetLiveWorkPointInputs(ctx, x.TicketID)
				if e != nil {
					return out, e
				}
				for _, row := range rows {
					if row.EndedAt == nil || !row.EndedAt.Equal(*s.EndedAt) {
						continue
					}
					// Session ID resolves equal timestamps deterministically.
					if row.SessionID != s.ID {
						continue
					}
					var p workPolicy
					var m *changeMetrics
					if e = json.Unmarshal(row.Policy, &p); e != nil {
						return out, e
					}
					if len(row.ChangeMetrics) > 0 {
						if e = json.Unmarshal(row.ChangeMetrics, &m); e != nil {
							return out, e
						}
					}
					inputs = append(inputs, workPointInput{p, m, row.EndedAt.UnixNano()})
				}
			}
			base.PointInputs, e = json.Marshal(compactPointInputs(inputs))
			if e != nil {
				return out, e
			}
			if e = q.AddWorkBase(ctx, base); e != nil {
				return out, e
			}
		}
		ticketIDs, _ := json.Marshal(ids)
		if e = q.ArchiveWork(ctx, gen.ArchiveWorkParams{ID: s.ID, ProjectID: projectID, ActorID: usageText(s.ActorID), TicketIds: ticketIDs, TimeMissing: missing}); e != nil {
			return out, e
		}
		if e = q.ArchiveWorkEvents(ctx, s.ID); e != nil {
			return out, e
		}
		if e = q.DeleteWorkSession(ctx, s.ID); e != nil {
			return out, e
		}
	}
	return out, nil
}
func (h *handler) cleanupWork(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST work-cleanup")
	if !ok {
		return
	}
	var req struct {
		DryRun *bool `json:"dry_run"`
	}
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.DryRun == nil {
		apierr.Write(w, r, usageInvalid("dry_run", "対象確認はtrue、整理の実行はfalseを明示してください"))
		return
	}
	var result workCleanupResult
	e := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		var e error
		result, e = cleanupTicketWork(ctx, q, scope.projectID, ticketID, *req.DryRun)
		if e != nil {
			return e
		}
		if *req.DryRun || len(result.WorkIDs) == 0 {
			return nil
		}
		field, value := "work_cleanup", string(workJSON(result))
		return activity.FromRequest(r).Record(ctx, q, activity.Entry{ProjectID: scope.projectID, EntityType: activity.EntityTicket, EntityID: ticketID, Action: activity.Update, Field: &field, NewValue: &value})
	})
	if e != nil {
		var problem *apierr.Error
		if errors.As(e, &problem) {
			apierr.Write(w, r, problem)
		} else {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(e))
		}
		return
	}
	WriteJSON(w, 200, result)
}
func workJSON(v any) []byte { b, _ := json.Marshal(v); return b }
