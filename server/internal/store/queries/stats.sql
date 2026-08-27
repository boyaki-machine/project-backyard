-- プロジェクトの集計（ApiDesign.md 9.13.1、GuiDesign.md 5.3 のダッシュボード）。
--
-- **status ではなく status_category で数える**（9.13.1）。ワークフローが
-- プロジェクトごとに違っても、画面の4枚のカードの意味が変わらないようにするため
-- である。カテゴリは workflow_status 側にあり、ticket.status_key から引く。

-- GetProjectTicketStats は 9.13.1 の応答をまるごと1行で返す。
--
-- **1文にまとめてあるのは、同じ WHERE を9回書かないため**である。走査は
-- 1プロジェクト分のチケット1回で済み、FILTER 句が数え分ける。
--
-- **workflow_status は LEFT JOIN である。** project.workflow_id は NULL 可能で
-- （DbDesign.md 6.4）、ステータスキーがワークフローに無いこともありうる。その行は
-- **4つのカテゴリのどれにも数えられないが total には入る**——つまり
-- by_category の合計は total と一致しないことがある。合わせに行かないのは、
-- 「分類できないチケットがある」ことを 0 で塗り潰さないためである。
--
-- **「今日」は CURRENT_DATE**（9.13.1）。9.2.1 の due_within と同じ基準にする。
-- @stale_days は 9.13.1 が 14 に固定した閾値で、応答にも載せて画面へ渡す。
--
-- **overdue / stale / unassigned はいずれも closed_at IS NULL が掛かる**
-- （9.13.1）。完了したチケットは要対応ではない。
--
-- name: GetProjectTicketStats :one
SELECT
  count(*) FILTER (WHERE ws.category = 'todo')::bigint        AS todo,
  count(*) FILTER (WHERE ws.category = 'in_progress')::bigint AS in_progress,
  count(*) FILTER (WHERE ws.category = 'review')::bigint      AS review,
  count(*) FILTER (WHERE ws.category = 'done')::bigint        AS done,
  count(*)::bigint                                            AS total,
  count(*) FILTER (WHERE t.closed_at IS NULL)::bigint         AS open_count,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.due_date IS NOT NULL
                     AND t.due_date < CURRENT_DATE)::bigint   AS overdue,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.updated_at
                         < now() - make_interval(days => @stale_days::int))::bigint AS stale,
  count(*) FILTER (WHERE t.closed_at IS NULL
                     AND t.assignee_id IS NULL)::bigint       AS unassigned
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws
       ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE t.project_id = @project_id::text;
