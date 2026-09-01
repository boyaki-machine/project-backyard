-- 正本: DbDesign.md 8.2（エージェント連携）、Design.md 6.5
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- Phase 2 の2つ目のスキーマ変更である。手順25 の MCP がつながるための
-- 資格情報の器を作る（Design.md 11章 手順24）。
--
-- **エージェントは人に紐づく**（owner_actor_id）。利用者の判断（2026-08-30）——
-- 現状のAIエージェントは人の支援を行う形態であるため、プロジェクトメンバの
-- 誰かに紐づけて登録する。その人の持つ権限がベースになり、そこにエージェント
-- 独自の権限を整理して割り当てる。人に紐づかず動く形態（PJ予算でエージェントに
-- 働いてもらう等）になったら、NOT NULL を外して NULL = 自立エージェントと
-- 定義する。**いま NOT NULL から始めるのは順序の問題**で、制約を外すのは
-- 1行だが、後から付けるには全行の埋め戻しが要る。
--
-- **権限は所有者から導く（委譲）。** エージェントは app_user の行を持たないため、
-- Design.md 6.4.1 の式のうちシステムロールの層が必ず空になる。owner_actor_id が
-- 指す人のロールを両層に用いる。**エージェントに project_member の行は作らない**
-- ——作ると所有者のロールと二重に持ち、片方だけ古くなる。
--
-- **task_lease は手順26（pb_claim_task）まで使わない。** それでも同じ
-- マイグレーションに入れるのは DbDesign.md 8.2 の採番表がそう定めているため。
-- make sqlc は migrations/ をスキーマ源に読むので、先に置いても害はない。
--
-- **API は 4.5、画面は手順24b。** 本ファイルは器だけを作る。

-- +goose Up

-- ── 8.2.1 エージェントの登録 ────────────────────────────────
--
-- 1行が表すのは「ある参加者の手元で動くクライアント1つ」である。人ではない。
-- 同じ人が Claude Code と VS Code を使えば2行になり、2つのプロジェクトに
-- つなぐならさらに分かれる（Requirements.md 10.10.3）。
--
-- owner_actor_id が app_user を参照するのは、所有者が人間に限られるため。
-- エージェントがエージェントを所有することはない。

CREATE TABLE agent (
  actor_id       char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  owner_actor_id char(26) COLLATE "C" NOT NULL
                 REFERENCES app_user(actor_id) ON DELETE CASCADE,
  project_id     char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  client_kind    text    NOT NULL CHECK (client_kind IN ('claude_code','copilot','other')),
  model_name     text,
  model_version  text,
  capabilities   jsonb   NOT NULL DEFAULT '[]'::jsonb,
  trust_level    integer NOT NULL DEFAULT 1 CHECK (trust_level BETWEEN 0 AND 3),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- 「自分のエージェント一覧」（ApiDesign.md 4.5.1）がこの索引で引く。
CREATE INDEX idx_agent_owner ON agent (owner_actor_id);

CREATE TRIGGER trg_agent_updated BEFORE UPDATE ON agent
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── 8.2.2 リース管理 ────────────────────────────────────────
--
-- 部分一意インデックスで「1チケットに有効なリースは1つ」をDBレベルで保証する。
-- アプリ側の排他制御に依存しないため、エージェントが並行して claim しても
-- 破綻しない。

CREATE TABLE task_lease (
  id             char(26) COLLATE "C" PRIMARY KEY,
  ticket_id      char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  actor_id       char(26) COLLATE "C" NOT NULL REFERENCES actor(id)  ON DELETE CASCADE,
  lease_token    text        NOT NULL UNIQUE,
  acquired_at    timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  heartbeat_at   timestamptz NOT NULL DEFAULT now(),
  released_at    timestamptz,
  release_reason text CHECK (release_reason IN ('completed','abandoned','expired','manual'))
);
CREATE UNIQUE INDEX uq_task_lease_active ON task_lease (ticket_id) WHERE released_at IS NULL;
CREATE INDEX idx_task_lease_expiry ON task_lease (expires_at) WHERE released_at IS NULL;

-- ── 8.2.6 権限 ──────────────────────────────────────────────
--
-- **新しい権限キーは足さない。** agent.register / agent.token.issue / agent.run は
-- 0010 の28件に既にある（7.2）。変えるのは割り当てのほうである。
--
-- agent.run の意味を「自分に紐づくエージェントを MCP から走らせてよい」と定め、
-- プロジェクトに参加する側のロールへ配り直す。委譲により、エージェントの権限は
-- 所有者から導かれる（8.2.1）ので、agent.run を持つのが project_admin と
-- administrator だけのままだと、**プロジェクト管理者のエージェントしか
-- MCP を使えない**。
--
-- project_viewer にも与えるのは、Requirements.md 10.9.1 の系統B が発行の用途に
-- 「実装用=write可 / 閲覧用=read only」を挙げているため。何を読み書きできるかは
-- agent.run ではなく、トークンのスコープと個々のツールの必要権限（Design.md 8.2）
-- が決める。
--
-- **当面このキーは誰も拒まない。** app_user.system_role は operator か
-- administrator のいずれかなので（6.2 の CHECK）、operator に与えた時点で
-- 全利用者が持つ。実際に効き始めるのは Design.md 付録A 論点②（operator の
-- 持ち物を減らす）を片付けてからで、それまでは「所有者が持つべき権限」を
-- 表明しているだけである。
--
-- agent.register / agent.token.issue の割り当ては変えない。登録とトークン発行は
-- 本人の操作（ApiDesign.md 4.5）で権限キーを要求せず、この2つは**他人の
-- エージェントを管理する側**の権限として project_admin に残る。

INSERT INTO role_permission (role_key, permission_key)
SELECT r.key, 'agent.run'
  FROM role r
 WHERE r.key IN ('operator','project_member','project_viewer')
ON CONFLICT DO NOTHING;
