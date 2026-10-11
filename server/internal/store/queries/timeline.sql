-- タイムラインのクエリ（ApiDesign.md 9.2.7・9.17、DbDesign.md 6.6.2・6.32。pb-296）。
--
-- **タイムラインの対象は1か所で決める。** 「settings.timeline_epics で選んだエピックの配下
-- （所属は 9.5.1 の epic と同じ求め方）で、時刻付きで、開始か終了の少なくとも一方を持つもの」
-- ——一覧（ticket.sql の ListTickets の timeline_members）・版の保存（本ファイル）・連鎖のずらしの行が
-- 同じ定義を使う。版に写す集合と画面に載る集合が食い違うと、重ねたときに「消えた」と誤って出る
-- （DbDesign.md 6.32「写すチケット」）。

-- GetProjectTimelineSettings はタイムラインが要求1回に1度読むプロジェクトの行（9.2.7 の
-- 「プロジェクト1本」）。settings の timeline_epics・timeline_snap_seconds と、ETag の材料の
-- version（9.2.5 の⑧）と、基準タイムゾーン。
-- name: GetProjectTimelineSettings :one
SELECT settings, version, timezone FROM project WHERE id = @id;

-- ListTimelineEpics は view=timeline の epics[]（9.2.7）。**選んだエピックのうち、いま在るもの**。
-- エピックでない番号・無い番号はここで落ちる（DbDesign.md 6.4「読み飛ばす」）。parent が
-- あれば、その部分木の内側だけ（見る人ごとの絞り込み）。並びは sort_key の昇順。
-- name: ListTimelineEpics :many
WITH RECURSIVE subtree AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::pg_catalog.bpchar AND seq = ANY(@parent_seqs::int[])
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
)
SELECT
  t.id,
  t.seq,
  t.title,
  t.status_key,
  ws.name     AS status_name,
  ws.category AS status_category,
  t.sort_key,
  t.closed_at,
  t.updated_at
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE t.project_id = @project_id::pg_catalog.bpchar
  AND t.type = 'epic'
  AND t.seq = ANY(@epic_seqs::int[])
  AND (cardinality(@parent_seqs::int[]) = 0 OR t.id IN (SELECT id FROM subtree))
ORDER BY t.sort_key COLLATE "C" NULLS LAST, t.seq;

-- ListTimelineActivityFacts は view=timeline の started_at と last_changed_by を1本で引く（9.2.7）。
--
-- **1チケットあたり最大2行**を返す。fact = 'last' は activity の最新の行の実行者（記録時の表示名）、
-- fact = 'started' は 9.2.1「着手日時を導く」と同じ定義（状態が todo 区分から初めて出た遷移の
-- occurred_at）。どちらも無ければ行が無い——NULL の時刻を列に載せない形にしてある（LEFT JOIN で
-- 返すと sqlc が NULL になりうる時刻を非 NULL の型で読む）。**行ごとに引かない**（設計方針3）。
-- name: ListTimelineActivityFacts :many
SELECT * FROM (
  SELECT DISTINCT ON (a.entity_id)
         a.entity_id AS ticket_id, 'last'::text AS fact, a.occurred_at AS at,
         a.actor_id, a.actor_kind, a.actor_name
    FROM activity a
   WHERE a.entity_type = 'ticket'
     AND a.entity_id = ANY(@ticket_ids::pg_catalog.bpchar[])
   ORDER BY a.entity_id, a.occurred_at DESC, a.id DESC
) la
UNION ALL
SELECT a.entity_id AS ticket_id, 'started'::text AS fact, min(a.occurred_at) AS at,
       NULL AS actor_id, NULL AS actor_kind, NULL AS actor_name
  FROM activity a
  JOIN ticket at ON at.id = a.entity_id
  JOIN project ap ON ap.id = at.project_id
  JOIN workflow_status os ON os.workflow_id = ap.workflow_id AND os.key = a.old_value
  JOIN workflow_status ns ON ns.workflow_id = ap.workflow_id AND ns.key = a.new_value
 WHERE a.entity_type = 'ticket'
   AND a.entity_id = ANY(@ticket_ids::pg_catalog.bpchar[])
   AND a.action = 'transition'
   AND a.field = 'status_key'
   AND os.category = 'todo'
   AND ns.category <> 'todo'
 GROUP BY a.entity_id;

