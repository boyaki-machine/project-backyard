package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// DELETE /projects/:key（ApiDesign.md 5.6.1）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - project.delete が administrator にだけ配られ、project_admin では 403 になること（0071）
//
//   - ON DELETE CASCADE で、チケット・コメント・エージェントまで本当に消えること
//
//   - 連鎖で残るエージェントの actor 行を、同じトランザクションで消すこと
//
//   - 監査ログが削除の後も残ること
//
//     PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestProjectDeleteIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	adminID := ulidgen.New()
	adminEmail := "del-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	pmID := ulidgen.New()
	pmEmail := "del-pm-" + pmID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, pmID, pmEmail, auth.SystemRoleOperator)

	adminSession := loginAs(t, r, adminEmail)
	pmSession := loginAs(t, r, pmEmail)

	key := "del-" + strings.ToLower(adminID[len(adminID)-8:])
	var projectID string
	var agentActorID string
	t.Cleanup(func() {
		bg := context.Background()
		// 失敗で消えずに残ったときの後始末。成功していれば0行
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
		if agentActorID != "" {
			if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1 AND kind = 'agent'`, agentActorID); err != nil {
				t.Errorf("エージェントの後始末に失敗した: %v", err)
			}
		}
		if projectID != "" {
			if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE target_id = $1`, projectID); err != nil {
				t.Errorf("監査ログの後始末に失敗した: %v", err)
			}
		}
	})

	created := viewOf(t, postWithCookie(r, "/api/v1/projects", adminSession,
		fmt.Sprintf(`{"key":%q,"name":"削除の結合テスト"}`, key)))
	projectID, _ = created["id"].(string)
	if len(projectID) != 26 {
		t.Fatalf("作成に失敗した: %v", created)
	}
	if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
		ProjectID: projectID, ActorID: pmID, RoleKey: "project_admin",
	}); err != nil {
		t.Fatalf("メンバーを追加できない: %v", err)
	}

	// 消える側の材料：チケット・コメント・プロジェクトに紐づくエージェント
	seedTickets(t, ctx, pool, projectID, 3, 1)
	if _, err := pool.Exec(ctx, `
		INSERT INTO comment (id, ticket_id, author_id, body_md)
		SELECT $1, id, $2, '消えるコメント' FROM ticket WHERE project_id = $3 AND seq = 1`,
		ulidgen.New(), adminID, projectID); err != nil {
		t.Fatalf("コメントを作れない: %v", err)
	}
	agent := viewOf(t, postWithCookie(r, "/api/v1/me/agents", adminSession,
		fmt.Sprintf(`{"project_key":%q,"display_name":"削除で消えるエージェント","client_kind":"other"}`, key)))
	agentActorID, _ = agent["id"].(string)
	if len(agentActorID) != 26 {
		t.Fatalf("エージェントを作れない: %v", agent)
	}

	path := "/api/v1/projects/" + key
	confirm := fmt.Sprintf(`{"confirm_key":%q}`, key)

	// ① project_admin は project.delete を持たない（administrator だけ）
	if rec := deleteProjectWithCookie(r, path, pmSession, confirm); rec.Code != http.StatusForbidden {
		t.Fatalf("project_admin の status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
	}

	// ② アーカイブしていなければ 409
	rec := deleteProjectWithCookie(r, path, adminSession, confirm)
	if rec.Code != http.StatusConflict {
		t.Fatalf("有効なプロジェクトの status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec).Code; got != "conflict" {
		t.Errorf("code = %q, want conflict", got)
	}

	if rec := postWithCookie(r, path+"/archive", adminSession, ""); rec.Code != http.StatusOK {
		t.Fatalf("archive の status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	// ③ 確認のキーが無い・違うと 422。どちらも消えていない
	for _, body := range []string{`{}`, `{"confirm_key":"other"}`} {
		rec := deleteProjectWithCookie(r, path, adminSession, body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("本文 %s の status = %d, want 422（body=%s）", body, rec.Code, rec.Body.String())
		}
		e := errorOf(t, rec)
		if e.Code != "validation_failed" || len(e.Details) == 0 || e.Details[0].Field != "confirm_key" {
			t.Errorf("本文 %s のエラー = %+v, want validation_failed / confirm_key", body, e)
		}
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM project WHERE id = $1`, projectID); n != 1 {
		t.Fatal("422 を返したのにプロジェクトが消えている")
	}

	// ④ 消す。前後の空白は落として照合する
	rec = deleteProjectWithCookie(r, path, adminSession, fmt.Sprintf(`{"confirm_key":" %s "}`, key))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("削除の status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}

	for _, c := range []struct{ what, sql string }{
		{"プロジェクト", `SELECT count(*) FROM project WHERE id = $1`},
		{"チケット", `SELECT count(*) FROM ticket WHERE project_id = $1`},
		{"コメント", `SELECT count(*) FROM comment c JOIN ticket t ON t.id = c.ticket_id WHERE t.project_id = $1`},
		{"メンバー", `SELECT count(*) FROM project_member WHERE project_id = $1`},
		{"エージェント", `SELECT count(*) FROM agent WHERE project_id = $1`},
	} {
		if n := scalarInt(t, pool, c.sql, projectID); n != 0 {
			t.Errorf("%sが %d 件残っている", c.what, n)
		}
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM actor WHERE id = $1`, agentActorID); n != 0 {
		t.Error("エージェントの actor 行が残っている（連鎖では消えないので明示的に消す）")
	}
	// 人のアクターは消さない
	if n := scalarInt(t, pool, `SELECT count(*) FROM actor WHERE id = ANY($1)`, []string{adminID, pmID}); n != 2 {
		t.Errorf("人のアクターが %d 件しか残っていない, want 2", n)
	}

	// ⑤ 監査ログは残り、消えたものを辿れる
	var detail string
	if err := pool.QueryRow(ctx, `
		SELECT detail::text FROM audit_log
		WHERE action = 'project.delete' AND target_id = $1 AND actor_id = $2`,
		projectID, adminID).Scan(&detail); err != nil {
		t.Fatalf("project.delete の監査ログが無い: %v", err)
	}
	for _, want := range []string{key, "削除の結合テスト", `"ticket_count": 3`} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %s, want %q を含む", detail, want)
		}
	}

	// ⑥ 消えたプロジェクトは 404
	if rec := deleteProjectWithCookie(r, path, adminSession, confirm); rec.Code != http.StatusNotFound {
		t.Errorf("2回目の status = %d, want 404", rec.Code)
	}
}

func deleteProjectWithCookie(r http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
