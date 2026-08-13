-- 正本: DbDesign.md 6.2（認証とアクター）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- access_token に実効権限のセッションキャッシュを足す。
-- Design.md 6.4.5「ログインごとに実効権限を計算し、セッションにキャッシュする。
-- ロール変更時は当該ユーザーのキャッシュを無効化する」の置き場である。
--
-- 入るのは Design.md 6.4.1 の式のうち**システムロールの層のみ**。
--
--   cached_permissions = システムロールの権限 ∩ トークンのスコープ
--
-- プロジェクトロールの層は入れない。RequireProjectPermission は権限だけでなく
-- 「そのプロジェクトが在るか・メンバーか」を同じクエリで判定しており（手順6a）、
-- 権限をキャッシュしてもクエリは1本も減らないためである。
--
-- 両列とも NULL は「キャッシュが無い（未計算、または無効化済み）」を意味する。
-- 空配列 '[]' は「権限0件」であって、NULL とは別の状態である。
--
-- access_token 自体の作成は 0002。適用済みファイルは編集しない（5.3）ため
-- 別ファイルとして足している。

-- +goose Up

ALTER TABLE access_token
  ADD COLUMN cached_permissions    jsonb,
  ADD COLUMN permissions_cached_at timestamptz;
