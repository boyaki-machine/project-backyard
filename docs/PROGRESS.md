# 実装進捗

`Design.md` 11章の手順に対応。**ステップを完了したら必ずこの表を更新すること。**

## Phase 1

| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 完了 | 2026-08-11 | `make up` でDBが起動し、`pb_app` ロールが存在する |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 完了 | 2026-08-11 | `make migrate` 後、テーブル23個と権限28件・ロール5件が存在する |
| 3 | `pb admin create` による初期管理者作成 | 完了 | 2026-08-12 | 作成した管理者が `app_user` に `system_role='administrator'` で入る |
| 4a | 共通基盤その1（sqlc導入・エラー形式・ページネーション・request_id・アクセスログ・ヘルスチェック・serve骨格） | 完了 | 2026-08-12 | `go test ./internal/httpapi/...` が通り、`make run` 後に `/healthcheck` が `{"status":"OK"}`、未知パスが 2.5 形式の 404 を返す |
| 4b | 共通基盤その2（認証ミドルウェア・監査ログ） | 完了 | 2026-08-12 | `go test ./internal/httpapi/...` が通る。`PB_TEST_DATABASE_URL` を与えると実DBに対する認証の結合テストも通る |
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
| エージェントの実行権限の整理（`.claude/settings.json`）と `CLAUDE.md` 絶対規則2の具体化 | 完了 | 2026-08-12 | `feature/step-04-http-foundation-auth` | 読み取り系コマンドが確認なしで通り、`rm` / `git merge` / `git push` などは確認を求める |

## バージョンの現況

| | 値 |
|---|---|
| 現在 | **v1.4.4**（マージ前。マージ後に `make version-check` が通る） |
| 内訳 | メジャー1 / マイナー4 / ビルド4 |
| ビルド1 | 手順2（`feature/step-02-migrations`）のマージ |
| ビルド2 | バージョン運用の導入（`feature/versioning`）のマージ |
| ビルド3 | 手順3（`feature/step-03-admin-create`）のマージ |
| ビルド4 | 手順4a・4b（`feature/step-04a-http-foundation`）のマージ |

