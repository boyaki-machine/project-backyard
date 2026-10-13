package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/notify"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 通知（ApiDesign.md 4.8、DbDesign.md 6.33）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - 他人の通知が 404 になること（クエリの recipient_id / source_id）
//   - 既読の時刻が、2回目の既読で書き換わらないこと
//   - 取り下げが宛先をまたいで消すこと
//   - 保持期間を過ぎた既読だけが消え、未読は残ること
//   - 遷移先の CHECK が、別のホストへ飛ばすパスを DB でも弾くこと
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMeNotificationsIntegration(t *testing.T) {
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
	r := routerWithDeps(Deps{
		Queries: q, Tx: store.NewTxRunner(pool), Settings: config.LiveDefaults(),
	})

	uniq := strings.ToLower(ulidgen.New())
	newUser := func(t *testing.T, label, role string) (string, string) {
		t.Helper()
		id := ulidgen.New()
		email := "notify-" + label + "-" + uniq + "@example.com"
		seedUserWithRole(t, ctx, pool, q, id, email, role)
		return id, loginAs(t, r, email)
	}

	projectID := ulidgen.New()
	projectKey := "ntf-" + uniq[len(uniq)-6:]
	if err := q.CreateProject(ctx, gen.CreateProjectParams{
		ID: projectID, Key: projectKey, Name: "通知の結合テスト",
	}); err != nil {
		t.Fatalf("プロジェクトを作れない: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})

	// create は使い手の代わりに通知を1件作り、その id を返す。
	create := func(t *testing.T, n notify.Notification) string {
		t.Helper()
		if n.Kind == "" {
			n.Kind = "test.event"
		}
		if n.Title == "" {
			n.Title = "結合テストの通知"
		}
		if n.Link == "" {
			n.Link = "/projects"
		}
		if n.LinkLabel == "" {
			n.LinkLabel = "プロジェクト一覧"
		}
		if err := notify.Create(ctx, q, n); err != nil {
			t.Fatalf("通知を作れない: %v", err)
		}
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM notification WHERE recipient_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
			n.RecipientID).Scan(&id); err != nil {
			t.Fatalf("作った通知を引けない: %v", err)
		}
		return id
	}

	unread := func(t *testing.T, token string) int {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/me/notifications/unread-count", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("未読数の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var v struct{ Count int }
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("未読数を読めない: %v", err)
		}
		return v.Count
	}

	type item struct {
		ID        string
		Kind      string
		Title     string
		Link      string
		LinkLabel string `json:"link_label"`
		Project   *struct{ Key, Name string }
		Source    struct {
			ID   *string
			Name string
		}
		ReadAt *int64 `json:"read_at"`
	}
	list := func(t *testing.T, token, query string) []item {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/me/notifications"+query, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var v struct{ Items []item }
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("一覧を読めない: %v", err)
		}
		return v.Items
	}
	readAtOf := func(t *testing.T, id string) *time.Time {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(ctx, `SELECT read_at FROM notification WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatalf("read_at を引けない: %v", err)
		}
		return at
	}

	t.Run("作った通知が宛先の未読数と一覧に出て、他人には出ない", func(t *testing.T) {
		aliceID, alice := newUser(t, "list-a", auth.SystemRoleOperator)
		bobID, bob := newUser(t, "list-b", auth.SystemRoleOperator)

		id := create(t, notify.Notification{
			RecipientID: aliceID, Kind: "test.with_project", Title: "確認してください",
			Link: "/p/" + projectKey + "/tickets/1", LinkLabel: projectKey + "-1 題名",
			ProjectID: projectID, SourceID: bobID, SourceName: "ボブ",
		})
		create(t, notify.Notification{RecipientID: aliceID}) // PB 自身が出した通知

		if got := unread(t, alice); got != 2 {
			t.Errorf("alice の未読数 = %d、want 2", got)
		}
		if got := unread(t, bob); got != 0 {
			t.Errorf("bob の未読数 = %d、want 0（他人の通知を数えた）", got)
		}
		items := list(t, alice, "")
		if len(items) != 2 {
			t.Fatalf("alice の一覧 = %d件、want 2", len(items))
		}
		var withProject, fromPB *item
		for i := range items {
			if items[i].ID == id {
				withProject = &items[i]
			} else {
				fromPB = &items[i]
			}
		}
		if withProject == nil || withProject.Project == nil || withProject.Project.Key != projectKey {
			t.Errorf("プロジェクト付きの通知の project が違う: %+v", withProject)
		}
		if withProject != nil && (withProject.Source.ID == nil || *withProject.Source.ID != bobID ||
			withProject.Source.Name != "ボブ" || withProject.LinkLabel != projectKey+"-1 題名") {
			t.Errorf("通知元か遷移先の名前が違う: %+v", withProject)
		}
		if fromPB == nil || fromPB.Project != nil || fromPB.Source.ID != nil || fromPB.Source.Name != notify.SourceNamePB {
			t.Errorf("PB 自身の通知の形が違う: %+v", fromPB)
		}
		if got := list(t, bob, ""); len(got) != 0 {
			t.Errorf("bob の一覧 = %d件、want 0", len(got))
		}
	})

	t.Run("既読にする・未読に戻す。2回目の既読で時刻を書き換えない", func(t *testing.T) {
		aliceID, alice := newUser(t, "read-a", auth.SystemRoleOperator)
		id := create(t, notify.Notification{RecipientID: aliceID})
		path := "/api/v1/me/notifications/" + id

		if rec := bodyWithCookie(r, http.MethodPatch, path, alice, `{"read":true}`, ""); rec.Code != http.StatusOK {
			t.Fatalf("既読の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		first := readAtOf(t, id)
		if first == nil {
			t.Fatal("既読にしたのに read_at が空")
		}
		if _, err := pool.Exec(ctx, `UPDATE notification SET read_at = read_at - interval '1 hour' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		before := readAtOf(t, id)
		if rec := bodyWithCookie(r, http.MethodPatch, path, alice, `{"read":true}`, ""); rec.Code != http.StatusOK {
			t.Fatalf("2回目の既読の status = %d", rec.Code)
		}
		if after := readAtOf(t, id); after == nil || !after.Equal(*before) {
			t.Errorf("2回目の既読で read_at が変わった: %v → %v", before, after)
		}
		if got := unread(t, alice); got != 0 {
			t.Errorf("既読の後の未読数 = %d、want 0", got)
		}
		if got := list(t, alice, "?filter=unread"); len(got) != 0 {
			t.Errorf("未読の絞り込みに既読が出た: %d件", len(got))
		}

		rec := bodyWithCookie(r, http.MethodPatch, path, alice, `{"read":false}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("未読に戻す status = %d", rec.Code)
		}
		if readAtOf(t, id) != nil {
			t.Error("未読に戻したのに read_at が残った")
		}
		if got := unread(t, alice); got != 1 {
			t.Errorf("未読に戻した後の未読数 = %d、want 1", got)
		}

		if rec := bodyWithCookie(r, http.MethodPatch, path, alice, `{}`, ""); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("read の無い PATCH の status = %d、want 422", rec.Code)
		}
	})

	t.Run("すべて既読にする", func(t *testing.T) {
		aliceID, alice := newUser(t, "all-a", auth.SystemRoleOperator)
		for range 3 {
			create(t, notify.Notification{RecipientID: aliceID})
		}
		rec := postWithCookie(r, "/api/v1/me/notifications/read-all", alice, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":3`) {
			t.Fatalf("すべて既読 = %d %s、want 200 updated 3", rec.Code, rec.Body.String())
		}
		if got := unread(t, alice); got != 0 {
			t.Errorf("すべて既読の後の未読数 = %d", got)
		}
	})

	t.Run("他人の通知は見る・既読にする・消すのいずれも 404", func(t *testing.T) {
		aliceID, _ := newUser(t, "other-a", auth.SystemRoleOperator)
		_, carol := newUser(t, "other-c", auth.SystemRoleOperator)
		id := create(t, notify.Notification{RecipientID: aliceID})
		path := "/api/v1/me/notifications/" + id

		if rec := bodyWithCookie(r, http.MethodPatch, path, carol, `{"read":true}`, ""); rec.Code != http.StatusNotFound {
			t.Errorf("他人の既読 = %d、want 404", rec.Code)
		}
		if rec := deleteWithCookie(r, path, carol); rec.Code != http.StatusNotFound {
			t.Errorf("他人の削除 = %d、want 404", rec.Code)
		}
		if readAtOf(t, id) != nil {
			t.Error("他人の操作で既読になった")
		}
	})

	t.Run("受けた人と出した人が消せる。出した人の削除だけ監査に残す", func(t *testing.T) {
		aliceID, alice := newUser(t, "del-a", auth.SystemRoleOperator)
		bobID, bob := newUser(t, "del-b", auth.SystemRoleOperator)

		own := create(t, notify.Notification{RecipientID: aliceID, SourceID: bobID, SourceName: "ボブ"})
		if rec := deleteWithCookie(r, "/api/v1/me/notifications/"+own, alice); rec.Code != http.StatusNoContent {
			t.Fatalf("受けた人の削除 = %d（body=%s）", rec.Code, rec.Body.String())
		}
		sent := create(t, notify.Notification{RecipientID: aliceID, SourceID: bobID, SourceName: "ボブ"})
		if rec := deleteWithCookie(r, "/api/v1/me/notifications/"+sent, bob); rec.Code != http.StatusNoContent {
			t.Fatalf("出した人の削除 = %d（body=%s）", rec.Code, rec.Body.String())
		}

		var ownAudits, sentAudits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'notification.delete' AND target_id = $1`, own).Scan(&ownAudits); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'notification.delete' AND target_id = $1 AND actor_id = $2`, sent, bobID).Scan(&sentAudits); err != nil {
			t.Fatal(err)
		}
		if ownAudits != 0 || sentAudits != 1 {
			t.Errorf("監査の件数：受けた人 %d（want 0）、出した人 %d（want 1）", ownAudits, sentAudits)
		}
	})

	t.Run("管理者の口は system.settings が要り、誰宛てでも消せる", func(t *testing.T) {
		aliceID, alice := newUser(t, "adm-a", auth.SystemRoleOperator)
		_, admin := newUser(t, "adm-x", auth.SystemRoleAdministrator)
		id := create(t, notify.Notification{RecipientID: aliceID})

		if rec := deleteWithCookie(r, "/api/v1/admin/notifications/"+id, alice); rec.Code != http.StatusForbidden {
			t.Errorf("権限の無い人の管理者の口 = %d、want 403", rec.Code)
		}
		if rec := deleteWithCookie(r, "/api/v1/admin/notifications/"+id, admin); rec.Code != http.StatusNoContent {
			t.Fatalf("管理者の削除 = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if rec := deleteWithCookie(r, "/api/v1/admin/notifications/"+id, admin); rec.Code != http.StatusNotFound {
			t.Errorf("消した後の管理者の削除 = %d、want 404", rec.Code)
		}
	})

	t.Run("取り下げは同じ対象の通知を宛先をまたいで消す", func(t *testing.T) {
		aliceID, alice := newUser(t, "wd-a", auth.SystemRoleOperator)
		bobID, bob := newUser(t, "wd-b", auth.SystemRoleOperator)
		key := "test_subject:" + ulidgen.New()
		create(t, notify.Notification{RecipientID: aliceID, SubjectKey: key})
		create(t, notify.Notification{RecipientID: bobID, SubjectKey: key})
		create(t, notify.Notification{RecipientID: aliceID, SubjectKey: "test_subject:other-" + uniq})

		n, err := notify.Withdraw(ctx, q, key)
		if err != nil || n != 2 {
			t.Fatalf("取り下げ = %d, %v、want 2", n, err)
		}
		if got := unread(t, alice); got != 1 {
			t.Errorf("alice の未読数 = %d、want 1（別の対象は残る）", got)
		}
		if got := unread(t, bob); got != 0 {
			t.Errorf("bob の未読数 = %d、want 0", got)
		}
	})

	t.Run("保持期間を過ぎた既読だけが消え、未読は残る", func(t *testing.T) {
		aliceID, _ := newUser(t, "sweep-a", auth.SystemRoleOperator)
		old := create(t, notify.Notification{RecipientID: aliceID})
		recent := create(t, notify.Notification{RecipientID: aliceID})
		oldUnread := create(t, notify.Notification{RecipientID: aliceID})
		if _, err := pool.Exec(ctx, `
			UPDATE notification SET read_at = now() - interval '31 days' WHERE id = $1;`, old); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE notification SET read_at = now() - interval '29 days' WHERE id = $1`, recent); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE notification SET created_at = now() - interval '400 days' WHERE id = $1`, oldUnread); err != nil {
			t.Fatal(err)
		}
		if _, err := notify.Sweep(ctx, q, time.Now()); err != nil {
			t.Fatal(err)
		}
		left := map[string]bool{}
		rows, err := pool.Query(ctx, `SELECT id FROM notification WHERE recipient_id = $1`, aliceID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			left[id] = true
		}
		if left[old] || !left[recent] || !left[oldUnread] {
			t.Errorf("残った通知：31日前の既読 %v（want false）、29日前の既読 %v、古い未読 %v（want true）",
				left[old], left[recent], left[oldUnread])
		}
	})

	t.Run("一覧の誤った指定は 422", func(t *testing.T) {
		_, alice := newUser(t, "bad-a", auth.SystemRoleOperator)
		for _, query := range []string{"?filter=new", "?sort=created_at", "?order=asc", "?per_page=201"} {
			if rec := getWithCookie(r, "/api/v1/me/notifications"+query, alice); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s = %d、want 422", query, rec.Code)
			}
		}
	})

	t.Run("遷移先の CHECK は別のホストへ飛ばすパスを DB でも弾く", func(t *testing.T) {
		aliceID, _ := newUser(t, "chk-a", auth.SystemRoleOperator)
		for _, link := range []string{"//evil.example.com/x", `/\evil.example.com`, "https://evil.example.com/"} {
			_, err := pool.Exec(ctx, `
				INSERT INTO notification (id, recipient_id, kind, title, link, link_label, source_name)
				VALUES ($1, $2, 'test.event', 't', $3, 'l', 'PB')`, ulidgen.New(), aliceID, link)
			if err == nil {
				t.Errorf("遷移先 %q が DB に入った", link)
			}
		}
	})
}
