-- 正本: DbDesign.md 6.6（チケット）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 手順16d。実機確認（2026-08-23）で出た2件を当てる。
--
-- ① type を epic / story / task の3値へ縮小する。
--    bug / phase / wbs は使い分けの定義がどの設計文書にも無く、実際に使うと
--    選べないことが分かった（利用者の判断）。バグは種別ではなくタグで表す
--    ——「この仕事はどういう性質のものか」に答えるのはタグである
--    （DbDesign.md 6.10）。
--
--    **既存行を先に task へ移してから CHECK を張り替える。** 逆順にすると
--    既存行が制約に違反して ALTER が失敗する。移送先を task にするのは、
--    3値のうち「最小の仕事単位」であり、bug / phase / wbs のいずれからも
--    意味を失わずに読み替えられる唯一の値だからである（story は「配下に
--    複数のタスクを含む」、epic は「グルーピング専用」を主張してしまう）。
--
-- ② staged_at を足す。バックログ画面を上下二段にするための列で、NULL が
--    バックログ（プロジェクトが行うべき仕事すべての保管庫）、値が入っている
--    ものがオンステージ（いま仕掛り中で、直近のスプリントで消化すべきもの）。
--    値そのものは「いつ上げたか」を持つ（GuiDesign.md 5.4）。
--
--    **順序は既存の sort_key を二段で共有する**ので、順序用の列は足さない
--    （ApiDesign.md 9.4）。段ごとにキーを持つと、行き来のたびにどちらを
--    更新するかを決めることになり、戻したときの位置が失われる。
--
--    **進捗（status_key）とは独立した軸である。** ステータスやスプリントで
--    代用すると「未着手だがオンステージ」が表せない。
--
-- ticket 自体の作成は 0006。適用済みファイルは編集しない（5.3）ため
-- 別ファイルとして足している。

-- +goose Up

-- ① 種別を3値へ
UPDATE ticket SET type = 'task' WHERE type IN ('bug', 'phase', 'wbs');

ALTER TABLE ticket DROP CONSTRAINT ticket_type_check;
ALTER TABLE ticket ADD  CONSTRAINT ticket_type_check
  CHECK (type IN ('epic', 'story', 'task'));

-- ② オンステージ
ALTER TABLE ticket
  ADD COLUMN staged_at timestamptz;

CREATE INDEX idx_ticket_staged ON ticket (project_id, staged_at)
  WHERE staged_at IS NOT NULL;
