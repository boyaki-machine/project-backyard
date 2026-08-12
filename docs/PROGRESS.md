# 実装進捗

`Design.md` 11章の手順に対応。**ステップを完了したら必ずこの表を更新すること。**

## Phase 1

| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 完了 | 2026-08-11 | `make up` でDBが起動し、`pb_app` ロールが存在する |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 完了 | 2026-08-11 | `make migrate` 後、テーブル23個と権限28件・ロール5件が存在する |
| 3 | `pb admin create` による初期管理者作成 | 未着手 | | 作成した管理者が `app_user` に `system_role='administrator'` で入る |
| 4 | 共通基盤（エラー形式・ページネーション・認証ミドルウェア・監査ログ） | 未着手 | | `go test ./internal/httpapi/...` が通る |
| 5 | `POST /auth/login`・`/auth/logout`・`GET /me` | 未着手 | | `curl -i -X POST .../auth/login` で Set-Cookie が返り、`GET /me` が権限一覧を返す |
| 6 | 認可ミドルウェア（`RequirePermission`） | 未着手 | | オペレータで `/admin/users` を叩くと 403 |
| 7 | `GET/POST /projects`、`check-key` | 未着手 | | プロジェクトを作成し、一覧に件数と進捗が出る |
| 8 | `GET/PATCH /projects/:key`、archive | 未着手 | | `If-Match` 不一致で 409 |
| 9 | `GET/POST /admin/users` | 未着手 | | ユーザーを作成し、初期パスワードが1回だけ返る |
| 10 | `GET/PATCH/DELETE /admin/users/:id`、password-reset、memberships | 未着手 | | 自分自身のロール変更が 409、最後の管理者の降格が 409 |
| 11 | `GET /roles`、`GET /permissions` | 未着手 | | 権限28件・ロール5件が返る |
| 12 | `/me/sessions`、`/me/tokens`、`PATCH /me`、`POST /me/password` | 未着手 | | パスワード変更後に他セッションが失効する |
| 13 | client の雛形と embed 経路の疎通 | 未着手 | | `make build` した単一バイナリで `/` にVueの初期画面が出る |
| 14 | フロント：ログイン画面、プロジェクト一覧、ユーザー管理 | 未着手 | | ブラウザでログイン→一覧表示→ユーザー追加ができる |
| 15 | フロント：ロールによるメニュー・ボタンの出し分け | 未着手 | | オペレータで「管理」セクションが表示されない |
| 16 | チケット API と画面 | 未着手 | | `ApiDesign.md` 9章の確定後に着手 |

**状態の記法**：未着手 / 進行中 / 完了 / 保留

## 設計文書との差異・積み残し

実装中に設計文書との食い違いや、文書に書かれていない判断が生じた場合はここに記録する。
**コードだけ直して済ませない。** 設計文書の修正提案とセットで残す。

