-- 正本: DbDesign.md 6.6「エージェント連携」、ApiDesign.md 9.3、GuiDesign.md 5.5「実行モード」
-- 前進のみ。down は書かない（DbDesign.md 5.3）。pb-65。
--
-- **ticket.execution_mode の既定を 'human_only' から 'agent_draft' へ変える。**
--
-- 0006 で列を先行定義したとき、既定は安全側に倒して 'human_only' にしてあった。
-- Phase 2 でエージェントが実際にチケットを取るようになって、**この既定が
-- 協業を止めていることが分かった**——エージェントは着手前に execution_mode を読み、
-- 'human_only' なら実装せず報告して終える（Requirements.md 10.5.4）。つまり
-- **人が1件ずつ明示的に変えるまで、エージェントは一切動けない。**
--
-- PB は「人とAIエージェントが同じプロジェクトを一緒に進める」ための道具である。
-- **協業できないほうを既定にしない**（利用者の判断、2026-09-06）。
--
-- **'agent_only' ではなく 'agent_draft' を選んだ。** あちらは人の確認を挟まないので、
-- 既定にすると「気づいたら終わっていた」が起きる。'agent_draft'（エージェントが
-- 下書きし、人が仕上げる）は協業を許容しつつ、**仕上げを人に残す。**
--
-- **既存の行は書き換えない。** 既定はこれから作る行にしか効かない。
-- いま 'human_only' が入っている行には、**そう決めた意図があるかもしれない**
-- ——意図の有無を DDL から見分けられない以上、黙って寄せてよい理由がない。
-- 変えたい行は画面から変える（GuiDesign.md 5.5「実行モード」で入力欄を出した）。
--
-- **適用済みの 0006 は編集しない**（規約「誤りが見つかったときは、次の番号で打ち消す」）。

-- +goose Up

ALTER TABLE ticket
  ALTER COLUMN execution_mode SET DEFAULT 'agent_draft';

COMMENT ON COLUMN ticket.execution_mode IS
  '実行モード。エージェントが着手してよいかを決める。既定は agent_draft（0025）。DbDesign.md 6.6';
