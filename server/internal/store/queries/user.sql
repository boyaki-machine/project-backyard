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

-- must_change を明示で受ける（DbDesign.md 6.2 の既定は false）。
-- POST /admin/users（ApiDesign.md 6.2）が must_change_password: true を
-- 既定とするため、列の既定値任せにできない。**呼び出し側は Go の
-- ゼロ値に頼らず明示的に書く**こと。どの経路が初回変更を要求するのかを
-- 呼び出し箇所だけで読めるようにするためである。
-- name: CreateLocalCredential :exec
INSERT INTO local_credential (identity_id, password_hash, must_change)
VALUES (@identity_id, @password_hash, @must_change);

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

-- ── ユーザー管理（ApiDesign.md 6章、手順12a）─────────────────────

-- ListAdminUsers は GET /admin/users の1ページ分を返す（ApiDesign.md 6.1）。
--
-- **人間とエージェントを同じ一覧に並べる**（GuiDesign.md 5.6、DbDesign.md 6.2 の
-- actor 統合設計）。したがって起点は app_user ではなく actor で、app_user は
-- LEFT JOIN になる。エージェントは app_user の行を持たないため、email と
-- system_role と last_login_at は NULL で返る（6.1 の「意味を持たない
-- フィールドは null」に一致する）。
--
-- **kind = 'system' の actor は除く。** 6.1 が列挙するのは user / agent / all の
-- 3つで、システムアクター（バッチ等が使う DbDesign.md 6.2 の3種目）は
-- 利用者が管理する対象ではない。Phase 1 のシードは system アクターを作らないが、
-- 将来作られても一覧に紛れ込まないようにここで落とす。
--
-- **project_count は project_member の行数**で、アーカイブ済みプロジェクトも
-- 数える。除くと詳細画面（6.3 の memberships）に並ぶ件数と食い違うため。
--
-- q は呼び出し側で LIKE のメタ文字をエスケープ済みのパターンを受け取る
-- （空文字なら絞り込まない）。SQL 側で escape すると入れ子が深くなり、
-- どの層でエスケープしたのかが読めなくなる。
--
-- 並び替えを CASE 式で静的に書く理由は ListProjects と同じ（sqlc は動的な
-- ORDER BY を組み立てられない）。display_name の比較に ICU collation を
-- 指定するのは DbDesign.md 4.4 の規約。
--
-- **system_role は role.sort_order で並べる**（ApiDesign.md 6.1）。表示名の
-- 五十音順ではない——シードが意図して序列を持っており（オペレータ 10 →
-- アドミニストレータ 20。DbDesign.md 7.3）、Phase 3 でカスタムロールが増えたとき
-- 表示名順では意味のない並びになる。**ロールを持たない行（エージェント）は
-- 昇順・降順とも末尾に置く**（NULLS LAST を両方に明示する。Postgres の既定は
-- DESC で NULLS FIRST であり、明示しないと先頭へ来る）。
--
-- **is_active の昇順は無効が先**（false < true をそのまま使う）。状態で並べ替える
-- 動機は「無効な利用者を探す」ことが多いため。
--
-- **q はロールの表示名にも当てる**（同 6.1）。画面に出ている文字列で探せることが
-- 目的なので、画面に出ないキー（administrator）は対象にしない。
--
-- name: ListAdminUsers :many
WITH filtered AS (
  SELECT
    a.id,
    a.kind,
    a.display_name,
    a.is_active,
    a.created_at,
    u.email,
    u.system_role,
    u.last_login_at,
    r.sort_order AS role_sort_order,
    (SELECT count(*) FROM project_member pm WHERE pm.actor_id = a.id) AS project_count
  FROM actor a
  LEFT JOIN app_user u ON u.actor_id = a.id
  LEFT JOIN role r ON r.key = u.system_role AND r.scope = 'system'
  WHERE a.kind <> 'system'
    AND (@kind_filter::text = 'all' OR a.kind = @kind_filter::text)
    AND (@active_filter::text = 'all' OR a.is_active = (@active_filter::text = 'true'))
    AND (
      @q_pattern::text = ''
      OR a.display_name ILIKE @q_pattern::text
      OR u.email::text ILIKE @q_pattern::text
      OR r.display_name ILIKE @q_pattern::text
    )
)
SELECT
  f.id, f.kind, f.display_name, f.email, f.system_role,
  f.is_active, f.last_login_at, f.project_count, f.created_at
FROM filtered f
ORDER BY
  CASE WHEN @sort::text = 'display_name'  AND @sort_order::text = 'asc'  THEN f.display_name COLLATE "ja-JP-x-icu" END ASC,
  CASE WHEN @sort::text = 'display_name'  AND @sort_order::text = 'desc' THEN f.display_name COLLATE "ja-JP-x-icu" END DESC,
  CASE WHEN @sort::text = 'email'         AND @sort_order::text = 'asc'  THEN f.email END ASC,
  CASE WHEN @sort::text = 'email'         AND @sort_order::text = 'desc' THEN f.email END DESC,
  CASE WHEN @sort::text = 'system_role'   AND @sort_order::text = 'asc'  THEN f.role_sort_order END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'system_role'   AND @sort_order::text = 'desc' THEN f.role_sort_order END DESC NULLS LAST,
  CASE WHEN @sort::text = 'is_active'     AND @sort_order::text = 'asc'  THEN f.is_active END ASC,
  CASE WHEN @sort::text = 'is_active'     AND @sort_order::text = 'desc' THEN f.is_active END DESC,
  CASE WHEN @sort::text = 'last_login_at' AND @sort_order::text = 'asc'  THEN f.last_login_at END ASC,
  CASE WHEN @sort::text = 'last_login_at' AND @sort_order::text = 'desc' THEN f.last_login_at END DESC,
  CASE WHEN @sort::text = 'created_at'    AND @sort_order::text = 'asc'  THEN f.created_at END ASC,
  CASE WHEN @sort::text = 'created_at'    AND @sort_order::text = 'desc' THEN f.created_at END DESC,
  f.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- SummarizeAdminUsers は ListAdminUsers と同じ絞り込みに対する総件数と
-- 最終更新日時を返す。total は 2.6、last_updated_at は 2.7 の ETag の材料。
--
-- **WHERE は ListAdminUsers と一字一句そろえる。** 片方だけ直すと、total が
-- items と食い違ったページャが出る。
--
-- **updated_at は actor と app_user の新しいほうを採る。** システムロールの
-- 変更は app_user の行だけを更新し（trg_app_user_updated）、actor.updated_at は
-- 動かない。actor だけを見ると、ロールを変えても ETag が変わらない。
--
-- name: SummarizeAdminUsers :one
SELECT
  count(*)                                                    AS total,
  max(GREATEST(a.updated_at, COALESCE(u.updated_at, a.updated_at)))::timestamptz AS last_updated_at
FROM actor a
LEFT JOIN app_user u ON u.actor_id = a.id
LEFT JOIN role r ON r.key = u.system_role AND r.scope = 'system'
WHERE a.kind <> 'system'
  AND (@kind_filter::text = 'all' OR a.kind = @kind_filter::text)
  AND (@active_filter::text = 'all' OR a.is_active = (@active_filter::text = 'true'))
  AND (
    @q_pattern::text = ''
    OR a.display_name ILIKE @q_pattern::text
    OR u.email::text ILIKE @q_pattern::text
    OR r.display_name ILIKE @q_pattern::text
  );