手順 4a と 4b は**同じブランチに載せて1回のマージにする**（ユーザーの選択、2026-08-12）。
4b は 4a の成果物（`apierr` / `middleware` / `store/gen` / `router`）の上に積むため、
4a を `develop` に出さないまま 4b を始める場合、`develop` から切ると土台が無い。
マージ回数は1回のままなのでビルド番号は4で変わらない。

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
| 2026-08-12 | 3 | `app_user.email` は `citext`（比較時のみ大小を無視）だが `user_identity.subject` は `text`（区別あり）。`Design.md` 6.2.1 のログインは ①email で `app_user` を引く → ②`(local, subject=email)` で `user_identity` を引く、という順序のため、**①は通るのに②で外れる**という非対称が生まれる。実測で「①1件 / ②0件」を再現した | **入力された表記のまま両列に格納する**（RFC 5321 §2.4「ローカル部の大小は保存せよ」）。照合の大小無視は citext に委ねる。**手順5のログインは、利用者の入力ではなく①で引き当てた `app_user.email` の値で②を引くこと。** 実装当初は小文字へ正規化していたが、RFC の保存要求に反するうえ citext があれば不要なため取りやめた（下記の検討経緯を参照） |
| 2026-08-12 | 3 | 7.5 のCLI例はパスワードを1回しか尋ねていない。だが唯一の管理者を打ち間違えると誰もログインできないインスタンスができる | **確認のため2回入力させた。** 文書の変更は提案しない（画面遷移ではなくCLIのUX詳細のため）。不要であれば削る |
| 2026-08-12 | 3 | 管理者作成を `audit_log` に記録するかが 7.5 に無い | **記録していない。** 監査ログの共通基盤は手順4のスコープ。手順4で「CLI由来の操作をどう記録するか（`actor_id` は作成された本人か、それとも NULL か）」を決める必要がある |
| 2026-08-12 | 3 | `Makefile` の `LDFLAGS` が `-X main.version` を指しているのに `main` パッケージが存在しなかった（`-X` は存在しないシンボルを黙って無視する） | `cmd/pb/main.go` に `var version = "dev"` と `pb version` サブコマンドを置いた。`go run -ldflags "-X main.version=1.2.2"` で `pb v1.2.2` が出ることを確認済み |
| 2026-08-12 | 3 | `CLAUDE.md` に `make test` があるのに Makefile に未定義だった（手順3で初めてGoのテストが入る） | `test` ターゲットを追加した（`cd server && go test ./...`） |
| 2026-08-12 | 3 | `go get` が `go.mod` の go ディレクティブを 1.24 → 1.25.0 に自動で引き上げた。`x/term` の最新（v0.45.0）が go 1.25 を要求するため。`Design.md` 3.1 の「Go 1.24 以上」と衝突する | goose と同じ扱いで、`golang.org/x/term` を **v0.33.0**、`golang.org/x/sys` を **v0.34.0** に固定し、go ディレクティブを 1.24 に戻した。`go build` / `go mod tidy` 後も 1.24 のまま保たれることを確認済み |
| 2026-08-12 | 4a | 手順4の分量（sqlc導入＋HTTP規約2種＋DBを引くミドルウェア2種）が1セッションに多い | **4a / 4b に分割することをユーザーが選択。** 4a はDBを読まない層まで（本ステップ）、4b が認証ミドルウェアと監査ログ。`PROGRESS.md` の手順4行も 4a / 4b に分けた |
| 2026-08-12 | 4a | **sqlc を `server/go.mod` の tool ディレクティブに足すと、indirect が 59→83 件に増え、さらに go ディレクティブが 1.25.0 へ引き上げられる**（`Design.md` 3.1 の「Go 1.24 以上」と衝突）。手順2で予告していた分岐点 | **goose と sqlc を `server/tools/go.mod` へ隔離することをユーザーが承認。** `server/go.mod` は実依存5件＋indirect 8件に戻った。**`DbDesign.md` 5.1 と `Design.md` 4.1 に反映済み**（2026-08-12） |
| 2026-08-12 | 4a | `pb serve` の骨格を手順4に含めるか。`PROGRESS.md` の検証欄は `go test` のみだった | **含めることをユーザーが選択。** エンドポイントは1つも定義せず、ミドルウェア連鎖と 2.5 形式の 404 のみ。`Design.md` 4.1 に serve の記載があり `PB_BIND` も config に実装済みのため、新しい仕様の発明にはならない |
| 2026-08-12 | 4a | CSRF（`ApiDesign.md` 2.4）とレート制限（2.9）を手順4に含めるか。`Design.md` 4.1 は両方を `middleware/` 配下に置いている | **含めないことをユーザーが選択。** どちらも手順5のログインで初めて必要になり（CSRF は `pb_csrf` の発行と対、レート制限は失敗回数と対）、手順5で実装するほうが検証しやすい |
| 2026-08-12 | 4a | **`ApiDesign.md` 2.5.1 の表に 405（Method Not Allowed）の行が無い。** chi の既定は本文なしの 405 を返すため、`CLAUDE.md`「エラー応答は 2.5 の形式に統一する」に反する | **`405 / method_not_allowed` を 2.5.1 に追加することをユーザーが承認**（2026-08-12）。実装も 405 を返す。コードは14件になった。**`ApiDesign.md` 2.5.1 に反映済み** |
| 2026-08-12 | 4a | **2.5.1 の13コードのうち、文言が設計文書にあるのは2件のみ**（`validation_failed` は 2.5、`invalid_credentials` は `Design.md` 6.3） | 残る11件の既定文言を実装側で定めた（`apierr.messages`）。方針は「アカウントの存在を漏らさない」「原因ではなく利用者の次の行動を書く」。**文言を設計文書側に持たせたい場合は 2.5.1 に message 列を足す修正を提案する** |
| 2026-08-12 | 4a | **`ApiDesign.md` 2.6 が、不正な `page` / `per_page` / `sort` / `order` を受けたときの挙動を定めていない**（既定へ丸めるのか、エラーにするのか） | **422 `validation_failed` を返し `details` に項目ごとの誤りを載せる方針をユーザーが承認**（2026-08-12）。`per_page` の上限超過も丸めない。入力と応答の対応表つきで **`ApiDesign.md` 2.6 に反映済み** |
| 2026-08-12 | 4a | `request_id` をコンテキストへ出し入れする関数の置き場所。`middleware` に置くと `apierr` → `middleware` の依存が生まれ、`middleware` は 404 応答のため `apierr` を必要とするので循環する | **`apierr` パッケージに置いた。** `request_id` は 2.5 のエラー本体のフィールドであり、描画するのが `apierr` の責務のため。`Design.md` 4.1 のディレクトリ構成を変えずに済む（新しいパッケージを足していない） |
| 2026-08-12 | 4a | HTTPサーバのタイムアウト値が設計文書のどこにも無い | 実装側の既定として ReadHeader 10s / Read 30s / Write 60s / Idle 120s / Shutdown 猶予 15s を置いた（`serve.go` に定数として明記）。**`Design.md` 10章「メトリクスとヘルスチェックエンドポイント」を扱う際に、あわせて文書化を提案する** |
| 2026-08-12 | 4a | アクセスログ（1リクエスト1行）を出すかどうか。`Design.md` 10章は「アプリケーションログの形式」を**今後扱う**としていた | **ユーザーの指示により形式を確定し実装した。** コンテナ前提のため**標準出力へ構造化JSON**（12-factor）。`stderr` から `stdout` へ変更。アクセスログとエラーログは**別行**とし、1行の意味を「1リクエストの結果」に保つ。**`Design.md` 10.1 に反映済み** |
| 2026-08-12 | 4a | ログ出力先を stdout / stderr のどちらにするか。k8s / CRI はどちらも同じログストリームへ集約するため、`kubectl logs` からはどちらでも見える | **stdout に一本化した。** ①`stderr` は「異常」の含意を持ち、正常なアクセスログを流すと収集基盤（Fluent Bit / Loki 等）で誤って error 扱いする設定を誘発しやすい ②2ストリームに分けると行の到着順が保証されない。`stderr` はロガー初期化前の致命的エラーと CLI の利用者向けメッセージにのみ使う |
| 2026-08-12 | 4a | ヘルスチェックエンドポイントを作るか。`Design.md` 10章が「今後扱う」としており `ApiDesign.md` にも定義が無かった | **ユーザーの指示により `GET /healthcheck` を新設した**（認証不要・副作用なし・DB非依存・固定応答）。**`ApiDesign.md` 2.11 / `Design.md` 10.2 に反映済み** |
| 2026-08-12 | 4a | `/healthcheck` に**バージョンを含めるか。** 未認証の呼び出し元への情報開示になる | **設定 `PB_HEALTH_SHOW_VERSION` で切り替える方式をユーザーが指示。既定は false**（既知の脆弱性との突き合わせを許さないため）。`make run` では true にして開発時のデプロイ確認に使えるようにした |
| 2026-08-12 | 4a | `/healthcheck` を `/api/v1` の下に置くか、ルート直下か | **ユーザーの指示によりルート直下 `/healthcheck`。** 監視・オーケストレータから叩くものでありAPIのバージョニングに従わせる意味がないため。**`Design.md` 3.4 の SPA フォールバック（`/api` `/mcp` 以外は index.html）の例外**にあたるので、3.4 と 2.11 の双方に明記した。**手順13 で embed 経路を作る際に取りこぼさないこと** |
| 2026-08-12 | 4a | probe が短間隔で叩く `/healthcheck` のアクセスログが1日数千行のノイズになる | **成功時のみ DEBUG に落とす。** 当初はパス一致だけで黙らせていたが、**`POST /healthcheck` の 405 まで消えることを実測で発見**したため、`status < 400` の条件を足した。probe の設定誤りに気づけなくなるのを避ける |
| 2026-08-12 | 4a | 設定項目が2つ増えた（`PB_LOG_LEVEL` / `PB_HEALTH_SHOW_VERSION`） | `config.go`・`deploy/base/env.example`・`deploy/base/compose.yaml`・`DbDesign.md` 3.2 の4か所を揃えた。**不正な値は既定へ倒さずエラーにする**（設定の書き誤りを黙って無視しないため） |
| 2026-08-12 | 4a | compose の `app` サービスに `healthcheck` を足すか | **足していない。** distroless / scratch イメージには curl も shell も無く、`healthcheck` の実行手段が `deploy/Dockerfile` の作り方に依存するため。**手順13 で Dockerfile とセットで決める**（`pb healthcheck` サブコマンドを足す案がある） |
| 2026-08-12 | 4b | **`ApiDesign.md` 2.5 は「`request_id` は `audit_log.id` と突き合わせられる」、`Design.md` 10.1 は「`request_id` で突き合わせる」と書くが、`DbDesign.md` 6.8 の `audit_log` に `request_id` 列が無い**（`activity` には有る）。`audit_log.id = request_id` と読む案は成立しない。`POST /me/password` が `password.change` と `session.revoke` の2行を書く（`Design.md` 6.3）ため主キーが衝突する | **マイグレーション 0011 で `request_id char(26) COLLATE "C"` を追加することをユーザーが承認**（2026-08-12）。`activity` と同じ形・同じ意味。索引は `activity` 同様に張らない。**`DbDesign.md` 6.8 / 5.2 と `ApiDesign.md` 2.5 に反映済み** |
| 2026-08-12 | 4b | 0011 は `DbDesign.md` 8章で Phase 2 の `agent.sql` に予約されていた | **Phase 2 / 3 の採番を 0012〜0019 へ1つずらした。** **`DbDesign.md` 8章と `Design.md` 11章（手順17・24）に反映済み**。ずらした理由も 8章に1行残した |
| 2026-08-12 | 4b | **CLI（`pb admin create`）由来の操作を `audit_log` に残すか**、`actor_id` に誰を入れるか（手順3の積み残し） | **`actor_id=NULL` / `actor_kind='system'` / `actor_label='pb admin create (CLI)'` で記録することをユーザーが承認**（2026-08-12）。端末の操作者にはまだアカウントが無いため、作成された本人を actor に据えると偽の帰属になる。誰を作ったかは `target_type='app_user'` / `target_id` に残る。`ip` / `user_agent` / `request_id` は NULL |
| 2026-08-12 | 4b | 監査ログの書き込みが失敗したとき、業務処理も失敗させるか（文書に記載なし） | **業務トランザクションを持つ操作は同一 tx で一緒に失敗させる（`Record`）。認証イベント（`login.*` / `logout`）は ERROR ログを残して続行する（`RecordOrLog`）。** 監査DBの一時障害でログイン不能にすると、可用性の低下が監査の欠落より重い被害になるため。`pb admin create` は前者を使い、監査記録を伴わない管理者アカウントが生まれないようにした（実測でロールバックを確認） |
| 2026-08-12 | 4b | `RecordOrLog` がリクエストのキャンセルを引き継ぐと、クライアントが接続を切るだけで `login.failure` の記録を落とせる | **`context.WithoutCancel` ＋ 5秒のタイムアウトで書く。** 総当たりの痕跡を攻撃者側から消せる状態にしないため。テストで再現している（`TestRecordOrLogIgnoresCanceledContext`） |
| 2026-08-12 | 4b | `actor.is_active=false` のトークンを受けたときの応答（`Design.md` 6.2.2 は `revoked_at` / `expires_at` しか書いていない） | **401 `unauthenticated`。** `ApiDesign.md` 3.1 が「アカウント無効も認証失敗と区別しない」としており揃えた。403 にすると「そのアカウントは存在する」と漏れる |
| 2026-08-12 | 4b | `access_token.expires_at` が NULL のときの扱い（6.2.2 は `expires_at > now()` としか書いていない。SQL では NULL は false になる） | **NULL は無期限として通す。** API トークン（`ApiDesign.md` 4.5）は `expires_in_days` が任意のため。**セッションには手順5で必ず期限を設定すること**（`ApiDesign.md` 3.1 の `Max-Age=1209600` = 14日） |
| 2026-08-12 | 4b | Cookie と `Authorization: Bearer` の両方が付いたときにどちらを採るか（文書に記載なし） | **Cookie を優先する。** `ApiDesign.md` 2.4 の CSRF は「Cookie 認証のときだけ要求する」規約であり、Cookie が付いているのに Bearer 側を採ると CSRF の対象外になってしまう。あわせて `Principal.Source`（`cookie` / `bearer`）を持たせた。**手順5の CSRF ミドルウェアは `token_type` ではなくこの値で判定すること**（api トークンを Cookie に載せることも技術的には可能なため） |
| 2026-08-12 | 4b | `access_token.scopes`（jsonb）が壊れていた場合の扱い | **500 にして通さない。** 空スライスに倒すと「絞り込みなし」＝ロールの全権限を意味してしまい（`Design.md` 6.4.1）、絞ったはずのエージェントトークンが全権限で通る |
| 2026-08-12 | 4b | 認証ミドルウェアをこの手順でルータに組み込むか | **組み込むことをユーザーが承認。** `/api/v1` 配下に認証必須の `r.Group` を置いた。所属するルートが0件なので外形上の挙動は変わらない。手順5は「login はグループの外、`/me` は中」と置くだけになる |
| 2026-08-12 | 4b | フェイクの `gen.Querier` でテストすると `queries/auth.sql` の SQL が一度も実行されない（列名・JOIN の向き・`last_used_at` の間引き条件が未検証のまま残る） | **`PB_TEST_DATABASE_URL` があるときだけ走る結合テストを追加した**（`internal/httpapi/auth_integration_test.go`）。未設定ならスキップするため `make test` は DB 無しでも通る。**DBを使うテストの作法として文書化するかは要判断**（現状 `Design.md` に記述が無い） |
| 2026-08-12 | 4b | `audit_log.ip` とアクセスログの `ip` を同じ値にする規約（`Design.md` 10.1）に対し、実装が2か所に分かれていた | `audit.ClientIP` に一本化し、`middleware.clientIP` はそれを呼ぶだけにした（スコープ外の変更だが、規約が求める同一性を保つために必要）。あわせて IPv4-mapped IPv6（`::ffff:127.0.0.1`）を IPv4 に畳むようにした。`inet` 列に同じ相手が2通りで入るのを避けるため |
| 2026-08-12 | 4b | 実効権限（`Design.md` 6.4.1）を `Principal` に持たせるか | **持たせない。** 権限の計算とセッションへのキャッシュ（6.4.5）は手順6の `RequirePermission` のスコープ。`Principal` が持つのはその入力になる素材（`system_role` / `scopes` / `project_id`）まで |

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

