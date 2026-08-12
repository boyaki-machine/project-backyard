-- アクターとユーザーに関するクエリ（DbDesign.md 6.2）。
--
-- 手順3で pb admin create が直接書いていたSQLを sqlc へ移したもの。

-- name: CountAdministrators :one
SELECT count(*) FROM app_user WHERE system_role = 'administrator';

-- name: CreateUserActor :exec
INSERT INTO actor (id, kind, display_name) VALUES (@id, 'user', @display_name);

-- name: CreateAdministrator :exec
INSERT INTO app_user (actor_id, email, system_role) VALUES (@actor_id, @email, 'administrator');

-- name: CreateUserIdentity :exec
INSERT INTO user_identity (id, user_id, provider_key, subject)
VALUES (@id, @user_id, @provider_key, @subject);

-- name: CreateLocalCredential :exec
INSERT INTO local_credential (identity_id, password_hash) VALUES (@identity_id, @password_hash);
