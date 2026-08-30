# 手順ごとの記録（Phase 1・手順2〜8）

`docs/PROGRESS.md` から分割した**過去の記録**（2026-08-18、`docs/progress-archive`）。
各手順で「何を作ったか（ファイル一覧）」と「どう検証したか（検証結果）」を、
実施当時のまま保管している。**毎セッションで読む文書ではない。**
既存コードの由来や過去の検証手順を調べたいときに grep して、必要な節だけを読むこと。

手順9以降はこの形式の節を作っていない（`PROGRESS.md` の進捗表と
`history/decisions.md` に記録がある）。新しい手順の記録はこの文書の末尾に追記する。

---

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

### 手順5aで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/internal/httpapi/v1/routes.go` | `/api/v1` のルート定義（認証不要／認証必須の2群）と `Deps`・`handler` |
| `server/internal/httpapi/v1/login.go` | `POST /auth/login`。`Design.md` 6.2.1 の8手順、ロック（6.3）、入力検証、監査記録 |
| `server/internal/httpapi/v1/logout.go` | `POST /auth/logout`。失効・Cookie削除・監査記録 |
| `server/internal/httpapi/v1/me.go` | `GET /me` と、3.1 / 4.1 で共通の応答組み立て（実効権限の計算を含む） |
| `server/internal/httpapi/v1/session.go` | セッション発行、`pb_session` / `pb_csrf` の付与・削除 |
| `server/internal/httpapi/v1/apitime.go` | 応答の日時を ISO8601 UTC・秒精度に固定する型（`ApiDesign.md` 2.2） |
| `server/internal/auth/permissions.go` | 6.4.1 の式（`EffectivePermissions` / `HasPermission`） |
| `server/internal/store/queries/authz.sql` | `ListRolePermissions`、`ListProjectMembershipsByActor` |
| `server/internal/httpapi/v1/auth_integration_test.go` | 実DBに対するログイン〜ログアウトの結合テスト |
| 各 `*_test.go` | v1 は 41件（login 22 / me 8 / logout 3 / paging 7 / 結合1）、auth に permissions 8件・password 5件、apierr 2件、config 1件を追加 |
| `server/internal/httpapi/v1/paging.go` `paging_test.go` | 4a で `httpapi` に作ったものを**移動**（`package v1`。内容は変えていない） |
| `server/internal/httpapi/router.go` | `v1.Mount` を呼ぶ形に変更。テスト用の `newRouter`（extra 注入）を削除（変更） |
| `server/internal/httpapi/health.go` | `WriteJSON` 依存を外し自前で書き出す（変更） |
| `server/internal/httpapi/apierr/apierr.go` | `RetryAfterSec` と `WithRetryAfter`、`Retry-After` ヘッダ（変更） |
| `server/internal/auth/password.go` | `NeedsRehash` / `VerifyAgainstDummy` を追加（変更） |
| `server/internal/auth/token.go` | `NewCSRFToken` を追加（変更） |
| `server/internal/auth/principal.go` | `ExpiresAt` を追加（変更） |
| `server/internal/httpapi/middleware/auth.go` | `Principal.ExpiresAt` を載せる（変更） |
| `server/internal/store/queries/auth.sql` | ログイン用に7クエリを追加（変更） |
| `server/internal/config/config.go` | `PB_COOKIE_SECURE` を追加（変更） |
| `server/cmd/pb/serve.go` `deploy/base/env.example` `deploy/base/compose.yaml` | 設定1件を反映（変更） |

**`internal/domain/` は引き続き作っていない。**

### 手順5aの検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（テスト180件、8パッケージすべて ok） |
| `make sqlc` の再現性 | 2回実行して `internal/store/gen/` のハッシュが一致 |
| 結合テスト（実DB） | `TestLoginIntegration` / `TestAuthenticateIntegration` とも通る。`authz.sql` の JOIN と `user_identity.subject = app_user.email` が実際に引けている |
| ログイン（大小の非対称） | `LoginTest@Example.com` で作成 → **小文字 `logintest@example.com` でログイン成功**。応答の `actor.email` は保存された表記のまま |
| ログイン応答 | `Set-Cookie` 2種（`pb_session` は HttpOnly、`pb_csrf` は非 HttpOnly、どちらも `Max-Age=1209600` / `SameSite=Lax` / `Path=/`）。本体に `actor` / `permissions`（**28件**）/ `projects`（`[]`）/ `expires_at`（14日後） |
| `GET /me` | ログイン応答と同一構造・同一内容。キー構成の一致をテストで固定 |
| `POST /auth/logout` | `204` ＋ 2つの Cookie の削除指示。同じ Cookie での `GET /me` は `401 unauthenticated` |
| ログイン失敗 | 1〜4回目が `401 invalid_credentials`、**5回目が `423 account_locked`**（`retry_after_sec: 900` ＋ `Retry-After: 900`）。以降は正しいパスワードでも `423` |
| 未登録メール | `401 invalid_credentials`（ダミーハッシュ検証を通してから返す） |
| 入力検証 | 空のメール・パスワードで `422 validation_failed` ＋ `details` 2件。壊れた JSON は `400`、`GET /auth/login` は `405` |
| 監査ログ | `login.success`（`detail.provider_key=local`、`token_id` は発行したトークン）/ `logout` / `login.failure`（`reason` が `unknown_email` / `wrong_password` / `wrong_password_locked` / `locked`）がすべて `request_id` と `ip` 付きで残る |
| 平文トークンの非保存 | `access_token.token_hash` は64文字の16進で `pb_` 始まりが0件。`token_prefix='pb_sess_'`、`client_info='curl/8.7.1'`、`scopes='[]'`、`expires_at` は非 NULL |
| サーバログ | 平文トークンの出現0件。stderr は0バイトのまま |
| `last_login_at` | ログインごとに更新される |

### 手順5bで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/internal/httpapi/middleware/csrf.go` | `RequireCSRF`（`ApiDesign.md` 2.4）。Cookie 認証の状態変更系に `X-PB-CSRF` を要求する |
| `server/internal/httpapi/middleware/csrf_test.go` | 10件 |
| `server/internal/httpapi/middleware/ratelimit.go` | `RateLimit` と `limiter`（2.9）。スライディングウィンドウ、`ClientIPKey` / `ActorKey` |
| `server/internal/httpapi/middleware/ratelimit_test.go` | 11件（`-race` で並行性も検証） |
| `server/internal/httpapi/v1/routes_test.go` | 6件。ルート定義に CSRF とレート制限が並んでいることをルータ越しに検証 |
| `server/internal/httpapi/v1/routes.go` | ログインに IP 制限、認証必須グループにアクター制限と CSRF を挿した（変更） |
| `server/internal/httpapi/v1/fake_test.go` | `addCSRF` ヘルパを追加（変更） |
| `server/internal/httpapi/v1/me_test.go` | `authed` が CSRF を付けるよう変更 |
| `server/internal/httpapi/v1/auth_integration_test.go` | `callWithCookie` が CSRF を付けるよう変更 |
| `VERSION` | `make bump-minor` で `1.4.4` → `1.5.5`（手順5の完了。マージ前に実行する規約） |

**新しい依存は追加していない**（`crypto/subtle`・`sync`・`time` はいずれも標準ライブラリ）。
`internal/domain/` は引き続き作っていない。

### 手順5bの検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（テスト205件、8パッケージすべて ok。5a の180件から25件増） |
| `go test ./internal/httpapi/... -race` | 通る。`limiter` の並行アクセス（200 goroutine で上限50）でも競合を検出しない |
| 結合テスト（実DB） | `TestLoginIntegration` / `TestAuthenticateIntegration` とも通る。ログアウトが CSRF ヘッダ込みで 204 |
| **`POST /auth/logout`（`X-PB-CSRF` 無し）** | **`403` ＋ `{"error":{"code":"csrf_failed",…}}`。** トークンは失効していない |
| `POST /auth/logout`（値が食い違う `X-PB-CSRF`） | `403`。理由はサーバログ側でのみ「一致しない」と区別される |
| `POST /auth/logout`（正しい `X-PB-CSRF`） | `204`。以後、同じ Cookie の `GET /me` は `401` |
| `GET /me`（CSRF ヘッダ無し） | `200`。安全なメソッドは対象外 |
| Bearer 認証の `POST /auth/logout` | `204`。CSRF を要求しない（単体テストで検証） |
| **ログインを1分に11回** | 1〜10回目が `200`（`X-RateLimit-Remaining` が 9→0）、**11回目が `429 rate_limited`** ＋ `retry_after_sec: 40` ＋ `Retry-After: 40` |
| `Retry-After` の値 | 40秒。窓の残り（60秒）ではなく**最古の試行が窓から出るまで**になっている（スライディングの確認） |
| 認証済みリクエストのヘッダ | `X-RateLimit-Limit: 600` / `X-RateLimit-Remaining: 596`。ログインの制限（10）とは別に数えている |
| `/healthcheck` | 15回連続で `200`。`X-RateLimit-*` は付かない（`ApiDesign.md` 2.11 のとおり制限の対象外） |
| 未知パス・405 | `GET /api/v1/nope` が `404`、`GET /api/v1/auth/login` が `405`。5a から不変 |
| 監査ログ | 成功10件（`login.success`）＋ `logout` 1件のみ。**429 と CSRF 失敗の分は増えない**（2.10 に該当アクションが無いため） |
| 拒否のサーバログ | `csrf_failed`（`X-PB-CSRF ヘッダが無い` / `一致しない`）と `rate_limited`（`key=127.0.0.1、上限 10回/1m0s`）が WARN で残る |
| graceful shutdown | SIGINT で「停止信号を受け取った」→「サーバを停止した」。5a から不変 |

**検証は :8099 で行った。** 前のセッションが起動したままの `pb` が :8080 を占有しており、
他人のプロセスを落とさずに済ませるため `PB_BIND` を変えた。`make run` の設定との差は
待受アドレスのみ。

**検証用に作った管理者（`csrf-5b@example.com`）は検証後に削除した。**
`audit_log` を先に消してから `actor` を消している（`actor_id` は `ON DELETE SET NULL` のため）。
残る `app_user` は手順3以来の `tanaka@example.com` 1件。

### 手順6aで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/internal/httpapi/middleware/authz.go` | `RequirePermission`（システムロール層）と `RequireProjectPermission`（プロジェクト層）。`ProjectKeyURLParam`、403/404 の切り分け、`permission.denied` の記録 |
| `server/internal/httpapi/middleware/authz_test.go` | 22件。許可・拒否・スコープ縮小・非メンバー404・不在404・両者の区別不能・監査記録・DB障害500・ルート定義の誤り500・リクエスト内キャッシュ |
| `server/internal/httpapi/authz_integration_test.go` | 実DBに対する認可の結合テスト（7サブテスト）。`role_permission` のシードと `FindProjectAuthzByKey` の SQL を実際に通す |
| `server/internal/auth/permissions.go` | `ProjectAuthz` 型と、実効権限のコンテキスト受け渡し4関数を追加（変更）。`EffectivePermissions` / `HasPermission` の本体は変更なし |
| `server/internal/store/queries/authz.sql` | `FindProjectAuthzByKey` を追加（変更）。`project` を起点にした LEFT JOIN で、不在（0行）と非メンバー（`role_key` NULL）を区別する |
| `server/internal/store/gen/authz.sql.go` `querier.go` | sqlc 生成物（変更） |
| `server/internal/httpapi/v1/routes.go` | コメントのみ更新（変更）。**ルート定義は変えていない** |

**新しい依存は追加していない。** マイグレーションも追加していない（DDL の変更は 6b）。
`internal/domain/` は引き続き作っていない。

### 手順6aの検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（テスト227件、8パッケージすべて ok。5b の205件から22件増） |
| `go test ./internal/httpapi/... -race` | 4パッケージとも通る |
| `make sqlc` の再現性 | 2回実行して `internal/store/gen/` のハッシュが一致 |
| **オペレータ → `user.manage`** | **`403` ＋ `{"error":{"code":"forbidden","message":"この操作を行う権限がありません"}}`**（実DBのシード。`role_permission` の operator 12件に `user.manage` が無い） |
| **アドミニストレータ → `user.manage`** | **`204`**（全権限） |
| **非メンバーのオペレータ → `GET /projects/{key}`** | **`404`。** システムロールとして `project.view` を持っていても通さない |
| 非メンバーのアドミニストレータ → 同 | `204`（`ApiDesign.md` 5.1「管理者は全件」と整合） |
| 存在しないプロジェクト → 同 | `404`。**「在るが見えない」と応答が1バイトも変わらない**（status もエラーコードも一致することをテストで固定） |
| `project_viewer` として参加後 | `GET` は `204`、`PATCH`（`project.edit`）は `403`。到達できるので 404 ではない |
| システムロールとプロジェクトロールの和 | `project_viewer`（`ticket.close` 無し）＋ オペレータ（`ticket.close` 有り）で `204`。6.4.1 の ∪ が効いている |
| トークンスコープによる縮小 | 管理者のトークンでも `scopes=["ticket.view"]` なら `user.manage` は `403`。プロジェクト側も同様 |
| `permission.denied` の記録 | 403・404 のいずれでも1行。`result='failure'`、`detail.required_permission` / `detail.path` / `detail.project_key`、プロジェクトが特定できる場合は `target_type='project'` / `target_id` |
| `system_role` を持たないアクター | `403`。`ListRolePermissions` の呼び出し回数が **0**（テストで確認） |
| DB障害 | `ListRolePermissions` / `FindProjectAuthzByKey` のいずれが落ちても `500`。403・404 に倒れない |
| ルート定義の誤り | `Authenticate` より前に置く／`{key}` の無いルートに置く、どちらも `500` |
| リクエスト内キャッシュ | ミドルウェアを2つ重ねても `ListRolePermissions` と `FindProjectAuthzByKey` は各1回 |
| 既存の挙動（`make run` 相当、:8099） | `/healthcheck` `200`、`GET /api/v1/nope` `404`、`GET /api/v1/auth/login` `405`、未認証 `GET /me` `401 unauthenticated`。手順5b から不変 |
| ログ | stdout のみ（stderr は0バイト）。graceful shutdown も 5b から不変 |
| 検証用データの後始末 | 結合テストの `t.Cleanup` で `audit_log` → `project` → `actor` の順に削除。実行後の `project` / `project_member` / `permission.denied` はいずれも **0件**、`actor` は手順3以来の1件のみ |

### 手順6bで作成したファイル

| ファイル | 内容 |
|---|---|
| `server/migrations/0012_access_token_permission_cache.sql` | `access_token` に `cached_permissions jsonb` / `permissions_cached_at timestamptz` を追加 |
| `server/internal/httpapi/middleware/permissions.go` | `SystemPermissions`（コンテキスト → キャッシュ → DB の3段）、`ComputeSystemPermissions`、`SaveSystemPermissionCache`、`requirePrincipal`。6a で `authz.go` にあった `systemPermissions` をここへ移した |
| `server/internal/httpapi/middleware/permissions_test.go` | 17件。キャッシュ命中でDBを引かない・期限切れで計算し直す・空のキャッシュも有効・スコープで縮小・書き戻し1回・書き戻し失敗でも判定不変・プロジェクト層は跨いでキャッシュしない・ロール無しは何も書かない・書き戻しに計算時のロールが載る・トークンのプロジェクト限定5件 |
| `server/internal/httpapi/v1/permission_cache_test.go` | 6件。ログインがキャッシュを書く／書けなくてもログインは成立する／`GET /me` がキャッシュを使う・期限切れで引き直す・スコープで縮小する |
| `server/internal/httpapi/permission_cache_integration_test.go` | 実DBに対する結合テスト（6サブテスト）。**`Authenticate` から通し、0012 の列と2本の SQL を実際に実行する。** 書き戻しが無効化を追い越さないことも含む |
| `server/internal/store/queries/authz.sql` | `SaveTokenPermissionCache` / `InvalidateActorPermissionCache` を追加（変更） |
| `server/internal/store/queries/auth.sql` | `FindAccessTokenByHash` に2列を追加（変更） |
| `server/internal/store/gen/*` | sqlc 生成物（変更） |
| `server/internal/auth/permissions.go` | `PermissionCacheTTL`（5分）と `EncodeCachedPermissions` / `DecodeCachedPermissions` を追加（変更） |
| `server/internal/auth/principal.go` | `CachedPermissions` / `PermissionsCachedAt` と `FreshPermissions`、`CanReachProject` を追加（変更）。冒頭コメントの「実効権限は持たせない」を 6b の結論に更新 |
| `server/internal/httpapi/middleware/auth.go` | `permissionCache` を追加し、読んだ2列を `Principal` に載せる（変更） |
| `server/internal/httpapi/middleware/authz.go` | `SystemPermissions` を呼ぶ形に変更。プリンシパル不在の 500 を `requirePrincipal` に集約（変更） |
| `server/internal/httpapi/v1/login.go` | ログイン時に実効権限を計算してキャッシュへ書く（変更。6.4.5「ログインごとに」） |
| `server/internal/httpapi/v1/me.go` | `buildSessionView` が解決済みの集合を引数で受け取る形に変更。`/me` は `middleware.SystemPermissions` で解決する（変更） |
| `server/internal/auth/permissions_test.go` `principal_test.go` `httpapi/v1/fake_test.go` `middleware/authz_test.go` | 既存テストへの追加（変更） |
| `VERSION` | `make bump-minor` で `1.5.5` → `1.6.6`（手順6の完了。マージ前に実行する規約） |

**新しい依存は追加していない。** `internal/domain/` は引き続き作っていない。

### 手順6bの検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（8パッケージすべて ok）。`go test ./... -v` の `=== RUN` は **267件**（DB無し・サブテスト込み）、`PB_TEST_DATABASE_URL` を与えると **280件**。6b で足したテスト関数は **30件**（middleware 16・v1 6・auth 7・結合テスト1） |
| `go test ./internal/httpapi/... -race` | 4パッケージとも通る |
| `make sqlc` の再現性 | 2回実行して `internal/store/gen/` のハッシュが一致 |
| `make migrate` | 0012 が適用され `access_token` が15列になる。**再実行は no-op**（`no migrations to run. current version: 12`） |
| **ログイン（実サーバ、:8099）** | オペレータで `permissions` **12件**が返り、同時に `access_token.cached_permissions` に**同じ12件**が入る |
| **ロール変更だけでは反映されない** | `system_role` を administrator に直接変えても `GET /me` は **12件のまま**。`actor.system_role` は administrator と表示される。**これが 6.4.5 が無効化を要求する理由そのもの** |
| **無効化すると次のリクエストで変わる** | 2列を NULL にした直後の `GET /me` が **28件**（全権限）。キャッシュも28件で書き直される |
| 無効化の範囲 | `InvalidateActorPermissionCache` は**アクターの全トークン**を消す。session と api の2本を作り、両方から消えることを結合テストで確認 |
| TTL | `permissions_cached_at` を6分前に戻すと計算し直す。0012 の列を直接操作して結合テストで確認 |
| キャッシュ命中時のクエリ | `ListRolePermissions` の呼び出し **0回**（単体テスト）。**認可のためにクエリが1本も増えない** |
| 権限0件のキャッシュ | `'[]'` はキャッシュとして有効。`NULL`（未計算）と区別され、計算し直さない |
| スコープとの積 | キャッシュに `user.manage` があってもトークンの `scopes` に無ければ `403`。読むときにも積を取っている |
| 書き戻しの失敗 | 403/204 の判定も `/me` の応答も変わらない。WARN のみ残る |
| `permission.denied` の記録 | 403 を返した回数と一致（結合テストで3件を確認）。6a から不変 |
| 既存の挙動（実サーバ、:8099） | `/healthcheck` `200`、`GET /api/v1/nope` `404`、`GET /api/v1/auth/login` `405`、未認証 `GET /me` `401`、`POST /me` `405`、未登録メールのログイン `401`。手順6a から不変 |
| ログ | stdout のみ（stderr は0バイト） |
| 検証用データの後始末 | 検証用ユーザー（`pbstep6b@example.com`）と、疎通で出た `login.failure` 1件を削除。実行後は `actor` 1件（手順3の `tanaka@example.com`）・`access_token` 0件・`audit_log` 2件（手順3の `user.create`）で**セッション開始前と同じ**。`local_credential.failed_attempts` も 0 |

### 手順6bのレビューで見つけて直したもの（2026-08-13）

コミット後に `/code-review` を掛けて4件見つかった。いずれもコードの側を直している。

| # | 内容 | 到達可能性 |
|---|---|---|
| 1 | `access_token.project_id` を読むコードが1つも無かった（`Design.md` 6.5 の「他プロジェクトへのアクセス」禁止が未実施） | Phase 1 では NULL のみのため実害なし。**Phase 2 で顕在化する前に塞いだ** |
| 2 | キャッシュの書き戻しが無効化を追い越し、旧権限を TTL ぶん復活させられた | 無効化を呼ぶのは手順10。**そのとき顕在化する前に塞いだ** |
| 3 | システムロールを持たないアクターにも `'[]'` を書き込んでいた（テストのコメントは「書かない」と嘘をついていた） | 実害は無駄な UPDATE のみ。**検証されていない宣言が残るほうが問題** |
| 4 | `SaveSystemPermissionCache` のコメントが実際の実行タイミングと違っていた | 動作に影響なし。読み手を誤らせる |

**1 と 3 は「テストが通っているのに実装が無い／宣言と違う」型である。** 単体テストのフェイクは
呼ばれなかったメソッドを検出しないため、`Principal.ProjectID` のように**誰も読まないフィールド**は
テストが緑のまま残る。同じ型の見落としを避けるには、`Principal` に足したフィールドごとに
「読み手はどこか」を確かめるのが早い。

### 手順7で作成したファイル

| ファイル | 内容 |
|---|---|
| `client/package.json` / `package-lock.json` | 依存は7つのみ（vue / vue-router / pinia ＋ vite / @vitejs/plugin-vue / typescript / vue-tsc）。`npm run build` は `vue-tsc --noEmit` を通してから `vite build` |
| `client/vite.config.ts` | `:5173`（`strictPort`）、`/api` と `/mcp` を `127.0.0.1:8080` へプロキシ（`Design.md` 3.4） |
| `client/tsconfig.json` / `env.d.ts` / `index.html` | strict。`@types/node` を足さずに済むよう、パスエイリアスを使わず相対 import にしている |
| `client/src/main.ts` / `App.vue` | Pinia と router を登録し、描画前にテーマを `<html>` へ当てる。`App.vue` は `<RouterView/>` のみ（`AppShell` は手順8） |
| `client/src/styles/tokens.css` | **`GuiDesign.md` 8.5 の css ブロックの機械的な転記**（`--pb-1`〜`--pb-12`、意味色、セマンティック別名、`[data-hue="green"]`、`[data-theme="dark"]`） |
| `client/src/styles/base.css` | 最小のリセットと 8.10 のタイポグラフィ（システムフォント・14px/1.7・等幅）。`color-scheme` も |
| `client/src/router/index.ts` | `createWebHistory`。ガード（7.2）は手順8で足す |
| `client/src/router/routes.ts` | **`GuiDesign.md` 3.2 の全21ルート**と `RouteMeta` の型（`permission` / `public` / `placeholder`） |
| `client/src/pages/PlaceholderPage.vue` | `GuiDesign.md` 6.5 のプレースホルダ。内容は `meta.placeholder` から受ける（画面ごとにファイルを作らない） |
| `client/src/pages/ForbiddenPage.vue` / `NotFoundPage.vue` | `/403` `/404` |
| `client/src/stores/ui.ts` | テーマ（system/light/dark）と色相（blue/green）。`localStorage` と `<html data-theme/data-hue>` |
| `server/internal/webui/embed.go` | `//go:embed all:dist` |
| `server/internal/webui/handler.go` | 実在ファイルの配信、SPA フォールバック、キャッシュ制御 |
| `server/internal/webui/handler_test.go` | 8件。アセット配信・キャッシュ制御・フォールバック・dist 外への脱出・index 欠落時の 500・埋め込み済み dist の疎通 |
| `server/internal/webui/dist/index.html`（変更） | embed のプレースホルダを「client 未ビルド」の案内ページに差し替え（従来は `// PlaceHolder` の1行で、HTML として成立していなかった） |
| `server/internal/httpapi/router.go`（変更） | `NotFound` を分岐。`/api` `/mcp` は 2.5 形式の 404、それ以外の GET/HEAD は SPA、他メソッドは 405 |
| `server/internal/httpapi/router_test.go`（変更） | 未知パスのテストを API 用と画面用に分割し、POST の 405 を追加 |
| `Makefile`（変更） | `dev-client` / `build-client` / `sync-webui` / `build` / `clean-webui` を追加（`Design.md` 4.2 のとおり） |

### 手順7の検証結果

| 検証 | 結果 |
|---|---|
| `npm run build`（`vue-tsc --noEmit` 込み） | 通る。`dist/index.html` 0.39 kB、`assets/*.css` 6.16 kB、`assets/*.js` 101.93 kB（gzip 39.80 kB） |
| `gofmt -l` / `go build ./...` / `go vet ./...` / `make test` | いずれも通る（9パッケージすべて ok。`internal/webui` が増えた） |
| `tokens.css` と `GuiDesign.md` 8.5 | `diff` で**完全一致**（98行） |
| `make build` | `bin/pb` が生成され、`client/dist` が `server/internal/webui/dist/` に入る |
| **`/`（ヘッドレス Chrome）** | **`/projects` へリダイレクトし、プロジェクト一覧のプレースホルダが描画される。** `<html>` に `data-theme="light"` / `data-hue="blue"`（ui ストアが効いている） |
| `/p/my-app/tickets/31`（同） | チケット詳細のプレースホルダ。`予定している内容` は 5.5 の転記、`ルート` は定義側の `/p/:key/tickets/:seq` |
| `/nowhere`（同） | catch-all で 404 画面。`/403` も表示できる |
| SPA フォールバック（curl） | `/projects` `/p/my-app/tickets/31` `/admin/users` `/404` がいずれも `200 text/html` |
| キャッシュ制御 | `assets/*.js` `assets/*.css` は `public, max-age=31536000, immutable`、`index.html` は `no-cache` |
| API を巻き込んでいないこと | `GET /api/v1/nope` `GET /api` `GET /api/` `GET /mcp/nope` はすべて 2.5 形式の `404 not_found` |
| `POST /projects` | `405 method_not_allowed`（画面のパスに index.html を返さない） |
| `/healthcheck` | `{"status":"OK","version":"1.6.6"}`。SPA フォールバックの例外として残っている（`ApiDesign.md` 2.11） |
| **`make dev-client`（:5173）** | `/` `/projects` が Vite から返り、`/api/v1/nope` は `:8080` の 404、`POST /api/v1/auth/login` は 422 `validation_failed`。**プロキシが通っている** |
| `make clean-webui` | ビルド成果物が消え、`dist/` がコミット済みの `index.html` 1つに戻る |
| 検証用リソースの後始末 | `make run` と Vite 開発サーバは停止済み。`bin/` と `client/dist` は `.gitignore` 済み。DBには一切触っていない（`audit_log` も増えていない） |

**ブラウザでの見え方（配色・余白）は目視で確認していない。** ヘッドレスで確認したのは
DOM とテーマ属性までである。手順8でログイン画面を作る際に、実ブラウザで合わせて見ること。
→ **手順8で解消した**（スクリーンショットを撮って目視。下記「手順8の検証結果」）。

### 手順8で作成したファイル

| ファイル | 内容 |
|---|---|
| `docs/openapi.yaml` | **実装済み4本**（login / logout / me / healthcheck）と 2.5 のエラー形式・Cookie/CSRF の securityScheme。`servers` は `/` |
| `server/internal/httpapi/openapi_drift_test.go` | `chi.Walk` と yaml の `paths` の突き合わせ。欠落・余剰の両方向を報告する（`Design.md` 3.3） |
| `client/src/api/schema.d.ts` | `openapi-typescript` の生成物（`make gen-api`）。**コミットする** |
| `client/src/api/client.ts` | fetch の薄いラッパ。CSRF ヘッダ（2.4）・Cookie の送出・2.5 のエラー→`ApiError`・401 の共通処理の登録口 |
| `client/src/api/auth.ts` | `login` / `logout` / `me`。型は生成物をそのまま使う |
| `client/src/stores/auth.ts` | アクター・実効権限・所属プロジェクト。`restore()`（起動時の `GET /me`、多重呼び出しを1本化）・`can` / `canInProject` / `canReachProject` |
| `client/src/router/guards.ts` | `beforeEach`（7.2 の4段）と `safeRedirect` |
| `client/src/pages/LoginPage.vue` | `GuiDesign.md` 5.1。エラーはサーバの `message`、422 の `details` は入力欄に紐づける |
| `client/src/version.ts` | `VERSION` をビルド時に埋める（`?raw`） |
| `client/src/components/AppShell.vue` | メニュー＋コンテンツペインの2枚（2.2）。`[` のショートカット（9.1）、768px 未満のオーバーレイ（2.4） |
| `client/src/components/SideMenu.vue` | 4.1 の構造と 4.3 の出し分け |
| `client/src/components/SideMenuToggle.vue` | 2.3 の案A（メニュー最上行に内包） |
| `client/src/components/UserMenu.vue` | 4.2。自分の設定／トークン／テーマ／バージョン／ログアウト |
| `client/src/components/ProjectSwitcher.vue` | 4.4。10件超で絞り込み入力。切替時は同じ画面種別を維持 |
| `client/src/stores/ui.ts`（変更） | 折りたたみ状態（2.3.3）とブレークポイント（2.4）を追加 |
| `client/src/App.vue`（変更） | 認証済みは AppShell で包む。`/login` は素で描く（5.1 の例外） |
| `client/src/router/index.ts` / `routes.ts`（変更） | `beforeEach` の登録、`/login` を実コンポーネントへ |
| `client/src/main.ts`（変更） | 401 ハンドラの登録（client.ts ↔ ストア／ルータの循環を避けるため） |
| `client/package.json` / `vite.config.ts`（変更） | `openapi-typescript`（devDependency）と `gen:api`、`server.fs.allow` |
| `Makefile` / `CLAUDE.md`（変更） | `make gen-api` の追加 |
| `docs/GuiDesign.md`（変更） | 7.2 のガード順の入れ替えと理由（上記の差異表） |

### 手順8の検証結果

| 検証 | 結果 |
|---|---|
| `npm run build`（`vue-tsc --noEmit` 込み） | 通る。`assets/*.css` 13.4 kB、`assets/*.js` 121 kB（gzip 45.9 kB） |
| `gofmt -l` / `go vet ./...` / `make test` | いずれも通る（**openapi ドリフト検出を含む**） |
| ドリフト検出が効くこと | yaml から `/healthcheck` を落とし、実装に無い `/api/v1/projects` を足した状態で**両方向とも FAIL する**ことを確認（確認後に復元） |
| `POST /auth/login`（curl） | Set-Cookie が2種（`pb_session` は HttpOnly、`pb_csrf` は非 HttpOnly、`Max-Age` は同じ 1209600） |
| **ブラウザ（ヘッドレス Chrome、1440×900）8a：16件** | 未認証で `/admin/users` → `/login?redirect=/admin/users` ／ 401 の文言はサーバの `message` ／ 422 の `details` が出る ／ ログイン後に `redirect` 先へ復帰 ／ 認証済みで `/login` → `/projects` ／ リロードで復元 ／ オペレータが `/admin/users` → `/403` ／ 非メンバーのプロジェクト → **`/404`** ／ メンバーのプロジェクトへは到達 ／ `POST /auth/logout` が 204（CSRF ヘッダあり）／ ログアウト後は `/login` |
| **ブラウザ 8b：39件** | 管理者に「管理」セクション ／ **オペレータでは見出しごと出ない** ／ プロジェクト未選択ならプロジェクト領域を出さない ／ 選択中は ダッシュボード・チケット・（`project.edit` があれば）プロジェクト設定 ／ 閲覧者に「プロジェクト設定」が出ない ／ ユーザーメニューの5項目とバージョン ／ テーマ切替が `<html data-theme>` に効く ／ 折りたたみ 240px↔56px・`localStorage`・`[` キー ／ 幅ごとの既定（1024px→56px、1440px→240px）と**選択の優先** ／ 768px 未満のオーバーレイと浮遊 ☰・スクリムで閉じる ／ ログアウトでログイン画面 |
| **実ブラウザでの見え方（目視）** | ログイン画面・シェル（ライト／ダーク）・ユーザーメニュー・プロジェクト切替・折りたたみの6枚を撮って確認。**2件を直した**（①ユーザーメニューのテーマ行が 240px 幅で折り返していた → ラベルと選択肢を2行に ②折りたたみ時にレールへ横スクロールバーが出ていた → `overflow-x: hidden`） |
| `make dev-client`（:5173） | `/login` が返り、`/api/v1/auth/login` のプロキシが 200。`VERSION?raw` も `/@fs/...` 経由で解決する |
| `make build` の単一バイナリ | 上記のブラウザ検証はすべて **`bin/pb serve`（embed 済み）** に対して実施した |
| 検証用リソースの後始末 | `make run` と Vite 開発サーバは停止済み。ヘッドレス Chrome のプロファイルと検証スクリプトはスクラッチパッド（リポジトリ外）。DB は `make dev-reset` を実行しておらず、手順7.5 のデモデータのままで、追加・削除していない |

