-- プロジェクトとワークフローに関するクエリ（DbDesign.md 6.4 / 6.5）。
--
-- 手順7.5（pb dev seed）で必要になった分から始まり、手順9で
-- GET/POST /projects と check-key（ApiDesign.md 5.1〜5.3）、手順11で
-- GET/PATCH /projects/:key と archive/unarchive（5.4〜5.6）が加わった。
-- テンプレートの複製は pb dev seed と POST /projects で同じ手順を通る。

-- name: FindProjectIDByKey :one
SELECT id FROM project WHERE key = @key;

-- workflow_id は後から埋める。非テンプレートの workflow は project_id が
-- NOT NULL 相当（ck_workflow_template）で、プロジェクトより先に作れないため。
-- name: CreateProject :exec
INSERT INTO project (id, key, name, description, created_by)
VALUES (@id, @key, @name, @description, @created_by);

-- name: SetProjectWorkflow :exec
UPDATE project SET workflow_id = @workflow_id WHERE id = @id;

-- name: CreateProjectCounter :exec
INSERT INTO project_counter (project_id) VALUES (@project_id);

-- ロールの妥当性はDBに問い合わせる。Go 側に 'project_admin' などを
-- 書き写すと 0010 のシード（DbDesign.md 7.3）と二重管理になるため。
-- name: IsProjectScopedRole :one
SELECT EXISTS (SELECT 1 FROM role WHERE key = @key AND scope = 'project');

-- name: AddProjectMember :exec
INSERT INTO project_member (project_id, actor_id, role_key)
VALUES (@project_id, @actor_id, @role_key)
ON CONFLICT (project_id, actor_id) DO NOTHING;

-- project_counter / project_member / workflow（と配下の status・transition）は
-- ON DELETE CASCADE で追従する（DbDesign.md 6.4 / 6.5）。
-- name: DeleteProjectByKey :execrows
DELETE FROM project WHERE key = @key;

-- ── テンプレートの複製（ApiDesign.md 5.3）─────────────────────

-- name: FindWorkflowTemplate :one
SELECT id, name, definition FROM workflow
WHERE is_template AND template_key = @template_key;

-- name: CreateProjectWorkflow :exec
INSERT INTO workflow (id, project_id, name, definition, is_template)
VALUES (@id, @project_id, @name, @definition, false);

-- name: ListWorkflowStatuses :many
SELECT key, name, category, sort_order, requires_human_approval, is_agent_reachable
FROM workflow_status WHERE workflow_id = @workflow_id ORDER BY sort_order;

-- name: CreateWorkflowStatus :exec
INSERT INTO workflow_status (
  id, workflow_id, key, name, category, sort_order,
  requires_human_approval, is_agent_reachable
) VALUES (
  @id, @workflow_id, @key, @name, @category, @sort_order,
  @requires_human_approval, @is_agent_reachable
);

-- name: ListWorkflowTransitions :many
SELECT from_status_key, to_status_key, required_permission, allowed_actor_kinds
FROM workflow_transition WHERE workflow_id = @workflow_id ORDER BY id;

-- name: CreateWorkflowTransition :exec
INSERT INTO workflow_transition (
  id, workflow_id, from_status_key, to_status_key,
  required_permission, allowed_actor_kinds
) VALUES (
  @id, @workflow_id, @from_status_key, @to_status_key,
  @required_permission, @allowed_actor_kinds
);

-- ── 一覧（ApiDesign.md 5.1）─────────────────────────────────

