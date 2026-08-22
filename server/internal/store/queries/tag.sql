-- タグに関するクエリ（DbDesign.md 6.10、ApiDesign.md 9.11）。
--
-- 手順16a で追加。プロジェクト設定のタグタブ（GuiDesign.md 5.9.4）が消費者で、
-- 手順16b のバックログがグループ化の軸として同じ一覧を読む。
--
-- **すべてのクエリが project_id で閉じている。** タグはプロジェクトの資源であり、
-- 他プロジェクトの ID を渡されても行が返らないようにするためである。到達可否
-- （メンバーか）の判定は RequireProjectPermission が済ませている（Design.md 6.4.5）。

-- items[] は sort_order 昇順、同値は name 昇順（ApiDesign.md 9.11）。
-- 第2キーを置くのは、sort_order が重複したときに順序が実行ごとに揺れないため。
-- name は ja-JP-x-icu で比較する（DbDesign.md 4.4 の既定照合順）。
--
-- ticket_count は削除確認ダイアログが出す「12件のチケットで使われています」
-- （GuiDesign.md 5.9.4 / 6.3）。LEFT JOIN + COUNT ではなく相関副問い合わせに
-- するのは、タグが数十件で、行ごとに1回引いても差が出ないためである。
-- name: ListTagsByProject :many
SELECT
  t.id,
  t.name,
  t.sort_order,
  (SELECT count(*) FROM ticket_tag tt WHERE tt.tag_id = t.id)::bigint AS ticket_count
FROM tag t
WHERE t.project_id = @project_id
ORDER BY t.sort_order, t.name;

-- 1件だけ返す形。POST / PATCH の応答（ApiDesign.md 9.11、B-2）で使う。
-- name: GetTagByID :one
SELECT
  t.id,
  t.name,
  t.sort_order,
  (SELECT count(*) FROM ticket_tag tt WHERE tt.tag_id = t.id)::bigint AS ticket_count
FROM tag t
WHERE t.project_id = @project_id AND t.id = @id;

-- sort_order 省略時の既定（現在の最大値 + 10）。行が無ければ 10 から始める。
-- 10刻みにするのは、並べ替え（9.11.1）が同じ間隔で振り直すためである。
-- name: NextTagSortOrder :one
SELECT COALESCE(max(sort_order), 0) + 10 FROM tag WHERE project_id = @project_id;

-- name: CreateTag :exec
INSERT INTO tag (id, project_id, name, sort_order)
VALUES (@id, @project_id, @name, @sort_order);

-- COALESCE による部分更新。sqlc.narg は NULL 可の引数を作る（PATCH の
-- 「送られたフィールドだけ変える」を1文で表すため。users_update と同じ型）。
-- name: UpdateTag :execrows
UPDATE tag SET
  name       = COALESCE(sqlc.narg('name'), name),
  sort_order = COALESCE(sqlc.narg('sort_order'), sort_order)
WHERE project_id = @project_id AND id = @id;

-- ticket_tag は ON DELETE CASCADE で追従する（DbDesign.md 6.10）。
-- 使用中でも削除できる。禁止すると、要らなくなった分類を消すために
-- 全チケットから手で外すことになる（ApiDesign.md 9.11）。
-- name: DeleteTag :execrows
DELETE FROM tag WHERE project_id = @project_id AND id = @id;
