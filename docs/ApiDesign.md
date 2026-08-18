# Project Backyard (PB) API設計書

> 本書は PB の REST API に関する**唯一の正本**である。
>
> **文書体系**：`Requirements.md`（要件）→ `Design.md`（全体設計・認証認可）→ `DbDesign.md` / 本書 / `GuiDesign.md`（領域別の正本）
>
> - 対象読者：サーバ／フロントエンド実装者（人間およびAIエージェント）
> - 関連：`DbDesign.md`（スキーマ）、`GuiDesign.md`（画面）、`Design.md` 6章（認証・認可）
> - 状態：**Phase 1 前半のAPIを確定**。チケット系・MCP系は未着手
> - 最終更新：2026-08-11（rev.2 文書間の整合）

---

## 目次

| 章 | 内容 | 状態 |
|---|---|---|
| 1 | 本書の範囲と方針 | 確定 |
| 2 | 共通仕様 | 確定 |
| **3** | **認証・セッションAPI** | **確定** |
| **4** | **自分自身に関するAPI（/me）** | **確定** |
| **5** | **プロジェクトAPI** | **確定** |
| **6** | **ユーザー管理API** | **確定** |
| **7** | **ロール・権限API** | **確定** |
| 8 | 画面とAPIの対応 | 確定 |
| 9 | チケットAPI | 未着手 |
| 10 | 実装順序と未解決事項 | 確定 |

---

# 1. 本書の範囲と方針

## 1.1 今回定義する範囲

`GuiDesign.md` の Phase 1 画面のうち、**最初に構築する3画面**に必要なAPIを優先して定義する。

| 画面 | 必要なAPI |
|---|---|
| ログイン（5.1） | 3章 |
| プロジェクト一覧・新規作成（5.2） | 5章 |
| ユーザー / 権限管理（5.6） | 6章・7章 |

加えて、全画面が起動時に依存する `GET /api/v1/me`（4章）を定義する。

チケット関連API（一覧・詳細・コメント）は9章として枠のみ置き、本改訂では定義しない。

## 1.2 設計方針

| # | 方針 | 帰結 |
|---|---|---|
| 1 | **リソース指向、動詞は最小限** | 状態遷移など名詞で表せない操作のみ `POST /:id/<action>` を許す |
| 2 | **権限判定はサーバが正本** | 全エンドポイントに必要権限を宣言し、ミドルウェアで検証する（`Design.md` 6.4.4） |
| 3 | **画面の1表示 = 1リクエスト を目指す** | 画面が必要とする情報を1回で返す。N+1 の往復を作らない |
| 4 | **エラーは機械可読に** | `code` による分岐を可能にし、メッセージ文字列でのマッチングを不要にする |
| 5 | **存在を隠す** | 権限のないリソースは 403 ではなく 404（`Design.md` 6.4.5） |
| 6 | **秘密は一度しか返さない** | パスワード・トークンの平文は発行応答に1回だけ含める |

## 1.3 OpenAPI

**本書は設計の正本である。** 仕様と判断根拠を残し、未実装の仕様（`ApiDesign.md` 9章のチケットAPI等）も含む。

**`docs/openapi.yaml` は実装済みAPIの現状を OpenAPI 形式で記述したものとする。** 設計の写しではない。APIを追加・変更したステップの成果物に含めて更新する（`Design.md` 3.3）。用途は次の4つ。

| # | 用途 |
|---|---|
| 1 | どのAPIがどの仕様で**実装済みか**を機械可読に残す |
| 2 | API試験ツール（Schemathesis / Bruno / k6 等）から読み込んで動作確認する |
| 3 | フロントエンドの型付きクライアントを生成する（`openapi-typescript`。`Design.md` 3.3） |
| 4 | Swagger UI / Redoc で開発者が閲覧する |

3が「実装済みの範囲のみ」を要求する。未実装のAPIを呼べる型が生えると、画面側が存在しないエンドポイントを叩くコードを書けてしまう。

**食い違いを見つけたときの扱い**

| 食い違い | 正 | とる行動 |
|---|---|---|
| `openapi.yaml` と実装 | **実装** | `openapi.yaml` を実装に合わせる（記述の更新漏れ） |
| **本書と実装** | どちらとも限らない | 実装の誤りなら実装を直す。設計変更が必要なら**先に本書を直す**（`CLAUDE.md` 絶対規則3） |
| 本書と `openapi.yaml` | — | 上の2つに分解して判断する。この対比だけでは意味を持たない（本書は未実装を含み、`openapi.yaml` は含まないため、差があること自体は正常） |

改訂前は「本書とOpenAPIが食い違った場合はOpenAPIを正とする」としていた。これはyamlからサーバを生成する前提（スペックファースト）の記述であり、`Design.md` 3.3 でその方式を採らないと決めた時点で前提が崩れている。またその規定のままでは、実装のずれが自動的に「正しい」ことになり、設計文書側の是正が起きない（`CLAUDE.md` 絶対規則3と衝突する）。

---

# 2. 共通仕様

## 2.1 ベースURLとバージョニング

```
http://localhost:8080/api/v1
```

- バージョンはパスに含める。`v1` の破壊的変更が必要になった時点で `v2` を並置する
- MCPサーバは別系統（`/mcp/<project_key>`）であり、本APIとエンドポイントを共有しない（`Requirements.md` 10.10.1）

## 2.2 リクエスト／レスポンス形式

| 項目 | 規約 |
|---|---|
| Content-Type | `application/json; charset=utf-8` |
| フィールド命名 | **snake_case**。DBスキーマ・MCPツールと表記を統一する。`jsonb` 列に対応するフィールドも `_json` 接尾辞を付けない（`DbDesign.md` 4.7） |
| 日時 | ISO8601 UTC（`2026-08-11T09:03:12Z`）。`DbDesign.md` 4.1 の格納形式と一致 |
| 日付 | `2026-08-11` |
| ID | ULID 26文字（`01K2F8...`） |
| 真偽 | JSON boolean（DB も `boolean` 型のためそのまま対応） |
| null | 「値がない」を表す。フィールド自体の省略と区別する（PATCH で意味が変わるため） |
| 文字コード | UTF-8 のみ |

