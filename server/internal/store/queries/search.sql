-- キーワード検索（ApiDesign.md 9.2.1「検索の条件」。pb-66）。
--
-- **全文検索の実装は、このファイルと store/search/ に閉じる**（Design.md 4.6、
-- DbDesign.md 4.5）。日本語検索を pg_trgm から pg_bigm へ替えるとき、変わるのは
-- インデックス定義とここだけにする。一覧（ticket.sql の ListTickets）は、ここが
-- 返した ID を受け取るだけで、語もパターンも知らない。

-- SearchTicketIDs は、すべてのパターンを含むチケットの ID を返す。
--
-- **語ごとに、タイトル・本文・コメント（削除済みを除く）のどれかに当たればよい。**
-- 「当たらない語が1つも無い」を NOT EXISTS で書く——語の数が可変なので、AND を
-- 並べる形にできない。パターンのエスケープは store/search が済ませている。
--
-- **本文は NULL を先に落とす。** body_md は NULL になりうる。NULL のまま ILIKE に
-- 渡すと判定が NULL になり、NOT (… OR NULL …) も NULL になって「当たらない語」
-- として数えられず、**語を含まないチケットが一致してしまう。**
--
-- patterns が空なら全件が返る。呼び出し側は語が無いときに呼ばない。
-- name: SearchTicketIDs :many
SELECT t.id
  FROM ticket t
 WHERE t.project_id = @project_id::pg_catalog.bpchar
   AND NOT EXISTS (
     SELECT 1
       FROM unnest(@patterns::text[]) AS p(pattern)
      WHERE NOT (
        t.title ILIKE p.pattern
        OR (t.body_md IS NOT NULL AND t.body_md ILIKE p.pattern)
        OR EXISTS (
          SELECT 1 FROM comment c
           WHERE c.ticket_id = t.id
             AND c.deleted_at IS NULL
             AND c.body_md ILIKE p.pattern
        )
      )
   );

-- SearchTicketIDsByTrigram は SearchTicketIDs と**同じ集合**を、pg_trgm の GIN
-- インデックスを使える形で返す（DbDesign.md 4.5。pb-143）。
--
-- **語ごと・列ごとに「当たる ID」を集め、すべての語に当たったものを残す。**
-- SearchTicketIDs の NOT EXISTS はチケットを1件ずつ読んで ILIKE を当てるので、
-- インデックスを1本も使えない（3万件で数百 ms）。ここでは列ごとに別の SELECT に
-- 分けるので、title / body_md / comment.body_md の各インデックスが語ごとに効く。
--
-- **使えるのは、どの語からも trigram を取り出せるときだけ**である。取り出せない語
-- （2文字以下、または DB の ctype が C のときの日本語）が1つでもあると、インデックスが
-- 全件を返して今の形より遅くなる。切り替えは store/search の TrigramUsable が行う。
--
-- NULL の本文は ILIKE が NULL を返すので、当たる側に入らない（SearchTicketIDs の
-- 「NULL を先に落とす」はここでは要らない）。
-- name: SearchTicketIDsByTrigram :many
SELECT h.id
  FROM (
    SELECT p.pattern, x.id
      FROM unnest(@patterns::text[]) AS p(pattern)
      CROSS JOIN LATERAL (
        SELECT t.id FROM ticket t WHERE t.title ILIKE p.pattern
        UNION
        SELECT t.id FROM ticket t WHERE t.body_md ILIKE p.pattern
        UNION
        SELECT c.ticket_id AS id FROM comment c
         WHERE c.body_md ILIKE p.pattern AND c.deleted_at IS NULL
      ) x
  ) h
  JOIN ticket t ON t.id = h.id AND t.project_id = @project_id::pg_catalog.bpchar
 GROUP BY h.id
HAVING count(DISTINCT h.pattern) = cardinality(@patterns::text[]);

-- CurrentDatabaseCtype は接続先 DB の LC_CTYPE を返す（DbDesign.md 3.1 / 4.5。pb-143）。
--
-- **pg_trgm が日本語から trigram を取り出せるかは、DB を作ったときの LC_CTYPE で決まる。**
-- C では英数字しか語の文字として数えない。検索の切り替えとサーバ起動時の警告が読む。
-- **pg_catalog. と修飾して書く。** 修飾しないと sqlc がマイグレーションに無い表として拒む。
-- current_setting('lc_ctype') は使えない——PostgreSQL 16 で設定から外れた。
-- name: CurrentDatabaseCtype :one
SELECT d.datctype::text AS lc_ctype FROM pg_catalog.pg_database d WHERE d.datname = current_database();
