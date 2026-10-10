-- 正本: Delegation.md 4.2・8.3、DbDesign.md 6.31。pb-277。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **指標の値を持つ表**（PM の判断 2026-10-06）。指標の定義（何を測るか・目標）は
-- delegation.scope の metrics に置き、PM の承認を通す。値はここへ追記のみで積む。
-- 入れるのは PM（画面）と main（MCP の pb_record_metric）で、入れた人を残す——
-- main の自己申告を、要約と評価で見分けられるように。

-- +goose Up
CREATE TABLE delegation_metric_value (
  id            char(26) COLLATE "C" PRIMARY KEY,
  delegation_id char(26) COLLATE "C" NOT NULL REFERENCES delegation(id) ON DELETE CASCADE,
  metric_key    text NOT NULL,          -- scope.metrics[].key
  value         numeric NOT NULL,
  note_md       text NOT NULL DEFAULT '',
  recorded_by   char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  recorded_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_delegation_metric_value ON delegation_metric_value (delegation_id, metric_key, recorded_at);

-- **周期の確認待ち（checkpoint）は、開いているものを委任ごとに1件まで**（8.3）。
-- PB は定期実行を持たず、状態が読まれたときに周期の区切りを判定して立てる。
-- 同時に読まれても二重に立たないよう、DB で守る。
CREATE UNIQUE INDEX uq_confirmation_open_checkpoint
  ON confirmation (delegation_id) WHERE reason = 'checkpoint' AND status = 'open';
