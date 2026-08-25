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
-- Phase 1 にエージェントは実在しないので常に 'human' になる。
-- name: CreateComment :exec
INSERT INTO comment (id, ticket_id, author_id, body_md, kind, origin)
VALUES (@id, @ticket_id, @author_id, @body_md, @kind, @origin);

-- CountTicketComments は 9.5.1 の comment_count。
--
-- **手順17 から実数を返す**（それまでは 0 固定だった）。9.6 の遷移がコメントを
-- 作る以上、0 を返し続けると事実と食い違う。本文は含めない——9.5.1 が
-- 「コメント本体は含めない」と定めており、件数だけで 5.5 の見出し
-- 「コメント (4)」が作れる。
-- name: CountTicketComments :one
SELECT count(*)::bigint FROM comment
 WHERE ticket_id = @ticket_id AND deleted_at IS NULL;
