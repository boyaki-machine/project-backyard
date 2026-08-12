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
| 2026-08-11 | 2 | **`DbDesign.md` 7.4 が `with_review`（+レビュー中）と `with_approval`（承認フロー付き）を「同様に定義する」と書くのみで、ステータス構成・遷移・`required_permission`・`allowed_actor_kinds` が未定義。** 推測で実装できない | 3テンプレートの定義をユーザー承認のうえ確定。**`DbDesign.md` 7.4 に反映済み**（2026-08-12）。0010 も3テンプレートに更新した |
| 2026-08-11 | 2 | `DbDesign.md` 5.1 がマイグレーションツールを「`sqlx-cli` / `golang-migrate` / `atlas`」としているが、採用は **goose v3**（`Design.md` 3.1、`CLAUDE.md`）。Rust 前提時代の記述が残っている | **`DbDesign.md` 5.1 に反映済み**（2026-08-12） |
| 2026-08-11 | 2 | `DbDesign.md` 5.2 のファイル構成が `migrations/` と書かれているが、実体は `server/migrations/`（`Design.md` 4.1 / 11章 手順2） | **`DbDesign.md` 5.2 に反映済み**（2026-08-12） |
| 2026-08-11 | 2 | **goose の実行方法（インストール・接続文字列の渡し方）が設計文書のどこにも書かれていない。** ホストに Go も goose も入っていなかった | `server/go.mod` の `tool` ディレクティブで goose を固定し、`make migrate` から `go tool goose` で呼ぶ方式を**ユーザー承認のうえ採用**。**`DbDesign.md` 5.1「goose の導入と実行」に反映済み**（2026-08-12） |
| 2026-08-12 | 2 | 7.4 の確定に伴い、適用済みの 0010 を編集した（`DbDesign.md` 5.3「適用済みファイルを編集しない」に形式上抵触する） | **0010 は `develop` へ出ておらず、検証用のローカルDB1環境にしか当たっていない**ため、環境間の不整合は起こり得ないと判断した。ボリュームごと作り直して 0001〜0010 を再適用し、全検証をやり直している。`develop` へマージした後は、テンプレート追加も含めて前進のみ（0011 以降）とする |
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

### 設計文書へ反映済みの修正（手順2、2026-08-12、承認のうえ適用）

1. **`DbDesign.md` 7.4** — `with_review`（4ステータス／5遷移）と `with_approval`（5ステータス／7遷移）の
   DDLを追記。3テンプレートの比較表、`approval` を `category='review'` とした理由、
   差し戻し遷移を必須とする理由、固定ULIDの採番規約（`…W<n>` / `…S<n>` / `…T<n>`）も明記した
2. **`DbDesign.md` 5.1** — ツール名を **goose v3** に修正し、「goose の導入と実行」を追記。
   `tool` ディレクティブによるバージョン固定、接続は `pb_owner`、接続文字列を recipe 内で組み立てて
   `ps` の argv に残さないこと、`+goose Up` / `StatementBegin` の扱い、**注釈以外にDDLへ手を入れない**旨
3. **`DbDesign.md` 5.2** — ファイル構成のパスを `migrations/` から `server/migrations/` に修正

### 手順2の照合結果

`DbDesign.md` 6.1〜6.9 と 7.1〜7.4 の `sql` ブロックを抽出し、`server/migrations/*.sql` から
コメントと goose 注釈を除いたものと機械的に比較した。**コメントを除くSQL 526行が完全一致**。

差分として出るのは 6.4.1（チケット採番の `UPDATE ... RETURNING`）のみで、これはアプリが実行する
クエリでありDDLではないため、マイグレーションに含まれないのが正しい。

適用結果（`down -v` でボリュームごと作り直したうえで再検証）：

| 項目 | 実測 |
|---|---|
| テーブル | 23（業務22 + `goose_db_version`） |
| `permission` / `role` / `role_permission` | 28 / 5 / 75 |
| ワークフローテンプレート | `simple` 3ステータス3遷移、`with_review` 4/5、`with_approval` 5/7 |
| エージェント到達不可のステータス | `simple.done` / `with_review.done` / `with_approval.approval` / `with_approval.done` |
| 固定ULID | 30件すべて26文字・Crockford Base32 適合 |
| `pb_app` | `SELECT` 可、`CREATE TABLE` / `DROP TABLE` は拒否、`statement_timeout=15s` |
| 冪等性 | `make migrate` 再実行が no-op。0010 の直接再適用でも件数不変 |

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
