-- 未確認の設定変更のクエリ（DbDesign.md 6.17、Design.md 10.3）。pb-97。
--
-- **行は0か1つである。** 未確認が残っている間は次の危険な変更を受け付けない
-- （ApiDesign.md 11.2 が 409 を返す）ので、複数行を前提にした操作を持たない。
-- ただし**点検は「期限が来たもの」を探す**形にしておく——起動時に古い行が
-- 残っていても、ひとつ残らず戻せる。

-- 1件作る。**previous は戻す値**（キー→値。null は行が無かったことを表す）。
-- name: CreatePendingSettingChange :one
INSERT INTO pending_setting_change (id, previous, expires_at, created_by)
VALUES (@id, @previous, @expires_at, @created_by)
RETURNING id, previous, expires_at, created_at;

-- 未確認を引く。**画面が残り時間と「誰が変えたか」を出すために使う。**
--
-- **変えた人の表示名も返す**（pb-107）。画面は「あなたが変えました」と
-- 「〇〇 が変えました」で文言を分ける——押す前に確かめることが違う。
-- name: GetPendingSettingChange :one
SELECT
  p.id,
  p.previous,
  p.expires_at,
  p.created_at,
  p.created_by,
  a.kind AS created_by_kind,
  a.display_name AS created_by_display_name
FROM pending_setting_change p
LEFT JOIN actor a ON a.id = p.created_by
ORDER BY p.created_at DESC
LIMIT 1;

-- 期限が来たものを全部引く。**起動時の点検と、プロセス内のタイマが使う。**
-- name: ListExpiredPendingSettingChanges :many
SELECT id, previous, expires_at
FROM pending_setting_change
WHERE expires_at <= now()
ORDER BY expires_at;

-- 1件消す。**確認できたとき（確定）と、戻し終えたとき**に呼ぶ。
-- **消した件数を返す**ので、競合して既に消えていたかが分かる。
-- name: DeletePendingSettingChange :execrows
DELETE FROM pending_setting_change WHERE id = @id;

-- 未確認が何件あるか。**次の危険な変更を断るために使う。**
-- name: CountPendingSettingChanges :one
SELECT count(*) FROM pending_setting_change;

-- 未確認を全部引く。**起動時に使う**（pb-97 の改訂、2026-09-12）。
--
-- **起動時は期限を見ない。** 締め出された人が最初に試すのは再起動であり、
-- そこで戻さないと**その設定では起動に失敗する場合に永遠に戻らない**
-- （プロセスが上がらないのでタイマも動かない）。ネットワーク機器の
-- commit confirmed も、再起動すると未確定の設定を捨てる。
-- name: ListPendingSettingChanges :many
SELECT id, previous, expires_at
FROM pending_setting_change
ORDER BY created_at;
