package v1

import (
	"net/http"
	"testing"
)

func TestTicketSeqParamRejectsOverflow(t *testing.T) {
	for _, raw := range []string{"2147483648", "4294967327", "9223372036854775807"} {
		t.Run(raw, func(t *testing.T) {
			req := ticketReq(http.MethodGet, "/api/v1/projects/demo/tickets/"+raw, "", raw)
			_, e := ticketSeqParam(req)
			if e == nil || e.Status() != http.StatusNotFound {
				t.Fatalf("overflowing seq accepted: %+v", e)
			}
		})
	}
	req := ticketReq(http.MethodGet, "/api/v1/projects/demo/tickets/2147483647", "", "2147483647")
	seq, e := ticketSeqParam(req)
	if e != nil || seq != 2147483647 {
		t.Fatalf("valid seq rejected: %d, %+v", seq, e)
	}
}