**フロントエンドは snake_case のまま扱う。** 変換層を挟むとAPIドキュメントとコードの対応が読み取りにくくなるため、TypeScript の型もそのまま snake_case で生成する。

## 2.3 認証

2方式を受け付ける。いずれも `DbDesign.md` 6.2 の `access_token` テーブルで検証する。

| 方式 | 用途 | 送出 |
|---|---|---|
| Cookie | ブラウザ（Web UI） | `Cookie: pb_session=pb_sess_xxxxx`（HttpOnly / SameSite=Lax / Secure） |
| Bearer | CLI・スクリプト | `Authorization: Bearer pb_api_xxxxx` |

サーバはトークンを SHA-256 でハッシュ化して `access_token.token_hash` を引く。**平文はDBにもログにも記録しない。**

未認証時は `401 unauthenticated` を返す。

## 2.4 CSRF

**Cookie認証の状態変更系リクエスト（POST/PATCH/PUT/DELETE）にのみ** CSRF トークンを要求する。Bearer 認証では不要。

```
Cookie:  pb_csrf=<random>          ← HttpOnly ではない（JSから読む）
Header:  X-PB-CSRF: <同じ値>
```

不一致時は `403 csrf_failed`。ログイン応答時に `pb_csrf` を発行する。

## 2.5 エラー形式

```json
{
  "error": {
    "code": "validation_failed",
    "message": "入力内容に誤りがあります",
    "details": [
      { "field": "key", "code": "already_exists", "message": "このプロジェクトキーは使用されています" }
    ],
    "request_id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
  }
}
```

- `message` は**そのまま画面に出せる日本語**とする。フロントで文言を組み立てない
- `details` はフィールド単位のエラー。フォームの各入力欄に紐づける
- `request_id` は `audit_log.request_id`（`DbDesign.md` 6.8）および構造化ログ（`Design.md` 10.1）と突き合わせられる
- `retry_after_sec`（整数、任意）は再試行までの秒数。`account_locked`（3.1）と `rate_limited`（2.9）で返す

```json
{
  "error": {
    "code": "account_locked",
    "message": "ログインの失敗が続いたため、アカウントを一時的にロックしました。しばらくしてからやり直してください",
    "retry_after_sec": 842,
    "request_id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
  }
}
```

**再試行までの秒数を `details` ではなく本体の任意フィールドに置く。** `details` は
`{field, code, message}` の配列であり、特定の入力欄に紐づかない数値を載せる場所がない。
フロントが「あと N 分」を自前で組み立てられるよう、文言ではなく数値のまま返す。
同じ値を `Retry-After` ヘッダ（2.9）にも入れる。

### 2.5.1 HTTPステータスとエラーコード

| Status | code | 意味 |
|---|---|---|
| 400 | `bad_request` | 形式不正、パースエラー |
| 401 | `unauthenticated` | 未認証・トークン失効 |
| 401 | `invalid_credentials` | ログイン失敗（**ユーザー不在と誤パスワードを区別しない**） |
| 403 | `forbidden` | 権限不足 |
| 403 | `csrf_failed` | CSRFトークン不一致 |
| 404 | `not_found` | 資源なし、または閲覧権限なし（1.2-5） |
| 405 | `method_not_allowed` | パスは存在するが、そのメソッドを受け付けない |
| 409 | `conflict` | 状態競合（`If-Match` 不一致など。2.8） |
| 409 | `already_exists` | 一意なキーが既に使われている（プロジェクトキーなど。5.3） |
| 409 | `last_administrator` | 最後の管理者を降格・無効化・削除しようとした |
| 409 | `self_modification_forbidden` | 自分自身のロール変更・削除 |
| 422 | `validation_failed` | 入力値の検証エラー（`details` を伴う） |
| 423 | `account_locked` | ログイン失敗回数超過によるロック |
| 429 | `rate_limited` | レート制限（`Retry-After` ヘッダを伴う） |
| 500 | `internal_error` | サーバ内部エラー |

## 2.6 一覧のページネーション

```
GET /api/v1/projects?page=1&per_page=25
```

```json
{
  "items": [ ... ],
  "page": 1,
  "per_page": 25,
  "total": 48,
  "total_pages": 2
}
```

| 項目 | 規約 |
|---|---|
| 既定 | `page=1`、`per_page=25` |
| 上限 | `per_page` は最大 200 |
| ソート | `sort=updated_at&order=desc`。許可する項目はエンドポイントごとに列挙 |
| 総件数 | 常に返す。Phase 1 の規模では `COUNT(*)` のコストは問題にならない |

**カーソルページネーションは採らない。** 対象データ量が小さく、画面が「48件中 1-25件」のような表示を必要とするため（`GuiDesign.md` 5.4）。

**範囲外・解釈不能な値は既定値へ丸めず、422 `validation_failed` を返す。** `details` に項目ごとの誤りを載せ、フォームの各入力欄へ紐づけられるようにする（2.5）。`per_page` の上限超過も同様に扱い、黙って 200 へ丸めない。**呼び出し側の誤りが表に出ないまま動き続けることを避ける**ためである。未指定の項目のみ既定値を使う。

| 入力 | 応答 |
|---|---|
| `?page=0` `?page=-1` `?per_page=0` | 422。`details[].field` に `page` / `per_page` |
| `?per_page=201` | 422（200 へ丸めない） |
| `?page=abc` | 422 |
| `?sort=<許可リスト外>` `?order=<asc,desc 以外>` | 422 |
| 未指定 | 既定値（`page=1` / `per_page=25` / エンドポイントごとの既定 `sort`・`order`） |

