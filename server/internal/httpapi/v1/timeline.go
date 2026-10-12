// タイムライン（ApiDesign.md 9.2.7・9.17.3。pb-296）。
//
// 読む口は一覧の view=timeline（tickets.go から呼ぶ writeTimelineList）、書く口は連鎖のずらし
// （timeline_reschedule.go）と当初計画の版（本ファイルの schedule-baselines）。
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ticketViewTimeline は view=timeline（9.2.7）。件数の上限はガントと同じ 5,000件。
const ticketViewTimeline = "timeline"

// timelineSnapChoices は settings.timeline_snap_seconds が取れる値（DbDesign.md 6.4）。
// **無い・知らない値は 60 に倒し、保存は拒まない**——サーバは連鎖の最小の長さと [ここで詰める] の
// 丸めに使う（画面と同じ値を使わないと、試算と判定が食い違う）。
var timelineSnapChoices = []int64{1, 10, 30, 60, 300, 900}

const timelineSnapDefault int64 = 60

// scheduleBaselineMaxItems は版1つに写せる件数の上限（DbDesign.md 6.32。9.2.6 と同じ 5,000件）。
const scheduleBaselineMaxItems = 5000

// scheduleBaselineLabelMaxLen は版の label の上限（9.17.3。DbDesign.md 6.32 の CHECK と同じ）。
const scheduleBaselineLabelMaxLen = 100

// timelineSettings は project.settings のうちタイムラインが読む2キー（DbDesign.md 6.4）。
type timelineSettings struct {
	// SnapSeconds は timeline_snap_seconds。連鎖の最小の長さ m（9.17.1）。
	SnapSeconds int64
	// Epics は timeline_epics。**無い・配列でないときは空**。整数でない要素は読み飛ばす。
	// エピックでない番号・無い番号はここでは落とさない（SQL が type='epic' で落とす）。
	Epics []int32
}

// readTimelineSettings は settings を読む（DbDesign.md 6.4 の「無い・知らない値のとき」の規則）。
//
// **サーバは settings を保存するときに検証しない**（ApiDesign.md 5.5、DbDesign.md 6.4「検証は
// フロントのみ」）。そのかわり読むときに既定へ倒す——知らない値が入っていても要求は失敗させない。
func readTimelineSettings(raw []byte) timelineSettings {
	out := timelineSettings{SnapSeconds: timelineSnapDefault, Epics: []int32{}}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return out
	}
	if v, ok := obj["timeline_snap_seconds"]; ok {
		var f float64
		if json.Unmarshal(v, &f) == nil && f == math.Trunc(f) && slices.Contains(timelineSnapChoices, int64(f)) {
			out.SnapSeconds = int64(f)
		}
	}
	if v, ok := obj["timeline_epics"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(v, &items) == nil {
			for _, it := range items {
				var f float64
				if json.Unmarshal(it, &f) != nil || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
					continue
				}
				if n := int32(f); !slices.Contains(out.Epics, n) {
					out.Epics = append(out.Epics, n)
				}
			}
		}
	}
	return out
}

// timelineTicketItem は view=timeline の items[] の1件（9.2.7）。9.2.2 の形に3項目を足す。
type timelineTicketItem struct {
	ticketListItem
	// EpicSeq は所属エピックの番号。**選んだエピックの配下でなければ載らない**（連鎖のずらしの
	// 応答の items で、列の外のきっかけを返すときだけ起こる）。
	EpicSeq *int32 `json:"epic_seq,omitempty"`
	// StartedAt は実際の着手（9.2.1「着手日時を導く」）。着手していなければ null。
	StartedAt *Time `json:"started_at"`
	// LastChangedBy は activity の最新の行の実行者（記録時の表示名）。無ければ null。
	LastChangedBy *actorRef `json:"last_changed_by"`
}

// timelineEpicItem は view=timeline の epics[]（9.2.7）。
type timelineEpicItem struct {
	Seq      int32            `json:"seq"`
	Title    string           `json:"title"`
	Status   ticketStatusView `json:"status"`
	SortKey  *string          `json:"sort_key"`
	ClosedAt *Time            `json:"closed_at"`
}

