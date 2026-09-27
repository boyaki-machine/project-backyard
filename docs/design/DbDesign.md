# Project Backyard (PB) データベース設計書

> 本書は PB のデータベースに関する**唯一の正本**である。スキーマ・マイグレーション・DB実行環境はすべて本書に集約する。
>
> **文書体系**：`Requirements.md`（要件）→ `Design.md`（全体設計）→ 本書 / `ApiDesign.md` / `GuiDesign.md`（領域別の正本）
>
> - 対象読者：サーバ実装者（人間およびAIエージェント）
> - 関連：`Design.md`（全体設計・認証設計）、`ApiDesign.md`、`GuiDesign.md`、`Requirements.md`
> - 適用済みのマイグレーションは 5.2 と 8章冒頭の一覧のとおり。8.3・8.4（構想）はテーブル構成案である

---

## 目次

| 章 | 内容 |
|---|---|
| 1 | 本書の位置づけ |
| 2 | PostgreSQL を使う理由と、DB の規約 |
| 3 | 実行環境（docker-compose / Kubernetes） |
| 4 | 共通規約 |
| 5 | マイグレーション運用 |
| 6 | スキーマ定義 |
| 7 | 初期データ |
| 8 | 拡張（文書・エージェント連携と、構想のDDL構成案） |
| 9 | 運用 |
| 10 | 未解決の検討事項 |

---

# 1. 本書の位置づけ

`Design.md` はシステム全体の設計を扱い、本書はそのうちデータベースに関する部分を**実装可能な粒度まで**降ろす。具体的には以下を含む。

- PostgreSQL のバージョン・拡張・ロール設計
- 開発端末での実行環境（docker-compose / Kubernetes）
- マイグレーションの運用規則とファイル構成
- 完全なDDL
- 初期データ（権限カタログ、ロール、ワークフローテンプレート、初期管理者）

---

# 2. PostgreSQL を使う理由と、DB の規約

## 2.1 PostgreSQL を使う理由

| 理由 | 内容 |
|---|---|
| 実装の自由度 | `jsonb` の内部検索・全文検索・トリガ・`RETURNING`・配列型をそのまま使える。複数の DB に対応するための制約を課さない |
| 実行環境が整っている | 開発端末で PB をコンテナとして動かす前提であり、DBコンテナを1つ増やす追加コストは小さい |
| エージェント並行アクセス | 複数のエージェントが同時に書き込む。単一ライタの制約を持たない |
| 拡張機能 | pgvector（`Requirements.md` 6.5 のRAG）、`LISTEN/NOTIFY`（`Requirements.md` 1章のリアルタイム更新）が最初から使える |

型と規約は 4.1（型の対応）と6章のDDLが正本である。

## 2.2 DB の規約

| 規約 | 理由 |
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
| DB既定 ctype | **`C.UTF-8`**（pg_trgm が日本語を語の文字として数えるため。4.5） |
| 日本語の並び順 | **ICU collation を明示指定**（4.4） |
| タイムゾーン | `UTC` |

| 拡張 | 用途 | 状態 |
|---|---|---|
| `pgcrypto` | ランダム値生成（`gen_random_bytes`） | 導入済み |
| `citext` | メールアドレスの大文字小文字を区別しない一意制約 | 導入済み |
| `pg_trgm` | 日本語を含む部分一致検索の高速化（4.5） | 導入済み |
| `vector` | 埋め込みベクトル（`Requirements.md` 6.5） | 構想 |

## 3.2 docker-compose（開発端末での既定構成）

```yaml
# deploy/base/compose.yaml（dev はこれをそのまま使う。stg は deploy/stg/compose.yaml を重ねる）
name: project-backyard

services:
  db:
    image: pgvector/pgvector:pg17
    restart: unless-stopped
    environment:
      POSTGRES_DB: pb
      POSTGRES_USER: pb_owner
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
      POSTGRES_INITDB_ARGS: "--encoding=UTF8 --locale=C --lc-ctype=C.UTF-8"
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
      dockerfile: deploy/Dockerfile  # `Design.md` 4.5。dev の make up は db だけを起動する
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
    # 第2層（ログ形式・ログレベル・ヘルスチェックのバージョン表示・Cookie の Secure）を
    # ここに書かない（`Design.md` 10.3）。書くと画面から変更できなくなる
    environment:
      PB_BIND: "0.0.0.0:8080"
      PB_DATABASE_URL_FILE: /run/secrets/app_database_url
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
- **`build.context` はリポジトリルートを指す。** compose ファイルの位置（`deploy/base/`）ではない。`deploy/Dockerfile` は `client/` と `server/` の双方をマルチステージでビルドするため、両方を含むルートを渡す必要がある（`Design.md` 4.1、4.5）。**材料として送るのは `VERSION`・`client/`・`server/` だけ**で、ルートの `.dockerignore` が名指しで許している
- **dev では app を起動しない。** `make up` は db だけで、PB 本体は `make run` でホストから動かす（`Development.md` 2.2）。**配布用の compose は `deploy/prod/compose/` にあり、base に重ねず単体で動く**（受け取った人は base を持たない。`Design.md` 4.5）

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

開発端末では compose を既定とする。**K8s 向けの一式は `make release TARGET=k8s` が出力する**（`Design.md` 4.5）。

**PostgreSQL は「試す」と「運用する」で分ける**。

| 段 | PostgreSQL | 位置づけ |
|---|---|---|
| **試す** | **素の StatefulSet 1台（PVC 付き）と Service。** `make release TARGET=k8s` がサンプルとして出す | 手元のクラスタで PB を動かしてみるためのもの。**バックアップ・フェイルオーバ・PITR を持たない** |
| **運用する** | **CloudNativePG オペレータ**、または**外部の PostgreSQL**（クラウドのマネージドサービスなど） | **サンプルの StatefulSet を運用へ持ち込まない。** CloudNativePG は `Cluster` リソースでバックアップ・フェイルオーバ・PITR を宣言的に扱える |

**サンプルのマニフェストには、外部の PostgreSQL へ繋ぐ設定をコメントアウトで併記する。** CloudNativePG の `Cluster` はサンプルにしない（同）。

| 対象 | 方式 |
|---|---|
| PB本体 | `Deployment`（レプリカ1、`strategy: Recreate`）＋ `Service` |
| マイグレーション | Deployment の initContainer。PB 本体と同じイメージの `/goose` を使う |
| 設定 | `ConfigMap` の `pb.yaml` を `PB_CONFIG_FILE` で読ませる。**第2層のキーは書かない**（`Design.md` 10.3） |
| 資格情報 | `Secret` を**環境変数で渡す**。PB 本体は `PB_DATABASE_URL`、goose は `PGPASSWORD`（接続先はパスワードを含めずに引数へ書く）。**CloudNativePG が作る `<Cluster名>-app` の Secret は所有者（`pb_owner`）の資格情報であり、`pb_app` の分は別に作る**（3.4 のロール分離） |
| 公開 | ローカル利用は `kubectl port-forward`。Ingress を張る場合はTLSを必須とする |

運用で CloudNativePG を使うときの `Cluster` の概略を下に置く。**この構成は確かめていない。**

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
      localeCType: C.UTF-8
  storage: { size: 10Gi }
```

**マイグレーションを initContainer に置くか Job にするか**は、レプリカ数を2以上にした時点で問題になる（同時実行）。**レプリカ1のため initContainer で足りる**。

**同時実行は、レプリカ1と `strategy: Recreate` で起こさない。** RollingUpdate では新旧の Pod が並ぶ時間ができ、新しい Pod の initContainer がスキーマを進めたあとも、古い版の Pod が新しいスキーマの上で動く。**代償として、版を入れ替えている間は PB が止まる。**

**goose の CLIには、アドバイザリロックを有効にする引数が無い。** 5.3 の「ツールのアドバイザリロック」は、いまの一式では効いていない。**再検討の条件は、レプリカを2以上にするとき**——そのとき migrate を `Job` へ分けるか、ロックを持つ呼び方（goose をライブラリとして呼ぶ）にするかを決める。

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

### PB が `pb_owner` で繋ぐのは、取り込みのあいだだけである

**PB の設定に `pb_owner` の資格情報を置かない。** 環境変数にも `pb.yaml` にも置き場を作らない。
**平常時の PB は `pb_app` の接続しか持たない。**

**書庫の取り込み（9.1.1）だけが DDL を要る。** そのときは**画面で人から資格情報を受け取り**、
取り込みのあいだだけ `pb_owner` の接続を張って、終わったら捨てる（`ApiDesign.md` 11.12）。
**保存しない。応答にもログにも出さない。**

**`GRANT pb_owner TO pb_app` による `SET ROLE` は採らない。** 一度与えると平常時も昇格でき、
**上の「インジェクションが成立してもテーブルを落とせない」が成り立たなくなる。**
昇格の口を常時開けるより、**操作のたびに人が資格情報を出すほうが、開いている時間が短い。**

**書き出しには要らない。** `pb_app` は `ALTER DEFAULT PRIVILEGES` により `public` の全表を
`SELECT` できる。

## 3.5 接続プール

| 項目 | 値 |
|---|---|
| アプリ側プール | 最小 2 / 最大 10（`sqlx::PgPoolOptions` 等） |
| `max_connections`（DB側） | 50 |
| `application_name` | `pb`。`pg_stat_activity` での識別に使う |
| ステートメントタイムアウト | 実行時ロールに `SET statement_timeout = '15s'` を既定として付与（3.4 の initdb で `ALTER ROLE` する） |

PgBouncer は不要。単一プロセス・少人数利用のため。

---

# 4. 共通規約

## 4.1 型の対応

| 論理型 | PostgreSQL 型 | 備考 |
|---|---|---|
| ID | `char(26) COLLATE "C"` | ULID（Crockford Base32、26文字固定） |
| 日時 | `timestamptz` | 既定値 `now()`。**Go では `time.Time` で受ける**（sqlc の override。`pgtype.Timestamptz` を生成コードの外へ出さない。pb-224） |
| 日付 | `date` | 期限・開始日 |
| 真偽 | `boolean` | |
| 列挙 | `text` + `CHECK` | 2.3 のとおりENUM型は使わない |
| 短い文字列 | `text` | `varchar(n)` を使わない。長さ制約は `CHECK` で表現する |
| メール | `citext` | 大文字小文字を区別しない一意制約 |
| 構造化データ | `jsonb` | 既定値を `'{}'::jsonb` / `'[]'::jsonb` とし `NULL` を避ける |
| 数値 | `integer` / `double precision` | 金額を扱わないため `numeric` は不要 |
| ベクトル | `vector(n)` | 構想。専用テーブルに隔離（8.4） |

**`varchar(n)` を使わない理由**：PostgreSQL では `text` と `varchar` に性能差がなく、長さ変更が `ALTER TABLE` を要する。長さ制限は `CHECK (length(title) <= 200)` として表現し、変更時はCHECK制約の張り替えで済ませる。

## 4.2 ID を ULID とし `char(26) COLLATE "C"` で保持する

| 選択肢 | 長所 | 短所 |
|---|---|---|
| **ULID（`char(26)`）** | 時系列順、URL・ログで読める、コピペしやすい | 26バイト。`uuid` より10バイト大きい |
| UUIDv7（`uuid`） | 16バイト、標準型 | 表示が長く読みにくい（36文字表記） |

**ULID を採る。** インデックスサイズの差は想定する規模（数万行）では無視できる一方、開発中にIDを目視・コピペする頻度は高い。

`COLLATE "C"` を明示するのは、**ロケール依存の文字列比較を避けてBツリー比較を最速にする**ため。DB既定 collation を `C` にしているため冗長だが、将来DB既定を変えた場合の事故を防ぐため列に明記する。

**問い合わせで ID を `text` にキャストしない**。`char(26)` の列を `@x::text` と比べると、PostgreSQL は**列の側を `text` に変換して**比べるので、その列のインデックスが使えない。sqlc に型を伝えるためにキャストが要る場所では **`@x::pg_catalog.bpchar`**（配列は `::pg_catalog.bpchar[]`）と書く。**`::bpchar` と書くと sqlc が Go の型を `interface{}` にする**（修飾名でないと型の対応を引けない）。キャストが要らない場所（`t.ticket_id = @ticket_id` のように列と直接比べる）は、sqlc が列から型を推すので何も書かない。

| 比べ方 | コメント10万件から `ticket_id` で引く（generic plan） |
|---|---|
| `ticket_id = $1`（`$1` が `text`） | Seq Scan、14.3ms |
| `ticket_id = $1`（`$1` が `bpchar`） | Bitmap Index Scan、0.03ms |

**行数が少ないうちは差が見えない**——数百行ならどちらの形でも PostgreSQL は Seq Scan を選ぶので、小さなデータの実測や結合テストでは気づけない。

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
| **`pg_trgm` + GIN**（採用） | 標準contribで導入が容易。`ILIKE '%キーワード%'` を高速化できる。**語の文字が3つ以上続く部分を含む語にだけ効く**（2文字の語には効かない）。**日本語に効くのは DB の `LC_CTYPE` が `C` でないときだけ**（下記） |
| `pg_bigm` | 日本語向けbigram索引。2文字クエリに強い。カスタムイメージのビルドが必要 |
| PGroonga | 形態素解析・スコアリングまで対応。高機能だが導入と運用の重さが原則（軽快さ）と衝突する |

`pg_trgm` を使い、実運用で不足が確認された時点で `pg_bigm` へ移行する。**移行時に影響するのはインデックス定義と検索クエリのみで、スキーマ本体は変わらない**よう、検索は `store/search/` に隔離する（`Design.md` 4.6）。

**DB の `LC_CTYPE` を `C.UTF-8` にする**（3.1）。pg_trgm は「語の文字」（英数字）が続く部分からだけ trigram を作り、語の文字かどうかを **DB を作ったときの libc の `LC_CTYPE`** で決める。**`C` では日本語が語の文字にならず、日本語の語から trigram が1つも取れない**（`show_trgm('ログイン')` が空）ので、**日本語の検索ではインデックスが効かない**。

- **`LC_COLLATE` は `C` のまま**にする。並び順（バイト順、4.2）は変わらない
- **PostgreSQL 17 の builtin プロバイダ（`C.UTF-8`）では足りない。** ILIKE は全角の大小まで畳むようになるが、pg_trgm は libc の `LC_CTYPE`（`C`）を見るので日本語は取れない
- **ctype は DB を作るときにしか決められない。** 既存の DB は、別名で作って復元し名前を入れ替える（`deploy/prod/MANUAL.md` 6章、`Development.md` 11.6）。**移していない DB では、サーバが起動時に WARN を出す**
- **ILIKE で大小を区別しない範囲が広がる。** `C.UTF-8` では全角の英字なども畳む（`ApiDesign.md` 9.2.1）

**検索の問い合わせを2つの形で切り替える**（`store/search`）。どの語からも trigram を取れるなら、**語ごと・列ごとに当たる ID を集めて、すべての語に当たったものを残す形**（`SearchTicketIDsByTrigram`）。1つでも取れない語があれば、**チケットを1件ずつ読んで ILIKE を当てる形**（`SearchTicketIDs`）。取れない語でインデックスを使うと、インデックスが全件を返して**かえって遅くなる**ためである。**取れるかの判定は DB の ctype を読んで変える**——`C` のままの DB では英数字だけを語の文字として数え、日本語の語は後者へ回す（移していない環境で遅くならない）。**2つの形は同じ集合を返す**（合成データで語の組み合わせ10通りを突き合わせ、差は0件）。

| 検索（dev、チケット3万・コメント10万） | 1件ずつ ILIKE | trgm を使う形 |
|---|---|---|
| まれな日本語の語×1（`LC_CTYPE=C.UTF-8`） | 237ms | **40ms** |
| まれな日本語の語＋よく出る語 | 274ms | **104ms** |
| よく出る日本語の語×2 | 290ms | **124ms** |
| まれな英数字の語×2（`LC_CTYPE=C`） | 259ms | **15ms** |
| 2文字の語だけ（`LC_CTYPE=C` で測定。2文字の語はどちらの ctype でも取れない） | 164ms | 211ms（**使わない**） |

**この規模では、チケット側の2本はプランナが全件スキャンを選ぶことが多い**（効いているのは主にコメント側）。件数が増えれば索引を選ぶようになる。

**2文字の語は速くならない。** 日本語は2文字の語が多いので、**2文字の語の検索が遅い・使えないと実際に言われたとき**に `pg_bigm` への移行を再検討する（下の10章）。そのとき変わるのはインデックス定義と `search.sql` と `store/search` の判定だけである。

## 4.6 削除方針

| 対象 | 方針 |
|---|---|
| 既定 | 物理削除。監査は `audit_log` / `activity` に残る |
| `comment` | `deleted_at` を持ち「削除されました」表示を維持 |
| `knowledge`（構想） | `deprecated_at` で無効化し履歴を保つ |
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

**配布物には goose を別の実行ファイルとして同梱する**（`Design.md` 4.5）。`make release` が `server/tools` からビルドするので、**版はここで固定したものと同じになる。** postgres 以外のドライバはビルドタグで外す（全ドライバ入りで約 38MB、外すと 11〜12MB。タグの一覧の正本は `deploy/prod/build-release.sh`）。**`pb` 本体には入れない**——上に書いた理由（ツールの依存をアプリの依存グラフに混ぜない）がそのまま効く。

**`server/tools/go.mod` の `go` ディレクティブは 1.24 のまま据え置く。** 本体（`server/go.mod`）は 1.26 だが（`Design.md` 3.1）、ツールまで上げる理由は無い。`go get -tool` は依存を最新へ引き上げる際にこの値も書き換えることがあるので、ツールを追加・更新したら `head -3 server/tools/go.mod` で確認する。

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
├── 0010_seed_phase1.sql                権限カタログ、ロール、ワークフローテンプレート
├── 0011_audit_log_request_id.sql       audit_log.request_id を追加（6.8）
├── 0012_access_token_permission_cache.sql
│                                       access_token に実効権限のキャッシュ2列を追加（6.2）
├── 0013_tag.sql                        tag, ticket_tag（6.10）
├── 0014_dod.sql                        dod_item（6.11）
├── 0015_ticket_type_and_stage.sql      ticket.type を3値へ縮小、
│                                       ticket.staged_at を追加（6.6）
├── 0016_ticket_reference.sql           ticket_reference（6.12）
├── 0017_document.sql                   document, document_revision,
│                                       doc 権限, 文書テンプレート（8.1）
└── 0018_document_template_text.sql     文書テンプレートの初期本文を直す（8.1.2）
```

**0011〜0016 は後から足したものである**（`ApiDesign.md` 9章の確定にともなって、使う前にファイルだけ先に置いたものを含む）。**0017 以降は8章の一覧にある**。**0018 はDDLを持たず、0017 で入れた初期本文の誤りだけを直す**（8.1.2）。前進のみの規則（5.3）に従い、既存のファイルは編集していない。**ファイルを先に置くのは、`make sqlc` が `migrations/` をスキーマ源に読むため**である。

`project.workflow_id` と `ticket.sprint_id` は後続テーブルを参照するため、**FK制約のみ後から `ALTER TABLE ... ADD CONSTRAINT` で付与する**（0005 / 0009 の末尾）。PostgreSQL は前方参照を許さないためである。

## 5.3 運用規則

| 規則 | 内容 |
|---|---|
| **前進のみ** | `down` マイグレーションを書かない。誤りは新しいマイグレーションで修正する。ロールバックはバックアップからの復元で行う |
| 適用済みファイルを編集しない | 内容ハッシュが変わり、環境間で不整合になる |
| 破壊的変更は2段階 | ①新列を追加してアプリを両対応にする → ②デプロイ後に旧列を削除する（expand / contract） |
| 大規模テーブルへのインデックス | `CREATE INDEX CONCURRENTLY` を使う。**トランザクション外で実行する必要があるため、単独のマイグレーションファイルに分ける** |
| 同時実行の防止 | ツールのアドバイザリロック機能を有効にする（K8sで複数Podが同時起動した場合の保護）。**goose の CLI にはこの引数が無く、K8s の一式ではレプリカ1と `strategy: Recreate` で防いでいる**（3.3） |
| シードの冪等性 | `ON CONFLICT DO NOTHING` / `DO UPDATE` を使い、再実行しても壊れないようにする |

---

# 6. スキーマ定義

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

-- 認証プロバイダ定義（'local' のみ。OIDC/SAML は構想）
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
  client_info  text,
  cached_permissions    jsonb,       -- 0012 で追加。実効権限のキャッシュ
  permissions_cached_at timestamptz  -- 同上。TTL の起点
);
CREATE INDEX idx_access_token_actor  ON access_token (actor_id);
CREATE INDEX idx_access_token_active ON access_token (expires_at)
  WHERE revoked_at IS NULL;
```

