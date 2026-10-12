-- name: LockWorkProject :exec
SELECT pg_advisory_xact_lock(hashtextextended(@project_id::text, 61));

-- name: GetWorkEvent :one
SELECT digest,response FROM work_event WHERE project_id= @project_id AND actor_id= @actor_id AND request_id= @request_id;

-- name: CreateWorkEvent :exec
INSERT INTO work_event(project_id,actor_id,request_id,session_id,digest,response)
VALUES(@project_id,@actor_id,@request_id,@session_id,@digest,@response);

-- name: GetOpenWorkSession :one
SELECT id FROM work_session WHERE project_id= @project_id AND actor_id= @actor_id AND state <> 'finished';

-- ListOpenWorkSessionTickets は自分の開いている記録と、その開始元のチケット番号を返す。
-- 案内に ID だけを出すと、別のチケットで開いた記録を今のチケットの計時と取り違える（pb-306）。
-- 委任の main は木の中で複数開ける（Design.md 8.5.7。pb-344）ので、すべてを開始の古い順に返す。
-- name: ListOpenWorkSessionTickets :many
SELECT ws.id, t.seq FROM work_session ws JOIN ticket t ON t.id = ws.ticket_id
WHERE ws.project_id= @project_id AND ws.actor_id= @actor_id AND ws.state <> 'finished'
ORDER BY ws.started_at, ws.id;

-- name: CreateWorkSession :exec
INSERT INTO work_session(id,project_id,ticket_id,actor_id,client_kind,model_name,model_version,state,started_at,changed_at,note)
VALUES(@id,@project_id,@ticket_id,@actor_id,sqlc.narg(client_kind),sqlc.narg(model_name),sqlc.narg(model_version),'running',@started_at,@started_at,@note);

-- name: GetWorkSession :one
SELECT * FROM work_session WHERE id= @id AND project_id= @project_id;

-- name: UpdateWorkSession :exec
UPDATE work_session SET state= @state,changed_at= @changed_at,ended_at=sqlc.narg(ended_at),
active_ms= @active_ms,waiting_ms= @waiting_ms,reported_active_ms=sqlc.narg(reported_active_ms),
outcome=sqlc.narg(outcome),result=sqlc.narg(result),tokens=sqlc.narg(tokens),note= @note,incomplete= @incomplete
WHERE id= @id;

-- name: AddWorkAllocation :exec
INSERT INTO work_allocation(session_id,ticket_id,share) VALUES(@session_id,@ticket_id,@share);

-- name: DeleteWorkAllocations :exec
DELETE FROM work_allocation WHERE session_id= @session_id;

-- name: ListWorkAllocations :many
SELECT a.ticket_id,a.share,t.seq,a.change_metrics FROM work_allocation a JOIN ticket t ON t.id=a.ticket_id WHERE a.session_id= @session_id ORDER BY t.seq;

-- name: EnsureTicketEffort :exec
INSERT INTO ticket_effort(ticket_id,manual_hours,manual_point)
SELECT id,(actual_hours IS NOT NULL),(actual_point IS NOT NULL) FROM ticket WHERE id= @ticket_id
ON CONFLICT DO NOTHING;

-- name: MarkTicketEffortManual :exec
UPDATE ticket_effort SET manual_hours=manual_hours OR @hours::boolean,manual_point=manual_point OR @point::boolean WHERE ticket_id= @ticket_id;

-- name: GetTicketEffort :one
SELECT t.actual_hours,t.actual_point,t.actual_point_version,
COALESCE(e.manual_hours,t.actual_hours IS NOT NULL)::boolean AS manual_hours,
COALESCE(e.manual_point,t.actual_point IS NOT NULL)::boolean AS manual_point
FROM ticket t LEFT JOIN ticket_effort e ON e.ticket_id=t.id WHERE t.id= @ticket_id;

-- name: RestoreTicketEffortAuto :exec
UPDATE ticket_effort SET manual_hours=false,manual_point=false WHERE ticket_id= @ticket_id;

