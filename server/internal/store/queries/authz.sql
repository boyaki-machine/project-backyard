-- 認可（実効権限の材料）に関するクエリ（Design.md 6.4.1、DbDesign.md 6.3）。
--
-- 権限は「コードのif文ではなくデータとして定義する」（Design.md 6.4.2）ため、
-- 判定に使う権限キーは必ずこの2本でDBから取る。Go 側に権限の割り当てを
-- 書き写さない。写すと DbDesign.md 7.2 / 7.3 のシードと二重管理になる。

-- ListRolePermissions はロール1つに割り当てられた権限キーを返す。
--
-- app_user.system_role（'operator' / 'administrator'）は role.key と同じ
-- 語彙であるため、システムロールの権限もこのクエリで引ける（DbDesign.md 7.3）。
--
-- name: ListRolePermissions :many
SELECT permission_key
FROM role_permission
WHERE role_key = @role_key
ORDER BY permission_key;

-- ListProjectMembershipsByActor は所属プロジェクトと、そこでの
-- プロジェクトロール由来の権限キーを返す（ApiDesign.md 3.1 の projects[]）。
--
-- **プロジェクトごとに複数行が返る**（権限の数だけ）。呼び出し側で畳む。
-- role_permission を LEFT JOIN にしているのは、権限を1件も持たないロールが
-- 割り当てられていても、プロジェクトの行自体は返すため。
--
-- app_user ではなく actor で引くのは、エージェントもプロジェクトのメンバーに
-- なれるため（DbDesign.md 6.3）。
--
-- アーカイブ済みプロジェクトも返す。この一覧はフロントの権限判定に使うもので
-- （Design.md 6.4.4）、表示用の絞り込みは GET /projects 側の役割である。
--
-- name: ListProjectMembershipsByActor :many
SELECT
  p.id   AS project_id,
  p.key  AS project_key,
  p.name AS project_name,
  p.status,
  pm.role_key,
  rp.permission_key
FROM project_member pm
JOIN project p ON p.id = pm.project_id
LEFT JOIN role_permission rp ON rp.role_key = pm.role_key
WHERE pm.actor_id = @actor_id
ORDER BY p.key, rp.permission_key;
