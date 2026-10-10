-- 正本: Delegation.md 6章・12.2、DbDesign.md 6.25。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 計画の版（pb-247）。D3：骨格（根の直下の枝・予算配分・チェックポイント）
-- だけを版で持つ。骨格より下の木は main の裁量で、版を出さずに直してよい。

-- +goose Up

CREATE TABLE delegation_plan (
  id             char(26) COLLATE "C" PRIMARY KEY,
  delegation_id  char(26) COLLATE "C" NOT NULL REFERENCES delegation(id) ON DELETE CASCADE,
  version        integer NOT NULL,
  scope          jsonb NOT NULL,         -- この版が前提とする委任範囲
  skeleton       jsonb NOT NULL,         -- 骨格：根の直下の枝・予算配分・チェックポイント
  body_md        text NOT NULL,          -- 計画の説明
  reason_md      text NOT NULL,          -- 前の版から変えた理由
  declared_class text CHECK (declared_class IN ('minor','structural','scope')),
  measured_class text CHECK (measured_class IN ('minor','structural','scope')),
  status         text NOT NULL DEFAULT 'proposed'
                 CHECK (status IN ('proposed','accepted','rejected','superseded','reverted')),
  reverts_to     integer,                -- 差し戻しで作った版なら、写した元の版
  submitted_by   char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  submitted_at   timestamptz NOT NULL DEFAULT now(),
  decided_by     char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  decided_at     timestamptz,
  CONSTRAINT uq_delegation_plan_version UNIQUE (delegation_id, version)
);
CREATE INDEX idx_delegation_plan_delegation ON delegation_plan (delegation_id, version DESC);