**ドライバは CDP（Chrome DevTools Protocol）を直接叩く自作スクリプト**（Python 標準ライブラリのみ）。
Playwright / Puppeteer は入れていない。スクリプトはリポジトリに入れていないため、
**次に画面を検証するときは書き直しになる。** 常設するなら `Design.md` 3.1 への追記提案とセットにする。


## 手順11a（2026-08-18、`feature/step-11-project-detail-api`）

`GET`/`PATCH /projects/:key` と `archive`/`unarchive`（`ApiDesign.md` 5.4〜5.6）。
**手順11 のうち Go だけ**を扱い、プロジェクト設定画面は 11b とした。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/httpapi/v1/projects_get.go` | `GET /projects/{key}`（5.4）。組み立ては既存の `buildProjectDetail` を呼ぶだけ。`projectRequestContext`（プリンシパルと `{key}` の取り出し）と `writeProjectDetailError`（`pgx.ErrNoRows` → 404）は**`/projects/{key}` 配下で共有する** |
| `server/internal/httpapi/v1/projects_update.go` | `PATCH`（5.5）と `archive`/`unarchive`（5.6）。`parseIfMatch`・`buildUpdateProjectParams`・`parseDescriptionField`・`classifyUpdateMiss`・`writeProjectUpdateError`。archive と unarchive は `setProjectStatus` 1つに集約し、渡す `status` だけが違う |
| `server/internal/httpapi/v1/projects_update_test.go` | 単体16件。5.4 の形・非メンバーの 404・部分更新・`null` の区別・`If-Match` の必須と 409・`immutable_field`・冪等な archive・403・`check-key` の予約 |
| `server/internal/httpapi/v1/projects_update_integration_test.go` | **実DBに対する結合テスト。** `COALESCE`/`sqlc.narg` の据え置き、`version` の照合と +1、`archived_at` の CASE、`status <> @status` の冪等、`trg_project_updated` による `updated_at` の前進 |
| `server/internal/store/queries/project.sql`（変更） | `UpdateProject`（`:execrows`）と `SetProjectStatus`（`:execrows`）を追加。影響行数で 409 / 404 / 冪等を呼び出し側へ伝える |
| `server/internal/httpapi/v1/routes.go`（変更） | 4ルートを `RequireProjectPermission` 付きで宣言（`project.view` / `project.edit` / `project.archive` × 2） |
| `server/internal/httpapi/v1/projects.go`（変更） | `reservedProjectKeys` に `check-key` を追加。`projectsETag` のコメントを、2.7 を直した後の状態に書き換え |
| `server/internal/httpapi/v1/fake_test.go`（変更） | `UpdateProject` / `SetProjectStatus` / `FindProjectAuthzByKey` と `withProjectMember` ヘルパ。**`FindProjectAuthzByKey` は v1 のフェイクに初めて入った**（プロジェクト層の認可を通るルートが手順11で初めて出たため） |
| `docs/openapi.yaml`（変更） | 4オペレーション、`components.parameters.ProjectKey`、`responses.ProjectStatusChanged`、`schemas.UpdateProjectRequest` |
| `client/src/api/schema.d.ts`（変更） | `make gen-api` の生成物。**API層（`api/projects.ts`）への追加は 11b** |
| `docs/ApiDesign.md`（変更） | 2.7 / 2.8 / 5.2 / 5.5 / 5.5.1（新設）/ 5.6 |

### 手順11a の検証結果

| 検証 | 結果 |
|---|---|
| `gofmt -l` / `go vet ./...` / `make test` | いずれも通る（**openapi ドリフト検出を含む**） |
| ドリフト検出が効くこと | 実装を先に足した時点で **4件すべてを「実装にあって yaml に無い」と報告**した（意図した検出。yaml を書いて解消） |
| 単体テスト（`-run 'Project'`） | 16件が通る |
| **実DB結合テスト**（`PB_TEST_DATABASE_URL`） | `TestProjectUpdateIntegration` が通る。`name` だけ送ると `description` が据え置かれる／古い `version` で 409 かつ値が書き換わらない／`"description":null` で列が NULL になる／`settings` の置き換えで `name` は据え置き／`archived_at` が入り、unarchive で NULL に戻る／2回目の archive で `version` が進まず監査も増えない／`updated_at` がトリガで進む |
| **実サーバ（`make run` ＋ `make dev-reset` のデモ4アカウント）** | `GET /projects/demo`（pm＝`project_admin`）が `workflow` 3ステータス・`members` 3件・`my_permissions` 22件を返す ／ `member`（`project_member`）は GET 200 だが **PATCH 403**（メンバーなので 404 ではない）／ 古い `If-Match` → 409 `conflict` ／ `If-Match` 省略 → 422 `If-Match/required` ／ `key` 送信 → 422 `key/immutable_field` ／ CSRF ヘッダ無し → 403 `csrf_failed` ／ archive → `archived` かつ `version` +1、2回目は据え置き、unarchive で `active` ／ `viewer`（`project_viewer`）の archive → 403 ／ 存在しないキー → 404 |
| `audit_log` | `project.archive` が2件（archive と unarchive）で `detail.status` が `archived` / `active`。**冪等な2回目は記録されていない** |
| 検証用リソースの後始末 | `make run` を停止（`:8080` の解放を確認）／ `PB_YES=1 make dev-reset` でデモデータを初期状態に戻した（`version=1`・`description` 復元・`audit_log` の `project.archive` 0件）／ `make down` でコンテナとネットワークを削除 ／ スクラッチパッドを空にした。**`make build` は実行していない**ため `make clean-webui` は不要 |

**実サーバの確認は使い捨てシェルスクリプトで行い、リポジトリには入れていない。**
本文とステータスを別々に受け取り（`-o` と `-w`）、`version` は毎回 `GET` で読み直すため
何度実行しても同じ判定になる作りにした。**次に同じ確認をするときは書き直しになる。**

## 手順11b（2026-08-18、`feature/step-11-project-settings-page`）

プロジェクト設定画面（`GuiDesign.md` 5.9）。**手順11 のうち TypeScript だけ**を扱った。
設計文書に節が無かったため、**5.9 の執筆と承認から始めた**（11a からの引き継ぎ）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/pages/ProjectSettingsPage.vue` | 本体。一般／メンバーの2タブ、基本情報（キーは読み取り専用・名前・説明・リポジトリ）、ワークフロー（参照のみ）、プロジェクトの状態。4状態（6.2）・変更検出・409 の復帰・確認ダイアログを持つ |
| `client/src/components/ConfirmDialog.vue` | 破壊的操作の確認（6.3）。`Modal.vue` に乗せ、足しているのは実行ボタンの意味づけ（`danger`）と処理中の二重押下防止だけ |
| `client/src/lib/datetime.ts` | `formatDateTime` / `formatDate`。`ProjectsPage.vue` から出した（10a の引き継ぎ「2つ目の消費者が出たら共通化」の消化） |
| `client/src/api/client.ts`（変更） | `patch` と、リクエストごとのヘッダ（`If-Match`）。**`Content-Type` と `X-PB-CSRF` は共通処理の値を優先**し、呼び出し側から上書きさせない |
| `client/src/api/projects.ts`（変更） | `getProject` / `updateProject` / `archiveProject` / `unarchiveProject` と、`readRepositories` / `mergeRepositories`（`settings` の読み書き） |
| `client/src/stores/project.ts`（変更） | 選択中プロジェクト（`GuiDesign.md` 7.1）。`current` / `fetchCurrent` / `setCurrent` / `clearCurrent`。**一覧とは独立**で、更新系の応答は `setCurrent` で入れる（通し番号を進めて、遅れて届いた取得結果が古い `version` を戻さないようにする） |
| `client/src/pages/ProjectsPage.vue`（変更） | ローカルの `formatDateTime` を消して `lib/datetime` を使う。**変更はこの1点のみ** |
| `client/src/router/routes.ts`（変更） | `/p/:key/settings` をプレースホルダから実画面へ差し替え、`meta.placeholder` を削除 |
| `client/src/api/schema.d.ts`（変更） | `make gen-api` の生成物（`openapi.yaml` の description 変更に追随。型は変わっていない） |
| `docs/GuiDesign.md` / `DbDesign.md` / `ApiDesign.md` / `openapi.yaml`（変更） | 5.9 の新設、6.4 の差し替え、`settings.repositories` の定義（`history/decisions.md` の「設計文書へ反映済みの修正（手順11b）」） |

### 手順11b の検証結果

| 検証 | 結果 |
|---|---|
| `npm run typecheck`（vue-tsc） | 通る |
| `make test` | 通る（**`openapi.yaml` のドリフト検出を含む**。API は 11a から変えていないので検出も無し） |
| `make build` | 単一バイナリ（1.13.18）が作られ、`/healthcheck` が応答する |
| **ブラウザ（ヘッドレス Chrome ＋ CDP、`Development.md` 8.2）** | **26件すべて PASS**（下記） |

**ブラウザ検証26件の内訳**（`pm@example.com`＝`project_admin` を主に使用）

1. `/p/demo/settings` が開く ／ 2. タブが2つ ／ 3. キーが読み取り専用で警告が出る ／
4. ワークフローが `未着手 ─ 進行中 ─ 完了` で出る ／ 5. 未変更では `[保存]` が無効 ／
6. 変更すると有効 ／ 7. 保存の結果が**その場に**出る（`✓ 保存しました`。トーストではない）／
8. サーバに反映され `version` が +1 ／ 9. 改名がダッシュボードの見出しに反映される
（`auth.refresh()`）／ 10. `https` だけがリンクになり `git@…` はならない ／
11. `settings.repositories` に2件保存される ／ 12. 空の `name` / `description` は保存されない ／
13. 再読込で残る ／ 14. URL未入力の行があると保存できず `✕ URLを入力してください` が出る ／
15. **409** でサーバの message（`他の利用者がこのプロジェクトを更新しました…`）がそのまま出る ／
16. `[最新の内容を取得]` で最新が入力欄に入る ／ 17. アーカイブで確認ダイアログが出る ／
18. アーカイブ後は `● アーカイブ済み` になりボタンが `アーカイブを解除` に変わる ／
19. サーバも `archived` ／ 20. 解除は確認なしで戻る ／ 21. メンバーが3件 ／
22. ロールが日本語（プロジェクト管理者・メンバー・閲覧者）／ 23. 追加・変更の導線を案内する ／
24. `member@example.com` のメニューに「プロジェクト設定」が出ない ／ 25. URL直打ちで `/403` ／
26. 到達できないプロジェクトは `/404`（403 ではない）

**検証スクリプトは使い捨てで、リポジトリには入れていない。** Python の標準ライブラリだけで
CDP を話す最小クライアント（`cdp.py`）と検証本体（`verify.py`）に分け、**開始時に現在値を
控えて `finally` で必ず戻す**作りにした。409 を作るために、画面とは別経路の API セッション
（`urllib` ＋ cookiejar）から先に `PATCH` している。**まずスモーク（ログイン→設定画面を開いて
本文を出す）だけを通してから全体を回した**（`pb-step.md` 手順6）。

### 手順11b の実機確認で見つかった不具合（2026-08-18、マージ前）

**利用者が実際に画面を触って3点を指摘した。** いずれも同じブランチで直し、26件を再実行した。

| 指摘 | 原因と対応 |
|---|---|
| リポジトリの **URL 入力欄が画面の右外に出て、見ることも編集することもできない** | **CSS の詳細度。** `.repo-name { width: 160px }`（0,1,0）より `input[type='text'] { width: 100% }`（0,1,1）が強く、表示名の欄が1行（846px）を占有して URL を押し出していた。**入力欄を縦積みにして幅の奪い合いをなくした。** 1440 / 1280 / 1024 / 820px で、3つの欄がブロック内に収まり横スクロールが出ないことを実測 |
| リポジトリの並びが「表示名 → URL → 説明」で、**必須の URL が任意項目の後ろ** | `URL`（必須）→ `表示名` → `説明` に変更し、各欄にラベルを付けた |
| 基本情報の**先頭がプロジェクトキー**だった | プロジェクト名を先頭へ。キーは一意に指すための識別子だが、プロジェクトの一属性にすぎない |
| 説明欄の文言「このリポジトリとプロジェクトの関係（任意）」が用途を狭めている | ラベルを `説明` に。意図は設計文書側（5.9.1）に残した |

**再検証**：`npm run typecheck` ／ ブラウザ26件が再び全 PASS ／ 幅4種でレイアウトを実測。
**このとき利用者が `demo` に入れていた検証用の説明文（複数行）は、開始時に控えて `finally` で
そのまま書き戻している**（検証スクリプトが現在値を読み直す作りになっているため）。

### 手順11b の追補（2026-08-18、実機確認の第2ラウンド）

利用者の指摘5件のうち、**4件を同じブランチで実装**した（1件は Phase 2 へ）。
ブランチを分けなかったのは利用者の判断（`history/decisions.md`）。

| 追補 | 作った・直したもの |
|---|---|
| メンバーのメール表示（**API変更**） | `project.sql` に `LEFT JOIN app_user`、`project_view.go` に `Email *string`、`ApiDesign.md` 5.4 と `openapi.yaml` の `ProjectMember`、`projects_update_test.go` に**エージェントの email が null になる**表明、メンバータブに列追加 |
| リポジトリの表形式 | `client/src/components/RepositoryModal.vue`（新規。`Modal.vue` を再利用）、`ProjectSettingsPage.vue` を表＋モーダルへ |
| プロジェクト一覧の省略記号 | `ProjectsPage.vue` を `table-layout: fixed` にし、数値・日時列へ固定幅。`GuiDesign.md` 5.2 に規約を明記 |
| `make restart` | `Makefile` に `stop-server` と `restart`、`Development.md` 3.1 / 3.3、`CLAUDE.md` の開発コマンド |

**検証（43件、すべて PASS）**

| | 件数 | 内容 |
|---|---|---|
| 既存の回帰 | 26 | 手順11b の検証をリポジトリの新UIに合わせて書き換えて再実行 |
| 追補 | 17 | 一覧が横に伸びない（表 1152px ≦ ペイン 1200px・横スクロール無し・説明が省略）／追加モーダル（URL 未入力では確定できない）／表に2行増える／`https` で始まる行だけリンク／**説明を編集したままリポジトリ操作をしても保存が有効**（前回の不具合の回帰確認）／保存されて `repositories` に入る／行クリックで編集モーダルが開き値が入っている／更新が表に反映／メンバー3件・メール3件・列が5つ（種別/名前/メール/ロール/参加日）／`GET /projects/:key` が `email` を返す |

`make restart` は**実際に実行して確認**した（`stop-server` → `down` → `build` → `up` → `run` が
順に走り、`/healthcheck` が新しいバイナリで応答する）。`make stop-server` は
**2回続けて実行**し、1回目は PID を出して停止、2回目は「掴んでいるプロセスは無い」を出すことを確認した。

**利用者が `demo` に入れていた検証データ（複数行の説明・`サンプリリポジトリ`）は、
開始時に控えて `finally` で書き戻している。**

### 手順11b の検証で残ったもの・戻したもの

| | |
|---|---|
| 戻した | `demo` の `name` / `description` / `settings` を検証前の値へ（`finally` で実行し、復元後の値を出力して確認）。`status` も `active` に戻した |
| 止めた | `make run` のサーバ（`/healthcheck` が応答しないことを確認）、ヘッドレス Chrome、Chrome のプロファイル（削除） |
| 消した | `client/dist` と `server/internal/webui/dist`（`make clean-webui`）、スクラッチパッド |
| **残っているもの** | `demo` の `project.version` が検証のぶん進んでいる（1 → 15 前後）。`updated_at` も検証時刻になっている。`audit_log` に検証中の `project.archive` が数件残る。**いずれも値としては正常で、次の手順の妨げにならないため `make dev-reset` はしていない**（実行すれば消えるが、開発DBの他のデータも作り直しになるため独断では行わなかった） |

---

---

## 進捗表から移した検証内容（手順1〜16a）

`PROGRESS.md` の Phase 1 表は、完了した手順の「検証方法」欄を1行に要約してある
（2026-08-18、`docs/progress-archive`）。**要約前の全文をここに保管する。**
過去にどこまで確認したかを正確に知りたいときはこちらを見ること。
**手順11a〜16a のぶんは 2026-08-23（16b 完了時）に移した**——`PROGRESS.md` が 40KB の
閾値を超えたため（`pb-step.md` 手順7 の掃除①「完了して今後の手順に不要な情報を移す」）。

| 手順 | 完了日 | 検証内容（当時の記述のまま） |
|---|---|---|
| 1 | 2026-08-11 | `make up` でDBが起動し、`pb_app` ロールが存在する |
| 2 | 2026-08-11 | `make migrate` 後、テーブル23個と権限28件・ロール5件が存在する |
| 3 | 2026-08-12 | 作成した管理者が `app_user` に `system_role='administrator'` で入る |
| 4a | 2026-08-12 | `go test ./internal/httpapi/...` が通り、`make run` 後に `/healthcheck` が `{"status":"OK"}`、未知パスが 2.5 形式の 404 を返す |
| 4b | 2026-08-12 | `go test ./internal/httpapi/...` が通る。`PB_TEST_DATABASE_URL` を与えると実DBに対する認証の結合テストも通る |
| 5a | 2026-08-12 | `curl -i -X POST .../auth/login` で Set-Cookie が2種返り、`GET /me` が権限28件を返す。`PB_TEST_DATABASE_URL` を与えると実DBに対する結合テストも通る |
| 5b | 2026-08-12 | Cookie 認証の POST に `X-PB-CSRF` が無いと 403、ログインを1分に11回叩くと 429 |
| 6a | 2026-08-13 | 実DBのシードで operator→403 / administrator→200、非メンバー→404、`permission.denied` が記録される。**実サーバでの実地確認は手順9b で完了**（`POST /projects` にオペレータで 403） |
| 6b | 2026-08-13 | 実サーバでオペレータ（12件）→ 管理者へ昇格しても 12件のまま → キャッシュ破棄後に 28件。TTL 超過でも計算し直す。無効化はアクターの全トークンに効く |
| 7 | 2026-08-13 | `make build` した単一バイナリの `/` で Vue が起動し、プロジェクト一覧のプレースホルダが描画される（ヘッドレス Chrome で確認）。SPA のパスは index.html にフォールバックし、`/api` `/mcp` は 2.5 形式の 404 のまま。`make dev-client` の :5173 から :8080 へのプロキシも通る |
| 7.5 | 2026-08-13 | `make dev-reset` で作り直し後、4アカウントとデモプロジェクト（ワークフロー複製つき）が投入される。2回目は作成0／スキップ全件で壊れない。`PB_ALLOW_DEV_SEED` なし・開発端末以外のホストのいずれでも中断する。実サーバで4アカウントともログインでき、`GET /me` が admin=28権限・他=12権限、`projects[].role` が定義どおり（`DbDesign.md` 7.6） |
| 8a | 2026-08-15 | ブラウザ（ヘッドレス Chrome）で16件：ログイン→`/projects`、未認証で保護ページ→`/login?redirect=`→ログイン後に復帰、オペレータで `/admin/users`→`/403`、非メンバーのプロジェクト→`/404`、ログアウト後は `/login`。`go test ./...` にドリフト検出（`chi.Walk` と yaml の突き合わせ）が入る |
| 8b | 2026-08-15 | ブラウザで39件：管理者に「管理」セクションが出て**オペレータでは見出しごと出ない**、プロジェクト選択中のみプロジェクト領域、`project.edit` の無い閲覧者に「プロジェクト設定」が出ない、テーマ切替、折りたたみ（`[` キー・`localStorage`・2.4 のブレークポイント）、ログアウトでログイン画面へ戻る |
| 9a | 2026-08-15 | 実サーバで管理者が一覧を取得でき、`ETag` が付く。オペレータには所属プロジェクトのみ返る。`?status=deleted&per_page=201` が `details` 2件の 422。`check-key` が未使用→`available:true`、`admin`→`reserved`、`My_App`→`invalid_format`、既存→`already_exists` |
| 9b | 2026-08-15 | 実サーバで作成すると 201・`Location`・`with_review` の4ステータスが複製され、作成者が `project_admin` で入り `audit_log` に `project.create` が残る。同じキーで 409 `already_exists`。オペレータは 403（**手順6a の積み残しだった実地確認**）。チケット4件（うち3件クローズ）で一覧が `4 / 3 / 0.75` を返す |
| 10a | 2026-08-15 | ブラウザで53件：管理者に30件が25行＋ページャで出て、`48 / 36 / 75%` が `GuiDesign.md` 5.2 の図と一致する。列ヘッダでソートが変わり「完了」だけソート不可。`⋯` の「アーカイブを表示」で31件になり `アーカイブ済み` が文字で出る。行のセルクリックで `/p/:key`、`j`/`k` で行を移動。オペレータには所属プロジェクトのみ・`+ 新規プロジェクト` が出ない。読み込み中はスケルトン、サーバ停止時は原因＋再試行、空状態は権限で2種類に分かれる |
| 10b | 2026-08-18 | ブラウザで32件：`+ 新規プロジェクト` で `/projects?new=1` になりモーダルが開く。名前 `Riders High 2026` から `riders-high-2026` が自動入力され、日本語名では空のまま。キーを手で編集したら名前に追随しない。`My_App`→`invalid_format` / `admin`→`reserved` / `demo`→`already_exists` / `my-app`→`✓ 使用可能です`。14文字を連続入力しても `check-key` は1回（debounce 400ms）。作成すると `/p/my-app` へ遷移して見出しがプロジェクト名になり、一覧へ戻ると先頭に `0 / 0 / 0%` で出る。`check-key` を待たずに既存キーで送ると 409 のメッセージがキー欄に出てモーダルは開いたまま。3テンプレートで `workflow_status` が 3 / 4 / 5 件複製され、作成者が `project_admin` で入り `audit_log` に `project.create` が残る。`Esc` で閉じてフォーカスが `+ 新規プロジェクト` に戻る。オペレータにはボタンが出ず `?new=1` を直接開いてもモーダルが出ない |

---

## 環境メモから移した記録（2026-08-18、その2・`feature/step-11-project-detail-api`）

`PROGRESS.md` が 30KB を超えたため、環境メモを**「いま効いていて、かつ手順書に落とせない
制約」だけ**に絞った（2026-08-18、11a）。再現手順は `docs/Development.md` へ写し
（テストの落とし穴＝6.2、依存とツールの固定版＝10章、initdb の実行ビット＝2.2）、
**役目を終えた回避策だけをここへ移した。**

- **検証用ユーザーを Argon2id ハッシュから手作りする方法**（手順6b）。当時は実サーバの
  ログイン検証にパスワードの分かるアカウントが無く、スクラッチパッドの小さなモジュールで
  ハッシュを生成し、`actor` → `app_user` → `user_identity` → `local_credential` を直接
  INSERT して作った（検証後に削除）。パラメータは `server/internal/auth/password.go` の
  `hashParams`（m=65536, t=3, p=4, salt=16, key=32）に合わせる必要があった。
  **手順7.5 の `make dev-seed` で共通パスワードの4アカウントが入るようになったため、
  この手順はもう使わない**（`make dev-info` で一覧できる）

## 環境メモから移した記録（2026-08-18）

`PROGRESS.md` の環境メモは**現在も効いている制約だけ**を残し、実施当時の事実は
ここへ移した。

- 初回の `go tool sqlc` はビルドに20秒ほどかかる（2回目以降はキャッシュ）。cgo は不要だった
- `go tool goose` は初回のみモジュールをダウンロードする（60秒程度）。2回目以降はキャッシュが効く
- 手順1の検証環境: Docker 29.1.3 / Docker Compose v5.3.1 / macOS (darwin 25.6.0, arm64)

---

## 手順12a（2026-08-18）— `GET/POST /admin/users`

`ApiDesign.md` 6.1 / 6.2。**手順12 の前半**で、画面は 12b（`Design.md` 11.2.1 の分割規約）。
ブランチ `feature/step-12-users-api`。

### 先行して行った設計改訂（同ブランチの最初のコミット）

会話でのみ合意していた承認済みの改訂を、実装前にコミットした（`LEARNINGS.md` の申し送り）。

| ファイル | 内容 |
|---|---|
| `docs/Design.md` | 11.1 の見出しを「API と画面を同じ手順で進める」へ。11.2 を「1ステップ = ブラウザで確認できる単位」へ全面改訂。**11.2.1 を新設**（セッションを a/b に分けるときの規約）。Phase 1 手順一覧を統合し番号を詰めた。11.4 に rev.7 の対応表 |
| `.claude/commands/pb-step.md` | 手順3 に「分けるなら a で止まる。止まらないなら分けない。既定は分けない」 |
| `docs/PROGRESS.md` | Phase 1 表・プレースホルダ表・引き継ぎの番号を rev.7 へ |
| `client/src/router/routes.ts` ほか6ファイル | コメントと `status` 文言の番号（17→15、18→16、13→12b など） |
| `docs/Development.md` | L98 / L103 の「手順13以降」→「手順12b以降」 |

### 作ったファイル

| ファイル | 役割 |
|---|---|
| `server/internal/httpapi/v1/users.go` | `GET /admin/users`（6.1）。絞り込み・並び替え・ページング・ETag・`likePattern` |
| `server/internal/httpapi/v1/users_create.go` | `POST /admin/users`（6.2）。検証・単一トランザクション・409 の分岐 |
| `server/internal/auth/genpassword.go` | 初期パスワードの生成（語句連結方式）。語彙は形容詞16・名詞16 |
| `server/internal/httpapi/v1/users_test.go` | 単体。既定値・絞り込み・422・生成・manual・409・メール検証 |
| `server/internal/httpapi/v1/users_integration_test.go` | 実DB。4表のトランザクション・citext の衝突・並び替え・ETag・403 |

### 変えたファイル

| ファイル | 変更 |
|---|---|
| `server/internal/store/queries/user.sql` | `ListAdminUsers` / `SummarizeAdminUsers` を追加。**`CreateLocalCredential` に `must_change` を追加** |
| `server/internal/store/gen/*` | `make sqlc` の生成物 |
| `server/internal/httpapi/v1/routes.go` | `user.manage` 付きで2本を登録 |
| `server/internal/httpapi/v1/apitime.go` | `apiTimestamptz`（nullable な timestamptz → 応答）を追加 |
| `server/internal/httpapi/v1/fake_test.go` | ユーザー管理のフェイク（`opLog` に呼び出し順を残す） |
| `server/cmd/pb/admin_create.go` / `dev_seed.go` | `MustChange: false` を明示（理由をコメント） |
| `docs/openapi.yaml` | `/api/v1/admin/users` の GET / POST と `UserList` / `UserListItem` / `AgentInfo` / `CreateUserRequest` / `CreatedUser` |
| `client/src/api/schema.d.ts` | `make gen-api` の生成物（+251行） |

### 検証結果

**1. `make sqlc` / `go build` / `gofmt`** — すべて通る。

**2. `go test ./...`（server 全体）** — 全パッケージ PASS。
**ドリフト検出テストが期待どおり効いた**：`openapi.yaml` を書く前は
`GET /api/v1/admin/users が実装されているが docs/openapi.yaml に無い` で落ちた。

**3. `make gen-api`** — `client/src/api/schema.d.ts` に +251行。`npx vue-tsc --noEmit` が通る。

**4. 実DB結合テスト**（`PB_TEST_DATABASE_URL` あり）— 6件すべて PASS。

| 項目 | 確認したこと |
|---|---|
| 作成した4表が単一トランザクションで入る | `actor` / `app_user` / `user_identity` / `local_credential` が各1行。`must_change=true`。**返された初期パスワードで実際にログインでき、`GET /me` の `actor.must_change_password` が true**。監査ログが同トランザクションで入り、`detail` に平文が無い |
| メール重複は409で1行も残さない | **大文字にしても衝突する**（citext）。`already_exists`。失敗した側の actor が0行 |
| 一覧の絞り込みと並び替え | `q` が表示名・メールの両方に当たる。**`_` がワイルドカードとして働かない**（0件）。`kind=agent` は0件。`asc` の先頭と `desc` の末尾が一致 |
| ETagは総件数と更新で変わる | `W/"user-…"` で始まり、ユーザーを増やすと値が変わる |
| project_countはメンバーシップの件数 | プロジェクト未所属の管理者が 0 |
| operatorは403 | GET / POST とも 403。403 のあと actor が0行 |

**5. 実サーバ**（`make restart` → curl。再実行可能なスクリプトで 22件）— **全 PASS**。

```
== 1. 一覧の既定と並び順 ==      PASS 既定の一覧 (200) / 総件数 4 / ETag W/"user-4-…"
== 2. 作成（generate）==         PASS 201 / generated_password = rapid-maple-1786 / PASS 形式
== 3. 返された初期パスワードでログインできる ==
                                 PASS 新ユーザーでログイン / PASS GET /me (200)
                                 PASS actor.must_change_password (True)
== 4. 作成（manual）==           PASS 201 / PASS manual の generated_password (null)
== 5. 重複と検証エラー ==        PASS 409 / PASS already_exists
                                 PASS 422（details = ['display_name','email']）
                                 PASS per_page=201 / PASS kind=system / PASS sort=password
== 6. 絞り込みと ETag の変化 ==  PASS q（メール）で2件 / PASS q（表示名）で2件
                                 PASS kind=agent は0件 / PASS ETag が変わった
== 7. operator は 403 ==         PASS GET / PASS POST
== 8. CSRF なしは 403 ==         PASS X-PB-CSRF なし
== 後始末 ==                     PASS 総件数が元に戻った (4)
=== PASS 22 / FAIL 0 ===
```

**検証スクリプトは再実行可能にした**（`pb-step.md` 手順6）。総件数と CSRF は毎回読み直し、
作ったユーザーは末尾で削除して件数が戻ることまで確かめる。**最初に一覧1件だけの
スモークを通してから全体を回した**ため、2回の不備（下記）は副作用を残さずに直せた。

### 検証で見つかった「検証側」の誤り2件（実装は正しかった）

`LEARNINGS.md` #14 のとおり、まず検証側を疑って正解だった。

1. **`must_change_password` をトップレベルで読んでいた。** 実際は `actor` の下
   （`ApiDesign.md` 3.1 の応答構造。4.1 は同一構造）
2. **`q=verify-<stamp>` が2件当たると想定していた。** `verify-man-<stamp>` は
   `verify-<stamp>` を含まないので1件が正しい。`q=<stamp>` に変えた
3. （スクリプトの不備）`"$BEFORE（…"` と書き、bash が変数名を切り損ねた。
   **多バイト文字が続くときは `${BEFORE}` と書く**（11b の `${pids}` と同じ轍）

### 片付けた資源

- 実サーバ（`make stop-server` で待受のみ停止。停止後 `/healthcheck` が無応答であることを確認）
- 検証で作ったユーザー（スクリプトの後始末で削除。`actor` は4件のデモアカウントのみに戻った）
- スクラッチパッドの検証スクリプト3本（削除）
- `make clean-webui`（`make restart` が `make build` を含むため）
- **DBコンテナは起動したまま残した**（12b で続けて使う。`make down` で畳める）

`audit_log` に `user.create` の孤児（`actor_id IS NULL`）が4件あるが、これは
**`pb dev seed` が CLI として書いたもの**でテストの残骸ではない（CLI は actor を持たない）。

---

## 手順12b（2026-08-18）— ユーザー管理画面

`GuiDesign.md` 5.6 / 5.6.1。**手順12 の後半**で、これで手順12 の完了条件
（ブラウザでユーザーを追加でき、初期パスワードが1回だけ出る）を満たす。
ブランチ `feature/step-12-users-page`。**サーバ側の変更はゼロ**（12a のAPIをそのまま使う）。

### 先行して行った設計改訂（実装前に反映。`CLAUDE.md` 絶対規則3）

| ファイル | 内容 |
|---|---|
| `docs/GuiDesign.md` | 5.6 の作例を実装する列構成（種別・名前・メール・ロール・状態・最終ログイン・作成）へ改訂。**「アドミン」→「アドミニストレータ」**（5.6 の作例と 5.6.3 の表頭）。行クリックで詳細へ遷移する旨・ロール表示名の正本・ソート可能な列・**検索と絞り込みは常時表示**を追記 |
| `docs/ApiDesign.md` | 6.1 の `q` から「ユーザー数20件超のときのみUIに表示」を削除 |
| `docs/openapi.yaml` | 同じ規則の写しを直し、`make gen-api` まで走らせた |

### 作ったファイル

