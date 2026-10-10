-- 正本: Delegation.md 4.3「取り下げ」、DbDesign.md 6.30。pb-281。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **取り下げた根で、もう一度自律実行を始められるようにする**（PM の判断 2026-10-06）。
-- 0050 の root_ticket_id の UNIQUE は、終了した委任があると2件目を作らせなかった。
-- 一意を「終了していない委任」に限り、終了した委任（計画・評価・確認待ちの履歴）は残す。

-- +goose Up
ALTER TABLE delegation DROP CONSTRAINT delegation_root_ticket_id_key;
CREATE UNIQUE INDEX uq_delegation_root_active
  ON delegation (root_ticket_id) WHERE status <> 'ended';
-- 終了した委任も根から引く（最新の1件）。ticket の削除の CASCADE にも要る。
CREATE INDEX ix_delegation_root_ticket ON delegation (root_ticket_id, created_at DESC);
