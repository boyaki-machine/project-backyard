// Package holiday はプロジェクトの休日の暦（DbDesign.md 6.23、ApiDesign.md 5.8）を扱う。
//
// iCal（RFC 5545）の解析は自前で持つ。読むのは終日の VEVENT の数項目
// （DTSTART / DTEND / SUMMARY / DESCRIPTION / RRULE）と暦の名前だけで、
// 依存ライブラリを足すほどの範囲ではない（pb-216 の判断）。
package holiday

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// 祝日と行事の区別（DbDesign.md 6.23）。
const (
	KindHoliday    = "holiday"
	KindObservance = "observance"
)

// nameMaxLen は holiday_source_day.name の CHECK と揃える。
const nameMaxLen = 200

// maxEventDays は1つの予定を日に展開するときの上限。**壊れた DTEND で
// 何万行も作らない**ための歯止めで、1年を超える休日の予定は想定しない。
const maxEventDays = 366

// ErrNotICS は本文が iCal として読めないことを表す。
var ErrNotICS = errors.New("iCal として読めない")

// Day は取り込む1日。
type Day struct {
	Date time.Time // その日の0時（UTC）。日付としてだけ使う
	Kind string
	Name string
}

// Calendar は解析の結果。
type Calendar struct {
	Name string // X-WR-CALNAME。無ければ空
	Days []Day
	// Skipped は取り込まなかった予定の数（時刻付き・繰り返し・名前なし）。
	Skipped int
}

// Parse は iCal の本文から終日の予定を日ごとに取り出す。
//
// **時刻付きの予定と繰り返し（RRULE）は取り込まない**（ApiDesign.md 5.8.4）。
// 数だけ Skipped に返す。同じ日に同じ名前が並んだら1件にまとめる
// （holiday_source_day の主キー）。
func Parse(text string) (Calendar, error) {
	lines := unfold(text)
	if len(lines) == 0 || !strings.EqualFold(strings.TrimSpace(lines[0]), "BEGIN:VCALENDAR") {
		return Calendar{}, ErrNotICS
	}

	var (
		cal     Calendar
		inEvent bool
		ev      map[string]prop
		seen    = map[string]bool{}
	)
	for _, line := range lines {
		name, params, value, ok := splitLine(line)
		if !ok {
			continue
		}
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VEVENT"):
			inEvent, ev = true, map[string]prop{}
		case name == "END" && strings.EqualFold(value, "VEVENT"):
			if inEvent {
				days, ok := eventDays(ev)
				if !ok {
					cal.Skipped++
				}
				for _, d := range days {
					k := d.Date.Format("20060102") + "\x00" + d.Name
					if !seen[k] {
						seen[k] = true
						cal.Days = append(cal.Days, d)
					}
				}
			}
			inEvent = false
		case inEvent:
			ev[name] = prop{params: params, value: value}
		case name == "X-WR-CALNAME":
			cal.Name = truncate(unescape(value))
		}
	}
	return cal, nil
}

// Classify は DESCRIPTION の1行目から祝日か行事かを決める（DbDesign.md 6.23）。
//
// **行事の印があるものだけを行事にする。** Google の公開祝日カレンダーは
// 日本語版で「祝日」「祭日」、他国で Public holiday / Observance を1行目に置く
// （2026-09-27 実測）。**行数では分けない**——半日休日や日付が暫定の祝日は、
// Public holiday の後ろに2行目が付く。印の無い暦（会社の休業日など）は
// 全件が休日になる。
func Classify(description string) string {
	first, _, _ := strings.Cut(description, "\n")
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "祭日") || strings.HasPrefix(first, "Observance") {
		return KindObservance
	}
	return KindHoliday
}

// ContentHash は取得した本文のハッシュを返す。**DTSTAMP の行を除く**——
// Google は取得のたびに DTSTAMP だけを書き換える（2026-09-27 実測）ため、
// そのまま比べると毎回「変わった」になる。
func ContentHash(text string) string {
	h := sha256.New()
	for _, line := range unfold(text) {
		if strings.HasPrefix(strings.ToUpper(line), "DTSTAMP") {
			continue
		}
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type prop struct {
	params map[string]string
	value  string
}

// eventDays は1つの VEVENT を日に展開する。取り込まない予定は ok=false。
func eventDays(ev map[string]prop) ([]Day, bool) {
	if _, recurring := ev["RRULE"]; recurring {
		return nil, false
	}
	start, ok := parseDate(ev["DTSTART"])
	if !ok {
		return nil, false
	}
	end := start.AddDate(0, 0, 1)
	if p, has := ev["DTEND"]; has {
		e, ok := parseDate(p)
		if !ok {
			return nil, false
		}
		if e.After(start) {
			end = e
		}
	}
	name := truncate(strings.TrimSpace(unescape(ev["SUMMARY"].value)))
	if name == "" {
		return nil, false
	}
	kind := Classify(unescape(ev["DESCRIPTION"].value))

	var days []Day
	for d := start; d.Before(end) && len(days) < maxEventDays; d = d.AddDate(0, 0, 1) {
		days = append(days, Day{Date: d, Kind: kind, Name: name})
	}
	return days, true
}

// parseDate は終日の日付（VALUE=DATE、または8桁だけの値）を読む。
// 時刻付き（20260101T090000Z など）は ok=false にする。
func parseDate(p prop) (time.Time, bool) {
	v := strings.TrimSpace(p.value)
	if len(v) != 8 {
		return time.Time{}, false
	}
	if vt, has := p.params["VALUE"]; has && !strings.EqualFold(vt, "DATE") {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102", v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// unfold は折り返された行（CRLF の直後に空白かタブ）を1行に戻す（RFC 5545 3.1）。
func unfold(text string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// splitLine は「名前;引数=値:値」を分ける。名前は大文字に揃える。
func splitLine(line string) (name string, params map[string]string, value string, ok bool) {
	head, value, found := strings.Cut(line, ":")
	if !found {
		return "", nil, "", false
	}
	parts := strings.Split(head, ";")
	name = strings.ToUpper(strings.TrimSpace(parts[0]))
	params = map[string]string{}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		params[strings.ToUpper(k)] = strings.Trim(v, `"`)
	}
	return name, params, value, name != ""
}

// unescape は TEXT 値のエスケープ（\n \, \; \\）を戻す（RFC 5545 3.3.11）。
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= nameMaxLen {
		return s
	}
	return string([]rune(s)[:nameMaxLen])
}