| ファイル | 役割 |
|---|---|
| `client/src/pages/UsersPage.vue` | 一覧・タブ・検索と絞り込み・4状態・ページャ・追加の結果表示（`GuiDesign.md` 5.6） |
| `client/src/components/AddUserModal.vue` | ユーザー追加モーダル（5.6.1）。`Modal.vue` の3つ目の消費者 |
| `client/src/components/GeneratedPasswordDialog.vue` | 初期パスワードの1回表示（5.6.1）。`Modal.vue` の4つ目の消費者 |
| `client/src/api/users.ts` | `listUsers` / `createUser`（`ApiDesign.md` 6.1 / 6.2） |
| `client/src/lib/roles.ts` | ロール表示名の対応表。**手順14 の `GET /roles` でファイルごと捨てる** |

### 変えたファイル

| ファイル | 内容 |
|---|---|
| `client/src/router/routes.ts` | `/admin/users` を `PlaceholderPage` から `UsersPage` へ |
| `client/src/pages/ProjectSettingsPage.vue` | `ROLE_LABELS` を `lib/roles.ts` へ移した（3行の移動。動作は変わらない） |
| `client/src/api/schema.d.ts` | `make gen-api` の再生成（`q` の説明のみ） |

### 検証結果

`go test ./...`（12パッケージ、`-count=1`）と `npm run typecheck` が通ること。
画面は `make restart` 後の :8080（embed 済み）に対し、ヘッドレス Chrome ＋ CDP で
**110件**を確認した（`Development.md` 8.2、`--window-size=1440,900`）。

| 区分 | 件数 | 主な内容 |
|---|---|---|
| 読み取り（`verify_read.py`） | 55 | メニューからの到達、見出し、タブ、**検索と絞り込みが1行に収まること（実測36px・同一 top）**、列7つ、押せるのは4列、既定の並びがAPIの応答と一致、ロールが略されないこと、件数、ソート4列、検索、空状態と条件のクリア、`kind`/`is_active` の絞り込み、`j`/`k`、行クリックで詳細へ、ロールと権限タブ、**オペレータではメニューに出ず直リンクで 403** |
| 追加（`verify_add.py`） | 38 | モーダルの初期フォーカス（表示名）、既定値（operator / generate / 初回変更オン）、入力欄が見えていること、**初期パスワードの形式・コピー・再表示不可の明記**、**表示された値で実際にログインできること**、結果が一覧の直上に出ること、取り直すと最終ログインが入ること、手動設定ではダイアログを出さないこと、12文字未満を弾くこと、**メール重複の 409 がメール欄の下に出ること** |
| ページング（`verify_paging.py`） | 11 | 26件での2ページ表示、スクロールでページャに届くこと、範囲表示の追随、検索で1ページ目に戻ること |
| 無効ユーザー（`verify_inactive.py`） | 5 | 状態列が「無効」と出ること、`is_active` の絞り込み3値、既定（すべて）では無効も同じ表に並ぶこと |

**実測（`getBoundingClientRect()`）で確かめたもの**：`[+ ユーザー追加]`、検索欄（幅240px）、
絞り込み2つ、モーダルの入力欄、コピーボタン、結果表示、ページャ、読み上げ用テキストが
1px以下であること。

### 検証で見つかった「検証側」の誤り4件（実装は正しかった）

| 誤り | 実際 |
|---|---|
| 並び順を Python の `sorted()` と突き合わせた | **サーバは `COLLATE "ja-JP-x-icu"` で並べる**（`DbDesign.md` 4.4、`user.sql`）。「閲覧者→管理者」は日本語の読み順として正しい。**APIの応答との突合に直した**（並び順の正本はサーバ） |
| 種別アイコンをセル全体の `textContent` で読んだ | 読み上げ用の `.visually-hidden`（9.2）が混ざっていた。表示用の `[aria-hidden]` だけを読み、隠し文字は別に「1px以下であること」を確かめる形にした |
| 作成直後の行に最終ログインが入ることを期待した | 一覧は**作成した時点**で取り直しており、その後に検証が行ったログインは反映されない（6.4 は楽観的更新を禁じる）。「作成直後は —」「取り直すと入る」の2件に分けた |
| ページャを画面座標のままクリックした | 25行の表はビューポートより高く、ページャは y=1457（画面外）にある。**利用者と同じくスクロールしてから押す**ようクリック補助を直した |

### 実機の画面を見て直したもの

| 気づき | 対応 |
|---|---|
| 「ロールと権限」タブの本文にバッククォートがそのまま出ていた | 平文なので記号として表示される。落とした |
| 名前列に余白が偏り、右側が間延びしていた | 余りをメール列へ渡し、名前列を 280px 固定にした（長くなりうるのはメール） |

**自動検証が全 PASS でも、見た目の崩れは拾えない**（11b と同じ）。今回はスクリーンショットを
撮って自分で見たことで気づいた。

### 手順12b の追補（2026-08-18、利用者の実機確認の指摘6件。マージ前）

利用者が実際に画面を触って出た指摘。**うち2件は私が実装した不具合**で、残りは設計の見直しである。

| # | 指摘 | 対応 |
|---|---|---|
| ① | メールがほとんど見えない | **不具合。** 全列に既定幅と下限を持たせ、表の幅を幅の合計にした。領域を超えたら表だけ横スクロール。あわせて**列幅をドラッグで変えられるように**した（保存しない） |
| ② | エージェントは別タブに | `GuiDesign.md` 5.6 を3タブへ改訂。12b では種別セレクトを外し `kind=user` を送る。エージェントタブの実装は Phase 2 |
| ③ | ロール・状態でもソートしたい | **12c として起票**（API変更を伴うため） |
| ④ | タブ分けは分かりやすい | 対応なし（手順14 で中身を作った時点で再判断） |
| ⑤ | フィルタがロール・状態にも効いてほしい | 状態は既に効く。**検索語がロール名にも当たる**ようにするのは 12c |
| ⑥ | 無効ユーザーを視覚的に | 行の文字を `--pb-text-muted` で一段下げ、種別アイコンの彩度を落とした |

#### 追補で変えたファイル

| ファイル | 内容 |
|---|---|
| `docs/GuiDesign.md` | 5.6 を3タブへ・種別セレクトの撤去・**列幅の可変（ドラッグのみ／保存しない）**・無効行の表現を追記。**9.2 にキーボード操作の例外を1行** |
| `client/src/pages/UsersPage.vue` | 列定義に既定幅と下限・`<colgroup>`・つまみとドラッグ・横スクロールのラッパ・エージェントタブ・無効行のスタイル・種別セレクトの撤去 |

#### 追補の検証結果（128件）

| 区分 | 件数 | 主な内容 |
|---|---|---|
| 読み取り | 54 | 既存の55件から、種別セレクトの撤去とタブ3つに合わせて更新。**件数はサーバから読む形に直した**（利用者の確認用アカウントが増えて固定値が壊れたため） |
| 列幅とタブ | 20 | **窓 1440/1280/1024/900px で下限を保つこと**（今回の不具合の回帰）、表だけが横スクロールしページ全体は流れないこと、ドラッグで広がる・戻る・下限で止まる、**つまみのドラッグでソートが変わらない**こと、`body` の style を残さないこと、読み直すと既定へ戻ること、タブ3つ、`kind=user` を送ること、エージェントタブの予定 |
| 追加 | 38 | 変更なし（追補後のビルドで通し直した） |
| ページング | 12 | 同上 |
| 無効行の見え方 | 4 | 文字色が有効な行と違うこと、アイコンの彩度が落ちること、**「無効」の文字が残ること**（9.2） |

#### 追補で片付けた資源

**`make dev-reset` は使っていない。** 利用者が手元の確認で作ったアカウント
（`fun.taro@mogemoge5.ab.cd`）が入っていたため、**検証が作ったものだけを消した**。

```
DELETE FROM actor WHERE id IN (SELECT actor_id FROM app_user
  WHERE email LIKE 'gen%@example.com' OR email LIKE 'man%@example.com'
     OR email LIKE 'bulk%@example.com');   -- 21件
```

`audit_log.actor_id` は `SET NULL`、`app_user` / `user_identity` / `local_credential` は
`CASCADE` なので、この1文で片付く（削除規則は実行前に `information_schema` で確認した）。
表示確認のため一時的に `is_active=false` にした `viewer@example.com` は**元の値に戻した**
（変更前の値を記録してから倒した）。

---

### 片付けた資源

- `make dev-reset` でDBを作り直した（検証で作った26ユーザーと、表示確認のため直接倒した `is_active=false` の1件を消した）。**`actor` は4件に戻っている**
- `make stop-server` でサーバを停止した（:8080 を掴むプロセスなし）
- `make clean-webui` で `server/internal/webui/dist` を戻した
- ヘッドレス Chrome のプロファイルは毎回 `tempfile.TemporaryDirectory()` で作り、終了時に消える
- **DBコンテナは起動したまま残した**（利用者が画面を見て「ロールと権限」タブを判断するため。`make down` で畳める）

---

## 手順12c（2026-08-19）— ユーザー一覧のソートと検索を広げる

`ApiDesign.md` 6.1。**利用者の実機確認（12b）から出た要望**で、`Design.md` 11章の手順ではなく
`PROGRESS.md`「手順外の作業」に 12c として起票したもの。ブランチ `feature/step-12-users-sort-search`。

### 先行して行った設計改訂（`CLAUDE.md` 絶対規則3）

| ファイル | 内容 |
|---|---|
| `docs/ApiDesign.md` 6.1 | `sort` に `system_role` / `is_active` を追加。`q` の対象に `role.display_name`。**並び順の規則**（`sort_order` 基準・エージェントは末尾・無効が先）を本文に明記 |
| `docs/GuiDesign.md` 5.6 | 「ソートできるのは4列」→「種別以外のすべての列」。検索がロール名に当たること、**「エージェント」表示は画面が作る文字列なので当たらない**ことを明記 |
| `docs/openapi.yaml` | `sort` の enum と `q` の説明。`make gen-api` まで実施 |

### 変えたファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/user.sql` | `role` を LEFT JOIN（`key = system_role AND scope='system'`）。`ORDER BY` に2組（`NULLS LAST` を両方向に明示）。`WHERE` は `ListAdminUsers` と `SummarizeAdminUsers` で一字一句そろえた |
| `server/internal/store/gen/*` | `make sqlc` の生成物 |
| `server/internal/httpapi/v1/users.go` | `userSortSpec.Allowed` に2値。`likePattern` のコメントに対象列 |
| `server/internal/httpapi/v1/users_test.go` | `TestListUsersAcceptsAllSortFields`（6つの `sort` がクエリ層へ渡る） |
| `server/internal/httpapi/v1/users_integration_test.go` | `ロールと状態での並び替え・ロール名での検索`（実DBでの行順とロール名検索） |
| `client/src/api/users.ts` | `UserSort` に2値 |
| `client/src/pages/UsersPage.vue` | ロール列・状態列に `sort`。初期方向は `DESC_FIRST`（日時2列）だけ降順 |

### 検証結果

`go test ./...`（実DB付き、`-count=1`）と `npm run typecheck` が通ること。画面はヘッドレス
Chrome ＋ CDP で **24件**（12c の本体12件＋回帰12件）。

| 区分 | 主な内容 |
|---|---|
| 結合テスト | **行の前後関係そのもの**を確認——昇順でオペレータがアドミニストレータより前、降順で逆。無効な行が昇順で先。`q=アドミニストレータ` でアドミンが当たりオペレータは当たらない。**`q=administrator`（キー）では当たらない** |
| ブラウザ（12件） | 押せる列が6つになったこと、ロール昇順が `role.sort_order` の順（オペレータ→アドミン）、降順が逆、**画面の並びが API の応答と一致すること**、ロール名での絞り込み、キーでは当たらないこと |
| ブラウザ（回帰12件） | 無効な行が昇順で先頭・降順で末尾に来ること、**無効な行の表現（12b）が壊れていないこと**、名前・メール・最終ログイン・作成のソートが API と一致すること、タブ3つ・絞り込み1つ・列幅のつまみ・件数表示 |

**並び順の検証は「API の応答と突き合わせる」形に統一した**（`LEARNINGS.md` #25）。並び順の正本は
サーバであり、検証側で並べ直すと `ja-JP-x-icu` や `sort_order` と食い違って誤検知する。

### 検証で見つけた「検証側」の誤り

`document.querySelector(...)` の戻り値をそのまま真偽に使っていた。**CDP は DOM 要素を `{}` に
直列化し、Python では偽になる**ため、件数表示（実物は「5件」と出ている）が FAIL した。
テキストを返す形に直した。

### 片付けた資源

- 状態の並びを見るため `viewer@example.com` を一時的に `is_active=false` にし、**元の値に戻した**（変更前の値を記録してから）
- 結合テストが作る行は `t.Cleanup` で消える（`usr-%` / `search-%` / `inactive-%` が0件であることを確認）
- `make stop-server` / `make clean-webui` 済み。**DBコンテナは起動したまま**（利用者が Rancher Desktop を起動したところなので畳まない）
- **スクラッチパッドは日付をまたいで消えていた**ため、CDP ドライバ（`cdp.py` / `common.py`）を作り直した

---

## 手順外の作業：画面名の改称と列幅の挙動（2026-08-19）

12c のマージ後、利用者の実機確認から出た3件。ブランチ `feature/account-naming-and-column-widths`。

### ① 画面名を「アカウント / 権限」へ

`GuiDesign.md` 3.1 / 3.2 / 4.1 / 4.3 / 5.6 / 5.9.2、`ApiDesign.md` 1章・11章の対応表、
`Design.md` 8.2 の画面名を揃えた。ASCII 図は「ユーザー」→「アカウント」で1文字ぶん広がるため、
**枠がずれないよう余白を1つずつ詰めた**。

**変えなかったもの**：URL（`/admin/users`。パスを変えるとリンクが壊れ、APIとも食い違う）、
タブ名の「ユーザー」（人間だけを指す）、ファイル名（`UsersPage.vue`。利用者から見えない）。

### ②③ 列幅の挙動（`GuiDesign.md` 5.6 を「総幅＝表示領域」の4規則に書き換えた）

| 場面 | 挙動 |
|---|---|
| 余りがある | 最後の列（作成）が受け取る |
| ウィンドウ幅・**メニューの折りたたみ**で表示領域が変わった | 差分を最後の列が吸収 |
| ドラッグ | 右隣とだけ融通し、総幅は変わらない |
| 最後の列が下限に達した | 総幅が表示領域を超え、表だけ横スクロール |

`client/src/pages/UsersPage.vue` に `fitToContainer()` と `ResizeObserver` を足し、
ドラッグを右隣との融通に変えた。**監視は `window` の resize ではなく表示領域の要素**——
メニューの折りたたみ（`[`）ではウィンドウ幅が変わらず resize が起きないため。

### 検証結果（32件）

| 区分 | 件数 | 主な内容 |
|---|---|---|
| 列幅（`verify_widths.py`） | 23 | 起動時・拡大・縮小で総幅＝表示領域、**広げたぶんを受け取るのは最後の列だけで他は動かない**、最後の列が下限に達したら横スクロールへ、ドラッグで右隣と融通して総幅が変わらない、両側の下限で止まる、読み直すと既定へ戻る |
| 改称と追従（`verify_naming.py`） | 9 | 見出しとメニューの表記、タブ名とURLは変えていないこと、**メニューの折りたたみに総幅が追従すること**、ソートとつまみが生きていること |

### 検証で見つけた「検証側」の誤り3件（実装は正しかった）

いずれも**規則どおりに動いた結果に対して期待値が誤っていた**。

| 誤り | 実際 |
|---|---|
| 窓 1280px で「総幅＝表示領域」を期待した | 他の列が既定幅のままなので作成列が下限（90px）に達し、総幅 1000 > 992 で横スクロールへ。**仕様どおり**。余りが残る幅（1360px）で追従を見る形に直し、下限に達する境目は別の1件として確かめた |
| ドラッグ 100px で「100px 動く」ことを期待した | 右隣（ロール 150px・下限 90px）が出せるのは 60px まで。**仕様どおり**。範囲内の 40px で「そのぶんだけ動く」ことを見る形に直した |
| （同上） | 同上 |

### 片付けた資源

- `make stop-server` / `make clean-webui` 済み。**DBは触っていない**（読み取りのみの検証）
- **DBコンテナは起動したまま**（利用者が Rancher Desktop を起動したところなので畳まない）

---

## 手順13a（2026-08-20）— ユーザー詳細・編集 API（`ApiDesign.md` 6.3〜6.8）

手順13 を **13a（API）/ 13b（画面）** に分けた1回目。ブランチ `feature/step-13-user-detail-api`。
**手順13 の完了条件（ブラウザでロール変更・無効化・パスワードリセットができる）は 13b で満たす。**

実装したエンドポイントは7本。すべて `user.manage`（6章の前書き）。

| | |
|---|---|
| `GET /admin/users/{id}` | 6.3。本体＋`identities` / `project_memberships` / `sessions` を1回で返す |
| `PATCH /admin/users/{id}` | 6.4。`If-Match` 必須。3つのガード |
| `DELETE /admin/users/{id}` | 6.5。物理削除。監査を先に書く |
| `POST /admin/users/{id}/password-reset` | 6.6。生成のみ。全セッション失効つき |
| `POST /admin/users/{id}/sessions/revoke` | 6.7。冪等 |
| `PUT /admin/users/{id}/memberships/{key}` | 6.8。追加と変更を兼ねる |
| `DELETE /admin/users/{id}/memberships/{key}` | 6.8。冪等 |

### 変えたファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/user.sql` | 21本追加（詳細4本・更新4本・削除5本・資格情報3本・メンバーシップ3本・件数2本） |
| `server/internal/store/gen/*` | `make sqlc` の生成物 |
| `server/internal/httpapi/v1/users_get.go` | 6.3。**`buildUserDetail` を PATCH と共有**（5.5 が 5.4 と同形式であるのと同じ扱い）。`userIDFromRequest` / `adminUserContext` を `/admin/users/{id}` 配下の共通の取り出しとして置いた |
| `server/internal/httpapi/v1/users_update.go` | 6.4 / 6.5。ガード3つ・`nargText` / `nargBool`・`classifyUserUpdateMiss`（409 と 404 の切り分け）・`writeUserUpdateError` |
| `server/internal/httpapi/v1/users_credentials.go` | 6.6 / 6.7。`decodeOptionalJSON`（空の本文を誤りとしない） |
| `server/internal/httpapi/v1/users_memberships.go` | 6.8。`resolveMembershipProject` / `recordMembershipChange` |
| `server/internal/httpapi/v1/routes.go` | 7本の宣言。**プロジェクト個別（5.4〜5.6）と違い `RequirePermission` のまま**——誰のアカウントを触るかで必要権限が変わらない |
| `server/internal/httpapi/v1/fake_test.go` | 新クエリ22本のフェイク実装とフィールド |
| `server/internal/httpapi/v1/users_detail_test.go` | 単体39件（新規） |
| `server/internal/httpapi/v1/users_detail_integration_test.go` | 実DB結合13件（新規） |
| `docs/openapi.yaml` | 7本のパスと8スキーマ（`UserDetail` / `UserIdentity` / `UserMembership` / `UserSession` / `UpdateUserRequest` / `PasswordResetRequest` / `GeneratedPassword` / `MembershipRequest`）、パラメータ `UserID` |
| `client/src/api/schema.d.ts` | `make gen-api` の生成物 |

**再利用したもの**（引き継ぎのとおり）：`validateEmail` / `isEmailConflict` / `adminUsersPath`
（12a）、`parseIfMatch` / `errVersionConflict`（11a）、`InvalidateActorPermissionCache`（6b、
初めての呼び出し元）、`auth.GeneratePassword`（12a）。

**画面と `client/src/api/users.ts` のラッパは 13b。** 消費者と同じセッションに入れる。

### 検証結果

| 区分 | 件数 | 内容 |
|---|---|---|
| `make test` | 全パッケージ | ビルド・単体・**OpenAPI ドリフト検出**（7本の未記載を実際に検出させてから `openapi.yaml` を書いた） |
| 単体（`users_detail_test.go`） | **39件** | 応答の形・ガードの分岐・監査に何を書くか。`nil` ではなく空配列・`role` というキー名・`token_hash` が漏れないこと |
| 実DB結合（`users_detail_integration_test.go`） | **13件** | 楽観ロック・`subject` の追随・CASCADE・冪等な `joined_at`・管理者の数え方・レート制限を消費しない作り |
| 実サーバ（curl） | **42件** | 下表 |
| `vue-tsc --noEmit` | — | 生成された型で client がコンパイルできること |

実サーバ検証（`scratchpad/verify13.sh`）の内訳。**スモーク（ログイン＋`GET /me`）を先に通してから
全体を回した**（`LEARNINGS.md` #21）。対象ユーザーは毎回新しく作り、`trap` で必ず消す。

| 区分 | 主な内容 |
|---|---|
| 6.3 | 200 と4ブロック、存在しない `id` は 404 |
| 6.4 | 表示名の更新／**`actor` だけ変えても `version` が進む**（1→2）／`If-Match` 省略で 422／古い `version` で 409／メール重複で 409 `already_exists`／不正な `system_role` で 422／**メール変更後にそのメールでログインできる**／自分のロール変更・無効化で 409 かつ **409 のとき `version` が進まない** |
| 6.6 | 生成パスワードが `^[a-z]+-[a-z]+-[0-9]{4}$`／**旧セッションが 401**／新パスワードでログイン／`mode=manual` は 422／本文なしでも 200 |
| 6.7 | 204 と冪等（2回目も 204） |
| 6.8 | 付与→ロール変更で **`joined_at` が動かない**／不正なロールは 422／存在しないプロジェクトは 404／剥奪と冪等な2回目 |
| 認可 | operator が7本すべてで 403、CSRF 無しで 403 |
| 6.5 | 自分の削除は 409、削除は 204、削除後は 404 |

監査ログを実DBで確認（対象1人ぶん）。**設計どおりの13行**だった。

```
user.create 1 / user.update 3 / role.change 4 / password.reset 2 / session.revoke 2 / user.delete 1
```

`role.change` 4件の内訳は、`system_role` の変更1＋メンバーシップの PUT 2＋DELETE 1。
**パスワードリセット2回は `session.revoke` を増やしていない**（1操作を2行にしない判断が効いている）。
`user.delete` の `detail` に削除時点の表示名・メール・ロールが残っていることも確認した。

### 検証で見つけた「検証側」の誤り（実装は正しかった）

**結合テストで `failed_attempts = 0` を期待して FAIL した。** 原因は測る順序で、
**旧パスワードで故意にログインを失敗させた直後に読んでいた**（失敗は正しく1に増える）。
しかも**リセット前が0でないことを一度も確かめておらず、通っても意味のない検証**だった。

直し方は2つ。①ロックされた状態を **SQL で先に作る**（`failed_attempts=3` / `locked_until`）——
6.6 の主な用途は「ロックされた利用者を救う」ことなので、これでようやく「戻った」ことを測れる。
②**リセットの直後に読む**（後続のログイン試行より前）。

**ログインで作らなかったのは、レート制限（2.9、IP あたり10回/分）を消費するため。**
`httptest.NewRequest` は固定の `RemoteAddr` を入れるので、同じ窓の11回目が 429 になる。
既存のテストは**ちょうど10回**で通っており、1件足すだけで無関係な検証が落ちる状態だった。
`loginAsWith` が呼び出しごとに違う擬似 IP（RFC 5737 の `198.51.100.0/24`）を使うようにした。

もう1件、`最後の有効なアドミニストレータを守る` は当初 `t.Skip` で逃げていた（同じDBに
利用者のアカウントが居るため1人にできない）。**スキップは検証していないのと同じ**なので、
`CountActiveAdministrators` の数え方そのものを測る形に書き換えた（上の判断表を参照）。

### 片付けた資源

- **検証で作ったユーザーは検証自身が消す**（`trap` と `t.Cleanup`）。`step13-%` / `dtl-%` /
  `mailchg-%` / `詳細テスト%` / `手順13%` と `kind='system'` の actor がいずれも0件、
  `dtl-%` のプロジェクトも0件であることを確認した
- **削除済みユーザーを指す `audit_log` 13行を消した**（もう存在しないアクターへの参照）
- **残したもの**：デモアカウント（`admin@` / `viewer@`）の `login.success` 9行と
  `permission.denied` 7行。**これらは通常の利用と区別がつかず、監査ログが本来残すべき種類の記録**
  であるため消していない
- `make stop-server` 済み（`:8080` が空いていることを確認）。**DBコンテナは起動したまま**
- **Rancher Desktop が停止していたので起動した**（`make up` の前提）。畳んでいない
- スクラッチパッドの `smoke.sh` / `verify13.sh` は残した（13b の回帰確認に使えるため）

---

## 手順13b（2026-08-21）— ユーザー詳細・編集画面と一覧の `[⋯]`（`GuiDesign.md` 5.6 / 5.6.2）

ブランチ `feature/step-13-user-detail-screen`。**手順13 の完了条件（ブラウザでロール変更・
無効化・パスワードリセット・プロジェクト権限の付与ができる）をここで満たした。**

API は 13a で実装済みで、13b は**画面と API ラッパだけ**である（`docs/openapi.yaml` は触っていない）。

### 実装前に一括で確認した設計判断（12件）

利用者の回答で4件が変わった（`[⋯]` はメニューアイコンのプレースホルダ／詳細のメニューは
「詳細画面に無い操作」の置き場／有効化は管理者が当該ユーザーのメニューから／ヘッダは1行）。
残り8件は推奨どおり。経緯は `decisions.md` の 2026-08-21 の行。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/pages/UserDetailPage.vue` | 詳細・編集（5.6.2 の5ブロック）。約860行 |
| `client/src/components/UserActionsMenu.vue` | `[⋯]`。**項目を配列で受け取る**。`<Teleport>` + `position: fixed` で祖先の `overflow` に切られない |
| `client/src/components/DeleteUserDialog.vue` | 削除の確認（6.3 が名前の入力を求める唯一の操作） |
| `client/src/components/MembershipModal.vue` | プロジェクト権限の付与（候補は `GET /projects?per_page=200&sort=key&order=asc&status=all`） |

### 変えたファイル

| ファイル | 変更 |
|---|---|
| `client/src/api/users.ts` | 6.3〜6.8 のラッパ7本（`getUser` / `updateUser` / `deleteUser` / `resetUserPassword` / `revokeUserSessions` / `putMembership` / `deleteMembership`） |
| `client/src/api/client.ts` | `put` / `del` を追加（`delete` は予約語なので `del`） |
| `client/src/pages/UsersPage.vue` | 操作列（固定44px・幅調整の対象外）、`[⋯]` の5項目、通知欄の使い回し（`createdNotice` → `notice`）、削除後の通知の受け取り |
| `client/src/components/PageHeader.vue` | `#lead` / `#subtitle` スロット（48px 1行を崩さない） |
| `client/src/components/GeneratedPasswordDialog.vue` | `title` / `leadSuffix` を prop 化し、作成とリセットで共用 |
| `client/src/lib/roles.ts` | `SYSTEM_ROLES` / `PROJECT_ROLES`（キー・表示名・説明。`DbDesign.md` 7.3 の写し） |
| `client/src/styles/base.css` | `.primary` / `.secondary` / `.danger` の正本（既存の写しは撤去せず起票） |
| `client/src/router/routes.ts` | `/admin/users/:id` をプレースホルダから実画面へ |
| `docs/GuiDesign.md` | 5.6（操作列は幅調整の対象外・`⋯` は暫定表示・有効化・編集の行き先）、5.6.2（ヘッダ1行・`[⋯]` の4項目・結果の出し先）、6.3（確認を挟む操作を2行追加） |

### 検証結果（68件・すべて PASS）

`npm run typecheck` / `npm run build` / `make test` に加えて、ヘッドレス Chrome + CDP
（`Development.md` 8.2）で通した。**検証用ユーザー3人を API で作り、API で消した**
（`make dev-reset` は打っていない。利用者のデータが同じDBにある）。

| 群 | 件数 | 内容 |
|---|---|---|
| A 列と幅 | 8 | 操作列が最後・44px 固定・つまみ無し・つまみは6本・総幅＝表示領域・900px でメール列が下限を割らない |
| B メニュー | 7 | 5項目・他人の行では全部押せる・自分の行では無効化と削除が `disabled` で理由が読める・Esc で閉じる |
| C 一覧からの操作 | 19 | 失効／リセット（生成値の形式）／無効化（行が背面へ下がる）／有効化（確認なし）／削除（名前が一致するまで押せない）と、結果が表の直上に出ること |
| D 詳細（前半） | 20 | ヘッダ 48px 1行・4要素が同じ行・5ブロックの順・編集で2欄・保存で `version` +1・`user_identity.subject` の追随・メール重複は欄の下・楽観ロック 409 と `[最新の内容を取得]` |
| E〜H 詳細（後半） | 32 | ロール変更と自分自身の `disabled`・メンバーシップの追加/変更/剥奪（`joined_at` が動かない）・リセット後のパスワードでログイン・**セッションを1本作ってから**の失効（「1件のセッションを失効しました」）・詳細からの削除と一覧への着地・再読み込みで通知が復活しない |
| I ガード | 1 | operator は `/admin/users/:id` で `/403` へ |
| K 文言 | 1 | 日本語の途中に空白が入らない（下記） |

**スクリーンショットを6枚撮って自分で見た**（1440px と 900px、メニューを開いた状態、編集中）。
自動検証が全 PASS のあとに**1件直した**——`プロジェクトの権限がありません。…メンバーでなくても すべての…`
と、テンプレートの改行位置に空白が入っていた（日本語は行を折り返すとその位置が空白になる）。

### 検証で見つけた「検証側」の誤り5件（実装は正しかった）

| 誤り | 実際 |
|---|---|
| つまみの本数を「5本」と手で置いた | 全8列 − 操作列 − 作成列 = **6本**。期待値を規則から計算していなかった（`LEARNINGS.md` #33） |
| `.notice` / `.ok` の文字を `startswith` で見た | 閉じるボタンの `✕` や先頭の `✓` が混じる。**要素の直下のテキストだけを読む**ようにした（#25） |
| メール重複のエラーを `.error` に探した | サーバは `details[].field='email'` を返すので**欄の下**に出る（2.5）。実装が正しい |
| `PATCH` を `If-Match` なしで送って 422 | 2.8 は必須。検証用の API ヘルパに `if_match` を足した |
| 2つ目の Chrome を同じデバッグポート（9222）で起動 | 1つ目の DevTools へつながり、**同じプロファイルの Cookie を書き換えて管理者のセッションを取り違えた**。インスタンスごとに空きポートを取るようにした |

### 片付けた資源

| 資源 | 扱い |
|---|---|
| 検証用ユーザー3人 | API で削除（`verify13b-*`）。残り0件を API で確認 |
| ヘッドレス Chrome・プロファイル・Cookie | すべて終了・削除（プロファイルは呼び出しごとに別ディレクトリ） |
| スクラッチパッドの検証スクリプトとスクリーンショット | 削除（セッションをまたいで残らない前提。`LEARNINGS.md` #31） |
| `audit_log` の21行 | **意図して残した。** 管理者が実際に行った操作の記録で、6.5 が「誰を消したか追えるように」と定めている当のものである。`login.failure` もロックも作っていない |
| `server/internal/webui/dist` | `make clean-webui` でプレースホルダへ戻した |

## 手順14（2026-08-22）— ロールと権限（`ApiDesign.md` 7.1 / 7.2、`GuiDesign.md` 5.6.3）

ブランチ `feature/step-14-roles-permissions`。**API 2本・画面1枚・`lib/roles.ts` の全廃を1セッションで行った**（分割せず）。

### 設計文書の改訂（実装より先に当てた）

