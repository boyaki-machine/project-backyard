-- 監査ログ（ApiDesign.md 2.10、DbDesign.md 6.8）。
--
-- 読み出し（GET /admin/audit、auditlog.view）は手順11以降で足す。
-- 手順4b では書き込みの共通基盤のみを用意する。

-- name: InsertAuditLog :exec
INSERT INTO audit_log (
  id, actor_id, actor_kind, actor_label, token_id,
  ip, user_agent, action, target_type, target_id, result, detail, request_id
) VALUES (
  @id, @actor_id, @actor_kind, @actor_label, @token_id,
  @ip, @user_agent, @action, @target_type, @target_id, @result, @detail, @request_id
);
