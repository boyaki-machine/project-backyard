-- 承認待ちの保留（DbDesign.md 6.34）。
--
-- エージェントのクライアントが人に承認を求めている間、作業記録を止めておく。クライアントの
-- フック（承認を求める直前と、次のツール実行の後）が PB に知らせる（ApiDesign.md「共通作業タイマー」）。
-- hold はフックが止めた印で、人の返答待ちで明示的に止めた記録と分ける——フックは自分が止めた
-- 記録だけを再開する。hold_ms は承認待ちの累計で、waiting_ms の内訳である。

-- +goose Up
ALTER TABLE work_session
  ADD COLUMN hold text CHECK (hold IN ('approval')),
  ADD COLUMN hold_ms bigint NOT NULL DEFAULT 0 CHECK (hold_ms >= 0);
