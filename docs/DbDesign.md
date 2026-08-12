# Project Backyard (PB) データベース設計書

> 本書は PB のデータベースに関する**唯一の正本**である。スキーマ・マイグレーション・DB実行環境はすべて本書に集約する。
>
> **文書体系**：`Requirements.md`（要件）→ `Design.md`（全体設計）→ 本書 / `ApiDesign.md` / `GuiDesign.md`（領域別の正本）
>
> - 対象読者：サーバ実装者（人間およびAIエージェント）
> - **方針変更**：SQLite先行をやめ、**初期から PostgreSQL を前提とする**（2章）
> - 関連：`Design.md`（全体設計・認証設計）、`ApiDesign.md`、`GuiDesign.md`、`Requirements.md`
> - 状態：Phase 1 のDDL・シードを確定。Phase 2/3 はテーブル構成案
> - 最終更新：2026-08-11

**`Design.md` 旧第5章「データベース設計」は本書に統合された。** 以降、DBに関する記述は本書を正とする。

---

## 目次

| 章 | 内容 |
|---|---|
| 1 | 本書の位置づけ |
| 2 | 方針変更：PostgreSQL 前提へ |
| 3 | 実行環境（docker-compose / Kubernetes） |
| 4 | 共通規約 |
| 5 | マイグレーション運用 |
| 6 | スキーマ定義（Phase 1） |
| 7 | 初期データ |
| 8 | Phase 2 / 3 の拡張（DDL構成案） |
| 9 | 運用 |
| 10 | 未解決の検討事項 |

---

# 1. 本書の位置づけ

`Design.md` はシステム全体の設計を扱い、本書はそのうちデータベースに関する部分を**実装可能な粒度まで**降ろす。具体的には以下を含む。

- PostgreSQL のバージョン・拡張・ロール設計
- 開発端末での実行環境（docker-compose / Kubernetes）
- マイグレーションの運用規則とファイル構成
- Phase 1 の完全なDDL
- 初期データ（権限カタログ、ロール、ワークフローテンプレート、初期管理者）

---

# 2. 方針変更：PostgreSQL 前提へ

## 2.1 変更の理由

当初は「単一バイナリ＋SQLite ファイルで起動が速い」ことを重視し、SQLite先行・PostgreSQL移行という方針を採っていた。これを改め、**開発初期から PostgreSQL を使う**。

| 理由 | 内容 |
|---|---|
| 移行コストの回避 | 両対応のために課していた制約（JSON内部を検索しない、全文検索を分離する等）は、実装の自由度を恒常的に下げる。**移行が確実に来るなら、最初から移行後の姿で作る方が安い** |
| 実行環境が整っている | 開発端末で PB をコンテナとして動かす前提であり、DBコンテナを1つ増やす追加コストは小さい |
| エージェント並行アクセス | Phase 2 で複数エージェントが同時に書き込む。SQLite の単一ライタ制約は旧設計で未解決事項として挙げていたが、PostgreSQL 採用で消える |
| 拡張機能 | pgvector（`Requirements.md` 6.5 のRAG）、`LISTEN/NOTIFY`（`Requirements.md` 1章のリアルタイム更新）が最初から使える |

## 2.2 解除される制約

旧方針（SQLite / PostgreSQL 両対応）で課していた制約のうち、以下を解除する。

| 旧制約 | 新方針 |
|---|---|
| 日時を `TEXT`（ISO8601文字列） | **`timestamptz`**。タイムゾーン演算・範囲検索がDBで完結する |
| 真偽を `INTEGER`（0/1） | **`boolean`** |
| JSONを `TEXT`、内部検索を禁止 | **`jsonb`**。GINインデックスによる内部検索を許可する |
| ベクトルを `BLOB` で別テーブル | **`vector`**（pgvector）。ただしテーブル分離は維持する（8.3） |
| 全文検索を本体から分離 | PostgreSQL 側で完結させる。ただし日本語処理に課題があり別途検討（4.5） |
| トリガの使用禁止 | **`updated_at` の自動更新にのみ**トリガを許可する（6.1、例外はこの1つだけ） |
| `RETURNING` への依存回避 | 自由に使う。チケット採番が1文で書けるようになる（6.4） |
| 配列型の禁止 | 使用可。ただし多値は原則として関連テーブルで表現し、配列は避ける |

## 2.3 維持する規約

方針変更後も以下は維持する。理由が SQLite 互換性ではなかったため。

| 規約 | 維持する理由 |
|---|---|
| **IDは ULID をアプリで生成**（自動採番を使わない） | ①エージェントが並行生成しても衝突しない、②時系列順に並ぶ、③URL・ログ上で読める |
| **ENUM型を使わず `text` + `CHECK`** | `ALTER TYPE ... ADD VALUE` は可能だが、値の削除・改名が困難。CHECK制約なら通常のマイグレーションで書き換えられる |
| **物理削除を既定とする** | 監査は `audit_log` / `activity` に残る。論理削除フラグはクエリを複雑にする |
| **命名規約**（単数形・スネークケース） | 変更する理由がない |

---

# 3. 実行環境

## 3.1 バージョンと拡張

| 項目 | 値 |
|---|---|
| PostgreSQL | **17**（`pgvector/pgvector:pg17` イメージを使用） |
| エンコーディング | UTF8 |
| DB既定 collation | **`C`**（比較が高速かつ決定的） |
| 日本語の並び順 | **ICU collation を明示指定**（4.4） |
| タイムゾーン | `UTC` |

| 拡張 | 用途 | Phase |
|---|---|---|
| `pgcrypto` | ランダム値生成（`gen_random_bytes`） | 1 |
| `citext` | メールアドレスの大文字小文字を区別しない一意制約 | 1 |
| `pg_trgm` | 日本語を含む部分一致検索の高速化（4.5） | 1 |
| `vector` | 埋め込みベクトル（`Requirements.md` 6.5） | 3 |

## 3.2 docker-compose（開発端末での既定構成）

```yaml
# deploy/base/compose.yaml（deploy/dev/compose.yaml で環境差分を重ねる）
name: project-backyard

services:
  db:
    image: pgvector/pgvector:pg17
    restart: unless-stopped
    environment:
      POSTGRES_DB: pb
      POSTGRES_USER: pb_owner
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
      POSTGRES_INITDB_ARGS: "--encoding=UTF8 --locale=C"
      TZ: UTC
      PGTZ: UTC
    secrets:
      - db_password
      - app_db_password              # 3.4 の initdb がロール作成時に読む
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./initdb:/docker-entrypoint-initdb.d:ro       # 3.4 のロール作成
    ports:
      - "127.0.0.1:5432:5432"        # ホストからの接続はローカルのみ
    command:
      - postgres
      - -c
      - shared_buffers=256MB
      - -c
      - work_mem=16MB
      - -c
      - max_connections=50
      - -c
      - log_min_duration_statement=200ms
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U pb_owner -d pb"]
      interval: 5s
      timeout: 3s
      retries: 20

  app:
    image: project-backyard:dev
    build:
      context: ../..                 # リポジトリルート（client/ と server/ を含む）
      dockerfile: deploy/Dockerfile  # `Design.md` 4.1
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
    environment:
      PB_BIND: "0.0.0.0:8080"
      PB_DATABASE_URL_FILE: /run/secrets/app_database_url
      PB_LOG_FORMAT: json            # 標準出力へ構造化JSON（`Design.md` 10.1）
      PB_LOG_LEVEL: info
      PB_HEALTH_SHOW_VERSION: "false" # `ApiDesign.md` 2.11
      TZ: UTC
    secrets:
      - app_database_url
    ports:
      - "127.0.0.1:8080:8080"        # `Requirements.md` 10.10.2：127.0.0.1 のみ

volumes:
  pgdata:

secrets:
  db_password:
    file: ../dev/secrets/db_password
  app_db_password:
    file: ../dev/secrets/app_db_password
  app_database_url:
    file: ../dev/secrets/app_database_url
```

**設計上の要点**

