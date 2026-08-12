-- 認可（実効権限の材料）に関するクエリ（Design.md 6.4.1、DbDesign.md 6.3）。
--
-- 権限は「コードのif文ではなくデータとして定義する」（Design.md 6.4.2）ため、
-- 判定に使う権限キーは必ずここのクエリでDBから取る。Go 側に権限の割り当てを
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

-- FindProjectAuthzByKey は、プロジェクトキー1つに対する認可の材料を返す。
-- RequireProjectPermission（Design.md 6.4.4）が使う。
--
-- **project を起点にした LEFT JOIN にしてある。** 返る行数で3つの状態を
-- 区別するためである。
--
--   0行                    プロジェクトが存在しない          → 404
--   1行以上・role_key NULL プロジェクトはあるが非メンバー    → 到達可否の判定へ
--   1行以上・role_key あり メンバー。permission_key が権限   → 実効権限の計算へ
--
-- ListProjectMembershipsByActor（project_member 起点）では、非メンバーと
-- 「プロジェクトが存在しない」がどちらも0行になって区別できない。
-- Design.md 6.4.5 は他プロジェクトの存在を隠すよう求めており、
-- 「存在しない」と「見えない」を同じ 404 に**倒す判断はアプリ側で行う**。
-- クエリはその判断ができるだけの事実を返す。
--
-- 権限を1件も持たないロールでも行を返すよう、role_permission も LEFT JOIN。
--
-- アーカイブ済みプロジェクトも返す。アーカイブは status で表す状態であって
-- 不可視にするものではない（ApiDesign.md 5.6）。
--
-- name: FindProjectAuthzByKey :many
SELECT
  p.id     AS project_id,
  p.key    AS project_key,
  p.status AS project_status,
  pm.role_key,
  rp.permission_key
FROM project p
LEFT JOIN project_member pm
       ON pm.project_id = p.id AND pm.actor_id = @actor_id
LEFT JOIN role_permission rp
       ON rp.role_key = pm.role_key
WHERE p.key = @project_key
ORDER BY rp.permission_key;
