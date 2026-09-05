-- 正本: DbDesign.md 8.2.1「token_env_suffix」、8.2.1.1「has_setup_template」
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- 手順28a（配置ファイルの生成）が要求する2列を足す。
--
-- ── agent.token_env_suffix ────────────────────────────────────
--
-- 接続設定ファイル（.mcp.json / .codex/config.toml）が読む環境変数の名前を、
-- 本人が決める（Requirements.md 10.8.3、利用者の判断 2026-09-06）。
--
--   token_env_suffix = 'MY_LAPTOP'  →  PB_TOKEN_MY_LAPTOP
--
-- **格納するのは接尾だけで、PB_TOKEN_ の接頭はアプリが付ける。** 接頭を持たせないのは
-- PATH や HOME を作れないようにするため。CHECK が文字種を環境変数名に限る。
--
-- **固定名（PB_TOKEN）では足りない。** 環境変数は ~/.zshrc の平らな名前空間に同居する
-- ので、同じ端末で2つ以上のエージェントを使うと衝突する。**衝突の症状は 404 not_found**
-- （トークンのプロジェクトと URL のプロジェクトの食い違い。Design.md 8.3）で、利用者から
-- 原因が見分けられない。
--
-- **導出にしない。** プロジェクトキーからでは端末を区別できず（エージェントが増える主な軸は
-- 端末で、ノートPCとデスクトップは同じプロジェクト・同じ種別になる）、表示名からでは一意に
-- ならず（キーは4つ組）、日本語が通るので変数名を作れないことがあり、しかも改名できる。
--
-- **NULL を許すのは、この時点で既に登録済みの行があるため。** 埋め戻しに使える決定的な規則が
-- 存在しない（日本語の表示名から環境変数名を作れない）。NULL の行はアプリ側で
-- PB_TOKEN_<エージェントの id> にフォールバックする（ApiDesign.md 4.5.1 の token_env_name）。
--
-- **一意は（所有者・接尾）。** 環境変数は端末ごとの名前空間なので他人と重なってよい。
-- 同じ人の中で重なると ~/.zshrc の1行が2つのエージェントに解釈されて事故になる。
-- NULL が複数あってよいので部分インデックスにする。
--
-- ── agent_client_kind.has_setup_template ──────────────────────
--
-- PB がそのクライアント向けの配置ファイルを出せるか（Requirements.md 10.8.2）。
-- 持たない種別をセットアップ画面の選択肢に出すと、選んだ先に何も出ない。
--
-- **true にするのは3種別だけ。** 10.8.2 が本文を定義しているものに限る——書いていない
-- テンプレートを「持っている」と名乗らない。gemini と other は行として在るが false。
--
-- 0020 が「列は持たない。28 でテンプレートを書くときに足す」と予告したもの。

-- +goose Up

ALTER TABLE agent
  ADD COLUMN token_env_suffix text
    CHECK (token_env_suffix IS NULL
        OR token_env_suffix ~ '^[A-Z][A-Z0-9_]{0,40}$');

COMMENT ON COLUMN agent.token_env_suffix IS
  'トークンを載せる環境変数の接尾。PB_TOKEN_ を付けた名前を接続設定が読む。DbDesign.md 8.2.1';

CREATE UNIQUE INDEX uq_agent_env_suffix
  ON agent (owner_actor_id, token_env_suffix)
  WHERE token_env_suffix IS NOT NULL;

ALTER TABLE agent_client_kind
  ADD COLUMN has_setup_template boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN agent_client_kind.has_setup_template IS
  'PB がこの種別の配置ファイルを出せるか。DbDesign.md 8.2.1.1';

UPDATE agent_client_kind
   SET has_setup_template = true
 WHERE key IN ('claude_code', 'copilot', 'codex');
