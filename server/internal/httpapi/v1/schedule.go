package v1

// 予定日時（ApiDesign.md 9.3.1、DbDesign.md 6.6.1）。チケットとスプリントが共有する。
//
// **予定はエポックミリ秒の半開区間 [開始, 終わり) と、終日の印で持つ**（pb-217）。
// 終日なら両端はプロジェクトの基準タイムゾーン（DbDesign.md 6.23）の0時でなければ
// ならない。CHECK では基準タイムゾーンを引けないので、ここで検証する。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// projectLocation はプロジェクトの基準タイムゾーンを返す。
//
// **読めない名前なら UTC に倒さず失敗にする。** 保存の時点で Go と DB の両方で
// 検証している（projects_update.go）ので、ここで読めないのは実行環境の tzdata の
// 欠けであり、黙って UTC で数えると終日の判定が静かにずれる。
func projectLocation(ctx context.Context, q gen.Querier, projectID string) (*time.Location, error) {
	name, err := q.GetProjectTimezone(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("プロジェクト %q の基準タイムゾーンを読めない: %w", projectID, err)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("基準タイムゾーン %q を解決できない: %w", name, err)
	}
	return loc, nil
}

// dayStartIn は t の日の0時（loc の暦で）に days 日を足した瞬間を返す。
// due_within の境界（N 日後の日の終わり＝翌日の0時）と、スプリントを終えたときの
// 「今日を含める」終わりに使う。
func dayStartIn(t time.Time, loc *time.Location, days int) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day()+days, 0, 0, 0, 0, loc)
}

// isMidnightIn は t が loc の0時ちょうどかを返す。
func isMidnightIn(t time.Time, loc *time.Location) bool {
	l := t.In(loc)
	return l.Hour() == 0 && l.Minute() == 0 && l.Second() == 0 && l.Nanosecond() == 0
}

// parseCreateSchedule は作成の本文から予定を読む（スプリントの作成と開始）。
// 終わりの欄は end_at。all_day の省略は終日。
func parseCreateSchedule(
	startRaw, endRaw json.RawMessage, allDay *bool, details *[]apierr.Detail,
) (*time.Time, *time.Time, bool) {
	var s, e optional[time.Time]
	s, *details = parseOptionalInstant(startRaw, "start_at", *details)
	e, *details = parseOptionalInstant(endRaw, "end_at", *details)
	return pick(s, nil), pick(e, nil), boolOr(allDay, true)
}

// validateSchedule は更新後に効く予定を検証する（9.3.1）。
//
// endField は終わりの欄の名前（チケットは due_at、スプリントは end_at）。
func validateSchedule(
	start, end *time.Time, allDay bool, loc *time.Location, endField string, details []apierr.Detail,
) []apierr.Detail {
	if start != nil && end != nil && start.After(*end) {
		details = append(details, apierr.Detail{
			Field: endField, Code: "invalid",
			Message: "終わりは開始以降の日時で指定してください",
		})
	}
	if !allDay {
		return details
	}
	for _, f := range []struct {
		name string
		t    *time.Time
	}{{"start_at", start}, {endField, end}} {
		if f.t != nil && !isMidnightIn(*f.t, loc) {
			details = append(details, apierr.Detail{
				Field: f.name, Code: "not_midnight",
				Message: "終日の予定は、プロジェクトの基準タイムゾーン（" + loc.String() + "）の0時で指定してください",
			})
		}
	}
	return details
}

// scheduleActivityValue は履歴（activity）に残す値を作る（ApiDesign.md 9.13.2）。
//
// **終日なら YYYY-MM-DD、時刻付きならエポックミリ秒の文字列**。終わりの欄（isEnd）は
// 半開区間の終わり＝締切日の翌日の0時なので、1日戻した締切日で残す——当時の
// 「9/30締切」を、後から基準タイムゾーンや all_day が変わっても読めるように。
func scheduleActivityValue(t *time.Time, allDay, isEnd bool, loc *time.Location) *string {
	if t == nil {
		return nil
	}
	var s string
	if allDay {
		l := t.In(loc)
		if isEnd {
			l = l.AddDate(0, 0, -1)
		}
		s = l.Format(time.DateOnly)
	} else {
		s = strconv.FormatInt(t.UnixMilli(), 10)
	}
	return &s
}

// optionalInstantField は PATCH の本文から、エポックミリ秒の欄を読む
// （送られない／null／値 を区別する）。
func optionalInstantField(
	raw updateTicketRequest, field string, details []apierr.Detail,
) (optional[time.Time], []apierr.Detail) {
	return parseOptionalInstant(raw[field], field, details)
}

// parseOptionalInstant は1欄ぶんの RawMessage を読む。nil は「送られていない」。
func parseOptionalInstant(
	v json.RawMessage, field string, details []apierr.Detail,
) (optional[time.Time], []apierr.Detail) {
	if v == nil {
		return optional[time.Time]{}, details
	}
	if isJSONNull(v) {
		return optional[time.Time]{Set: true, Null: true}, details
	}
	var ms int64
	if err := json.Unmarshal(v, &ms); err != nil {
		return optional[time.Time]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日時はエポックミリ秒の整数で指定してください",
		})
	}
	return optional[time.Time]{Set: true, Value: time.UnixMilli(ms).UTC()}, details
}

// optionalBoolField は PATCH の本文から真偽の欄を読む。null は受けない。
func optionalBoolField(
	raw updateTicketRequest, field string, details []apierr.Detail,
) (optional[bool], []apierr.Detail) {
	v, ok := raw[field]
	if !ok {
		return optional[bool]{}, details
	}
	var b bool
	if isJSONNull(v) || json.Unmarshal(v, &b) != nil {
		return optional[bool]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid", Message: field + " は true か false で指定してください",
		})
	}
	return optional[bool]{Set: true, Value: b}, details
}

// instantParam は optional を sqlc の引数（*_set と値）へ写す。
func instantParam(o optional[time.Time]) (bool, *time.Time) {
	if !o.Set {
		return false, nil
	}
	if o.Null {
		return true, nil
	}
	t := o.Value
	return true, &t
}

// pick は PATCH で送られた値があればそれを、無ければ現在値を返す。
func pick(o optional[time.Time], current *time.Time) *time.Time {
	if !o.Set {
		return current
	}
	if o.Null {
		return nil
	}
	t := o.Value
	return &t
}

// boolOr は省略可能な真偽に既定値を与える（all_day の省略は終日）。
func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}
