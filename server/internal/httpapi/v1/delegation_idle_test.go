package v1

import (
	"testing"
	"time"
)

// main が止まったとみなすか（Delegation.md 10.4。pb-310）。
func TestMainStopped(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	ago := func(m int) *time.Time { v := now.Add(-time.Duration(m) * time.Minute); return &v }
	for _, tc := range []struct {
		name     string
		status   string
		scope    string
		lastSeen *time.Time
		want     bool
	}{
		{"既定30分を過ぎた", "approved", `{}`, ago(30), true},
		{"既定30分に届かない", "approved", `{}`, ago(29), false},
		{"設定の10分を過ぎた", "approved", `{"main_idle_minutes":10}`, ago(11), true},
		{"0 は判定しない", "approved", `{"main_idle_minutes":0}`, ago(600), false},
		{"計画中も判定する", "planning", `{}`, ago(31), true},
		{"停止中は判定しない", "paused", `{}`, ago(600), false},
		{"終了は判定しない", "ended", `{}`, ago(600), false},
		{"1度も要求が無ければ判定しない", "approved", `{}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mainStopped(tc.status, []byte(tc.scope), tc.lastSeen, now); got != tc.want {
				t.Errorf("mainStopped = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateMainIdleMinutes(t *testing.T) {
	for _, tc := range []struct {
		scope string
		ok    bool
	}{
		{`{"main_idle_minutes":0}`, true},
		{`{"main_idle_minutes":30}`, true},
		{`{"main_idle_minutes":1440}`, true},
		{`{"main_idle_minutes":1441}`, false},
		{`{"main_idle_minutes":-1}`, false},
		{`{"main_idle_minutes":1.5}`, false},
		{`{"main_idle_minutes":"30"}`, false},
	} {
		got := validateScopeConstraints([]byte(tc.scope), "scope")
		if (len(got) == 0) != tc.ok {
			t.Errorf("%s: details = %v, want ok=%v", tc.scope, got, tc.ok)
		}
	}
}
