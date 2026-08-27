# Project Backyard (PB) API設計書

> 本書は PB の REST API に関する**唯一の正本**である。
>
> **文書体系**：`Requirements.md`（要件）→ `Design.md`（全体設計・認証認可）→ `DbDesign.md` / 本書 / `GuiDesign.md`（領域別の正本）
>
> - 対象読者：サーバ／フロントエンド実装者（人間およびAIエージェント）
> - 関連：`DbDesign.md`（スキーマ）、`GuiDesign.md`（画面）、`Design.md` 6章（認証・認可）
> - 状態：**Phase 1 のAPIを確定**（チケットAPI＝9章を含む）。MCP系は未着手（`Design.md` 8章）

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
| **9** | **チケットAPI** | **確定**（実装状況は `PROGRESS.md`） |
| 10 | 未解決の検討事項 | 確定 |

---

# 1. 本書の範囲と方針

## 1.1 今回定義する範囲

`GuiDesign.md` の Phase 1 画面のうち、**最初に構築する3画面**に必要なAPIを優先して定義する。

| 画面 | 必要なAPI |
|---|---|
| ログイン（5.1） | 3章 |
| プロジェクト一覧・新規作成（5.2） | 5章 |
| アカウント / 権限管理（5.6） | 6章・7章 |

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
    "theme": "dark",
    "hue": "blue",
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
{ "display_name": "田中", "email": "tanaka@example.com",
  "locale": "ja", "timezone": "Asia/Tokyo",
  "theme": "dark", "hue": "green" }
```

`theme` / `hue` は `GuiDesign.md` 8.11 のテーマ設定。`app_user` の列に保持する。

**`system_role` は変更不可**（管理者が 6.4 で変更する）。送られた場合は無視せず `422` を返す。

**`email` は本人が変更できる。** この列はログインIDでもあるため（`Design.md` 6.2.1 手順2〜3 が
`user_identity.subject` と突き合わせる）、変更時は `subject` も同じトランザクションで
追随させる。追随させないと当人がログインできなくなる。**現在のパスワードの再入力は求めない**
——既にセッションを持つ本人の操作であり、Phase 1 で再認証を求める箇所を他に持たないためである。

| 状況 | 応答 |
|---|---|
| 成功 | `200`。本体は `GET /me`（4.1）と**同一構造**。画面はこの応答で表示を差し替える（`GuiDesign.md` 6.4） |
| 他のユーザーが同じメールを使っている | `409 already_exists`（6.4 と同じ） |
| 形式誤り・`system_role` の送信 | `422 validation_failed` |

**楽観ロック（2.8）は課さない。** 自分の設定を同時に2箇所から編集する状況が実質無く、
課すと `GET /me` に `ETag` が要る——`GET /me` は全画面の起動時に呼ばれるため、影響が広い。
ただし `app_user.version` は加算し、6.4 の楽観ロックが壊れないようにする。

**画面上は「ログインID」と「メールアドレス」の2行に分かれている**（`GuiDesign.md` 5.8）が、
Phase 1 ではどちらも本列を指す。ログインIDは読み取り専用で、メールアドレス欄の変更に追随する。
**ログインIDと連絡先を別々に登録できるようにするのは、必要になった時点でのスキーマ変更を伴う**
（利用者の判断、2026-08-22）。

## 4.3 `POST /api/v1/me/password`

**必要権限**：本人

```json
{ "current_password": "••••", "new_password": "••••••••••••" }
```

- 現在のパスワード検証に失敗 → `401 invalid_credentials`
- ポリシー違反（12文字未満等） → `422 validation_failed`
- `local_credential` を持たないユーザー（IdP のみ、Phase 3） → `409 conflict`（6.6 と同じ）
- 成功 → `204`。**現在のセッションを除く全セッションを失効**（`Design.md` 6.3）

**成功時に `local_credential.must_change` を `false` にする。** これをしないと、
`must_change_password: true` で入った利用者が変更しても誘導が消えず、変更画面へ戻され続ける。

**現在のパスワードの検証に失敗しても `failed_attempts` は増やさない**（＝アカウントロックの
対象にしない）。既にセッションを持つ本人の操作であり、ここで数えると自分で自分を締め出せる。
連打はアクター単位のレート制限（2.9、600回/分）が抑える。

## 4.4 `GET|POST|DELETE /api/v1/me/tokens`

**必要権限**：本人

CLI・スクリプトから API を呼ぶための Bearer トークンを、本人が発行・一覧・失効する
（`GuiDesign.md` 5.8、`DbDesign.md` 6.2 の `access_token`）。

**扱うのは `token_type='api'` の行だけである。** ブラウザのセッション
（`token_type='session'`）はこの3本のどれにも現れない。本人が自分のセッションを
見る・切る画面を持たないと決めており（`GuiDesign.md` 5.8）、混ぜると
「一覧に出ているのに失効させられない行」が生まれる。エージェント用
（`token_type='agent'`）はプロジェクト設定側から発行する（`Design.md` 6.5、Phase 2）。

### 4.4.1 `GET /api/v1/me/tokens`

```json
{
  "items": [
    { "id": "01K2...", "name": "CLI (MacBook)", "token_prefix": "pb_api_9",
      "scopes": [], "issued_at": "2026-08-22T09:03:12Z",
      "last_used_at": "2026-08-22T10:41:00Z", "expires_at": "2026-11-20T09:03:12Z",
      "status": "active" }
  ]
}
```

**`token`（平文）は返さない。** 返すのは `token_prefix`（先頭8文字。`pb_api_` + 1文字）だけで、
これは一覧で行を見分けるためのものであり、検索キーではない。

`items[]` は `issued_at` の降順。

| 項目 | 内容 |
|---|---|
| `status` | `active`（有効）／ `expired`（`expires_at` を過ぎた） |
| `scopes` | 空配列は「絞り込みなし」＝本人の実効権限そのまま（`Design.md` 6.4.1） |
| `last_used_at` | 一度も使われていなければ `null`。更新は1分粒度（`Design.md` 6.2.2） |

**失効済み（`revoked_at IS NOT NULL`）は返さない。** 失効は本人が消したものであり、
残すと増え続けて読めなくなる。記録は監査ログの `token.revoke` にある。
**期限切れは返す**——「更新しないと使えない」と本人が気づく必要がある情報だからである。

**ページネーションも `ETag` も持たない**（7.1 / 7.2 と同じ）。1人あたり5本が上限であり、
絞り込みも差分取得も意味を持たない。

### 4.4.2 `POST /api/v1/me/tokens`

```json
// Request
{ "name": "CLI (MacBook)", "expires_in_days": 90, "scopes": [] }
```

```json
// 201 Created — token は「この応答でのみ」返る
{ "id": "01K2...", "name": "CLI (MacBook)", "token": "pb_api_9f3c...",
  "token_prefix": "pb_api_9", "scopes": [], "issued_at": "2026-08-22T09:03:12Z",
  "expires_at": "2026-11-20T09:03:12Z", "status": "active" }
