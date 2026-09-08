// スプリントAPI（ApiDesign.md 9.12）。
//
//	GET    /api/v1/projects/{key}/sprints        ticket.view
//	POST   /api/v1/projects/{key}/sprints        project.edit
//	PATCH  /api/v1/projects/{key}/sprints/{id}   project.edit
//	DELETE /api/v1/projects/{key}/sprints/{id}   project.edit
//
// **Phase 1 で開けるのは定義だけである。** バーンダウン・ベロシティを含む
// スプリント管理画面は Phase 2 の /p/:key/sprints（GuiDesign.md 10章）で、
// ここはプロジェクト設定のスプリントタブ（同 5.9.5）が消費者になる。
//
// **定義を Phase 1 に置く理由。** sprint 表は Phase 1（DbDesign.md 6.9）にあり、
// チケット詳細のサイドバー（GuiDesign.md 5.5）もスプリント欄を Phase 1 として
// 並べている。作る手段が無いと、常に空のドロップダウンになる。
//
// **audit_log にも activity にも記録しない**（9.1.1）。理由はタグと同じ。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// スプリントの制約（ApiDesign.md 9.12、DbDesign.md 6.9）。
const (
	sprintNameMaxLen = 50

	sprintStatusPlanned   = "planned"
	sprintStatusActive    = "active"
	sprintStatusCompleted = "completed"
)

// sprintStatuses は status の値域（DbDesign.md 6.9 の CHECK と同じ3値）。
var sprintStatuses = []string{sprintStatusPlanned, sprintStatusActive, sprintStatusCompleted}

// sprintView は 9.12 が返す1行。
type sprintView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Goal        *string `json:"goal"`
	StartDate   *Date   `json:"start_date"`
	EndDate     *Date   `json:"end_date"`
	Status      string  `json:"status"`
	TicketCount int64   `json:"ticket_count"`
	ClosedCount int64   `json:"closed_count"`
}

// sprintListView は 9.12 の一覧応答。タグ（9.11）と同じくページャも ETag も持たない。
type sprintListView struct {
	Items []sprintView `json:"items"`
}

