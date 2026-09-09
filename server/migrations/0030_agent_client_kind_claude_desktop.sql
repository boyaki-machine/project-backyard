-- 正本: DbDesign.md 8.2.1.1（agent_client_kind）、ApiDesign.md 4.5.7
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **クライアント種別に claude_desktop を足す**（pb-58）。0020 が予告した
-- 「増やすのが行の追加になる」の1件目である——DDL を伴わない。
--
-- **Claude Code とは別の種別である。** 種別が決めるのは設定ファイルの置き場で
-- （8.2.1.1）、Claude Code は作業フォルダ直下の .mcp.json、Claude Desktop は
-- ~/Library/Application Support/Claude/claude_desktop_config.json か設定のコネクタ
-- である。**claude_code を選ぶと .mcp.json を渡されるが、Desktop はそれを読まない。**
--
-- **has_setup_template は false。** 系統A（リポジトリにコミットする配置ファイル）は
-- 手順・常時コンテキストを作業フォルダへ置くものだが、**Desktop には作業フォルダが
-- 無い**ので置き場そのものが存在しない（Requirements.md 10.9.1「MCP 型では系統A に
-- 置き場が無い」）。**書いていないテンプレートを「持っている」と名乗らない**という
-- 0023 の判断をそのまま踏む。参画は「PB に参画して」の一文で足りる（10.8.2 の Codex と同じ形）。
--
-- **sort_order は 15。** 10（claude_code）と 20（codex）の間で、同じ事業者の製品が隣に並ぶ。
-- **既存の行の値を動かさない**——並びを直すためだけに UPDATE を打つと、
-- 適用済みの行を書き換えることになる。
--
-- **key は事業者名ではなく製品名**（0020 の規約）。claude_code との対で読める綴りにする。
--
-- **ON CONFLICT DO NOTHING で冪等にする**（0024 と同じ）。

-- +goose Up

INSERT INTO agent_client_kind (key, display_name, sort_order, has_setup_template) VALUES
  ('claude_desktop', 'Claude Desktop', 15, false)
ON CONFLICT DO NOTHING;
