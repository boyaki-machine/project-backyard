-- pb-205: 見積ポイントと同じ単位の実績、算出式の版、PM エージェント用の権限。
-- +goose Up
ALTER TABLE ticket
  ADD COLUMN actual_point double precision,
  ADD COLUMN actual_point_version text,
  ADD CONSTRAINT ck_ticket_actual_point CHECK (
    (actual_point IS NULL AND actual_point_version IS NULL)
    OR (actual_point >= 0 AND actual_point_version ~ '^actual-v[0-9]+$')
  );

INSERT INTO permission (key, category, description, sort_order) VALUES
  ('ticket.actual_point.edit', 'ticket', '実績ポイントと算出式の版の記録', 29)
ON CONFLICT (key) DO NOTHING;

INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', 'ticket.actual_point.edit'
ON CONFLICT DO NOTHING;
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', 'ticket.actual_point.edit'
ON CONFLICT DO NOTHING;

-- 既存の「実績 v0（たたき台）」コメントから、計測できた値だけを移す。
-- 同一チケットに複数あれば最新を採る。JSON の point=null は9件あり、空欄のままにする。
-- 元コメントは根拠として残し、既存の手入力値は上書きしない。
WITH source AS (
  SELECT DISTINCT ON (c.ticket_id)
    c.ticket_id,
    trim(split_part(split_part(c.body_md, '```json', 2), '```', 1))::jsonb AS data
  FROM comment c
  WHERE c.kind = 'reference' AND c.deleted_at IS NULL
    AND c.body_md LIKE '**実績 v0（たたき台）%'
  ORDER BY c.ticket_id, c.created_at DESC
)
UPDATE ticket t
SET actual_point = (s.data->>'point')::double precision,
    actual_point_version = s.data->>'method',
    version = t.version + 1
FROM source s
WHERE t.id = s.ticket_id AND t.closed_at IS NOT NULL
  AND t.actual_point IS NULL
  AND s.data->>'method' = 'actual-v0'
  AND jsonb_typeof(s.data->'point') = 'number';
