package v1

import (
	"testing"
	"time"
)

func policyBody() string {
	return "## 見積・実績\n\n### 実績\n\n```pb-effort-policy\n{\"version\":\"actual-v0\",\"point_formula\":\"change-volume-v0\",\"time_source\":\"reported_or_pb\",\"require_registration\":true}\n```\n\n### 見積\n"
}
func metric(code int64) changeMetrics {
	z, f, b := int64(0), int64(1), int64(1)
	return changeMetrics{Seq: 1, CodeLines: &code, TestLines: &z, DocLines: &z, OtherLines: &z, Files: &f, Branches: &b, LearningEvents: &z, Source: "fixture commit range, excludes generated files"}
}
func TestWorkPolicyAndPoint(t *testing.T) {
	p := parseWorkPolicy(policyBody(), 7)
	if p.Status != "ready" || p.DocumentVersion != 7 || !p.RequireRegistration {
		t.Fatalf("%+v", p)
	}
	for _, body := range []string{"### 実績\n```pb-effort-policy\n{}\n```", policyBody() + "\n### 実績\n```pb-effort-policy\n{}\n```"} {
		if parseWorkPolicy(body, 1).Status == "ready" {
			t.Fatalf("accepted %s", body)
		}
	}
	if parseWorkPolicy("### 実績\nNo contract", 1).Status != "unset" {
		t.Fatal("missing contract")
	}
	for _, tc := range []struct {
		code  int64
		point float64
	}{{1, 1}, {100, 3}, {1000, 5}, {10000, 13}, {1000000, 21}} {
		got := changePoint(metric(tc.code))
		if got == nil || *got != tc.point {
			t.Fatalf("%d: %v", tc.code, got)
		}
	}
	if changePoint(metric(0)) != nil {
		t.Fatal("zero changes must be unmeasured")
	}
	m := metric(100)
	m.Source = ""
	if changePoint(m) != nil {
		t.Fatal("missing source")
	}
	earlier, later := time.Unix(1, 0), time.Unix(2, 0)
	a, b := metric(1), metric(100)
	inputs := []workPointInput{{p, &a, earlier.UnixNano()}, {p, &b, later.UnixNano()}}
	got, version, reason := pointFromInputs(inputs)
	if got == nil || *got != 3 || version != "actual-v0" || reason != "" {
		t.Fatalf("%v %s %s", got, version, reason)
	}
	compact := compactPointInputs(inputs)
	if len(compact) != 1 {
		t.Fatal(compact)
	}
	inputs = append(inputs, workPointInput{workPolicy{}, &b, later.UnixNano()})
	if got, _, _ := pointFromInputs(inputs); got != nil {
		t.Fatal("unconfigured policy mixed with valid")
	}
}
