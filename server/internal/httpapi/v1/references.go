// チケットの外部参照（ApiDesign.md 9.10.2）。手順17c。
//
//	GET    /api/v1/projects/{key}/tickets/{seq}/references        ticket.view
//	POST   /api/v1/projects/{key}/tickets/{seq}/references        ticket.reference.edit
//	PATCH  /api/v1/projects/{key}/tickets/{seq}/references/{id}    ticket.reference.edit
//	DELETE /api/v1/projects/{key}/tickets/{seq}/references/{id}    ticket.reference.edit
//
// **更新系は 0027 で ticket.edit から切り出した**（DbDesign.md 6.12.1）。
// エージェントが MCP から commit / ブランチを積めるようにするためで、ticket.edit を
// そのまま許可リストへ入れると本文・担当・期日の書き換えまで開いてしまう。
// **ticket.edit を持つロールには機械的に配ってあるので、人から見た可否は変わらない。**
//
// **チケットから「外」を指す参照である。** ticket_link（9.10.1）が同じ
// プロジェクトの別のチケットを指すのに対し、こちらはリポジトリ・コミット・
// 仕様書のように PB の管理外にあるものを指す。URL が生きているかを PB は
// 知らないので、同じ表に混ぜると片方にしか効かない制約が並ぶ。
//
// **kind ごとに必須の項目が違う**（DbDesign.md 6.12 の CHECK）。code は
// repository、doc は url が要る。DB の CHECK が最後の砦だが、そこで落ちると
// 2.5 の形式ではなく 500 になるため、アプリ側で先に見る。
//
// **ページネーション・ETag・If-Match はいずれも持たない**（9.10.2）。1チケット
// あたり数件に収まり、2.8 の楽観ロックの対象は project と app_user に限られる。
// **親チケットの version も updated_at も動かさない**——参照の増減は ticket の
// 列を変えないためで、ticket_tag を動かす 9.2.5 とはここが違う。
//
// **変更は activity に記録する**（9.1.1 / 9.10.2）。
// action は常に update で、create / delete は使わない——ticket:31 の
// action='delete' は「そのチケットが消された」を意味しており（9.5.3）、
// 参照1行の削除に同じ値を当てるとチケットごと消えたように見える。
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
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 外部参照の制約（ApiDesign.md 9.10.2 の表）。
const (
	referenceRepositoryMaxLen = 200
	referenceBranchMaxLen     = 255
	referenceCommitShaMaxLen  = 64
	referenceURLMaxLen        = 1000
	referenceLabelMaxLen      = 200
	referenceNoteMaxLen       = 500
)

// referenceKinds は kind の CHECK（DbDesign.md 6.12）。
var referenceKinds = []string{"code", "doc"}

// referenceImmutableFields は PATCH で送ると 422 になる項目（9.10.2）。
//
// **kind だけである。** id / created_at / updated_at / created_by もサーバが
// 決めるが、9.10.2 が名指ししているのは kind なので、そこに揃えてある。
var referenceImmutableFields = []string{"kind"}

// referenceView は 9.10.2 が返す1行。
//
// **created_by を返すが、画面は使わない**（9.10.2、GuiDesign.md 5.5）。
// このセクションが表すのは「チケットの成果物としてリポジトリ・ブランチ・
// コミットが紐づいている」という関係であって、行を登録したのが誰かではない。
// それでも返して DB にも残すのは、将来エージェントの書いた行を区別したく
// なったときに遡れるようにするためである。
type referenceView struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Repository *string   `json:"repository"`
	Branch     *string   `json:"branch"`
	CommitSha  *string   `json:"commit_sha"`
	URL        *string   `json:"url"`
	Label      *string   `json:"label"`
	Note       *string   `json:"note"`
	SortOrder  int32     `json:"sort_order"`
	CreatedBy  *actorRef `json:"created_by"`
	CreatedAt  Time      `json:"created_at"`
	UpdatedAt  Time      `json:"updated_at"`
}

// referenceListView は 9.10.2 の一覧応答。
//
// **page / per_page / total を持たない**（9.10.2）。1チケットあたり数件に
// 収まり、画面は開いた時点で全件を出す（GuiDesign.md 5.5）。
type referenceListView struct {
	Items []referenceView `json:"items"`
}

// createReferenceRequest は POST の本文（9.10.2）。
type createReferenceRequest struct {
	Kind       string  `json:"kind"`
	Label      *string `json:"label"`
	URL        *string `json:"url"`
	Repository *string `json:"repository"`
	Branch     *string `json:"branch"`
	CommitSha  *string `json:"commit_sha"`
	Note       *string `json:"note"`
	SortOrder  *int32  `json:"sort_order"`
}

