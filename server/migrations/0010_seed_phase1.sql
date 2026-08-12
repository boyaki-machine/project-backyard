-- 正本: DbDesign.md 7.1〜7.4（初期データ）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- シードは冪等（ON CONFLICT DO NOTHING / DO UPDATE、DbDesign.md 5.3）。
-- 初期管理者はシードに含めない。CLI `pb admin create` で作る（DbDesign.md 7.5、手順3）。
--
-- ワークフローテンプレートは simple のみ。DbDesign.md 7.4 は with_review / with_approval も
-- 「同様に定義する」としているが、ステータス構成・遷移・権限が未定義のため実装していない。
-- 詳細は docs/PROGRESS.md の積み残しを参照。

-- +goose Up

-- 7.1 認証プロバイダ
INSERT INTO auth_provider (key, type, display_name, is_enabled, sort_order)
VALUES ('local', 'local', 'メールアドレス', true, 0)
ON CONFLICT (key) DO NOTHING;

-- 7.2 権限カタログ
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('project.view',        'project',   'プロジェクトの閲覧',           10),
  ('project.create',      'project',   'プロジェクトの作成',           11),
  ('project.edit',        'project',   'プロジェクト設定の変更',        12),
  ('project.archive',     'project',   'プロジェクトのアーカイブ',      13),
  ('ticket.view',         'ticket',    'チケットの閲覧',              20),
  ('ticket.create',       'ticket',    'チケットの作成',              21),
  ('ticket.edit',         'ticket',    'チケットの編集',              22),
  ('ticket.transition',   'ticket',    'ステータスの遷移',            23),
  ('ticket.close',        'ticket',    'チケットのクローズ',           24),
  ('ticket.assign',       'ticket',    '担当者の変更',                25),
  ('ticket.delete',       'ticket',    'チケットの削除',              26),
  ('comment.create',      'comment',   'コメントの投稿',              30),
  ('comment.edit_own',    'comment',   '自分のコメントの編集',         31),
  ('comment.delete_any',  'comment',   '任意のコメントの削除',         32),
  ('knowledge.view',      'knowledge', 'プロジェクトメモリの閲覧',      40),
  ('knowledge.propose',   'knowledge', 'プロジェクトメモリの提案',      41),
  ('knowledge.approve',   'knowledge', 'プロジェクトメモリの承認',      42),
  ('proposal.review',     'proposal',  'AI提案の承認・却下',           50),
  ('agent.register',      'agent',     'エージェントの登録',           60),
  ('agent.token.issue',   'agent',     'エージェント用トークンの発行',   61),
  ('agent.run',           'agent',     'MCP経由での実行',             62),
  ('user.manage',         'admin',     'ユーザーの管理',              70),
  ('role.manage',         'admin',     'ロールと権限の編集',           71),
  ('authprovider.manage', 'admin',     '認証プロバイダの設定',         72),
  ('auditlog.view',       'admin',     '監査ログの閲覧',              73),
  ('system.settings',     'admin',     'システム設定',                74),
  ('export.excel',        'export',    'Excelエクスポート',           80),
  ('share.publiclink',    'export',    '公開URLの発行',               81)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- 7.3 ロールと権限の割り当て
INSERT INTO role (key, scope, display_name, description, is_builtin, sort_order) VALUES
  ('operator',       'system',  'オペレータ',
   'プロジェクトとチケットの閲覧・編集ができます',                      true, 10),
  ('administrator',  'system',  'アドミニストレータ',
   'ユーザー管理・システム設定を含む全操作ができます',                   true, 20),
  ('project_admin',  'project', 'プロジェクト管理者',
   '当該プロジェクトの全操作と承認ができます',                          true, 30),
  ('project_member', 'project', 'メンバー',
   '当該プロジェクトのチケットを作成・編集できます',                     true, 40),
  ('project_viewer', 'project', '閲覧者',
   '当該プロジェクトを閲覧のみできます',                                true, 50)
ON CONFLICT (key) DO NOTHING;

-- administrator：全権限
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- operator
INSERT INTO role_permission (role_key, permission_key)
SELECT 'operator', key FROM permission
 WHERE key IN ('project.view',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'ticket.close','ticket.assign',
               'comment.create','comment.edit_own',
               'knowledge.view','knowledge.propose',
               'export.excel')
ON CONFLICT DO NOTHING;

-- project_admin
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', key FROM permission
 WHERE key IN ('project.view','project.edit','project.archive',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'ticket.close','ticket.assign','ticket.delete',
               'comment.create','comment.edit_own','comment.delete_any',
               'knowledge.view','knowledge.propose','knowledge.approve',
               'proposal.review',
               'agent.register','agent.token.issue','agent.run',
               'export.excel','share.publiclink')
ON CONFLICT DO NOTHING;

-- project_member
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_member', key FROM permission
 WHERE key IN ('project.view',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'comment.create','comment.edit_own',
               'knowledge.view','knowledge.propose',
               'export.excel')
ON CONFLICT DO NOTHING;

-- project_viewer
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_viewer', key FROM permission
 WHERE key IN ('project.view','ticket.view','knowledge.view')
ON CONFLICT DO NOTHING;

-- 7.4 ワークフローテンプレート
-- simple：未着手 / 進行中 / 完了
INSERT INTO workflow (id, project_id, name, is_template, template_key, definition)
VALUES ('01JZZZZZZZZZZZZZZZZZZZZZW1', NULL, 'シンプル', true, 'simple', '{}'::jsonb)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_status
  (id, workflow_id, key, name, category, sort_order,
   requires_human_approval, is_agent_reachable) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZS1','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'todo','未着手','todo',1,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS2','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','進行中','in_progress',2,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS3','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'done','完了','done',3,true,false)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZT1','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'todo','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT2','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','done','ticket.close','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT3','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb)
ON CONFLICT DO NOTHING;