// timelineBaselines は view=timeline の baselines（9.2.7）。版の中身は 9.17.3 で別に取る。
type timelineBaselines struct {
	Count           int32   `json:"count"`
	LatestID        *string `json:"latest_id"`
	LatestCreatedAt *Time   `json:"latest_created_at"`
}

// timelineList は view=timeline の応答（9.2.7）。
type timelineList struct {
	List[timelineTicketItem]
	Epics     []timelineEpicItem `json:"epics"`
	Links     []ganttLinkItem    `json:"links"`
	Baselines timelineBaselines  `json:"baselines"`
}

// timelineItemsFor は一覧の行に epic_seq・started_at・last_changed_by を足す（9.2.7）。
// 着手と最終変更者は activity から1本で引く（行ごとに引かない。設計方針3）。
func timelineItemsFor(
	ctx context.Context, q gen.Querier, rows []gen.ListTicketsRow, items []ticketListItem,
) ([]timelineTicketItem, error) {
	out := make([]timelineTicketItem, 0, len(items))
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	facts, err := q.ListTimelineActivityFacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("着手日時と最終変更者を読めない: %w", err)
	}
	started := map[string]time.Time{}
	last := map[string]*actorRef{}
	for _, f := range facts {
		switch f.Fact {
		case "started":
			started[f.TicketID] = f.At
		case "last":
			last[f.TicketID] = actorRefOf(f.ActorID, f.ActorKind, f.ActorName)
		}
	}
	for i, it := range items {
		ti := timelineTicketItem{ticketListItem: it, LastChangedBy: last[it.ID]}
		if s, ok := started[it.ID]; ok {
			ti.StartedAt = apiTime(&s)
		}
		if i < len(rows) && rows[i].TimelineEpicSeq.Valid {
			n := rows[i].TimelineEpicSeq.Int32
			ti.EpicSeq = &n
		}
		out = append(out, ti)
	}
	return out, nil
}

// writeTimelineList は view=timeline の応答を書く（9.2.7）。一覧の問い合わせ（チケット・タグ・依存）は
// listTickets が済ませてあり、ここではエピック・着手と最終変更者・版の集計を足す。
func (h *handler) writeTimelineList(
	w http.ResponseWriter, r *http.Request, projectID string, filters ticketFilters, page Page,
	rows []gen.ListTicketsRow, items []ticketListItem, total int64, baseETag string,
	links []ganttLinkItem, project gen.GetProjectTimelineSettingsRow, settings timelineSettings,
) {
	ctx := r.Context()

	// links[] は FS だけ、両端が items に含まれ、同じエピックに属するもの（9.2.7）
	epicOf := map[int32]int32{}
	for _, row := range rows {
		if row.TimelineEpicSeq.Valid {
			epicOf[row.Seq] = row.TimelineEpicSeq.Int32
		}
	}
	fsLinks := []ganttLinkItem{}
	for _, l := range links {
		se, ok1 := epicOf[l.SourceSeq]
		te, ok2 := epicOf[l.TargetSeq]
		if l.LinkType == "FS" && ok1 && ok2 && se == te {
			fsLinks = append(fsLinks, l)
		}
	}

	epicRows, err := h.q.ListTimelineEpics(ctx, gen.ListTimelineEpicsParams{
		ProjectID: projectID, EpicSeqs: settings.Epics, ParentSeqs: filters.parentSeqs,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("タイムラインのエピックを読めない: %w", err)))
		return
	}
	epics := make([]timelineEpicItem, 0, len(epicRows))
	var epicsUpdated time.Time
	for _, e := range epicRows {
		epics = append(epics, timelineEpicItem{
			Seq: e.Seq, Title: e.Title, Status: statusView(e.StatusKey, e.StatusName, e.StatusCategory),
			SortKey: textPtr(e.SortKey), ClosedAt: apiTime(e.ClosedAt),
		})
		if e.UpdatedAt.After(epicsUpdated) {
			epicsUpdated = e.UpdatedAt
		}
	}

	baselines, latestID, err := timelineBaselineSummary(ctx, h.q, projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	tlItems, err := timelineItemsFor(ctx, h.q, rows, items)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// ETag（9.2.5）。⑥エピックの件数と MAX(updated_at) ⑦版の件数と最新の id ⑧プロジェクトの version
	etag := strings.TrimSuffix(baseETag, `"`) + fmt.Sprintf(`-tl-%d-%d-%d-%s-%d"`,
		len(epics), etagStamp(epicsUpdated), baselines.Count, latestID, project.Version)
	if writeNotModified(w, r, etag) {
		return
	}
	w.Header().Set("ETag", etag)
	WriteJSON(w, http.StatusOK, timelineList{
		List: NewList(tlItems, page, int(total)), Epics: epics, Links: fsLinks, Baselines: baselines,
	})
}

