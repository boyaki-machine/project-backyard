// チケットのコメント（ApiDesign.md 9.8）。手順18a。
//
//	GET    /api/v1/projects/{key}/tickets/{seq}/comments        ticket.view
//	POST   /api/v1/projects/{key}/tickets/{seq}/comments        comment.create
//	PATCH  /api/v1/projects/{key}/tickets/{seq}/comments/{id}   comment.edit_own（自分のもののみ）
//	DELETE /api/v1/projects/{key}/tickets/{seq}/comments/{id}   comment.delete_any または comment.edit_own かつ自分のもの
//
// **人とエージェントが読み書きする一次資料である**（Requirements.md 6.5 / 10章）。
// kind は情報の類型（decision / discussion / artifact / caveat / reference /
// progress）で、Phase 3 の LLM 分類・要約がこの列を土台にする。
//
// **チケットの子資源で唯一 2.6 のページネーションと 2.7 の ETag を持つ**（9.8）。
// DoD・リンク・外部参照は1チケットあたり数件に収まるが、コメントは議論の量だけ
// 増える。ETag は Phase 2 のエージェントが「新しいコメントが付いたか」を安く
// 見る口になる。
//
// **削除は論理削除である**（DbDesign.md 4.6 / 6.7）。items に残し body_md を
// null にして返す——画面は「削除されました」と出す（GuiDesign.md 5.5）。
// **9.5.1 の comment_count だけは deleted_at IS NULL で数える**（読めるコメントの
// 件数を答えるため）。同じ表を数えて違う答えを返すのは意図的である。
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

// commentBodyMaxLen は本文の上限。
//
// **9.8 は下限（1文字以上）しか定めていない。** 上限を置くのは、activity の
// 要約（先頭40字）とは別に、1件で応答を膨らませる本文を弾くためである。
// 説明欄（ticket.body_md）と同じ上限に揃えてある。
const commentBodyMaxLen = 20000

// commentKinds は kind の CHECK（DbDesign.md 6.7）。9.8 の値域と一対一。
var commentKinds = []string{
	"discussion", "decision", "artifact", "caveat", "reference", commentKindProgress,
}

// commentKindDefault は kind 省略時の値（9.8）。
const commentKindDefault = "discussion"

// commentKindLabels は activity の要約に載せる表示名（9.8）。
//
// **画面のラベルと同じ語にする。** 履歴を読む人が、コメント欄で見たものと同じ
// 言葉を見られるようにするためで、reference.go の要約と同じ方針である。
// 語は Requirements.md 6.5 の情報類型に対応する。
var commentKindLabels = map[string]string{
	"discussion":        "議論",
	"decision":          "決定",
	"artifact":          "成果物",
	"caveat":            "注意",
	"reference":         "参照",
	commentKindProgress: "経過",
}

// commentImmutableFields は PATCH で送ると 422 になる項目（9.8）。
//
// **in_reply_to だけである。** 返信先を後から付け替えるとスレッドの形が変わり、
// 既に読まれた並びが崩れる。id / origin / created_at もサーバが決めるが、
// 9.8 が名指ししているのは in_reply_to なので、そこに揃えてある。
var commentImmutableFields = []string{"in_reply_to"}

// commentActivityBodyLimit は activity の要約に載せる本文の文字数（9.8）。
const commentActivityBodyLimit = 40

// permCommentDeleteAny は 9.8 の DELETE が OR の片側で見る権限。
const permCommentDeleteAny = "comment.delete_any"

// errCommentHandled は RunInTx を巻き戻さずに抜けるための番人。
var errCommentHandled = errors.New("comment: handled")

// commentView は 9.8 が返す1件。
//
// **author はポインタではない。** comment.author_id は NOT NULL かつ
// ON DELETE RESTRICT で、投稿者不在のコメントを DB が許さない
// （DbDesign.md 6.7）。参照の created_by（ON DELETE SET NULL）とはここが違う。
//
// **BodyMd はポインタである。** 削除済みの行で null にして返すためで、
// DB の列は NOT NULL のまま本文を保持している。
type commentView struct {
	ID        string   `json:"id"`
	BodyMd    *string  `json:"body_md"`
	Kind      string   `json:"kind"`
	InReplyTo *string  `json:"in_reply_to"`
	Origin    string   `json:"origin"`
	Author    actorRef `json:"author"`
	CreatedAt Time     `json:"created_at"`
	UpdatedAt Time     `json:"updated_at"`
	DeletedAt *Time    `json:"deleted_at"`
}