## 2.7 差分取得（ETag）

一覧系 GET は `ETag` を返す。`Requirements.md` 1章の「短間隔ポーリングによる差分取得」に対応する。

```
→ GET /api/v1/projects
← 200 OK / ETag: W/"proj-3-1723372992000000000"

→ GET /api/v1/projects / If-None-Match: W/"proj-3-1723372992000000000"
← 304 Not Modified
```

ETag はプロジェクト集合の `MAX(updated_at)` と件数から生成する。Phase 1 ではポーリングを実装しないが、**応答ヘッダだけ先に用意しておく**（後から追加すると全エンドポイントの改修になるため）。

**弱い検証子の `W/` は引用符の外に置く**（RFC 9110 8.8.3 の `entity-tag = [ weak ] opaque-tag`）。`"W/proj-…"` と内側に書くと、値そのものが `W/proj-…` という文字列の**強い**検証子になり、弱い比較の意味を失う。

## 2.8 楽観ロック

更新系は `version` 整数を持つリソースについて、`If-Match` による楽観ロックを行う。

```
PATCH /api/v1/projects/my-app
If-Match: "3"
```

不一致時は `409 conflict`。Phase 1 で対象とするのは `project` と `app_user` のみ。チケットは9章で扱う。

**`If-Match` を伴わない更新は受け付けない。** 省略時は `422 validation_failed` とし、`details` に
`{ "field": "If-Match", "code": "required" }` を載せる。ヘッダを付け忘れた実装が黙って上書きできると、
楽観ロックが「掛かっているつもり」の状態になるため。`GET` の応答が `version` を返しているので、
呼び出し側が値を持っていないことはない。

**状態を切り替えるだけの操作（5.6 の archive / unarchive）は `If-Match` を要求しない。** 冪等であり、
競合しても失われる編集内容がないためである。ただし `version` は他の更新と同じく +1 する。

## 2.9 レート制限

| 対象 | 制限 |
|---|---|
| `POST /auth/login` | IPあたり 10回/分、アカウントあたり 5回/15分（超過で `account_locked`） |
| その他の認証済みリクエスト | アクターあたり 600回/分 |

応答ヘッダ：`X-RateLimit-Limit` / `X-RateLimit-Remaining` / `Retry-After`

## 2.10 監査ログ

以下の操作は `audit_log`（`DbDesign.md` 6.8）に必ず記録する。

`login.success` / `login.failure` / `logout` / `password.change` / `password.reset` /
`token.issue` / `token.revoke` / `session.revoke` / `user.create` / `user.update` /
`user.delete` / `role.change` / `project.create` / `project.archive` / `permission.denied`

## 2.11 ヘルスチェック

```
GET /healthcheck
```

```json
{ "status": "OK" }
```

**唯一 `/api/v1` の外に置くエンドポイントである。** 監視・オーケストレータから叩くものであり、APIのバージョニング（2.1）に従わせる意味がないため。

| 項目 | 規約 |
|---|---|
| 認証 | **不要**。CSRF・レート制限・認可の対象外 |
| 副作用 | 無し。**DBへは接続しない** |
| 応答 | 常に `200` と `{"status":"OK"}`。異常時はプロセスが応答しないことで検知する |
| バージョン | 設定 `PB_HEALTH_SHOW_VERSION=true` のときのみ `version` を加える。**既定は false** |
| 用途 | compose の `healthcheck`、Kubernetes の `livenessProbe` |
| アクセスログ | 既定では出さない（`Design.md` 10.1） |

```json
{ "status": "OK", "version": "1.4.4" }   ← PB_HEALTH_SHOW_VERSION=true のとき
```

**バージョンを既定で返さないのは、未認証の呼び出し元に対する情報開示だからである。** 稼働中のバージョンを知られると、既知の脆弱性との突き合わせを許す。デプロイ後の確認に使いたい環境でのみ有効にする。

**DBの疎通は見ない。** DB断でプロセスを再起動しても復旧しないため、liveness で落とすと不要な再起動ループを招く。DBを含む可用性確認が必要になった時点で `/ready` を別に足す（Phase 1 では作らない）。

**SPAフォールバックの例外にあたる。** `Design.md` 3.4 は「`/api` `/mcp` 以外で未知のパスは `index.html` を返す」としているため、`/healthcheck` を明示的な例外として扱う。

---

# 3. 認証・セッションAPI

## 3.1 `POST /api/v1/auth/login`

**必要権限**：不要

```json
// Request
{ "email": "tanaka@example.com", "password": "••••••••••••" }
```

```json
// 200 OK
{
  "actor": {
    "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
    "kind": "user",
    "display_name": "田中",
    "email": "tanaka@example.com",
    "system_role": "administrator",
    "locale": "ja",
    "timezone": "Asia/Tokyo",
    "must_change_password": false
  },
  "permissions": ["project.view", "project.create", "ticket.view", "..."],
  "projects": [
    { "id": "01K2...", "key": "my-app", "name": "社内タスク管理の刷新",
      "role": "project_admin", "permissions": ["ticket.close", "..."] }
  ],
  "expires_at": "2026-08-25T09:03:12Z"
}
```

```
Set-Cookie: pb_session=pb_sess_...; HttpOnly; SameSite=Lax; Path=/; Max-Age=1209600
Set-Cookie: pb_csrf=...; SameSite=Lax; Path=/; Max-Age=1209600
```

**ログイン応答に `GET /me` と同じ内容を含める。** ログイン直後に必ず権限が必要になるため、往復を1回減らす（設計方針3）。

**2つの Cookie の `Max-Age` は揃える。** `pb_csrf` をセッションCookie（`Max-Age` なし）に
すると、ブラウザを閉じた時点で `pb_csrf` だけが消え、14日残る `pb_session` に対して
CSRF トークンが無い状態になる。以後すべての状態変更系が `403 csrf_failed`（2.4）になる。