// timelineBaselineSummary は baselines（9.2.7）と、ETag に混ぜる最新の版の id を返す。
func timelineBaselineSummary(ctx context.Context, q gen.Querier, projectID string) (timelineBaselines, string, error) {
	count, err := q.CountScheduleBaselines(ctx, projectID)
	if err != nil {
		return timelineBaselines{}, "", fmt.Errorf("当初計画の版を数えられない: %w", err)
	}
	out := timelineBaselines{Count: count}
	latest, err := q.GetLatestScheduleBaseline(ctx, projectID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return out, "", nil
	case err != nil:
		return timelineBaselines{}, "", fmt.Errorf("最新の当初計画の版を読めない: %w", err)
	}
	id := latest.ID
	out.LatestID = &id
	out.LatestCreatedAt = apiTime(&latest.CreatedAt)
	return out, id, nil
}

// writeNotModified は If-None-Match が etag と一致すれば 304 を書いて true を返す（2.7・9.2.5）。
//
// **弱い比較**（RFC 9110 8.8.3.2）。W/ の有無を無視して引用符の中身を比べる。`*` は何にでも一致する。
func writeNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	if !etagMatches(r, etag) {
		return false
	}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusNotModified)
	return true
}

func etagMatches(r *http.Request, etag string) bool {
	want := strings.TrimPrefix(etag, "W/")
	for _, header := range r.Header.Values("If-None-Match") {
		for _, part := range strings.Split(header, ",") {
			part = strings.TrimSpace(part)
			if part == "*" || (part != "" && strings.TrimPrefix(part, "W/") == want) {
				return true
			}
		}
	}
	return false
}

// ── 当初計画の版（ApiDesign.md 9.17.3、DbDesign.md 6.32）─────────────

// scheduleBaselineSummary は版の一覧の1行（9.17.3）。保存の応答も同じ形。
type scheduleBaselineSummary struct {
	ID        string                 `json:"id"`
	Label     *string                `json:"label"`
	ItemCount int32                  `json:"item_count"`
	CreatedBy scheduleBaselineAuthor `json:"created_by"`
	CreatedAt Time                   `json:"created_at"`
}

// scheduleBaselineAuthor は保存した人。消えていれば id が null で、表示は記録時の名前。
type scheduleBaselineAuthor struct {
	ID          *string `json:"id"`
	DisplayName string  `json:"display_name"`
}

type scheduleBaselineItem struct {
	TicketSeq   int32  `json:"ticket_seq"`
	Exists      bool   `json:"exists"`
	Title       string `json:"title"`
	EpicSeq     int32  `json:"epic_seq"`
	EpicTitle   string `json:"epic_title"`
	StartAt     *Time  `json:"start_at"`
	DueAt       *Time  `json:"due_at"`
	AllDay      bool   `json:"all_day"`
	NoOverlap   bool   `json:"no_overlap"`
	StartPinned bool   `json:"start_pinned"`
	DuePinned   bool   `json:"due_pinned"`
}

type scheduleBaselineDetail struct {
	scheduleBaselineSummary
	Items []scheduleBaselineItem `json:"items"`
}

func baselineSummaryOf(id string, label pgtype.Text, count int32, createdBy pgtype.Text, createdByName string, createdAt time.Time) scheduleBaselineSummary {
	return scheduleBaselineSummary{
		ID: id, Label: textPtr(label), ItemCount: count,
		CreatedBy: scheduleBaselineAuthor{ID: textPtr(createdBy), DisplayName: createdByName},
		CreatedAt: Time(createdAt),
	}
}

func scheduleBaselineNotFound() *apierr.Error {
	return apierr.New(apierr.NotFound).WithMessage("当初計画の版が見つかりません。消された可能性があります")
}