**`token_hash` に `UNIQUE` を張ることが、認証の主経路になる。** 受け取った平文をSHA-256にして1回のインデックス探索で引く（`ApiDesign.md` 3.2）。

**`cached_permissions` は `Design.md` 6.4.5 が求める「セッションへのキャッシュ」の置き場である。** 入るのは 6.4.1 の式のうち**システムロールの層のみ**（`システムロールの権限 ∩ トークンのスコープ`）で、プロジェクトロールの層は入れない。プロジェクト個別の判定は「そのプロジェクトが在るか・当人がメンバーか」を同じクエリで確かめる必要があり、権限だけをキャッシュしてもクエリが1本も減らないためである。

この列を `access_token` に置いたことで、**認可のためにクエリが1本も増えない。** 認証は全リクエストが通る経路であり（6.2.2）、そこで引く行にキャッシュが載っていれば `RequirePermission` は追加のDBアクセスなしで判定できる。

両列とも `NULL` は「キャッシュが無い（未計算、またはロール変更で無効化済み）」を意味する。`'[]'` は「権限0件」であって別の状態である。無効化は**アクター単位で全トークンを対象に**行う。権限を変えられた本人は複数のセッションとAPIトークンを持ちうるので、1本だけ消しても残りの経路から旧権限で通れてしまう。

**この2列は 0012 で追加した**（`access_token` 自体の作成は 0002）。前進のみの規則（5.3）に従い、0002 は編集していない。

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

**`timezone`（基準タイムゾーン）と `holiday_source_id`（祝日の取得元）は 0046 で足した**（6.23）。

**`key` の形式検証をDBの `CHECK` にも置く。** アプリ側（`ApiDesign.md` 5.3）と二重になるが、URLとMCPエンドポイントに直結する値であり、不正値が入ると経路そのものが壊れるため。

**`settings` で定義するキーは `repositories` のみ**である。

```jsonc
{
  "repositories": [
    { "name": "本体", "url": "https://github.com/org/project-backyard",
      "description": "サーバとフロントの実装。コミットに PB-123 を書く" }
  ]
}
```

| | |
|---|---|
| `url` | 必須。`https://…` と `git@host:org/repo.git` の双方を受ける |
| `name` | 任意。画面に出す表示名。省略時は URL をそのまま見せる |
| `description` | 任意。**そのリポジトリとプロジェクトの関係**を利用者が書く（「これは〇〇のリポジトリ」） |
| 並び順 | 配列の順 |

**列にせず `settings` に置くのは、用途がまだ「画面にリンクを出す」「MCP がプロジェクト情報として返す」に限られるためである。** どちらも値を読んで返すだけで、一意制約・並び替え・結合を必要としない。リポジトリ単位のトークン発行や横断検索（`Requirements.md` 10.9）が要件になった時点で、`project_repository` テーブルへ移す。**逆にテーブルを先に作ると、要らなかったときに戻せない。**

**検証はフロントのみで、サーバは JSON オブジェクトであることしか見ない**（`ApiDesign.md` 5.5）。`settings` は自由形式だからである。

### 6.4.1 チケット採番

```sql
UPDATE project_counter
   SET last_ticket_seq = last_ticket_seq + 1
 WHERE project_id = $1
RETURNING last_ticket_seq;
```

1文で行ロックと採番が完了する。**シーケンス（`CREATE SEQUENCE`）を使わない**理由は2つある。①プロジェクトごとにシーケンスを作ると DDL が動的になる、②シーケンスはトランザクションが巻き戻っても値を消費するため**欠番が出る**。チケット番号は人が読む識別子であり、`my-app-31` の次が `my-app-33` になるのは望ましくない。

同一プロジェクトへの同時作成はこの行で直列化されるが、想定する規模では競合しない。

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
  type           text    NOT NULL          -- 0015 で3値へ縮小
                 CHECK (type IN ('epic','story','task')),
  title          text    NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body_md        text,
  status_key     text    NOT NULL,          -- workflow_status.key への論理参照
  priority       text    CHECK (priority IN ('lowest','low','medium','high','highest')),
  assignee_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  reporter_id    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  working_agent_id char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
                                            -- 0021 で追加。実行者の自己申告

  estimate_point double precision,
  estimate_hours double precision,
  actual_hours   double precision,
  actual_point   double precision,          -- 0044。ポイント単位の実績
  actual_point_version text,                -- 0044。例: actual-v0
  CONSTRAINT ck_ticket_actual_point CHECK (
    (actual_point IS NULL AND actual_point_version IS NULL)
    OR (actual_point >= 0 AND actual_point_version ~ '^actual-v[0-9]+$')
  ),

  start_date     date,
  due_date       date,
  sprint_id      char(26) COLLATE "C",
  sort_key       text,                      -- LexoRank 方式の並び順
  staged_at      timestamptz,               -- 0015 で追加。NULL＝バックログ

  -- エージェント連携（列のみ先行定義）
  -- 既定は 'agent_draft'（0025）
  execution_mode text    NOT NULL DEFAULT 'agent_draft'
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
CREATE INDEX idx_ticket_staged         ON ticket (project_id, staged_at)
                                       WHERE staged_at IS NOT NULL;  -- 0015 で追加
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

**`type` は `epic` / `story` / `task` の3値である**（0015 で縮小した）。

| 値 | 意味 |
|---|---|
| `task` | **最小の仕事単位。** 多段にできる（タスクの下にタスクを置いてよい） |
| `story` | **配下に複数のタスクを含むことを示す。** 機能上の差は無く、アイコンだけが違う（`GuiDesign.md` 5.4）。**子を持たない `story` を許す**——作りかけの状態として自然であり、子の有無で種別が入れ替わると履歴とフィルタが揺れる |
| `epic` | **グルーピング専用。** バックログに行として出さず、複数選択できるフィルタになる（6.10、`GuiDesign.md` 5.4） |

**`bug` / `phase` / `wbs` は 0015 で廃止した**。**使い分けの定義が本書にもどこにも無く**、実機で使うと選べないことが分かったためである。**バグは種別ではなくタグで表す**（6.10）。0015 は**既存行を `task` へ移してから `CHECK` を張り替える**。前進のみの規則（5.3）に従い 0006 は編集していない。

**`staged_at` は「オンステージ」を表す**（0015 で追加）。バックログ画面は上下二段で、**下＝バックログ（プロジェクトが行うべき仕事すべての保管庫）／上＝オンステージ（いま仕掛り中で、直近のスプリントで消化すべきもの）**である（`GuiDesign.md` 5.4）。`NULL` がバックログ、値が入っているものがオンステージで、値そのものは**いつ上げたか**を持つ。

**二段は `sort_key` を共有する。** 同じ「消化順」の部分集合であり、順序キーを2本持つと段を行き来するたびにどちらを更新するかを決めることになり、**戻したときの位置が失われる**（`ApiDesign.md` 9.4）。

**オンステージは進捗（`status_key`）とは独立した軸である。** 「未着手だがオンステージ」が表せる必要があるため、ステータスやスプリントでは代用しない。**段に置けるのは表示上のトップレベル**（親を持たないもの、または親がエピックのもの）だけで、配下は親と一緒に運ばれる（`GuiDesign.md` 5.4）。

**この2つの変更は 0015 で行った**（`ticket` 自体の作成は 0006）。バックログの実機確認で、種別の使い分けが定義されていないことと、「いま仕掛り中」を表す軸が無いことが判明したためである。

**`status_key` を物理FKにしない。** ワークフロー定義を差し替えた際に既存チケットが更新不能になるのを避けるため、整合性はアプリ層で検証する（本書 6.6）。

**`title` / `body_md` の trigram インデックス**が 4.5 の部分一致検索を支える。

**`sort_key` はサーバが LexoRank 方式で採番する。** 値の生成規則をアプリの1か所に閉じ込め、クライアント（Web・MCP・将来のCLI）には書かせない。並べ替えは専用のエンドポイント `POST /tickets/:seq/move` で行う（`ApiDesign.md` 9.4）。**順序はプロジェクト内で1本**であり、バックログのグループ化（親・タグ・スプリント）は表示上の区切りにすぎない。

**`parent_id` の循環禁止はアプリ層で検証する。** `ck_ticket_not_self_parent` が防げるのは自己参照（A→A）だけで、A→B→A のような循環は `CHECK` では表現できない。`ApiDesign.md` 9.5.2 が `parent_cycle` として `422` を返す。**階層の深さに上限は設けない**（表示側が5段でインデントを打ち切る。`GuiDesign.md` 5.4）。

**`closed_at` はステータス遷移の副作用としてのみ動く。** 遷移先の `workflow_status.category` が `done` なら設定し、`done` 以外へ戻したら `NULL` へ戻す（`ApiDesign.md` 9.6）。直接更新させないことで、一覧の「未完了」フィルタ（`closed_at IS NULL`）と集計が食い違わないようにする。

**`sprint_id` も同じく、直接更新させない**（0028 で足した 6.9.1）。スプリントの開始・終了の副作用としてのみ動き、`PATCH` は `422` を返す（`ApiDesign.md` 9.5.2）。**スプリントは「チケットにあらかじめ付ける属性」ではなく「いまどの期間で消化しようとしているか」である**——付け替えはチケット1件ずつではなく、**オンステージ全体に対して1回起きる**。

**`staged_at` は、状態が未着手カテゴリを出たときにも立つ**（`ApiDesign.md` 9.6）。着手したものがバックログ段に残っていると、「オンステージだけを見れば仕掛りが全部わかる」という段の約束（`GuiDesign.md` 5.4）が崩れるためである。**手で上げる操作（9.4.1 の `staged`）は残る**——「未着手だがオンステージ」を表す軸としての役割は変わらない。

### `working_agent_id` — 誰が実際に処理しているか（0021 で追加）

**担当（`assignee_id`）と実行者を別の列にする**。

| 列 | 表すもの | 誰が書くか | いつ消えるか |
|---|---|---|---|
| `assignee_id` | **誰の仕事か**（責任者） | 人が割り当てる | 担当を外したとき |
| `working_agent_id` | **誰が実際に処理しているか**（実行者） | **エージェントが自分で宣言する** | **消えない。人が消す** |

**1列では両方を表せなかった。** `assignee_id` は `actor` を参照するのでエージェントを入れられ、`GuiDesign.md` 5.4 は「担当がエージェント」を想定した表示を定めている。ところが `Design.md` 8.5は「**担当は人が持つ**」と決め、MCP の `assignee=me` を所有者へ写した。**「田中の担当だが claude が処理している」を表す欄が無かった**のが食い違いの正体で、列を分けると両方が正しくなる。

**この列は権限判定に使わない。** エージェントが状態を変えてよいかは `assignee_id` が所有者かどうかで決まる（`ApiDesign.md` 9.6 の検証6）。**人がいつでも消せる列を判定に使うと、消しただけで作業が止まる。**

**エージェントの宣言は遷移のときに自動で立つ**（`ApiDesign.md` 9.6）。専用の操作を持たないのは、「着手した」と「宣言した」が別々に起こる状態を作らないためである。**チケットを消化しても消さない**——「このチケットは誰が処理したか」は完了後にこそ読みたい情報である（`activity` を辿らずに1列で分かる）。担当を付け替えるように、人が `PATCH` で消すか差し替える（`ApiDesign.md` 9.5.2）。

**`ON DELETE SET NULL` は `assignee_id` と同じ。** エージェントを削除しても、そのエージェントが処理したチケットは残る。

#### `task_lease` を採らなかった

**8.2.2 の `task_lease` は使わない**。`Requirements.md` 10.3.3 のリースが解こうとしていた3つを分解した結果である。

| 解こうとしていたもの | 扱い |
|---|---|
| **可視性**（いま誰が触っているか） | `assignee_id` ＋ `working_agent_id` ＋ `status_key` で足りる。リースの TTL（30分）は**エージェントのセッションの時間尺度**であり、PB が目指す分野横断のプロジェクト管理には合わない |
| **排他**（同じチケットを2つのエージェントが同時に処理しない） | **発生しない。** `/pb-implement <seq>` は人がチケット番号を指定し、方針の承認を経てから走る。エージェントが自律的に拾うのは `pb_next_task`（構想） |
| **詰まり防止**（放置された占有を解く） | 占有しないので詰まらない |

**再検討の条件は「自律取得（`pb_next_task`）を実装するとき」である。** そのときは `working_agent_id` を「宣言」から「条件」へ格上げすればよく（自分でなければ拒む）、**テーブルを足さずに済む。** TTL による失効（`stale` の検知）が要ると分かった時点で、8.2.2 の器を起こす。

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
  agent_run_id char(26) COLLATE "C",          -- FK は 0022 が付与（8.2.4）
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

**`agent_run_id` の FK は 0022 で付いた**（8.2.4）。**埋まるのは `pb_submit_result` が作る
完了レポートのコメントだけである**（`ApiDesign.md` 9.15）——作業中の `pb_post_note` は
run を持たない。

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
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
  request_id  char(26) COLLATE "C"   -- 0011 で追加。activity と同じ形
);
CREATE INDEX idx_audit_time   ON audit_log (occurred_at DESC);
CREATE INDEX idx_audit_action ON audit_log (action, occurred_at DESC);
CREATE INDEX idx_audit_actor  ON audit_log (actor_id, occurred_at DESC);
```

**`ip` に `inet` 型を使う。** IPアドレスの正規化とサブネット検索がDB側でできる。

`actor_label` を持たせるのは、**ユーザー削除後に「誰を消したか」を追えなくなることを防ぐ**ため（`ApiDesign.md` 6.5）。

**`request_id` は `activity` と同じ意味・同じ型で持つ。** `ApiDesign.md` 2.5 のエラー応答と `Design.md` 10.1 のアプリケーションログを、同一リクエストの監査記録と突き合わせるための列である。

`audit_log.id` を `request_id` と同じ値にする案は採らない。**1リクエストが複数の監査行を書く**ためである（例：`POST /me/password` は `password.change` と `session.revoke` の2行。`Design.md` 6.3「パスワード変更時に当該ユーザーのセッションを全失効」）。主キーでは表現できない。

CLI（`pb admin create`）由来の記録には HTTP リクエストが存在しないため `request_id` は NULL になる。`ip` / `user_agent` / `token_id` も同様。

**この列は 0011 で追加した**（`audit_log` 自体の作成は 0008）。監査ログの共通基盤を実装した時点で、上記の突き合わせ手段が無いことが判明したためである。前進のみの規則（5.3）に従い、0008 は編集していない。

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

**スプリントの CRUD を開ける**（`ApiDesign.md` 9.12）。表だけあって作る手段が無いと、チケット詳細のスプリント欄が常に空のドロップダウンになるためである。**定義**はプロジェクト設定のスプリントタブ、開始・終了はバックログで行う。バーンダウン・ベロシティは進捗分析（構想。`GuiDesign.md` 10章）が持つ。

**スプリントを動かす主体はオンステージである**。**定義**（名前・期間を作る）はプロジェクト設定のスプリントタブに残り、**運用**（開始・終了）はバックログのオンステージ段が持つ。チケット詳細のスプリント欄は**読み取り専用**になる（`GuiDesign.md` 5.5）。

### 6.9.1 チケットとスプリントの所属（0028）

```sql
CREATE TABLE ticket_sprint (
  ticket_id  char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  sprint_id  char(26) COLLATE "C" NOT NULL REFERENCES sprint(id) ON DELETE CASCADE,
  added_at   timestamptz NOT NULL DEFAULT now(),
  removed_at timestamptz,
  PRIMARY KEY (ticket_id, sprint_id)
);
CREATE INDEX idx_ticket_sprint_sprint ON ticket_sprint (sprint_id);

