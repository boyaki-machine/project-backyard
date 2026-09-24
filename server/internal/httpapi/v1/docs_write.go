// 文書API の更新系（ApiDesign.md 10.4）。
//
//	POST   /api/v1/projects/{key}/docs         doc.edit
//	PATCH  /api/v1/projects/{key}/docs/*path   doc.edit
//	DELETE /api/v1/projects/{key}/docs/*path   doc.edit
//
// **doc.edit は operator と project_member が持たない**（DbDesign.md 8.1.4）。
// 憲章は全参加者を縛るため、更新できる人を絞る。**「その操作ができない人」が
// 実在する例でもある**（Design.md 付録A）。
//
// **PATCH は If-Match を要求する**（2.8 / 10.4）。人とエージェントが同じ文書を触るため、
// プロジェクト設定より競合が起きやすい。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 文書の制約（ApiDesign.md 10.4、DbDesign.md 8.1.1 の CHECK）。
const (
	docTitleMaxLen        = 200
	docChangeReasonMaxLen = 200

	// docSortOrderStep は sort_order 省略時の刻み幅（10.4 / 8.1.2）。
	docSortOrderStep = 10
)

// docSlugPattern は DbDesign.md 8.1.1 の CHECK と同じ式である。
//
// **DDL と同じものを2か所に持っている。** DB に任せて 500 を返すのではなく、
// 422 で「どの欄が悪いか」を返すためで、tag / project のキー検証と同じ扱いである。
// **_ を含められない**ことが、10.1 の _revisions を予約語にできる根拠になっている。
var docSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// uq_document_slug は「同じ親の下に同じ slug は1つ」（DbDesign.md 8.1.1）。
const uqDocumentSlug = "uq_document_slug"

type createDocRequest struct {
	Slug       string  `json:"slug"`
	Title      string  `json:"title"`
	ParentPath *string `json:"parent_path"`
	BodyMd     *string `json:"body_md"`
	SortOrder  *int32  `json:"sort_order"`
}

// updateDocRequest は 10.4 の PATCH。
//
// **json.RawMessage で受けるのは、3つの状態を区別する必要があるため**である
// （9.5.2 の updateTicketRequest と同じ形）。
//
//	キーが無い      → 触らない
//	キーがあり null → parent_path をトップレベルへ移す
//	キーがあり値    → その値にする
type updateDocRequest map[string]json.RawMessage

// 10.4 が PATCH で受け付ける項目。これ以外のキーは 422 で弾く。
var docPatchableFields = []string{
	"title", "body_md", "slug", "parent_path", "sort_order", "change_reason",
}

// ── POST /api/v1/projects/{key}/docs ────────────────────────

// createDoc は文書を1件作る。201 + Location + 作った1件（10.3 の形）。
//
// **revision_no = 1 を同時に作る**（10.4）。リビジョンは「その変更のあとの本文」を
// 持つので、作成時の1件が無いと最初の編集で「作ったときの本文」がどの版にも
// 残らない。**文書とリビジョンは1トランザクションで書く**——片方だけ残ると
// 履歴の先頭が欠ける。
func (h *handler) createDoc(w http.ResponseWriter, r *http.Request) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/docs")
	if !ok {
		return
	}

	var req createDocRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	ctx := r.Context()
	tree, apiErr := h.loadDocTree(ctx, projectID)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var details []apierr.Detail
	slug := validateDocSlug(req.Slug, &details)
	title := validateDocTitle(req.Title, &details)

	// parent_path は省略・null でトップレベル（10.4）。存在しないパスは 422 の
	// details[].code = "not_found" であって 404 ではない——リクエストの「欄」が
	// 誤っているのであり、叩いた資源が無いわけではない。
	var parent *docNode
	if req.ParentPath != nil && strings.TrimSpace(*req.ParentPath) != "" {
		path := strings.Trim(strings.TrimSpace(*req.ParentPath), "/")
		parent = tree.byPath[path]
		if parent == nil {
			details = append(details, apierr.Detail{
				Field: "parent_path", Code: "not_found",
				Message: fmt.Sprintf("文書 %s が見つかりません", path),
			})
		}
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	parentID := pgtype.Text{}
	if parent != nil {
		parentID = text(parent.row.ID)
	}

	sortOrder, apiErr := h.resolveDocSortOrder(ctx, projectID, parentID, req.SortOrder)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	body := ""
	if req.BodyMd != nil {
		body = *req.BodyMd
	}

	id := ulidgen.New()
	actorID := pgtype.Text{}
	if p != nil {
		actorID = text(p.ActorID)
	}

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.CreateDocument(ctx, gen.CreateDocumentParams{
			ID:        id,
			ProjectID: text(projectID),
			ParentID:  parentID,
			Slug:      slug,
			Title:     title,
			BodyMd:    body,
			SortOrder: sortOrder,
			CreatedBy: actorID,
		}); err != nil {
			return err
		}
		return q.CreateDocumentRevision(ctx, gen.CreateDocumentRevisionParams{
			ID:         ulidgen.New(),
			DocumentID: id,
			RevisionNo: 1,
			Title:      title,
			BodyMd:     body,
			ChangedBy:  actorID,
		})
	})
	if err != nil {
		if isUniqueViolation(err, uqDocumentSlug) {
			apierr.Write(w, r, docSlugConflict(slug))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書を作成できない: %w", err)))
		return
	}

	path := slug
	if parent != nil {
		path = parent.path + "/" + slug
	}
	w.Header().Set("Location",
		fmt.Sprintf("/api/v1/projects/%s/docs/%s", key, path))
	h.writeDocByID(w, r, id, path)
}

