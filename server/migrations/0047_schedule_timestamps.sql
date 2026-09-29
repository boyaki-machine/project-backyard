-- pb-217: チケットとスプリントの予定日時をタイムスタンプ（半開区間）で持つ（DbDesign.md 6.6.1）。
-- 既存の日付はプロジェクトの基準タイムゾーンの0時（終わりは翌日の0時）へ移し、終日にする。
-- +goose Up
ALTER TABLE ticket
  ADD COLUMN start_at timestamptz,
  ADD COLUMN due_at   timestamptz,
  ADD COLUMN all_day  boolean NOT NULL DEFAULT true;

UPDATE ticket t SET
  start_at = (t.start_date::timestamp AT TIME ZONE p.timezone),
  due_at   = ((t.due_date + 1)::timestamp AT TIME ZONE p.timezone)
FROM project p
WHERE p.id = t.project_id
  AND (t.start_date IS NOT NULL OR t.due_date IS NOT NULL);

DROP INDEX idx_ticket_due_open;
ALTER TABLE ticket
  DROP CONSTRAINT ck_ticket_dates,
  DROP COLUMN start_date,
  DROP COLUMN due_date,
  ADD CONSTRAINT ck_ticket_schedule CHECK (start_at IS NULL OR due_at IS NULL OR start_at <= due_at);
CREATE INDEX idx_ticket_due_open ON ticket (project_id, due_at) WHERE closed_at IS NULL;

ALTER TABLE sprint
  ADD COLUMN start_at timestamptz,
  ADD COLUMN end_at   timestamptz,
  ADD COLUMN all_day  boolean NOT NULL DEFAULT true;

UPDATE sprint s SET
  start_at = (s.start_date::timestamp AT TIME ZONE p.timezone),
  end_at   = ((s.end_date + 1)::timestamp AT TIME ZONE p.timezone)
FROM project p
WHERE p.id = s.project_id
  AND (s.start_date IS NOT NULL OR s.end_date IS NOT NULL);

ALTER TABLE sprint
  DROP CONSTRAINT ck_sprint_dates,
  DROP COLUMN start_date,
  DROP COLUMN end_date,
  ADD CONSTRAINT ck_sprint_schedule CHECK (start_at IS NULL OR end_at IS NULL OR start_at <= end_at);