| 状況 | 応答 |
|---|---|
| 入力の形式誤り（メール未入力・形式不正など） | `422 validation_failed`（`details` に項目ごとの誤り） |
| 認証失敗 | `401 invalid_credentials`「メールアドレスまたはパスワードが正しくありません」 |
| アカウント無効 | 同上（**存在を漏らさないため区別しない**） |
| ロック中 | `423 account_locked`（`error.retry_after_sec` と `Retry-After` ヘッダに残り秒数。2.5） |
| 失敗が閾値に達した回 | 同上。**その要求からロック中として扱う**（`Design.md` 6.3 の「5回連続で15分ロック」を満たした時点で `locked_until` が入るため） |
| 要パスワード変更 | `200` だが `must_change_password: true`。フロントは変更画面へ誘導する |

`Design.md` 6.2.1 のフロー（ダミーハッシュ検証によるタイミング攻撃対策、成功時の再ハッシュ）に従う。

## 3.2 `POST /api/v1/auth/logout`

**必要権限**：認証済み

現在のセッショントークンを失効（`revoked_at` を設定）し、Cookie を削除する。`204 No Content`。

## 3.3 `GET /api/v1/auth/providers`

**必要権限**：不要

ログイン画面のIdPボタンを動的生成するために使う（`GuiDesign.md` 5.1）。

```json
{
  "providers": [
    { "key": "local", "type": "local", "display_name": "メールアドレス", "sort_order": 0 }
  ]
}
```

Phase 1 では `local` の1件のみ。**Phase 3 で OIDC/SAML を追加しても、フロントの改修が不要になる**ようこの形にしておく（`Design.md` 6.2.3）。`config` や `secret_ref` は**絶対に返さない**。

---

# 4. 自分自身に関するAPI（/me）

## 4.1 `GET /api/v1/me`

**必要権限**：認証済み

全画面の起動時に呼ぶ。レスポンスは 3.1 の `200 OK` と同一構造。

フロントは `auth` ストアにこの内容を保持し、`can('ticket.create')` の判定とルーターガードに用いる（`GuiDesign.md` 7.1）。

**`permissions` はシステムロール由来の実効権限**、`projects[].permissions` は当該プロジェクトでの実効権限。`Design.md` 6.4.1 の式に従い、トークンスコープによる縮小を適用済みの値を返す。

## 4.2 `PATCH /api/v1/me`

**必要権限**：本人

```json
{ "display_name": "田中", "locale": "ja", "timezone": "Asia/Tokyo",
  "theme": "dark", "hue": "green" }
```

`theme` / `hue` は `GuiDesign.md` 8.11 のテーマ設定。`app_user` に列を追加して保持する。

**`email` と `system_role` は変更不可**（管理者が 6.4 で変更する）。送られた場合は無視せず `422` を返す。

## 4.3 `POST /api/v1/me/password`

**必要権限**：本人

```json
{ "current_password": "••••", "new_password": "••••••••••••" }
```

- 現在のパスワード検証に失敗 → `401 invalid_credentials`
- ポリシー違反（12文字未満等） → `422 validation_failed`
- 成功 → `204`。**現在のセッションを除く全セッションを失効**（`Design.md` 6.3）

## 4.4 `GET /api/v1/me/sessions` / `DELETE /api/v1/me/sessions/:id`

```json
{ "items": [
  { "id": "01K2...", "client_info": "Chrome / macOS", "issued_at": "...",
    "last_used_at": "...", "expires_at": "...", "is_current": true }
]}
```

`DELETE` で個別失効。`DELETE /api/v1/me/sessions`（IDなし）で現在のセッション以外を全失効。

## 4.5 `GET|POST|DELETE /api/v1/me/tokens`

CLI用のAPIトークン管理（`GuiDesign.md` 5.8）。

```json
// POST Request
{ "name": "CLI (MacBook)", "expires_in_days": 90, "scopes": ["ticket:read", "ticket:write"] }
```

```json
// 201 Created — token は「この応答でのみ」返る
{ "id": "01K2...", "name": "CLI (MacBook)", "token": "pb_api_9f3c...",
  "scopes": ["ticket:read","ticket:write"], "expires_at": "2026-11-09T09:03:12Z" }
```

`GET` の一覧では `token` を返さず、`token_prefix`（先頭8文字）のみ表示する。

---

# 5. プロジェクトAPI

## 5.1 `GET /api/v1/projects`

**必要権限**：`project.view`（自分がメンバーであるプロジェクトのみ返る。管理者は全件）

```
GET /api/v1/projects?status=active&sort=updated_at&order=desc&page=1&per_page=25
```

| パラメータ | 既定 | 説明 |
|---|---|---|
| `status` | `active` | `active` / `archived` / `all` |
| `sort` | `updated_at` | `name` / `key` / `updated_at` / `ticket_count` / `progress` |
| `order` | `desc` | `asc` / `desc` |

```json
{
  "items": [
    {
      "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
      "key": "my-app",
      "name": "社内タスク管理の刷新",
      "description": "既存のExcel管理を置き換える",
      "status": "active",
      "ticket_count": 48,
      "closed_count": 36,
      "progress": 0.75,
      "my_role": "project_admin",
      "updated_at": "2026-08-11T09:12:44Z"
    }
  ],
  "page": 1, "per_page": 25, "total": 3, "total_pages": 1
}
```

**`ticket_count` / `closed_count` / `progress` を一覧に含める。** `GuiDesign.md` 5.2 の表がこれらを列として持つため、プロジェクトごとに件数を問い合わせる N+1 を避ける（設計方針3）。サーバ側は `ticket` を `project_id` でグルーピングした1クエリで取得する。

