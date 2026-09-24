-- 正本: DbDesign.md 8章の冒頭（マイグレーションの一覧）。表のコメントだけを直す（DDLなし）。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **表のコメントを、いまの仕様の言葉で書き直す。** 表のコメントは sqlc が生成する
-- store/gen/models.go の型の説明にもなる。0022・0034・0035 のコメントには開発の時期
-- （Phase）やチケット番号が入っていたが、適用済みのファイルは直さない（DbDesign.md 5.3）
-- ので、ここで上書きする。

-- +goose Up

COMMENT ON TABLE agent_run IS
  'エージェントの実行記録。pb_submit_result が1提出につき1行作る。DbDesign.md 8.2.4';
COMMENT ON TABLE context_pack_log IS
  'コンテキストパックの生成記録。書き手はまだ無い（器のみ）。DbDesign.md 8.2.5';
COMMENT ON TABLE pending_setting_change IS
  '未確認の設定変更。期限内に確認されなければ previous へ戻す';
COMMENT ON TABLE user_mfa_credential IS '第2要素の認証器（DbDesign.md 6.18）。種別は TOTP のみ';
