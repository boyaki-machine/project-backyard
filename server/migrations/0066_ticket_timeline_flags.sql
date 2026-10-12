-- タイムラインの時間重複チェックとアンカー（DbDesign.md 6.6.2。pb-296）。
--
-- 3つの印が効くのは連鎖のずらし（ApiDesign.md 9.17）の中だけで、PATCH は見ない。
-- CHECK も索引も掛けない（DbDesign.md 6.6.2）。DEFAULT false の列追加は既存行を書き換えない。

-- +goose Up
ALTER TABLE ticket
  ADD COLUMN no_overlap   boolean NOT NULL DEFAULT false,
  ADD COLUMN start_pinned boolean NOT NULL DEFAULT false,
  ADD COLUMN due_pinned   boolean NOT NULL DEFAULT false;
