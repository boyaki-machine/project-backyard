package v1

import (
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"testing"
	"time"
)

func TestMonitorCountsExplicitOutcomes(t *testing.T) {
	now := time.Now()
	limit := int32(2)
	rows := []gen.AgentAttempt{{TicketID: "t", EndedAt: &now, Outcome: txt("failed")}, {TicketID: "t", EndedAt: &now}, {TicketID: "t", EndedAt: &now, Outcome: txt("abandoned")}, {TicketID: "t", EndedAt: &now, Outcome: txt("completed")}, {TicketID: "t", EndedAt: &now, Outcome: txt("failed")}}
	s := summarizeAttempts(monitorLimits{MaxConsecutiveFailures: &limit}, rows, "t", true)
	if s.ConsecutiveFailures != 2 || !s.Stopped || s.Attempts != 5 {
		t.Fatalf("state=%+v", s)
	}
	rows[0].Outcome = txt("completed")
	s = summarizeAttempts(monitorLimits{MaxConsecutiveFailures: &limit}, rows, "t", true)
	if s.ConsecutiveFailures != 0 || s.Stopped {
		t.Fatalf("success did not reset: %+v", s)
	}
	s = summarizeAttempts(monitorLimits{MaxAttempts: &limit}, []gen.AgentAttempt{{TicketID: "t"}, {TicketID: "t", EndedAt: &now}}, "t", false)
	if s.Stopped {
		t.Fatal("active final allowed attempt was blocked")
	}
	s = summarizeAttempts(monitorLimits{MaxAttempts: &limit}, []gen.AgentAttempt{{TicketID: "t"}, {TicketID: "t", EndedAt: &now}}, "t", true)
	if !s.Stopped {
		t.Fatal("new attempt above limit allowed")
	}
}
