-- チケットに関するクエリ（DbDesign.md 6.6、ApiDesign.md 9.2 / 9.3 / 9.4）。
--
-- 手順16b で追加。消費者はバックログ画面（GuiDesign.md 5.4、手順16c）と、
-- 未実装のカンバン・ガントである。いずれも同じ ListTickets を読む。
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
-- 材料であり、別クエリにすると WHERE を二重に持つことになる。フィルタが20種類
-- あるため、写しが片方だけ古くなる危険が現実的に高い（user.sql の
-- ListAdminUsers / SummarizeAdminUsers は「一字一句そろえる」と注記して2本に
-- 分けているが、あちらは条件が3つである）。窓関数は WHERE の後・LIMIT の前に
-- 評価されるので、ページを切っても総件数は絞り込み全体のものになる。
--
-- **異なる種類の条件どうしは AND、同じ条件の複数指定は OR**（9.2.1）。
-- 「指定なし」は空配列で表す。none（未割当・未分類）は別のフラグに分けてある
-- ——配列の中に 'none' という値を混ぜると、その ULID を持つ行と区別できない。
--
-- 通常のフィルタは行単位で適用する（9.2.4）。backlog_search のときだけ、
-- 一致した子の祖先を補完し、補完した親も total と上限に含める。親が条件に合わない子は
-- parent_seq を保ったまま返り、画面がトップレベルに並べる。補完すると、条件に
-- 合致しない行が一覧に現れて total と表示件数が食い違う。
--
-- has_children は「プロジェクト内に子がいるか」であって「結果の中に子がいるか」
-- ではない。結果の中で数えると、親が絞り込みで
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
   WHERE project_id = @project_id::pg_catalog.bpchar AND seq = ANY(@parent_seqs::int[])
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
),
-- **未完了の行と、その全子孫**（9.2.1 の retired の条件3）。
--
-- ここに入る行は棚に戻さない。「自分が未完了」か「**未完了の祖先を持つ**」の
-- どちらかだからである。
--
-- **直下の親だけを見る形では足りない。**
-- 親→子→孫で子と孫だけを完了させると、孫の直下の親（子）は完了しているので
-- 孫が消える。**だが子は、その親が未完了なので残る**——結果として
-- 「子は見えるのに孫だけ消えた」歯抜けが起きる。**条件3 が防ごうとしていた
-- ものそのものである。**
--
-- 9.6 の検証7（未完了の子を抱えた親は完了にできない）があるので、通常の
-- 流れでは「根が完了した＝部分木が全部完了した」になる。**それでも祖先を
-- たどるのは、0026 の再オープン（done → in_progress）が、完了した親の下に
-- 未完了の子が居る状態を作れるためである。**
--
-- **エピックは祖先に数えない。** エピックは
-- グルーピング専用で**行として出ない**ので（GuiDesign.md 5.4）、完了しない
-- まま残っていても歯抜けを作らない。数えてしまうと、**エピック配下の
-- チケットが永久に棚へ戻らなくなる**——実運用のバックログはたいてい
-- エピックで束ねられているので、機能そのものが効かなくなる。
--
-- 起点を「未完了かつエピックでない行」に絞ることで、**表示上のトップレベル
-- （親が無いか、親がエピック。ApiDesign.md 9.4.1）から下だけを見る**形になる。
open_desc AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::pg_catalog.bpchar
     AND closed_at IS NULL
     AND type <> 'epic'
  UNION
  SELECT c.id FROM ticket c JOIN open_desc o ON c.parent_id = o.id
   WHERE c.type <> 'epic'
),
-- staged（9.2.1「オンステージで絞る」）。**staged_at を持つ行とその全子孫**で、
-- エピックを除く。スプリントの開始（sprint.sql の ListOnstageTicketIDs）と同じ定義である
-- ——**段を決めるのは親で、子は staged_at が NULL のまま親と一緒に運ばれる**（9.4.1）。
--
-- **棚に戻ったものはここでは外さない。** 下の retired の条件がそのまま効くので、
-- 既定では外れ、retired=true を一緒に送れば含まれる（条件は種類ごとに独立）。
staged_tree AS (
  SELECT id FROM ticket
   WHERE project_id = @project_id::pg_catalog.bpchar
     AND staged_at IS NOT NULL
     AND type <> 'epic'
  UNION
  SELECT c.id FROM ticket c JOIN staged_tree s ON c.parent_id = s.id
   WHERE c.type <> 'epic'
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
    t.working_agent_id,
    wa.kind         AS working_agent_kind,
    wa.display_name AS working_agent_name,
    pt.seq AS parent_seq,
    EXISTS (SELECT 1 FROM ticket ch WHERE ch.parent_id = t.id) AS has_children,
    t.sort_key,
    t.staged_at,
    t.sprint_id,
    sp.name AS sprint_name,
    t.estimate_point,
    t.estimate_hours,
    t.actual_hours,
    t.actual_point,
    t.actual_point_version,
    t.start_at,
    t.due_at,
    t.all_day,
    t.closed_at,
    t.version,
    t.created_at,
    t.updated_at
  FROM ticket t
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
  LEFT JOIN actor  aa ON aa.id = t.assignee_id
  LEFT JOIN actor  ra ON ra.id = t.reporter_id
  LEFT JOIN actor  wa ON wa.id = t.working_agent_id
  LEFT JOIN ticket pt ON pt.id = t.parent_id
  LEFT JOIN sprint sp ON sp.id = t.sprint_id
  WHERE t.project_id = @project_id::pg_catalog.bpchar
    AND (cardinality(@status_keys::text[]) = 0
         OR t.status_key = ANY(@status_keys::text[]))
    AND (cardinality(@status_categories::text[]) = 0
         OR ws.category = ANY(@status_categories::text[]))
    AND (cardinality(@types::text[]) = 0
         OR t.type = ANY(@types::text[]))
    AND (cardinality(@priorities::text[]) = 0
         OR t.priority = ANY(@priorities::text[]))
    AND (
      (cardinality(@assignee_ids::pg_catalog.bpchar[]) = 0 AND NOT @assignee_none::boolean)
      OR t.assignee_id = ANY(@assignee_ids::pg_catalog.bpchar[])
      OR (@assignee_none::boolean AND t.assignee_id IS NULL)
    )
    AND (
      (cardinality(@tag_ids::pg_catalog.bpchar[]) = 0 AND NOT @tag_none::boolean)
      OR EXISTS (SELECT 1 FROM ticket_tag tt
                  WHERE tt.ticket_id = t.id AND tt.tag_id = ANY(@tag_ids::pg_catalog.bpchar[]))
      OR (@tag_none::boolean
          AND NOT EXISTS (SELECT 1 FROM ticket_tag tt2 WHERE tt2.ticket_id = t.id))
    )
    AND (
      (cardinality(@sprint_ids::pg_catalog.bpchar[]) = 0 AND NOT @sprint_none::boolean
       AND NOT @sprint_active::boolean)
      OR t.sprint_id = ANY(@sprint_ids::pg_catalog.bpchar[])
      OR (@sprint_none::boolean AND t.sprint_id IS NULL)
      -- sprint=active（9.2.1）。**進行中のスプリントを問い合わせのたびに引く**——画面が
      -- ULID を探して送ると、開始・終了の直後に手持ちの ULID が古くなる。無ければ何にも当たらない。
      OR (@sprint_active::boolean AND t.sprint_id IN (
            SELECT sp.id FROM sprint sp
             WHERE sp.project_id = @project_id::pg_catalog.bpchar AND sp.status = 'active'))
    )
    -- open（9.2.1）。true で未完了のみ、false で完了のみ。
    AND (@open_filter::text = 'all'
         OR (@open_filter::text = 'open'   AND t.closed_at IS NULL)
         OR (@open_filter::text = 'closed' AND t.closed_at IS NOT NULL))
    -- due_within（9.2.1）。**期限超過を含む**ので下限を置かない。
    -- due_at が NULL のものは除外する。境界（基準タイムゾーンで N 日後の日の終わり
    -- ＝翌日の0時）は**ハンドラが計算して瞬間で渡す**（pb-217）。SQL で
    -- timezone() の入れ子に引数を置くと sqlc の書き換えが位置を誤り、文字が欠ける。
    AND (sqlc.narg('due_before')::timestamptz IS NULL
         OR (t.due_at IS NOT NULL
             AND t.due_at < sqlc.narg('due_before')::timestamptz))
    -- overdue（9.2.1。手順19b）。**stats.sql の overdue と同じ条件**にしてある
    -- ——ダッシュボードが出した件数と、押した先の一覧の件数が一致する必要がある
    -- （GuiDesign.md 5.3）。due_within=0d は「今日の終わりまで」でまだ過ぎていない
    -- 今日締切を含むため、代用するとずれる。**瞬間の比較なので「今日」に依らない**（pb-217）。
    AND (NOT @overdue_only::boolean
         OR (t.closed_at IS NULL
             AND t.due_at IS NOT NULL
             AND t.due_at <= now()))
    -- planned_from / planned_to（9.2.1）。**予定が少しでも重なるもの**（半開区間どうし）。
    -- 片方だけのチケットは長さ 1ms の点として扱う。期限だけのものは期限の直前の瞬間に
    -- 置くので、終日の「9/30締切」（due_at は 10/1 の0時）は 9/30 に当たる。両方 NULL は
    -- COALESCE も NULL になるため、期間を指定したときに外れる。
    AND ((sqlc.narg('planned_from')::timestamptz IS NULL
          AND sqlc.narg('planned_to')::timestamptz IS NULL)
         OR (COALESCE(t.start_at, t.due_at) IS NOT NULL
             AND (sqlc.narg('planned_to')::timestamptz IS NULL
                  OR COALESCE(t.start_at, t.due_at - interval '1 millisecond') < sqlc.narg('planned_to')::timestamptz)
             AND (sqlc.narg('planned_from')::timestamptz IS NULL
                  OR COALESCE(t.due_at, t.start_at + interval '1 millisecond') > sqlc.narg('planned_from')::timestamptz)))
    -- stale（9.2.1。手順19b）。**stats.sql の stale と同じ条件**。
    -- 日数を引数に取るのは、閾値の正本がサーバ側の定数だからである（9.13.1）。
    AND (@stale_days::int < 0
         OR (t.closed_at IS NULL
             AND t.updated_at < now() - make_interval(days => @stale_days::int)))
    -- retired（9.2.1）。**スプリントを終えて棚に戻ったものを
    -- 既定で外す。** 3つすべてを満たす行が対象である。
    --
    --   1. 完了している
    --   2. いま属しているスプリントが completed
    --   3. 親が無いか、親も完了している
    --
    -- **条件2 が「完了」だけで外さない理由。** 完了した直後に消えると、
    -- スプリント中に何が終わったかを振り返る面が無くなる。オンステージは
    -- 期間の作業台であり、期間が閉じるまでは終わったものも載っている。
    --
    -- **条件3 が要るのは、子が親より先に完了するからである。** 子だけ消えると
    -- 親を開いたときに配下が歯抜けになる。**祖先を根までたどる**——直下の親
    -- だけでは足りないことが実データで判明した（open_desc の説明を見ること）。
    --
    -- **t.sprint_id を読む**（ticket_sprint を並べ直さない）。sprint_id は
    -- 「いま属しているスプリント」を指す非正規化された写しであり
    -- （DbDesign.md 6.9.1）、最後の1件を引く結合と同じ答えになる。
    AND (@include_retired::boolean
         OR NOT (
           t.closed_at IS NOT NULL
           AND EXISTS (SELECT 1 FROM sprint rs
                        WHERE rs.id = t.sprint_id AND rs.status = 'completed')
           AND NOT EXISTS (SELECT 1 FROM open_desc od WHERE od.id = t.id)
         ))
    AND (cardinality(@parent_seqs::int[]) = 0 OR t.id IN (SELECT id FROM subtree))
    AND (NOT @staged_only::boolean OR t.id IN (SELECT id FROM staged_tree))
    -- ── 検索の条件（ApiDesign.md 9.2.1「検索の条件」）──────────
    --
    -- キーワードの一致は store/search（queries/search.sql）が済ませ、**一致した ID
    -- だけを受け取る**（Design.md 4.6 の隔離）。keyword_set が偽なら絞らない——
    -- 「語が無い」と「語はあったが0件に一致」を区別するためのフラグである。
    AND (NOT @keyword_set::boolean OR @backlog_search::boolean OR t.id = ANY(@keyword_ids::pg_catalog.bpchar[]))
    -- 番号の範囲は両端を含む。0 は指定なし（seq は1から始まる）。
    AND (@seq_from::int <= 0 OR t.seq >= @seq_from::int)
    AND (@seq_to::int <= 0 OR t.seq <= @seq_to::int)
    -- 完了日時は since 以上・before 未満。**指定すると未完了は外れる**（NULL との比較は偽）。
    AND (sqlc.narg('closed_since')::timestamptz IS NULL
         OR t.closed_at >= sqlc.narg('closed_since')::timestamptz)
    AND (sqlc.narg('closed_before')::timestamptz IS NULL
         OR t.closed_at < sqlc.narg('closed_before')::timestamptz)
    -- 着手日時（9.2.1「着手日時を導く」）。**状態が todo 区分から初めて出た遷移**の
    -- occurred_at で、列を持たず activity から導く。完了を取り消して着手し直しても
    -- min を採るので、最初の着手になる。区分はいまのワークフローで引くので、
    -- いまのワークフローに無いキーの遷移は結合で落ちる。
    AND ((sqlc.narg('started_since')::timestamptz IS NULL
          AND sqlc.narg('started_before')::timestamptz IS NULL)
         OR EXISTS (
           SELECT 1
             FROM (SELECT min(a.occurred_at) AS started_at
                     FROM activity a
                     JOIN workflow_status os
                       ON os.workflow_id = p.workflow_id AND os.key = a.old_value
                     JOIN workflow_status ns
                       ON ns.workflow_id = p.workflow_id AND ns.key = a.new_value
                    WHERE a.entity_type = 'ticket'
                      AND a.entity_id = t.id
                      AND a.action = 'transition'
                      AND a.field = 'status_key'
                      AND os.category = 'todo'
                      AND ns.category <> 'todo') st
            WHERE st.started_at IS NOT NULL
              AND (sqlc.narg('started_since')::timestamptz IS NULL
                   OR st.started_at >= sqlc.narg('started_since')::timestamptz)
              AND (sqlc.narg('started_before')::timestamptz IS NULL
                   OR st.started_at < sqlc.narg('started_before')::timestamptz)
         ))
),
-- 一致した子の祖先を、他のフィルタを適用した後で補完する。
-- 検索に当たっても他の条件から外れた子を起点にしない。UNION で重複・循環を防ぐ。
backlog_matches AS (
  SELECT t.id, t.parent_id FROM ticket t JOIN filtered f ON f.id = t.id
   WHERE @backlog_search::boolean AND t.id = ANY(@keyword_ids::pg_catalog.bpchar[])
  UNION
  SELECT p.id, p.parent_id FROM ticket p JOIN backlog_matches m ON p.id = m.parent_id
   WHERE p.project_id = @project_id::pg_catalog.bpchar
),
search_filtered AS (
  SELECT f.* FROM filtered f
   WHERE NOT @backlog_search::boolean OR f.id IN (SELECT id FROM backlog_matches)
)
SELECT
  f.*,
  count(*) OVER ()                        AS total,
  (max(f.updated_at) OVER ())::timestamptz AS last_updated_at
