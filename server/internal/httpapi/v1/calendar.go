// 暦API（ApiDesign.md 5.8）。
//
//	GET    /api/v1/projects/{key}/calendar             project.view
//	PUT    /api/v1/projects/{key}/calendar/source      project.edit
//	POST   /api/v1/projects/{key}/calendar/fetch       project.edit
//	POST   /api/v1/projects/{key}/calendar/import      project.edit
//	GET    /api/v1/projects/{key}/calendar/days        project.view
//	PUT    /api/v1/projects/{key}/calendar/days/{day}  project.edit
//	DELETE /api/v1/projects/{key}/calendar/days/{day}  project.edit
//
// **権限を新設しない。** 暦はタグ・スプリントの定義と同じく「プロジェクト全体の
// 語彙」であり、同じ project.edit で守る。**audit_log にも載せない**——2.10 が
// 記録するのは影響が1プロジェクトに収まらない操作である（5.8）。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/holiday"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 暦の制約（ApiDesign.md 5.8）。
const (
	// calendarFetchCooldown は同じ google_id への取得の間隔（5.8.3）。
	// プロジェクトをまたいで数え、失敗した試行も数える。
	calendarFetchCooldown = time.Hour

	// calendarImportMaxBytes は取り込む iCal 本文の上限（5.8.4）。
	calendarImportMaxBytes = 1 << 20
	// calendarImportBodyMax は取り込みの要求本文の上限。**JSON の中では改行が
	// \r\n の4文字になる**ので、ics の上限の数倍を読めるようにしておかないと、
	// 上限内の iCal が too_large ではなく「JSON として読めない」で落ちる。
	calendarImportBodyMax = 4 * calendarImportMaxBytes

	// calendarDaysMaxRange は GET /calendar/days の期間の上限（366日×3。5.8.5）。
	calendarDaysMaxRange = 366 * 3

	calendarDayNameMaxLen = 200
)

// 5.8.5 の reason。
const (
	calendarReasonOverride = "override"
	calendarReasonHoliday  = "holiday"
	calendarReasonWeekend  = "weekend"
	calendarReasonNone     = "none"
)

type calendarSourceView struct {
	Kind            string  `json:"kind"`
	GoogleID        *string `json:"google_id"`
	Name            *string `json:"name"`
	HolidayCount    int64   `json:"holiday_count"`
	ObservanceCount int64   `json:"observance_count"`
	FetchedAt       *Time   `json:"fetched_at"`
	LastAttemptAt   *Time   `json:"last_attempt_at"`
	LastError       *string `json:"last_error"`
	NextFetchAt     *Time   `json:"next_fetch_at"`
}

type calendarView struct {
	Source   *calendarSourceView `json:"source"`
	Imported *calendarImported   `json:"imported,omitempty"`
}

type calendarImported struct {
	Days    int `json:"days"`
	Skipped int `json:"skipped"`
}

type calendarEventView struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type calendarOverrideView struct {
	IsHoliday bool    `json:"is_holiday"`
	Name      *string `json:"name"`
}

type calendarDayView struct {
	Day       Date                  `json:"day"`
	IsHoliday bool                  `json:"is_holiday"`
	Reason    string                `json:"reason"`
	Events    []calendarEventView   `json:"events"`
	Override  *calendarOverrideView `json:"override"`
}

type calendarDaysView struct {
	Days []calendarDayView `json:"days"`
}

type putCalendarSourceRequest struct {
	GoogleID *string `json:"google_id"`
}

type importCalendarRequest struct {
	Filename string `json:"filename"`
	ICS      string `json:"ics"`
}

type putCalendarDayRequest struct {
	IsHoliday *bool   `json:"is_holiday"`
	Name      *string `json:"name"`
}

// ── GET /api/v1/projects/{key}/calendar ─────────────────────

