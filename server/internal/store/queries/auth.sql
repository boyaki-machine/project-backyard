-- 認証に関するクエリ（Design.md 6.2.2、DbDesign.md 6.2）。

-- FindAccessTokenByHash は受け取った平文の SHA-256 で access_token を引く。
--
-- **有効性（revoked_at / expires_at / actor.is_active）を WHERE で絞らない。**
-- 絞ると「そんなトークンは無い」と「失効している」を区別できず、サーバログに
-- 理由を残せなくなるため。応答はいずれも 401 で統一する（存在を漏らさない）が、
-- 運用者が原因を追えるようにする。判定は呼び出し側で行う。
--
-- app_user を LEFT JOIN にしているのは、システムのアクターが app_user の行を
-- 持たないため。
--
-- **エージェントは所有者の app_user を引く**（Design.md 6.5 の委譲、0019）。
-- agent を LEFT JOIN し、app_user の結合先を COALESCE(ag.owner_actor_id, a.id)
-- にしてある。これで「所有者のシステムロール」が**クエリを1本も増やさずに**
-- 載る——認証は全リクエストが通る経路であり、ここで引く行に相乗りするのが
-- 本設計の要点だからである（cached_permissions と同じ考え方）。
--
-- 人間のアクターでは ag.owner_actor_id が NULL なので COALESCE は a.id に
-- 落ち、従来と同じ結合になる。
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
  -- 実効権限のセッションキャッシュ（Design.md 6.4.5、0012 で追加）。
  -- **認可のためにクエリを1本足さない**ことがこの設計の要点である。
  -- 認証は全リクエストが通る経路であり、その行に相乗りすれば
  -- RequirePermission は追加のDBアクセス無しで判定できる。
  t.cached_permissions,
  t.permissions_cached_at,
  a.id           AS actor_id,
  a.kind         AS actor_kind,
  a.display_name,
  a.is_active,
  ag.owner_actor_id,
  -- 所有者が無効なら、そのエージェントも通さない（Design.md 6.5）。
  -- 人間のアクターでは NULL になり、判定は a.is_active だけで行う。
  owner.is_active AS owner_is_active,
  u.system_role,
  u.email
FROM access_token t
JOIN actor a ON a.id = t.actor_id
LEFT JOIN agent ag ON ag.actor_id = a.id
LEFT JOIN actor owner ON owner.id = ag.owner_actor_id
LEFT JOIN app_user u ON u.actor_id = COALESCE(ag.owner_actor_id, a.id)
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

-- ── ローカル ID/PW ログイン（Design.md 6.2.1、手順5） ────────────────

-- FindLocalLoginByEmail は Design.md 6.2.1 の手順2〜3を1文で行う。
--
--   2. app_user を email で検索（citext のため大文字小文字を区別しない）
--   3. user_identity を (provider_key='local', subject=email) で検索
--
-- **subject は入力されたメールではなく、手順2で引き当てた app_user.email と
-- 突き合わせる。** user_identity.subject は text（大小を区別する）であり、
-- 利用者が入力した表記でそのまま引くと、手順2は通るのに手順3で外れる
-- （PROGRESS.md「メールアドレスの大小の扱い」）。JOIN 条件に u.email を
-- 使えば、比較の対象は常にDBに保存された表記そのものになる。
--
-- **有効性（actor.is_active / locked_until）を WHERE で絞らない。**
-- 絞ると行が取れず、ダミーハッシュ検証（下記）と応答時間が揃わなくなる。
-- 判定は呼び出し側で行う。
--
-- name: FindLocalLoginByEmail :one
SELECT
  a.id            AS actor_id,
  a.display_name,
  a.is_active,
  u.email,
  u.system_role,
  u.locale,
  u.timezone,
  u.theme,
  u.hue,
  i.id            AS identity_id,
  c.password_hash,
  c.must_change,
  c.failed_attempts,
  c.locked_until
FROM app_user u
JOIN actor a ON a.id = u.actor_id
JOIN user_identity i
  ON i.user_id = u.actor_id
 AND i.provider_key = 'local'
 AND i.subject = u.email
JOIN local_credential c ON c.identity_id = i.id
WHERE u.email = @email;

