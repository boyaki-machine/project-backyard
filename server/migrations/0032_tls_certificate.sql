-- 正本: DbDesign.md 6.15（tls_certificate）、Design.md 6.6.1（TLS 終端）、ApiDesign.md 11.4〜11.6。pb-3。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **Design.md 10.3 の第3層である。** 第2層（app_setting）とは別の表にしたのは
-- ①値が長い（PEM は数KB）②暗号化した列と復号のための付帯情報を持つ
-- ③有効期間で選ぶという固有の問い合わせがある、の3点による。
-- **キーと値の表に混ぜると、app_setting の「平文である」という前提が崩れる。**
--
-- ── 証明書は平文、秘密鍵だけ暗号化する ────────────────────
--
-- **cert_pem を隠さない。** 証明書は TLS ハンドシェイクで相手に渡すもので、
-- 隠す意味が無い。平文で持つことで、画面が発行者・期間・SAN を出すのに
-- 復号が要らなくなる。
--
-- **key_ciphertext は AES-256-GCM である。** 鍵は secret_key（第1層）で、
-- DB の外にある。**pg_dump が秘密鍵をそのまま運ばないようにするため**で、
-- 規約「秘密をコードや文書に書かない」と同じ理由による。
--
-- ── 出す証明書の選定 ────────────────────────────────────
--
-- **now が [not_before, not_after] に入る行のうち、not_before が最大のもの**
-- （Design.md 6.6.1。利用者の決定、2026-09-11）。チケットの本文は
-- 「旧証明書期限で切り替え」だったが、**その形は有効な証明書が1枚も無い窓を
-- 作りうる**——新証明書の not_before が旧証明書の not_after より後のとき。
--
-- **切り替えのための仕掛けを持たない。** crypto/tls の GetCertificate が
-- 毎ハンドシェイクで選ぶので、時刻の比較だけで切り替わる。

-- +goose Up

CREATE TABLE tls_certificate (
  id             char(26) COLLATE "C" PRIMARY KEY,

  -- 登録時に cert_pem を解析して埋める派生の値。**正本は cert_pem である。**
  -- 毎回 PEM から引き直さないのは、①選定の問い合わせを SQL で書けるようにする
  -- ②画面の一覧が復号も解析もせずに描ける、の2点による。
  common_name    text NOT NULL,
  dns_names      text[] NOT NULL DEFAULT '{}',
  not_before     timestamptz NOT NULL,
  not_after      timestamptz NOT NULL,
  serial_number  text NOT NULL,
  fingerprint    text NOT NULL,

  -- 画面に「自己署名」と出すためだけに持つ。**振る舞いを変えない**——
  -- PB は検証の連鎖を辿らない（それをするのは接続するクライアントである）。
  is_self_signed boolean NOT NULL,

  cert_pem       text NOT NULL,

  key_ciphertext bytea NOT NULL,
  key_nonce      bytea NOT NULL,
  -- どの鍵で暗号化したかを識別する。**鍵を交換する日に、どの行がまだ古い鍵かを
  -- 引けるようにするため**である（交換の手順は pb-3 では作らない）。
  key_id         text NOT NULL,

  uploaded_by    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT tls_certificate_period CHECK (not_before < not_after),

  -- **同じ証明書を2回登録できないようにする。** 同一の指紋が2枚あると
  -- 「どちらを出したか」が not_before では決まらない（同じ値になる）。
  CONSTRAINT tls_certificate_fingerprint_unique UNIQUE (fingerprint)
);

CREATE INDEX tls_certificate_period_idx ON tls_certificate (not_before DESC, not_after);

CREATE TRIGGER tls_certificate_touch BEFORE UPDATE ON tls_certificate
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE tls_certificate IS
  'TLS 証明書（Design.md 10.3 の第3層）。秘密鍵は secret_key で暗号化して持つ';
COMMENT ON COLUMN tls_certificate.cert_pem IS
  '平文。証明書はハンドシェイクで相手に渡すものなので隠さない';
