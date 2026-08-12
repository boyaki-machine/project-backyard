-- 正本: DbDesign.md 6.8（履歴と監査）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- audit_log に request_id を足す。ApiDesign.md 2.5 のエラー応答と
-- Design.md 10.1 のアプリケーションログを、同一リクエストの監査記録と
-- 突き合わせるための列。activity（0008）が既に同じ形で持っている。
--
-- audit_log 自体の作成は 0008。適用済みファイルは編集しない（5.3）ため
-- 別ファイルとして足している。

-- +goose Up

ALTER TABLE audit_log
  ADD COLUMN request_id char(26) COLLATE "C";
