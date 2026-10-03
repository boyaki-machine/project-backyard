-- 引き継ぎノート（delegation_handoff）に関するクエリ（DbDesign.md 6.28、
-- Delegation.md 9.3・11章）。pb-250。

-- NextDelegationHandoffVersion は次に振る版番号を返す（1始まり。追記のみ）。
-- name: NextDelegationHandoffVersion :one
SELECT COALESCE(MAX(version), 0) + 1 FROM delegation_handoff WHERE delegation_id = @delegation_id;

-- CreateDelegationHandoff は引き継ぎノートを1件作る（11.1）。
-- name: CreateDelegationHandoff :exec
INSERT INTO delegation_handoff (id, delegation_id, version, body, trigger, written_by)
VALUES (@id, @delegation_id, @version, @body, @trigger, @written_by);

-- GetLatestDelegationHandoff は「最新の行が現在のノート」を読む
-- （4.4 の delegation_handoff のコメント）。
-- name: GetLatestDelegationHandoff :one
SELECT
  h.id, h.version, h.body, h.trigger, h.written_at,
  wa.id AS written_by_id, wa.kind AS written_by_kind, wa.display_name AS written_by_name
FROM delegation_handoff h
JOIN actor wa ON wa.id = h.written_by
WHERE h.delegation_id = @delegation_id
ORDER BY h.version DESC
LIMIT 1;

-- GetLatestDelegationPlan は「次の一手」（9.3）の判定に使う、版にかかわらず
-- 最新の計画を読む。既存の GetLatestAcceptedDelegationPlan は status='accepted'
-- だけを見るので、proposed・rejected を含む最新を見るにはこちらを使う。
-- name: GetLatestDelegationPlan :one
SELECT version, status, reverts_to, declared_class, measured_class
FROM delegation_plan
WHERE delegation_id = @delegation_id
ORDER BY version DESC
LIMIT 1;

-- HasAnsweredConfirmation は「次の一手」の判定に使う（9.3「ヒアリングに
-- 答えが付いた」「確認待ちに答えが付いた」）。
-- name: HasAnsweredConfirmation :one
SELECT EXISTS (
  SELECT 1 FROM confirmation
  WHERE delegation_id = @delegation_id AND status = 'answered' AND reason = @reason
)::boolean;
