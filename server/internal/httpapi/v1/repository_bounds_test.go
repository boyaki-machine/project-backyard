package v1

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

func TestPlanVersionParamRejectsOverflow(t *testing.T) {
	for _, raw := range []string{"2147483648", "4294967297", "9223372036854775807"} {
		t.Run(raw, func(t *testing.T) {
			req := ticketReq(http.MethodGet, "/plans/"+raw, "", "31")
			chi.RouteContext(req.Context()).URLParams.Add("version", raw)
			_, e := planVersionParam(req)
			if e == nil || e.Status() != http.StatusUnprocessableEntity {
				t.Fatalf("overflowing plan version accepted: %+v", e)
			}
		})
	}
	req := ticketReq(http.MethodGet, "/plans/2147483647", "", "31")
	chi.RouteContext(req.Context()).URLParams.Add("version", "2147483647")
	version, e := planVersionParam(req)
	if e != nil || version != 2147483647 {
		t.Fatalf("valid plan version rejected: %d, %+v", version, e)
	}
}

func TestAgentRunStartTimeForLargeCost(t *testing.T) {
	for _, minutes := range []int32{0, 42, 153722868, 2147483647} {
		t.Run(strconv.FormatInt(int64(minutes), 10), func(t *testing.T) {
			run, err := buildAgentRun(context.Background(), reportFake(), "run", testTicketID,
				&auth.Principal{ActorID: testRunActorID}, reportRequest{Cost: &reportCost{WallClockMin: &minutes}})
			if err != nil {
				t.Fatal(err)
			}
			if got := run.EndedAt.Unix() - run.StartedAt.Unix(); got != int64(minutes)*60 {
				t.Fatalf("run length = %d seconds, want %d", got, int64(minutes)*60)
			}
			if run.StartedAt.Nanosecond() != run.EndedAt.Nanosecond() {
				t.Fatal("sub-second precision was lost")
			}
		})
	}
}
