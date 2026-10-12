-- 当初計画の版（DbDesign.md 6.32。pb-296）。
--
-- [計画を保存] を押すたびに、プロジェクトのタイムライン全体を1つの版として積む。
-- 版は書き換えない（updated_at も version も持たない）。消すのは版ごとの物理削除だけ。
-- チケットは ticket_seq で指し、外部キーを張らない（チケットを消しても版の行は残る）。

-- +goose Up
CREATE TABLE schedule_baseline (
  id              char(26) COLLATE "C" PRIMARY KEY,
  project_id      char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  label           text CHECK (label IS NULL OR length(label) BETWEEN 1 AND 100),
  item_count      integer NOT NULL CHECK (item_count >= 0),
  created_by      char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_by_name text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_schedule_baseline_project ON schedule_baseline (project_id, created_at DESC);

CREATE TABLE schedule_baseline_item (
  baseline_id  char(26) COLLATE "C" NOT NULL
               REFERENCES schedule_baseline(id) ON DELETE CASCADE,
  ticket_seq   integer NOT NULL,
  title        text    NOT NULL,
  epic_seq     integer NOT NULL,
  epic_title   text    NOT NULL,
  start_at     timestamptz,
  due_at       timestamptz,
  all_day      boolean NOT NULL,
  no_overlap   boolean NOT NULL,
  start_pinned boolean NOT NULL,
  due_pinned   boolean NOT NULL,
  PRIMARY KEY (baseline_id, ticket_seq),
  CONSTRAINT ck_schedule_baseline_item_when CHECK (start_at IS NOT NULL OR due_at IS NOT NULL)
);