| 日付 | 手順 | 内容 | 対応 |
|---|---|---|---|
| 2026-08-11 | 1 | `DbDesign.md` 3.4 の `01_roles.sql` は `CREATE ROLE pb_app LOGIN PASSWORD 'change-me-via-secret'` とパスワードをリテラルで書いていた。`CLAUDE.md` 絶対規則6 と衝突し、`app_database_url` 内のパスワードと一致させる手段が文書になかった | `01_roles.sh` に変更し、secret ファイル `app_db_password` を psql 変数で注入。**`DbDesign.md` 3.2 / 3.4 に反映済み**（2026-08-11） |
| 2026-08-11 | 1 | `DbDesign.md` 3.2 の `app.build: .` はプロジェクトディレクトリ（`deploy/base/`）を指すが、Dockerfile は `deploy/Dockerfile`、ソースはリポジトリルート配下にある。マルチステージビルドが成立しない | `context: ../..` / `dockerfile: deploy/Dockerfile` に修正。**`DbDesign.md` 3.2 に反映済み**（2026-08-11）。実ビルドの検証は手順13以降 |
| 2026-08-11 | 1 | `DbDesign.md` 3.5 の「実行時ロールに `SET statement_timeout = '15s'` を既定として付与」が 3.4 の SQL に反映されていなかった | `ALTER ROLE pb_app SET statement_timeout = '15s'` を initdb に追加。**`DbDesign.md` 3.4 / 3.5 に反映済み**（2026-08-11） |
| 2026-08-11 | 1 | `deploy/Dockerfile` が未作成のため `docker compose up -d` が app のビルドで失敗する | compose には `Design.md` 4.3 どおり `app` を定義したうえで、`make up` の対象を当面 `db` のみとした。Makefile に TODO を記載。手順13以降で全サービスに戻す |
| 2026-08-11 | 1 | `deploy/dev/compose.yaml` は作成していない。base に 127.0.0.1 公開・ログ設定が入っており dev 固有の差分が現時点で無いため（`Design.md` 4.3 末尾の方針） | 差分が生じた手順で追加する。それまで起動は `-f deploy/base/compose.yaml` 単体 |
| 2026-08-11 | 2 | **`DbDesign.md` 7.4 が `with_review`（+レビュー中）と `with_approval`（承認フロー付き）を「同様に定義する」と書くのみで、ステータス構成・遷移・`required_permission`・`allowed_actor_kinds` が未定義。** 推測で実装できない | 0010 には `simple` のみ投入した。**`DbDesign.md` 7.4 への追記が必要**（下記1）。テンプレート追加は前進のみの後続マイグレーション（0011 以降）で行えるため、スキーマ本体への影響はない |
| 2026-08-11 | 2 | `DbDesign.md` 5.1 がマイグレーションツールを「`sqlx-cli` / `golang-migrate` / `atlas`」としているが、採用は **goose v3**（`Design.md` 3.1、`CLAUDE.md`）。Rust 前提時代の記述が残っている | 実装は goose。**`DbDesign.md` 5.1 の修正が必要**（下記2） |
| 2026-08-11 | 2 | `DbDesign.md` 5.2 のファイル構成が `migrations/` と書かれているが、実体は `server/migrations/`（`Design.md` 4.1 / 11章 手順2） | `server/migrations/` に作成。**`DbDesign.md` 5.2 のパス修正が必要**（下記3） |
| 2026-08-11 | 2 | **goose の実行方法（インストール・接続文字列の渡し方）が設計文書のどこにも書かれていない。** ホストに Go も goose も入っていなかった | `server/go.mod` の `tool` ディレクティブで goose を固定し、`make migrate` から `go tool goose` で呼ぶ方式を**ユーザー承認のうえ採用**。**`DbDesign.md` 5.1 への追記が必要**（下記2） |
| 2026-08-11 | 2 | goose 最新の v3.27.3 は `go 1.25.7` を要求し、`Design.md` 3.1 の「Go 1.24 以上」と衝突する | goose を **v3.26.0**（`go 1.23.0` 要求）に固定し、`server/go.mod` の go ディレクティブは `1.24` を維持した。goose を上げる際は `Design.md` 3.1 の最低バージョンとセットで見直す |
| 2026-08-11 | 2 | goose を tool 依存にすると、goose が対応する全DBドライバ（clickhouse / mssql / ydb / sqlite / vertica 等）が `server/go.mod` に indirect 60件として載る。`Design.md` 3.1 の「外部ライブラリを増やさない」方針と見た目が衝突する | ビルド対象ではなくツール依存であり実行バイナリには入らないため、現状のまま進める。手順4で実依存が入った際に `// indirect` の量が問題になるようなら、goose を別モジュール（`server/tools/go.mod`）へ隔離する案を出す |
| 2026-08-11 | 2 | 設計文書のDDLには goose の注釈（`-- +goose Up`、`-- +goose StatementBegin`）が無い | goose のパーサ要件であり設計判断ではないため、注釈のみ機械的に付加した。`set_updated_at()` は本体に `;` を含むため `StatementBegin` / `StatementEnd` で囲む必要がある。**DDL 本体は 6〜7章と1文字も変えていない**（下記の照合結果） |
| 2026-08-11 | 2 | 検証欄の「テーブル23個」は goose の管理表 `goose_db_version` を含めた数。`DbDesign.md` 6章の業務テーブルは22個 | 記述の誤りではないため文書は変更しない。内訳は 6.2:6 / 6.3:4 / 6.4:2 / 6.5:3 / 6.6:2 / 6.7:2 / 6.8:2 / 6.9:1 = 22 |

### 設計文書へ反映済みの修正（2026-08-11、承認のうえ適用）

