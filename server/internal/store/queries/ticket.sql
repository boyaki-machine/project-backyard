-- チケットに関するクエリ（DbDesign.md 6.6、ApiDesign.md 9.2 / 9.3 / 9.4）。
--
-- 手順16b で追加。消費者はバックログ画面（GuiDesign.md 5.4、手順16c）と、
-- Phase 2 のカンバン・ガントである。いずれも同じ ListTickets を読む。
--
-- **すべてのクエリが project_id で閉じている。** チケットはプロジェクトの資源で
-- あり、他プロジェクトの ID を渡されても行が返らないようにするためである。到達
-- 可否（メンバーか）の判定は RequireProjectPermission が済ませている
-- （Design.md 6.4.5）。
--
-- **sort_key の比較には必ず COLLATE "C" を付ける。** ticket.sort_key は照合順の
-- 指定を持たない text 列で、DBの既定は ja-JP-x-icu である（DbDesign.md 4.4）。
-- LexoRank（internal/lexorank）が仮定しているのはバイト順なので、ICU の言語規則で
-- 比較されると並びが崩れる。ORDER BY も比較演算子も明示する。

-- ── 一覧（ApiDesign.md 9.2）─────────────────────────────────

-- ListTickets はバックログの唯一のデータ源（9.2）。
--
-- **総件数と最終更新を同じクエリの窓関数で返す。** 2.6 の total と 2.7 の ETag の
-- 材料であり、別クエリにすると WHERE を二重に持つことになる。フィルタが13種類
-- あるため、写しが片方だけ古くなる危険が現実的に高い（user.sql の
-- ListAdminUsers / SummarizeAdminUsers は「一字一句そろえる」と注記して2本に
-- 分けているが、あちらは条件が3つである）。窓関数は WHERE の後・LIMIT の前に
-- 評価されるので、ページを切っても総件数は絞り込み全体のものになる。
--
-- **異なる種類の条件どうしは AND、同じ条件の複数指定は OR**（9.2.1）。
-- 「指定なし」は空配列で表す。none（未割当・未分類）は別のフラグに分けてある
-- ——配列の中に 'none' という値を混ぜると、その ULID を持つ行と区別できない。
--
-- **フィルタは行単位で適用し、親を補完しない**（9.2.4）。親が条件に合わない子は
-- parent_seq を保ったまま返り、画面がトップレベルに並べる。補完すると、条件に
-- 合致しない行が一覧に現れて total と表示件数が食い違う。
--
-- has_children は「プロジェクト内に子がいるか」であって「結果の中に子がいるか」
-- ではない（利用者の判断、2026-08-23）。結果の中で数えると、親が絞り込みで
-- 落ちた瞬間に子の有無まで消える。
--
-- name: ListTickets :many
WITH RECURSIVE subtree AS (
  -- parent 指定（9.2.1）。そのチケットと全子孫に限る。**カンマ区切りで
  -- 複数指定でき、いずれかの部分木に含まれるものが OR で返る**——バックログの
  -- エピックフィルタがこれを使う（GuiDesign.md 5.4「エピックをフィルタにする」）。
  -- 未指定（空配列）のときは起点が無いので空になる。
  --
  -- **UNION ALL ではなく UNION を使う。** 起点が複数あると、あるエピックと
  -- その配下のエピックを同時に選んだときに同じ行が2度出る。IN で使う限り
  -- 結果は変わらないが、重複を運ぶ意味がない。
  SELECT id FROM ticket
   WHERE project_id = @project_id::text AND seq = ANY(@parent_seqs::int[])
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
),
filtered AS (
  SELECT
    t.id,
    t.seq,
    t.type,
    t.title,
    t.status_key,
    ws.name       AS status_name,
    ws.category   AS status_category,
    ws.sort_order AS status_sort_order,
    t.priority,
    t.assignee_id,
    aa.kind         AS assignee_kind,
    aa.display_name AS assignee_name,
    t.reporter_id,
    ra.kind         AS reporter_kind,
    ra.display_name AS reporter_name,
    pt.seq AS parent_seq,
    EXISTS (SELECT 1 FROM ticket ch WHERE ch.parent_id = t.id) AS has_children,
    t.sort_key,
    t.staged_at,
    t.sprint_id,
    sp.name AS sprint_name,
    t.estimate_point,
    t.estimate_hours,
    t.actual_hours,
    t.start_date,
    t.due_date,
    t.closed_at,
    t.version,
    t.created_at,
    t.updated_at
  FROM ticket t
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
  LEFT JOIN actor  aa ON aa.id = t.assignee_id
  LEFT JOIN actor  ra ON ra.id = t.reporter_id
  LEFT JOIN ticket pt ON pt.id = t.parent_id
  LEFT JOIN sprint sp ON sp.id = t.sprint_id
  WHERE t.project_id = @project_id::text
    AND (cardinality(@status_keys::text[]) = 0
         OR t.status_key = ANY(@status_keys::text[]))
    AND (cardinality(@status_categories::text[]) = 0
         OR ws.category = ANY(@status_categories::text[]))
    AND (cardinality(@types::text[]) = 0
         OR t.type = ANY(@types::text[]))
    AND (cardinality(@priorities::text[]) = 0
         OR t.priority = ANY(@priorities::text[]))
    AND (
      (cardinality(@assignee_ids::text[]) = 0 AND NOT @assignee_none::boolean)
      OR t.assignee_id = ANY(@assignee_ids::text[])
      OR (@assignee_none::boolean AND t.assignee_id IS NULL)
    )
    AND (
      (cardinality(@tag_ids::text[]) = 0 AND NOT @tag_none::boolean)
      OR EXISTS (SELECT 1 FROM ticket_tag tt
                  WHERE tt.ticket_id = t.id AND tt.tag_id = ANY(@tag_ids::text[]))
      OR (@tag_none::boolean
          AND NOT EXISTS (SELECT 1 FROM ticket_tag tt2 WHERE tt2.ticket_id = t.id))
    )
    AND (
      (cardinality(@sprint_ids::text[]) = 0 AND NOT @sprint_none::boolean)
      OR t.sprint_id = ANY(@sprint_ids::text[])
      OR (@sprint_none::boolean AND t.sprint_id IS NULL)
    )
    -- open（9.2.1）。true で未完了のみ、false で完了のみ。
    AND (@open_filter::text = 'all'
         OR (@open_filter::text = 'open'   AND t.closed_at IS NULL)
         OR (@open_filter::text = 'closed' AND t.closed_at IS NOT NULL))
    -- due_within（9.2.1）。**期限超過を含む**ので下限を置かない。
    -- due_date が NULL のものは除外する。
    AND (@due_within_days::int < 0
         OR (t.due_date IS NOT NULL
             AND t.due_date <= CURRENT_DATE + @due_within_days::int))
    AND (cardinality(@parent_seqs::int[]) = 0 OR t.id IN (SELECT id FROM subtree))
)
SELECT
  f.*,
  count(*) OVER ()                        AS total,
  (max(f.updated_at) OVER ())::timestamptz AS last_updated_at
