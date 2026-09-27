package v1

import (
	"strconv"
	"testing"
	"time"
)

// tokyo は偽物の既定の基準タイムゾーン（fakeQuerier.GetProjectTimezone）。
var tokyo = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		panic(err)
	}
	return loc
}()

// jstAt は YYYY-MM-DD の Asia/Tokyo の0時を返す（終日の開始）。
func jstAt(d string) *time.Time {
	t, err := time.ParseInLocation(time.DateOnly, d, tokyo)
	if err != nil {
		panic(err)
	}
	return &t
}

// jstEnd は締切日 d の半開区間の終わり（翌日の0時。Asia/Tokyo）を返す。
func jstEnd(d string) *time.Time {
	t := jstAt(d).AddDate(0, 0, 1)
	return &t
}

// plusMS は t に n ミリ秒を足した瞬間を返す（0時から外れた値を作る）。
func plusMS(t *time.Time, n int64) *time.Time {
	v := t.Add(time.Duration(n) * time.Millisecond)
	return &v
}

// msOf は瞬間をエポックミリ秒の文字列にする（要求の本文に埋める）。
func msOf(t *time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func TestValidateSchedule(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	nine := jstAt("2026-09-30").Add(9 * time.Hour)
	cases := []struct {
		name       string
		start, end *time.Time
		allDay     bool
		loc        *time.Location
		want       []string // field/code
	}{
		{"終日で0時", jstAt("2026-09-30"), jstEnd("2026-09-30"), true, tokyo, nil},
		{"時刻付きは0時でなくてよい", &nine, nil, false, tokyo, nil},
		{"終日で0時でない", &nine, nil, true, tokyo, []string{"start_at/not_midnight"}},
		// 基準タイムゾーンが違えば、東京の0時は0時でない（UTC より西でも確かめる）
		{"基準タイムゾーンの0時で判定する", jstAt("2026-09-30"), nil, true, ny, []string{"start_at/not_midnight"}},
		{"終わりが開始より前", jstEnd("2026-09-30"), jstAt("2026-09-30"), true, tokyo, []string{"due_at/invalid"}},
		{"同じ瞬間は許す", &nine, &nine, false, tokyo, nil},
		{"片側だけ", nil, jstEnd("2026-09-30"), true, tokyo, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, d := range validateSchedule(c.start, c.end, c.allDay, c.loc, "due_at", nil) {
				got = append(got, d.Field+"/"+d.Code)
			}
			if len(got) != len(c.want) {
				t.Fatalf("details = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("details[%d] = %s, want %s", i, got[i], c.want[i])
				}
			}
		})
	}
}

// 履歴の値は、終日なら締切日（due_at の前日）、時刻付きならエポックミリ秒（9.13.2）。
func TestScheduleActivityValue(t *testing.T) {
	nine := jstAt("2026-09-30").Add(9 * time.Hour)
	for _, c := range []struct {
		name  string
		t     *time.Time
		all   bool
		isEnd bool
		want  string
	}{
		{"終日の開始", jstAt("2026-09-30"), true, false, "2026-09-30"},
		{"終日の期限は前日", jstEnd("2026-09-30"), true, true, "2026-09-30"},
		{"時刻付き", &nine, false, true, strconv.FormatInt(nine.UnixMilli(), 10)},
	} {
		if got := scheduleActivityValue(c.t, c.all, c.isEnd, tokyo); got == nil || *got != c.want {
			t.Errorf("%s: %v, want %s", c.name, got, c.want)
		}
	}
	if scheduleActivityValue(nil, true, true, tokyo) != nil {
		t.Error("nil が nil にならない")
	}
}

// due_within の境界は基準タイムゾーンで N 日後の日の終わり（翌日の0時）。
// UTC より西（New York）で、UTC の日付がもう翌日になっている時刻でも確かめる。
func TestDayStartIn(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	// 2026-09-30 22:00 EDT = 2026-10-01 02:00 UTC
	now := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	if got, want := dayStartIn(now, ny, 1), time.Date(2026, 10, 1, 0, 0, 0, 0, ny); !got.Equal(want) {
		t.Errorf("New York の翌日の0時 = %s, want %s", got, want)
	}
	if got, want := dayStartIn(now, tokyo, 0), *jstAt("2026-10-01"); !got.Equal(want) {
		t.Errorf("東京の今日の0時 = %s, want %s", got, want)
	}
}
