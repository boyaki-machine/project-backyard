# 実装進捗

`Design.md` 11章の手順に対応。**ステップを完了したら必ずこの表を更新すること。**

## Phase 1

| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 完了 | 2026-08-11 | `make up` でDBが起動し、`pb_app` ロールが存在する |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 完了 | 2026-08-11 | `make migrate` 後、テーブル23個と権限28件・ロール5件が存在する |
| 3 | `pb admin create` による初期管理者作成 | 完了 | 2026-08-12 | 作成した管理者が `app_user` に `system_role='administrator'` で入る |
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

## 手順外の作業

`Design.md` 11章の手順に属さない作業はここに記録する。

| 内容 | 状態 | 完了日 | ブランチ | 検証方法 |
|---|---|---|---|---|
| バージョン番号とビルド番号の運用（`Design.md` 11.1） | 完了 | 2026-08-12 | `feature/versioning` | マージ後に `make version-check` が通る |

## バージョンの現況

| | 値 |
|---|---|
| 現在 | **v1.3.3** |
| 内訳 | メジャー1 / マイナー3 / ビルド3 |
| ビルド1 | 手順2（`feature/step-02-migrations`）のマージ |
| ビルド2 | バージョン運用の導入（`feature/versioning`）のマージ |
| ビルド3 | 手順3（`feature/step-03-admin-create`）のマージ |