// createCommentRequest は POST の本文（9.8）。
type createCommentRequest struct {
	BodyMd    string  `json:"body_md"`
	Kind      *string `json:"kind"`
	InReplyTo *string `json:"in_reply_to"`
}

// commentPatch は PATCH を解いたあとの更新内容。Set が false の項目は触らない。
type commentPatch struct {
	BodyMd optional[string]
	Kind   optional[string]
}

// commentSort は 9.8 が許す並べ替え。**created_at だけである。**
var commentSort = SortSpec{
	Allowed:        []string{"created_at"},
	DefaultSort:    "created_at",
	DefaultOrder:   OrderAsc,
	DefaultPerPage: 50,
}

// ── GET /api/v1/projects/{key}/tickets/{seq}/comments ────────

// listTicketComments はコメントを新しい順・古い順で返す（9.8）。
func (h *handler) listTicketComments(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r,
		"GET /projects/{key}/tickets/{seq}/comments")
	if !ok {
		return
	}

	page, apiErr := ParsePage(r, commentSort)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	rows, err := h.q.ListTicketComments(ctx, gen.ListTicketCommentsParams{
		TicketID:   ticketID,
		SortOrder:  page.Order,
		PageLimit:  int32(page.Limit()),
		PageOffset: int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("コメントを読めない: %w", err)))
		return
	}

	items := make([]commentView, 0, len(rows))
	var (
		total       int64
		lastUpdated pgtype.Timestamptz
	)
	for _, row := range rows {
		items = append(items, buildCommentView(commentRowOfList(row)))
		total = row.Total
		lastUpdated = row.LastUpdatedAt
	}

	// **1件も返らなかったときは別クエリで数える。** ウィンドウ関数は行が無いと
	// 1行も返らないので、total と ETag の材料がここからは採れない（0件のチケット、
	// および範囲外のページを開いたとき）。
	if len(rows) == 0 {
		sum, err := h.q.SummarizeTicketComments(ctx, ticketID)
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("コメント件数を読めない: %w", err)))
			return
		}
		total, lastUpdated = sum.Total, sum.LastUpdatedAt
	}

	// 差分取得（2.7 / 9.8）。Phase 1 では If-None-Match を解釈せずヘッダだけ出す
	// （9.2.5 と同じ）。**論理削除も updated_at を動かす**ので、削除が 304 に
	// 埋もれることはない（DbDesign.md 6.7 の trg_comment_updated）。
	w.Header().Set("ETag", commentsETag(page, total, lastUpdated))

	WriteJSON(w, http.StatusOK, NewList(items, page, int(total)))
}

// commentsETag は一覧の ETag（9.8）。
//
// **フィルタ条件をハッシュに混ぜない。** 9.2.5 のチケット一覧と違い、絞り込みが
// 1つも無く、変わりうるのは並び順とページだけである。その2つは値として直接
// 入れてある——ETag は応答本文を指す検証子であり（RFC 9110 8.8.1）、
// 2ページ目と1ページ目が同じ値になってはならない。
func commentsETag(page Page, total int64, lastUpdated pgtype.Timestamptz) string {
	var stamp int64
	if lastUpdated.Valid {
		stamp = lastUpdated.Time.UTC().UnixNano()
	}
	return fmt.Sprintf(`W/"cmt-%d-%d-%s-%d-%d"`,
		page.Page, page.PerPage, page.Order, total, stamp)
}

// ── POST /api/v1/projects/{key}/tickets/{seq}/comments ───────

