-- プロジェクト文書に関するクエリ（DbDesign.md 8.1、ApiDesign.md 10章）。
--
-- 手順22a で追加。消費者は Docs 画面（GuiDesign.md 5.10）と、Phase 2 後半の
-- MCP（pb_list_docs / pb_get_doc / pb_put_doc。Design.md 8章）である。
--
-- **path は列ではない。** 文書の位置は parent_id の連なりで表し、ApiDesign.md 10.1 の
-- パス（vision、rules/naming）は slug を根から連ねて組み立てる。列に持たせると、
-- 部分木を移動するたびに子孫の行をすべて書き換えることになる（10.4 の「path は
-- 子孫の分も付け替わる」は見え方の話であって、行の更新ではない）。
--
-- **テンプレート行を返さない。** is_template = true の4件（8.1.2）は project_id が
-- NULL なので、project_id で閉じたクエリには最初から現れない。**唯一の例外が
-- ListDocumentTemplates**（末尾）で、こちらはテンプレートだけを返す。
--
-- **すべてのクエリが project_id か document_id で閉じている。** 到達可否（メンバーか）
-- の判定は RequireProjectPermission が済ませている（Design.md 6.4.5）。
-- ListDocumentTemplates だけはプロジェクトに属さない行を読むが、呼ぶのは
-- プロジェクト作成の手順（internal/project）だけで、HTTP の入力を受けない。

-- ListDocumentTree はプロジェクトの全文書を1回で返す。目次（10.2）の源であり、
-- **パスの解決・循環の検出・削除時の子孫の数え上げも、この1本から作る。**
--
-- **body_md を含めない。** 目次は「どこに何があるか」を答えるもので、本文は 10.3 が
-- 返す（10.2）。本文が要る経路は GetDocument が document_id で1件だけ読む。
--
-- **version を含める。** 木のドラッグ&ドロップ（GuiDesign.md 5.10）は目次だけを持って
-- 複数行を PATCH し、10.4 が If-Match を必須とするため（10.2）。
--
-- **並びは親ごとに sort_order 昇順、同値は slug 昇順**（10.2）。parent_id を第1キーに
-- 置いて同じ親の行を隣り合わせているので、呼び出し側は届いた順に子を積むだけで
-- 各階層の順序が揃う。NULLS FIRST でトップレベルが先に来る。
-- name: ListDocumentTree :many
SELECT
  d.id,
  d.parent_id,
  d.slug,
  d.title,
  d.sort_order,
  d.version,
  d.created_at,
  d.updated_at
FROM document d
WHERE d.project_id = @project_id AND NOT d.is_template
ORDER BY d.parent_id NULLS FIRST, d.sort_order, d.slug;

-- ListDocumentBodies は ?outline=1（10.2）のためだけに本文を読む。
--
-- **応答には本文を載せない。** 見出し一覧は本文から作るので読む必要があるが、
-- 10.2 の「body_md を含めない」は応答の話である。**?outline=1 が付いたときだけ
-- 呼ぶ**ので、既定の目次は本文をまったく運ばない。
-- name: ListDocumentBodies :many
SELECT d.id, d.body_md
FROM document d
WHERE d.project_id = @project_id AND NOT d.is_template;

-- GetDocument は本文1件（10.3）。created_by / updated_by は LEFT JOIN である。
--
-- **どちらも null になりうる**（ON DELETE SET NULL。8.1.1）。9.8 の author が
-- null にならないのと異なり、**文書は書いた人が退職しても内容が生き続ける**（10.3）。
-- name: GetDocument :one
SELECT
  d.id,
  d.parent_id,
  d.slug,
  d.title,
  d.body_md,
  d.sort_order,
  d.version,
  d.created_by,
  ca.kind         AS created_by_kind,
  ca.display_name AS created_by_name,
  d.updated_by,
  ua.kind         AS updated_by_kind,
  ua.display_name AS updated_by_name,
  d.created_at,
  d.updated_at
FROM document d
LEFT JOIN actor ca ON ca.id = d.created_by
LEFT JOIN actor ua ON ua.id = d.updated_by
WHERE d.id = @id;

-- NextDocumentSortOrder は sort_order 省略時の既定（同じ親の中の最大値 + 10。10.4）。
--
-- **IS NOT DISTINCT FROM でトップレベルを扱う。** parent_id は NULL を取りうるので、
-- = では uq_document_slug の NULLS NOT DISTINCT と食い違う（8.1.1）。
--
-- 10刻みにするのは 8.1.2 と同じ理由で、並べ替え（9.11.1 と同じ形）が同じ間隔で
-- 振り直すためである。
-- name: NextDocumentSortOrder :one
SELECT COALESCE(max(sort_order), 0) + 10
FROM document
WHERE project_id = @project_id
  AND parent_id IS NOT DISTINCT FROM sqlc.narg('parent_id')
  AND NOT is_template;

-- name: CreateDocument :exec
INSERT INTO document (
  id, project_id, parent_id, slug, title, body_md, sort_order,
  created_by, updated_by
) VALUES (
  @id, @project_id, sqlc.narg('parent_id'), @slug, @title, @body_md, @sort_order,
  sqlc.narg('created_by'), sqlc.narg('created_by')
);

