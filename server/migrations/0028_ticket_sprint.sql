-- 正本: DbDesign.md 6.9.1「チケットとスプリントの所属」、ApiDesign.md 9.12.1 / 9.12.2
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **1つのチケットは複数のスプリントに属しうる**（利用者の判断、2026-09-08。pb-6）。
-- スプリントは「消化するための一定期間」であり、期間内に終わらなかったチケットは
-- 次のスプリントへ持ち越される。**持ち越しは、あるスプリントで何件が企画され
-- 何件が消化されたかを数える材料**なので、上書きせず履歴として残す。
--
-- **ticket.sprint_id は捨てない。** 所属の履歴は本表が持ち、sprint_id は
-- そのうち最新の1件を指す非正規化された写しである。9.12 の ticket_count /
-- closed_count、9.2.1 の sprint フィルタ、GuiDesign.md 5.4 のグループ化は
-- いずれも「いま属しているのはどれか」しか要らない——結合で書き直しても
-- 答えは変わらず、読む側だけが複雑になる。
--
-- **sprint_id は PATCH では動かなくなる**（ApiDesign.md 9.5.2 の
-- use_sprint_endpoint）。スプリントの開始・終了の副作用としてのみ動く。
-- closed_at と同じ扱いで、直接更新させないことで本表と食い違わないようにする。
--
-- **removed_at を「未完了のときだけ立てない」という区別はしない。** 本表が
-- 答えるのは「そのスプリントの対象だった期間」であって「消化できたか」では
-- ない。消化できたかは ticket.closed_at と sprint.end_date の突き合わせで
-- 後から言える。**1つの列に2つの問いを答えさせない。**

-- +goose Up

CREATE TABLE ticket_sprint (
  ticket_id  char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  sprint_id  char(26) COLLATE "C" NOT NULL REFERENCES sprint(id) ON DELETE CASCADE,
  added_at   timestamptz NOT NULL DEFAULT now(),
  removed_at timestamptz,
  PRIMARY KEY (ticket_id, sprint_id)
);

CREATE INDEX idx_ticket_sprint_sprint ON ticket_sprint (sprint_id);

-- 既存の ticket.sprint_id を所属の履歴へ写す。
--
-- **added_at は now() の既定に任せる。** いつ対象になったかを遡って知る手段が
-- 無い（sprint_id は履歴を持たない列だった）ので、**分からない時刻を推測で
-- 埋めない**。移行した行は「0028 を適用した時点で属していた」とだけ言える。
INSERT INTO ticket_sprint (ticket_id, sprint_id)
SELECT id, sprint_id FROM ticket WHERE sprint_id IS NOT NULL
ON CONFLICT DO NOTHING;
