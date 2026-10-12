package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPlanningWritesUseExistingREST(t *testing.T) {
	for _, tt := range []struct{ name, args, path, method string }{
		{"pb_add_link", `{"seq":12,"target_seq":13,"link_type":"SF","lag_days":-2}`, "/api/v1/projects/demo/tickets/12/links", "POST"},
		{"pb_update_link", `{"seq":12,"id":"link","lag_days":3}`, "/api/v1/projects/demo/tickets/12/links/link", "PATCH"},
		{"pb_delete_link", `{"seq":12,"id":"link"}`, "/api/v1/projects/demo/tickets/12/links/link", "DELETE"},
		{"pb_move_task", `{"seq":12,"after_seq":13}`, "/api/v1/projects/demo/tickets/12/move", "POST"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rest := &fakeREST{body: `{"seq":12}`}
			out := callTool1(t, New(rest, "v0"), toolCallBody(tt.name, tt.args))
			if out.IsError || rest.gotPath != tt.path || rest.gotMethod != tt.method {
				t.Fatalf("request %+v output %+v", rest, out)
			}
			var body map[string]any
			json.Unmarshal([]byte(rest.gotBody), &body)
			if _, ok := body["seq"]; ok {
				t.Fatal("seq leaked into REST body")
			}
		})
	}
}
func TestPlanningReadKeepsBoundaryAndDates(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{{200, `{"items":[{"seq":12,"body_md":"private","start_at":1791212400000,"due_at":1791298800000,"all_day":true,"estimate_point":5,"no_overlap":true,"start_pinned":false,"due_pinned":true}],"links":[{"link_type":"FS","lag_days":1}],"total":5001,"page":1,"per_page":5000,"total_pages":2}`}, {200, `{"timezone":"Asia/Tokyo"}`}}}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_get_planning_data", `{"parent":"12","open":true}`))
	if out.IsError {
		t.Fatal(out)
	}
	if rest.gotPaths[0] != "/api/v1/projects/demo/tickets" {
		t.Fatal(rest.gotPaths)
	}
	var obj struct {
		Items     []map[string]any
		Truncated bool
		Total     int
		Links     []any
	}
	if e := json.Unmarshal([]byte(out.Content[0].Text), &obj); e != nil {
		t.Fatal(e)
	}
	if !obj.Truncated || obj.Total != 5001 || len(obj.Links) != 1 || obj.Items[0]["start_date"] != "2026-10-06" || obj.Items[0]["due_date"] != "2026-10-06" {
		t.Fatalf("output %+v", obj)
	}
	if obj.Items[0]["no_overlap"] != true || obj.Items[0]["start_pinned"] != false || obj.Items[0]["due_pinned"] != true {
		t.Fatalf("anchors %+v", obj.Items[0])
	}
	if _, ok := obj.Items[0]["body_md"]; ok {
		t.Fatal("body leaked")
	}
}
func TestPlanningDenialPreservesReason(t *testing.T) {
	rest := &fakeREST{status: 403, body: `{"error":{"code":"forbidden","message":"scope"}}`}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_add_link", `{"seq":12,"target_seq":13,"link_type":"FS"}`))
	if !out.IsError {
		t.Fatal("denial hidden")
	}
}
func TestPlanningMoveRejectsStageArguments(t *testing.T) {
	rest := &fakeREST{}
	h := New(rest, "v0")
	res := decodeRPC(t, callMCP(t, h, agentPrincipal(), toolCallBody("pb_move_task", `{"seq":12,"position":"first","staged":true}`)))
	if res.Error == nil || rest.calls != 0 {
		t.Fatal("stage argument ignored")
	}
}

