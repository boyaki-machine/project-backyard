-- 計画の版に関するクエリ（DbDesign.md 6.25、Delegation.md 6章）。pb-247。

-- NextDelegationPlanVersion は次に振る版番号を返す（1始まり）。
-- name: NextDelegationPlanVersion :one
SELECT COALESCE(MAX(version), 0) + 1 FROM delegation_plan WHERE delegation_id = @delegation_id;

-- CreateDelegationPlan は計画の版を1件作る（Delegation.md 6.2）。
-- name: CreateDelegationPlan :exec
INSERT INTO delegation_plan
  (id, delegation_id, version, scope, skeleton, body_md, reason_md,
   declared_class, measured_class, status, reverts_to, submitted_by)
VALUES
  (@id, @delegation_id, @version, @scope, @skeleton, @body_md, @reason_md,
   @declared_class, @measured_class, @status, @reverts_to, @submitted_by);

-- GetLatestAcceptedDelegationPlan は判定の基準にする直前の承認済み版を返す
-- （6.3「前の承認済みの版と比べて段階を決める」）。1件も無ければ初版の提出
-- （6.1「計画の初版は、評価が先」——判定規則を適用しない）。
-- name: GetLatestAcceptedDelegationPlan :one
SELECT version, scope, skeleton, submitted_at
FROM delegation_plan
WHERE delegation_id = @delegation_id AND status = 'accepted'
ORDER BY version DESC
LIMIT 1;

-- GetDelegationPlanByID は提出直後の応答の組み立てに使う。
-- name: GetDelegationPlanByID :one
SELECT
  p.id, p.version, p.scope, p.skeleton, p.body_md, p.reason_md,
  p.declared_class, p.measured_class, p.status, p.reverts_to,
  p.submitted_at, p.decided_at,
  sa.id AS submitted_by_id, sa.kind AS submitted_by_kind, sa.display_name AS submitted_by_name,
  da.id AS decided_by_id, da.kind AS decided_by_kind, da.display_name AS decided_by_name
FROM delegation_plan p
JOIN actor sa ON sa.id = p.submitted_by
LEFT JOIN actor da ON da.id = p.decided_by
WHERE p.id = @id;

-- GetDelegationPlanByVersion は差し戻し元の版を読むのに使う（6.2）。
-- name: GetDelegationPlanByVersion :one
SELECT id, scope, skeleton, body_md, status
FROM delegation_plan
WHERE delegation_id = @delegation_id AND version = @version;

-- AcceptDelegationPlan は軽微な変更を即座に反映する（6.3）。
-- **decided_by は呼び出し元ではなく PB 自身**——軽微は「main の判断で反映する」
-- のであって人・評価エージェントの決定ではないため、decided_by は NULL のままにする。
-- name: AcceptDelegationPlan :execrows
UPDATE delegation_plan SET status = 'accepted', decided_at = now()
WHERE id = @id AND status = 'proposed';

-- RejectDelegationPlan は評価が不合格だった構造の変更を確定する（7.2・6.3。
-- pb-248）。**decided_by は呼び出し元ではなく PB 自身**——AcceptDelegationPlan
-- と同じ理由で、不合格にしたのは main でも PM でもなく評価の結果である。
-- name: RejectDelegationPlan :execrows
UPDATE delegation_plan SET status = 'rejected', decided_at = now()
WHERE id = @id AND status = 'proposed';

-- CountTicketsInDelegationTreeSince は 6.3 の「1つの版で増える子孫が5件を超える」
-- の判定に使う。根自身は数えない。
--
-- **ticket.sql の subtree と同じ再帰パターンを使い回す。**
-- name: CountTicketsInDelegationTreeSince :one
WITH RECURSIVE subtree AS (
  SELECT id FROM ticket WHERE id = @root_id
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
)
SELECT count(*)::bigint FROM ticket t
 WHERE t.id IN (SELECT id FROM subtree) AND t.id <> @root_id AND t.created_at > @since;

-- GetLatestDelegationPlanWithVerdict は PM の承認・却下の対象を読む（6.1。pb-260）。
-- **対象は最新の版（version が最大）だけである。** 古い版を承認できると、main が
-- 出し直した後の版を飛ばして古い計画が通る。latest_verdict はその版への直近の
-- 評価の合否で、評価が無ければ空文字。
-- name: GetLatestDelegationPlanWithVerdict :one
SELECT p.id, p.version, p.scope, p.status,
  COALESCE((SELECT e.verdict FROM evaluation e
            WHERE e.plan_id = p.id
            ORDER BY e.created_at DESC LIMIT 1), '')::text AS latest_verdict
FROM delegation_plan p
WHERE p.delegation_id = @delegation_id
ORDER BY p.version DESC
LIMIT 1;

-- DecideDelegationPlan は PM が計画の版を確定する（承認なら accepted、却下なら
-- rejected。6.1。pb-260）。**decided_by は PM**——AcceptDelegationPlan /
-- RejectDelegationPlan（PB 自身の判定）と違い、人の決定である。
-- name: DecideDelegationPlan :execrows
UPDATE delegation_plan
SET status = @status, decided_by = @decided_by, decided_at = now()
WHERE id = @id AND status = 'proposed';

-- SupersedeProposedDelegationPlans は、PM が承認した版より前に出されて
-- proposed のまま残った版を superseded にする（pb-260）。計画中に main が
-- 出し直すと前の版は判定されずに残るため、承認の時点で畳む。
-- name: SupersedeProposedDelegationPlans :execrows
UPDATE delegation_plan
SET status = 'superseded', decided_at = now()
WHERE delegation_id = @delegation_id AND status = 'proposed' AND id <> @keep_id;

-- GetLatestPlanForStatus は画面の「計画の承認」のカードに要る、最新の版の中身と、
-- その版への直近の評価を読む（delegation-status。pb-252）。評価が無ければ
-- eval_verdict は空文字、eval_findings は NULL。
-- name: GetLatestPlanForStatus :one
SELECT p.version, p.status, p.scope, p.skeleton, p.body_md, p.reason_md,
       COALESCE(e.verdict, '')::text AS eval_verdict,
       COALESCE(e.round, 0)::integer AS eval_round,
       e.findings AS eval_findings
FROM delegation_plan p
LEFT JOIN LATERAL (
  SELECT ev.verdict, ev.round, ev.findings
  FROM evaluation ev
  WHERE ev.plan_id = p.id
  ORDER BY ev.created_at DESC
  LIMIT 1
) e ON true
WHERE p.delegation_id = @delegation_id
ORDER BY p.version DESC
LIMIT 1;