-- 既存の ticket.sprint_id を所属の履歴へ写す
INSERT INTO ticket_sprint (ticket_id, sprint_id)
SELECT id, sprint_id FROM ticket WHERE sprint_id IS NOT NULL
ON CONFLICT DO NOTHING;
```

**1つのチケットは複数のスプリントに属しうる**。スプリントは「消化するための一定期間」であり、**期間内に終わらなかったチケットは次のスプリントへ持ち越される**。持ち越しは、あるスプリントで何件が企画され何件が消化されたかを数える材料なので、**上書きせず履歴として残す**。

**`ticket.sprint_id` は捨てず、「いま属しているスプリント」を指す。** 所属の履歴は本表が持ち、`sprint_id` はそのうち最新の1件を指す**非正規化された写し**である。捨てなかったのは、`ApiDesign.md` 9.12 の `ticket_count` / `closed_count`、9.2.1 の `sprint` フィルタ、`GuiDesign.md` 5.4 のグループ化が、いずれも「**いま**属しているのはどれか」しか要らないためである。**結合で書き直しても答えは変わらず、読む側だけが複雑になる。**

**`sprint_id` は `PATCH` では動かない**（`ApiDesign.md` 9.5.2）。スプリントの開始・終了の副作用としてのみ動く。**`closed_at` と同じ扱いである**（6.6）——直接更新させないことで、本表と `sprint_id` が食い違わないようにする。

| 列 | 意味 |
|---|---|
| `added_at` | そのスプリントの対象になった時刻。**スプリント開始時**に入る |
| `removed_at` | そのスプリントを離れた時刻。**スプリント終了時に、完了・未完了を問わず入る** |

**`removed_at` を「未完了のときだけ立てない」という区別はしない。** 本表が答えるのは「**そのスプリントの対象だった期間**」であって「消化できたか」ではない。消化できたかは `ticket.closed_at` と `sprint.end_date` の突き合わせで後から言える。**1つの列に2つの問いを答えさせない。**

**行は消さない。** スプリントを削除したときだけ `ON DELETE CASCADE` で落ちる（`sprint` 自体が消えるので、属していた期間も意味を失う）。9.12 の `DELETE` が `ticket.sprint_id` を `SET NULL` にするのと揃う——**どちらもチケットは消えない。**

## 6.10 タグ（0013）

```sql
CREATE TABLE tag (
  id         char(26) COLLATE "C" PRIMARY KEY,
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  name       text        NOT NULL CHECK (length(name) BETWEEN 1 AND 30),
  sort_order integer     NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_tag_project_name UNIQUE (project_id, name)
);
CREATE INDEX idx_tag_project ON tag (project_id, sort_order);
CREATE TRIGGER trg_tag_updated BEFORE UPDATE ON tag
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE ticket_tag (
  ticket_id char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  tag_id    char(26) COLLATE "C" NOT NULL REFERENCES tag(id)    ON DELETE CASCADE,
  PRIMARY KEY (ticket_id, tag_id)
);
CREATE INDEX idx_ticket_tag_tag ON ticket_tag (tag_id);
```

**タグは階層とは別の軸である。** 「この仕事はどの大きな仕事の一部か」には `ticket.parent_id` が答え、答えは必ず1つになる。「この仕事はどういう性質のものか」にはタグが答え、答えは0個から複数になる。前者は分解（WBS・進捗のロールアップ）を、後者は横断的な分類（「GUI系」「設計」「MCP系」）を担う。

**1つの軸に混ぜない理由。** 「ログイン画面のCSS」は *認証エピックの一部* であると同時に *GUI系* でもある。階層1本に押し込むと、機能エピックの配下に置くか領域エピックの配下に置くかを毎回選ぶことになり、どちらか一方の見え方が失われる。他ツールでエピックが「分解の単位」と「分類ラベル」を兼ねて破綻するのは、この1軸化が原因である。

**グルーピングの実体は `ticket.parent_id` とタグであり、`ticket.type` ではない**（6.6）。**ただし `type='epic'` だけは画面が特別扱いする**——バックログに行として出さず、複数選択できるフィルタになる（`GuiDesign.md` 5.4）。エピックを選んで絞り込むと、その部分木に限られる（`ApiDesign.md` 9.2.1 の `parent`）。**絞り込みの実体は `parent_id` のまま**であり、この特別扱いは**表示の規約であってデータモデルの変更ではない**。

**バグは種別ではなくタグで表す**（0015 で `bug` を廃止した。6.6）。「この仕事はどういう性質のものか」に答えるのはタグであり、上の「1つの軸に混ぜない理由」がそのまま当てはまる。バグを種別に持たせると、「GUI のバグ」を種別・階層・タグのどれで表すかを毎回選ぶことになる。

| 判断 | 理由 |
|---|---|
| **色の列を持たない** | `GuiDesign.md` 8.6 が「ラベルに任意色を許さない」と定めている。ユーザーごとに色の意味が食い違い、一覧が虹色になって輝度による階層が崩れる |
| **`description` を持たない** | 30文字の名前で足りる範囲から始める（`GuiDesign.md` 設計原則3） |
| **`tag_group` を作らない** | 「領域」「工程」のような**タグの軸**を導入すると、軸ごとに単一選択を強制でき、グループ化で重複表示が起きなくなる。ただしその必要性は、バックログを実際に使ってみるまで分からない。**フラットなタグから移行するには `tag.group_id` を足す前進マイグレーション1本で済むが、逆は難しい**（10章） |
| **`ticket_tag` に `id` を持たない** | 複合主キーで足りる。付け外しは常に `(ticket_id, tag_id)` の組で行い、行そのものを参照する箇所がない |

**`ticket_tag` の付け外しは `ticket.updated_at` を動かす。** これはアプリ側の責務である（`ticket_tag` への更新は `ticket` のトリガでは拾えない）。動かさないと、タグだけを変えた場合に一覧の ETag が変わらず `304` が返り続ける（`ApiDesign.md` 9.2.5）。

## 6.11 完了条件（0014）

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

**完了条件は基本の表である。** 手動のチェックリストは AI 抜きでも人間だけで価値があり、`Requirements.md` 10.1.1「チケットは依頼メモから実行契約へ」の土台にもなる。

**列と `CHECK` は他の型も入る形で作り、API が受け付ける `type` だけを `manual` に絞る**（`ApiDesign.md` 9.9）。後から列を足すより、使わない列を持つほうが安い。`assertion`（コマンド実行）・`artifact`（成果物の存在確認）・`review`・`task_ref` は未実装である（`Requirements.md` 10.5.2）。

## 6.12 チケットの外部参照（0016）

```sql
CREATE TABLE ticket_reference (
  id          char(26) COLLATE "C" PRIMARY KEY,
  ticket_id   char(26) COLLATE "C" NOT NULL REFERENCES ticket(id) ON DELETE CASCADE,
  kind        text NOT NULL CHECK (kind IN ('code','doc')),
  label       text,
  url         text,
  repository  text,
  branch      text,
  commit_sha  text,
  note        text,
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  sort_order  integer NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_ticket_reference_doc  CHECK (kind <> 'doc'  OR url        IS NOT NULL),
  CONSTRAINT ck_ticket_reference_code CHECK (kind <> 'code' OR repository IS NOT NULL)
);
CREATE INDEX idx_ticket_reference_ticket ON ticket_reference (ticket_id, kind, sort_order, created_at);
CREATE TRIGGER trg_ticket_reference_updated BEFORE UPDATE ON ticket_reference
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**チケットから「外」を指す参照を1つの表にまとめる。** `ticket_link`（6.6）が
**チケットどうし**をつなぐのに対し、こちらは**リポジトリ・コミット・仕様書**のように
PB の外にあるものを指す。`ticket_link` は `target_ticket_id` に FK を持つため、
外部URLを入れる余地がそもそも無い。

| `kind` | 何を指すか | 必須 | 書き手 |
|---|---|---|---|
| `code` | リポジトリ・ブランチ・コミット | `repository` | **エージェント**（`GuiDesign.md` 5.5） |
| `doc` | 仕様書などのURL | `url` | 人 |

**1つの表にする。** 画面では隣り合って並び（`GuiDesign.md` 5.5）、API も1本で足りる。
2つに分けると API・クエリ・画面のセクションがすべて2系統になり、得られるのは
型の厳密さだけである。**その代償として `kind` ごとに使わない列が NULL になる**ので、
**必須項目は `CHECK` で DB に守らせる**（アプリ側の検証と二重にする）。

**`origin` 列は持たない。** 書き手は `created_by` から `actor.kind`（`user` / `agent` /
`system`。6.1）で分かる。**ただし画面はこれを表示しない**——`GuiDesign.md` 5.5
が書き手のアイコンを出さないと決めたため。**列とAPIの応答は
残す**（将来区別したくなったときに遡れるようにする）。`ticket_link` と `dod_item` は `origin` を持つが、あちらが表すのは
**「AI の提案か、確定した事実か」**という別の軸である（`ai_suggested` は承認待ちを意味する）。
ここに同じ列を置くと、**「誰が書いたか」を2か所に持つことになり、必ずずれる。**

**`kind='code'` は追記されて積み上がる。** エージェントが作業の経過として
「このブランチで始めた」「このコミットを積んだ」を残していくため、1チケットに複数行が並ぶ
（`Requirements.md` 10.6.1 の構造化完了レポートにある `artifacts` にあたる）。
**並びは `sort_order` ではなく `created_at` が実質の軸**になるので、索引に両方を入れてある。

**`repository` はリポジトリを識別する文字列であり、FK ではない。** リポジトリの定義は
`project.settings` の `repositories`（6.4）に jsonb で置かれており、**参照できる主キーが無い**。
表記を突き合わせる責務はアプリ側にも置かない——**プロジェクト設定に無いリポジトリ名を
書いても受け付ける**。エージェントが作業した事実のほうが、設定の登録漏れより優先する。

**画面から `code` は追加しない。** 書き手は、MCP の `pb_add_reference` を使うエージェントと、
**`/me/tokens` で発行した API トークンを持つクライアント**である（`ApiDesign.md` 4.4）。
画面が持つのは**表示と削除**だけで、誤って積まれた行を人が始末できるようにする
（`GuiDesign.md` 5.5）。`doc` は人が画面で追加・編集できる。

### 6.12.1 権限（0027）

```sql
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('ticket.reference.edit', 'ticket', 'チケットの外部参照の編集', 27)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- administrator は 7.3 の「全権限」SELECT で自動的に付く（再実行する）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- ticket.edit を持つロールへそのまま配る（退行を出さないため。下記）
INSERT INTO role_permission (role_key, permission_key)
SELECT role_key, 'ticket.reference.edit' FROM role_permission
 WHERE permission_key = 'ticket.edit'
ON CONFLICT DO NOTHING;
```

**外部参照の更新系（`POST` / `PATCH` / `DELETE`）を `ticket.edit` から切り出す**（`ApiDesign.md` 9.10.2）。読みは `ticket.view` のままである。

| ロール | `ticket.edit` | `ticket.reference.edit` |
|---|---|---|
| `administrator` | ✓ | ✓ |
| `project_admin` | ✓ | ✓ |
| `operator` | ✓ | ✓ |
| `project_member` | ✓ | ✓ |
| `project_viewer` | — | — |

**分けた理由は、エージェントに開けたい範囲がここで初めて `ticket.edit` より狭くなったからである。** 6.12 は「`kind='code'` の書き手は**エージェント**」「作業の経過として追記されて積み上がる」と定めているのに、**エージェントのトークンに載せられる権限の許可リスト**（`ApiDesign.md` 4.5.3）は `ticket.edit` を含まない。**設計文書が「エージェントが書く」と定めた行を、エージェントが書けない**状態だった。

**許可リストへ `ticket.edit` を足す案は棄却した。** `ticket.edit` は外部参照だけでなく `PATCH /tickets/:seq`（本文・担当・期日の書き換え）・`move`（並べ替え）・DoD（9.9）・チケット間リンク（9.10.1）も開ける。**「作業の跡を積む」ために「チケットの中身を書き換える」威力まで渡すことになる。** しかも一度許可リストに入れた権限は、**発行済みのトークンがある分だけ後から狭めにくい**——`access_token.scopes` は発行時に固定される jsonb 列である（6.2）。

**`ticket.edit` を持つロールへ機械的に配るのは、退行を出さないためである。** いま画面から `doc` 参照を編集できている人（`GuiDesign.md` 5.5）が編集できなくなってはならない。**`SELECT ... FROM role_permission WHERE permission_key = 'ticket.edit'` と書くことで、0010 以降にロールが増えていても取りこぼさない**——キーを並べて書くと、増えたロールを書き漏らしたときに黙って権限が落ちる。

**この権限は、エージェント用トークンの既定スコープに入る**（`Design.md` 6.5）。`doc.edit` のように発行時に選ぶ形は採らない——**作業の跡を残すのは実装エージェントの通常の仕事**であり、既定から外すと「コミットを記録できないエージェント」が既定になる。

**既に発行されているトークンには入らない。** `access_token.scopes` は発行時に固定されるので、この権限を使うには**トークンを発行し直す**必要がある（`ApiDesign.md` 4.5.3）。

## 6.13 チケットの自己編集の権限（0029）

```sql
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('ticket.self_edit', 'ticket', 'チケットの記述の編集（エージェントに開ける範囲）', 28)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- administrator は 7.3 の「全権限」SELECT で自動的に付く（再実行する）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

-- ticket.edit を持つロールへそのまま配る（退行を出さないため）
INSERT INTO role_permission (role_key, permission_key)
SELECT role_key, 'ticket.self_edit' FROM role_permission
 WHERE permission_key = 'ticket.edit'
ON CONFLICT DO NOTHING;
```

**`PATCH /tickets/:seq` と DoD の更新系を、エージェントに開ける範囲だけ `ticket.edit` から切り出す**（`ApiDesign.md` 9.5.2 / 9.9）。**0027 の `ticket.reference.edit` と同じ形である。**

**理由は 6.12.1 と同じである。** エージェントは起票できるので、**起票したあと直せなければならない。** 仕様の矛盾を最初に踏むのは実装する側であり、直す手段が無ければ修正案をコメントに置いて人に貼り替えてもらうしかない。

**許可リストへ `ticket.edit` を足す案は、0027 のときと同じ理由で棄却した。** `ticket.edit` は 9.5.2 の全項目を開ける——そこには `execution_mode` / `readiness` / `readiness_note` / `scope` が含まれる。**これらはエージェントを縛る側が書くものであり**（9.5.2「スコープ境界は縛る側が書くものである」）、**自分で緩められては意味がない。**

**この権限が開けるのは、9.5.2 のうち次の集合だけである**（正本は `ApiDesign.md` 9.5.2）。

| 開ける | 開けない |
|---|---|
| `title` `body_md` `priority` `parent_seq` `assignee_id` | `type` `execution_mode` `readiness` `readiness_note` `scope` |
| `tag_ids` `estimate_point` `estimate_hours` `start_date` `due_date` | `working_agent_id` `actual_hours` `sprint_id` |

**線は「作れるものは直せる。ただし `type` を除く」である。** 起票（9.3）で選べる項目を直せないのは筋が通らないが、**種別の切り替えは人が行う**。直せる項目には `tag_ids` と見積・日付も含む。

**`type` を外したのは、切り替えの影響が記述の修正に収まらないからである。** エピックはバックログに行として出ず、複数選択できるフィルタになる（6.6、`GuiDesign.md` 5.4）。**タスクをエピックへ変えると、その行は一覧から消えてフィルタの選択肢になる**——エージェントが記述を整えるつもりで盤面の見え方を変えてしまう。**起票のときに選ぶのは、まだ盤面に無いものについての選択なので事情が違う。**

**`working_agent_id` を開けないのは、あれが自己申告の欄だからである**（6.6）。遷移の副作用として自動で立つので（`ApiDesign.md` 9.6）、書く経路をもう1つ作る理由が無い。**`actual_hours` は `pb_submit_result` の `cost` と二重になる**ため開けない。**`sprint_id` は 0028 以降どの経路からも書けない**（9.5.2 の `use_sprint_endpoint`）。

**DoD は `body` の追加・編集・削除までで、`is_satisfied` は開けない**（`ApiDesign.md` 9.9）。**`pb_submit_result` が「盤面を動かさない」と決めた判断と正面からぶつかる**ためで、完了の判定は人が行う。

**0044 の `ticket.actual_point.edit` は実績ポイントと算出式の版だけを更新する追加権限である。** `project_admin` と `administrator` に配り、PM のエージェントは発行時にこの scope を選べる。通常の `ticket.self_edit` や `ticket.edit` だけでは書けない。2項目は対で更新し、DB の CHECK で非負値と `actual-v<番号>` の版を保証する。

**0044 は既存の reference コメント「実績 v0（たたき台）」の JSON から移す。** `method = actual-v0` と数値の `point` を採り、`point = null` は空欄のまま残す。stg で移行前に123件を確認し、114件が数値、9件が null だった。コメントは根拠として保持する。

**`ticket.self_edit` は `ticket.edit` の部分集合であって、上位ではない。** `ticket.edit` を持つ人は本表の「開ける」側も当然に編集でき、**画面の振る舞いは何も変わらない。**

**この権限は、エージェント用トークンの既定スコープに入る**（`Design.md` 6.5）。`ticket.reference.edit` と同じ判断で、**起票したチケットを直すのは実装エージェントの通常の仕事**である。**既に発行されているトークンには入らない。**

## 6.14 アプリケーション設定（0031）

```sql
CREATE TABLE app_setting (
  key         text PRIMARY KEY,
  value       text NOT NULL,
  updated_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT app_setting_key_format CHECK (key ~ '^[a-z][a-z0-9_]{0,62}$')
);

CREATE TRIGGER app_setting_touch BEFORE UPDATE ON app_setting
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**`Design.md` 10.3 の第2層の置き場である。** 第1層（接続文字列・待受）はここに置けない——**接続文字列は DB の中にあり得ない**ためで、第1層は環境変数に残る。第3層（TLS の秘密鍵）は**本表ではなく専用の表**に置く（値が長く、暗号化した列と有効期限を持つため）。

**行が無いことが既定値である。** 起動時に行を投入しない。既定値の正本は Go 側の設定レジストリであり、**DB とコードの2か所に既定値を持たない。**

### `value` を `text` の1列にし、型の列を置かない

**型・既定値・検証規則は Go 側の設定レジストリだけが持つ。** 型を列に持つと、`integer` と書かれた行に `true` が入ったときにどちらが正しいかを決める根拠が無くなる。**書き込みの経路は `PUT /api/v1/admin/settings`（`ApiDesign.md` 11.2）の1本だけ**であり、そこがレジストリを引いて検証する。

### キーの許可リストを CHECK に書かない

**CHECK は書式だけを見る。** 許可リストを DDL に書くと、**設定を1件足すたびにマイグレーションが要る**ことになり、本表を選んだ理由の3つめ（「設定を1件足すのがスキーマ変更でなくなる」。`Design.md` 10.3）が消える。

**レジストリに無いキーの行は、読む側が警告を1行出して無視する。** これは**古いバイナリへ戻したときに落ちない**ためである——新しい版が書いた行が、知らないキーとして残る。

### `updated_by` を置く理由

**`audit_log` と二重に見えるが、読む権限が違う。** 変更の記録は `audit_log` に残る（6.8）が、それを読むには `auditlog.view` が要る。**設定画面を開ける人（`system.settings`）が、いま出ている値を誰がいつ変えたかを見られるようにする**ため、行にも持つ。**値の履歴は持たない**——履歴が要るなら `audit_log` を見る。

### 秘密を本表に置かない

**`value` は平文である。** 秘密（パスワード・トークン・接続文字列・秘密鍵）を本表に入れない。**`pg_dump` がそのまま運ぶ**ためで、これは規約「秘密をコードや文書に書かない」と同じ理由による。第3層は暗号化した専用の表を使う。

## 6.15 TLS 証明書（0032）

```sql
CREATE TABLE tls_certificate (
  id             char(26) COLLATE "C" PRIMARY KEY,
  common_name    text NOT NULL,
  dns_names      text[] NOT NULL DEFAULT '{}',
  not_before     timestamptz NOT NULL,
  not_after      timestamptz NOT NULL,
  serial_number  text NOT NULL,
  fingerprint    text NOT NULL,
  is_self_signed boolean NOT NULL,

  -- 証明書の連鎖は平文で持つ。公開されるものであり、隠す意味が無い。
  cert_pem       text NOT NULL,

  -- 秘密鍵は secret_key で暗号化して持つ（Design.md 6.6.1）。
  key_ciphertext bytea NOT NULL,
  key_nonce      bytea NOT NULL,
  key_id         text NOT NULL,

  uploaded_by    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT tls_certificate_period CHECK (not_before < not_after),
  CONSTRAINT tls_certificate_fingerprint_unique UNIQUE (fingerprint)
);

-- 出す証明書の選定（6.6.1）は now が期間に入る行から notBefore が最大のものを採る。
CREATE INDEX tls_certificate_period_idx ON tls_certificate (not_before DESC, not_after);

CREATE TRIGGER tls_certificate_touch BEFORE UPDATE ON tls_certificate
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**`Design.md` 10.3 の第3層で、`app_setting`（第2層）とは別の表である。** 分けたのは
①値が長い（PEM は数KB）②**暗号化した列と復号のための付帯情報を持つ** ③有効期間で
選ぶという固有の問い合わせがある、の3点による。**キーと値の表に混ぜると、`app_setting`
の「平文である」という前提が崩れる。**

