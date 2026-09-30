package v1

import (
	"context"
	"encoding/csv"
	"encoding/json"
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
		_, err := pool.Exec(ctx, `INSERT INTO audit_log (id,occurred_at,actor_id,actor_kind,actor_label,action,target_type,target_id,result,detail) VALUES ($1,$2,$3,'user','監査結合テスト',$4,'agent',$5,$6,'{"source":"integration"}'::jsonb)`, ulidgen.New(), base.Add(time.Duration(i)*time.Second), adminID, action, marker, result)
		if err != nil {
			t.Fatal(err)
		}
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
			Action     string         `json:"action"`
			ActorLabel string         `json:"actor_label"`
			Detail     map[string]any `json:"detail"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Action != "agent.delete" || body.Items[0].ActorLabel != "監査結合テスト" {
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
	if len(csvRows) != 2 || csvRows[1][8] != "agent.delete" {
		t.Errorf("csv rows=%v", csvRows)
	}
}
