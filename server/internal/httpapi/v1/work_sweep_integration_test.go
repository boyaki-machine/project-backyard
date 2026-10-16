package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// TestWorkSweepIntegration は作業記録の浮きの片付けを確かめる（ApiDesign.md 11.14）。
//
// **時計は進めない。** 共有の DB には並行して走る他のテストの開いた記録があり、時計を
// 進めるとそれらまで浮きに見える。代わりに、自分の記録の時刻を過去へずらす。
func TestWorkSweepIntegration(t *testing.T) {
	f := newAgentControlFixture(t)
	ctx := context.Background()
	q := gen.New(f.pool)
	// 時計は差し替える口（WorkSweeper.Now）を通すが、値は実時間にする。固定すると、
	// 固定した後に始めた記録の終了が開始より前になり、DB の CHECK で落ちる。
	sweeper := WorkSweeper{Tx: store.NewTxRunner(f.pool), Q: q, Settings: config.LiveDefaults(), Now: time.Now}

	ticket := func(title string) (string, int32) {
		t.Helper()
		rec := postWithCookie(f.r, f.base+"/tickets", f.ownerSession, fmt.Sprintf(`{"type":"task","title":%q,"assignee_id":%q}`, title, f.ownerID))
		if rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
		var x struct {
			ID  string
			Seq int32
		}
		decodeJSONBody(t, rec, &x)
		return x.ID, x.Seq
	}
	agentToken := func(name string) (string, string) {
		t.Helper()
		a := f.register(name)
		var token issuedAgentTokenJSON
		decodeJSONBody(t, postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/tokens", f.ownerSession, `{"expires_in_days":30}`), &token)
		return a.ID, token.Token
	}
	start := func(seq int32, token string) string {
		t.Helper()
		path := fmt.Sprintf("%s/tickets/%d/work-sessions", f.base, seq)
		body := fmt.Sprintf(`{"request_id":"sweep-%d","note":"sweep fixture"}`, seq)
		var code int
		var raw string
		if token == "" {
			r := postWithCookie(f.r, path, f.ownerSession, body)
			code, raw = r.Code, r.Body.String()
		} else {
			r := bearerRequest(f.r, "POST", path, token, body, "")
			code, raw = r.Code, r.Body.String()
		}
		if code != 200 {
			t.Fatalf("start %d: %d %s", seq, code, raw)
		}
		var v struct{ ID string }
		if e := json.Unmarshal([]byte(raw), &v); e != nil {
			t.Fatal(e)
		}
		return v.ID
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := f.pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	session := func(id string) gen.WorkSession {
		t.Helper()
		var s gen.WorkSession
		if e := f.pool.QueryRow(ctx, `SELECT state,active_ms,time_missing,incomplete,coalesce(outcome,''),note FROM work_session WHERE id=$1`, id).
			Scan(&s.State, &s.ActiveMs, &s.TimeMissing, &s.Incomplete, &s.Outcome.String, &s.Note); e != nil {
			t.Fatal(e)
		}
		return s
	}

	_, staleSeq := ticket("stale agent")
	_, liveSeq := ticket("live agent")
	closedID, closedSeq := ticket("closed ticket")
	staleAgent, staleToken := agentToken("Sweep stale")
	liveAgent, liveToken := agentToken("Sweep live")
	staleWork := start(staleSeq, staleToken)
	liveWork := start(liveSeq, liveToken)
	humanWork := start(closedSeq, "")
	now := time.Now()

	// 30時間前に始まり、それきりトークンも使っていないエージェント → 浮き。
	exec(`UPDATE work_session SET started_at=$2, changed_at=$2 WHERE id=$1`, staleWork, now.Add(-30*time.Hour))
	exec(`UPDATE access_token SET last_used_at=$2 WHERE actor_id=$1`, staleAgent, now.Add(-30*time.Hour))
	// 記録は30時間前のままだが、トークンは1時間前に使った（サブエージェントの記録を開いた main）→ 浮きではない。
	exec(`UPDATE work_session SET started_at=$2, changed_at=$2 WHERE id=$1`, liveWork, now.Add(-30*time.Hour))
	exec(`UPDATE access_token SET last_used_at=$2 WHERE actor_id=$1`, liveAgent, now.Add(-time.Hour))
	// 人の記録は新しいが、チケットが完了している → 時間に関わらず浮き。
	exec(`UPDATE ticket SET closed_at=$2 WHERE id=$1`, closedID, now)

	view, err := sweeper.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, it := range view.Items {
		reasons[it.WorkID] = it.Reason
	}
	if reasons[staleWork] != "stale" || reasons[humanWork] != "ticket_closed" || reasons[liveWork] != "" {
		t.Fatalf("view reasons: %v", reasons)
	}

	// 閾値を48時間へ上げると、30時間の記録は浮きでなくなる。
	wide := config.LiveDefaults()
	wide.ApplyRows([]config.Row{{Key: config.KeyWorkStaleHours, Value: "48"}})
	if v, _ := (WorkSweeper{Tx: sweeper.Tx, Q: q, Settings: wide, Now: sweeper.Now}).View(ctx); containsWork(v.Items, staleWork) {
		t.Fatal("48h threshold still lists the 30h record")
	}
	// 0 なら日次の処理は何もしない。
	off := config.LiveDefaults()
	off.ApplyRows([]config.Row{{Key: config.KeyWorkStaleHours, Value: "0"}})
	if n, err := (WorkSweeper{Tx: sweeper.Tx, Q: q, Settings: off, Now: sweeper.Now}).RunDaily(ctx); err != nil || n != 0 {
		t.Fatalf("disabled daily: %d %v", n, err)
	}
	if s := session(staleWork); s.State != "running" {
		t.Fatalf("disabled daily closed: %+v", s)
	}

	if _, err := sweeper.RunDaily(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{staleWork, humanWork} {
		s := session(id)
		if s.State != "finished" || !s.TimeMissing || !s.Incomplete || s.Outcome.String != "abandoned" || !strings.Contains(s.Note, "浮きとして閉じた") {
			t.Fatalf("swept %s: %+v", id, s)
		}
		// 最後の区間を数えない（30時間を作業時間にしない）。
		if s.ActiveMs != 0 {
			t.Fatalf("swept %s counted the open interval: %d", id, s.ActiveMs)
		}
	}
	if s := session(liveWork); s.State != "running" {
		t.Fatalf("live delegation record was closed: %+v", s)
	}
	if n := scalarInt(t, f.pool, `SELECT count(*) FROM agent_run WHERE work_session_id=$1 AND status='abandoned'`, staleWork); n != 1 {
		t.Fatalf("abandoned run for the agent record: %d", n)
	}
	if n := scalarInt(t, f.pool, `SELECT count(*) FROM activity WHERE new_value=$1 AND actor_kind='system'`, "sweep:"+staleWork); n != 1 {
		t.Fatalf("system activity: %d", n)
	}
	var kind, detail string
	if e := f.pool.QueryRow(ctx, `SELECT actor_kind, detail::text FROM audit_log WHERE action='work.sweep' AND detail->'closed' @> $1::jsonb ORDER BY occurred_at DESC LIMIT 1`,
		fmt.Sprintf(`[{"work_id":%q}]`, staleWork)).Scan(&kind, &detail); e != nil {
		t.Fatal(e)
	}
	if kind != "system" || !strings.Contains(detail, `"trigger": "daily"`) {
		t.Fatalf("daily audit: %s %s", kind, detail)
	}
	t.Cleanup(func() {
		f.pool.Exec(ctx, `DELETE FROM audit_log WHERE action='work.sweep' AND detail->'closed' @> $1::jsonb`, fmt.Sprintf(`[{"work_id":%q}]`, staleWork))
	})

	// 画面の口。エージェントのトークンでは入れない。
	if rec := bearerRequest(f.r, "GET", "/api/v1/admin/work-sweep", liveToken, "", ""); rec.Code != 403 {
		t.Fatalf("agent GET: %d", rec.Code)
	}
	if rec := postWithCookie(f.r, "/api/v1/admin/work-sweep", f.ownerSession, `{"work_ids":[]}`); rec.Code != 422 {
		t.Fatalf("empty ids: %d %s", rec.Code, rec.Body.String())
	}
	// 見せたあとで活動が止まった記録は閉じ、既に閉じた記録は数えて飛ばす。
	exec(`UPDATE access_token SET last_used_at=$2 WHERE actor_id=$1`, liveAgent, time.Now().Add(-30*time.Hour))
	rec := getWithCookie(f.r, "/api/v1/admin/work-sweep", f.ownerSession)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), liveWork) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	rec = postWithCookie(f.r, "/api/v1/admin/work-sweep", f.ownerSession, fmt.Sprintf(`{"work_ids":[%q,%q]}`, liveWork, staleWork))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var res workSweepResult
	decodeJSONBody(t, rec, &res)
	t.Cleanup(func() {
		f.pool.Exec(ctx, `DELETE FROM audit_log WHERE action='work.sweep' AND detail->'closed' @> $1::jsonb`, fmt.Sprintf(`[{"work_id":%q}]`, liveWork))
	})
	if len(res.ClosedIDs) != 1 || res.ClosedIDs[0] != liveWork || res.SkippedCount != 1 || containsWork(res.Items, liveWork) {
		t.Fatalf("POST: %+v", res)
	}
	if res.LastRun == nil || res.LastRun.Trigger != "manual" || res.LastRun.ActorLabel == nil || res.LastRun.ClosedCount != 1 {
		t.Fatalf("last run: %+v", res.LastRun)
	}
}

func containsWork(items []workSweepItem, id string) bool {
	for _, it := range items {
		if it.WorkID == id {
			return true
		}
	}
	return false
}