FROM filtered f
ORDER BY
  -- 既定は sort_key の昇順（9.2.1）。人が手で並べた順を既定の見え方にする。
  -- **sort_key が NULL の行は末尾**（利用者の判断、2026-08-23）。
  CASE WHEN @sort::text = 'sort_key'   AND @sort_order::text = 'asc'  THEN f.sort_key COLLATE "C" END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'sort_key'   AND @sort_order::text = 'desc' THEN f.sort_key COLLATE "C" END DESC NULLS LAST,
  CASE WHEN @sort::text = 'seq'        AND @sort_order::text = 'asc'  THEN f.seq END ASC,
  CASE WHEN @sort::text = 'seq'        AND @sort_order::text = 'desc' THEN f.seq END DESC,
  CASE WHEN @sort::text = 'title'      AND @sort_order::text = 'asc'  THEN f.title COLLATE "ja-JP-x-icu" END ASC,
  CASE WHEN @sort::text = 'title'      AND @sort_order::text = 'desc' THEN f.title COLLATE "ja-JP-x-icu" END DESC,
  -- 状態はワークフローの sort_order で並べる（キーの辞書順ではない）。
  CASE WHEN @sort::text = 'status'     AND @sort_order::text = 'asc'  THEN f.status_sort_order END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'status'     AND @sort_order::text = 'desc' THEN f.status_sort_order END DESC NULLS LAST,
  -- **優先度は意味の順**（利用者の判断、2026-08-23）。キーの辞書順だと high が
  -- lowest より前に来て、「優先度で並べた」と読めない結果になる。
  --
  -- **順位を SELECT 側の列にしない。** CASE 式を列として出すと、優先度が未設定の
  -- 行で NULL になり、sqlc がキャストから NOT NULL と推論した型では読めなくなる
  -- （実サーバの検証で 500 になって気づいた）。array_position は要素が NULL でも
  -- 見つからなくても NULL を返すので、未設定は NULLS LAST でそのまま末尾に来る。
  CASE WHEN @sort::text = 'priority' AND @sort_order::text = 'asc'
       THEN array_position(ARRAY['lowest','low','medium','high','highest'], f.priority) END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'priority' AND @sort_order::text = 'desc'
       THEN array_position(ARRAY['lowest','low','medium','high','highest'], f.priority) END DESC NULLS LAST,
  CASE WHEN @sort::text = 'due_date'   AND @sort_order::text = 'asc'  THEN f.due_date END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'due_date'   AND @sort_order::text = 'desc' THEN f.due_date END DESC NULLS LAST,
  CASE WHEN @sort::text = 'created_at' AND @sort_order::text = 'asc'  THEN f.created_at END ASC,
  CASE WHEN @sort::text = 'created_at' AND @sort_order::text = 'desc' THEN f.created_at END DESC,
  CASE WHEN @sort::text = 'updated_at' AND @sort_order::text = 'asc'  THEN f.updated_at END ASC,
  CASE WHEN @sort::text = 'updated_at' AND @sort_order::text = 'desc' THEN f.updated_at END DESC,
  -- 同値の行の順序が実行ごとに揺れないようにする最終キー。
  f.seq ASC