// ── PATCH /api/v1/projects/{key}/docs/*path ─────────────────

// patchDoc は文書を部分更新する（ApiDesign.md 10.4）。
//
// **リビジョンを作るのは title か body_md が実際に変わったときだけ**である。
// sort_order の変更や同じ本文の送り直しでは作らない——並べ替えのたびに履歴が
// 伸びると「いつ内容が変わったか」が読めなくなる。**version はどの更新でも +1 する**
// （2.8 の規約を1本に保つため。9.4 の move と同じ扱い）。
func (h *handler) patchDoc(w http.ResponseWriter, r *http.Request) {
	ctx, s, ok := h.docScope(w, r, "PATCH /projects/{key}/docs/*")
	if !ok {
		return
	}
	if s.req.revisions {
		apierr.Write(w, r, docMethodNotAllowed(r.Method))
		return
	}

	version, apiErr := parseIfMatch(r)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var req updateDocRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	node := s.node()
	current, err := h.q.GetDocument(ctx, node.row.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s を読めない: %w", node.path, err)))
		return
	}

	params, plan, apiErr := buildUpdateDocParams(req, s, current)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}
	params.ID = node.row.ID
	params.Version = version
	if s.actorID != "" {
		params.UpdatedBy = text(s.actorID)
	}

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		rows, err := q.UpdateDocument(ctx, params)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errVersionConflict
		}
		if !plan.contentChanged {
			return nil
		}
		no, err := q.NextDocumentRevisionNo(ctx, node.row.ID)
		if err != nil {
			return err
		}
		return q.CreateDocumentRevision(ctx, gen.CreateDocumentRevisionParams{
			ID:         ulidgen.New(),
			DocumentID: node.row.ID,
			RevisionNo: no,
			Title:      plan.title,
			BodyMd:     plan.bodyMd,
			ChangedBy:  params.UpdatedBy,
			// リビジョンを作らない更新で change_reason を送っても捨てる（10.4）。
			// ここへ来るのは content が変わったときだけなので、素直に写す。
			ChangeReason: plan.changeReason,
		})
	})
	if err != nil {
		switch {
		case errors.Is(err, errVersionConflict):
			apierr.Write(w, r, apierr.New(apierr.Conflict))
		case isUniqueViolation(err, uqDocumentSlug):
			apierr.Write(w, r, docSlugConflict(plan.slug))
		default:
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("文書 %s を更新できない: %w", node.path, err)))
		}
		return
	}

	h.writeDocByID(w, r, node.row.ID, plan.path)
}

// ── DELETE /api/v1/projects/{key}/docs/*path ────────────────

