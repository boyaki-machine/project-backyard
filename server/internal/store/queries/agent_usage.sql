-- 提出済みの実行と評価の実績。ApiDesign.md 9.16。

-- name: ListAgentUsageEvents :many
WITH events (event_kind, event_id, ticket_id, seq, occurred_at, result_status, ticket_status_category, client_kind, model_name, model_version, tokens, turns, wall_clock_min, turns_known, time_known) AS (
 SELECT 'run'::text AS event_kind, r.id AS event_id, t.id AS ticket_id, t.seq,
        CASE WHEN r.status IN ('failed','abandoned') THEN r.ended_at ELSE COALESCE(ar.submitted_at,r.ended_at) END::timestamptz AS occurred_at, CASE WHEN r.status IN ('failed','abandoned') THEN r.status ELSE COALESCE(ar.status,r.status) END::text AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category,
        r.client_kind, r.model_name, r.model_version,
        COALESCE(r.tokens_used,(ar.report->'cost'->>'tokens')::bigint) AS tokens, COALESCE(r.turns,(ar.report->'cost'->>'turns')::bigint,0)::bigint AS turns,
        COALESCE((ar.report->'cost'->>'wall_clock_min')::bigint,0)::bigint AS wall_clock_min,
        (r.turns IS NOT NULL OR (ar.report->'cost'->>'turns') IS NOT NULL)::boolean AS turns_known, ((ar.report->'cost'->>'wall_clock_min') IS NOT NULL)::boolean AS time_known
 FROM agent_run r
 LEFT JOIN agent_report ar ON ar.agent_run_id = r.id
 JOIN ticket t ON t.id = r.ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id AND r.ended_at IS NOT NULL
 UNION ALL
 SELECT 'evaluation'::text AS event_kind, e.id AS event_id, t.id AS ticket_id, t.seq, e.created_at AS occurred_at, e.verdict AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category, e.evaluator_client_kind AS client_kind, e.evaluator_model_name AS model_name, e.evaluator_model_version AS model_version,
        e.tokens_used AS tokens, 0::bigint AS turns, 0::bigint AS wall_clock_min, false AS turns_known, false AS time_known
 FROM evaluation e
 JOIN delegation d ON d.id = e.delegation_id
 JOIN ticket t ON t.id = d.root_ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id
), filtered AS (
 SELECT * FROM events
 WHERE occurred_at >= @from_at::timestamptz AND occurred_at < @until_at::timestamptz
 AND (sqlc.narg('client_kind')::text IS NULL OR client_kind = sqlc.narg('client_kind'))
 AND (sqlc.narg('model_name')::text IS NULL OR model_name = sqlc.narg('model_name'))
 AND (sqlc.narg('model_version')::text IS NULL OR model_version = sqlc.narg('model_version'))
)
SELECT * FROM filtered ORDER BY occurred_at DESC, event_id DESC;