-- ListProjects は GET /projects の1ページ分を返す。
--
-- **可視範囲**：自分がメンバーであるプロジェクトのみ。ただし
-- アドミニストレータは全件（5.1）。判定は @is_administrator で受け取る。
-- 「project.view を持つか」では絞らない。オペレータもシステムロールとして
-- project.view を持つため、それでは全件が見えてしまう（手順6a の判断）。
--
-- **件数と進捗を一覧に含める**（5.1）。プロジェクトごとに問い合わせる N+1 を
-- 避けるためであり、LATERAL の集約1回で ticket_count / closed_count を得る。
-- idx_ticket_project_status が project_id 側から効く。
--
-- **完了は closed_at IS NOT NULL で数える**（DbDesign.md 6.6）。
-- workflow_status.category = 'done' を経由すると、ワークフローを差し替えた
-- プロジェクトで過去のチケットが数えられなくなる（status_key は論理参照）。
--
-- progress は「完了数 ÷ 全数」。定義をフロントに散らさないためサーバで計算し、
-- ticket_count = 0 のときは 0 を返す（null にしない。5.1）。
--
-- 並び替えは CASE 式で静的に書く。sqlc は動的な ORDER BY を組み立てられず、
-- 文字列連結で作ると SQL インジェクションの経路になるため。許可する項目は
-- 5.1 の5つで、呼び出し側（paging.go の SortSpec）が値を検証済みである。
-- 最後の v.id は同値のときの並びを固定するためのタイブレーカ。
-- name の比較に ICU collation を指定するのは DbDesign.md 4.4 の規約。
--
-- name: ListProjects :many
WITH visible AS (
  SELECT
    p.id,
    p.key,
    p.name,
    p.description,
    p.status,
    p.updated_at,
    pm.role_key AS my_role,
    t.ticket_count,
    t.closed_count,
    (CASE WHEN t.ticket_count = 0 THEN 0
          ELSE t.closed_count::double precision / t.ticket_count::double precision
     END)::double precision AS progress
  FROM project p
  LEFT JOIN project_member pm
         ON pm.project_id = p.id AND pm.actor_id = @actor_id
  CROSS JOIN LATERAL (
    SELECT
      count(*)                                         AS ticket_count,
      count(*) FILTER (WHERE tk.closed_at IS NOT NULL) AS closed_count
    FROM ticket tk
    WHERE tk.project_id = p.id
  ) t
  WHERE (pm.actor_id IS NOT NULL OR @is_administrator::boolean)
    AND (@status_filter::text = 'all' OR p.status = @status_filter::text)
)
SELECT
  v.id, v.key, v.name, v.description, v.status, v.updated_at,
  v.my_role, v.ticket_count, v.closed_count, v.progress
FROM visible v
ORDER BY
  CASE WHEN @sort::text = 'name'         AND @sort_order::text = 'asc'  THEN v.name COLLATE "ja-JP-x-icu" END ASC,
  CASE WHEN @sort::text = 'name'         AND @sort_order::text = 'desc' THEN v.name COLLATE "ja-JP-x-icu" END DESC,
  CASE WHEN @sort::text = 'key'          AND @sort_order::text = 'asc'  THEN v.key END ASC,
  CASE WHEN @sort::text = 'key'          AND @sort_order::text = 'desc' THEN v.key END DESC,
  CASE WHEN @sort::text = 'updated_at'   AND @sort_order::text = 'asc'  THEN v.updated_at END ASC,
  CASE WHEN @sort::text = 'updated_at'   AND @sort_order::text = 'desc' THEN v.updated_at END DESC,
  CASE WHEN @sort::text = 'ticket_count' AND @sort_order::text = 'asc'  THEN v.ticket_count END ASC,
  CASE WHEN @sort::text = 'ticket_count' AND @sort_order::text = 'desc' THEN v.ticket_count END DESC,
  CASE WHEN @sort::text = 'progress'     AND @sort_order::text = 'asc'  THEN v.progress END ASC,
  CASE WHEN @sort::text = 'progress'     AND @sort_order::text = 'desc' THEN v.progress END DESC,
  v.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- SummarizeProjects は ListProjects と同じ可視範囲・同じ絞り込みに対する
-- 総件数と最終更新日時を返す。
--
-- total は 2.6 の「総件数は常に返す」。last_updated_at は 2.7 の ETag の材料
-- （「プロジェクト集合の MAX(updated_at) と件数から生成する」）。**同じ WHERE を
-- 2回書かないよう1文にまとめてある。** 0件のとき last_updated_at は NULL。
--
-- name: SummarizeProjects :one
SELECT
  count(*)::bigint            AS total,
  max(p.updated_at)::timestamptz AS last_updated_at
FROM project p
LEFT JOIN project_member pm
       ON pm.project_id = p.id AND pm.actor_id = @actor_id
WHERE (pm.actor_id IS NOT NULL OR @is_administrator::boolean)
  AND (@status_filter::text = 'all' OR p.status = @status_filter::text);

-- ── 詳細（ApiDesign.md 5.4。POST /projects の応答も同じ形）───────