-- RecordLoginFailure は失敗回数とロック期限を書く（Design.md 6.2.1 手順5、6.3）。
-- 閾値の判定はアプリ側で行い、その結果をそのまま反映する。
--
-- name: RecordLoginFailure :exec
UPDATE local_credential
SET failed_attempts = @failed_attempts,
    locked_until    = @locked_until
WHERE identity_id = @identity_id;

-- ResetLoginFailure はログイン成功時に失敗回数とロックを消す
-- （Design.md 6.2.1 手順5 の「成功 → failed_attempts=0」）。
--
-- name: ResetLoginFailure :exec
UPDATE local_credential
SET failed_attempts = 0,
    locked_until    = NULL
WHERE identity_id = @identity_id;

-- RehashPassword はハッシュパラメータが旧世代のときに再ハッシュ結果を書く
-- （Design.md 6.2.1 手順5）。
--
-- **password_updated_at は変更しない。** パスワードそのものは変わっておらず、
-- 「いつ利用者がパスワードを変えたか」の意味を壊さないため。
--
-- name: RehashPassword :exec
UPDATE local_credential
SET password_hash = @password_hash
WHERE identity_id = @identity_id;

-- CreateAccessToken はセッション・APIトークン・エージェントトークンを発行する
-- （DbDesign.md 6.2）。**平文は渡さない。** token_hash は SHA-256、
-- token_prefix は一覧表示用の先頭8文字である。
--
-- name: CreateAccessToken :exec
INSERT INTO access_token (
  id, actor_id, token_type, token_hash, token_prefix,
  name, project_id, scopes, expires_at, client_info
) VALUES (
  @id, @actor_id, @token_type, @token_hash, @token_prefix,
  @name, @project_id, @scopes, @expires_at, @client_info
);

-- RevokeAccessToken は失効させる（ApiDesign.md 3.2 のログアウト）。
-- 既に失効済みなら no-op で返り、revoked_at を上書きしない。
--
-- name: RevokeAccessToken :exec
UPDATE access_token
SET revoked_at = now()
WHERE id = @id
  AND revoked_at IS NULL;

-- TouchLastLoginAt はログイン成功日時を記録する。
--
-- Design.md 6.2.1 のフローには現れないが、app_user.last_login_at 列が存在し
-- （DbDesign.md 6.2）、ApiDesign.md 6.1 のユーザー一覧がこの値を返すため、
-- ログイン時に書かなければ永久に NULL のままになる。
--
-- name: TouchLastLoginAt :exec
UPDATE app_user
SET last_login_at = now()
WHERE actor_id = @actor_id;

-- GetActorProfile は GET /me（ApiDesign.md 4.1）が返す actor 部分を引く。
--
-- 認証ミドルウェアが載せる Principal（Design.md 6.2.2）には locale / timezone /
-- theme / hue / must_change_password が無い。認証の判定に要らない値を
-- トークン検証の経路に足すと、全リクエストで読むことになるためである。
-- /me はこのクエリで補う。
--
-- theme / hue は GuiDesign.md 8.11 のテーマ設定（手順15 で足した）。**サーバに
-- 保存しても GET /me が返さなければ、別の端末で同じ見た目にならない**——
-- 8.11 が app_user と localStorage の両方に保存すると定めた目的がそれである。
--
-- **すべて LEFT JOIN にする。** エージェント（Phase 2）は app_user を持たず、
-- 将来の OIDC 専用ユーザーは local_credential を持たない。行が返らないことと
-- 「そのアクターが存在しない」ことを取り違えないようにする。
--
-- name: GetActorProfile :one
SELECT
  a.id   AS actor_id,
  a.kind,
  a.display_name,
  u.email,
  u.system_role,
  u.locale,
  u.timezone,
  u.theme,
  u.hue,
  c.must_change
FROM actor a
LEFT JOIN app_user u ON u.actor_id = a.id
LEFT JOIN user_identity i
  ON i.user_id = u.actor_id
 AND i.provider_key = 'local'
 AND i.subject = u.email
LEFT JOIN local_credential c ON c.identity_id = i.id
WHERE a.id = @actor_id;