| 文書 | 内容 |
|---|---|
| `ApiDesign.md` 7.1 | `scope` クエリ（値域 `system` / `project`、それ以外は 422）と **scope 別の必要権限**。開放の理由・暫定である旨・値域を閉じる理由を追記 |
| `ApiDesign.md` 7.2 | `user.manage` のまま据え置く理由、並びとエンベロープ |
| `GuiDesign.md` 5.6.3 | ワイヤーを**5列**へ。グループ見出し・カテゴリ区切り・キー＋説明・**表の規則（権限列の固定）**を追記 |
| `GuiDesign.md` 5.6 / 5.9.2 | 「画面が対応表を持つ」記述を撤去 |
| `Design.md` 6.4.3 | 「Phase 1 ではシステムロール2種のみをUIで扱う」を撤去（**13b の時点で実装と食い違っていた**） |

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/authz.sql`（追記） | `ListRoles` / `ListRolePermissionAssignments` / `ListPermissions`。**既存の `ListRolePermissions`（キー順・認可判定用）は触らない** |
| `server/internal/httpapi/v1/roles.go` | `listRoles` / `listPermissions`、`catalog[T]` エンベロープ、`parseRoleScopeFilter` |
| `server/internal/httpapi/middleware/authz.go`（追記） | **`RequirePermissionUnlessQuery`**。1本のルートでクエリの値により必要権限が変わる場合に宣言をルート定義へ残す |
| `server/internal/httpapi/v1/routes.go` | `GET /roles` / `GET /permissions` の2行 |
| `server/internal/httpapi/v1/roles_test.go` | 単体8件 |
| `server/internal/httpapi/middleware/authz_test.go`（追記） | 単体4件（素通し・それ以外は要権限・完全一致のみ） |
| `server/internal/httpapi/v1/roles_integration_test.go` | 実DB結合8件 |
| `docs/openapi.yaml` | 2パス＋`RoleList` / `Role` / `PermissionList` / `Permission` |
| `client/src/api/roles.ts` | `getRoles(scope)` / `getPermissions()` |
| `client/src/stores/roles.ts` | **表示名の唯一の供給元**。`ensureRoles(scope)` / `ensurePermissions()` / `roleLabel` / `userRoleLabel` |
| `client/src/components/RolePermissionMatrix.vue` | 5.6.3 のマトリクス |
| `client/src/lib/roles.ts` | **削除**（消費者5つをストアへ移した） |
| `client/src/pages/UsersPage.vue` | プレースホルダ→マトリクス、ロール列、`[+ ユーザー追加]` をユーザータブ限定に |
| `client/src/components/AddUserModal.vue` | 独自配列を捨て `GET /roles` へ。`SystemRole` 型を openapi 生成物から取る |
| `client/src/components/MembershipModal.vue` / `pages/UserDetailPage.vue` / `pages/ProjectSettingsPage.vue` | ストア経由へ |

### 検証結果

**Go（`make test` 全パッケージ通過）**

- 単体12件（`roles_test.go` 8・`authz_test.go` 4）
- 実DB結合8件：シード5ロールが `sort_order` 順／権限28件と category／`permissions[]` が `permission.sort_order` 順／administrator 28件・project_viewer 3件／`scope` の絞り込みが**2本のクエリに同じく効く**／オペレータは `?scope=project` だけ通る／`all` と不正値は 422／未認証は 401

**実サーバ（`make restart` 後、デモの4アカウント）**

| 利用者 | `/roles` | `?scope=system` | `?scope=project` | `/permissions` |
|---|---|---|---|---|
| admin（administrator） | 200（5件） | 200（2件） | 200（3件） | 200（28件） |
| pm（operator／PJ管理者） | 403 | 403 | **200（3件）** | 403 |
| member（operator／メンバー） | 403 | 403 | **200（3件）** | 403 |

`scope=SYSTEM` / `all` / `projects` / ULID はいずれも 422（`details[].field = "scope"`）。応答のキーは `items` のみ、`ETag` なし。

**ブラウザ（ヘッドレス Chrome / CDP、34件すべて PASS）**

- マトリクス13件：ⓘ の注意書き／5列／見出しが `display_name` と一致／グループ見出しの `colspan`／カテゴリ8つ／28行／キー＋説明／**140マスすべてが `GET /roles` の `permissions` と一致**／`✓` の総数／タブ別の `[+ ユーザー追加]`／一覧のロール列
- レイアウト14件（**1440px と 900px の両方**）：ページ全体が横に流れない／ヘッダ2段目が1段目の実測の高さに貼り付く／**横スクロールしても権限列が動かない**／ロール列は実際に動く／**固定した権限列の帯に何が描かれているか**（`elementFromPoint`）／縦スクロールでロール名の行が残る／中央寄せの実効値
- 回帰7件：追加モーダルの選択肢と説明／ユーザー詳細のシステムロールとメンバーシップ／**pm（`user.manage` なし）でプロジェクト設定のメンバータブにロール表示名が出る**

**実装を直した3件（いずれも検証が見つけた）**

| 見つけ方 | 内容 |
|---|---|
| 実サーバのAPI検証 | `?scope=all` が 200 だった（7.1 の値域は `system` / `project`）→ 実装を直した |
| 900px の実測 | **文書が横に7px流れていた。** はみ出しは中身のない空白で、入れ子のスクロール容器が自前の縦スクロールバーを持つときに生じる → `contain: paint` |
| スクリーンショットの目視 | 表より枠が広く右に空白 → `width: max-content`。**横スクロール時にロール名が固定列の上へはみ出す** → `.corner` の `z-index` が `.matrix thead th` に詳細度で負けていた。**`text-align: center` も同じ理由で効いておらず、ロール名も `✓` も左寄せのままだった** |

### 利用者の実機確認による修正（1件）

**グループ見出しがどの列を覆っているか分からない**という指摘を受け、**群の境目に縦罫1本＋見出しの下罫を強める**を入れた（`GuiDesign.md` 5.6.3 に追記）。レイアウトの検証は 14件 → **22件**（縦罫が境目の1本だけであること・本体のマスも同じ位置に持つこと・見出しの下罫が行の罫線より濃いこと）。

### 検証で作った資源

読み取りのみで、サーバの状態を変えていない。結合テストが作ったアカウントは `t.Cleanup` で消えており、残存0件を確認した。`make clean-webui` と `make stop-server` を実行済み。

## 手順16a — タグAPI・スプリントAPI とプロジェクト設定の2タブ（2026-08-23、`feature/step-16a-tags-sprints`）

`Design.md` 11章 Phase 1 の手順16 を着手時に 16a / 16b へ分けた（`Design.md` 11.2.1）。
16a は**タグとスプリントの定義**——16b のバックログがフィルタとグループ化の軸として消費する語彙を先に作る。

### 設計文書の改訂（実装より先に当てた）

| 文書 | 内容 |
|---|---|
| `GuiDesign.md` 5.9 | 本文の「タブは一般・メンバーの2つ」→「4つ」。3.2 と 5.9.1 のワイヤーに食い違っていた。API の表にタグ・スプリントの2行を追加し、「そのタブを開いたときに取得する」を明記 |
| `ApiDesign.md` 9.11 | 並び順（`sort_order, name`）・トリム・大小の区別・ページャと `ETag` を持たない理由・`If-Match` 不要を追記。**9.11.1「並べ替え」を新設** |
| `ApiDesign.md` 9.12 | 並び順（`start_date DESC NULLS LAST, created_at DESC`）・`ticket_count` / `closed_count` の定義・`name` に一意制約が無いこと・トリムを追記 |
| `ApiDesign.md` 9.1.1 | タグ・スプリントの定義変更を `activity` にも `audit_log` にも記録しないことと、その限界（タグ削除を追えない）を追記 |
| `ApiDesign.md` 10.2 | 未解決事項に2件追加（タグ並べ替えの原子性／タグ・スプリントの `activity` 記録） |

### 作ったファイル

| 種別 | ファイル |
|---|---|
| マイグレーション | `server/migrations/0013_tag.sql`（`tag` / `ticket_tag`）、`0014_dod.sql`（`dod_item`。**使うのは手順18**） |
| クエリ | `server/internal/store/queries/tag.sql`、`sprint.sql` → `make sqlc` で `gen/tag.sql.go`・`gen/sprint.sql.go` |
| ハンドラ | `server/internal/httpapi/v1/tags.go`、`sprints.go` |
| 共通 | `projects_get.go` に `projectScopeContext`（`project_id` まで解決する子資源用の取り出し）、`apitime.go` に `Date` / `apiDate` / `parseAPIDate` |
| ルート | `routes.go` に8本（一覧は `ticket.view`、変更は `project.edit`） |
| テスト | `tags_test.go`（20）、`sprints_test.go`（18）、`routes_test.go` に認可4件、`tags_sprints_integration_test.go`（実DB） |
| デモデータ | `deploy/dev/seed/dev-data.yaml` に `tags:` 4件・`sprints:` 4件、`cmd/pb/dev_seed.go` に `seedTags` / `seedSprints` / `devDate` と検証 |
| API定義 | `docs/openapi.yaml` に4パス・2パラメータ・4レスポンス・8スキーマ → `make gen-api` |
| クライアント | `client/src/api/tags.ts`（`reorderTags` を含む）、`sprints.ts`、`components/SprintModal.vue`、`lib/datetime.ts` に `formatPlainDate`、`pages/ProjectSettingsPage.vue` にタグ・スプリントの2タブ |

### 検証結果

| 種別 | 結果 |
|---|---|
| `make test` | 全 PASS。**`openapi.yaml` のドリフト検出が新設8本すべてを検出**し、追記して解消した |
| 単体 | タグ20件・スプリント18件（検証・応答の形・404 / 409・`null` と据え置きの区別・境界値30/50文字） |
| ルート認可 | 4件（`ticket.view` だけのメンバーは一覧のみ／`project.edit` を持つ管理者は変更可／非メンバーは 404／CSRF 無しは 403） |
| 実DB結合（`make test-db`） | **全74件 PASS**。うち `TestTagsSprintsIntegration` が11項目——並び順・一意制約の 409・大小の区別・`sort_order` の既定・`ticket_count`・改名・タグ削除で `ticket_tag` が CASCADE してもチケットは残ること・スプリント削除で `sprint_id` が SET NULL になること・他プロジェクトの ID が 404 になり**かつ実際に消えていない**こと |
| 実サーバ（curl） | 8件（重複 409／空名 422／CSRF 無し 403／存在しない ID 404／閲覧者は一覧 200・作成 403／スプリント作成も 403／日付逆転 422） |
| ブラウザ（CDP） | **68件 PASS**——タグ27（一覧が API の並びと一致・追加・重複の 409・改名・削除確認の件数・1440/900px の実測）、スプリント31（3状態の出し分け・`—` 表示・進捗 `完了/総数`・モーダルの5項目・日付逆転をその場で止める・追加/編集/削除・実測）、並べ替え10（`⠿` のドラッグ→`sort_order` が10刻みで振り直る→**元の並びへ復元**） |
| 目視 | 1440px と 900px のスクリーンショットを確認。**自動検証が全 PASS のまま2件の欠陥を見つけた**（下記） |
| 冪等性 | `make dev-seed` を2回実行し、2回目は「タグ 作成0 / スキップ4」「スプリント 作成0 / スキップ4」 |

### 自動検証を通り抜けた欠陥2件

| 欠陥 | なぜ実測で拾えないか |
|---|---|
| **表の行の区切り線が操作列の手前で切れる**（`th` / `td` に `display: flex` を掛けたため表のレイアウトから外れた） | `getBoundingClientRect()` は妥当な箱を返す。**スクリーンショットを見て気づいた** |
| **`date` 列を `formatDate` に通すと UTC より西の地域で前日へずれる** | 検証端末が Asia/Tokyo なので再現しない。`TZ=America/New_York node -e ...` で実測して確認した |

### あとしまつ

検証で作ったタグ・スプリントはすべて削除し、並べ替えは元の `sort_order` へ復元した。
実DB結合テストが作るプロジェクトとユーザーは `t.Cleanup` で消える（DB を数えて0件を確認）。
サーバ停止・`make clean-webui`・Cookie jar の削除まで実施。

---

## 手順外の作業（完了分）

### `TestExpiresAtFormat` の時限式を解消（2026-08-28、`fix/expires-at-test`）

**`make test` が 2026-08-26 以降どのセッションでも赤だった問題を解消した。** 実装は正しく、
テストの書き方だけの問題である。

**原因**——`me_test.go` が `q.tokenRow.ExpiresAt` に `2026-08-25 09:03:12.123456 JST` を
直書きしていた。測りたかったのは `expires_at` の**書式**（`ApiDesign.md` 2.2。非UTCの時刻帯と
端数を持つ値が UTC・秒精度で出ること）だが、**その値は同時に `access_token.expires_at`
そのもの**であり、その日を過ぎると `middleware/auth.go` が 401 を返す。`viewOf` は
ステータスを見ないので、401 の本文を読んで `expires_at = <nil>` に見えていた。

**直し方（利用者の承認、2026-08-28。案A＋案B）**

| | 内容 |
|---|---|
| A | `me_test.go` の期限を**未来から組み立てる**（`time.Now().Add(SessionMaxAge).Truncate(time.Second)` ＋既知の端数 123456µs）。`rec.Code != 200` を先に `Fatalf` して、**401 を書式の誤りに見せない**。正規表現で形も見る |
| B | **`apitime_test.go` を新設**（3件）。`Time.MarshalJSON` を固定の日時で直接測る。ここは認証を経由しないので `2026-08-25T00:03:12Z` を書いてよい |

**案C（認証ミドルウェアに時計を注入する）は採らなかった**——テストの都合で本番コードの形を
変えることになり、A で消える問題に対して割に合わない。

**A の期待値は実装（`apitime.go`）と同じ組み立て**（`.UTC().Format(RFC3339)`）なので、
**両方同時に誤ると気づけない。** 形だけは実装と独立に正規表現で見る
（`iso8601UTCSeconds`。`apitime_test.go` に置いて2ファイルで共有）。

**変えたファイル**

| ファイル | 変更 |
|---|---|
| `server/internal/httpapi/v1/me_test.go` | `TestExpiresAtFormat` を書き直し（固定日 → 未来から組み立て、200 の確認、形の検査）。**なぜ固定日を書いてはいけないか**をコメントに残した |
| `server/internal/httpapi/v1/apitime_test.go` | **新規。** `TestTimeMarshalJSON`（JST の端数つき → `2026-08-25T00:03:12Z`）／`TestTimeMarshalJSONTruncates`（0.9秒を切り上げない）／`TestTimeMarshalJSONZoneIndependent`（UTC / JST / EST で同じ文字列） |

**`apitime.go` にはこれまでテストが1件も無かった**（全エンドポイントの日時がこの型を通る）。

**検証**

| 見たもの | 結果 |
|---|---|
| `make test` | **全パッケージ PASS**（`internal/httpapi/v1` 11.1s） |
| **変異検査**（この検証は実装を壊せば落ちるか） | ①`.UTC()` を外す → 4件中3件が FAIL ②秒精度を外して `.000` を出す → 4件すべて FAIL。**どちらも検出できた**。`apitime.go` は復元し、`git diff` が空であることを確認 |
| 端末の時刻帯への依存 | `TZ=America/New_York` / `TZ=UTC` / `TZ=Pacific/Kiritimati`（UTC+14）でそれぞれ `-count=1` で実行し、いずれも PASS |
| 同種の時限式が他に無いか | 全 `_test.go` の `time.Date(20...)` を洗った。**`users_detail_test.go:123` の `ExpiresAt: 2026-08-25` はセッション一覧の表示データで認証に使われず**、期待値にも現れないので落ちない |

**片付けた資源**：`apitime.go` の退避（スクラッチパッド）以外に作った資源は無い。DB・サーバ・コンテナは使っていない。

### Phase 1 完了にともなう文書整理（2026-08-28、`docs/phase1-consolidation`）

**Phase 1 の完了を受けて、毎セッション読む4文書と設計文書を整理した**（利用者の指示）。
判断の経緯と移した内容は `history/decisions.md`「Phase 1 完了にともなう文書整理」にある。

**利用者に確認した4点と回答**

| 論点 | 回答 |
|---|---|
| Phase 2 手順一覧（20〜26）をどこまで整えるか | **今回は触らない。** 進め方と目標を含めて改めて整理・再構成する（別セッション） |
| `PROGRESS.md` の Phase 1 完了表 | **畳んで `history/steps.md` へ移す**（本書の「Phase 1 完了一覧」） |
| `LEARNINGS.md` の昇格候補と退役 | **昇格と退役を両方行う** |
| 設計文書に散在する完了した手順番号の写し | **落として `PROGRESS.md` / `history/` へ寄せる** |

**分量の変化**

| 対象 | 前 | 後 |
|---|---|---|
| **毎セッション読む4文書の合計** | 127.2KB（約42,000トークン） | **90.4KB（約30,000トークン）／−28.9%** |
| `docs/PROGRESS.md` | 44.9KB | 25.5KB |
| `LEARNINGS.md`（追跡対象外） | 48.3KB（66件） | 30.3KB（32件） |
| `.claude/commands/pb-step.md` | 23.3KB | 24.1KB（教訓7件を昇格したぶん増えた） |
| `CLAUDE.md` | 10.7KB | 10.5KB |
| 設計文書7本の合計 | 731.1KB | 720.0KB |

**変えたファイル**

| ファイル | 変更 |
|---|---|
| `.claude/commands/pb-step.md` | Phase 1 前提を撤去（`Design.md` 11章の手順 $1 を実装する形へ）。根拠の物語を圧縮し、`LEARNINGS.md` から7件を昇格。`pb-workflow-version` を 3 へ |
| `.claude/commands/pb-review.md` | 観点表に「画面」「実装規約」の2行を追加。範囲の特定を `docs/README.md`「領域と正本の対応」へ向けた |
| `docs/PROGRESS.md` | Phase 1 表を本書へ移し「現況」へ置き換え。手順外の作業13件を12件へ圧縮（1件は本表に無関係な重複）。引き継ぎ18行 → 12行（`実機で触るとき` 4行と `検証できなかったこと` 2行を統合、消化済み2行を削除、Phase 2 と権限の2行を新設） |
| `docs/Design.md` | 11章から手順17〜19 の分割理由・11.1.1・11.4 を history へ移動。Phase 1 一覧に完了を明記。11.1〜11.3 の rev.6/rev.7 の経緯を圧縮。2.2 と付録A を現況へ |
| `docs/ApiDesign.md` | 9.15 を削除（history へ）。1.1 を「今回定義する範囲」から「本書が定義する範囲」へ書き直し。完了した手順番号の写しを8か所撤去 |
| `docs/GuiDesign.md` | 3.2 の「Phase 1 での実装」列を「状態」へ。完了した手順番号の写しを36か所撤去 |
| `docs/DbDesign.md` | マイグレーション一覧から手順番号を撤去。0014 の「まだ使われない」を現況へ |
| `docs/Development.md` | `make up` の注記から古い手順番号を撤去。**「実際に踏んだ」の根拠としての手順番号は残した**（消すと注意書きの理由が消えるため） |
| `docs/README.md` | 状態列を「Phase 1 実装完了」へ。開発フェーズ表に状態列を追加 |
| `CLAUDE.md` | Phase 1 完了と Phase 2 の入口を明記。ブランチ運用を `Design.md` 11.0〜11.1 への参照＋表に圧縮。振り返り2種類の表を `pb-step.md` への1行参照へ |
| `LEARNINGS.md` | 7件を昇格・13件を退役（追跡対象外。記録は `history/decisions.md`） |

**検証**

| 見たもの | 結果 |
|---|---|
| 移した内容が移し先に在るか | `history/decisions.md` / `steps.md` を grep して5か所すべて確認 |
| 移し元から消えているか | `Design.md` 11.4 / `ApiDesign.md` 9.15 / `PROGRESS.md` の旧記述がいずれも 0 件 |
| 章番号の相互参照 | 全 `.md` の `` `Xxx.md` N.N `` 参照を実在する見出しと機械照合。**解決しない参照 0 件** |
| 完了した手順番号の残存 | 設計文書側は Phase 2 の前方参照（`手順21・22`）と `Development.md` の根拠だけになった |
| `make test` | `TestExpiresAtFormat` のみ FAIL。**`git worktree add <tmp> develop` で `develop` でも落ちることを確認**（起票済みの時限式。本ブランチは Markdown しか触っていない） |

**片付けた資源**：切り分け用の worktree を `git worktree remove --force` で撤去し、`git worktree list` が本体1件だけになることを確認した。

### 設計文書のダイエットと最新化（2026-08-27、`docs/context-diet`）

**毎セッションの固定費が、実際に必要な仕様の3.2倍になっていた。** 着手時に必ず読む4文書
（`CLAUDE.md` 5,621字 / `LEARNINGS.md` 24,133字 / `docs/PROGRESS.md` 32,282字 /
`pb-step.md` 8,250字 = 70,286字）に対し、手順17c に要る章は 22,172字。

**原因は「行き先の無い知識」だった。** `PROGRESS.md` の「次の手順への引き継ぎ」75行 36.9KB のうち、
**手順番号にひもづく約束は16行だけ**で、残りは「画面を作る手順では…」のように
**作業の種類にひもづく実装規約30行（16.0KB）**と、**Phase 2 以降・時期未定26行（9.7KB）**だった。
前者は消化の概念が無いので永久に残り、後者は Phase 1 の間ずっと効かないのに毎回読まれる。
設計文書は「何を作るか」、`LEARNINGS.md` は「進め方」なので、どちらにも属さない知識が
毎回読む文書に沈殿していた。

| 対象 | 変更 |
|---|---|
| `docs/PROGRESS.md` | 61.4KB → 35.0KB。引き継ぎ表 75行 → 23行。手順の分割の経緯35行を `history/decisions.md` へ。**手順外の作業の表が列数不整合（ヘッダ5列・各行4セル）で最終列が描画されていなかったのを修正** |
| `LEARNINGS.md` | 54.2KB → 42.3KB。退役12件、`GuiDesign.md` へ昇格3件、セッション別の経過記録3.8KB を「昇格待ち」表へ集約、退役表を本書の姉妹文書（`decisions.md`）へ移動、#69 の未エスケープな `\|` を修正 |
| `docs/GuiDesign.md` | **6.6 クラス名の衝突を避ける / 6.7 レイアウトの落とし穴 / 6.8 ドラッグ＆ドロップ / 7.4 `<script setup>` の書き方 / 7.5 日時と日付の出し方**を新設。6.1・6.4・7.1・7.2・11章へ追記 |
| `docs/Design.md` | 6.4.4 に `RequirePermissionUnlessQuery`、付録A に6件 |
| `docs/ApiDesign.md` | 10.2 に7件。目次の「9章は未実装」を現況へ |
| `docs/DbDesign.md` | 10章に2件 |
| `docs/Development.md` | 4章（seed に書ける列）・**8.5 検証そのものが誤りやすいところ**（新設）・10.3（一覧クエリの3点） |
| `docs/README.md` | 「現在の着手ポイント」が手順1〜3 のままだったので `PROGRESS.md` へ一本化。状態列2箇所を現況へ |

**移動元の行のうち11件は、移し先の設計文書に既に同じ記述があった**（`ApiDesign.md` 9.4.1 の
`staged_at`、10.2 のタグ原子性・`activity` 記録、4.4.2 のトークンスコープ、`Design.md` 6.4.1 /
6.5 のスコープ語彙、`GuiDesign.md` 11章 の通知・OKLCH フォールバック）。**写しを持っていた**ため、
移さず削除した。

**固定費は 70,286 → 51,115字（27%減）。** `LEARNINGS.md` は目安の40KB に 2.3KB 届いていない——
超過の実体は**昇格待ち12件の滞留**で、うち9件は `pb-step.md` への追記提案が出たまま
利用者の判断を待っている。今回 `pb-step.md`・`CLAUDE.md`・`Makefile` は対象外としたため
動かせず、Phase 1 完了時へ回した。

**検証**：`make test` は `TestExpiresAtFormat` の1件のみ失敗（時限式。2026-08-25 を過ぎると
落ちる既知の欠陥で `develop` でも同じ。`fix/expires-at-test` として起票済み）。
節番号の重複なし、変更した表の列数はすべて整合。


### バックログのドロップ先の挿入線と「表示上の親」での並べ替え（2026-08-24、`feature/backlog-drop-indicator`）

**手順16d-b の実機確認で利用者から出た2件。** マージ後に別ブランチで当てた。

**①バックログ表で順序が入れ替えられなかった**（利用者の指摘）。`GuiDesign.md` 5.4 の
「ドロップ先は同じ**親**を持つ行に限る」を実装していたが、**この規則の前提は
16d でエピックを行から外した時点で崩れていた**——エピック配下のチケットは
`parent_seq` を持ったまま**根として並ぶ**ので、画面上は同じ深さに見える行が
内部で群に割れる（seed では8つの根が4群に分かれ、大半の組み合わせが禁止だった）。
規則の理由づけ（「別の親の下へ落としても表示は動かない」）も根には当てはまらない。
**「同じ**表示上の親**を持つ行」へ改めた**（`GuiDesign.md` 5.4「並べ替えられる相手」を新設）。

**②ドラッグ中に落ちる位置が読めなかった**（利用者の要望）。**2px の挿入線**を出す
（`GuiDesign.md` 5.4「ドロップ先の見せ方」を新設）。**落ちる位置はポインタが指した
半分で決まる**（利用者の判断＝②案。「掴んだ行が相手を越えた向き」で決める案は、
行の下半分を指しても上に入ることがあり、線を出した意味がなくなる）。

| ファイル | 内容 |
|---|---|
| `docs/GuiDesign.md` | 5.4 の「並べ替え」行、**新設「並べ替えられる相手」**（表示上の親の定義と旧規則が崩れた理由）、**新設「ドロップ先の見せ方」**（落とし先と結果の対応表・線を `box-shadow` で描く理由・行を面で塗らない理由・`--pb-accent` が 8.6 に当たらない理由・見出しが「先頭へ」である理由） |
| `client/src/pages/BacklogPage.vue` | `Row.parentKey`（表示上の親）、`rowIndex`、`canDropOn` / `canDropOnSection` の判定を `parentKey` へ、`dropHint` と `sideOf()`、`dropOnRow` をポインタ基準へ（抜いてから数え直す・位置も段も変わらないなら送らない）、**段の見出し＝先頭／末尾の帯＝末尾**へ変更、挿入線の CSS |

**検証：ブラウザ32件 PASS / 0 FAIL**（1440px）。根どうしの入れ替え・インデント行は
兄弟だけ・孫は親違いへ落とせない・上下半分と着地の一致・落とせない相手では線を出さない・
線が出ても行の高さが変わらない・見出し＝先頭／帯＝末尾・同じ位置へ落としても送らない・
グループ化中も同じ見え方。`make test` 全パッケージ PASS。
**スクリーンショット6枚**（明暗の両テーマ × 1440/900px。利用者の設定がサーバ側で
dark + green になっていたため、撮る側で `data-theme` を差し替えて明るいほうも見た）。

**検証で見つけた実装の穴が1件**——`段の末尾へ` の帯が、**インデントされた行を掴んでいるときも出ていた**。
その行は段の中の位置を持たない（`position: "last"` を送っても親の下で兄弟の末尾へ動くだけ）ので、
**表示上の根を掴んでいるときだけ受け取る**ようにし、帯もそのときだけ出すようにした。

**あとしまつ**：`sort_key` / `staged_at` / `version` を控えから復元し `diff` で差分ゼロを確認。
**利用者が実機確認で作った `demo-17`「孫タスク」は消していない**——3段のツリーとして検証に使った。

#### 追補：バックログの行が一度も掴めなかった（2026-08-24、同ブランチ）

**利用者の実機確認で判明**——「バックログでの並べ替えが入っていない」「オンステージへ
上げる方法がない」。上の32件は全 PASS だったが、**それは合成した `DragEvent` を
dispatch していただけで、Chrome のドラッグ機構を一度も通していなかった**。

**原因。** `dragstart` を機に「〜の末尾へ」の帯（`.stage-tail`）を差し込んでいた。
**オンステージ側の帯がバックログ表の上に入るため、掴んだ行が直後に下へずれ、
Chrome がドラッグを取り消していた**（`dragstart` の 1〜2ms 後に `dragend`、
`dragover` は一度も起きない）。オンステージの行は帯が自分より下に入るのでずれず、
そちらだけ動いていた——**利用者の報告の「オンステージは動く／バックログは動かない」
がそのまま原因の切り分けになっていた。**

**切り分け。** `Input.setInterceptDrags` で本物のドラッグを回し、
**帯が1本も出ない行（インデントされた行＝段に置けない）だけドラッグが成立する**ことを
確かめて確定させた（根＝取り消し、インデント行＝成立）。

**直し方。** **末尾の帯を撤去した。** 段の末尾へ置きたいときは最終行の下半分へ落とす。
落とし場所は**掴む前から画面にあるもの**（行・段の見出し・空の段の枠）に限る。

| ファイル | 内容 |
|---|---|
| `docs/GuiDesign.md` | 5.4「ドロップ先の見せ方」の表から「段の末尾の帯」を外し「空の段の枠」へ、**新設「掴んだ瞬間に要素を差し込まない」** |
| `docs/Development.md` | 8.2 の D&D の記述を**全面的に書き直した**（合成 `DragEvent` では測れない／`Input` ドメインの使い方／`dragstart` での DOM 差し込み） |
| `client/src/pages/BacklogPage.vue` | `.stage-tail` の撤去、`draggingSeq` に制約のコメント |

**検証：本物のドラッグ17件 PASS / 0 FAIL**（`Input.setInterceptDrags` +
`Input.dispatchMouseEvent` / `Input.dispatchDragEvent`）。バックログの行を掴める・
並べ替えが効く・段へ上げる・段から戻す・見出しは先頭・落とせない相手では線が出ない。
900px でも掴めることと、**掴んでも画面が動かない**ことをスクリーンショットで確認。

### `ApiDesign.md` 9章（チケットAPI）の確定とチケット領域の情報設計（2026-08-22、`docs/ticket-api`）

`PROGRESS.md` から移した（2026-08-23、手順16a の掃除。結果が確定してもう変わらないため）。

①9章を13行から約600行へ書き起こし ②グルーピングを「階層＋フラットなタグ」に決めて
`tag` / `ticket_tag` を追加（`DbDesign.md` 6.10） ③メインメニューをチケットの「視点」
（バックログ／カンバン／ガント／検索／分析）の並びに組み替え、`/p/:key/backlog` を新設
④バックログをページングしない設計にして `GuiDesign.md` 11章の未解決事項1件を解消
⑤**文書間の食い違い2件を解決**（DoD は Phase 1 へ前倒し、スプリントCRUD を Phase 1 に追加）
⑥旧手順16 を 16〜19 に展開し Phase 2/3 を繰り下げ（`Design.md` 11.4 rev.8）。

**コードとマイグレーションは書いていない**（絶対規則3）。検証は相互参照の grep・
旧手順番号の洗い出し・`make test`・`/p/<key>/backlog` が 404 のまま（＝スコープが文書に閉じている）。

### 設計文書の棚卸し（2026-08-23、`docs/ticket-api`）

`PROGRESS.md` から移した（2026-08-23、手順16a の掃除）。

進行によって不要になった記述の整理。①4設計文書の**ヘッダの「最終更新」行を撤去**
（4文書とも `2026-08-11` で止まり、手で書いた日付は腐ると実証されたため。正確な最終更新は
git が持つ）②「状態」行を現況へ更新 ③役目を終えた4件を削除（`DbDesign.md` 2.2 解除される制約／
`Design.md` 11.4 rev.5対応表／`ApiDesign.md` 10.1 実装順序＝`Design.md` 11章との重複／
`Requirements.md` 9章の決着済み3項目）④`ApiDesign.md` 10.2 の未解決2件を決着済みとして削除。

検証は相互参照の再検証＋`make test`。

### `make build` が作業ツリーを汚す問題（2026-08-22、`fix/webui-dist-placeholder`）

**症状**：`make build` / `make restart` のたびに `server/internal/webui/dist/index.html` が
変更済みになり、コミット前に `make clean-webui` を実行しないと差分に紛れ込む。

**原因**：`//go:embed all:dist` はコンパイル時に最低1ファイルの存在を要求するため、
プレースホルダを1つコミットしてある。**その名前が `index.html` で、実ビルドの出力と
同じだった**ため、毎回上書きされていた。

**あわせて見つかった問題**：コミット済みのプレースホルダは step 10a で紛れ込んだ
古いビルド成果物で、`/assets/index-B0C3Ppn1.js` という**追跡されていないファイル**を
指していた。未ビルドで起動すると SPA フォールバックが `/assets/*` にも HTML を返し、
`<script type="module">` が HTML を受け取って**画面が真っ白**になっていた
（`PROGRESS.md` に引き継ぎ済みの項目）。

**直したこと**

| 対象 | 内容 |
|---|---|
| `dist/placeholder.html` | 新規・追跡対象。**自己完結**の「画面がまだビルドされていません」ページ（外部参照0件）。`make build` / `make restart` / `make dev-client` を案内する |
| `dist/index.html` | 追跡から外した（`git rm --cached`）。`.gitignore` の例外を `placeholder.html` に変更 |
| `Makefile` `sync-webui` | `rm -rf dist` をやめ、`find dist -mindepth 1 ! -name placeholder.html -delete` に。追跡対象を消さず、古い成果物は一掃する |
| `Makefile` `clean-webui` | `git restore index.html` を外した。**コミット前の必須手順ではなくなった**（ディスクを空ける用途のみ） |
| `webui/handler.go` | `index.html` が無ければ `placeholder.html` へ倒し、**`503 Service Unavailable`** で返す。`200` にすると監視や自動確認から「画面が出ている」と区別が付かない。両方無ければ従来どおり 500 |
| `webui/handler_test.go` | 9件（プレースホルダの 503・`index.html` の優先・**コミット済みプレースホルダが自己完結であること**を含む） |
| `httpapi/router_test.go` | SPA フォールバックの試験が 200 固定だった。**見たいのは経路であって client のビルド状態ではない**ので 200 か 503 を許す |
| `Design.md` 3.4 / `Development.md` 7.1・9章 | 記述を更新 |

