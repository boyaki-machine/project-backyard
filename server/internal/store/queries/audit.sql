-- 監査ログ（ApiDesign.md 2.10、DbDesign.md 6.8）。
--
-- 読み出しは auditlog.view で守る（ApiDesign.md 6.11）。

-- name: InsertAuditLog :exec
INSERT INTO audit_log (
  id, actor_id, actor_kind, actor_label, token_id,
  ip, user_agent, action, target_type, target_id, result, detail, request_id
) VALUES (
  @id, @actor_id, @actor_kind, @actor_label, @token_id,
  @ip, @user_agent, @action, @target_type, @target_id, @result, @detail, @request_id
);

-- 一覧とCSVは同じ条件を使う。日時は [from_at, to_at) の半開区間。
-- action / actor / q は呼び出し側で LIKE メタ文字をエスケープする。
-- actor_id は削除後に NULL になるため、検索と表示には actor_label を使う。
-- name: ListAuditLogs :many
SELECT id, occurred_at, actor_id, actor_kind, actor_label, token_id,
       ip, user_agent, action, target_type, target_id, coalesce(target_label::text, '') AS target_label,
       result, detail, request_id, category
FROM audit_event
WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.arg(category_filter)::text = '' OR category = sqlc.arg(category_filter)::text)
  AND (sqlc.arg(action_pattern)::text = '' OR action ILIKE sqlc.arg(action_pattern)::text ESCAPE '\')
  AND (sqlc.arg(actor_pattern)::text = '' OR coalesce(actor_label, '') ILIKE sqlc.arg(actor_pattern)::text ESCAPE '\')
  AND (sqlc.arg(result_filter)::text = '' OR result = sqlc.arg(result_filter)::text)
  AND (sqlc.arg(q_pattern)::text = '' OR (
    coalesce(actor_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR action ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_type, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_id, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
  ))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: SummarizeAuditLogs :one
SELECT count(*) AS total
FROM audit_event
WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.arg(category_filter)::text = '' OR category = sqlc.arg(category_filter)::text)
  AND (sqlc.arg(action_pattern)::text = '' OR action ILIKE sqlc.arg(action_pattern)::text ESCAPE '\')
  AND (sqlc.arg(actor_pattern)::text = '' OR coalesce(actor_label, '') ILIKE sqlc.arg(actor_pattern)::text ESCAPE '\')
  AND (sqlc.arg(result_filter)::text = '' OR result = sqlc.arg(result_filter)::text)
  AND (sqlc.arg(q_pattern)::text = '' OR (
    coalesce(actor_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR action ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_type, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_id, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
  ));

-- name: ExportAuditLogs :many
SELECT id, occurred_at, actor_id, actor_kind, actor_label, token_id,
       ip, user_agent, action, target_type, target_id, coalesce(target_label::text, '') AS target_label,
       result, detail, request_id, category
FROM audit_event
WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.arg(category_filter)::text = '' OR category = sqlc.arg(category_filter)::text)
  AND (sqlc.arg(action_pattern)::text = '' OR action ILIKE sqlc.arg(action_pattern)::text ESCAPE '\')
  AND (sqlc.arg(actor_pattern)::text = '' OR coalesce(actor_label, '') ILIKE sqlc.arg(actor_pattern)::text ESCAPE '\')
  AND (sqlc.arg(result_filter)::text = '' OR result = sqlc.arg(result_filter)::text)
  AND (sqlc.arg(q_pattern)::text = '' OR (
    coalesce(actor_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR action ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_type, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_id, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
    OR coalesce(target_label, '') ILIKE sqlc.arg(q_pattern)::text ESCAPE '\'
  ))
ORDER BY occurred_at DESC, id DESC;
