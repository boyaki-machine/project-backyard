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

-- ── 実効権限のセッションキャッシュ（Design.md 6.4.5、手順6b） ──────────
--
-- 「ログインごとに実効権限を計算し、セッションにキャッシュする。
--   ロール変更時は当該ユーザーのキャッシュを無効化する」
--
-- 置き場は access_token の cached_permissions / permissions_cached_at
-- （0012、DbDesign.md 6.2）。入るのはシステムロールの層のみである。

-- SaveTokenPermissionCache は1つのトークンにキャッシュを書く。
--
-- permissions_cached_at はDBの now() で入れる。アプリ側の時刻を渡さないのは、
-- 複数プロセスから書いても TTL の起点が1つの時計に揃うようにするためである。
--
-- **@system_role は「この権限を計算したときのロール」であり、書き込みの条件になる。**
-- 無条件の UPDATE にすると、次の順序で無効化を追い越す。
--
--   1. リクエストR が role_permission を読む（旧ロールの権限）
--   2. 管理者がロールを変更し InvalidateActorPermissionCache を実行
--   3. リクエストR が旧権限を書き戻す ← 無効化が成功を返したのに旧権限が TTL ぶん復活する
--
-- 降格の場合、高いほうの権限が残ることになる。EXISTS でロールを照合すれば
-- 2 を経た書き込みは0行更新で落ちる。**落ちても良い**（次のリクエストが計算し直す）。
--
-- ロールの割り当てそのものを変えた場合（role_permission のシードをマイグレーションで
-- 書き換えた場合）はこの条件を通り抜けるが、そちらは TTL が拾う（Design.md 6.4.5）。
--
-- app_user を持たないアクター（エージェント）では EXISTS が常に偽になる。
-- 呼び出し側がシステムロールを持たないアクターを除いているため、ここへは来ない。
--
-- name: SaveTokenPermissionCache :exec
UPDATE access_token
SET cached_permissions    = @cached_permissions,
    permissions_cached_at = now()
WHERE id = @id
  AND EXISTS (
    SELECT 1
    FROM app_user u
    WHERE u.actor_id    = access_token.actor_id
      AND u.system_role = @system_role
  );

-- InvalidateActorPermissionCache は、あるアクターの**全トークン**の
-- キャッシュを捨てる（Design.md 6.4.5「ロール変更時は当該ユーザーの
-- キャッシュを無効化する」）。
--
-- **トークン単位ではなくアクター単位で消す。** 権限を変えられた本人は
-- 複数のセッション（PC・スマートフォン）とAPIトークンを持ちうるので、
-- 1つだけ消しても他の経路から古い権限で通れてしまう。
--
-- 呼び出し側は ApiDesign.md 6.4 の PATCH /admin/users/:id（system_role の
-- 変更）と、メンバーシップの操作である。**いずれも手順10 のエンドポイント**
-- であり、Phase 1 の現時点では呼び出し元がまだ無い。
--
-- 権限を消す操作なので、失敗したら業務処理ごと失敗させること（RecordOrLog
-- ではなく Record と同じ扱い）。消せなかったまま成功を返すと、降格したはずの
-- 利用者が TTL の間だけ旧権限で動く。
--
-- name: InvalidateActorPermissionCache :exec
UPDATE access_token
SET cached_permissions    = NULL,
    permissions_cached_at = NULL
WHERE actor_id = @actor_id
  AND permissions_cached_at IS NOT NULL;

-- ── ロール・権限カタログ（ApiDesign.md 7.1 / 7.2、手順14）─────────────
--
-- GuiDesign.md 5.6.3 の権限マトリクスと、画面がロールの表示名を引くための
-- 問い合わせ。**マトリクス専用のクエリは作らない**——7.1 と 7.2 の2つを
-- 組み合わせて画面が組み立てる（7.2 末尾）。

-- ListRoles はロールのカタログを返す（ApiDesign.md 7.1）。
--
-- @scope_filter は 'system' / 'project' / 'all'。ListAdminUsers の
-- kind_filter と同じ書き方で、'all' のときだけ絞り込みを外す。
--
-- **並びは role.sort_order である**（7.1、GuiDesign.md 5.6）。表示名の
-- 五十音順ではない。シードが意図して序列を持っており（DbDesign.md 7.3、
-- オペレータ 10 → アドミニストレータ 20 → プロジェクト管理者 30 → …）、
-- Phase 3 でカスタムロールが増えたときに表示名順では意味のない並びになる。
-- 同着のときは key で並べ、応答が呼ぶたびに入れ替わらないようにする。
--
-- name: ListRoles :many
SELECT key, scope, display_name, description, is_builtin, sort_order
FROM role
WHERE @scope_filter::text = 'all' OR scope = @scope_filter::text
ORDER BY sort_order, key;

-- ListRolePermissionAssignments は、ListRoles と同じ絞り込みに対する
-- ロールと権限の割り当てを (role_key, permission_key) の対で返す
-- （ApiDesign.md 7.1 の permissions[]）。
--
-- **ロール1件ずつ問い合わせない。** ロールの数だけ往復すると N+1 になる
-- （ApiDesign.md 1.2 の設計方針3）。呼び出し側が role_key で畳む。
--
-- **既存の ListRolePermissions（キーの昇順）とは別に置く。** あちらは
-- 認可判定の材料で、並びに意味が無い。こちらは画面に出る順序であり、
-- permission.sort_order でなければマトリクスの行の並びと食い違う。
--
-- **WHERE は ListRoles と一字一句そろえる。** 片方だけ直すと、返らない
-- ロールの権限だけが応答に混ざる。
--
-- name: ListRolePermissionAssignments :many
SELECT rp.role_key, rp.permission_key
FROM role_permission rp
JOIN role r       ON r.key = rp.role_key
JOIN permission p ON p.key = rp.permission_key
WHERE @scope_filter::text = 'all' OR r.scope = @scope_filter::text
ORDER BY r.sort_order, r.key, p.sort_order, p.key;

-- ListPermissions は権限カタログを返す（ApiDesign.md 7.2）。
--
-- 正本は DbDesign.md 7.2 のシード（0010、28件）と 8.1.4（0017、doc の2件）で
-- 計30件。並びは permission.sort_order で、
-- category ごとに連続するよう採番されている（project 10番台 / ticket 20番台
-- / …）。GuiDesign.md 5.6.3 がカテゴリで行を区切れるのはこのためである。
--
-- name: ListPermissions :many
SELECT key, category, description, sort_order
FROM permission
ORDER BY sort_order, key;