// deleteDoc は文書を消す。**物理削除で、部分木ごと消える**（10.4 / DbDesign.md 8.1.1）。
// document_revision も CASCADE で一緒に消える。
//
// **子を持っていても API は止めない**（10.4）。件数を示して確認するのは画面の
// 仕事である（GuiDesign.md 6.3）——使用中のタグを消せるようにしたのと同じ判断で
// （9.11）、消せないと構造を直せなくなる。
func (h *handler) deleteDoc(w http.ResponseWriter, r *http.Request) {
	ctx, s, ok := h.docScope(w, r, "DELETE /projects/{key}/docs/*")
	if !ok {
		return
	}
	if s.req.revisions {
		apierr.Write(w, r, docMethodNotAllowed(r.Method))
		return
	}

	node := s.node()
	rows, err := h.q.DeleteDocument(ctx, node.row.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s を削除できない: %w", node.path, err)))
		return
	}
	if rows == 0 {
		apierr.Write(w, r, docNotFound(node.path))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 入力の検証 ──────────────────────────────────────────────

// docUpdatePlan は PATCH の結果として何が起きるかをまとめたもの。
type docUpdatePlan struct {
	// contentChanged が真のときだけ document_revision を1行足す（10.4）。
	contentChanged bool
	title          string
	bodyMd         string
	changeReason   pgtype.Text
	// slug / path は応答と Location、および 409 の文言に使う。
	slug string
	path string
}

// buildUpdateDocParams は 10.4 の本文を検証してクエリ引数へ写す。
//
// **details には見つかった誤りをすべて載せる**（2.5）。フォームの各入力欄に
// 紐づけるため、最初の1件で打ち切らない。
func buildUpdateDocParams(
	req updateDocRequest, s docScopeInfo, current gen.GetDocumentRow,
) (gen.UpdateDocumentParams, docUpdatePlan, *apierr.Error) {
	node := s.node()
	params := gen.UpdateDocumentParams{}
	plan := docUpdatePlan{
		title:  current.Title,
		bodyMd: current.BodyMd,
		slug:   current.Slug,
		path:   node.path,
	}
	var details []apierr.Detail

	for field := range req {
		if !contains(docPatchableFields, field) {
			details = append(details, apierr.Detail{
				Field: field, Code: "unknown_field",
				Message: fmt.Sprintf("%s は更新できません", field),
			})
		}
	}

	if raw, ok := req["title"]; ok {
		v, err := decodeDocString(raw)
		if err != nil {
			details = append(details, docInvalidType("title", "文字列"))
		} else {
			title := validateDocTitle(v, &details)
			params.Title = text(title)
			if title != current.Title {
				plan.contentChanged = true
				plan.title = title
			}
		}
	}

	if raw, ok := req["body_md"]; ok {
		v, err := decodeDocString(raw)
		if err != nil {
			details = append(details, docInvalidType("body_md", "文字列"))
		} else {
			// body_md は NOT NULL DEFAULT ''（8.1.1）。空文字は「本文を消す」
			// 正当な操作なので、COALESCE では表せる（NULL ではないため）。
			params.BodyMd = pgtype.Text{String: v, Valid: true}
			if v != current.BodyMd {
				plan.contentChanged = true
				plan.bodyMd = v
			}
		}
	}

	if raw, ok := req["slug"]; ok {
		v, err := decodeDocString(raw)
		if err != nil {
			details = append(details, docInvalidType("slug", "文字列"))
		} else {
			slug := validateDocSlug(v, &details)
			params.Slug = text(slug)
			plan.slug = slug
		}
	}

	if raw, ok := req["sort_order"]; ok {
		var v int32
		if err := json.Unmarshal(raw, &v); err != nil {
			details = append(details, docInvalidType("sort_order", "整数"))
		} else {
			params.SortOrder = pgtype.Int4{Int32: v, Valid: true}
		}
	}

	// parent_path は「送られていない」「null（トップレベルへ）」「値（移動先）」の
	// 3状態を持つ（10.4）。_set のフラグで区別する。
	parent := node
	if raw, ok := req["parent_path"]; ok {
		params.ParentIDSet = true
		switch {
		case isJSONNull(raw):
			parent = nil
		default:
			v, err := decodeDocString(raw)
			if err != nil {
				details = append(details, docInvalidType("parent_path", "文字列"))
				break
			}
			path := strings.Trim(strings.TrimSpace(v), "/")
			if path == "" {
				parent = nil
				break
			}
			target := s.tree.byPath[path]
			switch {
			case target == nil:
				details = append(details, apierr.Detail{
					Field: "parent_path", Code: "not_found",
					Message: fmt.Sprintf("文書 %s が見つかりません", path),
				})
			case isDocSelfOrDescendant(node, target):
				// 自分自身または自分の子孫を parent_path に指定した（10.4）。
				// DB は自己参照すら止めないので、ここで見るしかない。
				details = append(details, apierr.Detail{
					Field: "parent_path", Code: "cycle",
					Message: "自分自身または配下の文書には移動できません",
				})
			default:
				parent = target
				params.ParentID = text(target.row.ID)
			}
		}
	}

	if raw, ok := req["change_reason"]; ok && !isJSONNull(raw) {
		v, err := decodeDocString(raw)
		if err != nil {
			details = append(details, docInvalidType("change_reason", "文字列"))
		} else if utf8.RuneCountInString(v) > docChangeReasonMaxLen {
			details = append(details, apierr.Detail{
				Field: "change_reason", Code: "too_long",
				Message: fmt.Sprintf("更新理由は%d文字以内で入力してください", docChangeReasonMaxLen),
			})
		} else if v != "" {
			plan.changeReason = pgtype.Text{String: v, Valid: true}
		}
	}

	if len(details) > 0 {
		return params, plan, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}

	// 応答に載せる path は、移動と改名を反映したものである（10.4）。
	plan.path = plan.slug
	if parent != nil && parent != node {
		plan.path = parent.path + "/" + plan.slug
	} else if parent == node {
		// parent_path が送られていないので、親は変わらない。
		if i := lastIndexByte(node.path, '/'); i >= 0 {
			plan.path = node.path[:i] + "/" + plan.slug
		}
	}
	return params, plan, nil
}

// isDocSelfOrDescendant は target が node 自身または node の子孫かを見る（10.4）。
//
// **木の上で見る。** IsDocumentDescendant（document.sql）と同じ判定だが、
// docScope が既に木を読み込んでいるので往復を1つ増やさない。
func isDocSelfOrDescendant(node, target *docNode) bool {
	if node == target {
		return true
	}
	for _, c := range node.children {
		if isDocSelfOrDescendant(c, target) {
			return true
		}
	}
	return false
}

// resolveDocSortOrder は sort_order 省略時の既定（同じ親の中の末尾。10.4）。
func (h *handler) resolveDocSortOrder(
	ctx context.Context, projectID string, parentID pgtype.Text, requested *int32,
) (int32, *apierr.Error) {
	if requested != nil {
		return *requested, nil
	}
	next, err := h.q.NextDocumentSortOrder(ctx, gen.NextDocumentSortOrderParams{
		ProjectID: text(projectID),
		ParentID:  parentID,
	})
	if err != nil {
		return 0, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("sort_order の既定値を読めない: %w", err))
	}
	return next, nil
}

// writeDocByID は 10.3 の形で1件を返す。POST / PATCH の応答に使う。
//
// **木を読み直す。** 移動・改名のあとは path が変わっており、docScope が持っている
// 木は更新前のものだからである。
func (h *handler) writeDocByID(w http.ResponseWriter, r *http.Request, id, path string) {
	ctx := r.Context()
	row, err := h.q.GetDocument(ctx, id)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s を読めない: %w", path, err)))
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	WriteJSON(w, status, docViewOf(&docNode{row: gen.ListDocumentTreeRow{
		ID: row.ID, ParentID: row.ParentID, Slug: row.Slug, Title: row.Title,
		SortOrder: row.SortOrder, Version: row.Version,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, path: path}, row))
}

func validateDocSlug(raw string, details *[]apierr.Detail) string {
	slug := strings.TrimSpace(raw)
	switch {
	case slug == "":
		*details = append(*details, apierr.Detail{
			Field: "slug", Code: "required", Message: "slug を入力してください",
		})
	case !docSlugPattern.MatchString(slug):
		*details = append(*details, apierr.Detail{
			Field: "slug", Code: "invalid",
			Message: "slug は英小文字・数字・ハイフンで、英数字から始まる64文字以内で入力してください",
		})
	}
	return slug
}

func validateDocTitle(raw string, details *[]apierr.Detail) string {
	// 前後の空白を取り除いてから検証する（10.4）。
	title := strings.TrimSpace(raw)
	switch {
	case title == "":
		*details = append(*details, apierr.Detail{
			Field: "title", Code: "required", Message: "タイトルを入力してください",
		})
	case utf8.RuneCountInString(title) > docTitleMaxLen:
		*details = append(*details, apierr.Detail{
			Field: "title", Code: "too_long",
			Message: fmt.Sprintf("タイトルは%d文字以内で入力してください", docTitleMaxLen),
		})
	}
	return title
}

func docInvalidType(field, want string) apierr.Detail {
	return apierr.Detail{
		Field: field, Code: "invalid",
		Message: fmt.Sprintf("%s は%sで指定してください", field, want),
	}
}

// docSlugConflict は uq_document_slug に当たったときの 409（10.6）。
//
// **conflict ではなく already_exists である**（2.5.1）。conflict は If-Match
// 不一致のような状態の競合を指し、こちらは一意なキーの重複である。
func docSlugConflict(slug string) *apierr.Error {
	return apierr.New(apierr.AlreadyExists).
		WithMessage(fmt.Sprintf("同じ階層に %s という文書が既にあります", slug)).
		WithDetails(apierr.Detail{
			Field: "slug", Code: "already_exists",
			Message: "同じ階層では別の slug を指定してください",
		})
}

// isUniqueViolation は一意制約違反かを、制約名まで見て判定する。
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// decodeDocString は JSON の文字列を読む。null は空文字として扱わず error にする
// （null を許す欄は呼び出し側が isJSONNull で先に分岐している）。
func decodeDocString(raw json.RawMessage) (string, error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	return v, nil
}
