-- name: GetAgentMonitorSettings :one
SELECT COALESCE(settings->'agent_monitor','{}'::jsonb)::jsonb AS policy FROM project WHERE id= @project_id;

-- name: SetAgentMonitorSettings :exec
UPDATE project SET settings=jsonb_set(settings,'{agent_monitor}', @policy::jsonb),version=version+1,updated_at=now() WHERE id= @project_id;

-- name: ListAgentAttempts :many
SELECT a.* FROM agent_attempt a
LEFT JOIN agent_monitor_reset r ON r.project_id=a.project_id AND r.actor_id=a.actor_id
WHERE a.project_id= @project_id AND a.actor_id= @actor_id
AND a.started_at >= COALESCE(r.reset_at,'epoch'::timestamptz)
ORDER BY COALESCE(a.ended_at,a.started_at) DESC,a.id DESC;

-- name: CreateAgentAttempt :exec
INSERT INTO agent_attempt(id,project_id,ticket_id,actor_id,started_at)
VALUES(@id,@project_id,@ticket_id,@actor_id,@started_at);

-- name: FinishAgentAttempt :exec
UPDATE agent_attempt SET ended_at= @ended_at,outcome=sqlc.narg(outcome) WHERE id= @id AND ended_at IS NULL;

-- ResetAgentMonitor は解除の時刻をアプリから受け取る。**DB の now() を使わない。**
-- ListAgentAttempts が比べる agent_attempt.started_at はアプリの時計で付くので、
-- 解除だけ DB の時計で付けると、2つの時計のずれだけ解除前の失敗が解除後に数えられる。
-- name: ResetAgentMonitor :exec
INSERT INTO agent_monitor_reset(project_id,actor_id,reason,reset_at) VALUES(@project_id,@actor_id,@reason,@reset_at)
ON CONFLICT(project_id,actor_id) DO UPDATE SET reset_at=excluded.reset_at,reason=excluded.reason;

-- name: UpdateAgentAttributes :exec
UPDATE agent SET trust_level=COALESCE(sqlc.narg(trust_level),trust_level),capabilities=COALESCE(sqlc.narg(capabilities)::jsonb,capabilities)
WHERE actor_id= @actor_id;

-- name: LinkFailedAgentRun :exec
UPDATE agent_run SET work_session_id= @work_session_id WHERE id= @id;

-- name: FindFailedAgentRun :one
SELECT id FROM agent_run WHERE work_session_id= @work_session_id AND ticket_id= @ticket_id;

-- name: HasReportForRun :one
SELECT EXISTS(SELECT 1 FROM agent_report WHERE agent_run_id= @agent_run_id);

-- name: ReassignAgentAttempts :exec
UPDATE agent_attempt SET actor_id= @new_actor_id WHERE actor_id= @old_actor_id;

-- name: DeleteAgentMonitorResets :exec
DELETE FROM agent_monitor_reset WHERE actor_id= @actor_id;

-- name: CountAgentAttempts :one
SELECT count(*) FROM agent_attempt WHERE actor_id= @actor_id;
