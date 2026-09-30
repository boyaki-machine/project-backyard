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

-- ListLinksAmongTickets はガント（9.2.6）の依存を1本で引く。
--
-- **両端が渡したチケットの集合に含まれる行だけを返す。** ガントは絞り込み・打ち切りの
-- 後に残ったチケットどうしの線しか描けない（相手の居ない線は描けない）。行ごとに
-- ListTicketLinks を呼ぶと、チケットの数だけ往復になる（設計方針3）。
--
-- **種別はガントが描く5つに限る**（GuiDesign.md 5.14）。relates / duplicates は描かない。
-- 並びは source_seq → target_seq → link_type。実行ごとに揺れないようにする。
-- created_at は応答に出さず、ETag の材料（9.2.5）にだけ使う。
-- name: ListLinksAmongTickets :many
SELECT
  l.id,
  s.seq AS source_seq,
  t.seq AS target_seq,
  l.link_type,
  l.lag_days,
  l.origin,
  l.created_at
FROM ticket_link l
JOIN ticket s ON s.id = l.source_ticket_id
JOIN ticket t ON t.id = l.target_ticket_id
WHERE l.source_ticket_id = ANY(@ticket_ids::pg_catalog.bpchar[])
  AND l.target_ticket_id = ANY(@ticket_ids::pg_catalog.bpchar[])
  AND l.link_type IN ('FS', 'SS', 'FF', 'SF', 'blocks')
ORDER BY s.seq, t.seq, l.link_type;

-- LockProjectForDependency は依存の追加をプロジェクト単位で直列化する（9.10.1）。
--
-- 輪の判定と INSERT を同じトランザクションで行うだけでは、並行する2本（A→B と
-- B→A）がどちらも「輪にならない」と読み、両方入ってしまう。**FOR NO KEY UPDATE に
-- するのは、チケットの作成（FK の確認が project に取る KEY SHARE）を待たせない
-- ため**——FOR UPDATE だと KEY SHARE とぶつかる。
-- name: LockProjectForDependency :one
SELECT id FROM project WHERE id = @project_id FOR NO KEY UPDATE;

-- DependencyPathExists は依存を辿って from から to へ行けるかを返す（9.10.1 の
-- link_cycle）。source → target を1本足す前に、target から source へ辿れるなら輪になる。
--
-- **5種（FS / SS / FF / SF / blocks）を種別を問わず1つのグラフとして辿る。**
-- relates / duplicates は向きに意味が無いので数えない。**UNION（ALL ではない）で
-- 訪ねた行を重ねない**——以前の API で作れた輪が既にあっても、辿りが止まる。
-- **起点は ticket の行から取る。** ID の列は COLLATE "C" で、引数をそのまま起点に
-- すると既定の照合順序で届き、再帰の2項の照合順序が食い違って PostgreSQL が拒む。
-- name: DependencyPathExists :one
WITH RECURSIVE reach(id) AS (
  SELECT t.id FROM ticket t WHERE t.id = @from_ticket_id
  UNION
  SELECT l.target_ticket_id
    FROM ticket_link l
    JOIN reach r ON l.source_ticket_id = r.id
   WHERE l.link_type IN ('FS', 'SS', 'FF', 'SF', 'blocks')
)
SELECT EXISTS (SELECT 1 FROM reach WHERE id = @to_ticket_id::pg_catalog.bpchar)::boolean;