FROM search_filtered f
ORDER BY
  -- 既定は sort_key の昇順（9.2.1）。人が手で並べた順を既定の見え方にする。
  -- **sort_key が NULL の行は末尾**。
  CASE WHEN @sort::text = 'sort_key'   AND @sort_order::text = 'asc'  THEN f.sort_key COLLATE "C" END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'sort_key'   AND @sort_order::text = 'desc' THEN f.sort_key COLLATE "C" END DESC NULLS LAST,
  CASE WHEN @sort::text = 'seq'        AND @sort_order::text = 'asc'  THEN f.seq END ASC,
  CASE WHEN @sort::text = 'seq'        AND @sort_order::text = 'desc' THEN f.seq END DESC,
  CASE WHEN @sort::text = 'title'      AND @sort_order::text = 'asc'  THEN f.title COLLATE "ja-JP-x-icu" END ASC,
  CASE WHEN @sort::text = 'title'      AND @sort_order::text = 'desc' THEN f.title COLLATE "ja-JP-x-icu" END DESC,
  -- 状態はワークフローの sort_order で並べる（キーの辞書順ではない）。
  CASE WHEN @sort::text = 'status'     AND @sort_order::text = 'asc'  THEN f.status_sort_order END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'status'     AND @sort_order::text = 'desc' THEN f.status_sort_order END DESC NULLS LAST,
  -- **優先度は意味の順**。キーの辞書順だと high が
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
  CASE WHEN @sort::text = 'due_at'     AND @sort_order::text = 'asc'  THEN f.due_at END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'due_at'     AND @sort_order::text = 'desc' THEN f.due_at END DESC NULLS LAST,
  CASE WHEN @sort::text = 'created_at' AND @sort_order::text = 'asc'  THEN f.created_at END ASC,
  CASE WHEN @sort::text = 'created_at' AND @sort_order::text = 'desc' THEN f.created_at END DESC,
  CASE WHEN @sort::text = 'updated_at' AND @sort_order::text = 'asc'  THEN f.updated_at END ASC,
  CASE WHEN @sort::text = 'updated_at' AND @sort_order::text = 'desc' THEN f.updated_at END DESC,
  -- 完了日時（チケット検索の「完了日」の列）。**未完了（NULL）は昇順・降順とも末尾**（9.2.1）。
  CASE WHEN @sort::text = 'closed_at'  AND @sort_order::text = 'asc'  THEN f.closed_at END ASC  NULLS LAST,
  CASE WHEN @sort::text = 'closed_at'  AND @sort_order::text = 'desc' THEN f.closed_at END DESC NULLS LAST,
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
 WHERE tt.ticket_id = ANY(@ticket_ids::pg_catalog.bpchar[])
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
  t.working_agent_id,
  wa.kind         AS working_agent_kind,
  wa.display_name AS working_agent_name,
  pt.seq AS parent_seq,
  EXISTS (SELECT 1 FROM ticket ch WHERE ch.parent_id = t.id) AS has_children,
  t.staged_at,
  t.sort_key,
  t.sprint_id,
  sp.name AS sprint_name,
  t.estimate_point,
  t.estimate_hours,
  t.actual_hours,
  t.actual_point,
  t.actual_point_version,
  t.start_at,
  t.due_at,
  t.all_day,
  t.closed_at,
  t.version,
  t.created_at,
  t.updated_at,
  -- 9.5.1 の4項目（手順27）。**一覧（ListTickets）には足さない**——読む相手
  -- （pb_get_task と pb_get_context）はどちらもチケット1件を指して呼ぶ。
  t.execution_mode,
  t.readiness,
  t.readiness_note,
  t.scope
