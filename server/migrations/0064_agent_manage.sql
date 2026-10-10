-- +goose Up
INSERT INTO permission(key,category,description,sort_order)
VALUES('agent.manage','agent','プロジェクトのエージェント管理',63) ON CONFLICT DO NOTHING;
INSERT INTO role_permission(role_key,permission_key)
VALUES('administrator','agent.manage'),('project_admin','agent.manage') ON CONFLICT DO NOTHING;
