-- 自分自身に関するAPI（ApiDesign.md 4章）が使う問い合わせ。
--
--   PATCH  /api/v1/me              4.2
--   POST   /api/v1/me/password     4.3
--   GET    /api/v1/me/tokens       4.4.1
--   DELETE /api/v1/me/tokens/:id   4.4.3
--
-- **6章（ユーザー管理）のクエリと分けてある。** 更新できる列が違うためである
-- ——本人は locale / timezone / theme / hue を変えられるが system_role は
-- 変えられず、管理者はその逆である（is_active と system_role を変え、
-- 見た目の設定は触らない）。1つの UPDATE に両方の列を並べると、どちらの
-- 経路からでも全列が書ける形になり、4.2 と 6.4 の境界が SQL から読めなくなる。

-- UpdateMyProfile は PATCH /me の app_user 側を更新する。
--
-- **email を含む。** この列はログインIDでもあるため、呼び出し側は
-- UpdateLocalIdentitySubject（user.sql）を同じトランザクションで呼ぶこと。
-- 追随させないと当人がログインできなくなる（Design.md 6.2.1 手順2〜3）。
--
-- **version は加算するが、条件には使わない**（ApiDesign.md 4.2）。
-- 自分の設定に楽観ロックを課さないためである。加算だけ行うのは、
-- 管理画面側（6.4 の If-Match）が同じ行を見ており、そちらの検出を
-- 壊さないようにするため。**したがって :exec でよい**——0行になる
-- 理由が「消えた」しか無く、version 不一致と切り分ける必要がない。
--
-- name: UpdateMyProfile :execrows
UPDATE app_user SET
  email    = COALESCE(sqlc.narg('email')::citext, email),
  locale   = COALESCE(sqlc.narg('locale'), locale),
  timezone = COALESCE(sqlc.narg('timezone'), timezone),
  theme    = COALESCE(sqlc.narg('theme'), theme),
  hue      = COALESCE(sqlc.narg('hue'), hue),
  version  = version + 1
WHERE actor_id = @actor_id;

-- UpdateMyDisplayName は PATCH /me の actor 側を更新する。
--
-- display_name は actor の列である（DbDesign.md 6.2）。**UpdateMyProfile と
-- 同じトランザクションで呼ぶ**こと。片方だけ成功すると、表示名は変わったのに
-- version が進んでいない状態が残る。
--
-- **is_active を持たない点が UpdateAdminUserActor との違いである。**
-- 本人が自分を無効化する経路を作らない（6.4 が self_modification_forbidden で
-- 弾いているものを、/me から回り込めるようにしない）。
--
-- name: UpdateMyDisplayName :exec
UPDATE actor SET
  display_name = COALESCE(sqlc.narg('display_name'), display_name)
WHERE id = @actor_id;

-- FindMyLocalCredential は POST /me/password が現在のパスワードを検証するために
-- 資格情報を引く。
--
-- **local プロバイダに限る。** OIDC/SAML のみのユーザー（構想）は行が
-- 返らず、呼び出し側が 409 conflict に倒す（ApiDesign.md 4.3）。
--
-- **結合条件は FindLocalLoginByEmail と揃える**（i.subject = u.email）。
-- 揃えないと、メール変更で subject が追随していない行をこちらだけが拾い、
-- 「パスワードは変えられるのにログインできない」という食い違いが起きる。
--
-- name: FindMyLocalCredential :one
SELECT
  i.id AS identity_id,
  c.password_hash
FROM app_user u
JOIN user_identity i
  ON i.user_id = u.actor_id
 AND i.provider_key = 'local'
 AND i.subject = u.email
JOIN local_credential c ON c.identity_id = i.id
WHERE u.actor_id = @actor_id;

-- ChangeMyPassword は POST /me/password の書き込み（ApiDesign.md 4.3）。
--
-- **must_change を false に落とす。** これをしないと、要パスワード変更で
-- 入った利用者が変更しても誘導が消えず、変更画面へ戻され続ける。
--
-- **failed_attempts と locked_until には触らない。** 4.3 の失敗を
-- アカウントロックの対象にしないと決めており（既にセッションを持つ本人の
-- 操作であり、ここで数えると自分で自分を締め出せる）、成功時にリセットする
-- 意味も無い。ログインの失敗回数は ResetLoginFailure がログイン成功時に消す。
--
-- name: ChangeMyPassword :exec
UPDATE local_credential
SET password_hash       = @password_hash,
    password_updated_at = now(),
    must_change         = false
WHERE identity_id = @identity_id;

