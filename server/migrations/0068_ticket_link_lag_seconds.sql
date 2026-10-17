-- 依存の秒のずらし（DbDesign.md 6.6.3。pb-332）。
--
-- タイムラインの連鎖のずらし（ApiDesign.md 9.17.1）は依存で結んだ2つを隙間なくそろえる。lag_days は
-- 暦日の整数で「転換10分」を表せないため、1日未満の秒を別の列で持つ。実効のずらしは lag_days を
-- 足してから lag_seconds を足したもの。DEFAULT 0 の列追加は既存行を書き換えない。

-- +goose Up
ALTER TABLE ticket_link
  ADD COLUMN lag_seconds integer NOT NULL DEFAULT 0
    CHECK (lag_seconds BETWEEN -86399 AND 86399);
