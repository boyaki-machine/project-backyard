package holiday

import (
	"errors"
	"strings"
	"testing"
)

// googleJA は Google の日本の祝日カレンダー（2026-09-27 取得）の形を写したもの。
// 祝日・行事・折り返し・エスケープを1本に入れる。
const googleJA = "BEGIN:VCALENDAR\r\n" +
	"PRODID:-//Google Inc//Google Calendar 70.9054//EN\r\n" +
	"X-WR-CALNAME:日本の祝日\r\n" +
	"BEGIN:VEVENT\r\n" +
	"DTSTART;VALUE=DATE:20260921\r\n" +
	"DTEND;VALUE=DATE:20260922\r\n" +
	"DTSTAMP:20260927T092754Z\r\n" +
	"DESCRIPTION:祝日\r\n" +
	"SUMMARY:敬老の日\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"DTSTART;VALUE=DATE:20260203\r\n" +
	"DTEND;VALUE=DATE:20260204\r\n" +
	"DTSTAMP:20260927T092754Z\r\n" +
	"DESCRIPTION:祭日\\n祭日を非表示にするには、Google カレンダーの [設定] > [日本の祝日] に移動\r\n" +
	" してください\r\n" +
	"SUMMARY:節分\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParse_GoogleJapanese(t *testing.T) {
	cal, err := Parse(googleJA)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cal.Name != "日本の祝日" {
		t.Errorf("Name = %q", cal.Name)
	}
	got := map[string]string{}
	for _, d := range cal.Days {
		got[d.Date.Format("2006-01-02")+" "+d.Name] = d.Kind
	}
	want := map[string]string{
		"2026-09-21 敬老の日": KindHoliday,
		"2026-02-03 節分":   KindObservance,
	}
	if len(got) != len(want) {
		t.Fatalf("days = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: kind = %q, want %q", k, got[k], v)
		}
	}
	if cal.Skipped != 0 {
		t.Errorf("Skipped = %d", cal.Skipped)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		desc, want string
	}{
		{"祝日", KindHoliday},
		{"祭日\n祭日を非表示にするには…", KindObservance},
		{"Public holiday", KindHoliday},
		{"Public holiday in England, Wales, Northern Ireland", KindHoliday},
		// 2行目が付く祝日。行数で分けると行事に落ちる
		{"Public holiday\nThis is a half-day holiday.", KindHoliday},
		{"Observance\nTo hide observances, go to Google Calendar Settings", KindObservance},
		// 印の無い暦（会社の休業日など）は休日
		{"", KindHoliday},
		{"創立記念日のため休業", KindHoliday},
	}
	for _, c := range cases {
		if got := Classify(c.desc); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestParse_SkipsAndExpands(t *testing.T) {
	text := "BEGIN:VCALENDAR\n" +
		// 3日にわたる終日の予定。DTEND は含まない
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:20261229\nDTEND;VALUE=DATE:20270101\nSUMMARY:年末休業\nEND:VEVENT\n" +
		// 時刻付き
		"BEGIN:VEVENT\nDTSTART:20261001T090000Z\nDTEND:20261001T100000Z\nSUMMARY:会議\nEND:VEVENT\n" +
		// 繰り返し
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:20260101\nRRULE:FREQ=YEARLY\nSUMMARY:元日\nEND:VEVENT\n" +
		// 名前なし
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:20260102\nEND:VEVENT\n" +
		// DTEND なしは1日。同じ日・同じ名前の重複は1件にまとめる
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:20260615\nSUMMARY:創立記念日\\, 本社\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:20260615\nSUMMARY:創立記念日\\, 本社\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	cal, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var got []string
	for _, d := range cal.Days {
		got = append(got, d.Date.Format("0102")+d.Name)
	}
	want := "1229年末休業 1230年末休業 1231年末休業 0615創立記念日, 本社"
	if strings.Join(got, " ") != want {
		t.Errorf("days = %q, want %q", strings.Join(got, " "), want)
	}
	if cal.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", cal.Skipped)
	}
}

func TestParse_NotICS(t *testing.T) {
	for _, text := range []string{"", "hello", "<html>", "BEGIN:VEVENT\nEND:VEVENT"} {
		if _, err := Parse(text); !errors.Is(err, ErrNotICS) {
			t.Errorf("Parse(%q) err = %v, want ErrNotICS", text, err)
		}
	}
}

func TestParse_TruncatesLongName(t *testing.T) {
	long := strings.Repeat("祝", 250)
	text := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nDTSTART;VALUE=DATE:20260101\nSUMMARY:" + long + "\nEND:VEVENT\nEND:VCALENDAR\n"
	cal, err := Parse(text)
	if err != nil || len(cal.Days) != 1 {
		t.Fatalf("Parse: %v %v", cal, err)
	}
	if n := len([]rune(cal.Days[0].Name)); n != nameMaxLen {
		t.Errorf("name length = %d, want %d", n, nameMaxLen)
	}
}

func TestContentHash_IgnoresDTSTAMP(t *testing.T) {
	other := strings.ReplaceAll(googleJA, "DTSTAMP:20260927T092754Z", "DTSTAMP:20261001T000000Z")
	if ContentHash(googleJA) != ContentHash(other) {
		t.Error("DTSTAMP だけ違う本文でハッシュが変わった")
	}
	changed := strings.ReplaceAll(googleJA, "敬老の日", "敬老の日（改）")
	if ContentHash(googleJA) == ContentHash(changed) {
		t.Error("中身が変わってもハッシュが同じ")
	}
}

func TestGoogleURL(t *testing.T) {
	want := "https://calendar.google.com/calendar/ical/ja.japanese%23holiday%40group.v.calendar.google.com/public/basic.ics"
	if got := GoogleURL("ja.japanese"); got != want {
		t.Errorf("GoogleURL = %q", got)
	}
	for id, ok := range map[string]bool{
		"ja.japanese": true, "ja.south_korea": true,
		"": false, "japanese": false, "ja.japanese/../x": false, "JA.japanese": false,
		"ja.japanese#holiday@evil": false,
	} {
		if ValidGoogleID(id) != ok {
			t.Errorf("ValidGoogleID(%q) = %v", id, !ok)
		}
	}
}
