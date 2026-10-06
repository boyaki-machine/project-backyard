-- 委任の予算（Delegation.md 4.2・12.1）。pb-253。
--
-- **予算の値は delegation.scope の budget に持ち、消費は既存の行から測る。**
-- 消費を別の表に積まない——積むと、測る元（起票・実行記録・評価）と
-- 食い違ったときにどちらが正か決められない。

-- GetDelegationBudgetUsage は委任1件の消費を1回で測る（12.1）。
--
--   tickets          木の子孫の数（根を含まない）
--   max_depth        根からの最大の段数（根は0）
--   probe_depth      probe_ticket_id の段数。木に無ければ -1（起票の前に、親の深さを見る）
--   tokens_total     木の中の実行記録と、この委任の評価のトークンの和
--   tokens_missing   トークンを申告しなかった件数（12.1「漏れた件数を要約に出す」）
--   tokens_this_month  今月（プロジェクトのタイムゾーンの暦月）の分
--   today            プロジェクトのタイムゾーンでの今日（YYYY-MM-DD）
--
-- **実行記録は委任を作った後のものだけを数える。** 根に委任より前の作業が
-- あっても、委任の予算から引かない。
-- name: GetDelegationBudgetUsage :one
WITH RECURSIVE subtree AS (
  SELECT t.id, 0 AS depth
  FROM ticket t
  WHERE t.id = (SELECT d0.root_ticket_id FROM delegation d0 WHERE d0.id = @delegation_id)
  UNION
  SELECT c.id, s.depth + 1
  FROM ticket c JOIN subtree s ON c.parent_id = s.id
),
deleg AS (
  SELECT d.created_at, p.timezone
  FROM delegation d JOIN project p ON p.id = d.project_id
  WHERE d.id = @delegation_id
),
usage AS (
  SELECT r.tokens_used, r.started_at AS at
  FROM agent_run r
  JOIN subtree s ON s.id = r.ticket_id
  CROSS JOIN deleg
  WHERE r.started_at >= deleg.created_at
  UNION ALL
  SELECT e.tokens_used, e.created_at AS at
  FROM evaluation e
  WHERE e.delegation_id = @delegation_id
),
cal AS (
  SELECT (date_trunc('month', now() AT TIME ZONE deleg.timezone) AT TIME ZONE deleg.timezone) AS month_start,
         to_char(now() AT TIME ZONE deleg.timezone, 'YYYY-MM-DD') AS today
  FROM deleg
),
tree_stats AS (
  SELECT count(*) FILTER (WHERE subtree.depth > 0)                              AS tickets,
         COALESCE(max(subtree.depth), 0)                                        AS max_depth,
         COALESCE(max(subtree.depth) FILTER (WHERE subtree.id = sqlc.arg(probe_ticket_id)::text), -1) AS probe_depth
  FROM subtree
),
usage_stats AS (
  SELECT COALESCE(sum(usage.tokens_used), 0)                                    AS tokens_total,
         count(*) FILTER (WHERE usage.tokens_used IS NULL)                      AS tokens_missing,
         COALESCE(sum(usage.tokens_used) FILTER (WHERE usage.at >= cal.month_start), 0) AS tokens_this_month
  FROM usage CROSS JOIN cal
)
SELECT
  tree_stats.tickets::bigint            AS tickets,
  tree_stats.max_depth::integer         AS max_depth,
  tree_stats.probe_depth::integer       AS probe_depth,
  usage_stats.tokens_total::bigint      AS tokens_total,
  usage_stats.tokens_missing::bigint    AS tokens_missing,
  usage_stats.tokens_this_month::bigint AS tokens_this_month,
  cal.today::text                       AS today
FROM tree_stats CROSS JOIN usage_stats CROSS JOIN cal;

-- UpdateDelegationScope は PM が再開のときに予算を直す（pb-253）。範囲を
-- 変えるので scope_version を1つ進める（4.4）。
-- name: UpdateDelegationScope :execrows
UPDATE delegation
SET scope = @scope, scope_version = scope_version + 1
WHERE id = @id;

-- GetDelegationForControl は停止・再開の判定に要る列を読む（pb-253）。
-- name: GetDelegationForControl :one
SELECT id, status, scope, scope_version
FROM delegation
WHERE root_ticket_id = @root_ticket_id
ORDER BY created_at DESC, id DESC
LIMIT 1;
