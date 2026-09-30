-- pb-181: 全プロジェクトのチケット履歴を日時順に読む管理者画面用。
-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY idx_activity_audit_time ON activity (occurred_at DESC, id DESC);
