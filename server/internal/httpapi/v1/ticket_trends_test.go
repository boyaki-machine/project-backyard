package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

func TestTicketTrendsQuery(t *testing.T) {
	for _, tc := range []struct {
		query  string
		days   int
		status int
	}{
		{"", 7, 200}, {"?days=7", 7, 200}, {"?days=28", 28, 200}, {"?period=7d", 7, 200}, {"?period=4w", 28, 200}, {"?period=3m", 92, 200}, {"?period=6m", 183, 200},
		{"?period=", 0, 422}, {"?period=90d", 0, 422}, {"?period=7d&period=4w", 0, 422}, {"?period=4w&days=28", 0, 422},
		{"?days=", 0, 422}, {"?days=0", 0, 422}, {"?days=07", 0, 422},
		{"?days=8", 0, 422}, {"?days=7&days=28", 0, 422}, {"?timezone=UTC", 0, 422},
		{"?days=7;unknown=x", 0, 422}, {"?days=%ZZ", 0, 422},
	} {
		t.Run(tc.query, func(t *testing.T) {
			q := dashFake()
			h := &handler{q: q}
			// A malformed escape cannot be supplied to httptest.NewRequest.
			req := dashReq(http.MethodGet, "/api/v1/projects/demo/ticket-trends")
			if len(tc.query) > 0 {
				req.URL.RawQuery = tc.query[1:]
			}
			rec := httptest.NewRecorder()
			h.getTicketTrends(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.status != 200 {
				if q.ticket.trendIn.Period != "" {
					t.Fatal("invalid query reached SQL")
				}
				return
			}
			var v ticketTrendsView
			if err := decodeTrendJSON(rec.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if v.Days != tc.days || len(v.Daily) != int(tc.days) || q.ticket.trendIn.ProjectID != testProjectID {
				t.Fatalf("response=%+v params=%+v", v, q.ticket.trendIn)
			}
			if q.ticket.trendIn.AsOf.IsZero() || v.Timezone != "Asia/Tokyo" || !v.Daily[len(v.Daily)-1].IsPartial {
				t.Fatalf("metadata=%+v", v)
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("ETag") != "" {
				t.Fatal("incorrect caching")
			}
		})
	}
}

func TestTicketTrendsLatestAndFailure(t *testing.T) {
	q := dashFake()
	q.ticket.trendRows = make([]gen.GetProjectTicketTrendsRow, 7)
	for i := range q.ticket.trendRows {
		q.ticket.trendRows[i] = gen.GetProjectTicketTrendsRow{Date: time.Date(2026, 10, i+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), AsOf: time.Now(), Timezone: "Asia/Tokyo", IsPartial: i == 6}
	}
	q.ticket.trendRows[0].Todo = 1
	q.ticket.trendRows[1].Done = 2
	q.ticket.trendRows[1].InProgress = 1
	q.ticket.trendRows[2].Unknown = 3
	q.ticket.trendRows[6].Todo = 5
	q.ticket.trendRows[6].NewTodo = 2
	rec := httptest.NewRecorder()
	(&handler{q: q}).getTicketTrends(rec, dashReq("GET", "/api/v1/projects/demo/ticket-trends"))
	var v ticketTrendsView
	if err := decodeTrendJSON(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Latest != (ticketTrendCounts{0, 0, 5, 2, 0}) || v.Basis != "reconstructed_status_for_existing_tickets" {
		t.Fatalf("totals=%+v", v)
	}
	q.ticket.trendErr = errors.New("boom")
	rec = httptest.NewRecorder()
	(&handler{q: q}).getTicketTrends(rec, dashReq("GET", "/api/v1/projects/demo/ticket-trends"))
	if rec.Code != 500 {
		t.Fatalf("status=%d", rec.Code)
	}
	q.ticket.trendErr = nil
	q.ticket.trendRows = q.ticket.trendRows[:1]
	rec = httptest.NewRecorder()
	(&handler{q: q}).getTicketTrends(rec, dashReq("GET", "/api/v1/projects/demo/ticket-trends"))
	if rec.Code != 500 {
		t.Fatal("incomplete calendar silently succeeded")
	}
}

// Time has an output-only marshaler; decode the wire timestamp as milliseconds.
func decodeTrendJSON(data []byte, v *ticketTrendsView) error {
	type alias ticketTrendsView
	wire := struct {
		*alias
		AsOf int64 `json:"as_of"`
	}{alias: (*alias)(v)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	v.AsOf = Time(time.UnixMilli(wire.AsOf))
	return nil
}
