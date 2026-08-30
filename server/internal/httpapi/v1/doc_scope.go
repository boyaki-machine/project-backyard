// 文書API の入口で共通して行う処理（ApiDesign.md 10.1）。
//
// **文書はパスで指す。** slug を根から連ねたもので、vision、rules/naming のように
// なる。ULID も返すが指定には使わない——9.1 のチケットが seq を使うのと同じ理由で、
// 共有できる URL になり、画面の URL（/p/:key/docs/rules/naming）とそのまま一致する。
//
// **path は列ではない**（DbDesign.md 8.1 / queries/document.sql）。位置は parent_id の
// 連なりで表すので、パスを解くには木を1回読む。ListDocumentTree は body_md を運ばない
// ので、この読み込みは目次1回ぶんで済む。
//
// **_revisions はサブ資源の予約語である。** slug の CHECK が _ を弾くため
// （DbDesign.md 8.1.1）、.../docs/a/b/_revisions が「a/b のリビジョン一覧」なのか
// 「a/b/_revisions という文書」なのかで迷うことがない（10.1）。
package v1

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// docRevisionsSegment は 10.1 のサブ資源の予約語。
const docRevisionsSegment = "_revisions"

// docNode は木に組み立てた文書1件。path はここで作る。
type docNode struct {
	row      gen.ListDocumentTreeRow
	path     string
	children []*docNode
}

// docTree はプロジェクトの文書の木。
//
// **byPath を持つのは、パスの解決を1回の走査で済ませるため**である。
// 木を作るときに全ノードのパスが決まるので、追加の探索が要らない。
type docTree struct {
	roots  []*docNode
	byPath map[string]*docNode
	byID   map[string]*docNode
}

// buildDocTree は ListDocumentTree の結果を木に組み立てる。
//
// **並びは届いた順をそのまま使う。** クエリが parent_id, sort_order, slug の順で
// 返すので（document.sql）、同じ親の子は既に 10.2 の「sort_order 昇順、同値は
// slug 昇順」に並んでいる。ここで並べ替え直すと、DB の照合順（ja-JP-x-icu。
// DbDesign.md 4.4）ではなく Go のバイト順になってしまう。
//
// **親が先に来る保証は無い**（parent_id は ULID の順で並ぶ）ので、2周する——
// 1周目でノードを作り、2周目で親に繋ぐ。パスは繋いだあとに根から下ろす。
func buildDocTree(rows []gen.ListDocumentTreeRow) *docTree {
	t := &docTree{
		byPath: make(map[string]*docNode, len(rows)),
		byID:   make(map[string]*docNode, len(rows)),
	}
	for _, row := range rows {
		n := &docNode{row: row}
		t.byID[row.ID] = n
	}
	for _, row := range rows {
		n := t.byID[row.ID]
		if !row.ParentID.Valid {
			t.roots = append(t.roots, n)
			continue
		}
		parent, ok := t.byID[row.ParentID.String]
		if !ok {
			// 親が同じプロジェクトに無い行は木に置けない。CASCADE と
			// project_id の制約により実際には起きないが、黙って落として
			// 目次が壊れるより、根として見えるほうが直しやすい。
			t.roots = append(t.roots, n)
			continue
		}
		parent.children = append(parent.children, n)
	}
	for _, n := range t.roots {
		t.assignPaths(n, "")
	}
	return t
}

// assignPaths は根から下ろしながら path を決める（10.1）。
func (t *docTree) assignPaths(n *docNode, prefix string) {
	n.path = n.row.Slug
	if prefix != "" {
		n.path = prefix + "/" + n.row.Slug
	}
	t.byPath[n.path] = n
	for _, c := range n.children {
		t.assignPaths(c, n.path)
	}
}

// descendants は n を含まない子孫の数。削除の確認（GuiDesign.md 6.3）で使う。
func (n *docNode) descendants() int {
	total := 0
	for _, c := range n.children {
		total += 1 + c.descendants()
	}
	return total
}

// docPathRequest はワイルドカードのパスを解いた結果。
type docPathRequest struct {
	// slugs は文書を指すパスの各セグメント。空にはならない。
	slugs []string
	// revisions は末尾が _revisions だったか（10.1）。
	revisions bool
	// revisionNo は .../_revisions/:no の :no。0 なら一覧のほう。
	revisionNo int32
}

// path は 10.1 のパス表記に戻したもの。エラー文言に使う。
func (p docPathRequest) path() string { return strings.Join(p.slugs, "/") }