```

**`token` を返すのはこの応答だけである。** 再表示するAPIは無く、DBにはSHA-256の
ハッシュしか残らない（`DbDesign.md` 6.2）。画面は1回だけ全文を出す（`GuiDesign.md` 5.8）。

| 項目 | 規則 |
|---|---|
| `name` | **必須**。1〜100文字（`project.name` と同じ上限。`access_token.name` にDBの CHECK は無く、アプリ側が持つ） |
| `expires_in_days` | **必須**。1〜365 の整数。無期限は許さない |
| `scopes` | 省略可。既定は `[]`（絞り込みなし） |

**無期限を許さない理由。** `Design.md` 6.5 はエージェントトークンについて「有効期限必須」と
定めており、CLI トークンだけ例外にする理由が無い。`access_token.expires_at` の NULL を
許すと、失効操作でしか消えないトークンが残り、置き忘れを検出できなくなる。

#### スコープの語彙は権限キーである

**`scopes[]` に入るのは権限カタログのキー**（`ticket.view` / `user.manage` 等28件。
`Design.md` 6.4.2、`DbDesign.md` 7.2 のシードが正本）。カタログに無い値は `422`。

`Design.md` 6.4.1 の実効権限は

```
( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ トークンのスコープ
```

であり、**この積は権限キーどうしの完全一致で取る**。別の語彙を混ぜると、絞ったつもりの
トークンが権限0件になるか、解釈できない語彙を通して逆に広がるかのどちらかになる。

`Design.md` 6.5 がエージェントの既定スコープとして挙げる `ticket:read` / `ticket:claim` /
`result:submit` / `context:read` は、**権限カタログに対応するキーを持たない**。
エージェントの操作そのものが Phase 2 で設計されるため、対応表を先取りしない。

**Phase 1 の画面はスコープを選ばせない**（`GuiDesign.md` 5.8）。常に `[]` で発行するため、
発行されたトークンは本人の権限をそのまま持つ。どの権限をまとめて選ばせるかは、
トークンで実際に何をするか（Phase 2 の MCP 連携）が決まってから設計する。

**スコープを使い始めたら、この3本自身をスコープの対象にする必要がある。** 4章は権限キーを
要求しないため、**絞ったトークンで `POST /me/tokens` を叩き、絞っていないトークンを
発行し直せる**。Phase 1 では常に `[]` なので昇格にならない（そのトークンは既に本人の
全権を持つ）が、スコープが意味を持った時点で経路として残る。

#### 発行本数の上限

**1人あたり5本まで。** 超えると `409 conflict`。

数えるのは**失効していないもの**であり、**期限切れも含む**——4.4.1 が返す行と一致させる。
一致させないと、一覧に7行出ているのに「上限5本」と言われ、どれを失効させれば発行できるのかが
画面から読めなくなる。

上限を置くのは、**どこからアクセスしているのかを本人が把握できる本数に留める**ためである
（利用者の判断、2026-08-22）。大量に発行するユースケースが無く、増えるほど失効し忘れが残る。

| 状況 | 応答 |
|---|---|
| 成功 | `201`。上の本体 |
| `name` / `expires_in_days` / `scopes` の形式誤り | `422 validation_failed` |
| 失効していないトークンが既に5本ある | `409 conflict` |

### 4.4.3 `DELETE /api/v1/me/tokens/:id`

失効させる（`access_token.revoked_at` を立てる）。行は消さない——監査ログの `token_id` から
辿れる先を残すためである。

| 状況 | 応答 |
|---|---|
| 成功 | `204` |
| 既に失効済み | `204`（**冪等**。`revoked_at` は上書きしない） |
| 他人のトークン・存在しない `id`・`token_type` が `api` でない | `404 not_found` |

**他人のトークンを 403 ではなく 404 に倒す。** 存在を漏らさないためである
（`Design.md` 6.4.5）。

**現在のセッションはこの経路で切れない。** `token_type='session'` を対象外にしてあるため、
`id` にセッションのトークンを渡しても 404 になる。ログアウトは 3.2 が担う。

### 4.4.4 監査（2.10）

| 操作 | `action` | `detail` |
|---|---|---|
| 発行 | `token.issue` | `name` / `scopes` / `expires_at`。**平文は入れない** |
| 失効 | `token.revoke` | `name` / `token_prefix` |

いずれも `target_type='access_token'`、`target_id` はトークンの ID。
`audit_log.token_id` は**操作に使ったトークン**（通常はブラウザのセッション）であり、
発行・失効の対象とは別である。
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
| `q` | — | 表示名・メール・**ロールの表示名**の部分一致。ロールは `role.display_name`（`DbDesign.md` 7.3 のシード。「アドミニストレータ」「オペレータ」）と比べる——**画面に出ている文字列で探せることが目的**なので、画面に出ないキー（`administrator`）は対象にしない。**`%` と `_` はサーバ側でエスケープするため、ワイルドカードとしては働かない** |
| `sort` | `display_name` | `display_name` / `email` / `system_role` / `is_active` / `last_login_at` / `created_at` |
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

**`sort=system_role` は `role.sort_order` で並べる**（`DbDesign.md` 7.3 のシード。オペレータ 10 → アドミニストレータ 20）。表示名の五十音順ではない——シードが意図して序列を持っており、Phase 3 でカスタムロールが増えたときに表示名順では意味のない並びになるため。**`system_role` を持たない行（エージェント）は昇順・降順とも末尾に置く**（`NULLS LAST`）。ロールを持たない行が先頭に来ると、ロールで並べた意味が薄れる。

**`sort=is_active` の昇順は無効が先**（`false < true`）。状態で並べ替える動機は「無効な利用者を探す」ことが多いため、そのままにしている。

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
| `email` 重複 | `409 already_exists` |

**この3つのガードをAPI側に置くことが重要である。** UIだけで防ぐと、直接APIを叩いた場合に**誰もログインできないインスタンス**が生まれうる。

`system_role` 変更時は当該ユーザーの権限キャッシュを無効化する（`Design.md` 6.4.5）。

**`email` を変更したときは、同じトランザクションで `user_identity.subject` も更新する**（`provider_key='local'` の行のみ）。ログインは `user_identity` を `(provider_key='local', subject=app_user.email)` で引き当てるため（`Design.md` 6.2.1 手順2〜3）、`app_user.email` だけを変えると**当人がログインできなくなる**。OIDC/SAML の `subject` は IdP が払い出す識別子であり（`DbDesign.md` 6.2）、メールとは無関係に不変であるべきなので対象にしない。

**`version` は `app_user` の列だが、`display_name` と `is_active`（`actor` の列）だけを変えた場合も +1 する。** ユーザー1人につき `version` は1つ、という約束にしないと、`actor` 側だけを変えた直後に古い `version` でもう一度更新が通ってしまう。

## 6.5 `DELETE /api/v1/admin/users/:id`

物理削除（`DbDesign.md` 4.6）。`actor` の削除により `app_user` `user_identity` `local_credential` `access_token` `project_member` が CASCADE で消える。

`ticket.assignee_id` は `ON DELETE SET NULL` のため**チケットは残る**。`comment.author_id` は `NOT NULL` かつ `ON DELETE RESTRICT` のため、**削除前にシステムアクター（`kind='system'` の「削除されたユーザー」）へ付け替える**必要がある（`DbDesign.md` 6.7）。

| ガード | 応答 |
|---|---|
| 自分自身 | `409 self_modification_forbidden` |
| 最後の有効なアドミニストレータ | `409 last_administrator` |
| 有効な `task_lease` を保持中（Phase 2） | `409 conflict` |

**「最後の有効なアドミニストレータ」は 6.4 と同じ数え方をする**——`system_role='administrator'` かつ `actor.is_active` の人数で判定する。無効なアドミニストレータは認証を通れないため、管理者として「残っている」ことにならない。

削除前に `audit_log` へ `user.delete` を記録し、`detail` に削除時点の表示名・メールを保存する。**削除後に「誰を消したか」を追えなくなることを防ぐ。**

無効化のみ行いたい場合は 6.4 の `is_active: false` を使う。UIは「無効化」を既定の導線とし、「削除」は `⋯` メニューの下段に置く（`GuiDesign.md` 5.6）。

## 6.6 `POST /api/v1/admin/users/:id/password-reset`

```json
// Request（本文そのものを省略してもよい。2項目とも既定を持つ）
{ "mode": "generate", "must_change_password": true }
```

```json
// 200 OK
{ "generated_password": "quiet-harbor-4172" }
```

| フィールド | 既定 | 説明 |
|---|---|---|
| `mode` | `generate` | **Phase 1 は `generate` のみ受け付ける**（他は `422`）。応答が `generated_password` しか持たず、管理者が手で決めた値を返す意味が無いため。必要になれば 6.2 と同じ `password_mode` / `password` を足す |
| `must_change_password` | `true` | 6.2 と同じ既定。管理者が決めたパスワードを本人が使い続ける状態を既定にしない |

**生成される値の形式は 6.2.1 と同一である**（`<形容詞>-<名詞>-<4桁数字>`）。作成とリセットで生成器を2つ持たない。

当該ユーザーの `local_credential` を更新し、`failed_attempts` と `locked_until` をリセット。**全セッションを失効**する。ロックされた利用者を救うのがこの操作の主な用途であり、パスワードだけ変えてロックが残ると目的を果たさない。

監査は `password.reset` の1件のみとし、**あわせて行う失効を `session.revoke` として別に記録しない**（2.10）。1つの操作が2行になると、監査ログの読み手が二重に数える。失効した本数は `detail` に入れる。`session.revoke` を記録するのは 6.7 の単独の失効だけである。

`local_credential` を持たないユーザー（IdP のみ、Phase 3）に対しては `409 conflict`。

## 6.7 `POST /api/v1/admin/users/:id/sessions/revoke`

全セッションを失効。`204`。エージェントのトークンにも適用される（`kind='agent'` の場合）。**冪等**であり、有効なトークンが1本も無くても `204` を返す。

**個別のセッションだけを失効させるAPIは Phase 1 では持たない。** 管理者が他人の1セッションを選んで切る場面は考えにくく、怪しいセッションが1つあるなら全部を切るのが実務の動きである。要望が出た時点で `DELETE /admin/users/:id/sessions/:sid` を足す。

**本人が自分のセッションを一覧・失効させるAPIも持たない**（利用者の判断、2026-08-22）。
セッションの管理は管理者の作業であり、6.3 の一覧と本節の全失効で足りる。本人が他の端末を
締め出したい場合は、パスワードの変更（4.3）が現在のセッション以外を全失効させる。

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

| パラメータ | 既定 | 説明 |
|---|---|---|
| `scope` | 未指定＝全件 | `system` / `project`。`role.scope`（`DbDesign.md` 6.3）そのもの。**それ以外の値は 422** |

**必要権限は `scope` によって変わる。**

| 要求 | 返すもの | 必要権限 |
|---|---|---|
| `?scope=project` | `scope='project'` の3件 | **不要**（認証済みであればよい） |
| `?scope=system` | `scope='system'` の2件 | `user.manage` |
| 未指定 | 全5件 | `user.manage` |

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

`items[]` は `role.sort_order` の昇順。`permissions[]` は `permission.sort_order` の昇順で、**`scope` によらず常に含める**——呼び出し元の権限で応答の形が変わると、生成した型が両方を表現できず画面が分岐を持つことになる。

**ページネーションも `ETag` も持たない。** 件数はシード（`DbDesign.md` 7.2 / 7.3）で固定されており、絞り込みも差分取得も意味を持たないため。2.6 / 2.7 は適用しない。

### `?scope=project` だけを開放する理由

`GuiDesign.md` 5.9.2 のメンバータブは「`GET /roles`（7.1）を実装した時点で（画面が持つ対応表を）置き換える」と定める一方、同節は「**プロジェクト管理者はこの画面を開ける**」とも書いている。`user.manage` 必須のままでは両立しない。

開放しても渡る情報はほとんど増えない。プロジェクトロールのキーとその実効権限は、`POST /auth/login` と `GET /me` の `projects[]` で**既に本人へ渡っている**（3.1 / 4.1）。新たに渡るのは、本人が就いていないプロジェクトロールの権限セットだけである。Phase 2 で `POST /projects/:key/members`（6.8）が入れば、プロジェクト管理者はそれらを割り当てる側になる。

**これは暫定である**（利用者の判断、2026-08-22）。`project.edit` を要求して「プロジェクト管理者であること」を確認する案も検討したが、**判断材料になる画面がまだ揃っていない**ため見送った。プロジェクトメンバー向けの画面（5.3 のダッシュボード、5.4 / 5.5 のチケット）が実装され、誰がどの情報を見るかが具体化した時点で、`scope` 別の必要権限を含めて権限の全体像を再整理する。

### `scope` の値域を閉じる理由

**プロジェクトIDやユーザIDは受け付けない。** `role` テーブルは `project_id` を持たず（`DbDesign.md` 6.3）、Phase 3 のカスタムロール（7.3）も `is_builtin = 0` の行として同じグローバルな表に入るため、**プロジェクトIDで絞っても結果が変わらない**。

**「そのユーザに与えられたロール」は 6.3 の `GET /admin/users/:id` が返す**（`system_role` と `project_memberships[]`）。`?user=` を足すと同じ状態への読み取り経路が2本できる。6.8 が書き込み側について「同一の状態を2経路で変更することになるため、内部実装は共通の1関数に集約する」と警告しているのと同じ問題である。

**リストAPIのフィルタは、そのリソース自身の属性で絞るものに限る。** 他のリソースとの関係で絞りたくなったら、それは関係の側のエンドポイントである（`scope` は `role.scope` 列そのもので、リソース自身の属性）。

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

`items[]` は `permission.sort_order` の昇順。7.1 と同じくページネーションも `ETag` も持たない。

**7.1 と違い `user.manage` のままとする。** 消費者が `GuiDesign.md` 5.6.3 の権限マトリクスだけで、そのタブは `user.manage` を必要とする画面（`/admin/users`。3.2 のルーティング表）の中にあるためである。7.1 を開放したのは、権限を持たない画面がロールの**表示名**を必要とするからであって、権限カタログそのものを必要としているわけではない。

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
| プロジェクトダッシュボード | `GET /projects/:key/stats`<br>`GET /projects/:key/activity`<br>`GET /projects/:key/tickets?assignee=me&open=true` |
| バックログ | `GET /projects/:key/tickets`（＋ `GET /projects/:key/tags`）<br>`POST /projects/:key/tickets/:seq/move`（並べ替え） |
| 新規チケットモーダル | `POST /projects/:key/tickets` |
| チケット詳細 | `GET /projects/:key/tickets/:seq`<br>`GET /projects/:key/tickets/:seq/comments`<br>（遷移先はドロップダウンを開いたときに `GET .../transitions`） |
| アカウント / 権限（ユーザータブ） | `GET /admin/users` |
| ユーザー追加モーダル | `POST /admin/users` |
| ユーザー詳細・編集 | `GET /admin/users/:id`<br>`PATCH /admin/users/:id`<br>`PUT|DELETE /admin/users/:id/memberships/:key`<br>`POST /admin/users/:id/password-reset`<br>`POST /admin/users/:id/sessions/revoke` |
| アカウント / 権限（ロールタブ） | `GET /roles` + `GET /permissions` |
| 自分の設定 | `PATCH /me`<br>`POST /me/password` |
| アクセストークン | `GET|POST /me/tokens`<br>`DELETE /me/tokens/:id` |
| プロジェクト設定（タグタブ） | `GET|POST /projects/:key/tags`<br>`PATCH|DELETE /projects/:key/tags/:id` |
| プロジェクト設定（スプリントタブ） | `GET|POST /projects/:key/sprints`<br>`PATCH|DELETE /projects/:key/sprints/:id` |

**各画面が起動時に呼ぶAPIは1〜2本に収まっている。** 設計方針3が満たされていることの確認になる。

**唯一の例外がプロジェクトダッシュボードで、4本を呼ぶ。** 4本は互いに独立で並列に投げられ、いずれも小さい。「自分の担当」「期限が近い」を `stats` に畳み込まないのは、それがチケットの応答形をもう1つ作ることになるためである（`GuiDesign.md` 5.3）。設計方針3が避けたいのは N+1 の往復であって、独立した4本ではない。

---

# 9. チケットAPI

チケットは PB の中心にある資源であり、**同じデータをバックログ・カンバン・ガント・WBS のどの形式でも描ける**ことがデータモデルの前提である（`Requirements.md` 2章）。本章はその前提を API の形に落とす。

本章のエンドポイントは手順16〜19 に分かれて実装される（9.15）。

## 9.1 チケットの識別とURL

```
/api/v1/projects/:key/tickets/:seq
                  ↑            ↑
          プロジェクトキー   プロジェクト内連番（integer）
```

| | |
|---|---|
| `:key` | プロジェクトキー（`^[a-z0-9][a-z0-9-]{1,19}$`）。5.4 と同じ |
| `:seq` | `ticket.seq`。`UNIQUE (project_id, seq)`（`DbDesign.md` 6.6）が一意性を保証する |

**チケットを指すのは常に `seq` であり、ULID ではない。** 応答は `id`（ULID）も返すが、**リクエストでチケットを指定する箇所はすべて `seq`** とする。親は `parent_seq`、リンク先は `target_seq` である。

理由は3つ。

1. 親もリンク先も**同一プロジェクト内に限る**（Phase 1）。プロジェクトが URL で決まっているため、`seq` だけで一意に定まる
2. `GuiDesign.md` 3.2 が既にルーティングを `seq` で決めている。画面が ULID を別に持ち回らずに済む
3. MCP 経由でエージェントが扱う識別子も `my-app-31` の形になる（`Requirements.md` 10.3）。人が読める番号のまま API を組み立てられる

`id` を応答に残すのは、`activity.entity_id`（`DbDesign.md` 6.8）との突き合わせと、Phase 2 以降のエージェント連携（`task_lease.ticket_id` 等）が ULID を使うためである。

**完全形 `my-app-31` はサーバが組み立てない。** プロジェクトキーは URL に含まれており、フロントが `${key}-${seq}` を組める。応答に冗長な文字列を載せない。

**組み立て方は変えないが、画面が出す形は変わった。** `GuiDesign.md` 5.4 は当初「一覧では `-31` と接尾のみ表示（プロジェクトが自明なため）」としていたが、実機で**負の数に見える**ことが分かり、**一覧でも完全形 `my-app-31` を出す**ことにした（利用者の指摘、2026-08-23）。サーバ側の規約は変わらない。

**チケット以外の子資源（コメント・DoD項目・リンク・タグ・スプリント）は ULID で指す。** これらは `seq` に相当する連番を持たない。パスは `/tickets/:seq/comments/:id` のように、チケットまでを `seq`、その先を ULID とする。

### 9.1.1 監査ログではなく `activity` に記録する

チケットの作成・更新・遷移・削除、およびコメント・DoD・リンク・**外部参照**の変更は **`activity`（`DbDesign.md` 6.8）に記録し、`audit_log` には書かない。**

2.10 が `audit_log` の対象としているのは認証・権限・トークン・ユーザー管理であり、いずれも**インスタンス管理者が追うべき事象**である。チケットの変更は業務履歴であり、読み手はプロジェクトのメンバー（`GuiDesign.md` 5.5 の「変更履歴」）である。両者を混ぜると、監査ログがチケット更新で埋まって本来の用途に使えなくなる。

**タグとスプリントの定義変更（9.11 / 9.12）は、どちらにも記録しない。** `audit_log` の対象ではなく（上記のカタログに入らない）、`activity` の読み手はチケットの変更履歴であって、9.13.2 の `entity` も `ticket:31` の形しか受け付けない。**記録しても Phase 1 に読む画面が無い。**

ただし**タグの削除は、`ticket_tag` を `CASCADE` で消して全チケットからそのタグを外す**（`DbDesign.md` 6.10）。「使用中だったタグを誰が消したか」を後から追えない状態であり、運用に載せてから困ることがありうる。**その時点で `activity` に `entity_type='tag'` / `'sprint'` を足す**（10.2）。先に入れないのは、読む画面の無い記録が形だけ固まるのを避けるためである。

## 9.2 `GET /api/v1/projects/:key/tickets`

**必要権限**：`ticket.view`（メンバーでない場合はプロジェクトごと `404`。1.2-5）

バックログ画面（`GuiDesign.md` 5.4）の唯一のデータ源であり、カンバン・ガント（Phase 2）も同じエンドポイントを使う。

### 9.2.1 クエリパラメータ

| パラメータ | 既定 | 説明 |
|---|---|---|
| `status` | — | ワークフローのステータスキー。カンマ区切りで複数指定は OR |
| `status_category` | — | `todo` / `in_progress` / `review` / `done`。カンマ区切りは OR |
| `type` | — | `epic` / `story` / `task`。カンマ区切りは OR |
| `assignee` | — | 担当者の ULID。`me` で自分、`none` で未割当。カンマ区切りは OR |
| `priority` | — | `lowest` 〜 `highest`。カンマ区切りは OR |
| `tag` | — | タグの ULID（9.11）。`none` で未分類。カンマ区切りは OR |
| `sprint` | — | スプリントの ULID（9.12）。`none` で未割当。カンマ区切りは OR |
| `open` | — | `true` で `closed_at IS NULL` のもののみ。`false` で完了のみ |
| `due_within` | — | `7d` 形式。**今日から N 日以内に期限があるもの（期限超過を含む）**。`due_date IS NULL` は除外 |
| `parent` | — | `seq` を指定すると、そのチケットとその全子孫（部分木）に限る。**カンマ区切りで複数指定は OR**（いずれかの部分木に含まれるもの） |
| `sort` | `sort_key` | `sort_key` / `seq` / `title` / `status` / `priority` / `due_date` / `created_at` / `updated_at` |
| `order` | `asc` | `asc` / `desc` |
| `page` | `1` | 2.6 |
| `per_page` | **`200`** | 2.6。上限は 2.6 と同じ 200 |

**異なる種類の条件どうしは AND、同じ条件の複数指定は OR** とする（`?type=task&priority=high,highest` は「タスク、かつ優先度が高以上」）。

**バックログのエピックフィルタは `parent` を使う**（`GuiDesign.md` 5.4）。エピックは行として出さず、複数選択できるフィルタになるが、**絞り込みの実体は部分木であって種別ではない**（`DbDesign.md` 6.10）。`?parent=12,30` は「12 の部分木または 30 の部分木」で、エピック自身も部分木に含まれる（画面が行として捨てる）。**`epic` という専用パラメータを作らない**——作ると API が種別に依存し、グルーピングの実体が `parent_id` であるという定義と食い違う。

**`staged`（オンステージ）で絞るパラメータは持たない。** バックログ画面は**フィルタ後の全件を1回で取り切り、二段を手元で分ける**（9.2.3、`GuiDesign.md` 5.4）。2本に分けると、件数表示・`ETag`・並べ替え後の再取得がすべて2本になる。段は応答の `staged_at` で判別できる。

**`per_page` の既定が他の一覧（25）と違う。** バックログはページャを持たず、フィルタ後の全件を1回で取り切る画面だからである（9.2.3）。**`sort` の既定が `sort_key` であることも本エンドポイント固有**で、これは人が手で並べた順序（9.4）を既定の見え方にするためである。

**`q`（全文検索）は Phase 1 では受け付けない。** `GuiDesign.md` 5.4 が「全文検索は Phase 1 では実装しない」と決めている。部分一致検索を支える trigram インデックスは `DbDesign.md` 6.6 に既にあるが、専用画面 `/p/:key/search`（Phase 2）と同時に開ける。

**`status` と `status_category` の使い分け。** 画面のフィルタは `status`（プロジェクトのワークフローに定義されたキー）を使う。`status_category` は**ワークフローが違うプロジェクトを跨いでも意味が変わらない4値**であり、ダッシュボードの集計（9.13）とカンバンの列（Phase 2）が使う。

### 9.2.2 応答

```json
{
  "items": [
    {
      "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
      "seq": 31,
      "type": "task",
      "title": "認証APIの実装",
      "status": { "key": "in_progress", "name": "進行中", "category": "in_progress" },
      "priority": "high",
      "assignee": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "reporter": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "parent_seq": null,
      "has_children": true,
      "sort_key": "0|hzzzzz:",
      "staged_at": null,
      "tags": [ { "id": "01K2...", "name": "設計" } ],
      "sprint": { "id": "01K2...", "name": "Sprint 3" },
      "estimate_point": 5,
      "estimate_hours": null,
      "actual_hours": 3.5,
      "start_date": "2026-08-09",
      "due_date": "2026-08-14",
      "closed_at": null,
      "version": 3,
      "created_at": "2026-08-09T01:00:00Z",
      "updated_at": "2026-08-11T00:12:44Z"
    }
  ],
  "page": 1, "per_page": 200, "total": 48, "total_pages": 1
}
```

**`tags[]` と `parent_seq` / `has_children` を一覧に含めるのが本エンドポイントの要点である。** グループ化（タグ）と階層のインデント表示（親子）を、追加のリクエストなしに描けるようにする（設計方針3）。これらを含めないと、バックログは1画面あたり `1 + タグ数 + 階層の深さ` 回の往復を必要とする。

**`staged_at` は「オンステージ」を表す**（`DbDesign.md` 6.6）。`null` がバックログ、値が入っているものがオンステージで、値は**いつ上げたか**である。バックログ画面はこの1項目で上下二段に振り分ける。**進捗（`status`）とは独立した軸**であり、「未着手だがオンステージ」が表せる。

**`body_md` は含めない。** 一覧は本文を表示せず（`GuiDesign.md` 5.4）、200件分の Markdown は応答を数十倍にする。本文が要るのは詳細（9.5）だけである。

**`execution_mode` / `readiness` / `readiness_note` / `scope` / `custom_fields` も含めない。** 列は `DbDesign.md` 6.6 に先行定義されているが、`GuiDesign.md` 5.5 が「Phase 1 では非表示」と決めている。**画面が使わない項目を応答に載せない**（載せると、使われないまま形が固まる）。Phase 2 で有効化する際に足す。

`assignee` / `reporter` は担当者不在のとき `null`。`kind` は `user` / `agent` / `system` で、**画面はこれを見てエージェントに 🤖 バッジを付ける**（`GuiDesign.md` 5.4、設計原則5）。

### 9.2.3 ページャを画面に出さない

2.6 の `page` / `per_page` / `total` / `total_pages` は**規約どおり返す**。画面がページャを出さないだけである（`GuiDesign.md` 5.4）。

**理由は、グループ化・階層のインデント・ドラッグ&ドロップの並べ替えがいずれもページ境界をまたげないことにある。** 25件目と26件目の間で親子が切れると、子だけが孤立して2ページ目の先頭に現れる。これは `GuiDesign.md` 11章に「チケット一覧の階層表示とページングの相性」として未解決事項に挙げられていた問題であり、**バックログについてはページングを持たないことで解決する**。

`total > per_page` になったとき、画面は件数とともに「フィルタで絞り込んでください」を表示する。**サーバは 200 件で打ち切るだけで、エラーにはしない。**

**全件を返す専用のモード（`per_page=all` 等）は設けない。** 上限を外すと、応答サイズが利用者の入力ではなくデータ量で決まるようになり、性能の予測が立たなくなる。Phase 1〜2 の規模で 200 件を超えるプロジェクトは、フィルタを使うか、ビューを分ける（スプリント・タグ）べき段階にある。

### 9.2.4 フィルタで親が落ちた子の扱い

**フィルタは行単位で適用し、サーバは親を補完しない。** 親がフィルタに合致しない場合、その子は `parent_seq` を保ったまま返る。画面は「親が結果に含まれていない子」をトップレベルに並べる（`GuiDesign.md` 5.4）。

親を補完すると、**フィルタに合致しない行が一覧に現れ、`total` と表示件数が食い違う**。「進行中だけを見たい」ときに未着手の親が混ざるのは、フィルタの意味を壊す。

### 9.2.5 ETag

2.7 に従い `ETag` を返す。値は **①フィルタ条件を正規化した文字列のハッシュ ②結果の件数 ③結果の `MAX(updated_at)`** から生成する。

```
ETag: W/"tkt-a3f19c2b-48-1723372992000000000"
```

**フィルタ条件をハッシュに混ぜるのは本エンドポイント固有である。** 5.1 の `/projects` と違い、条件の組み合わせが多く、「件数と最終更新が同じで内容が違う結果」が現実に起こりうる（`?type=bug` と `?type=task` が偶然どちらも12件で最終更新が同じ、など）。

**`ticket_tag` の付け外しは `ticket.updated_at` を動かす。** これはアプリ側の責務である（`ticket_tag` に対する更新は `ticket` のトリガでは拾えない）。動かさないと、タグだけを変えた場合に ETag が変わらず、`304` が返り続ける。

## 9.3 `POST /api/v1/projects/:key/tickets`

**必要権限**：`ticket.create`

```json
{
  "type": "task",
  "title": "認証APIの実装",
  "body_md": "ローカルID/PW認証のAPIを実装する。",
  "priority": "high",
  "assignee_id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
  "parent_seq": 12,
  "tag_ids": ["01K2..."],
  "sprint_id": "01K2...",
  "estimate_point": 5,
  "start_date": "2026-08-09",
  "due_date": "2026-08-14"
}
```

| フィールド | 検証 |
|---|---|
| `type` | 必須。`epic` / `story` / `task`（`DbDesign.md` 6.6） |
| `title` | 必須。1〜200文字 |
| `body_md` | 任意 |
| `priority` | 任意。`lowest` 〜 `highest` |
| `assignee_id` | 任意。**当該プロジェクトの `project_member` であること**。違えば `422`（`details[].code = "not_a_member"`） |
| `parent_seq` | 任意。同一プロジェクトに存在すること |
| `tag_ids` | 任意。すべて当該プロジェクトのタグであること |
| `sprint_id` | 任意。当該プロジェクトのスプリントであること |
| `estimate_point` / `estimate_hours` | 任意。0以上 |
| `start_date` / `due_date` | 任意。両方あるとき `start_date <= due_date`（`DbDesign.md` 6.6 の `ck_ticket_dates`） |

**サーバが決めるもの（リクエストに含められない）**

| 項目 | 決め方 |
|---|---|
| `seq` | `project_counter` の1文 `UPDATE ... RETURNING`（`DbDesign.md` 6.4.1） |
| `status_key` | プロジェクトのワークフローのうち **`category='todo'` かつ `sort_order` 最小**のステータス。該当が無ければ `sort_order` 最小のステータス |
| `sort_key` | 現在の末尾の次（9.4 の LexoRank） |
| `reporter_id` | 呼び出し元のアクター |
| `staged_at` | **常に `NULL`**（バックログへ入る）。オンステージへ上げるのは 9.4 の `move` である |
| `version` | `1` |

`201 Created`（`Location: /api/v1/projects/my-app/tickets/31`）。応答は 9.5 の `GET` と同形式。

**採番・ワークフロー解決・タグ付与・`activity` 記録は単一トランザクションで行う。** 5.3 の `POST /projects` と同じ方針である。

**作成したチケットは必ずバックログに入る。** オンステージは「いま仕掛り中で、直近のスプリントで消化すべきもの」（`DbDesign.md` 6.6）であり、**上げる操作は人が段へドラッグしたときだけ**にする。作成時に指定できると、新規チケットが黙って仕掛りに混ざる。

**初期ステータスをリクエストで指定できないようにしている。** ワークフローの入口は `workflow_transition` に定義されておらず（遷移元が無い）、任意のステータスで作成できると 9.6 の遷移検証を素通りできてしまう。作成後に遷移させれば同じ状態に到達でき、その経路は検証を通る。

## 9.4 `POST /api/v1/projects/:key/tickets/:seq/move`

**必要権限**：`ticket.edit`

バックログのドラッグ&ドロップによる並べ替え（`GuiDesign.md` 5.4）。

```json
{ "after_seq": 44 }
```

| 指定 | 意味 |
|---|---|
| `{"after_seq": 44}` | 44 の直後へ |
| `{"before_seq": 44}` | 44 の直前へ |
| `{"after_seq": 44, "before_seq": 12}` | 44 と 12 の間へ |
| `{"position": "first"}` | 段の先頭へ |
| `{"position": "last"}` | 段の末尾へ |
| `{"staged": true, "position": "first"}` | **オンステージ**へ上げ、その先頭に置く |
| `{"staged": false, "after_seq": 44}` | **バックログ**へ戻し、44 の直後に置く |

`position` と `after_seq` / `before_seq` の同時指定は `422`。いずれも無い場合も `422`。

```json
{ "seq": 31, "sort_key": "0|hzzzr:", "staged_at": "2026-08-23T11:20:00Z",
  "version": 4, "rebalanced": false }
```

### 9.4.1 `staged` — 段を変える

**`staged` は任意で、省略すると段は変わらない**（並べ替えだけを行う）。`true` でオンステージへ、`false` でバックログへ戻す。`staged_at` は `true` のとき `now()`、`false` のとき `NULL` になる（`DbDesign.md` 6.6）。

**段と位置を1回のリクエストで決める。** ドラッグ&ドロップの1操作で両方が同時に決まるためで、2本のエンドポイントに分けると、途中で失敗したときに「段は移ったが位置は末尾」という中途半端な状態が残る。

**`position` は段の中で解釈する**（`staged` を伴うときは移動先の段、省略したときは現在の段）。`"first"` は「オンステージの先頭」であって「プロジェクト全体の先頭」ではない。**空の段へ最初の1件を落とすとき、基準にできる行が無い**ためこの解釈が要る。

**`after_seq` / `before_seq` の基準は段を問わない。** `sort_key` はプロジェクト内で1本であり（9.4）、どの行の隣を指定しても位置は一意に定まる。

**段に置けるのは表示上のトップレベルだけである**——親を持たないもの、または**親がエピックのもの**（`GuiDesign.md` 5.4）。それ以外に `staged: true` を送ると `422`（`details[].code = "not_stageable"`）。配下は親と一緒に運ばれるので、子を個別に上げる操作は意味を持たない。

**エピック自身も置けない。** 親を持たないので上の条件だけでは通ってしまうが、**エピックはどちらの段にも行として出ない**ため、上げても見えない状態になる（`GuiDesign.md` 5.4）。同じ規則を `pb dev seed` の定義ファイル検証も持つ。

**`PATCH` で `sort_key` を直接書かせない。** LexoRank の桁生成規則をクライアントに持たせると、Web・MCP・将来の CLI がそれぞれ同じ規則を実装することになり、1つでもずれると順序が壊れる。**順序キーの生成はサーバに1つだけ置く**（5.1 で `progress` をサーバ計算にしたのと同じ理由）。設計方針1の「状態遷移など名詞で表せない操作のみ `POST /:id/<action>` を許す」に当たる。

**`rebalanced`** は、隣接する2つのキーの間に新しいキーを作れず、プロジェクト全体の `sort_key` を振り直したことを示す。`true` のとき、**クライアントは一覧を取り直す**（手元の `sort_key` がすべて古くなっているため）。

**`If-Match` は要求しない。** 2.8 の archive / unarchive と同じく、競合しても失われる編集内容が無い（`sort_key` はフォームで編集する項目ではない）。ただし**`version` は他の更新と同じく +1 する**。並べ替えの直後に詳細画面が `409` を返す可能性があるが、規約を1本に保つことを優先する。実運用で不都合が出たら 2.8 ごと見直す（10.2）。

**並び順はプロジェクト内で1本である。** グループ化（親・タグ・スプリント）は表示上の区切りにすぎず、グループを切り替えても `sort_key` は変わらない。グループごとに別の順序を持たせると、軸を変えるたびに順序が失われる。

**二段（バックログ／オンステージ）も同じ1本を共有する。** 2つの表は同じ「消化順」の部分集合であり、順序キーを段ごとに持つと、**段を行き来するたびにどちらを更新するかを決めることになり、戻したときの位置が失われる**（`DbDesign.md` 6.6）。

## 9.5 `GET | PATCH | DELETE /api/v1/projects/:key/tickets/:seq`

### 9.5.1 `GET`

**必要権限**：`ticket.view`

チケット詳細画面（`GuiDesign.md` 5.5）のデータ源。9.2 の `items[]` に以下を加えたものを返す。

| 追加項目 | 内容 |
|---|---|
| `body_md` | 本文（Markdown ソース） |
| `parent` | 親の `{seq, title, type, status}`。無ければ `null` |
| `children` | 直下の子の `[{seq, title, type, status, assignee}]`（孫は含めない） |
| `dod` | 完了条件の配列（9.9） |
| `links` | 関連リンクの配列（9.10.1） |
| `references` | 外部参照の配列（9.10.2）。`kind` の昇順、同じ `kind` の中は `sort_order` → `created_at` の昇順 |
| `comment_count` | コメント件数（本文は含めない）。`deleted_at IS NULL` のものを数える |

**コメント本体と変更履歴は含めない。** コメントはページングを持ち（9.8）、履歴は既定で畳まれている（`GuiDesign.md` 5.5）。画面は起動時に本エンドポイントと `GET .../comments` の**2本**を呼ぶ。履歴は開いたときに3本目を遅延で呼ぶ。8章の「起動時1〜2本」に収まる。

**`dod` / `links` は手順18a から実数を返す**（9.9 / 9.10.1）。**`references` は手順17c から実数を返す。****`comment_count` は手順17 から実数を返す**——9.6 の遷移が `kind='progress'` のコメントを作るため、0 を固定で返すと事実と食い違う。

**`references` を別の `GET` に切らず詳細応答へ入れるのは、`dod` / `links` と同じ理由である**（8章の「起動時1〜2本」）。件数は1チケットあたり数件で、遷移先の一覧（9.7）のように**開いたときだけ要るもの**ではない——画面を開いた時点で見えている（`GuiDesign.md` 5.5）。

### 9.5.2 `PATCH`

**必要権限**：`ticket.edit`。ただし `assignee_id` を変える場合は `ticket.assign` も必要

`If-Match: "3"` による楽観ロック（2.8）。**省略時は `422`**。成功すると `version` が +1 される。送られたフィールドだけを更新する。

変更可能：`type` `title` `body_md` `priority` `assignee_id` `parent_seq` `tag_ids` `sprint_id` `estimate_point` `estimate_hours` `actual_hours` `start_date` `due_date`

**含められないフィールド**

| フィールド | `details[].code` | 理由 |
|---|---|---|
| `id` `seq` `version` `created_at` `updated_at` `reporter_id` | `immutable_field` | サーバが決める（5.5 と同じ扱い） |
| `sort_key` `staged_at` | `use_move_endpoint` | 9.4（段の出し入れも `move` が行う） |
| `status_key` `closed_at` | `use_transition_endpoint` | 9.6 |

`immutable_field` / `use_move_endpoint` / `use_transition_endpoint` はいずれも **`details[].code` の値**であって 2.5.1 の `error.code` ではない（`error.code` は `validation_failed`）。5.5 と同じ規約である。

**`tag_ids` は丸ごと置き換える**（部分更新ではない）。`settings` と同じ方針（5.5）。空配列でタグを全て外す。

**`parent_seq` に `null` を送ると親を外す。** 自分自身または自分の子孫を親に指定した場合は `422 validation_failed`、`details[].code = "parent_cycle"`。**循環検出はアプリ層で行う**（DBの `ck_ticket_not_self_parent` は自己参照しか防げない。`DbDesign.md` 6.6）。

**`type` と `parent_seq` の変更は、オンステージの規則を破れない**

`staged_at` が入っているチケット（オンステージ。`GuiDesign.md` 5.4）に対して、**段に置けなくなる変更を行うと `422 validation_failed`、`details[].code = "not_stageable"`** を返す。該当するのは次の2つで、いずれも 9.4.1 が `move` で弾いている条件と同じものである。

| 変更 | 弾く理由 |
|---|---|
| `type` を `epic` にする | エピックはどちらの段にも行として出ないので、上げても見えない（9.4.1） |
| `parent_seq` をエピック以外のチケットにする | 配下は親と一緒に運ばれるので、子を個別に段へ置く操作は意味を持たない（9.4.1） |

**自動で段から降ろす（`staged_at` を `NULL` にする）方式は採らない。** 種別や親を変えただけのつもりの利用者が、オンステージから消えたことに気づく手段がないためである。**先に `move` で段から降ろしてから変更する**、という順序を求める。

バックログにあるチケット（`staged_at` が `NULL`）はこの制限を受けない。降りている限り、どの種別・どの親にも変えられる。

**`activity` の記録は変更した項目ごとに1行**

`field` / `old_value` / `new_value` を埋めた行を、**変更が実際に生じた項目の数だけ**書く（`DbDesign.md` 6.8）。9.13.2 の応答がこの3列を持つのは、`GuiDesign.md` 5.5 の変更履歴が「いつ担当が誰から誰へ変わったか」を出すためである。更新1回につき1行にすると「更新した」しか残らない。

**`body_md` だけは `old_value` / `new_value` を `NULL` にする**（`field` は記録する）。本文は長く、9.13.2 は `per_page=20` の一覧APIなので、20件ぶんの Markdown を載せると 8章の「応答を軽く保つ」方針に反する。**「前の本文に戻す」は履歴一覧の仕事ではない**——本文の版管理が要件になったら別の仕組みとして設計する（10.2 に起票）。

値の送信はあったが内容が現在値と同じだった項目は、**変更が生じていないので記録しない。** `version` は 2.8 の規約どおり +1 する。

応答は 9.5.1 と同形式。

### 9.5.3 `DELETE`

**必要権限**：`ticket.delete`

物理削除（`DbDesign.md` 4.6 の既定）。`204 No Content`。

**子チケットは削除しない。** `ticket.parent_id` は `ON DELETE SET NULL` であり、子は親を失ってトップレベルへ上がる。**この挙動を確認ダイアログに明示する**（`GuiDesign.md` 6.3。「3件の子チケットは削除されず、親のないチケットになります」）。

コメント・DoD・リンク・タグ付けは `ON DELETE CASCADE` で消える。`activity` は `entity_id` で残るが、参照先のチケットは存在しなくなる。

**`activity` は消さず、`action='delete'` を1行足す。** `activity.entity_id` は多相参照で FK を持てないため（`DbDesign.md` 6.8）、チケットを消しても行は追従しない。**これは設計どおりの挙動である**——9.13.2 は「削除されたチケットの行は `entity_seq` / `entity_title` が `null` になる。画面は『削除されたチケット』と表示する」と定めており、**残る前提で組まれている**。消してしまうと、手順19 のダッシュボード「最近の動き」から「誰がどのチケットを消したか」が読めなくなる。

削除の行も `field` / `old_value` / `new_value` は持たない（作成と同じ）。何が消えたかは、同じ `entity_id` を持つ既存の行が答える。

**本節を定義したのは、`ticket.delete` 権限が `DbDesign.md` 7.2 の権限カタログに存在するのに、対応するエンドポイントがどこにも無かったためである。**

## 9.6 `POST /api/v1/projects/:key/tickets/:seq/transition`

**必要権限**：`ticket.transition`。加えて `workflow_transition.required_permission` が設定されていればその権限も必要

```json
{ "to": "in_review", "comment": "レビューをお願いします" }
```

**検証の順序**（`DbDesign.md` 6.5）

| # | 検証 | 失敗時 |
|---|---|---|
| 1 | `to` がプロジェクトのワークフローに存在するステータスか | `422 validation_failed`（`details[].code = "unknown_status"`） |
| 2 | 現在のステータスから `to` への `workflow_transition` が定義されているか | `409 invalid_transition` |
| 3 | 呼び出し元の `actor.kind` が `allowed_actor_kinds` に含まれるか | `403 forbidden` |
| 4 | 遷移先の `is_agent_reachable` が `false` で、呼び出し元がエージェントか | `403 forbidden` |
| 5 | `required_permission` を呼び出し元が持つか | `403 forbidden` |

3〜5 が `Requirements.md` 10.10.4「承認ゲートをAPIレベルで強制する」の実体である。**画面側の制御に依存しない。**

**`closed_at` の規則**

| 遷移先の `category` | `closed_at` |
|---|---|
| `done` | `now()` を設定 |
| `done` 以外 | `NULL` へ戻す |

**`closed_at` は遷移の副作用としてのみ動く。** これにより 9.2 の `?open=true`（`closed_at IS NULL`）が「完了していないもの」と一致することが保証される。`PATCH` で直接書けないようにしているのは（9.5.2）、両者がずれると一覧と集計が食い違うためである。

`comment` が付いていれば、**同じトランザクションで `kind='progress'` のコメントを作る**（`DbDesign.md` 6.7）。`activity` には `action='transition'` で記録する。

応答は 9.5.1 と同形式。**`version` は +1 される。**

**`If-Match` は要求しない。** 2.8 の archive / unarchive と同じ理由に加え、**遷移そのものが競合を検出する**ためである。2人が同時に「進行中 → レビュー」を実行した場合、後発は「レビュー → レビュー」の遷移を要求することになり、`workflow_transition` に定義が無いため検証2で `409 invalid_transition` になる。ヘッダによる保護を足す必要がない。

## 9.7 `GET /api/v1/projects/:key/tickets/:seq/transitions`

**必要権限**：`ticket.view`

詳細画面のステータスドロップダウン（`GuiDesign.md` 5.5）に出す選択肢を決める。

**`items[]` はワークフローの全ステータス（現在のものを除く）である。** 遷移が定義されている先だけに絞らない。以下は `with_review` テンプレート（`DbDesign.md` 7.4）を使うプロジェクトで、進行中のチケットを見た場合である。

```
GET /api/v1/projects/my-app/tickets/31/transitions
```

```json
{
  "current": { "key": "in_progress", "name": "進行中", "category": "in_progress" },
  "items": [
    { "key": "todo",   "name": "未着手",   "category": "todo",   "allowed": true },
    { "key": "review", "name": "レビュー中", "category": "review", "allowed": true },
    { "key": "done",   "name": "完了",     "category": "done",   "allowed": false,
      "reason": "進行中から完了へは直接進められません" }
  ]
}
```

**遷移できない先も `allowed: false` と `reason` を付けて返す。** 設計原則4「権限で見えないを作る」は**メニュー項目**についての規則であり、ここでは適用しない。ステータスは業務上の到達点であり、存在ごと隠すと「なぜ完了にできないのか」が分からなくなる。`reason` はそのまま画面に出せる日本語とする（2.5 と同じ方針）。

**この理由は、遷移が定義されていない先にこそ強く効く。** 上の例で `done` を返さないと、`with_review` のプロジェクトでは**レビューを通す必要があること自体が画面から読めなくなる**。

### `reason` の文言

`allowed: false` になる条件は、9.6 の検証表のうち2〜5 に対応する。**検証1（`to` がワークフローに存在しない）はここでは起こらない**——`items[]` はワークフローから組み立てるためである。

| 9.6 の検証 | 条件 | `reason` |
|---|---|---|
| 2 | `workflow_transition` に定義が無い | `<現在の名前>から<遷移先の名前>へは直接進められません` |
| 3 | 呼び出し元の `actor.kind` が `allowed_actor_kinds` に無い | `この状態への変更は<種別>からは行えません` |
| 4 | 遷移先の `is_agent_reachable` が `false` で、呼び出し元がエージェント | `この状態へはエージェントから変更できません` |
| 5 | `required_permission` を持たない | `<権限キー> 権限が必要です` |

**判定の順序は 9.6 と同じにする。** 複数に当たる場合は先の検証の `reason` を返す——利用者が最初に取り除くべき障害がそれだからである（権限を得ても遷移が定義されていなければ進めない）。

**種別の日本語は `user` = 「ユーザー」、`agent` = 「エージェント」、`system` = 「システム」**（`DbDesign.md` 6.1 の `actor.kind`）。

**このエンドポイントを別に置くのは、9.5.1 の詳細応答に埋めると `PATCH` のたびに再計算が要るためである。** ドロップダウンを開いたときにだけ呼べばよい。

## 9.8 コメント

```
GET|POST    /api/v1/projects/:key/tickets/:seq/comments
PATCH|DELETE /api/v1/projects/:key/tickets/:seq/comments/:id
```

| メソッド | 必要権限 | 備考 |
|---|---|---|
| `GET` | `ticket.view` | 既定 `sort=created_at`・`order=asc`・`per_page=50` |
| `POST` | `comment.create` | |
| `PATCH` | `comment.edit_own`（自分のもののみ） | 他人のものは `403` |
| `DELETE` | `comment.delete_any`、または `comment.edit_own` かつ自分のもの | |

**`DELETE` の必要権限は OR である。** ルート定義には両方を並べ、**どちらか一方でも
持っていればミドルウェアを通す**（`Design.md` 6.4.4 の「`routes.go` を眺めるだけで
必要権限が分かる」を保つため）。「自分のものか」は行を読まないと決まらないので、
**その判定だけをハンドラで行う**——`comment.delete_any` を持たない呼び出し元が
他人のコメントを消そうとした場合は `403 forbidden`。

`POST` / `PATCH` の本体：

| フィールド | 検証 |
|---|---|
| `body_md` | 必須。1文字以上 |
| `kind` | `discussion`（既定） / `decision` / `artifact` / `caveat` / `reference` / `progress` |
| `in_reply_to` | 任意。同じチケットのコメントの ULID |

**`in_reply_to` の参照先は、同じチケットの、削除されていないコメントであること。**
満たさない場合は `422 validation_failed`、`details[].code = "not_found"`（9.14）。

**`PATCH` で変えられるのは `body_md` と `kind` だけである。** `in_reply_to` を送ると
`422 validation_failed`、`details[].code = "immutable_field"`。返信先を後から
付け替えるとスレッドの形が変わり、**既に読まれた並びが崩れる。**

**削除は論理削除**（`DbDesign.md` 4.6 / 6.7 の `deleted_at`）。削除済みも `items` に残し、`body_md` を `null`、`deleted_at` を設定した形で返す。画面は「削除されました」と表示する。

**削除済みのコメントへの `PATCH` / `DELETE` は `404`。** 論理削除でも「もう無い」として
扱う。二重削除が `204` で通ると、`activity` に同じ削除が2行並んで履歴が読めなくなる。

**`origin` は応答に含める**（`human` / `agent`）。`GuiDesign.md` 5.5 が、エージェントのコメントをアバターの形（角丸四角）で人間と区別すると定めている。**リクエストでは指定できない**（呼び出し元のアクター種別から決まる）。

### コメントの応答

```json
{
  "items": [
    { "id": "01K2...", "body_md": "レビューをお願いします",
      "kind": "progress", "in_reply_to": null, "origin": "human",
      "author": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "created_at": "2026-08-27T02:10:00Z",
      "updated_at": "2026-08-27T02:10:00Z", "deleted_at": null },
    { "id": "01K2...", "body_md": null,
      "kind": "discussion", "in_reply_to": "01K2...", "origin": "human",
      "author": { "id": "01K2...", "kind": "user", "display_name": "佐藤" },
      "created_at": "2026-08-27T03:00:00Z",
      "updated_at": "2026-08-27T04:00:00Z",
      "deleted_at": "2026-08-27T04:00:00Z" }
  ],
  "page": 1, "per_page": 50, "total": 2, "total_pages": 1
}
```

`POST` は `201 Created` ＋ `Location` ＋ 作った1件（`items[]` と同じ形）。
`PATCH` は `200 OK` ＋ 更新後の1件。`DELETE` は `204 No Content`。

**`author` は 9.5.1 の `assignee` / `reporter` と同じ形である**（`{id, kind, display_name}`）。
`actor.kind` が `agent` のとき、画面はアバターを角丸四角にする（`GuiDesign.md` 5.5）。
**`author` が `null` になることはない**——`comment.author_id` は `NOT NULL` かつ
`ON DELETE RESTRICT` で、投稿者不在のコメントを DB が許さない（`DbDesign.md` 6.7）。

**削除済みの行も `total` に数える。** `items` に残す以上、`total` から外すと
ページの件数と合わなくなる。**9.5.1 の `comment_count` だけは `deleted_at IS NULL` で
数える**——あちらは見出し「コメント (4)」を作るための数であり、「**読める**コメントが
何件あるか」を答える（`GuiDesign.md` 5.5）。**同じ表を数えて違う答えを返すのは意図的である。**

### コメントのページネーションと `ETag`

**2.6 のページネーションを持つ**（既定 `per_page=50`、上限 200）。許可する `sort` は
`created_at` のみ、`order` は `asc` / `desc`。**チケットの子資源で 2.6 を持つのは
コメントだけである**——DoD（9.9）・リンク（9.10.1）・外部参照（9.10.2）はいずれも
1チケットあたり数件に収まるが、**コメントは議論の量だけ増える。**

**一覧は `ETag` を返す**（2.7）。値は `W/"cmt-<件数>-<MAX(updated_at) のナノ秒>"`。
**論理削除も `updated_at` を動かす**ので（`DbDesign.md` 6.7 の `trg_comment_updated`）、
削除が `304` に埋もれない。

**`ETag` を持つ子資源はコメントだけである。** 2.7 が「Phase 1 ではポーリングを実装しないが
応答ヘッダだけ先に用意する」と定めており、**Phase 2 のエージェントが「新しいコメントが
付いたか」を安く見る口がここになる**（`Requirements.md` 1章）。DoD とリンクは画面を
開いた時点で詳細応答（9.5.1）に入っており、単独で追う対象にならない。

**`If-Match` は持たない**（2.8 の楽観ロックの対象は `project` と `app_user` に限られる）。
**親チケットの `version` と `updated_at` も動かさない**——コメントの増減は `ticket` の
列を変えないためで、外部参照（9.10.2）と同じ扱いである。

### コメントの変更を `activity` に記録する

投稿・編集・削除のいずれも **`activity` に1行書く**（9.1.1。利用者の判断、2026-08-27）。

| 列 | 値 |
|---|---|
| `entity_type` / `entity_id` | `ticket` と**親チケットの id**（コメントの id ではない） |
| `action` | **常に `update`** |
| `field` | `comment` |
| `old_value` | 投稿のときは `NULL`。編集・削除は**変更前の要約** |
| `new_value` | 削除のときは `NULL`。投稿・編集は**変更後の要約** |

**要約は `<kind の表示名>: <本文の先頭40字>`**（40字を超えたら末尾に `…`）。
9.5.2 が `body_md` の `old_value` / `new_value` を `NULL` にするのは、**本文が長いまま
20件ぶん載ると 8章の「応答を軽く保つ」に反する**ためであり、**その理由は要約すれば消える。**
履歴に「何が書かれたか」が1行も残らないと、手順19 の「最近の動き」が
「コメントが変わった」しか言えなくなる。

**`action` に `create` / `delete` を使わない理由は 9.10.2 と同じである**——`ticket:31` の
`action='delete'` は「そのチケットが消された」を意味しており（9.5.3）、
コメント1件の削除に同じ値を当てると**チケットごと消えたように見える。**

**9.6 の遷移に添えたコメントは、`activity` に別行を書かない。** 遷移そのものが
`action='transition'` の1行として記録されており（9.6）、**同じ1回の操作に対して
「状態を変更した」と「コメントが付いた」の2行が並ぶと、履歴が同じ出来事を二重に見せる。**
本文は `GET .../comments` に `kind='progress'` として並ぶので、失われるものは無い。

## 9.9 完了条件（DoD）

```
GET|POST     /api/v1/projects/:key/tickets/:seq/dod
PATCH|DELETE /api/v1/projects/:key/tickets/:seq/dod/:id
```

**必要権限**：`GET` は `ticket.view`、更新系は `ticket.edit`

| フィールド | 検証 |
|---|---|
| `type` | **Phase 1 は `manual` のみ**。他の値は `422`（`details[].code = "phase_2_only"`） |
| `body` | 必須。完了条件の文 |
| `is_satisfied` | 真偽値。`PATCH` でチェックを付け外しする |
| `sort_order` | 並び順。省略時は末尾 |

`is_satisfied` を `true` にしたとき、サーバが `satisfied_at` と `satisfied_by`（呼び出し元）を設定する。`false` に戻すと両方 `NULL` へ戻す。

`assertion`（コマンド実行）・`artifact`（成果物の存在確認）・`review`・`task_ref` は Phase 2（`Requirements.md` 10.5.2、`GuiDesign.md` 5.5）。**表とその列は Phase 1 から `DbDesign.md` 6.11 の形で作り、API が受け付ける `type` だけを絞る。** 後から列を足すより、使わない列を持つほうが安い。

**`type` は `POST` で省略できる**（既定 `manual`）。**`PATCH` で送ると `422`、
`details[].code = "immutable_field"`**——Phase 1 で取りうる値が1つしかない以上、
変更を受け付けても何も起こせない。Phase 2 で他の型を開けるときに、
**型ごとに `config` の形が違う**（`DbDesign.md` 6.11）ので、そこで改めて設計する。

### 完了条件の応答

```json
{
  "items": [
    { "id": "01K2...", "type": "manual", "body": "ユニットテストが通ること",
      "is_satisfied": true, "satisfied_at": "2026-08-27T05:00:00Z",
      "satisfied_by": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "sort_order": 10,
      "created_at": "2026-08-27T02:10:00Z", "updated_at": "2026-08-27T05:00:00Z" },
    { "id": "01K2...", "type": "manual", "body": "設計文書を更新すること",
      "is_satisfied": false, "satisfied_at": null, "satisfied_by": null,
      "sort_order": 20,
      "created_at": "2026-08-27T02:11:00Z", "updated_at": "2026-08-27T02:11:00Z" }
  ]
}
```

**`items[]` は `sort_order` → `created_at` の昇順。** 第2キーを置くのは 9.10.2 と同じく
**順序が実行ごとに揺れないようにする**ためである。

`POST` は `201 Created` ＋ `Location` ＋ 作った1件。`PATCH` は `200 OK` ＋ 更新後の1件。
`DELETE` は `204 No Content`。

**`satisfied_by` は 9.5.1 の `assignee` と同じ形である**（`{id, kind, display_name}`）。
`ON DELETE SET NULL` なので、チェックした人を消した後は `null` になる——
**条件を満たした事実は消えず、誰が満たしたかだけが分からなくなる。**

**`config` / `evidence` / `origin` は返さない**（`DbDesign.md` 6.11 の列としては残る）。
いずれも Phase 2 の型と AI提案のためのもので、**Phase 1 の API が受け付けない値を
応答に並べると「使える」ように見える。** Phase 2 で `type` を開けるときに、
同じ改訂でこの3つも応答へ足す。

**`sort_order` を省略したときは末尾（現在の最大値 + 10）。** 9.10.2 の外部参照と同じ
採番で、**10 刻みにするのは間に挿し込む余地を残すため**である。

**ページネーション・`ETag`・`If-Match` はいずれも持たない**（9.10.2 と同じ）。
1チケットあたり数件に収まり、詳細応答（9.5.1）の `dod` に同じ一覧が入る。
**親チケットの `version` と `updated_at` も動かさない。**

**完了条件を満たしていなくても、`done` への遷移は止めない**（Phase 1）。9.6 の
検証の順序（`DbDesign.md` 6.5）に DoD は含まれず、**チェックリストは人が読む道具**である。
自動判定に基づいて遷移を止めるのは、`assertion` / `artifact` が入る Phase 2
（`Requirements.md` 10.5.2「AIが完了と言ったから完了」の回避）——**判定できない型で
遷移を止めると、人が自分のチェック漏れで進めなくなるだけになる。**

### 完了条件の変更を `activity` に記録する

追加・更新・削除のいずれも **`activity` に1行書く**（9.1.1。利用者の判断、2026-08-27）。
列の使い方は 9.10.2 と同じで、**`field` は `dod`、`action` は常に `update`** である。

| 変更 | `old_value` | `new_value` |
|---|---|---|
| 追加 | `NULL` | `body` |
| 本文の変更 | 変更前の `body` | 変更後の `body` |
| チェックを付ける | `未: <body>` | `済: <body>` |
| チェックを外す | `済: <body>` | `未: <body>` |
| 並び順だけの変更 | — | **記録しない** |
| 削除 | `body` | `NULL` |

**並び順だけの変更を記録しないのは、`move`（9.4）を記録しないのと同じ理由である**
（利用者の判断、2026-08-23）——順序を1回入れ替えただけで変更履歴が埋まり、
業務履歴として読む価値が薄い。

**チェックの付け外しに接頭辞を付けるのは、`is_satisfied` が真偽値だからである。**
`true` / `false` をそのまま載せると履歴が「`false` → `true`」としか言わず、
**どの条件を満たしたのかが読めない。**

**本文とチェックを1回の `PATCH` で同時に変えたときも1行に畳む**（`済: <新しい body>` へ
変わったものとして記録する）。9.5.2 が「変更した項目ごとに1行」と定めるのはチケットの
列の話で、**DoD の1件は画面でも1行として読まれる**（`GuiDesign.md` 5.5）。

## 9.10 関連リンクと外部参照

**チケットから何かを指す仕組みは2つある。区別は「指す先が PB の中か外か」である。**

| 節 | 指す先 | 表 | 実装手順 |
|---|---|---|---|
| 9.10.1 | 同じプロジェクトの別のチケット | `ticket_link`（`DbDesign.md` 6.6） | 18 |
| 9.10.2 | リポジトリ・コミット・仕様書（**PB の外**） | `ticket_reference`（`DbDesign.md` 6.12） | **17c** |

**分けるのは、指す先に FK を張れるかどうかが違うためである。** チケット間リンクは
`target_ticket_id` の FK で整合性を DB が保証できるが、外部参照が指す先は PB の管理外にあり、
**URL が生きているかを PB は知らない。** 同じ表に混ぜると、片方にしか効かない制約が並ぶ。

### 9.10.1 チケット間リンク

```
GET|POST /api/v1/projects/:key/tickets/:seq/links
DELETE   /api/v1/projects/:key/tickets/:seq/links/:id
```

**必要権限**：`GET` は `ticket.view`、更新系は `ticket.edit`

```json
{ "target_seq": 12, "link_type": "blocks", "lag_days": 0 }
```

| フィールド | 検証 |
|---|---|
| `target_seq` | 必須。**同一プロジェクト内**に存在すること（無ければ `422`、`details[].code = "not_found"`）。自分自身は `422`、`details[].code = "self_link"` |
| `link_type` | `FS` / `SS` / `FF` / `SF`（ガント用の依存）、`relates` / `duplicates` / `blocks` |
| `lag_days` | 整数。既定 `0`。`FS`〜`SF` のときのみ意味を持つ |

**同じ `(source, target, link_type)` の組が既にあるときは `409 already_exists`。**
`uq_ticket_link`（`DbDesign.md` 6.6）が一意なキーであり、2.5.1 の `already_exists` は
まさにこれを表す（プロジェクトキーの重複と同じ扱い）。`message` は
「同じ関連はすでに登録されています」。**`conflict` を使わない**——あちらは `If-Match`
不一致のような**状態**の競合で、こちらは**値**が既存の行と衝突している。

**逆向き（`target` → `source`）の同じ `link_type` は別の行として作れる。** 一意制約が
向きを含むためである。`A blocks B` と `B blocks A` は業務上は矛盾するが、
**それを禁じるのは DB でもこの API でもない**——依存の循環検出は Phase 2 のガントで扱う（10.2）。

`GET` の応答は、**当該チケットが `source` である行と `target` である行の両方**を返し、`direction` を付けて区別する。

```json
{
  "items": [
    { "id": "01K2...", "direction": "outgoing", "link_type": "blocks",
      "ticket": { "seq": 45, "title": "ticketテーブル定義", "type": "task",
                  "status": { "key": "todo", "name": "未着手", "category": "todo" } },
      "lag_days": 0, "origin": "human",
      "created_at": "2026-08-27T02:10:00Z" },
    { "id": "01K2...", "direction": "incoming", "link_type": "blocks",
      "ticket": { "seq": 12, "title": "DB設計", "type": "story",
                  "status": { "key": "done", "name": "完了", "category": "done" } },
      "lag_days": 0, "origin": "human",
      "created_at": "2026-08-27T02:11:00Z" }
  ]
}
```

**双方向を1本の `GET` で返す。** `GuiDesign.md` 5.5 の「関連チケット」は「ブロック元」と「ブロック先」を同じリストに並べる。2回問い合わせると N+1 になる（設計方針3）。

**`ticket` は 9.5.1 の `parent` と同じ形である**（`{seq, title, type, status}`）。
`type` を持つのは、**画面が行の先頭に種別アイコンを出す**ためである（`GuiDesign.md` 5.4 / 5.5）。
**`direction` は「相手がどちら側か」を表す**——`outgoing` なら `ticket` が `target`、
`incoming` なら `ticket` が `source` である。どちらの場合も `ticket` に入るのは**相手**であり、
自分は入らない。

**`items[]` は `direction`（`outgoing` → `incoming`）、同じ向きの中は
`link_type` → `ticket.seq` の昇順。** 9.10.2 と同じく、順序が実行ごとに揺れないようにする。

**`DELETE` は `direction` を問わない。** `incoming` の行——相手のチケットが `source` で
ある行——も、このエンドポイントから消せる。`GuiDesign.md` 5.5 が両方を同じリストに
並べる以上、**片方だけ消せないと画面に「消せない行」が混ざる。** `204 No Content`。

**`PATCH` は持たない。** 一意制約が `(source, target, link_type)` である以上、
`link_type` の変更は**別の行になるのと同じ**であり、消して作り直すのと変わらない。
`lag_days` だけのために1本増やす利得も無い——**Phase 1 に `lag_days` を読む画面が無い**（下記）。

**ページネーション・`ETag`・`If-Match` はいずれも持たない**（9.10.2 と同じ）。
**親チケットの `version` と `updated_at` も動かさず、相手側のチケットも動かさない**
——リンクの増減はどちらの `ticket` の列も変えないためである。

**プロジェクトを跨ぐリンクは Phase 1 では作れない。** `DbDesign.md` 6.6 の `ticket_link` に制約は無いが、API が `target_seq` で受ける以上、同一プロジェクトに閉じる（9.1）。跨ぐ必要が出た時点で `target` の指定方法ごと設計する（10.2）。

`origin` は `human` / `ai_suggested`。**Phase 1 は `human` のみ作られる**（AI提案の採用・却下は Phase 2。`GuiDesign.md` 5.5）。

**Phase 1 の画面が出す `link_type` は `relates` / `duplicates` / `blocks` の3つだけである**
（利用者の判断、2026-08-27。`GuiDesign.md` 5.5）。`FS` / `SS` / `FF` / `SF` と `lag_days` は
**ガントの依存線**のためのもので、ガントは Phase 2（`GuiDesign.md` 3.2）。
**読む画面が無い値を人に選ばせても、入れた本人が結果を確かめられない。**
**API は7種すべて受け続ける**——MCP とエージェントがガント用の依存を先に積むことは
妨げない（`Requirements.md` 10.5）。

#### リンクの変更を `activity` に記録する

追加・削除のいずれも **`activity` に1行書く**（9.1.1。利用者の判断、2026-08-27）。
列の使い方は 9.10.2 と同じで、**`field` は `link`、`action` は常に `update`** である。

| 変更 | `old_value` | `new_value` |
|---|---|---|
| 追加 | `NULL` | `blocks my-app-12` |
| 削除 | `blocks my-app-12` | `NULL` |

**要約は `<link_type> <相手の完全形ID>`**（9.1 の `<key>-<seq>`）。
**操作したチケット側に1行だけ書き、相手のチケットの履歴には書かない**——1回の操作で
2行増えると、手順19 の「最近の動き」で同じ出来事が二重に見える。
`direction` を要約に含めないのも同じ理由で、**読み手はそのチケットの履歴を見ている。**

### 9.10.2 外部参照

```
GET|POST     /api/v1/projects/:key/tickets/:seq/references
PATCH|DELETE /api/v1/projects/:key/tickets/:seq/references/:id
```

**必要権限**：`GET` は `ticket.view`、更新系は `ticket.edit`

表は `ticket_reference`（`DbDesign.md` 6.12）。`kind` は `code` と `doc` の2つで、**必須の項目が違う。**

```json
{ "kind": "code", "repository": "my-app", "branch": "pb/31",
  "commit_sha": "a1b2c3d4e5", "url": "https://github.com/…/commit/a1b2c3d4e5",
  "label": "認証ハンドラを追加", "note": null }
```

```json
{ "kind": "doc", "url": "https://…/auth-design.md", "label": "認証設計メモ", "note": null }
```

| フィールド | 検証 |
|---|---|
| `kind` | 必須。`code` / `doc`。**作成後は変えられない**（`details[].code = "immutable_field"`） |
| `repository` | **`kind='code'` のとき必須**。1〜200文字。**`project.settings` の `repositories`（5.9.1）と突き合わせない**（`DbDesign.md` 6.12） |
| `branch` | 任意。255文字まで |
| `commit_sha` | 任意。64文字まで。**短縮形を許す**——書き手が `git rev-parse --short` の出力をそのまま入れられるようにする |
| `url` | **`kind='doc'` のとき必須**。1〜1000文字（5.9.1 のリポジトリURLと同じ上限） |
| `label` | 任意。200文字まで |
| `note` | 任意。500文字まで |
| `sort_order` | 整数。省略時は末尾（現在の最大値 + 10） |

**URL の形式は検証しない**（空でないことのみ）。5.9.1 のリポジトリと同じ方針である。
**`https://` または `http://` で始まるものだけを画面がリンクにする**（`GuiDesign.md` 5.5）。

**`PATCH` は送られたフィールドだけを更新するが、更新後の行が必須の条件を満たすこと。**
`kind='doc'` の `url` に `null` を送る、`kind='code'` の `repository` を空にする、といった
要求は **`422 validation_failed`** で弾く（`details[].field` はその項目、`code` は
`required`）。DB の `CHECK`（`DbDesign.md` 6.12）が最後の砦だが、そこで落ちると
2.5 の形式ではなく 500 になるため、アプリ側で先に見る。

```json
{
  "items": [
    { "id": "01K2...", "kind": "code", "repository": "my-app", "branch": "pb/31",
      "commit_sha": "a1b2c3d4e5", "url": "https://github.com/…/commit/a1b2c3d4e5",
      "label": "認証ハンドラを追加", "note": null, "sort_order": 0,
      "created_by": { "id": "01K2...", "kind": "agent", "display_name": "claude-code" },
      "created_at": "2026-08-27T02:10:00Z", "updated_at": "2026-08-27T02:10:00Z" }
  ]
}
```

**`items[]` は `kind` 昇順（`code` → `doc`）、同じ `kind` の中は `sort_order` → `created_at` の昇順。**
第2・第3キーを置くのは、9.11 と同じく**順序が実行ごとに揺れないようにする**ためである。

**`created_by` は返すが、Phase 1 の画面は使わない**（利用者の判断、2026-08-27）。
`actor.kind`（`user` / `agent` / `system`）が入るので人が書いた行とエージェントが
書いた行を見分けられるが、**`GuiDesign.md` 5.5 は書き手のアイコンを出さないと決めた**
——このセクションが表すのは「このチケットの成果物としてリポジトリ・ブランチ・
コミットが紐づいている」という**関係**であって、行を登録したのが誰かではない。
**それでも返し、DB にも残す**——将来エージェントの書いた行を区別したくなったとき、
データが無いと遡れないためである。`ticket_reference` が `origin` 列を持たないのは
変わらず、**「誰が書いたか」の答えは1か所にしかない**（`DbDesign.md` 6.12）。

**`created_by` はコミットの committer ではない。** PB へその行を書き込んだアクターで
あり、git ホストへ問い合わせて committer を引く仕組みは持たない
（`GuiDesign.md` 5.9.1「PBがこのURLを使って自動で何かを行うことはしない」）。

**ページネーション・`ETag`・`If-Match` はいずれも持たない。** 9.11 のタグと同じ扱いで、
1チケットあたり数件に収まり、2.8 の楽観ロックの対象は `project` と `app_user` に限られる。
**親チケットの `version` も動かさない**——参照の増減は `ticket` の列を変えないため、
`ticket.updated_at` にも触れない（`ticket_tag` を動かす 9.2.5 とはここが違う）。

`DELETE` は `204 No Content`。

**`kind='code'` は積み上がる。** エージェントが作業の経過として「このブランチで始めた」
「このコミットを積んだ」を残していくため、1チケットに複数行が並ぶ
（`Requirements.md` 10.6.1 の `artifacts` の Phase 1 版）。

**Phase 1 の書き手は `/me/tokens` の API トークンを持つクライアントである**（4.4）。
エージェント用のアクターと MCP は Phase 2（`Design.md` 11章 手順21・22）であり、
**それまでは人のトークンで叩く**。画面は `code` の追加を持たず、表示と削除だけを行う
（`GuiDesign.md` 5.5）——誤って積まれた行を人が始末できる必要があるためである。

#### 変更を `activity` に記録する

追加・更新・削除のいずれも **`activity` に1行書く**（9.1.1。利用者の判断、2026-08-27）。
手順19 の変更履歴が「誰がいつどのコミットを紐づけたか」を読めるようにするためで、
**タグ・スプリントの定義変更を記録しないのとはここが違う**——外部参照はチケットに
ぶら下がる情報であり、`entity` は `ticket:31` の形にそのまま収まる（9.13.2）。

| 列 | 値 |
|---|---|
| `entity_type` / `entity_id` | `ticket` と**親チケットの id**（参照の id ではない） |
| `action` | **常に `update`** |
| `field` | `reference.code` / `reference.doc` |
| `old_value` | 追加のときは `NULL`。更新・削除は**変更前の要約** |
| `new_value` | 削除のときは `NULL`。追加・更新は**変更後の要約** |

**`action` に `create` / `delete` を使わない。** `ticket:31` の `action='delete'` は
**「そのチケットが消された」**を意味しており（9.5.3）、手順19 のダッシュボード
「最近の動き」がそう読む。参照1行の削除に同じ値を当てると、**チケットごと消えた
ように見える。** `update` ＋ `field` ＋ `old_value` / `new_value` は 9.5.2 が
チケットの項目変更に使っている形そのものである。

**要約は画面の表示と同じ形にする**（`GuiDesign.md` 5.5）。`code` は
`my-app : pb/31 : a1b2c3d`（欠けている要素は詰める）、`doc` は `label`、
`label` が無ければ `url`。**履歴を読む人が画面で見たものと同じ文字列を見る**
ことを優先し、id や JSON を入れない。

**記録しても親チケットの `version` と `updated_at` は動かない**（上記）。履歴は
「何が起きたか」の記録であり、楽観ロック（2.8）とは別の仕組みである。

## 9.11 タグAPI

```
GET|POST     /api/v1/projects/:key/tags
PATCH|DELETE /api/v1/projects/:key/tags/:id
```

| メソッド | 必要権限 |
|---|---|
| `GET` | `ticket.view` |
| `POST` / `PATCH` / `DELETE` | **`project.edit`** |

```json
{
  "items": [
    { "id": "01K2...", "name": "GUI", "sort_order": 10, "ticket_count": 12 },
    { "id": "01K2...", "name": "設計", "sort_order": 20, "ticket_count": 8 }
  ]
}
```

| フィールド | 検証 |
|---|---|
| `name` | 必須。1〜30文字。**同一プロジェクト内で一意**（重複は `409 already_exists`） |
| `sort_order` | 整数。省略時は末尾（現在の最大値 + 10） |

`items[]` は **`sort_order` 昇順、同値は `name` 昇順**。第2キーを置くのは、`sort_order` が重複したときに順序が実行ごとに揺れないようにするためである（`DbDesign.md` 6.10 の索引は `(project_id, sort_order)` までしか決めていない）。

**名前は前後の空白を取り除いてから検証する。** 長さも一意もトリム後の値で見る。

**大文字と小文字は区別する。** `uq_tag_project_name` が `text` の一意制約であり（`citext` ではない）、`GUI` と `gui` は別のタグになる。綴り違いの重複を防ぐのは、**タグを作れる場所をプロジェクト設定に限ること**（`GuiDesign.md` 5.9.4）で行っており、正規化では行わない。

**ページネーションも `ETag` も持たない**（`items` のみ）。2.6 / 2.7 は適用しない。バックログのグループ化は**全タグをセクションの順序に使う**ため（`GuiDesign.md` 5.4.1）、ページングすると2ページ目のタグがグループ化に現れず、設計上そもそも使えない。差分取得の対象になるのはチケット一覧のほうで、そちらは 9.2.5 に専用の `ETag` がある。7.1 の `GET /roles` と同じ扱いだが、**理由は「件数が固定だから」ではなく「全件が同時に要るから」である。**

**`If-Match` を要求しない。** 2.8 が楽観ロックの対象を `project` と `app_user` に限っており、`tag` は `version` 列を持たない（`DbDesign.md` 6.10）。

### 9.11.1 並べ替え

`GuiDesign.md` 5.9.4 のドラッグ&ドロップは、**`PATCH /tags/:id` の `sort_order` だけで表現する。** 専用のエンドポイントを設けない。

クライアントはドロップの確定時に、新しい並びへ `10, 20, 30, …` を割り当て直し、**値が変わった行だけ `PATCH` を送る**（1件動かすと変わるのは連続した数行だけである）。

**チケットの `POST /tickets/:seq/move`（9.4）と作りが違う。** あちらは LexoRank の桁生成規則をクライアントに持たせないための専用エンドポイントだが、タグは `sort_order` が単なる整数で、生成規則と呼べるものが無い。プロジェクトあたり数十件で頭打ちになる量でもあり、往復数が問題になる規模ではない。

**この操作は原子的ではない。** 途中で失敗すると順序が中途半端に残る。**クライアントは失敗時に一覧を取り直して画面を戻し、その場にエラーを出す**（`GuiDesign.md` 6.4）。順序が壊れても失われる情報は無く、もう一度並べ替えれば直る。原子性が要ると分かった時点で、ID の配列を受ける一括更新を足す（10.2）。

**タグは「チケットの横断的な分類」を担う。** 「どの大きな仕事の一部か」は親子関係（`parent_seq`）が答え、「どういう性質の仕事か」はタグが答える。1つのチケットは親を1つしか持てないが、タグは複数持てる。この2軸の分離が、`Requirements.md` 2章の「アジャイル/ウォーターフォール/ハイブリッドを単一の体系で扱う」を支える。

**権限を増やさない。** タグの定義は「プロジェクトの分類軸を決める」行為であり `project.edit`、チケットへの付与は「チケットの属性を変える」行為であり `ticket.edit`（9.5.2 の `tag_ids`）で足りる。`DbDesign.md` 7.2 の28件は `Design.md` 付録Aで確定済みとされており、既存権限の内側に収まるものでカタログを増やさない。

**`ticket_count` を一覧に含める。** プロジェクト設定のタグタブ（`GuiDesign.md` 5.9）が削除時に「12件のチケットで使われています」を出す（設計原則：破壊的操作の確認、6.3）。

`DELETE` は `ticket_tag` の行を `CASCADE` で消す。**使用中でも削除できる**（確認ダイアログで件数を示したうえで）。使用中の削除を禁止すると、要らなくなった分類を消すために全チケットを手で外すことになる。

**色を持たせない。** `GuiDesign.md` 8.6 が「ラベルに任意色を許さない」と定めている。ユーザーごとに色の意味が食い違い、一覧が虹色になって輝度による階層が崩れるためである。

## 9.12 スプリントAPI

```
GET|POST     /api/v1/projects/:key/sprints
PATCH|DELETE /api/v1/projects/:key/sprints/:id
```

| メソッド | 必要権限 |
|---|---|
| `GET` | `ticket.view` |
| `POST` / `PATCH` / `DELETE` | **`project.edit`** |

```json
{
  "items": [
    { "id": "01K2...", "name": "Sprint 3", "goal": "認証を通す",
      "start_date": "2026-08-05", "end_date": "2026-08-18",
      "status": "active", "ticket_count": 12, "closed_count": 5 }
  ]
}
```

| フィールド | 検証 |
|---|---|
| `name` | 必須。1〜50文字。**一意制約は無い**（`DbDesign.md` 6.9 に `UNIQUE` が無く、同名を作れる） |
| `goal` | 任意 |
| `start_date` / `end_date` | 任意。両方あるとき `start_date <= end_date`（`DbDesign.md` 6.9 の `ck_sprint_dates`） |
| `status` | `planned`（既定） / `active` / `completed` |

`name` は 9.11 と同じく**前後の空白を取り除いてから**検証する。

`items[]` は **`start_date` 降順（`NULL` は末尾）、同値は `created_at` 降順**。新しいものが上に来る並びで、`GuiDesign.md` 5.9.5 の図（`Sprint 3` が上、`Sprint 2` が下）と一致する。この画面で触るのは「これから始める／いま動いている」スプリントであり、完了済みは下へ流れてよい。

| 集計 | 定義 |
|---|---|
| `ticket_count` | `ticket.sprint_id` が当該スプリントを指す行数 |
| `closed_count` | そのうち `closed_at IS NOT NULL` の行数 |

**`closed_count` を `status_category = 'done'` で数えない。** 9.13 の `open` / `overdue` が `closed_at` を基準にしており、そちらへ揃える。`closed_at` はステータス遷移の副作用としてのみ動くため（`DbDesign.md` 6.6）、ワークフローの定義が違うプロジェクトでも意味が変わらない。

**ページネーションも `ETag` も `If-Match` も持たない**（9.11 と同じ理由）。

`DELETE` は `ticket.sprint_id` を `SET NULL` にする（`DbDesign.md` 6.9 の `fk_ticket_sprint`）。**チケットは消えない。**

**Phase 1 でスプリントの CRUD を定義する理由。** `sprint` 表は Phase 1（0009）にあり、`GuiDesign.md` 5.5 のチケット詳細もスプリント欄を Phase 1 のメタ情報として並べている。**作る手段が無いまま選択欄だけを置くと、常に空のドロップダウンになる。** バーンダウン・ベロシティを含むスプリント管理画面（`/p/:key/sprints`、Phase 2）とは別に、**定義だけをプロジェクト設定のスプリントタブで行う**（`GuiDesign.md` 5.9）。

## 9.13 `GET /projects/:key/stats` / `GET /projects/:key/activity`

プロジェクトダッシュボード（`GuiDesign.md` 5.3）のデータ源。**必要権限**：`project.view`

### 9.13.1 `GET /api/v1/projects/:key/stats`

```json
{
  "by_category": { "todo": 18, "in_progress": 8, "review": 4, "done": 36 },
  "total": 66,
  "open": 30,
  "overdue": 2,
  "stale": { "count": 3, "threshold_days": 14 },
  "unassigned": 5
}
```

**`status_category` で集計する**（`status` ではない）。ワークフローがプロジェクトごとに違っても4つのカードの意味が変わらないようにするためである（9.2.1）。

`overdue` は `due_date < 今日` かつ `closed_at IS NULL`。`stale` は `updated_at` が `threshold_days` 日より前で `closed_at IS NULL`。**閾値はサーバが持ち、応答に含めて返す**（画面に「14日以上」と出すため。文言をフロントで組み立てない）。

### 9.13.2 `GET /api/v1/projects/:key/activity`

```
GET /api/v1/projects/:key/activity?entity=ticket:31&page=1&per_page=20
```

| パラメータ | 説明 |
|---|---|
| `entity` | `ticket:31` の形。省略時はプロジェクト全体（ダッシュボードの「最近の動き」） |
| `action` | `create` / `update` / `delete` / `transition` |
| `page` / `per_page` | 2.6。既定 `per_page=20` |

```json
{
  "items": [
    { "id": "01K2...", "entity_type": "ticket", "entity_id": "01K2...",
      "entity_seq": 31, "entity_title": "認証APIの実装",
      "actor": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "action": "transition", "field": "status_key",
      "old_value": "todo", "new_value": "in_progress",
      "occurred_at": "2026-08-11T00:12:44Z" }
  ],
  "page": 1, "per_page": 20, "total": 142, "total_pages": 8
}
```

**`entity_seq` と `entity_title` を非正規化して返す。** `activity` は `entity_id`（ULID）しか持たない（`DbDesign.md` 6.8）。画面は「`my-app-31` を『進行中』に変更」と表示するため、行ごとにチケットを引くと N+1 になる（設計方針3）。**サーバは `activity` と `ticket` を1回の JOIN で取る。**

**削除されたチケットの行は `entity_seq` / `entity_title` が `null` になる**（9.5.3 が物理削除であるため）。画面は「削除されたチケット」と表示する。

**`old_value` / `new_value` は `text` のまま返す**（`DbDesign.md` 6.8 の列がそうであるため）。ステータスの表示名への変換は画面が行う。ワークフローの定義は既に `GET /projects/:key`（5.4）で手元にある。

## 9.14 チケット固有のエラーコード

2.5.1 の表に加わるもの。

| Status | `error.code` | 意味 |
|---|---|---|
| 409 | `invalid_transition` | 現在のステータスから要求された遷移が `workflow_transition` に定義されていない（9.6） |

`details[].code`（`error.code` は `validation_failed`）として加わるもの。

| `details[].code` | 意味 |
|---|---|
| `parent_cycle` | 自分自身または自分の子孫を親に指定した（9.5.2） |
| `unknown_status` | 遷移先がプロジェクトのワークフローに存在しない（9.6） |
| `not_a_member` | 担当者に指定したアクターがプロジェクトのメンバーでない（9.3） |
| `not_found` | `parent_seq` / `tag_ids` / `sprint_id` の参照先がこのプロジェクトに無い（9.3）。`target_seq` の相手がこのプロジェクトに無い（9.10.1）。`in_reply_to` の相手がこのチケットに無い、または削除済み（9.8） |
| `self_link` | 自分自身へのリンクを作ろうとした（9.10.1） |
| `immutable_field` | サーバが決める項目、または作成後に変えられない項目を送った（9.5.2 / 9.8 / 9.9 / 9.10.2） |
| `use_move_endpoint` | `sort_key` / `staged_at` を `PATCH` で変えようとした（9.5.2） |
| `not_stageable` | 表示上のトップレベルでないチケットを `staged: true` で上げようとした（9.4.1）。**オンステージのチケットを、段に置けなくなる `type` / `parent_seq` へ変えようとした場合も同じ**（9.5.2） |
| `use_transition_endpoint` | `status_key` / `closed_at` を `PATCH` で変えようとした（9.5.2） |
| `phase_2_only` | Phase 2 でのみ有効な値を指定した（DoD の `type` など。9.9） |

## 9.15 実装手順との対応

`Design.md` 11章 Phase 1 の手順16〜19 に対応する。

| 手順 | 本章の節 | 完了時にブラウザでできること |
|---|---|---|
| **16** | 9.2 / 9.3 / 9.4 / 9.11 / 9.12 | チケットを一覧・作成・並べ替え・グループ化できる |
| **17** | 9.5 / 9.6 / 9.7 | チケットを編集し、ワークフローに沿って状態を進められる |
| **17c** | **9.10.2**（＋ 9.5.1 の `references`） | チケットにリポジトリ・コミット・仕様書へのリンクが並び、子チケットを詳細から作れる |
| **18a** | 9.8 / 9.9 / **9.10.1** | （API のみ。画面は 18b） |
| **18b** | — | コメントを投稿し、完了条件と関連チケットを管理できる |
| **19** | 9.13 | プロジェクトの現況が見える |

9.1 / 9.14 は全手順に共通する規約であり、手順16 で確立する。

**手順17 は 17a（API）/ 17b（画面）/ 17c（外部参照）に分かれている**（`Design.md` 11章）。
本表の 9.5 / 9.6 / 9.7 は 17a で実装済みで、17b は画面だけを作るため本章に対応する節を持たない。

**手順18 も 18a（API）/ 18b（画面）に分かれている**（`Design.md` 11章）。18a が
9.8 / 9.9 / 9.10.1 の3資源をまとめて実装し、9.5.1 の `dod` / `links` を空配列から
実体へ差し替える。18b は `GuiDesign.md` 5.5 の3セクションを作るだけで、
**新しいエンドポイントを足さない。**
**17c の「子チケットを詳細から作る」は新しい API を足さない**——9.3 の `POST /tickets` に
`parent_seq` を添えるだけで、画面側の導線（`GuiDesign.md` 5.5）だけが増える。

---

# 10. 未解決の検討事項

## 10.1 実装順序 → `Design.md` 11章

**本節にあった独自の実装順序（1〜10）は本改訂で削除した**（2026-08-23）。`Design.md` 11章 Phase 1 の手順一覧と**同じ順序を2か所に持っており、片方だけ古くなる状態**だった（実際、rev.7 で手順番号が詰められた後も本節は追従していなかった）。

APIの実装順序は次の2か所を見る。

| 見るもの | 場所 |
|---|---|
| 全体の手順（APIと画面を含む） | `Design.md` 11章 Phase 1 |
| 9章のエンドポイントと手順16〜19 の対応 | 本書 9.15 |

## 10.2 未解決の検討事項

- **`GET /me` のキャッシュ戦略**。ロール変更が他セッションへ反映されるまでの許容遅延をどう決めるか（毎リクエスト検証はコスト、長期キャッシュは権限剥奪が効かない）
- 一覧APIの `total` を返し続けるコストが問題になる規模の見極め（Phase 2 のチケット一覧で再検討）
- **`POST /tickets/:seq/move` が `version` を +1 することの是非**（9.4）。並べ替えの直後に詳細画面の `PATCH` が `409` を返す。2.8 の規約を1本に保つことを優先したが、ドラッグ&ドロップの頻度によっては 2.8 ごと「順序の変更は `version` を動かさない」へ見直す
- **バックログの 200 件上限に達したときのフィルタ誘導が実運用で足りるか**（9.2.3）。足りなければ、スプリント・タグによるビューの分割か、`sort_key` に沿った範囲取得を検討する
- **チケット本文（`body_md`）の版管理**（9.5.2）。`activity` は「いつ誰が本文を変えたか」までを記録し、本文そのものは持たない（一覧APIの応答が重くなるため）。「前の説明に戻したい」が要件になったら、`activity` の拡張ではなく別の仕組みとして設計する
- **プロジェクトを跨ぐチケットリンク**（9.10.1）。`target_seq` は同一プロジェクトに閉じている。跨ぐ必要が出たときの指定方法（`{project_key, seq}` か ULID か）
- **`ticket.custom_fields` を API でどう開けるか**（`DbDesign.md` 6.6）。列はあるが Phase 1 の応答に含めていない。カスタムフィールドの定義（どのキーが存在するか）をプロジェクト設定に持たせるかどうかから決める必要がある
- **タグの並べ替えに原子性が要るか**（9.11.1）。いまは `PATCH /tags/:id` を複数回送る形で、途中で失敗すると順序が中途半端に残る。必要になったら ID の配列を受ける一括更新を足す
- **タグ・スプリントの定義変更を `activity` に残すか**（9.1.1）。読む画面が無いため Phase 1 では記録しない。タグ削除の追跡が運用上必要になった時点で `entity_type` を足す
- エラーメッセージの多言語化。Phase 1 は日本語固定とするが、`code` を機械可読にしてあるためフロント側での差し替えは可能
- エージェント用トークンの発行API（Phase 2）を `/me/tokens` と統合するか、プロジェクト配下（`/projects/:key/agents`）に置くか
- **`project.settings`（jsonb）の中身をサーバは検証しない**（5.5。JSONオブジェクトであることのみ）。`repositories` の必須・上限（10件／URL 1000文字／説明 200文字）は画面だけが持つ。**設定項目が増えるなら、サーバ側の検証をどこに置くかを決める必要がある**
- **`access_token.client_info` は `api` トークンでは空である**（4.4）。セッション（User-Agent）用の列で、CLI トークンには相当するものが無く、本人が付ける `name` が識別子になる。エージェント用トークンを作るときに、クライアント種別をここに入れるかを決める
- **`agent` は Phase 1 の応答で常に `null` である**（6.1）。サーバ側は型だけ置いてあり、`agent` テーブル（`DbDesign.md` 8.1）を作ったら中身を埋めて 6.1 の但し書きを外す
- **`GET /admin/users/:id` は `kind='user'` しか返さない**（6.3。エージェント・システムアクターは 404）。一覧が `kind=user` しか出さないので Phase 1 では届くが、**エージェントタブに実データが入ったら、詳細への導線を種別で分ける**必要がある
- **一覧の検索（`q`）はロールの表示名に当たるが、「エージェント」には当たらない**（6.1）。この文字列は `system_role` が `null` のときに画面が作っている代替表示でDBに無い。**エージェントタブを作るときに、種別で探せる形を決める**
- **`project_memberships[]` にプロジェクトの `status` を入れていない**（6.3）。アーカイブ済みのプロジェクトも含まれるが、画面がそれを区別できない。必要になったら 6.3 の改訂を先に出す
- **2.5.1 の11コードの既定文言が実装側（`apierr.messages`）にしかない。** 文書に持たせるなら 2.5.1 に message 列を足す
- 削除操作の監査における個人情報の保持期間（`audit_log.detail` と `actor_label` に残した表示名・メールをいつ消すか）
