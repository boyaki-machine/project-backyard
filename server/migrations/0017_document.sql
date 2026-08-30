-- 正本: DbDesign.md 8.1（プロジェクト文書）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- Phase 2 の最初のマイグレーションである。規約・価値観・判断の基準を1か所に置き、
-- 全参加者のエージェントが同じものを読むための器を作る（Requirements.md 10.6.2）。
--
-- テンプレートを別テーブルにしない。is_template / template_key / project_id IS NULL で
-- 同居させるのは、6.5 の workflow が既にこの形を採っているため（7.4 の3種は workflow の
-- 行として入り、プロジェクト作成時に複製される）。同じ「テンプレートから複製する」機構を
-- 2つの形で持つと、片方に入れた直しがもう片方に入らない。
--
-- 一意制約を2本に分けている。実文書はプロジェクト内で、テンプレートは template_key の
-- 中で、それぞれ「同じ親の下に同じ slug は1つ」を保証する。NULLS NOT DISTINCT が要るのは
-- parent_id IS NULL（トップレベル）のためで、既定の UNIQUE は NULL どうしを別物として
-- 扱うため、これが無いとトップレベルで slug が重複できてしまう（PostgreSQL 15 以降。
-- 本プロジェクトは 17）。
--
-- 削除は物理削除（4.6 の既定）。本文の履歴は document_revision に残るため、deleted_at を
-- 持たせても復元の役には立たない。parent_id の CASCADE により、親を消すと部分木ごと消える。
--
-- **API も画面もこの手順では作らない**（ApiDesign.md 10章 / GuiDesign.md 5.10 は手順22）。
-- make sqlc は migrations/ をスキーマ源に読むので、先に置いても害はない。

-- +goose Up

-- ── 8.1.1 文書の木 ──────────────────────────────────────────

CREATE TABLE document (
  id           char(26) COLLATE "C" PRIMARY KEY,
  project_id   char(26) COLLATE "C" REFERENCES project(id)  ON DELETE CASCADE,
  parent_id    char(26) COLLATE "C" REFERENCES document(id) ON DELETE CASCADE,
  slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  title        text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body_md      text        NOT NULL DEFAULT '',
  sort_order   integer     NOT NULL DEFAULT 0,
  is_template  boolean     NOT NULL DEFAULT false,
  template_key text,
  created_by   char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  updated_by   char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  version      integer     NOT NULL DEFAULT 1,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_document_template CHECK (
    (is_template AND project_id IS NULL AND template_key IS NOT NULL)
    OR (NOT is_template AND project_id IS NOT NULL AND template_key IS NULL)
  )
);
CREATE UNIQUE INDEX uq_document_slug ON document (project_id, parent_id, slug)
  NULLS NOT DISTINCT WHERE NOT is_template;
CREATE UNIQUE INDEX uq_document_template_slug ON document (template_key, parent_id, slug)
  NULLS NOT DISTINCT WHERE is_template;
CREATE INDEX idx_document_tree ON document (project_id, parent_id, sort_order, slug);
CREATE INDEX idx_document_body_trgm ON document USING gin (body_md gin_trgm_ops);
CREATE TRIGGER trg_document_updated BEFORE UPDATE ON document
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE document_revision (
  id            char(26) COLLATE "C" PRIMARY KEY,
  document_id   char(26) COLLATE "C" NOT NULL REFERENCES document(id) ON DELETE CASCADE,
  revision_no   integer NOT NULL,
  title         text    NOT NULL,
  body_md       text    NOT NULL,
  changed_by    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  change_reason text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_document_revision UNIQUE (document_id, revision_no)
);
CREATE INDEX idx_document_revision_doc ON document_revision (document_id, revision_no DESC);

-- ── 8.1.4 権限 ─────────────────────────────────────────────
--
-- doc.edit を operator と project_member に与えない。憲章は全参加者を縛るので、
-- 更新できる人を絞る（Requirements.md 10.6.2）。同時に、これが operator の持たない
-- 最初の権限になる——Design.md 付録A が積み残していた「画面の権限による出し分けの
-- 負の側を検証できない」に対する最初の実例である。
--
-- シードは冪等（ON CONFLICT DO NOTHING / DO UPDATE、DbDesign.md 5.3）。