### メールアドレスの大小の扱い（手順3、2026-08-12 に検討・結論）

**結論：`citext` を維持する。値は入力された表記のまま保存し、照合のみ大小を無視する。**

`citext` を採用した理由は `DbDesign.md` 3.1 / 4.1 に「大文字小文字を区別しない一意制約」とあるが、
**なぜそれが要るかは文書化されていない。** 手順3で下記を検討したため、結論と根拠を残す。

| 論点 | 確認したこと |
|---|---|
| RFC 5321 §2.4 | ドメイン部は大小を区別しない。**ローカル部は区別する（MUST）**が、それを活用することは相互運用性を損なうとして discouraged。ローカル部の意味を解釈できるのは受信ホストのみ（§2.3.11） |
| したがって `A@hoge.com` と `a@hoge.com` は | **仕様上「同じ」とは言えない。** 別ユーザーとして扱う設計も正当であり、その場合 `text` に揃えれば型の非対称も解消する |
| `citext` の実際の挙動（実測） | `Tanaka@Example.com` は**そのまま格納される**。`TANAKA@EXAMPLE.COM` で検索すると引ける。`tanaka@example.com` の追加は UNIQUE 違反で拒否。※ このDBは `locale=C` のため**非ASCIIは同一視されない**（`Á` ≠ `á`） |
| `subject` を `citext` にする案 | **採らない。** OIDC の `sub` / SAML の NameID を入れる列であり、それらは大小を区別する識別子のため |

