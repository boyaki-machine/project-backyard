package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// タイムライン（ApiDesign.md 9.2.7・9.17。pb-296）を実 DB に対して通す。
//
// 単体テスト（timeline_chain_test.go）は連鎖の計算だけを見る。**ここでしか確かめられないもの**：
//
//   - view=timeline の対象（選んだエピックの配下・時刻付き）と epics[]・baselines、304
//   - reschedule の原子性（途中の書き込みで失敗したら何も書かれない）・行のロックによる同時更新の 409・
//     expect の 409 stale_plan・変更履歴の request_id
//   - 当初計画の版の保存・一覧・1版（ETag と 304）・削除、権限（project_member は保存・削除できない）
//
// 題名には多バイト文字を含める（学びと知見「試験をするときは多バイト言語も意識する」）。

// faultTx は SetTicketSchedule の n 回目で失敗させる TxRunner（原子性の確認用）。0 なら素通し。
type faultTx struct {
	inner  TxRunner
	failOn atomic.Int32
}

type faultQuerier struct {
	gen.Querier
	calls  *int32
	failOn int32
}

var errInjected = errors.New("試験で注入した失敗")

func (q faultQuerier) SetTicketSchedule(ctx context.Context, arg gen.SetTicketScheduleParams) (int32, error) {
	*q.calls++
	if q.failOn > 0 && *q.calls >= q.failOn {
		return 0, errInjected
	}
	return q.Querier.SetTicketSchedule(ctx, arg)
}

func (f *faultTx) RunInTx(ctx context.Context, fn func(gen.Querier) error) error {
	failOn := f.failOn.Load()
	return f.inner.RunInTx(ctx, func(q gen.Querier) error {
		var calls int32
		return fn(faultQuerier{Querier: q, calls: &calls, failOn: failOn})
	})
}

type timelineFixture struct {
	pool      *pgxpool.Pool
	r         http.Handler
	tx        *faultTx
	session   string
	key       string
	projectID string
	base      string
}

func newTimelineFixture(t *testing.T) timelineFixture {
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
	ftx := &faultTx{inner: store.NewTxRunner(pool)}
	r := routerWithDeps(Deps{Queries: q, Tx: ftx})

	adminID := ulidgen.New()
	adminEmail := "tl-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "tl-" + strings.ToLower(adminID[len(adminID)-8:])
	var projectID string
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
		// 後始末で行が残らないこと（版・版の行・チケット・履歴）
		if projectID == "" {
			return
		}
		var n int
		if err := pool.QueryRow(bg, `
			SELECT (SELECT count(*) FROM schedule_baseline WHERE project_id = $1)
			     + (SELECT count(*) FROM schedule_baseline_item i
			         WHERE NOT EXISTS (SELECT 1 FROM schedule_baseline b WHERE b.id = i.baseline_id))
			     + (SELECT count(*) FROM ticket WHERE project_id = $1)
			     + (SELECT count(*) FROM activity WHERE project_id = $1)`, projectID).Scan(&n); err != nil {
			t.Errorf("後始末を数えられない: %v", err)
		} else if n != 0 {
			t.Errorf("後始末の後に %d 行残った", n)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"文化祭タイムライン","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, key).Scan(&projectID); err != nil {
		t.Fatalf("プロジェクトの id を読めない: %v", err)
	}
	return timelineFixture{pool: pool, r: r, tx: ftx, session: session, key: key, projectID: projectID, base: "/api/v1/projects/" + key}
}

