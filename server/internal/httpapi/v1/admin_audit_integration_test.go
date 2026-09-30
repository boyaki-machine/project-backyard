package v1

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

func TestUnifiedAuditIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	adminID := ulidgen.New()
	email := "unified-audit-" + strings.ToLower(adminID) + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, email, auth.SystemRoleAdministrator)
	session := loginAs(t, r, email)
	key := "aud-" + strings.ToLower(adminID[len(adminID)-8:])
	created := postWithCookie(r, "/api/v1/projects", session, fmt.Sprintf(`{"key":%q,"name":"監査対象"}`, key))
	if created.Code != http.StatusCreated {
		t.Fatalf("project create=%d %s", created.Code, created.Body.String())
	}
	projectID := viewOf(t, created)["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE target_id = $1`, projectID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	base := "/api/v1/projects/" + key
	ticket := createTicketIT(t, r, session, base, `{"type":"task","title":"監査対象チケット"}`)
	seq := int(ticket["seq"].(float64))
	label := fmt.Sprintf("%s-%d", key, seq)
	updated := patchProjectWithCookie(r, base, session, `"1"`, `{"name":"変更後"}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("project update=%d %s", updated.Code, updated.Body.String())
	}
	unchanged := patchProjectWithCookie(r, base, session, `"2"`, `{}`)
	if unchanged.Code != http.StatusOK {
		t.Fatalf("project no-op=%d %s", unchanged.Code, unchanged.Body.String())
	}
	deleted := deleteWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, seq), session)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("ticket delete=%d %s", deleted.Code, deleted.Body.String())
	}

	ticketList := getWithCookie(r, "/api/v1/admin/audit?category=ticket&q="+label, session)
	if ticketList.Code != http.StatusOK {
		t.Fatalf("ticket list=%d %s", ticketList.Code, ticketList.Body.String())
	}
	var ticketBody struct {
		Total int `json:"total"`
		Items []struct {
			Category    string `json:"category"`
			Action      string `json:"action"`
			ActorKind   string `json:"actor_kind"`
			ActorName   string `json:"actor_name"`
			TargetLabel string `json:"target_label"`
		} `json:"items"`
	}
	if err := json.Unmarshal(ticketList.Body.Bytes(), &ticketBody); err != nil {
		t.Fatal(err)
	}
	if ticketBody.Total != 2 || len(ticketBody.Items) != 2 || ticketBody.Items[0].Action != "ticket.delete" || ticketBody.Items[1].Action != "ticket.create" {
		t.Fatalf("ticket events=%s", ticketList.Body.String())
	}
	for _, item := range ticketBody.Items {
		if item.Category != "ticket" || item.ActorKind != "user" || item.ActorName == "" || item.TargetLabel != label {
			t.Errorf("ticket event=%+v", item)
		}
	}
	projectList := getWithCookie(r, "/api/v1/admin/audit?category=project&action=project.update&q="+key, session)
	if projectList.Code != http.StatusOK || !strings.Contains(projectList.Body.String(), `"total":1`) || !strings.Contains(projectList.Body.String(), `"changed_fields":["name"]`) || !strings.Contains(projectList.Body.String(), `"target_label":"`+key+`"`) {
		t.Errorf("project events=%d %s", projectList.Code, projectList.Body.String())
	}
	if strings.Contains(projectList.Body.String(), email) {
		t.Error("実行者のメールが応答に出た")
	}
	csvRes := getWithCookie(r, "/api/v1/admin/audit.csv?category=ticket&q="+label, session)
	if csvRes.Code != http.StatusOK {
		t.Fatalf("csv=%d %s", csvRes.Code, csvRes.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(csvRes.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][2] != "category" || rows[1][2] != "ticket" || rows[1][12] != label {
		t.Errorf("csv=%v", rows)
	}
}

func TestAuditIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	adminID, operatorID := ulidgen.New(), ulidgen.New()
	adminEmail, operatorEmail := "audit-admin-"+strings.ToLower(adminID)+"@example.com", "audit-operator-"+strings.ToLower(operatorID)+"@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	seedUserWithRole(t, ctx, pool, q, operatorID, operatorEmail, auth.SystemRoleOperator)
	adminSession, operatorSession := loginAs(t, r, adminEmail), loginAs(t, r, operatorEmail)
	marker := ulidgen.New()
	base := time.Now().UTC().Truncate(time.Second)
	for i, action := range []string{"agent.register", "agent.delete"} {
		result := "success"
		if i == 1 {
			result = "failure"
		}
		_, err := pool.Exec(ctx, `INSERT INTO audit_log (id,occurred_at,actor_id,actor_kind,actor_label,action,target_type,target_id,result,detail) VALUES ($1,$2,$3,'user','監査結合テスト <audit@example.com>',$4,'agent',$5,$6,'{"source":"integration"}'::jsonb)`, ulidgen.New(), base.Add(time.Duration(i)*time.Second), adminID, action, marker, result)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO audit_log (id,occurred_at,actor_id,actor_kind,actor_label,action,target_type,target_id,result,detail) VALUES ($1,$2,$3,'user','監査結合テスト <audit@example.com>','setting.update','app_setting',$4,'success','{}'::jsonb)`, ulidgen.New(), base.Add(3*time.Second), adminID, marker)
	if err != nil {
		t.Fatal(err)
	}
	application := getWithCookie(r, "/api/v1/admin/audit?category=application&q="+marker, adminSession)
	if application.Code != http.StatusOK || !strings.Contains(application.Body.String(), `"total":1`) || !strings.Contains(application.Body.String(), `"action":"setting.update"`) {
		t.Errorf("application events=%d %s", application.Code, application.Body.String())
	}
	query := "?q=" + marker + "&action=agent.&actor=%E7%9B%A3%E6%9F%BB&result=failure&from_at=" + strconv.FormatInt(base.UnixMilli(), 10) + "&to_at=" + strconv.FormatInt(base.Add(2*time.Second).UnixMilli(), 10)
	denied := getWithCookie(r, "/api/v1/admin/audit"+query, operatorSession)
	if denied.Code != http.StatusForbidden {
		t.Errorf("operator status=%d", denied.Code)
	}
	list := getWithCookie(r, "/api/v1/admin/audit"+query, adminSession)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var body struct {
		Total int `json:"total"`
		Items []struct {
			Action    string         `json:"action"`
			ActorName string         `json:"actor_name"`
			Detail    map[string]any `json:"detail"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Action != "agent.delete" || body.Items[0].ActorName != "監査結合テスト" || strings.Contains(list.Body.String(), "audit@example.com") {
		t.Errorf("body=%s", list.Body.String())
	}
	csvRes := getWithCookie(r, "/api/v1/admin/audit.csv"+query, adminSession)
	if csvRes.Code != http.StatusOK {
		t.Fatalf("csv status=%d body=%s", csvRes.Code, csvRes.Body.String())
	}
	csvRows, err := csv.NewReader(strings.NewReader(csvRes.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(csvRows) != 2 || csvRows[1][5] != "監査結合テスト" || csvRows[1][9] != "agent.delete" || strings.Contains(csvRes.Body.String(), "audit@example.com") {
		t.Errorf("csv rows=%v", csvRows)
	}
}
