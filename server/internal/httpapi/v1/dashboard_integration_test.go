package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// stats / activity（ApiDesign.md 9.13）を**実際のDBに対して**通す。手順19a。
//
// 単体テストはフェイクを差し替えるため、queries/stats.sql と activity.sql の
// 読み出しは一度も実行されない。ここでしか確かめられないものが6つある。
//
//   - FILTER 句の数え分けが実データで合うこと（by_category / open / overdue /
//     stale / unassigned）
//   - **status_key がワークフローに解決できないチケット**が、どのカテゴリにも
//     入らないまま total には入ること（LEFT JOIN の帰結。合わせに行かない）
//   - overdue の CURRENT_DATE と stale の make_interval が、境界の日付で
//     期待どおりに効くこと（13日前は放置でなく、20日前は放置である）
//   - **ORDER BY occurred_at DESC, id DESC の tie-break が SQL 側で効く**こと
//     （同じ時刻の2行の並びは、フェイクではなく DB が決める）
//   - 削除されたチケットを指す行が、LEFT JOIN で entity_seq / entity_title が
//     NULL のまま返ること
//   - **project_id の絞り込み**。別プロジェクトの履歴が混ざらないこと
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestDashboardIntegration(t *testing.T) {
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
	adminEmail := "dash-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	suffix := strings.ToLower(adminID[len(adminID)-8:])
	key := "ds-" + suffix
	otherKey := "do-" + suffix

	// **後始末は2つのプロジェクトごと消す。** activity も ticket も
	// project への FK が ON DELETE CASCADE なので、これで全部落ちる。
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = ANY($1)`, []string{key, otherKey}); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})

	// **with_review を使う。** simple はレビュー中を持たないので（DbDesign.md 7.4）、
	// by_category.review が実データで埋まる側を測れない。
	projectID := createDashProject(t, r, pool, session, key, "with_review")
	otherID := createDashProject(t, r, pool, session, otherKey, "simple")

	base := "/api/v1/projects/" + key

	// ── ① 集計の材料を作る（9.13.1）───────────────────────────
	//
	// **チケットは直接 INSERT する。** updated_at には BEFORE UPDATE の
	// トリガ（0006 の trg_ticket_updated）が効くので、API や UPDATE では
	// 過去の日時を置けない。INSERT にはトリガが無い。
	//
	//  seq status_key    closed  due          assignee  updated_at  期待
	//   1  todo          -       -            -         now         todo/open/unassigned
	//   2  in_progress   -       today-3      admin     now         in_progress/open/overdue
	//   3  review        -       today+30     -         -20d        review/open/unassigned/stale
	//   4  done          now     today-10     admin     -20d        done。**完了なので overdue/stale に入らない**
	//   5  zzz_unknown   -       -            -         now         **どのカテゴリにも入らない**が total に入る
	//   6  todo          -       -            admin     -13d        todo/open。**閾値の内側なので stale でない**
	tickets := []struct {
		seq        int32
		statusKey  string
		closed     bool
		dueInDays  *int32 // CURRENT_DATE からの日数。nil なら due_date は NULL
		assignee   bool
		updatedAgo int32 // 何日前に更新されたことにするか
	}{
		{1, "todo", false, nil, false, 0},
		{2, "in_progress", false, days(-3), true, 0},
		{3, "review", false, days(30), false, 20},
		{4, "done", true, days(-10), true, 20},
		{5, "zzz_unknown", false, nil, false, 0},
		{6, "todo", false, nil, true, 13},
	}
	ticketIDs := map[int32]string{}
	for _, c := range tickets {
		id := ulidgen.New()
		ticketIDs[c.seq] = id
		var assignee *string
		if c.assignee {
			assignee = &adminID
		}
		// **SQL を組み立てず、値はすべてパラメータで渡す。** 条件で $n を
		// 出し入れすると、使われない引数が残って pgx が弾く。
		if _, err := pool.Exec(ctx, `
			INSERT INTO ticket
			  (id, project_id, seq, type, title, status_key,
			   due_date, closed_at, assignee_id, created_at, updated_at)
			VALUES ($1, $2, $3, 'task', 'ダッシュボード結合テスト', $4,
			   CASE WHEN $5::int IS NULL THEN NULL ELSE CURRENT_DATE + $5::int END,
			   CASE WHEN $6::boolean THEN now() ELSE NULL END,
			   $7,
			   now(), now() - make_interval(days => $8::int))`,
			id, projectID, c.seq, c.statusKey,
			c.dueInDays, c.closed, assignee, c.updatedAgo); err != nil {
			t.Fatalf("チケット seq=%d を作れない: %v", c.seq, err)
		}
	}

	// ── ② stats（9.13.1）─────────────────────────────────────
	stats := getDashStats(t, r, session, base)
	want := projectStatsView{
		ByCategory: statsByCategoryView{Todo: 2, InProgress: 1, Review: 1, Done: 1},
		Total:      6,
		Open:       5,
		Overdue:    1,
		Stale:      statsStaleView{Count: 1, ThresholdDays: 14},
		Unassigned: 3,
	}
	if stats != want {
		t.Errorf("stats = %+v,\n  want %+v", stats, want)
	}

	// **by_category の合計（5）は total（6）と一致しない。** status_key が
	// ワークフローに無い seq=5 がどのカテゴリにも入らないためで、これは仕様である。
	sum := stats.ByCategory.Todo + stats.ByCategory.InProgress +
		stats.ByCategory.Review + stats.ByCategory.Done
	if sum != 5 {
		t.Errorf("by_category の合計 = %d, want 5（未解決の status_key は数えない）", sum)
	}

	// **空のプロジェクトでも4つのキーが揃う**（9.13.1）。simple なので
	// review というステータス自体が存在しない。
	emptyStats := getDashStats(t, r, session, "/api/v1/projects/"+otherKey)
	if (emptyStats != projectStatsView{
		Stale: statsStaleView{ThresholdDays: 14},
	}) {
		t.Errorf("空のプロジェクトの stats = %+v, want ゼロ値（threshold は 14）", emptyStats)
	}
	if rec := getWithCookie(r, "/api/v1/projects/"+otherKey+"/stats", session); !strings.Contains(
		rec.Body.String(), `"review":0`) {
		t.Errorf(`simple のプロジェクトに "review":0 が無い: %s`, rec.Body.String())
	}

	// ── ③ 履歴の材料を作る（9.13.2）──────────────────────────
	//
	// **activity は直接 INSERT する。** 記録側の経路は 16b / 17a / 18a の結合
	// テストが通してあり、ここで確かめたいのは読み出しである。occurred_at を
	// 手で置けないと、**同じ時刻の2行**（tie-break）を作れない。
	//
	// id は C ロケールの char(26)。**A3 > A2 になるように綴ってある。**
	actID := func(sfx string) string {
		return "01K2ACTINTEG" + strings.Repeat("0", 26-12-len(sfx)) + sfx
	}
	goneTicketID := actID("GONE") // ticket 表に無い ULID（削除済みを模す）
	rows := []struct {
		id       string
		entityID string
		action   string
		field    string
		oldValue string
		newValue string
		minsAgo  int32
		actor    bool
	}{
		{actID("A1"), ticketIDs[1], "create", "", "", "", 40, true},
		{actID("A2"), ticketIDs[1], "update", "title", "旧タイトル", "新タイトル", 30, true},
		{actID("A3"), ticketIDs[1], "update", "due_date", "", "2026-08-14", 30, true},
		{actID("B1"), ticketIDs[2], "transition", "status_key", "todo", "in_progress", 20, true},
		{actID("B2"), goneTicketID, "delete", "", "", "", 10, false},
	}
	for _, c := range rows {
		var actor *string
		if c.actor {
			actor = &adminID
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO activity
			  (id, project_id, entity_type, entity_id, action, field,
			   old_value, new_value, actor_id, occurred_at)
			VALUES ($1, $2, 'ticket', $3, $4, $5, $6, $7, $8,
			   now() - make_interval(mins => $9::int))`,
			c.id, projectID, c.entityID, c.action, nullIfEmptyStr(c.field),
			nullIfEmptyStr(c.oldValue), nullIfEmptyStr(c.newValue), actor,
			c.minsAgo); err != nil {
			t.Fatalf("activity %s を作れない: %v", c.id, err)
		}
	}
	// **別プロジェクトにも1行置く。** project_id の絞り込みが効いていないと
	// 下の total が 6 になる。
	if _, err := pool.Exec(ctx, `
		INSERT INTO activity (id, project_id, entity_type, entity_id, action, occurred_at)
		VALUES ($1, $2, 'ticket', $3, 'create', now())`,
		actID("OTHER"), otherID, actID("OTHERTKT")); err != nil {
		t.Fatalf("別プロジェクトの activity を作れない: %v", err)
	}

	// ── ④ 並び順と非正規化（9.13.2）──────────────────────────
	all := getDashActivity(t, r, session, base+"/activity")
	if all.Total != 5 {
		t.Fatalf("total = %d, want 5（別プロジェクトの1行が混ざっていないか）", all.Total)
	}
	wantOrder := []string{actID("B2"), actID("B1"), actID("A3"), actID("A2"), actID("A1")}
	for i, id := range wantOrder {
		if all.Items[i].ID != id {
			t.Errorf("items[%d].id = %s, want %s（occurred_at DESC, id DESC）",
				i, all.Items[i].ID, id)
		}
	}

	// 削除されたチケットを指す行（9.13.2）。
	del := all.Items[0]
	if del.EntitySeq != nil || del.EntityTitle != nil {
		t.Errorf("削除済みの entity_seq/title = %v/%v, want null/null", del.EntitySeq, del.EntityTitle)
	}
	if del.Actor != nil {
		t.Errorf("actor = %+v, want null", del.Actor)
	}
	if del.EntityID != goneTicketID {
		t.Errorf("entity_id = %q, want %q（行そのものは残る）", del.EntityID, goneTicketID)
	}

	// 生きているチケットを指す行は seq / title が埋まる。
	trans := all.Items[1]
	if trans.EntitySeq == nil || *trans.EntitySeq != 2 {
		t.Errorf("entity_seq = %v, want 2", trans.EntitySeq)
	}
	if trans.EntityTitle == nil || *trans.EntityTitle != "ダッシュボード結合テスト" {
		t.Errorf("entity_title = %v, want ダッシュボード結合テスト", trans.EntityTitle)
	}
	if trans.Actor == nil || trans.Actor.Kind != "user" {
		t.Errorf("actor = %+v, want kind=user", trans.Actor)
	}
	if trans.OldValue == nil || *trans.OldValue != "todo" ||
		trans.NewValue == nil || *trans.NewValue != "in_progress" {
		t.Errorf("old/new = %v/%v, want todo/in_progress（キーのまま）",
			trans.OldValue, trans.NewValue)
	}

	// ── ⑤ 絞り込みとページャ（9.13.2）───────────────────────
	byEntity := getDashActivity(t, r, session, base+"/activity?entity=ticket:1")
	if byEntity.Total != 3 {
		t.Errorf("entity=ticket:1 の total = %d, want 3", byEntity.Total)
	}
	for _, item := range byEntity.Items {
		if item.EntityID != ticketIDs[1] {
			t.Errorf("entity_id = %s, want %s", item.EntityID, ticketIDs[1])
		}
	}

	missing := getDashActivity(t, r, session, base+"/activity?entity=ticket:9999")
	if missing.Total != 0 || len(missing.Items) != 0 {
		t.Errorf("存在しない seq: total = %d / items = %d件, want 0 / 0（全件が返っていないか）",
			missing.Total, len(missing.Items))
	}

	byAction := getDashActivity(t, r, session, base+"/activity?action=update")
	if byAction.Total != 2 {
		t.Errorf("action=update の total = %d, want 2", byAction.Total)
	}

	page2 := getDashActivity(t, r, session, base+"/activity?per_page=2&page=2")
	if page2.Total != 5 || page2.TotalPages != 3 {
		t.Errorf("2ページ目: total = %d / total_pages = %d, want 5 / 3",
			page2.Total, page2.TotalPages)
	}
	if len(page2.Items) != 2 ||
		page2.Items[0].ID != actID("A3") || page2.Items[1].ID != actID("A2") {
		t.Errorf("2ページ目の items = %v, want A3, A2", idsOfActivity(page2.Items))
	}

	// 範囲外のページでも total が返る（Summarize 側から採れているか）。
	page9 := getDashActivity(t, r, session, base+"/activity?per_page=2&page=9")
	if page9.Total != 5 || len(page9.Items) != 0 {
		t.Errorf("範囲外: total = %d / items = %d件, want 5 / 0", page9.Total, len(page9.Items))
	}

	// ── ⑥ ETag（2.7）─────────────────────────────────────────
	e1 := getWithCookie(r, base+"/activity?per_page=2", session).Header().Get("ETag")
	e2 := getWithCookie(r, base+"/activity?per_page=2&page=2", session).Header().Get("ETag")
	if e1 == "" || e2 == "" {
		t.Errorf("ETag が空: %q / %q", e1, e2)
	}
	if e1 == e2 {
		t.Errorf("ページが違うのに ETag が同じ: %s", e1)
	}
	if !strings.HasPrefix(e1, `W/"act-`) {
		t.Errorf("ETag = %s, want W/\"act-… で始まる", e1)
	}
	// **stats には ETag を付けない**（9.13.1）。
	if v := getWithCookie(r, base+"/stats", session).Header().Get("ETag"); v != "" {
		t.Errorf("stats の ETag = %q, want 空", v)
	}

	// ── ⑦ 422（9.13.2）───────────────────────────────────────
	for _, c := range []struct{ query, field string }{
		{"entity=ticket:abc", "entity"},
		{"entity=foo:1", "entity"},
		{"action=bogus", "action"},
		{"action=update,create", "action"},
		{"sort=occurred_at", "sort"},
		{"order=asc", "order"},
	} {
		rec := getWithCookie(r, base+"/activity?"+c.query, session)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422 (%s)", c.query, rec.Code, rec.Body.String())
			continue
		}
		assertDetail(t, rec, c.field, "invalid")
	}
}

