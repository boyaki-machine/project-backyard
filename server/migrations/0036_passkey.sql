-- 正本: DbDesign.md 6.19（パスキーの2表）、Design.md 6.8。pb-104。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **パスキーは app_user に吊る。user_identity にも user_mfa_credential にも吊らない**
-- （Design.md 6.8.4）。前者の列の多くは IdP のためにあってパスキーでは使われず、
-- 後者に置くとログインの分岐（6.7.4 の手順2）がパスキーを第2要素として数えてしまう。
--
-- **挑戦を access_token に置かない**のは 0035 と同じ理由である。

-- +goose Up

CREATE TABLE user_passkey (
  id                 char(26) COLLATE "C" PRIMARY KEY,
  user_id            char(26) COLLATE "C" NOT NULL
                     REFERENCES app_user(actor_id) ON DELETE CASCADE,
  name               text        NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  -- credential_id は認証器が決める。**ログインのときは誰のパスキーかが分からない**ので、
  -- 利用者をまたいで一意でなければ引けない。
  credential_id      bytea       NOT NULL UNIQUE,
  -- public_key は COSE_Key のまま。**公開鍵なので封じない**——読めても署名は作れない。
  public_key         bytea       NOT NULL,
  -- rp_id は登録したときのホスト名（Design.md 6.8.3）。RP ID を設定にする日に、
  -- どのパスキーが使えなくなるかを数えるために残す。
  rp_id              text        NOT NULL,
  attestation_type   text        NOT NULL,
  attestation_format text        NOT NULL,
  aaguid             bytea,
  attachment         text,
  transports         jsonb       NOT NULL DEFAULT '[]'::jsonb,
  sign_count         bigint      NOT NULL DEFAULT 0,
  -- user_verified は WebAuthn の uvInitialized。**一度真になったら戻らない。**
  user_verified      boolean     NOT NULL,
  -- backup_eligible は**変わってはならない**。ログインのたびに今回の値と比べる。
  backup_eligible    boolean     NOT NULL,
  backup_state       boolean     NOT NULL,
  last_used_at       timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
-- 登録の途中の行が無いので、部分条件を付けない（6.18 との違い）。
-- user_id の先頭一致で一覧と件数にも使う。
CREATE UNIQUE INDEX uq_user_passkey_name ON user_passkey (user_id, name);
CREATE TRIGGER trg_user_passkey_updated BEFORE UPDATE ON user_passkey
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE webauthn_challenge (
  id          char(26) COLLATE "C" PRIMARY KEY,
  purpose     text        NOT NULL CHECK (purpose IN ('register', 'login')),
  user_id     char(26) COLLATE "C"
              REFERENCES app_user(actor_id) ON DELETE CASCADE,
  -- challenge は base64url の平文。**資格情報ではない**——知っていても秘密鍵が無ければ
  -- ログインできない。clientDataJSON に入っている値でそのまま引く。
  challenge   text        NOT NULL UNIQUE,
  -- session は go-webauthn の SessionData。ライブラリを上げると形が変わりうるが、
  -- 5分で失効するので流し切ってよい（Design.md 6.8.5）。
  session     jsonb       NOT NULL,
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  -- **登録の挑戦には利用者が必ずあり、ログインの挑戦には必ず無い。**
  CONSTRAINT ck_webauthn_challenge_user CHECK ((purpose = 'register') = (user_id IS NOT NULL))
);
CREATE INDEX idx_webauthn_challenge_user ON webauthn_challenge (user_id)
  WHERE user_id IS NOT NULL;
-- 期限切れは、次の挑戦を作るときに消す（専用のバッチを持たない）。
CREATE INDEX idx_webauthn_challenge_expires ON webauthn_challenge (expires_at);

COMMENT ON TABLE user_passkey IS 'パスワードの代わりにログインする鍵（DbDesign.md 6.19）';
COMMENT ON TABLE webauthn_challenge IS 'WebAuthn の登録とログインの途中状態（DbDesign.md 6.19）';
