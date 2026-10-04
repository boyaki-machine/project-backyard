// 委任（delegation）。Delegation.md 4章・12.2。pb-246。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/delegation   delegation.manage
//
// **{seq} は委任の根となるチケットを指す**（root_ticket_id が UNIQUE なので
// 1チケットにつき委任は1件）。承認（計画の合格・評価）は delegation_plan /
// evaluation（pb-247・pb-248）が無いと進められないので、作った直後の状態は
// 常に `planning` である——このエンドポイントは「委任を始める」だけを行う。
//
// **MCP ツールにしない**（Design.md 8.1 の「エージェントから見える面は MCP の
// み」の例外）。Delegation.md 15章（D7）が新設する5つの MCP ツールに委任の
// 作成は含まれておらず、`delegation.manage` は `project_admin` だけが持つ
// （5章・DbDesign.md 6.24）——これは人（PM）が画面・APIから直接行う操作である。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// uqDelegationRootTicket は delegation.root_ticket_id の一意制約名
// （Postgres の既定の命名: `<table>_<column>_key`）。
const uqDelegationRootTicket = "delegation_root_ticket_id_key"

// delegationView は委任1件の応答（Delegation.md 4.4）。
type delegationView struct {
	ID           string   `json:"id"`
	RootSeq      int32    `json:"root_seq"`
	Main         actorRef `json:"main"`
	Evaluator    actorRef `json:"evaluator"`
	Status       string   `json:"status"`
	Scope        any      `json:"scope"`
	ScopeVersion int32    `json:"scope_version"`
	CreatedBy    actorRef `json:"created_by"`
	CreatedAt    Time     `json:"created_at"`
	UpdatedAt    Time     `json:"updated_at"`
}

// createDelegationRequest は POST の本体。
type createDelegationRequest struct {
	MainActorID      string          `json:"main_actor_id"`
	EvaluatorActorID string          `json:"evaluator_actor_id"`
	Scope            json.RawMessage `json:"scope"`
}

// createDelegation は委任を1件始める（`planning` 状態で）。
func (h *handler) createDelegation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/delegation")
	if !ok {
		return
	}

	var req createDelegationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateCreateDelegation(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     delegationView
		invalid  *apierr.Error
		conflict bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **根であること**（execution_mode='delegated' かつ parent_id は
		// NULL のみ許す DB 制約 ck_ticket_delegated_no_parent と対になる）。
		ticket, err := q.GetTicketForDelegation(ctx, ticketID)
		if err != nil {
			return fmt.Errorf("チケットを読めない: %w", err)
		}
		if ticket.ParentID.Valid {
			invalid = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "seq", Code: "invalid",
				Message: "委任の根にできるのは、親を持たないチケットだけです",
			})
			return errTicketReference
		}

		for _, actor := range []struct {
			field string
			id    string
		}{{"main_actor_id", req.MainActorID}, {"evaluator_actor_id", req.EvaluatorActorID}} {
			usable, err := q.AgentUsableInProject(ctx, gen.AgentUsableInProjectParams{
				ActorID: actor.id, ProjectID: pgtype.Text{String: scope.projectID, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("エージェントを確認できない: %w", err)
			}
			if !usable {
				invalid = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: actor.field, Code: "not_found",
					Message: "このプロジェクトで使えるエージェントではありません",
				})
				return errTicketReference
			}
		}

		if err := q.CreateDelegation(ctx, gen.CreateDelegationParams{
			ID:               id,
			ProjectID:        scope.projectID,
			RootTicketID:     ticketID,
			MainActorID:      req.MainActorID,
			EvaluatorActorID: req.EvaluatorActorID,
			Scope:            req.Scope,
			CreatedBy:        scope.actorID,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == uqDelegationRootTicket {
				conflict = true
				return errTicketReference
			}
			return fmt.Errorf("委任を作成できない: %w", err)
		}

		// execution_mode を 'delegated' にする（D1）。
		if _, err := q.SetTicketExecutionMode(ctx, gen.SetTicketExecutionModeParams{
			ID: ticketID, ExecutionMode: "delegated",
		}); err != nil {
			return fmt.Errorf("execution_mode を更新できない: %w", err)
		}

		row, err := q.GetDelegationByID(ctx, id)
		if err != nil {
			return fmt.Errorf("作成した委任を読めない: %w", err)
		}
		view = delegationViewOf(row)

		return rec.Record(ctx, q, activity.Entry{
			ProjectID:  scope.projectID,
			EntityType: activity.EntityTicket,
			EntityID:   ticketID,
			Action:     activity.Update,
			Field:      strPtr("execution_mode"),
			NewValue:   strPtr("delegated"),
		})
	})
	switch {
	case invalid != nil:
		apierr.Write(w, r, invalid)
		return
	case conflict:
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage("このチケットには既に委任があります").
			WithDetails(apierr.Detail{Field: "seq", Code: "already_exists"}))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/delegation", scope.key, scope.seq))
	WriteJSON(w, http.StatusCreated, view)
}

