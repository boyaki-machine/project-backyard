package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// shortWait は待ち受けの間隔と長さを試験用に縮める。
func shortWait(t *testing.T) {
	t.Helper()
	poll, prog, with, without := waitPollInterval, waitProgressInterval, waitWithProgress, waitWithoutProgress
	waitPollInterval = 10 * time.Millisecond
	waitProgressInterval = 30 * time.Millisecond
	waitWithProgress = 200 * time.Millisecond
	waitWithoutProgress = 120 * time.Millisecond
	t.Cleanup(func() {
		waitPollInterval, waitProgressInterval, waitWithProgress, waitWithoutProgress = poll, prog, with, without
	})
}

const (
	ticketOK = `{"seq":7,"title":"評価を待つ"}`
	act1     = `{"items":[{"id":"01K0000000000000000000000A","actor":{"kind":"user","display_name":"PM"},"action":"create","field":null,"new_value":null,"occurred_at":1000}]}`
	// act2 は act1 の後に、評価エージェントがコメントを書いた状態。
	act2 = `{"items":[` +
		`{"id":"01K0000000000000000000000B","actor":{"kind":"agent","display_name":"評価役"},"action":"update","field":"comment","new_value":"discussion: 評価 計画 v1 第1回：不合格","occurred_at":5000},` +
		`{"id":"01K0000000000000000000000A","actor":{"kind":"user","display_name":"PM"},"action":"create","field":null,"new_value":null,"occurred_at":1000}]}`
	comments2 = `{"items":[` +
		`{"id":"01K0000000000000000000000C","kind":"discussion","body_md":"## 評価 計画 v1 第1回：不合格\n指摘1","origin":"agent","author":{"kind":"agent","display_name":"評価役"},"created_at":5000},` +
		`{"id":"01K00000000000000000000009","kind":"progress","body_md":"古いコメント","origin":"human","author":{"kind":"user","display_name":"PM"},"created_at":900}]}`
	// actTransition は act1 の後に状態が進んだ状態（コメントは含まない）。
	actTransition = `{"items":[` +
		`{"id":"01K0000000000000000000000D","actor":{"kind":"agent","display_name":"main"},"action":"transition","field":"status_key","new_value":"in_progress","occurred_at":6000},` +
		`{"id":"01K0000000000000000000000A","actor":{"kind":"user","display_name":"PM"},"action":"create","field":null,"new_value":null,"occurred_at":1000}]}`
)

type waitOut struct {
	Changed bool   `json:"changed"`
	Cursor  string `json:"cursor"`
	Next    string `json:"next"`
	Changes []struct {
		ID    string `json:"id"`
		Field string `json:"field"`
		Actor string `json:"actor"`
	} `json:"changes"`
	Comments []struct {
		ID     string `json:"id"`
		BodyMD string `json:"body_md"`
		Author string `json:"author"`
	} `json:"comments"`
}

func decodeWait(t *testing.T, out toolResult) waitOut {
	t.Helper()
	if out.IsError {
		t.Fatalf("isError が立った: %s", out.Content[0].Text)
	}
	var w waitOut
	if err := json.Unmarshal([]byte(out.Content[0].Text), &w); err != nil {
		t.Fatalf("pb_wait の結果が JSON ではない: %v（%s）", err, out.Content[0].Text)
	}
	return w
}

func TestWaitTimesOutWithoutError(t *testing.T) {
	// **時間切れはエラーにしない**——打ち切りのエラーが続くと、モデルは待ち受けをやめる
	// （Delegation.md 10.2 の Claude Desktop）。
	shortWait(t)
	rest := &fakeREST{steps: []fakeStep{{body: ticketOK}, {body: act1}}}
	h := New(rest, "v0")

	w := decodeWait(t, callTool1(t, h, toolCallBody("pb_wait", `{"seq":7}`)))

	if w.Changed {
		t.Error("変化が無いのに changed = true")
	}
	if w.Cursor != "01K0000000000000000000000A" {
		t.Errorf("cursor = %q, want 最新の行の id（いまから後を待つ）", w.Cursor)
	}
	if !strings.Contains(w.Next, "もう一度 pb_wait") {
		t.Errorf("next が次の一手を言っていない: %q", w.Next)
	}
	if rest.calls < 4 {
		t.Errorf("REST の呼び出し = %d 回、待つあいだに問い合わせを繰り返していない", rest.calls)
	}
}

func TestWaitReturnsNewCommentWithBody(t *testing.T) {
	// 前回の cursor より新しいコメントがあれば、待たずに本文つきで返る。
	shortWait(t)
	rest := &fakeREST{steps: []fakeStep{{body: ticketOK}, {body: act2}, {body: comments2}}}
	h := New(rest, "v0")

	w := decodeWait(t, callTool1(t, h, toolCallBody("pb_wait", `{"seq":7,"since":"01K0000000000000000000000A"}`)))

	if !w.Changed || w.Cursor != "01K0000000000000000000000B" {
		t.Fatalf("changed / cursor = %v / %q, want true / 01K…B", w.Changed, w.Cursor)
	}
	if len(w.Changes) != 1 || w.Changes[0].Field != "comment" || w.Changes[0].Actor != "評価役" {
		t.Errorf("changes = %+v, want 評価役のコメント1件", w.Changes)
	}
	if len(w.Comments) != 1 || !strings.Contains(w.Comments[0].BodyMD, "指摘1") {
		t.Errorf("comments = %+v, want 新しいコメントの全文1件（古いコメントは含めない）", w.Comments)
	}
	if !strings.HasSuffix(rest.gotPaths[2], "/tickets/7/comments") || rest.gotQuery.Get("order") != "desc" {
		t.Errorf("コメントの引き方 = %s?%v, want /comments?order=desc", rest.gotPaths[2], rest.gotQuery)
	}
}