// parseDocPath は chi のワイルドカードを解く（10.1）。
//
// **末尾のセグメントを見て分岐する。**
//
//	rules/naming                  文書
//	rules/_revisions              履歴の一覧
//	rules/_revisions/2            履歴の1件
//
// **_revisions を含む位置が末尾でないもの**（rules/_revisions/foo/bar）は、
// 文書としても履歴としても解けないので 404 に倒す。
func parseDocPath(r *http.Request) (docPathRequest, *apierr.Error) {
	raw := strings.Trim(chi.URLParam(r, "*"), "/")
	if raw == "" {
		return docPathRequest{}, docNotFound("")
	}

	segs := strings.Split(raw, "/")
	for _, s := range segs {
		if s == "" {
			return docPathRequest{}, docNotFound(raw)
		}
	}

	// 末尾から _revisions を探す。無ければ全部が文書のパスである。
	idx := -1
	for i, s := range segs {
		if s == docRevisionsSegment {
			idx = i
			break
		}
	}
	if idx < 0 {
		return docPathRequest{slugs: segs}, nil
	}
	if idx == 0 {
		return docPathRequest{}, docNotFound(raw)
	}

	req := docPathRequest{slugs: segs[:idx], revisions: true}
	switch rest := segs[idx+1:]; len(rest) {
	case 0:
		return req, nil
	case 1:
		no, err := strconv.ParseInt(rest[0], 10, 32)
		if err != nil || no < 1 {
			return docPathRequest{}, docRevisionNotFound(rest[0])
		}
		req.revisionNo = int32(no)
		return req, nil
	default:
		return docPathRequest{}, docNotFound(raw)
	}
}

// docScopeInfo は文書のハンドラが後段で使う値。
type docScopeInfo struct {
	key       string
	projectID string
	actorID   string
	tree      *docTree
	req       docPathRequest
}

// node は解決済みの文書。docScope が成功したときだけ非 nil。
func (s docScopeInfo) node() *docNode { return s.tree.byPath[s.req.path()] }

// docScope は {key} とワイルドカードのパスを解いて、文書1件までたどり着く。
//
// **3つの層を順に通す**（ticket_scope.go と同じ形）。
//
//	projectScopeContext  プリンシパルと {key} を解き、project_id まで解決する
//	parseDocPath         ワイルドカードを解き、_revisions を切り分ける（10.1）
//	buildDocTree         木を組み立て、パスから文書を引く
//
// 到達可否（メンバーか）は RequireProjectPermission が済ませている
// （Design.md 6.4.5）。ここから先のクエリはすべて document.id で閉じており、
// その id は project_id で絞った木からしか出てこない。
func (h *handler) docScope(
	w http.ResponseWriter, r *http.Request, route string,
) (context.Context, docScopeInfo, bool) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q, route)
	if !ok {
		return nil, docScopeInfo{}, false
	}
	req, apiErr := parseDocPath(r)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return nil, docScopeInfo{}, false
	}

	ctx := r.Context()
	tree, apiErr := h.loadDocTree(ctx, projectID)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return nil, docScopeInfo{}, false
	}

	info := docScopeInfo{key: key, projectID: projectID, tree: tree, req: req}
	if p != nil {
		info.actorID = p.ActorID
	}
	if info.node() == nil {
		apierr.Write(w, r, docNotFound(req.path()))
		return nil, docScopeInfo{}, false
	}
	return ctx, info, true
}

// loadDocTree は木を1回読んで組み立てる。
func (h *handler) loadDocTree(ctx context.Context, projectID string) (*docTree, *apierr.Error) {
	rows, err := h.q.ListDocumentTree(ctx, text(projectID))
	if err != nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("文書の木を読めない: %w", err))
	}
	return buildDocTree(rows), nil
}

// docNotFound は 10.6 の 404。
//
// **閲覧権限が無い場合も同じ応答になる**（10.6）。到達できないものの存在を
// 漏らさないためで、Design.md 6.4.5 の方針と同じである。
func docNotFound(path string) *apierr.Error {
	msg := "指定された文書が見つかりません"
	if path != "" {
		msg = fmt.Sprintf("文書 %s が見つかりません", path)
	}
	return apierr.New(apierr.NotFound).WithMessage(msg)
}

// docRevisionNotFound は 10.5 の「無い revision_no を指した」。
func docRevisionNotFound(no string) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage(fmt.Sprintf("リビジョン %s が見つかりません", no))
}

// docMethodNotAllowed は _revisions を GET 以外で叩いたときの 405（10.1 / 10.6）。
//
// **404 に寄せない。** slug の検証が _ を弾く以上「_revisions という名の文書」は
// 存在しえないので、これは実在するサブ資源に対する未定義のメソッドである。
// 「文書が無い」と答えると、書き込み先を間違えた呼び出し側が原因に辿り着けない。
func docMethodNotAllowed(method string) *apierr.Error {
	return apierr.New(apierr.MethodNotAllowed).
		WithMessage(fmt.Sprintf("履歴は読み取り専用です（%s は使えません）", method))
}