func (h *handler) getCalendar(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/calendar")
	if !ok {
		return
	}
	view, err := calendarByProject(r.Context(), h.q, projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── PUT /api/v1/projects/{key}/calendar/source ──────────────

// putCalendarSource は取得元を選ぶ・外す（5.8.2）。**選ぶだけで取りに行かない。**
// 同じ暦を他のプロジェクトが取得済みなら、その結果がすぐ使える。
func (h *handler) putCalendarSource(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "PUT /projects/{key}/calendar/source")
	if !ok {
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(errors.New("PUT /calendar/source にトランザクション実行口が渡っていない")))
		return
	}
	var req putCalendarSourceRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.GoogleID != nil && !holiday.ValidGoogleID(*req.GoogleID) {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "google_id", Code: "invalid",
			Message: "暦の識別子は ja.japanese のような形で指定してください",
		}))
		return
	}

	ctx := r.Context()
	var view calendarView
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		source := pgtype.Text{}
		if req.GoogleID != nil {
			id, err := q.EnsureGoogleHolidaySource(ctx, gen.EnsureGoogleHolidaySourceParams{
				ID: ulidgen.New(), GoogleID: pgtype.Text{String: *req.GoogleID, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("暦 %q の取得元を用意できない: %w", *req.GoogleID, err)
			}
			source = pgtype.Text{String: id, Valid: true}
		}
		if err := q.SetProjectHolidaySource(ctx, gen.SetProjectHolidaySourceParams{
			ProjectID: projectID, SourceID: source,
		}); err != nil {
			return fmt.Errorf("取得元を設定できない: %w", err)
		}
		// 取り込みの暦を使っていたなら、参照されなくなったので消す（DbDesign.md 6.23）。
		if err := q.DeleteOwnedHolidaySources(ctx, gen.DeleteOwnedHolidaySourcesParams{
			ProjectID: pgtype.Text{String: projectID, Valid: true},
		}); err != nil {
			return fmt.Errorf("取り込みの暦を消せない: %w", err)
		}
		var err error
		view, err = calendarByProject(ctx, q, projectID)
		return err
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── POST /api/v1/projects/{key}/calendar/fetch ──────────────

// fetchCalendar は Google の暦を取りに行き、取り込む（5.8.3）。
//
// **待ち時間の判定と last_attempt_at の更新を、取りに行く前に1つの
// トランザクションで行う。** 行ロック（FOR UPDATE）で直列化するので、同時に
// 2回押されても相手先へ飛ぶのは1回である。取りに行くあいだはトランザクションを
// 張らない——最大15秒、行ロックと接続を握り続けないためである。
func (h *handler) fetchCalendar(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/calendar/fetch")
	if !ok {
		return
	}
	if h.tx == nil || h.holidays == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(errors.New("POST /calendar/fetch に実行口が渡っていない")))
		return
	}
	ctx := r.Context()

	cur, err := h.q.GetProjectCalendarSource(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.Kind != "google") {
		apierr.Write(w, r, apierr.New(apierr.Conflict).
			WithMessage("Google の祝日カレンダーが選ばれていません").
			WithCause(fmt.Errorf("プロジェクト %q の取得元が Google の暦でない", projectID)))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	var (
		googleID, prevHash string
		wait               time.Duration
	)
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.LockHolidaySource(ctx, cur.ID)
		if err != nil {
			return fmt.Errorf("取得元 %q をロックできない: %w", cur.ID, err)
		}
		if row.LastAttemptAt != nil {
			if next := (*row.LastAttemptAt).Add(calendarFetchCooldown); time.Now().Before(next) {
				wait = time.Until(next)
				return nil
			}
		}
		googleID, prevHash = row.GoogleID.String, row.ContentSha256.String
		return q.MarkHolidaySourceAttempt(ctx, cur.ID)
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if wait > 0 {
		apierr.Write(w, r, apierr.New(apierr.RateLimited).
			WithMessage("この暦は1時間以内に取得しています。時間を置いてからやり直してください").
			WithRetryAfter(int(math.Ceil(wait.Seconds()))))
		return
	}

	body, fetchErr := h.holidays.Fetch(ctx, googleID)
	var cal holiday.Calendar
	if fetchErr == nil {
		cal, fetchErr = holiday.Parse(body)
		if fetchErr != nil {
			fetchErr = &holiday.FetchError{Message: "取得した内容を iCal として読めませんでした", Cause: fetchErr}
		}
	}
	if fetchErr != nil {
		msg := "取得元から取り込めませんでした"
		var fe *holiday.FetchError
		if errors.As(fetchErr, &fe) {
			msg = fe.Message
		}
		// 前回までの取り込み分は残す。失敗の記録だけを書く。
		if err := h.q.MarkHolidaySourceFailed(ctx, gen.MarkHolidaySourceFailedParams{
			ID: cur.ID, LastError: pgtype.Text{String: msg, Valid: true},
		}); err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.UpstreamFailed).WithMessage(msg).WithCause(fetchErr))
		return
	}

	hash := holiday.ContentHash(body)
	var view calendarView
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **中身が同じなら取り込み直さない。** Google は ETag / Last-Modified を
		// 返さないので、条件付き取得の代わりである（DbDesign.md 6.23）。
		if hash != prevHash {
			if err := replaceHolidayDays(ctx, q, cur.ID, cal.Days); err != nil {
				return err
			}
		}
		if err := q.MarkHolidaySourceFetched(ctx, gen.MarkHolidaySourceFetchedParams{
			ID: cur.ID, Name: nonEmptyText(cal.Name), ContentSha256: pgtype.Text{String: hash, Valid: true},
		}); err != nil {
			return fmt.Errorf("取得の結果を記録できない: %w", err)
		}
		var err error
		view, err = calendarByProject(ctx, q, projectID)
		return err
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── POST /api/v1/projects/{key}/calendar/import ─────────────

