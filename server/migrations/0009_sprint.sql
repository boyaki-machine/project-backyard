-- 正本: DbDesign.md 6.9（スプリント）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 末尾で ticket.sprint_id の前方参照 FK を付与する（DbDesign.md 5.2）。

-- +goose Up

CREATE TABLE sprint (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  name       text NOT NULL,
  goal       text,
  start_date date,
  end_date   date,
  status     text NOT NULL DEFAULT 'planned'
             CHECK (status IN ('planned','active','completed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_sprint_dates CHECK (start_date IS NULL OR end_date IS NULL
                                    OR start_date <= end_date)
);
CREATE INDEX idx_sprint_project ON sprint (project_id, status);
CREATE TRIGGER trg_sprint_updated BEFORE UPDATE ON sprint
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE ticket
  ADD CONSTRAINT fk_ticket_sprint
  FOREIGN KEY (sprint_id) REFERENCES sprint(id) ON DELETE SET NULL;
