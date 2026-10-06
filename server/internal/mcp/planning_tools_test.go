package mcp

import (
	"encoding/json"
	"testing"
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
	rest := &fakeREST{steps: []fakeStep{{200, `{"items":[{"seq":12,"body_md":"private","start_at":1791212400000,"due_at":1791298800000,"all_day":true,"estimate_point":5}],"links":[{"link_type":"FS","lag_days":1}],"total":5001,"page":1,"per_page":5000,"total_pages":2}`}, {200, `{"timezone":"Asia/Tokyo"}`}}}
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
