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
	Done       int64 `json:"done"`
	InProgress int64 `json:"in_progress"`
	Todo       int64 `json:"todo"`
	NewTodo    int64 `json:"new_todo"`
	Unknown    int64 `json:"unknown"`
}

type ticketTrendDay struct {
	Date string `json:"date"`
	ticketTrendCounts
	IsPartial bool `json:"is_partial"`
}

type ticketTrendsView struct {
	Period    string            `json:"period"`
	Days      int               `json:"days"`
	Timezone  string            `json:"timezone"`
	StartDate string            `json:"start_date"`
	EndDate   string            `json:"end_date"`
	AsOf      Time              `json:"as_of"`
	Basis     string            `json:"basis"`
	Latest    ticketTrendCounts `json:"latest"`
	Daily     []ticketTrendDay  `json:"daily"`
}

func parseTrendPeriod(query url.Values) (string, *apierr.Error) {
	period := "7d"
	for name, values := range query {
		valid := len(values) == 1
		if valid {
			switch name {
			case "period":
				valid = values[0] == "7d" || values[0] == "4w" || values[0] == "3m" || values[0] == "6m"
				period = values[0]
			case "days":
				valid = values[0] == "7" || values[0] == "28"
				if values[0] == "28" {
					period = "4w"
				}
			default:
				valid = false
			}
		}
		if !valid {
			return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: name, Code: "invalid", Message: "集計期間は7日・4週間・3ヶ月・半年から指定してください",
			})
		}
	}
	if query.Has("period") && query.Has("days") {
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "period", Code: "invalid", Message: "periodとdaysは同時に指定できません",
		})
	}
	return period, nil
}

// Status counts and calendar boundaries share a single SQL snapshot.
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
	period, apiErr := parseTrendPeriod(query)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}
	rows, err := h.q.GetProjectTicketTrends(r.Context(), gen.GetProjectTicketTrendsParams{
		ProjectID: projectID, Period: period, AsOf: time.Now().UTC().Truncate(time.Millisecond),
	})
	if err != nil || len(rows) < 7 || len(rows) > 184 {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
			fmt.Errorf("チケットの状態別推移を読めない: rows=%d: %v", len(rows), err)))
		return
	}
	v := ticketTrendsView{
		Period: period, Days: len(rows), Timezone: rows[0].Timezone, StartDate: rows[0].Date,
		EndDate: rows[len(rows)-1].Date, AsOf: Time(rows[0].AsOf),
		Basis: "reconstructed_status_for_existing_tickets", Daily: make([]ticketTrendDay, 0, len(rows)),
	}
	for _, row := range rows {
		counts := ticketTrendCounts{row.Done, row.InProgress, row.Todo, row.NewTodo, row.Unknown}
		v.Daily = append(v.Daily, ticketTrendDay{row.Date, counts, row.IsPartial})
		v.Latest = counts
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, v)
}