// importCalendar は .ics の本文を取り込む（5.8.4）。取得元はこのプロジェクトが
// 所有する暦になり、取り込むたびに中身を入れ替える。
func (h *handler) importCalendar(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/calendar/import")
	if !ok {
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(errors.New("POST /calendar/import にトランザクション実行口が渡っていない")))
		return
	}
	var req importCalendarRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, calendarImportBodyMax)).Decode(&req); err != nil {
		apierr.Write(w, r, apierr.New(apierr.BadRequest).
			WithCause(fmt.Errorf("リクエスト本文を JSON として読めない: %w", err)))
		return
	}
	icsDetail := func(code, msg string) {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "ics", Code: code, Message: msg,
		}))
	}
	if len(req.ICS) > calendarImportMaxBytes {
		icsDetail("too_large", "ファイルは 1MiB 以内にしてください")
		return
	}
	cal, err := holiday.Parse(req.ICS)
	if err != nil {
		icsDetail("invalid_ics", "iCal（.ics）として読めないファイルです")
		return
	}
	if len(cal.Days) == 0 {
		icsDetail("no_all_day_events", "終日の予定が1件もありません")
		return
	}
	name := cal.Name
	if name == "" {
		name = strings.TrimSpace(req.Filename)
	}

	ctx := r.Context()
	var view calendarView
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		sourceID := ""
		cur, err := q.GetProjectCalendarSource(ctx, projectID)
		switch {
		case err == nil && cur.Kind == "file" && cur.OwnerProjectID.String == projectID:
			sourceID = cur.ID // 取り込み直しは同じ行を使い回す
		case err == nil || errors.Is(err, pgx.ErrNoRows):
			sourceID = ulidgen.New()
			if err := q.CreateFileHolidaySource(ctx, gen.CreateFileHolidaySourceParams{
				ID: sourceID, OwnerProjectID: pgtype.Text{String: projectID, Valid: true},
			}); err != nil {
				return fmt.Errorf("取り込みの暦を作れない: %w", err)
			}
			if err := q.SetProjectHolidaySource(ctx, gen.SetProjectHolidaySourceParams{
				ProjectID: projectID, SourceID: pgtype.Text{String: sourceID, Valid: true},
			}); err != nil {
				return fmt.Errorf("取得元を設定できない: %w", err)
			}
		default:
			return err
		}
		if err := replaceHolidayDays(ctx, q, sourceID, cal.Days); err != nil {
			return err
		}
		if err := q.MarkHolidaySourceFetched(ctx, gen.MarkHolidaySourceFetchedParams{
			ID: sourceID, Name: nonEmptyText(name),
			ContentSha256: pgtype.Text{String: holiday.ContentHash(req.ICS), Valid: true},
		}); err != nil {
			return fmt.Errorf("取り込みの結果を記録できない: %w", err)
		}
		view, err = calendarByProject(ctx, q, projectID)
		return err
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	view.Imported = &calendarImported{Days: len(cal.Days), Skipped: cal.Skipped}
	WriteJSON(w, http.StatusOK, view)
}

// ── GET /api/v1/projects/{key}/calendar/days ────────────────

