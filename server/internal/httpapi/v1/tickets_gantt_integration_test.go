package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ganttFixture は結合試験の盤面（プロジェクト1つとログイン済みの管理者）。
type ganttFixture struct {
	pool    *pgxpool.Pool
	r       http.Handler
	session string
	key     string
	base    string
}

func newGanttFixture(t *testing.T, label string) ganttFixture {
	t.Helper()
	dsn := os.Getenv("PB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	adminID := ulidgen.New()
	adminEmail := "gnt-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "gn-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":%q,"workflow_template":"simple"}`, key, label))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	return ganttFixture{pool: pool, r: r, session: session, key: key, base: "/api/v1/projects/" + key}
}

func (f ganttFixture) create(t *testing.T, title string, parent int) int {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"type":"task"}`, title)
	if parent > 0 {
		body = fmt.Sprintf(`{"title":%q,"type":"task","parent_seq":%d}`, title, parent)
	}
	rec := postWithCookie(f.r, f.base+"/tickets", f.session, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("起票 %q の status = %d（body=%s）", title, rec.Code, rec.Body.String())
	}
	var v struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("起票の応答を読めない: %v", err)
	}
	return v.Seq
}

func (f ganttFixture) link(t *testing.T, src, dst int, linkType string) {
	t.Helper()
	rec := postWithCookie(f.r, fmt.Sprintf("%s/tickets/%d/links", f.base, src), f.session,
		fmt.Sprintf(`{"target_seq":%d,"link_type":%q}`, dst, linkType))
	if rec.Code != http.StatusCreated {
		t.Fatalf("依存 %d -%s-> %d の status = %d（body=%s）", src, linkType, dst, rec.Code, rec.Body.String())
	}
}

func (f ganttFixture) gantt(t *testing.T, query string) (ganttRespJSON, string) {
	t.Helper()
	path := f.base + "/tickets?view=gantt"
	if query != "" {
		path += "&" + query
	}
	rec := getWithCookie(f.r, path, f.session)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s の status = %d（body=%s）", path, rec.Code, rec.Body.String())
	}
	var v ganttRespJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	return v, rec.Header().Get("ETag")
}

func ganttSeqs(v ganttRespJSON) []int {
	out := make([]int, 0, len(v.Items))
	for _, it := range v.Items {
		out = append(out, int(it.Seq))
	}
	sort.Ints(out)
	return out
}

func ganttLinkKeys(v ganttRespJSON) []string {
	out := make([]string, 0, len(v.Links))
	for _, l := range v.Links {
		out = append(out, fmt.Sprintf("%d-%s-%d", l.SourceSeq, l.LinkType, l.TargetSeq))
	}
	return out
}

// view=gantt を**実際のDBに対して**通す（ApiDesign.md 9.2.6）。
//
// 単体試験はフェイクを差し替えるので、ListLinksAmongTickets と sprint=active の SQL は
// 一度も実行されない。**ここでしか確かめられないものが4つある。**
//
//   - 依存は**両端が結果に含まれるものだけ**が返る（絞り込みで片方が落ちた線は返らない）
//   - relates / duplicates は返らず、blocks は返る
//   - sprint=active が進行中のスプリントの行だけを返し、進行中が無ければ何にも当たらない
//   - 依存を足すと ETag が変わる（チケットの updated_at は動かない）
//
// 題名には多バイト文字を含める（学びと知見「試験をするときは多バイト言語も意識する」）。
func TestTicketGanttViewIntegration(t *testing.T) {
	f := newGanttFixture(t, "ガント取得の結合テスト")
	ctx := context.Background()

	// ── 盤面 ─────────────────────────────────────────────
	//   A ガントチャート画面を提供する
	//   ├ B デザイン案を作り、承認を得る
	//   └ C 依存を全件返す API
	//   D 集中モード
	//   E 受け入れを確認する
	a := f.create(t, "ガントチャート画面を提供する", 0)
	b := f.create(t, "デザイン案を作り、承認を得る", a)
	c := f.create(t, "依存を全件返す API（ガント用）", a)
	d := f.create(t, "メインメニューを 0px まで畳む集中モード", 0)
	e := f.create(t, "受け入れを確認する", 0)

	f.link(t, b, c, "FS")
	f.link(t, c, d, "blocks")
	f.link(t, b, d, "relates")    // ガントは描かない
	f.link(t, d, e, "duplicates") // 同上
	f.link(t, e, d, "SS")

	// 全件：relates / duplicates を除く3本
	all, etag1 := f.gantt(t, "")
	if got, want := ganttSeqs(all), []int{a, b, c, d, e}; !slices.Equal(got, want) {
		t.Errorf("items = %v、want %v", got, want)
	}
	want := []string{
		fmt.Sprintf("%d-FS-%d", b, c),
		fmt.Sprintf("%d-blocks-%d", c, d),
		fmt.Sprintf("%d-SS-%d", e, d),
	}
	if got := ganttLinkKeys(all); !slices.Equal(got, want) {
		t.Errorf("links = %v、want %v（source_seq → target_seq の順、relates/duplicates を除く）", got, want)
	}
	for _, l := range all.Links {
		if l.Origin != "human" || l.ID == "" {
			t.Errorf("依存の形が違う: %+v", l)
		}
	}

	// 部分木（A の配下）に絞ると、D が落ちて c→d の線も消える
	sub, _ := f.gantt(t, fmt.Sprintf("parent=%d", a))
	if got, want := ganttSeqs(sub), []int{a, b, c}; !slices.Equal(got, want) {
		t.Errorf("parent=%d の items = %v、want %v", a, got, want)
	}
	if got, want := ganttLinkKeys(sub), []string{fmt.Sprintf("%d-FS-%d", b, c)}; !slices.Equal(got, want) {
		t.Errorf("parent=%d の links = %v、want %v（片方が落ちた線は返さない）", a, got, want)
	}

	// 依存を足すと ETag が変わる（チケットの updated_at は動かない。9.10.1）
	f.link(t, a, e, "FF")
	_, etag2 := f.gantt(t, "")
	if etag1 == etag2 {
		t.Errorf("依存を足しても ETag が同じ: %s", etag1)
	}

	// ── sprint=active ──────────────────────────────────
	var projectID string
	if err := f.pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, f.key).Scan(&projectID); err != nil {
		t.Fatalf("プロジェクトの ID を引けない: %v", err)
	}
	// 進行中が無いときは何にも当たらない
	none, _ := f.gantt(t, "sprint=active")
	if len(none.Items) != 0 {
		t.Errorf("進行中のスプリントが無いのに %d 件返った", len(none.Items))
	}
	active, planned := ulidgen.New(), ulidgen.New()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO sprint (id, project_id, name, status) VALUES ($1, $3, 'スプリント 15', 'active'),
		                                                            ($2, $3, 'スプリント 16', 'planned')`,
		active, planned, projectID); err != nil {
		t.Fatalf("スプリントを作れない: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE ticket SET sprint_id = CASE seq WHEN $2 THEN $3 ELSE $4 END
		  WHERE project_id = $1 AND seq IN ($2, $5)`,
		projectID, c, active, planned, d); err != nil {
		t.Fatalf("スプリントへ割り当てられない: %v", err)
	}
	cur, _ := f.gantt(t, "sprint=active")
	if got, want := ganttSeqs(cur), []int{c}; !slices.Equal(got, want) {
		t.Errorf("sprint=active の items = %v、want %v", got, want)
	}
	// ULID・none と併記すると OR
	or, _ := f.gantt(t, "sprint=active,"+planned)
	if got, want := ganttSeqs(or), []int{c, d}; !slices.Equal(got, want) {
		t.Errorf("sprint=active,<planned> の items = %v、want %v", got, want)
	}
	// 通常の一覧でも同じ条件が効く（9.2.1 の共通の絞り込み）
	rec := getWithCookie(f.r, f.base+"/tickets?sprint=active", f.session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("一覧の sprint=active: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// 性能の目安（pb-219 のゴール条件）。チケット 2,000件・依存 4,000本で、応答時間と大きさを測る。
//
// **PB_TEST_PERF=1 のときだけ走る**（数値を記録するための測定で、合否の閾値は置かない）。
//
//	PB_TEST_PERF=1 make test-db RUN=TestTicketGanttPerfIntegration
//	PB_TEST_PERF=1 PB_TEST_PERF_TICKETS=4999 make test-db RUN=TestTicketGanttPerfIntegration
//
// データは SQL でまとめて入れる（API で 6,000回叩くと測定より準備が長くなる）。題名は多バイト文字。
func TestTicketGanttPerfIntegration(t *testing.T) {
	if os.Getenv("PB_TEST_PERF") != "1" {
		t.Skip("PB_TEST_PERF=1 のときだけ走る")
	}
	f := newGanttFixture(t, "ガント性能の測定")
	ctx := context.Background()

	// 状態キーと起票者は、API で作った1件から写す
	seed := f.create(t, "性能測定の起点", 0)
	var projectID, statusKey string
	if err := f.pool.QueryRow(ctx,
		`SELECT project_id, status_key FROM ticket t JOIN project p ON p.id = t.project_id
		  WHERE p.key = $1 AND t.seq = $2`, f.key, seed).Scan(&projectID, &statusKey); err != nil {
		t.Fatalf("起点を引けない: %v", err)
	}

	// 件数は PB_TEST_PERF_TICKETS で変えられる（既定 2,000。依存はその2倍）。上限（5,000）での値を見るため
	tickets := 2000
	if v, err := strconv.Atoi(os.Getenv("PB_TEST_PERF_TICKETS")); err == nil && v > 0 {
		tickets = v
	}
	links := tickets * 2
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// 10件ごとの先頭（1, 11, 21 …）を親にし、配下に9件ぶら下げる。予定は 0.1日ずつずらした3日間
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, parent_id, type, title, status_key, sort_key, start_at, due_at)
		SELECT 'PERF' || lpad(n::text, 22, '0'), $1, n + 1,
		       CASE WHEN n % 10 = 1 THEN NULL ELSE 'PERF' || lpad(((n - 1) / 10 * 10 + 1)::text, 22, '0') END,
		       'task',
		       'ガントの性能を測るチケット 第' || n || '号（予定と依存を持つ）',
		       $2, '0|' || lpad(n::text, 6, '0') || ':',
		       $3::timestamptz + (n || ' days')::interval / 10,
		       $3::timestamptz + (n || ' days')::interval / 10 + interval '3 days'
		  FROM generate_series(1, $4::int) AS n`,
		projectID, statusKey, day, tickets); err != nil {
		t.Fatalf("チケットを入れられない: %v", err)
	}
	// n → n+1 / n+2 / n+3 から 4,000本。種別は5つを巡回させる
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO ticket_link (id, source_ticket_id, target_ticket_id, link_type)
		SELECT 'PLNK' || lpad(k::text, 22, '0'),
		       'PERF' || lpad(src::text, 22, '0'),
		       'PERF' || lpad((src + step)::text, 22, '0'),
		       (ARRAY['FS','SS','FF','SF','blocks'])[1 + k % 5]
		  FROM (SELECT row_number() OVER () AS k, src, step
		          FROM generate_series(1, $1::int - 3) AS src, (VALUES (1), (2), (3)) AS s(step)) x
		 WHERE k <= $2`,
		tickets, links); err != nil {
		t.Fatalf("依存を入れられない: %v", err)
	}

	const runs = 5
	var times []time.Duration
	var size int
	var last ganttRespJSON
	for i := 0; i < runs; i++ {
		start := time.Now()
		rec := getWithCookie(f.r, f.base+"/tickets?view=gantt", f.session)
		times = append(times, time.Since(start))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		size = rec.Body.Len()
		if i == runs-1 {
			if err := json.Unmarshal(rec.Body.Bytes(), &last); err != nil {
				t.Fatalf("応答を読めない: %v", err)
			}
		}
	}
	slices.Sort(times)
	if want := min(tickets+1, ganttMaxTickets); len(last.Items) != want || len(last.Links) > links {
		t.Errorf("件数が違う: items=%d links=%d（want %d / 最大 %d）", len(last.Items), len(last.Links), min(tickets+1, ganttMaxTickets), links)
	}
	t.Logf("チケット %d 件・依存 %d 本：応答 %.2f MB、時間 最小 %s／中央 %s／最大 %s（%d 回）",
		len(last.Items), len(last.Links), float64(size)/1024/1024,
		times[0].Round(time.Millisecond), times[runs/2].Round(time.Millisecond),
		times[runs-1].Round(time.Millisecond), runs)
}
