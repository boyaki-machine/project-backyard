-- 正本: DbDesign.md 6.11（完了条件）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- この表は DbDesign.md 8.1.3（Phase 2）から 6.11（Phase 1）へ移したものである。
-- GuiDesign.md 5.5 が「完了条件は Phase 1 で manual 型のみ実装」と定めているのに、
-- 置き場所だけが Phase 2 にあり、文書どうしが食い違っていた。
--
-- 列と CHECK は Phase 2 の形のまま作り、API が受け付ける type だけを manual に
-- 絞る（ApiDesign.md 9.9）。後から列を足すより、使わない列を持つほうが安い。
--
-- **使うのは手順18 である。** 手順16 でまとめて当てるのは、分けても手順18 の
-- 直前で make migrate を打つだけで利得がないためである（Design.md 11章）。
-- make sqlc は migrations/ をスキーマ源に読むので、先に置いても害はない。

-- +goose Up

CREATE TABLE dod_item (
  id           char(26) COLLATE "C" PRIMARY KEY,
  ticket_id    char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  sort_order   integer NOT NULL,
  type         text    NOT NULL
               CHECK (type IN ('manual','task_ref','assertion','artifact','review')),
  body         text    NOT NULL,
  config       jsonb   NOT NULL DEFAULT '{}'::jsonb,
  is_satisfied boolean NOT NULL DEFAULT false,
  satisfied_at timestamptz,
  satisfied_by char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  evidence     text,
  origin       text    NOT NULL DEFAULT 'human' CHECK (origin IN ('human','ai_suggested')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_dod_ticket ON dod_item (ticket_id, sort_order);
CREATE TRIGGER trg_dod_updated BEFORE UPDATE ON dod_item
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