規約は `Design.md` 11.1。**マージ前に feature ブランチ上で `make bump-minor`（`fix/*`・`docs/*` は `make bump-build`）を実行し、`VERSION` の更新を同じブランチに含める。**

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
| 2026-08-12 | 手順外 | ビルド番号を `git rev-list --count --first-parent develop`（全コミット）で数えると、`develop` 上で `VERSION` を更新するたびに値が増え、`VERSION` が実測に追いつかず収束しない | **`--merges` を付けてマージコミットのみを数える**方式に変更した。「何回目の `develop` へのマージか」という定義とも正確に一致する。`Design.md` 11.1 に理由ごと明記済み |
| 2026-08-12 | 手順外 | `VERSION` の更新を `develop` 上で行うと「`develop` に直接コミットしない」（11.0）に反する | **更新は feature ブランチ側で行い、マージに含める。** `bump-*` は「これからマージする」前提でマージ回数 +1 を書き、マージ後に `make version-check` で検証する |
| 2026-08-12 | 手順外 | `feature/versioning` はビルド/リリースの仕組みであり、アプリの機能追加ではない。マイナーを上げるべきか判断が要った | **`feature/*` として扱いマイナーを上げた**（1.1→1.2）。バイナリがバージョンを報告できるようになる変更であり成果物の振る舞いが変わるため。異論があれば `VERSION` を `1.1.2` に直すだけで済む |
| 2026-08-11 | 2 | goose 最新の v3.27.3 は `go 1.25.7` を要求し、`Design.md` 3.1 の「Go 1.24 以上」と衝突する | goose を **v3.26.0**（`go 1.23.0` 要求）に固定し、`server/go.mod` の go ディレクティブは `1.24` を維持した。goose を上げる際は `Design.md` 3.1 の最低バージョンとセットで見直す |
| 2026-08-11 | 2 | goose を tool 依存にすると、goose が対応する全DBドライバ（clickhouse / mssql / ydb / sqlite / vertica 等）が `server/go.mod` に indirect 60件として載る。`Design.md` 3.1 の「外部ライブラリを増やさない」方針と見た目が衝突する | ビルド対象ではなくツール依存であり実行バイナリには入らないため、現状のまま進める。手順4で実依存が入った際に `// indirect` の量が問題になるようなら、goose を別モジュール（`server/tools/go.mod`）へ隔離する案を出す |
| 2026-08-11 | 2 | 設計文書のDDLには goose の注釈（`-- +goose Up`、`-- +goose StatementBegin`）が無い | goose のパーサ要件であり設計判断ではないため、注釈のみ機械的に付加した。`set_updated_at()` は本体に `;` を含むため `StatementBegin` / `StatementEnd` で囲む必要がある。**DDL 本体は 6〜7章と1文字も変えていない**（下記の照合結果） |
| 2026-08-11 | 2 | 検証欄の「テーブル23個」は goose の管理表 `goose_db_version` を含めた数。`DbDesign.md` 6章の業務テーブルは22個 | 記述の誤りではないため文書は変更しない。内訳は 6.2:6 / 6.3:4 / 6.4:2 / 6.5:3 / 6.6:2 / 6.7:2 / 6.8:2 / 6.9:1 = 22 |
| 2026-08-12 | 3 | `DbDesign.md` 7.5 の `パスワード: ************` を実現するには非エコー入力が要るが、`Design.md` 3.1 の採用技術表に該当ライブラリが無い | `golang.org/x/term` を**ユーザー承認のうえ 3.1 に追記**（2026-08-12）。非端末（検証スクリプトのパイプ入力）では通常の行読みにフォールバックする |
| 2026-08-12 | 3 | sqlc をこのステップで導入するか判断が要った。`admin create` のSQLは INSERT 4本と SELECT 1本のみ | **手順4へ回すことをユーザーが選択。** 手順3は pgx で直接クエリを書き、`internal/store/` を作っていない。移植対象は5クエリで小さい |
| 2026-08-12 | 3 | `deploy/dev/secrets/app_database_url` はコンテナ内から見た `db:5432` を指すため、ホストで動かす `pb admin create` から接続できない | `make admin-create` が `app_db_password` から `127.0.0.1:5432` 向けの接続文字列を recipe 内で組み立てる。既存の `migrate`（`db_password` から `pb_owner` 用を組み立て）と同じ方式で、新しい secret ファイルは増やしていない |
| 2026-08-12 | 3 | `<KEY>` と `<KEY>_FILE` の両方が設定された場合の優先順位が `DbDesign.md` 3.2 にも `env.example` にも無い | **`*_FILE` を優先**とした。より秘密を漏らしにくい経路のため。`config.go` にコメントで明記。異論があれば 3.2 に優先順位を1行足したい |
| 2026-08-12 | 3 | `app_user.email` は `citext`（大小区別なし）だが `user_identity.subject` は `text`（区別あり）。`Design.md` 6.2.1 のログインは ①email で `app_user` を引く → ②`(local, subject=email)` で `user_identity` を引く、という順序のため、格納時と入力時で大小が違うと②で外れる | 入力メールを**小文字に正規化**して両方に格納した。**手順5のログイン実装でも同じ正規化が要る**（`app_user.email` の値をそのまま subject 検索に使えば安全） |
| 2026-08-12 | 3 | 7.5 のCLI例はパスワードを1回しか尋ねていない。だが唯一の管理者を打ち間違えると誰もログインできないインスタンスができる | **確認のため2回入力させた。** 文書の変更は提案しない（画面遷移ではなくCLIのUX詳細のため）。不要であれば削る |
| 2026-08-12 | 3 | 管理者作成を `audit_log` に記録するかが 7.5 に無い | **記録していない。** 監査ログの共通基盤は手順4のスコープ。手順4で「CLI由来の操作をどう記録するか（`actor_id` は作成された本人か、それとも NULL か）」を決める必要がある |
| 2026-08-12 | 3 | `Makefile` の `LDFLAGS` が `-X main.version` を指しているのに `main` パッケージが存在しなかった（`-X` は存在しないシンボルを黙って無視する） | `cmd/pb/main.go` に `var version = "dev"` と `pb version` サブコマンドを置いた。`go run -ldflags "-X main.version=1.2.2"` で `pb v1.2.2` が出ることを確認済み |
| 2026-08-12 | 3 | `CLAUDE.md` に `make test` があるのに Makefile に未定義だった（手順3で初めてGoのテストが入る） | `test` ターゲットを追加した（`cd server && go test ./...`） |
| 2026-08-12 | 3 | `go get` が `go.mod` の go ディレクティブを 1.24 → 1.25.0 に自動で引き上げた。`x/term` の最新（v0.45.0）が go 1.25 を要求するため。`Design.md` 3.1 の「Go 1.24 以上」と衝突する | goose と同じ扱いで、`golang.org/x/term` を **v0.33.0**、`golang.org/x/sys` を **v0.34.0** に固定し、go ディレクティブを 1.24 に戻した。`go build` / `go mod tidy` 後も 1.24 のまま保たれることを確認済み |

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

