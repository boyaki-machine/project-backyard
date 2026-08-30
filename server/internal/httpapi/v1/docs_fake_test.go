package v1

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 文書API（ApiDesign.md 10章）のフェイク。手順22a。
//
// **fake_test.go ではなくこのファイルに置く。** あちらは Phase 1 の資源で既に
// 1000行を超えており、資源ごとにファイルを分けたほうが「どのクエリを差し替えて
// いるか」を読み取りやすい。fakeQuerier への埋め込みは docs フィールド1つで済む。

// docFakeState は文書のクエリが読み書きする状態。
//
// **実DBの振る舞いを1つだけ真似ている**——uq_document_slug（同じ親の下で slug は
// 一意）である。409 の倒し方を単体で測るために要る（結合テストは実DBで測る）。
type docFakeState struct {
	tree    []gen.ListDocumentTreeRow
	bodies  map[string]string
	byID    map[string]gen.GetDocumentRow
	treeErr error

	nextSortOrder int32

	created         []gen.CreateDocumentParams
	createErr       error
	updated         []gen.UpdateDocumentParams
	updateRows      int64
	updateErr       error
	deleted         []string
	deleteRows      int64
	revisionsMade   []gen.CreateDocumentRevisionParams
	nextRevisionNo  int32
	revisionRows    []gen.ListDocumentRevisionsRow
	revisionByNo    map[int32]gen.GetDocumentRevisionRow
	revisionListErr error
}

func (q *fakeQuerier) ListDocumentTree(
	_ context.Context, projectID pgtype.Text,
) ([]gen.ListDocumentTreeRow, error) {
	q.opLog = append(q.opLog, "ListDocumentTree")
	if q.docs.treeErr != nil {
		return nil, q.docs.treeErr
	}
	_ = projectID
	// クエリの ORDER BY（parent_id NULLS FIRST, sort_order, slug）を真似る。
	// **並びはハンドラの責務ではなくクエリの責務**なので、フェイクが崩すと
	// 「実装が正しいのに落ちる」になる。
	rows := append([]gen.ListDocumentTreeRow(nil), q.docs.tree...)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ParentID.String != b.ParentID.String {
			return a.ParentID.String < b.ParentID.String
		}
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.Slug < b.Slug
	})
	return rows, nil
}

