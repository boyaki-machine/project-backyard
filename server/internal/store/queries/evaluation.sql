-- 評価記録（evaluation）に関するクエリ（DbDesign.md 6.26、Delegation.md 7章）。
-- pb-248。confirmation / delegation_handoff は別チケット（pb-249・pb-250）で足す。

-- CountEvaluationsSinceLastPass は round を決めるのに使う（7.3「同じ対象への
-- 何回目か」）。
--
-- **「同じ対象」は delegation_id × target_kind である。** 直前の合格以降の件数
-- +1 が今回の round になる——合格のたびに往復は0から数え直す（一度正したものを
-- 再び咎めない）。合格が1件も無ければ、委任のこの対象への最初からの件数+1。
-- name: CountEvaluationsSinceLastPass :one
WITH last_pass AS (
  SELECT max(e.created_at) AS at FROM evaluation e
  WHERE e.delegation_id = @delegation_id AND e.target_kind = @target_kind AND e.verdict = 'pass'
)
SELECT count(*)::bigint FROM evaluation e, last_pass
WHERE e.delegation_id = @delegation_id AND e.target_kind = @target_kind
  AND (last_pass.at IS NULL OR e.created_at > last_pass.at);

-- CreateEvaluation は評価を1件作る（Delegation.md 7.3）。
-- name: CreateEvaluation :exec
INSERT INTO evaluation
  (id, delegation_id, target_kind, plan_id, agent_report_id, round, verdict,
   findings, class_check, evaluator_actor_id,
   evaluator_client_kind, evaluator_model_name, evaluator_model_version,
   main_client_kind, main_model_name, main_model_version, tokens_used)
VALUES
  (@id, @delegation_id, @target_kind, @plan_id, @agent_report_id, @round, @verdict,
   @findings, @class_check, @evaluator_actor_id,
   @evaluator_client_kind, @evaluator_model_name, @evaluator_model_version,
   @main_client_kind, @main_model_name, @main_model_version, @tokens_used);

-- GetEvaluationByID は提出直後の応答の組み立てに使う。
-- name: GetEvaluationByID :one
SELECT
  e.id, e.target_kind, e.plan_id, e.agent_report_id, e.round, e.verdict,
  e.findings, e.class_check, e.tokens_used, e.objected_at, e.created_at,
  ea.id AS evaluator_actor_id, ea.kind AS evaluator_actor_kind, ea.display_name AS evaluator_actor_name,
  p.version AS plan_version
FROM evaluation e
JOIN actor ea ON ea.id = e.evaluator_actor_id
LEFT JOIN delegation_plan p ON p.id = e.plan_id
WHERE e.id = @id;

-- GetEvaluationVerdictForReport は、根の受け入れ（ticket.transition の検証8）
-- が「最新の完了レポートは評価に合格しているか」を見るのに使う（7.2）。
-- name: GetEvaluationVerdictForReport :one
SELECT verdict FROM evaluation WHERE agent_report_id = @agent_report_id;

-- GetLatestEvaluationForDelegation は、パック（9.1「開いている評価の指摘」）
-- が表示する直近の評価を読む。target_kind を問わない（pb-251）。
-- name: GetLatestEvaluationForDelegation :one
SELECT target_kind, round, verdict, findings, created_at
FROM evaluation
WHERE delegation_id = @delegation_id
ORDER BY created_at DESC
LIMIT 1;

-- MarkEvaluationObjected は PM の事後の異議を記録する（Delegation.md 6.4・6.5。pb-275）。
-- 一度だけ立てる（2回目は 0 行。呼び出し側が 409 にする）。#254 の集計の材料になる。
-- name: MarkEvaluationObjected :execrows
UPDATE evaluation SET objected_at = now()
WHERE id = @id AND objected_at IS NULL;