INSERT INTO permission (key, category, description, sort_order) VALUES
  ('doc.view', 'doc', 'プロジェクト文書の閲覧', 35),
  ('doc.edit', 'doc', 'プロジェクト文書の編集', 36)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- administrator は 7.3 の「全権限」SELECT で自動的に付く（再実行する）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', key FROM permission WHERE key IN ('doc.view','doc.edit')
ON CONFLICT DO NOTHING;

INSERT INTO role_permission (role_key, permission_key)
SELECT r, 'doc.view' FROM unnest(ARRAY['operator','project_member','project_viewer']) AS r
ON CONFLICT DO NOTHING;

-- ── 8.1.2 文書テンプレート ──────────────────────────────────
--
-- template_key = 'default' の4件。役割と本文の方針は DbDesign.md 8.1.2 が正本。
--
-- ULID は固定値で置く（0010 のワークフローテンプレートと同じ形）。再実行しても主キーで
-- 弾かれるので冪等になる。末尾2文字の D1〜D4 は 0010 の W*（workflow）/ S*（status）/
-- T*（transition）に続く採番である。
--
-- sort_order は 10 刻み。ApiDesign.md 10.4 が「省略時は同じ親の中の末尾（現在の
-- 最大値 + 10）」と定めているので、後から足される文書がテンプレートの後ろに並ぶ。
--
-- created_by / updated_by は NULL。マイグレーションの時点でアクターが存在しない
-- （初期管理者は CLI で後から作る。7.5）。
--
-- 本文に見出し（##）を置かない。ApiDesign.md 10.2 の ?outline=1 は「エージェントが
-- どの章を読むかを決める」ために使うので、中身の無い見出しを並べると、目次だけを見た
-- 相手に「読むべき章がある」と読まれる。

INSERT INTO document (id, project_id, parent_id, slug, title, body_md, sort_order,
                      is_template, template_key) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZD1', NULL, NULL, 'vision', '価値観・世界観',
   'このプロジェクトが何を目指し、何を大切にするかを書く。**意思決定のベースになる文書**である。

背景・目指す姿・大切にしたい価値を、理由とともに残す。規約に書かれていない場面は必ず来る。
そのとき参加者それぞれが自分の判断で動けるように、拠りどころをここに置く。
', 10, true, 'default'),

  ('01JZZZZZZZZZZZZZZZZZZZZZD2', NULL, NULL, 'rules', '規約',
   'このプロジェクトで**守るべきこと**を書く。命名・進め方・レビューの通し方など。

**誰が作業しても同じであるべきことだけを書く。** 個人の好みや端末固有の事情は書かない。
規約は互いの前提を揃えるためのもので、一人ひとりの判断を置き換えるものではない。

なぜその規約にしたのかは「判断の記録」へ、規約に無い場面の拠りどころは「価値観・世界観」へ置く。
', 20, true, 'default'),

  ('01JZZZZZZZZZZZZZZZZZZZZZD3', NULL, NULL, 'decisions', '判断の記録',
   'なぜそう決めたかを書く。**追記のみで使い、過去の行を書き換えない。**

1件につき、いつ・何を決めたか・なぜか・何を検討して採らなかったかを残す。
棄却した案まで書くのは、決着済みの議論が蒸し返されるのを防ぐためであり、
同時に、後から参加した人が決定を追認ではなく理解から始められるようにするためである。
', 30, true, 'default'),

  ('01JZZZZZZZZZZZZZZZZZZZZZD4', NULL, NULL, 'learnings', '学びと知見',
   'やってみて分かったことを書く。**うまくいったことと、駄目だったことの両方を残す。**

「この方法が効いた」も「この方法を試したが動かなかった」も、次に同じ道へ入る人の
時間をそのまま節約する。再現する手順と、そのとき何が起きたかを添える。

一人が得た学びをここへ置くことで、全員の持ち物になる。
', 40, true, 'default')
ON CONFLICT DO NOTHING;
