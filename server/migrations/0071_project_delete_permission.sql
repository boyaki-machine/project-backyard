-- プロジェクトの物理削除の権限。ApiDesign.md 5.6.1、DbDesign.md 7.2。
-- **administrator にだけ配る。** project_admin が持つ project.archive は解除で戻せるが、
-- 削除は戻せないため分ける。
-- +goose Up
INSERT INTO permission (key,category,description,sort_order)
VALUES ('project.delete','project','プロジェクトの削除',14)
ON CONFLICT (key) DO NOTHING;
INSERT INTO role_permission (role_key,permission_key)
VALUES ('administrator','project.delete')
ON CONFLICT DO NOTHING;
