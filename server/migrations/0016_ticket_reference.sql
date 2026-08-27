-- 正本: DbDesign.md 6.12（チケットの外部参照）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- チケットから「外」を指す参照を1つの表にまとめる。ticket_link（6.6）が
-- チケットどうしをつなぐのに対し、こちらはリポジトリ・コミット・仕様書のように
-- PB の外にあるものを指す。ticket_link は target_ticket_id に FK を持つため、
-- 外部URLを入れる余地がそもそも無い。
--
-- kind ごとに必須項目が違うので、CHECK で DB に守らせる（アプリ側の検証と二重にする）。
-- 1つの表にするのは、画面で隣り合って並び（GuiDesign.md 5.5）、API も1本で足りるため。
-- 2つに分けると API・クエリ・画面のセクションがすべて2系統になり、得られるのは
-- 型の厳密さだけである。
--
-- origin 列は持たない。書き手は created_by から actor.kind（6.1）で分かる。
-- ticket_link と dod_item が持つ origin は「AI の提案か、確定した事実か」という
-- 別の軸であり、ここに同じ列を置くと「誰が書いたか」を2か所に持つことになる。
--
-- kind='code' は追記されて積み上がる（1チケットに複数行）。並びは sort_order では
-- なく created_at が実質の軸になるので、索引に両方を入れてある。
--
-- repository はリポジトリを識別する文字列であり FK ではない。リポジトリの定義は
-- project.settings の repositories（6.4）に jsonb で置かれており、参照できる
-- 主キーが無い。プロジェクト設定に無いリポジトリ名を書いても受け付ける。

-- +goose Up

CREATE TABLE ticket_reference (
  id          char(26) COLLATE "C" PRIMARY KEY,
  ticket_id   char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  kind        text NOT NULL CHECK (kind IN ('code','doc')),
  label       text,
  url         text,
  repository  text,
  branch      text,
  commit_sha  text,
  note        text,
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  sort_order  integer NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_ticket_reference_doc  CHECK (kind <> 'doc'  OR url        IS NOT NULL),
  CONSTRAINT ck_ticket_reference_code CHECK (kind <> 'code' OR repository IS NOT NULL)
);
CREATE INDEX idx_ticket_reference_ticket ON ticket_reference (ticket_id, kind, sort_order, created_at);
CREATE TRIGGER trg_ticket_reference_updated BEFORE UPDATE ON ticket_reference
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
