package v1

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWorkHoldIntegration は承認待ちの保留を確かめる（DbDesign.md 6.34）。
func TestWorkHoldIntegration(t *testing.T) {
	f := newAgentControlFixture(t)
	ctx := context.Background()
	a := f.register("Hold worker")
	var token issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(f.r, "/api/v1/me/agents/"+a.ID+"/tokens", f.ownerSession, `{"expires_in_days":30}`), &token)
	rec := postWithCookie(f.r, f.base+"/tickets", f.ownerSession, fmt.Sprintf(`{"type":"task","title":"Hold fixture","assignee_id":%q}`, f.ownerID))
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var ticket struct{ Seq int32 }
	decodeJSONBody(t, rec, &ticket)
	path := fmt.Sprintf("%s/tickets/%d/work-sessions", f.base, ticket.Seq)
	rec = bearerRequest(f.r, "POST", path, token.Token, `{"request_id":"hold-start"}`, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var work testWorkView
	decodeJSONBody(t, rec, &work)

	hook := func(tok, event string) {
		t.Helper()
		// 本文はフックの JSON そのまま。PostToolUse はツールの出力を丸ごと含む。
		body := fmt.Sprintf(`{"session_id":"s","tool_response":{"stdout":%q},"hook_event_name":%q}`, strings.Repeat("y", 200<<10), event)
		r := bearerRequest(f.r, "POST", f.base+"/work-hold", tok, body, "")
		if r.Code != 204 || r.Body.Len() != 0 {
			t.Fatalf("%s: %d %q", event, r.Code, r.Body.String())
		}
	}
	state := func() (string, *string, int64, int64) {
		t.Helper()
		var s string
		var hold *string
		var waiting, held int64
		if e := f.pool.QueryRow(ctx, `SELECT state, hold, waiting_ms, hold_ms FROM work_session WHERE id=$1`, work.ID).Scan(&s, &hold, &waiting, &held); e != nil {
			t.Fatal(e)
		}
		return s, hold, waiting, held
	}
	backdate := func(d time.Duration) {
		t.Helper()
		if _, e := f.pool.Exec(ctx, `UPDATE work_session SET changed_at=changed_at-$2::interval, started_at=started_at-$2::interval WHERE id=$1`, work.ID, fmt.Sprintf("%d milliseconds", d.Milliseconds())); e != nil {
			t.Fatal(e)
		}
	}

	// 承認を求めたら止まり、次のツール実行の後に戻る。止めていた間は承認待ちに数える。
	hook(token.Token, "PermissionRequest")
	if s, hold, _, _ := state(); s != "waiting" || hold == nil || *hold != "approval" {
		t.Fatalf("after PermissionRequest: %s %v", s, hold)
	}
	backdate(10 * time.Minute)
	hook(token.Token, "PostToolUse")
	s, hold, waiting, held := state()
	if s != "running" || hold != nil || held < (10*time.Minute).Milliseconds() || waiting < held {
		t.Fatalf("after PostToolUse: %s %v waiting=%d held=%d", s, hold, waiting, held)
	}

	// 知らないイベントと、人のトークンは何もしない。
	hook(token.Token, "SessionStart")
	if r := postWithCookie(f.r, f.base+"/work-hold", f.ownerSession, `{"hook_event_name":"PermissionRequest"}`); r.Code != 204 {
		t.Fatalf("human: %d", r.Code)
	}
	if s, _, _, _ := state(); s != "running" {
		t.Fatalf("ignored events changed state: %s", s)
	}

	// 承認を求めていたのが待機の操作そのものなら、保留を解いてから待機する。
	hook(token.Token, "PermissionRequest")
	rec = bearerRequest(f.r, "POST", path+"/"+work.ID+"/events", token.Token, `{"request_id":"hold-pause","action":"pause","note":"人の返答を待つ"}`, "")
	if rec.Code != 200 {
		t.Fatalf("pause on held: %d %s", rec.Code, rec.Body.String())
	}
	// 明示的に止めた記録は、フックでは再開しない。
	hook(token.Token, "PostToolUse")
	if s, hold, _, _ := state(); s != "waiting" || hold != nil {
		t.Fatalf("explicit pause was resumed by hook: %s %v", s, hold)
	}
	// 明示的に止めた記録はフックの保留の対象にもならない（実行中だけを止める）。
	hook(token.Token, "PermissionRequest")
	if _, hold, _, _ := state(); hold != nil {
		t.Fatal("explicitly paused record was held")
	}

	// 保留中の再開と終了も通る。
	rec = bearerRequest(f.r, "POST", path+"/"+work.ID+"/events", token.Token, `{"request_id":"hold-resume","action":"resume"}`, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	hook(token.Token, "PermissionRequest")
	rec = bearerRequest(f.r, "POST", path+"/"+work.ID+"/events", token.Token, `{"request_id":"hold-resume-2","action":"resume"}`, "")
	if rec.Code != 200 {
		t.Fatalf("resume on held: %d %s", rec.Code, rec.Body.String())
	}
	hook(token.Token, "PermissionRequest")
	rec = bearerRequest(f.r, "POST", path+"/"+work.ID+"/events", token.Token, `{"request_id":"hold-finish","action":"finish","result":"completed"}`, "")
	if rec.Code != 200 {
		t.Fatalf("finish on held: %d %s", rec.Code, rec.Body.String())
	}
	var done struct {
		State  string  `json:"state"`
		Hold   *string `json:"hold"`
		HoldMS int64   `json:"hold_ms"`
	}
	decodeJSONBody(t, rec, &done)
	if done.State != "finished" || done.Hold != nil || done.HoldMS < held {
		t.Fatalf("finished view: %+v", done)
	}
}
