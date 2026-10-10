-- 共通の作業タイマーと、手入力を保持する実績の算出状態。
-- +goose Up
CREATE TABLE work_session (
  id char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  actor_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  client_kind text, model_name text, model_version text,
  state text NOT NULL CHECK (state IN ('running','waiting','finished')),
  started_at timestamptz NOT NULL, changed_at timestamptz NOT NULL, ended_at timestamptz,
  active_ms bigint NOT NULL DEFAULT 0 CHECK (active_ms >= 0),
  waiting_ms bigint NOT NULL DEFAULT 0 CHECK (waiting_ms >= 0),
  reported_active_ms bigint CHECK (reported_active_ms >= 0),
  result text CHECK (result IN ('completed','partial','blocked')),
  tokens bigint CHECK (tokens >= 0),
  note text NOT NULL DEFAULT '', incomplete boolean NOT NULL DEFAULT false,
  CHECK ((state = 'finished') = (ended_at IS NOT NULL)),
  CHECK (changed_at >= started_at AND (ended_at IS NULL OR ended_at >= started_at))
);
CREATE INDEX idx_work_session_ticket ON work_session(ticket_id, started_at DESC);
CREATE UNIQUE INDEX idx_work_session_running ON work_session(project_id, actor_id) WHERE state <> 'finished';
CREATE TABLE work_allocation (
  session_id char(26) COLLATE "C" NOT NULL REFERENCES work_session(id) ON DELETE CASCADE,
  ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  share double precision NOT NULL CHECK (share > 0 AND share <= 1),
  PRIMARY KEY(session_id,ticket_id)
);
CREATE INDEX idx_work_allocation_ticket ON work_allocation(ticket_id);
CREATE TABLE ticket_effort (
  ticket_id char(26) COLLATE "C" PRIMARY KEY REFERENCES ticket(id) ON DELETE CASCADE,
  manual_hours boolean NOT NULL, manual_point boolean NOT NULL
);
CREATE TABLE work_event (
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  actor_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  request_id text NOT NULL,
  session_id char(26) COLLATE "C" NOT NULL REFERENCES work_session(id) ON DELETE CASCADE,
  digest text NOT NULL, response jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(project_id,actor_id,request_id)
);