-- UpdateDocument は 10.4 の部分更新。**送られたフィールドだけを更新する。**
--
-- **2つの書き方を使い分けている**（ticket.sql の UpdateTicket と同じ形）。
--
--   NOT NULL の列（slug / title / body_md / sort_order）  COALESCE(sqlc.narg(…), 現在値)
--   NULL にできる列（parent_id）                         CASE WHEN @…_set THEN … END
--
-- COALESCE では「null を送ってトップレベルへ移す」を表せない。10.4 が
-- parent_path の null をトップレベルと定めているので、_set のフラグで
-- 「送られていない」と「null が送られた」を区別する。
--
-- **version は必ず +1 する**（10.4）。sort_order だけの変更でも動かすのは、
-- 2.8 の規約を1本に保つためである（9.4 の move と同じ扱い）。
--
-- **WHERE に version を置くのが If-Match そのものである。** 0行なら 409 conflict
-- か 404 のどちらかで、呼び出し側が行の存在を別に確かめて切り分ける。
--
-- **updated_at はトリガが動かす**（trg_document_updated。8.1.1）。
-- name: UpdateDocument :execrows
UPDATE document SET
  parent_id  = CASE WHEN @parent_id_set::boolean THEN sqlc.narg('parent_id') ELSE parent_id END,
  slug       = COALESCE(sqlc.narg('slug'), slug),
  title      = COALESCE(sqlc.narg('title'), title),
  body_md    = COALESCE(sqlc.narg('body_md'), body_md),
  sort_order = COALESCE(sqlc.narg('sort_order'), sort_order),
  updated_by = sqlc.narg('updated_by'),
  version    = version + 1
WHERE id = @id AND version = @version;

-- DELETE は物理削除で、部分木ごと消える（parent_id の CASCADE。10.4 / 8.1.1）。
-- document_revision も CASCADE で一緒に消える。
-- name: DeleteDocument :execrows
DELETE FROM document WHERE id = @id;

-- IsDocumentDescendant は 10.4 の cycle 検出。
--
-- **自分自身を含む。** 起点を UNION の第1項に置いてあるので、「自分自身または
-- 自分の子孫を parent_path に指定した」（10.4）を1文で判定できる。
-- ticket.sql の IsTicketDescendant と同じ形である。
-- name: IsDocumentDescendant :one
WITH RECURSIVE subtree AS (
  SELECT root.id FROM document root WHERE root.id = @ancestor_id
  UNION
  SELECT c.id FROM document c JOIN subtree s ON c.parent_id = s.id
)
SELECT (count(d.id) > 0)::boolean AS is_descendant
  FROM document d
 WHERE d.id = @candidate_id AND d.id IN (SELECT id FROM subtree);

-- ── リビジョン（ApiDesign.md 10.5）──────────────────────────

-- NextDocumentRevisionNo は次の版番号。uq_document_revision (document_id,
-- revision_no) があるので、競合しても2件目が一意制約で落ちる。
-- name: NextDocumentRevisionNo :one
SELECT COALESCE(max(revision_no), 0) + 1 FROM document_revision WHERE document_id = @document_id;

-- CreateDocumentRevision は 10.4 の「リビジョンを作る条件」に当たったときだけ呼ぶ。
--
-- **POST は revision_no = 1 を作る**（10.4）。リビジョンは「その変更のあとの本文」を
-- 持つので、作成時の1件が無いと最初の編集で「作ったときの本文」が残らない。
-- name: CreateDocumentRevision :exec
INSERT INTO document_revision (
  id, document_id, revision_no, title, body_md, changed_by, change_reason
) VALUES (
  @id, @document_id, @revision_no, @title, @body_md,
  sqlc.narg('changed_by'), sqlc.narg('change_reason')
);

-- ListDocumentRevisions は履歴の一覧（10.5）。
--
-- **body_md を含めない。** 20件ぶんの Markdown を載せると応答が重くなる（10.5）。
-- 本文が要るときは GetDocumentRevision を呼ぶ。
--
-- **revision_no の降順に固定**（10.5）。sort / order を受け付けない。
-- total は window 関数で同じ1回の走査から取る（comment.sql と同じ形）。
-- name: ListDocumentRevisions :many
SELECT
  r.revision_no,
  r.title,
  r.changed_by,
  a.kind         AS changed_by_kind,
  a.display_name AS changed_by_name,
  r.change_reason,
  r.created_at,
  count(*) OVER () AS total
FROM document_revision r
LEFT JOIN actor a ON a.id = r.changed_by
WHERE r.document_id = @document_id
ORDER BY r.revision_no DESC
LIMIT @page_limit OFFSET @page_offset;

-- GetDocumentRevision は1件ぶんの本文（10.5）。
-- name: GetDocumentRevision :one
SELECT
  r.revision_no,
  r.title,
  r.body_md,
  r.changed_by,
  a.kind         AS changed_by_kind,
  a.display_name AS changed_by_name,
  r.change_reason,
  r.created_at
FROM document_revision r
LEFT JOIN actor a ON a.id = r.changed_by
WHERE r.document_id = @document_id AND r.revision_no = @revision_no;

-- ListDocumentTemplates はプロジェクト作成時に複製する文書テンプレートを返す
-- （DbDesign.md 8.1.2、ApiDesign.md 5.3）。呼ぶのは internal/project だけである。
--
-- **並びが複製の順序をそのまま決める。** parent_id NULLS FIRST で親が必ず子より先に
-- 来るので、呼び出し側は届いた順に1件ずつ作りながら旧 id → 新 id の対応を貯めるだけで
-- parent_id を張り替えられる（8.1.2「複製は木として行う」）。同じ親の中の並びは
-- ListDocumentTree と揃えて sort_order, slug の昇順。
--
-- **いまテンプレートは4件ともトップレベルだが、それに依存しない。**
-- uq_document_template_slug が parent_id を含んでおり、テンプレート側は子を持てる。
-- name: ListDocumentTemplates :many
SELECT
  d.id,
  d.parent_id,
  d.slug,
  d.title,
  d.body_md,
  d.sort_order
FROM document d
WHERE d.is_template AND d.template_key = @template_key
ORDER BY d.parent_id NULLS FIRST, d.sort_order, d.slug;