**`progress` はサーバで計算して返す。** 「完了数 ÷ 全数」の定義をフロントに散らさないため。`ticket_count = 0` のときは `0` を返す（`null` にしない）。

## 5.2 `GET /api/v1/projects/check-key`

**必要権限**：`project.create`

```
GET /api/v1/projects/check-key?key=my-app
```

```json
{ "key": "my-app", "available": true }
{ "key": "admin",  "available": false, "reason": "reserved" }
{ "key": "my-app", "available": false, "reason": "already_exists" }
{ "key": "My_App", "available": false, "reason": "invalid_format" }
```

新規作成モーダルの即時検証に使う（`GuiDesign.md` 5.2.1）。debounce 400ms でフロントから呼ぶ。

**予約語**：`admin` `api` `mcp` `login` `logout` `me` `p` `new` `projects` `static` `assets` `check-key`（ルーティングと衝突するため）

`check-key` を含めるのは、これが `/api/v1/projects/` 直下の兄弟パスだからである。`check-key` という
キーのプロジェクトを作れてしまうと、`GET /projects/check-key`（5.4）が本エンドポイントに吸われて
そのプロジェクトへ到達できなくなる。

## 5.3 `POST /api/v1/projects`

**必要権限**：`project.create`（Phase 1 = アドミニストレータのみ）

```json
{
  "key": "my-app",
  "name": "社内タスク管理の刷新",
  "description": "既存のExcel管理を置き換える",
  "workflow_template": "simple"
}
```

| フィールド | 検証 |
|---|---|
| `key` | 必須。`^[a-z0-9][a-z0-9-]{1,19}$`、予約語でない、未使用。**作成後は変更不可** |
| `name` | 必須。1〜100文字 |
| `description` | 任意。0〜1000文字 |
| `workflow_template` | `simple` / `with_review` / `with_approval`。既定 `simple` |

`201 Created`（`Location: /api/v1/projects/my-app`）。応答は 5.4 と同形式。

**サーバ側の処理**：`project` 作成、`project_counter` 初期化、テンプレートから `workflow` / `workflow_status` / `workflow_transition` を複製、作成者を `project_member`（`project_admin`）として登録。**これらは単一トランザクションで行う。**

キー重複は `409 conflict`（`code: "already_exists"`）。競合検出はDBの `UNIQUE` 制約に委ね、`check-key` の結果を信頼しない（TOCTOU 対策）。

## 5.4 `GET /api/v1/projects/:key`

**必要権限**：`project.view`（メンバーでない場合は `404`）

```json
{
  "id": "01K2...", "key": "my-app", "name": "社内タスク管理の刷新",
  "description": "...", "status": "active",
  "workflow": { "id": "01K2...", "name": "シンプル",
                "statuses": [ { "key": "todo", "name": "未着手", "category": "todo",
                                "sort_order": 1, "requires_human_approval": false,
                                "is_agent_reachable": true } ] },
  "members": [ { "actor_id": "01K2...", "kind": "user", "display_name": "田中",
                 "email": "tanaka@example.com",
                 "role": "project_admin", "joined_at": "..." } ],
  "my_role": "project_admin",
  "my_permissions": ["ticket.close", "..."],
  "settings": { "max_concurrent_agents": 2 },
  "version": 3,
  "created_at": "...", "updated_at": "..."
}
```

`:key` はプロジェクトキー（ULIDではない）。URL・チケット番号と一致させ、開発時のデバッグを容易にする。

`settings` は `project.settings`（jsonb）をそのまま返す。**Phase 1 が定義するキーは `repositories` のみ**で、構造の正本は `DbDesign.md` 6.4 にある（上の例の `max_concurrent_agents` は Phase 2）。

`members[].email` は `app_user.email`（`DbDesign.md` 6.2）。**エージェントとシステムアクターは `app_user` の行を持たないため `null`** になる。キーは常に返す（省略しない）。`display_name` とは別物で、画面はログイン名として並べて表示する（`GuiDesign.md` 5.9.2）。

## 5.5 `PATCH /api/v1/projects/:key`

**必要権限**：`project.edit`

変更可能：`name` `description` `settings`。**送られたフィールドだけを更新する**（部分更新）。
検証は 5.3 の表と同じ（`name` 1〜100文字、`description` 0〜1000文字）。

**`settings` は丸ごと置き換える**（部分更新ではない）。サーバは JSON オブジェクトであることだけを確かめ、中身は検証しない。**呼び出し側は取得した `settings` を保持し、変更するキーだけ差し替えて全体を送ること。** 知らないキーを落とすと、他の機能の設定が消える。

**`key` は含められない。** 送られた場合は `422`、`details` に
`{ "field": "key", "code": "immutable_field" }` を載せる。`immutable_field` は
**`details[].code` の値**であって 2.5.1 の `error.code` ではない（`error.code` は
`validation_failed`）。2.5.1 のコード表は本体の `code` だけを列挙している。

`If-Match: "3"` による楽観ロック（2.8）。**成功すると `version` が +1 される。**
応答は 5.4 と同形式で、更新後の値を返す。

### 5.5.1 応答

| 状況 | 応答 |
|---|---|
| 更新できた | `200` ＋ 5.4 形式 |
| `If-Match` が無い | `422 validation_failed`（`details[].field = "If-Match"`、`code = "required"`） |
| `If-Match` が現在の `version` と違う | `409 conflict` |
| `key` が送られた | `422 validation_failed`（`details[].code = "immutable_field"`） |
| メンバーでない | `404 not_found`（存在を隠す。`Design.md` 6.4.5） |
| メンバーだが `project.edit` が無い | `403 forbidden` |

## 5.6 `POST /api/v1/projects/:key/archive` / `POST /api/v1/projects/:key/unarchive`

**必要権限**：`project.archive`