LIMIT @page_limit OFFSET @page_offset;

-- ListTagsForTickets は一覧の tags[] を一括で引く（9.2.2）。
--
-- **チケット1件ごとに引かない。** 一覧は最大200件で、行ごとに1回引くと
-- 200往復になる。並びはタグの sort_order であり、バックログのグループ化の
-- セクション順（GuiDesign.md 5.4.1）と同じ根拠を使う。
-- name: ListTagsForTickets :many
SELECT tt.ticket_id, tg.id, tg.name
  FROM ticket_tag tt
  JOIN tag tg ON tg.id = tt.tag_id
 WHERE tt.ticket_id = ANY(@ticket_ids::text[])
 ORDER BY tg.sort_order, tg.name;

-- ── 詳細（ApiDesign.md 9.5.1。手順16b では POST の応答にだけ使う）────

-- GetTicketBySeq は 9.5 形式の本体を1件引く。列は ListTickets とそろえてある
-- （9.5.1 が「9.2 の items[] に body_md 等を加えたもの」と定めているため）。
-- name: GetTicketBySeq :one
SELECT
  t.id,
  t.seq,
  t.type,
  t.title,
  t.body_md,
  t.status_key,
  ws.name     AS status_name,
  ws.category AS status_category,
  t.priority,
  t.assignee_id,
  aa.kind         AS assignee_kind,
  aa.display_name AS assignee_name,
  t.reporter_id,
  ra.kind         AS reporter_kind,
  ra.display_name AS reporter_name,
  pt.seq AS parent_seq,
  EXISTS (SELECT 1 FROM ticket ch WHERE ch.parent_id = t.id) AS has_children,
  t.staged_at,
  t.sort_key,
  t.sprint_id,
  sp.name AS sprint_name,
  t.estimate_point,
  t.estimate_hours,
  t.actual_hours,
  t.start_date,
  t.due_date,
  t.closed_at,
  t.version,
  t.created_at,
  t.updated_at
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
LEFT JOIN actor  aa ON aa.id = t.assignee_id
LEFT JOIN actor  ra ON ra.id = t.reporter_id
LEFT JOIN ticket pt ON pt.id = t.parent_id
LEFT JOIN sprint sp ON sp.id = t.sprint_id
WHERE t.project_id = @project_id AND t.seq = @seq;

-- GetTicketBrief は 9.5.1 の parent（親の要約）を引く。
-- name: GetTicketBrief :one
SELECT t.seq, t.title, t.type, t.status_key,
       ws.name AS status_name, ws.category AS status_category
  FROM ticket t
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.id = @id;

-- ListTicketChildrenBrief は 9.5.1 の children（直下の子だけ。孫は含めない）。
-- name: ListTicketChildrenBrief :many
SELECT c.seq, c.title, c.type, c.status_key,
       ws.name AS status_name, ws.category AS status_category,
       c.assignee_id, aa.kind AS assignee_kind, aa.display_name AS assignee_name
  FROM ticket c
  JOIN project p ON p.id = c.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = c.status_key
  LEFT JOIN actor aa ON aa.id = c.assignee_id
 WHERE c.parent_id = @parent_id
 ORDER BY c.sort_key COLLATE "C" NULLS LAST, c.seq;

-- ── 作成（ApiDesign.md 9.3）─────────────────────────────────

-- NextTicketSeq は DbDesign.md 6.4.1 の1文。行ロックと採番が同時に完了する。
--
-- **シーケンスを使わない。** トランザクションが巻き戻っても値を消費するため
-- 欠番が出る。チケット番号は人が読む識別子であり、my-app-31 の次が my-app-33
-- になるのは望ましくない（同 6.4.1）。
-- name: NextTicketSeq :one
UPDATE project_counter
   SET last_ticket_seq = last_ticket_seq + 1
 WHERE project_id = @project_id
