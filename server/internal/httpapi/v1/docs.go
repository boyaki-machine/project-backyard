// 文書API の読み取り（ApiDesign.md 10.2 / 10.3）。
//
//	GET /api/v1/projects/{key}/docs           doc.view  目次（?outline=1）
//	GET /api/v1/projects/{key}/docs/*path     doc.view  本文（?section=）
//
// **プロジェクト文書（憲章）の供給経路である**（Requirements.md 10.6.2）。規約・
// 価値観・判断の基準を1か所に置き、全参加者のエージェントが同じものを読む。
// MCP（pb_list_docs / pb_get_doc）も、このデータを同じ形で配る。
package v1

import (
	"fmt"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// docTreeItem は 10.2 の目次1件。
//
// **body_md を持たない。** 目次は「どこに何があるか」を答えるものであり、本文は
// 10.3 が返す。全文を一度に返す設計にすると、リポジトリの md ファイルより劣る
// （ファイルなら部分読みができる）。
//
// **version を持つ。** 木のドラッグ&ドロップ（GuiDesign.md 5.10）が If-Match に
// 使う（10.2 / 10.4）。
type docTreeItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	SortOrder int32  `json:"sort_order"`
	Version   int32  `json:"version"`
	UpdatedAt Time   `json:"updated_at"`
	// Outline は ?outline=1 のときだけ現れる（10.2）。
	//
	// **ポインタなのは、「要求していない」と「見出しが1つも無い」を分けるため**
	// である。値型 + omitempty では空スライスもキーごと消え、?outline=1 を付けた
	// 相手に「読むべき章が無い」と「まだ調べていない」を取り違えさせる。
	Outline  *[]docOutlineItem `json:"outline,omitempty"`
	Children []docTreeItem     `json:"children"`
}

// docTreeListView は 10.2 の応答。
//
// **ページネーションを持たない**（items のみ）。9.11 のタグと同じく全件が同時に
// 要る——目次は木であり、途中で切ると子が親から外れる（10.2）。
type docTreeListView struct {
	Items []docTreeItem `json:"items"`
}

// docView は 10.3 の本文。
type docView struct {
	ID         string           `json:"id"`
	Path       string           `json:"path"`
	Slug       string           `json:"slug"`
	ParentPath *string          `json:"parent_path"`
	Title      string           `json:"title"`
	BodyMd     string           `json:"body_md"`
	Outline    []docOutlineItem `json:"outline"`
	SortOrder  int32            `json:"sort_order"`
	Version    int32            `json:"version"`
	CreatedBy  *actorRef        `json:"created_by"`
	UpdatedBy  *actorRef        `json:"updated_by"`
	CreatedAt  Time             `json:"created_at"`
	UpdatedAt  Time             `json:"updated_at"`
}

// docSectionView は 10.3 の ?section= 指定時の応答。
//
// **本文の応答と別の型にしてある。** 10.3 の例が返しているのは id / path / title /
// section / body_md / version / updated_at の7つだけで、**outline も created_by も
// 含まない**——章1つを読みに来た相手に、文書全体の情報は要らない。
type docSectionView struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Section   string `json:"section"`
	BodyMd    string `json:"body_md"`
	Version   int32  `json:"version"`
	UpdatedAt Time   `json:"updated_at"`
}

// ── GET /api/v1/projects/{key}/docs ─────────────────────────

// listDocs は目次を返す（ApiDesign.md 10.2）。
//
// **?outline=1 のときだけ本文を読む**（10.2）。各文書の見出し一覧は本文から
// 作るためで、既定の目次は body_md をまったく運ばない。
func (h *handler) listDocs(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/docs")
	if !ok {
		return
	}

	ctx := r.Context()
	tree, apiErr := h.loadDocTree(ctx, projectID)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var outlines map[string][]docOutlineItem
	if boolQuery(r, "outline") {
		rows, err := h.q.ListDocumentBodies(ctx, text(projectID))
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("文書の本文を読めない: %w", err)))
			return
		}
		outlines = make(map[string][]docOutlineItem, len(rows))
		for _, row := range rows {
			outlines[row.ID] = outlineItems(row.BodyMd)
		}
	}

	WriteJSON(w, http.StatusOK, docTreeListView{Items: docTreeItems(tree.roots, outlines)})
}

