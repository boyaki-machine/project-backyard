package v1

import (
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
	"net/url"
	"testing"
	"time"
)

func TestAgentUsagePeriod(t *testing.T) {
	p, e := parseUsagePeriod(url.Values{"from_date": {"2026-03-08"}, "to_date": {"2026-03-08"}}, "America/New_York", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if p.until.Sub(p.from) != 23*time.Hour {
		t.Fatalf("DST day = %v", p.until.Sub(p.from))
	}
	for _, v := range []url.Values{{"from_date": {"2026-03-01"}}, {"from_date": {"2026-02-30"}, "to_date": {"2026-03-01"}}, {"from_date": {"2026-03-09"}, "to_date": {"2026-03-08"}}} {
		if _, e := parseUsagePeriod(v, "Asia/Tokyo", time.Now()); e == nil {
			t.Fatalf("accepted %v", v)
		}
	}
	p, e = parseUsagePeriod(url.Values{}, "Asia/Tokyo", time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC))
	if e != nil || p.fromDate != "2026-09-08" || p.toDate != "2026-10-07" {
		t.Fatalf("default period %+v %v", p, e)
	}
}
func TestAgentUsageCountsPreserveMissingAndResubmissions(t *testing.T) {
	p, _ := parseUsagePeriod(url.Values{"from_date": {"2026-10-07"}, "to_date": {"2026-10-08"}}, "Asia/Tokyo", time.Now())
	at := p.from.Add(time.Hour)
	rows := []gen.ListAgentUsageEventsRow{
		{EventKind: "run", TicketID: "one", OccurredAt: at, ResultStatus: "partial", ModelName: txt("old"), Tokens: pgtype.Int8{Int64: 10, Valid: true}, Turns: 2, TurnsKnown: true, WallClockMin: 3, TimeKnown: true},
		{EventKind: "run", TicketID: "one", OccurredAt: at, ResultStatus: "completed", ModelName: txt("new"), Tokens: pgtype.Int8{Valid: true}, TurnsKnown: true, TimeKnown: true},
		{EventKind: "run", TicketID: "two", OccurredAt: at, ResultStatus: "blocked"},
		{EventKind: "evaluation", TicketID: "one", OccurredAt: at, ModelName: txt("evaluator"), Tokens: pgtype.Int8{Int64: 7, Valid: true}},
		{EventKind: "evaluation", TicketID: "one", OccurredAt: at},
	}
	v := aggregateAgentUsage(rows, p)
	c := v.Totals
	if c.RunsCount != 3 || c.TicketsCount != 2 || c.RunTokens != 10 || c.RunTokensMissing != 1 || c.EvaluationTokens != 7 || c.EvaluationTokensMissing != 1 || c.EvaluationCount != 2 || c.Turns != 2 || c.TurnsMissing != 1 || c.WallClockMin != 3 || c.WallClockMinMissing != 1 {
		t.Fatalf("counts %+v", c)
	}
	if c.CompletedCount != 1 || c.PartialCount != 1 || c.BlockedCount != 1 {
		t.Fatalf("results %+v", c)
	}
	if len(v.Daily) != 2 || v.Daily[1].RunsCount != 0 || len(v.Models) != 4 {
		t.Fatalf("groups %+v", v)
	}
	if usageKnownInt(0, false) != nil || usageKnownInt(0, true) == nil {
		t.Fatal("missing and measured zero are conflated")
	}
}