// createScheduleBaseline は POST /projects/{key}/schedule-baselines（9.17.3）。必要権限は project.edit。
//
// **写すチケットは view=timeline が絞り込みなしで返す集合と同じ**（DbDesign.md 6.32）。If-Match は
// 要求しない（追加するだけで、上書きして失われるものが無い）。5,000件を超えれば切り詰めずに 422。
func (h *handler) createScheduleBaseline(w http.ResponseWriter, r *http.Request) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/schedule-baselines")
	if !ok {
		return
	}
	label, e := parseScheduleBaselineLabel(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	id := ulidgen.New()
	var (
		summary  scheduleBaselineSummary
		writeErr *apierr.Error
	)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		project, err := q.GetProjectTimelineSettings(ctx, projectID)
		if err != nil {
			return fmt.Errorf("プロジェクトの設定を読めない: %w", err)
		}
		settings := readTimelineSettings(project.Settings)
		count, err := q.CountTimelineTickets(ctx, gen.CountTimelineTicketsParams{ProjectID: projectID, EpicSeqs: settings.Epics})
		if err != nil {
			return fmt.Errorf("版に写すチケットを数えられない: %w", err)
		}
		if count > scheduleBaselineMaxItems {
			writeErr = scheduleBaselineTooMany(int64(count))
			return errTicketReference
		}
		createdAt, err := q.CreateScheduleBaseline(ctx, gen.CreateScheduleBaselineParams{
			ID: id, ProjectID: projectID, Label: label, ItemCount: count,
			CreatedBy: pgtype.Text{String: p.ActorID, Valid: p.ActorID != ""}, CreatedByName: p.DisplayName,
		})
		if err != nil {
			return fmt.Errorf("当初計画の版を作れない: %w", err)
		}
		copied, err := q.CopyTimelineToBaseline(ctx, gen.CopyTimelineToBaselineParams{
			ProjectID: projectID, EpicSeqs: settings.Epics, BaselineID: id,
		})
		if err != nil {
			return fmt.Errorf("当初計画の版にチケットを写せない: %w", err)
		}
		// 数えてから写すまでに他の要求がチケットを足した・消したとき（同じトランザクションでも
		// READ COMMITTED では起こりうる）は、写した件数を正にする
		if copied != int64(count) {
			if copied > scheduleBaselineMaxItems {
				writeErr = scheduleBaselineTooMany(copied)
				return errTicketReference
			}
			if err := q.SetScheduleBaselineItemCount(ctx, gen.SetScheduleBaselineItemCountParams{ID: id, ItemCount: int32(copied)}); err != nil {
				return fmt.Errorf("当初計画の版の件数を直せない: %w", err)
			}
			count = int32(copied)
		}
		summary = baselineSummaryOf(id, label, count, pgtype.Text{String: p.ActorID, Valid: p.ActorID != ""}, p.DisplayName, createdAt)
		return nil
	})
	switch {
	case writeErr != nil:
		apierr.Write(w, r, writeErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+key+"/schedule-baselines/"+id)
	WriteJSON(w, http.StatusCreated, summary)
}

func scheduleBaselineTooMany(n int64) *apierr.Error {
	return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "timeline_epics", Code: "too_many",
		Message: fmt.Sprintf("タイムラインのチケットが %d件あり、版に保存できる上限（%d件）を超えています。載せるエピックを減らしてください",
			n, scheduleBaselineMaxItems),
	})
}