-- name: ListAgentUsageRuns :many
WITH events (event_kind, event_id, ticket_id, seq, occurred_at, result_status, ticket_status_category, client_kind, model_name, model_version, tokens, turns, wall_clock_min, turns_known, time_known) AS (
 SELECT 'run'::text AS event_kind, r.id AS event_id, t.id AS ticket_id, t.seq,
        CASE WHEN r.status IN ('failed','abandoned') THEN r.ended_at ELSE COALESCE(ar.submitted_at,r.ended_at) END::timestamptz AS occurred_at, CASE WHEN r.status IN ('failed','abandoned') THEN r.status ELSE COALESCE(ar.status,r.status) END::text AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category,
        r.client_kind, r.model_name, r.model_version,
        COALESCE(r.tokens_used,(ar.report->'cost'->>'tokens')::bigint) AS tokens, COALESCE(r.turns,(ar.report->'cost'->>'turns')::bigint,0)::bigint AS turns,
        COALESCE((ar.report->'cost'->>'wall_clock_min')::bigint,0)::bigint AS wall_clock_min,
        (r.turns IS NOT NULL OR (ar.report->'cost'->>'turns') IS NOT NULL)::boolean AS turns_known, ((ar.report->'cost'->>'wall_clock_min') IS NOT NULL)::boolean AS time_known
 FROM agent_run r
 LEFT JOIN agent_report ar ON ar.agent_run_id = r.id
 JOIN ticket t ON t.id = r.ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id AND r.ended_at IS NOT NULL
 UNION ALL
 SELECT 'evaluation'::text AS event_kind, e.id AS event_id, t.id AS ticket_id, t.seq, e.created_at AS occurred_at, e.verdict AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category, e.evaluator_client_kind AS client_kind, e.evaluator_model_name AS model_name, e.evaluator_model_version AS model_version,
        e.tokens_used AS tokens, 0::bigint AS turns, 0::bigint AS wall_clock_min, false AS turns_known, false AS time_known
 FROM evaluation e
 JOIN delegation d ON d.id = e.delegation_id
 JOIN ticket t ON t.id = d.root_ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id
), filtered AS (
 SELECT * FROM events
 WHERE occurred_at >= @from_at::timestamptz AND occurred_at < @until_at::timestamptz
 AND (sqlc.narg('client_kind')::text IS NULL OR client_kind = sqlc.narg('client_kind'))
 AND (sqlc.narg('model_name')::text IS NULL OR model_name = sqlc.narg('model_name'))
 AND (sqlc.narg('model_version')::text IS NULL OR model_version = sqlc.narg('model_version'))
)
SELECT * FROM filtered WHERE event_kind = 'run' ORDER BY occurred_at DESC, event_id DESC LIMIT @page_limit OFFSET @page_offset;

-- name: CountAgentUsageRuns :one
WITH events (event_kind, event_id, ticket_id, seq, occurred_at, result_status, ticket_status_category, client_kind, model_name, model_version, tokens, turns, wall_clock_min, turns_known, time_known) AS (
 SELECT 'run'::text AS event_kind, r.id AS event_id, t.id AS ticket_id, t.seq,
        CASE WHEN r.status IN ('failed','abandoned') THEN r.ended_at ELSE COALESCE(ar.submitted_at,r.ended_at) END::timestamptz AS occurred_at, CASE WHEN r.status IN ('failed','abandoned') THEN r.status ELSE COALESCE(ar.status,r.status) END::text AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category,
        r.client_kind, r.model_name, r.model_version,
        COALESCE(r.tokens_used,(ar.report->'cost'->>'tokens')::bigint) AS tokens, COALESCE(r.turns,(ar.report->'cost'->>'turns')::bigint,0)::bigint AS turns,
        COALESCE((ar.report->'cost'->>'wall_clock_min')::bigint,0)::bigint AS wall_clock_min,
        (r.turns IS NOT NULL OR (ar.report->'cost'->>'turns') IS NOT NULL)::boolean AS turns_known, ((ar.report->'cost'->>'wall_clock_min') IS NOT NULL)::boolean AS time_known
 FROM agent_run r
 LEFT JOIN agent_report ar ON ar.agent_run_id = r.id
 JOIN ticket t ON t.id = r.ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id AND r.ended_at IS NOT NULL
 UNION ALL
 SELECT 'evaluation'::text AS event_kind, e.id AS event_id, t.id AS ticket_id, t.seq, e.created_at AS occurred_at, e.verdict AS result_status,
        COALESCE(ws.category,'unknown')::text AS ticket_status_category, e.evaluator_client_kind AS client_kind, e.evaluator_model_name AS model_name, e.evaluator_model_version AS model_version,
        e.tokens_used AS tokens, 0::bigint AS turns, 0::bigint AS wall_clock_min, false AS turns_known, false AS time_known
 FROM evaluation e
 JOIN delegation d ON d.id = e.delegation_id
 JOIN ticket t ON t.id = d.root_ticket_id
 JOIN project p ON p.id = t.project_id
 LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.project_id = @project_id
), filtered AS (
 SELECT * FROM events
 WHERE occurred_at >= @from_at::timestamptz AND occurred_at < @until_at::timestamptz
 AND (sqlc.narg('client_kind')::text IS NULL OR client_kind = sqlc.narg('client_kind'))
 AND (sqlc.narg('model_name')::text IS NULL OR model_name = sqlc.narg('model_name'))
 AND (sqlc.narg('model_version')::text IS NULL OR model_version = sqlc.narg('model_version'))
)
SELECT count(*) FROM filtered WHERE event_kind = 'run';