// do は Cookie 認証で1リクエスト投げる。headers は "If-Match" などを足す。
func (f timelineFixture) do(method, path, session, body string, headers ...string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	addCSRF(req)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

// jst2030 は 2030-10-24（JST）の時:分。「いま」より十分先にして「いま」の規則に掛けない。
func jst2030(h, m int) int64 {
	return time.Date(2030, 10, 24, h, m, 0, 0, chainTestLoc).UnixMilli()
}

func (f timelineFixture) createTicket(t *testing.T, body string) int32 {
	t.Helper()
	rec := f.do(http.MethodPost, f.base+"/tickets", f.session, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("起票 %s の status = %d（body=%s）", body, rec.Code, rec.Body.String())
	}
	var v struct {
		Seq int32 `json:"seq"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Seq
}

func (f timelineFixture) timed(t *testing.T, title string, parent int32, sh, sm, eh, em int, noOverlap bool) int32 {
	t.Helper()
	return f.createTicket(t, fmt.Sprintf(
		`{"type":"task","title":%q,"parent_seq":%d,"start_at":%d,"due_at":%d,"all_day":false,"no_overlap":%t}`,
		title, parent, jst2030(sh, sm), jst2030(eh, em), noOverlap))
}

type tlTicket struct {
	Seq         int32  `json:"seq"`
	Title       string `json:"title"`
	StartAt     *int64 `json:"start_at"`
	DueAt       *int64 `json:"due_at"`
	NoOverlap   bool   `json:"no_overlap"`
	StartPinned bool   `json:"start_pinned"`
	DuePinned   bool   `json:"due_pinned"`
	Version     int32  `json:"version"`
	EpicSeq     *int32 `json:"epic_seq"`
	StartedAt   *int64 `json:"started_at"`
	LastChanged *struct {
		DisplayName string `json:"display_name"`
	} `json:"last_changed_by"`
}

type tlResp struct {
	Items []tlTicket `json:"items"`
	Epics []struct {
		Seq   int32  `json:"seq"`
		Title string `json:"title"`
	} `json:"epics"`
	Links     []ganttLinkItem `json:"links"`
	Baselines struct {
		Count    int     `json:"count"`
		LatestID *string `json:"latest_id"`
	} `json:"baselines"`
}

func (f timelineFixture) timeline(t *testing.T) (tlResp, string) {
	t.Helper()
	rec := f.do(http.MethodGet, f.base+"/tickets?view=timeline", f.session, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("view=timeline の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var v tlResp
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v, rec.Header().Get("ETag")
}

func (f timelineFixture) ticket(t *testing.T, seq int32) tlTicket {
	t.Helper()
	rec := f.do(http.MethodGet, fmt.Sprintf("%s/tickets/%d", f.base, seq), f.session, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("詳細 %d の status = %d", seq, rec.Code)
	}
	var v tlTicket
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f timelineFixture) reschedule(t *testing.T, seq int32, version int32, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(http.MethodPost, fmt.Sprintf("%s/tickets/%d/reschedule", f.base, seq), f.session, body,
		"If-Match", fmt.Sprintf(`"%d"`, version))
}

func (f timelineFixture) activityCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM activity WHERE project_id = $1`, f.projectID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type rescheduleJSON struct {
	Applied bool          `json:"applied"`
	Result  string        `json:"result"`
	Changes []chainChange `json:"changes"`
	Block   *chainBlock   `json:"block"`
	Alt     *struct {
		Changes []chainChange `json:"changes"`
	} `json:"alternative"`
	Items []tlTicket `json:"items"`
}

func decodeReschedule(t *testing.T, rec *httptest.ResponseRecorder) rescheduleJSON {
	t.Helper()
	var v rescheduleJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v（%s）", err, rec.Body.String())
	}
	return v
}

func expectOf(r rescheduleJSON, target int32) string {
	parts := []string{}
	for _, c := range r.Changes {
		if c.Seq != target {
			parts = append(parts, fmt.Sprintf(`{"seq":%d,"version":%d}`, c.Seq, c.Version))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestTimelineIntegration(t *testing.T) {
	f := newTimelineFixture(t)
	ctx := context.Background()

	stage := f.createTicket(t, `{"type":"epic","title":"体育館ステージ"}`)
	other := f.createTicket(t, `{"type":"epic","title":"中庭（載せない）"}`)
	a := f.timed(t, "吹奏楽部「秋の調べ」", stage, 10, 0, 10, 30, true)
	b := f.timed(t, "ダンス部「Re:Start」", stage, 10, 40, 11, 10, true)
	c := f.timed(t, "演劇部「夏の夜の夢」", stage, 11, 10, 11, 40, true)
	prep := f.timed(t, "準備・片付け", stage, 9, 30, 12, 0, false)
	f.createTicket(t, fmt.Sprintf(`{"type":"task","title":"終日の掲示","parent_seq":%d}`, stage))
	f.timed(t, "中庭ライブ", other, 10, 0, 11, 0, true)
	f.do(http.MethodPost, fmt.Sprintf("%s/tickets/%d/links", f.base, a), f.session, fmt.Sprintf(`{"target_seq":%d,"link_type":"FS"}`, b))

	if _, err := f.pool.Exec(ctx, `UPDATE project SET settings = $2::jsonb, version = version + 1 WHERE id = $1`,
		f.projectID, fmt.Sprintf(`{"timeline_epics":[%d, 99999],"timeline_snap_seconds":60}`, stage)); err != nil {
		t.Fatal(err)
	}

	// ── view=timeline（9.2.7）──────────────────────────────
	tl, etag := f.timeline(t)
	seqs := []int{}
	for _, it := range tl.Items {
		seqs = append(seqs, int(it.Seq))
		if it.EpicSeq == nil || *it.EpicSeq != stage {
			t.Errorf("%d の epic_seq = %v", it.Seq, it.EpicSeq)
		}
	}
	sort.Ints(seqs)
	if want := []int{int(a), int(b), int(c), int(prep)}; !slices.Equal(seqs, want) {
		t.Fatalf("items = %v, want %v（選んだエピックの配下の時刻付きだけ）", seqs, want)
	}
	if len(tl.Epics) != 1 || tl.Epics[0].Seq != stage || tl.Epics[0].Title != "体育館ステージ" {
		t.Fatalf("epics = %+v", tl.Epics)
	}
	if len(tl.Links) != 1 || tl.Links[0].SourceSeq != a || tl.Links[0].LinkType != "FS" {
		t.Fatalf("links = %+v", tl.Links)
	}
	if tl.Baselines.Count != 0 || tl.Baselines.LatestID != nil {
		t.Fatalf("baselines = %+v", tl.Baselines)
	}
	if it := tl.Items[0]; it.LastChanged == nil || it.StartedAt != nil {
		t.Fatalf("last_changed_by / started_at = %+v", it)
	}

	// 304（9.2.5）。一覧も timeline も、変わっていなければ本体を返さない
	if rec := f.do(http.MethodGet, f.base+"/tickets?view=timeline", f.session, "", "If-None-Match", etag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("If-None-Match の status = %d", rec.Code)
	}
	plain := f.do(http.MethodGet, f.base+"/tickets", f.session, "")
	if rec := f.do(http.MethodGet, f.base+"/tickets", f.session, "", "If-None-Match", plain.Header().Get("ETag")); rec.Code != http.StatusNotModified {
		t.Fatalf("一覧の If-None-Match の status = %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, f.base+"/tickets?view=timeline", f.session, "", "If-None-Match", plain.Header().Get("ETag")); rec.Code != http.StatusOK {
		t.Fatalf("別の view の ETag で 304 になった: %d", rec.Code)
	}

	// ずらしを変えると、view=timeline と view=gantt の ETag が変わる（9.2.5。pb-335）。依存の PATCH は
	// どちらのチケットの updated_at も動かさないので、件数と MAX(created_at) だけでは 304 が返り続けた
	first := etag
	ganttTag := f.do(http.MethodGet, f.base+"/tickets?view=gantt", f.session, "").Header().Get("ETag")
	linkPath := fmt.Sprintf("%s/tickets/%d/links/%s", f.base, a, tl.Links[0].ID)
	for _, lag := range []string{`{"lag_seconds":600}`, `{"lag_days":1}`} {
		if rec := f.do(http.MethodPatch, linkPath, f.session, lag); rec.Code != http.StatusOK {
			t.Fatalf("ずらし %s の status = %d（%s）", lag, rec.Code, rec.Body.String())
		}
		rec := f.do(http.MethodGet, f.base+"/tickets?view=timeline", f.session, "", "If-None-Match", etag)
		if rec.Code != http.StatusOK {
			t.Fatalf("ずらし %s の後の view=timeline が %d（200 のはず）", lag, rec.Code)
		}
		var moved tlResp
		if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil || len(moved.Links) != 1 || moved.Links[0].LagSeconds != 600 {
			t.Fatalf("ずらし %s の後の links = %+v（%v）", lag, moved.Links, err)
		}
		etag = rec.Header().Get("ETag")
		if rec := f.do(http.MethodGet, f.base+"/tickets?view=gantt", f.session, "", "If-None-Match", ganttTag); rec.Code != http.StatusOK {
			t.Fatalf("ずらし %s の後の view=gantt が %d（200 のはず）", lag, rec.Code)
		} else {
			ganttTag = rec.Header().Get("ETag")
		}
	}
	// 戻すと、中身が同じなので ETag も最初と同じになる（後ろの連鎖の検証のため、ずらし無しに戻す）
	if rec := f.do(http.MethodPatch, linkPath, f.session, `{"lag_days":0,"lag_seconds":0}`); rec.Code != http.StatusOK {
		t.Fatalf("ずらしを戻せない: %d", rec.Code)
	}
	if _, back := f.timeline(t); back != first {
		t.Fatalf("ずらしを戻した ETag = %s, want %s（最初と同じ）", back, first)
	}

	// 着手すると started_at が載り、ETag が変わる
	if rec := f.do(http.MethodPost, fmt.Sprintf("%s/tickets/%d/transition", f.base, prep), f.session, `{"to":"in_progress"}`); rec.Code != http.StatusOK {
		t.Fatalf("着手の status = %d（%s）", rec.Code, rec.Body.String())
	}
	tl2, etag2 := f.timeline(t)
	if etag2 == etag {
		t.Fatal("着手しても ETag が変わらない")
	}
	for _, it := range tl2.Items {
		if it.Seq == prep && it.StartedAt == nil {
			t.Fatal("着手したのに started_at が null")
		}
	}

	// ── reschedule：検証（9.17.2）───────────────────────────
	av := f.ticket(t, a).Version
	if rec := f.do(http.MethodPost, fmt.Sprintf("%s/tickets/%d/reschedule", f.base, a), f.session,
		fmt.Sprintf(`{"due_at":%d}`, jst2030(10, 45))); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "If-Match") {
		t.Fatalf("If-Match なしの status = %d（%s）", rec.Code, rec.Body.String())
	}

	// ── 試算 → 確定（expect）─────────────────────────────────
	before, bVersion := f.activityCount(t), f.ticket(t, b).Version
	rec := f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"dry_run":true}`, jst2030(10, 45)))
	if rec.Code != http.StatusOK {
		t.Fatalf("試算の status = %d（%s）", rec.Code, rec.Body.String())
	}
	dry := decodeReschedule(t, rec)
	if dry.Applied || dry.Result != "ok" || len(dry.Changes) != 3 {
		t.Fatalf("試算 = %+v", dry)
	}
	if f.ticket(t, b).Version != bVersion || f.activityCount(t) != before {
		t.Fatal("試算で書いた")
	}

	var confirmedAt time.Time
	if err := f.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&confirmedAt); err != nil {
		t.Fatal(err)
	}
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"expect":%s}`, jst2030(10, 45), expectOf(dry, a)))
	if rec.Code != http.StatusOK {
		t.Fatalf("確定の status = %d（%s）", rec.Code, rec.Body.String())
	}
	done := decodeReschedule(t, rec)
	if !done.Applied || len(done.Items) != 3 {
		t.Fatalf("確定 = %+v", done)
	}
	bt := f.ticket(t, b)
	// B は A の FS の後続なので A の終わり（10:45）に寄り、C は B に押される
	if *bt.StartAt != jst2030(10, 45) || *bt.DueAt != jst2030(11, 15) {
		t.Fatalf("B = %d–%d", *bt.StartAt, *bt.DueAt)
	}
	if ct := f.ticket(t, c); *ct.StartAt != jst2030(11, 15) {
		t.Fatalf("C の開始 = %d", *ct.StartAt)
	}
	if pt := f.ticket(t, prep); *pt.StartAt != jst2030(9, 30) {
		t.Fatal("時間重複チェックの無いものが動いた")
	}
	// 変更履歴：動いた行ごとに start_at / due_at を記録し、同じ request_id を付ける
	var entities, requests int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(DISTINCT entity_id), count(DISTINCT coalesce(request_id, '-')) FROM activity
		 WHERE project_id = $1 AND field IN ('start_at','due_at') AND occurred_at >= $2`,
		f.projectID, confirmedAt).Scan(&entities, &requests); err != nil {
		t.Fatal(err)
	}
	if entities != 3 || requests != 1 {
		t.Fatalf("履歴：チケット %d 件・request_id %d 種", entities, requests)
	}

	// ── 409 stale_plan：試算の後に誰かが動かした ────────────────
	av = f.ticket(t, a).Version
	dry = decodeReschedule(t, f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"dry_run":true}`, jst2030(10, 50))))
	bt = f.ticket(t, b)
	if rec := f.do(http.MethodPatch, fmt.Sprintf("%s/tickets/%d", f.base, b), f.session, `{"title":"ダンス部「Re:Start」改"}`,
		"If-Match", fmt.Sprintf(`"%d"`, bt.Version)); rec.Code != http.StatusOK {
		t.Fatalf("PATCH の status = %d", rec.Code)
	}
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"expect":%s}`, jst2030(10, 50), expectOf(dry, a)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stale_plan") || !strings.Contains(rec.Body.String(), `"retrial"`) {
		t.Fatalf("stale の status = %d（%s）", rec.Code, rec.Body.String())
	}

	// ── 原子性：2行目の書き込みで失敗したら何も書かれない ────────────
	snapshot := func() string {
		var s string
		if err := f.pool.QueryRow(ctx, `SELECT string_agg(seq || ':' || start_at || ':' || due_at || ':' || version, ',' ORDER BY seq)
			FROM ticket WHERE project_id = $1 AND start_at IS NOT NULL`, f.projectID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	beforeRows, beforeActs := snapshot(), f.activityCount(t)
	f.tx.failOn.Store(2)
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d}`, jst2030(11, 0)))
	f.tx.failOn.Store(0)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("注入した失敗の status = %d（%s）", rec.Code, rec.Body.String())
	}
	if snapshot() != beforeRows || f.activityCount(t) != beforeActs {
		t.Fatal("途中で失敗したのに書かれた行がある")
	}

	// ── 同時更新：同じ版で2本同時に確定すると、片方は 409 ──────────
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d}`, jst2030(10, 55+i))).Code
		}()
	}
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != http.StatusOK || codes[1] != http.StatusConflict {
		t.Fatalf("同時の確定 = %v（200 と 409 のはず）", codes)
	}

	// ── 解けない：開始のアンカーに当たる ───────────────────────
	ct := f.ticket(t, c)
	if rec := f.do(http.MethodPatch, fmt.Sprintf("%s/tickets/%d", f.base, c), f.session, `{"start_pinned":true}`,
		"If-Match", fmt.Sprintf(`"%d"`, ct.Version)); rec.Code != http.StatusOK {
		t.Fatalf("アンカーの PATCH = %d（%s）", rec.Code, rec.Body.String())
	}
	av = f.ticket(t, a).Version
	// C の開始（アンカー）は 11:25 か 11:26。A を 11:00 まで延ばすと B が数分食い込む
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"dry_run":true}`, jst2030(11, 0)))
	blocked := decodeReschedule(t, rec)
	if rec.Code != http.StatusOK || blocked.Result != "blocked" || blocked.Block == nil || blocked.Block.Code != "cannot_fit" || blocked.Block.Seq != c {
		t.Fatalf("試算の block = %d %+v", rec.Code, blocked)
	}
	if blocked.Alt == nil {
		t.Fatal("縮めれば収まるのに alternative が無い")
	}
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d}`, jst2030(11, 0)))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cannot_fit") || !strings.Contains(rec.Body.String(), "演劇部") {
		t.Fatalf("確定の cannot_fit = %d（%s）", rec.Code, rec.Body.String())
	}
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"on_block":"shrink"}`, jst2030(11, 0)))
	if rec.Code != http.StatusOK {
		t.Fatalf("shrink の status = %d（%s）", rec.Code, rec.Body.String())
	}
	if bt, ct := f.ticket(t, b), f.ticket(t, c); *bt.StartAt != jst2030(11, 0) || *bt.DueAt != *ct.StartAt {
		t.Fatalf("B が C の開始のアンカーまでに縮んでいない: B %d–%d C %d", *bt.StartAt, *bt.DueAt, *ct.StartAt)
	}

	// ── 当初計画の版（9.17.3）──────────────────────────────────
	memberID := ulidgen.New()
	memberEmail := "tl-member-" + memberID + "@example.com"
	seedUserWithRole(t, ctx, f.pool, gen.New(f.pool), memberID, memberEmail, auth.SystemRoleOperator)
	if _, err := f.pool.Exec(ctx, `INSERT INTO project_member (project_id, actor_id, role_key) VALUES ($1, $2, 'project_member')`,
		f.projectID, memberID); err != nil {
		t.Fatal(err)
	}
	memberSession := loginAs(t, f.r, memberEmail)
	if rec := f.do(http.MethodPost, f.base+"/schedule-baselines", memberSession, `{"label":"当日朝"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("project_member の保存 = %d", rec.Code)
	}
	rec = f.do(http.MethodPost, f.base+"/schedule-baselines", f.session, `{"label":"  当日朝・リハーサル後  "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("保存の status = %d（%s）", rec.Code, rec.Body.String())
	}
	var saved struct {
		ID        string  `json:"id"`
		Label     *string `json:"label"`
		ItemCount int     `json:"item_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Label == nil || *saved.Label != "当日朝・リハーサル後" || saved.ItemCount != 4 ||
		rec.Header().Get("Location") != f.base+"/schedule-baselines/"+saved.ID {
		t.Fatalf("保存 = %+v Location=%s", saved, rec.Header().Get("Location"))
	}
	// 本文なしでも保存できる（label は null）
	if rec := f.do(http.MethodPost, f.base+"/schedule-baselines", f.session, ""); rec.Code != http.StatusCreated {
		t.Fatalf("本文なしの保存 = %d（%s）", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, f.base+"/schedule-baselines", f.session, fmt.Sprintf(`{"label":%q}`, strings.Repeat("版", 101))); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("101文字の label = %d", rec.Code)
	}

	rec = f.do(http.MethodGet, f.base+"/schedule-baselines", memberSession, "")
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Items) != 2 || list.Items[1].ID != saved.ID {
		t.Fatalf("一覧 = %d %+v", rec.Code, list)
	}
	tl3, _ := f.timeline(t)
	if tl3.Baselines.Count != 2 || tl3.Baselines.LatestID == nil || *tl3.Baselines.LatestID != list.Items[0].ID {
		t.Fatalf("baselines = %+v", tl3.Baselines)
	}

	rec = f.do(http.MethodGet, f.base+"/schedule-baselines/"+saved.ID, memberSession, "")
	var one struct {
		Items []struct {
			TicketSeq int32  `json:"ticket_seq"`
			Exists    bool   `json:"exists"`
			Title     string `json:"title"`
			EpicTitle string `json:"epic_title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || rec.Code != http.StatusOK || len(one.Items) != 4 {
		t.Fatalf("1版 = %d %s", rec.Code, rec.Body.String())
	}
	if one.Items[0].EpicTitle != "体育館ステージ" || !one.Items[0].Exists {
		t.Fatalf("版の行 = %+v", one.Items[0])
	}
	if r2 := f.do(http.MethodGet, f.base+"/schedule-baselines/"+saved.ID, memberSession, "", "If-None-Match", rec.Header().Get("ETag")); r2.Code != http.StatusNotModified {
		t.Fatalf("1版の 304 = %d", r2.Code)
	}
	// チケットを消すと exists が偽になる（版の行は残る）
	if r2 := f.do(http.MethodDelete, fmt.Sprintf("%s/tickets/%d", f.base, prep), f.session, ""); r2.Code != http.StatusNoContent {
		t.Fatalf("チケットの削除 = %d", r2.Code)
	}
	rec = f.do(http.MethodGet, f.base+"/schedule-baselines/"+saved.ID, f.session, "")
	if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"ticket_seq":%d,"exists":false`, prep)) {
		t.Fatalf("消したチケットの exists: %s", rec.Body.String())
	}

	if rec := f.do(http.MethodDelete, f.base+"/schedule-baselines/"+saved.ID, memberSession, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("project_member の削除 = %d", rec.Code)
	}
	if rec := f.do(http.MethodDelete, f.base+"/schedule-baselines/"+saved.ID, f.session, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("削除 = %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, f.base+"/schedule-baselines/"+saved.ID, f.session, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("消した版 = %d", rec.Code)
	}
	if rec := f.do(http.MethodDelete, f.base+"/schedule-baselines/"+saved.ID, f.session, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("消した版の削除 = %d", rec.Code)
	}
	var items int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM schedule_baseline_item WHERE baseline_id = $1`, saved.ID).Scan(&items); err != nil || items != 0 {
		t.Fatalf("版の行が残った: %d %v", items, err)
	}
}

