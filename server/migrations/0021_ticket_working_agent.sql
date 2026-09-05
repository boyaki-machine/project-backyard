-- 正本: DbDesign.md 6.6「working_agent_id — 誰が実際に処理しているか」、ApiDesign.md 9.6
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **担当（assignee_id）と実行者を別の列にする**（利用者の判断、2026-09-05。手順26b）。
--
--   assignee_id      = 誰の仕事か（責任者）。人が割り当てる
--   working_agent_id = 誰が実際に処理しているか（実行者）。エージェントが自分で宣言する
--
-- **1列では両方を表せなかった。** assignee_id は actor を参照するのでエージェントを
-- 入れられ、GuiDesign.md 5.4 は「担当がエージェント」を想定した表示を定めている。
-- ところが Design.md 8.5（手順25）は「担当は人が持つ」と決め、MCP の assignee=me を
-- 所有者へ写した。**「田中の担当だが claude が処理している」を表す欄が無かった**のが
-- 食い違いの正体で、列を分けると両方が正しくなる。
--
-- **この列は権限判定に使わない。** エージェントが状態を変えてよいかは assignee_id が
-- 所有者かどうかで決まる（ApiDesign.md 9.6 の検証6）。**人がいつでも消せる列を認可に
-- 使うと、消しただけで作業が止まる。**
--
-- **task_lease（0019、DbDesign.md 8.2.2）は使わない。** 排他が実際に要るのは自律取得
-- （pb_next_task、Phase 3）からで、Phase 2 は人がチケット番号を指定して走らせるため、
-- 同じチケットを2つのエージェントが取り合う状況が起きない。器は寝かせたまま残す
-- （前進のみの規則では、落とすより安い）。
--
-- **インデックスを張らない。** 実行者で絞り込む経路が Phase 2 に無い（ApiDesign.md
-- 9.2.1 のクエリパラメータに working_agent が無く、GuiDesign.md 5.4 のフィルタ8つにも
-- 無い）。**要るようになってから足す**——書き込みのたびに維持費だけが掛かる索引を、
-- 読む人が居ないうちに置かない。

-- +goose Up

ALTER TABLE ticket
  ADD COLUMN working_agent_id char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL;

COMMENT ON COLUMN ticket.working_agent_id IS
  '実行者（誰が実際に処理しているか）。エージェントが遷移時に自己申告する。DbDesign.md 6.6';