RETURNING last_ticket_seq;

-- ResolveInitialStatusKey は 9.3 の「category='todo' かつ sort_order 最小。
-- 該当が無ければ sort_order 最小」をそのまま1文で表す。
--
-- 初期ステータスをリクエストで指定させないのは、ワークフローの入口が
-- workflow_transition に定義されておらず、任意のステータスで作成できると
-- 9.6 の遷移検証を素通りできてしまうためである（9.3）。
-- name: ResolveInitialStatusKey :one
SELECT ws.key
  FROM workflow_status ws
  JOIN project p ON p.workflow_id = ws.workflow_id
 WHERE p.id = @project_id
 ORDER BY (ws.category = 'todo') DESC, ws.sort_order
 LIMIT 1;

-- name: CreateTicket :exec
INSERT INTO ticket (
  id, project_id, seq, parent_id, type, title, body_md, status_key, priority,
  assignee_id, reporter_id, estimate_point, estimate_hours,
  start_date, due_date, sprint_id, sort_key
) VALUES (
  @id, @project_id, @seq, @parent_id, @type, @title, @body_md, @status_key, @priority,
  @assignee_id, @reporter_id, @estimate_point, @estimate_hours,
  @start_date, @due_date, @sprint_id, @sort_key
);

-- name: AttachTicketTag :exec
INSERT INTO ticket_tag (ticket_id, tag_id) VALUES (@ticket_id, @tag_id)
ON CONFLICT DO NOTHING;

-- FindTicketIDBySeq は parent_seq（9.3）の解決に使う。**同一プロジェクトに
-- 限る**——親もリンク先も同一プロジェクト内に限るのが Phase 1 の前提である（9.1）。
-- name: FindTicketIDBySeq :one
SELECT id FROM ticket WHERE project_id = @project_id AND seq = @seq;

-- CountProjectTagsByIDs は tag_ids がすべて当該プロジェクトのものかを数える（9.3）。
-- 渡した件数と一致しなければ、他プロジェクトのタグか存在しない ID が混ざっている。
-- name: CountProjectTagsByIDs :one
SELECT count(*)::bigint FROM tag
 WHERE project_id = @project_id AND id = ANY(@ids::text[]);

-- name: SprintExistsInProject :one
SELECT EXISTS (
  SELECT 1 FROM sprint WHERE project_id = @project_id AND id = @id
);

-- IsProjectMember は assignee_id の検証に使う（9.3 の not_a_member）。
-- name: IsProjectMember :one
SELECT EXISTS (
  SELECT 1 FROM project_member WHERE project_id = @project_id AND actor_id = @actor_id
);

-- ── 並べ替え（ApiDesign.md 9.4）─────────────────────────────

-- GetTicketSortRow は move の対象を引く。
--
-- staged_at と parent_type を一緒に返すのは、9.4.1 の2つの判定に要るためである。
--
--   - position を「段の中」で解釈する（staged 省略時は現在の段）
--   - 段に置けるのは表示上のトップレベルだけ（親を持たない、または親がエピック）。
--     **エピック自身は除く**——どちらの段にも行として出ないので、上げても
--     見えない（GuiDesign.md 5.4）。判定に自分の type も要る。
--
-- parent_type は親がいなければ NULL になる。
-- name: GetTicketSortRow :one
SELECT t.id, t.type, t.sort_key, t.staged_at, t.version, pt.type AS parent_type
  FROM ticket t
  LEFT JOIN ticket pt ON pt.id = t.parent_id
 WHERE t.project_id = @project_id AND t.seq = @seq;

-- 以下5本が「どのキーとどのキーの間へ入れるか」を決める。**空文字は境界**
-- （先頭より前／末尾より後）を表し、lexorank.Between の引数の約束と同じである。

-- MinTicketSortKeyInStage / MaxTicketSortKeyInStage は position の解決に使う。
--
-- **段の中で解釈する**（9.4.1）。"first" は「オンステージの先頭」であって
-- 「プロジェクト全体の先頭」ではない。**空の段へ最初の1件を落とすとき、
-- 基準にできる行が無い**ため、この2本が要る。
-- name: MinTicketSortKeyInStage :one
SELECT COALESCE(min(sort_key COLLATE "C"), '')::text FROM ticket
 WHERE project_id = @project_id AND (staged_at IS NOT NULL) = @staged::boolean;

