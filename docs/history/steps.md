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

## 進捗表から移した検証内容（手順1〜10b）

`PROGRESS.md` の Phase 1 表は、完了した手順の「検証方法」欄を1行に要約してある
（2026-08-18、`docs/progress-archive`）。**要約前の全文をここに保管する。**
過去にどこまで確認したかを正確に知りたいときはこちらを見ること。

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

## 手順外の作業（完了分）

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

