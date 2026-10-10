-- +goose Up
ALTER TABLE work_session ADD COLUMN outcome text CHECK(outcome IN ('completed','failed','abandoned'));
CREATE TABLE agent_attempt (
 id char(26) COLLATE "C" PRIMARY KEY,
 project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
 ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
 actor_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
 started_at timestamptz NOT NULL, ended_at timestamptz,
 outcome text CHECK(outcome IN ('completed','failed','abandoned'))
);
CREATE INDEX idx_agent_attempt_monitor ON agent_attempt(project_id,actor_id,started_at DESC);
CREATE TABLE agent_monitor_reset (
 project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
 actor_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
 reset_at timestamptz NOT NULL DEFAULT now(), reason text NOT NULL,
 PRIMARY KEY(project_id,actor_id)
);
ALTER TABLE agent_run ADD COLUMN work_session_id char(26) COLLATE "C";
CREATE UNIQUE INDEX idx_agent_run_failed_work ON agent_run(work_session_id) WHERE work_session_id IS NOT NULL;
