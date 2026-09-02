-- 正本: DbDesign.md 8.2.1.1（agent_client_kind）、ApiDesign.md 4.5.7
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **クライアント種別を CHECK から参照テーブルへ移す**（利用者の判断、2026-09-02——
-- 「業界は流動的で今後増える可能性も存分にある」）。0019 は
-- CHECK (client_kind IN ('claude_code','copilot','other')) だった。
--
-- 得るものは3つ。①**増やすのが行の追加になる**（CHECK だと毎回 DDL のマイグレーション）
-- ②**表示名がDBに来る**ので、画面が対応表を持たなくてよくなる——GET /roles が
-- display_name を返すようになった時点で lib/roles.ts を廃止したのと同じ形
-- （GuiDesign.md 5.6）③**値域はDBが守る**（FK なので、アプリの検証を抜けた値は落ちる）。
--
-- **値が決めるのは「設定ファイルの置き場」であって、エディタではない。**
-- 同じ VS Code でも Claude 拡張なら .mcp.json + .claude/commands/、
-- GitHub Copilot なら .vscode/mcp.json + .github/prompts/ になる（Requirements.md 10.8）。
-- **2026年に「エージェント」と「エディタ」が1対1でなくなった**（ACP により
-- Claude Code / Codex / Gemini CLI が Zed・JetBrains・Neovim の中で動く）ため、
-- 軸をエディタに取ると値域が定まらない。
--
-- **key は事業者名ではなく製品名にする。** 1つの事業者が複数のクライアントを
-- 出しうるためで、値が表すのは事業者ではなく置き場である。
--
-- **claude_code と copilot は 0019 からの綴りを変えない**（既存の行がある）。
--
-- **「PB が接続手順を提供できるか」の列は持たない。** 配置ファイルの生成は手順28
-- （Requirements.md 10.9.1 系統A）であり、使うものが無いうちに入口を作ると意味が固まる
-- ——trust_level を 0019 で受け取らなかったのと同じ判断である。

-- +goose Up

CREATE TABLE agent_client_kind (
  key          text PRIMARY KEY,
  display_name text    NOT NULL,
  sort_order   integer NOT NULL
);

-- 主要な商用AI提供事業者系に絞り、それ以外は other にまとめる（利用者の判断、2026-09-02）。
-- OSS のエージェント（Cline / Goose / OpenCode / OpenHands / Aider / Continue 等）は
-- other に入る。**手順28 で個別のテンプレートを書いたものから行として独立させていく。**
INSERT INTO agent_client_kind (key, display_name, sort_order) VALUES
  ('claude_code', 'Claude Code',                10),  -- Anthropic
  ('codex',       'OpenAI Codex',               20),  -- OpenAI
  ('copilot',     'GitHub Copilot',             30),  -- Microsoft
  ('gemini',      'Gemini（CLI / Code Assist）', 40),  -- Google
  ('other',       'その他・OSS 等',              90);

-- **CHECK を外して FK に置き換える。** 制約名は 0019 が明示していないので、
-- PostgreSQL が付けた既定の名前（<表>_<列>_check）を使う。
ALTER TABLE agent DROP CONSTRAINT agent_client_kind_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_client_kind_fkey
  FOREIGN KEY (client_kind) REFERENCES agent_client_kind(key);