FROM ticket t
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
LEFT JOIN actor  aa ON aa.id = t.assignee_id
LEFT JOIN actor  ra ON ra.id = t.reporter_id
LEFT JOIN actor  wa ON wa.id = t.working_agent_id
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

-- GetTicketEpicAncestor は 9.5.1 の epic を引く——祖先をたどって最初に
-- 見つかるエピック。**自分自身は数えない**（エピックの詳細では、その上のエピック）。
-- 無ければ 0行で、呼び出し側が null にする。
--
-- up の1行は「深さ depth の行の親」を持つ。**深さに上限を置く**のは
-- GetDisplayRootForStaging と同じ理由（万一の循環で要求が返らなくなるのを避ける）。
-- name: GetTicketEpicAncestor :one
WITH RECURSIVE up AS (
  SELECT t.parent_id, 0 AS depth
    FROM ticket t
   WHERE t.id = @ticket_id
  UNION ALL
  SELECT p.parent_id, up.depth + 1
    FROM ticket p JOIN up ON p.id = up.parent_id
   WHERE up.depth < 32
)
SELECT t.seq, t.title, t.type, t.status_key,
       ws.name AS status_name, ws.category AS status_category
  FROM up
  JOIN ticket t ON t.id = up.parent_id
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.type = 'epic'
 ORDER BY up.depth
 LIMIT 1;

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

