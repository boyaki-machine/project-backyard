-- Recorded operations for currently existing story/task tickets (ApiDesign.md 9.13.3).
-- Generate calendar dates before converting to instants, including DST boundaries.
-- name: GetProjectTicketTrends :many
WITH dates AS (
  SELECT p.timezone, sqlc.arg(as_of)::timestamptz AS as_of,
         to_char((sqlc.arg(as_of)::timestamptz AT TIME ZONE p.timezone)::date
                   - (sqlc.arg(days)::int - 1) + series.n, 'YYYY-MM-DD')::text AS date,
         (series.n = sqlc.arg(days)::int - 1)::boolean AS is_partial
  FROM project p CROSS JOIN generate_series(0, sqlc.arg(days)::int - 1) AS series(n)
  WHERE p.id = sqlc.arg(project_id)::char(26)
), recorded AS (
  SELECT to_char(a.occurred_at AT TIME ZONE p.timezone, 'YYYY-MM-DD')::text AS date,
         a.action, old_status.category AS old_category, new_status.category AS new_category
  FROM project p
  JOIN activity a ON a.project_id = p.id AND a.entity_type = 'ticket'
  JOIN ticket t ON t.id = a.entity_id AND t.project_id = p.id
               AND t.type IN ('story', 'task')
  LEFT JOIN workflow_status old_status
    ON old_status.workflow_id = p.workflow_id AND old_status.key = a.old_value
  LEFT JOIN workflow_status new_status
    ON new_status.workflow_id = p.workflow_id AND new_status.key = a.new_value
  WHERE p.id = sqlc.arg(project_id)::char(26)
    AND a.occurred_at >= (((sqlc.arg(as_of)::timestamptz AT TIME ZONE p.timezone)::date
                           - (sqlc.arg(days)::int - 1))::timestamp AT TIME ZONE p.timezone)
    AND a.occurred_at < sqlc.arg(as_of)::timestamptz
    AND (a.action = 'create' OR (a.action = 'transition' AND a.field = 'status_key'))
)
SELECT d.date::text AS date, d.timezone::text AS timezone,
       d.as_of::timestamptz AS as_of, d.is_partial::boolean AS is_partial,
       count(*) FILTER (WHERE r.action = 'create')::bigint AS created,
       count(*) FILTER (WHERE r.action = 'transition'
           AND r.old_category <> 'done' AND r.new_category = 'done')::bigint AS completed,
       count(*) FILTER (WHERE r.action = 'transition'
           AND r.old_category = 'done' AND r.new_category <> 'done')::bigint AS reopened,
       count(*) FILTER (WHERE r.action = 'transition'
           AND (r.old_category IS NULL OR r.new_category IS NULL))::bigint AS unclassified_transitions
FROM dates d LEFT JOIN recorded r ON r.date = d.date
GROUP BY d.date, d.timezone, d.as_of, d.is_partial
ORDER BY d.date;
