-- +goose Up
-- 削除されたAgentの複数の開いた記録も保持する。通常の開始の排他はPJロックで行う。
DROP INDEX idx_work_session_running;
CREATE INDEX idx_work_session_running ON work_session(project_id,actor_id) WHERE state <> 'finished';
