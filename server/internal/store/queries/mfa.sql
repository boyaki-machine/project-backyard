-- 多要素認証のクエリ（DbDesign.md 6.18、Design.md 6.7）。pb-103。
--
-- **未確定の行（confirmed_at IS NULL）を、確定済みを引くクエリに混ぜない。**
-- 認証の要素として数えないためであり、条件はクエリ側に閉じ込めてある——
-- ハンドラが毎回書くと、書き忘れた1本が「照合していない認証器で関門が立つ」を招く。

-- ── 認証器 ──────────────────────────────────────────────

-- ListConfirmedMfaCredentials は本人の確定済みの認証器を返す（ApiDesign.md 4.6.1）。
-- name: ListConfirmedMfaCredentials :many
SELECT id, name, kind, created_at, last_used_at
FROM user_mfa_credential
WHERE user_id = @user_id
  AND confirmed_at IS NOT NULL
ORDER BY created_at;

-- CountConfirmedMfaCredentials は上限（5件）とログイン時の分岐に使う。
--
-- **ログインのたびに通る。** 行そのものは要らず、1件でもあるかを見る
-- （Design.md 6.7.4 の手順2）。
-- name: CountConfirmedMfaCredentials :one
SELECT count(*) FROM user_mfa_credential
WHERE user_id = @user_id AND confirmed_at IS NOT NULL;

-- ListConfirmedMfaSecrets は照合のために共有秘密ごと引く。
--
-- **封じられたまま返す。** 復号はハンドラが app_secret の鍵で行う
-- （Design.md 6.7.3）。**どの認証器で通るかは分からない**ので、
-- 確定済みを全部返して順に試す（1人5件までなので許容できる）。
-- name: ListConfirmedMfaSecrets :many
SELECT id, name, secret, secret_nonce, last_used_step
FROM user_mfa_credential
WHERE user_id = @user_id AND confirmed_at IS NOT NULL
ORDER BY created_at;

-- FindPendingMfaCredential は登録の途中の行を引く（ApiDesign.md 4.6.3）。
--
-- **user_id を条件に含めるのが要点である。** 他人の id を渡されても行が返らない
-- ので、404 に落ちる（存在を漏らさない。Design.md 6.4.5）。
-- name: FindPendingMfaCredential :one
SELECT id, name, secret, secret_nonce, failed_attempts
FROM user_mfa_credential
WHERE id = @id AND user_id = @user_id AND confirmed_at IS NULL;

-- FindMfaCredentialByName は同じ名前の確定済みがあるかを見る（409 の判定）。
-- name: FindMfaCredentialByName :one
SELECT id FROM user_mfa_credential
WHERE user_id = @user_id AND name = @name AND confirmed_at IS NOT NULL;

-- DeletePendingMfaCredentials は登録を始め直したときに古い途中の行を捨てる。
--
-- **途中の行は1人1件まで**という規則をここで守る（DbDesign.md 6.18。DB では縛らない）。
-- name: DeletePendingMfaCredentials :exec
DELETE FROM user_mfa_credential
WHERE user_id = @user_id AND confirmed_at IS NULL;

-- CreateMfaCredential は登録の途中の行を作る（confirmed_at は NULL のまま）。
-- name: CreateMfaCredential :exec
INSERT INTO user_mfa_credential (id, user_id, kind, name, secret, secret_nonce)
VALUES (@id, @user_id, @kind, @name, @secret, @secret_nonce);

-- ConfirmMfaCredential は照合が通った行を確定させる。
--
-- **confirmed_at IS NULL を条件に残す。** 二重に送られたときに2回目が0行になり、
-- 呼び出し側が 404 に落とせる。
-- name: ConfirmMfaCredential :one
UPDATE user_mfa_credential
SET confirmed_at = now(), last_used_step = @last_used_step,
    last_used_at = now(), failed_attempts = 0
WHERE id = @id AND user_id = @user_id AND confirmed_at IS NULL
RETURNING id, name, kind, created_at, last_used_at;

-- RecordMfaCredentialFailure は登録時の照合失敗を数える（5回で捨てる）。
-- name: RecordMfaCredentialFailure :one
UPDATE user_mfa_credential
SET failed_attempts = failed_attempts + 1
WHERE id = @id AND user_id = @user_id AND confirmed_at IS NULL
RETURNING failed_attempts;

-- TouchMfaCredentialUsed は照合が通った刻みを保存する。
--
-- **同じ刻みのコードを2回受け付けない**という規則の保存側である（Design.md 6.7.2）。
-- name: TouchMfaCredentialUsed :exec
UPDATE user_mfa_credential
SET last_used_step = @last_used_step, last_used_at = now()
WHERE id = @id;