- **ポートは `127.0.0.1` にのみ公開する。** `0.0.0.0` にすると同一ネットワークの他端末から到達可能になる（`Requirements.md` 10.10.2）
- アプリコンテナ内部では `0.0.0.0:8080` で待ち受け、公開範囲は compose の `ports` で制御する
- パスワードは環境変数に直接書かず `*_FILE` で渡す。**`docker inspect` や `ps` で見えないようにするため**
- `deploy/<env>/secrets/` は `.gitignore` に含める（`.example` のみコミット）
- `healthcheck` + `depends_on: condition: service_healthy` により、DB起動前のマイグレーション失敗を防ぐ
- **`build.context` はリポジトリルートを指す。** compose ファイルの位置（`deploy/base/`）ではない。`deploy/Dockerfile` は `client/` と `server/` の双方をマルチステージでビルドするため、両方を含むルートを渡す必要がある（`Design.md` 4.1、4.5）

`deploy/dev/secrets/` に置くファイル：

| ファイル | 内容 |
|---|---|
| `db_password` | `pb_owner`（スキーマ所有者）のパスワード |
| `app_db_password` | `pb_app`（実行時ロール）のパスワード。3.4 の initdb が読む |
| `app_database_url` | `pb_app` での接続文字列 |

`app_database_url` の内容例：

```
postgres://pb_app:<app_db_password と同じ値>@db:5432/pb?sslmode=disable&application_name=pb
```

**`app_db_password` と `app_database_url` のパスワードは同じ値にする。** 食い違っても起動時には失敗せず、アプリがDBへ接続する時点で初めて認証エラーになる。

## 3.3 Kubernetes（任意）

開発端末では compose を既定とする。K8s を使う場合は以下の構成を推奨する。

| 対象 | 方式 |
|---|---|
| PostgreSQL | **CloudNativePG オペレータ**。`Cluster` リソースでバックアップ・フェイルオーバ・PITR を宣言的に扱える。素の StatefulSet で自作しない |
| PB本体 | `Deployment`（レプリカ1）＋ `Service` |
| マイグレーション | Deployment の initContainer、または `Job` として分離 |
| 資格情報 | `Secret`。CloudNativePG が生成する接続情報 Secret をそのまま参照する |
| 公開 | ローカル利用は `kubectl port-forward`。Ingress を張る場合はTLSを必須とする |

```yaml
# 概略のみ
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: { name: pb-db }
spec:
  instances: 1
  imageName: ghcr.io/cloudnative-pg/postgresql:17
  postgresql:
    parameters:
      shared_buffers: "256MB"
      log_min_duration_statement: "200ms"
  bootstrap:
    initdb:
      database: pb
      owner: pb_owner
      encoding: UTF8
      localeCollate: C
  storage: { size: 10Gi }
```

**マイグレーションを initContainer に置くか Job にするか**は、レプリカ数を2以上にした時点で問題になる（同時実行）。Phase 1 はレプリカ1のため initContainer で足りるが、マイグレーションツール側のアドバイザリロックに依存する設計にしておく（5.3）。

## 3.4 DBロールと権限

**アプリケーションを DB オーナーで動かさない。** DDL権限を持つロールと、実行時に使うロールを分離する。

**パスワードをSQLにリテラルで書かない。** initdb を `.sql` ではなくシェルスクリプトにし、secret ファイルから読んで psql 変数として渡す。

```bash
#!/bin/bash
# deploy/base/initdb/01_roles.sh（コンテナ初回起動時に一度だけ実行される）
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

-- 実行時ロールの既定のステートメントタイムアウト（3.5）
ALTER ROLE pb_app SET statement_timeout = '15s';

SQL
```

| ロール | 用途 | 権限 |
|---|---|---|
| `pb_owner` | マイグレーション実行 | スキーマ所有者。DDL可 |
| `pb_app` | アプリ実行時 | DML のみ。テーブル作成・削除不可 |

**この分離により、実行時のSQLインジェクションが成立してもテーブルを落とせない。** 監査ログの改ざんについては `audit_log` の `DELETE` / `UPDATE` 権限も外すことを検討する（10章）。

**運用上の注意**

- **スクリプトには実行ビットを立てる。** `:ro` マウントでもホスト側のファイルモードがそのまま使われる。立っていない場合は entrypoint がシェルに読み込む（source する）挙動になる
- **initdb が走るのは `pgdata` ボリュームが空の初回起動時のみ。** ロール定義を変えた場合、既存の環境には反映されない。開発端末では `docker compose -f deploy/base/compose.yaml down -v` でボリュームごと作り直す
- ヒアドキュメントは `<<'SQL'` とクォートする。シェルによる変数展開を止め、パスワードは psql の `:'app_password'` 経由でのみ渡す

## 3.5 接続プール

| 項目 | 値 |
|---|---|
| アプリ側プール | 最小 2 / 最大 10（`sqlx::PgPoolOptions` 等） |
| `max_connections`（DB側） | 50 |
| `application_name` | `pb`。`pg_stat_activity` での識別に使う |
| ステートメントタイムアウト | 実行時ロールに `SET statement_timeout = '15s'` を既定として付与（3.4 の initdb で `ALTER ROLE` する） |

PgBouncer は Phase 1 では不要。単一プロセス・少人数利用のため。

---

# 4. 共通規約

## 4.1 型の対応

| 論理型 | PostgreSQL 型 | 備考 |
|---|---|---|
| ID | `char(26) COLLATE "C"` | ULID（Crockford Base32、26文字固定） |
| 日時 | `timestamptz` | 既定値 `now()` |
| 日付 | `date` | 期限・開始日 |
| 真偽 | `boolean` | |
| 列挙 | `text` + `CHECK` | 2.3 のとおりENUM型は使わない |
| 短い文字列 | `text` | `varchar(n)` を使わない。長さ制約は `CHECK` で表現する |
| メール | `citext` | 大文字小文字を区別しない一意制約 |
| 構造化データ | `jsonb` | 既定値を `'{}'::jsonb` / `'[]'::jsonb` とし `NULL` を避ける |
| 数値 | `integer` / `double precision` | 金額を扱わないため `numeric` は不要 |
| ベクトル | `vector(n)` | Phase 3。専用テーブルに隔離（8.3） |

**`varchar(n)` を使わない理由**：PostgreSQL では `text` と `varchar` に性能差がなく、長さ変更が `ALTER TABLE` を要する。長さ制限は `CHECK (length(title) <= 200)` として表現し、変更時はCHECK制約の張り替えで済ませる。

## 4.2 ID を ULID とし `char(26) COLLATE "C"` で保持する

| 選択肢 | 長所 | 短所 |
|---|---|---|
| **ULID（`char(26)`）** | 時系列順、URL・ログで読める、コピペしやすい | 26バイト。`uuid` より10バイト大きい |
| UUIDv7（`uuid`） | 16バイト、標準型 | 表示が長く読みにくい（36文字表記） |

**ULID を採る。** インデックスサイズの差は Phase 1〜2 の規模（数万行）では無視できる一方、開発中にIDを目視・コピペする頻度は高い。

`COLLATE "C"` を明示するのは、**ロケール依存の文字列比較を避けてBツリー比較を最速にする**ため。DB既定 collation を `C` にしているため冗長だが、将来DB既定を変えた場合の事故を防ぐため列に明記する。

## 4.3 タイムスタンプと `updated_at` トリガ

すべてのテーブルに `created_at`、更新のあるテーブルに `updated_at` を置く。

**`updated_at` の更新はトリガで行う。** 旧方針ではトリガ禁止としていたが、この1点だけ例外とする。理由は、アプリ側で `updated_at` の設定を忘れると**更新が検知できずポーリング差分やETagが壊れる**ためで、抜けを許さない仕組みが必要になる。トリガは共通関数1つのみで、ビジネスロジックは一切含まない。

```sql
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;
```

## 4.4 日本語の並び順

DB既定 collation を `C` にしているため、`ORDER BY display_name` はコードポイント順となり、日本語では実用にならない。**表示順が必要な箇所で ICU collation を明示する。**

```sql
SELECT * FROM app_user ORDER BY display_name COLLATE "ja-JP-x-icu";
```

対象は `app_user.display_name`、`project.name`、`ticket.title` の並び替え。列定義側に collation を持たせず、**クエリ側で指定する**（既定の比較・一意制約は `C` のまま高速に保つため）。

## 4.5 日本語の全文検索

**PostgreSQL 標準の `to_tsvector` は日本語を分かち書きできない。** 空白区切りのない日本語文はそのまま1トークンになり、実用的な全文検索にならない。