// validateCreateDelegation は POST の本体を検証する。
func validateCreateDelegation(req createDelegationRequest) *apierr.Error {
	var details []apierr.Detail
	if req.MainActorID == "" {
		details = append(details, apierr.Detail{
			Field: "main_actor_id", Code: "required", Message: "main_actor_id を指定してください",
		})
	}
	if req.EvaluatorActorID == "" {
		details = append(details, apierr.Detail{
			Field: "evaluator_actor_id", Code: "required", Message: "evaluator_actor_id を指定してください",
		})
	}
	if req.MainActorID != "" && req.MainActorID == req.EvaluatorActorID {
		// ck_delegation_independent（DbDesign.md 6.24）と同じ条件を先に見る。
		details = append(details, apierr.Detail{
			Field: "evaluator_actor_id", Code: "invalid",
			Message: "main と evaluator には別のエージェントを指定してください",
		})
	}
	if len(req.Scope) == 0 {
		details = append(details, apierr.Detail{
			Field: "scope", Code: "required", Message: "scope を指定してください",
		})
	} else {
		var v any
		if err := json.Unmarshal(req.Scope, &v); err != nil {
			details = append(details, apierr.Detail{
				Field: "scope", Code: "invalid", Message: "scope は JSON で指定してください",
			})
		} else if _, ok := v.(map[string]any); !ok {
			details = append(details, apierr.Detail{
				Field: "scope", Code: "invalid", Message: "scope はオブジェクトで指定してください",
			})
		} else {
			// 予算の形（4.2。pb-253）。
			details = append(details, validateScopeBudget(req.Scope, "scope")...)
		}
	}
	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

func delegationViewOf(row gen.GetDelegationByIDRow) delegationView {
	var scope any
	_ = json.Unmarshal(row.Scope, &scope)
	return delegationView{
		ID:      row.ID,
		RootSeq: row.RootSeq,
		Main: actorRef{
			ID: row.MainActorID, Kind: row.MainActorKind, DisplayName: row.MainActorName,
		},
		Evaluator: actorRef{
			ID: row.EvaluatorActorID, Kind: row.EvaluatorActorKind, DisplayName: row.EvaluatorActorName,
		},
		Status:       row.Status,
		Scope:        scope,
		ScopeVersion: row.ScopeVersion,
		CreatedBy: actorRef{
			ID: row.CreatedByID, Kind: row.CreatedByKind, DisplayName: row.CreatedByName,
		},
		CreatedAt: Time(row.CreatedAt),
		UpdatedAt: Time(row.UpdatedAt),
	}
}

// ── 委任の木への強制（Delegation.md 5章・12.2）───────────────────

// delegationGate はこの状態で main が書き込んでよいかを返す（5章の表）。
//
// **`approved` だけが可である。** `planning`（評価の合格前）・`paused`
// （停止）・`ended`（終了）は、いずれも main の書き込みを拒む行として表に
// 並んでいる。評価待ち・差し戻しの細分（評価が要る）は pb-247・pb-248 まで
// 持たない——本関数は `delegation.status` だけで判定できる範囲に留める。
func delegationGate(status string) bool {
	return status == "approved"
}

// delegationForbidden は 403 に委任の段階を理由として載せる（5章・12.2）。
func delegationForbidden(rootSeq int32, reason string) *apierr.Error {
	return apierr.New(apierr.Forbidden).WithMessage(reason).WithDetails(apierr.Detail{
		Field: "parent_seq", Code: "delegation_stage",
		Message: fmt.Sprintf("根のチケット #%d の委任の段階により拒まれました", rootSeq),
	})
}

// findActiveDelegationByMain は 12.2 の判定に使う。呼び出し元がこのプロジェクト
// のどこかの委任の main でなければ nil を返す（第2戻り値は「該当なし」）。
func findActiveDelegationByMain(
	ctx context.Context, q gen.Querier, projectID, actorID string,
) (gen.GetActiveDelegationByMainActorRow, bool, error) {
	row, err := q.GetActiveDelegationByMainActor(ctx, gen.GetActiveDelegationByMainActorParams{
		ProjectID: projectID, MainActorID: actorID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.GetActiveDelegationByMainActorRow{}, false, nil
		}
		return gen.GetActiveDelegationByMainActorRow{}, false, err
	}
	return row, true, nil
}

// findDelegationContainingTicket は、与えたチケットがこのプロジェクトの
// どれかの委任の木（根またはその子孫）に属するかを調べる（12.2・5章）。
//
// **委任ごとに IsTicketDescendant を1回ずつ照合する**（ticket.sql）。
// プロジェクト内の現役の委任は少数であり、ticket_id ごとの新しい再帰 CTE を
// もう1本持つより単純である（delegation.sql の設計メモに同じ説明がある）。
func findDelegationContainingTicket(
	ctx context.Context, q gen.Querier, projectID, ticketID string,
) (gen.ListActiveDelegationsForProjectRow, bool, error) {
	delegations, err := q.ListActiveDelegationsForProject(ctx, projectID)
	if err != nil {
		return gen.ListActiveDelegationsForProjectRow{}, false, err
	}
	for _, d := range delegations {
		in, err := q.IsTicketDescendant(ctx, gen.IsTicketDescendantParams{
			AncestorID: d.RootTicketID, CandidateID: ticketID,
		})
		if err != nil {
			return gen.ListActiveDelegationsForProjectRow{}, false, err
		}
		if in {
			return d, true, nil
		}
	}
	return gen.ListActiveDelegationsForProjectRow{}, false, nil
}