-- **sprint_id を受け取らない**（ApiDesign.md 9.3）。作られたチケットは
-- 必ずスプリント未所属で始まり、次にスプリントを開始したときに入る。
-- name: CreateTicket :exec
INSERT INTO ticket (
  id, project_id, seq, parent_id, type, title, body_md, status_key, priority,
  assignee_id, reporter_id, estimate_point, estimate_hours,
  start_at, due_at, all_day, sort_key
) VALUES (
  @id, @project_id, @seq, @parent_id, @type, @title, @body_md, @status_key, @priority,
  @assignee_id, @reporter_id, @estimate_point, @estimate_hours,
  @start_at, @due_at, @all_day, @sort_key
);

-- name: AttachTicketTag :exec
INSERT INTO ticket_tag (ticket_id, tag_id) VALUES (@ticket_id, @tag_id)
ON CONFLICT DO NOTHING;

-- FindTicketIDBySeq は parent_seq（9.3）の解決に使う。**同一プロジェクトに
-- 限る**——親もリンク先も同一プロジェクト内に限る（9.1）。
-- name: FindTicketIDBySeq :one
SELECT id FROM ticket WHERE project_id = @project_id AND seq = @seq;

-- CountProjectTagsByIDs は tag_ids がすべて当該プロジェクトのものかを数える（9.3）。
-- 渡した件数と一致しなければ、他プロジェクトのタグか存在しない ID が混ざっている。
-- name: CountProjectTagsByIDs :one
SELECT count(*)::bigint FROM tag
 WHERE project_id = @project_id AND id = ANY(@ids::pg_catalog.bpchar[]);


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

