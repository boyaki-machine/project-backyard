-- 正本: DbDesign.md 6.13「チケットの自己編集の権限」、ApiDesign.md 9.5.2 / 9.9 / 4.5.3。pb-75 / pb-76。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **PATCH /tickets/:seq と DoD の更新系を、エージェントに開ける範囲だけ
-- ticket.edit から切り出す**（利用者の判断、2026-09-09）。0027 の
-- ticket.reference.edit と同じ形である。
--
-- **きっかけは 6.12.1 と同じ構図だった。** エージェントは起票できるのに、
-- 起票したあと何も直せない。pb-72 の実装中にチケットの記述そのものの矛盾を
-- 踏んだとき、直す手段が無く、修正案をコメントに置いて人に貼り替えてもらう
-- 形になった。**仕様の矛盾を最初に踏むのは実装する側である。**
--
-- **許可リストへ ticket.edit を足す案は棄却した。** あれは 9.5.2 の全項目を
-- 開ける——そこには execution_mode / readiness / readiness_note / scope が
-- 含まれる。**これらはエージェントを縛る側が書くものであり、自分で緩められては
-- 意味がない。**
--
-- **type も開けない**（利用者の判断、2026-09-09）。種別の切り替えは人が行う
-- ——タスクをエピックへ変えると、その行はバックログから消えてフィルタの
-- 選択肢になる（GuiDesign.md 5.4）。**記述を整えるつもりで盤面の見え方を
-- 変えてしまう。**

-- +goose Up

-- ── ① 権限カタログ（DbDesign.md 7.2 の規則により、本体の 0010 には追記しない）──

INSERT INTO permission (key, category, description, sort_order) VALUES
  ('ticket.self_edit', 'ticket', 'チケットの記述の編集（エージェントに開ける範囲）', 28)
ON CONFLICT (key) DO UPDATE
  SET category    = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order  = EXCLUDED.sort_order;

-- ── ② ロールへの割り当て ────────────────────────────────

-- administrator は 7.3 の「全権限」SELECT で付く。0010 は適用済みなので再実行する
-- （0017 / 0027 と同じ形）。
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- **ticket.edit を持つロールへそのまま配る。**
--
-- 切り出しの目的は「エージェントに開ける範囲を ticket.edit より狭くする」ことで
-- あって、**人から見た可否を変えることではない**。ticket.self_edit は
-- ticket.edit の部分集合であり、画面の振る舞いは何も変わらない。
--
-- **ロールのキーを並べ書きせず role_permission から引くのは、0010 以降にロールが
-- 増えていても取りこぼさないためである**（0027 と同じ理由）。並べ書きすると、
-- 増えたロールを書き漏らしたときに黙って権限が落ちる。
INSERT INTO role_permission (role_key, permission_key)
SELECT role_key, 'ticket.self_edit' FROM role_permission
 WHERE permission_key = 'ticket.edit'
ON CONFLICT DO NOTHING;