// createDashProject はプロジェクトを1つ作り、その ULID を返す。
func createDashProject(
	t *testing.T, r http.Handler, pool *pgxpool.Pool, session, key, template string,
) string {
	t.Helper()
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"ダッシュボード結合テスト","workflow_template":%q}`,
			key, template))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト %s の作成 status = %d（body=%s）", key, rec.Code, rec.Body.String())
	}
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM project WHERE key = $1`, key).Scan(&id); err != nil {
		t.Fatalf("project_id を読めない: %v", err)
	}
	return id
}

func getDashStats(t *testing.T, r http.Handler, session, base string) projectStatsView {
	t.Helper()
	rec := getWithCookie(r, base+"/stats", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	return decodeStats(t, rec)
}

func getDashActivity(t *testing.T, r http.Handler, session, path string) List[activityItem] {
	t.Helper()
	rec := getWithCookie(r, path, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s の status = %d（body=%s）", path, rec.Code, rec.Body.String())
	}
	return decodeActivity(t, rec)
}

func idsOfActivity(items []activityItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// nullIfEmptyStr は空文字を SQL の NULL にする（テスト用の小道具）。
func nullIfEmptyStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// days は due_date のオフセットをポインタで渡すための小道具。
func days(n int32) *int32 { return &n }
