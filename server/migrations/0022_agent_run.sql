-- 正本: DbDesign.md 8.2.4（agent_run / agent_report）、8.2.5（context_pack_log）、ApiDesign.md 9.15
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **Phase 3 から Phase 2 へ戻した表である**（利用者の判断、2026-09-05。手順26c）。
-- pb_submit_result（完了レポート）を Phase 2 で実装すると決めたため、格納先が要る。
-- DDL は送ったときの形のまま転記する。**Phase 3 の採番は 0023〜0027 へずれた**
-- （Phase 2 の途中で4回目。DbDesign.md 8章）。
--
-- ── Phase 2 での書き手は pb_submit_result ひとつである ──────────
--
-- **1回の提出が agent_run 1行と agent_report 1行を同時に作る。** 開始を告げる口を
-- Phase 2 は持たない——pb_claim_task は Phase 3 へ送られ（8.2.2）、pb_transition_task の
-- 副作用は ticket.working_agent_id だけと決めた（6.6）。
--
-- **「走っている run」を読む者が居ないので、開始の口を作らない。** 「いま誰が処理して
-- いるか」は ticket.working_agent_id が既に担っており、status='running' の行を足すと
-- 同じ事実が2か所になる。**再提出は別の run になる**——Requirements.md 10.8.6 の手順7 が
-- 「未充足の完了条件が返ったら修正して再提出する」と定めており、その修正はエージェントが
-- 実際に作業をやり直したことを意味する。
--
-- **したがって Phase 2 で status に立つのは completed だけである。** failed / abandoned は
-- 「レポートを出さずに終わった run」で、それを観測する口が無い。**CHECK は 4値のまま
-- 作る**——列と CHECK を Phase 3 の形で作り、アプリが書く値だけを絞るのは、dod_item.type を
-- Phase 1 でそうしたのと同じ扱いである（6.11）。
--
-- **workflow_version は埋めない。** workflow に版の列が無く（6.5）、Requirements.md 10.9.3 の
-- 陳腐化検出は Phase 3 である。**使うものが無いうちに値を入れると意味が固まる。**
--
-- **actor_id は ON DELETE RESTRICT である。** エージェントを削除する前に、その run を
-- 「削除されたエージェント」へ付け替える（ApiDesign.md 4.5.4）。comment.author_id が同じ
-- 制約を持ち、26a が同じ付け替えを実装している。**付け替えないと DELETE /me/agents/:id
-- そのものが失敗する。**

-- +goose Up

-- ── 8.2.4 agent_run / agent_report ──────────────────────────

CREATE TABLE agent_run (
  id               char(26) COLLATE "C" PRIMARY KEY,
  ticket_id        char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  actor_id         char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  token_id         char(26) COLLATE "C",
  client_kind      text,
  model_name       text,
  model_version    text,
  workflow_version integer,
  started_at       timestamptz NOT NULL DEFAULT now(),
  ended_at         timestamptz,
  status           text NOT NULL
                   CHECK (status IN ('running','completed','failed','abandoned')),
  tokens_used      bigint,
  turns            integer,
  retry_count      integer NOT NULL DEFAULT 0
);
CREATE INDEX idx_agent_run_ticket ON agent_run (ticket_id, started_at DESC);
CREATE INDEX idx_agent_run_actor  ON agent_run (actor_id, started_at DESC);

-- report に Requirements.md 10.6.1 のレポート全体を保存しつつ、頻繁に検索・集計する
-- status と knowledge_impact のみ列に展開する（8.2.4）。

CREATE TABLE agent_report (
  id               char(26) COLLATE "C" PRIMARY KEY,
  agent_run_id     char(26) COLLATE "C" NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
  ticket_id        char(26) COLLATE "C" NOT NULL REFERENCES ticket(id)    ON DELETE CASCADE,
  status           text NOT NULL CHECK (status IN ('completed','blocked','partial')),
  report           jsonb NOT NULL,
  knowledge_impact text CHECK (knowledge_impact IN ('none','minor','major')),
  submitted_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_report_ticket ON agent_report (ticket_id, submitted_at DESC);

-- 0007 が「Phase 2 で FK を付与」と書いて空けていた列である（6.7）。
-- **埋まるのは pb_submit_result が作る完了レポートのコメントだけ**——作業中の
-- pb_post_note は run を持たない（提出時にしか run が存在しないため）。

ALTER TABLE comment
  ADD CONSTRAINT fk_comment_agent_run
  FOREIGN KEY (agent_run_id) REFERENCES agent_run(id) ON DELETE SET NULL;

-- ── 8.2.5 context_pack_log ──────────────────────────────────
--
-- **器だけ作り、Phase 2 では書かない。** agent_run への FK を持つので同じファイルに
-- 入れる必要があり、8章の採番表も 0022 の中身としてこの表を挙げている。書き手
-- （pb_get_context の記録）が現れるのは Phase 3 である——手順27 が pb_get_context を
-- 実装するが、Requirements.md 10.4.4 の効果計測は運用の実績が要る。
-- **0019 が task_lease を同じ理由で寝かせたのと同じ扱いである。**

CREATE TABLE context_pack_log (
  id            char(26) COLLATE "C" PRIMARY KEY,
  ticket_id     char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  agent_run_id  char(26) COLLATE "C" REFERENCES agent_run(id) ON DELETE SET NULL,
  budget_tokens integer,
  actual_tokens integer,
  included      jsonb NOT NULL DEFAULT '[]'::jsonb,
  truncated     jsonb NOT NULL DEFAULT '[]'::jsonb,
  generated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_context_pack_run ON context_pack_log (agent_run_id);

COMMENT ON TABLE agent_run IS
  'エージェントの実行記録。Phase 2 では pb_submit_result が1提出につき1行作る。DbDesign.md 8.2.4';
COMMENT ON TABLE agent_report IS
  'エージェントの完了レポート。report に Requirements.md 10.6.1 の全体が入る。DbDesign.md 8.2.4';
COMMENT ON TABLE context_pack_log IS
  'コンテキストパックの生成記録。Phase 2 では書き手を持たない（器のみ）。DbDesign.md 8.2.5';
