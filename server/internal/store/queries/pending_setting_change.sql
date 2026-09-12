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

-- 未確認を引く。**画面が残り時間を出すために使う。**
-- name: GetPendingSettingChange :one
SELECT id, previous, expires_at, created_at, created_by
FROM pending_setting_change
ORDER BY created_at DESC
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