**検証**

- `make build` の前後で `git status` に新しい差分が出ないこと（実測）
- `git check-ignore`：`dist/index.html` と `dist/assets/` は無視、`placeholder.html` は追跡
- 未ビルドで `make run`：`/`・`/admin/users`・`/assets/*` が **503 + `text/html`**、`/healthcheck` は 200、`/api/v1/me` は 401
- ヘッドレス Chrome でページを表示し、**外部参照0件**と文面を目視で確認
- `make test` を**未ビルド・ビルド済みの両方**で実行して全パッケージ通過


`docs/PROGRESS.md` の「手順外の作業」表から、**完了して今後の手順に不要になった行**を
移したもの（2026-08-18、`pb-step.md` 手順7 の掃除）。未着手の行は `PROGRESS.md` に残っている。

| 内容 | 完了日 | ブランチ | 検証 |
|---|---|---|---|
| **12c：ユーザー一覧のソートと検索を広げる**（ロール・状態でのソート、検索語がロール名に当たること）。12b の実機確認から出た要望で、`Design.md` 11章の手順ではないため手順外として起票した | 2026-08-19 | `feature/step-12-users-sort-search` | 実DBの結合テスト（行の前後関係とロール名検索）＋ブラウザ24件。詳細は上の「手順12c」 |

| 内容 | 状態 | 完了日 | ブランチ | 検証方法 |
|---|---|---|---|---|
| バージョン番号とビルド番号の運用（`Design.md` 11.1） | 完了 | 2026-08-12 | `feature/versioning` | マージ後に `make version-check` が通る |
| エージェントの実行権限の整理（`.claude/settings.json`）と `CLAUDE.md` 絶対規則2の具体化 | 完了 | 2026-08-12 | `feature/step-04-http-foundation-auth` | 読み取り系コマンドが確認なしで通り、`rm` / `git merge` / `git push` などは確認を求める |
| `GuiDesign.md` 3.2 のルーティング表に `/` の行を追加（手順7の積み残し） | 完了 | 2026-08-13 | `docs/routing-root-path` | 表の記述（`/projects` へリダイレクト・必要権限なし）が `client/src/router/routes.ts` の実装と一致する。コードは変更していない |
| `openapi.yaml` の役割の再定義（設計の写し → **実装済みAPIの現状**）と 3.3 の文言の書き直し | 完了 | 2026-08-13 | `docs/openapi-role` | `ApiDesign.md` 1.3 に食い違い時の行動表がある。`Design.md` 3.3 から「手書き」の語が消え、採らない3方式とその理由が表になっている。`pb-step.md` の実装の節に openapi.yaml の更新が入っている |
| **`docs/Development.md` の作成**（手順7.5 の積み残し。`Design.md` 4.1 が予定し `DbDesign.md` 7.6.6 が参照を約束していた） | 完了 | 2026-08-15 | `docs/development-guide` | 初回セットアップ（秘密3ファイル → `make up` → `migrate` → `dev-reset`）から**実際にログインできる**ところまでを、書かれたとおりに実行して確認した。`docs/README.md` と `CLAUDE.md` から参照が張られている。`PROGRESS.md` の環境メモは手順を移し、記録とポインタだけ残した。**利用者が実際に上から順にたどったフィードバックで4点を追補**（前提の確認コマンドと付録A、`make build` が要る場面、サーバの止め方、HMR の説明） |
| `GuiDesign.md` 4.2 / 5.1 の図の追従（バージョン表記とテーマの選択肢） | 完了 | 2026-08-15 | `docs/development-guide` | 図から `v0.1.0` が消え、`PB v<version>` になっている。実装が出すのは `VERSION` の値（現在 `1.9.12`） |
| **進め方の振り返りと `LEARNINGS.md` の導入**（ループエンジニアリング。利用者からの指摘） | 完了 | 2026-08-18 | `docs/session-learnings` | `pb-step.md` の振り返りを**成果物（手順7）と進め方（手順8）の2種類**にし、番号を 9=記録 / 10=マージ提案へ繰り下げた。`LEARNINGS.md`（44行・5.2KB）を新設し、**セッション開始時に読む**導線を `CLAUDE.md` と手順1に張った。**個人のセッション履歴から抽出した内容を含むため `.gitignore` の対象**とし、履歴にも残していない（無ければ各自が作り直す）。初期値は**過去16セッションの JSONL から利用者の発話119件を抽出・分析**して得た9件。**2回ルール**（同じことが2回起きてから規約へ昇格）で増え続けないようにした |
| **セッションの振り返りフローの導入**（マージ提案の直前に行う。利用者からの指摘） | 完了 | 2026-08-18 | `docs/progress-archive` | `pb-step.md` に手順7を新設（11項目・該当なしも述べる）。番号を繰り下げ、`Design.md` 11.0 の参照（手順8→9）も直した。**直前の手順10bのセッションに当てて空振りしないことを確認**：`Modal.vue` の再利用前提・`PROGRESS.md` の肥大化・ビルド成果物の汚れ・検証資源の後始末を拾えた |
| **`PROGRESS.md` の分割**（毎セッションの読み込み量の削減。利用者からの指摘） | 完了 | 2026-08-18 | `docs/progress-archive` | 182KB → 27KB（15%）。**1行も削除せず `docs/history/` へ移動**した（機械的に切り出し、行の欠落が無いことを sort ＋ diff で確認）。未消化の約束は「次の手順への引き継ぎ」に20行で抜き出した。`pb-step.md` に記録の書き分け表を入れ、再肥大化を防いだ |
| **`PROGRESS.md` の環境メモの整理**（30KB 超過。`pb-step.md` 手順7の閾値による） | 完了 | 2026-08-18 | `feature/step-11-project-detail-api` | 243行/31.9KB → **192行/25.1KB**。**1件も捨てず**、テストの落とし穴を `Development.md` 6.2、依存とツールの固定版を同 10章（新設）、initdb の実行ビットを同 2.2 へ写し、役目を終えた回避策（Argon2id での検証用ユーザー作成）を `history/steps.md` へ移した。環境メモに残したのは**手順書に落とせない制約4件**のみ。移動漏れが無いことは旧ブロックの全項目を `Development.md` に対して grep して確認した |
| **`make restart` / `make stop-server` の追加**（利用者からの要望。画面を直したあと1コマンドで確かめる） | 完了 | 2026-08-18 | `feature/step-11-project-settings-page` | 実行して `stop-server → down → build → up → run` が順に走り、新しいバイナリで `/healthcheck` が応答する。`stop-server` は待受（`-sTCP:LISTEN`）だけを止める |
| **プロジェクト一覧で長い説明が表を横に伸ばす不具合**（利用者からの指摘） | 完了 | 2026-08-18 | `feature/step-11-project-settings-page` | 表 4046px → ペイン幅に収まり、説明が省略記号で切れる。`GuiDesign.md` 5.2 に「横幅を伸ばさず縦スクロールのみ」を明記 |
| ダッシュボードのページヘッダを「プロジェクト名 ＋ 画面名」にする（`GuiDesign.md` 5.3。利用者からの指摘） | 完了 | 2026-08-15 | `feature/step-10-projects-page` | ブラウザで5件：メンバーは `デモプロジェクト ダッシュボード`、demo に未所属の管理者は `demo ダッシュボード`（キーで代替）、チケット一覧は `チケット一覧` のまま、プレースホルダのカード内は `プロジェクトダッシュボードのページ予定` のまま（6.5 の規約を保つ） |


### `make test-db`（2026-08-22、`feature/step-15b-me-tokens` に同梱）

**結合テストを走らせる make ターゲットが無かった。** `Development.md` 6.1 は
`PB_TEST_DATABASE_URL` に接続文字列を手で書く手順を載せていたが、
`deploy/dev/secrets/app_db_password` は `.claude/settings.json` の `deny` により
**エージェントからは読めない**。そのため 15a の実DB結合テストは一度も走らなかった。

`Makefile` に既にあった `PB_DATABASE_URL_APP`（`make run` と同じ組み立て）を使う
`test-db` ターゲットを足し、`Development.md` 6.1 をそれに差し替えた。`RUN=` で
個別のテストへ絞れる。秘密は recipe の中でだけ読まれ、argv にも Makefile にも残らない。

**手順15b に同梱したのは、15b 自身の検証がこれに依存したためである**（引き継ぎ
「次に着手する人が最初に」の解消と一体だった）。

## ビルド番号とマージの対応（1〜20）

`docs/PROGRESS.md` から移した（2026-08-18）。**正本は `make version` / `make version-check`**
（`git rev-list --count --first-parent --merges develop` の実測）であり、この表は経緯の記録にすぎない。
実際に記入漏れ（ビルド16・17）が起きたため、`PROGRESS.md` 側では一覧を持たないことにした。

| | |
|---|---|
| ビルド1 | 手順2（`feature/step-02-migrations`）のマージ |
| ビルド2 | バージョン運用の導入（`feature/versioning`）のマージ |
| ビルド3 | 手順3（`feature/step-03-admin-create`）のマージ |
| ビルド4 | 手順4a・4b（`feature/step-04a-http-foundation`）のマージ |
| ビルド5 | 手順5a・5b（`feature/step-05-auth-session`）のマージ |
| ビルド6 | 手順6a・6b（`feature/step-06-authz-middleware`）のマージ |
| ビルド7 | 手順7（`feature/step-07-client-scaffold`）のマージ |
| ビルド8 | `GuiDesign.md` 3.2 への `/` の行の追加（`docs/routing-root-path`）のマージ |
| ビルド9 | `openapi.yaml` の役割の再定義（`docs/openapi-role`）のマージ |
| ビルド10 | 手順7.5（`feature/step-07-5-dev-seed`）のマージ |
| ビルド11 | 手順8a・8b（`feature/step-08-login-and-menu`）のマージ |
| ビルド12 | `docs/Development.md` の作成（`docs/development-guide`）のマージ |
| ビルド13 | 手順9a・9b（`feature/step-09-projects-api`）のマージ |
| ビルド14 | 手順10a（`feature/step-10-projects-page`）のマージ |
| ビルド15 | 手順10b（`feature/step-10-projects-page`。同じブランチの2回目のマージ）|
| ビルド16 | `PROGRESS.md` の分割（`docs/progress-archive`）のマージ |
| ビルド17 | 進め方の振り返りと `LEARNINGS.md` の導入（`docs/session-learnings`）のマージ |
| ビルド18 | 手順11a（`feature/step-11-project-detail-api`）のマージ |
| ビルド19 | 手順11b（`feature/step-11-project-settings-page`）のマージ |
| ビルド20 | 手順12a（`feature/step-12-users-api`）のマージ |

**手順1以前の2コミットはマージではないため数えない。** 手順1は `Design.md` 11.0 の規約が
固まる前に `develop` へ直接コミットされており、feature ブランチを経ていない。

## 手順15a — `PATCH /me`・`POST /me/password` と自分の設定画面（2026-08-22）

ブランチ `feature/step-15-me-settings`。**手順15 を機能で 15a / 15b に分けた**うちの前半
（`Design.md` 11.2.1）。15b はアクセストークン（`GET|POST|DELETE /me/tokens` と `/me/tokens` 画面）。

### 設計文書の改訂（実装より先に全部当てた）

| 文書 | 改訂 |
|---|---|
| `ApiDesign.md` 3.1 / 4.1 | 応答例の `actor` に `theme` / `hue` を追加 |
| `ApiDesign.md` 4.2 | **`email` を変更可**に改訂（`system_role` のみ不可）。応答は 4.1 と同一構造・楽観ロックなし・重複は 409。ログインIDと画面の2行の関係を明記 |
| `ApiDesign.md` 4.3 | `must_change` を false にする／`local_credential` 無しは 409／`failed_attempts` を増やさない、を追記 |
| `ApiDesign.md` 4.4 | **`GET\|DELETE /me/sessions` を削除。** 旧 4.5（トークン）を 4.4 へ繰り上げ |
| `ApiDesign.md` 6.7 | 「本人が 4.4 で切れる」という根拠を差し替え |
| `ApiDesign.md` 8章 / 10.1 | 対応表・実装順序から `/me/sessions` を削除 |
| `Design.md` 7.2 | 定義済みの範囲から `/me/sessions` を削除 |
| `Design.md` 11章 | 手順15 の完了条件にトークン発行を追加 |
| `GuiDesign.md` 5.8 | **全面改訂。** タブ `[一般][アクセストークン]`、3セクション（基本情報／デザイン／セキュリティ）、ログインID行、保存の単位、セッション一覧を置かない理由、要パスワード変更の誘導 |
| `GuiDesign.md` 5.6.2 | 「本人が 5.8 で切れる」の1文を差し替え |
| `GuiDesign.md` 818行 | `ApiDesign.md 4.5` → `4.4` |

### 作ったファイル

| ファイル | 中身 |
|---|---|
| `server/internal/store/queries/me.sql` | 新規。`UpdateMyProfile` / `UpdateMyDisplayName` / `FindMyLocalCredential` / `ChangeMyPassword` / `RevokeMyOtherSessions` |
| `server/internal/httpapi/v1/me_update.go` | 新規。`PATCH /me`（検証・監査・`subject` の追随・409 への写し） |
| `server/internal/httpapi/v1/me_password.go` | 新規。`POST /me/password`（現在値の検証・`must_change` のクリア・他セッションの失効） |
| `server/internal/httpapi/v1/me_settings_test.go` | 新規。単体19件 |
| `server/internal/httpapi/v1/me_settings_integration_test.go` | 新規。実DB結合12件 |
| `server/internal/store/gen/me.sql.go` | `make sqlc` の生成物 |
| `client/src/api/me.ts` | 新規。`updateMe` / `changePassword` |
| `client/src/pages/MySettingsPage.vue` | 新規。3セクションの画面 |

### 変更したファイル

| ファイル | 変更 |
|---|---|
| `server/internal/store/queries/auth.sql` | `GetActorProfile` と `FindLocalLoginByEmail` に `theme` / `hue` |
| `server/internal/httpapi/v1/me.go` | `actorView` / `profile` に `theme` / `hue`。**`buildSessionView` が `gen.Querier` を引数で受けるようにした**（`PATCH /me` がトランザクションの中で応答を組み立てるため） |
| `server/internal/httpapi/v1/login.go` | `profile` の詰め替えに `theme` / `hue` |
| `server/internal/httpapi/v1/routes.go` | `PATCH /me` と `POST /me/password`。**4章に権限キーを要求しない理由**をコメントで明示 |
| `client/src/api/client.ts` | **401 を `error.code` で分けた**（`unauthenticated` だけを失効として扱う） |
| `client/src/stores/auth.ts` | `setSession` を公開し、そこから `ui` ストアへ `theme` / `hue` を流す。`mustChangePassword` を追加 |
| `client/src/stores/ui.ts` | `syncFromServer` を追加 |
| `client/src/router/guards.ts` | 要パスワード変更なら `/me` から出さない判定を追加（判定2） |
| `client/src/router/routes.ts` | `/me` を実画面へ差し替え |
| `docs/openapi.yaml` | `PATCH /me` / `POST /me/password` / `UpdateMeRequest` / `ChangePasswordRequest`、`Actor` に `theme` / `hue` |

### 検証結果

**単体 19件**（`go test ./internal/httpapi/v1/ -run "TestPatchMe|TestChangeMyPassword"`）

応答が 4.1 と同一構造／送った項目だけが narg に載る／メール変更で `subject` が追随する・
送らなければ触らない／`system_role` は 422 で DB を触らない／値域（theme・hue・locale・
timezone・`Local` を弾く・`UTC` は通す・表示名・メール形式）／すべての項目を見てから返す／
409 への写し／行が消えたら 401／監査は変更した項目だけ／空の PATCH は監査を書かない／
平文を保存しない・監査に残さない／現在のセッションだけ残す／誤ったパスワードで
`failed_attempts` を増やさない・何も書き換えない／`local_credential` 無しは 409／
監査は `password.change` 1件のみ／書き込みの順序（変更 → 失効 → 監査）

**実DB結合 12件（書いたが、まだ一度も実行していない）**

`-run TestMeSettingsIntegration`。`PB_TEST_DATABASE_URL` に接続文字列が要り、
`deploy/dev/secrets/app_db_password` は `deny` によりエージェントから読めないため、
**このセッションでは走らせられなかった**（`SKIP` のまま）。`go vet` は通っている。
**次にこの手順へ触る人が最初に実行すること。** 内容は以下を意図している。

プロフィールと見た目の更新が `GET /me` でも読める／**メール変更後に新しいメールでログインでき、
古いメールでは入れない**／他人のメールは 409／`version` が加算される／`If-Match` を要求しない／
**パスワード変更で他のセッションだけが 401 になり、操作したセッションは残る**／新パスワードで
入れて旧パスワードでは入れない／誤ったパスワードでは何も変わらない／**5回失敗してもロック
されない**／`must_change_password` が下りる／未認証は 401／CSRF 無しは 403／オペレータでも通る

**ブラウザ 59件**（ヘッドレス Chrome + CDP。`Development.md` 8.2）

- レイアウト 32件：タブ2つ・3セクション・基本情報の5項目の並び・ログインIDが入力欄でない・
  テーマ3／色相2のラジオ・パスワード欄2つが `type=password`・**1440px と 900px の両方**で
  各欄の幅と位置を実測・横スクロールしない・狭い窓で言語とタイムゾーンが潰れない
- 操作 19件：保存ボタンの活性・保存結果がそのセクションに出る・メニューの表示名が追随する・
  読み直しても残る・422 が欄に紐づく・409 のメッセージ・**別のブラウザで同じテーマと色相になる**・
  12文字未満は 422・**誤ったパスワードで 401 が出てもログイン画面へ飛ばされない**
- 要パスワード変更 8件：ログイン直後に `/me` へ着く・`/projects` へ行こうとしても戻される・
  変更すると警告が消えて他画面へ行ける・新パスワードで入り直せる

**スクリーンショット 4枚を目視**（1440px 上下・900px・ダーク）。自動検証が全 PASS の状態で
**ラジオが縦積みになっていた**のを見つけて直した（`.field` の `flex-direction: column` を
`.choices` が受けていた。5.8 のワイヤーは横1行）。

### 後始末

検証で使った使い捨てユーザーは削除（再取得で `not_found` を確認）。デモアカウント
`viewer@example.com` の表示名・タイムゾーン・テーマ・色相は元の値へ戻した。Cookie jar と
ヘッドレス Chrome のプロファイルを削除。`make stop-server` と `make clean-webui` を実行。


## 手順15b — `GET|POST /me/tokens`・`DELETE /me/tokens/:id` とアクセストークン管理画面（2026-08-22）

ブランチ `feature/step-15b-me-tokens`。`ApiDesign.md` 4.4、`GuiDesign.md` 5.8.1。

**設計文書を先に全部当ててからコードへ入った**（15a で効いた型の2回目）。実装中の設計相談はゼロ。

### 設計文書（先に当てた分）

| ファイル | 変更 |
|---|---|
| `docs/ApiDesign.md` | **4.4 を節として書き下ろした**（4.4.1 一覧 / 4.4.2 発行 / 4.4.3 失効 / 4.4.4 監査）。従来は JSON 例2つだけで、パス・必要権限・応答形状・状況別の応答・スコープ語彙・`token_type` の絞り込みのいずれも無かった。8章の対応表を `DELETE /me/tokens/:id` に修正 |
| `docs/GuiDesign.md` | **5.8.1「アクセストークンタブ」を新設**（ワイヤー3枚＝一覧・発行モーダル・1回表示、列の説明、上限に達したときの見せ方、失効の確認） |
| `docs/Design.md` | 6.4.1 に**スコープの語彙は権限カタログのキー**であることを追記。6.5 の既定スコープに「この語彙は権限カタログに対応していない」と注記 |
| `docs/openapi.yaml` | `/api/v1/me/tokens`（GET / POST）と `/api/v1/me/tokens/{id}`（DELETE）、`AccessToken` / `AccessTokenList` / `IssuedAccessToken` / `CreateTokenRequest`、`TokenID` パラメータ |
| `docs/Development.md` | 6.1 を `make test-db` に差し替え。6.2 の落とし穴表に「ログインは IP あたり10回/分」を追加 |

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/httpapi/v1/me_tokens.go` | 3ハンドラ、`accessTokenView` / `issuedAccessTokenView`、値域の検証、スコープのカタログ照合、監査 |
| `server/internal/httpapi/v1/me_tokens_test.go` | 単体18件（一覧・平文の非露出・上限の境界・422 の6通り・スコープ語彙・監査・冪等・404） |
| `server/internal/httpapi/v1/me_tokens_integration_test.go` | 実DB結合9件 |
| `client/src/pages/MyTokensPage.vue` | 一覧・発行モーダル・失効の確認・結果表示 |
| `client/src/components/IssuedTokenDialog.vue` | 1回だけの平文表示 |
| `client/src/components/MeTabs.vue` | `/me` と `/me/tokens` で共有するタブ |
| `client/src/lib/clipboard.ts` | `copySecret` / `selectContents`（`GeneratedPasswordDialog` と共有） |

### 直したファイル

`server/internal/store/queries/me.sql`（`ListMyAPITokens` / `CountMyAPITokens` / `FindMyAPIToken` /
`RevokeMyAPIToken`。発行は既存の `CreateAccessToken` を流用）、`routes.go`（3行）、`fake_test.go`、
`client/src/api/me.ts`、`router/routes.ts`（プレースホルダの差し替え）、`MySettingsPage.vue`
（タブを `MeTabs` へ）、`GeneratedPasswordDialog.vue`（コピーを `lib/clipboard.ts` へ）、
`Makefile`（`test-db`）、`projects_integration_test.go`（`loginAs` の擬似 IP）。

古い節番号の参照を直した（`4.5` → `4.4`）：`auth/token.go`、`session.go`、`user.sql`、
`GeneratedPasswordDialog.vue`。

### 検証

| 種類 | 件数 | 内容 |
|---|---|---|
| 単体（`make test`） | 18件 + 既存全部 | ドリフト検出テストを含めて全 PASS |
| 実DB結合（`make test-db`） | 9件 | **発行した平文で Bearer 認証が通り、失効すると 401**／一覧が `api` だけ／失効済みは出ず期限切れは出る／上限5本と枠の解放／スコープ語彙／他人のトークンは 404 で失効もされない／冪等（`revoked_at` を上書きしない・監査は1件）／セッションの ID は 404／パスワード変更で API トークンも失効 |
| 実サーバ（curl） | 19件 | 上記に加えて Cookie 認証で CSRF 無しは 403、Bearer は CSRF 不要（GET も POST も） |
| ブラウザ（CDP） | 36件 | 発行モーダル（既定90日・スコープ欄が無い・期日の表示・空名で `disabled`）、1回表示、一覧7列、上限5本での `disabled` と理由の表示、失効の確認（復元不可・401）、タブの往復 |
| レイアウト実測 | 17件 | 1440px と 900px で文書が横に流れない・全列が 1px 以上・`[失効]` が `elementFromPoint` で押せる・モーダルの入力欄が画面内・ラジオが横1行。900px の1回表示でトークンが折り返して全文出る |

**端から端まで**：ブラウザで発行した平文を控え、`curl -H "Authorization: Bearer …"` で
`GET /me`（200、`display_name` が「開発メンバー」）と `GET /projects`（200）を確認した。
一覧の「最終利用」にその時刻が載ることも見た。

**スクリーンショット9枚を目視。** 自動検証36件が全 PASS の状態で
**トークンが横スクロールの箱に収まっていた**のを見つけて折り返しへ直した。

### 後始末

検証で作った `api` トークンは画面と curl で全て失効させ、`member@example.com` の
`access_token`（`token_type='api'`）17行と `token.issue` / `token.revoke` の監査ログ34行を
DB から削除した（有効・失効済みとも0件を確認）。Cookie jar と発行した平文を書いたファイルを削除。
ヘッドレス Chrome のプロファイルはスクリプトが毎回消す。`make stop-server` と `make clean-webui` を実行。

### 手順11a〜16a（2026-08-23 に移動）

| 手順 | 完了日 | 検証内容（当時の記述のまま） |
|---|---|---|
| 11a | 2026-08-18 | 実DB結合テスト＋実サーバで 409 / 422 / 403 / 404 と冪等な archive |
| 11b | 2026-08-18 | ブラウザ43件（保存・409・アーカイブ・リポジトリ表形式・メンバー・権限の出し分け・一覧の省略） |
| 12a | 2026-08-18 | 実DB結合テスト6件＋実サーバ22件（作成・409・422・403・CSRF・ETag・生成パスワードでのログイン） |
| 12b | 2026-08-18 | ブラウザ128件（一覧・ソート4列・検索・絞り込み・列幅ドラッグ・ページャ・追加・1回表示・409・403）。**利用者の実機確認の指摘6件を反映済み** |
| 13a | 2026-08-20 | 単体39件＋実DB結合13件＋実サーバ42件（楽観ロック・3つのガード・メール変更後のログイン・リセット後の失効・冪等なメンバーシップ・operator は 403） |
| 14 | 2026-08-22 | 単体12件＋実DB結合8件＋実サーバ（認可12通り・並び・422）＋ブラウザ34件（マトリクス13・レイアウト14・回帰7）。`?scope=project` は権限不要 |
| 15a | 2026-08-22 | 単体28件＋ブラウザ59件（保存・409・422・401・テーマと色相の端末間同期・要パスワード変更の誘導）。**実DB結合12件は書いたが未実行**（下記「次の手順への引き継ぎ」） |
| 15b | 2026-08-22 | 単体18件＋実DB結合9件＋実サーバ19件（Bearer 認証・CSRF・上限5本・冪等な失効）＋ブラウザ53件（一覧・発行・1回表示・失効・タブ・1440/900px の実測）。**画面で発行した平文で `curl -H 'Authorization: Bearer …'` が通ることを確認** |
| 16a | 2026-08-23 | 単体38件＋実DB結合（`make test-db` 全74件 PASS）＋実サーバ8件（409・422・403・404・権限の読み書き分離）＋ブラウザ68件（タグ27・スプリント31・並べ替え10。1440/900px の実測とスクリーンショット確認） |

## 手順16b — チケットAPI（一覧・作成・並べ替え）と dev seed のチケット（2026-08-23、`feature/step-16b-ticket-api`）

**手順16 の3分割のうち2つめ。** サーバ側だけを扱い、`client/` は生成物（`schema.d.ts`）以外
触っていない。バックログ画面は 16c。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/lexorank/lexorank.go` ＋ `_test.go` | `sort_key` の生成（`Between` / `Rebalance` / `Valid`）。`0|<a〜zの26進>:`。**本体に数字を使わない**（終端 `:` が `9` より大きいため）。末尾追加だけ中点法をやめて桁を1つ進める（`appendBody`） |
| `server/internal/activity/activity.go` | `activity` への記録口。`internal/audit` と同じ形（`FromRequest` → `Record`）。**`Field` / `OldValue` / `NewValue` はポインタ**——「送られていない」と「空にした」を区別するため |
| `server/internal/store/queries/ticket.sql` | 一覧（フィルタ13種＋窓関数の総件数）・詳細・採番・作成・タグ付与・並べ替えの近傍取得・振り直し・dev seed 用の2本 |
| `server/internal/store/queries/activity.sql` | `InsertActivity` |
| `server/internal/httpapi/v1/tickets.go` | `GET /projects/{key}/tickets`（9.2）。フィルタの解析と `ETag` |
| `server/internal/httpapi/v1/tickets_create.go` | `POST /projects/{key}/tickets`（9.3）。単一トランザクション |
| `server/internal/httpapi/v1/tickets_move.go` | `POST /projects/{key}/tickets/{seq}/move`（9.4）。振り直しを含む |
| `server/internal/httpapi/v1/ticket_view.go` | 9.2.2 / 9.5.1 の応答の組み立て。`ticketDetailView` が `ticketListItem` を埋め込む |
| `server/internal/httpapi/v1/tickets_test.go` | 単体26件（フィルタ・422・ETag・作成・並べ替えの境界） |
| `server/internal/httpapi/v1/tickets_integration_test.go` | 実DB結合（フィルタ・COLLATE・採番・再帰CTE・窓関数・activity） |

### 変更したファイル

`routes.go`（3ルートと必要権限の宣言）／`routes_test.go`（認可5件）／`fake_test.go`（チケットの
フェイク。**並び順は固定値ではなく `sortRowBySeq` から計算する**——振り直しでキーが変わることを
再現するため）／`paging.go`（`SortSpec.DefaultPerPage`。バックログだけ既定が 200）／
`cmd/pb/dev_seed.go` と `deploy/dev/seed/dev-data.yaml`（チケット13件）／`docs/openapi.yaml`（3本＋
`Ticket` 系スキーマ12個）／`client/src/api/schema.d.ts`（`make gen-api` の生成物）。

### 検証結果

| 種別 | 件数 | 内容 |
|---|---|---|
| 単体（`make test`） | 全 PASS | チケット26件（フィルタの解析・422 の8通り・ETag の可変性・作成の5件・並べ替えの境界5件）＋ LexoRank 10件＋ 認可5件 |
| 実DB結合（`make test-db`） | **全94件 PASS**（16a の 74件から +20） | フィルタ16通り・COLLATE "C" での並び・意味の順のソート・採番の連番性・再帰CTE の部分木・窓関数の総件数・`activity` 5件と `audit_log` 0件・`sort_key` が NULL の行からの回復・他プロジェクトの資源を指したときの 422 |
| 実サーバ（curl） | **50件 PASS / 0 FAIL** | 一覧の既定（`per_page=200` / `sort=sort_key`）・フィルタ12通り・422 の7通り・ETag の一致と差異・非メンバーの 404・作成の 201 と `Location` と 9.5 形式・422 の5通り・CSRF 403・並べ替え（`first` / `last` / `after_seq` / `before_seq`）と 422 の4通り・404 |
| ブラウザ | 4件 PASS | **seed のチケットで 16a の画面が実データになること**——タグタブの「使用中」が `設計 6件 / GUI 3件 / API 5件 / MCP連携 1件`、スプリントタブの「進捗」が `Sprint 1 3/3 / Sprint 2 1/4 / Sprint 3 0/4 / 未定 0/1`。1440px と 900px でスクリーンショットを確認し、崩れが無いことを目で見た |

### 検証で見つけた実装の欠陥（1件）

**優先度が未設定のチケットがあると一覧が 500 になった。** 並べ替えの順位を
`CASE … END::int AS priority_rank` として SELECT の列に出したため、sqlc がキャストから
NOT NULL と推論し、NULL を読めなかった。**単体テストはフェイクを返すので通り、結合テストも
「優先度を持つチケット」しか作っていなかった。** `ORDER BY` に `array_position` を直接書く形へ
直し、結合テストに「優先度を決めていない仕事」を1件足した。

### 検証で作った資源の後始末

検証用プロジェクト（`v16b-*`）と `__verify16b__` で始まるチケットは削除した。demo の
チケットは **seed の13件（`seq` 1〜13、`sort_key` は `0|n:` 〜 `0|z:`）に戻してある**——16c の
バックログ画面がそのまま使う。サーバは停止し、`make clean-webui` で `webui/dist` を戻し、
検証で作った Cookie jar（セッショントークンの平文）は削除した。


## 手順16c（バックログ画面）— 2026-08-23

