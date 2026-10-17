package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

type usageCounts struct {
	FailedCount             int64 `json:"failed_count"`
	AbandonedCount          int64 `json:"abandoned_count"`
	RunsCount               int64 `json:"runs_count"`
	TicketsCount            int64 `json:"tickets_count"`
	CompletedCount          int64 `json:"completed_count"`
	PartialCount            int64 `json:"partial_count"`
	BlockedCount            int64 `json:"blocked_count"`
	RunTokens               int64 `json:"run_tokens"`
	RunTokensMissing        int64 `json:"run_tokens_missing"`
	EvaluationCount         int64 `json:"evaluation_count"`
	EvaluationTokens        int64 `json:"evaluation_tokens"`
	EvaluationTokensMissing int64 `json:"evaluation_tokens_missing"`
	Turns                   int64 `json:"turns"`
	TurnsMissing            int64 `json:"turns_missing"`
	WallClockMin            int64 `json:"wall_clock_min"`
	WallClockMinMissing     int64 `json:"wall_clock_min_missing"`
}
type usageDay struct {
	Date string `json:"date"`
	usageCounts
}
type usageModel struct {
	ClientKind   *string `json:"client_kind"`
	ModelName    *string `json:"model_name"`
	ModelVersion *string `json:"model_version"`
	usageCounts
}
type agentUsageView struct {
	FromDate string       `json:"from_date"`
	ToDate   string       `json:"to_date"`
	Timezone string       `json:"timezone"`
	Totals   usageCounts  `json:"totals"`
	Daily    []usageDay   `json:"daily"`
	Models   []usageModel `json:"models"`
}
type usageRunView struct {
	RunID                string  `json:"run_id"`
	Seq                  int32   `json:"seq"`
	SubmittedAt          Time    `json:"submitted_at"`
	Status               string  `json:"status"`
	TicketStatusCategory string  `json:"ticket_status_category"`
	ClientKind           *string `json:"client_kind"`
	ModelName            *string `json:"model_name"`
	ModelVersion         *string `json:"model_version"`
	Tokens               *int64  `json:"tokens"`
	Turns                *int64  `json:"turns"`
	WallClockMin         *int64  `json:"wall_clock_min"`
}
type usagePeriod struct {
	from, until                time.Time
	fromDate, toDate, timezone string
	loc                        *time.Location
}

