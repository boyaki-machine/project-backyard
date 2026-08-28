package v1

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"
)

// iso8601UTCSeconds は ApiDesign.md 2.2 の日時の形（2026-08-11T09:03:12Z）。
// 時刻帯のオフセット表記も秒未満の端数も持たない。
var iso8601UTCSeconds = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// Time は非UTCの時刻帯と秒未満の端数を落として ISO8601 UTC で出る
// （ApiDesign.md 2.2）。
//
// ここは認証を経由しないので、期待値を固定の日時で書いてよい。
func TestTimeMarshalJSON(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)

	got, err := json.Marshal(Time(time.Date(2026, 8, 25, 9, 3, 12, 123456000, jst)))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"2026-08-25T00:03:12Z"`; string(got) != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
}

// 端数は切り上げずに落とす。0.9 秒を足しても秒は繰り上がらない。
func TestTimeMarshalJSONTruncates(t *testing.T) {
	got, err := json.Marshal(Time(time.Date(2026, 8, 25, 0, 3, 12, 900000000, time.UTC)))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"2026-08-25T00:03:12Z"`; string(got) != want {
		t.Errorf("Time = %s, want %s（切り上げてはならない）", got, want)
	}
}

// UTC より東西どちらの時刻帯でも、同じ瞬間は同じ文字列になる。
func TestTimeMarshalJSONZoneIndependent(t *testing.T) {
	instant := time.Date(2026, 8, 25, 0, 3, 12, 0, time.UTC)
	zones := map[string]*time.Location{
		"UTC": time.UTC,
		"JST": time.FixedZone("JST", 9*3600),
		"EST": time.FixedZone("EST", -5*3600),
	}
	for name, loc := range zones {
		got, err := json.Marshal(Time(instant.In(loc)))
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if want := `"2026-08-25T00:03:12Z"`; string(got) != want {
			t.Errorf("%s: Time = %s, want %s", name, got, want)
		}
		if !iso8601UTCSeconds.MatchString(string(got[1 : len(got)-1])) {
			t.Errorf("%s: Time = %s が 2.2 の形でない", name, got)
		}
	}
}