func (q *fakeQuerier) ListDocumentBodies(
	_ context.Context, _ pgtype.Text,
) ([]gen.ListDocumentBodiesRow, error) {
	q.opLog = append(q.opLog, "ListDocumentBodies")
	rows := make([]gen.ListDocumentBodiesRow, 0, len(q.docs.bodies))
	for id, body := range q.docs.bodies {
		rows = append(rows, gen.ListDocumentBodiesRow{ID: id, BodyMd: body})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (q *fakeQuerier) GetDocument(_ context.Context, id string) (gen.GetDocumentRow, error) {
	q.opLog = append(q.opLog, "GetDocument")
	row, ok := q.docs.byID[id]
	if !ok {
		return gen.GetDocumentRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeQuerier) NextDocumentSortOrder(
	_ context.Context, _ gen.NextDocumentSortOrderParams,
) (int32, error) {
	return q.docs.nextSortOrder, nil
}

func (q *fakeQuerier) CreateDocument(_ context.Context, arg gen.CreateDocumentParams) error {
	q.opLog = append(q.opLog, "CreateDocument")
	if q.docs.createErr != nil {
		return q.docs.createErr
	}
	if q.docSlugTaken(arg.ParentID, arg.Slug, "") {
		return uniqueViolation(uqDocumentSlug)
	}
	q.docs.created = append(q.docs.created, arg)

	// **作った行を読める状態にする。** ハンドラはコミットのあとに GetDocument で
	// 応答を組み立てる（docs_write.go の writeDocByID）ので、ここで登録しないと
	// 「作成は成功したのに 500」という、実装には無い症状をフェイクが作る。
	q.docs.tree = append(q.docs.tree, gen.ListDocumentTreeRow{
		ID: arg.ID, ParentID: arg.ParentID, Slug: arg.Slug, Title: arg.Title,
		SortOrder: arg.SortOrder, Version: 1,
	})
	q.docs.byID[arg.ID] = gen.GetDocumentRow{
		ID: arg.ID, ParentID: arg.ParentID, Slug: arg.Slug, Title: arg.Title,
		BodyMd: arg.BodyMd, SortOrder: arg.SortOrder, Version: 1,
		CreatedBy: arg.CreatedBy, UpdatedBy: arg.CreatedBy,
	}
	return nil
}

func (q *fakeQuerier) UpdateDocument(
	_ context.Context, arg gen.UpdateDocumentParams,
) (int64, error) {
	q.opLog = append(q.opLog, "UpdateDocument")
	if q.docs.updateErr != nil {
		return 0, q.docs.updateErr
	}
	if arg.Slug.Valid || arg.ParentIDSet {
		parent := arg.ParentID
		slug := arg.Slug.String
		if cur, ok := q.docs.byID[arg.ID]; ok {
			if !arg.ParentIDSet {
				parent = cur.ParentID
			}
			if !arg.Slug.Valid {
				slug = cur.Slug
			}
		}
		if q.docSlugTaken(parent, slug, arg.ID) {
			return 0, uniqueViolation(uqDocumentSlug)
		}
	}
	q.docs.updated = append(q.docs.updated, arg)
	return q.docs.updateRows, nil
}

func (q *fakeQuerier) DeleteDocument(_ context.Context, id string) (int64, error) {
	q.opLog = append(q.opLog, "DeleteDocument")
	q.docs.deleted = append(q.docs.deleted, id)
	return q.docs.deleteRows, nil
}

func (q *fakeQuerier) IsDocumentDescendant(
	_ context.Context, _ gen.IsDocumentDescendantParams,
) (bool, error) {
	return false, nil
}

func (q *fakeQuerier) NextDocumentRevisionNo(_ context.Context, _ string) (int32, error) {
	return q.docs.nextRevisionNo, nil
}

func (q *fakeQuerier) CreateDocumentRevision(
	_ context.Context, arg gen.CreateDocumentRevisionParams,
) error {
	q.opLog = append(q.opLog, "CreateDocumentRevision")
	q.docs.revisionsMade = append(q.docs.revisionsMade, arg)
	return nil
}

func (q *fakeQuerier) ListDocumentRevisions(
	_ context.Context, arg gen.ListDocumentRevisionsParams,
) ([]gen.ListDocumentRevisionsRow, error) {
	q.opLog = append(q.opLog, "ListDocumentRevisions")
	if q.docs.revisionListErr != nil {
		return nil, q.docs.revisionListErr
	}
	rows := q.docs.revisionRows
	from := int(arg.PageOffset)
	if from > len(rows) {
		from = len(rows)
	}
	to := from + int(arg.PageLimit)
	if to > len(rows) {
		to = len(rows)
	}
	return rows[from:to], nil
}

func (q *fakeQuerier) GetDocumentRevision(
	_ context.Context, arg gen.GetDocumentRevisionParams,
) (gen.GetDocumentRevisionRow, error) {
	q.opLog = append(q.opLog, "GetDocumentRevision")
	row, ok := q.docs.revisionByNo[arg.RevisionNo]
	if !ok {
		return gen.GetDocumentRevisionRow{}, pgx.ErrNoRows
	}
	return row, nil
}

// docSlugTaken は uq_document_slug を真似る（同じ親の下で slug は一意。
// NULLS NOT DISTINCT なのでトップレベルも1つの集合として扱う）。
func (q *fakeQuerier) docSlugTaken(parentID pgtype.Text, slug, exceptID string) bool {
	for _, row := range q.docs.tree {
		if row.ID == exceptID {
			continue
		}
		if row.ParentID.String == parentID.String &&
			row.ParentID.Valid == parentID.Valid && row.Slug == slug {
			return true
		}
	}
	return false
}

// uniqueViolation は実DBが返す 23505 を組み立てる。
//
// **制約名まで載せる。** isUniqueViolation（docs_write.go）が名前で判定するので、
// コードだけのフェイクでは「どの一意制約か」を測れない。
func uniqueViolation(constraint string) error {
	return &pgconn.PgError{Code: "23505", TableName: "document", ConstraintName: constraint}
}
