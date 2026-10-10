-- 計画の依存・順序・予定だけを変更する権限。DbDesign.md 6.13。
-- +goose Up
INSERT INTO permission (key,category,description,sort_order)
VALUES ('ticket.plan.edit','ticket','依存・並び順・開始と終了予定の編集',30)
ON CONFLICT (key) DO NOTHING;
INSERT INTO role_permission (role_key,permission_key)
VALUES ('administrator','ticket.plan.edit'),('project_admin','ticket.plan.edit')
ON CONFLICT DO NOTHING;