func TestWaitWakesOnTransitionDuringWait(t *testing.T) {
	// 待っている途中に状態が進めば返る。**コメントが無ければ一覧を引かない。**
	shortWait(t)
	rest := &fakeREST{steps: []fakeStep{{body: ticketOK}, {body: act1}, {body: act1}, {body: actTransition}}}
	h := New(rest, "v0")

	w := decodeWait(t, callTool1(t, h, toolCallBody("pb_wait", `{"seq":7}`)))

	if !w.Changed || w.Cursor != "01K0000000000000000000000D" {
		t.Fatalf("changed / cursor = %v / %q, want true / 01K…D", w.Changed, w.Cursor)
	}
	if len(w.Comments) != 0 {
		t.Errorf("コメントが無いのに comments = %+v", w.Comments)
	}
	for _, p := range rest.gotPaths {
		if strings.HasSuffix(p, "/comments") {
			t.Errorf("コメントの一覧を引いた: %v", rest.gotPaths)
		}
	}
}

func TestWaitOnMissingTicketFailsFast(t *testing.T) {
	// activity は無い seq にも空の一覧を返すので、先にチケットを確かめないと時間いっぱい待つ。
	shortWait(t)
	rest := &fakeREST{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"チケットが見つかりません"}}`}
	h := New(rest, "v0")

	start := time.Now()
	out := callTool1(t, h, toolCallBody("pb_wait", `{"seq":999}`))
	if !out.IsError {
		t.Errorf("無いチケットで isError が立たない: %s", out.Content[0].Text)
	}
	if time.Since(start) > waitWithoutProgress {
		t.Errorf("待ってから失敗した（%s）。先にチケットを確かめていない", time.Since(start))
	}
}

func TestWaitSendsProgressWhenTokenGiven(t *testing.T) {
	// progressToken があれば進捗の通知を流す。打ち切りを延ばすため（Claude Code・Codex）。
	shortWait(t)
	rest := &fakeREST{steps: []fakeStep{{body: ticketOK}, {body: act1}}}
	h := New(rest, "v0")

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"progressToken":"p1"},` +
		`"name":"pb_wait","arguments":{"seq":7}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp/demo", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer pb_agt_dummy")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, agentPrincipal())
	w := httptest.NewRecorder()
	h.HTTP().ServeHTTP(w, r.WithContext(ctx))

	raw := w.Body.String()
	if !strings.Contains(raw, "notifications/progress") || !strings.Contains(raw, `"p1"`) {
		t.Errorf("進捗の通知が流れていない:\n%s", raw)
	}
	if !strings.Contains(raw, `\"changed\":false`) {
		t.Errorf("最後に時間切れの結果が返っていない:\n%s", raw)
	}
}

// 評価役が根で待つとき、main が止まっていれば待たずに返す（Delegation.md 10.4。pb-310）。
func TestWaitReturnsMainStoppedToEvaluator(t *testing.T) {
	shortWait(t)
	lastSeen := time.Now().Add(-45 * time.Minute).UnixMilli()
	rest := &fakeREST{steps: []fakeStep{
		{body: `{"seq":7,"title":"文化祭モード","execution_mode":"delegated"}`},
		{body: act1},
		{body: `{"in_delegation":true,"role":"evaluator","root_seq":7,"status":"approved","main_stopped":true,"main_last_seen_at":` +
			strconv.FormatInt(lastSeen, 10) + `}`},
	}}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_wait", `{"seq":7}`))
	var w struct {
		waitOut
		MainStopped bool `json:"main_stopped"`
	}
	if err := json.Unmarshal([]byte(out.Content[0].Text), &w); err != nil {
		t.Fatalf("pb_wait の結果が JSON ではない: %v", err)
	}
	if !w.MainStopped || w.Changed {
		t.Fatalf("main_stopped / changed = %v / %v, want true / false（paths=%v）", w.MainStopped, w.Changed, rest.gotPaths)
	}
	if !strings.Contains(w.Next, "main が止まっている（最後の要求から 45 分）") || !strings.Contains(w.Next, "止まる") {
		t.Errorf("next = %q, want 止まる指示", w.Next)
	}
	if w.Cursor != "01K0000000000000000000000A" {
		t.Errorf("cursor = %q, want 再開に使う最新の行", w.Cursor)
	}
	if rest.calls != 3 {
		t.Errorf("REST の呼び出し = %d 回, want 3（待たずに返る）", rest.calls)
	}
}

// main 自身が待つときは、main が止まっていても（自分のこと）ふだんどおり待つ。
func TestWaitIgnoresMainStoppedForMain(t *testing.T) {
	shortWait(t)
	rest := &fakeREST{steps: []fakeStep{
		{body: `{"seq":7,"title":"文化祭モード","execution_mode":"delegated"}`},
		{body: act1},
		{body: `{"in_delegation":true,"role":"main","root_seq":7,"status":"approved","main_stopped":true}`},
		{body: act1},
	}}
	w := decodeWait(t, callTool1(t, New(rest, "v0"), toolCallBody("pb_wait", `{"seq":7}`)))
	if w.Changed || !strings.Contains(w.Next, "もう一度 pb_wait") {
		t.Errorf("changed / next = %v / %q, want 時間切れ", w.Changed, w.Next)
	}
}