-- MoveTicket は動かした1件の sort_key・段・親を書き、version を +1 する（9.4）。
--
-- **段（staged_at）と親（parent_id）を位置と同じ文で書く**（9.4.1 / 9.4.2）。
-- どちらもドラッグ&ドロップの1操作で位置と同時に決まるため、文を分けると
-- 途中で失敗したときに「段は移ったが位置は末尾」「ルートにはなったが位置は
-- 元のまま」という中途半端な状態が残る。
--
-- change_stage が false のとき staged_at は現在値のままで、並べ替えだけを行う
-- （リクエストで staged を省略した場合）。unparent も同じで、false なら
-- parent_id を触らない。
--
-- **@unparent は「ルートにする」だけを表す。** move が受け取る parent_seq は
-- null に限られる（9.4.2）ので、親を付け替える経路はここに無い——それは
-- PATCH（9.5.2）の仕事である。
-- name: MoveTicket :one
UPDATE ticket SET
   sort_key  = @sort_key,
   staged_at = CASE WHEN @change_stage::boolean THEN @staged_at ELSE staged_at END,
   parent_id = CASE WHEN @unparent::boolean THEN NULL ELSE parent_id END,
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

-- ── 更新・削除・遷移（ApiDesign.md 9.5.2 / 9.5.3 / 9.6。手順17a）──────

