-- 通知のクエリ（DbDesign.md 6.33、ApiDesign.md 4.8）。
--
-- **本人の口のクエリは、すべて recipient_id を条件に含める。** 他人の通知の id では
-- 行が返らず、ハンドラが 404 にする（4.8）。

-- CreateNotification は使い手の処理から通知を1件作る（DbDesign.md 6.33.1）。
-- name: CreateNotification :exec
INSERT INTO notification (
  id, recipient_id, kind, title, body, link, link_label,
  subject_key, project_id, source_id, source_name
) VALUES (
  @id, @recipient_id, @kind, @title, @body, @link, @link_label,
  sqlc.narg(subject_key), sqlc.narg(project_id), sqlc.narg(source_id), @source_name
);

-- WithdrawNotifications は同じ対象の通知をまとめて取り下げる（物理削除）。
-- name: WithdrawNotifications :execrows
DELETE FROM notification WHERE subject_key = sqlc.arg(subject_key)::text;

-- ListMyNotifications は自分宛ての通知を新しい順に返す（4.8.1）。
-- name: ListMyNotifications :many
SELECT n.id, n.kind, n.title, n.body, n.link, n.link_label,
       n.source_id, n.source_name, n.created_at, n.read_at,
       p.key AS project_key, p.name AS project_name
FROM notification n
LEFT JOIN project p ON p.id = n.project_id
WHERE n.recipient_id = @recipient_id
  AND (NOT sqlc.arg(unread_only)::boolean OR n.read_at IS NULL)
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- CountMyNotifications は一覧の総件数（2.6）。
-- name: CountMyNotifications :one
SELECT count(*) FROM notification
WHERE recipient_id = @recipient_id
  AND (NOT sqlc.arg(unread_only)::boolean OR read_at IS NULL);

-- CountMyUnreadNotifications は未読の数（4.8.2）。部分索引で数える。
-- name: CountMyUnreadNotifications :one
SELECT count(*) FROM notification WHERE recipient_id = @recipient_id AND read_at IS NULL;

-- GetMyNotification は1件を返す（4.8.3 の応答）。
-- name: GetMyNotification :one
SELECT n.id, n.kind, n.title, n.body, n.link, n.link_label,
       n.source_id, n.source_name, n.created_at, n.read_at,
       p.key AS project_key, p.name AS project_name
FROM notification n
LEFT JOIN project p ON p.id = n.project_id
WHERE n.id = @id AND n.recipient_id = @recipient_id;

-- SetMyNotificationRead は既読にする・未読に戻す（4.8.3）。
--
-- **既に同じ状態なら read_at を書き換えない。** 既読の時刻は最初に読んだ時刻のまま残す。
-- 行が無ければ（他人の通知）0行で、ハンドラが 404 にする。
-- name: SetMyNotificationRead :execrows
UPDATE notification
SET read_at = CASE
      WHEN sqlc.arg(read)::boolean THEN coalesce(read_at, now())
      ELSE NULL
    END
WHERE id = @id AND recipient_id = @recipient_id;

-- MarkAllMyNotificationsRead は自分宛ての未読をすべて既読にする（4.8.4）。
-- name: MarkAllMyNotificationsRead :execrows
UPDATE notification SET read_at = now()
WHERE recipient_id = @recipient_id AND read_at IS NULL;

-- DeleteMyNotification は受けた人か出した人が消す（4.8.5）。
--
-- **宛先と種類を返す**のは、受けた人以外が消したときの監査（4.8.7）に使うためである。
-- name: DeleteMyNotification :one
DELETE FROM notification
WHERE id = @id AND (recipient_id = @actor_id OR source_id = @actor_id)
RETURNING recipient_id, kind;

-- DeleteNotificationByAdmin は管理者が誰宛てでも消す（4.8.6）。
-- name: DeleteNotificationByAdmin :one
DELETE FROM notification WHERE id = @id
RETURNING recipient_id, kind;

-- DeleteExpiredNotifications は既読から保持期間を過ぎた通知を消す（DbDesign.md 6.33）。
-- **未読は消さない。** read_at の部分索引で引く。
-- name: DeleteExpiredNotifications :execrows
DELETE FROM notification WHERE read_at < sqlc.arg(read_before)::timestamptz;