-- RevokeMyOtherSessions は現在のトークン以外を失効させる（ApiDesign.md 4.3、
-- Design.md 6.3「変更時に当該ユーザーのセッションを全失効（現在のセッションを除く）」）。
--
-- **token_type で絞らない。** CLI用のAPIトークン（4.4）も切る。パスワードが
-- 漏れた疑いで変更する場面を想定すると、ブラウザだけ切って Bearer トークンを
-- 残す理由が無い。RevokeActorSessions（user.sql、管理者による全失効）と同じ
-- 考え方で、違いは「現在のトークンを残すか」の1点だけである。
--
-- 返す行数が「何本切ったか」で、監査ログの detail に入れる。
--
-- name: RevokeMyOtherSessions :execrows
UPDATE access_token
SET revoked_at = now()
WHERE actor_id = @actor_id
  AND id <> @current_token_id
  AND revoked_at IS NULL;

-- ── アクセストークン（ApiDesign.md 4.4）──────────────────────────────
--
-- **いずれも token_type = 'api' に限る。** ブラウザのセッション
-- （token_type='session'）とエージェント用（'agent'）を混ぜない。
-- 本人が自分のセッションを見る・切る画面を持たないと決めており
-- （GuiDesign.md 5.8）、混ぜると「一覧に出ているのに失効させられない行」が
-- 生まれる。DELETE の対象からも外れるので、現在のセッションを /me/tokens 経由で
-- 切ることはできない。
--
-- 発行そのものは auth.sql の CreateAccessToken を使う（セッションと同じ1文）。

-- ListMyAPITokens は GET /me/tokens の本体（ApiDesign.md 4.4.1）。
--
-- **失効済みは返さない。期限切れは返す。** 失効は本人が消したものであり、
-- 残すと増え続けて読めなくなる（記録は監査ログの token.revoke にある）。
-- 期限切れは「更新しないと使えない」と本人が気づく必要があり、かつ発行本数の
-- 上限5本を占めている。
--
-- **ListUserSessions（user.sql、6.3 の管理者向け）と条件が違う。** あちらは
-- 認証側と揃えて期限切れを落とすが、こちらは本人が管理するための一覧である。
--
-- name: ListMyAPITokens :many
SELECT
  t.id,
  t.name,
  t.token_prefix,
  t.scopes,
  t.issued_at,
  t.last_used_at,
  t.expires_at
FROM access_token t
WHERE t.actor_id = @actor_id
  AND t.token_type = 'api'
  AND t.revoked_at IS NULL
ORDER BY t.issued_at DESC;

-- CountMyAPITokens は発行本数の上限（1人5本。ApiDesign.md 4.4.2）を判定する。
--
-- **数え方は ListMyAPITokens と同一にする**（失効していないもの。期限切れを含む）。
-- 揃えないと、一覧に7行出ているのに「上限5本」と言われ、どれを失効させれば
-- 発行できるのかが画面から読めなくなる。
--
-- **呼び出し側はアクター行をロックし、CreateAccessToken と同じトランザクションで使うこと。** 別々に
-- 実行すると、同時に2本 POST されたときに上限を超える。
--
-- name: CountMyAPITokens :one
SELECT count(*) FROM access_token
WHERE actor_id = @actor_id
  AND token_type = 'api'
  AND revoked_at IS NULL;

-- FindMyAPIToken は DELETE /me/tokens/:id の対象を引く（ApiDesign.md 4.4.3）。
--
-- **actor_id と token_type を条件に含めるのが要点である。** 他人のトークンや
-- セッションを 403 ではなく 404 に倒すため（Design.md 6.4.5「存在を隠す」）、
-- 「見つからない」1つの結果に寄せる。
--
-- revoked_at を返すのは、既に失効済みかどうかを呼び出し側が知るためである
-- （冪等に 204 を返すが、監査ログは二重に書かない）。
--
-- name: FindMyAPIToken :one
SELECT
  t.id,
  t.name,
  t.token_prefix,
  t.revoked_at
FROM access_token t
WHERE t.id = @id
  AND t.actor_id = @actor_id
  AND t.token_type = 'api';

-- RevokeMyAPIToken は失効させる（ApiDesign.md 4.4.3）。
--
-- **行は消さない。** audit_log.token_id から辿れる先を残すためである。
-- 既に失効済みなら WHERE が外れ、revoked_at を上書きしない（冪等）。
--
-- auth.sql の RevokeAccessToken（ログアウト）と条件が違う。あちらは id だけで
-- 引く——認証を通ったトークン自身を切るので、持ち主の確認が済んでいる。
--
-- name: RevokeMyAPIToken :execrows
UPDATE access_token
SET revoked_at = now()
WHERE id = @id
  AND actor_id = @actor_id
  AND token_type = 'api'
  AND revoked_at IS NULL;
