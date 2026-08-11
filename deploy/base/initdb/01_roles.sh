#!/bin/bash
# deploy/base/initdb/01_roles.sh
#
# 正本: DbDesign.md 3.4（DBロールと権限）/ 3.5（statement_timeout）
# コンテナの初回起動時（pgdata が空のとき）に一度だけ実行される。
# pb_app のパスワードは secret ファイルから読み、SQL には直接書かない。

set -euo pipefail

app_password="$(cat /run/secrets/app_db_password)"

psql -v ON_ERROR_STOP=1 \
     --username "$POSTGRES_USER" \
     --dbname "$POSTGRES_DB" \
     -v app_password="$app_password" <<'SQL'

-- 実行時ロール：DMLのみ。DDLは実行できない
CREATE ROLE pb_app LOGIN PASSWORD :'app_password';

-- スキーマの使用権
GRANT CONNECT ON DATABASE pb TO pb_app;
GRANT USAGE  ON SCHEMA public TO pb_app;

-- 既存テーブルへの権限
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES    IN SCHEMA public TO pb_app;
GRANT USAGE, SELECT                  ON ALL SEQUENCES IN SCHEMA public TO pb_app;

-- 今後 pb_owner が作るテーブルにも自動で権限を付与する
ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO pb_app;
ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO pb_app;

-- public スキーマへの CREATE 権限を一般ロールから剥奪（PG15以降は既定で剥奪済み）
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- 実行時ロールの既定のステートメントタイムアウト（DbDesign.md 3.5）
ALTER ROLE pb_app SET statement_timeout = '15s';

SQL
