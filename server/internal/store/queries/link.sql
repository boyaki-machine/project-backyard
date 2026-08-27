-- チケット間リンク（DbDesign.md 6.6 の ticket_link、ApiDesign.md 9.10.1）。
--
-- 手順18a で追加。チケット詳細（GuiDesign.md 5.5）の「関連チケット」が消費者で、
-- 詳細応答（9.5.1）の links も同じ一覧を読む。
--
-- **外部参照（reference.sql）と役割が違う。** あちらは PB の外を指し、こちらは
-- 同じプロジェクトの別のチケットを指す。target_ticket_id に FK があるぶん、
-- 整合性は DB が保証する（9.10）。
--
-- **一覧だけが ticket_id で閉じていない。** 当該チケットが source である行と
-- target である行の両方を返すためで、WHERE は2本に分かれる（下記）。更新系は
-- 「このチケットに紐づく行か」を id と合わせて確かめる。

-- ListTicketLinks は双方向を1本で返す（9.10.1）。2回問い合わせると N+1 になる。
--
-- **ticket に入るのは常に「相手」である。** outgoing なら target、incoming なら
-- source で、自分は入らない。画面は「ブロック元」と「ブロック先」を同じリストに
-- 並べるため（GuiDesign.md 5.5）、行の形が揃っている必要がある。
--
-- **direction_rank は並び順のためだけの列である。** UNION の ORDER BY は出力列
-- しか参照できず、'outgoing' < 'incoming' は文字列としては逆順になる。
-- 見た目の値（direction）と並びの値を分けておくと、後で順序を変えるときに
-- 文字列の綴りに引きずられない。
--
-- **status は LEFT JOIN。** ticket.status_key は物理FKではなく（DbDesign.md 6.6）、
-- ワークフローを差し替えた後に定義の無いキーが残りうる。
-- name: ListTicketLinks :many
SELECT
  l.id,
  0::integer       AS direction_rank,
  'outgoing'::text AS direction,
  l.link_type,
  l.lag_days,
  l.origin,
  l.created_at,
  t.seq            AS ticket_seq,
  t.title          AS ticket_title,
  t.type           AS ticket_type,
  t.status_key     AS ticket_status_key,
  ws.name          AS ticket_status_name,
  ws.category      AS ticket_status_category
FROM ticket_link l
JOIN ticket t ON t.id = l.target_ticket_id
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE l.source_ticket_id = @ticket_id
UNION ALL
SELECT
  l.id,
  1::integer       AS direction_rank,
  'incoming'::text AS direction,
  l.link_type,
  l.lag_days,
  l.origin,
  l.created_at,
  t.seq            AS ticket_seq,
  t.title          AS ticket_title,
  t.type           AS ticket_type,
  t.status_key     AS ticket_status_key,
  ws.name          AS ticket_status_name,
  ws.category      AS ticket_status_category
FROM ticket_link l
JOIN ticket t ON t.id = l.source_ticket_id
JOIN project p ON p.id = t.project_id
LEFT JOIN workflow_status ws ON ws.workflow_id = p.workflow_id AND ws.key = t.status_key
WHERE l.target_ticket_id = @ticket_id
ORDER BY direction_rank, link_type, ticket_seq;

-- GetTicketLink は1件を、**このチケットに紐づいているかを含めて**引く。
--
-- **source と target のどちらでもよい。** DELETE は direction を問わないため
-- （9.10.1。画面が両方を同じリストに並べる以上、片方だけ消せないと
-- 「消せない行」が混ざる）。
--
-- 相手の seq と link_type を返すのは、削除を activity へ記録するとき
-- 「blocks my-app-12」の要約を組み立てるためである（9.10.1）。
-- name: GetTicketLink :one
SELECT
  l.id,
  l.link_type,
  l.lag_days,
  l.origin,
  l.created_at,
  CASE WHEN l.source_ticket_id = @ticket_id THEN 'outgoing' ELSE 'incoming' END::text AS direction,
  t.seq   AS ticket_seq,
  t.title AS ticket_title
FROM ticket_link l
JOIN ticket t
  ON t.id = CASE WHEN l.source_ticket_id = @ticket_id
                 THEN l.target_ticket_id ELSE l.source_ticket_id END
WHERE l.id = @id
  AND (l.source_ticket_id = @ticket_id OR l.target_ticket_id = @ticket_id);

-- name: CreateTicketLink :exec
INSERT INTO ticket_link (
  id, source_ticket_id, target_ticket_id, link_type, lag_days, origin, created_by
) VALUES (
  @id, @source_ticket_id, @target_ticket_id, @link_type, @lag_days, @origin, @created_by
);

-- TicketLinkExists は uq_ticket_link（source, target, link_type）の重複を、
-- INSERT の前に見る（9.10.1 の 409 already_exists）。
--
-- **一意制約違反を捕まえて 409 に訳す方式は採らない。** pgx のエラーコードから
-- 制約名を読む処理が1か所増えるうえ、**同じトランザクションが中断される**ため、
-- activity の記録と同じ RunInTx の中で扱いにくい。先に見るほうが素直である。
-- name: TicketLinkExists :one
SELECT EXISTS (
  SELECT 1 FROM ticket_link
   WHERE source_ticket_id = @source_ticket_id
     AND target_ticket_id = @target_ticket_id
     AND link_type = @link_type
)::boolean;

-- name: DeleteTicketLink :execrows
DELETE FROM ticket_link
WHERE id = @id
  AND (source_ticket_id = @ticket_id OR target_ticket_id = @ticket_id);
