-- 正本: DbDesign.md 6.4（プロジェクト）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 末尾で 0002 / 0003 の前方参照 FK を付与する（DbDesign.md 5.2）。

-- +goose Up

CREATE TABLE project (
  id          char(26) COLLATE "C" PRIMARY KEY,
  key         text        NOT NULL UNIQUE
              CHECK (key ~ '^[a-z0-9][a-z0-9-]{1,19}$'),
  name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  description text        CHECK (description IS NULL OR length(description) <= 1000),
  status      text        NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active','archived')),
  workflow_id char(26) COLLATE "C",
  settings    jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  version     integer     NOT NULL DEFAULT 1,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);
CREATE INDEX idx_project_status ON project (status);
CREATE TRIGGER trg_project_updated BEFORE UPDATE ON project
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- チケット採番カウンタ（project 本体への行ロックを避けるため分離）
CREATE TABLE project_counter (
  project_id      char(26) COLLATE "C" PRIMARY KEY
                  REFERENCES project(id) ON DELETE CASCADE,
  last_ticket_seq integer NOT NULL DEFAULT 0
);

ALTER TABLE project_member
  ADD CONSTRAINT fk_project_member_project
  FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE;

ALTER TABLE access_token
  ADD CONSTRAINT fk_access_token_project
  FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE;