### 設計文書へ反映済みの修正（手順3、2026-08-12、承認のうえ適用）

1. **`Design.md` 3.1** — 採用技術表に `| 対話入力 | golang.org/x/term | pb admin create のパスワードを非表示で読む（DbDesign.md 7.5） |` を追加

### 手順3で作成したファイル

| ファイル | 内容 |
|---|---|
| `server/cmd/pb/main.go` | サブコマンド振り分け（`admin` / `version` / `help`）。`serve` は手順5以降 |
| `server/cmd/pb/admin_create.go` | 対話入力と、4テーブルへの INSERT を1トランザクションで実行 |
| `server/internal/config/config.go` | 環境変数と `*_FILE` 展開（`PB_BIND` / `PB_DATABASE_URL` / `PB_LOG_FORMAT`） |
| `server/internal/auth/password.go` | Argon2id（`m=64MiB, t=3, p=4`）と最小長12の検証 |
| `server/internal/ulidgen/ulidgen.go` | ULID 生成 |
| 各 `*_test.go` | config 3件 / auth 4件 / ulidgen 1件 |
| `Makefile` | `admin-create` と `test` を追加 |

`internal/store/` は作っていない（sqlc とあわせて手順4）。

### 手順3の検証結果

| 検証 | 結果 |
|---|---|
| `go build ./...` / `go vet ./...` / `make test` | いずれも通る |
| `make admin-create`（初回） | `actor` / `app_user` / `user_identity` / `local_credential` に各1行。`system_role='administrator'`、`kind='user'`、`provider_key='local'`、`subject` = 小文字化したメール |
| `password_hash` | `$argon2id$v=19$m=65536,t=3,p=4$` で始まり全長97文字（ソルト16→22 + キー32→43）。PHC 文字列が欠けずに格納されている |
| 既に管理者がいる状態での再実行 | 「アドミニストレータが既に 1 件存在します」と警告し `[y/N]` で確認。既定 no で中止し、行数が変わらない |
| 重複メールでの作成 | `actor` の INSERT が成功した後に `app_user` で一意制約違反 → **全体がロールバックされ `actor` の残骸が残らない**（1/1/1/1 のまま） |
| 入力検証 | 空の表示名・61文字以上・メール形式不正・12文字未満のパスワード・確認との不一致で、いずれも再入力を促す |
| 端末での非表示入力 | `expect` で疑似端末から実行し、表示名とメールは表示され、パスワード2行は表示されないことを確認 |
| `pb version` | `go run -ldflags "-X main.version=1.2.2"` で `pb v1.2.2`、未指定で `pb vdev` |

## 環境メモ

実際に動かして分かったこと（バージョンの相性、ハマった点、回避策）を追記する。
ここに書いた内容は、後から `CLAUDE.md` や設計文書へ昇格させることを検討する。

- Go の直接依存（手順3時点）: `jackc/pgx/v5 v5.7.5` / `oklog/ulid/v2 v2.1.2` / `alexedwards/argon2id v1.0.0` / `golang.org/x/term v0.33.0`
  - **`x/term` と `x/sys` はバージョンを上げないこと。** 最新版は go 1.25 を要求し、`go get` が go ディレクティブを勝手に 1.25.0 へ引き上げる（`Design.md` 3.1 と衝突）。上げる際は 3.1 の最低バージョンとセットで見直す
  - `go get` 後は `head -3 server/go.mod` で go ディレクティブが `1.24` のままか確認する
- **パスワード入力のエコー抑止には競合窓がある。** プロンプトを出してから `term.ReadPassword` が echo を切るまでの数マイクロ秒に文字が届くと、その分だけ端末に表示される。`expect` から遅延なしで送ると再現するが、人間の入力では起こらない（`sudo` や `ssh` も同じ挙動）
  - 端末ありの検証は `expect` に `sleep 0.4` を入れて行う。`printf ... | script -q /dev/null` は stdin を即座に閉じるため `EOF` になり検証に使えない
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
