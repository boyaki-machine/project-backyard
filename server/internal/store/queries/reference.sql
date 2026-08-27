-- チケットの外部参照（DbDesign.md 6.12、ApiDesign.md 9.10.2）。
--
-- 手順17c で追加。チケット詳細（GuiDesign.md 5.5）の「コード」「参考リンク」の
-- 2セクションが消費者で、詳細応答（9.5.1）の references も同じ一覧を読む。
--
-- **すべてのクエリが ticket_id で閉じている。** 外部参照はチケットの子資源であり、
-- 他チケットの ID を渡されても行が返らないようにするためである。チケットが
-- そのプロジェクトのものかは FindTicketIDBySeq が済ませており、到達可否
-- （メンバーか）は RequireProjectPermission が済ませている（Design.md 6.4.5）。
--
-- **created_by は LEFT JOIN で引く。** actor は ON DELETE SET NULL なので、
-- 書き手を消した後も参照の行は残る（作業が起きた事実は消えない）。

-- items[] は kind 昇順（code → doc）、同じ kind の中は sort_order → created_at の
-- 昇順（ApiDesign.md 9.10.2）。第2・第3キーを置くのは、9.11 と同じく順序が
-- 実行ごとに揺れないようにするためである。
--
-- kind は 'code' / 'doc' の2値しか取らないので、照合順によらず code が先に来る。
-- name: ListTicketReferences :many
SELECT
  r.id,
  r.kind,
  r.label,
  r.url,
  r.repository,
  r.branch,
  r.commit_sha,
  r.note,
  r.sort_order,
  r.created_at,
  r.updated_at,
  r.created_by,
  a.kind         AS created_by_kind,
  a.display_name AS created_by_name
FROM ticket_reference r
LEFT JOIN actor a ON a.id = r.created_by
WHERE r.ticket_id = @ticket_id
ORDER BY r.kind, r.sort_order, r.created_at;

-- 1件だけ返す形。POST / PATCH の応答（ApiDesign.md 9.10.2）で使う。
-- name: GetTicketReference :one
SELECT
  r.id,
  r.kind,
  r.label,
  r.url,
  r.repository,
  r.branch,
  r.commit_sha,
  r.note,
  r.sort_order,
  r.created_at,
  r.updated_at,
  r.created_by,
  a.kind         AS created_by_kind,
  a.display_name AS created_by_name
FROM ticket_reference r
LEFT JOIN actor a ON a.id = r.created_by
WHERE r.ticket_id = @ticket_id AND r.id = @id;

-- sort_order 省略時の既定（現在の最大値 + 10）。行が無ければ 10 から始める。
--
-- **kind をまたいで1本の連番にする。** 並びの第1キーは kind であり（9.10.2）、
-- sort_order は同じ kind の中でしか比較されないためである。kind ごとに
-- 数え直す利得は無く、クエリが1本増えるだけになる。
-- name: NextTicketReferenceSortOrder :one
SELECT COALESCE(max(sort_order), 0) + 10 FROM ticket_reference WHERE ticket_id = @ticket_id;

-- name: CreateTicketReference :exec
INSERT INTO ticket_reference (
  id, ticket_id, kind, label, url, repository, branch, commit_sha, note,
  created_by, sort_order
) VALUES (
  @id, @ticket_id, @kind, @label, @url, @repository, @branch, @commit_sha, @note,
  @created_by, @sort_order
);

-- 部分更新（ApiDesign.md 9.10.2 の PATCH）。
--
-- **NULL 可の項目は `<列>_set` で「送られたか」を分ける。** COALESCE だけでは
-- 「null を送って空にする」と「キーごと送らない」が区別できない。UpdateTicket
-- （9.5.2）と同じ形である。
--
-- **kind は含めない。** 作成後は変えられない（9.10.2 が immutable_field と定める）。
-- sort_order は NOT NULL なので COALESCE で足りる。
-- name: UpdateTicketReference :execrows
UPDATE ticket_reference SET
  label      = CASE WHEN @label_set::boolean      THEN sqlc.narg('label')      ELSE label      END,
  url        = CASE WHEN @url_set::boolean        THEN sqlc.narg('url')        ELSE url        END,
  repository = CASE WHEN @repository_set::boolean THEN sqlc.narg('repository') ELSE repository END,
  branch     = CASE WHEN @branch_set::boolean     THEN sqlc.narg('branch')     ELSE branch     END,
  commit_sha = CASE WHEN @commit_sha_set::boolean THEN sqlc.narg('commit_sha') ELSE commit_sha END,
  note       = CASE WHEN @note_set::boolean       THEN sqlc.narg('note')       ELSE note       END,
  sort_order = COALESCE(sqlc.narg('sort_order'), sort_order)
WHERE ticket_id = @ticket_id AND id = @id;

-- name: DeleteTicketReference :execrows
DELETE FROM ticket_reference WHERE ticket_id = @ticket_id AND id = @id;
