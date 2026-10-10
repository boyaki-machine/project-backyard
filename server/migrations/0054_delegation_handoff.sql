-- 正本: Delegation.md 9.3・11章、DbDesign.md 6.28。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 引き継ぎノート（pb-250）。D4 で確定した5表のうち最後の1つ。これで
-- delegation / delegation_plan / evaluation / confirmation / delegation_handoff
-- がすべて揃う（pb-246〜250）。

-- +goose Up

CREATE TABLE delegation_handoff (
  id            char(26) COLLATE "C" PRIMARY KEY,
  delegation_id char(26) COLLATE "C" NOT NULL REFERENCES delegation(id) ON DELETE CASCADE,
  version       integer NOT NULL,
  body          jsonb NOT NULL,
  trigger       text NOT NULL CHECK (trigger IN (
                  'plan_version', 'evaluation', 'report',
                  'before_confirmation', 'session_end')),
  written_by    char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  written_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_delegation_handoff_version UNIQUE (delegation_id, version)
);
CREATE INDEX idx_delegation_handoff_latest ON delegation_handoff (delegation_id, version DESC);
