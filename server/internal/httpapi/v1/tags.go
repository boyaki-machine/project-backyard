// タグAPI（ApiDesign.md 9.11）。
//
//	GET    /api/v1/projects/{key}/tags        ticket.view
//	POST   /api/v1/projects/{key}/tags        project.edit
//	PATCH  /api/v1/projects/{key}/tags/{id}   project.edit
//	DELETE /api/v1/projects/{key}/tags/{id}   project.edit
//
// **タグは「プロジェクトの分類軸を決める」ものである。** 定義できるのは
// プロジェクト設定のタグタブ（GuiDesign.md 5.9.4）だけで、チケット詳細からは
// 既存のタグを付け外しできるにとどまる。作れるようにすると、綴り違いの重複
// （GUI と gui と ＧＵＩ）が増えて分類として機能しなくなる。
//
// **権限を増やしていない。** 定義は project.edit、チケットへの付与は
// ticket.edit（9.5.2 の tag_ids、手順17）で足りる。DbDesign.md 7.2 の28件は
// Design.md 付録Aで確定済みであり、既存権限の内側に収める。
//
// **audit_log にも activity にも記録しない**（9.1.1）。2.10 のカタログに
// 入らず、activity の読み手はチケットの変更履歴である。記録しても
// 読む画面が無い。タグ削除の追跡が要ると分かった時点で足す（10.2）。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// タグの制約（ApiDesign.md 9.11、DbDesign.md 6.10 の CHECK）。
const (
	tagNameMaxLen = 30

	// tagSortOrderStep は sort_order 省略時の刻み幅。
	//
	// 並べ替え（9.11.1）がクライアント側で 10, 20, 30… と振り直すため、
	// 末尾への追加も同じ間隔にしておく。値そのものに意味は無く、
	// 順序を決めるのは大小関係だけである。
	tagSortOrderStep = 10
)

// tagView は 9.11 が返す1行。
type tagView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SortOrder   int32  `json:"sort_order"`
	TicketCount int64  `json:"ticket_count"`
}

// tagListView は 9.11 の一覧応答。
//
// **page / per_page / total を持たない。** バックログのグループ化は全タグを
// セクションの順序に使うため（GuiDesign.md 5.4.1）、ページングすると2ページ目の
// タグがグループ化に現れず、設計上そもそも使えない（9.11）。
type tagListView struct {
	Items []tagView `json:"items"`
}

type createTagRequest struct {
	Name      string `json:"name"`
	SortOrder *int32 `json:"sort_order"`
}

type patchTagRequest struct {
	Name      *string `json:"name"`
	SortOrder *int32  `json:"sort_order"`
}

// ── GET /api/v1/projects/{key}/tags ─────────────────────────

// listTags はプロジェクトのタグを sort_order 昇順・同値は name 昇順で返す。
func (h *handler) listTags(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/tags")
	if !ok {
		return
	}

	rows, err := h.q.ListTagsByProject(r.Context(), projectID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグ一覧を読めない: %w", err)))
		return
	}

	items := make([]tagView, 0, len(rows))
	for _, row := range rows {
		items = append(items, tagView{
			ID:          row.ID,
			Name:        row.Name,
			SortOrder:   row.SortOrder,
			TicketCount: row.TicketCount,
		})
	}
	WriteJSON(w, http.StatusOK, tagListView{Items: items})
}

// ── POST /api/v1/projects/{key}/tags ────────────────────────

// createTag はタグを1件作る。201 + Location + 作った1件（B-2）。
//
// **トランザクションを張らない。** 書き込みは INSERT 1文だけで、監査記録も
// 伴わない（9.1.1）。sort_order の既定を読むクエリとの間に競合が起きても
// 同じ値のタグが2件並ぶだけで、一覧は sort_order, name の順で安定する。
func (h *handler) createTag(w http.ResponseWriter, r *http.Request) {
	_, key, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/tags")
	if !ok {
		return
	}

	var req createTagRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}
	name, apiErr := validateTagName(req.Name)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	ctx := r.Context()
	sortOrder, apiErr := h.resolveTagSortOrder(ctx, projectID, req.SortOrder)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	id := ulidgen.New()
	if err := h.q.CreateTag(ctx, gen.CreateTagParams{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		SortOrder: sortOrder,
	}); err != nil {
		if isTagNameConflict(err) {
			apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
				WithMessage(fmt.Sprintf("タグ「%s」は既にあります", name)).
				WithCause(fmt.Errorf("タグ名 %q が重複した: %w", name, err)))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグを作成できない: %w", err)))
		return
	}

	view, err := h.tagByID(ctx, projectID, id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("作成したタグを読めない: %w", err)))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/tags/%s", key, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── PATCH /api/v1/projects/{key}/tags/{id} ──────────────────

