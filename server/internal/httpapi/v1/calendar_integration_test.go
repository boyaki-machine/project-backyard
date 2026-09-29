package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/holiday"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// fakeHolidayFetcher は Google の代わりに決まった本文を返す。呼ばれた回数を数える。
type fakeHolidayFetcher struct {
	body  atomic.Value // string
	err   atomic.Value // error（nil を入れられないので *holiday.FetchError）
	calls atomic.Int32
}

func (f *fakeHolidayFetcher) Fetch(_ context.Context, _ string) (string, error) {
	f.calls.Add(1)
	if e, _ := f.err.Load().(*holiday.FetchError); e != nil {
		return "", e
	}
	s, _ := f.body.Load().(string)
	return s, nil
}

// calendarICS は日本の祝日カレンダーの形を写した本文（多バイトの名前を含む）。
func calendarICS(dtstamp string) string {
	return "BEGIN:VCALENDAR\r\nX-WR-CALNAME:日本の祝日\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260921\r\nDTEND;VALUE=DATE:20260922\r\n" +
		"DTSTAMP:" + dtstamp + "\r\nDESCRIPTION:祝日\r\nSUMMARY:敬老の日\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260923\r\nDTEND;VALUE=DATE:20260924\r\n" +
		"DTSTAMP:" + dtstamp + "\r\nDESCRIPTION:祝日\r\nSUMMARY:秋分の日\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260915\r\nDTEND;VALUE=DATE:20260916\r\n" +
		"DTSTAMP:" + dtstamp + "\r\nDESCRIPTION:祭日\\n祭日を非表示にするには…\r\nSUMMARY:十五夜\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}

// 暦API（ApiDesign.md 5.8）と PATCH /projects/:key の timezone を**実際のDBに対して**通す。
//
// 単体テストでは確かめられないもの：
//
//   - 取得元が google_id 単位で共有され、2つ目のプロジェクトは取りに行かずに使えること
//   - 待ち時間（1時間）がプロジェクトをまたいで効き、429 と Retry-After になること
//   - 中身が同じなら取り込み直さないこと（DTSTAMP だけ違う本文）
//   - 失敗しても前回までの取り込み分が残ること
//   - 休日の決まり方（上書き → 祝日 → 土日 → 平日）
//   - 取り込みの暦が、Google へ切り替えたときに消えること
//   - DB が知らないタイムゾーン名を 422 で弾くこと
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestCalendarIntegration(t *testing.T) {
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
	fetcher := &fakeHolidayFetcher{}
	fetcher.body.Store(calendarICS("20260927T092754Z"))
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool), Holidays: fetcher})

	adminID := ulidgen.New()
	adminEmail := "cal-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	// **google_id は試験ごとに変える。** 取得元は PB 全体で共有されるので、
	// 実データの ja.japanese を使うと dev の DB の取り込み分を書き換えてしまう。
	suffix := strings.ToLower(adminID[len(adminID)-8:])
	googleID := "zz.test_" + strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0')
		}
		return r
	}, suffix)
	keyA, keyB := "ca-"+suffix, "cb-"+suffix
	t.Cleanup(func() {
		bg := context.Background()
		for _, stmt := range []string{
			`DELETE FROM project WHERE key IN ($1, $2)`,
		} {
			if _, err := pool.Exec(bg, stmt, keyA, keyB); err != nil {
				t.Errorf("後始末に失敗した: %v", err)
			}
		}
		if _, err := pool.Exec(bg, `DELETE FROM holiday_source WHERE google_id = $1`, googleID); err != nil {
			t.Errorf("取得元の後始末に失敗した: %v", err)
		}
	})
	for _, key := range []string{keyA, keyB} {
		rec := postWithCookie(r, "/api/v1/projects", session,
			fmt.Sprintf(`{"key":%q,"name":"暦の結合テスト","workflow_template":"simple"}`, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
	}
	baseA, baseB := "/api/v1/projects/"+keyA, "/api/v1/projects/"+keyB

	decode := func(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
		t.Helper()
		if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
			t.Fatalf("応答を読めない: %v（body=%s）", err, rec.Body.String())
		}
	}
	type sourceResp struct {
		Source *struct {
			Kind            string  `json:"kind"`
			GoogleID        *string `json:"google_id"`
			Name            *string `json:"name"`
			HolidayCount    int     `json:"holiday_count"`
			ObservanceCount int     `json:"observance_count"`
			FetchedAt       *int64  `json:"fetched_at"`
			LastError       *string `json:"last_error"`
			NextFetchAt     *int64  `json:"next_fetch_at"`
		} `json:"source"`
		Imported *struct {
			Days    int `json:"days"`
			Skipped int `json:"skipped"`
		} `json:"imported"`
	}

	// ── ① 取得元が無い ───────────────────────────────────
	rec := getWithCookie(r, baseA+"/calendar", session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":null`) {
		t.Fatalf("取得元なしの GET = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postWithCookie(r, baseA+"/calendar/fetch", session, ``); rec.Code != http.StatusConflict {
		t.Errorf("取得元なしの fetch = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}

	// ── ② 選ぶだけでは取りに行かない ─────────────────────
	rec = putWithCookie(r, baseA+"/calendar/source", session, fmt.Sprintf(`{"google_id":%q}`, googleID))
	if rec.Code != http.StatusOK {
		t.Fatalf("source の PUT = %d %s", rec.Code, rec.Body.String())
	}
	var sr sourceResp
	decode(t, rec, &sr)
	if sr.Source == nil || sr.Source.Kind != "google" || sr.Source.FetchedAt != nil || fetcher.calls.Load() != 0 {
		t.Fatalf("選んだ直後の source = %+v, calls = %d", sr.Source, fetcher.calls.Load())
	}
	if rec := putWithCookie(r, baseA+"/calendar/source", session, `{"google_id":"https://evil"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("URL を渡した PUT = %d, want 422", rec.Code)
	}

	// ── ③ 取得して取り込む ───────────────────────────────
	rec = postWithCookie(r, baseA+"/calendar/fetch", session, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch = %d %s", rec.Code, rec.Body.String())
	}
	decode(t, rec, &sr)
	if sr.Source.HolidayCount != 2 || sr.Source.ObservanceCount != 1 || sr.Source.Name == nil ||
		*sr.Source.Name != "日本の祝日" || sr.Source.NextFetchAt == nil {
		t.Errorf("取得後の source = %+v", sr.Source)
	}

	// ── ④ 待ち時間はプロジェクトをまたいで効く／結果は共有される ──
	rec = putWithCookie(r, baseB+"/calendar/source", session, fmt.Sprintf(`{"google_id":%q}`, googleID))
	decode(t, rec, &sr)
	if sr.Source.HolidayCount != 2 {
		t.Errorf("B が選んだ時点で共有されていない: %+v", sr.Source)
	}
	rec = postWithCookie(r, baseB+"/calendar/fetch", session, ``)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("1時間以内の fetch = %d Retry-After=%q, want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
	if n := fetcher.calls.Load(); n != 1 {
		t.Errorf("相手先へ飛んだ回数 = %d, want 1", n)
	}

	// ── ⑤ 中身が同じなら取り込み直さない ─────────────────
	// 待ち時間を過ぎたことにする。DTSTAMP だけ違う本文を返させ、
	// 取り込み分（holiday_source_day）の行が作り直されないことを ctid で見る。
	expire := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE holiday_source SET last_attempt_at = now() - interval '2 hours' WHERE google_id = $1`, googleID); err != nil {
			t.Fatalf("待ち時間を過ぎたことにできない: %v", err)
		}
	}
	ctids := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT string_agg(d.ctid::text, ',' ORDER BY d.day) FROM holiday_source_day d
			JOIN holiday_source s ON s.id = d.source_id WHERE s.google_id = $1`, googleID).Scan(&s); err != nil {
			t.Fatalf("ctid を読めない: %v", err)
		}
		return s
	}
	before := ctids()
	expire()
	fetcher.body.Store(calendarICS("20261001T000000Z"))
	if rec := postWithCookie(r, baseB+"/calendar/fetch", session, ``); rec.Code != http.StatusOK {
		t.Fatalf("2回目の fetch = %d %s", rec.Code, rec.Body.String())
	}
	if after := ctids(); after != before {
		t.Errorf("中身が同じなのに取り込み直した（ctid %s → %s）", before, after)
	}

	// ── ⑥ 失敗しても前回までの取り込み分は残る ───────────
	expire()
	fetcher.err.Store(&holiday.FetchError{Message: "Google カレンダーが 503 を返しました"})
	rec = postWithCookie(r, baseA+"/calendar/fetch", session, ``)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "upstream_failed") {
		t.Errorf("失敗した fetch = %d %s, want 502 upstream_failed", rec.Code, rec.Body.String())
	}
	rec = getWithCookie(r, baseA+"/calendar", session)
	decode(t, rec, &sr)
	if sr.Source.HolidayCount != 2 || sr.Source.LastError == nil || *sr.Source.LastError != "Google カレンダーが 503 を返しました" {
		t.Errorf("失敗後の source = %+v", sr.Source)
	}
	fetcher.err.Store((*holiday.FetchError)(nil))

	// ── ⑦ 休日の決まり方 ─────────────────────────────────
	// 2026-09-19(土) 20(日) 21(月・敬老の日) 22(火) 23(水・秋分の日) 15(火・十五夜=行事)
	// 22 を休日に、19 を平日に上書きする。23 の祝日を平日に上書きする。
	for _, c := range []struct{ day, body string }{
		{"2026-09-22", `{"is_holiday":true,"name":"創立記念日"}`},
		{"2026-09-19", `{"is_holiday":false,"name":"出勤日"}`},
		{"2026-09-23", `{"is_holiday":false}`},
	} {
		if rec := putWithCookie(r, baseA+"/calendar/days/"+c.day, session, c.body); rec.Code != http.StatusOK {
			t.Fatalf("上書き %s = %d %s", c.day, rec.Code, rec.Body.String())
		}
	}
	rec = getWithCookie(r, baseA+"/calendar/days?from=2026-09-14&to=2026-09-25", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("days = %d %s", rec.Code, rec.Body.String())
	}
	var dr struct {
		Days []struct {
			Day       string `json:"day"`
			IsHoliday bool   `json:"is_holiday"`
			Reason    string `json:"reason"`
		} `json:"days"`
	}
	decode(t, rec, &dr)
	var got []string
	for _, d := range dr.Days {
		got = append(got, fmt.Sprintf("%s:%v:%s", d.Day[8:], d.IsHoliday, d.Reason))
	}
	want := "15:false:none 19:false:override 20:true:weekend 21:true:holiday 22:true:override 23:false:override"
	if strings.Join(got, " ") != want {
		t.Errorf("days =\n  %s\nwant\n  %s", strings.Join(got, " "), want)
	}
	// 上書きを外すと取得元の判定へ戻る。無い上書きを外しても 204
	for _, day := range []string{"2026-09-23", "2026-09-23"} {
		if rec := deleteWithCookie(r, baseA+"/calendar/days/"+day, session); rec.Code != http.StatusNoContent {
			t.Errorf("上書きを外す = %d", rec.Code)
		}
	}
	if rec := getWithCookie(r, baseA+"/calendar/days?from=2026-09-23&to=2026-09-24", session); !strings.Contains(rec.Body.String(), `"reason":"holiday"`) {
		t.Errorf("外した後の 09-23 = %s", rec.Body.String())
	}
	if rec := getWithCookie(r, baseA+"/calendar/days?from=2026-01-01&to=2029-01-10", session); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("期間超過 = %d, want 422", rec.Code)
	}

	// ── ⑧ 取り込み → Google へ切り替えると取り込みの暦は消える ──
	body, _ := json.Marshal(map[string]string{
		"filename": "会社の休業日.ics",
		"ics": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20261229\r\n" +
			"DTEND;VALUE=DATE:20270104\r\nSUMMARY:年末年始休業\r\nEND:VEVENT\r\n" +
			"BEGIN:VEVENT\r\nDTSTART:20261001T090000Z\r\nSUMMARY:会議\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	})
	rec = postWithCookie(r, baseB+"/calendar/import", session, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	decode(t, rec, &sr)
	if sr.Source.Kind != "file" || sr.Source.HolidayCount != 6 || sr.Imported == nil ||
		sr.Imported.Days != 6 || sr.Imported.Skipped != 1 || *sr.Source.Name != "会社の休業日.ics" {
		t.Errorf("取り込み後 = %+v imported=%+v", sr.Source, sr.Imported)
	}
	if rec := postWithCookie(r, baseB+"/calendar/import", session, `{"ics":"hello"}`); !strings.Contains(rec.Body.String(), "invalid_ics") {
		t.Errorf("iCal でない本文 = %d %s", rec.Code, rec.Body.String())
	}
	countOwned := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM holiday_source s JOIN project p ON p.id = s.owner_project_id WHERE p.key = $1`, keyB).Scan(&n); err != nil {
			t.Fatalf("所有の暦を数えられない: %v", err)
		}
		return n
	}
	if n := countOwned(); n != 1 {
		t.Errorf("取り込み後の所有の暦 = %d, want 1", n)
	}
	putWithCookie(r, baseB+"/calendar/source", session, fmt.Sprintf(`{"google_id":%q}`, googleID))
	if n := countOwned(); n != 0 {
		t.Errorf("Google へ切り替えた後の所有の暦 = %d, want 0", n)
	}

	// ── ⑨ 基準タイムゾーン ───────────────────────────────
	rec = getWithCookie(r, baseA, session)
	var pr struct {
		Timezone string `json:"timezone"`
		Version  int    `json:"version"`
	}
	decode(t, rec, &pr)
	if pr.Timezone != "Asia/Tokyo" {
		t.Errorf("既定の timezone = %q", pr.Timezone)
	}
	for _, c := range []struct {
		tz   string
		code int
	}{
		{"America/New_York", http.StatusOK},
		{"Mars/Olympus", http.StatusUnprocessableEntity},
		{"Local", http.StatusUnprocessableEntity},
	} {
		rec := patchProjectWithCookie(r, baseA, session, fmt.Sprint(pr.Version), fmt.Sprintf(`{"timezone":%q}`, c.tz))
		if rec.Code != c.code {
			t.Errorf("timezone=%s の PATCH = %d, want %d（body=%s）", c.tz, rec.Code, c.code, rec.Body.String())
		}
		if rec.Code == http.StatusOK {
			decode(t, rec, &pr)
		}
	}
	if pr.Timezone != "America/New_York" {
		t.Errorf("更新後の timezone = %q", pr.Timezone)
	}
}
