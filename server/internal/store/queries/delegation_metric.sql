-- 指標の値と周期の要約（Delegation.md 4.2・8.3。DbDesign.md 6.31）。pb-277。

-- CreateDelegationMetricValue は指標の値を1件積む（追記のみ）。
-- name: CreateDelegationMetricValue :exec
INSERT INTO delegation_metric_value (id, delegation_id, metric_key, value, note_md, recorded_by)
VALUES (@id, @delegation_id, @metric_key, (@value::float8)::numeric, @note_md, @recorded_by);

-- ListDelegationMetricValues は指標の値を古い順に返す（要約・画面）。
-- name: ListDelegationMetricValues :many
SELECT v.id, v.metric_key, v.value::float8 AS value, v.note_md, v.recorded_at,
  a.id AS recorded_by_id, a.kind AS recorded_by_kind, a.display_name AS recorded_by_name
FROM delegation_metric_value v
JOIN actor a ON a.id = v.recorded_by
WHERE v.delegation_id = @delegation_id
ORDER BY v.recorded_at, v.id;

-- GetCheckpointBase は周期の起点を返す（8.3）。直近の checkpoint の確認待ちを立てた時刻、
-- 無ければ最初に計画が反映された時刻、それも無ければ委任を作った時刻。
-- name: GetCheckpointBase :one
SELECT COALESCE(
  (SELECT max(c.created_at) FROM confirmation c
   WHERE c.delegation_id = d.id AND c.reason = 'checkpoint'),
  (SELECT min(p.decided_at) FROM delegation_plan p
   WHERE p.delegation_id = d.id AND p.status = 'accepted'),
  d.created_at
)::timestamptz AS base
FROM delegation d WHERE d.id = @delegation_id;

-- ListDelegationPlansBetween は期間に出された計画の版（要約の「計画をどう変えたか」）。
-- name: ListDelegationPlansBetween :many
SELECT p.version, p.status, p.declared_class, p.measured_class, p.reason_md, p.reverts_to, p.submitted_at,
  COALESCE((SELECT ev.verdict FROM evaluation ev WHERE ev.plan_id = p.id
            ORDER BY ev.created_at DESC LIMIT 1), '')::text AS latest_verdict
FROM delegation_plan p
WHERE p.delegation_id = @delegation_id AND p.submitted_at >= @since AND p.submitted_at < @until
ORDER BY p.version;
