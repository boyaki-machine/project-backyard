-- 正本: DbDesign.md 6.2（アクターと認証）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。

-- +goose Up

-- 行為主体（人間・エージェント・システム）の統一表現
CREATE TABLE actor (
  id            char(26) COLLATE "C" PRIMARY KEY,
  kind          text        NOT NULL CHECK (kind IN ('user','agent','system')),
  display_name  text        NOT NULL CHECK (length(display_name) BETWEEN 1 AND 60),
  avatar_url    text,
  is_active     boolean     NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_actor_kind ON actor (kind) WHERE is_active;
CREATE TRIGGER trg_actor_updated BEFORE UPDATE ON actor
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 人間ユーザー
CREATE TABLE app_user (
  actor_id          char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  email             citext      NOT NULL UNIQUE,
  is_email_verified boolean     NOT NULL DEFAULT false,
  system_role       text        NOT NULL DEFAULT 'operator'
                                CHECK (system_role IN ('operator','administrator')),
  locale            text        NOT NULL DEFAULT 'ja',
  timezone          text        NOT NULL DEFAULT 'Asia/Tokyo',
  theme             text        NOT NULL DEFAULT 'system'
                                CHECK (theme IN ('light','dark','system')),
  hue               text        NOT NULL DEFAULT 'blue'
                                CHECK (hue IN ('blue','green')),
  last_login_at     timestamptz,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_app_user_role ON app_user (system_role);
CREATE TRIGGER trg_app_user_updated BEFORE UPDATE ON app_user
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 認証プロバイダ定義（Phase 1 は 'local' のみ）
CREATE TABLE auth_provider (
  key                  text        PRIMARY KEY,
  type                 text        NOT NULL CHECK (type IN ('local','oidc','saml')),
  display_name         text        NOT NULL,
  is_enabled           boolean     NOT NULL DEFAULT true,
  sort_order           integer     NOT NULL DEFAULT 0,
  config               jsonb       NOT NULL DEFAULT '{}'::jsonb,
  secret_ref           text,
  is_jit_provisioning  boolean     NOT NULL DEFAULT false,
  default_system_role  text        NOT NULL DEFAULT 'operator',
  role_mapping         jsonb       NOT NULL DEFAULT '{}'::jsonb,
  allowed_domains      jsonb       NOT NULL DEFAULT '[]'::jsonb,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_auth_provider_updated BEFORE UPDATE ON auth_provider
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ユーザーと認証手段の結合（将来のOIDC/SAML対応の要）
CREATE TABLE user_identity (
  id           char(26) COLLATE "C" PRIMARY KEY,
  user_id      char(26) COLLATE "C" NOT NULL REFERENCES app_user(actor_id) ON DELETE CASCADE,
  provider_key text        NOT NULL REFERENCES auth_provider(key) ON DELETE RESTRICT,
  subject      text        NOT NULL,
  attributes   jsonb       NOT NULL DEFAULT '{}'::jsonb,
  linked_at    timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  CONSTRAINT uq_user_identity_provider_subject UNIQUE (provider_key, subject)
);
CREATE INDEX idx_user_identity_user ON user_identity (user_id);

-- ローカルID/PW認証の資格情報
CREATE TABLE local_credential (
  identity_id         char(26) COLLATE "C" PRIMARY KEY
                      REFERENCES user_identity(id) ON DELETE CASCADE,
  password_hash       text        NOT NULL,   -- Argon2id の PHC 文字列
  password_updated_at timestamptz NOT NULL DEFAULT now(),
  must_change         boolean     NOT NULL DEFAULT false,
  failed_attempts     integer     NOT NULL DEFAULT 0,
  locked_until        timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_local_credential_updated BEFORE UPDATE ON local_credential
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- セッション・APIトークン・エージェントトークンの統一表現
CREATE TABLE access_token (
  id           char(26) COLLATE "C" PRIMARY KEY,
  actor_id     char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE CASCADE,
  token_type   text        NOT NULL CHECK (token_type IN ('session','api','agent')),
  token_hash   text        NOT NULL UNIQUE,     -- SHA-256。平文は保存しない
  token_prefix text,                            -- 一覧表示用の先頭8文字
  name         text,
  project_id   char(26) COLLATE "C",            -- NULL = 全プロジェクト
  scopes       jsonb       NOT NULL DEFAULT '[]'::jsonb,
  issued_at    timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz,
  last_used_at timestamptz,
  revoked_at   timestamptz,
  client_info  text
);
CREATE INDEX idx_access_token_actor  ON access_token (actor_id);
CREATE INDEX idx_access_token_active ON access_token (expires_at)
  WHERE revoked_at IS NULL;