// referencePatch は PATCH を解いたあとの更新内容。Set が false の項目は触らない。
type referencePatch struct {
	Label      optional[string]
	URL        optional[string]
	Repository optional[string]
	Branch     optional[string]
	CommitSha  optional[string]
	Note       optional[string]
	SortOrder  optional[int32]
}

// ── GET /api/v1/projects/{key}/tickets/{seq}/references ──────

// listTicketReferences はチケットの外部参照を返す（9.10.2）。
func (h *handler) listTicketReferences(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r,
		"GET /projects/{key}/tickets/{seq}/references")
	if !ok {
		return
	}

	items, err := ticketReferencesFor(ctx, h.q, ticketID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, referenceListView{Items: items})
}

// ── POST /api/v1/projects/{key}/tickets/{seq}/references ─────

// createTicketReference は外部参照を1件足す。201 + Location + 作った1件。
//
// **トランザクションを張る。** 参照の INSERT と activity の記録が不可分で
// あるためで、記録の無い参照が生まれると手順19 の変更履歴に穴があく
// （tickets_create.go と同じ理由）。
func (h *handler) createTicketReference(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/references")
	if !ok {
		return
	}

	var req createReferenceRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	fields, e := validateNewReference(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var view referenceView

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// sort_order 省略時は末尾（現在の最大値 + 10。9.10.2）。
		var sortOrder int32
		if req.SortOrder != nil {
			sortOrder = *req.SortOrder
		} else {
			next, err := q.NextTicketReferenceSortOrder(ctx, ticketID)
			if err != nil {
				return fmt.Errorf("外部参照の並び順を決められない: %w", err)
			}
			sortOrder = next
		}

		if err := q.CreateTicketReference(ctx, gen.CreateTicketReferenceParams{
			ID:         id,
			TicketID:   ticketID,
			Kind:       fields.kind,
			Label:      fields.label,
			Url:        fields.url,
			Repository: fields.repository,
			Branch:     fields.branch,
			CommitSha:  fields.commitSha,
			Note:       fields.note,
			CreatedBy:  pgtype.Text{String: scope.actorID, Valid: scope.actorID != ""},
			SortOrder:  sortOrder,
		}); err != nil {
			return fmt.Errorf("外部参照を作成できない: %w", err)
		}

		row, err := q.GetTicketReference(ctx, gen.GetTicketReferenceParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("作成した外部参照を読めない: %w", err)
		}
		view = buildReferenceView(referenceRow(row))

		return recordReferenceChange(ctx, q, rec, scope.projectID, ticketID,
			view.Kind, nil, referenceSummaryOf(view))
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/references/%s", scope.key, scope.seq, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── PATCH /api/v1/projects/{key}/tickets/{seq}/references/{id} ─

// patchTicketReference は外部参照を部分更新する（9.10.2）。
//
// **更新後の行が必須の条件を満たすことを、UPDATE の前に確かめる。** kind='doc'
// の url を null にする、kind='code' の repository を空にするといった要求は
// 422 で弾く。DB の CHECK に任せると 2.5 の形式ではなく 500 になる。
func (h *handler) patchTicketReference(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"PATCH /projects/{key}/tickets/{seq}/references/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var raw map[string]json.RawMessage
	if e := decodeJSON(r, &raw); e != nil {
		apierr.Write(w, r, e)
		return
	}
	patch, e := parseReferencePatch(raw)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	var (
		view     referenceView
		notFound bool
		invalid  *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		before, err := q.GetTicketReference(ctx, gen.GetTicketReferenceParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errReferenceHandled
			}
			return fmt.Errorf("外部参照 %q を読めない: %w", id, err)
		}

		// 更新後の姿で必須項目を見る。CHECK と同じ条件をアプリ側でも持つ。
		if d := validatePatchedReference(referenceRow(before), patch); d != nil {
			invalid = apierr.New(apierr.ValidationFailed).WithDetails(*d)
			return errReferenceHandled
		}

		rows, err := q.UpdateTicketReference(ctx, updateReferenceParams(ticketID, id, patch))
		if err != nil {
			return fmt.Errorf("外部参照 %q を更新できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errReferenceHandled
		}

		after, err := q.GetTicketReference(ctx, gen.GetTicketReferenceParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("更新した外部参照を読めない: %w", err)
		}
		view = buildReferenceView(referenceRow(after))

		oldSummary := referenceSummaryOf(buildReferenceView(referenceRow(before)))
		newSummary := referenceSummaryOf(view)
		// 値が変わっていなければ記録しない（9.5.2 と同じ扱い）。
		if oldSummary == newSummary {
			return nil
		}
		return recordReferenceChange(ctx, q, rec, scope.projectID, ticketID,
			view.Kind, &oldSummary, newSummary)
	})
	switch {
	case notFound:
		apierr.Write(w, r, referenceNotFound(id))
		return
	case invalid != nil:
		apierr.Write(w, r, invalid)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── DELETE /api/v1/projects/{key}/tickets/{seq}/references/{id} ─

// deleteTicketReference は外部参照を1件消す。204（9.10.2）。
//
// **画面は code にも削除を置く**（GuiDesign.md 5.5）。追加の導線が無いぶん、
// 誤って積まれた行を人が始末できないと詰むためである。
func (h *handler) deleteTicketReference(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"DELETE /projects/{key}/tickets/{seq}/references/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	rec := activity.FromRequest(r)
	var notFound bool

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.GetTicketReference(ctx, gen.GetTicketReferenceParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errReferenceHandled
			}
			return fmt.Errorf("外部参照 %q を読めない: %w", id, err)
		}
		view := buildReferenceView(referenceRow(row))

		rows, err := q.DeleteTicketReference(ctx, gen.DeleteTicketReferenceParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("外部参照 %q を削除できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errReferenceHandled
		}

		summary := referenceSummaryOf(view)
		return recordReferenceChange(ctx, q, rec, scope.projectID, ticketID,
			view.Kind, &summary, "")
	})
	switch {
	case notFound:
		apierr.Write(w, r, referenceNotFound(id))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 共通の足場 ──────────────────────────────────────────────

// errReferenceHandled は「応答を決めたので巻き戻して抜ける」の合図。
//
// tickets_create.go の errTicketReference と同じ型の仕掛けで、
// RunInTx の外へ 500 として漏らさないために呼び出し側で握りつぶす。
var errReferenceHandled = errors.New("外部参照の処理をハンドラ内で決めた")

// referenceRow は List と Get の行を1つの形に揃える。
//
// sqlc は同じ SELECT でもクエリごとに別の構造体を作るので、View の組み立てを
// 2本持たないようにここで畳む。
type referenceRow gen.GetTicketReferenceRow

// ticketReferencesFor は1チケットぶんの外部参照を組み立てる。
//
// **詳細応答（9.5.1）と一覧（9.10.2）が同じ関数を通る。** 並び順の規則
// （kind 昇順、同じ kind の中は sort_order → created_at）はクエリ側にあり、
// 2か所で書き分けない。
func ticketReferencesFor(
	ctx context.Context, q gen.Querier, ticketID string,
) ([]referenceView, error) {
	rows, err := q.ListTicketReferences(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("外部参照を読めない: %w", err)
	}
	items := make([]referenceView, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildReferenceView(referenceRow(row)))
	}
	return items, nil
}

func buildReferenceView(row referenceRow) referenceView {
	return referenceView{
		ID:         row.ID,
		Kind:       row.Kind,
		Repository: textPtr(row.Repository),
		Branch:     textPtr(row.Branch),
		CommitSha:  textPtr(row.CommitSha),
		URL:        textPtr(row.Url),
		Label:      textPtr(row.Label),
		Note:       textPtr(row.Note),
		SortOrder:  row.SortOrder,
		CreatedBy:  actorRefOf(row.CreatedBy, row.CreatedByKind, row.CreatedByName),
		CreatedAt:  Time(row.CreatedAt),
		UpdatedAt:  Time(row.UpdatedAt),
	}
}

// referenceNotFound は「そのチケットに無い外部参照」への応答。
//
// **他チケットの参照も 404 に寄せる。** クエリの WHERE が ticket_id を含むので、
// 存在しない ID と区別せず1つの結果になる（Design.md 6.4.5）。
func referenceNotFound(id string) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage("参照が見つかりません").
		WithCause(fmt.Errorf("外部参照 %q が見つからない", id))
}

// ── activity への記録（9.1.1 / 9.10.2）──────────────────────

// recordReferenceChange は外部参照の変更を1行書く。
//
// **entity は親チケットである**（参照の id ではない）。9.13.2 の entity は
// ticket:31 の形しか受け付けず、変更履歴の読み手はチケットのページに居る。
//
// old が nil なら追加、new が空なら削除、どちらも埋まっていれば更新。
func recordReferenceChange(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID, ticketID, kind string, old *string, next string,
) error {
	field := "reference." + kind
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

// referenceSummaryOf は履歴に載せる要約（9.10.2）。
//
// **画面の表示と同じ形にする**（GuiDesign.md 5.5）。履歴を読む人が、画面で見た
// ものと同じ文字列を見られるようにするためで、id や JSON を入れない。
//
//	code : my-app : pb/31 : a1b2c3d（欠けている要素は詰める）
//	doc  : label、無ければ url
func referenceSummaryOf(v referenceView) string {
	if v.Kind == "doc" {
		if v.Label != nil {
			return *v.Label
		}
		if v.URL != nil {
			return *v.URL
		}
		return "(参考リンク)"
	}
	parts := make([]string, 0, 3)
	for _, p := range []*string{v.Repository, v.Branch, v.CommitSha} {
		if p != nil {
			parts = append(parts, *p)
		}
	}
	if len(parts) == 0 {
		return "(コード)"
	}
	return strings.Join(parts, " : ")
}

// ── 検証（9.10.2 の表）───────────────────────────────────────

// referenceFields は検証を通したあとの値。
type referenceFields struct {
	kind       string
	label      pgtype.Text
	url        pgtype.Text
	repository pgtype.Text
	branch     pgtype.Text
	commitSha  pgtype.Text
	note       pgtype.Text
}

// validateNewReference は POST の本文を検証する（9.10.2）。
//
// **必須は kind ごとに違う**——code は repository、doc は url。誤りは
// 2.5 の details にまとめて並べる（1件ずつ返すと直すたびに往復が要る）。
func validateNewReference(req createReferenceRequest) (referenceFields, *apierr.Error) {
	var (
		fields  referenceFields
		details []apierr.Detail
	)

	kind := strings.TrimSpace(req.Kind)
	switch {
	case kind == "":
		details = append(details, apierr.Detail{
			Field: "kind", Code: "required", Message: "種別を指定してください",
		})
	case !slices.Contains(referenceKinds, kind):
		details = append(details, apierr.Detail{
			Field: "kind", Code: "invalid",
			Message: "種別は " + strings.Join(referenceKinds, " / ") + " のいずれかです",
		})
	default:
		fields.kind = kind
	}

	fields.repository, details = referenceTextField(
		req.Repository, "repository", "リポジトリ", referenceRepositoryMaxLen, details)
	fields.branch, details = referenceTextField(
		req.Branch, "branch", "ブランチ", referenceBranchMaxLen, details)
	fields.commitSha, details = referenceTextField(
		req.CommitSha, "commit_sha", "コミット", referenceCommitShaMaxLen, details)
	fields.url, details = referenceTextField(
		req.URL, "url", "URL", referenceURLMaxLen, details)
	fields.label, details = referenceTextField(
		req.Label, "label", "ラベル", referenceLabelMaxLen, details)
	fields.note, details = referenceTextField(
		req.Note, "note", "メモ", referenceNoteMaxLen, details)

	switch fields.kind {
	case "code":
		if !fields.repository.Valid {
			details = append(details, apierr.Detail{
				Field: "repository", Code: "required",
				Message: "リポジトリを入力してください",
			})
		}
	case "doc":
		if !fields.url.Valid {
			details = append(details, apierr.Detail{
				Field: "url", Code: "required", Message: "URLを入力してください",
			})
		}
	}

	if len(details) > 0 {
		return referenceFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return fields, nil
}

// referenceTextField は任意の文字列項目を1つ検証する。
//
// **空文字は「無い」と同じ**に倒す（NULL にする）。「設定された空の文字列」と
// 「設定されていない」を区別しても、画面に出るものは同じである。
func referenceTextField(
	v *string, field, label string, maxLen int, details []apierr.Detail,
) (pgtype.Text, []apierr.Detail) {
	if v == nil {
		return pgtype.Text{}, details
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return pgtype.Text{}, details
	}
	if utf8.RuneCountInString(s) > maxLen {
		return pgtype.Text{}, append(details, apierr.Detail{
			Field: field, Code: "too_long",
			Message: fmt.Sprintf("%sは%d文字以内で入力してください", label, maxLen),
		})
	}
	return pgtype.Text{String: s, Valid: true}, details
}

// parseReferencePatch は PATCH の本文を解く（9.10.2）。
//
// **kind は送ると 422**（作成後は変えられない）。details[].code は
// immutable_field で、9.5.2 と同じ規約である。
func parseReferencePatch(raw map[string]json.RawMessage) (referencePatch, *apierr.Error) {
	var (
		patch   referencePatch
		details []apierr.Detail
	)

	for _, field := range referenceImmutableFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "immutable_field",
				Message: "種別は作成後に変更できません",
			})
		}
	}

	patch.Repository, details = referencePatchText(
		raw, "repository", "リポジトリ", referenceRepositoryMaxLen, details)
	patch.Branch, details = referencePatchText(
		raw, "branch", "ブランチ", referenceBranchMaxLen, details)
	patch.CommitSha, details = referencePatchText(
		raw, "commit_sha", "コミット", referenceCommitShaMaxLen, details)
	patch.URL, details = referencePatchText(
		raw, "url", "URL", referenceURLMaxLen, details)
	patch.Label, details = referencePatchText(
		raw, "label", "ラベル", referenceLabelMaxLen, details)
	patch.Note, details = referencePatchText(
		raw, "note", "メモ", referenceNoteMaxLen, details)

	if v, ok := raw["sort_order"]; ok {
		var n int32
		if err := json.Unmarshal(v, &n); err != nil {
			details = append(details, apierr.Detail{
				Field: "sort_order", Code: "invalid", Message: "整数で指定してください",
			})
		} else {
			patch.SortOrder = optional[int32]{Set: true, Value: n}
		}
	}

	if len(details) > 0 {
		return referencePatch{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return patch, nil
}

// referencePatchText は optionalStringField に長さの検証を添えたもの。
//
// **空白だけの値は「空にする」に倒す。** optionalStringField が null と同じ
// 扱いにするのは空文字だけなので、そのままだと `"  "` が列へ入る——DB の CHECK は
// NOT NULL しか見ないため（DbDesign.md 6.12）、**リポジトリが空白だけの code 参照が
// できてしまう**。ここで畳んでおくと validatePatchedReference が 422 で捕まえる。
func referencePatchText(
	raw map[string]json.RawMessage, field, label string, maxLen int,
	details []apierr.Detail,
) (optional[string], []apierr.Detail) {
	o, details := optionalStringField(raw, field, details, func(s string) *apierr.Detail {
		if utf8.RuneCountInString(strings.TrimSpace(s)) > maxLen {
			return &apierr.Detail{
				Field: field, Code: "too_long",
				Message: fmt.Sprintf("%sは%d文字以内で入力してください", label, maxLen),
			}
		}
		return nil
	})
	if o.Set && !o.Null && strings.TrimSpace(o.Value) == "" {
		return optional[string]{Set: true, Null: true}, details
	}
	return o, details
}

// validatePatchedReference は「更新後の行」が必須の条件を満たすかを見る。
//
// **DB の CHECK と同じ条件をアプリ側でも持つ**（DbDesign.md 6.12）。CHECK に
// 任せると 23514 が返り、2.5 の形式ではなく 500 になる。
func validatePatchedReference(before referenceRow, patch referencePatch) *apierr.Detail {
	switch before.Kind {
	case "code":
		if patch.Repository.Set && (patch.Repository.Null || patch.Repository.Value == "") {
			return &apierr.Detail{
				Field: "repository", Code: "required",
				Message: "リポジトリを入力してください",
			}
		}
	case "doc":
		if patch.URL.Set && (patch.URL.Null || patch.URL.Value == "") {
			return &apierr.Detail{
				Field: "url", Code: "required", Message: "URLを入力してください",
			}
		}
	}
	return nil
}

// updateReferenceParams は referencePatch をクエリ引数へ写す。
func updateReferenceParams(ticketID, id string, patch referencePatch) gen.UpdateTicketReferenceParams {
	params := gen.UpdateTicketReferenceParams{TicketID: ticketID, ID: id}
	params.LabelSet, params.Label = patchText(patch.Label)
	params.UrlSet, params.Url = patchText(patch.URL)
	params.RepositorySet, params.Repository = patchText(patch.Repository)
	params.BranchSet, params.Branch = patchText(patch.Branch)
	params.CommitShaSet, params.CommitSha = patchText(patch.CommitSha)
	params.NoteSet, params.Note = patchText(patch.Note)
	if patch.SortOrder.Set {
		params.SortOrder = pgtype.Int4{Int32: patch.SortOrder.Value, Valid: true}
	}
	return params
}

// patchText は optional[string] を「送られたか」と値に分ける。
//
// Set=false なら触らない。Set かつ Null なら NULL を書く（項目を空にする）。
func patchText(o optional[string]) (bool, pgtype.Text) {
	if !o.Set {
		return false, pgtype.Text{}
	}
	if o.Null {
		return true, pgtype.Text{}
	}
	return true, pgtype.Text{String: strings.TrimSpace(o.Value), Valid: true}
}