// parseScheduleBaselineLabel は本文の label を読む（9.17.3）。本文は省略できる。
// 前後の空白を除いて 1〜100文字。空・省略は null。
func parseScheduleBaselineLabel(r *http.Request) (pgtype.Text, *apierr.Error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(r.Body, maxRequestBodyBytes)); err != nil {
		return pgtype.Text{}, apierr.New(apierr.BadRequest).WithCause(fmt.Errorf("本文を読めない: %w", err))
	}
	if len(bytes.TrimSpace(buf.Bytes())) == 0 {
		return pgtype.Text{}, nil
	}
	var req struct {
		Label json.RawMessage `json:"label"`
	}
	if err := json.Unmarshal(buf.Bytes(), &req); err != nil {
		return pgtype.Text{}, apierr.New(apierr.BadRequest).WithCause(fmt.Errorf("リクエスト本文を JSON として読めない: %w", err))
	}
	s, isNull, err := decodeOptionalString(req.Label)
	if len(req.Label) == 0 || isNull {
		return pgtype.Text{}, nil
	}
	if err != nil {
		return pgtype.Text{}, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "label", Code: "invalid", Message: "名前は文字列で指定してください",
		})
	}
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return pgtype.Text{}, nil
	case n > scheduleBaselineLabelMaxLen:
		return pgtype.Text{}, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "label", Code: "too_long",
			Message: fmt.Sprintf("名前は%d文字以内で入力してください", scheduleBaselineLabelMaxLen),
		})
	}
	return pgtype.Text{String: s, Valid: true}, nil
}

// listScheduleBaselines は GET /projects/{key}/schedule-baselines（9.17.3）。ページネーションも ETag も持たない。
func (h *handler) listScheduleBaselines(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/schedule-baselines")
	if !ok {
		return
	}
	rows, err := h.q.ListScheduleBaselines(r.Context(), projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("当初計画の版の一覧を読めない: %w", err)))
		return
	}
	items := make([]scheduleBaselineSummary, 0, len(rows))
	for _, b := range rows {
		items = append(items, baselineSummaryOf(b.ID, b.Label, b.ItemCount, b.CreatedBy, b.CreatedByName, b.CreatedAt))
	}
	WriteJSON(w, http.StatusOK, struct {
		Items []scheduleBaselineSummary `json:"items"`
	}{Items: items})
}

// getScheduleBaseline は GET /projects/{key}/schedule-baselines/{id}（9.17.3）。
// **版は書き換わらないので、ETag は id だけから作り、If-None-Match で 304**。
func (h *handler) getScheduleBaseline(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/schedule-baselines/{id}")
	if !ok {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	b, err := h.q.GetScheduleBaseline(ctx, gen.GetScheduleBaselineParams{ProjectID: projectID, ID: id})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		apierr.Write(w, r, scheduleBaselineNotFound())
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("当初計画の版を読めない: %w", err)))
		return
	}
	etag := `W/"sbl-` + b.ID + `"`
	if writeNotModified(w, r, etag) {
		return
	}
	rows, err := h.q.ListScheduleBaselineItems(ctx, gen.ListScheduleBaselineItemsParams{ProjectID: projectID, BaselineID: b.ID})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("当初計画の版の行を読めない: %w", err)))
		return
	}
	items := make([]scheduleBaselineItem, 0, len(rows))
	for _, it := range rows {
		items = append(items, scheduleBaselineItem{
			TicketSeq: it.TicketSeq, Exists: it.TicketExists, Title: it.Title,
			EpicSeq: it.EpicSeq, EpicTitle: it.EpicTitle,
			StartAt: apiTime(it.StartAt), DueAt: apiTime(it.DueAt), AllDay: it.AllDay,
			NoOverlap: it.NoOverlap, StartPinned: it.StartPinned, DuePinned: it.DuePinned,
		})
	}
	w.Header().Set("ETag", etag)
	WriteJSON(w, http.StatusOK, scheduleBaselineDetail{
		scheduleBaselineSummary: baselineSummaryOf(b.ID, b.Label, b.ItemCount, b.CreatedBy, b.CreatedByName, b.CreatedAt),
		Items:                   items,
	})
}

// deleteScheduleBaseline は DELETE /projects/{key}/schedule-baselines/{id}（9.17.3）。必要権限は project.edit。
// 版とその行を物理削除する。If-Match は要求しない。activity にも監査ログにも記録しない（DbDesign.md 6.32）。
func (h *handler) deleteScheduleBaseline(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "DELETE /projects/{key}/schedule-baselines/{id}")
	if !ok {
		return
	}
	n, err := h.q.DeleteScheduleBaseline(r.Context(), gen.DeleteScheduleBaselineParams{ProjectID: projectID, ID: chi.URLParam(r, "id")})
	switch {
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("当初計画の版を消せない: %w", err)))
		return
	case n == 0:
		apierr.Write(w, r, scheduleBaselineNotFound())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
