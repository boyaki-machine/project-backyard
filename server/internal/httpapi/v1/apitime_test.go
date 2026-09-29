package v1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Time はエポックミリ秒の整数で出る（ApiDesign.md 2.2、pb-224）。
// ミリ秒より下は切り上げずに落とし、時刻帯は結果に影響しない。
//
// ここは認証を経由しないので、期待値を固定の日時で書いてよい。
func TestTimeMarshalJSON(t *testing.T) {
	const want = `1787616192123` // 2026-08-25T00:03:12.123Z
	jst := time.FixedZone("JST", 9*3600)
	est := time.FixedZone("EST", -5*3600)
	instant := time.Date(2026, 8, 25, 0, 3, 12, 123456789, time.UTC)
	for name, v := range map[string]time.Time{
		"UTC": instant, "JST": instant.In(jst), "EST": instant.In(est),
	} {
		got, err := json.Marshal(Time(v))
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s: Time = %s, want %s", name, got, want)
		}
	}
}

// **v1 の応答に time.Time / pgtype.Timestamptz を直に置かない。** そのまま出すと
// RFC3339 の文字列になり、エポックミリ秒と形が混ざる（pb-224 で9項目が漏れていた）。
// 日時は apitime.go の Time を通す。
func TestNoRawTimeInJSONViews(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	raw := regexp.MustCompile(`^\s*\w+\s+\*?(time\.Time|pgtype\.Timestamptz)\s+` + "`" + `json:"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if raw.MatchString(line) {
				t.Errorf("%s:%d: 応答に日時を直に置いている（Time を使う）: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