// listCalendarDays は期間 [from, to) の休日・行事・上書きを返す（5.8.5）。
// **平日で何も無い日は返さない。** 返らない日は平日である。
func (h *handler) listCalendarDays(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/calendar/days")
	if !ok {
		return
	}
	from, fromOK := parseCalendarDay(r.URL.Query().Get("from"))
	to, toOK := parseCalendarDay(r.URL.Query().Get("to"))
	var details []apierr.Detail
	if !fromOK {
		details = append(details, apierr.Detail{Field: "from", Code: "invalid",
			Message: "from は YYYY-MM-DD で指定してください"})
	}
	if !toOK {
		details = append(details, apierr.Detail{Field: "to", Code: "invalid",
			Message: "to は YYYY-MM-DD で指定してください"})
	}
	if fromOK && toOK {
		if n := int(to.Sub(from).Hours() / 24); n <= 0 || n > calendarDaysMaxRange {
			details = append(details, apierr.Detail{Field: "to", Code: "out_of_range",
				Message: fmt.Sprintf("期間は1日以上%d日以内で指定してください", calendarDaysMaxRange)})
		}
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}
	days, err := calendarDays(r.Context(), h.q, projectID, from, to)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, calendarDaysView{Days: days})
}

// ── PUT / DELETE /api/v1/projects/{key}/calendar/days/{day} ─

// putCalendarDay は日ごとの上書きを作るか置き換える（5.8.6）。
// 手動の追加（祝日でない日を休日に）も削除（祝日を平日に）もこの口で行う。
func (h *handler) putCalendarDay(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "PUT /projects/{key}/calendar/days/{day}")
	if !ok {
		return
	}
	day, dayOK := parseCalendarDay(chi.URLParam(r, "day"))
	var req putCalendarDayRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	var details []apierr.Detail
	if !dayOK {
		details = append(details, apierr.Detail{Field: "day", Code: "invalid",
			Message: "日付は YYYY-MM-DD で指定してください"})
	}
	if req.IsHoliday == nil {
		details = append(details, apierr.Detail{Field: "is_holiday", Code: "required",
			Message: "休日にするかどうかを指定してください"})
	}
	name := pgtype.Text{}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if utf8.RuneCountInString(n) > calendarDayNameMaxLen {
			details = append(details, apierr.Detail{Field: "name", Code: "too_long",
				Message: fmt.Sprintf("名前は%d文字以内で入力してください", calendarDayNameMaxLen)})
		}
		name = nonEmptyText(n)
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	ctx := r.Context()
	if err := h.q.UpsertProjectCalendarDay(ctx, gen.UpsertProjectCalendarDayParams{
		ProjectID: projectID, Day: pgtype.Date{Time: day, Valid: true},
		IsHoliday: *req.IsHoliday, Name: name,
		CreatedBy: pgtype.Text{String: p.ActorID, Valid: p.ActorID != ""},
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("上書きを保存できない: %w", err)))
		return
	}
	days, err := calendarDays(ctx, h.q, projectID, day, day.AddDate(0, 0, 1))
	if err != nil || len(days) != 1 {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("保存した上書きを読めない: %v", err)))
		return
	}
	WriteJSON(w, http.StatusOK, days[0])
}

// deleteCalendarDay は上書きを外す。**上書きが無くても 204**（外した後の状態は同じ）。
func (h *handler) deleteCalendarDay(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "DELETE /projects/{key}/calendar/days/{day}")
	if !ok {
		return
	}
	day, dayOK := parseCalendarDay(chi.URLParam(r, "day"))
	if !dayOK {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "day", Code: "invalid", Message: "日付は YYYY-MM-DD で指定してください",
		}))
		return
	}
	if err := h.q.DeleteProjectCalendarDay(r.Context(), gen.DeleteProjectCalendarDayParams{
		ProjectID: projectID, Day: pgtype.Date{Time: day, Valid: true},
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("上書きを外せない: %w", err)))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 補助 ────────────────────────────────────────────────────

