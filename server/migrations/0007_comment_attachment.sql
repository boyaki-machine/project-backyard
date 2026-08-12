-- 正本: DbDesign.md 6.7（コメントと添付）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。

-- +goose Up

CREATE TABLE comment (
  id           char(26) COLLATE "C" PRIMARY KEY,
  ticket_id    char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  author_id    char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  body_md      text    NOT NULL,
  kind         text    NOT NULL DEFAULT 'discussion'
               CHECK (kind IN ('discussion','decision','artifact','caveat',
                               'reference','progress')),
  in_reply_to  char(26) COLLATE "C" REFERENCES comment(id) ON DELETE SET NULL,
  origin       text    NOT NULL DEFAULT 'human' CHECK (origin IN ('human','agent')),
  agent_run_id char(26) COLLATE "C",          -- Phase 2 で FK を付与
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  deleted_at   timestamptz
);
CREATE INDEX idx_comment_ticket ON comment (ticket_id, created_at);
CREATE INDEX idx_comment_kind   ON comment (ticket_id, kind) WHERE deleted_at IS NULL;
CREATE INDEX idx_comment_body_trgm ON comment USING gin (body_md gin_trgm_ops);
CREATE TRIGGER trg_comment_updated BEFORE UPDATE ON comment
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE attachment (
  id           char(26) COLLATE "C" PRIMARY KEY,
  project_id   char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  ticket_id    char(26) COLLATE "C" REFERENCES ticket(id)  ON DELETE CASCADE,
  comment_id   char(26) COLLATE "C" REFERENCES comment(id) ON DELETE CASCADE,
  storage_kind text    NOT NULL
               CHECK (storage_kind IN ('local','s3','sharepoint','gdrive','external_url')),
  storage_ref  text    NOT NULL,
  filename     text    NOT NULL,
  content_type text,
  size_bytes   bigint,
  checksum     text,
  uploaded_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_attachment_ticket ON attachment (ticket_id);
