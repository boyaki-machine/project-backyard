-- コメントに関するクエリ（DbDesign.md 6.7、ApiDesign.md 9.8）。
--
-- 手順17a で追加。**コメントAPI（9.8）そのものは手順18 だが、9.6 の遷移が
-- kind='progress' のコメントを作る**ため、投入と件数だけを先に置く。
-- 手順18 は本ファイルに一覧・更新・削除を足す形になる。
--
-- **deleted_at は論理削除である**（DbDesign.md 6.7）。数えるときも読むときも
-- deleted_at IS NULL で絞る。

-- CreateComment は 9.6 の遷移コメントが使う。
--
-- **kind は呼び出し側が決める。** 遷移は 'progress'、手順18 の 9.8 は投稿時に
-- 利用者が選ぶ（discussion / decision / …）。既定値に頼らず明示で渡すのは、
-- 「既定で作られたのか意図して選ばれたのか」が行から読めなくなるためである。
--
-- **origin は呼び出し元の actor.kind から決める**（human / agent）。
-- **in_reply_to は手順18a で足した**（9.8 の返信）。9.6 の遷移コメントは返信を
-- 持たないので、あちらは NULL を渡す——列を増やすより、呼び出し側が「返信では
-- ない」を明示するほうが、後から読んだときに意図が残る。
-- **agent_run_id は手順26c で足した**（0022 で FK が付いた。DbDesign.md 6.7 / 8.2.4）。
-- **埋まるのは pb_submit_result が作る完了レポートのコメントだけ**——遷移コメントも
-- pb_post_note も、その時点で run が存在しないので NULL を渡す。
-- name: CreateComment :exec
INSERT INTO comment (id, ticket_id, author_id, body_md, kind, origin, in_reply_to, agent_run_id)
VALUES (@id, @ticket_id, @author_id, @body_md, @kind, @origin,
        sqlc.narg('in_reply_to'), sqlc.narg('agent_run_id'));

-- CountTicketComments は 9.5.1 の comment_count。
--
-- **手順17 から実数を返す**（それまでは 0 固定だった）。9.6 の遷移がコメントを
-- 作る以上、0 を返し続けると事実と食い違う。本文は含めない——9.5.1 が
-- 「コメント本体は含めない」と定めており、件数だけで 5.5 の見出し
-- 「コメント (4)」が作れる。
-- name: CountTicketComments :one
SELECT count(*)::bigint FROM comment
 WHERE ticket_id = @ticket_id AND deleted_at IS NULL;

-- ── 手順18a：コメントAPI（ApiDesign.md 9.8）─────────────────────────
--
-- **すべてのクエリが ticket_id で閉じている。** コメントはチケットの子資源であり、
-- 他チケットの ID を渡されても行が返らないようにするためである（reference.sql と
-- 同じ形）。チケットがそのプロジェクトのものかは FindTicketIDBySeq が済ませており、
-- 到達可否（メンバーか）は RequireProjectPermission が済ませている。
--
-- **author は JOIN で引く**（LEFT ではない）。comment.author_id は NOT NULL かつ
-- ON DELETE RESTRICT なので、投稿者不在のコメントは DB が許さない（DbDesign.md 6.7）。
-- reference.sql の created_by が LEFT JOIN なのは、あちらが ON DELETE SET NULL
-- だからである——**同じ「書き手」でも、消せるかどうかが違う。**

-- ListTicketComments は 9.8 の一覧。**削除済みの行も返す**（body_md を null にして
-- 画面が「削除されました」と出す）ので、deleted_at では絞らない。
--
-- **total と last_updated をウィンドウ関数で同時に取る。** LIMIT より先に評価される
-- ので全件に対する値になり、ListTickets（9.2.5）と同じ形である。1回の問い合わせで
-- 済ませるのは、件数と ETag が同じスナップショットを指すようにするためでもある。
--
-- 並びは created_at の昇順が既定（9.8）。**許可する sort は created_at だけ**なので
-- CASE 式は order の2通りだけで足りる（ListAdminUsers は5項目ぶん並ぶ）。
-- **同値の tie-break は id** ——ULID は時刻順なので created_at と向きが揃う。
-- name: ListTicketComments :many
SELECT
  c.id,
  c.body_md,
  c.kind,
  c.in_reply_to,
  c.origin,
  c.created_at,
  c.updated_at,
  c.deleted_at,
  c.author_id,
  a.kind         AS author_kind,
  a.display_name AS author_name,
  count(*) OVER ()                        AS total,
  (max(c.updated_at) OVER ())::timestamptz AS last_updated_at