-- name: GetWorkTotals :one
SELECT count(*)::bigint AS sessions_count,
 count(*) FILTER (WHERE s.state <> 'finished')::bigint AS open_count,
 count(*) FILTER (WHERE s.incomplete)::bigint AS incomplete_count,
 count(*) FILTER (WHERE s.time_missing)::bigint AS time_missing,
 count(*) FILTER (WHERE s.state='finished' AND s.tokens IS NULL)::bigint AS tokens_missing,
 COALESCE(sum((CASE WHEN s.time_missing OR s.policy->>'time_source'='pb' THEN s.active_ms ELSE COALESCE(s.reported_active_ms,s.active_ms) END::double precision)*a.share) FILTER (WHERE s.state='finished'),0)::double precision AS active_ms,
 COALESCE(sum((s.active_ms+s.waiting_ms)::double precision*a.share) FILTER (WHERE s.state='finished'),0)::double precision AS elapsed_ms,
 COALESCE(sum(s.waiting_ms::double precision*a.share) FILTER (WHERE s.state='finished'),0)::double precision AS waiting_ms,
 COALESCE(sum(s.tokens::double precision*a.share) FILTER (WHERE s.state='finished'),0)::double precision AS tokens
FROM work_allocation a JOIN work_session s ON s.id=a.session_id WHERE a.ticket_id= @ticket_id;

-- name: ApplyTicketEffort :exec
UPDATE ticket t SET
actual_hours=CASE WHEN e.manual_hours THEN t.actual_hours ELSE @hours::double precision END,
actual_point=CASE WHEN e.manual_point THEN t.actual_point ELSE sqlc.narg(point)::double precision END,
actual_point_version=CASE WHEN e.manual_point THEN t.actual_point_version ELSE sqlc.narg(point_version)::text END,
version=t.version+1,updated_at=now()
FROM ticket_effort e WHERE e.ticket_id=t.id AND t.id= @ticket_id
AND ((NOT e.manual_hours AND t.actual_hours IS DISTINCT FROM @hours::double precision)
OR (NOT e.manual_point AND (t.actual_point IS DISTINCT FROM sqlc.narg(point)::double precision OR t.actual_point_version IS DISTINCT FROM sqlc.narg(point_version)::text)));

-- name: ListWorkSessions :many
SELECT s.*,a.share FROM work_allocation a JOIN work_session s ON s.id=a.session_id
WHERE a.ticket_id= @ticket_id ORDER BY s.started_at DESC,s.id DESC LIMIT @page_limit OFFSET @page_offset;

-- name: CountWorkSessions :one
SELECT count(*) FROM work_allocation WHERE ticket_id= @ticket_id;

-- name: ClearTicketEffortAuto :exec
UPDATE ticket t SET actual_hours=CASE WHEN e.manual_hours THEN t.actual_hours ELSE NULL END,
actual_point=CASE WHEN e.manual_point THEN t.actual_point ELSE NULL END,
actual_point_version=CASE WHEN e.manual_point THEN t.actual_point_version ELSE NULL END,
version=t.version+1,updated_at=now()
FROM ticket_effort e WHERE e.ticket_id=t.id AND t.id= @ticket_id
AND ((NOT e.manual_hours AND t.actual_hours IS NOT NULL) OR (NOT e.manual_point AND (t.actual_point IS NOT NULL OR t.actual_point_version IS NOT NULL)));

-- name: CountActorWorkRecords :one
SELECT ((SELECT count(*) FROM work_session s WHERE s.actor_id= @actor_id) + (SELECT count(*) FROM work_event e WHERE e.actor_id= @actor_id))::bigint;

-- name: ReassignWorkSessions :exec
UPDATE work_session SET actor_id= @new_actor_id WHERE actor_id= @old_actor_id;

-- name: ReassignWorkEvents :exec
UPDATE work_event SET request_id=actor_id||':'||request_id,actor_id= @new_actor_id WHERE actor_id= @old_actor_id;

-- name: ListWorkDeletionAffectedTickets :many
SELECT DISTINCT a.ticket_id FROM work_allocation a JOIN work_session s ON s.id=a.session_id
WHERE s.ticket_id= @ticket_id AND a.ticket_id<> @ticket_id ORDER BY a.ticket_id;

