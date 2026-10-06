-- 計画の変更対象と委任の境界。Delegation.md 5章。
-- name: GetPlanningDelegation :one
WITH RECURSIVE ancestors AS (
 SELECT t.id,t.parent_id,0 AS depth FROM ticket t WHERE t.id = @ticket_id AND t.project_id = @project_id
 UNION ALL
 SELECT t.id,t.parent_id,a.depth+1 FROM ticket t JOIN ancestors a ON t.id=a.parent_id WHERE t.project_id= @project_id
)
SELECT d.id,d.status,d.main_actor_id,root.seq AS root_seq
FROM ancestors a JOIN delegation d ON d.root_ticket_id=a.id
JOIN ticket root ON root.id=d.root_ticket_id
ORDER BY a.depth LIMIT 1 FOR SHARE OF d;

-- name: GetPlanningPosition :one
SELECT id,parent_id,(staged_at IS NOT NULL)::boolean AS staged FROM ticket WHERE project_id= @project_id AND seq= @seq FOR UPDATE;

-- name: ListPlanningSiblings :many
SELECT id,seq FROM ticket
WHERE project_id = @project_id AND parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::text
 AND (staged_at IS NOT NULL) = sqlc.arg(staged)::boolean AND id <> @except_id
ORDER BY sort_key NULLS LAST,seq;