### 証明書は平文、秘密鍵だけ暗号化する

**`cert_pem` を隠さない。** 証明書は TLS ハンドシェイクで相手に渡すもので、**隠す意味が無い。**
平文で持つことで、画面が発行者・期間・SAN を出すのに復号が要らなくなる。

**`key_ciphertext` は AES-256-GCM である。** 鍵は `secret_key`（第1層）。`key_nonce` は
行ごとに新しく生成し、`key_id` は**どの鍵で暗号化したかを識別する**（鍵を交換する日に、
どの行がまだ古い鍵かを引けるようにする）。**GCM は認証付きなので、改竄された行は
復号で失敗する。**

### 解析した値を列に持つ

`common_name` / `dns_names` / `not_before` / `not_after` / `serial_number` / `fingerprint` は
**登録時に `cert_pem` を解析して埋める。** PEM から毎回引き直さないのは、①選定の問い合わせ
（`not_before` / `not_after`）を SQL で書けるようにする ②画面の一覧が復号も解析もせずに
描ける、の2点による。

**`cert_pem` が正本で、これらは派生である。** 食い違ったら `cert_pem` が正しい——
**登録は1経路しかなく**（`ApiDesign.md` 11.4）、そこが両方を同時に書く。

### `fingerprint` に一意制約を置く

**同じ証明書を2回登録できないようにする。** SHA-256 の指紋で、**同じものを2枚持つと
「どちらを出したか」が `notBefore` では決まらなくなる**（同一なので同じ値になる）。
更新のつもりで同じ PEM を貼った利用者に、その場で気づかせる。

### `is_self_signed` は表示のためだけに持つ

**PB は検証の連鎖を辿らない**（`Design.md` 6.6.1）。この列は**画面に「自己署名」と
出すためだけ**で、振る舞いを変えない——フォーマル証明書と自己署名証明書の扱いは同じである。
判定は「発行者と主体が一致するか」の1点で行う。

### 行の削除はできるが、更新はできない

**登録と削除だけを開ける**（`ApiDesign.md` 11.4 / 11.6）。`PATCH` を作らないのは、
**証明書の中身を部分的に差し替えられるものが無い**ためである——変えたいなら新しいものを
登録する。`updated_at` とトリガを置いてあるのは、4.3 の規約に合わせた形式上のものである。

## 6.16 秘密の暗号鍵（0033）

```sql
CREATE TABLE app_secret (
  key_id      text PRIMARY KEY,
  secret      bytea NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT app_secret_len CHECK (octet_length(secret) = 32)
);
```

**PB が初回に生成する暗号鍵の置き場である**（`Design.md` 6.6.1）。`tls_certificate`
（6.15）の秘密鍵を AES-256-GCM で暗号化するのに使う。

### なぜ DB に置くのか

**利用者の操作を要らなくするためである**（利用者の決定）。
**環境変数を必須にすると、証明書を1枚登録するために端末と再起動が必要になり**、
「設定は WebGUI を第一の口とする」という方針（憲章「判断の記録」）と矛盾する。

**ファイルに置く案は K8s で壊れる。** Pod ごとに違う鍵ができ、互いに復号できない。

### 代償を隠さない

**既定では `pg_dump` に鍵と暗号文の両方が入る。** 暗号化の目的はバックアップや
レプリカの持ち出しから守ることなので、**既定ではその保護が効かない。**

**`PB_SECRET_KEY` を与えればこの表を読まない**（`Design.md` 6.6.1 の優先順）。
**効かないことを画面に出す**のが、この判断を成立させる条件である。

### 行は1つだけを期待するが、制約では縛らない

**鍵を交換する日に2行目が要る**（古い鍵で暗号化された行が残っているあいだ）。
`key_id` を主キーにしてあるのはそのためで、**いまは `generated` の1行だけを使う。**

**32バイトであることを CHECK で縛る。** AES-256-GCM の鍵長で、ここを外れた行は
アプリ側でも弾かれるが、**入れられないようにするほうが早く気づける。**

## 6.17 未確認の設定変更（0034）

```sql
CREATE TABLE pending_setting_change (
  id          char(26) COLLATE "C" PRIMARY KEY,
  previous    jsonb NOT NULL,
  expires_at  timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL
);
```

**締め出されうる設定を変えたときの「戻し方」を持つ表である**（`Design.md` 10.3）。
ネットワーク機器の `commit confirmed` と同じ形で、**期限内に「アクセスできました」が
押されなければ元の値へ戻す。**

### `previous` は戻す値をそのまま持つ

キーから値への対応を `jsonb` で持つ。**`null` は「行が無かった」を表す**ので、戻すときは
`app_setting` の行を消す（6.14 の「既定に戻す」と同じ操作になる）。

```json
{ "tls_enabled": null, "cookie_secure": "false" }
```

**1回の保存を1件として持つ**。**利用者から見て「さっきの
変更」は1つ**であり、1つだけ確認できたときに残りをどうするかという問いを作らない。

### 行は1つまでだが、制約では縛らない

**未確認が残っている間は次の危険な変更を受け付けない**（`ApiDesign.md` 11.2 が `409`
を返す）ので、実際には常に0行か1行である。**`id` を ULID にしてあるのは規約
（「ID は ULID」）に従うためで、単一行を DB で強制はしない**——強制すると、
**取り消しと作成が同時に来たときに誤りの形が分かりにくくなる。**

### なぜ DB に置くのか

**再起動をまたいで戻せるようにするためである**。

**プロセス内に持つ案は、締め出された人の行動で壊れる。** 画面へ入れなくなった人が
まず試すのは再起動であり、**そのとき記録が消えると、未確認のまま確定してしまう。**
**戻り道を消すのが復旧の試み自身である**という形になり、最悪である。

**期限は `expires_at` が正本**で、プロセス内のタイマがこれを見る。

**ただし起動時は期限を見ない**（改訂）。**未確認は全部戻す**——
締め出された人が最初に試すのは再起動であり、**そこで戻さないと、その設定では
起動に失敗する場合に永遠に戻らない**（`Design.md` 10.3 の ③''）。
## 6.18 多要素認証（0035）

```sql
-- 第2要素の認証器。TOTP だけ（Design.md 6.7）
CREATE TABLE user_mfa_credential (
  id              char(26) COLLATE "C" PRIMARY KEY,
  user_id         char(26) COLLATE "C" NOT NULL
                  REFERENCES app_user(actor_id) ON DELETE CASCADE,
  kind            text        NOT NULL CHECK (kind IN ('totp')),
  name            text        NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  secret          bytea       NOT NULL,   -- AES-256-GCM で封じた Base32 の共有秘密
  secret_nonce    bytea       NOT NULL,
  confirmed_at    timestamptz,            -- NULL = 登録の途中（要素として数えない）
  last_used_step  bigint,                 -- 照合が通った刻みの番号。再利用を拒むため
  last_used_at    timestamptz,
  failed_attempts integer     NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_mfa_credential_user ON user_mfa_credential (user_id)
  WHERE confirmed_at IS NOT NULL;
CREATE UNIQUE INDEX uq_user_mfa_credential_name ON user_mfa_credential (user_id, name)
  WHERE confirmed_at IS NOT NULL;
CREATE TRIGGER trg_user_mfa_credential_updated BEFORE UPDATE ON user_mfa_credential
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ログインの中途状態。パスワードは通ったが、コードがまだ（Design.md 6.7.4）
CREATE TABLE mfa_login_challenge (
  id          char(26) COLLATE "C" PRIMARY KEY,
  user_id     char(26) COLLATE "C" NOT NULL
              REFERENCES app_user(actor_id) ON DELETE CASCADE,
  token_hash  text        NOT NULL UNIQUE,   -- SHA-256。平文は応答にしか出さない
  attempts    integer     NOT NULL DEFAULT 0,
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_mfa_login_challenge_user ON mfa_login_challenge (user_id);

-- 締め出しの手当て（Design.md 6.7.5）
CREATE TABLE mfa_recovery_code (
  id         char(26) COLLATE "C" PRIMARY KEY,
  user_id    char(26) COLLATE "C" NOT NULL
             REFERENCES app_user(actor_id) ON DELETE CASCADE,
  code_hash  text        NOT NULL UNIQUE,    -- SHA-256。平文は発行の1回だけ
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_mfa_recovery_code_user ON mfa_recovery_code (user_id)
  WHERE used_at IS NULL;
```

### 認証器は `app_user` に吊る。`user_identity` には吊らない

**第2要素は「誰であるかを特定する手段」ではなく、特定できたあとに重ねる関門である**
（`Design.md` 6.7.1）。`user_identity` に吊ると、OIDC（構想）を足したときに
**同じ人が手段ごとに別の認証器を登録することになる。**

### `confirmed_at` が NULL の行を要素として数えない

**QR を出しただけで登録が済んだことにしない。** コードの照合が通るまで `NULL` のままで、
**索引に部分条件を付けて数と一意性の両方から外してある。**

**照合しないまま確定させると、利用者が自分を締め出せる**——登録できたつもりの認証器で
ログインの関門が立ち、そのコードは誰も出せない。

**途中の行は1人1件までだが、DB では縛らない**（6.17 と同じ）。登録を始め直したときに
古い行を消すのはアプリ側の仕事であり、**制約で縛ると「やり直し」が誤りとして跳ね返る。**

### 名前の一意は確定済みの行だけに掛ける

**部分 UNIQUE にしてあるのは、途中の行が名前を占有しないようにするためである。**
「iPhone」で登録に失敗した人が、もう一度「iPhone」で始められる。

### `last_used_step` は刻みの番号をそのまま持つ

**時刻ではなく `floor(unixtime / 30)` を持つ。** 時刻で持つと、許容窓（前後1刻み）との
比較のたびに刻みへ戻す計算が要る。**同じ刻みのコードを2回受け付けない**という規則は、
**「保存された番号以下を拒む」という1回の比較で書ける。**

### 挑戦を `access_token` に置かない

**認証ミドルウェアが `token_type` で絞らないためである**（6.2 の `FindAccessTokenByHash`）。
**`access_token` に中間状態を置いた瞬間、挑戦トークンが API 全体を通る資格情報になる。**
詳細と棄却した代替案は `Design.md` 6.7.4 にある。

### リカバリコードは行で持ち、本数を列で持たない

**1本ずつ独立に消費される**ので、残数は `used_at IS NULL` の件数である。
**「10本のうち何本使ったか」を列に持つと、行と列の2か所が同じ事実を持つ。**

## 6.19 パスキー（0036）

```sql
-- パスワードの代わりにログインする鍵（Design.md 6.8）
CREATE TABLE user_passkey (
  id                 char(26) COLLATE "C" PRIMARY KEY,
  user_id            char(26) COLLATE "C" NOT NULL
                     REFERENCES app_user(actor_id) ON DELETE CASCADE,
  name               text        NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  credential_id      bytea       NOT NULL UNIQUE,  -- 認証器が決める。ログインはこれで引く
  public_key         bytea       NOT NULL,         -- COSE_Key のまま。公開鍵なので封じない
  rp_id              text        NOT NULL,         -- 登録したときのホスト名（Design.md 6.8.3）
  attestation_type   text        NOT NULL,         -- none を求めるので、ほぼ 'none'
  attestation_format text        NOT NULL,
  aaguid             bytea,                        -- 認証器の機種。示さなければ NULL
  attachment         text,                         -- platform / cross-platform。示さなければ NULL
  transports         jsonb       NOT NULL DEFAULT '[]'::jsonb,
  sign_count         bigint      NOT NULL DEFAULT 0,
  user_verified      boolean     NOT NULL,         -- UV を一度でも確かめたか（uvInitialized）
  backup_eligible    boolean     NOT NULL,         -- 同期できる鍵か。変わってはならない
  backup_state       boolean     NOT NULL,         -- いま同期されているか
  last_used_at       timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_user_passkey_name ON user_passkey (user_id, name);
CREATE TRIGGER trg_user_passkey_updated BEFORE UPDATE ON user_passkey
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- WebAuthn の挑戦。登録とログインの途中状態（Design.md 6.8.2）
CREATE TABLE webauthn_challenge (
  id          char(26) COLLATE "C" PRIMARY KEY,
  purpose     text        NOT NULL CHECK (purpose IN ('register', 'login')),
  user_id     char(26) COLLATE "C"
              REFERENCES app_user(actor_id) ON DELETE CASCADE,
  challenge   text        NOT NULL UNIQUE,   -- base64url。clientDataJSON の challenge で引く
  session     jsonb       NOT NULL,          -- go-webauthn の SessionData
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_webauthn_challenge_user CHECK ((purpose = 'register') = (user_id IS NOT NULL))
);
CREATE INDEX idx_webauthn_challenge_user ON webauthn_challenge (user_id)
  WHERE user_id IS NOT NULL;
CREATE INDEX idx_webauthn_challenge_expires ON webauthn_challenge (expires_at);
```

### パスキーは `app_user` に吊る。`user_identity` にも `user_mfa_credential` にも吊らない

**判断と理由は `Design.md` 6.8.4 にある。** 要点は2つで、`user_identity` の列の多くは IdP のために
あってパスキーでは使われないこと、`user_mfa_credential` に置くとログインの分岐（6.18）が
パスキーを第2要素として数えてしまうことである。

### 公開鍵は封じない

**6.18 の共有秘密と違い、`public_key` は平文で持つ。** 公開鍵が読めても署名は作れず、
**DB が漏れてもその人としてログインできない。** 封じると、鍵（`app_secret`。6.16）を失ったときに
全員のパスキーが使えなくなるという代償だけが残る。

### `credential_id` は表全体で一意にする

**ログインのときは誰のパスキーかが分からない**（メールアドレスを入力させない。`Design.md` 6.8.1）。
引く手がかりは `credential_id` だけなので、利用者をまたいで一意でなければならない。
**同じ値がもう一度登録されようとしたら、同じ認証器の二重登録である**（`ApiDesign.md` 4.7.3 が `409`）。

### 名前は1人の中で一意にし、部分条件を付けない

**6.18 と違い、登録の途中の行が無い。** 登録は `webauthn_challenge` の挑戦を経て1回の要求で確定するので、
`user_passkey` に入る行はすべて使えるパスキーである。**部分 UNIQUE で途中の行を外す必要が無い。**
この索引は `user_id` の先頭一致で一覧と件数にも使う（別の索引を持たない）。

### フラグと sign count を列で持つ

**go-webauthn の `Credential` を JSON のまま1列に入れない。** 列にしておけば、ライブラリを替えても
行はそのまま読める（公開鍵は COSE_Key の生バイトである）。**`backup_eligible` は変わってはならない**
——ログインのたびに今回の値と比べ、違えば拒否する（ライブラリの検証）。`backup_state` と
`sign_count` はログインのたびに書き戻す。**`user_verified` は一度真になったら戻らない**（WebAuthn の uvInitialized）。

### 挑戦を `mfa_login_challenge` と分ける

**6.18 の挑戦はパスワードが通った人のものであり、`user_id` が必ずある。** パスキーのログインの挑戦は、
**まだ誰か分からない状態で作る**（`user_id` が NULL）。持つ中身も違い、こちらは go-webauthn の
`SessionData` を持ち、あちらはトークンのハッシュを持つ。**`access_token` に置かない理由は 6.18 と同じである。**

### `challenge` はハッシュにせず平文で持つ

**挑戦は資格情報ではない。** ブラウザへ渡して認証器に署名させる値であり、
**知っていても秘密鍵が無ければログインできない。** 6.18 の `token_hash` をハッシュにしたのは、
あのトークンが「パスワードが通った」ことの証明そのものだからである。
**引くときは clientDataJSON に入っている値をそのまま使う。**

### `purpose` と `user_id` を CHECK で結ぶ

**登録の挑戦には利用者が必ずあり、ログインの挑戦には必ず無い。** 前者は go-webauthn が、挑戦を作った
利用者と確定させる利用者を比べるため、後者は discoverable なログインが `user_id` を持つ挑戦を拒むためである。
**どちらかを取り違えた行は、検証の段で原因の分かりにくい失敗になる**ので、入口で弾く。

### 期限切れの行は、次の挑戦を作るときに消す

**ログインの挑戦は認証なしで作られる。** 専用のバッチを持たず、挑戦を1件作るたびに期限切れを全部消す
（6.18 の挑戦を次のログインが片付けるのと同じ考え方）。**増える速さは IP 単位のレート制限（`ApiDesign.md` 2.9）が抑える。**
登録の挑戦は、同じ利用者が始め直したら古いものを消す（`ApiDesign.md` 4.7.2）。

## 6.20 閲覧者の権限を守る（0040）

`Design.md` 6.4.1 の実効権限はシステムロールとプロジェクトロールの和である。0010 の `operator` はチケット作成・編集・クローズ等を持つため、`project_viewer` を割り当てても書き込みができていた。0040 で `operator` の権限を `project.view`、`ticket.view`、`knowledge.view`、`doc.view`、`export.excel`、`agent.run` に絞る。`agent.run` は MCP 接続の入口であり、個々の操作にはプロジェクト権限が別途必要である。

**適用済みの 0010・0017・0019・0027・0029 は編集しない。** 0040 で `role_permission` の余分な割り当てを削除する。`administrator` は全体管理の例外として全権限を維持し、`project_member` と `project_admin` の割り当ては変えない。

## 6.21 不整合なエージェント資格情報を失効する（0041）

旧 `/me/tokens` はエージェントトークンでも通れたため、エージェント名義で `token_type='api'` の無制限トークンを作れた。0041 は既存の `actor.kind='agent'` かつ `token_type<>'agent'` の未失効トークンを失効する。認証処理でも種別の不一致を拒否し、今後の発行経路にも制約を掛ける（`Design.md` 6.5、`ApiDesign.md` 4.1）。

---

## 6.22 ユーザーアイコン（0045）

`actor.avatar_url` は表示 URL を保持し、画像本体は次の表に置く。DB に保存することで
複数レプリカで共有でき、既存のバックアップに含まれる。

```sql
CREATE TABLE actor_avatar (
  actor_id     char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  content_type text        NOT NULL CHECK (content_type = 'image/png'),
  image_data   bytea       NOT NULL,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
```

## 6.23 プロジェクトの暦（0046）

**プロジェクトごとに基準タイムゾーンと休日の暦を持つ。** ガント（`GuiDesign.md` 10章）の土日祝の塗り分けと、
予定日時のタイムスタンプ化（pb-217）が集計上の「今日」を決める基準になる。

