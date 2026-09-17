-- スプリントに関するクエリ（DbDesign.md 6.9、ApiDesign.md 9.12）。
--
-- 手順16a で追加。プロジェクト設定のスプリントタブ（GuiDesign.md 5.9.5）が
-- 消費者で、手順17 のチケット詳細サイドバーが選択肢として同じ一覧を読む。
--
-- **Phase 1 で開けるのは定義だけ**である。バーンダウン・ベロシティを含む
-- スプリント管理画面は Phase 2（GuiDesign.md 10章）。

-- items[] は start_date 降順（NULL は末尾）、同値は created_at 降順
-- （ApiDesign.md 9.12）。新しいものが上に来る並びで、5.9.5 の図と一致する。
--
-- closed_count は closed_at IS NOT NULL で数える。status_category = 'done'
-- では数えない——9.13 の open / overdue が closed_at を基準にしており、
-- closed_at は遷移の副作用としてのみ動く（DbDesign.md 6.6）ため、
-- ワークフローの定義が違うプロジェクトでも意味が変わらない。
-- name: ListSprintsByProject :many
SELECT
  s.id,
  s.name,
  s.goal,
  s.start_date,
  s.end_date,
  s.status,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id)::bigint AS ticket_count,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id AND t.closed_at IS NOT NULL)::bigint AS closed_count
FROM sprint s
WHERE s.project_id = @project_id
ORDER BY s.start_date DESC NULLS LAST, s.created_at DESC;

-- 1件だけ返す形。POST / PATCH の応答（B-2）で使う。
-- name: GetSprintByID :one
SELECT
  s.id,
  s.name,
  s.goal,
  s.start_date,
  s.end_date,
  s.status,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id)::bigint AS ticket_count,
  (SELECT count(*) FROM ticket t
    WHERE t.sprint_id = s.id AND t.closed_at IS NOT NULL)::bigint AS closed_count
FROM sprint s
WHERE s.project_id = @project_id AND s.id = @id;

-- name: CreateSprint :exec
INSERT INTO sprint (id, project_id, name, goal, start_date, end_date, status)
VALUES (@id, @project_id, @name, @goal, @start_date, @end_date, @status);

-- COALESCE による部分更新。goal / start_date / end_date は NULL を
-- 「値として設定する」ことがある（欄を空にする操作）ため、送られたかどうかを
-- COALESCE では区別できない。**明示的なフラグ引数で分ける**
-- （users_update.go の同種の扱いに揃える）。
-- name: UpdateSprint :execrows
UPDATE sprint SET
  name       = COALESCE(sqlc.narg('name'), name),
  goal       = CASE WHEN @set_goal::boolean       THEN sqlc.narg('goal')       ELSE goal END,
  start_date = CASE WHEN @set_start_date::boolean THEN sqlc.narg('start_date') ELSE start_date END,
  end_date   = CASE WHEN @set_end_date::boolean   THEN sqlc.narg('end_date')   ELSE end_date END,
  status     = COALESCE(sqlc.narg('status'), status)
WHERE project_id = @project_id AND id = @id;

-- ticket.sprint_id は fk_ticket_sprint の ON DELETE SET NULL で外れる
-- （DbDesign.md 6.9）。チケットは消えず、スプリント未設定に戻る。
-- name: DeleteSprint :execrows
DELETE FROM sprint WHERE project_id = @project_id AND id = @id;

-- ─────────────────────────────────────────────────────────────
-- スプリントの運用（開始・終了）。ApiDesign.md 9.12.1 / 9.12.2。pb-6。
--
-- **定義（上の CRUD）と運用を分ける。** 上はプロジェクト設定のスプリントタブ
-- （GuiDesign.md 5.9.5）が使い、ここはバックログのオンステージ段（同 5.4）が使う。
-- ─────────────────────────────────────────────────────────────

-- 進行中のスプリントは同時に1本だけである（ApiDesign.md 9.12.1。利用者の判断、
-- 2026-09-08）。開始の前にこれを引き、在れば 409 active_sprint_exists にする。
--
-- **LIMIT 1 を置くのは保険である。** 0028 より前に作られたデータや、9.12 の
-- PATCH で status を直接 active にした行が複数あると2件返りうる。:one は
-- 2行返ると失敗するので、いちばん新しい1件に倒す。
-- name: GetActiveSprint :one
SELECT id, name FROM sprint
 WHERE project_id = @project_id AND status = 'active'
 ORDER BY created_at DESC
 LIMIT 1;

-- オンステージ段に出ているチケットの id をすべて返す（ApiDesign.md 9.12.1）。
--
-- **staged_at だけで決めてはならない**（GuiDesign.md 5.4、ApiDesign.md 9.4.1）。
-- 段を決めるのは親であり、**子は staged_at が NULL のまま親と一緒にオンステージ
-- 段へ出る**。ここが取り違えると、スプリントの対象から配下が丸ごと落ちる。
--
-- **エピックは除く。** どちらの段にも行として出ないので（GuiDesign.md 5.4）、
-- スプリントの対象にしても数に混ざるだけである。
--
-- **棚に戻ったものを明示的に外す。** 9.12.2 は終了の時点で完了していた根だけを
-- 降ろすので、**終了したあとに完了した根はオンステージに残ったままになる**
-- （実データで判明、2026-09-08）。そのまま次を始めると、画面から消えている
-- はずの行が次のスプリントの対象に入り、ticket_count が実態と合わなくなる。
-- 判定は 9.2.1 の条件1・2 と同じで、根は表示上のトップレベルなので条件3 は
-- 自動的に満たされる。
-- name: ListOnstageTicketIDs :many
WITH RECURSIVE roots AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::text
     AND staged_at IS NOT NULL
     AND type <> 'epic'
     AND NOT (
       closed_at IS NOT NULL
       AND EXISTS (SELECT 1 FROM sprint os
                    WHERE os.id = ticket.sprint_id AND os.status = 'completed')
     )
),
subtree AS (
  SELECT id FROM roots
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
   WHERE c.type <> 'epic'
)
SELECT id FROM subtree;