// 連鎖のずらし（pb-314）。試算は版を読んでから叩き、確定でそのまま渡せる expect を添える。
func TestRescheduleDryRunAddsExpect(t *testing.T) {
	due, _ := time.Parse(time.RFC3339, "2030-10-24T11:00:00+09:00")
	for _, tt := range []struct {
		name, changes, want string
	}{
		{"きっかけが動く", `[{"seq":12,"reason":"target","version":4},{"seq":13,"reason":"ripple","version":2}]`, `[{"seq":12,"version":4},{"seq":13,"version":2}]`},
		// 動かないきっかけは changes に載らない（9.17.2）。版は読んだものを入れる
		{"きっかけが動かない", `[{"seq":13,"reason":"ripple","version":2}]`, `[{"seq":12,"version":4},{"seq":13,"version":2}]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rest := &fakeREST{steps: []fakeStep{{200, `{"seq":12,"version":4}`}, {200, `{"applied":false,"result":"ok","changes":` + tt.changes + `,"block":null,"alternative":null}`}}}
			out := callTool1(t, New(rest, "v0"), toolCallBody("pb_reschedule_task", `{"seq":12,"dry_run":true,"due_at":"2030-10-24T11:00:00+09:00","on_block":"shrink"}`))
			if out.IsError || rest.gotPaths[0] != "/api/v1/projects/demo/tickets/12" || rest.gotPaths[1] != "/api/v1/projects/demo/tickets/12/reschedule" || rest.gotIfMatch != `"4"` {
				t.Fatalf("request %+v output %+v", rest, out)
			}
			var body map[string]any
			json.Unmarshal([]byte(rest.gotBody), &body)
			if body["dry_run"] != true || body["due_at"] != float64(due.UnixMilli()) || body["on_block"] != "shrink" || body["expect"] != nil || body["seq"] != nil {
				t.Fatalf("body %s", rest.gotBody)
			}
			var got struct{ Expect json.RawMessage }
			json.Unmarshal([]byte(out.Content[0].Text), &got)
			if string(got.Expect) != tt.want {
				t.Fatalf("expect = %s, want %s", got.Expect, tt.want)
			}
		})
	}
}

func TestRescheduleBlockedDryRunHasNoExpect(t *testing.T) {
	rest := &fakeREST{steps: []fakeStep{{200, `{"version":4}`}, {200, `{"applied":false,"result":"blocked","changes":[],"block":{"code":"cannot_fit"}}`}}}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_reschedule_task", `{"seq":12,"dry_run":true,"due_at":"2030-10-24T11:00:00Z"}`))
	if out.IsError || strings.Contains(out.Content[0].Text, "expect") {
		t.Fatalf("output %+v", out)
	}
}

func TestRescheduleCommitSplitsExpect(t *testing.T) {
	rest := &fakeREST{body: `{"applied":true,"result":"ok","changes":[]}`}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_reschedule_task", `{"seq":12,"dry_run":false,"due_at":"2030-10-24T11:00:00Z","expect":[{"seq":12,"version":4},{"seq":13,"version":2}]}`))
	if out.IsError || rest.calls != 1 || rest.gotIfMatch != `"4"` || rest.gotMethod != "POST" {
		t.Fatalf("request %+v output %+v", rest, out)
	}
	var body struct {
		DryRun bool            `json:"dry_run"`
		Expect json.RawMessage `json:"expect"`
	}
	json.Unmarshal([]byte(rest.gotBody), &body)
	if body.DryRun || string(body.Expect) != `[{"seq":13,"version":2}]` {
		t.Fatalf("body %s", rest.gotBody)
	}
}

func TestRescheduleRejectsMisuse(t *testing.T) {
	for name, args := range map[string]string{
		"dry_run が無い":     `{"seq":12,"due_at":"2030-10-24T11:00:00Z"}`,
		"確定に expect が無い":  `{"seq":12,"dry_run":false}`,
		"expect にきっかけが無い": `{"seq":12,"dry_run":false,"expect":[{"seq":13,"version":2}]}`,
		"試算に expect":      `{"seq":12,"dry_run":true,"expect":[]}`,
		"時差の無い日時":         `{"seq":12,"dry_run":true,"due_at":"2030-10-24T11:00:00"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rest := &fakeREST{}
			res := decodeRPC(t, callMCP(t, New(rest, "v0"), agentPrincipal(), toolCallBody("pb_reschedule_task", args)))
			if res.Error == nil || res.Error.Code != codeInvalidParams || rest.calls != 0 {
				t.Fatalf("error %+v calls %d", res.Error, rest.calls)
			}
		})
	}
}

func TestRescheduleConflictTellsToRetry(t *testing.T) {
	rest := &fakeREST{status: 409, body: `{"error":{"code":"conflict","message":"他の人が更新しました","details":[{"code":"stale_plan"}]},"retrial":{}}`}
	out := callTool1(t, New(rest, "v0"), toolCallBody("pb_reschedule_task", `{"seq":12,"dry_run":false,"expect":[{"seq":12,"version":4}]}`))
	if !out.IsError || !strings.Contains(out.Content[0].Text, "試算し直し") || !strings.Contains(out.Content[0].Text, "stale_plan") {
		t.Fatalf("output %+v", out)
	}
}

// 秒のずらし（pb-336）。作成と変更の両方で REST へ届き、変更は渡したものだけを送る。
func TestLinkToolsPassLagSeconds(t *testing.T) {
	for _, tt := range []struct{ name, args, want string }{
		{"pb_add_link", `{"seq":12,"target_seq":13,"link_type":"FS","lag_seconds":600}`, `{"lag_seconds":600,"link_type":"FS","target_seq":13}`},
		{"pb_update_link", `{"seq":12,"id":"link","lag_seconds":-300}`, `{"lag_seconds":-300}`},
		{"pb_update_link", `{"seq":12,"id":"link","lag_days":1,"lag_seconds":600}`, `{"lag_days":1,"lag_seconds":600}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rest := &fakeREST{body: `{"id":"link"}`}
			out := callTool1(t, New(rest, "v0"), toolCallBody(tt.name, tt.args))
			if out.IsError || rest.gotBody != tt.want {
				t.Fatalf("body = %s, want %s（%+v）", rest.gotBody, tt.want, out)
			}
		})
	}
}

func TestUpdateLinkRejectsEmptyChange(t *testing.T) {
	rest := &fakeREST{}
	res := decodeRPC(t, callMCP(t, New(rest, "v0"), agentPrincipal(), toolCallBody("pb_update_link", `{"seq":12,"id":"link"}`)))
	if res.Error == nil || res.Error.Code != codeInvalidParams || rest.calls != 0 {
		t.Fatalf("error %+v calls %d", res.Error, rest.calls)
	}
}