**判断の分かれ目は「失敗に気づけるか」に置いた。**

- `citext`：本当に別人の2人がいた場合、2人目の登録が「既に登録されています」で**明示的に失敗する**。管理者がエイリアスを払い出せば回避できる
- `text`：同一人物が別アカウントに分裂しても、ログインできなくても、`Design.md` 6.3 がエラーを
  「メールアドレスまたはパスワードが正しくありません」に統一しているため**原因を特定できない**

加えて、大小で人を識別する運用は口頭・名刺・他SaaS で表記を保てず、主要なメールサービス
（Microsoft 365 / Google Workspace）ではそもそも大小違いのメールボックスを作れない。

**`text` に変更する場合に必要なもの**（今回は採らなかったが、再検討時のために記録）：
`DbDesign.md` 3.1 / 4.1 / 6.2 と `Design.md` 6.2.1 の修正案、マイグレーション 0011（前進のみ、
`ALTER TABLE app_user ALTER COLUMN email TYPE text`）、`citext` 拡張を残すかの判断。

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
| `make admin-create`（初回） | `actor` / `app_user` / `user_identity` / `local_credential` に各1行。`system_role='administrator'`、`kind='user'`、`provider_key='local'` |
| メールの大小の保存 | `Suzuki@Example.com` で作成 → `app_user.email` と `user_identity.subject` の**両方が入力どおりの表記**で、かつ完全一致 |
| ログイン経路の再現 | 全小文字 `suzuki@example.com` で `app_user` を引き、その `email` の値で `subject` を引くと `Suzuki@Example.com` が取れる（`Design.md` 6.2.1 の①→②が通る） |
| `ON DELETE CASCADE` | 検証用レコードを `DELETE FROM actor` で1行消すと、`app_user` / `user_identity` / `local_credential` も連動して消える |
| `password_hash` | `$argon2id$v=19$m=65536,t=3,p=4$` で始まり全長97文字（ソルト16→22 + キー32→43）。PHC 文字列が欠けずに格納されている |
| 既に管理者がいる状態での再実行 | 「アドミニストレータが既に 1 件存在します」と警告し `[y/N]` で確認。既定 no で中止し、行数が変わらない |
| 重複メールでの作成 | `actor` の INSERT が成功した後に `app_user` で一意制約違反 → **全体がロールバックされ `actor` の残骸が残らない**（1/1/1/1 のまま） |
| 入力検証 | 空の表示名・61文字以上・メール形式不正・12文字未満のパスワード・確認との不一致で、いずれも再入力を促す |
| 端末での非表示入力 | `expect` で疑似端末から実行し、表示名とメールは表示され、パスワード2行は表示されないことを確認 |
| `pb version` | `go run -ldflags "-X main.version=1.2.2"` で `pb v1.2.2`、未指定で `pb vdev` |

