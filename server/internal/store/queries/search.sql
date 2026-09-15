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
 WHERE t.project_id = @project_id::text
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
