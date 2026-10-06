package mcp

import "testing"

func TestUsageToolsPreserveFiltersAndPaging(t *testing.T) {
	for _, name := range []string{"pb_get_agent_usage", "pb_list_agent_runs"} {
		rest := &fakeREST{body: `{"items":[],"total":201,"page":2,"per_page":50,"total_pages":5}`}
		h := New(rest, "v0")
		out := callTool1(t, h, toolCallBody(name, `{"from_date":"2026-10-01","to_date":"2026-10-07","model_name":"model","page":2,"per_page":50}`))
		if out.IsError || rest.gotQuery.Get("from_date") != "2026-10-01" || rest.gotQuery.Get("to_date") != "2026-10-07" || rest.gotQuery.Get("page") != "2" || rest.gotQuery.Get("model_name") != "model" {
			t.Fatalf("request %+v output %+v", rest, out)
		}
		if out.Content[0].Text != rest.body {
			t.Fatal("paging was lost")
		}
	}
}
