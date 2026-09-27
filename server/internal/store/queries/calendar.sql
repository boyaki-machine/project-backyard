-- プロジェクトの暦に関するクエリ（DbDesign.md 6.23、ApiDesign.md 5.8）。
--
-- pb-216 で追加。プロジェクト設定のカレンダータブ（GuiDesign.md 5.9.6）と、
-- 休日を塗り分ける画面（ガント。GuiDesign.md 10章）が消費者である。
--
-- **取得元（holiday_source）はプロジェクトの資源ではない。** Google の暦は
-- google_id ごとに1行で複数プロジェクトが共有する。プロジェクトからは
-- project.holiday_source_id を通してだけ辿る。

-- 5.8.1 の source。取得元が無ければ行が返らない。
-- name: GetProjectCalendarSource :one
SELECT
  s.id, s.kind, s.google_id, s.owner_project_id, s.name,
  s.fetched_at, s.last_attempt_at, s.last_error,
  (SELECT count(*) FROM holiday_source_day d
    WHERE d.source_id = s.id AND d.kind = 'holiday')::bigint AS holiday_count,
  (SELECT count(*) FROM holiday_source_day d
    WHERE d.source_id = s.id AND d.kind = 'observance')::bigint AS observance_count
FROM project p
JOIN holiday_source s ON s.id = p.holiday_source_id
WHERE p.id = @project_id;

-- google_id の行を作るか、既にあればその id を返す（5.8.2）。
-- **DO UPDATE で同じ値を書くのは RETURNING に行を返させるため**である
-- （DO NOTHING では衝突した行が返らない）。
-- name: EnsureGoogleHolidaySource :one
INSERT INTO holiday_source (id, kind, google_id)
VALUES (@id, 'google', @google_id)
ON CONFLICT (google_id) DO UPDATE SET google_id = EXCLUDED.google_id
RETURNING id;

-- name: SetProjectHolidaySource :exec
UPDATE project SET holiday_source_id = sqlc.narg('source_id') WHERE id = @project_id;

-- プロジェクトが所有する取り込みの暦のうち、keep_id 以外を消す。
-- Google の暦へ切り替えた・暦を外したときに、参照されない所有物を残さない（6.23）。
-- name: DeleteOwnedHolidaySources :exec
DELETE FROM holiday_source
WHERE owner_project_id = @project_id
  AND id IS DISTINCT FROM sqlc.narg('keep_id');

-- 取得の前に待ち時間を判定するため、行をロックして読む（5.8.3）。
-- name: LockHolidaySource :one
SELECT id, kind, google_id, content_sha256, last_attempt_at
FROM holiday_source
WHERE id = @id
FOR UPDATE;

-- name: MarkHolidaySourceAttempt :exec
UPDATE holiday_source SET last_attempt_at = now() WHERE id = @id;

-- 取り込めたとき。中身が変わっていなくても fetched_at は進める。
-- name: MarkHolidaySourceFetched :exec
UPDATE holiday_source SET
  name           = COALESCE(sqlc.narg('name'), name),
  content_sha256 = @content_sha256,
  fetched_at     = now(),
  last_error     = NULL
WHERE id = @id;

-- name: MarkHolidaySourceFailed :exec
UPDATE holiday_source SET last_error = @last_error WHERE id = @id;

-- 取り込みの暦を作る（5.8.4）。取り込み直しは同じ行を使い回す。
-- name: CreateFileHolidaySource :exec
INSERT INTO holiday_source (id, kind, owner_project_id)
VALUES (@id, 'file', @owner_project_id);

-- name: DeleteHolidaySourceDays :exec
DELETE FROM holiday_source_day WHERE source_id = @source_id;

-- 日の一括投入。3つの配列は同じ長さで、添字が1日に対応する。
-- name: InsertHolidaySourceDays :exec
INSERT INTO holiday_source_day (source_id, day, kind, name)
-- SELECT 句に unnest を並べると、同じ長さの配列は添字ごとに1行になる。
SELECT @source_id::text, unnest(@days::date[]), unnest(@kinds::text[]), unnest(@names::text[]);

-- 5.8.5 の events。期間は [from, to)。
-- name: ListProjectHolidayEvents :many
SELECT d.day, d.kind, d.name
FROM project p
JOIN holiday_source_day d ON d.source_id = p.holiday_source_id
WHERE p.id = @project_id AND d.day >= @from_day AND d.day < @to_day
ORDER BY d.day, d.kind, d.name;

-- 5.8.5 の override。期間は [from, to)。
-- name: ListProjectCalendarDays :many
SELECT day, is_holiday, name
FROM project_calendar_day
WHERE project_id = @project_id AND day >= @from_day AND day < @to_day
ORDER BY day;

-- name: UpsertProjectCalendarDay :exec
INSERT INTO project_calendar_day (project_id, day, is_holiday, name, created_by)
VALUES (@project_id, @day, @is_holiday, sqlc.narg('name'), sqlc.narg('created_by'))
ON CONFLICT (project_id, day) DO UPDATE SET
  is_holiday = EXCLUDED.is_holiday,
  name       = EXCLUDED.name,
  created_by = EXCLUDED.created_by,
  updated_at = now();

-- name: DeleteProjectCalendarDay :exec
DELETE FROM project_calendar_day WHERE project_id = @project_id AND day = @day;

-- PATCH /projects/:key の timezone（5.5）。Go の time.LoadLocation に加えて
-- DB も知っている名前かを見る——集計の SQL が AT TIME ZONE で使う（6.23）。
-- **知らない名前ならエラー（22023）になる。** pg_timezone_names を引く形は
-- sqlc がカタログを知らず生成できないため、実際に変換させて確かめる。
-- name: CheckTimezoneInDB :one
SELECT (now() AT TIME ZONE @name::text)::timestamp;