### 設計文書へ反映済みの修正（手順4a、2026-08-12、承認のうえ適用）

1. **`DbDesign.md` 5.1** — 「goose の導入と実行」を「**goose・sqlc の導入と実行**」に改題し、
   ツールを `server/tools/go.mod` へ隔離する方針、隔離する理由（推移依存と go ディレクティブ）、
   ディレクトリ図、`migrate` / `sqlc` の recipe、生成物をコミットする旨、
   `server/tools/go.mod` の go ディレクティブを 1.24 に保つ注意を記載
2. **`Design.md` 4.1** — ディレクトリ図に `server/tools/go.mod` を追加し、
   `server/go.mod` に「アプリの依存のみ」と注記
3. **`ApiDesign.md` 2.5.1** — `| 405 | method_not_allowed | パスは存在するが、そのメソッドを受け付けない |` を追加（コードは13→14件）
4. **`ApiDesign.md` 2.6** — 不正な値を既定へ丸めず 422 `validation_failed` とする段落と、入力→応答の対応表を追加
5. **`ApiDesign.md` 2.11（新設）** — ヘルスチェック `GET /healthcheck`。認証不要・副作用なし・DB非依存、
   `PB_HEALTH_SHOW_VERSION` によるバージョン表示、SPAフォールバックの例外である旨
6. **`Design.md` 3.4** — SPAフォールバックの除外パスに `/healthcheck` を追加
7. **`Design.md` 10章** — 「今後扱う」から **10.1 アプリケーションログ**（出力先・形式・レベル・
   アクセスログ・エラーログ）、**10.2 ヘルスチェック**、**10.3 今後扱うもの** に再構成。目次の状態も更新
8. **`DbDesign.md` 3.2** — compose の `app` サービスに `PB_LOG_LEVEL` と `PB_HEALTH_SHOW_VERSION` を追加

### 手順4aで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/tools/go.mod` `go.sum` | goose v3.26.0 / sqlc v1.30.0 を tool ディレクティブで固定する専用モジュール |
| `server/sqlc.yaml` | pgx/v5 モード。`migrations/` をスキーマ源に、`internal/store/gen/` へ生成 |
| `server/internal/store/db.go` | pgxpool（Min 2 / Max 10、`application_name=pb`。`DbDesign.md` 3.5） |
| `server/internal/store/queries/user.sql` | 手順3の5クエリ（COUNT 1・INSERT 4）を sqlc へ移植 |
| `server/internal/store/gen/*.go` | sqlc 生成物4ファイル（`db.go` / `models.go` / `querier.go` / `user.sql.go`） |
| `server/internal/httpapi/apierr/apierr.go` | `ApiDesign.md` 2.5 のエラー形式、2.5.1 の14コードとステータス対応、`request_id` のコンテキスト受け渡し |
| `server/internal/httpapi/paging.go` | 2.6 のパラメータ解析（`ParsePage`）、一覧エンベロープ（`List[T]`）、`WriteJSON` |
| `server/internal/httpapi/router.go` | chi v5 のルータ組み立て。`/api/v1` の階層、`/healthcheck`、2.5 形式の 404 / 405 |
| `server/internal/httpapi/health.go` | `GET /healthcheck`（`ApiDesign.md` 2.11） |
| `server/internal/httpapi/middleware/requestid.go` | リクエストごとの ULID 発行 |
| `server/internal/httpapi/middleware/accesslog.go` | 1リクエスト1行のアクセスログ（`Design.md` 10.1） |
| `server/cmd/pb/serve.go` | `pb serve`。slog 初期化（stdout・レベル）・プール生成・待受・graceful shutdown |
| 各 `*_test.go` | apierr 9件 / paging 7件 / router 5件 / health 4件 / middleware 7件 / config 3件追加 |
| `server/cmd/pb/admin_create.go` | 直書き pgx → sqlc へ差し替え（変更） |
| `server/cmd/pb/main.go` | `serve` の振り分けと usage（変更） |
| `server/internal/config/config.go` | `PB_LOG_LEVEL` / `PB_HEALTH_SHOW_VERSION` を追加（変更） |
| `Makefile` | `sqlc` / `run` を追加、`migrate` を `server/tools` から実行するよう変更 |
| `deploy/base/env.example` `deploy/base/compose.yaml` | 追加した設定2件を反映（変更） |

