-- スプリントに関するクエリ（DbDesign.md 6.9、ApiDesign.md 9.12）。
--
-- 手順16a で追加。プロジェクト設定のスプリントタブ（GuiDesign.md 5.9.5）が
-- 消費者で、手順17 のチケット詳細サイドバーが選択肢として同じ一覧を読む。
--
-- **Phase 1 で開けるのは定義だけ**である。バーンダウン・ベロシティを含む
-- スプリント管理画面は Phase 2（GuiDesign.md 10章）。

-- items[] は start_date 降順（NULL は末尾）、同値は created_at 降順
-- （ApiDesign.md 9.12）。新しいものが上に来る並びで、5.9.5 の図と一致する。
--
-- closed_count は closed_at IS NOT NULL で数える。status_category = 'done'
-- では数えない——9.13 の open / overdue が closed_at を基準にしており、
-- closed_at は遷移の副作用としてのみ動く（DbDesign.md 6.6）ため、
-- ワークフローの定義が違うプロジェクトでも意味が変わらない。
-- name: ListSprintsByProject :many
SELECT
  s.id,
  s.name,
  s.goal,
  s.start_date,
  s.end_date,
  s.status,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id)::bigint AS ticket_count,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id AND t.closed_at IS NOT NULL)::bigint AS closed_count
FROM sprint s
WHERE s.project_id = @project_id
ORDER BY s.start_date DESC NULLS LAST, s.created_at DESC;

-- 1件だけ返す形。POST / PATCH の応答（B-2）で使う。
-- name: GetSprintByID :one
SELECT
  s.id,
  s.name,
  s.goal,
  s.start_date,
  s.end_date,
  s.status,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id)::bigint AS ticket_count,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id AND t.closed_at IS NOT NULL)::bigint AS closed_count
FROM sprint s
WHERE s.project_id = @project_id AND s.id = @id;

-- name: CreateSprint :exec
INSERT INTO sprint (id, project_id, name, goal, start_date, end_date, status)
VALUES (@id, @project_id, @name, @goal, @start_date, @end_date, @status);

-- COALESCE による部分更新。goal / start_date / end_date は NULL を
-- 「値として設定する」ことがある（欄を空にする操作）ため、送られたかどうかを
-- COALESCE では区別できない。**明示的なフラグ引数で分ける**
-- （users_update.go の同種の扱いに揃える）。
-- name: UpdateSprint :execrows
UPDATE sprint SET
  name       = COALESCE(sqlc.narg('name'), name),
  goal       = CASE WHEN @set_goal::boolean       THEN sqlc.narg('goal')       ELSE goal END,
  start_date = CASE WHEN @set_start_date::boolean THEN sqlc.narg('start_date') ELSE start_date END,
  end_date   = CASE WHEN @set_end_date::boolean   THEN sqlc.narg('end_date')   ELSE end_date END,
  status     = COALESCE(sqlc.narg('status'), status)
WHERE project_id = @project_id AND id = @id;

-- ticket.sprint_id は fk_ticket_sprint の ON DELETE SET NULL で外れる
-- （DbDesign.md 6.9）。チケットは消えず、スプリント未設定に戻る。
-- name: DeleteSprint :execrows
DELETE FROM sprint WHERE project_id = @project_id AND id = @id;
