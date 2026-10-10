-- name: ListProjectAgents :many
SELECT ag.actor_id,a.display_name,a.is_active,ag.client_kind,ag.model_name,ag.model_version,
 ag.trust_level,ag.capabilities,ag.created_at,ag.owner_actor_id,o.display_name AS owner_name
FROM agent ag JOIN actor a ON a.id=ag.actor_id JOIN actor o ON o.id=ag.owner_actor_id
WHERE ag.project_id= @project_id ORDER BY a.display_name,ag.actor_id
LIMIT @page_limit OFFSET @page_offset;

-- name: CountProjectAgents :one
SELECT count(*) FROM agent WHERE project_id= @project_id;

-- name: LockProjectAgent :one
SELECT ag.actor_id,ag.owner_actor_id,a.is_active
FROM agent ag JOIN actor a ON a.id=ag.actor_id
WHERE ag.project_id= @project_id AND ag.actor_id= @actor_id FOR UPDATE OF a;

-- name: DisableProjectAgent :exec
UPDATE actor SET is_active=false WHERE id= @actor_id;