-- UpdateTicket は 9.5.2 の部分更新。**送られたフィールドだけを更新する。**
--
-- **2つの書き方を使い分けている**（project.sql の UpdateProject と同じ形）。
--
--   NOT NULL の列（type / title）  COALESCE(sqlc.narg(…), 現在値)
--   NULL にできる列               CASE WHEN @…_set THEN sqlc.narg(…) ELSE 現在値 END
--
-- COALESCE では「NULL を送って空にする」を表せない。assignee_id を外す・親を
-- 外す・期限を消すはいずれも 9.5.2 が認めている操作なので、_set のフラグで
-- 「送られていない」と「NULL が送られた」を区別する。
--
-- **status_key / closed_at / sort_key / staged_at は含めない**（9.5.2）。
-- 前2つは 9.6 の遷移、後2つは 9.4 の move が書く。
--
-- **working_agent_id はここにも 9.6 にも書き込み口がある**（手順26b）。この文が
-- 受けるのは人の操作（消す・差し替える。ApiDesign.md 9.5.2）で、エージェント自身の
-- 宣言は SetTicketWorkingAgent が別に行う——あちらは遷移の副作用なので version を
-- 動かさず、If-Match の照合も持たない。
--
-- **updated_at はトリガが動かす**（trg_ticket_updated。DbDesign.md 6.6）。
-- タグだけを付け外しした場合もこの文を通るので、9.2.5 の ETag が必ず変わる。
--
-- name: UpdateTicket :execrows
UPDATE ticket SET
  type           = COALESCE(sqlc.narg('type'), type),
  title          = COALESCE(sqlc.narg('title'), title),
  body_md        = CASE WHEN @body_md_set::boolean        THEN sqlc.narg('body_md')        ELSE body_md END,
  priority       = CASE WHEN @priority_set::boolean       THEN sqlc.narg('priority')       ELSE priority END,
  assignee_id    = CASE WHEN @assignee_id_set::boolean    THEN sqlc.narg('assignee_id')    ELSE assignee_id END,
  working_agent_id = CASE WHEN @working_agent_id_set::boolean THEN sqlc.narg('working_agent_id') ELSE working_agent_id END,
  parent_id      = CASE WHEN @parent_id_set::boolean      THEN sqlc.narg('parent_id')      ELSE parent_id END,
  -- sprint_id は 9.5.2 から外した。スプリントの開始・終了だけが動かす
  -- （sprint.sql の SetTicketsSprintID）。DbDesign.md 6.9.1。
  estimate_point = CASE WHEN @estimate_point_set::boolean THEN sqlc.narg('estimate_point') ELSE estimate_point END,
  estimate_hours = CASE WHEN @estimate_hours_set::boolean THEN sqlc.narg('estimate_hours') ELSE estimate_hours END,
  actual_hours   = CASE WHEN @actual_hours_set::boolean   THEN sqlc.narg('actual_hours')   ELSE actual_hours END,
  actual_point   = CASE WHEN @actual_point_set::boolean   THEN sqlc.narg('actual_point')   ELSE actual_point END,
  actual_point_version = CASE WHEN @actual_point_version_set::boolean THEN sqlc.narg('actual_point_version') ELSE actual_point_version END,
  start_at       = CASE WHEN @start_at_set::boolean       THEN sqlc.narg('start_at')       ELSE start_at END,
  due_at         = CASE WHEN @due_at_set::boolean         THEN sqlc.narg('due_at')         ELSE due_at END,
  all_day        = COALESCE(sqlc.narg('all_day'), all_day),
  -- 9.5.2 で開けた4項目（手順27）。**execution_mode と scope は NOT NULL** なので
  -- COALESCE で足りる（null を送れば 422 で先に落ちる）。readiness と
  -- readiness_note は null が「未判定へ戻す」を表すので _set の形が要る。
  execution_mode = COALESCE(sqlc.narg('execution_mode'), execution_mode),
  readiness      = CASE WHEN @readiness_set::boolean      THEN sqlc.narg('readiness')      ELSE readiness END,
  readiness_note = CASE WHEN @readiness_note_set::boolean THEN sqlc.narg('readiness_note') ELSE readiness_note END,
  scope          = COALESCE(sqlc.narg('scope'), scope),
  version        = version + 1
