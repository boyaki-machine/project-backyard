-- 正本: Delegation.md 4.1、DbDesign.md 6.24。pb-282。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **エピックに属するチケットを、自律実行の根にできるようにする**（PM の判断、案A）。
-- エピックへの所属は parent_id がエピックを指す形で持つ（GuiDesign.md 5.5「エピック欄」）。
-- 0050 の ck_ticket_delegated_no_parent は「根は親を持たない」を求めており、エピックの
-- 直下のチケットを根にできなかった。
--
-- **「親が無いか、親がエピック」は CHECK では表せない**（他の行を参照できない）。
-- 制約を外し、判定はサーバが行う（委任の作成と、根の親の変更。delegationRootParentProblem）。

-- +goose Up
ALTER TABLE ticket DROP CONSTRAINT ck_ticket_delegated_no_parent;
