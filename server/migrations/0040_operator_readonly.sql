-- 正本: DbDesign.md 6.20、Design.md 6.4.1。pb-153。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- operator の書き込み権限が project_viewer を上書きしていた。
-- 全体管理者の権限は維持し、非管理者の操作権限はプロジェクトロールから与える。

-- +goose Up

DELETE FROM role_permission
WHERE role_key = 'operator'
  AND permission_key NOT IN (
    'project.view',
    'ticket.view',
    'knowledge.view',
    'doc.view',
    'export.excel',
    'agent.run'
  );

UPDATE role
SET description = 'プロジェクトの閲覧ができます。編集はプロジェクトロールに従います'
WHERE key = 'operator';