WHERE project_id = @project_id AND seq = @seq AND version = @version;

-- GetTicketTypeByID は 9.5.2 のオンステージ判定に使う。
--
-- **新しい親の種別が要る。** 「段に置けるのは親を持たないもの、または親が
-- エピックのもの」（9.4.1）を、変更後の値で判定するためである。
-- name: GetTicketTypeByID :one
SELECT type FROM ticket WHERE id = @id;

-- IsTicketDescendant は 9.5.2 の parent_cycle 検出。
--
-- **自分自身を含む。** 起点をそのまま UNION の第1項に置いてあるので、
-- 「自分自身または自分の子孫を親に指定した」（9.5.2）を1文で判定できる。
-- DBの ck_ticket_not_self_parent は自己参照しか防げない（DbDesign.md 6.6）。
--
-- UNION ALL ではなく UNION を使うのは ListTickets の subtree と同じ理由で、
-- 重複を運ぶ意味がないためである。
-- name: IsTicketDescendant :one
WITH RECURSIVE subtree AS (
  SELECT root.id FROM ticket root WHERE root.id = @ancestor_id
  UNION
  SELECT c.id FROM ticket c JOIN subtree s ON c.parent_id = s.id
)
SELECT (count(t.id) > 0)::boolean AS is_descendant
  FROM ticket t
 WHERE t.id = @candidate_id AND t.id IN (SELECT id FROM subtree);

-- DetachTicketTags は 9.5.2 の tag_ids の置き換えに使う（丸ごと消してから付け直す）。
--
-- **差分を計算しない。** 9.5.2 は「tag_ids は丸ごと置き換える」と定めており、
-- 消してから AttachTicketTag で付け直すほうが、付ける側と外す側の2本の集合演算を
-- 持つより読み違えが少ない。件数はチケット1件ぶんで、多くても数件である。
-- name: DetachTicketTags :exec
DELETE FROM ticket_tag WHERE ticket_id = @ticket_id;

-- DeleteTicket は 9.5.3 の物理削除。
--
-- **子は消えない**（ticket.parent_id が ON DELETE SET NULL）。親を失って
-- トップレベルへ上がる。コメント・DoD・リンク・タグ付けは CASCADE で消える。
-- **activity は残る**——entity_id は多相参照で FK を持てず、9.13.2 が
-- 「削除されたチケットの行」を表示する前提で組まれている（9.5.3）。
-- name: DeleteTicket :execrows
DELETE FROM ticket WHERE project_id = @project_id AND seq = @seq;

-- SetTicketStatus は 9.6 の遷移。**closed_at を同じ文で決める。**
--
-- 遷移先の category が 'done' なら now()、それ以外なら NULL へ戻す（9.6 の表）。
-- **closed_at が動くのはこの経路だけである**——PATCH で直接書けないようにして
-- あるので（9.5.2）、9.2.1 の ?open=true（closed_at IS NULL）が「完了していない
-- もの」と一致することが保証される。
--
-- **If-Match による version の照合をしない**（9.6）。遷移そのものが競合を検出する
-- ——2人が同時に同じ遷移を実行すると、後発は「同じ状態から同じ状態へ」を
-- 要求することになり、workflow_transition に定義が無いため 409 になる。
-- name: SetTicketStatus :one
UPDATE ticket SET
  status_key = @status_key,
  closed_at  = CASE WHEN @closing::boolean THEN now() ELSE NULL END,
  version    = version + 1
WHERE project_id = @project_id AND seq = @seq
RETURNING version;

-- ── 親子の連動（ApiDesign.md 9.6 の検証7 と「子が動いたら親を進行中に」）──