-- GetProjectByKey は1プロジェクトの本体とワークフローの見出しを返す。
--
-- workflow を LEFT JOIN にしているのは、project.workflow_id が NULL 可能で
-- あり（DbDesign.md 6.4、ON DELETE SET NULL）、ワークフローを持たない
-- プロジェクトでも本体は返す必要があるため。
--
-- **可視性で絞らない。** 到達可否の判定は認可ミドルウェア
-- （RequireProjectPermission）と呼び出し側の責務である。
--
-- name: GetProjectByKey :one
SELECT
  p.id, p.key, p.name, p.description, p.status, p.settings,
  p.version, p.created_at, p.updated_at,
  w.id   AS workflow_id,
  w.name AS workflow_name
FROM project p
LEFT JOIN workflow w ON w.id = p.workflow_id
WHERE p.key = @key;

-- ListProjectMembers は 5.4 の members[] を返す。
--
-- actor を JOIN するのは kind と display_name のため。エージェントも
-- プロジェクトのメンバーになれる（DbDesign.md 6.3）。
--
-- **app_user は LEFT JOIN にする。** エージェントとシステムアクターは
-- app_user の行を持たないため、INNER にするとメンバー一覧から消える。
-- email が NULL になるのはその2種別である（ApiDesign.md 5.4）。
--
-- name: ListProjectMembers :many
SELECT
  a.id AS actor_id,
  a.kind,
  a.display_name,
  u.email,
  pm.role_key,
  pm.joined_at
FROM project_member pm
JOIN actor a ON a.id = pm.actor_id
LEFT JOIN app_user u ON u.actor_id = a.id
WHERE pm.project_id = @project_id
ORDER BY pm.joined_at, a.id;

-- ── キーの重複確認（ApiDesign.md 5.2）───────────────────────

-- ProjectKeyExists は check-key の判定に使う。
--
-- **作成時の重複検出には使わない。** 5.3 が「競合検出はDBの UNIQUE 制約に
-- 委ね、check-key の結果を信頼しない」と定めている（TOCTOU 対策）。
--
-- name: ProjectKeyExists :one
SELECT EXISTS (SELECT 1 FROM project WHERE key = @key);

-- ── 更新（ApiDesign.md 5.5 / 5.6）───────────────────────────

-- UpdateProject は PATCH /projects/:key を1文で行う（5.5）。
--
-- **部分更新**：送られなかったフィールドは現在値のままにする。sqlc.narg は
-- NULL 可能な引数になるので、name / settings は COALESCE で「NULL なら据え置き」
-- にできる。description だけは **NULL への更新が正当な操作**（説明を消す）なので
-- COALESCE では表せず、「送られたか」を @description_set で別に受け取る。
--
-- **楽観ロック**は WHERE の version 照合で行う（2.8）。不一致なら 0 行になり、
-- 呼び出し側が 409 と 404 を区別する。version は +1 し、updated_at は
-- trg_project_updated が埋める（0004）。
--
-- **RETURNING を使わない。** 応答は buildProjectDetail が GetProjectByKey から
-- 組み立てる（5.5 の応答は 5.4 と同形式）。同じ形を2か所で作らないため。
--
-- name: UpdateProject :execrows
UPDATE project SET
  name        = COALESCE(sqlc.narg('name'), name),
  description = CASE WHEN @description_set::boolean THEN sqlc.narg('description')
                     ELSE description END,
  settings    = COALESCE(sqlc.narg('settings'), settings),
  version     = version + 1
WHERE key = @key AND version = @version;

-- SetProjectStatus は archive / unarchive を1文で行う（5.6）。
--
-- archived_at は archive で now()、unarchive で NULL（5.6 の表）。
-- 分岐を Go 側に持たず、@status から導く。両方の呼び出しで同じ文を通るため、
-- 片方だけ archived_at を更新し忘れることがない。
--
-- **既にその状態なら 0 行になる**（WHERE の status <> @status）。5.6 の
-- 「既にその状態なら何も変えずに 200 を返す」を、行数で呼び出し側へ伝える。
-- version が二重送信で進まないのもこの条件による。
--
-- name: SetProjectStatus :execrows
UPDATE project SET
  status      = @status::text,
  archived_at = CASE WHEN @status::text = 'archived' THEN now() ELSE NULL END,
  version     = version + 1
WHERE key = @key AND status <> @status::text;
