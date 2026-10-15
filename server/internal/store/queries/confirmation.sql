-- 確認待ち（confirmation）に関するクエリ（DbDesign.md 6.27、Delegation.md 8章）。pb-249。

-- CreateConfirmation は確認待ちを1件立てる（8.1）。
-- name: CreateConfirmation :exec
INSERT INTO confirmation
  (id, delegation_id, ticket_id, reason, question_md, options, raised_by)
VALUES
  (@id, @delegation_id, @ticket_id, @reason, @question_md, @options, @raised_by);

-- HasOpenConfirmation は5章「確認待ち（どの主状態でも）」の関門を判定する
-- （4.3「関門は列に持たず、開いている確認待ちから導く」）。
--
-- **hearing（8.1 の8）は関門に数えない**（8.2「ヒアリングは止めない」）。
-- name: HasOpenConfirmation :one
SELECT EXISTS (
  SELECT 1 FROM confirmation
  WHERE delegation_id = @delegation_id AND status = 'open' AND reason <> 'hearing'
)::boolean;

-- ListOpenConfirmations はパック（9.1）と画面に出す一覧。
-- name: ListOpenConfirmations :many
SELECT
  c.id, c.ticket_id, t.seq AS ticket_seq, c.reason, c.question_md, c.options,
  c.created_at,
  ra.id AS raised_by_id, ra.kind AS raised_by_kind, ra.display_name AS raised_by_name
FROM confirmation c
JOIN ticket t ON t.id = c.ticket_id
JOIN actor ra ON ra.id = c.raised_by
WHERE c.delegation_id = @delegation_id AND c.status = 'open'
ORDER BY c.created_at;

-- GetConfirmationByID は応答の組み立てと、回答の対象確認に使う。
-- name: GetConfirmationByID :one
SELECT
  c.id, c.delegation_id, c.ticket_id, t.seq AS ticket_seq, c.reason,
  c.question_md, c.options, c.status, c.answer_md, c.created_at, c.answered_at,
  ra.id AS raised_by_id, ra.kind AS raised_by_kind, ra.display_name AS raised_by_name,
  aa.id AS answered_by_id, aa.kind AS answered_by_kind, aa.display_name AS answered_by_name
FROM confirmation c
JOIN ticket t ON t.id = c.ticket_id
JOIN actor ra ON ra.id = c.raised_by
LEFT JOIN actor aa ON aa.id = c.answered_by
WHERE c.id = @id;

-- AnswerConfirmation はPMの回答を記録する（8.2「答えると、関門が外れて元の主状態に戻る」）。
-- name: AnswerConfirmation :execrows
UPDATE confirmation SET status = 'answered', answer_md = @answer_md,
  answered_by = @answered_by, answered_at = now()
WHERE id = @id AND status = 'open';

-- ListRecentAnsweredConfirmations は、PM が答えた確認待ちを新しい順に返す（Delegation.md
-- 9.3「答えの本文は main のパックに載る」。pb-278）。**止められていた main が、その間に
-- 届いた答えを読む口である**——pb_wait の変化の要約は冒頭しか持たず、呼び直した後の
-- 待ち受けは止めていた間の変化を取りこぼしうる。
-- name: ListRecentAnsweredConfirmations :many
SELECT c.reason, c.question_md, c.answer_md, c.answered_at
FROM confirmation c
WHERE c.delegation_id = @delegation_id AND c.status = 'answered'
ORDER BY c.answered_at DESC
LIMIT @max_rows;

-- WithdrawOpenConfirmations は委任の取り下げで、開いている確認待ちを畳む
-- （Delegation.md 4.3「取り下げ」。pb-281）。答える人がいない問いを画面に残さない。
--
-- **畳んだ確認待ちの ID を返す。** 使い手は、それぞれの通知を同じトランザクションで
-- 取り下げる（Delegation.md 8.2、DbDesign.md 6.33.1）。
-- name: WithdrawOpenConfirmations :many
UPDATE confirmation SET status = 'withdrawn'
WHERE delegation_id = @delegation_id AND status = 'open'
RETURNING id;

-- GetConfirmationNotice は確認待ちの通知を組み立てる材料を返す（Delegation.md 8.2）。
-- **宛先は委任を作った人**（`delegation.created_by`）。遷移先は委任の根である——
-- ユーザ確認待ちの欄は根の詳細に出る（GuiDesign.md 5.5）。
-- name: GetConfirmationNotice :one
SELECT
  c.id, c.reason, c.question_md,
  d.created_by AS recipient_id, d.project_id, p.key AS project_key,
  rt.seq AS root_seq, rt.title AS root_title,
  ra.id AS raised_by_id, ra.display_name AS raised_by_name
FROM confirmation c
JOIN delegation d ON d.id = c.delegation_id
JOIN project p ON p.id = d.project_id
JOIN ticket rt ON rt.id = d.root_ticket_id
JOIN actor ra ON ra.id = c.raised_by
WHERE c.id = @id;