`feature/step-16c-backlog-page`。`GuiDesign.md` 5.4 のバックログ画面を作り、
ルートを付け替えた。**手順16 の完了条件（一覧・作成・並べ替え・グループ化）をここで満たす。**

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/api/tickets.ts` | `listTickets` / `createTicket` / `moveTicket`。種別アイコン・優先度記号・ステータス記号・表示名のマップ（`GuiDesign.md` 5.4 / 8.7 の転記） |
| `client/src/pages/BacklogPage.vue` | 本体。フィルタ6種・グループ化6軸・ツリー・D&D・4状態・キーボード（`c` / `j` / `k`） |
| `client/src/components/NewTicketModal.vue` | 新規チケット（5.4.3）。状態の欄を持たない |

### 変更したファイル

| ファイル | 変更 |
|---|---|
| `client/src/router/routes.ts` | `/p/:key/backlog` を新設（実画面）／`/p/:key/tickets` をプレースホルダから**クエリを保つリダイレクト**へ／`/p/:key/search`（Phase 2）と `/p/:key/insights`（Phase 3）のプレースホルダを追加／`/p/:key` と `/p/:key/tickets/:seq` の `status` を手順19・17 へ振り直し |
| `client/src/components/SideMenu.vue` | `☑ チケット` → `≡ バックログ`。リンク先を `/backlog` へ。`ticket.view` で出し分け（4.3）。件数バッジを出さない理由を doc コメントへ |
| `client/src/components/ProjectSwitcher.vue` | doc コメントの「チケット一覧を見ていたら」→「バックログを見ていたら」 |
| `client/src/lib/datetime.ts` | `todayPlainDate()` を追加（期限超過の判定用。`toISOString()` を使わない） |
| `docs/GuiDesign.md` | 5.4 に「期限」「フィルタ」「`[解除]`」の行を新設、「階層表示」「並べ替え」「ソート」「API」を改訂、ワイヤーへの注記を追加。5.4.1 にグループ化中は親子を畳まない旨、5.4.3 に「選択肢の出どころ」の表 |
| `docs/ApiDesign.md` | 9.14 の `details[].code` に `not_found` を追加 |

**`docs/openapi.yaml` は変更なし**——16c は `client/` だけを触り、API を追加・変更していない。

### 検証結果

| 種別 | 件数 | 内容 |
|---|---|---|
| 型・ビルド | PASS | `vue-tsc --noEmit` / `vite build` / `make build`（単一バイナリ） |
| 単体（`make test`） | 全 PASS（11パッケージ） | `openapi.yaml` のドリフト検出を含む |
| 実DB結合（`make test-db`） | **全95件 PASS** | 16b から回帰なし |
| ブラウザ・読み取り（`verify_backlog.py`） | **92件 PASS / 0 FAIL** | ルーティング6・メニュー4・一覧の中身26・階層7・ソート8・フィルタ13・グループ化17・レイアウト実測8（1440px / 900px） |
| ブラウザ・状態変更（`verify_mutate.py`） | **27件 PASS / 0 FAIL** | モーダルからの作成14・検証エラー3・インデントの打ち切り1・並べ替え9 |
| タイムゾーン | PASS | `TZ=Asia/Tokyo` / `America/New_York` / `Pacific/Kiritimati` の3つで node により実測。`formatPlainDate` は不変、`new Date()` 経由は New York で前日へずれる、`todayPlainDate()` は Kiritimati で正しく翌日を返す |
| 目視 | 実施 | 1440px と 900px で6枚（一覧・グループ化・モーダル・空状態・畳んだセクション） |

**期待値は seed の13件から数え上げて書いた**（`type=bug` 1件、`type=epic` 3件、
`priority=highest` 3件、`assignee=none` 5件、`tag=none` 2件、`status=done` 4件、
タグのセクションが `設計6 / GUI3 / API5 / MCP連携1 / 未分類2` で表示17行・実数13件）。
**並び順は API の応答と突き合わせた**——検証側で並べ直さない（`LEARNINGS.md` #25）。

### 検証で見つけた実装の欠陥（3件）

1. **`⠿` のドロップが一度も効かなかった。** `dropOn` が `draggingSeq` を消してから
   判定関数を呼び、判定側が同じ ref を読み直して必ず `null` を見ていた。掴んだ `seq` を
   引数で渡す形へ直した。**周囲の3件が「意味のない PASS」になっていた**——何も動かないので
   「元に戻る」も「別の親へは動かない」も通る
2. **`.section` というクラス名が `SideMenu.vue`（「管理」の見出し）と衝突した。**
   scoped スタイルは DOM のクラス名を分けないので、検証の `querySelectorAll('.section')` が
   両方に当たり **11件が同時に FAIL**。バックログ側を `.group-section` へ改名した
3. **グループの見出しが「どこまでを覆うか」読めなかった**（自動検証92件は全 PASS）。
   見出しを地より一段明るい帯（`--pb-elevated`）にした

あわせて**新規チケットモーダルの見積・開始日・期限が折り返し、期限だけが幅いっぱいで
下に残っていた**のをスクリーンショットで見つけ、見積の基準幅を 130px に固定して1行へ収めた
（1440px / 900px の両方で3つの `top` が一致することを実測）。

### 検証できなかったこと（2件）

- **権限による出し分け**（`[+ 新規チケット]` は `ticket.create`、`⠿` は `ticket.edit`）。
  Phase 1 の operator が両方を持つため、**seed の4アカウントすべてが満たす**。
  実装は入れたが、ブラウザでは負の側を確かめられない
- **`ProjectSwitcher` の「同じ画面種別を維持する」**（4.4）。seed のプロジェクトが
  `demo` 1つしかなく、切り替え先が無い。16c では実装を触っていない（コメントのみ）

### 検証で作った資源の後始末

検証が作ったチケット（`seq` 14 以降、2回の実行で計24件）と、それに紐づく `activity` の行を
削除した。**`activity` は多相参照で FK を持たない**ため、同じトランザクションで消している。
並べ替えで動いた `seq` 2・3 の `sort_key` と `version` は、**検証前に取った控えと突き合わせて
元の値へ戻した**（`0|o:` / `0|p:`、version 1）。`diff` で差分ゼロを確認ずみで、demo の
チケットは seed 直後と同一である。サーバは停止し、`make clean-webui` を実行した。
スクラッチパッド（検証スクリプト・スクリーンショット・Chrome のプロファイル）はセッションの
作業領域にあり、リポジトリには残っていない。

## 手順16d-a（種別の3値化とオンステージ）— 2026-08-23

**ブランチ**：`feature/step-16d-a-ticket-types-and-stage`

**16c の実機確認で出た5件の指摘のうち、サーバと設計文書に属する部分を当てた。**
画面の作り直しは 16d-b。**設計文書の改訂をコードより先に当てきったので、
実装中の設計相談はゼロだった**（`LEARNINGS.md` #43 の型の3回目）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/migrations/0015_ticket_type_and_stage.sql` | ①`type` の `CHECK` を3値へ（既存行を `task` へ移してから張り替え）②`staged_at timestamptz` と部分索引 `idx_ticket_staged` |

### 変更したファイル

| ファイル | 内容 |
|---|---|
| `docs/DbDesign.md` | 6.6（種別3値の意味の表・`staged_at`・二段が `sort_key` を共有する理由・進捗と独立した軸）、6.10（グルーピングの実体は `parent_id`／エピックだけ画面が特別扱いする／バグはタグ）、5.2（0015 を追加）、8章（Phase 2 の採番を 0016 起点へ） |
| `docs/ApiDesign.md` | 9.1（一覧も完全形で出す）、9.2.1（`type` 3値・`parent` の複数指定・`staged` フィルタを持たない理由）、9.2.2（`staged_at`）、9.3（`type` 3値・作成は必ずバックログ）、9.4＋**新設 9.4.1**（`staged`・`position` は段の中・`after`/`before` は段を問わない・`not_stageable`）、9.5.2（`staged_at` も `use_move_endpoint`）、9.14（`not_stageable`） |
| `docs/GuiDesign.md` | 5.4 のワイヤーを**二段に描き直し**（全角幅を計算して全行82桁に揃えた）、仕様表（種別アイコン3つ・ID完全形・折りたたみ・フィルタ7つ・エピック行）、**新設「二段（オンステージとバックログ）」「エピックをフィルタにする」**、5.4.1（グループ化中は二段をやめる・ツリーの開閉は別に持つ）、5.4.3（種別2択・段を出さない・説明欄テンプレート・親をエピック配下に絞る） |
| `docs/Requirements.md` | 2章の種別の写しを3値へ（経緯つき。利用者の判断） |
| `docs/openapi.yaml` | `type` の enum 4か所、`type`/`parent` クエリ、`Ticket.staged_at`（`required` にも）、`MoveTicketRequest.staged`、`MoveTicketResult.staged_at`、move の `details[].code` |
| `server/internal/store/queries/ticket.sql` | `ListTickets`（`parent_seqs::int[]`・再帰CTEを `UNION` へ・`staged_at`）、`GetTicketBySeq`（`staged_at`）、`GetTicketSortRow`（`type`・`staged_at`・`parent_type`）、`MinTicketSortKeyInStage`／`MaxTicketSortKeyInStage` を新設し `MinTicketSortKey` を廃止、`MoveTicket`（段も書く）、`SetTicketStagedAt`（seed 用） |
| `server/internal/httpapi/v1/tickets.go` | `ticketTypes` を3値へ、`ticketTypeEpic` 定数、`parent` のカンマ区切り解析（**正規化は数として並べ替える**）、`parentSeqs` を**空スライスで初期化**（nil だと pgx が NULL を送り `cardinality(NULL)` で全件落ちる） |
| `server/internal/httpapi/v1/ticket_view.go` | `staged_at` を一覧・詳細の両方へ |
| `server/internal/httpapi/v1/tickets_move.go` | `staged`（ポインタで省略と false を区別）、段の決定、`stageable()`、`not_stageable`、`position` を段で解決、応答に `staged_at` |
| `server/cmd/pb/dev_seed.go` | 種別3値、`staged` 項目、段に置ける条件の検証、`SetTicketStagedAt` の呼び出し |
| `deploy/dev/seed/dev-data.yaml` | タグ「バグ」追加、`bug`→`task`＋タグ「バグ」、`phase`→`task`、オンステージ2件（うち1件は `todo`） |
| `client/src/api/tickets.ts` | 種別の語彙を3つへ、`backlogTicketTypes`（`['story','task']`）を新設 |
| `client/src/api/schema.d.ts` | `make gen-api` の生成物 |
| テスト | `tickets_test.go`（オンステージ6件・ETag の `parent` 2件・`parentSeqs` の nil 検査を追加、`moveRespJSON` を新設）、`tickets_integration_test.go`（オンステージ一式・`parent` の OR 2件）、`fake_test.go`（`*InStage` 2本・段を書く `MoveTicket`）、`dev_seed_test.go`（`TestDevDataValidateStaged` と実ファイルの検査） |

### 検証結果

| 層 | 件数 | 結果 |
|---|---|---|
| マイグレーション | — | `make migrate` で 0015 適用。**実DBの `bug` 2件・`phase` 2件・`wbs` 1件が `task` へ移り**、`CHECK` が3値・`staged_at` 列・`idx_ticket_staged` を確認 |
| 単体（`make test`） | 全パッケージ PASS（チケット関連65件） | ドリフト検出 `TestOpenAPIMatchesRoutes` を含む |
| 実DB結合（`make test-db`） | **97件 PASS / 0 FAIL** | 16c の95件 + `部分木の複数指定は OR` / `部分木の複数指定（重なる）` |
| 実サーバ（curl） | **24件 PASS / 0 FAIL** | 廃止種別 422×3、`parent` の OR、`staged_at` の往復、`not_stageable`、`position` が段で絞れていること、作成は必ずバックログ |
| seed | — | `make dev-reset` 後、種別3値・seq 7 がタグ「バグ」付き `task`・seq 9/10 がオンステージ（**10 は `todo`＝未着手**）を実データで確認 |
| ブラウザ（CDP） | **7件 PASS / 0 FAIL** | 種別フィルタ4項目・廃止種別なし・`◈`/`▦` が出ない・タグ「バグ」表示・13行・新規モーダルの種別・900px で横に流れない |
| スクリーンショット | 3枚 | `backlog-1440` / `newticket-1440` / `backlog-900` を**目で確認**。崩れなし |

### 実装中に見つけて直したこと（1件）

**エピック自身が段に置けてしまう状態だった。** `stageable()` を「親を持たない、または
親がエピック」だけで書いたため、**エピックは親を持たないので通ってしまう**。実DBの
デモデータを見ていて気づいた。エピックはどちらの段にも行として出ないので、上げると
**見えないのに段に居る**状態になる。`GetTicketSortRow` に自分の `type` を足し、API・
`pb dev seed` の検証・`ApiDesign.md` 9.4.1 の3か所を揃えた。

### 検証側の誤りだったもの（2件）

- **ETag の検証で `base` と比較対象を同じ値にした**（`type=bug` を機械的に `type=task` へ
  置換した結果、「フィルタが違うのに ETag が同じ」を測れなくなった）。`base` を `type=story` へ
  変えて意味を戻した。**テストデータを変えたら、そのテストが何を測っているかを数え直す**
- **種別フィルタの選択肢にアイコンが前置されることを数え落とした**（`⚑ エピック`）。実装が正

### 検証で作った資源の後始末

| 資源 | 扱い |
|---|---|
| 実サーバ検証が作ったチケット1件（demo seq=38） | 削除。**孤児になった `activity` 1行も削除**（16b と同じ経路） |
| `sort_key` / `staged_at` / `version` の変更 | 検証前に生成した `UPDATE` 13文で復元し、**`diff` で差分ゼロを確認** |
| Cookie jar・Chrome プロファイル・検証スクリプト | スクラッチパッドに置き、削除 |
| `make build` の成果物 | `make clean-webui` |
| デモDB | `make dev-reset` で作り直した（利用者の承認。`tic` プロジェクトは消えた） |

## 手順16d-b（バックログ画面の作り直し）— 2026-08-23

**ブランチ**：`feature/step-16d-b-backlog-rebuild`

**16d-a が書いた `GuiDesign.md` 5.4 のとおりに画面を組んだ。** サーバは 16d-a で
できあがっており、**APIの追加・変更は無い**（`docs/openapi.yaml` は触っていない）。
設計判断で残っていたのは「上げた行の配下をどちらの段に出すか」1件だけで、
実装前の一括確認（A-1 / A-2 と確認7件・実装判断4件）で片付けてから着手した。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/components/EpicFilter.vue` | エピックの複数選択パネル。`<Teleport>` + `position: fixed`（`UserActionsMenu` と同じ型）。チェックボックス＋詳細への `↗`。**選んでも閉じない** |

### 変更したファイル

| ファイル | 内容 |
|---|---|
| `docs/GuiDesign.md` | 5.4「二段」に**「配下の行き先」を新設**（どちらの段にも出さない／到達手段は手順17／ツリーの折りたたみが有効ならオンステージへ展開／総件数は出していない配下を含む）。表の「段に置けるもの」にも1行 |
| `deploy/dev/seed/dev-data.yaml` | `MCP サーバの足場を作る` の配下に2件（**story/task どうしの親子**。エピック配下だけではツリーが画面に出ない）。冒頭の組み合わせ表の「階層2段」の説明も直した |
| `client/src/api/tickets.ts` | `ListTicketsQuery.parent` を `number` → `string`（**カンマ区切りの OR。openapi と食い違っていた**）、`newTicketBodyTemplate` を新設 |
| `client/src/components/NewTicketModal.vue` | 種別の選択肢を `backlogTicketTypes`（2つ）へ、説明欄の初期値をテンプレートに、`projectKey` を受け取り**親の候補を完全形 `demo-8 …` で出す**、`rows` を 7 へ |
| `client/src/pages/BacklogPage.vue` | 上下二段（`splitByStage` / `Section.stage`）、配下を伏せる、`carriedCount` の注記、エピックフィルタ（`epicSeqs` ↔ `parent`）、`type` が「すべて」のとき `story,task` を送る、エピック語彙の取得、`isStageable`、ツリーの折りたたみ（`pb.backlog_tree_collapsed`）、ID完全形（`copy` 乗っ取りを撤去）、段をまたぐ D&D、`parentCandidates`、列幅の再調整 |

### 検証

**ブラウザ（読み取り）54件 PASS / 0 FAIL**（1440px と 900px）
段の分割と件数・行が片方にしか出ないこと・ID完全形・ツリーのインデントと
キャレット・オンステージがフラットであること・エピックフィルタ（1件／2件の OR・
詳細への導線・パネルの開閉）・`[解除]`・グループ化で二段をやめること・ソート中は
ツリーを組まないこと・**ID／期限／担当が切れていないこと**・幅0の列が無いこと。

**ブラウザ（状態変更）37件 PASS / 0 FAIL**
段へ上げる／戻す（見出しへのドロップ＝段の末尾）、**上げると配下が両方の段から
消え、件数の隣に「うち2件」が出ること**、グループ化に切り替えると配下が戻ること、
同じ親への並べ替え、**別の親の行へは落とせないこと**、段をまたぐドロップの可否、
ツリーとセクションの開閉が**別のキー**に入り読み直しても残ること、
新規チケットの説明欄テンプレート・種別2択・親の初期値と候補、
**作ったチケットの `body_md` が実DBでテンプレートになっていること**。

**単体・結合**：`make test` 全パッケージ PASS、`make test-db` 97件 PASS / 0 FAIL。

**スクリーンショット7枚**（1440px：二段／エピックのパネル／ツリーを畳んだ状態／
新規チケット／タグでグループ化、900px：二段／エピックのパネル）。
**実測が全 PASS のまま崩れていたのを2件、目で見つけた**——①900px でタイトルが
タグより先に0幅まで潰れた ②`⚠` の分だけ期限が切れた。直したあと撮り直した。

### 検証で作った資源のあとしまつ

| 残るもの | 戻し方 |
|---|---|
| 検証で作ったチケット1件（demo seq=16） | SQL で削除（`DELETE /tickets/:seq` は手順17）。**`activity` の `create` 1行も削除** |
| `sort_key` / `version` の変更（seq 3・12） | 検証前の控えから `UPDATE` で復元し、**`diff` で差分ゼロを確認**（`updated_at` は戻していない） |
| `staged_at` | 検証の中でバックログへ戻したので、控えと一致 |
| Cookie jar・Chrome プロファイル・検証スクリプト | スクラッチパッドに置き、削除 |
| `make build` の成果物 | `make clean-webui` |
| デモDB | **作り直していない**（`make dev-seed` で2件足しただけ。利用者のデータを消していない） |

## 手順17a（チケット1件のAPI）— 2026-08-26

`ApiDesign.md` 9.5 / 9.6 / 9.7 の5本。**画面（`GuiDesign.md` 5.5）は 17b。**

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/workflow.sql` | 新規。`FindProjectWorkflowID` の1本だけ。**ステータスと遷移そのものは `project.sql` の `ListWorkflowStatuses` / `ListWorkflowTransitions` を再利用した**（手順9b が 5.4 のために作ったもの）。project_id 起点で作り直すと、片方だけ列が増えたときに応答が割れる |
| `server/internal/store/queries/comment.sql` | 新規。`CreateComment` / `CountTicketComments`。**コメントAPI（9.8）は手順18 だが、9.6 の遷移が `kind='progress'` を作るので投入と件数だけ先に置いた** |
| `server/internal/httpapi/v1/tickets_get.go` | `GET`（9.5.1）。組み立ては `buildTicketDetail` に任せる薄い層 |
| `server/internal/httpapi/v1/tickets_update.go` | `PATCH`（9.5.2）。約620行で本手順の中心 |
| `server/internal/httpapi/v1/tickets_delete.go` | `DELETE`（9.5.3） |
| `server/internal/httpapi/v1/ticket_workflow.go` | 遷移の判定。**9.6 と 9.7 が同じ `denyTransition` を通る**——2か所に別々の条件を書くと、画面が出した選択肢が押した瞬間に断られる |
| `server/internal/httpapi/v1/tickets_transition.go` | `POST .../transition`（9.6） |
| `server/internal/httpapi/v1/tickets_transitions.go` | `GET .../transitions`（9.7） |
| `server/internal/httpapi/v1/tickets_detail_test.go` | 単体71件 |
| `server/internal/httpapi/v1/tickets_detail_integration_test.go` | 実DB結合12件 |

### 直したファイル

`ticket.sql`（`UpdateTicket` / `DeleteTicket` / `SetTicketStatus` / `IsTicketDescendant` /
`DetachTicketTags` / `GetTicketTypeByID`）、`ticket_view.go`（`comment_count` を実数へ）、
`routes.go`（5本の宣言）、`tickets.go`（`permTicketAssign`）、`apierr.go`（`invalid_transition`）、
`fake_test.go`・`routes_test.go`・`apierr_test.go`、
`docs/ApiDesign.md`（9.5.1 / 9.5.2 / 9.5.3 / 9.7 / 9.14 / 10.2）、`docs/openapi.yaml`（5パス＋4スキーマ）、
`client/src/api/schema.d.ts`（`make gen-api` の生成物）。

### `PATCH` を `map[string]json.RawMessage` で受けた理由

9.5.2 は「送られたフィールドだけを更新する」と「`null` で項目を空にする」の両方を求める。
**`*T` のポインタでは、キーが無い場合と `null` が送られた場合がどちらも `nil` になり、
「担当を外す」を表せない。** そのため生の JSON を受けてから `optional[T]{Set, Null, Value}`
へ解いている。SQL 側は `project.sql` の `UpdateProject` と同じ2通りの書き方で受ける——
NOT NULL の列は `COALESCE(sqlc.narg(…), 現在値)`、NULL にできる列は
`CASE WHEN @…_set THEN sqlc.narg(…) ELSE 現在値 END`。

### 検証結果

| 層 | 件数 | 内容 |
|---|---|---|
| 単体 | **71件 PASS** | 楽観ロック3種・不変フィールド10種・形の検証11種・`null` の撃ち分け・タグの置換・`parent_cycle`・オンステージの規則4件・`ticket.assign` の4件・`activity` の粒度3件・遷移の5段階・`transitions` の4件 |
| ルート宣言 | **9件 PASS** | `routes_test.go`。`ticket.view` / `edit` / `delete` / `transition` の出し分けと、`{seq}` と `{seq}/move` `{seq}/transitions` が衝突しないこと |
| 実DB結合 | **`make test-db` 全110件 PASS**（うち手順17a は12件） | `COALESCE` と `CASE WHEN` の撃ち分け・`WHERE version =` の楽観ロック・再帰CTEが孫まで届くこと・**タグ変更で一覧の `ETag` が変わること**・`closed_at` の往復・**子が `ON DELETE SET NULL` で残ること**・`activity` と `comment` の同時確定 |
| 実サーバ | **62件 PASS**（46＋16） | 5本すべてを Cookie 認証で通した。**`ticket.delete` は `viewer` / `member` で 403、`admin` で 204** を実データで実測（Phase 1 に負の側が実在する唯一のチケット権限）。9.7 は seed の3ステータスすべてで `items` の中身を読み上げた |

**検証側の誤りが2件あった。**

1. 「タグだけ変えても `updated_at` が動く」を**応答の `updated_at` で測っていた**。2.2 に従って
   秒精度で出しているので、同じ秒のうちに更新すると文字列が変わらない。**要件は
   「一覧の `ETag` が変わること」なので、`ETag` そのものを突き合わせる形に直した**
   （`ETag` は `UnixNano` を使う）。実装は最初から正しかった
2. `transitions` の `items` を印字する行が、シェル内の入れ子引用符で空を出していた。
   **「現在のステータスは出ない」が PASS していたが、その根拠を読めていなかった。**
   読み取りだけの追加検証（16件）を書いて中身を測り直した

### 検証で作った資源とその後始末

| 残るもの | 対応 |
|---|---|
| 検証用チケット1件（`[verify17a]`） | API で削除（204） |
| そのチケットの `activity` 10件 | **設計どおり残る**（9.5.3）ので、使い捨ての検証ぶんとして `DELETE` した。`activity` は 1件（検証前と同じ）に戻した |
| `comment` 1件 | チケット削除の `CASCADE` で消えた（0件。検証前と同じ） |
| seed のチケット16件 | **JSON で控えを取り、`diff` で差分ゼロを確認**（`sort_key` が `\|` を含むため区切り文字を使わない。LEARNINGS #74） |
| `project_counter.last_ticket_seq` | **17→18 のまま戻していない。** チケット番号は人が読む識別子であり、欠番は出てよいが再利用してはならない（`DbDesign.md` 6.4.1） |
| Cookie jar・検証スクリプト・サーバログ | スクラッチパッドに置き、Cookie jar は削除。サーバは `make stop-server` |
| `develop` の一時 worktree | `TestExpiresAtFormat` の切り分けに使い、`git worktree remove` |
| `make build` の成果物 | **`make build` していない**（API のみのため）。`make clean-webui` は不要 |

## 手順17b の設計改訂（チケット詳細のペイン化と手順17c の新設）— 2026-08-27

**コードは1行も書いていない。** `docs/ticket-detail-redesign` ブランチで設計文書だけを当てた。
利用者の要望（詳細を別画面にせず一覧の右へ開く／コード参照と参考リンクの領域）が
`GuiDesign.md` 5.5 だけでなく**基本構成そのもの**に及んだため、絶対規則3 に従って
**コードより先に文書を固める**工程を分けた。

### 直したファイル

| ファイル | 変更 |
|---|---|
| `docs/GuiDesign.md` | **2.1**（`←` の戻りリンクを撤回）／**2.2.1 を新設**（詳細ペイン。マスター・ディテール）／**2.4**（3ペインの閾値をコンテンツ幅 960px で定義）／**3.1**（遷移図に注記）／**3.2**（URL がフィルタを持ち回る。ルーティング表の実装状況を実測に合わせる）／**5.4**（行クリック・縮小時の一覧・一覧へ戻る導線）／**5.5 を全面改訂**（2カラム→1カラム、子チケット・コード・参考リンク、編集の単位、状態のドロップダウン）／**6.1**（`SplitPane` を追加）／**11章**（未解決事項を3件入れ替え） |
| `docs/DbDesign.md` | **6.12 を新設**（`ticket_reference`、マイグレーション 0016）／冒頭の状態行・2.1・5.2 のファイル一覧・8章の採番説明を実測に合わせる |
| `docs/ApiDesign.md` | **9.5.1**（詳細応答に `references`）／**9.10 を「関連リンクと外部参照」の傘に広げ 9.10.1 / 9.10.2 へ割った**／**9.15**（手順対応表に 17c）／5.5 を指す文中の「サイドバー」「関連」欄の呼称を 1カラム版へ |
| `docs/Design.md` | **3.1**（Markdown エディタと描画の依存4つ）／**11章**（手順17 の行、17a/17b/17c の表、17c 新設の理由、Phase 2/3 のマイグレーション番号の修正） |
| `docs/PROGRESS.md` | Phase 1 表に 17c を追加、17b の内容を更新、引き継ぎ表を消化・更新・追加 |

### 節番号を動かさなかった理由

**`ApiDesign.md` 9.10 に外部参照を足すとき、9.11 以降へ番号をずらす案を採らなかった。**
先に参照の写しを数えたところ、`9.11` が98件・`9.12` が82件（docs＋server＋client の合計）あった。
**9.10 自体を傘にして 9.10.1 / 9.10.2 へ割れば番号は1つも動かない**ので、そちらを選んだ
（`LEARNINGS.md` #64「番号を動かす前に、まず数える」の適用。同文書の
`### 表の規則（…）` と同じ前例）。

### 見つかった設計文書どうしの食い違い（3件・いずれも本改訂で修正）

| 食い違い | 状態 |
|---|---|
| `Design.md` 11章 Phase 2 手順20「マイグレーション 0015〜0017」 | **0015 は手順16d-a で使用済み。** 0017〜0019 へ直した（手順27 も 0020〜0023 へ） |
| `DbDesign.md` 冒頭「Phase 1 のDDL・シードを確定（0001〜0014）」と 2.1 の同じ写し | **0015 の時点で既に古くなっていた。** 0016 へ直した |
| `GuiDesign.md` 3.2 ルーティング表の `/p/:key/backlog`「プレースホルダ」 | **手順16c で実装済み。** 「実装（手順16c）」へ直した |

### 検証

**文書のみのため、実行する検証は無い。** 代わりに機械で2つ点検した。

| 点検 | 結果 |
|---|---|
| 変更した4文書の Markdown 表の列数がそろっているか（コードフェンス内を除く） | **新規追加分の不整合ゼロ。** 検出された5件はすべて既存行で、`\|` をセル内に持つ書き方による偽陽性 |
| 見出しの重複 | **4文書とも無し** |

**相互参照の点検**——`右サイドバー` の全文検索で 5.5 の1カラム化に取り残された記述が
`ApiDesign.md` に2件あり（9.12 のスプリント欄、9.10.1 の「関連」欄）、あわせて直した。

### 検証で作った資源とその後始末

**スクラッチパッドに2つ作り、いずれも本文へ取り込んだ後は不要である**
（`dbdesign_612.md` / `gui_55.md` = 挿入する節の原稿）。スクラッチパッドは
セッションをまたいで残らないため、明示的な削除は行っていない。
**サーバもDBもコンテナも起動していない。**

## 手順17b（チケット詳細画面。バックログの右に開くペイン）— 2026-08-27

`GuiDesign.md` 5.5 の実画面と、2.2.1 のマスター・ディテール。**`server/` は1行も触っていない**
（API は 17a で全部揃っている）。`docs/openapi.yaml` も不変。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/components/SplitPane.vue` | 一覧と詳細の境界（6.1 / 2.2.1）。ポインタ操作で幅を変え `pb.detail_pane_w` に保存。**並ぶかどうかは受け取った幅で決める**（2.4。窓幅ではない） |
| `client/src/components/TicketDetailPane.vue` | 詳細ペイン本体（5.5）。1カラム・2列のメタ情報グリッド・インライン編集・削除 |
| `client/src/components/MarkdownEditor.vue` | CodeMirror 6（`minimalSetup`）＋ 縦に積んだライブプレビュー（5.5「説明欄」） |
| `client/src/components/StatusDropdown.vue` | 状態のドロップダウン（5.5）。`<Teleport>` ＋ `fixed`。`allowed:false` を理由付きで出す |
| `client/src/lib/markdown.ts` | `markdown-it` → `dompurify`。**見出しを3段下げる**規則を持つ |

### 直したファイル

| ファイル | 変更 |
|---|---|
| `client/src/api/tickets.ts` | `getTicket` / `updateTicket`（`If-Match`）/ `deleteTicket` / `transitionTicket` / `listTransitions` を追加 |
| `client/src/pages/BacklogPage.vue` | `SplitPane` の組み込み、縮小時の3列、フィルタ行の折り畳み、戻る導線、選択行、`watch` の対象をクエリへ変更 |
| `client/src/components/PageHeader.vue` | **追加のみ**。`titleActionLabel` / `titleActionIcon` を渡したときだけタイトル領域がボタンになる |
| `client/src/components/EpicFilter.vue` | `↗` がクエリを持ち回るようにした（開いてもエピックの絞り込みが外れない） |
| `client/src/styles/base.css` | `.markdown-body`（描画した Markdown の体裁）を追加 |
| `client/src/router/routes.ts` | `/p/:key/tickets/:seq` を `BacklogPage` へ差し替え（プレースホルダを撤去） |
| `client/package.json` / `package-lock.json` | 依存4つ ＋ `@types/markdown-it` |
| `deploy/dev/seed/dev-data.yaml` | `body_md` を2件（demo-9 / demo-12）。**開いた瞬間に Markdown が描画される状態を seed に作った** |
| `docs/GuiDesign.md` | 5.5（見積3つ・`updated_at` 非表示・親を選択式・「説明欄」の新設）、5.4（縮小時はタグを出さない） |
| `docs/Development.md` | 10.4（実行時依存 3つ → 7つ）、8.2（`Page.bringToFront`） |

### 実装中に見つけた欠陥（いずれも自分で踏んで直した）

| 症状 | 原因 | 直し方 |
|---|---|---|
| **詳細が真っ白**（ヘッダだけ出る） | `immediate: true` の `watch` をスクリプト上部に置き、`cancelEdit()` が**まだ宣言前の `const`** を触って TDZ の `ReferenceError`。Vue は console.error に流すだけで画面は出る | `watch` をスクリプト末尾へ移した。**関数宣言は巻き上がるが、参照する `const` は巻き上がらない** |
| **一覧ペイン全体が `--pb-accent` で塗り潰される** | `SplitPane` のスロット用 div に `class="primary"` / `"secondary"` を使い、**`base.css` のボタンの正本と衝突**した | `.split-primary` / `.split-secondary` へ改名（`LEARNINGS.md` #46 と同じ根） |
| **数値欄がまったく保存されない**（編集モードのまま無反応） | **`v-model` は `type="number"` の値を自動で数値へ変換する**（Vue 3 `vModelText`）。`Ref<string>` の型は嘘になり、`draft.value.trim()` が実行時 `TypeError`。`commitEdit` の中で投げるので画面は何も言わない | `draftText()` で必ず `String()` を通す |
| **境界を一度も掴めない** | `.divider` を `margin-right: -9px` で重ねたため、DOM 順で後ろの `.split-secondary` が上に載っていた | `.divider` に `position: relative; z-index: 1` |
| **メタ情報が3列になり、見積・実績と開始・期限の組が行をまたいで割れた** | `grid-template-columns: repeat(auto-fit, minmax(210px, 1fr))` が 750px で3列を作った | 5.5 のとおり `repeat(2, minmax(0, 1fr))` で固定 |
| **親の `↗` が次の行へ落ちた** | `select` に `max-width: 100%` だけを与え、選択肢の文字数ぶんの幅を主張していた | `flex: 1 1 0` ＋ `min-width: 0` |
| **本文の `#` が `<h1>` を作り、9.2「`<h1>` は唯一のページ見出し」を破った** | `markdown-it` の既定 | `pb_demote_headings` で3段下げ（`#` → `<h4>`）。`base.css` の見た目もその階層で作り直した |
| **縮小した一覧でタグが枠の途中で切れる** | 5.4 に縮小時のタグの扱いが無く、全幅と同じに出していた | **縮小中はタグを出さない**（5.4 に1行足した）。「読めないのに在る」は 2.2.1 が「重ねる」案を却下した理由と同じ |
| **CodeMirror が1行ぶんの高さしか出ない** | 中身で伸びる作りで `min-height` を置いていなかった | `min-height: 180px`。**PB で最も打鍵回数の多い入力欄**（`Design.md` 3.1）に1行の窓を出すのは素の `textarea` より狭い |

