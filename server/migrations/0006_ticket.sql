-- 正本: DbDesign.md 6.6（チケット）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- ticket.sprint_id の FK は sprint 作成後（0009 末尾）に付与する（DbDesign.md 5.2）。

-- +goose Up

CREATE TABLE ticket (
  id             char(26) COLLATE "C" PRIMARY KEY,
  project_id     char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  seq            integer NOT NULL,
  parent_id      char(26) COLLATE "C" REFERENCES ticket(id) ON DELETE SET NULL,
  type           text    NOT NULL
                 CHECK (type IN ('epic','story','task','bug','phase','wbs')),
  title          text    NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body_md        text,
  status_key     text    NOT NULL,          -- workflow_status.key への論理参照
  priority       text    CHECK (priority IN ('lowest','low','medium','high','highest')),
  assignee_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  reporter_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,

  estimate_point double precision,
  estimate_hours double precision,
  actual_hours   double precision,

  start_date     date,
  due_date       date,
  sprint_id      char(26) COLLATE "C",
  sort_key       text,                      -- LexoRank 方式の並び順

  -- エージェント連携（Phase 1 で列のみ先行定義）
  execution_mode text    NOT NULL DEFAULT 'human_only'
                 CHECK (execution_mode IN ('human_only','agent_only','agent_draft')),
  readiness      text    CHECK (readiness IN ('red','yellow','green')),
  readiness_note text,
  scope          jsonb   NOT NULL DEFAULT '{}'::jsonb,

  -- 起票元（Requirements.md 10.10.6 プロンプトインジェクション対策）
  source            text    NOT NULL DEFAULT 'internal',
  is_trusted_source boolean NOT NULL DEFAULT true,

  custom_fields  jsonb   NOT NULL DEFAULT '{}'::jsonb,
  closed_at      timestamptz,
  version        integer NOT NULL DEFAULT 1,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_ticket_project_seq UNIQUE (project_id, seq),
  CONSTRAINT ck_ticket_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id),
  CONSTRAINT ck_ticket_dates CHECK (start_date IS NULL OR due_date IS NULL
                                    OR start_date <= due_date)
);
CREATE INDEX idx_ticket_project_status ON ticket (project_id, status_key);
CREATE INDEX idx_ticket_assignee_open  ON ticket (assignee_id) WHERE closed_at IS NULL;
CREATE INDEX idx_ticket_parent         ON ticket (parent_id);
CREATE INDEX idx_ticket_sprint         ON ticket (sprint_id);
CREATE INDEX idx_ticket_due_open       ON ticket (project_id, due_date) WHERE closed_at IS NULL;
CREATE INDEX idx_ticket_updated        ON ticket (project_id, updated_at DESC);
CREATE INDEX idx_ticket_title_trgm     ON ticket USING gin (title gin_trgm_ops);
CREATE INDEX idx_ticket_body_trgm      ON ticket USING gin (body_md gin_trgm_ops);
CREATE INDEX idx_ticket_custom_fields  ON ticket USING gin (custom_fields);
CREATE TRIGGER trg_ticket_updated BEFORE UPDATE ON ticket
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE ticket_link (
  id               char(26) COLLATE "C" PRIMARY KEY,
  source_ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  target_ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  link_type        text    NOT NULL
                   CHECK (link_type IN ('FS','SS','FF','SF','relates','duplicates','blocks')),
  lag_days         integer NOT NULL DEFAULT 0,
  origin           text    NOT NULL DEFAULT 'human'
                   CHECK (origin IN ('human','ai_suggested')),
  confidence       double precision,
  created_by       char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_ticket_link UNIQUE (source_ticket_id, target_ticket_id, link_type),
  CONSTRAINT ck_ticket_link_diff CHECK (source_ticket_id <> target_ticket_id)
);
CREATE INDEX idx_ticket_link_source ON ticket_link (source_ticket_id);
CREATE INDEX idx_ticket_link_target ON ticket_link (target_ticket_id);
