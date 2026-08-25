-- ワークフローの解決（DbDesign.md 6.5、ApiDesign.md 9.6 / 9.7）。
--
-- 手順17a で追加。消費者はステータス遷移（9.6）と遷移先の一覧（9.7）で、
-- どちらもチケット詳細画面（GuiDesign.md 5.5）のステータスドロップダウンが使う。
--
-- **本ファイルには1本しか置かない。** ステータスと遷移そのものを引くクエリは
-- project.sql に既にある（ListWorkflowStatuses / ListWorkflowTransitions。
-- 手順9b がプロジェクト詳細 5.4 のために作った）。同じものを project_id 起点で
-- 作り直すと、片方だけ列が増えたときに応答が割れる。**足りなかったのは
-- 「プロジェクトからワークフローへの1段」だけ**なので、それだけを足す。

-- FindProjectWorkflowID は project_id からワークフローを引く。
--
-- **NULL になりうる。** project.workflow_id は ON DELETE SET NULL であり
-- （DbDesign.md 6.5 末尾）、ワークフローを持たないプロジェクトが存在しうる。
-- そのとき 9.6 の遷移はすべて 422 unknown_status（検証1）に倒れ、9.7 は
-- 空の items[] を返す——どちらも「行ける先が無い」という同じ事実を表す。
-- name: FindProjectWorkflowID :one
SELECT workflow_id FROM project WHERE id = @project_id;