### 検証結果

**ブラウザ 129件 PASS**（読み取り72 ＋ 状態変更57）。1440px / 1100px / 900px の3幅、
メインメニューの展開・折りたたみの両方で実測し、**スクリーンショット10枚を目で見た。**

| 区分 | 主な内容 |
|---|---|
| 読み取り（72件） | 全幅7列 → 開くと3列／`⠿`・優先・担当・期限・タグが落ちる／フィルタ行が `[絞り込み ▾]` へ／一覧450px・詳細750px／行の高さ 40px が動かない／ID 完全形・`✕`・`[⋯]`／メタ12項目（見積3つ）／`updated_at` を出さない／Markdown（見出し3・表・コード・リンクの `target`＋`rel`）／`<h1>` はページに1つ／出さないセクション5つ／子チケット2件と行クリックでの差し替え／ヘッダ左と `✕` の両方で戻る／フィルタの持ち回りと素のURL／縮小時の `⚠` のみ／状態ドロップダウン（完了チケットで全項目不可＋理由）／**2.4 が窓幅ではなくコンテンツペインの幅で決まること**（同じ 1100px でメニュー折りたたみ＝並ぶ、展開＝並ばない） |
| 状態変更（57件） | タイトルの保存・`version` +1・無変更なら送らない・`Esc` で戻す・一覧の行も差し替わる／見積・実績・期限（`date` が前日へずれない）／空にして `null` へ戻す／優先度・担当・スプリントの即時 `PATCH`／担当を外す／タグの付け外し／CodeMirror の実キー入力とプレビュー追随／保存と取消／遷移（**まず「進める」ことを確かめてから**）／`422 not_stageable`・`422 parent_cycle`・`409` を**欄の直下に理由付きで、入力値を捨てずに**／削除の確認ダイアログに子2件／作って消す一巡／`SplitPane` の実マウスドラッグ・保存・読み直し・両側の下限 |

**Go は1行も変えていない。** `make test` は既知の時限式 `TestExpiresAtFormat` だけが落ち、
**`git worktree` で `develop` を切り出して同じ失敗を確認**した（手順外の作業に起票済み）。
`deploy/dev/seed/dev-data.yaml` を変えたので `go test ./cmd/pb -count=1` を再実行して PASS。
**`make test-db` は走らせていない**——`server/` に変更が無く、結合テストが測る対象が増えていない。

### 検証で作った資源とその後始末

| 作ったもの | 後始末 |
|---|---|
| チケット16件の全列・タグの控え（JSON） | `before.json` → 復元 → 再取得して `diff` で**差分ゼロ**を確認 |
| 検証が作った `activity` 66行 | 削除（検証前の1行だけが残っていることを確認）。**日付リテラルで切らない**——DBのセッションTZは UTC で、JST の「今日」とずれる |
| 検証のログインが作った `audit_log` 24行 | 削除（`login.success` / `login.failure` / `permission.denied`） |
| ログイン失敗のロック | `failed_attempts = 0` / `locked_until = NULL` へ戻した |
| 検証用に作って消したチケット2件 | 削除まで検証の一部。控えに無い `seq` を落とす復元スクリプトでも二重に担保 |
| ヘッドレス Chrome のプロファイル・検証スクリプト・スクリーンショット | スクラッチパッド。セッション終了で消える |
| `make build` の埋め込み成果物 | `make clean-webui` |

---

## 手順17c（チケットの外部参照と子チケットの追加）— 2026-08-27、`feature/step-17c-ticket-references`

`DbDesign.md` 6.12 の `ticket_reference` を新設し、`ApiDesign.md` 9.10.2 の4本と、
`GuiDesign.md` 5.5 の「コード」「参考リンク」の2セクションを実装した。
**利用者の要望で「子チケットを追加」も同じ手順に含めた**（新しい API は不要で、
9.3 の `POST /tickets` に `parent_seq` を添えるだけ）。

**設計文書の改訂を先に9件当てきってからコードへ入り、実装中の設計相談ゼロで通した**
（#43 の型。7回目）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/migrations/0016_ticket_reference.sql` | `DbDesign.md` 6.12 のDDLを転記。`kind` の CHECK と、`code`→`repository` / `doc`→`url` の必須2本 |
| `server/internal/store/queries/reference.sql` | List / Get / Create / Update / Delete / NextSortOrder の6本。**NULL 可の項目は `CASE WHEN <列>_set`**（`UpdateTicket` と同じ形） |
| `server/internal/httpapi/v1/references.go` | 4ハンドラ＋検証＋`activity` の記録＋要約の組み立て |
| `server/internal/httpapi/v1/references_test.go` | 単体33件（検証・404・`immutable_field`・要約・詳細への同梱） |
| `server/internal/httpapi/v1/references_integration_test.go` | 実DB結合。**CHECK の実在をアプリ迂回の直 INSERT で見る**・並び順・CASCADE・`version` 不変 |
| `client/src/api/references.ts` | 4本のエンドポイントと `codeSummary` / `docSummary`（**サーバが `activity` に載せる要約と同じ形**） |
| `client/src/components/ReferenceModal.vue` | 参考リンクの追加・編集（`RepositoryModal` と同じ型。`doc` だけを扱う） |
| `client/src/lib/url.ts` | `isWebUrl` の**正本**。5.5 の外部参照と 5.9.1 のリポジトリが共有する |

### 変えたファイル

| ファイル | 変更 |
|---|---|
| `server/internal/httpapi/v1/ticket_view.go` | `ticketDetailView.References` を足し、`buildTicketDetail` が `ticketReferencesFor` を通す（一覧APIと同じ関数） |
| `server/internal/httpapi/v1/routes.go` | 4本を登録。`GET` は `ticket.view`、更新系は `ticket.edit` |
| `server/internal/httpapi/v1/fake_test.go` | `ticketFakeState` に参照を足し、**create / update / delete をスライスへ実際に適用する**（「書いてから読み直す」形を測るため） |
| `server/cmd/pb/dev_seed.go` | `devReference` と `seedTicketReferences`、`validate` に `kind` ごとの必須検証 |
| `deploy/dev/seed/dev-data.yaml` | demo-9 に `code` 3件＋`doc` 1件、demo-10 に `doc` 2件（**うち1件は SSH 形式**でリンクにならない側） |
| `client/src/components/TicketDetailPane.vue` | 2セクション・`[⋯]` の3項目・子チケット追加。`members` の型を `ProjectMember` へ広げた |
| `client/src/components/NewTicketModal.vue` | `lockParent`（親を固定し「なし」を出さない） |
| `client/src/pages/ProjectSettingsPage.vue` | ローカルの `isWebUrl` を消して共有関数へ差し替え（2行） |
| `docs/openapi.yaml` | 4本のパス、`TicketReference` 系4スキーマ、`TicketReferenceID` / `TicketReferenceNotFound`、`TicketDetail.references` |

### 設計文書の改訂（コードより先に当てた9件）

| | 対象 |
|---|---|
| D-1 | `GuiDesign.md` 5.5 の表に**編集**列。行操作を `[編集]` / `[削除]` の文字列に |
| D-2 | `GuiDesign.md` 6.3 に「外部参照の削除」 |
| D-3 | `GuiDesign.md` 5.5 / 5.9.1 のリンク規則を `https://` または `http://` へ。正本は `lib/url.ts` |
| D-4 | `DbDesign.md` 5.2 の「0014 と 0016 は未適用」→「当たっているが、まだ使われない」 |
| D-5 | `ApiDesign.md` 9.5.1 の「`references` は 17c まで空」→ 実数 |
| D-6 | `GuiDesign.md` 5.5 に「子チケットの追加」と「`[⋯]` メニューの項目」、ワイヤーの更新 |
| D-7 | `GuiDesign.md` 5.5 と設計原則6 の表から**書き手のアイコンを削除** |
| D-8 | `ApiDesign.md` 9.1.1 / 9.10.2 に `activity` への記録（`action='update'`、`field='reference.*'`） |
| D-9 | `ApiDesign.md` 9.10.2 の `created_by` の位置づけを弱め、**committer ではない**と明記 |

### 検証結果

| 層 | 結果 |
|---|---|
| `make test` | `TestExpiresAtFormat` 以外すべて PASS。**同じ失敗が `develop` でも出ることを一時 worktree で確認**（起票済みの時限式テスト。#77 の型） |
| `make test-db` | **129件 PASS / FAIL 0**（うち `TestTicketReferenceIntegration` が18件） |
| 実サーバ（Bearer） | **27件 PASS。** `/me/tokens` のトークンで `code` を積む経路を通した——**画面に追加の導線が無いのはこの経路だけが書き手だから**（9.10.2）。作った行は最後にすべて削除し、件数が検証前へ戻ったことを確認 |
| ブラウザ（読み取り） | **32件 PASS。** 1440 / 1100 / 900px の3幅で `getBoundingClientRect()` 実測。セクションの出し分け・`[削除]`/`[編集]` の並び・リンクの `target`/`rel`・アイコン非表示・`[⋯]` の3項目・下線の位置 |
| ブラウザ（状態変更） | **22件 PASS。** 追加 → 編集 → 削除（**キャンセルで消えないことを先に確認**してから削除）→ `[⋯]` からの追加 → 子チケットの作成 |
| スクリーンショット | **10枚を目で確認**（1440 / 900px × 3チケット、モーダル2枚、メニュー1枚、状態変更後2枚） |

### 検証が拾った実装の欠陥（2件）

| 欠陥 | 気づいた場所 |
|---|---|
| **空白だけの `repository` が列に入る。** `optionalStringField` は空文字だけを null と同じ扱いにするので `"  "` が素通りし、DB の `CHECK` は `NOT NULL` しか見ない | 単体テスト（`PATCH は必須項目を空にできない` の空白ケース） |
| **子チケットの追加で親を「なし」に変えられた。** 子を作るつもりで開いてトップレベルのチケットができる | ブラウザ（状態変更）の「親はこのチケットに固定されている」 |

### 検証側の誤り（2件。実装は正しかった）

| 誤り | |
|---|---|
| **期待値に `子チケット (0)` を含めた。** seq=9 は子を持たず、5.5 どおりセクションごと出ない | #33（対象を数え上げてから期待値を書く）の再発 |
| **`?.click() \|\| …` でメニューを開いて閉じていた。** `click()` は `undefined` を返すので2つ目の式も評価される | #46 の系統。**1回だけ押す形に書き直した** |

### 片付けた資源

| 残るもの | 戻し方 |
|---|---|
| 検証で作った外部参照7件 | スクリプトが最後に全部 DELETE し、件数が検証前（seed の6件）へ戻ったことを確認 |
| 検証で作った子チケット2件 | API で削除。`ticket` は seed の15件へ戻った |
| 検証が書いた `activity` 9行 | **seed は `activity` を書かない**ので `field LIKE 'reference%'` と孤児の `create` 行を削除。0行へ戻した |
| 検証で発行した API トークン1本・ログインセッション9本 | トークンは API で失効、セッションは `revoked_at` を立てた |
| Cookie jar・トークンの平文ファイル | 削除（セッショントークンの平文が残るため） |
| **前セッション（17b）が置き忘れたヘッドレス Chrome** | プロファイル名 `pb-cdp-` で完全一致させて終了。プロファイルのディレクトリも削除 |
| `make build` の埋め込み成果物 | `make clean-webui` |

## 手順18a — コメント・DoD・チケット間リンクのAPI（2026-08-27、`feature/step-18a-comment-dod-link-api`）

`ApiDesign.md` 9.8 / 9.9 / 9.10.1 の実装と、9.5.1 の `dod` / `links` の実体化。**API のみ**で、
画面（`GuiDesign.md` 5.5 の3セクション）は 18b。**マイグレーションは不要**だった——
`comment`（0007）・`ticket_link`（0006）・`dod_item`（0014）はいずれも適用済み。

### 設計文書の改訂（コードより先に当てきった）

| 文書 | 改訂 |
|---|---|
| `ApiDesign.md` 9.8 | **応答の JSON 例を新設**（それまでフィールドの定義が無かった）。ページネーションと `ETag`、削除済みの扱い、`PATCH` の不変フィールド、`activity` の粒度 |
| `ApiDesign.md` 9.9 | 同上（DoD の応答例）。`config` / `evidence` / `origin` を返さない理由、`done` への遷移を止めない理由 |
| `ApiDesign.md` 9.10.1 | 重複の `409 already_exists`、`incoming` の削除、`ticket` に `type` を含めること、`PATCH` を持たない理由、画面が3種に絞ること |
| `ApiDesign.md` 9.14 | `self_link` を追加。`not_found` に `target_seq` / `in_reply_to` を追記。`immutable_field` を明記 |
| `ApiDesign.md` 9.15 | 手順18 を 18a / 18b へ展開 |
| `ApiDesign.md` 9.5.1 | 「`dod` / `links` は手順18 まで空」→「手順18a から実数」 |
| `Design.md` 11章 | 18a / 18b の分割と、横に割った理由（縦に割る案を却下した経緯を含む） |

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/comment.sql` | **追記**。一覧（窓関数で `total` と `MAX(updated_at)` を同時取得）・0件用の要約・1件取得・返信先の検証・部分更新・論理削除。`CreateComment` に `in_reply_to` を追加 |
| `server/internal/store/queries/dod.sql` | **新規**。一覧・1件取得・次の `sort_order`・作成・部分更新（`satisfied_set` で3列を束ねる）・削除 |
| `server/internal/store/queries/link.sql` | **新規**。双方向の一覧（`UNION ALL` ＋ `direction_rank`）・1件取得・作成・重複検査・削除 |
| `server/internal/httpapi/v1/ticket_scope.go` | **新規**。子資源の共通の入口（`{key}` → `project_id` → `{seq}` → `ticket_id`）と `origin` の決め方 |
| `server/internal/httpapi/v1/comments.go` | **新規**。`GET`/`POST`/`PATCH`/`DELETE`、`ETag`、要約の組み立て |
| `server/internal/httpapi/v1/dod.go` | **新規**。`GET`/`POST`/`PATCH`/`DELETE`、`phase_2_only` の弾き分け |
| `server/internal/httpapi/v1/links.go` | **新規**。`GET`/`POST`/`DELETE`、`self_link` と重複の 409 |
| `server/internal/httpapi/middleware/authz.go` | **`RequireAnyProjectPermission` を新設**（OR）。`RequireProjectPermission` は共通関数へ委譲（振る舞いは不変） |
| `server/internal/httpapi/v1/routes.go` | ルート11本 |
| `server/internal/httpapi/v1/ticket_view.go` | `dod` / `links` を `[]any{}` から実体へ |
| `server/cmd/pb/dev_seed.go` | `devDoDItem` / `devComment` / `devLink` と検証・投入の3関数 |
| `deploy/dev/seed/dev-data.yaml` | コメント5件・DoD 6件・リンク2件 |
| `docs/openapi.yaml` | パス6本・パラメータ3・応答4・スキーマ12（約620行） |
| テスト | `comments_test.go`（26）/ `dod_test.go`（17）/ `links_test.go`（13）/ `comment_dod_link_integration_test.go`（26のサブテスト） |

### 検証結果

| 層 | 結果 |
|---|---|
| `make sqlc` / `go build` / `go vet` | 通過 |
| 単体（`make test`） | **新規56関数・実行64件が PASS。** 失敗は `TestExpiresAtFormat` 1件のみで、**`git worktree add <tmp> develop` で `develop` でも同じく落ちることを確認**（起票済みの時限式。私の変更と無関係） |
| 実DB結合（`make test-db`） | **全156件 PASS**（うち新規26件）。実DBでしか測れないもの——`ORDER BY`/`LIMIT`/`OFFSET`、`UNION ALL` の双方向、`uq_ticket_link`、`ck_ticket_link_diff`、`satisfied_set` の3列、3表の `CASCADE`、`trg_comment_updated` が論理削除で動くこと |
| 実サーバ（curl） | **41件 PASS**。4つのデモアカウントで権限差を実測。CSRF・論理削除・双方向リンク・重複の409・許可外 `sort` の422 |
| seed | `make dev-reset` を実行（利用者の承認）。コメント5・DoD 6・リンク2 が入り、**API 経由で定義順に並ぶことと、`demo-9` に `outgoing` と `incoming` が1つのリストで返ることを実測** |
| `npm run typecheck` | `make gen-api` で型を生成し直して通過 |

### 検証で作った資源の後始末

| 資源 | 後始末 |
|---|---|
| 実サーバ検証が作ったチケット2件と、その配下のコメント・DoD・リンク | スクリプトの `finally` で毎回削除（子資源は `CASCADE`）。実行後に `comment` 5 / `dod_item` 6 / `ticket_link` 2＝seed のままであることを確認 |
| 結合テストが作ったプロジェクト（`cdl-*`） | `t.Cleanup` で削除。実行後に `key LIKE 'cdl-%'` が 0件であることを確認 |
| 切り分け用の一時 worktree（`develop`） | `git worktree remove` で削除。`git worktree list` が1件であることを確認 |
| ローカルサーバ（:8080） | `make stop-server` |
| スクラッチパッド（検証スクリプト・ログ） | セッションをまたいで残らないため放置。**`verify_18a.py` は 18b で使い回せる形にしていない**（画面の検証は別物） |
| `make build` の埋め込み成果物 | **該当なし**（18a は `client/` を触らないのでビルドしていない） |

### 未検証のまま残ったこと

- **`comment.create` / `ticket.edit` の負の側**——seed の4アカウントはすべてシステムロールが operator 以上で、
  実効権限の和にこの2つが必ず入る（`DbDesign.md` 7.3）。**16c から続く同じ制約**である。
  **`comment.delete_any` だけは負の側を作れており**（pm は持ち member は持たない）、OR の必要権限はそこで測れている

## 手順18b — チケット詳細の3セクションと遷移コメント欄（2026-08-27、`feature/step-18b-ticket-detail-sections`）

**`client/` だけの手順。** API は 18a ですべて通してあり、**新しいエンドポイントは1本も足していない**
（`docs/openapi.yaml` 不変、`server/` 不変）。生成ずみの型（`schema.d.ts`）をそのまま使った。

### 設計文書の改訂（コードより先に当てきった）

| 文書 | 内容 |
|---|---|
| `GuiDesign.md` 5.5 | **「完了条件（DoD）」「関連チケット」「コメント」の3小節を新設**。それまでワイヤーフレームの1行と対応表の1行しか無かった。`link_type` の日本語ラベル表（関連／重複／自先行／自後行）、`kind` の表示名表、返信の両向きリンク、0件のときの扱いを書いた |
| `GuiDesign.md` 5.5 | 「状態のドロップダウン」の末尾にあった「遷移にコメントを添える欄は手順17b では置かない」を、**「遷移にコメントを添える」小節**へ書き換えた |
| `GuiDesign.md` 5.5 | ワイヤーフレームの `← 手順18` マーカーを3つ外し、対応表の手順欄を `18` → `18b` に。「まだ実装していないセクションは見出しごと出さない」を**「実装済みは0件でも見出しを出す。ただしコードと子チケットだけは0件で消す」**の基準とともに書き直した |
| `GuiDesign.md` 6.3 | 破壊的操作の表に**3行**追加（コメント／完了条件／関連チケットの削除） |
| `GuiDesign.md` 6.1 | `Avatar` を実体化した旨と、**`TicketComments.vue` を切り出した基準**（自己完結の単位か、親が配列を所有しているか）を追記 |
| `Development.md` 8.2 | 検証の道具の落とし穴を**2件**追記（下記「新しく分かった環境の制約」） |

### 作ったファイル

| ファイル | 中身 |
|---|---|
| `client/src/api/comments.ts` | 9.8 の4本＋`commentKindLabels`（サーバの `commentKindLabels` と同じ語）・`commentKindOptions`・`defaultCommentKind` |
| `client/src/api/dod.ts` | 9.9 の4本。**一覧を呼ぶ画面は無い**（詳細応答の `dod` を使う）——`references.ts` と同じ位置づけ |
| `client/src/api/links.ts` | 9.10.1 の3本＋`linkLabel()` / `linkLabelTitle()` / `linkChoices`（画面の4択と API の3種の対応） |
| `client/src/components/Avatar.vue` | 人＝円／エージェント＝角丸四角（8.4.2）。表示名の先頭1文字。**サロゲートペアで割らない** |
| `client/src/components/TicketComments.vue` | コメントの取得・ページング・投稿・編集・削除・返信の両向きリンク。**自己完結の単位** |
| `client/src/components/TicketLinkModal.vue` | 関係の4択（2列グリッド）＋候補の絞り込み一覧。**編集は無い**（9.10.1 が `PATCH` を持たない） |
| `client/src/components/TransitionModal.vue` | 「`<from>` → `<to>` へ変更します。」＋任意のコメント欄 |

### 変更したファイル

| ファイル | 変更 |
|---|---|
| `client/src/components/TicketDetailPane.vue` | 3セクションの追加（完了条件・関連チケット・コメント）、遷移の確認モーダル化、`comment_count` の足し引き |
| `client/src/components/StatusDropdown.vue` | `select` の payload を `key` から **`TicketTransitionOption` そのもの**へ（モーダルが遷移先の `name` を要るため）。**選んでも即座には遷移しなくなった** |

### 検証

| 種類 | 結果 |
|---|---|
| `vue-tsc --noEmit` | 通過 |
| `make test`（Go） | `TestExpiresAtFormat` のみ FAIL。**`git worktree add <tmp> develop` で `develop` でも落ちることを確認**（起票ずみ・18b と無関係） |
| `make test-db` | **156件 PASS / 0 FAIL**（18a と同数。`server/` 不変なので変わらないことの確認） |
| ブラウザ（読み取り） | **48件 PASS / 0 FAIL** |
| ブラウザ（変更） | **49件 PASS / 0 FAIL** |
| スクリーンショット | 8枚を目で確認（1440 / 1100 / 900px の3幅、モーダル4種） |

**読み取り系で見たもの**——3セクションの見出しが1つずつ出る／完了条件は件数を出さない／コメントの見出しは
`comment_count`／DoD のチェック状態と並び／関連チケットの双方向とラベル（`関連` / `自後行`）と
主語つき `title`／相手側（demo-10）から見ると `自先行` になる／コメントの古い順・書き手・類型・
Markdown 描画・見出しの3段下げ／投稿欄が閉じた1行であること／0件のときの1行（demo-11）／
履歴の見出しをまだ出さないこと／**セクションの並びを2通りで確認**（子あり・コードなしの demo-8、
子なし・コードありの demo-9）／状態を選んでも即遷移せずモーダルが開くこと／`Esc` で状態が変わらないこと。

**変更系で見たもの**——DoD の追加（末尾に付く・追加欄が空へ戻る）／チェックの付け外しで
**`satisfied_at` と `satisfied_by` が API で入り、外すと NULL へ戻る**（画面に出さないので API を実測）／
本文のその場編集／関連の追加（`outgoing`）・**重複は 409 でモーダルに残る**（「同じ関連はすでに登録されています」）・
**`自後行` が相手側の `blocks` として入り、相手（demo-7）からは `自先行` に見える**／コメントの投稿・
類型の選択・末尾に並ぶこと・見出しの件数が増えること／**返信の両向きリンク**（`↩ 返信先:` と `返信 1件:`）と
押したときの強調／編集と「（編集済み）」／**遷移コメントが `経過` として並ぶこと**・コメント無しの遷移は
コメントを作らないこと／**論理削除で行が残り「削除されました」になり、見出しの件数だけ1つ減ること**。

**権限の負の側を16c 以来はじめて実測した。** `member@example.com`（project_member ＋ システム operator）は
`comment.delete_any` を持たないため、**他人のコメントに `[編集]` も `[削除]` も出ず、`[返信]` だけが出る**。
自分のコメントには3つとも出る。**`GET /me` から実効権限を組み立ててから期待値を書いた**（18a の反省）。

**`activity` が手順19 のために積むものを実測した**（検証の片づけ前に確認）。

| `action` | `field` | 件数 | 要約の例 |
|---|---|---|---|
| `update` | `comment` | 4 | `議論: PB18BVERIFY 返信のコメント（直した）` |
| `update` | `dod` | 4 | `済: PB18BVERIFY 追加した条件` |
| `update` | `link` | 2 | `relates demo-3` |
| `transition` | `status_key` | 2 | `todo` |

**遷移にコメントを添えても `activity` は `transition` の1行だけ**で、コメントの行は増えない
（`ApiDesign.md` 9.8 の規定どおり）。

### 検証で作った資源の片づけ

**検証の前に DB を JSON で控え、終了後に差分ゼロを確認した**（`dod_item` / `comment` / `ticket_link` の13行）。

- 作った DoD・コメント・リンク・`activity` を削除（`cleanup.sh <cutoff>` として書いた。何度でも走る）
- `demo-11` の `status_key` は検証内で `todo` へ戻した
- **戻せなかったもの：`ticket.version`**（demo-11 が 5 → 7）。遷移2回ぶんの単調増加で、
  **楽観ロックのカウンタなので実害は無い**（`sort_key` を戻せない 16c と同じ性質）
- ヘッドレス Chrome のプロファイル（セッショントークンの平文を含む）は `Chrome.close()` で毎回削除
- `make stop-server` / `make clean-webui` を実行し、`lsof -sTCP:LISTEN` で 8080 が空くことを確認

### 新しく分かった環境の制約（`Development.md` 8.2 へ写した）

1. **値を入れる `Runtime.evaluate` と押す `Runtime.evaluate` を分ける。** Vue は `:disabled` の DOM 反映を
   nextTick で行うので、**同じ evaluate で入力してすぐ押すと、まだ `disabled` のボタンを押す**。
   症状は「追加されない」で、**実装は正しいのに FAIL になる**（スモークで捕まえた）
2. **`Input.insertText` はカーソル位置へ入る。** CodeMirror を開いた直後のカーソルは**先頭**なので、
   追記したつもりの文字が本文の**前**に付く。置き換えたいときは `commands: ["selectAll"]` を先に送る

## 手順19a — stats / activity API（2026-08-28、`feature/step-19a-stats-activity-api`）

**手順19 を 19a（API）/ 19b（画面）に分けた**（`Design.md` 11.2.1 に従って着手時に見積もった。
利用者の承認、2026-08-28）。API は2本しかなく 17a（5本）・18a（11本）より小さいが、
**19b が作る画面は2か所ある**——ダッシュボード（`GuiDesign.md` 5.3）と、
**チケット詳細の「履歴」セクション**（同 5.5。Phase 1 に残る最後の未実装セクション）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/internal/store/queries/stats.sql` | **新規。** `GetProjectTicketStats`（9件の `FILTER` を1文にまとめた集計） |
| `server/internal/store/queries/activity.sql` | `ListActivity` / `SummarizeActivity` を追記（読み出しは 19a で初めて足した） |
| `server/internal/httpapi/v1/stats.go` | **新規。** `getProjectStats`、`staleThresholdDays = 14` |
| `server/internal/httpapi/v1/activity.go` | **新規。** `listProjectActivity`、`entity` の解析、`activityETag` |
| `server/internal/httpapi/v1/routes.go` | 2ルート（どちらも `RequireProjectPermission("project.view")`） |
| `server/internal/httpapi/v1/stats_test.go` | **新規。** 6件 |
| `server/internal/httpapi/v1/activity_test.go` | **新規。** 17件（サブテスト込み） |
| `server/internal/httpapi/v1/dashboard_integration_test.go` | **新規。** `TestDashboardIntegration` |
| `server/internal/httpapi/v1/routes_test.go` | ルート宣言の検証3件を追記 |
| `server/internal/httpapi/v1/fake_test.go` | フェイク3メソッド＋フィールド。**17c から崩れていた整列を `gofmt` で直した** |
| `docs/openapi.yaml` | 2パス＋`ProjectStats` / `Activity` / `ActivityList` |
| `docs/ApiDesign.md` | 9.13.1 / 9.13.2 の追記、9.15 の 19a / 19b 分割 |
| `docs/Design.md` | 11章に 19a / 19b の分割表 |

### 検証結果

| 層 | 件数 | 内容 |
|---|---|---|
| 単体 | 23件（実行26件） | 並び順の tie-break・ページャ・`entity` / `action` の絞り込みと 422・null になる3項目・ETag の一意性・閾値がクエリまで届くこと |
| ルート宣言 | 3件 | 2本とも `project.view`（`ticket.view` だけでは 403）・非メンバーは 404・`{seq}` 配下と衝突しない |
| 実DB結合 | `make test-db` 全157件 PASS（19a で1件増） | `FILTER` の数え分け・**未解決の `status_key`**・`overdue` / `stale` の境界（13日前と20日前）・SQL 側の tie-break・`project_id` の絞り込み |
| 実サーバ | 62件 | demo の実データを DB の実測と突き合わせ。4アカウントの認可・422 を9通り・ETag の一意性 |

**マイグレーションは不要**（`activity` は 0008 で適用済み。読み出しを足しただけ）。

#### 実DB結合でしか測れなかったもの

- `status_key = 'zzz_unknown'` のチケットが**どのカテゴリにも入らないまま `total` には入る**
  （`by_category` の合計 5 ≠ `total` 6）。`LEFT JOIN workflow_status` の帰結で、**合わせに行かない**
- `stale` の境界。**13日前は放置でなく、20日前は放置である**（閾値14）。
  **完了済み（`closed_at` あり）は 20日前でも数えない**
- `ORDER BY occurred_at DESC, id DESC` の tie-break が SQL 側で効くこと。
  フェイクではなく DB が並べた結果を見ている
- **`project_id` の絞り込み。** 別プロジェクトに1行置き、`total` が 5 のままであることを確かめた

#### 実サーバで分かった demo の実測値

```
by_category: todo 6 / in_progress 5 / review 0 / done 4    total 15  open 11
overdue 3   stale 0   unassigned 6   threshold_days 14
activity 21件（create 2 / delete 2 / update 17：comment 6・dod 9・link 2）
```

- **`review` が 0 でもキーが残る**ことを実データで確認した（demo は `simple` ワークフロー）
- **`stale` は 0 である。** seed の `updated_at` が投入時刻なので、`make dev-reset` の直後は
  必ずそうなる（境界は結合テスト側で測った。**利用者のデモデータは触っていない**）
- `delete` の2行はどちらも `entity_seq` / `entity_title` が `null` で、`entity_id` は残っていた
- **`transition` と `reference.code` / `reference.doc` の行は demo に無い**（18b の検証で作った分は
  片付けられている）。19b が要約文を作るときは、画面を操作して積む必要がある

### 検証で作った資源の片づけ

- 結合テストが作る2プロジェクトは `t.Cleanup` で `DELETE FROM project`（`activity` も `ticket` も CASCADE）
- **利用者の demo データは読むだけで、1行も変えていない。** `stale` を実データで発火させるには
  `updated_at` を古くする必要があるが、**結合テスト側で境界を測れるので触らないことにした**
- `make stop-server` を実行し、`lsof -sTCP:LISTEN` で 8080 が空くことを確認
- スクラッチパッドの検証スクリプト・ログ・一時 worktree を削除

### 気づいたこと

**`make test` の `TestExpiresAtFormat` は 19a と無関係に落ちる。** `git worktree add <tmp> develop`
で `develop` でも落ちることを確かめてから進めた（手順外の作業に起票ずみ）。

---

## 手順19b：プロジェクトダッシュボード・チケット詳細の履歴・タイムゾーン（2026-08-28）

`GuiDesign.md` 5.3 の新規ページ1枚と、詳細ペインへ足す**Phase 1 最後のセクション**（同 5.5）、
および `app_user.timezone` の反映（同 7.5）。**19a の API を2点変えている**（下記）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `client/src/pages/DashboardPage.vue` | ダッシュボード本体（5.3）。5本のAPIを `Promise.allSettled` で並列に投げ、**ブロック単位で4状態を持つ** |
| `client/src/components/StatCard.vue` | 集計カード（6.1 の `StatCard` を実体化）。`to` を渡すとリンクになる |
| `client/src/components/TicketActivity.vue` | 履歴セクション（5.5）。開閉と追記読み込みを持つ自己完結の単位。見出しは `#title` スロットで受ける |
| `client/src/api/dashboard.ts` | `getProjectStats` / `listActivity` / `ticketEntity`（9.13） |
| `client/src/lib/activity.ts` | **要約文18種類の正本**（9.13.2 の `field`）。2画面が同じ関数を通る |

### 変えたファイル

