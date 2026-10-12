-- 委任（delegation）に関するクエリ（DbDesign.md 6.24、Delegation.md 4章・12.2）。
-- pb-246。計画の版（delegation_plan）は delegation_plan.sql（pb-247）。
-- evaluation / confirmation / delegation_handoff は別チケット（pb-248〜250）で足す。
--
-- **木への所属の判定は、既存の IsTicketDescendant（ticket.sql）を使い回す。**
-- 「候補チケットが、ある根の木に属するか」は ticket.sql が既に持っている
-- （9.5.2 の parent_cycle 検出と同じ再帰）。ここで重ねて同じ CTE を書かない。

-- CreateDelegation は委任を1件作る（Delegation.md 4.4）。
-- **終了していない委任は根ごとに1件**（uq_delegation_root_active。0058）。
-- name: CreateDelegation :exec
INSERT INTO delegation
  (id, project_id, root_ticket_id, main_actor_id, evaluator_actor_id, scope, created_by)
VALUES
  (@id, @project_id, @root_ticket_id, @main_actor_id, @evaluator_actor_id, @scope, @created_by);

-- GetDelegationByID は作成直後の応答の組み立てに使う。
-- name: GetDelegationByID :one
SELECT
  d.id, d.project_id, d.status, d.scope, d.scope_version,
  d.created_at, d.updated_at,
  rt.seq          AS root_seq,
  ma.id           AS main_actor_id,   ma.kind AS main_actor_kind,   ma.display_name AS main_actor_name,
  ea.id           AS evaluator_actor_id, ea.kind AS evaluator_actor_kind, ea.display_name AS evaluator_actor_name,
  ca.id           AS created_by_id,   ca.kind AS created_by_kind,   ca.display_name AS created_by_name
FROM delegation d
JOIN ticket rt ON rt.id = d.root_ticket_id
JOIN actor ma ON ma.id = d.main_actor_id
JOIN actor ea ON ea.id = d.evaluator_actor_id
JOIN actor ca ON ca.id = d.created_by
WHERE d.id = @id;

-- GetActiveDelegationByMainActor は 12.2 の判定に使う。
-- **呼び出し元がどこかの委任の main かどうかを、このプロジェクトに限って見る。**
-- status <> 'ended' を「現役」とする——計画中・承認済・停止のいずれでも、
-- その委任の main は「委任の木の外に起票できない」制約を受ける（5章）。
-- 行が無ければ、呼び出し元はどの委任の main でもない（通常の ticket.create の
-- 判定だけが効く）。
-- name: GetActiveDelegationByMainActor :one
SELECT d.id, d.root_ticket_id, rt.seq AS root_seq, d.status
FROM delegation d
JOIN ticket rt ON rt.id = d.root_ticket_id
WHERE d.project_id = @project_id
  AND d.main_actor_id = @main_actor_id
  AND d.status <> 'ended'
ORDER BY d.created_at DESC
LIMIT 1;

-- ListActiveDelegationsForProject は「このチケットはどこかの委任の木に
-- 属するか」を調べるのに使う（報告の提出・移動の強制）。
--
-- **プロジェクト内の委任は少数である**（1委任=1根）。候補チケットごとに
-- IsTicketDescendant（ticket.sql）へ1回ずつ照合するほうが、新しい再帰 CTE を
-- もう1本持つより単純である。
-- name: ListActiveDelegationsForProject :many
SELECT d.id, d.root_ticket_id, d.status
FROM delegation d
WHERE d.project_id = @project_id AND d.status <> 'ended';

-- GetTicketForDelegation は委任を作る前に、対象が根（親を持たない）かを
-- 確かめるのに使う。
-- name: GetTicketForDelegation :one
SELECT parent_id, execution_mode FROM ticket WHERE id = @id;

-- SetTicketExecutionMode は委任の作成時に execution_mode を 'delegated' に
-- する専用の更新である（D1）。
--
-- **9.5.2 の一般的な PATCH（UpdateTicket）を使わない。** あちらは execution_mode
-- を開けていない——「スコープ境界は縛る側が書くものである」（同節）ため、
-- エージェントが自分で緩められる経路を作らない。ここは delegation.manage を
-- 持つ人間だけが通る別経路（本ファイルの createDelegation）であり、
-- 1列だけを狙って書く。
-- name: SetTicketExecutionMode :execrows
UPDATE ticket SET execution_mode = @execution_mode WHERE id = @id;