`status` を切り替える。**物理削除のAPIは Phase 1 では提供しない。** チケット・コメント・監査記録を巻き込むため、必要になった時点で「削除の確認方法」と併せて設計する。

| | archive | unarchive |
|---|---|---|
| `status` | `archived` | `active` |
| `archived_at`（`DbDesign.md` 6.4） | `now()` | `NULL` |
| `version` | +1 | +1 |

**応答は `200` ＋ 5.4 形式**（更新後のプロジェクト）。`204` にしないのは、画面が `status` と
`version` を1往復で更新できるようにするためである。リクエスト本文は取らない。

**既にその状態なら何も変えずに `200` を返す**（冪等）。二重送信やブラウザの戻る操作で
`version` だけが進むのを避ける。

`If-Match` は要求しない（2.8）。**監査ログは archive / unarchive のどちらも `project.archive`
として記録し**（2.10 のカタログにこの1つしかない）、`detail` に遷移後の `status` を入れて区別する。

---

# 6. ユーザー管理API

すべて **アドミニストレータ専用**（`user.manage`）。`GuiDesign.md` 5.6 に対応する。

## 6.1 `GET /api/v1/admin/users`

```
GET /api/v1/admin/users?kind=all&is_active=all&sort=display_name&order=asc&page=1&per_page=25
```

| パラメータ | 既定 | 説明 |
|---|---|---|
| `kind` | `all` | `user` / `agent` / `all`。`GuiDesign.md` 5.6 は人間とエージェントを同一一覧に並べる。**`kind='system'` の actor は返さない**（`DbDesign.md` 6.2 の3種目。利用者が管理する対象ではない） |
| `is_active` | `all` | `true` / `false` / `all`。**真偽値ではなく3値の文字列**として扱う（未指定と `false` を区別するため） |
| `q` | — | 表示名・メールの部分一致。**`%` と `_` はサーバ側でエスケープするため、ワイルドカードとしては働かない** |
| `sort` | `display_name` | `display_name` / `email` / `last_login_at` / `created_at` |
| `order` | `asc` | `asc` / `desc`（2.6 の共通仕様）。**名簿は昇順で読むため、`GET /projects` の既定（`desc`）とは違う** |

```json
{
  "items": [
    { "id": "01K2...", "kind": "user", "display_name": "田中",
      "email": "tanaka@example.com", "system_role": "administrator",
      "is_active": true, "last_login_at": "2026-08-11T09:03:12Z",
      "project_count": 3, "created_at": "2026-07-01T00:00:00Z" },
    { "id": "01K2...", "kind": "agent", "display_name": "claude-code (my-app)",
      "email": null, "system_role": null,
      "agent": { "client_kind": "claude_code", "model_name": "claude-opus-5",
                 "project_key": "my-app", "trust_level": 1 },
      "is_active": true, "last_login_at": "2026-08-11T08:41:00Z",
      "project_count": 1, "created_at": "2026-08-01T00:00:00Z" }
  ],
  "page": 1, "per_page": 25, "total": 4, "total_pages": 1
}
```

**`kind` によって意味を持たないフィールドは `null` を返し、フィールド自体を省略しない。** フロントの分岐を単純にするため。

**`agent` は Phase 1 では常に `null` である。** 中身（`client_kind` / `model_name` / `project_key` / `trust_level`）は `DbDesign.md` 8.1 の `agent` テーブルの列で、そのテーブルは Phase 2 のマイグレーションで作られる。Phase 1 のスキーマから埋められる値が1つも無いため、**キーだけを返して中身は推測しない**。上の例はエージェントを作れるようになった後の姿である。

**`project_count` は `project_member` の行数**で、アーカイブ済みプロジェクトも数える。除くと 6.3 の `memberships` に並ぶ件数と食い違うため。

一覧は `ETag` を返す（2.7）。`W/"user-<件数>-<MAX(updated_at) のナノ秒>"` で、**`updated_at` は `actor` と `app_user` の新しいほうを採る**。システムロールの変更は `app_user` の行だけを更新するため、`actor` だけを見るとロールを変えても値が変わらない。

## 6.2 `POST /api/v1/admin/users`

```json
{
  "display_name": "山田 太郎",
  "email": "yamada@example.com",
  "system_role": "operator",
  "password_mode": "generate",
  "password": null,
  "must_change_password": true
}
```

| フィールド | 検証 |
|---|---|
| `display_name` | 必須。1〜60文字（`actor.display_name` の CHECK 制約と同じ。前後の空白は落とす） |
| `email` | 必須。形式検証＋未使用（`409 conflict` / `already_exists`）。**254文字以内**（RFC 5321 4.5.3.1.3）。**表示名付き（`山田 <a@example.com>`）は受け付けない** |
| `system_role` | `operator` / `administrator`。既定 `operator` |
| `password_mode` | `generate` / `manual`。**既定 `generate`**（`GuiDesign.md` 5.6.1 の初期選択） |
| `password` | `manual` のとき必須。12文字以上。**`generate` のときに送られても無視する**（モードを切り替えるフォームが前の入力を残したまま送るのは自然な作りであり、それを誤りとして弾くと画面側が余計な制御を持つ） |
| `must_change_password` | **既定 `true`**（`GuiDesign.md` 5.6.1 のチェックボックスが既定でオン）。管理者が決めたパスワードを本人が使い続ける状態を既定にしない |

```json
// 201 Created — generated_password は「この応答でのみ」返る
{
  "id": "01K2...", "kind": "user", "display_name": "山田 太郎",
  "email": "yamada@example.com", "system_role": "operator", "is_active": true,
  "generated_password": "quiet-harbor-4172"
}
```

**`password_mode=manual` のときは `generated_password: null` を返す**（キーは省略しない）。呼び出し側が既に平文を持っており、返す意味がないため。