| ファイル | 変更 |
|---|---|
| `server/internal/store/queries/stats.sql` | `AND t.type <> 'epic'`（9.13.1） |
| `server/internal/store/queries/ticket.sql` | `overdue` / `stale` の条件（**`stats.sql` の写し**） |
| `server/internal/httpapi/v1/tickets.go` | `overdue` / `stale` の解析・検証・ETag の正規化 |
| `server/internal/httpapi/v1/tickets_test.go` | 既定値・422・ETag・解釈の4か所を拡張 |
| `server/internal/httpapi/v1/dashboard_integration_test.go` | **エピック（seq=7）を fixture に足し**、6項目すべてに当たる値にした。**`stats` と `GET /tickets` の件数一致**を9通り測る |
| `client/src/router/routes.ts` | `/p/:key` を実画面へ。`projectKey` を props で渡す |
| `client/src/pages/BacklogPage.vue` | 状態の箱に3系列（`optgroup`）＋期限の箱。`setExclusive` で排他 |
| `client/src/components/TicketDetailPane.vue` | 履歴セクション、`workflow` prop、`.block-title.bare` |
| `client/src/components/Avatar.vue` | `position: relative`（**欠陥修正**） |
| `client/src/lib/datetime.ts` | `setTimezone` / `currentTimezone`。`Intl.DateTimeFormat` の `formatToParts` |
| `client/src/stores/auth.ts` | `setSession` / `clear` でタイムゾーンを流す |
| `client/src/api/tickets.ts` | `statusCategoryLabels` / `statusCategoryOrder` / `overdue` / `stale` |
| `docs/ApiDesign.md` | 9.2.1（`overdue` / `stale`）・9.13.1（エピック除外） |
| `docs/GuiDesign.md` | 3.2・5.3（全面）・5.4（状態と期限のフィルタ）・5.5（履歴）・6.1・6.5・6.7・7.5 |
| `docs/openapi.yaml` | 2パラメータと `ProjectStats` の説明 |
| `docs/Development.md` | 8.6「seed に無い状態を作る（放置チケット・古い更新日時）」を新設 |

**マイグレーションは不要**（`activity` は 0008、`ticket` は 0006 で適用済み）。

### 検証結果

| 層 | 件数 | 内容 |
|---|---|---|
| 単体 | 全パッケージ ok | `TestExpiresAtFormat` のみ FAIL。**`git worktree add <tmp> develop` で `develop` でも落ちることを確認**（起票済みの時限式） |
| 実DB結合 | **157件 PASS / 0 FAIL** | `stats` のエピック除外、`stats` と `GET /tickets` の**件数一致9通り**（区分4・overdue・stale・unassigned・`due_within=0d` との差） |
| ブラウザ | **100件 PASS / 0 FAIL** | ダッシュボード44・導線26・履歴22・タイムゾーン8 |
| スクリーンショット | 9枚 | 1440/1100/900px × （ダッシュボード上部・要対応・履歴）。**すべて目で確認した** |

**ブラウザ検証の内訳**

- **ダッシュボード44件**：見出し（プロジェクト名＋画面名）／カード4枚の見出し・数・リンク先・同じ段に並ぶこと／4ブロックの存在／自分の担当と期限が近いの行・リンク先・**期限の昇順**・`YYYY-MM-DD` の形／最近の動きの10件・アバター・**絶対表記**・相対表記が出ていないこと・「以前の動きを読む」／要対応3行の文と件数と**チケット名を名指ししないこと**とリンク先／コンソールにエラーが無いこと／横スクロールしないこと
- **導線26件**：6通りのクエリで**バックログの総件数が `stats` と一致**すること、**フィルタの箱に選択が復元される**こと、`[解除]` が出ること、「すべて見る →」2本、状態の箱が3系列の `optgroup` に分かれること、**区分を選ぶと `open` が URL から消える**こと（排他）
- **履歴22件**：既定は畳んである／**開くまで `/activity` を呼ばない**（`fetch` を数えて確認）／開くと呼ぶ／ボタンが「開く」↔「閉じる」／絶対表記／ステータスが表示名で出る／担当が表示名で出る／**ULID が生で出ていない**／値が「」で囲まれる／子資源の要約が別行に出る／閉じて開き直しても読み直さない／**履歴が無くても見出しは出る**
- **タイムゾーン8件**：`Asia/Tokyo` 13:47 → `UTC` 04:47（−9h）→ `America/New_York` 00:47（−4h、夏時間）。**`date` 列（期限）は3つとも変わらない**（7.5 の表のとおり）

### 検証で作った資源と後始末

| 作ったもの | 後始末 |
|---|---|
| 検証用チケット `demo-18`（API で作成 → `updated_at` を20日前へ） | 削除。参考参照2件とコメント1件は FK の CASCADE で落ちた |
| `activity` 9行（作成・タイトル・優先度・担当・見積・期限・遷移・コード・参考リンク） | **控えた `max(id)` より後**を削除（ULID は単調増加） |
| `pm@example.com` のタイムゾーン | 検証の最後に `Asia/Tokyo` へ戻した（4アカウントとも `Asia/Tokyo`） |
| サーバ・Chrome・スクラッチパッド | 停止・削除。`make clean-webui` 実行済み |

**DB は JSON で控えて `diff` した——チケット15件、差分ゼロ。** `activity` は21行に戻った。
**利用者の demo データは1行も変えていない。**

### 気づいたこと

**seed が `activity` を書かないため、要約文18種類のうち5種類しか実データに無かった**
（dod・comment・delete・create・link）。画面を操作して13種類まで増やしてから確かめた。
`make dev-reset` の直後は「最近の動き」も「履歴」も空になる（起票済み）。

## Phase 1 完了一覧（手順1〜19b、`docs/PROGRESS.md` から移動、2026-08-28）

**Phase 1 は 2026-08-28 に完了した。** 本表は完了時点の `docs/PROGRESS.md` の Phase 1 表をそのまま移したものである。
手順番号の定義は `Design.md` 11章、各手順で作ったファイルと検証の全文は本書の該当節にある。


| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 完了 | 2026-08-11 | DB起動と `pb_app` ロール |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 完了 | 2026-08-11 | テーブル23個・権限28件・ロール5件 |
| 3 | `pb admin create` による初期管理者作成 | 完了 | 2026-08-12 | `system_role='administrator'` で入る |
| 4a | 共通基盤その1（sqlc導入・エラー形式・ページネーション・request_id・アクセスログ・ヘルスチェック・serve骨格） | 完了 | 2026-08-12 | `go test` ＋ `/healthcheck` と 2.5 形式の 404 |
| 4b | 共通基盤その2（認証ミドルウェア・監査ログ） | 完了 | 2026-08-12 | `go test` ＋ 実DBの認証結合テスト |
| 5a | `POST /auth/login`・`/auth/logout`・`GET /me`・実効権限の計算 | 完了 | 2026-08-12 | Set-Cookie 2種・`GET /me` が権限28件 |
| 5b | CSRF ミドルウェア（2.4）・レート制限（2.9） | 完了 | 2026-08-12 | `X-PB-CSRF` 無しで 403、11回目で 429 |
| 6a | 認可ミドルウェア（`RequirePermission` / `RequireProjectPermission`） | 完了 | 2026-08-13 | operator→403 / 非メンバー→404（**実サーバでの実地確認は手順9b で完了**） |
| 6b | 実効権限のセッションキャッシュ（マイグレーション 0012、`Design.md` 6.4.5） | 完了 | 2026-08-13 | 昇格しても12件のまま→破棄後28件 |
| 7 | client 雛形（Vite + Pinia + router + デザイントークン）と embed 疎通 | 完了 | 2026-08-13 | 単一バイナリで Vue が起動・SPA フォールバック |
| 7.5 | 開発用デモデータ（`pb dev seed`）・`make dev-reset` / `dev-seed` / `dev-info` | 完了 | 2026-08-13 | 4アカウント＋デモPJ・2回目は冪等 |
| 8a | `openapi.yaml` 初版・ドリフト検出テスト・API層・auth ストア・ログイン画面・ルーターガード | 完了 | 2026-08-15 | ブラウザ16件＋ドリフト検出テスト |
| 8b | AppShell・メインメニュー・権限による出し分け・ユーザーメニュー | 完了 | 2026-08-15 | ブラウザ39件（出し分け・テーマ・折りたたみ） |
| 9a | `GET /projects`（件数・進捗・ページネーション・ETag）と `check-key` | 完了 | 2026-08-15 | 実サーバで一覧・ETag・422・`check-key` 4判定 |
| 9b | `POST /projects`（テンプレート複製・単一トランザクション・5.4 形式の応答） | 完了 | 2026-08-15 | 201＋複製＋監査ログ、重複で 409、operator は 403 |
| 10a | プロジェクト一覧画面（表・ソート・ページング・4状態・アーカイブ切替） | 完了 | 2026-08-15 | ブラウザ53件（ソート・ページャ・4状態） |
| 10b | 新規プロジェクト作成モーダル（`check-key` の即時検証・作成・409） | 完了 | 2026-08-18 | ブラウザ32件（即時検証・debounce・409・3テンプレート） |
| 11a | `GET/PATCH /projects/:key`、`archive`/`unarchive`（API のみ） | 完了 | 2026-08-18 | 実DB結合＋実サーバで 409 / 422 / 403 / 404 と冪等な archive |
| 11b | プロジェクト設定画面（`GuiDesign.md` 5.9 の新設から。リポジトリ設定・メンバーのメール表示を含む） | 完了 | 2026-08-18 | ブラウザ43件（保存・409・アーカイブ・リポジトリ・メンバー・権限の出し分け） |
| 12a | `GET/POST /admin/users`（API のみ） | 完了 | 2026-08-18 | 実DB結合6件＋実サーバ22件（作成・409・422・403・CSRF・ETag） |
| 12b | ユーザー管理画面（一覧・追加モーダル・初期パスワードの1回表示） | 完了 | 2026-08-18 | ブラウザ128件（一覧・ソート・検索・列幅・追加・1回表示）。**利用者の実機確認の指摘6件を反映済み** |
| 13a | `GET/PATCH/DELETE /admin/users/:id`、password-reset、sessions/revoke、memberships（**API のみ**） | 完了 | 2026-08-20 | 単体39件＋実DB結合13件＋実サーバ42件（楽観ロック・3つのガード・冪等なメンバーシップ） |
| 14 | `GET /roles`、`GET /permissions` とロールと権限タブ（`lib/roles.ts` の全廃を含む） | 完了 | 2026-08-22 | 単体12件＋実DB結合8件＋実サーバ＋ブラウザ34件。`?scope=project` は権限不要 |
| 15a | `PATCH /me`・`POST /me/password` と自分の設定画面（基本情報／デザイン／セキュリティ） | 完了 | 2026-08-22 | 単体28件＋ブラウザ59件（保存・409・422・401・テーマの端末間同期）。**実DB結合12件は 15b の `make test-db` で実行済み** |
| 15b | `GET\|POST /me/tokens`・`DELETE /me/tokens/:id` とアクセストークン管理画面 | 完了 | 2026-08-22 | 単体18件＋実DB結合9件＋実サーバ19件＋ブラウザ53件。**画面で発行した平文で Bearer 認証が通ることを確認** |
| 16a | マイグレーション 0013・0014、タグAPI・スプリントAPI（`ApiDesign.md` 9.11 / 9.12）とプロジェクト設定のタグ・スプリントタブ | 完了 | 2026-08-23 | 単体38件＋実DB結合（`make test-db` 全74件）＋実サーバ8件＋ブラウザ68件（1440/900px の実測とスクリーンショット確認） |
| 16b | チケット一覧・作成・move API（9.2 / 9.3 / 9.4）、LexoRank、`activity` 記録、dev seed のチケット（**API のみ**） | 完了 | 2026-08-23 | 単体44件＋実DB結合（`make test-db` 全94件 PASS）＋実サーバ50件（フィルタ13種・ETag・422 の7通り・認可・並べ替えの境界）＋ブラウザ4件（seed のチケットで 16a のタグ「使用中」とスプリント「進捗」が実数になることを 1440/900px で確認） |
| 16c | バックログ画面（`GuiDesign.md` 5.4）とルート付け替え・メインメニュー | 完了 | 2026-08-23 | ブラウザ119件（読み取り92＋状態変更27）＋実DB結合95件＋タイムゾーン3種。**手順16 の完了条件（一覧・作成・並べ替え・グループ化）を満たした**。1440/900px の実測とスクリーンショット6枚を確認 |
| 16d-a | 種別を `epic`/`story`/`task` の3値へ縮小、`ticket.staged_at`（オンステージ）追加、`parent` の複数指定、`move` の `staged`、seed とタグ「バグ」 | 完了 | 2026-08-23 | 単体（チケット関連65件を含む全パッケージ）＋実DB結合97件＋実サーバ24件＋ブラウザ7件（1440/900px の実測とスクリーンショット3枚）。**設計文書の改訂を先に当てきってからコードへ入り、実装中の設計相談ゼロ** |
| 16d-b | **バックログ画面の作り直し**（上下二段・エピックのフィルタ化・ツリーの折りたたみ・ID完全形・説明欄テンプレート） | 完了 | 2026-08-23 | ブラウザ読み取り54件＋状態変更37件（1440/900px の実測とスクリーンショット7枚）＋`make test` 全パッケージ＋実DB結合97件。**手順16 の完了条件（一覧・作成・並べ替え・グループ化）を二段の形で満たした。** APIの追加・変更は無し（`openapi.yaml` 不変） |
| 17a | `GET\|PATCH\|DELETE /tickets/:seq`、`transition`、`transitions`（**API のみ**） | 完了 | 2026-08-26 | 単体71件＋実DB結合（`make test-db` 全110件 PASS）＋実サーバ62件（楽観ロック・不変フィールド3系統・`parent_cycle`・オンステージの規則・遷移の5段階・`ticket.delete` の 403 を実データで実測） |
| 17b | チケット詳細画面（`GuiDesign.md` 5.5）。**バックログの右に開くペイン**（2.2.1）とルート差し替え | 完了 | 2026-08-27 | ブラウザ129件（読み取り72＋状態変更57）。1440/1100/900px の3幅とメニュー展開・折りたたみの両方で実測し、**スクリーンショット10枚を目で確認**。`server/` は変更なし（`openapi.yaml` 不変）。**自動検証が全 PASS のまま崩れていた欠陥を3件、スクリーンショットで拾った** |
| 17c | 外部参照（マイグレーション 0016・`ApiDesign.md` 9.10.2）と詳細画面への組み込み。**子チケットの追加**（`[⋯]` と見出し右）を含む | 完了 | 2026-08-27 | 単体33件＋実DB結合（`make test-db` 全129件 PASS）＋実サーバ27件（Bearer トークンで `code` を積む経路）＋ブラウザ54件（読み取り32＋状態変更22）。1440/1100/900px の3幅で実測し、**スクリーンショット10枚を目で確認**。**検証が実装の欠陥を2件拾った**（空白だけの `repository` が列に入る／子の追加で親を「なし」に変えられた） |
| 18a | コメント・DoD・チケット間リンクのAPI（9.8 / 9.9 / 9.10.1）と、9.5.1 の `dod` / `links` の実体化。**API のみ** | 完了 | 2026-08-27 | 単体56件（実行64件）＋実DB結合（`make test-db` 全156件 PASS）＋実サーバ41件（4アカウントの権限差・論理削除・双方向リンク・重複の409・CSRF）＋seed の実データ確認。**マイグレーションは不要**（0006 / 0007 / 0014 は適用済み）。**検証の期待値の誤りを8件、実測で正した**（viewer が operator の権限を持つ） |
| 18b | チケット詳細の3セクション（`GuiDesign.md` 5.5 の完了条件・関連チケット・コメント）と遷移コメント欄 | 完了 | 2026-08-27 | ブラウザ97件（読み取り48＋変更49）。1440/1100/900px の3幅で実測し、**スクリーンショット8枚を目で確認**。`server/` は変更なし（`openapi.yaml` 不変）。**権限の負の側を16c 以来はじめて実測した**（`comment.delete_any` を持たない member@ に他人のコメントの削除が出ない）。検証は DB を JSON で控えて差分ゼロまで戻した |
| 19a | `GET /projects/:key/stats`・`GET /projects/:key/activity`（`ApiDesign.md` 9.13）。**API のみ** | 完了 | 2026-08-28 | 単体23件（実行26件）＋ルート宣言3件＋実DB結合（`make test-db` 全157件 PASS）＋実サーバ62件（demo の実データを DB の実測と突き合わせ・4アカウントの認可・422 を9通り・ETag の一意性）。**マイグレーションは不要**（`activity` は 0008 で適用済み）。**利用者の demo データは読むだけで1行も変えていない** |
| 19b | プロジェクトダッシュボード（`GuiDesign.md` 5.3）＋**チケット詳細の「履歴」セクション**（同 5.5）＋`app_user.timezone` の反映 | 完了 | 2026-08-28 | 単体（`make test` 全パッケージ）＋実DB結合（`make test-db` 全157件 PASS）＋ブラウザ100件（ダッシュボード44・導線26・履歴22・タイムゾーン8）。1440/1100/900px の3幅で実測し、**スクリーンショット9枚を目で確認**。**19a の API を2点変えた**（`stats` からエピックを除く／`GET /tickets` に `overdue` / `stale`）。**検証が実装の欠陥を2件拾った**（ダッシュボードの2ブロックがエピックを数えて導線と食い違う／`Avatar` の `.sr-only` が文書を縦に伸ばす）。demo の DB は JSON で控えて差分ゼロまで戻した |

### Phase 2 の再構成（2026-08-29、`docs/phase2-premise` / `docs/phase2-docs-design` / `docs/phase2-steps`）

**`docs/PROGRESS.md` の引き継ぎ「Phase 2 の着手前：手順一覧を進め方と目標を含めて再構成する」を消化した。**
コードは書いていない（文書のみ。ただし手順番号の振り直しに伴い `routes.go` のコメント1行と
`openapi.yaml`・`schema.d.ts` の記述1件を追随させた）。

| 段 | ブランチ | 変更した文書 |
|---|---|---|
| 1 | `docs/phase2-premise` | `Requirements.md` 10章（10.0 新設ほか13節）、`Design.md` 1.1（原則8） |
| 2 | `docs/phase2-docs-design` | `DbDesign.md` 8章、`ApiDesign.md` 10章（新設）、`GuiDesign.md` 5.10（新設）、`Design.md` 8章、`README.md` |
| 3 | `docs/phase2-steps` | `Design.md` 11章・付録A、`CLAUDE.md`、`PROGRESS.md`、`README.md` |

**判断の経緯は `history/decisions.md`** の「Phase 2 の前提反転」と「Phase 2 段2」にある。

### 検証

文書作業のため、**相互参照の機械的な点検**を検証とした。

```
`<文書>.md <番号>` の形の参照を全文から抜き、
各文書の見出しに同じ番号が存在するかを突き合わせる
```

| 段 | 検査した参照 | 未解決 |
|---|---|---|
| 1 | 217件 | 0 |
| 3（最終） | **1360件** | **0**（`history/` の14件を除く） |

**`history/` の14件は当時削除した章を指しており、これは想定どおり**である（あの2文書は
過去の状態の記録であり、参照専用・追記のみ）。

**検証スクリプト自体の点検を1度落とした。** 段2 で zsh の展開により実在する見出し15件を
`MISSING` と出し、危うく「文書が壊れている」と報告するところだった。**わざと壊れた入力を
1件通して検知できることを確かめる**手順を足して偽陽性と判明した（`LEARNINGS.md` #14）。

### 手順番号の振り直しで追随させたもの

**Phase 3 が 27〜33 から 29〜38 へずれた。** Phase 2 が7手順から9手順へ増え、
`knowledge`・承認キュー・DoD machine 型の3項目が Phase 2 から移ったためである。

| 追随先 | 内容 |
|---|---|
| `ApiDesign.md` 9.10.2 / `DbDesign.md` 6.12 / `GuiDesign.md` 5.5 | 「手順21・22」→「手順24・25」（エージェントアクターと MCP） |
| `docs/openapi.yaml` / `client/src/api/schema.d.ts` | 同上。`make gen-api` で再生成し、手で直した内容と一致することを確認した |
| `server/internal/httpapi/v1/routes.go` | 同上（コメント1行）。`make test` が通ることを確認 |
| `Design.md` 5.4 / 6.4.2 / 付録A、`ApiDesign.md` 4.4.2 / 9.11 | 権限カタログ「28件で確定」→ 0017 で30件になる旨 |

## 手順20 — ドッグフーディング用インスタンス（2026-08-29、`feature/step-20-stg-instance`）

**完了条件**：`make dev-reset` を実行しても stg のデータが残る → **満たした**（下記の実測）。

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `deploy/stg/compose.yaml` | base への上書き。`name: pb-stg` / `restart: always` / `ports: !override` で `127.0.0.1:5433:5432` / secrets を `../stg/secrets/` へ |
| `deploy/stg/init.sh` | 初回セットアップ。秘密を `openssl rand -hex 16` で生成 → `pb.env` を作る → DB を `--wait` で起動 → `make stg-migrate`。**冪等**（既にあるものは作り直さない） |
| `deploy/stg/build.sh` | 動作に必要な一式を出力する。`make sync-webui` → `go build` → `pb.env` / `secrets/app_database_url` を同梱 → `run.sh` と `README.txt` を生成。`OUT` で出力先を変えられる |
| `deploy/stg/pb.env.example` | 出力に同梱する設定のテンプレート。`PB_BIND=127.0.0.1:8081` / `PB_DATABASE_URL_FILE=./secrets/app_database_url` / `PB_HEALTH_SHOW_VERSION=true` / `PB_COOKIE_SECURE=false` |

### 変更したファイル

| ファイル | 内容 |
|---|---|
| `Makefile` | `STG_COMPOSE` / `STG_*_FILE` / `STG_GOOSE_DBSTRING_OWNER` と、`stg-init` / `stg-up` / `stg-down` / `stg-psql` / `stg-migrate` / `stg-build` / `stg-run` / `stg-stop` / `stg-admin-create` の9ターゲット |
| `.gitignore` | `deploy/stg/out/` と `deploy/stg/pb.env`（secrets は既存の `deploy/*/secrets/*` が覆う） |
| `docs/Design.md` | 4.1 のツリー、4.4 の全面補強（データの置き場・`restart: always`・常駐を保証しない・`localhost` で開く理由・出力一式の中身） |
| `docs/Development.md` | **11章を新設**（付録A の前。既存の章番号を動かさないため末尾に置いた）。3.3 と 3.4 に stg への導線を1行ずつ |
| `CLAUDE.md` | 開発コマンドに `stg-init` / `stg-build` / `stg-run` / `stg-migrate` を追記 |
| `deploy/stg/.gitkeep` | 削除（実ファイルが入ったため） |

### 検証（全 PASS）

**ブラウザ検証（7件、Chrome ヘッドレス 1440×900）**

```
PASS  stg にログインできる                          http://localhost:8081/projects
PASS  stg の /me が作成した管理者を返す              {"email":"<stg の管理者>","role":"administrator","name":"<表示名>"}
PASS  stg にプロジェクト pb を作れる                 status=201
PASS  stg の一覧の行に pb がある                     keys=[pb] names=[Project Backyard]
PASS  dev（127.0.0.1:8080）にログインできる
PASS  dev の /me は dev のアカウント                 admin@example.com
PASS  dev にログインしても stg のセッションが残る    <stg の管理者>（dev は admin@example.com のまま）
```

**最後の1件が `localhost` / `127.0.0.1` の使い分けの実証である。** 同じブラウザプロファイルで
dev にログインした後も、stg のセッションが生きている。

**完了条件（`make dev-reset` をまたぐ）**

| | before | after | 判定 |
|---|---|---|---|
| stg の `actor` / `app_user` / `project` / `ticket` / goose | 1 / 1 / 1 / 0 / 16 | 1 / 1 / 1 / 0 / 16 | **差分ゼロ** |
| stg の `project` 行 | `pb\|Project Backyard\|2026-08-28 17:02:52.96454+00` | 同左 | **差分ゼロ** |
| stg のサーバ | 動作中 | **動作中**（`{"status":"OK","version":"1.36.55"}`） | 落ちていない |
| stg のブラウザ操作 | — | ログイン・一覧表示ともに可 | PASS |
| dev | `ticket` 16 | `ticket` 15（＝seed の投入数） | **作り直された**（想定どおり） |

**データの実体が別の場所であることの実測**

```
pb-stg_pgdata            /var/lib/docker/volumes/pb-stg_pgdata/_data
project-backyard_pgdata  /var/lib/docker/volumes/project-backyard_pgdata/_data
```

**「出力を丸ごと別のパスへ置いて動く」ことの実測**

`deploy/stg/out` をスクラッチパッドへ `cp -R` し、そこで `nohup ./run.sh &` を実行して
`/healthcheck` が `{"status":"OK","version":"1.36.55"}` を返すことを確認した。**リポジトリの外で動く。**

**`make` ターゲットの通し確認**

`stg-init`（2回：`!override` 修正の前後）/ `stg-admin-create`（非対話で stdin から）/
`stg-build` / `stg-run` / `stg-stop` / `stg-up` / `stg-migrate`（冪等：`no migrations to run`）/
`stg-psql` を実際に実行した。**`stg-down` だけは実行していない**——コンテナごと消えて
`restart: always` の自動起動も止まるため（`--dry-run` で形だけ確認）。

`make test` は14パッケージすべて ok。

### 検証で踏んだ誤り（検証側の不備）

| 症状 | 原因 |
|---|---|
| `/me` が `{}` を返す（2件 FAIL） | 応答は `{actor:{email,…},permissions,projects}` で、`j.email` は存在しない（`ApiDesign.md` 3.1）。**`j.actor.email` が正しい** |
| プロジェクト作成が 403（1件 FAIL） | CSRF ヘッダ名を `X-CSRF-Token` と決めつけていた。**正しくは `X-PB-CSRF`**（`client/src/api/client.ts:18`、`ApiDesign.md` 2.4） |
| 「一覧に Project Backyard が出る」が実装前から PASS しうる（偽陽性） | `body.innerText` で見ており、**アプリ名そのもの**に当たっていた。`tr.row .key` を数える形に直した |

### 後片付け

- `make stg-stop` / `make stop-server` で検証用サーバを停止
- スクラッチパッドへ置いた出力一式の写しを削除
- `make clean-webui` を実行し、`git status` に汚れが無いことを確認
- **stg は残した**（本手順の成果物そのもの）。DB コンテナ `pb-stg-db-1`（`restart: always`）と、
  `deploy/stg/out/run.sh` を `nohup` で起動したサーバ（:8081）が動いている
- **個人情報の排除を同ブランチで行った**（経緯は `decisions.md`「個人情報の排除」）。
  `steps.md` の2行をプレースホルダ化して `--amend`、`PROGRESS.md` のローカルパスを一般化、
  過去102コミットを `git filter-repo` で書き換え、stg の管理者アドレスを変更、
  stg の `audit_log` 8行を削除した

## 手順21 — マイグレーション 0017（2026-08-30、`feature/step-21-document-migration`）

**完了条件**（`Design.md` 11章）：`make migrate` と `make test-db` が通る。**両方 PASS。**

### 作ったファイル

| ファイル | 内容 |
|---|---|
| `server/migrations/0017_document.sql`（新規） | `document` / `document_revision`（8.1.1）、`doc.view` / `doc.edit` と5ロールへの割り当て（8.1.4）、文書テンプレート4件（8.1.2） |
| `server/internal/store/gen/models.go` | `make sqlc` の生成物。`Document`（14列）と `DocumentRevision`（8列）が増えた。**クエリは書いていない**（文書API は手順22） |

### 直したファイル

| ファイル | 内容 |
|---|---|
| `docs/DbDesign.md` | 8.1.2 を全面改訂（slug / title / `sort_order` 列の追加、`knowledge` を避けた理由、見出しを置かない理由）、冒頭の状態行、5.2 のファイル一覧、「裁く」2か所 |
| `docs/Requirements.md` | 10.6.2 のテンプレート表と本文、3章の表。「裁く」4か所を「答えを与える／拠りどころになる」へ。**「うまくいったことも同じ場所に置く」を追記** |
| `docs/ApiDesign.md` | 10章の例 11か所（`conventions`→`rules` 9、`values`→`vision` 2）、10.2 の応答例のタイトル |
| `docs/GuiDesign.md` | 5.10 のワイヤーフレーム（タイトル4件＋枠線の整列）、URL 例、5.6.3 の「28件→30件 / 8つ→9つ」 |
| `docs/openapi.yaml` | 権限カタログの件数3か所 |
| `server/internal/httpapi/v1/roles_integration_test.go` | 28→30（2か所）、`project_viewer` の期待値に `doc.view` |
| `server/internal/httpapi/v1/auth_integration_test.go` | 28→30（2か所） |
| `server/internal/store/queries/authz.sql` | `ListPermissions` のコメント（正本が2か所になった） |
| `client/src/components/RolePermissionMatrix.vue` | コメント1行（権限は28件→30件） |

### 検証結果

**1. スキーマ（`DbDesign.md` 8.1.1 と突き合わせ）**

| 見たもの | 結果 |
|---|---|
| テーブル | `document` / `document_revision` の2つ |
| 索引 | 8本。`uq_document_slug` と `uq_document_template_slug` の**2本だけが `NULLS NOT DISTINCT`**（`pg_indexes.indexdef` で実測） |
| トリガ | `trg_document_updated` |

**2. 制約を実際に破った（7件。すべて1つのトランザクションで行い `ROLLBACK`、後に残存0件を確認）**

| # | 破ったもの | 結果 |
|---|---|---|
| 5-1 | テンプレートの同じ親の下に同じ `slug` | `unique_violation` |
| 5-2 | 実文書の**トップレベル**（`parent_id IS NULL`）で同じ `slug` | `unique_violation`。**`NULLS NOT DISTINCT` が効いていることの実測** |
| 5-3 | `is_template = true` なのに `project_id` あり | `check_violation`（`ck_document_template`） |
| 5-4 | 実文書なのに `template_key` あり | `check_violation` |
| 5-5 | `slug` に `_`（`_revisions`） | `check_violation`。**`ApiDesign.md` 10.1 が「`_revisions` を予約語にできる」根拠にしている前提** |
| 5-6 | `document_revision` の `(document_id, revision_no)` 重複 | `unique_violation` |
| 5-7 | 親を削除 | 子と `document_revision` が CASCADE で消えた |

**3. `updated_at` トリガ — 最初の測り方が間違っていた**

`BEGIN` → `INSERT` → `pg_sleep(1.1)` → `UPDATE` で `updated_at > created_at` を見たが `false`。
**トランザクション内では `now()` が固定される**ため、`created_at` と `updated_at` が同値になる（`pg_sleep`
では動かない）。**実装ではなく検証側の欠陥。** `updated_at` を1日前に置いてから `UPDATE` する形に
変え、**先に `ticket`（トリガが在ると分かっている表）で同じ方法を通してコントロールを取った**うえで
`document` を測り、PASS。

**4. シードと冪等性**

| 見たもの | 結果 |
|---|---|
| `permission` の総数 | **30件**（0010 の28 + `doc.view` / `doc.edit`） |
| `doc.view` / `doc.edit` | `category='doc'`、`sort_order` 35 / 36 |
| ロール割り当て | `doc.view` は5ロール全部、**`doc.edit` は `administrator` と `project_admin` の2つだけ**（8.1.4 の表と一致） |
| テンプレート4件 | `vision`(10) / `rules`(20) / `decisions`(30) / `learnings`(40)。`is_template=true`、`project_id IS NULL`、`template_key='default'`、`created_by IS NULL`、`version=1`、本文に `##` 見出しなし |
| 冪等性 | 0017 のシード部分をもう一度流しても `permission` 30 / テンプレート4 のまま。**`body_md` は上書きされない**（`ON CONFLICT DO NOTHING`） |

**5. テスト**

| コマンド | 結果 |
|---|---|
| `make test` | 全パッケージ ok |
| `make test-db` | **155件 PASS、FAIL 0、exit 0** |

**6. stg**

| コマンド | 結果 |
|---|---|
| `make stg-migrate` | `0017_document.sql` 適用、version 17 |
| 実測 | dev / stg ともに `permission` 30件。stg のテンプレート4件も同じ並び |
| `make stg-build` | `v2.2.58` のバイナリ一式を `deploy/stg/out/` へ出力（`make bump-minor` の**後**に実行し直した） |

### あとしまつ

- 検証で作った行は**すべて `ROLLBACK`**。終了後に `document` の検証用 ID が0件であることを確認
- スクラッチパッド（`verify_0017.sql` / `testdb.log` / 8.1.2 の下書き）を削除
- `make build` は行っていないので `make clean-webui` は不要
- **stg のマイグレーションと `deploy/stg/out/` は意図して残した**（`Development.md` 11.3 の運用そのもの。`out/` は `.gitignore` 対象）
