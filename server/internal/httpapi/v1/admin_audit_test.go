package v1

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

type auditQueryFake struct {
	gen.Querier
	listParams   gen.ListAuditLogsParams
	exportParams gen.ExportAuditLogsParams
	listRows     []gen.AuditLog
	exportRows   []gen.AuditLog
}

func (q *auditQueryFake) ListAuditLogs(_ context.Context, p gen.ListAuditLogsParams) ([]gen.AuditLog, error) {
	q.listParams = p
	return q.listRows, nil
}
func (q *auditQueryFake) SummarizeAuditLogs(_ context.Context, _ gen.SummarizeAuditLogsParams) (int64, error) {
	return int64(len(q.listRows)), nil
}
func (q *auditQueryFake) ExportAuditLogs(_ context.Context, p gen.ExportAuditLogsParams) ([]gen.AuditLog, error) {
	q.exportParams = p
	return q.exportRows, nil
}

func TestAuditFiltersAndList(t *testing.T) {
	q := &auditQueryFake{listRows: []gen.AuditLog{{
		ID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", OccurredAt: time.UnixMilli(1786438992000),
		Action: "login.failure", Result: "failure", Detail: []byte(`{"reason":"bad"}`),
		ActorLabel: pgtype.Text{String: "監査担当 <audit@example.com>", Valid: true},
	}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit?from_at=1786438991000&to_at=1786438993000&action=login_&actor=%E7%9B%A3%E6%9F%BB&result=failure&q=bad%25&page=2", nil)
	(&handler{q: q}).listAuditLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.listParams.PageOffset != 50 || q.listParams.PageLimit != 50 {
		t.Errorf("page params=%+v", q.listParams)
	}
	if q.listParams.ActionPattern != `%login\_%` {
		t.Errorf("action pattern=%q", q.listParams.ActionPattern)
	}
	if q.listParams.QPattern != `%bad\%%` {
		t.Errorf("q pattern=%q", q.listParams.QPattern)
	}
	if q.listParams.FromAt == nil || q.listParams.ToAt == nil {
		t.Fatal("日時がクエリへ渡らない")
	}
	var body struct {
		Items []struct {
			ActorName *string `json:"actor_name"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ActorName == nil || *body.Items[0].ActorName != "監査担当" || strings.Contains(rec.Body.String(), "audit@example.com") {
		t.Errorf("items=%+v", body.Items)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag が無い")
	}
}

func TestAuditRejectsInvalidFilters(t *testing.T) {
	for _, qs := range []string{"?result=denied", "?from_at=abc", "?from_at=2000&to_at=1000", "?order=asc", "?per_page=401"} {
		rec := httptest.NewRecorder()
		(&handler{q: &auditQueryFake{}}).listAuditLogs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit"+qs, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status=%d", qs, rec.Code)
		}
	}
}

func TestAuditAllows400PerPage(t *testing.T) {
	q := &auditQueryFake{}
	rec := httptest.NewRecorder()
	(&handler{q: q}).listAuditLogs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit?per_page=400", nil))
	if rec.Code != http.StatusOK || q.listParams.PageLimit != 400 {
		t.Errorf("status=%d params=%+v", rec.Code, q.listParams)
	}
}

func TestAuditCSVUsesFiltersAndProtectsFormula(t *testing.T) {
	q := &auditQueryFake{exportRows: []gen.AuditLog{
		{ID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", OccurredAt: time.UnixMilli(1000), ActorLabel: pgtype.Text{String: "=SUM(1)", Valid: true}, Action: "login.failure", Result: "failure", Detail: []byte(`{"note":"a,b"}`)},
		{ID: "01K2F8QW3H7YRJ4M5N6P7Q8R9T", OccurredAt: time.UnixMilli(900), ActorLabel: pgtype.Text{String: "監査担当 <audit@example.com>", Valid: true}, Action: "login.failure", Result: "failure", Detail: []byte(`{}`)},
	}}
	rec := httptest.NewRecorder()
	(&handler{q: q}).exportAuditLogs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit.csv?result=failure", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	if q.exportParams.ResultFilter != "failure" {
		t.Errorf("filters=%+v", q.exportParams)
	}
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][4] != "actor_name" || rows[1][4] != "'=SUM(1)" || rows[1][12] != `{"note":"a,b"}` || rows[2][4] != "監査担当" || strings.Contains(rec.Body.String(), "audit@example.com") {
		t.Errorf("csv=%v", rows)
	}
}

func TestAuditActorName(t *testing.T) {
	for _, test := range []struct{ label, want string }{
		{"山田 <yamada@example.com>", "山田"},
		{"agent:claude", "agent:claude"},
		{"山田 <補足>", "山田 <補足>"},
	} {
		got := auditActorName(pgtype.Text{String: test.label, Valid: true})
		if got == nil || *got != test.want {
			t.Errorf("label=%q got=%v", test.label, got)
		}
	}
}
