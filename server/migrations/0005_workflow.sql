-- 正本: DbDesign.md 6.5（ワークフロー）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 末尾で project.workflow_id の前方参照 FK を付与する（DbDesign.md 5.2）。

-- +goose Up

CREATE TABLE workflow (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  name       text        NOT NULL,
  definition jsonb       NOT NULL DEFAULT '{}'::jsonb,   -- インポート/エクスポート用の原本
  is_template boolean    NOT NULL DEFAULT false,
  template_key text,                                     -- 'simple' 等
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_workflow_template CHECK (
    (is_template AND project_id IS NULL AND template_key IS NOT NULL)
    OR (NOT is_template AND project_id IS NOT NULL)
  )
);
CREATE UNIQUE INDEX uq_workflow_template ON workflow (template_key) WHERE is_template;
CREATE TRIGGER trg_workflow_updated BEFORE UPDATE ON workflow
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workflow_status (
  id                      char(26) COLLATE "C" PRIMARY KEY,
  workflow_id             char(26) COLLATE "C" NOT NULL
                          REFERENCES workflow(id) ON DELETE CASCADE,
  key                     text    NOT NULL,
  name                    text    NOT NULL,
  category                text    NOT NULL
                          CHECK (category IN ('todo','in_progress','review','done')),
  sort_order              integer NOT NULL,
  requires_human_approval boolean NOT NULL DEFAULT false,
  is_agent_reachable      boolean NOT NULL DEFAULT true,
  CONSTRAINT uq_workflow_status_key UNIQUE (workflow_id, key)
);

CREATE TABLE workflow_transition (
  id                  char(26) COLLATE "C" PRIMARY KEY,
  workflow_id         char(26) COLLATE "C" NOT NULL
                      REFERENCES workflow(id) ON DELETE CASCADE,
  from_status_key     text  NOT NULL,
  to_status_key       text  NOT NULL,
  required_permission text  REFERENCES permission(key) ON DELETE SET NULL,
  allowed_actor_kinds jsonb NOT NULL DEFAULT '["user","agent"]'::jsonb,
  CONSTRAINT uq_workflow_transition UNIQUE (workflow_id, from_status_key, to_status_key),
  CONSTRAINT ck_workflow_transition_diff CHECK (from_status_key <> to_status_key)
);

ALTER TABLE project
  ADD CONSTRAINT fk_project_workflow
  FOREIGN KEY (workflow_id) REFERENCES workflow(id) ON DELETE SET NULL;
