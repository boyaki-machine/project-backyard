package mcp

import (
	"encoding/json"
	"testing"
)

// REST のエポックミリ秒を MCP では ISO8601 UTC に戻す（pb-224）。入れ子の中も戻し、
// 日時でない整数・文字列のままの日時・日付には触らない。
func TestISOTimes(t *testing.T) {
	in := `{"seq":31,"created_at":1786439564000,"closed_at":null,"due_date":"2026-08-14",` +
		`"items":[{"updated_at":1786439564123,"version":3}],` +
		`"cert":{"not_before":1786439564000},"audit":{"expires_at":"2026-08-25T09:03:12Z"}}`
	var got map[string]any
	if err := json.Unmarshal(isoTimes([]byte(in)), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"created_at": "2026-08-11T09:12:44Z", "closed_at": nil, "due_date": "2026-08-14",
		"seq": float64(31),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	item := got["items"].([]any)[0].(map[string]any)
	if item["updated_at"] != "2026-08-11T09:12:44Z" || item["version"] != float64(3) {
		t.Errorf("items[0] = %v", item)
	}
	if got["cert"].(map[string]any)["not_before"] != "2026-08-11T09:12:44Z" {
		t.Errorf("cert = %v", got["cert"])
	}
	if got["audit"].(map[string]any)["expires_at"] != "2026-08-25T09:03:12Z" {
		t.Errorf("保存済みの ISO 文字列が変わった: %v", got["audit"])
	}
}

// JSON でない本文と、日時を含まない本文はそのまま返す。
func TestISOTimesPassThrough(t *testing.T) {
	for _, in := range []string{"", "plain text", `{"seq":1}`, `not json {`} {
		if got := string(isoTimes([]byte(in))); got != in {
			t.Errorf("isoTimes(%q) = %q", in, got)
		}
	}
}
