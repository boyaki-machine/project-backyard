-- 認証に関するクエリ（Design.md 6.2.2、DbDesign.md 6.2）。

-- FindAccessTokenByHash は受け取った平文の SHA-256 で access_token を引く。
--
-- **有効性（revoked_at / expires_at / actor.is_active）を WHERE で絞らない。**
-- 絞ると「そんなトークンは無い」と「失効している」を区別できず、サーバログに
-- 理由を残せなくなるため。応答はいずれも 401 で統一する（存在を漏らさない）が、
-- 運用者が原因を追えるようにする。判定は呼び出し側で行う。
--
-- app_user を LEFT JOIN にしているのは、エージェント（Phase 2）とシステムの
-- アクターが app_user の行を持たないため。
--
-- name: FindAccessTokenByHash :one
SELECT
  t.id           AS token_id,
  t.token_type,
  t.scopes,
  t.project_id,
  t.expires_at,
  t.revoked_at,
  t.last_used_at,
  a.id           AS actor_id,
  a.kind         AS actor_kind,
  a.display_name,
  a.is_active,
  u.system_role,
  u.email
FROM access_token t
JOIN actor a ON a.id = t.actor_id
LEFT JOIN app_user u ON u.actor_id = a.id
WHERE t.token_hash = @token_hash;

-- TouchAccessTokenLastUsed は last_used_at を更新する。
--
-- **1分粒度で間引く**（Design.md 6.2.2）。リクエストのたびに UPDATE すると、
-- 認証という最も高頻度な経路で毎回行ロックと WAL を発生させることになる。
-- 直近1分以内に更新済みなら WHERE が外れ、no-op で返る。
--
-- name: TouchAccessTokenLastUsed :exec
UPDATE access_token
SET last_used_at = now()
WHERE id = @id
  AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');
