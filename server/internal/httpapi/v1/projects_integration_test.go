package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// プロジェクトAPI（ApiDesign.md 5.1〜5.3）を**実際のDBに対して**通す。
//
// 他のテストは gen.Querier をフェイクに差し替えるため、queries/project.sql の
// SQL は一度も実行されない。ここでしか確かめられないものが4つある。
//
//   - ListProjects の CASE 式による ORDER BY と ICU collation が実際に動くこと
//   - LATERAL の集約が ticket_count / closed_count / progress を正しく出すこと
//     （チケットAPIは手順18のため、行は直接 INSERT して用意する）
//   - キー重複が本当にDBの UNIQUE 制約で捕まり、409 に写ること
//     （pgconn.PgError の TableName / ConstraintName が期待どおりに入るか）
//   - 途中で失敗したときにトランザクションが巻き戻ること
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
func TestProjectsIntegration(t *testing.T) {
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
	seedUserWithRole(t, ctx, pool, q, adminID, "proj-admin-"+adminID+"@example.com",
		auth.SystemRoleAdministrator)
	operatorID := ulidgen.New()
	seedUserWithRole(t, ctx, pool, q, operatorID, "proj-op-"+operatorID+"@example.com",
		auth.SystemRoleOperator)

	adminSession := loginAs(t, r, "proj-admin-"+adminID+"@example.com")
	operatorSession := loginAs(t, r, "proj-op-"+operatorID+"@example.com")

	// キーは 20 文字以内・英小文字と数字とハイフン（ApiDesign.md 5.3）。
	// ULID の末尾を小文字にして流用する。
	key := "it-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})

	// ① 作成。
	body := fmt.Sprintf(`{"key":%q,"name":"結合テスト用","description":"説明","workflow_template":"with_review"}`, key)
	rec := postWithCookie(r, "/api/v1/projects", adminSession, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("作成の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/projects/"+key {
		t.Errorf("Location = %q, want /api/v1/projects/%s", got, key)
	}

	created := viewOf(t, rec)
	projectID, _ := created["id"].(string)
	if len(projectID) != 26 {
		t.Fatalf("id = %v, want ULID", created["id"])
	}
	if created["my_role"] != creatorRoleKey {
		t.Errorf("my_role = %v, want %s", created["my_role"], creatorRoleKey)
	}
	wf, ok := created["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("workflow = %v, want オブジェクト", created["workflow"])
	}
	// with_review は「+レビュー中」を持つ（DbDesign.md 7.4）。
	statuses, _ := wf["statuses"].([]any)
	if len(statuses) < 4 {
		t.Errorf("workflow.statuses = %d件, want 4件以上（with_review）", len(statuses))
	}

	// ② DBの状態。project_counter・workflow・メンバー・監査。
	if got := scalarInt(t, pool, `SELECT count(*) FROM project_counter WHERE project_id = $1`, projectID); got != 1 {
		t.Errorf("project_counter = %d件, want 1", got)
	}
	if got := scalarInt(t, pool,
		`SELECT count(*) FROM workflow WHERE project_id = $1 AND NOT is_template`, projectID); got != 1 {
		t.Errorf("プロジェクト固有の workflow = %d件, want 1", got)
	}
	if got := scalarInt(t, pool, `
		SELECT count(*) FROM workflow_transition wt
		  JOIN workflow w ON w.id = wt.workflow_id
		 WHERE w.project_id = $1`, projectID); got == 0 {
		t.Error("遷移が複製されていない")
	}
	if got := scalarInt(t, pool, `
		SELECT count(*) FROM project_member
		 WHERE project_id = $1 AND actor_id = $2 AND role_key = $3`,
		projectID, adminID, creatorRoleKey); got != 1 {
		t.Errorf("作成者が %s として登録されていない", creatorRoleKey)
	}
	if got := scalarInt(t, pool, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'project.create' AND target_id = $1`, projectID); got != 1 {
		t.Error("audit_log に project.create が無い")
	}

	// ③ キー重複は 409 already_exists。**DBの UNIQUE 制約で捕まること。**
	dup := postWithCookie(r, "/api/v1/projects", adminSession,
		fmt.Sprintf(`{"key":%q,"name":"重複"}`, key))
	if dup.Code != http.StatusConflict {
		t.Fatalf("重複作成の status = %d, want 409（body=%s）", dup.Code, dup.Body.String())
	}
	if got := errorOf(t, dup).Code; got != "already_exists" {
		t.Errorf("code = %q, want already_exists", got)
	}

	// ④ チケットを入れて件数と進捗を確かめる（チケットAPIは手順18）。
	seedTickets(t, ctx, pool, projectID, 4, 3)

	list := listProjectsAs(t, r, adminSession, "")
	item := findProjectItem(t, list, key)
	if got, _ := item["ticket_count"].(float64); got != 4 {
		t.Errorf("ticket_count = %v, want 4", item["ticket_count"])
	}
	if got, _ := item["closed_count"].(float64); got != 3 {
		t.Errorf("closed_count = %v, want 3", item["closed_count"])
	}
	if got, _ := item["progress"].(float64); got != 0.75 {
		t.Errorf("progress = %v, want 0.75", item["progress"])
	}
	if item["my_role"] != creatorRoleKey {
		t.Errorf("my_role = %v, want %s", item["my_role"], creatorRoleKey)
	}

	// ⑤ 並び替えは5項目 × 2方向すべてが実際に実行できること
	//    （CASE 式の ORDER BY と ICU collation は SQL を投げないと確かめられない）。
	for _, sort := range projectSortSpec.Allowed {
		for _, order := range []string{OrderAsc, OrderDesc} {
			rec := getWithCookie(r, "/api/v1/projects?sort="+sort+"&order="+order, adminSession)
			if rec.Code != http.StatusOK {
				t.Errorf("sort=%s order=%s の status = %d（body=%s）",
					sort, order, rec.Code, rec.Body.String())
			}
		}
	}

	// ⑥ ETag が出る（ApiDesign.md 2.7）。
	if got := listRecorder(r, adminSession, "").Header().Get("ETag"); !strings.HasPrefix(got, `W/"proj-`) {
		t.Errorf("ETag = %q, want W/\"proj-…\"", got)
	}

	// ⑦ アーカイブの絞り込み（既定は active のみ）。
	if _, err := pool.Exec(ctx,
		`UPDATE project SET status = 'archived' WHERE id = $1`, projectID); err != nil {
		t.Fatalf("アーカイブに更新できない: %v", err)
	}
	if hasProjectItem(t, listProjectsAs(t, r, adminSession, ""), key) {
		t.Error("既定（status=active）にアーカイブ済みが含まれている")
	}
	if !hasProjectItem(t, listProjectsAs(t, r, adminSession, "?status=archived"), key) {
		t.Error("status=archived にアーカイブ済みが含まれていない")
	}
	if !hasProjectItem(t, listProjectsAs(t, r, adminSession, "?status=all"), key) {
		t.Error("status=all にアーカイブ済みが含まれていない")
	}
	if _, err := pool.Exec(ctx, `UPDATE project SET status = 'active' WHERE id = $1`, projectID); err != nil {
		t.Fatalf("戻せない: %v", err)
	}

	// ⑧ 非メンバーのオペレータには見えず、作成もできない（5.1 / 5.3）。
	if hasProjectItem(t, listProjectsAs(t, r, operatorSession, "?status=all"), key) {
		t.Error("非メンバーのオペレータに他人のプロジェクトが見えている")
	}
	forbidden := postWithCookie(r, "/api/v1/projects", operatorSession,
		`{"key":"op-cannot-create","name":"作れないはず"}`)
	if forbidden.Code != http.StatusForbidden {
		t.Errorf("オペレータの作成 status = %d, want 403（body=%s）", forbidden.Code, forbidden.Body.String())
	}

	// ⑨ check-key（5.2）。
	taken := viewOf(t, getWithCookie(r, "/api/v1/projects/check-key?key="+key, adminSession))
	if taken["available"] != false || taken["reason"] != keyReasonAlreadyExists {
		t.Errorf("既存キーの check-key = %v", taken)
	}
	free := viewOf(t, getWithCookie(r, "/api/v1/projects/check-key?key=it-free-"+
		strings.ToLower(operatorID[len(operatorID)-4:]), adminSession))
	if free["available"] != true {
		t.Errorf("未使用キーの check-key = %v", free)
	}

	// ⑩ トランザクションが巻き戻ること（5.3 の「単一トランザクション」）。
	rollbackKey := "it-rb-" + strings.ToLower(operatorID[len(operatorID)-6:])
	sentinel := errors.New("わざと失敗させる")
	err = store.NewTxRunner(pool).RunInTx(ctx, func(tq gen.Querier) error {
		if err := tq.CreateProject(ctx, gen.CreateProjectParams{
			ID: ulidgen.New(), Key: rollbackKey, Name: "巻き戻るはず",
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunInTx のエラー = %v, want %v", err, sentinel)
	}
	if got := scalarInt(t, pool, `SELECT count(*) FROM project WHERE key = $1`, rollbackKey); got != 0 {
		t.Errorf("ロールバックされていない: project が %d件残っている", got)
	}
}

// seedUserWithRole は指定したシステムロールのユーザーを作り、後始末を登録する。
func seedUserWithRole(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, q *gen.Queries,
	actorID, email, systemRole string,
) {
	t.Helper()

	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: actorID, DisplayName: "プロジェクト結合テスト",
	}); err != nil {
		t.Fatalf("actor を作れない: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		// audit_log.actor_id は ON DELETE SET NULL なので actor より先に消す。
		if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, actorID); err != nil {
			t.Errorf("監査ログの後始末に失敗した: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, actorID); err != nil {
			t.Errorf("後始末に失敗した: %v", err)
		}
	})

	if err := q.CreateAppUser(ctx, gen.CreateAppUserParams{
		ActorID: actorID, Email: email, SystemRole: systemRole,
	}); err != nil {
		t.Fatalf("app_user を作れない: %v", err)
	}

	identityID := ulidgen.New()
	if err := q.CreateUserIdentity(ctx, gen.CreateUserIdentityParams{
		ID: identityID, UserID: actorID, ProviderKey: "local", Subject: email,
	}); err != nil {
		t.Fatalf("user_identity を作れない: %v", err)
	}
	phc, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := q.CreateLocalCredential(ctx, gen.CreateLocalCredentialParams{
		IdentityID: identityID, PasswordHash: phc,
	}); err != nil {
		t.Fatalf("local_credential を作れない: %v", err)
	}
}

// seedTickets は project に total 件のチケットを入れ、うち closed 件を完了にする。
//
// **チケットAPIは手順18のため、行を直接入れる。** 一覧の ticket_count /
// closed_count / progress は、チケットが1件も無いと 0 のままで検証できない。
func seedTickets(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID string, total, closed int) {
	t.Helper()
	for i := 1; i <= total; i++ {
		var closedAt any
		if i <= closed {
			closedAt = "now()"
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO ticket (id, project_id, seq, type, title, status_key, closed_at)
			VALUES ($1, $2, $3, 'task', $4, 'todo', CASE WHEN $5::boolean THEN now() END)`,
			ulidgen.New(), projectID, i, fmt.Sprintf("結合テスト %d", i), closedAt != nil)
		if err != nil {
			t.Fatalf("チケットを作れない: %v", err)
		}
	}
	// project の CASCADE でチケットも落ちるため、個別の後始末は登録しない。
}

// loginAs はログインしてセッショントークンを返す。
func loginAs(t *testing.T, r http.Handler, email string) string {
	t.Helper()
	rec := call(r, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s のログイン status = %d（body=%s）", email, rec.Code, rec.Body.String())
	}
	c := cookieOf(rec, auth.SessionCookieName)
	if c == nil {
		t.Fatalf("%s のログインで pb_session が返らない", email)
	}
	return c.Value
}

func getWithCookie(r http.Handler, path, token string) *httptest.ResponseRecorder {
	return callWithCookie(r, http.MethodGet, path, token)
}

func postWithCookie(r http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	addCSRF(req)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func listRecorder(r http.Handler, token, query string) *httptest.ResponseRecorder {
	return getWithCookie(r, "/api/v1/projects"+query, token)
}

func listProjectsAs(t *testing.T, r http.Handler, token, query string) []any {
	t.Helper()
	rec := listRecorder(r, token, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	items, _ := viewOf(t, rec)["items"].([]any)
	return items
}

func findProjectItem(t *testing.T, items []any, key string) map[string]any {
	t.Helper()
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["key"] == key {
			return m
		}
	}
	t.Fatalf("一覧に %q が無い", key)
	return nil
}

func hasProjectItem(t *testing.T, items []any, key string) bool {
	t.Helper()
	for _, it := range items {
		if m, _ := it.(map[string]any); m["key"] == key {
			return true
		}
	}
	return false
}

func scalarInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("クエリに失敗した（%s）: %v", sql, err)
	}
	return n
}