**`internal/domain/` は作っていない。** ビジネスルールを持つ型が現れる手順以降で作る。

### 手順4aの検証結果

| 検証 | 結果 |
|---|---|
| `go build ./...` / `go vet ./...` / `make test` | いずれも通る（テスト35件追加、全パッケージ ok） |
| `go test ./internal/httpapi/...` | 3パッケージとも ok |
| `server/go.mod` の依存 | 実依存5件（pgx / argon2id / ulid / term / chi）＋ **indirect 8件**。goose 隔離前は 59件 |
| go ディレクティブ | `server/go.mod` `server/tools/go.mod` とも **1.24 のまま** |
| `make migrate`（新しい実行位置） | `no migrations to run. current version: 10`。`server/tools` からの `-dir ../migrations` が効いている |
| `make sqlc` の再現性 | 再実行しても `internal/store/gen/` に差分が出ない |
| `make admin-create`（sqlc 移植後） | 4テーブルに各1行。`kind='user'` / `system_role='administrator'` / `provider_key='local'`、`password_hash` が `$argon2id$v=19$m=65536,t=3,p=4` |
| メールの大小の保存 | `SqlcTest@Example.com` が `app_user.email` と `user_identity.subject` の両方に入力どおりの表記で入る（手順3から不変） |
| 重複メールでのロールバック | `sqlctest@example.com`（小文字）で一意制約違反 → 全体がロールバックし `actor` の残骸が残らない。citext の大小無視も維持 |
| `make run` | `{"level":"INFO","msg":"サーバを起動した","bind":"127.0.0.1:8080","version":"1.3.3"}`。slog の JSON と `-X main.version` が効いている |
| 未知パスの応答 | `GET /api/v1/nope`・`GET /`・`POST /` のいずれも `404` ＋ `{"error":{"code":"not_found","message":"対象が見つかりません","request_id":"01KZT…"}}`。`Content-Type: application/json; charset=utf-8` |
| `request_id` | 26文字の ULID。リクエストごとに変わる。クライアントの `X-Request-Id` は採用しない |
| `GET /healthcheck` | `200` ＋ `{"status":"OK"}`。`PB_HEALTH_SHOW_VERSION=true`（`make run`）では `{"status":"OK","version":"1.4.4"}` |
| `POST /healthcheck` | `405` ＋ `{"error":{"code":"method_not_allowed",…}}` |
| `GET /api/v1/healthcheck` | `404`。`/healthcheck` は `/api/v1` の外にある |
| ログ出力先 | **stdout のみ**。サーバ起動から停止まで stderr は空 |
| アクセスログ | `{"level":"INFO","msg":"request","request_id":"01KZTC…","method":"GET","path":"/api/v1/nope","status":404,"duration_ms":0.026,"bytes":116,"ip":"127.0.0.1"}`。クエリ文字列は出ない |
| `/healthcheck` のログ抑止 | 既定レベルでは 200 が出ず、`POST`（405）は出る。`PB_LOG_LEVEL=debug` にすると 200 も `DEBUG` で出る |
| 設定値の検証 | `PB_LOG_LEVEL=verbose` と `PB_HEALTH_SHOW_VERSION=yes` はいずれも起動時にエラーで停止する |
| 接続プール | `pg_stat_activity` に `application_name=pb` / `usename=pb_app` で3接続（Max 10 以内） |
| graceful shutdown | SIGINT で「停止信号を受け取った」→「サーバを停止した」の順にログが出て、プロセスが残らない |

### 設計文書へ反映済みの修正（手順4b、2026-08-12、承認のうえ適用）

1. **`DbDesign.md` 6.8** — `audit_log` の DDL に `request_id char(26) COLLATE "C"` を追加。
   `activity` と同じ意味で持つ旨、`audit_log.id = request_id` 案を採らない理由
   （1リクエストが複数行を書く）、CLI 由来では NULL になる旨、0011 で足した旨を追記
2. **`DbDesign.md` 5.2** — ファイル構成に `0011_audit_log_request_id.sql` を追加
3. **`DbDesign.md` 8章** — Phase 2 / 3 の採番を `0011〜0018` から **`0012〜0019`** へずらし、
   ずらした理由を1行追記