// patchTag はタグの name / sort_order を変える。
//
// **If-Match を要求しない**（2.8 の楽観ロックの対象は project と app_user のみで、
// tag は version 列を持たない）。並べ替えはこのエンドポイントへ sort_order を
// 送ることで表現する（9.11.1）。専用の move は設けていない。
//
// 何も送られていない PATCH は現在の値をそのまま返す（users_update と同じ扱い）。
func (h *handler) patchTag(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "PATCH /projects/{key}/tags/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var req patchTagRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	params := gen.UpdateTagParams{ProjectID: projectID, ID: id}
	if req.Name != nil {
		name, apiErr := validateTagName(*req.Name)
		if apiErr != nil {
			apierr.Write(w, r, apiErr)
			return
		}
		params.Name = pgtype.Text{String: name, Valid: true}
	}
	if req.SortOrder != nil {
		params.SortOrder = pgtype.Int4{Int32: *req.SortOrder, Valid: true}
	}

	ctx := r.Context()
	rows, err := h.q.UpdateTag(ctx, params)
	if err != nil {
		if isTagNameConflict(err) {
			apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
				WithMessage(fmt.Sprintf("タグ「%s」は既にあります", params.Name.String)).
				WithCause(fmt.Errorf("タグ名 %q が重複した: %w", params.Name.String, err)))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグ %q を更新できない: %w", id, err)))
		return
	}
	if rows == 0 {
		writeTagNotFound(w, r, id)
		return
	}

	view, err := h.tagByID(ctx, projectID, id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("更新したタグを読めない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// ── DELETE /api/v1/projects/{key}/tags/{id} ─────────────────

// deleteTag はタグを消す。ticket_tag は CASCADE で追従する（DbDesign.md 6.10）。
//
// **使用中でも削除できる。** 禁止すると、要らなくなった分類を消すために全
// チケットから手で外すことになる（9.11）。画面は確認ダイアログに使用中の件数を
// 出す（GuiDesign.md 6.3）。
func (h *handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "DELETE /projects/{key}/tags/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	rows, err := h.q.DeleteTag(r.Context(), gen.DeleteTagParams{ProjectID: projectID, ID: id})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグ %q を削除できない: %w", id, err)))
		return
	}
	if rows == 0 {
		writeTagNotFound(w, r, id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 補助 ────────────────────────────────────────────────────

// tagByID は応答用に1件を読み直す。ticket_count が要るため、INSERT / UPDATE の
// 戻り値では組み立てられない。
func (h *handler) tagByID(ctx context.Context, projectID, id string) (tagView, error) {
	row, err := h.q.GetTagByID(ctx, gen.GetTagByIDParams{ProjectID: projectID, ID: id})
	if err != nil {
		return tagView{}, err
	}
	return tagView{
		ID:          row.ID,
		Name:        row.Name,
		SortOrder:   row.SortOrder,
		TicketCount: row.TicketCount,
	}, nil
}

// resolveTagSortOrder は sort_order 省略時の既定（現在の最大値 + 10）を返す。
func (h *handler) resolveTagSortOrder(
	ctx context.Context, projectID string, given *int32,
) (int32, *apierr.Error) {
	if given != nil {
		return *given, nil
	}
	next, err := h.q.NextTagSortOrder(ctx, projectID)
	if err != nil {
		return 0, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグの並び順を決められない: %w", err))
	}
	return next, nil
}

// validateTagName は 9.11 の name を検証する。
//
// **前後の空白を取り除いてから長さを見る。** 空白だけの名前を弾き、
// 「 設計 」と「設計」が別のタグになる経路を塞ぐ。
func validateTagName(raw string) (string, *apierr.Error) {
	name := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "name", Code: "required",
			Message: "タグ名を入力してください",
		})
	case n > tagNameMaxLen:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "name", Code: "too_long",
			Message: fmt.Sprintf("タグ名は%d文字以内で入力してください", tagNameMaxLen),
		})
	}
	return name, nil
}

// writeTagNotFound は「そのプロジェクトに無いタグ」への応答。
//
// **他プロジェクトのタグも 404 に寄せる。** クエリの WHERE が project_id を
// 含むので、存在しない ID と区別せず1つの結果になる（Design.md 6.4.5）。
func writeTagNotFound(w http.ResponseWriter, r *http.Request, id string) {
	apierr.Write(w, r, apierr.New(apierr.NotFound).
		WithMessage("タグが見つかりません").
		WithCause(fmt.Errorf("タグ %q が見つからない", id)))
}

// isTagNameConflict は uq_tag_project_name（DbDesign.md 6.10）の違反かを返す。
//
// 23505 は unique_violation。tag 表の主キーは ULID で衝突しないため、
// この表の 23505 は (project_id, name) の重複を意味する。
func isTagNameConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return pgErr.TableName == "tag" || pgErr.ConstraintName == "uq_tag_project_name"
}