```sql
ALTER TABLE project
  ADD COLUMN timezone text NOT NULL DEFAULT 'Asia/Tokyo';   -- IANA 名。検証はアプリ側

-- 祝日の取得元。Google の公開祝日カレンダーは google_id（= URL）単位で1行とし、複数プロジェクトで共有する。
-- 取り込んだファイルはプロジェクトが所有する1行になる。
CREATE TABLE holiday_source (
  id               char(26) COLLATE "C" PRIMARY KEY,
  kind             text NOT NULL CHECK (kind IN ('google','file')),
  google_id        text UNIQUE CHECK (google_id ~ '^[a-z]{2}\.[a-z_]+$'),  -- 例: ja.japanese
  owner_project_id char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  name             text,                   -- X-WR-CALNAME。画面に出す暦の名前
  content_sha256   text,                   -- DTSTAMP を除いた本文のハッシュ。同じなら取り込み直さない
  fetched_at       timestamptz,            -- 最後に取り込めた時刻
  last_attempt_at  timestamptz,            -- 最後に取りに行った時刻（待ち時間の起点）
  last_error       text,                   -- 最後の失敗。成功で NULL に戻す
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_holiday_source_kind CHECK (
    (kind = 'google' AND google_id IS NOT NULL AND owner_project_id IS NULL)
    OR (kind = 'file' AND google_id IS NULL AND owner_project_id IS NOT NULL))
);
CREATE TRIGGER trg_holiday_source_updated BEFORE UPDATE ON holiday_source
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE holiday_source_day (
  source_id char(26) COLLATE "C" NOT NULL REFERENCES holiday_source(id) ON DELETE CASCADE,
  day       date NOT NULL,
  kind      text NOT NULL CHECK (kind IN ('holiday','observance')),
  name      text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  PRIMARY KEY (source_id, day, name)
);

ALTER TABLE project
  ADD COLUMN holiday_source_id char(26) COLLATE "C"
             REFERENCES holiday_source(id) ON DELETE SET NULL;

-- 日ごとの上書き。休日にする／しない（手動の追加・削除を兼ねる）
CREATE TABLE project_calendar_day (
  project_id char(26) COLLATE "C" NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  day        date    NOT NULL,
  is_holiday boolean NOT NULL,
  name       text    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 200),
  created_by char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, day)
);
```

**`timezone` を `settings`（jsonb）に置かず列にする。** 集計の SQL（`stats.overdue` など。pb-217）が
結合して使うためである。値は IANA 名で、**サーバが `time.LoadLocation` と PostgreSQL の
`pg_timezone_names` の両方で引けることを確かめてから保存する**（CHECK に副問い合わせは書けない）。

**ある日が休日かは、次の順で決まる。** 上の段が勝つ。

| 順 | 根拠 | 休日か |
|---|---|---|
| 1 | `project_calendar_day` に行がある | `is_holiday` の値 |
| 2 | 取得元の `holiday_source_day` に `kind = 'holiday'` の行がある | 休日 |
| 3 | 土曜・日曜 | 休日 |
| 4 | それ以外（`kind = 'observance'` の行事だけの日を含む） | 平日 |

**土日は固定である。** 曜日の設定は持たない——土曜が出勤日なら、その日を上書きで平日にする。

**祝日と行事は、iCal の `DESCRIPTION` の1行目で分ける。** Google の公開祝日カレンダーは両者を
同じ暦に入れ、区別はこの欄にしかない（2026-09-27 実測。日本は「祝日」「祭日」、他国は
`ja.` を付けても英語で `Public holiday` / `Public holiday in …` / `Observance`）。
**1行目が「祭日」または `Observance` で始まれば `observance`、それ以外はすべて `holiday`。**
同じ規則をファイルの取り込みにも使う——会社の休業日の暦のように `DESCRIPTION` を持たない暦は、
全件が休日として入る。**行数では分けない**——半日休日と日付が暫定の祝日は、`Public holiday` の
後ろに2行目が付く。

**取得元を URL 単位で1行にする**のは、同じ暦を使う複数のプロジェクトから同じ URL へ取りに行かない
ためである。取得の契機と待ち時間は `ApiDesign.md` 5.8.3。**Google 側は `ETag` も `Last-Modified` も
返さない**（実測。条件付きで要求しても 200 で全体が返る）ため条件付き取得はできず、
`content_sha256` の比較で「変わっていなければ書き換えない」に留める。

**ファイルの取り込みはプロジェクト所有の行を作る。** 取り込み直すたびに同じ行の中身を入れ替え、
Google の暦へ切り替えたり暦を外したりしたら所有の行は消す（参照されない所有物を残さない）。

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

**後続のマイグレーションで足す権限は、本節に追記せず、その機能の節に置く。** 0010 のブロックを増やすと、**どのマイグレーションが何を入れたかが読めなくなる**ためである。現時点の追加は以下のとおり。

| 追加 | 権限 | 置き場 |
|---|---|---|
| 0017 | `doc.view` / `doc.edit` を新設 | 8.1.4 |
| 0019 | **キーは足さず、`agent.run` の割り当てを広げる** | 8.2.6 |
| 0027 | `ticket.reference.edit` を新設 | **6.12.1** |
| 0029 | `ticket.self_edit` を新設 | **6.13** |

**`Design.md` 付録A の「`permission` カタログの粒度は28件で確定」は、0010 時点の件数である。** 0017 適用後は30件、0027 適用後は31件、0029 適用後は32件になる。

## 7.3 ロールと権限の割り当て

以下は 0010 の初期シードである。`operator` の書き込み権限は 0040 で剥奪する（6.20）。

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
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTG','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'done','in_progress','ticket.close','["user"]'::jsonb)
ON CONFLICT DO NOTHING;
```

**`done` は `is_agent_reachable = false`、遷移の `allowed_actor_kinds` も `["user"]`。** エージェントは自分でチケットをクローズできない（`Requirements.md` 10.8.6 の禁止事項をDBレベルで担保する）。

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
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTH','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'done','in_progress','ticket.close','["user"]'::jsonb)
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
   'in_progress','todo','ticket.transition','["user","agent"]'::jsonb),
  ('01JZZZZZZZZZZZZZZZZZZZZZTJ','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'done','in_progress','ticket.close','["user"]'::jsonb)
ON CONFLICT DO NOTHING;
```

3テンプレートの比較：

| | simple | with_review | with_approval |
|---|---|---|---|
| ステータス数 | 3 | 4 | 5 |
| 遷移数 | 4 | 6 | 8 |
| エージェントが到達できる最終地点 | `in_progress` | **`review`** | **`review`** |
| 人間限定の遷移 | `→done`、`done→in_progress` | `→done`、`done→in_progress` | `→approval`、`→done`、`approval→in_progress`、`done→in_progress` |

**`approval`（承認待ち）の `category` は `review` とする。** `workflow_status.category` の `CHECK` は `todo / in_progress / review / done` の4値であり（6.5）、承認待ちは「完了していないが作業も止まっている」状態なので `review` に含める。**カテゴリはボードの列やバーンダウンの集計単位であり、承認待ちをレビュー中と同じ列に置くのが実態に合う。**

**`with_review` は `in_progress → review` をエージェントに許す。** これがテンプレートを分ける最大の意味で、**エージェントが作業を終えて人間のレビューに載せるところまでを自律的に行える**。一方 `with_approval` では `review → approval` を人間限定にしており、承認ゲートの手前へエージェントが自分で進むことを禁じている（`Requirements.md` 10.10.4）。

**差し戻し遷移（`review → in_progress`、`approval → in_progress`）を必ず持たせる。** これが無いと、レビューで問題が見つかったチケットを前進させるしか手がなくなり、承認ゲートが実質的に骨抜きになる。

**再オープン（`done → in_progress`）も3つとも持たせる**（0026 で追加）。0010 では `done` から出る遷移を1つも置いていなかった——完了は終端であり、そこから戻る用途を想定していなかったためである。**運用で逆だと分かった。** 完了と判定したチケットを確認したら直っていなかった、誤操作で完了にした、という場面が実際に起きる（利用者の報告）。行が無ければ `ApiDesign.md` 9.6 は検証2 で 409 に倒れるので、**戻す手段が画面にもAPIにも無い**状態だった。

**再オープンだけ `required_permission` が `ticket.close` である。** 差し戻し遷移が `ticket.transition` なのは、あれが完了していないものを前段へ戻す操作で、**完了判定そのものは動いていない**からである。再オープンは完了判定の取り消しなので、**閉じられる人だけが開け直せる**（`ticket.close` は 7.3 で project_admin にだけ与えてある）。`allowed_actor_kinds` も `["user"]` にしてエージェントには通させない——「エージェントは自分でチケットをクローズできない」の裏返しである。**`closed_at` は遷移の副作用として NULL へ戻る**（`ApiDesign.md` 9.6 の表）ので、API 側に足すものは無い。

**0040 以降は `ticket.close` を持つ `project_admin` または全体管理者だけが再オープンできる。** 0010 の `operator` もこの権限を持っていたが、6.20 で剥奪する。クローズ（`in_progress → done`）も同じ権限で守る。

**`done → todo` は置かない。** 完了から未着手まで一息に戻す場面が挙がっていない。必要になってから足す。

**0026 は既存プロジェクトのワークフローにも同じ行を入れる。** プロジェクトのワークフローはテンプレートの複製であり（6.5）、複製が走るのは作成時だけなので、テンプレートを直しても既存には波及しない。**0024（文書テンプレートの5件目）は「波及しない」と決めたが、ここでは同じ判断をしない**——あちらは足りない1枚をプロジェクト管理者が画面から書けるのに対し、**ワークフローの遷移を画面から足す経路が無い。** 入れなければ既存プロジェクトは永久に完了から戻せない。複製ぶんの ID は、ワークフローの ULID の先頭24文字に `R1` を継いで作る。「ID はアプリ側で生成する」（4.2）からの局所的な逸脱であり、**決定的なので再実行しても冪等**で、ULID の時刻部分が残るので `C` ロケールの並びも崩れない。**次に同じ形で足すときは `R2` を使う。**

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

## 7.6 開発用デモデータ（dev seed）

**7.1〜7.5 の初期データ（本番シード）とは完全に分ける。** 権限カタログやワークフローテンプレートはどの環境でも必要だが、デモユーザーやサンプルプロジェクトは開発端末でしか使わない。

**マイグレーションに含めてはならない。** マイグレーションは前進のみで本番にも適用されるため、デモデータが混入すると取り除けなくなる。

### 7.6.1 CLI で投入する理由

パスワードは Argon2id でハッシュ化する必要があり（`Design.md` 6.3）、**SQL だけでは投入できない**。事前計算したハッシュを SQL に埋め込む方法は、ハッシュパラメータを変更した時点で無効になるうえ、秘密をリポジトリに書かない方針とも整合しない。

したがって `pb dev seed` サブコマンドを設け、アプリと同じ経路でハッシュ化・ULID生成・トランザクション制御を行う。

### 7.6.2 コマンド

```
pb dev seed [--file deploy/dev/seed/dev-data.yaml] [--reset-demo]
```

| 項目 | 仕様 |
|---|---|
| 冪等性 | **再実行しても壊れない。** 既存のメール／プロジェクトキーは作成せずスキップし、結果を件数で報告する |
| `--reset-demo` | シードで作ったデモデータのみ削除してから投入し直す。**`--reset-demo` は `dev-data.yaml` に定義された対象しか消さない**（手動で作ったデータには触れない） |
| トランザクション | 全体を1トランザクションで実行する。途中で失敗したら何も入らない |
| 出力 | 作成／スキップした件数と、**ログイン用のアカウント一覧**を表示する |

### 7.6.3 安全装置

本番DBに対して実行される事故を防ぐため、**二重のガード**を設ける。いずれかに掛かったら何もせず終了する。

1. 環境変数 `PB_ALLOW_DEV_SEED=1` が設定されていること
2. 接続先ホストが `localhost` / `127.0.0.1` / `db`（compose のサービス名）のいずれかであること

加えて、`app_user` の既存件数が 50 を超えている場合は「開発環境ではない可能性が高い」と判断して中断する。

### 7.6.4 定義ファイル

`deploy/dev/seed/dev-data.yaml` を宣言的な定義とする。**Go を触らずにユーザーやプロジェクトを増やせる**ようにするためである。このファイルはコミットする（`deploy/*/secrets/` 配下ではない）。

```yaml
# deploy/dev/seed/dev-data.yaml
# 開発端末での動作確認用。本番には投入されない（7.6.3 のガード）。
password: pbdev-password        # 全デモアカウント共通

users:
  - email: admin@example.com
    display_name: 開発管理者
    system_role: administrator
  - email: pm@example.com
    display_name: 開発PM
    system_role: operator
  - email: member@example.com
    display_name: 開発メンバー
    system_role: operator
  - email: viewer@example.com
    display_name: 開発閲覧者
    system_role: operator

projects:
  - key: demo
    name: デモプロジェクト
    description: 動作確認用のサンプルプロジェクト
    workflow_template: simple
    members:
      - { email: pm@example.com,     role: project_admin }
      - { email: member@example.com, role: project_member }
      - { email: viewer@example.com, role: project_viewer }
```

**チケット・タグ・スプリントも投入する。** バックログ（`GuiDesign.md` 5.4）の検証には、**階層を持つチケット・複数タグの付いたチケット・タグの無いチケット**が揃っている必要がある。グループ化と階層インデントは、それらが無いと目で確かめられない。定義は同じ `dev-data.yaml` に `tags:` / `sprints:` / `tickets:`（`parent` を `seq` ではなく YAML 内の参照名で書く）として足す。実データは `dev-data.yaml` が持つ。

**業務履歴と放置のチケットは、数件だけ作る**。`activity` は原則として書かない——投入は業務上の出来事ではなく、全件に書くと変更履歴が投入の記録で埋まる。ただし1行も無いと、ダッシュボードの「最近の動き」とチケットの履歴が画面で確かめられない。そこで **`history: true` を付けたチケットにだけ、作成と、入口からの遷移1回を API と同じ形で書く**（コメントの行は書かない。要約の作り方を API と二重に持たないため）。**放置（`ApiDesign.md` 9.13.1 の `stale`）は `make dev-seed` の最後の段が作る**——`updated_at` はトリガが上書きし、止められるのはテーブルの所有者だけなので、`pb_app` で動く CLI ではなく make が所有者の接続で1件を20日前へ振る（`Development.md` 8.6 と同じ SQL）。

**`password` を平文で書いているのは意図的である。** `deploy/base/env.example` と同じく「公開前提の既定値」であり、7.6.3 のガードにより本番へ入らない。PB の規約「秘密と個人情報」の対象外として扱う。

### 7.6.5 デモアカウントの構成

**権限による画面の出し分けを検証できる組み合わせ**にしてある。

| アカウント | システムロール | `demo` での役割 | 確認できること |
|---|---|---|---|
| `admin@example.com` | administrator | — | 「管理」セクションが表示される。ユーザー管理・監査ログに入れる |
| `pm@example.com` | operator | project_admin | 管理セクションが出ない。プロジェクト設定が触れる |
| `member@example.com` | operator | project_member | プロジェクト設定が触れない |
| `viewer@example.com` | operator | project_viewer | 閲覧のみ |

**プロジェクトロールのUIは構想だが、`project_member` テーブルは存在し `GET /me` の `projects[].role` に反映される**（`ApiDesign.md` 4.1）。したがってメニューの権限による出し分けの検証にそのまま使える。

### 7.6.6 Makefile ターゲット

```
make dev-reset   # コンテナとボリュームを破棄 → 起動 → migrate → dev seed
make dev-seed    # デモデータのみ投入（冪等）
make dev-info    # URL とデモアカウント一覧を表示
```

`dev-reset` は `deploy/dev/reset.sh` に処理を置き、Makefile からは呼ぶだけにする。**`docker compose down -v` を含むため、実行前に確認を求める**（`.claude/settings.json` でも `docker compose down` は `ask` にしてある）。

`dev-info` を用意するのは、パスワードや URL を探す時間をなくすためである。実装後は `docs/Development.md` からも参照する。

---

# 8. 拡張

6章のテーブルは変更せず、**テーブル追加のみ**で拡張する。**8.1・8.2 の DDL は適用済みで、8.3・8.4（構想）の DDL は構成案である**（着手時に確定させる）。

```
適用済み
  0017_document.sql       document, document_revision, doc 権限, 文書テンプレート
  0018_document_template_text.sql
                          文書テンプレートの初期本文を直す（DDLなし）
  0019_agent.sql          agent, task_lease, agent.run の再配布
  0020_agent_client_kind.sql
                          agent_client_kind（クライアント種別のカタログ）と FK 化
  0021_ticket_working_agent.sql
                          ticket.working_agent_id（実行者の自己申告。6.6）
  0022_agent_run.sql      agent_run, agent_report, context_pack_log,
                          comment.agent_run_id の FK 付与（8.2.4）
  0023_agent_setup.sql    agent.token_env_suffix（8.2.1）,
                          agent_client_kind.has_setup_template（8.2.1.1）
  0024_document_template_agent_onboarding.sql
                          文書テンプレートに agent-onboarding を足す（8.1.2）
  0025_ticket_execution_mode_default.sql
                          ticket.execution_mode の既定を agent_draft へ（6.6）
  0026_workflow_reopen.sql
                          done → in_progress の再オープン（7.4）
  0027_ticket_reference_permission.sql
                          ticket.reference.edit（6.12）
  0028_ticket_sprint.sql  ticket_sprint（チケットとスプリントの所属。6.9.1）
  0029_ticket_self_edit.sql
                          ticket.self_edit（6.13）
  0030_agent_client_kind_claude_desktop.sql
                          agent_client_kind に claude_desktop（8.2.1.1）
  0031_app_setting.sql    app_setting（6.14）
  0032_tls_certificate.sql tls_certificate（6.15）
  0033_app_secret.sql     app_secret（6.16）
  0034_pending_setting_change.sql
                          pending_setting_change（6.17）
  0035_mfa.sql            user_mfa_credential, mfa_login_challenge,
                          mfa_recovery_code（6.18）
  0036_passkey.sql        user_passkey, webauthn_challenge（6.19）
  0037_document_template_rules_workspace.sql
                          規約テンプレートの案内に作業場所を例示（DDLなし。8.1.2）
  0038_document_template_decisions_headings.sql
                          判断の記録テンプレートに1判断＝1見出し（DDLなし。8.1.2）
  0039_table_comments.sql 表のコメントを書き直す（DDLなし）
  0040_operator_readonly.sql
                          operator の権限を閲覧・出力・MCP 接続に絞る（6.20）
  0041_revoke_agent_api_tokens.sql
                          エージェント名義の不整合な資格情報を失効（6.21）
  0042_document_pack_mode.sql
                          文書ごとのコンテキストパック掲載方法（8.1.1）
  0043_project_management_note_template.sql
                          新規プロジェクト向け規約案内と運営ノート（8.1.2）
  0044_ticket_actual_point.sql
                          実績ポイント・算出式の版と PM エージェント用権限（6.6）
構想
  knowledge.sql           knowledge, knowledge_revision, proposal
  comment_signal.sql      comment_signal
  embedding.sql           vector 拡張 + embedding
  project_event.sql       project_event
  analytics.sql           estimate_record, contribution
```

0016 までは 5.2 の一覧にある。**構想の番号は、それまでに足したマイグレーションの分だけ後ろへずれる。** 構想の DDL は着手時に確定させる構成案であり、ファイル名を先に固定する意味はない。

**マイグレーションを足したら、上の一覧も直す。** 節（6章・8章）を足すことと、一覧の採番を直すことは別の作業として漏れやすい。

## 8.1 プロジェクト文書

`Requirements.md` 10.6.2 のプロジェクト文書（憲章）を保持する。**規約・価値観・判断の基準を1か所に置き、全参加者のエージェントが同じものを読む**ための器である。

**PB は文書管理の機構だけを持ち、型はテンプレートで配る。** `kind` に「価値観」「規約」といった語彙を**持たせない**。プロジェクト作成時にテンプレートから初期の文書を複製し、以後どう使うかはプロジェクトに委ねる。

### 8.1.1 `document` — 文書の木

