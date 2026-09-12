-- 正本: DbDesign.md 6.17（pending_setting_change）、Design.md 10.3。pb-97。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **締め出されうる設定を変えたときの「戻し方」を持つ。** 期限内に
-- 「アクセスできました」が押されなければ、previous の値へ戻す。
--
-- **プロセス内に持たないのは、締め出された人の行動で壊れるからである。**
-- 画面へ入れなくなった人がまず試すのは再起動で、そのとき記録が消えると
-- 未確認のまま確定してしまう——戻り道を消すのが復旧の試み自身になる。

-- +goose Up

CREATE TABLE pending_setting_change (
  id          char(26) COLLATE "C" PRIMARY KEY,
  -- previous はキーから値への対応。**null は「行が無かった」**を表し、
  -- 戻すときは app_setting の行を消す（6.14 の「既定に戻す」と同じ）。
  previous    jsonb NOT NULL,
  expires_at  timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL
);

-- 期限の古い順に引く。**行は0か1つだが、点検は常に「期限が来たもの」を探す。**
CREATE INDEX pending_setting_change_expires_at_idx
  ON pending_setting_change (expires_at);

COMMENT ON TABLE pending_setting_change IS
  '未確認の設定変更。期限内に確認されなければ previous へ戻す（pb-97）';
