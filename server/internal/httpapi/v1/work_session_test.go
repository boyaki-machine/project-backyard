package v1

import (
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"testing"
	"time"
)

func TestWorkTimingAndPoint(t *testing.T) {
	start := time.Unix(0, 0)
	s := gen.WorkSession{State: "running", ChangedAt: start}
	advanceWork(&s, start.Add(30*time.Minute))
	s.State = "waiting"
	advanceWork(&s, start.Add(90*time.Minute))
	s.State = "running"
	advanceWork(&s, start.Add(120*time.Minute))
	if s.ActiveMs != 3600000 || s.WaitingMs != 3600000 {
		t.Fatalf("%+v", s)
	}

}
func TestWorkValidation(t *testing.T) {
	for _, r := range []workRequest{
		{RequestID: "r", Action: "start", Allocations: []workAllocation{{1, .5}, {2, .4}}, Note: "share"},
		{RequestID: "r", Action: "start", Allocations: []workAllocation{{1, .5}, {1, .5}}, Note: "share"},
		{RequestID: "r", Action: "pause"}, {RequestID: "r", Action: "finish"},
		{RequestID: "r", Action: "start", Allocations: []workAllocation{{1, .5}, {2, .5}}},
	} {
		if validateWork(r) == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	if e := validateWork(workRequest{RequestID: "r", Action: "start", Note: "shared", Allocations: []workAllocation{{1, .5}, {2, .5}}}); e != nil {
		t.Fatal(e)
	}
}
