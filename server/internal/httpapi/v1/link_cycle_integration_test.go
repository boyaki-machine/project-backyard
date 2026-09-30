package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 依存の輪（ApiDesign.md 9.10.1 の link_cycle）を**実際のDBに対して**通す。pb-221。
//
// 単体テストはフェイクが DependencyPathExists の答えを決めるので、再帰 CTE も
// 行の取り方も一度も実行されない。**ここでしか確かめられないものが3つある。**
//
//   - **再帰 CTE が種別を問わず辿ること**（FS → SS → blocks をまたいだ輪を見つける）
//   - **以前から在る輪で辿りが止まること**（UNION で訪ねた行を重ねない）
//   - **同時に足された A→B と B→A の片方だけが入ること**（FOR NO KEY UPDATE の直列化）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestLinkCycleIntegration(t *testing.T) {
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
	adminEmail := "cyc-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "cyc-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"依存の輪の結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	link := func(from, to int, linkType string) *int {
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/links", base, from), session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":%q}`, to, linkType))
		return &rec.Code
	}
	mustLink := func(t *testing.T, from, to int, linkType string) {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/links", base, from), session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":%q}`, to, linkType))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%d -%s-> %d の status = %d, want 201（%s）", from, linkType, to, rec.Code, rec.Body.String())
		}
	}
	wantCycle := func(t *testing.T, from, to int, linkType string) {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/links", base, from), session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":%q}`, to, linkType))
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "link_cycle") {
			t.Fatalf("%d -%s-> %d の status = %d, want 422 link_cycle（%s）", from, linkType, to, rec.Code, rec.Body.String())
		}
	}

	a, aID := createIntegrationTicket(t, r, session, base, "設計を固める")
	b, bID := createIntegrationTicket(t, r, session, base, "API を実装する")
	c, _ := createIntegrationTicket(t, r, session, base, "画面を実装する")
	d, _ := createIntegrationTicket(t, r, session, base, "受け入れ試験")

	t.Run("種別をまたいだ輪を弾く", func(t *testing.T) {
		mustLink(t, a, b, "FS")
		mustLink(t, b, c, "SS")
		wantCycle(t, c, a, "blocks")
		wantCycle(t, c, a, "FF")
		wantCycle(t, b, a, "SF")
	})

	t.Run("relates / duplicates は輪を数えない", func(t *testing.T) {
		mustLink(t, c, a, "relates")
		mustLink(t, c, a, "duplicates")
	})

	t.Run("輪にならない依存は作れる", func(t *testing.T) {
		mustLink(t, c, d, "FS")
		mustLink(t, a, d, "FF")
	})

	// 以前の API で作れた輪を DB に直接置く（B → A）。**辿りが止まらなければ
	// 要求が返らない**ので、ここが通ること自体が UNION の確かめになる。
	t.Run("既にある輪の上でも辿りが止まる", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO ticket_link (id, source_ticket_id, target_ticket_id, link_type, lag_days, origin)
			 VALUES ($1, $2, $3, 'FS', 0, 'human')`, ulidgen.New(), bID, aID); err != nil {
			t.Fatalf("前提の輪を置けない: %v", err)
		}
		// D から辿っても A・B・C には行けない（D は行き止まり）→ 作れる
		e, _ := createIntegrationTicket(t, r, session, base, "リリース")
		mustLink(t, d, e, "FS")
		// E → B は、B → A → D → E と辿れるので輪
		wantCycle(t, e, b, "FS")
	})

	// **同時に足された2本の片方だけが入る。** 直列化が無いと、両方が「輪に
	// ならない」と読んで入る。1回では偶然通るので、組を変えて繰り返す。
	t.Run("同時の A→B と B→A は片方だけ入る", func(t *testing.T) {
		for i := range 10 {
			x, _ := createIntegrationTicket(t, r, session, base, fmt.Sprintf("並行の左 %d", i))
			y, _ := createIntegrationTicket(t, r, session, base, fmt.Sprintf("並行の右 %d", i))
			var wg sync.WaitGroup
			var c1, c2 *int
			wg.Add(2)
			go func() { defer wg.Done(); c1 = link(x, y, "FS") }()
			go func() { defer wg.Done(); c2 = link(y, x, "FS") }()
			wg.Wait()
			created := 0
			for _, code := range []int{*c1, *c2} {
				switch code {
				case http.StatusCreated:
					created++
				case http.StatusUnprocessableEntity:
				default:
					t.Fatalf("%d 回目: 予期しない status %d", i, code)
				}
			}
			if created != 1 {
				t.Fatalf("%d 回目: 作れた本数 = %d, want 1（%d / %d）", i, created, *c1, *c2)
			}
		}
	})
}
