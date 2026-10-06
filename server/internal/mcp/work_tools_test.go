package mcp

import (
	"strings"
	"testing"
)

func TestWorkToolsForward(t *testing.T) {
	for _, tc := range []struct{ name, args, path string }{
		{"pb_start_work", `{"seq":133,"request_id":"start","allocations":[{"seq":133,"share":1}]}`, "/tickets/133/work-sessions"},
		{"pb_record_work", `{"seq":133,"work_id":"work","request_id":"finish","action":"finish","result":"completed","tokens":0}`, "/tickets/133/work-sessions/work/events"},
		{"pb_list_work", `{"seq":133,"page":2,"per_page":1}`, "/tickets/133/work-sessions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rest := &fakeREST{body: `{"id":"work","tokens":null}`}
			out := callTool1(t, New(rest, "v0"), toolCallBody(tc.name, tc.args))
			if out.IsError || !strings.HasSuffix(rest.gotPath, tc.path) {
				t.Fatalf("%+v %+v", rest, out)
			}
			if tc.name == "pb_list_work" && rest.gotQuery.Get("page") != "2" {
				t.Fatal("page lost")
			}
			if tc.name == "pb_record_work" && !strings.Contains(string(rest.gotBody), `"tokens":0`) {
				t.Fatal("zero lost")
			}
		})
	}
}
