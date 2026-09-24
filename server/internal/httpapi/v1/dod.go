// チケットの完了条件（DoD）（ApiDesign.md 9.9）。手順18a。
//
//	GET    /api/v1/projects/{key}/tickets/{seq}/dod        ticket.view
//	POST   /api/v1/projects/{key}/tickets/{seq}/dod        ticket.edit
//	PATCH  /api/v1/projects/{key}/tickets/{seq}/dod/{id}   ticket.edit
//	DELETE /api/v1/projects/{key}/tickets/{seq}/dod/{id}   ticket.edit
//
// **このチケットを「終わった」と言うための条件の一覧である。** 人が列挙して
// チェックし、将来エージェントが assertion / artifact により自動判定する
// 土台になる（Requirements.md 10.5.2）。
//
// **受け付ける type は manual だけである**（9.9）。表と CHECK は
// 他の型も入る形で作ってあり（DbDesign.md 6.11）、絞るのは API の仕事——
// 後から列を足すより、使わない列を持つほうが安い。
//
// **満たしていなくても done への遷移は止めない**（9.9）。9.6 の検証の順序に
// DoD は含まれず、チェックリストは人が読む道具である。判定できない
// 型で遷移を止めると、人が自分のチェック漏れで進めなくなるだけになる。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// dodBodyMaxLen は完了条件の文の上限。
//
// **9.9 は「必須。完了条件の文」としか定めていない。** 上限を置くのは、
// activity の要約がこの文をそのまま載せるためで（9.9）、1件で履歴の1行を
// 埋め尽くさない長さにしてある。
const dodBodyMaxLen = 500

// dodTypeManual は受け付ける唯一の type（9.9）。
const dodTypeManual = "manual"

// dodUnsupportedTypes は DbDesign.md 6.11 の CHECK が許すが、まだ開けない型。
//
// **値そのものを持っておく**のは、`422 unsupported_type` と「綴りが違う」を
// 区別して返すためである——`asertion` と書いた人には「正しくありません」、
// `assertion` と書いた人には「まだ使えません」と伝わるほうが速い。
var dodUnsupportedTypes = []string{"task_ref", "assertion", "artifact", "review"}

// errDoDHandled は RunInTx を巻き戻さずに抜けるための番人。
var errDoDHandled = errors.New("dod: handled")

// dodView は 9.9 が返す1行。
//
// **config / evidence / origin は持たない**（9.9）。いずれも manual 以外の型と
// AI提案のためのもので、API が受け付けない値を応答に並べると
// 「使える」ように見える。列は DbDesign.md 6.11 のまま残っている。
type dodView struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Body        string    `json:"body"`
	IsSatisfied bool      `json:"is_satisfied"`
	SatisfiedAt *Time     `json:"satisfied_at"`
	SatisfiedBy *actorRef `json:"satisfied_by"`
	SortOrder   int32     `json:"sort_order"`
	CreatedAt   Time      `json:"created_at"`
	UpdatedAt   Time      `json:"updated_at"`
}

// dodListView は 9.9 の一覧応答。
//
// **page / per_page / total を持たない**（9.9）。1チケットあたり数件に収まり、
// 詳細応答（9.5.1）の dod に同じ一覧が入る。
type dodListView struct {
	Items []dodView `json:"items"`
}

// createDoDRequest は POST の本文（9.9）。
type createDoDRequest struct {
	Type        *string `json:"type"`
	Body        string  `json:"body"`
	IsSatisfied *bool   `json:"is_satisfied"`
	SortOrder   *int32  `json:"sort_order"`
}

// dodPatch は PATCH を解いたあとの更新内容。Set が false の項目は触らない。
type dodPatch struct {
	Body        optional[string]
	IsSatisfied optional[bool]
	SortOrder   optional[int32]
}

// ── GET /api/v1/projects/{key}/tickets/{seq}/dod ─────────────

// listTicketDoD はチケットの完了条件を返す（9.9）。
func (h *handler) listTicketDoD(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r,
		"GET /projects/{key}/tickets/{seq}/dod")
	if !ok {
		return
	}

	items, err := ticketDoDFor(ctx, h.q, ticketID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, dodListView{Items: items})
}

// ── POST /api/v1/projects/{key}/tickets/{seq}/dod ────────────

