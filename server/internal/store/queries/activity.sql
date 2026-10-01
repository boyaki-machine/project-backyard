-- 業務履歴（ApiDesign.md 9.1.1、DbDesign.md 6.8 の activity）。
--
-- activity はチケット詳細の変更履歴を支える。管理者の横断監査一覧は
-- audit_event ビューを通じて audit_log と activity を一緒に読む。
--
-- 書き込みは手順16b（チケット作成）から始まり、17a・17c・18a で対象が広がった。
-- 読み出し（GET /projects/:key/activity）は手順19a で足した。

-- name: InsertActivity :exec
INSERT INTO activity (
  id, project_id, entity_type, entity_id, actor_id, actor_kind, actor_name, target_label,
  action, field, old_value, new_value, request_id
) VALUES (
  @id, @project_id, @entity_type, @entity_id, @actor_id, @actor_kind, @actor_name,
  (SELECT p.key || '-' || t.seq::text FROM ticket t JOIN project p ON p.id = t.project_id
   WHERE t.id = @entity_id::pg_catalog.bpchar AND t.project_id = @project_id::pg_catalog.bpchar),
  @action, @field, @old_value, @new_value, @request_id
);

-- ── 読み出し（ApiDesign.md 9.13.2。手順19a）────────────────────

-- ListActivity はプロジェクトの業務履歴を新しい順に返す。
--
-- **entity_seq / entity_title を非正規化して返す**（9.13.2）。activity は
-- entity_id（ULID）しか持たないが、画面は「my-app-31 を『進行中』に変更」と
-- 出すため、行ごとにチケットを引くと N+1 になる（設計方針3）。
--
-- **ticket は LEFT JOIN である。** DELETE は物理削除なので（9.5.3）、消えた
-- チケットの行は entity_seq / entity_title が NULL になる。**ここを内部結合に
-- すると、削除の記録そのものが履歴から消える**——action='delete' の行は
-- 定義上いつも「もう存在しないチケット」を指している。
--
-- **並び順は occurred_at DESC, id DESC で固定**（9.13.2）。occurred_at だけでは
-- 足りないのは、1回の PATCH が変更した項目ごとに複数行を書くためで（9.5.2）、
-- タイトルと期限を同時に変えると2行が同じ時刻になる。ULID は単調増加なので
-- id DESC は「最後に書いた項目が上」を意味する。
--
-- @entity_id と @action_filter は空文字で「絞らない」を表す。**存在しない
-- チケットを指されたときに空文字を渡してはならない**——全件が返る。呼び出し側は
-- 解決に失敗した時点で空の一覧を返す（activity.go）。
--
-- **@entity_id には entity_type = 'ticket' を添える**。9.13.2 の `entity` は
-- `ticket:<seq>` だけなので意味は変わらないが、添えないと idx_activity_entity
-- （entity_type, entity_id, occurred_at）が使えず、チケット1件の履歴を引くたびに
-- プロジェクトの履歴を全部読む。**ID は ::pg_catalog.bpchar で受ける**（DbDesign.md 4.2）。
--
-- name: ListActivity :many
SELECT
  a.id,
  a.entity_type,
  a.entity_id,
  t.seq   AS entity_seq,
  t.title AS entity_title,
  a.actor_id,
  ac.kind         AS actor_kind,
  ac.display_name AS actor_display_name,
  a.action,
  a.field,
  a.old_value,
  a.new_value,
  a.occurred_at,
  count(*) OVER ()                          AS total,
  (max(a.occurred_at) OVER ())::timestamptz AS last_occurred_at
FROM activity a
LEFT JOIN ticket t ON t.id = a.entity_id AND a.entity_type = 'ticket'
LEFT JOIN actor ac ON ac.id = a.actor_id
WHERE a.project_id = @project_id::pg_catalog.bpchar
  AND (@entity_id::pg_catalog.bpchar = ''     OR (a.entity_type = 'ticket' AND a.entity_id = @entity_id::pg_catalog.bpchar))
  AND (@action_filter::text = '' OR a.action    = @action_filter::text)
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- SummarizeActivity は ListActivity が1件も返さないときの total と
-- last_occurred_at。**ウィンドウ関数は行が無いと1行も返らない**ので、ETag と
-- ページャの total をここから採る（0件のプロジェクト、および範囲外のページ）。
-- **同じ WHERE を2回書いている**が、片方だけ直さないよう並べて置く。
--
-- name: SummarizeActivity :one
SELECT
  count(*)::bigint                 AS total,
  COALESCE(max(occurred_at), 'epoch'::timestamptz)::timestamptz AS last_occurred_at
FROM activity
WHERE project_id = @project_id::pg_catalog.bpchar
  AND (@entity_id::pg_catalog.bpchar = ''     OR (entity_type = 'ticket' AND entity_id = @entity_id::pg_catalog.bpchar))
  AND (@action_filter::text = '' OR action    = @action_filter::text);