-- CountScheduleBaselines と GetLatestScheduleBaseline は view=timeline の baselines（9.2.7）。
-- 件数と最後に保存した版。版はチケットの updated_at を動かさないので、ETag の⑦にもなる（9.2.5）。
-- 最新の版が無ければ GetLatestScheduleBaseline は0行。
-- name: CountScheduleBaselines :one
SELECT count(*)::int FROM schedule_baseline WHERE project_id = @project_id;

-- name: GetLatestScheduleBaseline :one
SELECT id, created_at FROM schedule_baseline
 WHERE project_id = @project_id
 ORDER BY created_at DESC, id DESC
 LIMIT 1;

-- ── 当初計画の版（ApiDesign.md 9.17.3、DbDesign.md 6.32）──────────

-- CountTimelineTickets は版に写すチケットの件数。5,000件を超えれば保存を拒む（9.17.3）。
-- 条件は ListTickets の view=timeline（絞り込みなし）と同じ。
-- name: CountTimelineTickets :one
WITH RECURSIVE members AS (
  SELECT e.id, e.seq AS epic_seq FROM ticket e
   WHERE e.project_id = @project_id::pg_catalog.bpchar
     AND e.type = 'epic'
     AND e.seq = ANY(@epic_seqs::int[])
  UNION
  SELECT c.id, m.epic_seq FROM ticket c JOIN members m ON c.parent_id = m.id
   WHERE c.type <> 'epic'
)
SELECT count(*)::int
  FROM members m
  JOIN ticket t ON t.id = m.id
 WHERE t.type <> 'epic'
   AND NOT t.all_day
   AND (t.start_at IS NOT NULL OR t.due_at IS NOT NULL);

-- CreateScheduleBaseline は版の見出しを1行積む。
-- name: CreateScheduleBaseline :one
INSERT INTO schedule_baseline (id, project_id, label, item_count, created_by, created_by_name)
VALUES (@id, @project_id, @label, @item_count, @created_by, @created_by_name)
RETURNING created_at;

-- CopyTimelineToBaseline は版の行を写す（DbDesign.md 6.32「写すチケット」「写す値」）。
-- 写すのはバーを描くのに要る値だけ。所属エピックの題名も写す（エピックが消えても見出しを出せる）。
-- name: CopyTimelineToBaseline :execrows
WITH RECURSIVE members AS (
  SELECT e.id, e.seq AS epic_seq, e.title AS epic_title FROM ticket e
   WHERE e.project_id = @project_id::pg_catalog.bpchar
     AND e.type = 'epic'
     AND e.seq = ANY(@epic_seqs::int[])
  UNION
  SELECT c.id, m.epic_seq, m.epic_title FROM ticket c JOIN members m ON c.parent_id = m.id
   WHERE c.type <> 'epic'
)
INSERT INTO schedule_baseline_item (
  baseline_id, ticket_seq, title, epic_seq, epic_title,
  start_at, due_at, all_day, no_overlap, start_pinned, due_pinned
)
SELECT @baseline_id::pg_catalog.bpchar, t.seq, t.title, m.epic_seq, m.epic_title,
       t.start_at, t.due_at, t.all_day, t.no_overlap, t.start_pinned, t.due_pinned
  FROM members m
  JOIN ticket t ON t.id = m.id
 WHERE t.type <> 'epic'
   AND NOT t.all_day
   AND (t.start_at IS NOT NULL OR t.due_at IS NOT NULL);

-- SetScheduleBaselineItemCount は保存の途中で写した件数が数えた件数と違ったときだけ使う
-- （同じトランザクションの中で、版を返す前に直す。版の書き換えではない）。
-- name: SetScheduleBaselineItemCount :exec
UPDATE schedule_baseline SET item_count = @item_count WHERE id = @id;

-- ListScheduleBaselines は版の一覧（9.17.3）。created_at の新しい順、同じ時刻なら id の降順。
-- name: ListScheduleBaselines :many
SELECT id, label, item_count, created_by, created_by_name, created_at
  FROM schedule_baseline
 WHERE project_id = @project_id
 ORDER BY created_at DESC, id DESC;

-- GetScheduleBaseline は版の見出しを1件引く。**project_id で閉じる**（他プロジェクトの id を
-- 投げても行が返らない）。
-- name: GetScheduleBaseline :one
SELECT id, label, item_count, created_by, created_by_name, created_at
  FROM schedule_baseline
 WHERE project_id = @project_id AND id = @id;