```sql
CREATE TABLE document (
  id           char(26) COLLATE "C" PRIMARY KEY,
  project_id   char(26) COLLATE "C" REFERENCES project(id)  ON DELETE CASCADE,
  parent_id    char(26) COLLATE "C" REFERENCES document(id) ON DELETE CASCADE,
  slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  title        text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body_md      text        NOT NULL DEFAULT '',
  pack_mode    text        NOT NULL DEFAULT 'outline' CHECK (pack_mode IN ('full', 'outline', 'none')),
  sort_order   integer     NOT NULL DEFAULT 0,
  is_template  boolean     NOT NULL DEFAULT false,
  template_key text,
  created_by   char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  updated_by   char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  version      integer     NOT NULL DEFAULT 1,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_document_template CHECK (
    (is_template AND project_id IS NULL AND template_key IS NOT NULL)
    OR (NOT is_template AND project_id IS NOT NULL AND template_key IS NULL)
  )
);
CREATE UNIQUE INDEX uq_document_slug ON document (project_id, parent_id, slug)
  NULLS NOT DISTINCT WHERE NOT is_template;
CREATE UNIQUE INDEX uq_document_template_slug ON document (template_key, parent_id, slug)
  NULLS NOT DISTINCT WHERE is_template;
CREATE INDEX idx_document_tree ON document (project_id, parent_id, sort_order, slug);
CREATE INDEX idx_document_body_trgm ON document USING gin (body_md gin_trgm_ops);
CREATE TRIGGER trg_document_updated BEFORE UPDATE ON document
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE document_revision (
  id            char(26) COLLATE "C" PRIMARY KEY,
  document_id   char(26) COLLATE "C" NOT NULL REFERENCES document(id) ON DELETE CASCADE,
  revision_no   integer NOT NULL,
  title         text    NOT NULL,
  body_md       text    NOT NULL,
  changed_by    char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  change_reason text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_document_revision UNIQUE (document_id, revision_no)
);
CREATE INDEX idx_document_revision_doc ON document_revision (document_id, revision_no DESC);
```

**テンプレートを別テーブルにしない。** `is_template` / `template_key` / `project_id IS NULL` で同居させるのは、**6.5 の `workflow` が既にこの形を採っている**ためである（7.4 の3種のテンプレートは `workflow` の行として入り、プロジェクト作成時に複製される）。同じ「テンプレートから複製する」機構を2つの形で持つと、片方に入れた直しがもう片方に入らない。

**一意制約を2本に分けている。** 実文書はプロジェクト内で、テンプレートは `template_key` の中で、それぞれ「同じ親の下に同じ slug は1つ」を保証する。**`NULLS NOT DISTINCT` が要るのは `parent_id IS NULL`（トップレベル）のため**である。既定の `UNIQUE` は NULL どうしを別物として扱うので、これが無いとトップレベルで slug が重複できてしまう。**PostgreSQL 15 以降の機能**で、本プロジェクトは 17（3.1）。

**`version` は楽観ロック用**で、`project`（6.4）と同じ使い方をする。**人とエージェントが同じ文書を触る**ため、プロジェクト設定より競合が起きやすい。競合時の扱いは `ApiDesign.md` に置く。

**削除は物理削除**（4.6 の既定）。本文の履歴は `document_revision` に残るため、`deleted_at` を持たせても復元の役には立たない。`parent_id` の `CASCADE` により、親を消すと部分木ごと消える。

**`body_md` に GIN トライグラムインデックスを張る**のは、`knowledge`（8.3.1）と同じ理由による。日本語の部分一致検索を `pg_trgm` で賄う（4.5）。

### 8.1.2 文書テンプレート（0017 の初期データ、本文は 0018、5件目は 0024、規約の本文は 0037 と 0043、判断の記録の本文は 0038、6件目は 0043）

`template_key = 'default'` の6件を置く。`Requirements.md` 10.6.2 の表に対応する。

| `slug` | `title` | `sort_order` | 役割 |
|---|---|---|---|
| `vision` | 価値観・世界観 | 10 | **意思決定のベースになる。** 何を目指し何を大切にするか。規約に書かれていない場面の拠りどころ |
| `rules` | 規約 | 20 | 守るべきこと。命名・進め方（作業場所とブランチの用意と後始末を含む）・レビューの通し方 |
| `decisions` | 判断の記録 | 30 | なぜそう決めたか。追記のみで使い、**1判断＝1見出し**で書く |
| `learnings` | 学びと知見 | 40 | やってみて分かったこと。**うまくいったことと駄目だったことの両方** |
| `agent-onboarding` | エージェントの参画情報 | 50 | **新しい参加者とそのエージェントが作業を始められるようになるまでに要ること。** 作業材料の取り方、参画の合図、資格情報の要否（**秘密そのものは書かない**） |
| `project-management-note` | プロジェクトマネジメントノート | 60 | 見積・実績の算出と検証をプロジェクトごとに記録する。`pack_mode = none` |

**0043 は新規プロジェクト向けの規約案内に、完了レポートの `cost`（トークン数・やり取りの回数・掛かった分、束ねたセッションの按分）と `findings`（モデルの名前と版）を例として加える。** 具体的な単位や按分方法は各プロジェクトで決める。運営ノートには「見積・実績」章の下に「実績」と「見積」を置き、材料、算出式、記録の置き場、確かめ方を記入する骨組みにする。既存プロジェクトの文書は書き換えない。

**`sort_order` は 10 刻みにする。** `ApiDesign.md` 10.4 が「省略時は同じ親の中の末尾（現在の最大値 + 10）」と定めているので、既定で足される文書がテンプレートの後ろに並ぶ。1 刻みにすると、間に1件挿し込むだけで全件の付け替えが要る。

**`slug` に `knowledge` を使わない。** 8.3.1 のテーブル `knowledge`（構想のプロジェクトメモリ）と、7.2 の権限 `knowledge.view` / `knowledge.propose` / `knowledge.approve` が既に同じ語を使っている。**同名にすると、権限マトリクス（`GuiDesign.md` 5.6.3）で `knowledge.view` を見た人が「この文書の閲覧権限だ」と読む**が、文書に効くのは `doc.view` である。`learnings` はうまくいったことも含む語で、「駄目だったこと」に寄る `caveats` より 10.6.2 の意図に近い。

**5件目の `agent-onboarding` は 0024 で足した。PB が完成品として出せるのは「MCP が使える状態になるまで」で、その先——作業材料をどこからどう手元に用意するか——は PB が知らない**（`Requirements.md` 10.9.1）。材料の取り方はリポジトリ型・配布型・MCP 型の3通りあり、指定するのはプロジェクト管理者である。**置き場を `settings` ではなく文書にしたのは、MCP 型で辻褄が合う唯一の置き場だからである**——手元に材料を持たず PB から読む型では、取り方の説明そのものも `pb_get_doc` で読めなければならない。

**`slug` に `agents` を使わない。** `/me/agents`（`GuiDesign.md` 5.8.2）と `/p/:key/settings/agents`（5.11）が既に同じ語を画面のパスに使っており、**文書の `path` が `agents` になると、同じ語が「エージェントの一覧」と「参画の手引き」の2つを指す。** ハイフンを含む `slug` は `^[a-z0-9][a-z0-9-]{0,63}$` の範囲内である（`ApiDesign.md` 10.4）。

**`sort_order` は 50 とし、先頭へ挿さない。** 既存のプロジェクトは 10〜40 で複製済みで、**先頭へ挿すと新規プロジェクトとの並びが食い違う。** 憲章の4件は「なぜ→守ること→決めたこと→学んだこと」で互いに順序の意味を持つ組だが、参画情報は性質の違う運用情報である。

**0042 で `agent-onboarding` と子孫は `pack_mode = none` に移行する。** 参画時に一度読む文書であり、チケットごとのパックには載せない。管理者は文書ごとに変更できる（`Design.md` 8.5.5）。

**本文を空にしない。** 各文書に「ここに何を書くか」の短い案内を初期本文として入れる。空の文書が並ぶと、何を書く場所か分からないまま放置される。

**初期本文の正本は 0018 である。** `agent-onboarding` の本文は 0024、`rules` の本文は 0037、`decisions` の本文は 0038 が正本である。 0017 が入れた本文は段落の途中で改行しており、
**画面で読むと全角文字の間に半角空白が出る。**
`GuiDesign.md` 6.6 の「画面に出す日本語は1行に収める」は、**設計文書ではなくアプリが表示する
文字列すべてに効く**。0017 は適用済みなので編集せず（5.3）、0018 で本文だけを差し替えた。

**CommonMark の強調は、日本語の約物と相性が悪い。** `**` が開くか閉じるかは前後の文字種で
決まる（flanking の規則）ため、**約物に接する `**` は黙って働かなくなる**（markdown-it で両方向とも確かめてある）。

| 書き方 | 何が起きるか | 直し方 |
|---|---|---|
| `**…だけを書く。**個人の…` | **閉じない**（`**` が地の文に残る）。閉じる `**` が約物 `。` に続き、直後が全角文字だと右フランキングにならない | 半角空白を入れるか（ただし画面にその空白が見える）、**段落を分ける** |
| `これが**「読める人」が…**` | **開かない**（同じく `**` が残る）。開く `**` の直前が全角文字で直後が約物 `「` だと左フランキングにならない | **約物を強調の外へ出す**（`これが「読める人」が…**最初の権限**`） |

**強調するのは文ではなく句にする。** 文全体を `**…。**` で囲むとどちらの罠にも当たりやすい。
句を囲めば前後が文字になり、空白も段落分けも要らない。**書いたら必ず一度描画して確かめる**
——`**` が生のまま残っても画面は出るので、読むまで気づかない。

**規約の案内には、書く項目の例として「作業場所とブランチの用意と後始末」を挙げる**（0037）。配布する `/pb-implement` の手順4 は、作業場所の作り方と後始末を書かず「規約に従い、規約に無ければ利用者に確認する」としている（`Requirements.md` 10.8.6）。**規約に書く場所があることを、プロジェクト管理者に知らせる**ための例示である。

- **規約そのもの（worktree をタスク単位で切る、など）は雛形に入れない。** PB のプロジェクトは git を使うものに限らず（`Requirements.md` 10.9.1 のリポジトリ型・配布型・MCP 型）、git を使わない型では誤った規約になる。初期本文を「ここに何を書くか」の案内にとどめる本節の方針も崩れる
- **何も足さない案は採らなかった。** 規約に記述が無ければエージェントは毎回利用者に確認することになり、`Requirements.md` 10.7.3 の推奨（タスク単位で worktree を切り、完了時に破棄する）を書く場所があることが、どこからも見えない
- **再検討の条件は、プロジェクトの型（リポジトリ型など）をテンプレートの選び分けに使うようになったとき**である。型ごとに雛形を分けられれば、リポジトリ型にだけ具体的な規約を入れられる

**判断の記録の案内には、「1判断＝1見出し」の書き方を入れる**（0038）。初期設定ではコンテキストパックは判断の記録を本文ではなく目次だけ載せ、エージェントは見出しを指定して1件ずつ読む（`Design.md` 8.5.5）。**見出しの無い判断の記録は、パックに1件も届かない**——見出しが無いときは本文を載せない。0018 の案内には見出しで書く指示が無く、案内どおりに書くとこの形になっていた。

- **書き方の例（`### <日付> / <何を決めたか>`）はインラインコードで示し、見出しそのものは置かない。** 下の「初期本文に見出しを置かない」を崩さないため
- **形の組み替え（見出しの付け方・章の移し替え）を許す一文は入れない。** 組み替えが要るのは見出しの無い記録を持つ既存プロジェクトだけで、新しいプロジェクトには組み替える記録が無い
- **再検討の条件は、パックが判断の記録を目次以外の形（本文の抜粋など）で運ぶようになったとき**である。そのとき見出しを強いる理由が変わる

**最初の5文書の初期本文には見出し（`##`）を置かない。** `ApiDesign.md` 10.2 の `?outline=1` は**エージェントが「どの章を読むか」を決めるため**に使う。中身の無い見出しを並べると、目次だけを見た相手に「読むべき章がある」と読まれる。0043 の運営ノートだけは、記入先を示す「見積・実績」「実績」「見積」の骨組みを置き、`pack_mode = none` でパックには載せない。

**複製はプロジェクト作成時に行う**（7.4 のワークフローテンプレートと同じ手順の中で）。複製後はそのプロジェクトのものになり、テンプレート側を直しても既存プロジェクトには波及しない。**プロジェクト作成の経路は `POST /projects` と `pb dev seed` の2つがあり、どちらも同じ手順を通る**（実体は `server/internal/project`）。

**複製は木として行う。** いまテンプレートは4件ともトップレベルだが、`uq_document_template_slug` が `parent_id` を含んでおり、**テンプレート側は子を持てる**。親から順に複製して旧 `id` → 新 `id` の対応で `parent_id` を張り替える。トップレベルだけを写す実装は、テンプレートに子が1件足された日に**無言で落とす**。

**`sort_order` はテンプレートの値をそのまま使う**（10 / 20 / 30 / 40）。振り直さないのは、並べ替え（`ApiDesign.md` 10.4）が同じ 10 刻みで書き戻す作りだからで、初期値が揃っていれば最初の並べ替えで余計な更新が出ない。

**複製した文書には `revision_no = 1` を作り、`created_by` / `updated_by` にはプロジェクトの作成者を入れる**（`ApiDesign.md` 5.3）。

**既存プロジェクトには波及しない。** 複製が走るのはプロジェクト作成時だけなので、**それ以前に作られたプロジェクトの文書は0件のまま**である（`GuiDesign.md` 5.10「空状態」）。テンプレートを後から適用する口は持たない。

**テンプレートに件数を足したときも同じである。** 0024 で `agent-onboarding` を足したが、**それ以前に作られたプロジェクトには入らない。そこは欠落ではなく、プロジェクト管理者が自分で1件書く場面である**——`Requirements.md` 10.9.1 の「立ち上げ相」がそれに当たる。

**後から足す文書は自由でよい。** 階層も slug も利用者が決める。テンプレートは出発点であって制約ではない。

### 8.1.3 章を永続化しない

`pb_get_doc(path, section)` の `section` は**見出しテキストそのもの**とし、**どこにも保存しない**。

**スラッグ化も番号付けもしない**（`ApiDesign.md` 10.2）。同じ文書に同名の見出しが2つあるときだけ、
2つ目以降に `#2` を付けて区別する。

| 参照の寿命 | 使うもの | 見出しを改名したら |
|---|---|---|
| セッション内（`pb_list_docs` で目次 → `pb_get_doc` で章） | 見出しテキスト | **壊れない。** 目次は常に現在の本文から作る |
| 保存される参照 | **`document.id` のみ。章は保存しない** | **壊れない。** 指す先が見出しに依存しない |

**章を列に持たせないことで、`Requirements.md` 10.13 が挙げていた「見出しが変わると既存の参照が壊れる」問題が消える。** 著者に `{#anchor}` のような記法を書かせる必要も無い。存在しない章を指されたときは **`404 not_found` を返し、本体に `available_sections`（その文書の見出し一覧）を添える**（`ApiDesign.md` 10.3）——呼び出し側が、もう一度目次を取りに行かずに次の一手を選べる。

### 8.1.4 権限（0017）

```sql
INSERT INTO permission (key, category, description, sort_order) VALUES
  ('doc.view', 'doc', 'プロジェクト文書の閲覧', 35),
  ('doc.edit', 'doc', 'プロジェクト文書の編集', 36)
ON CONFLICT (key) DO UPDATE
  SET category = EXCLUDED.category,
      description = EXCLUDED.description,
      sort_order = EXCLUDED.sort_order;

-- administrator は 7.3 の「全権限」SELECT で自動的に付く（再実行する）
INSERT INTO role_permission (role_key, permission_key)
SELECT 'administrator', key FROM permission
ON CONFLICT DO NOTHING;

INSERT INTO role_permission (role_key, permission_key)
SELECT 'project_admin', key FROM permission WHERE key IN ('doc.view','doc.edit')
ON CONFLICT DO NOTHING;

INSERT INTO role_permission (role_key, permission_key)
SELECT r, 'doc.view' FROM unnest(ARRAY['operator','project_member','project_viewer']) AS r
ON CONFLICT DO NOTHING;
```

| ロール | `doc.view` | `doc.edit` |
|---|---|---|
| `administrator` | ✓ | ✓ |
| `project_admin` | ✓ | ✓ |
| `operator` | ✓ | **—** |
| `project_member` | ✓ | **—** |
| `project_viewer` | ✓ | — |

**`doc.edit` を `operator` と `project_member` に与えない。** 理由は2つある。

ひとつは `Requirements.md` 10.6.2 との整合である。**憲章は全参加者を縛る**ので、更新できる人を絞る。編集そのものは「PM が自分のエージェントに指示して行う」形を想定している（同 10.7.5）。

もうひとつは検証上の理由である。0010 の時点では `operator` が `ticket.*` を持ち、チケット画面で「読めるが作れない人」を検証できなかった。`doc.edit` は当初から `operator` に付与せず、文書画面で権限による出し分けの負の側を検証できるようにした。0040 で `operator` のチケット書き込み権限も剥奪する（6.20）。

**これは権限モデル全体の再整理ではない。** 付録A の論点①（`GET /roles?scope=project` を権限不要としたのが暫定であること）は未決のまま残る。

## 8.2 エージェント連携

### 8.2.1 `agent` — エージェントの登録

```sql
CREATE TABLE agent (
  actor_id       char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  owner_actor_id char(26) COLLATE "C" NOT NULL
                 REFERENCES app_user(actor_id) ON DELETE CASCADE,
  project_id     char(26) COLLATE "C" REFERENCES project(id) ON DELETE CASCADE,
  client_kind    text    NOT NULL REFERENCES agent_client_kind(key),
  model_name     text,
  model_version  text,
  capabilities   jsonb   NOT NULL DEFAULT '[]'::jsonb,
  trust_level    integer NOT NULL DEFAULT 1 CHECK (trust_level BETWEEN 0 AND 3),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_owner ON agent (owner_actor_id);
CREATE TRIGGER trg_agent_updated BEFORE UPDATE ON agent
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 0023
ALTER TABLE agent ADD COLUMN token_env_suffix text
  CHECK (token_env_suffix IS NULL
      OR token_env_suffix ~ '^[A-Z][A-Z0-9_]{0,40}$');
CREATE UNIQUE INDEX uq_agent_env_suffix
  ON agent (owner_actor_id, token_env_suffix)
  WHERE token_env_suffix IS NOT NULL;
```

**`client_kind` は 0020 で参照テーブルに変えた**（8.2.1.1）。0019 では
`CHECK (client_kind IN ('claude_code','copilot','other'))` だった。

#### `token_env_suffix` — トークンを載せる環境変数の名前（0023 で追加）

**接続設定ファイルが読む環境変数の名前を、本人が決める**（`Requirements.md` 10.8.3）。**格納するのは接尾だけ**で、`PB_TOKEN_` の接頭はアプリが付ける。

```
token_env_suffix = 'MY_LAPTOP'   →   PB_TOKEN_MY_LAPTOP
```

**接頭を持たせないのは、`PATH` や `HOME` を作れないようにするため**である。CHECK が
`^[A-Z][A-Z0-9_]{0,40}$` に限るので、環境変数名として妥当な文字だけが入る。