FROM comment c
JOIN actor a ON a.id = c.author_id
WHERE c.ticket_id = @ticket_id
ORDER BY
  CASE WHEN @sort_order::text = 'asc'  THEN c.created_at END ASC,
  CASE WHEN @sort_order::text = 'desc' THEN c.created_at END DESC,
  CASE WHEN @sort_order::text = 'desc' THEN c.id END DESC,
  c.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- SummarizeTicketComments は ListTicketComments が1件も返さないときの total と
-- last_updated_at。**ウィンドウ関数は行が無いと1行も返らない**ので、ETag と
-- ページャの total をここから採る（0件のページを開いたときも ETag が要る）。
-- name: SummarizeTicketComments :one
SELECT
  count(*)                        AS total,
  max(updated_at)::timestamptz    AS last_updated_at
FROM comment
WHERE ticket_id = @ticket_id;

-- GetTicketComment は1件。POST / PATCH の応答と、更新・削除の前の読み取りに使う。
--
-- **削除済みでも返す。** 「もう無い」として 404 に倒すのはハンドラの仕事で、
-- クエリは行の姿をそのまま返す——deleted_at を見て判断するために読んでいる。
-- name: GetTicketComment :one
SELECT
  c.id,
  c.body_md,
  c.kind,
  c.in_reply_to,
  c.origin,
  c.created_at,
  c.updated_at,
  c.deleted_at,
  c.author_id,
  a.kind         AS author_kind,
  a.display_name AS author_name
FROM comment c
JOIN actor a ON a.id = c.author_id
WHERE c.ticket_id = @ticket_id AND c.id = @id;

-- CommentRepliableInTicket は in_reply_to の相手が「同じチケットの、削除されて
-- いないコメント」であることを見る（9.8）。**存在しない ID と他チケットの ID と
-- 削除済みを1つの結果に畳む**——呼び出し元にとってはどれも「指せない」であり、
-- 区別して返すと他チケットのコメントの存在を探れる（Design.md 6.4.5）。
-- name: CommentRepliableInTicket :one
SELECT EXISTS (
  SELECT 1 FROM comment
   WHERE comment.ticket_id = @ticket_id
     AND comment.id = @reply_to_id
     AND comment.deleted_at IS NULL
)::boolean;

-- UpdateComment は 9.8 の PATCH。**変えられるのは body_md と kind だけ**で、
-- in_reply_to は immutable_field として先に弾かれている。
--
-- **deleted_at IS NULL を条件に入れる。** 削除済みへの PATCH は 404 だが、
-- ハンドラの読み取りと UPDATE の間に別のリクエストが消す余地がある——
-- 行数0 で返れば、ハンドラは同じ 404 の経路に合流できる。
-- name: UpdateComment :execrows
UPDATE comment SET
  body_md = COALESCE(sqlc.narg('body_md'), body_md),
  kind    = COALESCE(sqlc.narg('kind'), kind)
WHERE ticket_id = @ticket_id AND id = @id AND deleted_at IS NULL;

-- SoftDeleteComment は論理削除（DbDesign.md 4.6 / 6.7）。
--
-- **body_md は消さない。** 列が NOT NULL であり、応答で null にするのは view の
-- 仕事である（9.8）。DB に本文を残すのは、誤削除からの復旧手段を
-- 捨てないためでもある。
--
-- **updated_at はトリガが動かす**（trg_comment_updated）ので、削除も ETag に効く。
-- name: SoftDeleteComment :execrows
UPDATE comment SET deleted_at = now()
WHERE ticket_id = @ticket_id AND id = @id AND deleted_at IS NULL;
