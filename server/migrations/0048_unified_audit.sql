-- pb-181: チケット履歴を管理者の横断監査一覧から読めるようにする。
-- 記録時の実行者と対象を残し、actor/ticket の削除後も識別できるようにする。
-- +goose Up
ALTER TABLE activity
  ADD COLUMN actor_kind text,
  ADD COLUMN actor_name text,
  ADD COLUMN target_label text;

UPDATE activity a SET actor_kind = ac.kind, actor_name = ac.display_name
FROM actor ac WHERE a.actor_id = ac.id;

UPDATE activity a SET target_label = p.key || '-' || t.seq::text
FROM ticket t JOIN project p ON p.id = t.project_id
WHERE a.entity_type = 'ticket' AND a.entity_id = t.id;

CREATE VIEW audit_event AS
SELECT
  l.id, l.occurred_at, l.actor_id, l.actor_kind, l.actor_label,
  l.token_id, l.ip, l.user_agent, l.action, l.target_type, l.target_id,
  CASE WHEN l.target_type = 'project' THEN l.detail->>'key' ELSE NULL END AS target_label,
  l.result, l.detail, l.request_id,
  CASE
    WHEN l.action LIKE 'project.%' OR l.target_type = 'project_member' THEN 'project'
    WHEN l.action = 'setting.update' OR l.action LIKE 'tls.%' OR l.action LIKE 'database.%' THEN 'application'
    ELSE 'security'
  END AS category
FROM audit_log l
UNION ALL
SELECT
  a.id, a.occurred_at, a.actor_id, a.actor_kind, a.actor_name AS actor_label,
  NULL::char(26) AS token_id, NULL::inet AS ip, NULL::text AS user_agent,
  'ticket.' || a.action AS action, a.entity_type AS target_type, a.entity_id AS target_id,
  a.target_label, 'success'::text AS result,
  jsonb_build_object('field', a.field, 'old_value', a.old_value,
                     'new_value', a.new_value, 'project_key', p.key) AS detail,
  a.request_id, 'ticket'::text AS category
FROM activity a JOIN project p ON p.id = a.project_id
WHERE a.entity_type = 'ticket';