type createSprintRequest struct {
	Name      string  `json:"name"`
	Goal      *string `json:"goal"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Status    *string `json:"status"`
}

// patchSprintRequest は「キーが無い／null／値」の3通りを見分ける必要がある
// 項目を json.RawMessage で受ける（projects_update.go の description と同じ型）。
type patchSprintRequest struct {
	Name      *string         `json:"name"`
	Goal      json.RawMessage `json:"goal"`
	StartDate json.RawMessage `json:"start_date"`
	EndDate   json.RawMessage `json:"end_date"`
	Status    *string         `json:"status"`
}

// ── GET /api/v1/projects/{key}/sprints ──────────────────────

// listSprints は start_date 降順（NULL は末尾）・同値は created_at 降順で返す。
func (h *handler) listSprints(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/sprints")
	if !ok {
		return
	}

	rows, err := h.q.ListSprintsByProject(r.Context(), projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリント一覧を読めない: %w", err)))
		return
	}

	items := make([]sprintView, 0, len(rows))
	for _, row := range rows {
		items = append(items, sprintView{
			ID:          row.ID,
			Name:        row.Name,
			Goal:        textPtr(row.Goal),
			StartDate:   apiDate(row.StartDate),
			EndDate:     apiDate(row.EndDate),
			Status:      row.Status,
			TicketCount: row.TicketCount,
			ClosedCount: row.ClosedCount,
		})
	}
	WriteJSON(w, http.StatusOK, sprintListView{Items: items})
}

// ── POST /api/v1/projects/{key}/sprints ─────────────────────

// createSprint はスプリントを1件作る。201 + Location + 作った1件（B-2）。
//
// **名前に一意制約が無い**（DbDesign.md 6.9）。同名のスプリントを作れる。
func (h *handler) createSprint(w http.ResponseWriter, r *http.Request) {
	_, key, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/sprints")
	if !ok {
		return
	}

	var req createSprintRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var details []apierr.Detail
	name := validateSprintName(req.Name, &details)
	start := parseSprintDateField("start_date", req.StartDate, &details)
	end := parseSprintDateField("end_date", req.EndDate, &details)
	status := sprintStatusPlanned
	if req.Status != nil {
		status = validateSprintStatus(*req.Status, &details)
	}
	validateSprintDateOrder(start, end, &details)
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	goal := pgtype.Text{}
	if req.Goal != nil {
		goal = optionalText(strings.TrimSpace(*req.Goal))
	}

	ctx := r.Context()
	id := ulidgen.New()
	if err := h.q.CreateSprint(ctx, gen.CreateSprintParams{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		Goal:      goal,
		StartDate: start,
		EndDate:   end,
		Status:    status,
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリントを作成できない: %w", err)))
		return
	}

	view, err := h.sprintByID(ctx, projectID, id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("作成したスプリントを読めない: %w", err)))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/sprints/%s", key, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── PATCH /api/v1/projects/{key}/sprints/{id} ───────────────

// patchSprint はスプリントの各項目を変える。If-Match は要求しない（2.8）。
//
// **更新前に現在の行を読む。** ck_sprint_dates（DbDesign.md 6.9）は更新後の
// 2列の関係を見る制約であり、片方だけを送る PATCH では「送られなかった側の
// 現在値」と突き合わせないと判定できない。DB の CHECK 違反に任せると 500 に
// なるので、ここで 422 に倒す。
func (h *handler) patchSprint(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "PATCH /projects/{key}/sprints/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var req patchSprintRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	ctx := r.Context()
	current, err := h.q.GetSprintByID(ctx, gen.GetSprintByIDParams{ProjectID: projectID, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeSprintNotFound(w, r, id)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリント %q を読めない: %w", id, err)))
		return
	}

	var details []apierr.Detail
	params := gen.UpdateSprintParams{ProjectID: projectID, ID: id}

	if req.Name != nil {
		if name := validateSprintName(*req.Name, &details); name != "" {
			params.Name = pgtype.Text{String: name, Valid: true}
		}
	}
	if req.Status != nil {
		if st := validateSprintStatus(*req.Status, &details); st != "" {
			params.Status = pgtype.Text{String: st, Valid: true}
		}
	}
	if req.Goal != nil {
		if g := parseSprintGoalField(req.Goal, &details); g != nil {
			params.SetGoal = true
			params.Goal = *g
		}
	}

	// 更新後に効く日付を組み立てる。送られていない側は現在値を使う。
	effectiveStart, effectiveEnd := current.StartDate, current.EndDate
	if req.StartDate != nil {
		if d := parseSprintDateRawField("start_date", req.StartDate, &details); d != nil {
			params.SetStartDate = true
			params.StartDate = *d
			effectiveStart = *d
		}
	}
	if req.EndDate != nil {
		if d := parseSprintDateRawField("end_date", req.EndDate, &details); d != nil {
			params.SetEndDate = true
			params.EndDate = *d
			effectiveEnd = *d
		}
	}
	validateSprintDateOrder(effectiveStart, effectiveEnd, &details)

	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	rows, err := h.q.UpdateSprint(ctx, params)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリント %q を更新できない: %w", id, err)))
		return
	}
	if rows == 0 {
		// 読んだ直後に他者が消した場合だけここへ来る。
		writeSprintNotFound(w, r, id)
		return
	}

	view, err := h.sprintByID(ctx, projectID, id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("更新したスプリントを読めない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── DELETE /api/v1/projects/{key}/sprints/{id} ──────────────

// deleteSprint はスプリントを消す。
//
// **チケットは消えない。** fk_ticket_sprint の ON DELETE SET NULL により
// ticket.sprint_id が外れ、スプリント未設定に戻る（DbDesign.md 6.9）。
// 画面は確認ダイアログにその旨を明記する（GuiDesign.md 6.3）。
func (h *handler) deleteSprint(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "DELETE /projects/{key}/sprints/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	rows, err := h.q.DeleteSprint(r.Context(), gen.DeleteSprintParams{ProjectID: projectID, ID: id})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("スプリント %q を削除できない: %w", id, err)))
		return
	}
	if rows == 0 {
		writeSprintNotFound(w, r, id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 補助 ────────────────────────────────────────────────────

// sprintByID は応答用に1件を読み直す（ticket_count / closed_count のため）。
func (h *handler) sprintByID(ctx context.Context, projectID, id string) (sprintView, error) {
	return sprintByIDWith(ctx, h.q, projectID, id)
}

// sprintByIDWith は sprintByID の Querier を差し替えられる形（pb-6）。
//
// **トランザクションの中から呼ぶために分けた。** 9.12.1 / 9.12.2 は書き込みと
// 同じトランザクションで応答用の行を読む必要があり、h.q（プール）で読むと
// **まだコミットされていない自分の書き込みが見えない。**
func sprintByIDWith(
	ctx context.Context, q gen.Querier, projectID, id string,
) (sprintView, error) {
	row, err := q.GetSprintByID(ctx, gen.GetSprintByIDParams{ProjectID: projectID, ID: id})
	if err != nil {
		return sprintView{}, err
	}
	return sprintView{
		ID:          row.ID,
		Name:        row.Name,
		Goal:        textPtr(row.Goal),
		StartDate:   apiDate(row.StartDate),
		EndDate:     apiDate(row.EndDate),
		Status:      row.Status,
		TicketCount: row.TicketCount,
		ClosedCount: row.ClosedCount,
	}, nil
}

// validateSprintName は 9.12 の name を検証する。誤りは details へ足し、"" を返す。
func validateSprintName(raw string, details *[]apierr.Detail) string {
	name := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		*details = append(*details, apierr.Detail{
			Field: "name", Code: "required",
			Message: "スプリント名を入力してください",
		})
		return ""
	case n > sprintNameMaxLen:
		*details = append(*details, apierr.Detail{
			Field: "name", Code: "too_long",
			Message: fmt.Sprintf("スプリント名は%d文字以内で入力してください", sprintNameMaxLen),
		})
		return ""
	}
	return name
}

// validateSprintStatus は status の値域を見る（DbDesign.md 6.9 の CHECK と同じ3値）。
func validateSprintStatus(raw string, details *[]apierr.Detail) string {
	for _, s := range sprintStatuses {
		if raw == s {
			return raw
		}
	}
	*details = append(*details, apierr.Detail{
		Field: "status", Code: "invalid",
		Message: fmt.Sprintf("状態は %s のいずれかで指定してください",
			strings.Join(sprintStatuses, " / ")),
	})
	return ""
}

// parseSprintDateField は POST の日付（*string）を date 列へ写す。
func parseSprintDateField(field string, raw *string, details *[]apierr.Detail) pgtype.Date {
	if raw == nil {
		return pgtype.Date{}
	}
	d, ok := parseAPIDate(strings.TrimSpace(*raw))
	if !ok {
		*details = append(*details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日付は YYYY-MM-DD の形式で指定してください",
		})
		return pgtype.Date{}
	}
	return d
}

// parseSprintDateRawField は PATCH の日付を読む。null は「消す」を意味する。
//
// 呼び出し側が req.X != nil を確かめてから呼ぶ（キーが無い場合は据え置きで、
// ここへは来ない）。
func parseSprintDateRawField(
	field string, raw json.RawMessage, details *[]apierr.Detail,
) *pgtype.Date {
	if string(raw) == "null" {
		return &pgtype.Date{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		*details = append(*details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日付は文字列で指定してください",
		})
		return nil
	}
	d, ok := parseAPIDate(strings.TrimSpace(s))
	if !ok {
		*details = append(*details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日付は YYYY-MM-DD の形式で指定してください",
		})
		return nil
	}
	return &d
}

// parseSprintGoalField は PATCH の goal を読む。null と空文字はどちらも NULL。
func parseSprintGoalField(raw json.RawMessage, details *[]apierr.Detail) *pgtype.Text {
	if string(raw) == "null" {
		return &pgtype.Text{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		*details = append(*details, apierr.Detail{
			Field: "goal", Code: "invalid",
			Message: "ゴールは文字列で指定してください",
		})
		return nil
	}
	t := optionalText(strings.TrimSpace(s))
	return &t
}

// validateSprintDateOrder は ck_sprint_dates と同じ関係を見る
// （両方あるとき start_date <= end_date。DbDesign.md 6.9）。
func validateSprintDateOrder(start, end pgtype.Date, details *[]apierr.Detail) {
	if !start.Valid || !end.Valid {
		return
	}
	if start.Time.After(end.Time) {
		*details = append(*details, apierr.Detail{
			Field: "end_date", Code: "invalid",
			Message: "終了日は開始日以降の日付を指定してください",
		})
	}
}

// writeSprintNotFound は「そのプロジェクトに無いスプリント」への応答。
// 他プロジェクトの ID も 404 に寄せる（Design.md 6.4.5）。
func writeSprintNotFound(w http.ResponseWriter, r *http.Request, id string) {
	apierr.Write(w, r, apierr.New(apierr.NotFound).
		WithMessage("スプリントが見つかりません").
		WithCause(fmt.Errorf("スプリント %q が見つからない", id)))
}