4. **`Design.md` 11章** — 手順17 を `0012〜0015`、手順24 を `0016〜0019` に修正
5. **`ApiDesign.md` 2.5** — 「`request_id` は `audit_log.id` …」を
   **「`audit_log.request_id`（`DbDesign.md` 6.8）および構造化ログ（`Design.md` 10.1）」** に修正
6. **`Design.md` 4.1** — ディレクトリ図に `internal/audit/` を追加

### 手順4bで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/migrations/0011_audit_log_request_id.sql` | `audit_log` に `request_id` を追加（前進のみ。0008 は編集していない） |
| `server/internal/auth/token.go` | 平文トークンの生成（`pb_sess_` / `pb_api_` + base64url 32バイト）、SHA-256（小文字16進64文字）、`token_prefix`、`Authorization: Bearer` の解析 |
| `server/internal/auth/principal.go` | `Principal` 型とコンテキスト受け渡し、`CredentialSource`、Cookie / ヘッダ名の定数、`scopes` の JSON 変換、`AuditLabel` |
| `server/internal/store/queries/auth.sql` | `FindAccessTokenByHash`（`actor` と JOIN、`app_user` は LEFT JOIN）、`TouchAccessTokenLastUsed`（1分粒度） |
| `server/internal/store/queries/audit.sql` | `InsertAuditLog` |
| `server/internal/store/gen/auth.sql.go` `audit.sql.go` | sqlc 生成物 |
| `server/internal/audit/audit.go` | 監査ログの共通基盤。15アクションの定数、`FromRequest` / `FromCLI`、`Record` / `RecordOrLog`、`ClientIP` |
| `server/internal/httpapi/middleware/auth.go` | `Authenticate`。Cookie / Bearer → SHA-256 → 検証 → `Principal` をコンテキストへ |
| `server/internal/httpapi/auth_integration_test.go` | 実DBに対する認証経路の結合テスト（`PB_TEST_DATABASE_URL` 未設定ならスキップ） |
| 各 `*_test.go` | auth 21件 / audit 13件 / middleware 13件 / router 2件追加（全体で121件） |
| `server/internal/httpapi/router.go` | 認証必須グループを追加。`Deps.Queries` を追加（変更） |
| `server/internal/httpapi/middleware/accesslog.go` | `clientIP` を `audit.ClientIP` に委譲（変更） |
| `server/cmd/pb/admin_create.go` | `user.create` の監査記録を同一トランザクションで追加（変更） |
| `server/cmd/pb/serve.go` | 先頭コメントの更新のみ（変更） |

**`internal/domain/` は引き続き作っていない。** CSRF・レート制限・`RequirePermission` は手順5・6。

### 手順4bの検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（テスト121件、7パッケージすべて ok） |
| `go test ./internal/httpapi/...` | 3パッケージとも ok |
| `make sqlc` の再現性 | 再実行しても `internal/store/gen/` に差分が出ない |
| `make migrate` | `0011_audit_log_request_id.sql` が適用され version 11。再実行は `no migrations to run` |
| `audit_log` の列 | `request_id | character(26) | C | nullable` が末尾に付き、`activity` と同じ形。既存の3索引と CHECK は不変 |
| 認証の結合テスト（実DB） | 有効トークンで通り、`system_role`（LEFT JOIN）と `scopes`（jsonb）が載る。失効後は 401、未知トークンも 401 |
| `last_used_at` の間引き | 1回目で記録され、直後の2回目では**変わらない**。2分前に巻き戻すと3回目で更新される（`Design.md` 6.2.2 の1分粒度） |
| ミドルウェア単体 | Cookie / Bearer / 両方（Cookie 優先）/ 空 Cookie / 資格情報なし / 未知 / 失効 / 期限切れ / 無効アクター / `expires_at` NULL / DB障害 / 壊れた scopes / touch 失敗 の13ケース |
| 401 の応答 | 「無い」「失効」「期限切れ」「無効アクター」がすべて `{"error":{"code":"unauthenticated",…}}`。理由は `WithCause` でサーバログにのみ出る |
| `make admin-create` の監査記録 | `action='user.create'` / `actor_id=NULL` / `actor_kind='system'` / `actor_label='pb admin create (CLI)'` / `target_id`=作成した actor / `detail={"via":"pb admin create","system_role":"administrator"}` / `ip`・`user_agent`・`request_id` は NULL |
| 監査記録のロールバック | 重複メールで作成すると `actor` / `app_user` / `audit_log` のいずれも増えない（1トランザクション） |
| 平文トークンの非保存 | `access_token.token_hash` は全件が64文字の16進で `pb_` 始まりが0件。サーバログにも平文が出ない |
| `make run` | 起動・`/healthcheck` 200・未知パス 404・graceful shutdown が手順4a から不変。stderr は空のまま |
| アクセスログの `ip` | `audit.ClientIP` へ一本化した後も `127.0.0.1`。`X-Forwarded-For` は採らない（テストで固定） |

## 環境メモ

実際に動かして分かったこと（バージョンの相性、ハマった点、回避策）を追記する。
ここに書いた内容は、後から `CLAUDE.md` や設計文書へ昇格させることを検討する。

