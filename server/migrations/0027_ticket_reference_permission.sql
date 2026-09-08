-- 正本: DbDesign.md 6.12.1（チケットの外部参照の権限）。pb-68。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **外部参照の更新系を ticket.edit から切り出す**（ApiDesign.md 9.10.2。
-- 利用者の判断、2026-09-08）。読み（GET）は ticket.view のままである。
--
-- **きっかけは、設計文書が定めた書き手が書けなかったことである。** DbDesign.md
-- 6.12 は「kind='code' の書き手は**エージェント**」「作業の経過として追記されて
-- 積み上がる」と定めているのに、エージェントのトークンに載せられる権限の許可リスト
-- （ApiDesign.md 4.5.3）は ticket.edit を含まない。**MCP から commit / ブランチを
-- 積む経路が、権限のところで塞がっていた。**
--
-- **許可リストへ ticket.edit を足す案は棄却した。** ticket.edit は外部参照だけで
-- なく PATCH /tickets/:seq（本文・担当・期日の書き換え）・move（並べ替え）・
-- DoD（9.9）・チケット間リンク（9.10.1）も開ける。**「作業の跡を積む」ために
-- 「チケットの中身を書き換える」威力まで渡すことになる。** しかも access_token.scopes
-- は発行時に固定される jsonb 列（0002）なので、**一度許可リストに入れた権限は
-- 発行済みのトークンがある分だけ後から狭めにくい。**
--
-- **sort_order は 27。** ticket.* は 0010 で 20〜26 を使っており、その次に置く。
--
-- **コードの変更を伴う**（0026 とはここが違う）——routes.go の3行と、
-- me_agents.go の既定スコープ。

-- +goose Up

-- ── ① 権限カタログ（DbDesign.md 7.2 の規則により、本体の 0010 には追記しない）──

INSERT INTO permission (key, category, description, sort_order) VALUES
  ('ticket.reference.edit', 'ticket', 'チケットの外部参照の編集', 27)
ON CONFLICT (key) DO UPDATE
  SET category    = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order  = EXCLUDED.sort_order;

-- ── ② ロールへの割り当て ────────────────────────────────

-- administrator は 7.3 の「全権限」SELECT で付く。0010 は適用済みなので再実行する
-- （0017 と同じ形）。
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- **ticket.edit を持つロールへそのまま配る。**
--
-- 切り出しの目的は「エージェントに開ける範囲を ticket.edit より狭くする」ことで
-- あって、**人から見た可否を変えることではない**。いま画面から doc 参照を編集
-- できている人（GuiDesign.md 5.5）が編集できなくなってはならない。
--
-- **ロールのキーを並べ書きせず role_permission から引くのは、0010 以降にロールが
-- 増えていても取りこぼさないためである。** 並べ書きすると、増えたロールを書き
-- 漏らしたときに黙って権限が落ちる（落ちても 403 が出るだけで、原因が遠い）。
--
-- 同じ表への INSERT ... SELECT だが、PostgreSQL は文の開始時点のスナップショットを
-- 読むので自己参照で増殖しない。
INSERT INTO role_permission (role_key, permission_key)
SELECT role_key, 'ticket.reference.edit' FROM role_permission
 WHERE permission_key = 'ticket.edit'
ON CONFLICT DO NOTHING;
