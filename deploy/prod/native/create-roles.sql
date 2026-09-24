-- create-roles.sql — PB のための DB とロールを、既存の PostgreSQL に作る
--
-- 使い方は MANUAL.md「3.3 DB を用意する」。スーパーユーザで、psql から1回だけ実行する:
--
--   psql -h <ホスト> -p <ポート> -U postgres -d postgres -f create-roles.sql
--
-- **パスワードをこのファイルに書かない。** \password が入力を画面に出さずに尋ね、
-- ハッシュにしてからサーバへ送る。psql の履歴にもサーバのログにも平文が残らない。
--
-- **2回目は最初の CREATE ROLE で止まる。** 既にあるロールを上書きしない。
--
-- 中身は、コンテナで DB を立てるときの deploy/base/initdb/01_roles.sh と同じ権限の分け方である
-- （DbDesign.md 3.4）。pb_owner がスキーマを持ち、pb_app は読み書きだけができる。

\set ON_ERROR_STOP on

-- ── ロール ──────────────────────────────────────────────

-- スキーマの所有者。migrate（goose）はこのロールで繋ぐ。
CREATE ROLE pb_owner LOGIN;
\echo 'pb_owner のパスワードを2回入力する（secrets/pgpass に書くものと同じ値）'
\password pb_owner

-- 実行時のロール。pb serve はこのロールで繋ぐ（テーブルを作れない・消せない）。
CREATE ROLE pb_app LOGIN;
\echo 'pb_app のパスワードを2回入力する（secrets/app_database_url に書くものと同じ値）'
\password pb_app

-- ── データベース ────────────────────────────────────────

-- 文字コードは UTF8、既定の照合順序は C、文字の種類（LC_CTYPE）は C.UTF-8（DbDesign.md 3.1）。
-- **LC_CTYPE を C にしない。** C では pg_trgm が日本語から trigram を取り出せず、キーワード
-- 検索でインデックスが効かない（4.5）。C.UTF-8 がサーバに無い OS では作成が失敗する
-- ——その場合は UTF-8 のロケール（例：ja_JP.UTF-8）を LC_CTYPE に指定する。
-- template0 から作るのは、サーバ既定のロケールを引き継がないため。
CREATE DATABASE pb OWNER pb_owner ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C.UTF-8' TEMPLATE template0;

\connect pb

-- 拡張。migrate も IF NOT EXISTS で作るが、contrib が入っていないサーバでは
-- ここで先に失敗させる（どれが足りないかがこの場で分かる）。
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ── 権限 ────────────────────────────────────────────────

GRANT CONNECT ON DATABASE pb TO pb_app;
GRANT USAGE ON SCHEMA public TO pb_app;

-- 既存のテーブルへの権限（作った直後なので対象は無いが、01_roles.sh と揃える）。
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO pb_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO pb_app;

-- これから pb_owner が作るテーブルにも、自動で権限を付ける。
ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO pb_app;
ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO pb_app;

-- public スキーマへの CREATE を一般のロールから外す（PostgreSQL 15 以降は既定で外れている）。
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- 実行時のロールの既定のステートメントタイムアウト（DbDesign.md 3.5）。
ALTER ROLE pb_app SET statement_timeout = '15s';

\echo 'PB の DB とロールを作った。次は MANUAL.md「3.4 秘密を置く」。'
