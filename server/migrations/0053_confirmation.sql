-- 正本: Delegation.md 8章・12.2、DbDesign.md 6.27。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 確認待ち（pb-249）。D4 で確定した5表のうち、残る delegation_handoff は
-- 別チケット（pb-250）で足す。

-- +goose Up

CREATE TABLE confirmation (
  id            char(26) COLLATE "C" PRIMARY KEY,
  delegation_id char(26) COLLATE "C" NOT NULL REFERENCES delegation(id) ON DELETE CASCADE,
  ticket_id     char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  reason        text NOT NULL CHECK (reason IN (
                  'scope_change', 'irreversible_external', 'charter_change',
                  'sensitive_area', 'budget_exceeded', 'evaluation_unable',
                  'checkpoint', 'hearing')),
  question_md   text NOT NULL,
  options       jsonb NOT NULL DEFAULT '[]'::jsonb,
  raised_by     char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered','withdrawn')),
  answer_md     text,
  answered_by   char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  answered_at   timestamptz
);
CREATE INDEX idx_confirmation_delegation_open ON confirmation (delegation_id) WHERE status = 'open';