// docTreeItems は木を応答の形へ写す。並びは buildDocTree が保っている（10.2）。
func docTreeItems(nodes []*docNode, outlines map[string][]docOutlineItem) []docTreeItem {
	items := make([]docTreeItem, 0, len(nodes))
	for _, n := range nodes {
		item := docTreeItem{
			ID:        n.row.ID,
			Path:      n.path,
			Slug:      n.row.Slug,
			Title:     n.row.Title,
			SortOrder: n.row.SortOrder,
			Version:   n.row.Version,
			UpdatedAt: Time(n.row.UpdatedAt.Time),
			Children:  docTreeItems(n.children, outlines),
		}
		if outlines != nil {
			// 見出しが1つも無い文書でも outline: [] を返す。
			list := outlines[n.row.ID]
			if list == nil {
				list = []docOutlineItem{}
			}
			item.Outline = &list
		}
		items = append(items, item)
	}
	return items
}

// ── GET /api/v1/projects/{key}/docs/*path ───────────────────

// getDoc は本文1件を返す（ApiDesign.md 10.3）。
//
// **?section= が付いたらその章だけを返す**（10.3）。見つからないときは
// 404 not_found に available_sections を添える——呼び出し側（多くはエージェント）が、
// もう一度目次を取りに行かずに次の一手を選べるようにするためである。
func (h *handler) getDoc(w http.ResponseWriter, r *http.Request) {
	ctx, s, ok := h.docScope(w, r, "GET /projects/{key}/docs/*")
	if !ok {
		return
	}
	if s.req.revisions {
		h.getDocRevisions(w, r, ctx, s)
		return
	}

	node := s.node()
	row, err := h.q.GetDocument(ctx, node.row.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書 %s を読めない: %w", node.path, err)))
		return
	}

	if section := r.URL.Query().Get("section"); section != "" {
		body, found := extractSection(row.BodyMd, section)
		if !found {
			apierr.Write(w, r, apierr.New(apierr.NotFound).
				WithMessage("指定された章が見つかりません").
				WithAvailableSections(sectionNames(row.BodyMd)))
			return
		}
		WriteJSON(w, http.StatusOK, docSectionView{
			ID:        row.ID,
			Path:      node.path,
			Title:     row.Title,
			Section:   section,
			BodyMd:    body,
			Version:   row.Version,
			UpdatedAt: Time(row.UpdatedAt.Time),
		})
		return
	}

	WriteJSON(w, http.StatusOK, docViewOf(node, row))
}

// docViewOf は 10.3 の応答を組み立てる。POST / PATCH も同じ形を返す（10.4）。
func docViewOf(node *docNode, row gen.GetDocumentRow) docView {
	var parentPath *string
	if i := lastIndexByte(node.path, '/'); i >= 0 {
		p := node.path[:i]
		parentPath = &p
	}
	return docView{
		ID:         row.ID,
		Path:       node.path,
		Slug:       row.Slug,
		ParentPath: parentPath,
		Title:      row.Title,
		BodyMd:     row.BodyMd,
		Outline:    outlineItems(row.BodyMd),
		SortOrder:  row.SortOrder,
		Version:    row.Version,
		CreatedBy:  actorRefOf(row.CreatedBy, row.CreatedByKind, row.CreatedByName),
		UpdatedBy:  actorRefOf(row.UpdatedBy, row.UpdatedByKind, row.UpdatedByName),
		CreatedAt:  Time(row.CreatedAt.Time),
		UpdatedAt:  Time(row.UpdatedAt.Time),
	}
}

// lastIndexByte は strings.LastIndexByte の別名。parent_path は path の
// 最後の / より前であり（10.1）、トップレベルでは -1 になって null を返す。
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// boolQuery は ?outline=1 のような真偽のクエリを読む。
//
// **1 / true を真とする。** 10.2 が示すのは ?outline=1 だけだが、値を付けずに
// ?outline とだけ書く呼び出し（curl や MCP の実装）も受ける。
func boolQuery(r *http.Request, name string) bool {
	if !r.URL.Query().Has(name) {
		return false
	}
	switch r.URL.Query().Get(name) {
	case "", "1", "true":
		return true
	default:
		return false
	}
}