-- name: GetWorkPolicyDocument :one
SELECT body_md,version FROM document WHERE project_id= @project_id AND parent_id IS NULL AND slug='project-management-note' AND NOT is_template;

-- name: SetWorkPolicy :exec
UPDATE work_session SET policy= @policy WHERE id= @id;

-- name: SetWorkMissingTime :exec
UPDATE work_session SET time_missing= @time_missing WHERE id= @id;

-- name: SetWorkChanges :exec
UPDATE work_allocation SET change_metrics= @change_metrics WHERE session_id= @session_id AND ticket_id= @ticket_id;

-- name: GetLiveWorkPointInputs :many
SELECT s.id AS session_id,s.policy,a.change_metrics,s.ended_at FROM work_allocation a JOIN work_session s ON s.id=a.session_id
WHERE a.ticket_id= @ticket_id AND s.state='finished' ORDER BY s.ended_at,s.id;

-- name: GetWorkBase :one
SELECT * FROM ticket_work_base WHERE ticket_id= @ticket_id;

-- name: AddWorkBase :exec
INSERT INTO ticket_work_base(ticket_id,sessions_count,incomplete_count,tokens_missing,time_missing,active_ms,elapsed_ms,waiting_ms,tokens,point_inputs)
VALUES(@ticket_id,@sessions_count,@incomplete_count,@tokens_missing,@time_missing,@active_ms,@elapsed_ms,@waiting_ms,@tokens,@point_inputs)
ON CONFLICT(ticket_id) DO UPDATE SET
sessions_count=ticket_work_base.sessions_count+excluded.sessions_count,
incomplete_count=ticket_work_base.incomplete_count+excluded.incomplete_count,
tokens_missing=ticket_work_base.tokens_missing+excluded.tokens_missing,
time_missing=ticket_work_base.time_missing+excluded.time_missing,
active_ms=ticket_work_base.active_ms+excluded.active_ms,
elapsed_ms=ticket_work_base.elapsed_ms+excluded.elapsed_ms,
waiting_ms=ticket_work_base.waiting_ms+excluded.waiting_ms,
tokens=ticket_work_base.tokens+excluded.tokens,
point_inputs=excluded.point_inputs;

-- name: GetArchivedWork :one
SELECT * FROM work_archive WHERE id= @id AND project_id= @project_id;

-- name: GetWorkReplay :one
SELECT session_id,digest FROM work_replay WHERE project_id= @project_id AND actor_id= @actor_id AND request_id= @request_id;

-- name: ArchiveWork :exec
INSERT INTO work_archive(id,project_id,actor_id,ticket_ids,time_missing) VALUES(@id,@project_id,@actor_id,@ticket_ids,@time_missing);

-- name: ArchiveWorkEvents :exec
INSERT INTO work_replay(project_id,actor_id,request_id,session_id,digest)
SELECT project_id,actor_id,request_id,session_id,digest FROM work_event e WHERE e.session_id= @work_id;

-- name: DeleteWorkSession :exec
DELETE FROM work_session WHERE id= @id;

-- name: LockWorkTickets :many
SELECT t.id,t.closed_at,t.seq FROM ticket t WHERE t.project_id= @project_id AND (t.id= @target_ticket_id OR t.id IN
(SELECT a.ticket_id FROM work_allocation a WHERE a.session_id IN (SELECT a2.session_id FROM work_allocation a2 WHERE a2.ticket_id= @target_ticket_id)))
ORDER BY t.id FOR UPDATE;

-- name: ListTicketCleanupWork :many
SELECT s.* FROM work_session s JOIN work_allocation a ON a.session_id=s.id WHERE a.ticket_id= @ticket_id ORDER BY s.id;

-- name: HasTicketOpenWork :one
SELECT EXISTS(SELECT 1 FROM work_session s JOIN work_allocation a ON a.session_id=s.id
WHERE a.ticket_id= @ticket_id AND s.actor_id= @actor_id AND s.state<>'finished');

-- name: WorkRequiresRegistration :one
SELECT EXISTS(SELECT 1 FROM work_session s JOIN work_allocation a ON a.session_id=s.id
WHERE a.ticket_id= @ticket_id AND s.actor_id= @actor_id AND s.policy->>'require_registration'='true');