// createDoDItem は完了条件を1件足す。201 + Location + 作った1件。
func (h *handler) createDoDItem(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/dod")
	if !ok {
		return
	}

	var req createDoDRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	// **ticket.self_edit では is_satisfied を送れない**（9.9。0029）。
	if req.IsSatisfied != nil {
		if e := denyDoDSatisfied(r, scope.key); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}
	body, satisfied, e := validateNewDoD(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var view dodView

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// sort_order 省略時は末尾（現在の最大値 + 10。9.9）。
		var sortOrder int32
		if req.SortOrder != nil {
			sortOrder = *req.SortOrder
		} else {
			next, err := q.NextDoDSortOrder(ctx, ticketID)
			if err != nil {
				return fmt.Errorf("完了条件の並び順を決められない: %w", err)
			}
			sortOrder = next
		}

		if err := q.CreateDoDItem(ctx, gen.CreateDoDItemParams{
			ID:          id,
			TicketID:    ticketID,
			Type:        dodTypeManual,
			Body:        body,
			SortOrder:   sortOrder,
			IsSatisfied: satisfied,
		}); err != nil {
			return fmt.Errorf("完了条件を作成できない: %w", err)
		}
		// **作成時に is_satisfied=true で来たら、満たした人も同時に記録する。**
		// 9.9 は PATCH について定めているが、作成でも「満たしたのに満たした人が
		// いない」行を作らないほうが一貫する。
		if satisfied {
			if _, err := q.UpdateDoDItem(ctx, gen.UpdateDoDItemParams{
				TicketID:     ticketID,
				ID:           id,
				SatisfiedSet: true,
				IsSatisfied:  true,
				SatisfiedBy:  pgtype.Text{String: scope.actorID, Valid: scope.actorID != ""},
			}); err != nil {
				return fmt.Errorf("完了条件の充足者を記録できない: %w", err)
			}
		}

		row, err := q.GetTicketDoDItem(ctx, gen.GetTicketDoDItemParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("作成した完了条件を読めない: %w", err)
		}
		view = buildDoDView(dodRow(row))

		return recordDoDChange(ctx, q, rec, scope.projectID, ticketID,
			nil, dodSummaryOf(view))
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/dod/%s", scope.key, scope.seq, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── PATCH /api/v1/projects/{key}/tickets/{seq}/dod/{id} ──────

// patchDoDItem は完了条件を部分更新する（9.9）。
//
// **is_satisfied を true にしたときサーバが satisfied_at と satisfied_by を
// 設定し、false に戻すと両方 NULL へ戻す**（9.9）。3つはクエリ側で束ねてあり、
// 「満たしたのに満たした人がいない」行を作れないようにしてある。
func (h *handler) patchDoDItem(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"PATCH /projects/{key}/tickets/{seq}/dod/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var raw map[string]json.RawMessage
	if e := decodeJSON(r, &raw); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if _, ok := raw["is_satisfied"]; ok {
		if e := denyDoDSatisfied(r, scope.key); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}
	patch, e := parseDoDPatch(raw)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	var (
		view     dodView
		notFound bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		before, err := q.GetTicketDoDItem(ctx, gen.GetTicketDoDItemParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errDoDHandled
			}
			return fmt.Errorf("完了条件 %q を読めない: %w", id, err)
		}

		params := gen.UpdateDoDItemParams{TicketID: ticketID, ID: id}
		if patch.Body.Set {
			params.Body = pgtype.Text{String: patch.Body.Value, Valid: true}
		}
		if patch.SortOrder.Set {
			params.SortOrder = pgtype.Int4{Int32: patch.SortOrder.Value, Valid: true}
		}
		if patch.IsSatisfied.Set {
			params.SatisfiedSet = true
			params.IsSatisfied = patch.IsSatisfied.Value
			params.SatisfiedBy = pgtype.Text{
				String: scope.actorID, Valid: scope.actorID != "",
			}
		}

		rows, err := q.UpdateDoDItem(ctx, params)
		if err != nil {
			return fmt.Errorf("完了条件 %q を更新できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errDoDHandled
		}

		after, err := q.GetTicketDoDItem(ctx, gen.GetTicketDoDItemParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("更新した完了条件を読めない: %w", err)
		}
		view = buildDoDView(dodRow(after))

		// **並び順だけの変更は記録しない**（9.9）。move（9.4）を記録しないのと
		// 同じ理由で、順序を入れ替えただけで変更履歴が埋まる。
		// 要約は body と充足状態からしか作らないので、比較するだけで足りる。
		oldSummary := dodSummaryOf(buildDoDView(dodRow(before)))
		newSummary := dodSummaryOf(view)
		if oldSummary == newSummary {
			return nil
		}
		return recordDoDChange(ctx, q, rec, scope.projectID, ticketID,
			&oldSummary, newSummary)
	})
	switch {
	case notFound:
		apierr.Write(w, r, dodNotFound(id))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── DELETE /api/v1/projects/{key}/tickets/{seq}/dod/{id} ─────

// deleteDoDItem は完了条件を1件消す。204（9.9）。
func (h *handler) deleteDoDItem(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"DELETE /projects/{key}/tickets/{seq}/dod/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	rec := activity.FromRequest(r)
	var notFound bool

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.GetTicketDoDItem(ctx, gen.GetTicketDoDItemParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errDoDHandled
			}
			return fmt.Errorf("完了条件 %q を読めない: %w", id, err)
		}
		view := buildDoDView(dodRow(row))

		rows, err := q.DeleteDoDItem(ctx, gen.DeleteDoDItemParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("完了条件 %q を削除できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errDoDHandled
		}

		oldSummary := dodSummaryOf(view)
		return recordDoDChange(ctx, q, rec, scope.projectID, ticketID,
			&oldSummary, "")
	})
	switch {
	case notFound:
		apierr.Write(w, r, dodNotFound(id))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 組み立てと検証 ───────────────────────────────────────────

// dodRow は List と Get の行を1つの形に揃える（references.go と同じ形）。
type dodRow gen.GetTicketDoDItemRow

// ticketDoDFor は1チケットぶんの完了条件を組み立てる。
//
// **詳細応答（9.5.1）と一覧（9.9）が同じ関数を通る。** 並び順の規則
// （sort_order → created_at）はクエリ側にあり、2か所で書き分けない。
func ticketDoDFor(
	ctx context.Context, q gen.Querier, ticketID string,
) ([]dodView, error) {
	rows, err := q.ListTicketDoD(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("完了条件を読めない: %w", err)
	}
	items := make([]dodView, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildDoDView(dodRow(row)))
	}
	return items, nil
}

func buildDoDView(row dodRow) dodView {
	return dodView{
		ID:          row.ID,
		Type:        row.Type,
		Body:        row.Body,
		IsSatisfied: row.IsSatisfied,
		SatisfiedAt: apiTimestamptz(row.SatisfiedAt),
		SatisfiedBy: actorRefOf(row.SatisfiedBy, row.SatisfiedByKind, row.SatisfiedByName),
		SortOrder:   row.SortOrder,
		CreatedAt:   Time(row.CreatedAt.Time),
		UpdatedAt:   Time(row.UpdatedAt.Time),
	}
}

// dodNotFound は「そのチケットに無い完了条件」への応答。
func dodNotFound(id string) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage("完了条件が見つかりません").
		WithCause(fmt.Errorf("完了条件 %q が見つからない", id))
}

// validateNewDoD は POST の本文を検証する（9.9）。
func validateNewDoD(req createDoDRequest) (body string, satisfied bool, apiErr *apierr.Error) {
	var details []apierr.Detail

	if req.Type != nil {
		t := strings.TrimSpace(*req.Type)
		switch {
		case t == dodTypeManual:
			// 明示の manual は受ける。
		case slices.Contains(dodUnsupportedTypes, t):
			details = append(details, apierr.Detail{
				Field: "type", Code: "unsupported_type",
				Message: "この種別は今後のバージョンで使えるようになります",
			})
		default:
			details = append(details, apierr.Detail{
				Field: "type", Code: "invalid", Message: "種別の指定が正しくありません",
			})
		}
	}

	body = strings.TrimSpace(req.Body)
	switch {
	case body == "":
		details = append(details, apierr.Detail{
			Field: "body", Code: "required", Message: "完了条件を入力してください",
		})
	case utf8.RuneCountInString(body) > dodBodyMaxLen:
		details = append(details, apierr.Detail{
			Field: "body", Code: "too_long",
			Message: fmt.Sprintf("完了条件は%d文字以内で入力してください", dodBodyMaxLen),
		})
	}

	if req.IsSatisfied != nil {
		satisfied = *req.IsSatisfied
	}

	if len(details) > 0 {
		return "", false, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return body, satisfied, nil
}

// parseDoDPatch は PATCH の本文を解く（9.9）。
//
// **type は送ると 422**（作成後は変えられない）。取りうる値が1つしか
// ない以上、変更を受け付けても何も起こせない。
func parseDoDPatch(raw map[string]json.RawMessage) (dodPatch, *apierr.Error) {
	var (
		patch   dodPatch
		details []apierr.Detail
	)

	if _, ok := raw["type"]; ok {
		details = append(details, apierr.Detail{
			Field: "type", Code: "immutable_field",
			Message: "種別は作成後に変更できません",
		})
	}

	if v, ok := raw["body"]; ok {
		var s string
		switch {
		case json.Unmarshal(v, &s) != nil:
			details = append(details, apierr.Detail{
				Field: "body", Code: "invalid", Message: "文字列で指定してください",
			})
		case strings.TrimSpace(s) == "":
			details = append(details, apierr.Detail{
				Field: "body", Code: "required", Message: "完了条件を入力してください",
			})
		case utf8.RuneCountInString(strings.TrimSpace(s)) > dodBodyMaxLen:
			details = append(details, apierr.Detail{
				Field: "body", Code: "too_long",
				Message: fmt.Sprintf("完了条件は%d文字以内で入力してください", dodBodyMaxLen),
			})
		default:
			patch.Body = optional[string]{Set: true, Value: strings.TrimSpace(s)}
		}
	}

	if v, ok := raw["is_satisfied"]; ok {
		var b bool
		if json.Unmarshal(v, &b) != nil {
			details = append(details, apierr.Detail{
				Field: "is_satisfied", Code: "invalid",
				Message: "true または false で指定してください",
			})
		} else {
			patch.IsSatisfied = optional[bool]{Set: true, Value: b}
		}
	}

	if v, ok := raw["sort_order"]; ok {
		var n int32
		if json.Unmarshal(v, &n) != nil {
			details = append(details, apierr.Detail{
				Field: "sort_order", Code: "invalid", Message: "整数で指定してください",
			})
		} else {
			patch.SortOrder = optional[int32]{Set: true, Value: n}
		}
	}

	if len(details) > 0 {
		return dodPatch{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return patch, nil
}

// ── activity（9.9）─────────────────────────────────────────

// recordDoDChange は追加・更新・削除を activity へ1行書く（9.1.1 / 9.9）。
//
// entity は親チケット、action は常に update（references.go / comments.go と同じ）。
func recordDoDChange(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID, ticketID string, old *string, next string,
) error {
	field := "dod"
	entry := activity.Entry{
		ProjectID:  projectID,
		EntityType: activity.EntityTicket,
		EntityID:   ticketID,
		Action:     activity.Update,
		Field:      &field,
		OldValue:   old,
	}
	if next != "" {
		entry.NewValue = &next
	}
	return rec.Record(ctx, q, entry)
}

// dodSummaryOf は履歴に載せる要約（9.9）。
//
//	未: テストが通ること   ／   済: テストが通ること
//
// **接頭辞を付けるのは is_satisfied が真偽値だからである。** true / false を
// そのまま載せると履歴が「false → true」としか言わず、**どの条件を満たしたのかが
// 読めない。**
//
// **sort_order は含めない。** 並び順だけの変更を記録しないため（9.9）、
// 要約が同じなら「変わっていない」と判定されて1行も増えない。
func dodSummaryOf(v dodView) string {
	prefix := "未: "
	if v.IsSatisfied {
		prefix = "済: "
	}
	return prefix + v.Body
}

// denyDoDSatisfied は ticket.self_edit だけを持つ呼び出し元が is_satisfied を
// 書こうとしていないかを見る（ApiDesign.md 9.9。0029）。
//
// **pb_submit_result が「盤面を動かさない」と決めた判断と正面からぶつかる**
// （9.15、手順26c）。完了の判定は人が行うので、エージェントに開けるのは
// body の追加・編集・削除までである。
//
// **ticket.edit を持っていれば何もしない**——画面からチェックを付け外しする
// 経路は従来どおりである。
func denyDoDSatisfied(r *http.Request, key string) *apierr.Error {
	a := auth.ProjectAuthzFromContext(r.Context(), key)
	if a == nil {
		return apierr.New(apierr.Forbidden).
			WithMessage("完了条件のチェックを変更する権限がありません").
			WithCause(errors.New("権限の計算結果が文脈に無い"))
	}
	if auth.HasPermission(a.Permissions, permTicketEdit) {
		return nil
	}
	return apierr.New(apierr.Forbidden).
		WithMessage("完了条件のチェック（is_satisfied）を変更する権限がありません").
		WithCause(errors.New("ticket.self_edit では is_satisfied を書けない"))
}
