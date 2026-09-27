-- pb-216: プロジェクトの基準タイムゾーンと休日の暦（DbDesign.md 6.23）。
-- +goose Up
ALTER TABLE project
  ADD COLUMN timezone text NOT NULL DEFAULT 'Asia/Tokyo';

CREATE TABLE holiday_source (
  id               char(26) COLLATE "C" PRIMARY KEY,
  kind             text NOT NULL CHECK (kind IN ('google','file')),
  google_id        text UNIQUE CHECK (google_id ~ '^[a-z]{2}\.[a-z_]+$'),
  owner_project_id char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  name             text,
  content_sha256   text,
  fetched_at       timestamptz,
  last_attempt_at  timestamptz,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_holiday_source_kind CHECK (
    (kind = 'google' AND google_id IS NOT NULL AND owner_project_id IS NULL)
    OR (kind = 'file' AND google_id IS NULL AND owner_project_id IS NOT NULL))
);
CREATE TRIGGER trg_holiday_source_updated BEFORE UPDATE ON holiday_source
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE holiday_source_day (
  source_id char(26) COLLATE "C" NOT NULL REFERENCES holiday_source(id) ON DELETE CASCADE,
  day       date NOT NULL,
  kind      text NOT NULL CHECK (kind IN ('holiday','observance')),
  name      text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  PRIMARY KEY (source_id, day, name)
);

ALTER TABLE project
  ADD COLUMN holiday_source_id char(26) COLLATE "C"
             REFERENCES holiday_source(id) ON DELETE SET NULL;

CREATE TABLE project_calendar_day (
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  day        date    NOT NULL,
  is_holiday boolean NOT NULL,
  name       text    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 200),
  created_by char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, day)
);