// 依存の4種・行またぎ・秒のずらし（ApiDesign.md 9.17.1・9.10.1。pb-332）を実 DB に通す。
// 単体テストでは確かめられない、プロジェクト全体の依存の読み込み・行の外のチケットのロックと書き込み・
// links[] の行またぎ・lag_seconds の保存を見る。
func TestTimelineDependencyIntegration(t *testing.T) {
	f := newTimelineFixture(t)
	ctx := context.Background()

	gym := f.createTicket(t, `{"type":"epic","title":"体育館ステージ"}`)
	light := f.createTicket(t, `{"type":"epic","title":"照明・音響"}`)
	back := f.createTicket(t, `{"type":"epic","title":"裏方（載せない）"}`)
	a := f.timed(t, "吹奏楽部「秋の調べ」", gym, 10, 0, 10, 30, true)
	x := f.timed(t, "照明の転換", light, 10, 30, 11, 0, true)
	y := f.timed(t, "音響チェック", light, 11, 0, 11, 30, true)
	z := f.timed(t, "楽器の搬出", back, 10, 0, 10, 30, false)
	q := f.timed(t, "控室の案内", back, 12, 0, 12, 30, false)
	if _, err := f.pool.Exec(ctx, `UPDATE project SET settings = $2::jsonb, version = version + 1 WHERE id = $1`,
		f.projectID, fmt.Sprintf(`{"timeline_epics":[%d,%d],"timeline_snap_seconds":60}`, gym, light)); err != nil {
		t.Fatal(err)
	}

	link := func(src, dst int32, typ, extra string) string {
		t.Helper()
		rec := f.do(http.MethodPost, fmt.Sprintf("%s/tickets/%d/links", f.base, src), f.session,
			fmt.Sprintf(`{"target_seq":%d,"link_type":%q%s}`, dst, typ, extra))
		if rec.Code != http.StatusCreated {
			t.Fatalf("依存 %d %s %d の status = %d（%s）", src, typ, dst, rec.Code, rec.Body.String())
		}
		var v struct {
			ID         string `json:"id"`
			LagSeconds int32  `json:"lag_seconds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(extra, "lag_seconds") && v.LagSeconds != 600 {
			t.Fatalf("lag_seconds = %d", v.LagSeconds)
		}
		return v.ID
	}
	link(a, x, "FS", `,"lag_seconds":600`) // 行またぎ・転換10分
	link(a, z, "FF", "")                   // 行の外（タイムラインに載らない）へ

	// links[] は行をまたいでも載る。片端が items に無い依存（a→z）は載せない
	tl, _ := f.timeline(t)
	if len(tl.Links) != 1 || tl.Links[0].SourceSeq != a || tl.Links[0].TargetSeq != x || tl.Links[0].LagSeconds != 600 {
		t.Fatalf("links = %+v", tl.Links)
	}

	// 試算 → 確定。a を 10:45 まで延ばすと、x は 10:55 に寄って y を押し、z は終了が 10:45 にそろう
	av := f.ticket(t, a).Version
	rec := f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"dry_run":true}`, jst2030(10, 45)))
	trial := decodeReschedule(t, rec)
	if rec.Code != http.StatusOK || trial.Result != "ok" {
		t.Fatalf("試算 = %d %+v", rec.Code, trial)
	}
	want := map[int32]chainSpan{
		x: {Start: jst2030(10, 55), Due: jst2030(11, 25)},
		y: {Start: jst2030(11, 25), Due: jst2030(11, 55)},
		z: {Start: jst2030(10, 15), Due: jst2030(10, 45)},
	}
	for seq, span := range want {
		got, _, ok := spanOf(trial.Changes, seq)
		if !ok || got != span {
			t.Errorf("%d の試算 = %+v, want %+v（%+v）", seq, got, span, trial.Changes)
		}
	}
	rec = f.reschedule(t, a, av, fmt.Sprintf(`{"due_at":%d,"expect":%s}`, jst2030(10, 45), expectOf(trial, a)))
	if done := decodeReschedule(t, rec); rec.Code != http.StatusOK || !done.Applied {
		t.Fatalf("確定 = %d %s", rec.Code, rec.Body.String())
	}
	for seq, span := range want {
		got := f.ticket(t, seq)
		if got.StartAt == nil || got.DueAt == nil || *got.StartAt != span.Start || *got.DueAt != span.Due {
			t.Errorf("%d = %v–%v, want %+v", seq, got.StartAt, got.DueAt, span)
		}
	}

	// 秒のずらしを PATCH で変える（日はそのまま）
	links := f.do(http.MethodGet, fmt.Sprintf("%s/tickets/%d/links", f.base, x), f.session, "")
	var lv struct {
		Items []linkJSON `json:"items"`
	}
	if err := json.Unmarshal(links.Body.Bytes(), &lv); err != nil || len(lv.Items) != 1 {
		t.Fatalf("x の依存 = %s", links.Body.String())
	}
	rec = f.do(http.MethodPatch, fmt.Sprintf("%s/tickets/%d/links/%s", f.base, x, lv.Items[0].ID), f.session, `{"lag_seconds":-300}`)
	if got := decodeLink(t, rec); rec.Code != http.StatusOK || got.LagSeconds != -300 || got.LagDays != 0 {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body.String())
	}

	// 動かさずに計算し直す（依存を作った直後にそろえる。9.17.2）。x の SS の後続 q が x の開始にそろう
	link(x, q, "SS", "")
	xv := f.ticket(t, x).Version
	rec = f.reschedule(t, x, xv, `{"dry_run":true}`)
	re := decodeReschedule(t, rec)
	if rec.Code != http.StatusOK || re.Result != "ok" {
		t.Fatalf("計算し直し = %d %s", rec.Code, rec.Body.String())
	}
	if _, _, moved := spanOf(re.Changes, x); moved {
		t.Fatalf("きっかけは動かさない: %+v", re.Changes)
	}
	if got, _, _ := spanOf(re.Changes, q); got != (chainSpan{Start: jst2030(10, 55), Due: jst2030(11, 25)}) {
		t.Fatalf("q = %+v（%+v）", got, re.Changes)
	}

	// 並びと依存の輪（order_cycle）。列で x の後ろにある y から x への依存は、依存だけなら輪でないので作れる
	link(y, x, "FS", "")
	rec = f.reschedule(t, x, xv, fmt.Sprintf(`{"due_at":%d,"dry_run":true}`, jst2030(11, 30)))
	if cyc := decodeReschedule(t, rec); rec.Code != http.StatusOK || cyc.Block == nil || cyc.Block.Code != chainOrderCycle {
		t.Fatalf("輪の試算 = %d %s", rec.Code, rec.Body.String())
	}
	rec = f.reschedule(t, x, xv, fmt.Sprintf(`{"due_at":%d}`, jst2030(11, 30)))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"order_cycle"`) {
		t.Fatalf("輪の確定 = %d %s", rec.Code, rec.Body.String())
	}
}
