-- pb-209: 文書ごとにコンテキストパックへの掲載方法を選ぶ。
-- 前進のみ。既存プロジェクトの掲載内容を保ってから、パスによる決め打ちを外す。
-- +goose Up
ALTER TABLE document
  ADD COLUMN pack_mode text NOT NULL DEFAULT 'outline'
    CHECK (pack_mode IN ('full', 'outline', 'none'));

-- 旧動作は最上位の agent-onboarding を除外し、decisions を目次だけ、
-- それ以外を全文にする。移動済みの文書は旧動作どおり全文にする。
UPDATE document SET pack_mode = 'full', version = version + 1;

WITH RECURSIVE omitted AS (
  SELECT id FROM document WHERE parent_id IS NULL AND slug = 'agent-onboarding'
  UNION ALL
  SELECT child.id FROM document child JOIN omitted parent ON child.parent_id = parent.id
)
UPDATE document SET pack_mode = 'none', version = version + 1
WHERE id IN (SELECT id FROM omitted);

WITH RECURSIVE outlined AS (
  SELECT id FROM document WHERE parent_id IS NULL AND slug = 'decisions'
  UNION ALL
  SELECT child.id FROM document child JOIN outlined parent ON child.parent_id = parent.id
)
UPDATE document SET pack_mode = 'outline', version = version + 1
WHERE id IN (SELECT id FROM outlined);
