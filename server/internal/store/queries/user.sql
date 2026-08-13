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

-- 以下は pb dev seed（DbDesign.md 7.6）が使う。

-- name: CountAppUsers :one
SELECT count(*) FROM app_user;

-- name: FindActorIDByEmail :one
SELECT actor_id FROM app_user WHERE email = @email;

-- system_role を引数に取る点だけが CreateAdministrator と違う。
-- name: CreateAppUser :exec
INSERT INTO app_user (actor_id, email, system_role)
VALUES (@actor_id, @email, @system_role);

-- actor を消せば app_user / user_identity / local_credential /
-- project_member / access_token は ON DELETE CASCADE で追従する（DbDesign.md 6.2 / 6.3）。
-- name: DeleteActorByEmail :execrows
DELETE FROM actor WHERE id = (SELECT actor_id FROM app_user WHERE email = @email);
