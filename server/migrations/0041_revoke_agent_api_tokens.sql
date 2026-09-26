-- 正本: Design.md 6.5、ApiDesign.md 4.1。pb-153。
-- 旧 /me/tokens 経由でエージェント名義の API トークンが発行できたため失効する。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。

-- +goose Up

UPDATE access_token t
SET revoked_at = now()
FROM actor a
WHERE a.id = t.actor_id
  AND a.kind = 'agent'
  AND t.token_type <> 'agent'
  AND t.revoked_at IS NULL;
