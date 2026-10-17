-- 通知（DbDesign.md 6.33）。
--
-- 人に対応や確認を求める出来事を、宛先の利用者に届ける。仕組みは使い手を知らず、使い手が
-- サーバ内の関数で作り、取り下げる。通知を作る REST の口は持たない（ApiDesign.md 4.8）。
-- 既読から30日で、サーバの定期処理が消す。未読は消さない。

-- +goose Up
CREATE TABLE notification (
  id           char(26) COLLATE "C" PRIMARY KEY,
  recipient_id char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE CASCADE,
  kind         text NOT NULL CHECK (length(kind) <= 64
                 AND kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  title        text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body         text NOT NULL DEFAULT '' CHECK (length(body) <= 2000),
  link         text NOT NULL CHECK (length(link) <= 500
                 AND link ~ '^/[^/\\]'),
  link_label   text NOT NULL CHECK (length(link_label) BETWEEN 1 AND 100),
  subject_key  text CHECK (subject_key IS NULL OR length(subject_key) BETWEEN 1 AND 200),
  project_id   char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  source_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  source_name  text NOT NULL CHECK (length(source_name) BETWEEN 1 AND 100),
  created_at   timestamptz NOT NULL DEFAULT now(),
  read_at      timestamptz
);
CREATE INDEX idx_notification_recipient ON notification (recipient_id, created_at DESC);
CREATE INDEX idx_notification_unread    ON notification (recipient_id) WHERE read_at IS NULL;
CREATE INDEX idx_notification_subject   ON notification (subject_key)  WHERE subject_key IS NOT NULL;
CREATE INDEX idx_notification_read_at   ON notification (read_at)      WHERE read_at IS NOT NULL;
