-- エージェントの完了レポート（DbDesign.md 8.2.4、ApiDesign.md 9.15）。手順26c。
--
-- **Phase 2 での書き手は pb_submit_result ひとつである**（DbDesign.md 8.2.4）。
-- 1回の提出が agent_run 1行・agent_report 1行・完了レポートのコメント1件を
-- 同じトランザクションで作る。
--
-- **読み取りのクエリを置かない。** Phase 2 で人が読むのは完了レポートのコメント
-- であり（GuiDesign.md 5.5）、agent_report の行そのものを読む面が無い（9.15 が
-- GET .../reports を置かないと決めた）。**要るようになってから足す。**

-- CreateAgentRun は1回の実行記録を作る。
--
-- **Phase 2 で status に入るのは completed だけである**（DbDesign.md 8.2.4）。
-- 開始を告げる口が無いので running は作られず、failed / abandoned は「レポートを
-- 出さずに終わった run」で観測する口が無い。
--
-- **workflow_version は渡さない**（NULL のまま）。workflow に版の列が無く
-- （DbDesign.md 6.5）、陳腐化検出は Phase 3 である。
--
-- **started_at はレポートの cost.wall_clock_min から逆算した値が入る。**
-- 無ければ呼び出し側が ended_at と同じ値を渡す（DbDesign.md 8.2.4）。
--
-- name: CreateAgentRun :exec
INSERT INTO agent_run (
  id, ticket_id, actor_id, token_id, client_kind, model_name, model_version,
  started_at, ended_at, status, tokens_used, turns, retry_count
) VALUES (
  @id, @ticket_id, @actor_id,
  sqlc.narg('token_id'), sqlc.narg('client_kind'),
  sqlc.narg('model_name'), sqlc.narg('model_version'),
  @started_at, @ended_at, @status,
  sqlc.narg('tokens_used'), sqlc.narg('turns'), @retry_count
);

-- CreateAgentReport は提出されたレポートを1件保存する。
--
-- **report に Requirements.md 10.6.1 の全体が入る。** 集計対象になる status と
-- knowledge_impact だけを列へ展開する（DbDesign.md 8.2.4）。
--
-- name: CreateAgentReport :exec
INSERT INTO agent_report (
  id, agent_run_id, ticket_id, status, report, knowledge_impact
) VALUES (
  @id, @agent_run_id, @ticket_id, @status, @report, sqlc.narg('knowledge_impact')
);

-- CountAgentRunsForTicket は同じ（チケット × アクター）の既存の run 数を数える。
--
-- **agent_run.retry_count に入れる値である**（DbDesign.md 8.2.4）。初回は 0。
-- 再提出が別の run になるので、これが「何回目のやり直しか」と一致する。
--
-- name: CountAgentRunsForTicket :one
SELECT count(*) FROM agent_run
WHERE ticket_id = @ticket_id AND actor_id = @actor_id;

-- GetAgentRuntimeInfo は実行時点のクライアント種別とモデルを引く。
--
-- **agent_run へ非正規化して写す**（DbDesign.md 8.2.4）。Requirements.md 10.10.3 の
-- 「モデル更新後に品質が変化した際の切り分け」のためで、後から agent.model_name を
-- 書き換えても過去の実行記録が動かないことがこの列の値である。
--
-- **人のトークンで叩かれたときは行が無い**（agent の行を持たないため）。
-- 呼び出し側は pgx.ErrNoRows を「エージェントではない」として扱う。
--
-- name: GetAgentRuntimeInfo :one
SELECT client_kind, model_name, model_version
FROM agent WHERE actor_id = @actor_id;

-- CountAgentRuns はそのアクターの実行記録の数を数える（ApiDesign.md 4.5.4）。
--
-- **0 件なら付け替え先を作らない**（CountAgentComments と同じ）。
--
-- name: CountAgentRuns :one
SELECT count(*) FROM agent_run WHERE actor_id = @actor_id;

-- ReassignAgentRuns は実行記録のアクターを付け替える（ApiDesign.md 4.5.4）。
--
-- **agent_run.actor_id は NOT NULL かつ ON DELETE RESTRICT である**
-- （DbDesign.md 8.2.4）。DBが「付け替えてからでないと消せない」という順序を
-- 強制するので、comment.author_id と同じ手順が要る。
--
-- name: ReassignAgentRuns :execrows
UPDATE agent_run SET actor_id = @new_actor_id WHERE actor_id = @old_actor_id;