// createTicketComment はコメントを1件投稿する。201 + Location + 作った1件。
//
// **トランザクションを張る。** コメントの INSERT と activity の記録が不可分で
// あるためで、記録の無いコメントが生まれると手順19 の変更履歴に穴があく
// （references.go と同じ理由）。
func (h *handler) createTicketComment(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/comments")
	if !ok {
		return
	}

	var req createCommentRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	body, kind, replyTo, e := validateNewComment(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view    commentView
		invalid *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// in_reply_to は「同じチケットの、削除されていないコメント」であること
		// （9.8）。**存在しない ID と他チケットの ID と削除済みを区別しない**
		// ——どれも「指せない」であり、分けて返すと他チケットのコメントの
		// 存在を探れる（Design.md 6.4.5）。
		if replyTo.Valid {
			okReply, err := q.CommentRepliableInTicket(ctx, gen.CommentRepliableInTicketParams{
				TicketID: ticketID, ReplyToID: replyTo.String,
			})
			if err != nil {
				return fmt.Errorf("返信先のコメントを読めない: %w", err)
			}
			if !okReply {
				invalid = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "in_reply_to", Code: "not_found",
					Message: "返信先のコメントが見つかりません",
				})
				return errCommentHandled
			}
		}

		if err := q.CreateComment(ctx, gen.CreateCommentParams{
			ID:        id,
			TicketID:  ticketID,
			AuthorID:  scope.actorID,
			BodyMd:    body,
			Kind:      kind,
			Origin:    scope.origin(),
			InReplyTo: replyTo,
		}); err != nil {
			return fmt.Errorf("コメントを作成できない: %w", err)
		}

		row, err := q.GetTicketComment(ctx, gen.GetTicketCommentParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("作成したコメントを読めない: %w", err)
		}
		view = buildCommentView(commentRow(row))

		return recordCommentChange(ctx, q, rec, scope.projectID, ticketID,
			nil, commentSummaryOf(view))
	})
	switch {
	case invalid != nil:
		apierr.Write(w, r, invalid)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/comments/%s", scope.key, scope.seq, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── PATCH /api/v1/projects/{key}/tickets/{seq}/comments/{id} ─

// patchTicketComment はコメントを部分更新する（9.8）。
//
// **自分のものだけ**（comment.edit_own）。他人のものは 403 で、404 に倒さない
// ——同じチケットのコメント欄に並んで見えている行であり、存在を隠す意味がない。
func (h *handler) patchTicketComment(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"PATCH /projects/{key}/tickets/{seq}/comments/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var raw map[string]json.RawMessage
	if e := decodeJSON(r, &raw); e != nil {
		apierr.Write(w, r, e)
		return
	}
	patch, e := parseCommentPatch(raw)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	var (
		view      commentView
		notFound  bool
		forbidden bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		before, err := q.GetTicketComment(ctx, gen.GetTicketCommentParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errCommentHandled
			}
			return fmt.Errorf("コメント %q を読めない: %w", id, err)
		}
		// 削除済みは「もう無い」（9.8）。
		if before.DeletedAt.Valid {
			notFound = true
			return errCommentHandled
		}
		if before.AuthorID != scope.actorID {
			forbidden = true
			return errCommentHandled
		}

		rows, err := q.UpdateComment(ctx, gen.UpdateCommentParams{
			TicketID: ticketID,
			ID:       id,
			BodyMd:   patchTextValue(patch.BodyMd),
			Kind:     patchTextValue(patch.Kind),
		})
		if err != nil {
			return fmt.Errorf("コメント %q を更新できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errCommentHandled
		}

		after, err := q.GetTicketComment(ctx, gen.GetTicketCommentParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("更新したコメントを読めない: %w", err)
		}
		view = buildCommentView(commentRow(after))

		oldSummary := commentSummaryOf(buildCommentView(commentRow(before)))
		newSummary := commentSummaryOf(view)
		// 値が変わっていなければ記録しない（9.5.2 と同じ扱い）。
		if oldSummary == newSummary {
			return nil
		}
		return recordCommentChange(ctx, q, rec, scope.projectID, ticketID,
			&oldSummary, newSummary)
	})
	switch {
	case notFound:
		apierr.Write(w, r, commentNotFound(id))
		return
	case forbidden:
		apierr.Write(w, r, apierr.New(apierr.Forbidden).
			WithMessage("自分が投稿したコメントだけを編集できます").
			WithCause(fmt.Errorf("コメント %q は他のアクターのもの", id)))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── DELETE /api/v1/projects/{key}/tickets/{seq}/comments/{id} ─

// deleteTicketComment はコメントを論理削除する。204（9.8）。
//
// **必要権限は OR である**（9.8）。ルート定義には comment.delete_any と
// comment.edit_own の両方を並べ（RequireAnyProjectPermission）、どちらか一方でも
// 持っていればここへ届く。**「自分のものか」は行を読まないと決まらない**ので、
// その判定だけをここで行う——delete_any を持たない呼び出し元が他人のコメントを
// 消そうとした場合は 403。
func (h *handler) deleteTicketComment(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"DELETE /projects/{key}/tickets/{seq}/comments/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	// **delete_any を持つかをここで見る**（9.8 の OR 条件）。判定は
	// RequireAnyProjectPermission が計算済みのものを使う——同じリクエストの中で
	// 実効権限を2回計算せず、「ミドルウェアとハンドラで別々に数えた権限が
	// 食い違う」状態も作らない（tickets_update.go の ticket.assign と同じ形）。
	canDeleteAny := false
	if a := auth.ProjectAuthzFromContext(r.Context(), scope.key); a != nil {
		canDeleteAny = auth.HasPermission(a.Permissions, permCommentDeleteAny)
	}

	rec := activity.FromRequest(r)
	var (
		notFound  bool
		forbidden bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.GetTicketComment(ctx, gen.GetTicketCommentParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errCommentHandled
			}
			return fmt.Errorf("コメント %q を読めない: %w", id, err)
		}
		if row.DeletedAt.Valid {
			notFound = true
			return errCommentHandled
		}
		if !canDeleteAny && row.AuthorID != scope.actorID {
			forbidden = true
			return errCommentHandled
		}
		view := buildCommentView(commentRow(row))

		rows, err := q.SoftDeleteComment(ctx, gen.SoftDeleteCommentParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("コメント %q を削除できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errCommentHandled
		}

		oldSummary := commentSummaryOf(view)
		return recordCommentChange(ctx, q, rec, scope.projectID, ticketID,
			&oldSummary, "")
	})
	switch {
	case notFound:
		apierr.Write(w, r, commentNotFound(id))
		return
	case forbidden:
		apierr.Write(w, r, apierr.New(apierr.Forbidden).
			WithMessage("自分が投稿したコメントだけを削除できます").
			WithCause(fmt.Errorf("コメント %q は他のアクターのもの", id)))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 組み立てと検証 ───────────────────────────────────────────

// commentRow は List と Get の行を1つの形に揃える。
//
// sqlc は同じ SELECT でもクエリごとに別の構造体を作るので、View の組み立てを
// 2本持たないようにここで畳む（references.go と同じ形）。
type commentRow gen.GetTicketCommentRow

// commentRowOfList は一覧の行を commentRow へ写す。
//
// **一覧だけ列が2つ多い**（total / last_updated_at のウィンドウ関数）ので、
// 参照（references.go）のように型変換1つでは畳めない。ページャと ETag の材料を
// 同じ問い合わせで取る利得のほうが、この写し1本より大きい。
func commentRowOfList(row gen.ListTicketCommentsRow) commentRow {
	return commentRow{
		ID:         row.ID,
		BodyMd:     row.BodyMd,
		Kind:       row.Kind,
		InReplyTo:  row.InReplyTo,
		Origin:     row.Origin,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		DeletedAt:  row.DeletedAt,
		AuthorID:   row.AuthorID,
		AuthorKind: row.AuthorKind,
		AuthorName: row.AuthorName,
	}
}

func buildCommentView(row commentRow) commentView {
	view := commentView{
		ID:        row.ID,
		Kind:      row.Kind,
		InReplyTo: textPtr(row.InReplyTo),
		Origin:    row.Origin,
		Author: actorRef{
			ID: row.AuthorID, Kind: row.AuthorKind, DisplayName: row.AuthorName,
		},
		CreatedAt: Time(row.CreatedAt.Time),
		UpdatedAt: Time(row.UpdatedAt.Time),
		DeletedAt: apiTimestamptz(row.DeletedAt),
	}
	// **削除済みは本文を返さない**（9.8）。DB には残っている。
	if !row.DeletedAt.Valid {
		body := row.BodyMd
		view.BodyMd = &body
	}
	return view
}

// commentNotFound は「そのチケットに無いコメント」への応答。
//
// **削除済みもここへ合流する**（9.8）。論理削除でも「もう無い」として扱い、
// 二重削除が 204 で通らないようにする。
func commentNotFound(id string) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage("コメントが見つかりません").
		WithCause(fmt.Errorf("コメント %q が見つからない", id))
}

// validateNewComment は POST の本文を検証する（9.8）。
//
// 誤りは 2.5 の details にまとめて並べる（1件ずつ返すと直すたびに往復が要る）。
func validateNewComment(req createCommentRequest) (
	body, kind string, replyTo pgtype.Text, apiErr *apierr.Error,
) {
	var details []apierr.Detail

	body = strings.TrimSpace(req.BodyMd)
	switch {
	case body == "":
		details = append(details, apierr.Detail{
			Field: "body_md", Code: "required", Message: "本文を入力してください",
		})
	case utf8.RuneCountInString(body) > commentBodyMaxLen:
		details = append(details, apierr.Detail{
			Field: "body_md", Code: "too_long",
			Message: fmt.Sprintf("本文は%d文字以内で入力してください", commentBodyMaxLen),
		})
	}

	kind = commentKindDefault
	if req.Kind != nil {
		kind = strings.TrimSpace(*req.Kind)
		if !slices.Contains(commentKinds, kind) {
			details = append(details, apierr.Detail{
				Field: "kind", Code: "invalid", Message: "種別の指定が正しくありません",
			})
		}
	}

	if req.InReplyTo != nil {
		if s := strings.TrimSpace(*req.InReplyTo); s != "" {
			replyTo = pgtype.Text{String: s, Valid: true}
		}
	}

	if len(details) > 0 {
		return "", "", pgtype.Text{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return body, kind, replyTo, nil
}

// parseCommentPatch は PATCH の本文を解く（9.8）。
//
// **in_reply_to は送ると 422**（作成後は変えられない）。details[].code は
// immutable_field で、9.5.2 / 9.10.2 と同じ規約である。
//
// **body_md と kind に null は送れない。** どちらも列が NOT NULL であり、
// 「空にする」が意味を持たない——本文を消したいなら削除である。
func parseCommentPatch(raw map[string]json.RawMessage) (commentPatch, *apierr.Error) {
	var (
		patch   commentPatch
		details []apierr.Detail
	)

	for _, field := range commentImmutableFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "immutable_field",
				Message: "返信先は投稿後に変更できません",
			})
		}
	}

	if v, ok := raw["body_md"]; ok {
		var s string
		switch {
		case json.Unmarshal(v, &s) != nil:
			details = append(details, apierr.Detail{
				Field: "body_md", Code: "invalid", Message: "文字列で指定してください",
			})
		case strings.TrimSpace(s) == "":
			details = append(details, apierr.Detail{
				Field: "body_md", Code: "required", Message: "本文を入力してください",
			})
		case utf8.RuneCountInString(strings.TrimSpace(s)) > commentBodyMaxLen:
			details = append(details, apierr.Detail{
				Field: "body_md", Code: "too_long",
				Message: fmt.Sprintf("本文は%d文字以内で入力してください", commentBodyMaxLen),
			})
		default:
			patch.BodyMd = optional[string]{Set: true, Value: strings.TrimSpace(s)}
		}
	}

	if v, ok := raw["kind"]; ok {
		var s string
		if json.Unmarshal(v, &s) != nil || !slices.Contains(commentKinds, strings.TrimSpace(s)) {
			details = append(details, apierr.Detail{
				Field: "kind", Code: "invalid", Message: "種別の指定が正しくありません",
			})
		} else {
			patch.Kind = optional[string]{Set: true, Value: strings.TrimSpace(s)}
		}
	}

	if len(details) > 0 {
		return commentPatch{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return patch, nil
}

// patchTextValue は optional[string] を COALESCE 用の引数へ写す。
//
// **patchText（references.go）と違い「null を書く」経路を持たない。** コメントの
// body_md と kind はどちらも NOT NULL の列であり、送られなければ触らない、
// 送られたら置き換える、の2通りしかない。
func patchTextValue(o optional[string]) pgtype.Text {
	if !o.Set {
		return pgtype.Text{}
	}
	return pgtype.Text{String: o.Value, Valid: true}
}

// ── activity（9.8）─────────────────────────────────────────

// recordCommentChange は投稿・編集・削除を activity へ1行書く（9.1.1 / 9.8）。
//
// **entity は親チケットである**（コメントの id ではない）。9.13.2 の entity は
// `ticket:31` の形しか受け付けず、変更履歴の読み手はチケットを見ている。
//
// **action は常に update。** ticket:31 の action='delete' は「そのチケットが
// 消された」を意味しており（9.5.3）、コメント1件の削除に同じ値を当てると
// チケットごと消えたように見える（references.go と同じ判断）。
func recordCommentChange(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID, ticketID string, old *string, next string,
) error {
	field := "comment"
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

// commentSummaryOf は履歴に載せる要約（9.8）。
//
//	<kind の表示名>: <本文の先頭40字>
//
// **9.5.2 が body_md の old_value / new_value を NULL にするのとは扱いを変える。**
// あちらの理由は「20件ぶんの Markdown を載せると応答が重い」ことで、
// **要約すればその理由は消える。** 履歴に「何が書かれたか」が1行も残らないと、
// 手順19 の「最近の動き」が「コメントが変わった」としか言えなくなる。
//
// **削除済みの行は本文を持たない**（buildCommentView が null にする）ので、
// 削除の記録は削除前の view から作る。
func commentSummaryOf(v commentView) string {
	label, ok := commentKindLabels[v.Kind]
	if !ok {
		label = v.Kind
	}
	body := ""
	if v.BodyMd != nil {
		body = *v.BodyMd
	}
	// **改行は空白へ畳む。** 履歴は1行で読むもので、複数行がそのまま入ると
	// 9.13.2 の一覧が縦に伸びる。
	body = strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(body) > commentActivityBodyLimit {
		body = string([]rune(body)[:commentActivityBodyLimit]) + "…"
	}
	return label + ": " + body
}