-- DeleteMfaCredential は本人の認証器を消す（ApiDesign.md 4.6.4）。
--
-- **行を消す。** access_token のように revoked_at を立てる形にしないのは、
-- 残した行が「登録されているのに効かない認証器」として一覧の判定を複雑にするため。
-- name: DeleteMfaCredential :execrows
DELETE FROM user_mfa_credential
WHERE id = @id AND user_id = @user_id AND confirmed_at IS NOT NULL;

-- DeleteAllMfaCredentials は管理者の解除と CLI が使う（ApiDesign.md 6.9）。
-- name: DeleteAllMfaCredentials :execrows
DELETE FROM user_mfa_credential WHERE user_id = @user_id;

-- ── ログインの挑戦 ──────────────────────────────────────

-- CreateMfaLoginChallenge は中途状態を1件作る（Design.md 6.7.4 の手順3）。
-- name: CreateMfaLoginChallenge :exec
INSERT INTO mfa_login_challenge (id, user_id, token_hash, expires_at)
VALUES (@id, @user_id, @token_hash, @expires_at);

-- FindMfaLoginChallenge は受け取った平文の SHA-256 で挑戦を引く。
--
-- **有効性を WHERE で絞らない。** access_token と同じ考え方で（6.2 の
-- FindAccessTokenByHash）、「無い」と「期限切れ」を区別してサーバログに残す。
-- 応答はどちらも 401 で統一する。
-- name: FindMfaLoginChallenge :one
SELECT c.id, c.user_id, c.attempts, c.expires_at, c.consumed_at,
       a.display_name, a.is_active,
       u.email, u.system_role, u.locale, u.timezone, u.theme, u.hue,
       lc.must_change
FROM mfa_login_challenge c
JOIN actor a ON a.id = c.user_id
JOIN app_user u ON u.actor_id = c.user_id
LEFT JOIN user_identity ui
  ON ui.user_id = u.actor_id AND ui.provider_key = 'local'
LEFT JOIN local_credential lc ON lc.identity_id = ui.id
WHERE c.token_hash = @token_hash;

-- RecordMfaChallengeFailure は試行回数を加算し、加算後の値を返す。
-- name: RecordMfaChallengeFailure :one
UPDATE mfa_login_challenge SET attempts = attempts + 1
WHERE id = @id
RETURNING attempts;

-- ConsumeMfaLoginChallenge は挑戦を使い切った印を付ける。
--
-- **consumed_at IS NULL を条件に残す。** 同じ挑戦で2本のセッションを出さない。
-- name: ConsumeMfaLoginChallenge :execrows
UPDATE mfa_login_challenge SET consumed_at = now()
WHERE id = @id AND consumed_at IS NULL;

-- DeleteMfaLoginChallengesForUser は古い挑戦を片付ける。
--
-- **新しい挑戦を作る前に呼ぶ。** 期限切れの行が積むのを防ぐ掃除を、
-- 専用のバッチではなく**次のログインに相乗りさせる**（1人あたり数行で済む）。
-- name: DeleteMfaLoginChallengesForUser :exec
DELETE FROM mfa_login_challenge WHERE user_id = @user_id;

-- ── リカバリコード ──────────────────────────────────────

-- CountUnusedRecoveryCodes は残数（ApiDesign.md 4.6.1）。
-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM mfa_recovery_code
WHERE user_id = @user_id AND used_at IS NULL;

-- GetRecoveryCodeStatus は残数と発行時刻をまとめて返す。
--
-- **1本も持たないときは行が返らない**ので、呼び出し側は null を返せる
-- （「作って全部使った」と「まだ無い」を区別する。ApiDesign.md 4.6.1）。
-- name: GetRecoveryCodeStatus :one
-- **min() に型を明示する。** 付けないと sqlc が interface{} で生成し、
-- 呼び出し側が時刻として扱えない。
SELECT count(*) FILTER (WHERE used_at IS NULL) AS remaining,
       min(created_at)::timestamptz AS generated_at
FROM mfa_recovery_code
WHERE user_id = @user_id
HAVING count(*) > 0;

-- CreateRecoveryCode は1本を保存する（平文は保存しない）。
-- name: CreateRecoveryCode :exec
INSERT INTO mfa_recovery_code (id, user_id, code_hash)
VALUES (@id, @user_id, @code_hash);

-- ConsumeRecoveryCode は未使用の1本を消費する。
--
-- **used_at IS NULL を条件に含めるのが要点である。** 同じコードを2回使えない。
-- 0行なら「無い」か「既に使った」で、どちらも応答は同じ 401 である。
-- name: ConsumeRecoveryCode :execrows
UPDATE mfa_recovery_code SET used_at = now()
WHERE user_id = @user_id AND code_hash = @code_hash AND used_at IS NULL;

-- DeleteRecoveryCodes は全部消す（作り直し・最後の認証器の削除・管理者の解除）。
-- name: DeleteRecoveryCodes :execrows
DELETE FROM mfa_recovery_code WHERE user_id = @user_id;