**一意は（所有者・接尾）である。** 環境変数は端末ごとの名前空間なので、他人と重なって構わない。
**同じ人の中で重なると、`~/.zshrc` の1行が2つのエージェントに解釈されて事故になる。**

**固定名（`PB_TOKEN`）では足りない。** 同じ端末で2つ以上のエージェントを使うと衝突し、
症状は `404 not_found`（トークンのプロジェクトと URL のプロジェクトの食い違い。`Design.md` 8.3）
になる——**利用者からは原因が見分けられない。**

**導出にしない理由**（3案とも検討して落とした）。

| 導出元 | 落ちる理由 |
|---|---|
| プロジェクトキー | エージェントが増える主な軸は**端末**である。「ノートPC」と「デスクトップ」は同じプロジェクト・同じ種別になり、区別できない |
| 表示名 | 一意でない（キーは4つ組。`ApiDesign.md` 4.5.4）。**日本語が通る**ので環境変数名を作れないことがある。しかも**改名できる**ので、`~/.zshrc` の行が黙って効かなくなる |
| ULID | 一意で不変だが読めない。`~/.zshrc` を開いた本人が何の変数か分からない |

**「どの端末か」は PB が知らない情報である。** `ApiDesign.md` 4.5.1 が `display_name` を
「**本人が**思い出すための手がかり」と定めているのと同じ理由で、本人に書いてもらう。

**NULL を許すのは、0023 の時点で既に登録済みの行があるため**である。埋め戻しに使える
決定的な規則が上のとおり存在しない（日本語の表示名から作れない）。**NULL の行は
`PB_TOKEN_<エージェントの ULID>` にフォールバックし、画面が設定を促す**（`ApiDesign.md` 4.5.1）。

**1行が表すのは「ある参加者の手元で動くクライアント1つ」である。** 人ではない。同じ人が Claude Code と VS Code を使えば2行になり（`Requirements.md` 10.10.3「クライアントごとに分ける」）、2つのプロジェクトにつなぐならさらに分かれる。**キーは（所有者・クライアント種別・プロジェクト・表示名）の4つ組**であり（`ApiDesign.md` 4.5.4）、`ApiDesign.md` 6.1 のエージェント行の例（`claude-code (my-app)`）がこの形を前提にしている。**表示名まで含むので、同じ端末種別で「ノートPC」と「デスクトップ」を分けられる。**

`model_name` / `model_version` を保持するのは、`Requirements.md` 10.10.3 の「モデル更新後に品質が変化した際の切り分け」のため。`agent_run` にも実行時点の値をコピーする（後からモデルを変えても過去の実行記録が壊れないよう非正規化する）。

`trust_level` は`Requirements.md` 10.10.3 の段階的権限昇格に対応する。**0019 の時点では既定値のまま置き、APIも画面も受け取らない**——昇格の材料になる実績（`agent_run` の集計。構想）がまだ無く、使うものが無いうちに入口を作ると意味が固まるためである。

#### 8.2.1.1 `agent_client_kind` — クライアント種別のカタログ（0020 で追加）

```sql
CREATE TABLE agent_client_kind (
  key          text PRIMARY KEY,
  display_name text    NOT NULL,
  sort_order   integer NOT NULL
);

-- 0023
ALTER TABLE agent_client_kind
  ADD COLUMN has_setup_template boolean NOT NULL DEFAULT false;
UPDATE agent_client_kind SET has_setup_template = true
 WHERE key IN ('claude_code', 'copilot', 'codex');

-- 0030。**行の追加だけで済む**——0020 が参照テーブルにした狙いの実物である。
INSERT INTO agent_client_kind (key, display_name, sort_order, has_setup_template) VALUES
  ('claude_desktop', 'Claude Desktop', 15, false);
```

| `key` | `display_name` | `sort_order` | `has_setup_template` | 事業者 |
|---|---|---|---|---|
| `claude_code` | Claude Code | 10 | **true** | Anthropic |
| `claude_desktop` | Claude Desktop | 15 | false | Anthropic |
| `codex` | OpenAI Codex | 20 | **true** | OpenAI |
| `copilot` | GitHub Copilot | 30 | **true** | Microsoft |
| `gemini` | Gemini（CLI / Code Assist） | 40 | false | Google |
| `other` | その他・OSS 等 | 90 | false | — |

**値が決めるのは「設定ファイルの置き場」である。** エディタではない——同じ VS Code でも
Claude 拡張なら `.mcp.json` + `.claude/commands/`、GitHub Copilot なら
`.vscode/mcp.json` + `.github/prompts/` になる（`Requirements.md` 10.8）。
**両方を使う人は2行になる。**

**2026年に「エージェント」と「エディタ」が1対1でなくなった**（ACP により Claude Code /
Codex / Gemini CLI が Zed・JetBrains・Neovim の中で動く）。**軸をエディタに取ると値域が
定まらない**ので、**MCP クライアントとして振る舞うもの**を1軸に採る。

**`key` は事業者名ではなく製品名にする。** 1つの事業者が複数のクライアントを出しうるためで、
値が表すのは事業者ではなく置き場である。**`claude_code` と `copilot` は 0019 からの綴りを
変えない**（既存の行がある）。

**CHECK ではなく参照テーブルにした理由**——クライアントの種類は流動的で、今後も増える。

| | 得るもの |
|---|---|
| 増やすのが行の追加になる | CHECK だと値を増やすたびに DDL のマイグレーションが要る |
| **表示名がDBに来る** | **画面が対応表を持たなくてよくなる**（`GET /roles` が `lib/roles.ts` を廃止させたのと同じ形。`GuiDesign.md` 5.6） |
| 値域はDBが守る | FK なので、アプリの検証を抜けた値は INSERT で落ちる |

#### `has_setup_template` — PB が配置ファイルを出せるか（0023 で追加）

**PB がそのクライアント向けのテンプレートを持っているかを表す。** 持たない種別を
セットアップ画面の選択肢に出すと、**選んだ先に何も出ない。**

**true にしたのは3種別だけである**（`Requirements.md` 10.8.2 が本文を定義しているものに限る）。
**書いていないテンプレートを「持っている」と名乗らない。**

| 種別 | 接続設定 | 手順 | 常時コンテキスト |
|---|---|---|---|
| `claude_code` | `.mcp.json` | `.claude/commands/*.md` | `CLAUDE.md` |
| `copilot` | `.vscode/mcp.json` | `.github/prompts/*.prompt.md` | `.github/copilot-instructions.md` |
| `codex` | `.codex/config.toml` | `.agents/skills/*/SKILL.md` | `AGENTS.md` |

**3種別とも置き場が違う。** これが「値が決めるのは設定ファイルの置き場である」の実物であり、
**同じ人が複数のクライアントを使うなら、エージェントごとに違うものを渡す必要がある**
（`Requirements.md` 10.9.1 の系統B）。

**`other` を残す。** OSS のエージェント（Cline / Goose / OpenCode / OpenHands / Aider /
Continue など）や、事業者系でも PB がまだ手順を持たないものがここへ入る。
**個別のテンプレートを書いたものから、行として独立させ `has_setup_template` を立てる。**
`gemini` は行として在るがテンプレートが無いので false である。

**`claude_desktop` が false なのは理由が違う**（0030）。書いていないからではなく、
**置き場が存在しないからである**——系統A が作るのは作業フォルダへ置くファイルだが、
**Claude Desktop に作業フォルダは無い**（`Requirements.md` 10.9.1「MCP 型では系統A に
置き場が無い」）。**したがって、テンプレートを書けば true になる種別ではない。**
手順ファイルを配れないことは欠落ではなく、参画は「PB に参画して」の一文で足りる
——それは Codex に対して既に採っている形である（10.8.2）。

**この列で系統B の有無を判定しない。** `claude_desktop` は false のまま**接続設定を持つ**
（`claude_desktop_config.json`。`Requirements.md` 10.8.4.2）。**2つは元から別の問いで、
この種別が現れるまで答えが一致していただけである**（`ApiDesign.md` 4.5.8.3）。
**系統B の正本は `internal/agentsetup` の `connectSpecs`** で、鍵がカタログにあることは
テストが本表と突き合わせて確かめる。

#### `owner_actor_id` — エージェントは人に紐づく（0019 で追加）

現状のAIエージェントは人の支援を行う形態なので、**エージェントはプロジェクトメンバの誰かに紐づけて登録する。** その人の持つ権限がベースになり、そこにエージェント独自の権限を整理して割り当てる。

参照先を `actor` ではなく **`app_user`** にしているのは、所有者が人間に限られるためである。エージェントがエージェントを所有することはない。

**この列が無いと「その人の権限がベース」を構造で守れない。** `Requirements.md` 10.10.3 が属性に挙げる「**どの参加者に付いているか**」を DDL で表すのがこの列である。

#### 権限は所有者から導く（委譲）

エージェントは `app_user` の行を持たないため、`Design.md` 6.4.1 の式のうち**システムロールの層が必ず空になる**。そこで**所有者の層をそのまま使う**。

```
実効権限 = ( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ トークンのスコープ
```

**エージェントに `project_member` の行を作らない。** 作ると所有者のロールと二重に持つことになり、所有者のロールを変えたときに片方だけ古くなる。委譲なら常に一致する。

`Design.md` 6.5 の「**人間アカウントの借用をしない**」はこれで破れていない——principal（`actor`）もトークンも監査の `actor_id` も別のままで、**借りるのは資格情報ではなく権限の根拠**である。副次的に、**所有者を無効化するとその人のエージェントも同時に効かなくなる**（安全側）。

**自立したエージェントが要るようになったら、`NOT NULL` を外して `NULL = 所有者を持たない自立エージェント`と定義する。** そのときは `project_member` に自前の行を持ち、メンバの一員として並ぶ（利用者の言う「PJ予算でエージェントに働いてもらう」形態）。**いま `NOT NULL` から始めるのは順序の問題である**——制約を外すのは前進のみのマイグレーションで1行だが、後から付けるには全行の埋め戻しが要る。

#### 所有者を物理削除したとき

`owner_actor_id` の `ON DELETE CASCADE` により、**利用者を削除するとその人のエージェントの行も消える**（`actor` の CASCADE を通じてトークンとメンバーシップも消える）。所有者を失ったエージェントを残さないためである。

**エージェントが書き手になる手順（`pb_post_note`）で、この経路を見直すこと。** `comment.author_id` は `NOT NULL` かつ `ON DELETE RESTRICT` で、`ApiDesign.md` 6.5 は削除前にシステムアクターへ付け替えると定めている。**その規定は人についてしか書かれていない**ため、エージェントがコメントを持つようになると、所有者の削除が RESTRICT に当たる。0019 の時点ではエージェントはコメントを書けないので問題は起きない。

### 8.2.2 `task_lease` — リース管理

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

**この器は使わない**。**行を1行も書かない。** 判断の理由と再検討の条件は 6.6「`task_lease` を採らなかった」にある。要点だけ再掲すると——**排他が実際に要るのは自律取得（`pb_next_task`、構想）からで、いまは人がチケット番号を指定して走らせる**ため、同じチケットを2つのエージェントが取り合う状況が起きない。

**`lease_token` の用途を本節は定義していなかった。** 分散リースの定型でいう**能力トークン**——リースを取った側に秘密値を渡し、以降の操作でその提示を求めることで、呼び出し元の身元とは独立に所持を証明させるもの——を意図した列である。**PB では要らない**：MCP の口は Bearer 必須で呼び出し元のアクターが常に判明しており（`Design.md` 8.3）、`uq_task_lease_active` が「1チケットに有効なリースは1つ」を保証するので、`actor_id` の一致だけで所持証明が済む。**要るようになるのは、同じエージェント登録で複数のセッションを同時に走らせたとき**（同一トークンを2つの端末で `export` した場合）である。`Requirements.md` 10.10.3 が「Claude Code と VS Code を使えば2行になる」と定めるので、クライアントが違うだけなら `actor_id` で区別できる。

**器を消さずに残す。** 前進のみのマイグレーション（5.3）では、使わない表を落とすより寝かせるほうが安い。

### 8.2.3 `dod_item` — 6.11 にある

DDL と判断根拠は 6.11 にある。将来開けるのは `manual` 以外の `type`（`assertion` / `artifact` / `review` / `task_ref`）であり、**テーブルの追加は要らない**。

### 8.2.4 `agent_run` / `agent_report`（0022）

**`pb_submit_result`（完了レポート）の格納先である。**

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

#### 書き手は `pb_submit_result` ひとつである

**1回の提出が `agent_run` 1行と `agent_report` 1行を同時に作る**（`ApiDesign.md` 9.15）。**開始を告げる口を持たない**——`pb_claim_task` は構想で
（8.2.2）、`pb_transition_task` の副作用は `ticket.working_agent_id` だけと決めた（6.6）。

**「走っている run」を読む者が居ないので、開始の口を作らない。** 「いま誰が
処理しているか」は `ticket.working_agent_id` が既に担っており（6.6）、`status='running'` の行を
足すと**同じ事実が2か所になる**。**再提出は別の run になる**——`/pb-implement` の手順7
（`Requirements.md` 10.8.6）は「未充足の完了条件が返ったら修正して再提出する」と定めており、
その修正はエージェントが実際に作業をやり直したことを意味する。

この帰結を4つ書き下す。**列の意味が後から変わらないよう、埋めない列は
埋めないままにする。**

| 列 | 扱い |
|---|---|
| `status` | **`completed` しか立たない。** `failed` / `abandoned` は「レポートを出さずに終わった run」で、それを観測する口が無い。`running` は上記のとおり作らない |
| `started_at` | `report.cost.wall_clock_min` があればそこから逆算し、無ければ `now()`。**エージェントの自己申告である** |
| `ended_at` | 提出時刻。`agent_report.submitted_at` と同じ値になる |
| `workflow_version` | **NULL のまま。** `workflow` に版の列が無く（6.5）、`Requirements.md` 10.9.3 の陳腐化検出は構想である |
| `retry_count` | **同じ（チケット × アクター）の既存の run 数**を入れる。初回は 0 |

`token_id` / `client_kind` / `model_name` / `model_version` は**提出時点の値を写す**
（`access_token` と `agent` の行から）。非正規化するのは `Requirements.md` 10.10.3 の
「モデル更新後に品質が変化した際の切り分け」のためで、**後から `agent.model_name` を
書き換えても過去の実行記録が動かない**ことがこの列の値である。

#### `comment.agent_run_id` の FK は本節で使い手を得る

0007 が FK を付けずに空けていた列である（6.7）。**0022 が FK を付け、
`pb_submit_result` が作る完了レポートのコメントがこの列を埋める**（`ApiDesign.md` 9.15）。
**この列を埋めるのはそのコメント1種類だけである**——`pb_post_note` が作る
コメントは run を持たない（作業中に run が存在しないため）。

#### `actor_id` は `ON DELETE RESTRICT` である

**エージェントを削除する前に、その run を「削除されたエージェント」へ付け替える**
（`ApiDesign.md` 4.5.4）。`comment.author_id` が同じ制約を持ち、26a が同じ付け替えを
実装している（6.7）。**付け替えないと `DELETE /me/agents/:id` そのものが失敗する。**

### 8.2.5 `context_pack_log`

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

**0022 で器だけ作り、書かない。** `agent_run` への FK を持つので同じファイルに
入れる必要があり、8章の採番表も 0022 の中身としてこの表を挙げている。**0019 が `task_lease` を
同じ理由で寝かせたのと同じ扱いで**（8.2.2）、前進のみのマイグレーション（5.3）では使わない表を
落とすより寝かせるほうが安い。

**`pb_get_context` は `context_pack_log` を書かない。**
**結び先が無いためである**——`agent_run` は `pb_submit_result` のときにしか作られないので
（8.2.4）、パックを返す時点では **`agent_run_id` が必ず `NULL` になる。** `Requirements.md`
10.4.4 の効果計測は **`agent_report.status` との突き合わせ**が本体であり、**結べない行を
貯めても計測にならない。** 10.10.7 の監査（何を見せたか）だけなら成り立つが、**そのために
書き手を置くと、結べる形へ変えるときに既存行の扱いが要る。**

**再検討の条件は、run の開始を告げる口ができたときである**（`agent_run.status` に `running` を
立てる経路。`Design.md` 8.5.5）。そのとき `pb_get_context` が `agent_run_id` を受け取れるようになる。

### 8.2.6 権限（0019）

**新しい権限キーは足さない。** `agent.register` / `agent.token.issue` / `agent.run` は 0010 の 28件に既にある（7.2）。0019 が変えるのは**割り当てのほう**である。

```sql
-- agent.run を「自分に紐づくエージェントを MCP から走らせてよい」と定め、
-- プロジェクトに参加する側のロールへ配り直す（Design.md 6.5）。
INSERT INTO role_permission (role_key, permission_key)
SELECT r.key, 'agent.run'
  FROM role r
 WHERE r.key IN ('operator','project_member','project_viewer')
ON CONFLICT DO NOTHING;
```

**理由。** 8.2.1 の委譲により、エージェントの権限は**所有者から導かれる**。`agent.run` を持つのが `project_admin` と `administrator` だけのままだと、**プロジェクト管理者のエージェントしか MCP を使えない**。人の支援を行う形態（8.2.1）ではメンバも閲覧者もエージェントを伴うため、参加する側のロールが持つべき権限である。

**`project_viewer` にも与える。** `Requirements.md` 10.9.1 の系統B が発行の用途に「実装用＝write可／**閲覧用＝read only**」を挙げており、読むだけのエージェントも走る必要がある。何を読み書きできるかは `agent.run` ではなく、トークンのスコープと個々のツールの必要権限（`Design.md` 8.2）が決める。

`app_user.system_role` は `operator` か `administrator` のいずれかであり（6.2 の CHECK）、`operator` に与えた時点で全利用者がこのキーを持つ。0040 でも `operator` の `agent.run` は維持する。MCP への接続を許し、個々の読み書きは別の権限とトークンスコープで制限する。

`agent.register` / `agent.token.issue` の割り当ては**変えない**。登録とトークン発行は本人の操作（`ApiDesign.md` 4.5）であり、`/me/tokens` と同じく権限キーを要求しないためである。この2つは**他人のエージェントを管理する側**の権限として `project_admin` に残る。

## 8.3 知識還流（構想）

**本節は構想である。** 知識はまず **8.1 の文書として運用し、押し付けたい粒度が実測で見えてから**エンティティに切り出す（`Requirements.md` 10.6.2 の末尾）。先に器を作ると、要らなかったときに戻せない。

**承認キュー（`proposal`）も構想である。** 承認の対象になる `knowledge` と文書差分の両方が無いと、**残る承認対象がサブタスク提案だけになり、画面を作る理由が薄い**（`Requirements.md` 10.12）。いまは文書の編集を権限（`doc.edit`）で直接行う。

### 8.3.1 `knowledge` — プロジェクトメモリ

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

### 8.3.2 `proposal` — AIの提案を集約する

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

`auto_applied` は、`Requirements.md` 10.6.3 の承認ポリシー（**影響範囲を軸に、自動採用と承認必須を分ける**）で自動反映されたものを表す。**自動反映であっても proposal 行は必ず残す**ことで、後から遡って取り消せる。

## 8.4 AI機能・分析（構想）

### 8.4.1 `comment_signal` — コメント重要度

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

### 8.4.2 `embedding` — ベクトルインデックス

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

