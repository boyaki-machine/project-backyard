package v1

import (
	"context"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// assignmentWriteProblem applies the same boundary to creation and reassignment.
// ticketID is the existing ticket, or the proposed parent when creating a child.
func assignmentWriteProblem(ctx context.Context, q gen.Querier, p *auth.Principal, projectID, ticketID string) *apierr.Error {
	if !p.IsAgent() {
		return nil
	}
	denied := apierr.New(apierr.Forbidden).WithMessage("担当・実行者は人が設定するか、承認済みの自律実行のmainが管理範囲内で変更してください")
	d, found, err := findActiveDelegationByMain(ctx, q, projectID, p.ActorID)
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	if !found || !delegationGate(d.Status) || ticketID == "" {
		return denied
	}
	open, err := q.HasOpenConfirmation(ctx, d.ID)
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	if open {
		return denied
	}
	in, err := q.IsTicketDescendant(ctx, gen.IsTicketDescendantParams{AncestorID: d.RootTicketID, CandidateID: ticketID})
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	if !in {
		return denied
	}
	// A nested delegation belongs to its own main.
	target, contained, err := findDelegationContainingTicket(ctx, q, projectID, ticketID)
	if err != nil {
		return apierr.New(apierr.InternalError).WithCause(err)
	}
	if contained && target.ID != d.ID {
		return denied
	}
	return nil
}