1. **`DbDesign.md` 3.4** — `01_roles.sql` を `01_roles.sh` に置き換え。`/run/secrets/app_db_password` を読み、`CREATE ROLE pb_app LOGIN PASSWORD :'app_password';` として psql 変数で渡す。あわせて 3.2 の `db` サービスと `secrets:` に `app_db_password` を追加
2. **`DbDesign.md` 3.2** — `app.build: .` を `context: ../..` / `dockerfile: deploy/Dockerfile` に差し替え。「`build.context` はリポジトリルート」の要点も追記
3. **`DbDesign.md` 3.4** — SQL 末尾に `ALTER ROLE pb_app SET statement_timeout = '15s';` を追記。3.5 の表からも 3.4 を相互参照
4. **`DbDesign.md` 3.2** — `deploy/dev/secrets/` に置く3ファイルの表を追加。`app_db_password` と `app_database_url` のパスワードを一致させる注意を明記
5. **`DbDesign.md` 3.4** — 運用上の注意（実行ビット、initdb は初回起動時のみ、ヒアドキュメントのクォート）を追記

設計文書のコードブロックと実装の一致は、コメントを除いた差分で確認済み（`deploy/base/compose.yaml`、`deploy/base/initdb/01_roles.sh`）。

### 設計文書への修正提案（手順2、未反映）

1. **`DbDesign.md` 7.4** — `with_review` と `with_approval` のDDLを書き切る。必要なのは各テンプレートの
   固定ULID、`workflow_status`（`key` / `name` / `category` / `sort_order` / `requires_human_approval` /
   `is_agent_reachable`）、`workflow_transition`（`from` / `to` / `required_permission` /
   `allowed_actor_kinds`）。`simple` と同じ粒度で確定すれば、そのまま 0011 に落とせる
2. **`DbDesign.md` 5.1** — ツール名を goose v3 に直し（現状は `sqlx-cli` / `golang-migrate` / `atlas`）、
   実行方法を追記する。実装は以下を採った

   ```
   server/go.mod   tool github.com/pressly/goose/v3/cmd/goose （v3.26.0 を固定）
   make migrate    cd server && GOOSE_DRIVER=postgres GOOSE_DBSTRING=<pb_owner の接続文字列> \
                     go tool goose -dir migrations up
   ```

   接続文字列は `deploy/dev/secrets/db_password` を recipe 内で読んで組み立てる。Makefile にも
   `ps` の argv にも平文を残さないため、レシピは `@` 付きで実行する
3. **`DbDesign.md` 5.2** — ファイル構成のパスを `migrations/` から `server/migrations/` に直す

### 手順2の照合結果

`DbDesign.md` 6.1〜6.9 と 7.1〜7.4 の `sql` ブロックを抽出し、`server/migrations/*.sql` から
コメントと goose 注釈を除いたものと機械的に比較した。**コメントを除くSQL 462行が完全一致**。

差分として出るのは 6.4.1（チケット採番の `UPDATE ... RETURNING`）のみで、これはアプリが実行する
クエリでありDDLではないため、マイグレーションに含まれないのが正しい。

## 環境メモ

実際に動かして分かったこと（バージョンの相性、ハマった点、回避策）を追記する。
ここに書いた内容は、後から `CLAUDE.md` や設計文書へ昇格させることを検討する。

- goose のバージョン: **v3.26.0**（`server/go.mod` の tool ディレクティブで固定）
  - v3.27.3 以降は `go 1.25.7` を要求し、`Design.md` 3.1 の「Go 1.24 以上」と衝突するため上げていない
- （記入例）sqlc のバージョン: v1.x
- ホストの Go: 1.26.5（Homebrew。手順2で導入）。`make migrate` / 手順3以降の `make run` / `make build` に必要
- `go tool goose` は初回のみモジュールをダウンロードする（60秒程度）。2回目以降はキャッシュが効く
- 手順1の検証環境: Docker 29.1.3 / Docker Compose v5.3.1 / macOS (darwin 25.6.0, arm64)
- ホストに `psql` が入っていないため、`make psql` は `docker compose exec db psql` でコンテナ内に入る
- `initdb/01_roles.sh` は**実行ビットを立てておくこと**。`:ro` マウントでもホスト側のファイルモードがそのまま使われる
- initdb スクリプトが走るのは `pgdata` ボリュームが空の初回起動時のみ。ロール定義を変えたら `docker compose -f deploy/base/compose.yaml down -v` でボリュームごと作り直す
- 秘密の実ファイルは `deploy/dev/secrets/` に3つ必要（`db_password` / `app_db_password` / `app_database_url`）。`app_database_url` に埋め込むパスワードは `app_db_password` と同じ値にする
- **マイグレーションは `pb_owner` で接続する。** `pb_app` はDDLを実行できない（それが 3.4 のロール分離の目的）。`make migrate` は `db_password` から接続文字列を組み立てている
- initdb の `ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner` により、goose が作ったテーブルにも `pb_app` の DML 権限が自動で付く。マイグレーション後に GRANT を流す必要はない（`goose_db_version` も同様）