func usageInvalid(field, message string) *apierr.Error {
	return apierr.New(apierr.ValidationFailed).WithMessage(message).WithDetails(apierr.Detail{Field: field, Code: "invalid", Message: message})
}
func parseUsagePeriod(v url.Values, tz string, now time.Time) (usagePeriod, *apierr.Error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return usagePeriod{}, apierr.New(apierr.InternalError).WithCause(err)
	}
	from, to := v.Get("from_date"), v.Get("to_date")
	if from == "" && to == "" {
		today := now.In(loc)
		to = today.Format(time.DateOnly)
		from = today.AddDate(0, 0, -29).Format(time.DateOnly)
	}
	a, e := time.ParseInLocation(time.DateOnly, from, loc)
	if e != nil {
		return usagePeriod{}, usageInvalid("from_date", "開始日と終了日をYYYY-MM-DDで指定してください")
	}
	b, e := time.ParseInLocation(time.DateOnly, to, loc)
	if e != nil || b.Before(a) || a.Year() < 1 || b.Year() > 9998 || b.Sub(a) > 3660*24*time.Hour {
		return usagePeriod{}, usageInvalid("to_date", "終了日は開始日以降の日付を指定してください")
	}
	return usagePeriod{a, b.AddDate(0, 0, 1), from, to, tz, loc}, nil
}
func usageText(v string) pgtype.Text { return pgtype.Text{String: v, Valid: v != ""} }
func usageString(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func usageInt(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func (h *handler) usageScope(w http.ResponseWriter, r *http.Request) (string, usagePeriod, bool) {
	_, _, id, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/agent-usage")
	if !ok {
		return "", usagePeriod{}, false
	}
	tz, err := h.q.GetProjectTimezone(r.Context(), id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return "", usagePeriod{}, false
	}
	period, e := parseUsagePeriod(r.URL.Query(), tz, time.Now())
	if e != nil {
		apierr.Write(w, r, e)
		return "", usagePeriod{}, false
	}
	return id, period, true
}
func usageParams(id string, p usagePeriod, v url.Values) gen.ListAgentUsageEventsParams {
	return gen.ListAgentUsageEventsParams{ProjectID: id, FromAt: p.from, UntilAt: p.until, ClientKind: usageText(v.Get("client_kind")), ModelName: usageText(v.Get("model_name")), ModelVersion: usageText(v.Get("model_version"))}
}
func (h *handler) getAgentUsage(w http.ResponseWriter, r *http.Request) {
	id, p, ok := h.usageScope(w, r)
	if !ok {
		return
	}
	rows, err := h.q.ListAgentUsageEvents(r.Context(), usageParams(id, p, r.URL.Query()))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("実績を取得できない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, aggregateAgentUsage(rows, p))
}

// Cost records are independent events. Distinct tickets are counted in each group,
// rather than summed across days or models.
func aggregateAgentUsage(rows []gen.ListAgentUsageEventsRow, p usagePeriod) agentUsageView {
	out := agentUsageView{FromDate: p.fromDate, ToDate: p.toDate, Timezone: p.timezone, Daily: []usageDay{}, Models: []usageModel{}}
	for d := p.from; d.Before(p.until); d = d.AddDate(0, 0, 1) {
		k := d.Format(time.DateOnly)
		out.Daily = append(out.Daily, usageDay{Date: k})
	}
	type group struct {
		counts  *usageCounts
		tickets map[string]bool
	}
	total := group{&out.Totals, map[string]bool{}}
	daily := map[string]*group{}
	models := map[string]*group{}
	modelValues := map[string]usageModel{}
	for i := range out.Daily {
		d := &out.Daily[i]
		daily[d.Date] = &group{&d.usageCounts, map[string]bool{}}
	}
	for _, row := range rows {
		keyBytes, _ := json.Marshal([]*string{usageString(row.ClientKind), usageString(row.ModelName), usageString(row.ModelVersion)})
		key := string(keyBytes)
		if models[key] == nil {
			models[key] = &group{&usageCounts{}, map[string]bool{}}
			modelValues[key] = usageModel{ClientKind: usageString(row.ClientKind), ModelName: usageString(row.ModelName), ModelVersion: usageString(row.ModelVersion)}
		}
		day := daily[row.OccurredAt.In(p.loc).Format(time.DateOnly)]
		if day == nil {
			continue
		}
		for _, g := range []*group{&total, day, models[key]} {
			c := g.counts
			if row.EventKind == "evaluation" {
				c.EvaluationCount++
				if row.Tokens.Valid {
					c.EvaluationTokens += row.Tokens.Int64
				} else {
					c.EvaluationTokensMissing++
				}
				continue
			}
			c.RunsCount++
			g.tickets[row.TicketID] = true
			c.TicketsCount = int64(len(g.tickets))
			switch row.ResultStatus {
			case "failed":
				c.FailedCount++
			case "abandoned":
				c.AbandonedCount++
			case "completed":
				c.CompletedCount++
			case "partial":
				c.PartialCount++
			case "blocked":
				c.BlockedCount++
			}
			if row.Tokens.Valid {
				c.RunTokens += row.Tokens.Int64
			} else {
				c.RunTokensMissing++
			}
			if row.TurnsKnown {
				c.Turns += row.Turns
			} else {
				c.TurnsMissing++
			}
			if row.TimeKnown {
				c.WallClockMin += row.WallClockMin
			} else {
				c.WallClockMinMissing++
			}
		}
	}
	keys := make([]string, 0, len(models))
	for k := range models {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := modelValues[k]
		v.usageCounts = *models[k].counts
		out.Models = append(out.Models, v)
	}
	return out
}
func (h *handler) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	id, p, ok := h.usageScope(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Has("sort") || r.URL.Query().Has("order") {
		apierr.Write(w, r, usageInvalid("sort", "実行明細の並び順は変更できません"))
		return
	}
	page, e := ParsePage(r, SortSpec{Allowed: []string{"submitted_at"}, DefaultSort: "submitted_at", DefaultPerPage: 50})
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	a := usageParams(id, p, r.URL.Query())
	total, err := h.q.CountAgentUsageRuns(r.Context(), gen.CountAgentUsageRunsParams(a))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	rows, err := h.q.ListAgentUsageRuns(r.Context(), gen.ListAgentUsageRunsParams{ProjectID: a.ProjectID, FromAt: a.FromAt, UntilAt: a.UntilAt, ClientKind: a.ClientKind, ModelName: a.ModelName, ModelVersion: a.ModelVersion, PageLimit: int32(page.Limit()), PageOffset: int32(page.Offset())})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	items := make([]usageRunView, 0, len(rows))
	for _, v := range rows {
		items = append(items, usageRunView{v.EventID, v.Seq, Time(v.OccurredAt), v.ResultStatus, v.TicketStatusCategory, usageString(v.ClientKind), usageString(v.ModelName), usageString(v.ModelVersion), usageInt(v.Tokens), usageKnownInt(v.Turns, v.TurnsKnown), usageKnownInt(v.WallClockMin, v.TimeKnown)})
	}
	WriteJSON(w, http.StatusOK, NewList(items, page, int(total)))
}

func usageKnownInt(v int64, known bool) *int64 {
	if !known {
		return nil
	}
	return &v
}
