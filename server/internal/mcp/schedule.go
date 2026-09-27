package mcp

// 予定日時の写し替え（Design.md 8.5.1、ApiDesign.md 9.3.1。pb-217）。
//
// **REST は start_at / due_at（エポックミリ秒・半開区間）＋ all_day だけを受ける**が、
// エージェントに「締切日の翌日の0時」を計算させると誤る。MCP は日付でも受け、
// プロジェクトの基準タイムゾーンで変換してから REST へ渡す。読み出しでは、終日の
// チケットに start_date / due_date（締切日を含む日付）を添える。

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// scheduleArgs は作成・更新で受ける予定の引数。nil は「渡されていない」。
type scheduleArgs struct {
	StartDate, DueDate *string // 終日。YYYY-MM-DD（締切日を含む）
	StartAt, DueAt     *string // 時刻付き。時差を含む ISO8601
}

// projectLocation はプロジェクトの基準タイムゾーンを REST から引く。
func (h *Handler) projectLocation(r *http.Request, key string) (*time.Location, *rpcError) {
	res, err := h.getREST(r, projectPath(key), nil)
	if err != nil || !res.ok() {
		return nil, newError(codeInternalError, "プロジェクトの基準タイムゾーンを読めない")
	}
	var p struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(res.body, &p); err != nil || p.Timezone == "" {
		return nil, newError(codeInternalError, "プロジェクトの基準タイムゾーンを読めない")
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return nil, newError(codeInternalError, "基準タイムゾーン "+p.Timezone+" を解決できない")
	}
	return loc, nil
}

// applySchedule は予定の引数を REST の本文へ写す。**空文字は null（外す）**として送る。
//
// 終日と時刻付きを同じ呼び出しで混ぜると -32602——all_day は行ごとに1つなので、
// どちらの意味で送ったのかを MCP が決められない。
func (h *Handler) applySchedule(r *http.Request, key string, a scheduleArgs, body map[string]any) *rpcError {
	dates := a.StartDate != nil || a.DueDate != nil
	times := a.StartAt != nil || a.DueAt != nil
	switch {
	case dates && times:
		return newError(codeInvalidParams,
			"終日（start_date / due_date）と時刻付き（start_at / due_at）を同じ呼び出しで混ぜないこと")
	case dates:
		loc, rpcErr := h.projectLocation(r, key)
		if rpcErr != nil {
			return rpcErr
		}
		for _, f := range []struct {
			arg   *string
			field string
			days  int
		}{{a.StartDate, "start_at", 0}, {a.DueDate, "due_at", 1}} {
			if f.arg == nil {
				continue
			}
			if strings.TrimSpace(*f.arg) == "" {
				body[f.field] = nil
				continue
			}
			d, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(*f.arg), loc)
			if err != nil {
				return newError(codeInvalidParams, "日付は YYYY-MM-DD で渡すこと: "+*f.arg)
			}
			body[f.field] = d.AddDate(0, 0, f.days).UnixMilli()
		}
		body["all_day"] = true
	case times:
		for _, f := range []struct {
			arg   *string
			field string
		}{{a.StartAt, "start_at"}, {a.DueAt, "due_at"}} {
			if f.arg == nil {
				continue
			}
			if strings.TrimSpace(*f.arg) == "" {
				body[f.field] = nil
				continue
			}
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(*f.arg))
			if err != nil {
				return newError(codeInvalidParams,
					"日時は時差を含む ISO8601 で渡すこと（例: 2026-09-30T17:00:00+09:00）: "+*f.arg)
			}
			body[f.field] = t.UnixMilli()
		}
		body["all_day"] = false
	}
	return nil
}

// scheduleDates は終日のチケットの start_date / due_date（締切日を含む日付）を返す。
// 時刻付きや値が無い欄は空文字。obj の start_at / due_at は isoTimes が戻した ISO 文字列である。
func scheduleDates(obj map[string]any, loc *time.Location) (start, due string) {
	if allDay, _ := obj["all_day"].(bool); !allDay {
		return "", ""
	}
	day := func(k string, back int) string {
		s, _ := obj[k].(string)
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return ""
		}
		return t.In(loc).AddDate(0, 0, -back).Format(time.DateOnly)
	}
	return day("start_at", 0), day("due_at", 1)
}

// withScheduleDates は1件の本文に start_date / due_date を添える（pb_get_task）。
// 終日でなければ何も足さない。読めない本文はそのまま返す。
func withScheduleDates(body []byte, loc *time.Location) []byte {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	start, due := scheduleDates(obj, loc)
	if start == "" && due == "" {
		return body
	}
	if start != "" {
		obj["start_date"] = start
	}
	if due != "" {
		obj["due_date"] = due
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}