-- ListScheduleBaselineItems は版の行（9.17.3）。exists は「いまのプロジェクトに同じ番号の
-- チケットが在るか」——番号は使い回さないので、無ければ「版にあったが、いまは無い」。
-- name: ListScheduleBaselineItems :many
SELECT
  i.ticket_seq,
  EXISTS (SELECT 1 FROM ticket t WHERE t.project_id = @project_id AND t.seq = i.ticket_seq) AS ticket_exists,
  i.title,
  i.epic_seq,
  i.epic_title,
  i.start_at,
  i.due_at,
  i.all_day,
  i.no_overlap,
  i.start_pinned,
  i.due_pinned
FROM schedule_baseline_item i
WHERE i.baseline_id = @baseline_id
ORDER BY i.epic_seq, i.start_at NULLS LAST, i.ticket_seq;

-- DeleteScheduleBaseline は版を物理削除する。行は ON DELETE CASCADE で消える（6.32）。
-- name: DeleteScheduleBaseline :execrows
DELETE FROM schedule_baseline WHERE project_id = @project_id AND id = @id;

-- ── 連鎖のずらし（ApiDesign.md 9.17.1・9.17.2）────────────────────

-- LockTicketRow は行（エピック）の ticket 行を FOR UPDATE で掴み、同じ行への連鎖を直列にする
-- （9.17.2「同時更新の扱い」）。エピックに属さなければきっかけ自身を掴む。
-- name: LockTicketRow :one
SELECT id FROM ticket WHERE id = @id FOR UPDATE;

-- GetRescheduleTicket はきっかけを行のロックを取って読む。状態の区分は暗黙のアンカー
-- （着手済み・完了）の判定に使う（9.17.1）。
-- name: GetRescheduleTicket :one
SELECT
  t.id,
  t.seq,
  t.type,
  t.title,
  t.start_at,
  t.due_at,
  t.all_day,
  t.no_overlap,
  t.start_pinned,
  t.due_pinned,
  t.closed_at,
  t.version,
  ws.category AS status_category
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE t.project_id = @project_id AND t.seq = @seq
FOR UPDATE OF t;

-- ListEpicMembersForUpdate は1つのエピック（行）に属するチケットを、行のロックを取って読む
-- （9.17.2）。所属は 9.5.1 の epic と同じ求め方（入れ子のエピックの手前で降りるのを止める）。
-- **列（no_overlap・時刻付き・両端あり）に絞らずに返す**——FS の先行は列の外でもよく、
-- その終了を読むため。列の判定はアプリが行う（純粋な関数に渡す）。
-- name: ListEpicMembersForUpdate :many
WITH RECURSIVE members AS (
  SELECT c.id FROM ticket c
   WHERE c.parent_id = @epic_id::pg_catalog.bpchar AND c.type <> 'epic'
  UNION
  SELECT c.id FROM ticket c JOIN members m ON c.parent_id = m.id
   WHERE c.type <> 'epic'
)
SELECT
  t.id,
  t.seq,
  t.title,
  t.start_at,
  t.due_at,
  t.all_day,
  t.no_overlap,
  t.start_pinned,
  t.due_pinned,
  t.closed_at,
  t.version,
  ws.category AS status_category
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE t.id IN (SELECT id FROM members)
  AND t.project_id = @project_id::pg_catalog.bpchar
ORDER BY t.seq
FOR UPDATE OF t;

-- ListFSLinksAmongTickets は連鎖が寄せる FS の依存（9.17.1 の手順1）。両端が渡した ID に含まれる
-- ものだけ。lag_days は暦日。
-- name: ListFSLinksAmongTickets :many
SELECT s.seq AS source_seq, t.seq AS target_seq, l.lag_days
  FROM ticket_link l
  JOIN ticket s ON s.id = l.source_ticket_id
  JOIN ticket t ON t.id = l.target_ticket_id
 WHERE l.source_ticket_id = ANY(@ticket_ids::pg_catalog.bpchar[])
   AND l.target_ticket_id = ANY(@ticket_ids::pg_catalog.bpchar[])
   AND l.link_type = 'FS'
 ORDER BY s.seq, t.seq;

-- SetTicketSchedule は連鎖のずらしが動かした行を書く（9.17.2）。**version を進める**——動いた行は
-- 人が見ていた前提が変わったので、他の画面の If-Match を外す。行は FOR UPDATE で掴んであるので
-- version の照合は保険である。
-- name: SetTicketSchedule :one
UPDATE ticket
   SET start_at = @start_at,
       due_at   = @due_at,
       version  = version + 1
 WHERE id = @id AND version = @version
RETURNING version;