**サーバ側の処理**：`actor` → `app_user` → `user_identity`（`provider_key='local'`, `subject=email`）→ `local_credential` を単一トランザクションで作成する（`DbDesign.md` 6.2）。

作成時は `audit_log` に `user.create` を記録する（2.10）。`target_type='app_user'` / `target_id=<actor_id>`、`detail` は `email` / `system_role` / `password_mode` / `must_change_password`。**平文のパスワードは `detail` に入れない**——`audit_log` は長期保存される記録であり、残ると「この応答でのみ返る」が崩れる。

### 6.2.1 自動生成パスワード

**読み上げ・転記しやすい語句連結方式**とする。ランダム英数字は電話やチャットでの伝達時に誤りが生じやすいため。

形式は **`<形容詞>-<名詞>-<4桁数字>`**（例 `quiet-harbor-4172`）。語彙は形容詞16語・名詞16語で、いずれも英小文字のみ・4文字以上とし、連結が常に最小長（12文字、`Design.md` 6.3）を超えるようにする。乱数は暗号論的擬似乱数から採る。

**この強度（約21ビット）は暫定である。** Phase 1 の開発中は生成された値を手で打ち込んで動作確認するため、**長さと打ちやすさを優先**している。単発の初期パスワードであり、`must_change_password` が既定で `true`、かつアカウントロック（5回/15分、`Design.md` 6.3）が効くため、オンラインでの推測は現実的でない。**セキュリティ監査の時点で語彙数または要素数を増やす**（`docs/PROGRESS.md`「手順外の作業」に起票済み）。

## 6.3 `GET /api/v1/admin/users/:id`

`GuiDesign.md` 5.6.2 の詳細画面が必要とする情報を1回で返す（設計方針3）。

```json
{
  "id": "01K2...", "kind": "user", "display_name": "山田 太郎",
  "email": "yamada@example.com", "system_role": "operator",
  "is_active": true, "last_login_at": "...", "created_at": "...", "version": 2,

  "identities": [
    { "id": "01K2...", "provider_key": "local", "provider_type": "local",
      "subject": "yamada@example.com", "linked_at": "...", "last_used_at": "...",
      "password_updated_at": "2026-07-01T00:00:00Z" }
  ],
  "project_memberships": [
    { "project_id": "01K2...", "project_key": "my-app",
      "project_name": "社内タスク管理の刷新", "role": "project_admin", "joined_at": "..." }
  ],
  "sessions": [
    { "id": "01K2...", "client_info": "Chrome / macOS",
      "issued_at": "...", "last_used_at": "...", "expires_at": "..." }
  ]
}
```

**`identities` 配列が Phase 3 の IdP 連携をそのまま受け入れる。** OIDC を追加しても要素が1つ増えるだけで、レスポンス構造もUIも変わらない（`DbDesign.md` 6.2）。

## 6.4 `PATCH /api/v1/admin/users/:id`

変更可能：`display_name` `email` `system_role` `is_active`

`If-Match` による楽観ロック。

| ガード | 応答 |
|---|---|
| 自分自身の `system_role` 変更 | `409 self_modification_forbidden` |
| 自分自身の `is_active: false` | `409 self_modification_forbidden` |
| 最後の有効なアドミニストレータの降格・無効化 | `409 last_administrator` |
| `email` 重複 | `409 conflict` |

**この3つのガードをAPI側に置くことが重要である。** UIだけで防ぐと、直接APIを叩いた場合に**誰もログインできないインスタンス**が生まれうる。

`system_role` 変更時は当該ユーザーの権限キャッシュを無効化する（`Design.md` 6.4.5）。

## 6.5 `DELETE /api/v1/admin/users/:id`

物理削除（`DbDesign.md` 4.6）。`actor` の削除により `app_user` `user_identity` `local_credential` `access_token` `project_member` が CASCADE で消える。

`ticket.assignee_id` は `ON DELETE SET NULL` のため**チケットは残る**。`comment.author_id` は `NOT NULL` かつ `ON DELETE RESTRICT` のため、**削除前にシステムアクター（`kind='system'` の「削除されたユーザー」）へ付け替える**必要がある（`DbDesign.md` 6.7）。

| ガード | 応答 |
|---|---|
| 自分自身 | `409 self_modification_forbidden` |
| 最後のアドミニストレータ | `409 last_administrator` |
| 有効な `task_lease` を保持中（Phase 2） | `409 conflict` |

削除前に `audit_log` へ `user.delete` を記録し、`detail` に削除時点の表示名・メールを保存する。**削除後に「誰を消したか」を追えなくなることを防ぐ。**

無効化のみ行いたい場合は 6.4 の `is_active: false` を使う。UIは「無効化」を既定の導線とし、「削除」は `⋯` メニューの下段に置く（`GuiDesign.md` 5.6）。

## 6.6 `POST /api/v1/admin/users/:id/password-reset`

```json
// Request
{ "mode": "generate", "must_change_password": true }
```

```json
// 200 OK
{ "generated_password": "clear-meadow-8821-sage" }
```

当該ユーザーの `local_credential` を更新し、`failed_attempts` と `locked_until` をリセット。**全セッションを失効**する。

`local_credential` を持たないユーザー（IdP のみ、Phase 3）に対しては `409 conflict`。

## 6.7 `POST /api/v1/admin/users/:id/sessions/revoke`

全セッションを失効。`204`。エージェントのトークンにも適用される（`kind='agent'` の場合）。

## 6.8 プロジェクトメンバーシップ

```
PUT    /api/v1/admin/users/:id/memberships/:project_key   { "role": "project_admin" }
DELETE /api/v1/admin/users/:id/memberships/:project_key
```

`PUT` は追加と変更を兼ねる（冪等）。`GuiDesign.md` 5.6.2 の「プロジェクトごとの権限」ブロックに対応する。

