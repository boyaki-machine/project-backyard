-- 正本: Delegation.md 7章・12.2、DbDesign.md 6.26。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 評価エージェントによる評価記録（pb-248）。D4 で確定した5表のうち、
-- 残る confirmation / delegation_handoff は別チケット（pb-249・pb-250）で足す。

-- +goose Up

CREATE TABLE evaluation (
  id                     char(26) COLLATE "C" PRIMARY KEY,
  delegation_id          char(26) COLLATE "C" NOT NULL REFERENCES delegation(id) ON DELETE CASCADE,
  target_kind            text NOT NULL CHECK (target_kind IN ('plan','deliverable')),
  plan_id                char(26) COLLATE "C" REFERENCES delegation_plan(id) ON DELETE CASCADE,
  agent_report_id        char(26) COLLATE "C" REFERENCES agent_report(id) ON DELETE CASCADE,
  round                  integer NOT NULL,
  verdict                text NOT NULL CHECK (verdict IN ('pass','fail','unable')),
  findings               jsonb NOT NULL,
  class_check            text CHECK (class_check IN ('agree','stricter','looser')),
  evaluator_actor_id     char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  evaluator_client_kind  text,
  evaluator_model_name   text,
  evaluator_model_version text,
  main_client_kind       text,
  main_model_name        text,
  main_model_version     text,
  tokens_used            bigint,
  objected_at            timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_evaluation_target CHECK (
    (target_kind = 'plan' AND plan_id IS NOT NULL AND agent_report_id IS NULL)
    OR (target_kind = 'deliverable' AND agent_report_id IS NOT NULL AND plan_id IS NULL))
);
CREATE INDEX idx_evaluation_delegation ON evaluation (delegation_id, target_kind, created_at DESC);
CREATE INDEX idx_evaluation_plan ON evaluation (plan_id) WHERE plan_id IS NOT NULL;
CREATE INDEX idx_evaluation_report ON evaluation (agent_report_id) WHERE agent_report_id IS NOT NULL;