-- CountOpenChildren は検証7 の材料（9.6）。**直下の子だけを数える。**
--
-- 孫まで数えないのは、同じ規則が子にも掛かるためである——孫が未完了なら子も
-- 完了にできず、子が完了していなければ親はここで止まる。規則が段ごとに効くので
-- 再帰は要らない。
--
-- **closed_at IS NULL で数える。** 9.6 の「closed_at は遷移の副作用としてのみ動く」
-- により、これが「完了していない」と一致することが保証されている（9.2 の
-- ?open=true と同じ判定）。ステータスのカテゴリで数え直すと、同じ事実を2通りに
-- 数えることになり、片方だけ直した日にずれる。
-- name: CountOpenChildren :one
SELECT count(*) FROM ticket
 WHERE parent_id = @parent_id AND closed_at IS NULL;

-- GetParentForCascade は「子が動いたら親を進行中にする」で祖先をたどる1段ぶん
-- （9.6）。子の id を渡すと、その親の seq とステータスのカテゴリが返る。
--
-- **親を持たなければ行が返らない**（parent_id が NULL のとき、内側の SELECT が
-- NULL を返して外側が0件になる）。呼び出し側はそこでたどるのをやめる。
--
-- **seq を返すのは、SetTicketStatus が project_id と seq で更新するためである。**
-- id で更新する口を別に作ると、同じ更新が2通りになる。
-- name: GetParentForCascade :one
SELECT t.id, t.seq, t.status_key, ws.category AS status_category
  FROM ticket t
  JOIN project p ON p.id = t.project_id
  LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
 WHERE t.id = (SELECT c.parent_id FROM ticket c WHERE c.id = @child_id);

-- ── 実行者（ApiDesign.md 9.6 / 9.5.2。手順26b）───────────────

-- SetTicketWorkingAgent は、遷移に成功したエージェントを実行者として立てる
-- （ApiDesign.md 9.6「遷移に成功したとき、エージェントは自分を working_agent_id に
-- 立てる」）。既に自分なら何も書かない。別のエージェントが入っていれば上書きする。
--
-- **version を動かさない。** 同じトランザクションで SetTicketStatus が既に +1 して
-- おり、ここでもう一度上げると1回の遷移で version が2つ進む。2.8 の楽観ロックは
-- 「利用者の1操作で1つ」を前提にしている。
--
-- **WHERE に現在値との比較を置いて、変わらないときは行を触らない。** trg_ticket_updated
-- が updated_at を動かすため、無変更の UPDATE でも 9.2.5 の ETag が変わってしまう。
-- name: SetTicketWorkingAgent :exec
UPDATE ticket SET working_agent_id = @working_agent_id
WHERE project_id = @project_id AND seq = @seq
  AND working_agent_id IS DISTINCT FROM @working_agent_id;

-- GetAgentOwner は working_agent_id の検証（9.5.2）と、9.6 の検証6 に使う。
--
-- **agent テーブルを引く。** actor.kind='agent' であることと所有者が誰かを一度に
-- 取るためで、行が無ければ「エージェントではない」である。
-- name: GetAgentOwner :one
SELECT ag.owner_actor_id
  FROM agent ag
  JOIN actor a ON a.id = ag.actor_id
 WHERE ag.actor_id = @actor_id AND a.kind = 'agent';

-- 表示上のトップレベルの祖先（自分を含む）を返す（ApiDesign.md 9.6
-- 「着手したら、オンステージへ上げる」）。
--
-- **段に置けるのは表示上のトップレベルだけである**（9.4.1）——親を持たないか、
-- 親がエピックのもの。着手したのが子タスクでも、動かすべきなのは**その子を
-- 含む部分木の根**であり、配下は親と一緒に運ばれる。
--
-- 上へたどって「親が無いか、親がエピック」を最初に満たした行が答えになる。
-- **その上にあるのはエピックか、何も無いかのどちらか**で、どちらも段には
-- 出ないためである。
--
-- **深さに上限を置く。** parent_id の循環は 9.5.2 の parent_cycle が書き込み時に
-- 防いでいるが、万一の循環で要求が返らなくなるのを避ける（cascade と同じ 32）。
-- name: GetDisplayRootForStaging :one
WITH RECURSIVE up AS (
  SELECT t.id, t.parent_id, t.type, t.staged_at, 0 AS depth
    FROM ticket t
   WHERE t.id = @ticket_id
  UNION ALL
  SELECT p.id, p.parent_id, p.type, p.staged_at, up.depth + 1
    FROM ticket p JOIN up ON p.id = up.parent_id
   WHERE up.depth < 32
)
SELECT u.id, u.type, (u.staged_at IS NOT NULL)::boolean AS staged
  FROM up u
  LEFT JOIN ticket pt ON pt.id = u.parent_id
 WHERE u.parent_id IS NULL OR pt.type = 'epic'
 ORDER BY u.depth
 LIMIT 1;