-- GetDelegationByRootTicket は根のチケットから委任を引く（pb-247）。
-- **最新の1件を返す。** 取り下げた根では委任を作り直せる（0058。pb-281）ので、
-- 終了した委任が並ぶことがある。終了していない委任は根ごとに1件まで。
-- name: GetDelegationByRootTicket :one
SELECT id, status, scope, main_actor_id, evaluator_actor_id
FROM delegation
WHERE root_ticket_id = @root_ticket_id
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- SetDelegationStatus は委任の主状態を変える（4.3）。pb-247 では、範囲の変更が
-- 提出されたときに承認済から計画中へ戻すのに使う。
-- name: SetDelegationStatus :execrows
UPDATE delegation SET status = @status WHERE id = @id;

-- AgentUsableInProject は main_actor_id / evaluator_actor_id の検証に使う。
--
-- **agent.project_id が NULL なら、どのプロジェクトでも使える**
-- （複数プロジェクト兼用のエージェント。DbDesign.md 8.2.1 の agent テーブル）。
-- NOT NULL なら、このプロジェクトと一致することを要る。
-- name: AgentUsableInProject :one
SELECT EXISTS (
  SELECT 1 FROM agent ag
  JOIN actor a ON a.id = ag.actor_id
  WHERE ag.actor_id = @actor_id
    AND a.is_active
    AND (ag.project_id IS NULL OR ag.project_id = @project_id)
)::boolean;

-- ApproveDelegationScope は PM が委任範囲を承認する（4.3「計画中 → 承認済」。
-- pb-260）。**scope は承認した計画の版の写し**であり、scope_version を1つ進める
-- （0 は未承認。4.4）。計画中でなければ 0 行を返す。
-- name: ApproveDelegationScope :execrows
UPDATE delegation
SET status = 'approved', scope = @scope, scope_version = scope_version + 1
WHERE id = @id AND status = 'planning';

-- HasDelegatedChild は、エピックの種別を変えるときに、直下に自律実行の根が無いかを
-- 見る（pb-282）。根の親はエピックに限るので、エピックでなくなると根の条件が崩れる。
-- name: HasDelegatedChild :one
SELECT EXISTS (
  SELECT 1 FROM ticket WHERE parent_id = @parent_id AND execution_mode = 'delegated'
)::boolean;

-- ListDelegationMarks は一覧の行に出す委任の印を引く（Delegation.md 8.2。pb-276）。
-- 終了していない委任の木（根と子孫）を根から辿り、渡したチケットのぶんだけ返す。
-- **委任が無いプロジェクトでは再帰の起点が0行**なので、一覧の重さはほぼ変わらない。
-- open_confirmations はヒアリングも数える（どれも PM の回答を待っている）。
-- reported は、根ではない子で最新の完了レポートが completed なのに完了していないこと
-- （Delegation.md 5章 D2）。レビュー相当の状態が無いワークフローでも、PM が
-- まとめて完了にする子を見分けられるようにする。
-- name: ListDelegationMarks :many
WITH RECURSIVE tree AS (
  SELECT d.id AS delegation_id, d.root_ticket_id AS ticket_id
  FROM delegation d
  WHERE d.project_id = @project_id AND d.status <> 'ended'
  UNION ALL
  SELECT tree.delegation_id, t.id
  FROM ticket t JOIN tree ON t.parent_id = tree.ticket_id
)
SELECT
  tree.ticket_id,
  rt.seq AS root_seq,
  (tree.ticket_id = d.root_ticket_id)::boolean AS is_root,
  d.status,
  (SELECT count(*) FROM confirmation c
   WHERE c.delegation_id = d.id AND c.status = 'open')::bigint AS open_confirmations,
  (tree.ticket_id <> d.root_ticket_id AND t.closed_at IS NULL AND COALESCE((
     SELECT ar.status = 'completed' FROM agent_report ar
     WHERE ar.ticket_id = tree.ticket_id
     ORDER BY ar.submitted_at DESC, ar.id DESC LIMIT 1), false))::boolean AS reported
FROM tree
JOIN delegation d ON d.id = tree.delegation_id
JOIN ticket rt ON rt.id = d.root_ticket_id
JOIN ticket t ON t.id = tree.ticket_id
WHERE tree.ticket_id = ANY(@ticket_ids::text[]);