-- name: MaxTicketSortKeyInStage :one
SELECT COALESCE(max(sort_key COLLATE "C"), '')::text FROM ticket
 WHERE project_id = @project_id AND (staged_at IS NOT NULL) = @staged::boolean;

-- MaxTicketSortKey はプロジェクト全体の末尾。**作成時の採番だけが使う**
-- （9.3。新規チケットは必ずバックログへ入るので、段で絞る意味がない）。
-- name: MaxTicketSortKey :one
SELECT COALESCE(max(sort_key COLLATE "C"), '')::text FROM ticket
 WHERE project_id = @project_id;

-- TicketSortKeyAfter / TicketSortKeyBefore は**段を問わない**（9.4.1）。
-- sort_key はプロジェクト内で1本であり、どの行の隣を指定しても位置は一意に定まる。
-- name: TicketSortKeyAfter :one
SELECT COALESCE(min(sort_key COLLATE "C"), '')::text FROM ticket
 WHERE project_id = @project_id AND sort_key COLLATE "C" > @after::text;

-- name: TicketSortKeyBefore :one
SELECT COALESCE(max(sort_key COLLATE "C"), '')::text FROM ticket
 WHERE project_id = @project_id AND sort_key COLLATE "C" < @before::text;

-- ListTicketIDsInSortOrder は振り直し（9.4 の rebalanced）の対象を現在の並びで返す。
-- sort_key が NULL の行も含める——振り直しはそれを埋める機会でもある。
-- name: ListTicketIDsInSortOrder :many
SELECT id FROM ticket
 WHERE project_id = @project_id
 ORDER BY sort_key COLLATE "C" NULLS LAST, seq;

-- SetTicketSortKey は振り直しの1行ぶん。
--
-- **version を上げない。** 振り直しはプロジェクトの全行に触るので、上げると
-- 開いている詳細画面がすべて 409 になる。動かしたい1件だけが MoveTicket で
-- 上がる（9.4）。updated_at はトリガで動くため、一覧の ETag は変わる
-- ——rebalanced=true を受けた画面が取り直すのと同じ結果になる。
-- name: SetTicketSortKey :exec
UPDATE ticket SET sort_key = @sort_key WHERE id = @id;

-- MoveTicket は動かした1件の sort_key と段を書き、version を +1 する（9.4）。
--
-- **段と位置を1文で書く**（9.4.1）。ドラッグ&ドロップの1操作で両方が同時に
-- 決まるため、2文に分けると途中で失敗したときに「段は移ったが位置は末尾」と
-- いう中途半端な状態が残る。
--
-- change_stage が false のとき staged_at は現在値のままで、並べ替えだけを行う
-- （リクエストで staged を省略した場合）。
-- name: MoveTicket :one
UPDATE ticket SET
   sort_key  = @sort_key,
   staged_at = CASE WHEN @change_stage::boolean THEN @staged_at ELSE staged_at END,
   version   = version + 1
 WHERE project_id = @project_id AND id = @id
RETURNING seq, sort_key, staged_at, version;

-- ── 開発用デモデータ（pb dev seed。DbDesign.md 7.6.4）──────────

-- ListTicketTitlesByProject は投入の冪等判定と親の解決に使う。
--
-- **API からは使わない。** ticket には title の一意制約が無い（DbDesign.md 6.6）ので、
-- 名前で突き合わせるのは定義ファイルから投入する場面に限る。
-- name: ListTicketTitlesByProject :many
SELECT id, seq, title FROM ticket WHERE project_id = @project_id ORDER BY seq;

-- SetTicketClosedAt は完了済みのデモチケットを作るためだけのもの。
--
-- **本来 closed_at はステータス遷移の副作用としてのみ動く**（DbDesign.md 6.6、
-- ApiDesign.md 9.6）。その経路は手順17 で実装するため、それまでの間、
-- デモデータが「完了したチケット」を持てるようにここで直接書く。
-- 一覧の open フィルタ（9.2.1）とスプリントの進捗（9.12 の closed_count）が
-- closed_at を基準にしており、NULL のままでは目で確かめられない。
-- name: SetTicketClosedAt :exec
UPDATE ticket SET closed_at = @closed_at WHERE id = @id;

-- SetTicketStagedAt はデモデータをオンステージに置くためだけのもの。
--
-- **本来 staged_at は move の副作用としてのみ動く**（ApiDesign.md 9.4.1）。
-- ただし二段の画面（GuiDesign.md 5.4）は、seed 直後にオンステージが空だと
-- 「動いていること」を目で確かめられない。SetTicketClosedAt と同じ扱いである。
-- name: SetTicketStagedAt :exec
UPDATE ticket SET staged_at = @staged_at WHERE id = @id;
