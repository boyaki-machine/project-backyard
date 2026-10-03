package v1

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

type ticketTrendCounts struct {
	Created                 int64 `json:"created"`
	Completed               int64 `json:"completed"`
	Reopened                int64 `json:"reopened"`
	UnclassifiedTransitions int64 `json:"unclassified_transitions"`
}

type ticketTrendDay struct {
	Date string `json:"date"`
	ticketTrendCounts
	IsPartial bool `json:"is_partial"`
}

type ticketTrendsView struct {
	Days      int32             `json:"days"`
	Timezone  string            `json:"timezone"`
	StartDate string            `json:"start_date"`
	EndDate   string            `json:"end_date"`
	AsOf      Time              `json:"as_of"`
	Basis     string            `json:"basis"`
	Totals    ticketTrendCounts `json:"totals"`
	Daily     []ticketTrendDay  `json:"daily"`
}

func parseTrendDays(query url.Values) (int32, *apierr.Error) {
	for name, values := range query {
		if name != "days" || len(values) != 1 || (values[0] != "7" && values[0] != "28") {
			return 0, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: name, Code: "invalid", Message: "集計期間は7日または28日で指定してください",
			})
		}
	}
	if query.Get("days") == "28" {
		return 28, nil
	}
	return 7, nil
}

// getTicketTrends counts recorded operations, never inferred operations from closed_at.
// Calendar boundaries, classification and existing-ticket scope share one SQL snapshot.
func (h *handler) getTicketTrends(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/ticket-trends")
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "days", Code: "invalid", Message: "集計期間の指定を確認してください",
		}))
		return
	}
	days, apiErr := parseTrendDays(query)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}
	rows, err := h.q.GetProjectTicketTrends(r.Context(), gen.GetProjectTicketTrendsParams{
		ProjectID: projectID, Days: days, AsOf: time.Now().UTC().Truncate(time.Millisecond),
	})
	if err != nil || len(rows) != int(days) {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
			fmt.Errorf("期間内の増加・消化を読めない: rows=%d: %v", len(rows), err)))
		return
	}
	v := ticketTrendsView{
		Days: days, Timezone: rows[0].Timezone, StartDate: rows[0].Date,
		EndDate: rows[len(rows)-1].Date, AsOf: Time(rows[0].AsOf),
		Basis: "recorded_activity_for_existing_tickets", Daily: make([]ticketTrendDay, 0, days),
	}
	for _, row := range rows {
		counts := ticketTrendCounts{row.Created, row.Completed, row.Reopened, row.UnclassifiedTransitions}
		v.Daily = append(v.Daily, ticketTrendDay{row.Date, counts, row.IsPartial})
		v.Totals.Created += row.Created
		v.Totals.Completed += row.Completed
		v.Totals.Reopened += row.Reopened
		v.Totals.UnclassifiedTransitions += row.UnclassifiedTransitions
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, v)
}