// calendarByProject は 5.8.1 の応答を組み立てる。取得元が無ければ source は null。
func calendarByProject(ctx context.Context, q gen.Querier, projectID string) (calendarView, error) {
	row, err := q.GetProjectCalendarSource(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return calendarView{}, nil
	}
	if err != nil {
		return calendarView{}, fmt.Errorf("プロジェクト %q の暦を読めない: %w", projectID, err)
	}
	src := &calendarSourceView{
		Kind:            row.Kind,
		GoogleID:        textPtr(row.GoogleID),
		Name:            textPtr(row.Name),
		HolidayCount:    row.HolidayCount,
		ObservanceCount: row.ObservanceCount,
		FetchedAt:       apiTime(row.FetchedAt),
		LastAttemptAt:   apiTime(row.LastAttemptAt),
		LastError:       textPtr(row.LastError),
	}
	if row.Kind == "google" && row.LastAttemptAt != nil {
		if next := (*row.LastAttemptAt).Add(calendarFetchCooldown); time.Now().Before(next) {
			v := Time(next)
			src.NextFetchAt = &v
		}
	}
	return calendarView{Source: src}, nil
}

// calendarDays は期間 [from, to) の日を DbDesign.md 6.23 の順で判定する。
//
// 上書き → 取得元の祝日 → 土日 → 平日。平日で何も無い日は返さない。
func calendarDays(
	ctx context.Context, q gen.Querier, projectID string, from, to time.Time,
) ([]calendarDayView, error) {
	span := gen.ListProjectHolidayEventsParams{
		ProjectID: projectID,
		FromDay:   pgtype.Date{Time: from, Valid: true},
		ToDay:     pgtype.Date{Time: to, Valid: true},
	}
	events, err := q.ListProjectHolidayEvents(ctx, span)
	if err != nil {
		return nil, fmt.Errorf("祝日を読めない: %w", err)
	}
	overrides, err := q.ListProjectCalendarDays(ctx, gen.ListProjectCalendarDaysParams(span))
	if err != nil {
		return nil, fmt.Errorf("日ごとの上書きを読めない: %w", err)
	}

	byDay := map[string][]calendarEventView{}
	for _, e := range events {
		k := e.Day.Time.Format(time.DateOnly)
		byDay[k] = append(byDay[k], calendarEventView{Kind: e.Kind, Name: e.Name})
	}
	overrideByDay := map[string]*calendarOverrideView{}
	for _, o := range overrides {
		overrideByDay[o.Day.Time.Format(time.DateOnly)] = &calendarOverrideView{
			IsHoliday: o.IsHoliday, Name: textPtr(o.Name),
		}
	}

	out := []calendarDayView{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		k := d.Format(time.DateOnly)
		evs, ov := byDay[k], overrideByDay[k]
		weekend := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
		if evs == nil && ov == nil && !weekend {
			continue
		}
		v := calendarDayView{Day: Date(d), Events: evs, Override: ov}
		if v.Events == nil {
			v.Events = []calendarEventView{}
		}
		switch {
		case ov != nil:
			v.IsHoliday, v.Reason = ov.IsHoliday, calendarReasonOverride
		case hasHolidayEvent(evs):
			v.IsHoliday, v.Reason = true, calendarReasonHoliday
		case weekend:
			v.IsHoliday, v.Reason = true, calendarReasonWeekend
		default:
			v.Reason = calendarReasonNone
		}
		out = append(out, v)
	}
	return out, nil
}

func hasHolidayEvent(evs []calendarEventView) bool {
	for _, e := range evs {
		if e.Kind == holiday.KindHoliday {
			return true
		}
	}
	return false
}

// replaceHolidayDays は取得元の日を丸ごと入れ替える。
func replaceHolidayDays(ctx context.Context, q gen.Querier, sourceID string, days []holiday.Day) error {
	if err := q.DeleteHolidaySourceDays(ctx, sourceID); err != nil {
		return fmt.Errorf("取得元 %q の日を消せない: %w", sourceID, err)
	}
	params := gen.InsertHolidaySourceDaysParams{SourceID: sourceID}
	for _, d := range days {
		params.Days = append(params.Days, pgtype.Date{Time: d.Date, Valid: true})
		params.Kinds = append(params.Kinds, d.Kind)
		params.Names = append(params.Names, d.Name)
	}
	if err := q.InsertHolidaySourceDays(ctx, params); err != nil {
		return fmt.Errorf("取得元 %q の日を書けない: %w", sourceID, err)
	}
	return nil
}

// parseCalendarDay は YYYY-MM-DD を読む。空は不正とする（5.8.5 の from / to は必須）。
func parseCalendarDay(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}