| 方式 | 評価 |
|---|---|
| **`pg_trgm` + GIN**（Phase 1 で採用） | 標準contribで導入が容易。`ILIKE '%キーワード%'` を高速化できる。3文字未満のクエリで精度が落ちるが、Phase 1 の規模と用途（チケットのタイトル・本文検索）には十分 |
| `pg_bigm` | 日本語向けbigram索引。2文字クエリに強い。カスタムイメージのビルドが必要 |
| PGroonga | 形態素解析・スコアリングまで対応。高機能だが導入と運用の重さが原則（軽快さ）と衝突する |

Phase 1 は `pg_trgm` とし、実運用で不足が確認された時点で `pg_bigm` へ移行する。**移行時に影響するのはインデックス定義と検索クエリのみで、スキーマ本体は変わらない**よう、検索は `repo/search/` に隔離する（`Design.md` 4章）。

## 4.6 削除方針

| 対象 | 方針 |
|---|---|
| 既定 | 物理削除。監査は `audit_log` / `activity` に残る |
| `comment` | `deleted_at` を持ち「削除されました」表示を維持 |
| `knowledge`（Phase 2） | `deprecated_at` で無効化し履歴を保つ |
| 外部キー | 子の存在意義が親に依存するなら `CASCADE`、参照が失われても本体が意味を持つなら `SET NULL` |

## 4.7 命名規約

- テーブル：単数形・スネークケース（`ticket`、`access_token`）
- 主キー：`id`
- 外部キー：`<参照先>_id`。役割で参照する場合は役割名（`assignee_id`、`author_id`）
- 真偽値：`is_` / `has_` 接頭辞
- 日時：`_at`、日付：`_date`
- **`jsonb` 列に `_json` 接尾辞を付けない**（旧方針からの変更）。型がスキーマ上明白になったため、冗長な接尾辞は外す。API のフィールド名もこれに合わせる（`ApiDesign.md` 2.2）
- インデックス：`idx_<table>_<列>`、一意インデックス：`uq_<table>_<列>`、制約：`ck_<table>_<内容>`

---

# 5. マイグレーション運用

## 5.1 ツール

SQLファイルベースのマイグレーションツールとして **goose v3** を使う（`Design.md` 3.1）。**ORM の自動マイグレーション生成は使わない。**

理由：生成されるDDLがレビュー困難で、意図しないテーブル再作成を招くことがある。PBはスキーマそのものが設計資産であり、SQLを直接管理する。

**PostgreSQL はDDLがトランザクショナル**であるため、各マイグレーションは全体が成功するか全体が巻き戻るかのいずれかになる。中途半端な適用状態が生まれない。

### goose・sqlc の導入と実行

**開発ツール（goose と sqlc）は `server/tools/go.mod` という専用モジュールに隔離し、そこの `tool` ディレクティブでバージョンを固定する。**

バージョンを固定するのは、開発者ごとに `go install` したバージョンがばらつくと、`goose_db_version` の扱いや注釈の解釈、sqlc の生成結果が環境間でずれるため。

**アプリ本体の `server/go.mod` に置かないのは、ツールの推移依存がアプリの依存として並んでしまうため。** goose は対応する全DBドライバ（clickhouse / mssql / ydb / sqlite / vertica 等）を、sqlc は構文解析器とプラグイン基盤を引き込む。両方を `server/go.mod` に入れると `// indirect` が80件を超え、さらに `go` ディレクティブが 1.25 へ引き上げられて `Design.md` 3.1 の「Go 1.24 以上」と衝突する。隔離すれば `server/go.mod` は実依存4件＋indirect 8件に収まり、**リリースビルドが読む依存グラフとツールの依存グラフが混ざらない。**

```
server/
├── go.mod          ← アプリの依存のみ（pgx / argon2id / ulid / term / chi）
├── sqlc.yaml       ← 生成設定。パスはこのファイルからの相対で解決される
├── migrations/
└── tools/
    ├── go.mod      ← tool ( goose v3.26.0, sqlc v1.30.0 )
    └── go.sum
```

```make
# Makefile
migrate:
	@cd server/tools && GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(GOOSE_DBSTRING_OWNER)" \
		go tool goose -dir ../migrations up

sqlc:
	cd server/tools && go tool sqlc -f ../sqlc.yaml generate
```

| 項目 | 内容 |
|---|---|
| 接続ロール | **`pb_owner`**。DDL を実行するため（3.4）。`pb_app` では実行できない |
| 接続文字列 | `deploy/dev/secrets/db_password` を Makefile の recipe 内で読んで組み立てる。**Makefile にも `ps` の argv にも平文を残さない**ため、レシピは `@` 付きで実行する |
| 権限の伝播 | 3.4 の `ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner` により、goose が作ったテーブルにも `pb_app` の DML 権限が自動で付く。マイグレーション後に `GRANT` を流す必要はない |
| 生成物 | `server/internal/store/gen/` は**コミットする**（`Design.md` 4.6）。sqlc を導入していない環境でもビルドが通る状態を保つ |

**`server/tools/go.mod` の `go` ディレクティブは 1.24 に保つ。** `go get -tool` は依存を最新へ引き上げる際にこの値も書き換えることがあり、そうなると Go 1.24 の環境で `make migrate` が動かなくなる。ツールを追加・更新したら `head -3 server/tools/go.mod` で確認する。

**各ファイルの冒頭に `-- +goose Up` を置く。** `down` は書かない（5.3）。`set_updated_at()` のように本体に `;` を含む定義は、goose のパーサがステートメント境界を誤らないよう `-- +goose StatementBegin` / `-- +goose StatementEnd` で囲む。

**注釈以外に、本書 6〜7章のDDLへ手を入れてはならない。** goose の注釈はツールの要件であって設計判断ではない。

## 5.2 ファイル構成

```
server/migrations/                      ← Design.md 4.1。sqlc がスキーマ源として読む
├── 0001_extensions_and_functions.sql   拡張、set_updated_at()
├── 0002_actor_auth.sql                 actor, app_user, auth_provider,
│                                       user_identity, local_credential, access_token
├── 0003_authz.sql                      permission, role, role_permission, project_member
├── 0004_project.sql                    project, project_counter
├── 0005_workflow.sql                   workflow, workflow_status, workflow_transition
├── 0006_ticket.sql                     ticket, ticket_link
├── 0007_comment_attachment.sql         comment, attachment
├── 0008_history.sql                    activity, audit_log
├── 0009_sprint.sql                     sprint
└── 0010_seed_phase1.sql                権限カタログ、ロール、ワークフローテンプレート
```

`project.workflow_id` と `ticket.sprint_id` は後続テーブルを参照するため、**FK制約のみ後から `ALTER TABLE ... ADD CONSTRAINT` で付与する**（0005 / 0009 の末尾）。PostgreSQL は前方参照を許さないためである。

## 5.3 運用規則

| 規則 | 内容 |
|---|---|
| **前進のみ** | `down` マイグレーションを書かない。誤りは新しいマイグレーションで修正する。ロールバックはバックアップからの復元で行う |
| 適用済みファイルを編集しない | 内容ハッシュが変わり、環境間で不整合になる |
| 破壊的変更は2段階 | ①新列を追加してアプリを両対応にする → ②デプロイ後に旧列を削除する（expand / contract） |
| 大規模テーブルへのインデックス | `CREATE INDEX CONCURRENTLY` を使う。**トランザクション外で実行する必要があるため、単独のマイグレーションファイルに分ける** |
| 同時実行の防止 | ツールのアドバイザリロック機能を有効にする（K8sで複数Podが同時起動した場合の保護） |
| シードの冪等性 | `ON CONFLICT DO NOTHING` / `DO UPDATE` を使い、再実行しても壊れないようにする |

---

# 6. スキーマ定義（Phase 1）

## 6.1 拡張と共通関数（0001）

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;
```

## 6.2 アクターと認証（0002）

```sql
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
```

**`token_hash` に `UNIQUE` を張ることが、認証の主経路になる。** 受け取った平文をSHA-256にして1回のインデックス探索で引く（`ApiDesign.md` 3.2）。

## 6.3 認可（0003）

```sql
CREATE TABLE permission (
  key         text PRIMARY KEY,
  category    text NOT NULL,
  description text NOT NULL,
  sort_order  integer NOT NULL DEFAULT 0
);

CREATE TABLE role (
  key          text PRIMARY KEY,
  scope        text NOT NULL CHECK (scope IN ('system','project')),
  display_name text NOT NULL,
  description  text,
  is_builtin   boolean NOT NULL DEFAULT true,
  sort_order   integer NOT NULL DEFAULT 0
);

