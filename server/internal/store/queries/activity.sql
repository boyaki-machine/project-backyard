-- 業務履歴（ApiDesign.md 9.1.1、DbDesign.md 6.8 の activity）。
--
-- **audit_log とは読み手が違う。** audit_log は認証・権限・トークン・ユーザー管理を
-- インスタンス管理者が追うためのもので、activity はチケットの変更をプロジェクトの
-- メンバーが読むためのものである（GuiDesign.md 5.5 の「変更履歴」）。混ぜると
-- 監査ログがチケット更新で埋まって本来の用途に使えなくなる。
--
-- 読み出し（GET /projects/:key/activity）は手順19 で足す。手順16b では
-- チケット作成の記録だけを書く。

-- name: InsertActivity :exec
INSERT INTO activity (
  id, project_id, entity_type, entity_id, actor_id,
  action, field, old_value, new_value, request_id
) VALUES (
  @id, @project_id, @entity_type, @entity_id, @actor_id,
  @action, @field, @old_value, @new_value, @request_id
);
