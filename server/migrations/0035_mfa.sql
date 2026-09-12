-- 正本: DbDesign.md 6.18（多要素認証の3表）、Design.md 6.7。pb-103。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **第2要素は app_user に吊る。user_identity には吊らない。** MFA は「誰である
-- かを特定する手段」ではなく、特定できたあとに重ねる関門である（Design.md 6.7.1）。
--
-- **挑戦を access_token に置かない。** 認証ミドルウェアは token_hash で引いた行を
-- token_type で絞らずアクターを載せる（6.2 の FindAccessTokenByHash）。access_token
-- に中間状態を置けば、挑戦トークンがそのまま API 全体を通る資格情報になる。

-- +goose Up

CREATE TABLE user_mfa_credential (
  id              char(26) COLLATE "C" PRIMARY KEY,
  user_id         char(26) COLLATE "C" NOT NULL
                  REFERENCES app_user(actor_id) ON DELETE CASCADE,
  kind            text        NOT NULL CHECK (kind IN ('totp')),
  name            text        NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  -- secret は AES-256-GCM で封じた Base32 の共有秘密（Design.md 6.7.3）。
  -- **ハッシュではなく可逆の暗号である**——検証のために平文が必要だからで、
  -- 鍵は tls_certificate の秘密鍵と同じ app_secret の1本を使う。
  secret          bytea       NOT NULL,
  secret_nonce    bytea       NOT NULL,
  -- confirmed_at が NULL の行は登録の途中であり、**認証の要素として数えない。**
  -- 照合しないまま確定させると、利用者が自分を締め出せる。
  confirmed_at    timestamptz,
  -- last_used_step は照合が通った刻みの番号（floor(unixtime / 30)）。
  -- **同じ刻みのコードを2回受け付けない**という規則が、1回の比較で書ける。
  last_used_step  bigint,
  last_used_at    timestamptz,
  failed_attempts integer     NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
-- **索引に部分条件を付けて、未確定の行を数と一意性の両方から外す。**
CREATE INDEX idx_user_mfa_credential_user ON user_mfa_credential (user_id)
  WHERE confirmed_at IS NOT NULL;
-- 名前の一意は確定済みだけに掛ける。**途中の行が名前を占有しない**ので、
-- 「iPhone」で登録に失敗した人がもう一度「iPhone」で始められる。
CREATE UNIQUE INDEX uq_user_mfa_credential_name ON user_mfa_credential (user_id, name)
  WHERE confirmed_at IS NOT NULL;
CREATE TRIGGER trg_user_mfa_credential_updated BEFORE UPDATE ON user_mfa_credential
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE mfa_login_challenge (
  id          char(26) COLLATE "C" PRIMARY KEY,
  user_id     char(26) COLLATE "C" NOT NULL
              REFERENCES app_user(actor_id) ON DELETE CASCADE,
  -- token_hash は SHA-256。平文は応答にしか出さない（access_token と同じ作法）。
  token_hash  text        NOT NULL UNIQUE,
  attempts    integer     NOT NULL DEFAULT 0,
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_mfa_login_challenge_user ON mfa_login_challenge (user_id);

CREATE TABLE mfa_recovery_code (
  id         char(26) COLLATE "C" PRIMARY KEY,
  user_id    char(26) COLLATE "C" NOT NULL
             REFERENCES app_user(actor_id) ON DELETE CASCADE,
  -- code_hash は SHA-256。**Argon2id を使わないのは、コードが利用者の記憶に
  -- 由来しないためである**（50ビットの乱数に辞書攻撃は効かない。Design.md 6.7.5）。
  code_hash  text        NOT NULL UNIQUE,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
-- 残数は used_at IS NULL の件数である。**本数を列に持たない。**
CREATE INDEX idx_mfa_recovery_code_user ON mfa_recovery_code (user_id)
  WHERE used_at IS NULL;

COMMENT ON TABLE user_mfa_credential IS '第2要素の認証器（DbDesign.md 6.18）。Phase 2 は TOTP のみ';
COMMENT ON TABLE mfa_login_challenge IS 'パスワードは通ったが第2要素がまだ、という中途状態（Design.md 6.7.4）';
COMMENT ON TABLE mfa_recovery_code IS '認証器を失ったときのリカバリコード（Design.md 6.7.5）';
