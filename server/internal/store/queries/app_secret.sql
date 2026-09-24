-- 秘密の暗号鍵のクエリ（DbDesign.md 6.16、Design.md 6.6.1）。
--
-- **PB が初回に生成した鍵を読む／書く。** PB_SECRET_KEY を与えたときは
-- この表を読まない（環境変数が勝つ）。

-- 鍵を引く。無ければ行が返らない（初回）。
-- name: GetAppSecret :one
SELECT key_id, secret FROM app_secret WHERE key_id = @key_id;

-- 鍵を作る。**既にあれば何もしない**——複数のレプリカが同時に起動したとき、
-- 先に入れたほうを両方が使うようにするためである。
-- **書いた／既にあったに関わらず、呼び出し側は改めて GetAppSecret で読む。**
-- name: CreateAppSecretIfAbsent :exec
INSERT INTO app_secret (key_id, secret) VALUES (@key_id, @secret)
ON CONFLICT (key_id) DO NOTHING;
