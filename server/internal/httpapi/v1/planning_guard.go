package v1

import (
	"context"
	"errors"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"net/http"
)

// All affected tickets, including link counterparts and move anchors, must be
// inside the approved tree. The delegation row remains locked through the write.
func planningWriteProblem(ctx context.Context, q gen.Querier, r *http.Request, projectID string, ticketIDs ...string) *apierr.Error {
	p := auth.PrincipalFromContext(ctx)
	if p == nil || !p.IsAgent() {
		return nil
	}
	main, isMain, err := findActiveDelegationByMain(ctx, q, projectID, p.ActorID)
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	for _, id := range ticketIDs {
		d, e := q.GetPlanningDelegation(ctx, gen.GetPlanningDelegationParams{TicketID: id, ProjectID: projectID})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return apierr.New(apierr.InternalError).WithCause(e)
		}
		if errors.Is(e, pgx.ErrNoRows) {
			if isMain {
				return delegationForbidden(main.RootSeq, "委任の範囲外の計画は変更できません")
			}
			continue
		}
		if d.MainActorID != p.ActorID {
			return delegationForbidden(d.RootSeq, "この計画を変更できるのは委任のmainだけです")
		}
		if !delegationGate(d.Status) {
			return delegationForbidden(d.RootSeq, "計画が承認され、委任が実行中の場合だけ変更できます")
		}
		blocked, e := q.HasOpenConfirmation(ctx, d.ID)
		if e != nil {
			return apierr.New(apierr.InternalError).WithCause(e)
		}
		if blocked {
			return delegationForbidden(d.RootSeq, "確認待ちに答えられるまで計画は変更できません")
		}
	}
	return nil
}

func planningLinkProblem(ctx context.Context, q gen.Querier, r *http.Request, projectID, ticketID string, counterpart int32) *apierr.Error {
	p := auth.PrincipalFromContext(ctx)
	if p == nil || !p.IsAgent() {
		return nil
	}
	id, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{ProjectID: projectID, Seq: counterpart})
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	return planningWriteProblem(ctx, q, r, projectID, ticketID, id)
}

func preparePlanningMove(ctx context.Context, q gen.Querier, r *http.Request, projectID string, seq int32, req *moveTicketRequest) *apierr.Error {
	target, err := q.GetPlanningPosition(ctx, gen.GetPlanningPositionParams{ProjectID: projectID, Seq: seq})
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	if e := planningWriteProblem(ctx, q, r, projectID, target.ID); e != nil {
		return e
	}
	if req.Position != "" {
		siblings, e := q.ListPlanningSiblings(ctx, gen.ListPlanningSiblingsParams{ProjectID: projectID, ParentID: target.ParentID, Staged: target.Staged, ExceptID: target.ID})
		if e != nil {
			return apierr.New(apierr.InternalError).WithCause(e)
		}
		if len(siblings) == 0 {
			return nil
		}
		if req.Position == positionFirst {
			n := siblings[0].Seq
			req.BeforeSeq = &n
		} else {
			n := siblings[len(siblings)-1].Seq
			req.AfterSeq = &n
		}
		req.Position = ""
	}
	for _, anchor := range []*int32{req.AfterSeq, req.BeforeSeq} {
		if anchor == nil {
			continue
		}
		row, e := q.GetPlanningPosition(ctx, gen.GetPlanningPositionParams{ProjectID: projectID, Seq: *anchor})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.New(apierr.ValidationFailed).WithMessage("基準のチケットが見つかりません")
		}
		if e != nil {
			return apierr.New(apierr.InternalError).WithCause(e)
		}
		if row.ParentID != target.ParentID || row.Staged != target.Staged {
			return apierr.New(apierr.ValidationFailed).WithMessage("同じ親と段の中で並べ替えてください")
		}
		if e := planningWriteProblem(ctx, q, r, projectID, row.ID); e != nil {
			return e
		}
	}
	return nil
}
