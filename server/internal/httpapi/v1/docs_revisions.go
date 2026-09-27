// 文書の履歴（ApiDesign.md 10.5）。
//
//	GET /api/v1/projects/{key}/docs/*path/_revisions        doc.view
//	GET /api/v1/projects/{key}/docs/*path/_revisions/:no    doc.view
//
// **専用のルートを持たない。** chi のワイルドカード /docs/* が両方を受け、
// 末尾のセグメントで分岐する（10.1、doc_scope.go の parseDocPath）。slug の
// CHECK が _ を弾くため（DbDesign.md 8.1.1）、「_revisions という名の文書」と
// 取り違えることがない。
//
// **「前の版に戻す」の専用エンドポイントを置かない**（10.5）。取得した body_md を
// PATCH で書き戻すと、それが新しいリビジョンとして積まれる。**履歴は消さない。**
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// docRevisionsSort は 10.5 の「revision_no の降順に固定」。
//
// **Allowed を空にしてあるので ?sort= は 422 になる。** 履歴は新しい順に読むもので
// あり、並べ替える理由が無い。**?order= はこのエンドポイントのパラメータではない**
// ——openapi.yaml にも書かないので、送られても ParsePage が読むだけで SQL には
// 届かない（ORDER BY は revision_no DESC に固定されている）。
var docRevisionsSort = SortSpec{
	Allowed:        nil,
	DefaultSort:    "",
	DefaultOrder:   OrderDesc,
	DefaultPerPage: 20,
}

// docRevisionItem は 10.5 の一覧1件。
//
// **body_md を持たない**（10.5）。20件ぶんの Markdown を載せると応答が重くなる。
type docRevisionItem struct {
	RevisionNo   int32     `json:"revision_no"`
	Title        string    `json:"title"`
	ChangedBy    *actorRef `json:"changed_by"`
	ChangeReason *string   `json:"change_reason"`
	CreatedAt    Time      `json:"created_at"`
}

// docRevisionView は 10.5 の1件（本文つき）。
//
// **version も outline も持たない**（10.5）。version は現在の文書の楽観ロック値で
// あって過去の版に属さず、outline は現在の本文から作るものである。
type docRevisionView struct {
	RevisionNo   int32     `json:"revision_no"`
	Title        string    `json:"title"`
	BodyMd       string    `json:"body_md"`
	ChangedBy    *actorRef `json:"changed_by"`
	ChangeReason *string   `json:"change_reason"`
	CreatedAt    Time      `json:"created_at"`
}

// getDocRevisions は履歴を返す。一覧と1件のどちらかは parseDocPath が決めている。
//
// **getDoc から呼ばれる。** ルートは /docs/* の1本しかないので、分岐はここにある
// （10.1）。GET 以外のメソッドは patchDoc / deleteDoc が 405 を返す。
func (h *handler) getDocRevisions(
	w http.ResponseWriter, r *http.Request, ctx context.Context, s docScopeInfo,
) {
	node := s.node()
	if s.req.revisionNo > 0 {
		h.writeDocRevision(w, r, ctx, node, s.req.revisionNo)
		return
	}

	page, apiErr := ParsePage(r, docRevisionsSort)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	rows, err := h.q.ListDocumentRevisions(ctx, gen.ListDocumentRevisionsParams{
		DocumentID: node.row.ID,
		PageLimit:  int32(page.Limit()),
		PageOffset: int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s の履歴を読めない: %w", node.path, err)))
		return
	}

	items := make([]docRevisionItem, 0, len(rows))
	total := 0
	for _, row := range rows {
		total = int(row.Total)
		items = append(items, docRevisionItem{
			RevisionNo:   row.RevisionNo,
			Title:        row.Title,
			ChangedBy:    actorRefOf(row.ChangedBy, row.ChangedByKind, row.ChangedByName),
			ChangeReason: textPtr(row.ChangeReason),
			CreatedAt:    Time(row.CreatedAt),
		})
	}
	WriteJSON(w, http.StatusOK, NewList(items, page, total))
}

// writeDocRevision は 1件ぶんの本文を返す（10.5）。
func (h *handler) writeDocRevision(
	w http.ResponseWriter, r *http.Request, ctx context.Context, node *docNode, no int32,
) {
	row, err := h.q.GetDocumentRevision(ctx, gen.GetDocumentRevisionParams{
		DocumentID: node.row.ID,
		RevisionNo: no,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.Write(w, r, docRevisionNotFound(fmt.Sprint(no)))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s のリビジョン %d を読めない: %w", node.path, no, err)))
		return
	}

	WriteJSON(w, http.StatusOK, docRevisionView{
		RevisionNo:   row.RevisionNo,
		Title:        row.Title,
		BodyMd:       row.BodyMd,
		ChangedBy:    actorRefOf(row.ChangedBy, row.ChangedByKind, row.ChangedByName),
		ChangeReason: textPtr(row.ChangeReason),
		CreatedAt:    Time(row.CreatedAt),
	})
}
