-- 正本: DbDesign.md 6.8（履歴と監査）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。

-- +goose Up

-- 業務履歴：チケット画面の「変更履歴」に表示する
CREATE TABLE activity (
  id          char(26) COLLATE "C" PRIMARY KEY,
  project_id  char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  entity_type text NOT NULL,
  entity_id   char(26) COLLATE "C" NOT NULL,
  actor_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  action      text NOT NULL CHECK (action IN ('create','update','delete','transition')),
  field       text,
  old_value   text,
  new_value   text,
  request_id  char(26) COLLATE "C",
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_activity_entity  ON activity (entity_type, entity_id, occurred_at DESC);
CREATE INDEX idx_activity_project ON activity (project_id, occurred_at DESC);

-- 監査ログ：認証・権限・トークン・エージェント操作。管理者のみ閲覧可
CREATE TABLE audit_log (
  id          char(26) COLLATE "C" PRIMARY KEY,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  actor_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  actor_kind  text,          -- actor 削除後も種別が残るよう非正規化
  actor_label text,          -- 同上。削除時点の表示名・メール
  token_id    char(26) COLLATE "C",
  ip          inet,
  user_agent  text,
  action      text NOT NULL,
  target_type text,
  target_id   char(26) COLLATE "C",
  result      text NOT NULL CHECK (result IN ('success','failure')),
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_audit_time   ON audit_log (occurred_at DESC);
CREATE INDEX idx_audit_action ON audit_log (action, occurred_at DESC);
CREATE INDEX idx_audit_actor  ON audit_log (actor_id, occurred_at DESC);