### 8.4.3 `project_event` — プロジェクトヒストリー

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

### 8.4.4 分析用テーブル

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

**2つの方式を併用する**。**どちらか一方に寄せない**——
救う場面が違い、打てる場所も違う。

| 方式 | 何を持つ | 打つ場所 | 救う場面 |
|---|---|---|---|
| **PB 独自の書き出し**（9.1.1） | **行だけ。** スキーマは持たず、マイグレーション番号で表す | **PB の画面**（アプリケーション設定の DB タブ。`GuiDesign.md` 5.12.2） | 版上げの前の退避、誤操作からの巻き戻し、**DB へ直に触れない配置**（K8s・マネージド） |
| **`pg_dump`**（9.1.2） | **DB 全体。** スキーマ・索引・拡張を含む | **DB のある場所**（compose なら `db` コンテナ、外部 DB ならそのホスト） | ディスク障害、DB ごとの移設、PITR の土台 |

**PB 独自の書き出しが要る理由は、`pg_dump` が打てない場所があること**である。実行イメージは
distroless で `pg_dump` を持たず（`deploy/Dockerfile`）、マネージドの PostgreSQL では
DB のホストに入れない。**PB 独自の書き出しは、PB が動いていれば必ず打てる。**

**`pg_dump` が要る理由は、PB 独自の書き出しがスキーマを持たないこと**である。表がすべて
消えた状態からは、PB 独自の書き出しだけでは戻せない（マイグレーションを先に走らせる必要がある）。

### 9.1.1 PB 独自の書き出しと取り込み

**PB が `pb_app` の権限で全表を読み、1つの書庫にまとめる。** 取り込みは逆を行う。
正本は API が `ApiDesign.md` 11.11〜11.13、画面が `GuiDesign.md` 5.12.2。

**代償を隠さない。** マイグレーションを足すたびに、書き出しと取り込みが追従できているかを
**往復のテストで確かめる必要がある。** これは形式を独自にした対価であり、消えない。

**後から形式を変えると、既に取ったバックアップを戻せなくなる。** `meta.json` の
`format_version` はそのための番号で、**読めない版を黙って読もうとしない。**

#### 書庫の形

```
pb-backup-<YYYYMMDD-HHMMSS>.tar.gz
├ meta.json              形式の版・マイグレーション番号・取得時刻・PB の版・表ごとの件数
└ data/<表名>.jsonl      1行1レコードの JSON
```

**`tar.gz` にする。`zip` ではない。** zip は**末尾の索引を読まないと中身を取り出せない**ので、
取り込みで書庫全体をメモリか一時ファイルに載せることになる。**K8s の Pod は
`readOnlyRootFilesystem: true`** で動いており（`deploy/prod/k8s/app.yaml`）、一時ファイルを書けない。
**tar は先頭から順に流せる**ので、書き出しも取り込みも全体を抱えずに済む。

**`.tar.gz` はブラウザが素通しする。** `.crt` が拒まれた件（`ApiDesign.md` 11.7）とは別で、
危険な形式として扱われない。**証明書と手引きの zip とは形式が揃わないが、揃えることより
`readOnlyRootFilesystem` で動くことを採る。**

**1行1レコードの JSON（JSONL）にする。** 表ごとに1ファイルで、**全件をメモリに載せずに
読み書きできる。** 型の対応は 4.1 に従う。**日時は ISO8601 のまま書き出す**——REST API は
エポックミリ秒に変わった（`ApiDesign.md` 2.2、pb-224）が、書庫の形を変えると既存の書庫を取り込めなくなる。

| 列の型 | 書き出す形 |
|---|---|
| `text` / `char(26)` | 文字列 |
| `timestamptz` | **ISO8601 UTC の文字列**（`2026-09-18T15:04:05Z`） |
| `date` | `YYYY-MM-DD` の文字列 |
| `jsonb` | **そのまま JSON の値として埋める。** 文字列に包まない |
| `bytea` | **base64 の文字列**（`app_secret.secret` など7列。6.15・6.16・6.18・6.19） |
| `boolean` / 数値 | JSON の値 |
| NULL | `null` |

**`goose_db_version` は `data/` に入れない。** マイグレーションの適用履歴は取り込みのときに
goose が作るもので、行として運ぶと**履歴と実際のスキーマが食い違いうる。** 番号は
`meta.json` の `migration_version` が持つ。

**`meta.json` は表ごとの件数を持つ。** 取り込んだあとの突き合わせを PB の中で完結させるため
（`GuiDesign.md` 5.12.2）。DB タブの件数（`ApiDesign.md` 11.10）が `count(*)` の正確な数なのは、
**この突き合わせに使うから**である。

#### 秘密も暗号鍵も、そのまま入る

**書庫には `app_secret` の鍵と、それで暗号化した暗号文の両方が入る**（6.16 の「代償を隠さない」と
同じ形）。**書庫そのものを秘密として扱う。** PB は書庫を暗号化しないし、パスフレーズも求めない。

**`PB_SECRET_KEY` を与えている環境では、鍵は書庫に入らない**（`app_secret` を読まないため）。
**その環境で取った書庫は、同じ `PB_SECRET_KEY` が無いと TLS の秘密鍵を復号できない。**

`access_token` も入る。**トークンの平文は保存されていない**（SHA-256 のハッシュだけ。6.2）ので、
書庫からトークンを取り出すことはできないが、**ハッシュが漏れれば失効の状況は読める。**

#### 取り込みは、作り直してから行う

**行を消して入れ直すのではなく、表を落として作り直す**。

1. **保守モードに入る**（`Design.md` 10.4）
2. `meta.json` の `format_version` と `migration_version`（= M）を確かめる。**M が現在の版より
   新しければ拒む**——知らない列を推測して埋めることになる
3. **`public` の表と、マイグレーションが作った他のものを落とす**（下記）
4. **同梱の goose でスキーマを版 M まで進める**
5. **`goose_db_version` を除く全表を、1つの `TRUNCATE … CASCADE` で空にする**（下記）
6. **外部キーを遅延できる形にし、1つのトランザクションで** `data/*.jsonl` を取り込む（下記）
7. **件数を数え、`meta.json` と突き合わせる**（下記）
8. **最新の版まで進める**
9. 操作者のセッション行を入れ直す（`ApiDesign.md` 11.12）
10. 保守モードを出て、監査ログを1件書く

**この順にすると、取り込みの時点のスキーマが書き出したときと同じになる。** 列の突き合わせが
要らなくなり、書庫の行をそのまま入れられる。

**⑧ でデータを入れるマイグレーションが本来の順で走る。** 0010 の初期データ、0037・0038 の
文書テンプレート本文のように、**マイグレーションが行を足す版がある**（7章・8.1.2）。
行だけを現在のスキーマへ流し込む方式では、これらが欠けたままになる。

##### ③ で落とすのは、表だけではない

**マイグレーションは表以外も作る。** 0001 は `set_updated_at()` という関数を作っており、
**表を落としても関数は残る。** 残ったまま④を走らせると
`function "set_updated_at" already exists` で止まる。

**`public` にある、拡張が持っていないものを全て落とす**——表・ビュー・順序・関数・型。
**拡張が持つものは落とさない**（`pg_depend` の `deptype = 'e'` で除く）。`citext` や
`pg_trgm` は `public` にたくさんの関数と型を作るが、**これらは `CREATE EXTENSION IF NOT
EXISTS` で作られるので、残っていて構わない**（④が何度走っても通る）。

**`DROP SCHEMA public` はしない**（下記）ので、落とす対象を自分で数える必要がある。

##### ⑤ が要るのは、④ が行も入れるからである

**マイグレーションはスキーマだけを作るのではない。** 0010 は権限カタログとワークフロー
テンプレートを、0018・0024・0037・0038 は文書テンプレートの本文を**行として入れる**（7章・8.1.2）。

**④ を終えた時点で、これらの行は既に在る。** そのまま ⑥ に進むと、**書庫の同じ行と主キーで
衝突する**——書庫は版 M で取ったので、同じ行を持っているためである。

**`TRUNCATE … CASCADE` で空にする。** `pb_owner` は表の所有者なので打てる（`pb_app` には
権限が無い）。**全表を1つの文に並べれば、外部キーの順序を気にしなくてよい。**

**`goose_db_version` は除く。** ここを空にすると、④ で進めた版が無かったことになり、
⑧ が最初からやり直してしまう。

##### ⑥ で外部キーを遅延させる

**「整合した行の集合なら、どの順で入れても通る」は成り立たない。**
**外部キーは行ごとに、その場で検査される。** `ticket.parent_id` のような自己参照では、
**子を親より先に入れた時点で落ちる。**

**取り込みの前に、すべての外部キーを `DEFERRABLE` に変える**（`pb_owner` なら
`ALTER TABLE … ALTER CONSTRAINT … DEFERRABLE INITIALLY IMMEDIATE` で変えられる）。
**1つのトランザクションで `SET CONSTRAINTS ALL DEFERRED` してから入れ、終わりに戻す。**

**検査が消えるわけではない。** 遅延した検査は `COMMIT` のときにまとめて走るので、
**整合していない書庫はそこで弾かれる**（実機で確かめた）。**遅れるのは時期だけである。**

**表の順序を計算する案は採らない。** 表のあいだは外部キーから並べられるが、**表の中は
並べられない**——`ticket` の親子は行の深さで決まり、書き出す側か取り込む側で
木をたどることになる。**仕組みが2つ要る**のに対し、遅延は1つで済む。

**途中で落ちると、外部キーが `DEFERRABLE` のまま残る。** 取り込みは③の「表を落とす」から
やり直すので次の取り込みで消えるが、**やり直さずに使い続けると、本来と違う形のスキーマになる。**

##### ⑦ で数えるのは、⑧ より前である

**突き合わせが答えるのは**「書庫を忠実に入れられたか」であって、**いまの件数ではない。**
**⑧ でマイグレーションが行を足せば、その表は `meta.json` より多くなる**ので、
**⑧ のあとに数えると、正しく入ったものまで食い違いとして出る。**

**⑨ のセッション1行も、⑦ のあとに足す。** 数えたあとに足すので、**突き合わせには現れない。**

**いまの件数は DB タブ（`ApiDesign.md` 11.10）で見る。** 取り込み直後の数と一致しないことが
あるのは、⑧ と ⑨ の分である。

**`DROP SCHEMA public` はしない。** `ALTER DEFAULT PRIVILEGES … IN SCHEMA public` の登録は
**スキーマに紐づいており**（3.4 の initdb）、スキーマを作り直すと一緒に消える。**initdb が走るのは
`pgdata` が空の初回だけ**なので、復元後に `pb_owner` が作った表を `pb_app` が読めなくなる。
**表を1つずつ落とせば、この登録は残る。**

#### 取り込みだけ `pb_owner` が要る。ただし PB は持たない

**③④⑤⑧ は `pb_app` では実行できない**（3.4。③④⑧ は DDL、⑤ の `TRUNCATE` は表の所有者だけが打てる）。**それでも PB に `pb_owner` の
資格情報を持たせない**。

**取り込みのときだけ、画面で受け取る。** PB は保存せず、その取り込みのあいだだけ
`pb_owner` の接続を張り、終わったら捨てる。**応答にもログにも残さない**（`ApiDesign.md` 11.12）。

**PB 側の権限を先に確かめる。** `system.settings` を持たない要求は、**資格情報を受け取る前に
403 で拒む**——持っていない人に入力させない。

**`GRANT pb_owner TO pb_app` による `SET ROLE` は採らない。** 一度与えると平常時も昇格でき、
**3.4 の「実行時の SQL インジェクションが成立してもテーブルを落とせない」が成り立たなくなる。**
昇格の口を常時開けるより、**操作のたびに人が資格情報を出すほうが、開いている時間が短い。**

**書き出しには要らない。** `pb_app` は全表を `SELECT` できる（3.4 の `ALTER DEFAULT PRIVILEGES`）。

#### goose は全配置に同梱済みである

| 配置 | goose の在処 |
|---|---|
| コンテナ（compose / docker） | イメージの `/goose` と `/migrations/`（`deploy/Dockerfile`） |
| K8s | 同じイメージ。initContainer が `/goose` を叩く（`deploy/prod/k8s/app.yaml`） |
| ネイティブ | 一式の `goose` と `migrate.sh`（`deploy/prod/build-release.sh`） |
| 開発端末 | `make migrate`（`server/tools` の道具として） |

**PB 本体から走らせるには、`server/migrations/` を埋め込み、goose v3 をサーバ本体の依存に
入れる必要がある**（いまは `server/tools/` の道具用モジュールにある）。**子プロセスとして
`/goose` を起こす案は採らない**——配置ごとに在処が違い、distroless に「外のコマンドを起こす」
前提を作ることになる。

**これに伴って `database/sql` が入る。goose の API は
`*sql.DB` しか取らない**ので、`database/sql` と `pgx/v5/stdlib` を避けられない。

**入るのは、マイグレーションを走らせる箇所だけである。** 書庫の読み書きも、アプリのデータの
読み書きも、これまでどおり pgx で直に行う（`Design.md` 3.1 の「`database/sql` を経由しない」は
**アプリのデータ経路について**であり、その旨を同表に書いた）。

**自前の最小実行器を書く案は採らない。** goose 形式の注釈（`+goose StatementBegin` / `End`。
関数定義を含む 0001 で使う）を自分で解釈することになり、**間違えると復元が壊れる。**
goose 本体と挙動がずれていくのも避けたい。

### 9.1.2 `pg_dump`（DB のある場所で打つ）

| 項目 | 内容 |
|---|---|
| 論理バックアップ | `pg_dump -Fc` を日次。開発端末では compose の `profiles` で任意起動するサイドカー、またはホスト側 cron |
| 保持 | 直近7世代 |
| リストア確認 | 月1回、別DBへ `pg_restore` して起動確認する。**取得できているだけでは復元できる保証にならない** |
| K8s | 運用では CloudNativePG のバックアップ機能（オブジェクトストレージ＋WALアーカイブによるPITR）か、外部の PostgreSQL が持つ機能。**`make release TARGET=k8s` のサンプルの StatefulSet は持たない**（3.3）。**この場合、PB 独自の書き出し（9.1.1）が唯一の手段になる** |

```bash
docker compose exec -T db pg_dump -U pb_owner -Fc pb > backup/pb_$(date +%Y%m%d).dump
```

**PB の画面からは打てない。** `pg_dump` は実行イメージに無く（`deploy/Dockerfile`）、
PB は `pb_app` で繋いでいる。**手順は `deploy/prod/MANUAL.md` にある。**

### 9.1.3 まだ決めていないこと

- **定期実行と保持する世代数。** 9.1.1 の書き出しはブラウザへのダウンロードで、サーバ側に
  置き場を持たない。**定期実行には置き場が要る**ので、
  保存先を決めるところから別に扱う
- **添付ファイル。** `attachment` 表だけがあり、実体の置き場は未実装（6.7）。**実装したら
  9.1.1 の対象に足す**——足さないと、書庫を戻しても添付だけが戻らない

## 9.2 ログと監視

| 項目 | 設定 |
|---|---|
| スロークエリ | `log_min_duration_statement = 200ms` |
| 接続状況 | `pg_stat_activity`（`application_name = 'pb'` で識別）。**セッション数・DB の大きさ・表ごとの件数は画面でも見られる**（アプリケーション設定の DB タブ。`GuiDesign.md` 5.12.2） |
| 統計 | `pg_stat_statements` を有効化する（未実装） |
| autovacuum | **既定のまま**。`ticket` などに自動の VACUUM / ANALYZE が走ることを確かめてある。追記だけの `activity` / `audit_log` も、PostgreSQL 13 以降の `autovacuum_vacuum_insert_scale_factor`（既定 0.2）で拾われる。**設定を見直すきっかけは、9.3 の保持期間ポリシーを入れて大量の削除が起きるようになったとき**である（削除は不要行を一度に作る） |

## 9.3 データ量の見積り

| テーブル | 1年後の想定行数（小規模利用） |
|---|---|
| `ticket` | 数千 |
| `comment` | 数万 |
| `activity` | 数十万（フィールド単位で記録するため最多） |
| `audit_log` | 数万 |

**参照する側の列にインデックスを置いていない外部キーが31本ある**（多くは `created_by` / `updated_by` / `actor_id` のように `actor` を指す列）。**使われるのは、参照される行を消すときの存在確認と、エージェントを消すときの付け替え（`me_agents.go`）だけ**で、日常の一覧や検索はこれらの列で絞らない。dev の合成データ（`activity` 30万件・`comment` 10万件）で、参照先を消すときの確認が 30ms、付け替え前の件数が 13ms だったので、足していない。**足すきっかけは、エージェントやユーザーの削除が日常の操作になったとき**である。

`activity` の肥大が最初に問題化する見込み。**保持期間ポリシー（例：2年経過分をアーカイブテーブルへ移動）を検討する。** パーティショニング（`occurred_at` によるレンジ分割）は、その時点で必要なら導入する。

---

# 10. 未解決の検討事項

- `audit_log` に対する `UPDATE` / `DELETE` 権限を `pb_app` から剥奪するか（改ざん防止と、保持期間ポリシーによる削除運用の両立）
- `comment.author_id` の `NOT NULL` とユーザー削除の整合。「削除されたユーザー」システムアクターの生成タイミング（マイグレーションでの事前作成か、初回削除時の遅延生成か）
- `activity` の粒度。チケット1回の更新で何行増えるか、まとめ方（1リクエスト＝1行にJSONで差分を持つ案との比較）
- `sort_key`（LexoRank）の実装方式と再採番が必要になる境界条件。再採番が起きたことは `ApiDesign.md` 9.4 の `rebalanced` で呼び出し側へ伝える
- **タグを軸（`tag_group`）へ拡張するかの判断時期**（6.10）。フラットなタグは「1チケットが複数タグを持つ」ため、タグでグループ化すると複数のセクションに重複表示される。「領域」「工程」のような軸を導入して軸ごとに単一選択とすれば重複は消えるが、必要性はバックログを使ってみるまで分からない。**判断はバックログを実運用に載せてから**行う
- ワークフローの `definition`（jsonb 原本）と正規化テーブルの同期方法。どちらを正とするか
- 日本語検索を `pg_trgm` から `pg_bigm` へ移行する判断基準（データ量・検索頻度・精度の不満）。**`LC_CTYPE=C.UTF-8` と、語の長さによる問い合わせの切り替えは入れてある**（4.5）。残るのは2文字の語で、**2文字の語の検索が遅いと実際に言われたとき**に判断する
- エージェントが並行書き込みする際のトランザクション分離レベル（既定の Read Committed で足りるか、`task_lease` 取得時に `SELECT FOR UPDATE` が必要か）
- **リポジトリを `project.settings`（jsonb）に置いた**（6.4）。リポジトリ単位のトークン発行や横断検索（`Requirements.md` 10.9）が要件になったら `project_repository` テーブルへ移す
- **`kind='system'` の actor に一意なキー列が無い**（6.2）。ユーザー削除時のコメント付け替え先「削除されたユーザー」を `display_name` で引いている。**システムアクターが2種類目になった時点で壊れる。** `agent` テーブル（8.2）を設計するときに、システムアクターの識別子も決める
- マルチテナント（スキーマ分離）を導入する場合の移行手順。いまは単一テナント前提のためテナントID列を持たない
