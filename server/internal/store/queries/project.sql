-- プロジェクトとワークフローに関するクエリ（DbDesign.md 6.4 / 6.5）。
--
-- 手順7.5（pb dev seed）で必要になった分だけを置いている。
-- テンプレートの複製は POST /projects（ApiDesign.md 5.2）と同じ手順であり、
-- 手順9はここのクエリを再利用する。

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

-- ── テンプレートの複製（ApiDesign.md 5.2）─────────────────────

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