-- スプリント中にオンステージへ入った部分木の id を返す（ApiDesign.md 9.12.3。pb-129）。
--
-- **ticket_id の表示上の根がオンステージに居るときだけ返す**（居なければ0行）。
-- 根のたどり方は GetDisplayRootForStaging と同じ（親が無いか、親がエピック）。
-- **棚に戻った根（完了し、最後のスプリントが completed）は対象にしない**——
-- ListOnstageTicketIDs が開始の対象から外すのと同じ判定である。
--
-- 返すのは ticket_id を根とする部分木で、エピックを除く。
-- name: ListSubtreeIDsJoiningSprint :many
WITH RECURSIVE up AS (
  SELECT t.id, t.parent_id, 0 AS depth
    FROM ticket t
   WHERE t.project_id = @project_id::text AND t.id = @ticket_id::text
  UNION ALL
  SELECT p.id, p.parent_id, up.depth + 1
    FROM ticket p JOIN up ON p.id = up.parent_id
   WHERE up.depth < 32
),
root AS (
  SELECT u.id
    FROM up u
    LEFT JOIN ticket pt ON pt.id = u.parent_id
   WHERE u.parent_id IS NULL OR pt.type = 'epic'
   ORDER BY u.depth
   LIMIT 1
),
onstage AS (
  SELECT 1
    FROM ticket r JOIN root ON r.id = root.id
   WHERE r.staged_at IS NOT NULL
     AND r.type <> 'epic'
     AND NOT (
       r.closed_at IS NOT NULL
       AND EXISTS (SELECT 1 FROM sprint os
                    WHERE os.id = r.sprint_id AND os.status = 'completed')
     )
),
subtree AS (
  SELECT t.id FROM ticket t
   WHERE t.id = @ticket_id::text AND t.type <> 'epic'
     AND EXISTS (SELECT 1 FROM onstage)
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
   WHERE c.type <> 'epic'
)
SELECT id FROM subtree;

-- 所属を1回で書く（DbDesign.md 6.9.1）。
--
-- **行ごとに INSERT しない。** オンステージは200件になりうる（9.2.1 の per_page）。
-- ON CONFLICT DO NOTHING は、同じスプリントを二重に開始できない以上ほぼ起きないが、
-- 同じチケットが部分木の重なりで2度現れた場合の保険である。
-- name: AddTicketsToSprint :exec
INSERT INTO ticket_sprint (ticket_id, sprint_id)
SELECT unnest(@ticket_ids::text[]), @sprint_id
ON CONFLICT (ticket_id, sprint_id) DO NOTHING;

-- ticket.sprint_id を「いま属しているスプリント」へ揃える（DbDesign.md 6.9.1）。
--
-- **version を上げない**（利用者の判断の範囲外だが、9.5.2 が sprint_id を
-- 編集不可にしたことの帰結である）。sprint_id は PATCH で書けない欄なので、
-- 開いている詳細ペインの If-Match が古くなっても**失われる編集が無い**。
-- 逆に上げると、スプリントを開始するたびに開いている全ペインが 409 になる。
-- updated_at は trg_ticket_updated が動かすので、一覧の再取得は効く。
-- name: SetTicketsSprintID :exec
UPDATE ticket SET sprint_id = @sprint_id
 WHERE project_id = @project_id::text AND id = ANY(@ticket_ids::text[]);

-- スプリントを終える（ApiDesign.md 9.12.2）。
--
-- **end_date が空なら今日を入れる。** 期間を切らずに始めたスプリントでも、
-- 終わった日付は残る——9.2.1 の「棚に戻ったか」の判定は status を見るので
-- ここに依存しないが、あとから振り返る材料になる。
-- name: FinishSprint :execrows
UPDATE sprint
   SET status = 'completed',
       end_date = COALESCE(end_date, CURRENT_DATE)
 WHERE project_id = @project_id AND id = @id AND status = 'active';

-- そのスプリントの所属を閉じる（DbDesign.md 6.9.1）。
--
-- **完了・未完了を問わず立てる。** この列が答えるのは「そのスプリントの対象
-- だった期間」であって「消化できたか」ではない。
-- name: MarkSprintMembershipRemoved :exec
UPDATE ticket_sprint SET removed_at = now()
 WHERE sprint_id = @sprint_id AND removed_at IS NULL;

-- 完了しているオンステージの根を、段から降ろす（ApiDesign.md 9.12.2）。
--
-- **子は staged_at を持たないので触るものが無く、親と一緒に降りる。**
-- 根が未完了なら、完了した子も一緒にオンステージへ残る（部分木は丸ごと動く）。
--
-- **sprint_id は消さない。** 終わったあとも「最後に属したスプリント」を指し
-- 続ける——9.2.1 の判定がこれを読むので、ここで NULL にすると材料が消える。
-- name: UnstageClosedTicketsInSprint :execrows
UPDATE ticket SET staged_at = NULL
 WHERE project_id = @project_id::text
   AND sprint_id = @sprint_id
   AND staged_at IS NOT NULL
   AND closed_at IS NOT NULL;