CREATE TABLE role_permission (
  role_key       text NOT NULL REFERENCES role(key)       ON DELETE CASCADE,
  permission_key text NOT NULL REFERENCES permission(key) ON DELETE CASCADE,
  PRIMARY KEY (role_key, permission_key)
);

CREATE TABLE project_member (
  project_id char(26) COLLATE "C" NOT NULL,
  actor_id   char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE CASCADE,
  role_key   text        NOT NULL REFERENCES role(key) ON DELETE RESTRICT,
  joined_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, actor_id)
);
CREATE INDEX idx_project_member_actor ON project_member (actor_id);
```

`project_member.actor_id` が `app_user` ではなく `actor` を参照している点に注意。**エージェントもプロジェクトのメンバーになれる**（本書 6.2）。

`project_id` のFK制約は `project` テーブル作成後（0004 末尾）に付与する。

## 6.4 プロジェクト（0004）

```sql
CREATE TABLE project (
  id          char(26) COLLATE "C" PRIMARY KEY,
  key         text        NOT NULL UNIQUE
              CHECK (key ~ '^[a-z0-9][a-z0-9-]{1,19}$'),
  name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  description text        CHECK (description IS NULL OR length(description) <= 1000),
  status      text        NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active','archived')),
  workflow_id char(26) COLLATE "C",
  settings    jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  version     integer     NOT NULL DEFAULT 1,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);
CREATE INDEX idx_project_status ON project (status);
CREATE TRIGGER trg_project_updated BEFORE UPDATE ON project
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- チケット採番カウンタ（project 本体への行ロックを避けるため分離）
CREATE TABLE project_counter (
  project_id      char(26) COLLATE "C" PRIMARY KEY
                  REFERENCES project(id) ON DELETE CASCADE,
  last_ticket_seq integer NOT NULL DEFAULT 0
);

ALTER TABLE project_member
  ADD CONSTRAINT fk_project_member_project
  FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE;

ALTER TABLE access_token
  ADD CONSTRAINT fk_access_token_project
  FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE;
```

**`key` の形式検証をDBの `CHECK` にも置く。** アプリ側（`ApiDesign.md` 5.3）と二重になるが、URLとMCPエンドポイントに直結する値であり、不正値が入ると経路そのものが壊れるため。

### 6.4.1 チケット採番

```sql
UPDATE project_counter
   SET last_ticket_seq = last_ticket_seq + 1
 WHERE project_id = $1
RETURNING last_ticket_seq;
```

1文で行ロックと採番が完了する。**シーケンス（`CREATE SEQUENCE`）を使わない**理由は2つある。①プロジェクトごとにシーケンスを作ると DDL が動的になる、②シーケンスはトランザクションが巻き戻っても値を消費するため**欠番が出る**。チケット番号は人が読む識別子であり、`my-app-31` の次が `my-app-33` になるのは望ましくない。

同一プロジェクトへの同時作成はこの行で直列化されるが、Phase 1〜2 の規模では競合しない。

## 6.5 ワークフロー（0005）

```sql
CREATE TABLE workflow (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  name       text        NOT NULL,
  definition jsonb       NOT NULL DEFAULT '{}'::jsonb,   -- インポート/エクスポート用の原本
  is_template boolean    NOT NULL DEFAULT false,
  template_key text,                                     -- 'simple' 等
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_workflow_template CHECK (
    (is_template AND project_id IS NULL AND template_key IS NOT NULL)
    OR (NOT is_template AND project_id IS NOT NULL)
  )
);
CREATE UNIQUE INDEX uq_workflow_template ON workflow (template_key) WHERE is_template;
CREATE TRIGGER trg_workflow_updated BEFORE UPDATE ON workflow
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workflow_status (
  id                      char(26) COLLATE "C" PRIMARY KEY,
  workflow_id             char(26) COLLATE "C" NOT NULL
                          REFERENCES workflow(id) ON DELETE CASCADE,
  key                     text    NOT NULL,
  name                    text    NOT NULL,
  category                text    NOT NULL
                          CHECK (category IN ('todo','in_progress','review','done')),
  sort_order              integer NOT NULL,
  requires_human_approval boolean NOT NULL DEFAULT false,
  is_agent_reachable      boolean NOT NULL DEFAULT true,
  CONSTRAINT uq_workflow_status_key UNIQUE (workflow_id, key)
);

CREATE TABLE workflow_transition (
  id                  char(26) COLLATE "C" PRIMARY KEY,
  workflow_id         char(26) COLLATE "C" NOT NULL
                      REFERENCES workflow(id) ON DELETE CASCADE,
  from_status_key     text  NOT NULL,
  to_status_key       text  NOT NULL,
  required_permission text  REFERENCES permission(key) ON DELETE SET NULL,
  allowed_actor_kinds jsonb NOT NULL DEFAULT '["user","agent"]'::jsonb,
  CONSTRAINT uq_workflow_transition UNIQUE (workflow_id, from_status_key, to_status_key),
  CONSTRAINT ck_workflow_transition_diff CHECK (from_status_key <> to_status_key)
);

ALTER TABLE project
  ADD CONSTRAINT fk_project_workflow
  FOREIGN KEY (workflow_id) REFERENCES workflow(id) ON DELETE SET NULL;
