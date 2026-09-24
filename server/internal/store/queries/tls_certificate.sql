-- TLS 証明書のクエリ（DbDesign.md 6.15、ApiDesign.md 11.4〜11.6）。
--
-- **Design.md 10.3 の第3層である。** 秘密鍵は secret_key で暗号化されて入っており、
-- **復号はアプリ側（internal/tlscert）で行う。** DB は暗号文を運ぶだけである。
--
-- **プロジェクトで閉じていない。** 証明書はサーバ全体のもので、必要権限は
-- system.settings（アドミニストレータ）。

-- 一覧。**秘密鍵の暗号文も返す**——起動時と登録・削除のあとに、出す証明書を
-- 選び直して復号するために要る（Design.md 6.6.1）。
-- 画面向けの応答では鍵を落とす（ApiDesign.md 11.4 は private_key を返さない）。
--
-- not_before の降順にするのは、画面が新しいものから並べるためである。
-- name: ListTLSCertificates :many
SELECT
  c.id,
  c.common_name,
  c.dns_names,
  c.not_before,
  c.not_after,
  c.serial_number,
  c.fingerprint,
  c.is_self_signed,
  c.cert_pem,
  c.key_ciphertext,
  c.key_nonce,
  c.key_id,
  c.created_at,
  c.uploaded_by,
  a.kind AS uploaded_by_kind,
  a.display_name AS uploaded_by_display_name
FROM tls_certificate c
LEFT JOIN actor a ON a.id = c.uploaded_by
ORDER BY c.not_before DESC;

-- 1件を作る。**指紋の一意制約に当たると誤りが返る**ので、
-- 呼び出し側が 409 へ写す（ApiDesign.md 11.5）。
-- name: CreateTLSCertificate :one
INSERT INTO tls_certificate (
  id, common_name, dns_names, not_before, not_after, serial_number,
  fingerprint, is_self_signed, cert_pem, key_ciphertext, key_nonce, key_id, uploaded_by
) VALUES (
  @id, @common_name, @dns_names, @not_before, @not_after, @serial_number,
  @fingerprint, @is_self_signed, @cert_pem, @key_ciphertext, @key_nonce, @key_id, @uploaded_by
)
RETURNING id, created_at;

-- 1件を消す。**消した件数を返す**ので、0 なら 404 にできる。
-- name: DeleteTLSCertificate :execrows
DELETE FROM tls_certificate WHERE id = @id;

-- 1件を引く。削除の前に「存在するか」と「消したら有効なものが残るか」を
-- 判定するために使う（ApiDesign.md 11.6）。
-- name: GetTLSCertificate :one
SELECT id, common_name, not_before, not_after, fingerprint
FROM tls_certificate WHERE id = @id;

-- 1件を PEM のまま引く。**画面から保存するために使う**（ApiDesign.md 11.7）。
-- **秘密鍵は引かない**——この口が返すのは証明書だけである。
-- name: GetTLSCertificatePEM :one
SELECT common_name, cert_pem
FROM tls_certificate WHERE id = @id;
