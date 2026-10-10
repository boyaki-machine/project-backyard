-- 正本: Delegation.md 4章・12.2、DbDesign.md 6.24。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 自律実行（Delegated）の器。delegation_plan / evaluation / confirmation /
-- delegation_handoff は別チケット（pb-247〜250）で足す（Delegation.md 14.2）。

-- +goose Up

-- execution_mode に 'delegated' を足す（D1）。根だけがこの値を持てる。
ALTER TABLE ticket DROP CONSTRAINT ticket_execution_mode_check;
ALTER TABLE ticket ADD CONSTRAINT ticket_execution_mode_check
  CHECK (execution_mode IN ('human_only','agent_only','agent_draft','delegated'));
ALTER TABLE ticket ADD CONSTRAINT ck_ticket_delegated_no_parent
  CHECK (execution_mode <> 'delegated' OR parent_id IS NULL);

-- 委任の器（D4）。main・評価・委任範囲・状態を持つ。
CREATE TABLE delegation (
  id                 char(26) COLLATE "C" PRIMARY KEY,
  project_id         char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  root_ticket_id     char(26) COLLATE "C" NOT NULL UNIQUE REFERENCES ticket(id) ON DELETE CASCADE,
  main_actor_id      char(26) COLLATE "C" NOT NULL REFERENCES agent(actor_id) ON DELETE RESTRICT,
  evaluator_actor_id char(26) COLLATE "C" NOT NULL REFERENCES agent(actor_id) ON DELETE RESTRICT,
  status             text NOT NULL DEFAULT 'planning'
                     CHECK (status IN ('planning','approved','paused','ended')),
  scope              jsonb NOT NULL,     -- 4.2 の委任範囲
  scope_version      integer NOT NULL DEFAULT 0,  -- 承認された範囲の版。0 は未承認
  created_by         char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_delegation_independent CHECK (main_actor_id <> evaluator_actor_id)
);
CREATE INDEX idx_delegation_project  ON delegation (project_id);
CREATE INDEX idx_delegation_main     ON delegation (main_actor_id) WHERE status <> 'ended';
CREATE TRIGGER trg_delegation_updated BEFORE UPDATE ON delegation
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 権限キー（D5）。delegation.manage は project_admin が持つ。delegation.evaluate は
-- エージェントの発行時に選ぶ（doc.edit と同じ形。0017）ので、既定には入れない
-- （server/internal/httpapi/v1/me_agents.go の agentGrantableScopes で足す）。
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('delegation.manage',   'delegation', '委任範囲の承認・確認待ちへの回答・停止と再開', 90),
  ('delegation.evaluate', 'delegation', '評価エージェントとして計画・成果を評価する',     91)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- administrator は 7.3 の「全権限」SELECT で自動的に付く（再実行する）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- project_admin がどちらも持つ（0017 の doc.edit と同じ形）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', key FROM permission
 WHERE key IN ('delegation.manage', 'delegation.evaluate')
ON CONFLICT DO NOTHING;
