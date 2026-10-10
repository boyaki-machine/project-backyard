-- プロジェクトの集計（ApiDesign.md 9.13.1、GuiDesign.md 5.3 のダッシュボード）。
--
-- **status ではなく status_category で数える**（9.13.1）。ワークフローが
-- プロジェクトごとに違っても、画面の4枚のカードの意味が変わらないようにするため
-- である。カテゴリは workflow_status 側にあり、ticket.status_key から引く。

-- GetProjectTicketStats は全体値とオンステージ内の状態件数を1SQL・1行で返す（pb-178）。
--
-- **1文にまとめ、全体と段内を同じスナップショットで数える。**
-- 親子・棚の判定を先に計算し、FILTER 句が数え分ける。
--
-- **workflow_status は LEFT JOIN である。** project.workflow_id は NULL 可能で
-- （DbDesign.md 6.4）、ステータスキーがワークフローに無いこともありうる。その行は
-- **4つのカテゴリのどれにも数えられないが total には入る**——つまり
-- by_category の合計は total と一致しないことがある。合わせに行かないのは、
-- 「分類できないチケットがある」ことを 0 で塗り潰さないためである。
--
-- **overdue は瞬間の比較**（due_at <= now()。pb-217）。9.2.1 の overdue と同じ条件にする。
-- @stale_days は 9.13.1 が 14 に固定した閾値で、応答にも載せて画面へ渡す。
--
-- **overdue / stale / unassigned はいずれも closed_at IS NULL が掛かる**
-- （9.13.1）。完了したチケットは要対応ではない。
--
-- **エピックはどの項目にも数えない**（9.13.1。手順19b で足した条件）。エピックは
-- グルーピング専用で（DbDesign.md 6.10）、GuiDesign.md 5.4 はバックログの行としても
-- 出さない。**4枚のカードは押すとバックログをそのカテゴリで絞って開く**ため、
-- 除かないとカードの数と押した先の件数が一致しない。unassigned にエピックが混ざる
-- のも実害がある——エピックに担当者を置く運用が無いので、常に「要対応」に見える。
--
-- name: GetProjectTicketStats :one
WITH RECURSIVE open_desc AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::pg_catalog.bpchar
     AND closed_at IS NULL AND type <> 'epic'
  UNION
  SELECT c.id FROM ticket c JOIN open_desc o ON c.parent_id = o.id
   WHERE c.project_id = @project_id::pg_catalog.bpchar AND c.type <> 'epic'
), staged_tree AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::pg_catalog.bpchar
     AND staged_at IS NOT NULL AND type <> 'epic'
  UNION
  SELECT c.id FROM ticket c JOIN staged_tree s ON c.parent_id = s.id
   WHERE c.project_id = @project_id::pg_catalog.bpchar AND c.type <> 'epic'
), eligible AS (
  -- ListTickets と同じ部分木・棚除外。状態で絞る前に全親子関係をたどる。
  SELECT t.*, ws.category,
    (t.id IN (SELECT id FROM staged_tree)
     AND NOT (t.closed_at IS NOT NULL
       AND EXISTS (SELECT 1 FROM sprint rs WHERE rs.id = t.sprint_id AND rs.status = 'completed')
       AND t.id NOT IN (SELECT id FROM open_desc))) AS is_onstage
  FROM ticket t
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
  WHERE t.project_id = @project_id::pg_catalog.bpchar AND t.type <> 'epic'
)
SELECT
  count(*) FILTER (WHERE t.category = 'todo')::bigint        AS todo,
  count(*) FILTER (WHERE t.category = 'in_progress')::bigint AS in_progress,
  count(*) FILTER (WHERE t.category = 'review')::bigint      AS review,
  count(*) FILTER (WHERE t.category = 'done')::bigint        AS done,
  count(*)::bigint                                            AS total,
  count(*) FILTER (WHERE t.closed_at IS NULL)::bigint         AS open_count,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.due_at IS NOT NULL
                     AND t.due_at <= now())::bigint           AS overdue,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.updated_at
                         < now() - make_interval(days => @stale_days::int))::bigint AS stale,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.assignee_id IS NULL)::bigint       AS unassigned,
  count(*) FILTER (WHERE t.is_onstage AND t.category = 'todo')::bigint AS onstage_todo,
  count(*) FILTER (WHERE t.is_onstage AND t.category = 'in_progress')::bigint AS onstage_in_progress,
  count(*) FILTER (WHERE t.is_onstage AND t.category = 'review')::bigint AS onstage_review,
  count(*) FILTER (WHERE t.is_onstage AND t.category = 'done')::bigint AS onstage_done,
  count(*) FILTER (WHERE t.is_onstage)::bigint AS onstage_total,
  count(*) FILTER (WHERE t.is_onstage AND t.category IS NULL)::bigint AS onstage_unknown
FROM eligible t;