プロジェクト側からも同じ操作ができるよう、Phase 2 で `POST /api/v1/projects/:key/members` を追加する。**同一の状態を2経路で変更することになるため、内部実装は共通の1関数に集約する。**

---

# 7. ロール・権限API

## 7.1 `GET /api/v1/roles`

**必要権限**：`user.manage`

```json
{
  "items": [
    { "key": "operator", "scope": "system", "display_name": "オペレータ",
      "is_builtin": true, "sort_order": 10,
      "permissions": ["project.view", "ticket.view", "ticket.create", "..."] },
    { "key": "administrator", "scope": "system", "display_name": "アドミニストレータ",
      "is_builtin": true, "sort_order": 20, "permissions": ["..."] }
  ]
}
```

## 7.2 `GET /api/v1/permissions`

**必要権限**：`user.manage`

```json
{
  "items": [
    { "key": "project.view",   "category": "project", "description": "プロジェクトの閲覧" },
    { "key": "project.create", "category": "project", "description": "プロジェクトの作成" },
    { "key": "ticket.close",   "category": "ticket",  "description": "チケットのクローズ" }
  ]
}
```

`GuiDesign.md` 5.6.3 の権限マトリクス表は、7.1 と 7.2 の2レスポンスから組み立てる。**マトリクス専用のエンドポイントは作らない**（データが重複し、片方だけ更新される事故を招くため）。

## 7.3 Phase 3 で追加するもの

`POST/PATCH/DELETE /api/v1/roles` によるカスタムロール（`role.is_builtin = 0`）。Phase 1 は読み取り専用（`GuiDesign.md` 5.6.3）。

---

# 8. 画面とAPIの対応

| 画面 | 呼ぶAPI |
|---|---|
| ログイン | `GET /auth/providers`（初期表示）<br>`POST /auth/login`（送信） |
| アプリ起動時 | `GET /me` |
| プロジェクト一覧 | `GET /projects?status=active` |
| 新規プロジェクトモーダル | `GET /projects/check-key`（入力時）<br>`POST /projects`（作成） |
| プロジェクトダッシュボード | `GET /projects/:key`<br>（統計・自分の担当・最近の動きはチケットAPI。9章） |
| ユーザー / 権限（ユーザータブ） | `GET /admin/users` |
| ユーザー追加モーダル | `POST /admin/users` |
| ユーザー詳細・編集 | `GET /admin/users/:id`<br>`PATCH /admin/users/:id`<br>`PUT|DELETE /admin/users/:id/memberships/:key`<br>`POST /admin/users/:id/password-reset`<br>`POST /admin/users/:id/sessions/revoke` |
| ユーザー / 権限（ロールタブ） | `GET /roles` + `GET /permissions` |
| 自分の設定 | `PATCH /me`<br>`POST /me/password`<br>`GET|DELETE /me/sessions` |
| アクセストークン | `GET|POST|DELETE /me/tokens` |

**各画面が起動時に呼ぶAPIは1〜2本に収まっている。** 設計方針3が満たされていることの確認になる。

---

# 9. チケットAPI（未着手）

以下を次の改訂で定義する。

- `GET|POST /projects/:key/tickets`、`GET|PATCH /projects/:key/tickets/:seq`
- `POST /projects/:key/tickets/:seq/transition`（ステータス遷移。`workflow_transition` と `is_agent_reachable` を検証）
- `GET|POST /projects/:key/tickets/:seq/comments`
- `GET|POST|PATCH /projects/:key/tickets/:seq/dod`
- `GET|POST|DELETE /projects/:key/tickets/:seq/links`
- `GET /projects/:key/stats`、`GET /projects/:key/activity`（ダッシュボード用）
- チケット採番（`project_counter` の行ロック）とその並行制御
- 一覧のフィルタ・ソート・階層表示の表現方法

---

# 10. 実装順序と未解決事項

## 10.1 実装順序

```
1.  共通基盤：エラー形式、ページネーション、認証ミドルウェア、監査ログ
2.  POST /auth/login、POST /auth/logout、GET /me            ← ここでログインが通る
3.  認可ミドルウェア（require_permission）と権限カタログのシード
4.  GET /projects、POST /projects、GET /projects/check-key   ← プロジェクト一覧が動く
5.  GET /projects/:key、PATCH、archive
6.  GET /admin/users、POST /admin/users                      ← ユーザー管理が動く
7.  GET /admin/users/:id、PATCH、DELETE、password-reset
8.  memberships、sessions/revoke
9.  GET /roles、GET /permissions                             ← 権限マトリクスが出る
10. GET /me/sessions、/me/tokens、PATCH /me、POST /me/password
```

**手順2の完了時点で「ログインできる」、手順4で「プロジェクト一覧が見える」、手順6で「ユーザーを追加できる」という区切りになる。** それぞれで動作確認を挟める順序にしてある。

## 10.2 未解決の検討事項

- **`GET /me` のキャッシュ戦略**。ロール変更が他セッションへ反映されるまでの許容遅延をどう決めるか（毎リクエスト検証はコスト、長期キャッシュは権限剥奪が効かない）
- 一覧APIの `total` を返し続けるコストが問題になる規模の見極め（Phase 2 のチケット一覧で再検討）
- `PATCH` における「フィールド省略」と「明示的 null」の扱いを、サーバ実装（serde の `Option<Option<T>>` 等）でどう表現するか
- エラーメッセージの多言語化。Phase 1 は日本語固定とするが、`code` を機械可読にしてあるためフロント側での差し替えは可能
- `POST /projects` のワークフローテンプレート定義を、コードに埋め込むかDBのシードとして持つか
- エージェント用トークンの発行API（Phase 2）を `/me/tokens` と統合するか、プロジェクト配下（`/projects/:key/agents`）に置くか
- 削除操作の監査における個人情報の保持期間（`audit_log.detail` と `actor_label` に残した表示名・メールをいつ消すか）
