-- パスキーのクエリ（DbDesign.md 6.19、Design.md 6.8）。pb-104。
--
-- **公開鍵と credential_id は、一覧のクエリで返さない。** 画面に要らず
-- （ApiDesign.md 4.7.1）、返す口を増やすほど鍵の材料が漏れる経路が増える。
-- 照合と登録のクエリだけが扱う。

-- ── パスキー ──────────────────────────────────────────────

-- ListPasskeys は本人のパスキーを返す（ApiDesign.md 4.7.1）。
-- name: ListPasskeys :many
SELECT id, name, rp_id, backup_state, created_at, last_used_at
FROM user_passkey
WHERE user_id = @user_id
ORDER BY created_at;

-- CountPasskeys は上限（5件）に使う。
-- name: CountPasskeys :one
SELECT count(*) FROM user_passkey WHERE user_id = @user_id;

-- ListPasskeyDescriptors は excludeCredentials の材料を返す（ApiDesign.md 4.7.2）。
--
-- **同じホスト名で登録したものだけを返す。** RP ID が違うパスキーは、
-- ブラウザがそもそも同じ認証器として扱わない（Design.md 6.8.3）。
-- name: ListPasskeyDescriptors :many
SELECT credential_id, transports
FROM user_passkey
WHERE user_id = @user_id AND rp_id = @rp_id
ORDER BY created_at;

-- FindPasskeyByName は同名の有無を確かめる。
--
-- **一意索引と同じ条件を先に見る**ので、制約違反ではなく 409 already_exists として返せる。
-- name: FindPasskeyByName :one
SELECT id FROM user_passkey WHERE user_id = @user_id AND name = @name;

-- CreatePasskey は検証の済んだパスキーを1件保存する（ApiDesign.md 4.7.3）。
--
-- created_at を返すのは、201 の応答に DB の値をそのまま載せるためである。
-- name: CreatePasskey :one
INSERT INTO user_passkey (
  id, user_id, name, credential_id, public_key, rp_id,
  attestation_type, attestation_format, aaguid, attachment, transports,
  sign_count, user_verified, backup_eligible, backup_state
) VALUES (
  @id, @user_id, @name, @credential_id, @public_key, @rp_id,
  @attestation_type, @attestation_format, @aaguid, @attachment, @transports,
  @sign_count, @user_verified, @backup_eligible, @backup_state
)
RETURNING created_at;

-- FindPasskeyLogin はログインの照合のために、パスキーと利用者を1回で引く
-- （Design.md 6.8.2）。
--
-- **credential_id だけで引く。** ログインのときは誰のパスキーかが分からない。
-- 応答（3.1 と同一構造）に要る利用者の属性も一緒に返し、往復を増やさない
-- ——6.18 の FindMfaLoginChallenge と同じ形である。
-- name: FindPasskeyLogin :one
SELECT p.id, p.user_id, p.name, p.credential_id, p.public_key,
       p.attestation_type, p.attestation_format, p.aaguid, p.attachment, p.transports,
       p.sign_count, p.user_verified, p.backup_eligible, p.backup_state,
       a.display_name, a.is_active,
       u.email, u.system_role, u.locale, u.timezone, u.theme, u.hue,
       lc.must_change
FROM user_passkey p
JOIN actor a ON a.id = p.user_id
JOIN app_user u ON u.actor_id = p.user_id
LEFT JOIN user_identity ui
  ON ui.user_id = u.actor_id AND ui.provider_key = 'local'
LEFT JOIN local_credential lc ON lc.identity_id = ui.id
WHERE p.credential_id = @credential_id;

-- TouchPasskeyUsed はログインが通ったあとに書き戻す（DbDesign.md 6.19）。
--
-- **backup_eligible は書かない。** 変わってはならない値であり、変わっていたら
-- ライブラリの検証が先に拒んでいる。
-- name: TouchPasskeyUsed :exec
UPDATE user_passkey
SET sign_count = @sign_count,
    user_verified = @user_verified,
    backup_state = @backup_state,
    last_used_at = now()
WHERE id = @id;

-- DeletePasskey は本人のパスキーを1件消す（ApiDesign.md 4.7.4）。
--
-- **user_id を条件に含めるのが要点である。** 他人の id では行が返らず 404 になる。
-- 名前を返すのは監査の detail に入れるためである。
-- name: DeletePasskey :one
DELETE FROM user_passkey
WHERE id = @id AND user_id = @user_id
RETURNING name;

-- DeleteAllPasskeys は管理者による全削除（ApiDesign.md 6.10）。
-- name: DeleteAllPasskeys :execrows
DELETE FROM user_passkey WHERE user_id = @user_id;

-- ── WebAuthn の挑戦 ────────────────────────────────────────

-- CreateWebauthnChallenge は挑戦を1件作る。
--
-- **user_id はログインでは NULL、登録では本人である。** 取り違えは CHECK が弾く
-- （DbDesign.md 6.19）。
-- name: CreateWebauthnChallenge :exec
INSERT INTO webauthn_challenge (id, purpose, user_id, challenge, session, expires_at)
VALUES (@id, @purpose, sqlc.narg('user_id'), @challenge, @session, @expires_at);

-- DeleteExpiredWebauthnChallenges は期限切れの挑戦を全部消す。
--
-- **挑戦を作るたびに呼ぶ。** 専用のバッチを持たない（DbDesign.md 6.19）。
-- name: DeleteExpiredWebauthnChallenges :exec
DELETE FROM webauthn_challenge WHERE expires_at < now();

-- DeleteWebauthnChallengesForUser は本人の挑戦（＝登録の挑戦）を消す。
--
-- 登録を始め直したとき（ApiDesign.md 4.7.2）と、管理者の全削除（6.10）で使う。
-- name: DeleteWebauthnChallengesForUser :execrows
DELETE FROM webauthn_challenge WHERE user_id = @user_id;

-- FindWebauthnChallenge は clientDataJSON の challenge で挑戦を引く。
--
-- **有効性を WHERE で絞らない。** 「無い」「期限切れ」「消費済み」を
-- 監査の reason で区別するためである（FindMfaLoginChallenge と同じ考え方）。
-- name: FindWebauthnChallenge :one
SELECT id, purpose, user_id, session, expires_at, consumed_at
FROM webauthn_challenge
WHERE challenge = @challenge AND purpose = @purpose;

-- ConsumeWebauthnChallenge は挑戦を使い切った印を付ける。
--
-- **検証より先に呼ぶ。** consumed_at IS NULL を条件にしてあるので、
-- 同じ応答を並行して2回送られても、通るのは1回だけになる。
-- name: ConsumeWebauthnChallenge :execrows
UPDATE webauthn_challenge SET consumed_at = now()
WHERE id = @id AND consumed_at IS NULL;
