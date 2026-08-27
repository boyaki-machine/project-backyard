-- チケットの完了条件（DoD）（DbDesign.md 6.11、ApiDesign.md 9.9）。
--
-- 手順18a で追加。チケット詳細（GuiDesign.md 5.5）の「完了条件 (DoD)」が消費者で、
-- 詳細応答（9.5.1）の dod も同じ一覧を読む。
--
-- **すべてのクエリが ticket_id で閉じている**（reference.sql と同じ）。DoD はチケットの
-- 子資源であり、他チケットの ID を渡されても行が返らないようにするためである。
--
-- **Phase 1 が受け付ける type は manual だけである**（9.9）。表と CHECK は Phase 2 の
-- 形のまま作ってあり（DbDesign.md 6.11）、絞るのは API の仕事なので SQL には現れない。
--
-- **config / evidence / origin は SELECT しない。** Phase 1 の応答に載せないため
-- （9.9）。列は残っており、Phase 2 で type を開けるときに同じ改訂で足す。
--
-- **satisfied_by は LEFT JOIN で引く。** actor は ON DELETE SET NULL なので、
-- チェックした人を消した後も行は残る——条件を満たした事実は消えない。

-- items[] は sort_order → created_at の昇順（9.9）。第2キーを置くのは、
-- 9.10.2 と同じく順序が実行ごとに揺れないようにするためである。
-- name: ListTicketDoD :many
SELECT
  d.id,
  d.type,
  d.body,
  d.is_satisfied,
  d.satisfied_at,
  d.sort_order,
  d.created_at,
  d.updated_at,
  d.satisfied_by,
  a.kind         AS satisfied_by_kind,
  a.display_name AS satisfied_by_name
FROM dod_item d
LEFT JOIN actor a ON a.id = d.satisfied_by
WHERE d.ticket_id = @ticket_id
ORDER BY d.sort_order, d.created_at;

-- 1件だけ返す形。POST / PATCH の応答（9.9）と、更新前の読み取りに使う。
-- name: GetTicketDoDItem :one
SELECT
  d.id,
  d.type,
  d.body,
  d.is_satisfied,
  d.satisfied_at,
  d.sort_order,
  d.created_at,
  d.updated_at,
  d.satisfied_by,
  a.kind         AS satisfied_by_kind,
  a.display_name AS satisfied_by_name
FROM dod_item d
LEFT JOIN actor a ON a.id = d.satisfied_by
WHERE d.ticket_id = @ticket_id AND d.id = @id;

-- sort_order 省略時の既定（現在の最大値 + 10）。行が無ければ 10 から始める。
-- **10 刻みにするのは間に挿し込む余地を残すため**で、NextTicketReferenceSortOrder
-- と同じ採番である。
-- name: NextDoDSortOrder :one
SELECT COALESCE(max(sort_order), 0) + 10 FROM dod_item WHERE ticket_id = @ticket_id;

-- name: CreateDoDItem :exec
INSERT INTO dod_item (id, ticket_id, type, body, sort_order, is_satisfied)
VALUES (@id, @ticket_id, @type, @body, @sort_order, @is_satisfied);

-- 部分更新（9.9 の PATCH）。
--
-- **is_satisfied と satisfied_at / satisfied_by は同時に動く。** 9.9 が
-- 「true にしたときサーバが satisfied_at と satisfied_by を設定し、false に
-- 戻すと両方 NULL へ戻す」と定めるため、3つを別々の引数にせず
-- satisfied_set の1つで束ねる——**バラバラに送れる形にすると、
-- 「満たしたのに満たした人がいない」行を作れてしまう。**
--
-- type は含めない。作成後は変えられない（9.9 が immutable_field と定める）。
-- name: UpdateDoDItem :execrows
UPDATE dod_item SET
  body         = COALESCE(sqlc.narg('body'), body),
  sort_order   = COALESCE(sqlc.narg('sort_order'), sort_order),
  is_satisfied = CASE WHEN @satisfied_set::boolean THEN @is_satisfied::boolean ELSE is_satisfied END,
  satisfied_at = CASE WHEN @satisfied_set::boolean
                      THEN (CASE WHEN @is_satisfied::boolean THEN now() ELSE NULL END)
                      ELSE satisfied_at END,
  satisfied_by = CASE WHEN @satisfied_set::boolean
                      THEN (CASE WHEN @is_satisfied::boolean THEN sqlc.narg('satisfied_by') ELSE NULL END)
                      ELSE satisfied_by END
WHERE ticket_id = @ticket_id AND id = @id;

-- name: DeleteDoDItem :execrows
DELETE FROM dod_item WHERE ticket_id = @ticket_id AND id = @id;