- **`.claude/settings.json` の deny は Read ツールにしか効かない。** `Read(./deploy/*/secrets/**)` を deny していても、`allow` にある `Bash(cat:*)` 経由では読めてしまう（手順4b の検証で `app_db_password` を実際にそう読んだ）。秘密を機械的に守りたい場合は Bash 側にも `deny` を足す必要がある
- **DBを使うテストは `PB_TEST_DATABASE_URL` で切り替える**（手順4b で導入）。未設定ならスキップするので `make test` は DB 無しでも通る。実行例：

  ```
  PW=$(cat deploy/dev/secrets/app_db_password)
  cd server && PB_TEST_DATABASE_URL="postgres://pb_app:${PW}@127.0.0.1:5432/pb" go test ./internal/httpapi/ -run Integration -v
  ```

  接続は `pb_app`（DML のみ）で行う。実運用と同じ権限で通ることを確かめるため
- **`t.Cleanup` は `defer` より後に走る。** 結合テストで `defer pool.Close()` と `t.Cleanup(削除)` を併用すると、後片付けの時点でプールが閉じていて `closed pool` になる。プールの close も `t.Cleanup` で登録し、LIFO の順序を使うこと
- Go の直接依存（手順4b時点）: `jackc/pgx/v5 v5.7.5` / `oklog/ulid/v2 v2.1.2` / `alexedwards/argon2id v1.0.0` / `golang.org/x/term v0.33.0` / `go-chi/chi/v5 v5.3.1`（手順4a から**増えていない**。トークンのハッシュと乱数は標準ライブラリの `crypto/sha256` / `crypto/rand` で足りる）
  - **`x/term` と `x/sys` はバージョンを上げないこと。** 最新版は go 1.25 を要求し、`go get` が go ディレクティブを勝手に 1.25.0 へ引き上げる（`Design.md` 3.1 と衝突）。上げる際は 3.1 の最低バージョンとセットで見直す
  - `go get` 後は `head -3 server/go.mod` で go ディレクティブが `1.24` のままか確認する
- **パスワード入力のエコー抑止には競合窓がある。** プロンプトを出してから `term.ReadPassword` が echo を切るまでの数マイクロ秒に文字が届くと、その分だけ端末に表示される。`expect` から遅延なしで送ると再現するが、人間の入力では起こらない（`sudo` や `ssh` も同じ挙動）
  - 端末ありの検証は `expect` に `sleep 0.4` を入れて行う。`printf ... | script -q /dev/null` は stdin を即座に閉じるため `EOF` になり検証に使えない
- goose のバージョン: **v3.26.0**（手順4a から `server/tools/go.mod` の tool ディレクティブで固定）
  - v3.27.3 以降は `go 1.25.7` を要求し、`Design.md` 3.1 の「Go 1.24 以上」と衝突するため上げていない
- sqlc のバージョン: **v1.30.0**（`server/tools/go.mod`）
  - v1.31.1 は `go 1.26.0` を要求するため上げていない。v1.30.0 自体は `go 1.23.0` 要求
  - **`go get -tool` は実行順で結果が変わる。** `tools/go.mod` に sqlc → goose の順で入れると go ディレクティブが 1.24 のまま保たれるが、goose → sqlc の順だと `x/*` が最新へ上がって 1.25.0 に書き換えられる。ツールを足したら必ず `head -3 server/tools/go.mod` を見る
  - 初回の `go tool sqlc` はビルドに20秒ほどかかる（2回目以降はキャッシュ）。cgo は不要だった
  - `citext` は sqlc が既定の対応を持たないため、`sqlc.yaml` の `overrides` で `string` に写している。`inet` は `*netip.Addr`、NULL 許容列は `pgtype.*` になる
- **ツールは `server/tools/go.mod` に隔離してある。** `make migrate` / `make sqlc` は `cd server/tools` してから `go tool` を呼ぶ。`server/go.mod` にツールを足さないこと（indirect が80件超に膨らみ、go ディレクティブも 1.25 へ上がる）
- ホストの Go: 1.26.5（Homebrew。手順2で導入）。`make migrate` / 手順3以降の `make run` / `make build` に必要
- `go tool goose` は初回のみモジュールをダウンロードする（60秒程度）。2回目以降はキャッシュが効く
- 手順1の検証環境: Docker 29.1.3 / Docker Compose v5.3.1 / macOS (darwin 25.6.0, arm64)
- ホストに `psql` が入っていないため、`make psql` は `docker compose exec db psql` でコンテナ内に入る
- `initdb/01_roles.sh` は**実行ビットを立てておくこと**。`:ro` マウントでもホスト側のファイルモードがそのまま使われる
- initdb スクリプトが走るのは `pgdata` ボリュームが空の初回起動時のみ。ロール定義を変えたら `docker compose -f deploy/base/compose.yaml down -v` でボリュームごと作り直す
- 秘密の実ファイルは `deploy/dev/secrets/` に3つ必要（`db_password` / `app_db_password` / `app_database_url`）。`app_database_url` に埋め込むパスワードは `app_db_password` と同じ値にする
- **マイグレーションは `pb_owner` で接続する。** `pb_app` はDDLを実行できない（それが 3.4 のロール分離の目的）。`make migrate` は `db_password` から接続文字列を組み立てている
- initdb の `ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner` により、goose が作ったテーブルにも `pb_app` の DML 権限が自動で付く。マイグレーション後に GRANT を流す必要はない（`goose_db_version` も同様）
