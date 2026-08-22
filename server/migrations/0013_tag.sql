-- 正本: DbDesign.md 6.10（タグ）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- タグは階層（ticket.parent_id）とは別の軸である。「この仕事はどの大きな仕事の
-- 一部か」には親子が答えて答えは必ず1つになり、「この仕事はどういう性質のものか」
-- にはタグが答えて答えは0個から複数になる。
--
-- 色の列を持たない（GuiDesign.md 8.6 がラベルへの任意色を許さない）。
-- description も tag_group も持たない。tag_group が要ると分かったら、
-- tag.group_id を足す前進マイグレーション1本で移れる（DbDesign.md 6.10 / 10章）。
--
-- ticket_tag の付け外しは ticket.updated_at を動かす。これはアプリ側の責務で、
-- ticket のトリガでは拾えない（ApiDesign.md 9.2.5。手順17 で実装する）。

-- +goose Up

CREATE TABLE tag (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  name       text        NOT NULL CHECK (length(name) BETWEEN 1 AND 30),
  sort_order integer     NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_tag_project_name UNIQUE (project_id, name)
);
CREATE INDEX idx_tag_project ON tag (project_id, sort_order);
CREATE TRIGGER trg_tag_updated BEFORE UPDATE ON tag
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE ticket_tag (
  ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  tag_id    char(26) COLLATE "C" NOT NULL REFERENCES tag(id)    ON DELETE CASCADE,
  PRIMARY KEY (ticket_id, tag_id)
);
CREATE INDEX idx_ticket_tag_tag ON ticket_tag (tag_id);