```

`is_agent_reachable` と `allowed_actor_kinds` が、`Requirements.md` 10.10.4「承認ゲートをAPIレベルで強制する」の実体である。

## 6.6 チケット（0006）

```sql
CREATE TABLE ticket (
  id             char(26) COLLATE "C" PRIMARY KEY,
  project_id     char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  seq            integer NOT NULL,
  parent_id      char(26) COLLATE "C" REFERENCES ticket(id) ON DELETE SET NULL,
  type           text    NOT NULL
                 CHECK (type IN ('epic','story','task','bug','phase','wbs')),
  title          text    NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body_md        text,
  status_key     text    NOT NULL,          -- workflow_status.key への論理参照
  priority       text    CHECK (priority IN ('lowest','low','medium','high','highest')),
  assignee_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  reporter_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,

  estimate_point double precision,
  estimate_hours double precision,
  actual_hours   double precision,

  start_date     date,
  due_date       date,
  sprint_id      char(26) COLLATE "C",
  sort_key       text,                      -- LexoRank 方式の並び順

  -- エージェント連携（Phase 1 で列のみ先行定義）
  execution_mode text    NOT NULL DEFAULT 'human_only'
                 CHECK (execution_mode IN ('human_only','agent_only','agent_draft')),
  readiness      text    CHECK (readiness IN ('red','yellow','green')),
  readiness_note text,
  scope          jsonb   NOT NULL DEFAULT '{}'::jsonb,

  -- 起票元（`Requirements.md` 10.10.6 プロンプトインジェクション対策）
  source            text    NOT NULL DEFAULT 'internal',
  is_trusted_source boolean NOT NULL DEFAULT true,

  custom_fields  jsonb   NOT NULL DEFAULT '{}'::jsonb,
  closed_at      timestamptz,
  version        integer NOT NULL DEFAULT 1,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_ticket_project_seq UNIQUE (project_id, seq),
  CONSTRAINT ck_ticket_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id),
  CONSTRAINT ck_ticket_dates CHECK (start_date IS NULL OR due_date IS NULL
                                    OR start_date <= due_date)
);
CREATE INDEX idx_ticket_project_status ON ticket (project_id, status_key);
CREATE INDEX idx_ticket_assignee_open  ON ticket (assignee_id) WHERE closed_at IS NULL;
CREATE INDEX idx_ticket_parent         ON ticket (parent_id);
CREATE INDEX idx_ticket_sprint         ON ticket (sprint_id);
CREATE INDEX idx_ticket_due_open       ON ticket (project_id, due_date) WHERE closed_at IS NULL;
CREATE INDEX idx_ticket_updated        ON ticket (project_id, updated_at DESC);
CREATE INDEX idx_ticket_title_trgm     ON ticket USING gin (title gin_trgm_ops);
CREATE INDEX idx_ticket_body_trgm      ON ticket USING gin (body_md gin_trgm_ops);
CREATE INDEX idx_ticket_custom_fields  ON ticket USING gin (custom_fields);
CREATE TRIGGER trg_ticket_updated BEFORE UPDATE ON ticket
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE ticket_link (
  id               char(26) COLLATE "C" PRIMARY KEY,
  source_ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  target_ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  link_type        text    NOT NULL
                   CHECK (link_type IN ('FS','SS','FF','SF','relates','duplicates','blocks')),
  lag_days         integer NOT NULL DEFAULT 0,
  origin           text    NOT NULL DEFAULT 'human'
                   CHECK (origin IN ('human','ai_suggested')),
  confidence       double precision,
  created_by       char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_ticket_link UNIQUE (source_ticket_id, target_ticket_id, link_type),
  CONSTRAINT ck_ticket_link_diff CHECK (source_ticket_id <> target_ticket_id)
);
CREATE INDEX idx_ticket_link_source ON ticket_link (source_ticket_id);
CREATE INDEX idx_ticket_link_target ON ticket_link (target_ticket_id);
```

**`status_key` を物理FKにしない。** ワークフロー定義を差し替えた際に既存チケットが更新不能になるのを避けるため、整合性はアプリ層で検証する（本書 6.6）。

**`title` / `body_md` の trigram インデックス**が 4.5 の部分一致検索を支える。

## 6.7 コメントと添付（0007）

```sql
CREATE TABLE comment (
  id           char(26) COLLATE "C" PRIMARY KEY,
  ticket_id    char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  author_id    char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  body_md      text    NOT NULL,
  kind         text    NOT NULL DEFAULT 'discussion'
               CHECK (kind IN ('discussion','decision','artifact','caveat',
                               'reference','progress')),
  in_reply_to  char(26) COLLATE "C" REFERENCES comment(id) ON DELETE SET NULL,
  origin       text    NOT NULL DEFAULT 'human' CHECK (origin IN ('human','agent')),
  agent_run_id char(26) COLLATE "C",          -- Phase 2 で FK を付与
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  deleted_at   timestamptz
);
CREATE INDEX idx_comment_ticket ON comment (ticket_id, created_at);
CREATE INDEX idx_comment_kind   ON comment (ticket_id, kind) WHERE deleted_at IS NULL;
CREATE INDEX idx_comment_body_trgm ON comment USING gin (body_md gin_trgm_ops);
CREATE TRIGGER trg_comment_updated BEFORE UPDATE ON comment
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE attachment (
  id           char(26) COLLATE "C" PRIMARY KEY,
  project_id   char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  ticket_id    char(26) COLLATE "C" REFERENCES ticket(id)  ON DELETE CASCADE,
  comment_id   char(26) COLLATE "C" REFERENCES comment(id) ON DELETE CASCADE,
  storage_kind text    NOT NULL
               CHECK (storage_kind IN ('local','s3','sharepoint','gdrive','external_url')),
  storage_ref  text    NOT NULL,
  filename     text    NOT NULL,
  content_type text,
  size_bytes   bigint,
  checksum     text,
  uploaded_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_attachment_ticket ON attachment (ticket_id);
```

**`comment.author_id` を `NOT NULL` かつ `ON DELETE RESTRICT` としている。** コメントは必ず投稿者を持つべきだが、`SET NULL` は `NOT NULL` と衝突して削除時に不可解なエラーになる。`RESTRICT` にすることで、**アプリ側がシステムアクター（`kind='system'` の「削除されたユーザー」）へ付け替えてからでないとユーザーを削除できない**、という順序をDBが強制する。この付け替え処理は `ApiDesign.md` 6.5 の削除処理に含める。

`ticket.assignee_id` / `reporter_id` は NULL 許容のため `SET NULL` でよい。担当者不在のチケットは意味を持つが、投稿者不在のコメントは意味を持たない、という違いによる。

## 6.8 履歴と監査（0008）

```sql
-- 業務履歴：チケット画面の「変更履歴」に表示する
CREATE TABLE activity (
  id          char(26) COLLATE "C" PRIMARY KEY,
  project_id  char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  entity_type text NOT NULL,
  entity_id   char(26) COLLATE "C" NOT NULL,
  actor_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  action      text NOT NULL CHECK (action IN ('create','update','delete','transition')),
  field       text,
  old_value   text,
  new_value   text,
  request_id  char(26) COLLATE "C",
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_activity_entity  ON activity (entity_type, entity_id, occurred_at DESC);
CREATE INDEX idx_activity_project ON activity (project_id, occurred_at DESC);

-- 監査ログ：認証・権限・トークン・エージェント操作。管理者のみ閲覧可
CREATE TABLE audit_log (
  id          char(26) COLLATE "C" PRIMARY KEY,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  actor_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  actor_kind  text,          -- actor 削除後も種別が残るよう非正規化
  actor_label text,          -- 同上。削除時点の表示名・メール
  token_id    char(26) COLLATE "C",
  ip          inet,
  user_agent  text,
  action      text NOT NULL,
  target_type text,
  target_id   char(26) COLLATE "C",
  result      text NOT NULL CHECK (result IN ('success','failure')),
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_audit_time   ON audit_log (occurred_at DESC);
CREATE INDEX idx_audit_action ON audit_log (action, occurred_at DESC);
CREATE INDEX idx_audit_actor  ON audit_log (actor_id, occurred_at DESC);
```

**`ip` に `inet` 型を使う。** PostgreSQL 採用により、IPアドレスの正規化とサブネット検索がDB側でできるようになった。

`actor_label` を持たせるのは、**ユーザー削除後に「誰を消したか」を追えなくなることを防ぐ**ため（`ApiDesign.md` 6.5）。

## 6.9 スプリント（0009）

```sql
CREATE TABLE sprint (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  name       text NOT NULL,
  goal       text,
  start_date date,
  end_date   date,
  status     text NOT NULL DEFAULT 'planned'
             CHECK (status IN ('planned','active','completed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_sprint_dates CHECK (start_date IS NULL OR end_date IS NULL
                                    OR start_date <= end_date)
);
CREATE INDEX idx_sprint_project ON sprint (project_id, status);
CREATE TRIGGER trg_sprint_updated BEFORE UPDATE ON sprint
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE ticket
  ADD CONSTRAINT fk_ticket_sprint
  FOREIGN KEY (sprint_id) REFERENCES sprint(id) ON DELETE SET NULL;
```

---

# 7. 初期データ（0010）

## 7.1 認証プロバイダ

```sql
INSERT INTO auth_provider (key, type, display_name, is_enabled, sort_order)
VALUES ('local', 'local', 'メールアドレス', true, 0)
ON CONFLICT (key) DO NOTHING;
```

## 7.2 権限カタログ

```sql
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('project.view',        'project',   'プロジェクトの閲覧',           10),
  ('project.create',      'project',   'プロジェクトの作成',           11),
  ('project.edit',        'project',   'プロジェクト設定の変更',        12),
  ('project.archive',     'project',   'プロジェクトのアーカイブ',      13),
  ('ticket.view',         'ticket',    'チケットの閲覧',              20),
  ('ticket.create',       'ticket',    'チケットの作成',              21),
  ('ticket.edit',         'ticket',    'チケットの編集',              22),
  ('ticket.transition',   'ticket',    'ステータスの遷移',            23),
  ('ticket.close',        'ticket',    'チケットのクローズ',           24),
  ('ticket.assign',       'ticket',    '担当者の変更',                25),
  ('ticket.delete',       'ticket',    'チケットの削除',              26),
  ('comment.create',      'comment',   'コメントの投稿',              30),
  ('comment.edit_own',    'comment',   '自分のコメントの編集',         31),
  ('comment.delete_any',  'comment',   '任意のコメントの削除',         32),
  ('knowledge.view',      'knowledge', 'プロジェクトメモリの閲覧',      40),
  ('knowledge.propose',   'knowledge', 'プロジェクトメモリの提案',      41),
  ('knowledge.approve',   'knowledge', 'プロジェクトメモリの承認',      42),
  ('proposal.review',     'proposal',  'AI提案の承認・却下',           50),
  ('agent.register',      'agent',     'エージェントの登録',           60),
  ('agent.token.issue',   'agent',     'エージェント用トークンの発行',   61),
  ('agent.run',           'agent',     'MCP経由での実行',             62),
  ('user.manage',         'admin',     'ユーザーの管理',              70),
  ('role.manage',         'admin',     'ロールと権限の編集',           71),
  ('authprovider.manage', 'admin',     '認証プロバイダの設定',         72),
  ('auditlog.view',       'admin',     '監査ログの閲覧',              73),
  ('system.settings',     'admin',     'システム設定',                74),
  ('export.excel',        'export',    'Excelエクスポート',           80),
  ('share.publiclink',    'export',    '公開URLの発行',               81)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;
```

**`ON CONFLICT DO UPDATE` にしている**のは、説明文の修正を後続のマイグレーションで反映できるようにするため。権限キーそのものは削除しない（削除は `role_permission` の CASCADE を伴うため、専用のマイグレーションで慎重に扱う）。

## 7.3 ロールと権限の割り当て

```sql
INSERT INTO role (key, scope, display_name, description, is_builtin, sort_order) VALUES
  ('operator',       'system',  'オペレータ',
   'プロジェクトとチケットの閲覧・編集ができます',                      true, 10),
  ('administrator',  'system',  'アドミニストレータ',
   'ユーザー管理・システム設定を含む全操作ができます',                   true, 20),
  ('project_admin',  'project', 'プロジェクト管理者',
   '当該プロジェクトの全操作と承認ができます',                          true, 30),
  ('project_member', 'project', 'メンバー',
   '当該プロジェクトのチケットを作成・編集できます',                     true, 40),
  ('project_viewer', 'project', '閲覧者',
   '当該プロジェクトを閲覧のみできます',                                true, 50)
ON CONFLICT (key) DO NOTHING;

-- administrator：全権限
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- operator
INSERT INTO role_permission (role_key, permission_key)
SELECT 'operator', key FROM permission
 WHERE key IN ('project.view',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'ticket.close','ticket.assign',
               'comment.create','comment.edit_own',
               'knowledge.view','knowledge.propose',
               'export.excel')
ON CONFLICT DO NOTHING;

-- project_admin
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', key FROM permission
 WHERE key IN ('project.view','project.edit','project.archive',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'ticket.close','ticket.assign','ticket.delete',
               'comment.create','comment.edit_own','comment.delete_any',
               'knowledge.view','knowledge.propose','knowledge.approve',
               'proposal.review',
               'agent.register','agent.token.issue','agent.run',
               'export.excel','share.publiclink')
ON CONFLICT DO NOTHING;

-- project_member
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_member', key FROM permission
 WHERE key IN ('project.view',
               'ticket.view','ticket.create','ticket.edit','ticket.transition',
               'comment.create','comment.edit_own',
               'knowledge.view','knowledge.propose',
               'export.excel')
ON CONFLICT DO NOTHING;

-- project_viewer
INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_viewer', key FROM permission
 WHERE key IN ('project.view','ticket.view','knowledge.view')
ON CONFLICT DO NOTHING;
```

**割り当てを `SELECT ... WHERE key IN (...)` で書く**ことで、権限キーの綴り誤りがあっても行が挿入されないだけで済み、マイグレーションが壊れない。同時に、新しい権限を追加した際に `administrator` へは自動で付与される。

## 7.4 ワークフローテンプレート

```sql
-- simple：未着手 / 進行中 / 完了
INSERT INTO workflow (id, project_id, name, is_template, template_key, definition)
VALUES ('01JZZZZZZZZZZZZZZZZZZZZZW1', NULL, 'シンプル', true, 'simple', '{}'::jsonb)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_status
  (id, workflow_id, key, name, category, sort_order,
   requires_human_approval, is_agent_reachable) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZS1','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'todo','未着手','todo',1,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS2','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','進行中','in_progress',2,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS3','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'done','完了','done',3,true,false)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZT1','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'todo','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT2','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','done','ticket.close','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT3','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb)
ON CONFLICT DO NOTHING;
```

**`done` は `is_agent_reachable = false`、遷移の `allowed_actor_kinds` も `["user"]`。** エージェントは自分でチケットをクローズできない（`Requirements.md` 10.8.5 の禁止事項をDBレベルで担保する）。

```sql
-- with_review：未着手 / 進行中 / レビュー中 / 完了
INSERT INTO workflow (id, project_id, name, is_template, template_key, definition)
VALUES ('01JZZZZZZZZZZZZZZZZZZZZZW2', NULL, 'レビューあり', true, 'with_review', '{}'::jsonb)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_status
  (id, workflow_id, key, name, category, sort_order,
   requires_human_approval, is_agent_reachable) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZS4','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'todo','未着手','todo',1,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS5','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'in_progress','進行中','in_progress',2,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS6','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'review','レビュー中','review',3,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS7','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'done','完了','done',4,true,false)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZT4','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'todo','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT5','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'in_progress','review','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT6','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'review','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT7','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'review','done','ticket.close','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZT8','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb)
ON CONFLICT DO NOTHING;

-- with_approval：未着手 / 進行中 / レビュー中 / 承認待ち / 完了
INSERT INTO workflow (id, project_id, name, is_template, template_key, definition)
VALUES ('01JZZZZZZZZZZZZZZZZZZZZZW3', NULL, '承認フロー付き', true, 'with_approval', '{}'::jsonb)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_status
  (id, workflow_id, key, name, category, sort_order,
   requires_human_approval, is_agent_reachable) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZS8','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'todo','未着手','todo',1,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZS9','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'in_progress','進行中','in_progress',2,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZSA','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'review','レビュー中','review',3,false,true),
  ('01JZZZZZZZZZZZZZZZZZZZZZSB','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'approval','承認待ち','review',4,true,false),
  ('01JZZZZZZZZZZZZZZZZZZZZZSC','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'done','完了','done',5,true,false)
ON CONFLICT DO NOTHING;

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds) VALUES
  ('01JZZZZZZZZZZZZZZZZZZZZZT9','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'todo','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTA','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'in_progress','review','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTB','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'review','in_progress','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTC','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'review','approval','ticket.transition','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTD','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'approval','in_progress','ticket.transition','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTE','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'approval','done','ticket.close','["user"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTF','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb)
ON CONFLICT DO NOTHING;
```

3テンプレートの比較：

| | simple | with_review | with_approval |
|---|---|---|---|
| ステータス数 | 3 | 4 | 5 |
| 遷移数 | 3 | 5 | 7 |
| エージェントが到達できる最終地点 | `in_progress` | **`review`** | **`review`** |
| 人間限定の遷移 | `→done` | `→done` | `→approval`、`→done`、`approval→in_progress` |

**`approval`（承認待ち）の `category` は `review` とする。** `workflow_status.category` の `CHECK` は `todo / in_progress / review / done` の4値であり（6.5）、承認待ちは「完了していないが作業も止まっている」状態なので `review` に含める。**カテゴリはボードの列やバーンダウンの集計単位であり、承認待ちをレビュー中と同じ列に置くのが実態に合う。**

**`with_review` は `in_progress → review` をエージェントに許す。** これがテンプレートを分ける最大の意味で、**エージェントが作業を終えて人間のレビューに載せるところまでを自律的に行える**。一方 `with_approval` では `review → approval` を人間限定にしており、承認ゲートの手前へエージェントが自分で進むことを禁じている（`Requirements.md` 10.10.4）。

**差し戻し遷移（`review → in_progress`、`approval → in_progress`）を必ず持たせる。** これが無いと、レビューで問題が見つかったチケットを前進させるしか手がなくなり、承認ゲートが実質的に骨抜きになる。

テンプレートIDは固定ULIDとし、マイグレーションの再実行で重複しないようにする。採番は `…W<n>`（workflow）、`…S<n>`（status）、`…T<n>`（transition）の連番で、`n` は Crockford Base32（`0-9A-Z` から `I L O U` を除く）1文字。**テンプレートを追加する場合も既存のIDを再利用しない。**

## 7.5 初期管理者

**シードに管理者アカウントを含めない。** 既定パスワードを埋め込むと、そのまま運用に乗ってしまう危険がある。

代わりに CLI サブコマンドで対話的に作成する。

```
$ pb admin create
  表示名: 田中
  メールアドレス: tanaka@example.com
  パスワード: ************
  → アドミニストレータを作成しました（01K2F8QW3H7YRJ4M5N6P7Q8R9S）
```

内部処理は `actor` → `app_user`（`system_role='administrator'`）→ `user_identity`（`provider_key='local'`）→ `local_credential` を1トランザクションで作成する。既に管理者が存在する場合は警告を出して確認を求める。

---

# 8. Phase 2 / 3 の拡張

Phase 1 のテーブルは変更せず、**テーブル追加のみ**で拡張する。本章のDDLは構成案であり、各Phase着手時に確定させる。

```
0011_agent.sql            agent, task_lease
0012_dod.sql              dod_item
0013_agent_run.sql        agent_run, agent_report, context_pack_log
0014_knowledge.sql        knowledge, knowledge_revision, proposal
0015_comment_signal.sql   comment_signal
0016_embedding.sql        vector 拡張 + embedding
0017_project_event.sql    project_event
0018_analytics.sql        estimate_record, contribution
```

## 8.1 エージェント連携（Phase 2）

### 8.1.1 `agent` — エージェントの登録

```sql
CREATE TABLE agent (
  actor_id      char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  project_id    char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  client_kind   text    NOT NULL CHECK (client_kind IN ('claude_code','copilot','other')),
  model_name    text,
  model_version text,
  capabilities  jsonb   NOT NULL DEFAULT '[]'::jsonb,
  trust_level   integer NOT NULL DEFAULT 1 CHECK (trust_level BETWEEN 0 AND 3),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_agent_updated BEFORE UPDATE ON agent
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`model_name` / `model_version` を保持するのは、`Requirements.md` 10.10.3 の「モデル更新後に品質が変化した際の切り分け」のため。`agent_run` にも実行時点の値をコピーする（後からモデルを変えても過去の実行記録が壊れないよう非正規化する）。

`trust_level` は`Requirements.md` 10.10.3 の段階的権限昇格に対応する。

### 8.1.2 `task_lease` — リース管理

```sql
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
```

**部分一意インデックスで「1チケットに有効なリースは1つ」をDBレベルで保証する。** アプリ側の排他制御に依存しないため、エージェントが並行して claim しても破綻しない。

### 8.1.3 `dod_item` — 機械可読な完了条件

```sql
CREATE TABLE dod_item (
  id           char(26) COLLATE "C" PRIMARY KEY,
  ticket_id    char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  sort_order   integer NOT NULL,
  type         text    NOT NULL
               CHECK (type IN ('manual','task_ref','assertion','artifact','review')),
  body         text    NOT NULL,
  config       jsonb   NOT NULL DEFAULT '{}'::jsonb,
  is_satisfied boolean NOT NULL DEFAULT false,
  satisfied_at timestamptz,
  satisfied_by char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  evidence     text,
  origin       text    NOT NULL DEFAULT 'human' CHECK (origin IN ('human','ai_suggested')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_dod_ticket ON dod_item (ticket_id, sort_order);
CREATE TRIGGER trg_dod_updated BEFORE UPDATE ON dod_item
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`config` の例：`task_ref` は `{"ticket_id":"01K2..."}`、`assertion` は `{"command":"pytest tests/auth/","expect":"pass"}`。

### 8.1.4 `agent_run` / `agent_report`

```sql
CREATE TABLE agent_run (
  id               char(26) COLLATE "C" PRIMARY KEY,
  ticket_id        char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  actor_id         char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  token_id         char(26) COLLATE "C",
  client_kind      text,
  model_name       text,
  model_version    text,
  workflow_version integer,
  started_at       timestamptz NOT NULL DEFAULT now(),
  ended_at         timestamptz,
  status           text NOT NULL
                   CHECK (status IN ('running','completed','failed','abandoned')),
  tokens_used      bigint,
  turns            integer,
  retry_count      integer NOT NULL DEFAULT 0
);
CREATE INDEX idx_agent_run_ticket ON agent_run (ticket_id, started_at DESC);
CREATE INDEX idx_agent_run_actor  ON agent_run (actor_id, started_at DESC);

CREATE TABLE agent_report (
  id               char(26) COLLATE "C" PRIMARY KEY,
  agent_run_id     char(26) COLLATE "C" NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
  ticket_id        char(26) COLLATE "C" NOT NULL REFERENCES ticket(id)    ON DELETE CASCADE,
  status           text NOT NULL CHECK (status IN ('completed','blocked','partial')),
  report           jsonb NOT NULL,
  knowledge_impact text CHECK (knowledge_impact IN ('none','minor','major')),
  submitted_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_report_ticket ON agent_report (ticket_id, submitted_at DESC);

ALTER TABLE comment
  ADD CONSTRAINT fk_comment_agent_run
  FOREIGN KEY (agent_run_id) REFERENCES agent_run(id) ON DELETE SET NULL;
```

`report` に`Requirements.md` 10.6.1 のレポート全体を保存しつつ、頻繁に検索・集計する `status` と `knowledge_impact` のみ列に展開する。PostgreSQL では `jsonb` の内部検索も可能だが、**集計対象になる値は列に出す**方が実行計画が安定する。

`workflow_version` は`Requirements.md` 10.9.3 の陳腐化検出、`retry_count` は 10.10.5 のサーキットブレーカー判定に用いる。

### 8.1.5 `context_pack_log`

```sql
CREATE TABLE context_pack_log (
  id            char(26) COLLATE "C" PRIMARY KEY,
  ticket_id     char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  agent_run_id  char(26) COLLATE "C" REFERENCES agent_run(id) ON DELETE SET NULL,
  budget_tokens integer,
  actual_tokens integer,
  included      jsonb NOT NULL DEFAULT '[]'::jsonb,
  truncated     jsonb NOT NULL DEFAULT '[]'::jsonb,
  generated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_context_pack_run ON context_pack_log (agent_run_id);
```

**1テーブルで2つの要件を満たす。** `Requirements.md` 10.10.7（監査：エージェントが何を見たか）と 10.4.4（効果計測：どの情報を含めたときに成功率が上がったか）は、記録すべき内容が同一である。`agent_report.status` と突き合わせることで有用性スコアを算出する。

## 8.2 知識還流（Phase 2）

### 8.2.1 `knowledge` — プロジェクトメモリ

```sql
CREATE TABLE knowledge (
  id                char(26) COLLATE "C" PRIMARY KEY,
  project_id        char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  scope             text    NOT NULL DEFAULT 'project',
  kind              text    NOT NULL
                    CHECK (kind IN ('convention','decision','caveat','failure')),
  title             text    NOT NULL,
  body_md           text    NOT NULL,
  status            text    NOT NULL DEFAULT 'approved'
                    CHECK (status IN ('proposed','approved','deprecated')),
  usefulness        double precision,
  reference_count   integer NOT NULL DEFAULT 0,
  valid_until       timestamptz,
  source_ticket_id  char(26) COLLATE "C" REFERENCES ticket(id) ON DELETE SET NULL,
  created_by        char(26) COLLATE "C" REFERENCES actor(id)  ON DELETE SET NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deprecated_at     timestamptz,
  deprecated_reason text
);
CREATE INDEX idx_knowledge_active ON knowledge (project_id, scope, kind)
  WHERE status = 'approved';
CREATE INDEX idx_knowledge_body_trgm ON knowledge USING gin (body_md gin_trgm_ops);
CREATE TRIGGER trg_knowledge_updated BEFORE UPDATE ON knowledge
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE knowledge_revision (
  id            char(26) COLLATE "C" PRIMARY KEY,
  knowledge_id  char(26) COLLATE "C" NOT NULL REFERENCES knowledge(id) ON DELETE CASCADE,
  revision_no   integer NOT NULL,
  body_md       text    NOT NULL,
  changed_by    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  change_reason text,
  proposal_id   char(26) COLLATE "C",
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_knowledge_revision UNIQUE (knowledge_id, revision_no)
);
```

`scope` は `project` または `component:<名前>`。`kind = 'failure'` が`Requirements.md` 10.6.2 の「失敗の記録を第一級の資産とする」に対応する。**別テーブルを作らず `knowledge` の一種として扱う**のは、コンテキストパックへの供給経路を1本にまとめるため。

`knowledge_revision` により全変更が保持され、`proposal_id` を辿ればどのエージェントのどの提案に由来する変更かを特定できる。ロールバックは特定リビジョンの `body_md` を新リビジョンとして書き戻すことで行う（履歴を消さない）。

### 8.2.2 `proposal` — AIの提案を集約する

```sql
CREATE TABLE proposal (
  id           char(26) COLLATE "C" PRIMARY KEY,
  project_id   char(26) COLLATE "C" NOT NULL REFERENCES project(id)   ON DELETE CASCADE,
  ticket_id    char(26) COLLATE "C" REFERENCES ticket(id)             ON DELETE CASCADE,
  agent_run_id char(26) COLLATE "C" REFERENCES agent_run(id)          ON DELETE SET NULL,
  kind         text NOT NULL
               CHECK (kind IN ('memory','memory_update','subtask','doc_diff','link','event')),
  target_type  text,
  target_id    char(26) COLLATE "C",
  payload      jsonb NOT NULL,
  diff_text    text,
  rationale    text,
  proposed_by  char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE RESTRICT,
  proposed_at  timestamptz NOT NULL DEFAULT now(),
  status       text NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending','approved','rejected','superseded','auto_applied')),
  reviewed_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  reviewed_at  timestamptz,
  review_note  text
);
CREATE INDEX idx_proposal_queue ON proposal (project_id, proposed_at)
  WHERE status = 'pending';

ALTER TABLE knowledge_revision
  ADD CONSTRAINT fk_knowledge_revision_proposal
  FOREIGN KEY (proposal_id) REFERENCES proposal(id) ON DELETE SET NULL;
```

**すべての「AIの提案」がこの1テーブルを通る。** 承認キュー画面（`GuiDesign.md` 10章）が単一の問い合わせで成立し、承認・却下の監査も一元化できる。

`auto_applied` は、`Requirements.md` 10.6.3 の「`kind` ごとに自動採用／承認必須を設定する」ポリシーで自動反映されたものを表す。**自動反映であっても proposal 行は必ず残す**ことで、後から遡って取り消せる。

## 8.3 AI機能・分析（Phase 3）

### 8.3.1 `comment_signal` — コメント重要度

```sql
CREATE TABLE comment_signal (
  comment_id       char(26) COLLATE "C" PRIMARY KEY
                   REFERENCES comment(id) ON DELETE CASCADE,
  importance       double precision NOT NULL,
  category_weight  double precision,
  entity_density   double precision,
  reference_count  integer NOT NULL DEFAULT 0,
  triggered_change boolean NOT NULL DEFAULT false,
  scored_at        timestamptz NOT NULL DEFAULT now(),
  scorer_version   text
);
CREATE INDEX idx_comment_signal_importance ON comment_signal (importance DESC);
```

`comment` 本体から分離するのは、①スコアが頻繁に更新されて本体の `updated_at` が汚れる（「編集された」との区別がつかなくなる）ことを避けるため、②スコアリングモデルを入れ替えた際にこのテーブルのみ再構築すればよいため。`Requirements.md` 6.6 に対応する。

### 8.3.2 `embedding` — ベクトルインデックス

pgvector を最初から使える環境になったが、**専用テーブルへの隔離は維持する**。理由は、①埋め込みモデル変更時にこのテーブルのみ再構築すればよい、②本体テーブルの行サイズを膨らませない（1536次元で約6KB）、③ベクトル検索を使わない構成でもスキーマが成立する、の3点。

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE embedding (
  id          char(26) COLLATE "C" PRIMARY KEY,
  entity_type text NOT NULL CHECK (entity_type IN ('comment','ticket','knowledge')),
  entity_id   char(26) COLLATE "C" NOT NULL,
  model       text NOT NULL,
  vec         vector(1536) NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_embedding UNIQUE (entity_type, entity_id, model)
);
CREATE INDEX idx_embedding_hnsw ON embedding USING hnsw (vec vector_cosine_ops);
```

### 8.3.3 `project_event` — プロジェクトヒストリー

```sql
CREATE TABLE project_event (
  id          char(26) COLLATE "C" PRIMARY KEY,
  project_id  char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  occurred_on date NOT NULL,
  kind        text NOT NULL
              CHECK (kind IN ('decision','phase','breakthrough','milestone',
                              'team_change','artifact')),
  title       text NOT NULL,
  body_md     text,
  related_ticket_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  origin      text NOT NULL DEFAULT 'human' CHECK (origin IN ('human','ai_suggested')),
  status      text NOT NULL DEFAULT 'approved'
              CHECK (status IN ('proposed','approved','rejected')),
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_project_event_timeline ON project_event (project_id, occurred_on DESC)
  WHERE status = 'approved';
CREATE TRIGGER trg_project_event_updated BEFORE UPDATE ON project_event
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`Requirements.md` 6.7 のセミオート方式（AIが候補提示、PMが取捨選択）を `origin` と `status` で表現する。`proposal(kind='event')` が承認されるとこのテーブルへ昇格する。

### 8.3.4 分析用テーブル

```sql
CREATE TABLE estimate_record (
  id            char(26) COLLATE "C" PRIMARY KEY,
  ticket_id     char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  series        text NOT NULL CHECK (series IN ('human','ai','agent_cost','actual')),
  value         double precision NOT NULL,
  unit          text NOT NULL CHECK (unit IN ('point','hour','token','turn')),
  model_version text,
  recorded_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_estimate_ticket ON estimate_record (ticket_id, series);

CREATE TABLE contribution (
  ticket_id   char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  actor_id    char(26) COLLATE "C" NOT NULL REFERENCES actor(id)  ON DELETE CASCADE,
  score       double precision NOT NULL,
  signals     jsonb NOT NULL DEFAULT '{}'::jsonb,
  computed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ticket_id, actor_id)
);
CREATE INDEX idx_contribution_actor ON contribution (actor_id);
```

`estimate_record` を「チケット×系列」の縦持ちにしているのは、**人間の見積り・AI推定・エージェント実行コスト・実績を同じ構造で比較したい**ため（`Requirements.md` 6.1 / 10.5.5）。系列を列として横持ちにすると、系列追加のたびに `ALTER TABLE` が必要になる。

`contribution` は`Requirements.md` 6.4 の貢献度可視化に対応する。

---

# 9. 運用

## 9.1 バックアップ

| 方式 | 内容 |
|---|---|
| 論理バックアップ | `pg_dump -Fc` を日次。開発端末では compose の `profiles` で任意起動するサイドカー、またはホスト側 cron |
| 保持 | 直近7世代 |
| リストア確認 | 月1回、別DBへ `pg_restore` して起動確認する。**取得できているだけでは復元できる保証にならない** |
| K8s | CloudNativePG のバックアップ機能（オブジェクトストレージ＋WALアーカイブによるPITR） |

```bash
docker compose exec -T db pg_dump -U pb_owner -Fc pb > backup/pb_$(date +%Y%m%d).dump
```

## 9.2 ログと監視

| 項目 | 設定 |
|---|---|
| スロークエリ | `log_min_duration_statement = 200ms` |
| 接続状況 | `pg_stat_activity`（`application_name = 'pb'` で識別） |
| 統計 | `pg_stat_statements` を Phase 2 で有効化 |
| autovacuum | 既定のまま。`activity` / `audit_log` は追記のみで肥大するため、Phase 2 で監視対象に加える |

## 9.3 データ量の見積り

| テーブル | 1年後の想定行数（小規模利用） |
|---|---|
| `ticket` | 数千 |
| `comment` | 数万 |
| `activity` | 数十万（フィールド単位で記録するため最多） |
| `audit_log` | 数万 |

`activity` の肥大が最初に問題化する見込み。**Phase 2 で保持期間ポリシー（例：2年経過分をアーカイブテーブルへ移動）を検討する。** パーティショニング（`occurred_at` によるレンジ分割）は、その時点で必要なら導入する。

---

# 10. 未解決の検討事項

- `audit_log` に対する `UPDATE` / `DELETE` 権限を `pb_app` から剥奪するか（改ざん防止と、保持期間ポリシーによる削除運用の両立）
- `comment.author_id` の `NOT NULL` とユーザー削除の整合。「削除されたユーザー」システムアクターの生成タイミング（マイグレーションでの事前作成か、初回削除時の遅延生成か）
- `activity` の粒度。チケット1回の更新で何行増えるか、まとめ方（1リクエスト＝1行にJSONで差分を持つ案との比較）
- `sort_key`（LexoRank）の実装方式と再採番が必要になる境界条件
- ワークフローの `definition`（jsonb 原本）と正規化テーブルの同期方法。どちらを正とするか
- 日本語検索を `pg_trgm` から `pg_bigm` へ移行する判断基準（データ量・検索頻度・精度の不満）
- Phase 2 でエージェントが並行書き込みする際のトランザクション分離レベル（既定の Read Committed で足りるか、`task_lease` 取得時に `SELECT FOR UPDATE` が必要か）
- マルチテナント（スキーマ分離）を導入する場合の移行手順。Phase 1〜2 は単一テナント前提のためテナントID列を持たない
