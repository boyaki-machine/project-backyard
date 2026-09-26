# Project Backyard (PB) API設計書

> 本書は PB の REST API に関する**唯一の正本**である。
>
> **文書体系**：`Requirements.md`（要件）→ `Design.md`（全体設計・認証認可）→ `DbDesign.md` / 本書 / `GuiDesign.md`（領域別の正本）
>
> - 対象読者：サーバ／フロントエンド実装者（人間およびAIエージェント）
> - 関連：`DbDesign.md`（スキーマ）、`GuiDesign.md`（画面）、`Design.md` 6章（認証・認可）
> - 状態：**確定・実装済み**（本文で「未実装」「構想」と書いたものを除く）。MCP のツールは `Design.md` 8章

---

## 目次

| 章 | 内容 | 状態 |
|---|---|---|
| 1 | 本書の範囲と方針 | 確定 |
| 2 | 共通仕様 | 確定 |
| **3** | **認証・セッションAPI** | **確定** |
| **4** | **自分自身に関するAPI（/me・トークン・エージェント・第2要素・パスキー）** | **確定** |
| **5** | **プロジェクトAPI** | **確定** |
| **6** | **ユーザー管理API** | **確定** |
| **7** | **ロール・権限API** | **確定** |
| 8 | 画面とAPIの対応 | 確定 |
| **9** | **チケットAPI** | **確定** |
| **10** | **プロジェクト文書API** | **確定** |
| **11** | **アプリケーション設定API** | **確定** |
| 12 | 未解決の検討事項 | — |

---

# 1. 本書の範囲と方針

## 1.1 本書が定義する範囲

PB の全APIを定義する。**本文で「未実装」「構想」と書いたものを除き、実装済みである。**

| 章 | 範囲 | 主な消費者（`GuiDesign.md`） |
|---|---|---|
| 3 | 認証・セッション | ログイン（5.1） |
| 4 | 自分自身（`/me`・トークン・**エージェント**） | 自分の設定（5.8）。`GET /me` は全画面が起動時に依存する |
| 5 | プロジェクト | プロジェクト一覧・作成（5.2）、プロジェクト設定（5.9） |
| 6・7 | ユーザー管理・ロール・権限 | アカウント / 権限管理（5.6） |
| 9 | チケット（一覧・詳細・コメント・DoD・リンク・タグ・スプリント・集計） | バックログ（5.4）、チケット詳細（5.5）、ダッシュボード（5.3） |
| 10 | **プロジェクト文書（憲章）** | Docs（5.10）。**MCP の `pb_list_docs` / `pb_get_doc` / `pb_put_doc` もここを通る** |
| 11 | **アプリケーション設定・TLS 証明書・DB の状態** | アプリケーション設定（5.12）。**設定の3層は `Design.md` 10.3、TLS は 6.6.1 が正本** |

MCPサーバ向けのツール定義は本書の範囲外である（`Design.md` 8章）。

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

**`docs/design/openapi.yaml` は実装済みAPIの現状を OpenAPI 形式で記述したものとする。** 設計の写しではない。APIを追加・変更したステップの成果物に含めて更新する（`Design.md` 3.3）。用途は次の4つ。

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
| **本書と実装** | どちらとも限らない | 実装の誤りなら実装を直す。設計変更が必要なら**先に本書を直す**（PB の規約「実装の前に」） |
| 本書と `openapi.yaml` | — | 上の2つに分解して判断する。この対比だけでは意味を持たない（本書は未実装を含み、`openapi.yaml` は含まないため、差があること自体は正常） |

**「OpenAPI を正とする」とはしない。** それは yaml からサーバを生成する前提（スペックファースト）の規定であり、`Design.md` 3.3 はその方式を採らない。またその規定では、実装のずれが自動的に「正しい」ことになり、設計文書側の是正が起きない。

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
| 503 | `maintenance` | 保守モード中（11.13）。**`Retry-After` は伴わない** |

**この表に加わる、領域ごとのコードがある。** チケットは 9.14、バックアップの取り込みは 11.12 の
「失敗したとき」に書いてある。**実装だけに在るコードを作らない。**

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
| 上限 | `per_page` は最大 200。**`page` は `(page - 1) × per_page` が int32 に収まる範囲**（OFFSET の型） |
| ソート | `sort=updated_at&order=desc`。許可する項目はエンドポイントごとに列挙 |
| 総件数 | 常に返す。想定する規模では `COUNT(*)` のコストは問題にならない |

**カーソルページネーションは採らない。** 対象データ量が小さく、画面が「48件中 1-25件」のような表示を必要とするため（`GuiDesign.md` 5.4）。

**範囲外・解釈不能な値は既定値へ丸めず、422 `validation_failed` を返す。** `details` に項目ごとの誤りを載せ、フォームの各入力欄へ紐づけられるようにする（2.5）。`per_page` の上限超過も同様に扱い、黙って 200 へ丸めない。**呼び出し側の誤りが表に出ないまま動き続けることを避ける**ためである。未指定の項目のみ既定値を使う。

| 入力 | 応答 |
|---|---|
| `?page=0` `?page=-1` `?per_page=0` | 422。`details[].field` に `page` / `per_page` |
| `?per_page=201` | 422（200 へ丸めない） |
| `?page=abc` | 422 |
| `?page=85899347&per_page=25` | 422（`(page-1) × per_page` が int32 を超える。`details[].field` は `page`） |
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

ETag はプロジェクト集合の `MAX(updated_at)` と件数から生成する。ポーリングは実装していないが、**応答ヘッダだけ先に用意しておく**（後から追加すると全エンドポイントの改修になるため）。

**弱い検証子の `W/` は引用符の外に置く**（RFC 9110 8.8.3 の `entity-tag = [ weak ] opaque-tag`）。`"W/proj-…"` と内側に書くと、値そのものが `W/proj-…` という文字列の**強い**検証子になり、弱い比較の意味を失う。

## 2.8 楽観ロック

更新系は `version` 整数を持つリソースについて、`If-Match` による楽観ロックを行う。

```
PATCH /api/v1/projects/my-app
If-Match: "3"
```

不一致時は `409 conflict`。本章で対象とするのは `project` と `app_user` のみ。チケットは9章、文書は10章で扱う。

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
| `POST /auth/passkey/options`・`POST /auth/login/passkey` | IPあたり 10回/分。**アカウント単位の制限もロックも持たない**（パスキーは総当たりできない。`Design.md` 6.8.2） |
| その他の認証済みリクエスト | アクターあたり 600回/分 |

応答ヘッダ：`X-RateLimit-Limit` / `X-RateLimit-Remaining` / `Retry-After`

## 2.10 監査ログ

以下の操作は `audit_log`（`DbDesign.md` 6.8）に必ず記録する。

`login.success` / `login.failure` / `logout` / `password.change` / `password.reset` /
`token.issue` / `token.revoke` / `session.revoke` / `user.create` / `user.update` /
`user.delete` / `role.change` / `project.create` / `project.archive` / `permission.denied` /
`agent.register` / `agent.update` / `agent.delete` / `setting.update` /
`tls.certificate.upload` / `tls.certificate.delete` / `mfa.register` / `mfa.unregister` /
`mfa.recovery_codes.regenerate` / `mfa.reset` / `login.mfa_failure` /
`passkey.register` / `passkey.unregister` / `passkey.reset` / `login.passkey_failure` /
`database.backup` / `database.restore`

**`agent.` の3件**（4.5.6）。エージェントの登録・変更・削除は
アカウントの作成・変更・削除と同じ重みを持つ操作であり、`user.create` / `user.update` /
`user.delete` と並べてある。

**`setting.update`**（11.2）。**1回の保存が1行**で、`detail.changes[]` に変更した
キーと新旧の実効値を並べる。**サーバ全体の設定を変える操作**であり、影響範囲が1プロジェクトに
収まらないため記録する。

**`tls.certificate.*`**（11.5 / 11.6）。**`detail` には指紋・`common_name`・
有効期間を入れ、PEM と秘密鍵は入れない**——`audit_log` は長期保存される記録である。

**`mfa.*` の4件と `login.mfa_failure`**（4.6.6）。**共有秘密・
`otpauth_uri`・リカバリコードを `detail` に入れない**——同じ理由である。
**`login.mfa_failure` を `login.failure` と分けてある**のは、前者ではパスワードが
既に通っており、**総当たりの調査で見る対象が違う**ためである。

**`database.*` の2件**（11.11 / 11.12）。**書き出しにも残す**——書庫には
`app_secret` の鍵と暗号文の両方が入るので、**持ち出した事実そのものが監査の対象**である
（証明書の取り出し＝11.7 が記録を残さないのとは扱いが違う）。**`detail` に
`owner_password` を入れない**——ロール名だけを残す。**取り込みは終わったあと、別の
トランザクションで書く**（`audit_log` 自身が入れ替わるため）。**失敗したときも残す**
——表を落としたあとで落ちた場合、この記録だけが何が起きたかを伝える。

**`passkey.*` の3件と `login.passkey_failure`**（4.7.5・6.10）。**公開鍵も
`credential_id` も `detail` に入れない**——秘密ではないが、長期保存する記録に鍵の材料を残す理由が無い。
**`login.passkey_failure` も `login.failure` と分けてある。** パスキーの失敗は、挑戦が誰にも結び付いて
いないため**アカウントを特定できないことが多い**（`Design.md` 6.8.1）。メールアドレスごとに失敗を数える
`login.failure` の読み方と混ぜない。

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
| 用途 | Kubernetes の `livenessProbe`、外からの監視と確認。**配布用の compose と docker の一式は、コンテナの `healthcheck` を持たない**（イメージにこれを叩くコマンドが無く、叩き役も足さない） |
| アクセスログ | 既定では出さない（`Design.md` 10.1） |

```json
{ "status": "OK", "version": "1.4.4" }   ← PB_HEALTH_SHOW_VERSION=true のとき
```

**バージョンを既定で返さないのは、未認証の呼び出し元に対する情報開示だからである。** 稼働中のバージョンを知られると、既知の脆弱性との突き合わせを許す。デプロイ後の確認に使いたい環境でのみ有効にする。

## 2.12 セキュリティヘッダ

**すべての応答に付ける**（画面・API・`/mcp`・`/healthcheck` を問わない）。理由は `Design.md` 6.6.2。

| ヘッダ | 値 |
|---|---|
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `same-origin` |

**`Strict-Transport-Security` は出さない**（6.6.2）。

**`style-src` にだけ `'unsafe-inline'` が要る。** Vue の `:style` 束縛と CodeMirror が実行時にスタイルを当てるためで、**`script-src` は `'self'` のままである**——スクリプトの実行を止めるのはそちらであり、こちらを緩めても注入の道にはならない。

**`img-src` に `data:` が要る。** TOTP の QR を `QRCode.toDataURL` が `data:image/png;base64,…` で返す（4.6.2）。

**API の応答にも付ける。** CSP は JSON には効かないが、**`nosniff` は効く**——`Content-Type` を無視して別の型として解釈させる経路を塞ぐ。

**DBの疎通は見ない。** DB断でプロセスを再起動しても復旧しないため、liveness で落とすと不要な再起動ループを招く。DBを含む可用性確認が必要になった時点で `/ready` を別に足す（いまは作らない）。

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

### 第2要素が登録されているときは、セッションを出さない

**パスワードが通っても、確定済みの第2要素があれば挑戦を返す**（`Design.md` 6.7.4）。

```json
// 200 OK — Set-Cookie は付かない
{
  "mfa_required": true,
  "mfa_token": "pb_mfa_...",
  "methods": ["totp", "recovery_code"],
  "expires_at": "2026-09-13T02:16:40Z"
}
```

**2つの応答は `mfa_required` の有無で見分ける。** 成功応答（上）には `actor` があり、
こちらには無い。**フロントは `mfa_required` を見てコード入力へ進む**（`GuiDesign.md` 5.1）。

**`200` を返す。** パスワードは実際に通っており、**エラーではない。** また、この応答が
返るのは**正しいパスワードを送った人だけ**なので、「誰が MFA を使っているか」は漏れない。

**`methods` は挑戦ごとに組む。** 未使用のリカバリコードが1本も無ければ `["totp"]` だけになり、
画面は「リカバリコードを使う」の導線を出さない。**出せない選択肢を出さない**（5.6.2 と同じ）。

**`expires_at` は挑戦の期限**（5分）であって、セッションの期限ではない。

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

`local` の1件のみ。**OIDC/SAML（構想）を追加しても、フロントの改修が不要になる**ようこの形にしておく（`Design.md` 6.2.3）。`config` や `secret_ref` は**絶対に返さない**。


## 3.4 `POST /api/v1/auth/login/mfa`

**必要権限**：不要（挑戦トークンが本人であることの証明を兼ねる）

3.1 が `mfa_required: true` を返したときの2段目である（`Design.md` 6.7.4）。

```json
// Request（どちらか一方を送る）
{ "mfa_token": "pb_mfa_...", "code": "123456" }
{ "mfa_token": "pb_mfa_...", "recovery_code": "K7M2QX9B4T" }
```

```json
// 200 OK — 本体は 3.1 の成功応答と同一構造
{ "actor": { "..." }, "permissions": ["..."], "projects": ["..."],
  "expires_at": "2026-09-27T09:03:12Z" }
```

```
Set-Cookie: pb_session=pb_sess_...; HttpOnly; SameSite=Lax; Path=/; Max-Age=1209600
Set-Cookie: pb_csrf=...; SameSite=Lax; Path=/; Max-Age=1209600
```

| 状況 | 応答 |
|---|---|
| `mfa_token` が無い・両方送られた・どちらのコードも無い | `422 validation_failed` |
| 挑戦が存在しない・期限切れ・消費済み | `401 invalid_credentials`「確認の有効期限が切れました。もう一度ログインしてください」 |
| コードが合わない | `401 invalid_credentials`「確認コードが正しくありません」。`attempts` を加算する |
| 試行が5回に達した | `401 invalid_credentials`（挑戦を消費済みにして捨てる。以降は上の行と同じ応答） |
| 認証器が照合中に削除された | 同上（**存在しない挑戦と区別しない**） |

**新しいエラーコードを作らない。** 2.5.1 の `invalid_credentials` に `message` を被せる
（実装の `WithMessage`）。**利用者が取る行動は「もう一度やる」で共通**であり、
コードを分けても画面の分岐が増えるだけである。

**`retry_after_sec` は返さない。** 失敗しても待ち時間は生じない——第2要素の失敗は
`local_credential.failed_attempts` を増やさず、アカウントをロックしないためである
（`Design.md` 6.7.4）。抑止は挑戦ごとの5回と、2.9 の IP 単位のレート制限が担う。

**挑戦トークンは本文で送る。** Cookie に載せないので CSRF（2.4）の対象外であり、
3.1 と同じく IP 単位のレート制限だけが掛かる。**Cookie に載せると、第2要素を通す前の
状態がブラウザに残り、他のタブから使い回せる。**

**成功時は 3.1 の手順6〜8 をそのまま行う。** セッションの発行・`last_login_at`・
実効権限のキャッシュ・`login.success` の監査は、パスワードだけで通ったときと同じ経路を通る。
**監査には第2要素で通ったことを `detail.mfa` として残す。**

### リカバリコードを使ったときは残数を応答に出さない

**画面は `GET /me/mfa`（4.6.1）で残数を読む。** ログインの応答に混ぜると、
3.1 と同一構造という約束（設計方針3）が崩れる。**残り1本になったことに気づかせるのは
`/me` の画面の仕事**であり、ログイン直後の画面ではない。

## 3.5 `POST /api/v1/auth/passkey/options`

**必要権限**：不要

パスキーでのログインを始める（`Design.md` 6.8.2 の手順1）。**本文を受けない。**

```json
// 200 OK
{
  "options": {
    "publicKey": {
      "challenge": "Q2hhbGxlbmdlLTMyLWJ5dGVzLi4u",
      "timeout": 300000,
      "rpId": "localhost",
      "userVerification": "required"
    }
  },
  "expires_at": "2026-09-13T02:16:40Z"
}
```

**`options` は WebAuthn の `CredentialRequestOptions` の JSON 表現である**（WebAuthn Level 3 の
`PublicKeyCredentialRequestOptionsJSON`）。画面は `options.publicKey` を
`PublicKeyCredential.parseRequestOptionsFromJSON()` に渡し、`navigator.credentials.get()` を呼ぶ。
**PB の命名規約（snake_case。2.2）に合わせて変換しない**——ブラウザの API が受け取る形そのものであり、
変換すると画面側で戻す手間だけが増える。

**`allowCredentials` を持たない。** 誰がログインしようとしているかを知らないまま挑戦を作るので、
**アカウントの有無が応答に現れない**（`Design.md` 6.8.1）。

**`expires_at` は挑戦の期限**（5分）であり、`timeout` と同じ長さである。

| 状況 | 応答 |
|---|---|
| IP アドレスで開いている（Host がドメインでない） | `409 conflict`「IP アドレスで開いた画面ではパスキーを使えません。ホスト名で開いてください」 |

**挑戦を作るたびに、期限切れの挑戦を消す**（`DbDesign.md` 6.19）。

## 3.6 `POST /api/v1/auth/login/passkey`

**必要権限**：不要（パスキーの署名が本人であることの証明を兼ねる）

```json
// Request
{
  "credential": {
    "id": "b3JpZ2luYWwtY3JlZGVudGlhbC1pZA",
    "rawId": "b3JpZ2luYWwtY3JlZGVudGlhbC1pZA",
    "type": "public-key",
    "response": {
      "clientDataJSON": "eyJ0eXBlIjoid2ViYXV0aG4uZ2V0Ii...",
      "authenticatorData": "SZYN5YgOjGh0NBcPZHZgW4_krrmihjLHmVzzuoMdl2MFAAAAAQ",
      "signature": "MEUCIQ...",
      "userHandle": "MDFLMkY4UVczSDdZUko0TTVONlA3UThSOVM"
    },
    "clientExtensionResults": {}
  }
}
```

```json
// 200 OK — 本体は 3.1 の成功応答と同一構造
{ "actor": { "..." }, "permissions": ["..."], "projects": ["..."],
  "expires_at": "2026-09-27T09:03:12Z" }
```

```
Set-Cookie: pb_session=pb_sess_...; HttpOnly; SameSite=Lax; Path=/; Max-Age=1209600
Set-Cookie: pb_csrf=...; SameSite=Lax; Path=/; Max-Age=1209600
```

**`credential` は `PublicKeyCredential.toJSON()` の結果をそのまま入れる**（3.5 と同じ理由で変換しない）。

| 状況 | 応答 |
|---|---|
| `credential` が無い・JSON として解釈できない | `422 validation_failed`（`details[].field = "credential"`） |
| 挑戦が存在しない・期限切れ・消費済み | `401 invalid_credentials`「パスキーを確認できませんでした。もう一度お試しください」 |
| 登録されていないパスキー・署名や origin の検証に失敗・UV が無い・sign count の逆行 | 同上 |
| 利用者が無効化されている | 同上（**存在を漏らさないため区別しない**。3.1 と同じ） |

**失敗の文言を分けない。** 3.4 は期限切れとコード違いで文言を分けたが、あちらはパスワードを通した
本人にしか返らない。**こちらは誰でも叩ける**ので、「そのパスキーは登録されていない」と「署名が合わない」を
区別して見せない。理由はサーバログと監査（`login.passkey_failure` の `detail.reason`。4.7.5）に残す。

**ロックと第2要素を通らない**（`Design.md` 6.8.2）。`local_credential.failed_attempts` を増やさず、
`locked_until` も見ず、確定済みの TOTP があっても 3.1 の挑戦（`mfa_required`）を返さない。

**成功時は 3.1 の手順6〜8 をそのまま行う**（3.4 と同じ）。監査の `login.success` には
`detail.method = "passkey"` と、通ったパスキーの `passkey_id` を残す。

**3.4 と同じく、CSRF（2.4）の対象外である。** Cookie を使わない要求だからである。

---

# 4. 自分自身に関するAPI（/me）

## 4.1 `GET /api/v1/me`

**必要権限**：認証済み

全画面の起動時に呼ぶ。レスポンスは 3.1 の `200 OK` と同一構造。

フロントは `auth` ストアにこの内容を保持し、`can('ticket.create')` の判定とルーターガードに用いる（`GuiDesign.md` 7.1）。

**`permissions` はシステムロール由来の実効権限**、`projects[].permissions` は当該プロジェクトでの実効権限。`Design.md` 6.4.1 の式に従い、トークンスコープによる縮小を適用済みの値を返す。

**`access_token.project_id` があるとき、`projects[]` はそのプロジェクトだけを返す。** 所有者がほかのプロジェクトに参加していても、プロジェクト専用トークンへ名前や権限を漏らさない。

**`/me` 配下の変更系と資格情報の一覧・取得は、人間のブラウザセッションだけが使える。** `actor.kind='user'`、`token_type='session'`、Cookie 送出の3条件を満たさなければ `403`。制限付き Bearer トークンから `/me/tokens` や `/me/agents` を通じて新たな資格情報を作り、スコープやプロジェクトの上限を外してはならない。`GET /me` は Bearer トークンでも使える。

## 4.2 `PATCH /api/v1/me`

**必要権限**：本人

```json
{ "display_name": "田中", "email": "tanaka@example.com",
  "locale": "ja", "timezone": "Asia/Tokyo",
  "theme": "dark", "hue": "green" }
```

`locale` は `ja`（日本語）または `en`（英語）。言語コードは BCP 47 に合わせ、
日本語には国コードの `jp` ではなく言語コードの `ja` を使う。

`theme` / `hue` は `GuiDesign.md` 8.11 のテーマ設定。`app_user` の列に保持する。

**`system_role` は変更不可**（管理者が 6.4 で変更する）。送られた場合は無視せず `422` を返す。

**`email` は本人が変更できる。** この列はログインIDでもあるため（`Design.md` 6.2.1 手順2〜3 が
`user_identity.subject` と突き合わせる）、変更時は `subject` も同じトランザクションで
追随させる。追随させないと当人がログインできなくなる。**現在のパスワードの再入力は求めない**
——既にセッションを持つ本人の操作であり、再認証を求める箇所を他に持たないためである。

| 状況 | 応答 |
|---|---|
| 成功 | `200`。本体は `GET /me`（4.1）と**同一構造**。画面はこの応答で表示を差し替える（`GuiDesign.md` 6.4） |
| 他のユーザーが同じメールを使っている | `409 already_exists`（6.4 と同じ） |
| 形式誤り・`system_role` の送信 | `422 validation_failed` |

**楽観ロック（2.8）は課さない。** 自分の設定を同時に2箇所から編集する状況が実質無く、
課すと `GET /me` に `ETag` が要る——`GET /me` は全画面の起動時に呼ばれるため、影響が広い。
ただし `app_user.version` は加算し、6.4 の楽観ロックが壊れないようにする。

**画面上は「ログインID」と「メールアドレス」の2行に分かれている**（`GuiDesign.md` 5.8）が、
どちらも本列を指す。ログインIDは読み取り専用で、メールアドレス欄の変更に追随する。
**ログインIDと連絡先を別々に登録できるようにするのは、必要になった時点でのスキーマ変更を伴う**
。

## 4.3 `POST /api/v1/me/password`

**必要権限**：本人

```json
{ "current_password": "••••", "new_password": "••••••••••••" }
```

- 現在のパスワード検証に失敗 → `401 invalid_credentials`
- ポリシー違反（12文字未満等） → `422 validation_failed`
- `local_credential` を持たないユーザー（IdP のみ。構想） → `409 conflict`（6.6 と同じ）
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
（`token_type='agent'`）も**この3本には現れない**——本人の操作である点は同じだが、
**エージェント1件にぶら下がる資格情報**なので 4.5 が別に扱う（`Design.md` 6.5）。

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

**`scopes[]` に入るのは権限カタログのキー**（`ticket.view` / `user.manage` 等。32件。
`Design.md` 6.4.2、`DbDesign.md` 7.2 のシードが正本）。カタログに無い値は `422`。

`Design.md` 6.4.1 の実効権限は

```
( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ トークンのスコープ
```

であり、**この積は権限キーどうしの完全一致で取る**。別の語彙を混ぜると、絞ったつもりの
トークンが権限0件になるか、解釈できない語彙を通して逆に広がるかのどちらかになる。

**`Design.md` 6.5 のエージェントの既定スコープも、同じ語彙で書かれている**。
**エージェント用トークンは 4.5.3 が発行する。** スコープは**許可リストの中から選べる**
（省略時は 6.5 の既定。中身は 4.5.9 が返す）。

**`/me/tokens` の画面はスコープを選ばせない**（`GuiDesign.md` 5.8）。常に `[]` で発行するため、
発行されたトークンは本人の権限をそのまま持つ。エージェントに渡すトークンは 4.5 で
スコープを持たせて発行する。

**この3本は人間のブラウザセッションだけが使える**（4.1）。Bearer トークンから再発行すると、
制限付きトークンが `scopes: []` の無制限トークンを作れる。エージェントトークンでも
同じ経路を通ると、所有者の権限を継承し、プロジェクト制限の無い API トークンを作れてしまう。

#### 発行本数の上限

**1人あたり5本まで。** 超えると `409 conflict`。

数えるのは**失効していないもの**であり、**期限切れも含む**——4.4.1 が返す行と一致させる。
一致させないと、一覧に7行出ているのに「上限5本」と言われ、どれを失効させれば発行できるのかが
画面から読めなくなる。

**発行時はアクター行をロックし、同じトランザクション内で件数確認と挿入を行う。** トランザクションにまとめるだけでは並行リクエストが同じ件数を読めるため、5本の上限を保証できない。

上限を置くのは、**どこからアクセスしているのかを本人が把握できる本数に留める**ためである
。大量に発行するユースケースが無く、増えるほど失効し忘れが残る。

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

## 4.5 `/api/v1/me/agents` — 自分のエージェント

**必要権限**：本人

```
GET|POST      /api/v1/me/agents
PATCH         /api/v1/me/agents/:id
POST          /api/v1/me/agents/:id/tokens
DELETE        /api/v1/me/agents/:id/tokens/:token_id
GET           /api/v1/agent-client-kinds        （カタログ。必要権限は「認証済み」。4.5.7）
```

自分の端末で動くクライアント（Claude Code / VS Code+Copilot）を PB に登録し、
その資格情報を発行する（`DbDesign.md` 8.2.1、`Design.md` 6.5）。

**登録するのは本人である。** `Requirements.md` 10.9.1 の系統B（メンバーの参画）が
「誰が」の欄に「**参加する本人**」を挙げており、トークンは 10.10.3 のとおり参加者ごとに
発行する。**4.4 と同じく権限キーを要求しない**——`agent.register` / `agent.token.issue` は
**他人のエージェントを管理する**側の権限であり、自分のものには要らない。

**1件が表すのは「ある参加者の手元で動くクライアント1つ」である。** 人ではない。
同じ人が Claude Code と VS Code を使えば2件になり、2つのプロジェクトにつなぐなら
さらに分かれる（`DbDesign.md` 8.2.1）。

**権限は所有者から導く。** エージェントの実効権限は
`( 自分のシステムロール ∪ 自分のプロジェクトロール ) ∩ トークンのスコープ` であり、
**自分の権限を超えるエージェントは作れない**（`Design.md` 6.4.1 / 6.5）。

### 4.5.1 `GET /api/v1/me/agents`

```json
{
  "items": [
    { "id": "01K2...", "display_name": "私の Claude Code",
      "client_kind": "claude_code", "model_name": "claude-opus-5", "model_version": null,
      "project": { "key": "pb", "name": "Project Backyard" },
      "token_env_suffix": "MY_LAPTOP", "token_env_name": "PB_TOKEN_MY_LAPTOP",
      "trust_level": 1, "is_active": true,
      "created_at": "2026-08-30T09:03:12Z",
      "token": { "id": "01K3...", "token_prefix": "pb_agt_7",
                 "issued_at": "2026-08-30T09:03:12Z",
                 "last_used_at": "2026-08-30T10:41:00Z",
                 "expires_at": "2026-11-28T09:03:12Z", "status": "active" } }
  ]
}
```

`items[]` は `created_at` の降順。

| 項目 | 内容 |
|---|---|
| `id` | エージェントの `actor.id`。**`agent.actor_id` と同じ値**である（`DbDesign.md` 8.2.1 は `actor_id` を主キーにしている） |
| `display_name` | 本人が付けた名前。**どの端末のどのクライアントかを本人が思い出すための手がかり**であり、一意ではない |
| `project` | 参加プロジェクト。`null` にならない（登録時に必須） |
| `token` | **有効なトークンが無ければ `null`。** 1件につき有効なトークンは1本（4.5.3） |
| `is_active` | `false` は無効化されたエージェント。行は残る |
| `token_env_suffix` | **接続設定ファイルが読む環境変数の接尾**（`DbDesign.md` 8.2.1）。本人が決める。**未設定なら `null`** |
| `token_env_name` | 上に `PB_TOKEN_` を付けた**実際の変数名**。**`token_env_suffix` が `null` のときは `PB_TOKEN_<エージェントの id>`** |

**`token_env_name` を返すのは、フォールバックの規則をサーバに1つだけ置くためである**。
接尾が未設定のとき何になるかを画面と生成器が各々計算すると、**`.mcp.json` に書いた名前と
画面が出す `export` 行がずれる**——ずれても誰も気づかず、症状は「エージェントが繋がらない」に
なる。**組み立てはサーバの1か所で行い、消費側は受け取った文字列をそのまま使う。**

**`token.token`（平文）は返さない。** 返すのは `token_prefix`（先頭8文字。`pb_agt_` + 1文字）だけで、
4.4.1 と同じ扱いである。`status` は `active` / `expired`。

**ページネーションも `ETag` も持たない**（4.4.1 と同じ）。1人が持つ件数は端末とプロジェクトの
積であり、絞り込みも差分取得も意味を持たない。

### 4.5.2 `POST /api/v1/me/agents`

```json
// Request
{ "display_name": "私の Claude Code", "project_key": "pb",
  "client_kind": "claude_code", "model_name": "claude-opus-5",
  "token_env_suffix": "MY_LAPTOP" }
```

```json
// 201 Created
{ "id": "01K2...", "display_name": "私の Claude Code",
  "client_kind": "claude_code", "model_name": "claude-opus-5", "model_version": null,
  "project": { "key": "pb", "name": "Project Backyard" },
  "token_env_suffix": "MY_LAPTOP", "token_env_name": "PB_TOKEN_MY_LAPTOP",
  "trust_level": 1, "is_active": true,
  "created_at": "2026-08-30T09:03:12Z", "token": null }
```

**トークンは同時に発行しない。** 登録と発行を分けるのは、**再発行が必要になったときに同じ
経路を通す**ためである（4.5.3）。登録直後の `token` は `null` で、画面は続けて発行を呼ぶ。

| 項目 | 規則 |
|---|---|
| `display_name` | **必須**。1〜60文字（`actor.display_name` の CHECK に合わせる。`DbDesign.md` 6.2） |
| `project_key` | **必須**。**自分がメンバーであるプロジェクトに限る。** それ以外は `422`（`details[].code` は `not_found`） |
| `client_kind` | **必須**。`agent_client_kind` の `key`（`DbDesign.md` 8.2.1.1）。値域は固定せず、**4.5.7 のカタログが正本**である |
| `model_name` | 省略可。1〜100文字 |
| `model_version` | 省略可。1〜100文字 |
| `token_env_suffix` | 省略可。**`^[A-Z][A-Z0-9_]{0,40}$`**。同じ所有者の中で一意（重複は `409 already_exists`）。省略・`null` は「未設定」で、`token_env_name` が id へフォールバックする |

**`trust_level` は受け取らない。** 段階的な権限昇格の材料（`agent_run` の実績）が構想の
ため、既定値の 1 で作る（`Design.md` 6.5）。

**`capabilities` も受け取らない。** 何を能力として並べるかが決まっていない。

**自分がメンバーでないプロジェクトを 404 ではなく 422 に倒す。** `project_key` は本体の
フィールドであり、`Design.md` 6.4.5 の「存在を隠す」は**パスで指した資源**についての規約である
（他人のエージェントを指した 4.5.4 の `404` はそちら）。本体の値の誤りは 2.5 の検証エラーで返す。

| 状況 | 応答 |
|---|---|
| 成功 | `201`。上の本体 |
| 形式誤り／非メンバーのプロジェクト | `422 validation_failed` |
| 同じ（プロジェクト・クライアント種別・表示名）の組が既にある | `409 already_exists` |

### 4.5.3 `POST /api/v1/me/agents/:id/tokens`

```json
// Request
{ "expires_in_days": 90 }
```

```json
// 201 Created — token は「この応答でのみ」返る
{ "id": "01K3...", "token": "pb_agt_7f3c...", "token_prefix": "pb_agt_7",
  "scopes": ["agent.run","comment.create","doc.view","project.view",
             "ticket.assign","ticket.create","ticket.reference.edit",
             "ticket.self_edit","ticket.transition","ticket.view"],
  "issued_at": "2026-08-30T09:03:12Z",
  "expires_at": "2026-11-28T09:03:12Z", "status": "active" }
```

**`token` を返すのはこの応答だけである**（4.4.2 と同じ。DBには SHA-256 のハッシュしか残らない）。

| 項目 | 規則 |
|---|---|
| `expires_in_days` | **必須**。1〜365 の整数。無期限は許さない（`Design.md` 6.5「有効期限必須」） |
| `scopes` | 省略可。**省略すると `Design.md` 6.5 の既定10件**。渡すときは**許可リストの中だけ**（下記）。カタログに無い値・許可リスト外の値は `422` |

**画面は既定と許可リストを 4.5.9 から引く**。`scopes` が絶対指定なので、「既定に `doc.edit` を足す」を送るには既定の中身が要るが、**画面に写しを持たせない。**

**許可リストは「6.5 の既定10件 ∪ `doc.edit` ∪ `ticket.actual_point.edit`」の12件である。**

```
agent.run  comment.create  doc.view  project.view
ticket.assign  ticket.create  ticket.transition  ticket.view
ticket.reference.edit  ticket.self_edit                         ← 既定の10件
doc.edit  ticket.actual_point.edit                              ← 発行時に足せる
```

**`ticket.self_edit` も既定に入れる**。`ticket.reference.edit` と同じ理由である——**起票したチケットを直すのは実装エージェントの通常の仕事**であり、付く相手で変わらない。**`ticket.edit` は許可リストに入れない**。あれは 9.5.2 の全項目を開けるので、**エージェントが `execution_mode` や `scope` を自分で緩められる**（`DbDesign.md` 6.13）。

**`ticket.reference.edit` は「足せるもの」ではなく既定に入れる**。`doc.edit` が発行時の選択になっているのは 6.5 が「載せるかは**そのエージェントが誰に付いているか**で決まる」と定めるためだが、**作業の跡（`kind='code'` の外部参照）を積むのは実装エージェントの通常の仕事**であり、付く相手で変わらない。既定から外すと「コミットを記録できないエージェント」が既定になる。

**既に発行済みのトークンには入らない。** `scopes` は `access_token` の jsonb 列として**発行時に固定される**ため、既定を増やしても遡って効かない。**この権限を使うにはトークンを発行し直す**（本節「有効なトークンは1件につき1本」により、再発行すると古いものは失効する）。

**発行の口が `scopes` を受け取るのは、`pb_put_doc` のためである**
——`Design.md` 8.2 が「`pb_put_doc` は `doc.edit` を要求する」、6.5 が「載せるかは**その
エージェントが誰に付いているか**で決まる。PM のエージェントは持ち、実装だけを行う
エージェントは持たない」と定めているのに、**発行の口が固定では 6.5 を実行できない**。

**`ticket.close` は許可リストにも入れない。** 6.5 の禁止のうち、`doc.edit` だけが
「誰に付いているかで決まる」と書かれており、クローズは**エージェントに開けない**と
定められている（ワークフローの `is_agent_reachable = false` と `allowed_actor_kinds` で
DB レベルでも担保される。`DbDesign.md` 7.4）。

**許可リストを持つのは、画面の作りに禁止を依存させないためである。`scopes` に任意の権限キーを通すと、`/me/agents` を
叩ける本人が `user.manage` を載せたトークンを自分のエージェントへ渡せる**——所有者との積で
消えるとはいえ、アドミニストレータが所有者のときは消えない。

**トークンは所有者の権限との積になる**ため、許可リストの中でも所有者が持たない権限は付かない
——`doc.edit` を持つのは `project_admin` だけである（0017。`DbDesign.md` 8.1.4）。

**発行するトークンは `project_id` を持つ**（`access_token.project_id` にエージェントの
プロジェクトを入れる）。他プロジェクトへのアクセスは `404` になる（`Design.md` 6.4.5）。

**`client_info` にクライアント種別を入れる**（`claude_code` 等）。`api` トークンでは空だった
列で、エージェントには入れる値がある。

#### 有効なトークンは1件につき1本

**再発行すると、それまでの有効なトークンを失効させる。** 応答は `201` で、失効は暗黙に行う。
発行時はエージェントのアクター行をロックし、旧トークンの失効と新トークンの挿入を同じトランザクションで行う。並行する再発行でも新トークンが複数残らないようにする。

`/me/tokens` の「1人5本まで」と形を変えているのは、**同じ principal に複数のトークンを
持たせる理由が無い**ためである。10.10.3 が「トークンは参加者ごとに発行する。1リポジトリに
1本ではない」と言うのは **principal を分ける**話であり、1つの principal が複数の資格情報を
持つことを求めてはいない。1本に保てば、「どれが生きているか」を画面で数えなくてよい。

| 状況 | 応答 |
|---|---|
| 成功 | `201`。上の本体（**既存の有効なトークンがあれば失効させたうえで発行する**） |
| `expires_in_days` の形式誤り | `422 validation_failed` |
| 他人のエージェント・存在しない `id` | `404 not_found` |
| 無効化されたエージェント（`is_active=false`） | `409 conflict` |

### 4.5.4 `PATCH` / `DELETE` `/api/v1/me/agents/:id`

```json
{ "display_name": "私の Claude Code (mini)", "client_kind": "codex",
  "model_name": "claude-opus-5", "model_version": "20260501",
  "token_env_suffix": "MY_LAPTOP", "is_active": false }
```

いずれも省略可（送られた項目だけを更新する）。応答は 4.5.2 と同じ本体。

**`project_key` は変えられない。** 変えたければ別のエージェントとして登録する
——**そのエージェントが行った仕事はプロジェクトに属する**ので、付け替えると過去の操作の
文脈が後から変わる。

**`client_kind` は変えられる。** `project_key` と違い、変えても過去の仕事の文脈は変わらない
——同じ端末で Claude Code から別のクライアントへ乗り換えても、
「私の端末の、このプロジェクト用のエージェント」という同一性は変わらない。
**変更可能にする実利のほうが大きい**——`agent_client_kind` は**今後も増える**ので
（`DbDesign.md` 8.2.1.1）、`other` で登録した人が、PB がその種別に対応した日に移れる必要がある。
できないと登録し直し＋トークン再発行になる。

**`client_kind` を変えると一意性の判定に効く。** キーは（所有者・プロジェクト・
クライアント種別・表示名）の4つ組なので、**更新でも重複を検査して `409 already_exists` を返す**
（検査を省くとアプリを抜けてDBの一意制約に当たる）。

**`is_active: false` が無効化である。** 行は消さない（`comment.author_id` などが参照する）。
**無効化すると、そのエージェントのトークンも失効する**——無効化したのに動き続けるのは
利用者の期待に反する。

**`token_env_suffix` も変えられる**。**端末を替えたときに直せる必要がある**のは
`display_name` と同じ理由である（4.5.1）。**変えたら接続設定を取り直す**——`.mcp.json` に
古い変数名が残っていると、`~/.zshrc` を直しても繋がらない。**画面はその旨を出す**
（`GuiDesign.md` 5.8.2）。

| 状況 | 応答 |
|---|---|
| 成功 | `200` |
| 形式誤り・値域にない `client_kind`・`token_env_suffix` の形式違反 | `422 validation_failed` |
| 変更後の4つ組が既にある／`token_env_suffix` が同じ所有者の中で重複 | `409 already_exists` |
| 他人のエージェント・存在しない `id` | `404 not_found` |

**`client_kind` を変えても、発行済みトークンの `client_info` は書き換えない**
（4.5.3 が発行時の値を入れる列であり、**そのトークンがいつ何として発行されたか**を残す）。
次に発行し直したときに新しい値が入る。

#### `DELETE /api/v1/me/agents/:id` — エージェントを消す

**`204 No Content`。** エージェントの `actor` 行を物理削除する（`DbDesign.md` 4.6）。
`agent` `access_token` は `ON DELETE CASCADE` で追従するので、**そのエージェントの
資格情報は1本残らず消える**。

**消しても監査は読める。** `audit_log.actor_id` は
`ON DELETE SET NULL` で、`actor_kind` / `actor_label` を非正規化して持つ（0008）ため、
**アクターを消しても監査は読める**。人の削除（6.5）を成立させているのがこの仕組みである。
**エージェントは書き手であり、`ticket.create` / `comment.create` / `doc.edit` を
持つ資格情報を配る以上、それを完全に取り消す手段が要る。**

**削除前に、そのエージェントが書いたコメントと実行記録を付け替える。** `comment.author_id` と
`agent_run.actor_id` は `NOT NULL` かつ `ON DELETE RESTRICT` であり（`DbDesign.md` 6.7 / 8.2.4）、
付け替えないと削除そのものが失敗する。6.5 が人について定めているのと同じ手順である。

**同じ制約を持つ表が増えるたびに、ここへ足す
必要がある**ので、付け替えを1か所にまとめてある（`me_agents.go`）。

**付け替え先は `kind='agent'` の「削除されたエージェント」である。人の付け替え先（`kind='system'` の「削除されたユーザー」）と分ける**——
`GuiDesign.md` 8.4.2 はアバターの**形**で人（円）とエージェント（角丸四角）を区別しており、
`system` へ寄せると**過去のコメントが全部円になり、人が書いたように見える**。
形は恒常的に表示される属性であり、消したあとに変わると出所が読めなくなる。

**この付け替え先は `agent` 行を持たない `actor` である。** トークンを1本も持たないため
認証の経路（`Design.md` 6.4.5）には現れず、`/me/agents` にも `GET /admin/users?kind=agent` にも
出ない。**最初に必要になった削除で作る**（`CreateSystemActor` と同じ作法）。

| 状況 | 応答 |
|---|---|
| 成功 | `204` |
| 他人のエージェント・存在しない `id` | `404 not_found` |
| 有効な `task_lease` を保持中（**構想**） | `409 conflict` |

**リースのガードはまだ無い。** `task_lease` に行を書く経路（`pb_claim_task`）が
**構想である**ため（`Requirements.md` 10.3.3）、この状況は起こらない。**6.5 が人について同じガードを定めている**ので、形は揃っている。

**`ticket.working_agent_id` は削除を妨げない。** 列は `ON DELETE SET NULL` で、
消したエージェントが処理していたチケットは残り、欄だけが空になる（`DbDesign.md` 6.6）。
**リースと違って「いま走っている」ことを表さない**ので、削除を止める根拠にならない。

**監査は `agent.delete`**（4.5.6）。**`is_active: false` による無効化は残す**——
「いま止めたいが記録は残したい」と「消したい」は別の要求である（`GuiDesign.md` 5.6 が
人について「無効化を既定の導線にし、削除は `⋯` の下段に置く」と定めているのと同じ）。

### 4.5.5 `DELETE /api/v1/me/agents/:id/tokens/:token_id`

トークンを失効させる（`access_token.revoked_at` を立てる）。4.4.3 と同じ作法で、行は消さない。

| 状況 | 応答 |
|---|---|
| 成功 | `204` |
| 既に失効済み | `204`（**冪等**） |
| 他人のエージェント・存在しない `id` / `token_id`・`token_type` が `agent` でない | `404 not_found` |

### 4.5.6 監査（2.10）

| 操作 | `action` | `detail` |
|---|---|---|
| 登録 | `agent.register` | `display_name` / `client_kind` / `project_key` |
| 更新・無効化 | `agent.update` | 変更した項目。無効化は `is_active: false` |
| 削除 | `agent.delete` | 削除時点の `display_name` / `client_kind` / `project_key`、**付け替えたコメントの件数**（`reassigned_comments`） |
| 発行 | `token.issue` | `client_kind` / `project_key` / `scopes` / `expires_at`。**平文は入れない** |
| 失効 | `token.revoke` | `token_prefix`。再発行に伴う暗黙の失効もここに残す |

登録・更新・削除は `target_type='agent'`、`target_id` はエージェントの `actor.id`。
トークンの2つは 4.4.4 と同じく `target_type='access_token'`。

**`audit_log.actor_id` は操作した本人**（エージェントではない）。登録も発行も人の操作である。

### 4.5.7 `GET /api/v1/agent-client-kinds` — クライアント種別のカタログ

**必要権限**：**不要**（認証済みであればよい）

```json
{
  "items": [
    { "key": "claude_code", "display_name": "Claude Code",  "has_setup_template": true },
    { "key": "claude_desktop", "display_name": "Claude Desktop", "has_setup_template": false },
    { "key": "codex",       "display_name": "OpenAI Codex", "has_setup_template": true },
    { "key": "copilot",     "display_name": "GitHub Copilot", "has_setup_template": true },
    { "key": "gemini",      "display_name": "Gemini（CLI / Code Assist）", "has_setup_template": false },
    { "key": "other",       "display_name": "その他・OSS 等", "has_setup_template": false }
  ]
}
```

`items[]` は `sort_order` の昇順。**ページネーションも `ETag` も持たない**（7.1 / 7.2 と同じ扱い）。

**`/me/agents` の配下ではなく最上位に置く。** 本人のデータではなく**カタログ**であり、
`GET /permissions`（7.2）や `GET /roles`（7.1）と同じ性格のものだからである。**本節に置いたのは、
唯一の消費者が 4.5 の画面（`GuiDesign.md` 5.8.2）だからである**——読む人が対応を追いやすい。

**画面に対応表を持たせない。** `GET /roles` が `display_name` を返すようになった時点で
`lib/roles.ts` を廃止したのと同じ形である（`GuiDesign.md` 5.6）。**値域は今後も増える**ので
（`DbDesign.md` 8.2.1.1）、写しを置くと必ず腐る。

**`has_setup_template` は「PB が配置ファイルを出せるか」である**（`DbDesign.md` 8.2.1.1）。

**消費者が2つになった。** `GuiDesign.md` 5.8.2 の登録モーダル（**全種別を出す**。テンプレートが
無くてもエージェントは登録できる）と、5.11 のセットアップ画面（**`true` の種別だけを出す**。
選んだ先に何も出ないのを防ぐ）。**画面ごとに絞り方が違うので、フィルタはサーバでなく画面が行う。**

### 4.5.8 `GET /api/v1/me/agents/:id/setup` — 自分の接続設定

```
GET /api/v1/me/agents/:id/setup
GET /api/v1/me/agents/:id/setup.zip
```

**必要権限**：本人（4.5 と同じ。他人のエージェントは `404`）

**そのエージェントが PB に繋がるために、本人が自分の端末へ置くもの**を組み立てて返す
（`Requirements.md` 10.9.1 の系統B）。**5.7 と対になる**——あちらはリポジトリにコミットする
ファイル（1リポジトリに1回、管理者が）、こちらは**各人の手元にしか残らないもの**
（人ごとに、何度でも）。

**この口が答えるのは「MCP が使える状態になるまで」だけである。** 作業の材料をどこから
どう用意するか（clone するのか、配布物を展開するのか、PB の文書だけで足りるのか）は
**PB が知らない**ので返さない——それはプロジェクトの文書に書かれ、人もエージェントも
そこから読む（10.9.1）。

**平文のトークンは返さない。** `export` 行は**変数名まで**で、値はプレースホルダである。
**何度でも開ける画面に平文を置くのは 10.10.1 に反する**（`GET /me/agents` が平文を返さないのと
同じ理由。4.5.1）。**失った場合の復旧経路は再発行**である。

**接続できたかどうかもここでは返さない。** `GET /me/agents` の `token.last_used_at` が
既にそれを表しており（4.5.1）、**同じ事実を2か所から出すと必ず食い違う。**

#### 4.5.8.1 応答（`/setup`）

```json
{
  "agent": { "id": "01K2...", "display_name": "私の Claude Code",
             "client_kind": "claude_code", "client_display_name": "Claude Code",
             "token_env_name": "PB_TOKEN_MY_LAPTOP" },
  "project": { "key": "pb", "name": "Project Backyard" },
  "base_url": "http://localhost:8081",
  "mcp_url": "http://localhost:8081/mcp/pb",
  "transport": "direct",
  "export_line": "export PB_TOKEN_MY_LAPTOP='ここに発行したトークンを貼る'",
  "files": [
    { "path": ".mcp.json",
      "client_kind": "claude_code",
      "mode": "merge", "language": "json",
      "content": "{\n  \"mcpServers\": {\n    \"pb\": {\n      \"type\": \"http\", …" }
  ],
  "ca_trust": {
    "verification": "verified",
    "body_md": "**公開 CA の証明書なら、この手当ては要りません。** …"
  }
}
```

| 項目 | 内容 |
|---|---|
| `agent.client_display_name` | カタログの表示名（4.5.7）。**画面が対応表を持たないため**に返す（4.5.1 と同じ形） |
| `agent.token_env_name` | 接頭を付けた実際の変数名。**組み立てはサーバの1か所**（4.5.1） |
| `mcp_url` | `base_url` ＋ `/mcp/<project_key>`（`Design.md` 8.3）。**`files[]` の中に埋まっているものと同じ文字列**である |
| `transport` | `direct`（既定）または Codex の `bridge`。クエリ `?transport=bridge` は Codex のみ受け付ける。`bridge` の設定はローカル stdio の `pb-mcp-bridge` を起動する |
| `os`・`arch` | `transport=bridge` のとき両方必須。`os` は `darwin` / `windows` / `linux`、`arch` は `amd64` / `arm64`。両エンドポイントに同じ値を渡す。不足・未知の値、`direct` との併用は `422 validation_failed` |
| `base_url` | **リクエストの `Host` から組み立てた暫定値**（5.7.1 と同じ規則。スキームは `PB_COOKIE_SECURE`） |
| `export_line` | 環境変数へトークンを置く行。**`null` になることがある**（下記） |
| `files[]` | **5.7.1 の `files[]` と同じ形**（`AgentSetupFile`）。**接続設定の1枚だけ**で、`.gitignore` も手順ファイルも入らない |
| `ca_trust` | 「HTTPS の証明書を信頼させる」手順（4.5.8.1b）。**`null` にしない** |

#### 4.5.8.1a Codex の TLS と stdio ブリッジ

`direct` は Codex の Streamable HTTP 接続であり、公開 CA の証明書に使う。ローカル CA・社内 CA・
自己署名の証明書では、Codex の設定を `?transport=bridge` で取り直す。**Codex 標準の HTTP MCP
クライアントは、OS の信頼ストアへ登録した自己署名の証明書を受け付けなかった**（pb-160 の観測）。
**ローカル CA の証明書と `CODEX_CA_CERTIFICATE` での直接接続は実機未確認**で、ブリッジを正とする（pb-202）。PB は TLS 検証を無効にする設定を返さない。
生成物は `pb-mcp-bridge --url <https MCP URL> --token-env <name>` を stdio 子プロセスとして起動する。
Windows 向けだけコマンド名を `pb-mcp-bridge.exe` とする。OS と CPU は利用者が画面で指定し、
PB サーバの実行環境から推測しない。配布物は `darwin` / `windows` / `linux` × `amd64` / `arm64` の
6種類を `bridges/<os>-<arch>/pb-mcp-bridge[.exe]` に持ち、ZIP には指定された1種類だけを入れる。
該当するバイナリが無ければ、別の種類へフォールバックせずエラーにする。
Windows 向けの `export_line` と手引きは PowerShell の `$env:` 構文を使い、同じセッションから
Codex を起動するよう案内する。
ブリッジは待受を持たず、OS の信頼ストアを使う。`PB_MCP_CA_FILE` が指定された場合は、その PEM を
OS の既定信頼ストアへ追加する。ZIP の `PB-README.md` は、証明書登録または CA 指定とブリッジの
導入・削除手順を含む。

#### 4.5.8.1b `ca_trust`——証明書を信頼させる手順

**PB を HTTPS にしたとき、エージェントのクライアントに PB の証明書を信頼させる手順**を、
種別ごとに返す（`Development.md` 14.6 の「クライアントに信頼させる」の表と同じ事実）。

| 項目 | 内容 |
|---|---|
| `verification` | 実機で確かめたか。`verified`＝そのクライアントで繋がるところまで／`partial`＝下の層（Node 単体・Go の既定のクライアント）だけ／`unverified`＝確かめていない |
| `body_md` | 本文（Markdown。**見出しを持たない**）。先頭に全種別共通の前置き（公開 CA なら不要・自己署名は対象外）が付く |

**サーバが持つ。** 手順は種別の数だけ違い（Codex は `transport` でも変わる）、画面の分岐で
持つと zip の `PB-README.md` と同じ文を2か所に書くことになる（`GuiDesign.md` 5.8.2 が
「3つ目が現れたら、サーバが持つべき」と書いていた場合にあたる）。**正本は
`server/internal/agentsetup/templates/connect/catrust/*.md`** で、`PB-README.md` の
「HTTPS の証明書を信頼させる」の節に同じ本文が入る。**14.6 の表と食い違わないことはテストが
突き合わせる**（表の変数名・設定名が本文にあり、「確かめたこと」の判定が `verification` と一致する）。

**接続先が `http` でも返す。** 後から HTTPS にする人が先に読めるように、画面は常に畳んで出す。

**本文は5つの区切りで書く**——「この手順が要るとき」「先に済ませておくこと」（ここまでが全種別
共通の前置き）、**「手順」（番号付きの箇条書き。やることだけ）**、「うまくいかないとき」、「確かめたこと」。
**前提と手順を混ぜない**のは、手順の中に条件が紛れると、やることを1つずつ追えなくなるためである
（stg で利用者が求めた。テストが順序を見る）。**段落を折り返さない**——画面の Markdown は改行を
`<br>` にする。

**本文は日本語だけである**（`PB-README.md` と同じ扱い）。`verification` の札は画面が翻訳して出す。

**`ETag` もページネーションも持たない**（4.5.1 と同じ）。

#### 4.5.8.2 `export_line` が `null` になる種別がある

**2種別が環境変数を使わない。同じ `null` でも理由が違う。**

| 種別 | 環境変数を使わない理由 | トークンの渡し方 |
|---|---|---|
| `copilot` | `${input:pb-token}` で VS Code が初回に入力を求め、以降は安全に保存する（`Requirements.md` 10.8.4） | クライアントの入力欄 |
| `claude_desktop` | **GUI アプリはシェルから起動しないので、`export` した値が届かない**（10.8.4.2） | **設定ファイルの `env` に平文で書く** |

**環境変数名はそのエージェントに意味を持たない**ので、`export_line` を `null` にする。

**画面は2つを区別して書く必要がある。** どちらも `export_line` が `null` だが、
**「入力を求められる」と「ファイルに書く」は別の指示**である（`GuiDesign.md` 5.8.2）。

**画面は `null` のとき「トークンを環境変数へ置く」の節ごと出さない。** 意味のない行を
出すと、利用者は**書いていない前提を自分の期待で埋める**（`GuiDesign.md` 6.3 と同じ理由）。

**`token_env_name` は `null` にしない。** 値そのものは常に決まっており（4.5.1）、
種別を変えれば意味を持ち直すためである。

#### 4.5.8.3 配置ファイルを持たない種別では `files` が空になる

**PB が接続設定の書式を持たない種別**（`gemini` / `other`）では、
**`files` を空配列で返し、`200` を返す。** エラーにしない——**エージェントは既に登録されており、
`mcp_url` と `token_env_name` は正しく決まっている**ので、それを使って自分で書ける。

**判定に `has_setup_template` を使わない。**
**あれは系統A——リポジトリにコミットする配置ファイル——を出せるかである**（`DbDesign.md` 8.2.1.1）。
**`claude_desktop` で2つが割れた**：作業フォルダを持たないので `has_setup_template` は偽だが、
**接続設定は在る**（`Requirements.md` 10.8.4.2）。**2つの問いは元から別だったが、
`claude_desktop` が現れるまで答えが一致していた。**

**5.7.1 が未対応の種別を `422` で拒むのと形が違う。** あちらは**これから選ぶ**ものなので
「選んだ先に何も出ない選択肢を通さない」が効くが、こちらは**既に選ばれた結果**である。
**選び直しを促すために画面を空にするのは、持ち主に対して乱暴である。**

#### 4.5.8.4 `files[].mode` は常に `merge` である

**接続設定は既存の構造へ該当キーだけを足すものだからである**（`mcpServers` / `servers` /
`[mcp_servers.pb]`）。**5.7.2 の規則により、zip には別名で入る**
（`.mcp.json` → `.mcp.pb-block.json`）。

**`create` を採らなかった**。`.gitignore` に入るファイルなので
clone 直後には存在せず、**主経路では上書きの相手がいない**——それでも安全側に倒したのは、
**プロジェクトフォルダ直下で展開する人を排除できない**ためである。**他の MCP サーバを
登録済みの `.mcp.json` は履歴管理の対象外で、消えると git から戻せない。**

#### 4.5.8.5 `/setup.zip`

**`Content-Type: application/zip`**、
`Content-Disposition: attachment; filename="pb-connect-<key>-<client_kind>.zip"`。
Codex のブリッジZIPだけは `pb-connect-<key>-codex-<os>-<arch>.zip` とし、別の端末向けを
取り直したときにファイル名でも区別できるようにする。

**5.7.2 と違い、種別を名前に入れる。** 1人が同じプロジェクトに Claude Code と Copilot の
2件を持つことがあり（4.5.1）、**同じ名前の zip が2つダウンロードフォルダに並ぶと
どちらがどちらか分からなくなる。**

**zip には `PB-README.md` が1枚入る**。中身は**種別ごとに違う**
——置き場も、改名の要否も、`.gitignore` の扱いも種別で変わるためである。

| zip の中身 | 例（Claude Code） |
|---|---|
| 接続設定（**別名**） | `.mcp.pb-block.json` |
| **手引き** | `PB-README.md`（4.5.8.1b の `ca_trust` を「HTTPS の証明書を信頼させる」の節として含む） |

Codex では `transport=direct` と `transport=bridge` で手引きも分ける。直接接続ZIPは公開 CA に使うことと、ローカル CA・社内 CA・自己署名ではブリッジを選び直すことを記す。ブリッジZIPは選択した OS / CPU の `pb-mcp-bridge`（Windows は `.exe`）、OS の信頼ストアへの登録または CA PEM と `PB_MCP_CA_FILE` の指定、導入・削除を記す。Codex の設定は Finder 等で隠れない `_codex/config.pb-block.toml` として入れ、手引きで `.codex/config.toml` への改名を案内する。ブリッジZIPは実行ファイルも同梱する。

**`PB-README.md` は JSON の `files[]` に含めない。** **画面が同じ内容を節として描いている**
ためで、`files[]` に入れると「これも置くファイルだ」と読まれる。**zip にだけ入れるのは、
落として後日展開した人に画面の注意書きが届かないからである**（`GuiDesign.md` 5.11 が
「画面にだけ書くと zip の人に届かない／生成物にだけ書くと貼り終えた後にしか読まれない」と
定めたのと同じ判断で、**ここでは置く先が repo ではないので手引きを別の1枚にした**）。

**`README.md` という名前にしない。** プロジェクト直下で展開されたときに**本物の
`README.md` を消す**——それは本節が `merge` を選んだ理由そのものである。

| 状況 | 応答 |
|---|---|
| 成功 | `200`（`files` が空でも `200`。4.5.8.3） |
| 他人のエージェント・存在しない `id` | `404 not_found` |

#### 4.5.8.6 生成物には、利用者が貼り替える欄が入ることがある

**PB が値を決められない項目は、placeholder の文字列として生成物に埋める。**

| 種別 | 欄 | なぜ PB が決められないか |
|---|---|---|
| 全種別 | `export_line` の値 | **平文を返さないため**（10.10.1）。復旧経路は再発行である |
| `claude_desktop` | `command` / `env.PATH` | **相手の端末に node がどこにあるかを知らない**（`Requirements.md` 10.8.4.2） |
| `claude_desktop` | `env.<token_env_name>` | 上と同じく平文を返さないため |

**空文字やキーの省略にしない。** 欄ごと無いと、**利用者は「書かなくてよい」と読む**——
`claude_desktop` では `command` が無ければ起動そのものができない。**「ここに貼る」と
日本語で書いてある欄は、埋めるまで動かないことが形から分かる。**

**placeholder が残ったまま動く形にしない。** `command` に `npx` のような**それらしい既定を
置くと、動かないのに正しく見える**——実際には起動できず、症状はクライアント側の
「サーバが起動しない」だけで、PB には何も出ない。


### 4.5.9 `GET /api/v1/agent-scopes` — エージェント用トークンのスコープ

**必要権限**：**不要**（認証済みであればよい）。4.5.7 と同じ扱いで、中身は秘密ではない。

```json
{
  "default": ["agent.run", "comment.create", "doc.view", "project.view", "ticket.assign",
              "ticket.create", "ticket.reference.edit", "ticket.self_edit",
              "ticket.transition", "ticket.view"],
  "grantable": ["doc.edit", "ticket.actual_point.edit"]
}
```

| 項目 | 内容 |
|---|---|
| `default` | 4.5.3 で `scopes` を省略したときに入る既定（`Design.md` 6.5）。昇順 |
| `grantable` | 既定に足せるもの。4.5.3 の許可リストは `default` ∪ `grantable` である |

**画面に既定スコープの写しを持たせないために在る。** 4.5.3 の `scopes` は絶対指定なので、画面が「既定に `doc.edit` を足す」を送るには既定の中身が要る。画面が写しを持つと、**権限を既定に足すたびに腐る。** 写しが古いと、**`doc.edit` つきで発行したトークンだけが足した権限を欠き**、エージェントが 403 を踏むまで誰も気づかない。クライアント種別（4.5.7）とロールの表示名（7.1）を写しからサーバへ寄せたのと同じ形である。

**ページネーションも `ETag` も持たない**（4.5.7 と同じ）。**トークンが実際に持つ権限は所有者との積**なので（4.5.3）、ここに並ぶ権限がすべて付くとは限らない。

**採らなかった案**

- **`GET /me/agents` か `GET /permissions` に載せる。** 口は増えないが、本人のデータ（または権限カタログ全体）と、エージェント用の既定という意味の違うものが同じ応答に同居する。**再検討のきっかけは、カタログの口が増えて画面を開くときの往復が問題になったとき**である
- **要求を「足すものだけ」（`add_scopes`）に変える。** 写しそのものが要らなくなるが、`scopes` を絞る用途（read だけを持つエージェント）の口を別に設計し直すことになる。**再検討のきっかけは、read だけを持つエージェントの役割が現れたとき**である

## 4.6 `/api/v1/me/mfa` — 自分の第2要素

**必要権限**：本人（4章の他の節と同じ。権限キーを要求しない）

**設計は `Design.md` 6.7、表は `DbDesign.md` 6.18、画面は `GuiDesign.md` 5.8。**

| メソッド | パス | 用途 |
|---|---|---|
| `GET` | `/me/mfa` | 登録済みの認証器とリカバリコードの残数 |
| `POST` | `/me/mfa/totp` | 登録を始める（QR の材料を受け取る） |
| `POST` | `/me/mfa/totp/:id/confirm` | コードを照合して確定させる |
| `DELETE` | `/me/mfa/totp/:id` | 削除する |
| `POST` | `/me/mfa/recovery-codes` | リカバリコードを作り直す |

### 4.6.1 `GET /api/v1/me/mfa`

```json
{
  "totp": [
    { "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S", "name": "iPhone",
      "created_at": "2026-09-13T02:11:40Z", "last_used_at": "2026-09-13T08:20:02Z" }
  ],
  "recovery_codes": { "remaining": 8, "generated_at": "2026-09-13T02:11:40Z" }
}
```

**確定していない登録は返さない。** `confirmed_at` が `NULL` の行は認証の要素として
数えない（`DbDesign.md` 6.18）ので、**一覧に出すと「登録できている」と読める。**
失効済みのトークンを 4.4.1 が出さないのと同じ作法である。

**リカバリコードを1本も持たないときは `recovery_codes` を `null` にする。**
`remaining: 0` は「10本作って全部使った」であり、**別の状態である**（画面は前者に
「作成」、後者に「作り直す」を出す）。

**ページネーションも `ETag` も持たない。** 上限5件である（4.4.1 と同じ理由）。

### 4.6.2 `POST /api/v1/me/mfa/totp`

```json
// Request
{ "name": "iPhone" }
```

```json
// 201 Created
{
  "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
  "name": "iPhone",
  "secret": "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
  "otpauth_uri": "otpauth://totp/Project%20Backyard:tanaka%40example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Project+Backyard&algorithm=SHA1&digits=6&period=30",
  "digits": 6,
  "period_sec": 30
}
```

**`secret` と `otpauth_uri` の両方を返す。** QR を読めない環境（カメラの無い端末、
画像を表示できない設定）で手入力に落とせるようにするためであり、**同じ値の2つの表現**である。

**QR 画像を返さない。** 描くのはクライアントである（`GuiDesign.md` 5.8）。
サーバが画像を返すと、`Content-Type` の分岐とキャッシュ禁止の指定がこの1本のために要る。

| 状況 | 応答 |
|---|---|
| `name` が空・61文字以上 | `422 validation_failed` |
| 同じ名前の確定済みの認証器がある | `409 already_exists` |
| 確定済みが既に5件 | `409 conflict`「登録できるのは5件までです。いずれかを削除してください」 |

**登録の途中の行は1人1件までとし、始め直したら置き換える。** 2回続けて `POST` すると
1回目の行は消える。**利用者から見て「いま出ている QR」は1つ**であり、**捨てた QR で
確定できる余地を残さない。**

**この応答を監査に残さない。** 確定していない登録は認証に何の影響も与えず、
**記録すると `secret` を作った回数だけ行が増える。** 監査は 4.6.3 の確定で1件残す。

### 4.6.3 `POST /api/v1/me/mfa/totp/:id/confirm`

```json
// Request
{ "code": "123456" }
```

```json
// 200 OK — MFA を最初に有効にしたときだけ recovery_codes が入る
{
  "credential": { "id": "01K2...", "name": "iPhone",
                  "created_at": "2026-09-13T02:11:40Z", "last_used_at": null },
  "recovery_codes": ["K7M2QX9B4T", "9FRD3HJ5PW", "..."]
}
```

| 状況 | 応答 |
|---|---|
| コードが合わない | `422 validation_failed`（`details[].field = "code"`） |
| 同じ登録で5回失敗した | `422 validation_failed`。**途中の行を捨てる**ので、やり直しは 4.6.2 から |
| `id` が他人の・存在しない・既に確定済み | `404 not_found` |

**ここは `401` ではなく `422` である。** 既にセッションを持つ本人が入力を間違えた
だけであり、**セッションを疑う場面ではない**（3.4 は認証そのものなので `401`）。

**リカバリコードは1回しか出ない。** 4.4.2 のトークンと 6.2.1 の初期パスワードと同じ作法で、
**再表示できない旨は画面が明記する**（`GuiDesign.md` 5.8）。**2件目以降の認証器を確定
させたときは返さない**——すでに持っているコードが無効になると読めてしまう。

監査は `mfa.register`（`target_type = "user_mfa_credential"`）。

### 4.6.4 `DELETE /api/v1/me/mfa/totp/:id`

`204 No Content`。存在しない・他人のものは `404`。

**現在のパスワードを求めない。** 4.2 が「既にセッションを持つ本人の操作であり、
再認証を求める箇所を他に持たない」と定めた扱いに揃える。
**再検討の条件は、セッションの盗用を想定した見直しを行うとき**である。そのときは
パスワード変更・メール変更・第2要素の削除を**まとめて**再認証の対象にする——
**この1本だけを固くしても、同じセッションでパスワードを変えられるなら意味が無い。**

**最後の認証器を削除したら、リカバリコードも消す**（`Design.md` 6.7.5）。
応答は変わらず `204` で、画面は `GET /me/mfa` を読み直して表示を更新する。

監査は `mfa.unregister`。**消したリカバリコードの本数を `detail` に入れる**
——1つの操作が2行にならないようにする（6.6 と同じ扱い）。

### 4.6.5 `POST /api/v1/me/mfa/recovery-codes`

```json
// 200 OK
{ "recovery_codes": ["K7M2QX9B4T", "9FRD3HJ5PW", "..."] }
```

**既存のコードは未使用のものも含めて全部無効になる。** 10本を作って返す。

| 状況 | 応答 |
|---|---|
| 確定済みの認証器が1件も無い | `409 conflict`「先に認証アプリを登録してください」 |

**本文を受けない**（本数も形式も選ばせない）。**選択肢を先に作ると、使われ方を知る前に
語彙が固まる**（4.4.2 のスコープと同じ判断）。

監査は `mfa.recovery_codes.regenerate`。

### 4.6.6 監査（2.10）

| action | 記録する操作 |
|---|---|
| `mfa.register` | 4.6.3 の確定 |
| `mfa.unregister` | 4.6.4 の削除 |
| `mfa.recovery_codes.regenerate` | 4.6.5 の再発行 |
| `login.mfa_failure` | 3.4 の照合失敗（`detail.reason` に `wrong_code` / `expired` / `attempts_exceeded`） |
| `mfa.reset` | 6.9 の管理者による解除 |

**`secret` も `otpauth_uri` もリカバリコードも `detail` に入れない**（憲章「秘密と個人情報」）。
`audit_log` は管理者が読めるため（`DbDesign.md` 6.8）、**入れると他人の第2要素を作れる。**


## 4.7 `/api/v1/me/passkeys` — 自分のパスキー

**必要権限**：本人（4.6 と同じ。権限キーを要求しない）

**設計は `Design.md` 6.8、表は `DbDesign.md` 6.19、画面は `GuiDesign.md` 5.8。**

| メソッド | パス | 用途 |
|---|---|---|
| `GET` | `/me/passkeys` | 登録済みのパスキー |
| `POST` | `/me/passkeys/options` | 登録を始める（ブラウザへ渡す options を受け取る） |
| `POST` | `/me/passkeys` | 認証器の応答を検証して登録する |
| `DELETE` | `/me/passkeys/:id` | 削除する |

**4.6 と違い、登録の途中の行を作らない。** TOTP は QR を出してからコードを照合するまで行を持つが、
パスキーは認証器が署名した応答1つで確定する。途中の状態は挑戦（`webauthn_challenge`）だけにある。

### 4.7.1 `GET /api/v1/me/passkeys`

```json
{
  "items": [
    { "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S", "name": "MacBook",
      "rp_id": "localhost", "backed_up": true,
      "created_at": "2026-09-13T02:11:40Z", "last_used_at": "2026-09-13T08:20:02Z" }
  ]
}
```

`items[]` は `created_at` の昇順（4.6.1 と同じ）。

| 項目 | 内容 |
|---|---|
| `rp_id` | 登録したときのホスト名。**画面はいま開いているホスト名と比べ、違えば「このアドレスでは使えません」と出す**（`Design.md` 6.8.3） |
| `backed_up` | 端末をまたいで同期されているか（`backup_state`）。**画面は「同期」として出す**——端末を失っても他の端末で使えるかの手がかりになる |
| `last_used_at` | 一度も使われていなければ `null` |

**公開鍵も `credential_id` も返さない。** 画面に要らず、返すと一覧が鍵の材料の置き場になる。

**ページネーションも `ETag` も持たない。** 上限5件である（4.6.1 と同じ）。

### 4.7.2 `POST /api/v1/me/passkeys/options`

**本文を受けない。**

```json
// 200 OK
{
  "options": {
    "publicKey": {
      "rp": { "id": "localhost", "name": "Project Backyard" },
      "user": { "id": "MDFLMkY4UVczSDdZUko0TTVONlA3UThSOVM",
                "name": "tanaka@example.com", "displayName": "田中" },
      "challenge": "Q2hhbGxlbmdlLTMyLWJ5dGVzLi4u",
      "pubKeyCredParams": [ { "type": "public-key", "alg": -7 }, "..." ],
      "timeout": 300000,
      "excludeCredentials": [ { "type": "public-key", "id": "b3JpZ2luYWwt..." } ],
      "authenticatorSelection": { "residentKey": "required", "requireResidentKey": true,
                                  "userVerification": "required" },
      "attestation": "none"
    }
  },
  "expires_at": "2026-09-13T02:16:40Z"
}
```

**3.5 と同じく、WebAuthn の JSON 表現をそのまま返す。** 画面は
`PublicKeyCredential.parseCreationOptionsFromJSON()` に渡し、`navigator.credentials.create()` を呼ぶ。

| 項目 | 値 | 理由 |
|---|---|---|
| `user.id` | `actor.id`（ULID）の base64url | `Design.md` 6.8.4 |
| `residentKey` | `required` | メールアドレスを入力させずに引けるパスキーだけを受ける（`Design.md` 6.8.1） |
| `userVerification` | `required` | `Design.md` 6.8.1 |
| `attestation` | `none` | 機種を検証しない（`Design.md` 6.8.5） |
| `excludeCredentials` | 同じホスト名で登録済みのパスキー | **同じ認証器を二重に登録させない**——ブラウザが登録の前に断る |

| 状況 | 応答 |
|---|---|
| 既に5件登録している | `409 conflict`「登録できるのは5件までです。いずれかを削除してください」 |
| IP アドレスで開いている | `409 conflict`（3.5 と同じ文言） |

**登録の挑戦は1人1件までとし、始め直したら置き換える**（4.6.2 と同じ考え方）。**この応答を監査に残さない**
——まだ何も登録されていない。

### 4.7.3 `POST /api/v1/me/passkeys`

```json
// Request
{
  "name": "MacBook",
  "credential": {
    "id": "...", "rawId": "...", "type": "public-key",
    "response": { "clientDataJSON": "...", "attestationObject": "...",
                  "transports": ["internal", "hybrid"] },
    "authenticatorAttachment": "platform",
    "clientExtensionResults": {}
  }
}
```

```json
// 201 Created
{ "id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S", "name": "MacBook", "rp_id": "localhost",
  "backed_up": true, "created_at": "2026-09-13T02:11:40Z", "last_used_at": null }
```

**名前は認証器の応答と一緒に送る。** 4.6.2 は名前を先に受けたが、こちらは登録の途中の行を持たないので、
確定の要求で受ける。

| 状況 | 応答 |
|---|---|
| `name` が空・61文字以上、`credential` が無い | `422 validation_failed` |
| 同じ名前のパスキーがある | `409 already_exists` |
| 既に5件登録している | `409 conflict`（4.7.2 と同じ文言） |
| 挑戦が無い・期限切れ・消費済み・他人のもの | `422 validation_failed`（`details[].field = "credential"`）「登録の有効期限が切れました。もう一度やり直してください」 |
| 検証に失敗（origin・署名・UV が無い など） | `422 validation_failed`（`details[].field = "credential"`）「パスキーを確認できませんでした」 |
| 同じ認証器が登録済み（`credential_id` の重複） | `409 already_exists`「このパスキーは登録済みです」 |

**名前の重複と件数は、挑戦を消費する前に確かめる。** 名前だけを直して同じ応答を送り直せるようにするためである
（端末にはもうパスキーができている）。

**ここは `401` ではなく `422` である**（4.6.3 と同じ理由）。既にセッションを持つ本人の操作であり、
セッションを疑う場面ではない。

**挑戦は検証より先に消費する**（`Design.md` 6.8.2 と同じ）。検証に失敗したら 4.7.2 からやり直す。

**リカバリコードを出さない。** パスキーは第2要素ではなく、失ってもパスワードで入れる（`Design.md` 6.8.1）。

監査は `passkey.register`（`target_type = "user_passkey"`、`detail` に `name` と `backed_up`）。

### 4.7.4 `DELETE /api/v1/me/passkeys/:id`

`204 No Content`。存在しない・他人のものは `404`。

**現在のパスワードを求めない**（4.6.4 と同じ扱い。再検討の条件も同じ）。

**最後の1件を消しても、他に何も消さない。** パスワードで入れる状態は変わらない
（4.6.4 が最後の認証器でリカバリコードを消すのとは違う）。

**端末の中のパスキーは消えない。** PB が消せるのは自分の側の記録だけであり、端末の一覧には残る。
その端末で「パスキーでログイン」を選んでも、PB は 3.6 の `401` を返す。画面の確認ダイアログにそう書く
（`GuiDesign.md` 5.8）。

監査は `passkey.unregister`（`detail` に `name`）。

### 4.7.5 監査（2.10）

| action | 記録する操作 |
|---|---|
| `passkey.register` | 4.7.3 の登録 |
| `passkey.unregister` | 4.7.4 の削除 |
| `passkey.reset` | 6.10 の管理者による全削除 |
| `login.passkey_failure` | 3.6 の失敗（`detail.reason` に `unknown_challenge` / `expired` / `consumed` / `unknown_credential` / `inactive` / `verification_failed` / `clone_warning`） |
| `login.success` | 3.6 の成功（既存の action。`detail.method = "passkey"`） |

**公開鍵・`credential_id`・`clientDataJSON` を `detail` に入れない**（2.10）。

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

**トークンで絞る**。**エージェントのトークンでは、所有者のメンバーシップで引く**——5.4 と同じ委譲で、エージェント自身は `project_member` の行を持たないので、自分の ID で引くと必ず0件になる。**トークンにプロジェクトが紐づいていれば（`access_token.project_id`）、そのプロジェクト1件だけを返す**。所有者が他のプロジェクトのメンバーでも、管理者でも返さない（`Design.md` 6.5「他プロジェクトへのアクセス」）。**人のトークンにプロジェクトが紐づいている場合も同じ**で、個別のプロジェクト API が拒む範囲（`CanReachProject`）と一覧に出る範囲を揃える。`total` と `ETag` も同じ条件で数える。

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

**必要権限**：`project.create`（アドミニストレータのみ）

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

**サーバ側の処理**：`project` 作成、`project_counter` 初期化、テンプレートから `workflow` / `workflow_status` / `workflow_transition` を複製、**テンプレートから `document` を複製**（`DbDesign.md` 8.1.2）、作成者を `project_member`（`project_admin`）として登録。**これらは単一トランザクションで行う。**

**文書テンプレートは `template_key = 'default'` 固定である。** `workflow_template` に対応する入力フィールドを持たない——テンプレートが1種類しかないうちは、選ばせる意味がないため。2種類目を持つときに 5.3 の本体へ足すかを判断する。

**複製した文書には `revision_no = 1` を作る**（10.4 の `POST` と同じ）。テンプレートの初期本文は「ここに何を書くか」の案内であり、**消して書き直したあとに「何が書いてあったか」を見たくなる**。作成時の1件が無いと、その本文はどの版にも残らない。

**`created_by` / `updated_by` はプロジェクトの作成者**。テンプレート行の `created_by` は NULL だが（マイグレーションの時点でアクターがいない。`DbDesign.md` 8.1.2）、複製を起こしたのは作成者である。

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

`settings` は `project.settings`（jsonb）をそのまま返す。**定義するキーは `repositories` のみ**で、構造の正本は `DbDesign.md` 6.4 にある（上の例の `max_concurrent_agents` は未実装）。

**`my_role` と `my_permissions` は、エージェントのトークンでは所有者のものが出る**（`Design.md` 6.5 の委譲）。エージェントは `project_member` の行を持たないため、自分自身で引くと `my_role` が常に `null` になり、**`my_permissions` からプロジェクトロールの層が丸ごと落ちる**——認可は所有者のロールで通る（6.4.1）ので、「できるのに、できないと応答している」状態になる。`GET /me`（4.1）も同じ規則である。

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

`status` を切り替える。**物理削除のAPIは提供しない。** チケット・コメント・監査記録を巻き込むため、必要になった時点で「削除の確認方法」と併せて設計する。

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

## 5.7 エージェント連携セットアップ

```
GET /api/v1/projects/:key/agent-setup?client=claude_code&client=codex
GET /api/v1/projects/:key/agent-setup.zip?client=claude_code&client=codex
```

**必要権限**：`agent.register`（`project_admin` のみ。`GuiDesign.md` 3.2）

**リポジトリにコミットする配置ファイルを組み立てて返す**（`Requirements.md` 10.9.1 の系統A）。
**接続設定は含まない**——10.8.1 が履歴管理の対象外と定めたので、リポジトリに置くものを作る
この口では出せない。接続設定は**系統B**（`/me/agents` のエージェントごと。**4.5.8**）で本人へ渡す。

### 5.7.1 `GET /api/v1/projects/:key/agent-setup`

```json
{
  "project": { "key": "pb", "name": "Project Backyard" },
  "base_url": "http://localhost:8081",
  "workflow_version": 5,
  "clients": ["claude_code", "codex"],
  "files": [
    { "path": ".claude/commands/pb-onboard.md",
      "client_kind": "claude_code",
      "mode": "create", "language": "markdown",
      "content": "---\ndescription: PBのプロジェクトに参画する…" },
    { "path": "CLAUDE.md",
      "client_kind": "claude_code",
      "mode": "append", "language": "markdown",
      "marker_begin": "<!-- PB:BEGIN v5",
      "marker_end": "<!-- PB:END -->",
      "content": "<!-- PB:BEGIN v5 (Project Backyard が生成・管理します。…" },
    { "path": ".gitignore",
      "client_kind": null,
      "mode": "append", "language": "text",
      "content": "# Project Backyard — 各自の環境。共有しない\n.mcp.json\n…" }
  ]
}
```

| 項目 | 内容 |
|---|---|
| `client` | **繰り返し指定**。`agent_client_kind` の `key` のうち **`has_setup_template` が真のもの**（4.5.7）。1件以上必須 |
| `base_url` | **リクエストの `Host` から組み立てた PB の公開 URL**（下記） |
| `workflow_version` | 生成した手順ファイルに埋まる版番号（`Requirements.md` 10.9.3）。**手順ファイルの本文を変えたら上がる**（いまは `5`） |
| `files[].mode` | `create`（そのまま置く）／`append`（既存の末尾へ追記する）／**`merge`**（既存の構造へ該当キーだけを足す） |
| `files[].client_kind` | どのクライアント向けか。**共通のもの（`.gitignore`）は `null`** |
| `files[].marker_begin` / `marker_end` | `append` のときだけ入る。**マーカーの内側だけが PB の管理範囲**（`Requirements.md` 10.8.8） |

**`mode` を持たせるのが本節の要点である。** これが無いと、**画面が「上書きしてよいファイル」と
「壊してはいけないファイル」を同じ見た目で並べる**。10.8.8 がマーカーを要求しているのと同じ理由で、
**追記であることは受け渡しの形に現れていなければならない。**

**`merge` を `append` と分けたのは、JSON を追記できないためである**（実装中に判明）。
`.claude/settings.json` は既に130行あることがあり——**PB 自身のリポジトリがそうだった**
——丸ごと置き換えると Bash の許可設定が全部消える。`merge` が渡すのは**足す断片**で、
画面は「既にあるなら `permissions.allow` にこの中身を足してください」と案内する。

| 状況 | 応答 |
|---|---|
| 成功 | `200` |
| `client` が無い／`has_setup_template` が偽の値／未知の値 | `422 validation_failed` |
| 非メンバー・存在しないプロジェクト | `404 not_found`（6.4.5） |
| `agent.register` を持たない | `403 forbidden` |

**`ETag` もページネーションも持たない**（4.5.1 と同じ）。件数は選んだ種別で決まり、
差分取得の意味がない。

#### `base_url` はリクエストの `Host` から組み立てる（暫定）

**PB は自分の公開 URL を知らない。** 設定にあるのは待受アドレス（`PB_BIND`）だけで、
これは URL に使えない。**外から見えるアドレスを伝えるのはリクエストの `Host` ヘッダだけ**である。

**利用者がいま画面を開いている URL がそのまま入る**ので、インスタンスが複数あっても
自動で正しく分かれる。**リバースプロキシ配下では外れうる**が、生成物は画面に全文が出る
テキストなので、置く前に手で直せる。

**スキームは `PB_COOKIE_SECURE` を見る。**`Design.md` 3.1 が
この設定に「リバースプロキシで TLS を終端する構成ではアプリに平文で届くため、自動判定は
『HTTPS で公開しているのに Secure が付かない』を招く」と注記しており、**スキームの判定は
まったく同じ問題である**。`r.TLS` だけを見ると、プロキシの背後で必ず `http://` になる。
**新しい設定項目を増やさずに済む。**

**新しい設定項目（`PB_PUBLIC_URL` 等）は足さない**。
**着地はアプリケーション設定画面**（アドミニストレータが公開エンドポイント・アクセス元・TLS を
見る。`Design.md` 10.3）であり、**env を今足すと、DB へ移す日に「env と DB のどちらが正本か」を
解く仕事が増える**。加えて環境変数は端末ごとで共有されないため、**参加者間で
黙って食い違う。**

### 5.7.2 `GET /api/v1/projects/:key/agent-setup.zip`

**5.7.1 と同じ内容を zip で返す。** `Content-Type: application/zip`、
`Content-Disposition: attachment; filename="pb-setup-<key>.zip"`。

- **リポジトリ直下からの相対パスでフォルダを掘る**（`.claude/commands/pb-onboard.md` のまま）
- **`mode` が `create` でないものは別名で入れる**（`CLAUDE.md` → `CLAUDE.pb-block.md`）。
  **展開した瞬間に既存の `CLAUDE.md` を消す zip を配らない。** 拡張子の前に入れるので、
  ドットで始まる名前は末尾に付く（`.gitignore` → `.gitignore.pb-block`）
- **別パスにするのは、ブラウザの `<a download href>` で素直に落とすためである**
  （`Accept` ヘッダでの切り替えにしない）。認証は Cookie が載る

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
                 "project_key": "my-app", "trust_level": 1,
                 "owner": { "id": "01K2...", "display_name": "田中" } },
      "is_active": true, "last_login_at": "2026-08-11T08:41:00Z",
      "project_count": 1, "created_at": "2026-08-01T00:00:00Z" }
  ],
  "page": 1, "per_page": 25, "total": 4, "total_pages": 1
}
```

**`kind` によって意味を持たないフィールドは `null` を返し、フィールド自体を省略しない。** フロントの分岐を単純にするため。

**`sort=system_role` は `role.sort_order` で並べる**（`DbDesign.md` 7.3 のシード。オペレータ 10 → アドミニストレータ 20）。表示名の五十音順ではない——シードが意図して序列を持っており、カスタムロール（構想）が増えたときに表示名順では意味のない並びになるため。**`system_role` を持たない行（エージェント）は昇順・降順とも末尾に置く**（`NULLS LAST`）。ロールを持たない行が先頭に来ると、ロールで並べた意味が薄れる。

**`sort=is_active` の昇順は無効が先**（`false < true`）。状態で並べ替える動機は「無効な利用者を探す」ことが多いため、そのままにしている。

**`agent` は `kind='agent'` の行にだけ入る**（人間の行では `null`）。中身は `DbDesign.md` 8.2.1 の `agent` テーブルの列である。**0019 まで常に `null` だった**——テーブルが無く、埋められる値が1つも無かったためである。

**`owner` は「このエージェントが誰に付いているか」を表す**（`agent.owner_actor_id`）。**エージェントの実効権限はこの人から導かれる**ため（`Design.md` 6.5 の委譲）、管理者が一覧で最初に見るべき列である。**登録した人ではなく、権限の根拠である。**

**`project_count` は `project_member` の行数**で、アーカイブ済みプロジェクトも数える。除くと 6.3 の `memberships` に並ぶ件数と食い違うため。**エージェントは `project_member` の行を持たない**（権限を所有者から導くため）ので、常に `0` になる。**参加プロジェクトは `agent.project_key` のほうで読む。**

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
  "generated_password": "quiet-harbor-4172-mint"
}
```

**`password_mode=manual` のときは `generated_password: null` を返す**（キーは省略しない）。呼び出し側が既に平文を持っており、返す意味がないため。

**サーバ側の処理**：`actor` → `app_user` → `user_identity`（`provider_key='local'`, `subject=email`）→ `local_credential` を単一トランザクションで作成する（`DbDesign.md` 6.2）。

作成時は `audit_log` に `user.create` を記録する（2.10）。`target_type='app_user'` / `target_id=<actor_id>`、`detail` は `email` / `system_role` / `password_mode` / `must_change_password`。**平文のパスワードは `detail` に入れない**——`audit_log` は長期保存される記録であり、残ると「この応答でのみ返る」が崩れる。

### 6.2.1 自動生成パスワード

**読み上げ・転記しやすい語句連結方式**とする。ランダム英数字は電話やチャットでの伝達時に誤りが生じやすいため。

形式は **`<形容詞>-<名詞>-<4桁数字>-<名詞>`**（例 `quiet-harbor-4172-mint`）。語彙は形容詞64語・名詞64語で、いずれも英小文字のみ・4文字以上とし、連結が常に最小長（12文字、`Design.md` 6.3）を超えるようにする。乱数は暗号論的擬似乱数から採る。

**強度は 64 × 64 × 10⁴ × 64 ≈ 2³¹·³ である。**

**語を増やす形を採り、ランダム英数字にはしない。** 読み上げ・転記のしやすさは本節が語句連結を採る理由そのものであり、強度のために捨てない。**名詞を2回引く**ので同じ語が並ぶことはあるが、一様独立に引く限り強度は変わらない。

**単発の初期パスワードである。** `must_change_password` が既定で `true`、かつアカウントロック（5回/15分、`Design.md` 6.3）が効くため、オンラインでの推測は現実的でない。

**語彙の選定基準は試験が守る**（`genpassword_test.go`）。英小文字のみ・4文字以上・重複なしに加えて、**1文字しか違わない組を禁じる**——口頭で伝えたときに取り違えるためで、実際に `loyal` と `royal`、`bridge` と `ridge` を試験が拾った。

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
  ],
  "mfa_credential_count": 1,
  "passkey_count": 2
}
```

**`identities` 配列が IdP 連携（構想）をそのまま受け入れる。** OIDC を追加しても要素が1つ増えるだけで、レスポンス構造もUIも変わらない（`DbDesign.md` 6.2）。

**`mfa_credential_count` は確定済みの認証器の件数である**（`DbDesign.md` 6.18）。
**配列ではなく件数だけを返す。** 画面（`GuiDesign.md` 5.6.2）が出すのも件数で、
**他人の端末の名前は管理に要らない。** 0 なら `[解除]`（6.9）を `disabled` にする根拠になる。

**`passkey_count` は登録済みのパスキーの件数である**（`DbDesign.md` 6.19）。
`mfa_credential_count` と同じ理由で件数だけを返し、0 なら `[全削除]`（6.10）を `disabled` にする根拠になる。

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

**その人が所有するエージェントは、削除の前にこちらで始末する。**

**`agent.owner_actor_id` の `ON DELETE CASCADE` が消すのは `agent` の行だけである。**
FK の向きは `agent.actor_id → actor(id)` なので、**エージェントの `actor` 行・その
`access_token`・そのコメントは残る**。放置すると、起きるのは `comment.author_id` の
`RESTRICT` ではなく**孤児のアクターである**——`agent` 行を失った `actor` は
`FindAccessTokenByHash` の `LEFT JOIN agent` から外れ、**認証は通るが実効権限が0件の
トークンが残る**（`Design.md` 6.4.1 の両層が空になるため）。

したがって `DELETE /admin/users/:id` は、本人を消す前に**所有するエージェントを
4.5.4 の `DELETE /me/agents/:id` と同じ手順で1件ずつ消す**——コメントを
`kind='agent'` の「削除されたエージェント」へ付け替え、エージェントの `actor` を削除する
（`agent` と `access_token` は CASCADE で追従する）。**同一トランザクションで行う。**

**監査は本人の `user.delete` 1行に集約する**（`detail` に `deleted_agents` の件数を入れる）。
消したエージェントごとに `agent.delete` を並べない——**利用者から見た操作は1回**であり、
1人の削除で監査が数行に散ると「誰を消したか」が読みにくくなる。

| ガード | 応答 |
|---|---|
| 自分自身 | `409 self_modification_forbidden` |
| 最後の有効なアドミニストレータ | `409 last_administrator` |
| 有効な `task_lease` を保持中（**構想**） | `409 conflict` |

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
{ "generated_password": "quiet-harbor-4172-mint" }
```

| フィールド | 既定 | 説明 |
|---|---|---|
| `mode` | `generate` | **`generate` のみ受け付ける**（他は `422`）。応答が `generated_password` しか持たず、管理者が手で決めた値を返す意味が無いため。必要になれば 6.2 と同じ `password_mode` / `password` を足す |
| `must_change_password` | `true` | 6.2 と同じ既定。管理者が決めたパスワードを本人が使い続ける状態を既定にしない |

**生成される値の形式は 6.2.1 と同一である**（`<形容詞>-<名詞>-<4桁数字>-<名詞>`）。作成とリセットで生成器を2つ持たない。

当該ユーザーの `local_credential` を更新し、`failed_attempts` と `locked_until` をリセット。**全セッションを失効**する。ロックされた利用者を救うのがこの操作の主な用途であり、パスワードだけ変えてロックが残ると目的を果たさない。

### 対象が自分自身のときは、いま操作しているセッションを残す

対象が呼び出した本人のときだけ、**現在のトークンを失効の対象から外す**（4.3 と同じ扱い）。他のセッションは全部切る。

**自分のセッションまで切ると、自分を締め出す。** 画面は `generated_password` を表示する前に 401 を受けてログイン画面へ飛ぶ。`generated_password` は**この応答でしか手に入らない**ので、**その値は永久に失われる。**

**単独利用者だと致命的である。** 管理者が1人しかいない構成では、**セッションが残っていても自力で戻れず**、`pb admin create` で別の管理者を作るしか手が無い。**GUI だけで完結する復旧経路が無い**のは、憲章「設定は WebGUI を第一の口とする」とずれる。

**「乗っ取られた疑いがあるので直す」という用途は壊れない。** 自分で押したなら、**いま操作している端末は本人のものである。** 他の端末はすべて切れる。これは自分のパスワード変更（4.3）がすでに採っている扱いと同じである。

監査は `password.reset` の1件のみとし、**あわせて行う失効を `session.revoke` として別に記録しない**（2.10）。1つの操作が2行になると、監査ログの読み手が二重に数える。失効した本数は `detail` に入れる。`session.revoke` を記録するのは 6.7 の単独の失効だけである。

`local_credential` を持たないユーザー（IdP のみ。構想）に対しては `409 conflict`。

## 6.7 `POST /api/v1/admin/users/:id/sessions/revoke`

全セッションを失効。`204`。エージェントのトークンにも適用される（`kind='agent'` の場合）。**冪等**であり、有効なトークンが1本も無くても `204` を返す。

**個別のセッションだけを失効させるAPIは持たない。** 管理者が他人の1セッションを選んで切る場面は考えにくく、怪しいセッションが1つあるなら全部を切るのが実務の動きである。要望が出た時点で `DELETE /admin/users/:id/sessions/:sid` を足す。

**本人が自分のセッションを一覧・失効させるAPIも持たない**。
セッションの管理は管理者の作業であり、6.3 の一覧と本節の全失効で足りる。本人が他の端末を
締め出したい場合は、パスワードの変更（4.3）が現在のセッション以外を全失効させる。

## 6.8 プロジェクトメンバーシップ

```
PUT    /api/v1/admin/users/:id/memberships/:project_key   { "role": "project_admin" }
DELETE /api/v1/admin/users/:id/memberships/:project_key
```

`PUT` は追加と変更を兼ねる（冪等）。`GuiDesign.md` 5.6.2 の「プロジェクトごとの権限」ブロックに対応する。

プロジェクト側からも同じ操作ができるよう、`POST /api/v1/projects/:key/members` を追加する（未実装）。**同一の状態を2経路で変更することになるため、内部実装は共通の1関数に集約する。**


## 6.9 `POST /api/v1/admin/users/:id/mfa/reset`

**必要権限**：`user.manage`

対象ユーザーの**第2要素をすべて外す**（認証器・リカバリコード・未消費の挑戦）。`204`。
**冪等**であり、1件も登録が無くても `204` を返す。

**本人がリカバリコードまで失ったときの口である**（`Design.md` 6.7.5）。対象は次のログインから
パスワードだけで入れるようになるので、**画面は確認ダイアログで「第2要素の保護が外れる」と
明記する**（`GuiDesign.md` 5.6.2）。

**パスワードには触らない。** 6.6 のリセットと分けてあるのは、**締め出しの原因が2つある**
ためである——パスワードを忘れた人と、認証アプリを失った人は別の人で、**まとめて直すと
必要のない資格情報まで作り替える。**

**セッションも切らない。** 対象が他の端末でログインできているなら、それは**本人が自力で
`/me` から直せる状態**であり（4.6.4）、切ると救済のつもりで締め出すことになる。

**自分自身に対しても許す。** `self_modification_forbidden`（2.5.1）の対象にしない——
**自分の認証器を外すのは `/me` からできる**ので、この口を自分に向けて使う理由は
「`/me` に入れているのに外せない」という壊れた状態の復旧だけである。**そこを塞ぐ意味が無い。**

**管理者が1人しかいない構成では、この口は成立しない**——締め出された本人が呼べないためである。
その場合は `pb admin mfa-reset --email <アドレス>` を端末から実行する（`Development.md` 9章）。

監査は `mfa.reset`（`target_type = "app_user"`、`detail` に外した認証器とコードの本数）。

## 6.10 `POST /api/v1/admin/users/:id/passkeys/reset`

**必要権限**：`user.manage`

対象ユーザーの**パスキーをすべて消す**（未消費の登録の挑戦も）。`204`。
**冪等**であり、1件も無くても `204` を返す。

**乗っ取りの疑いがあるときの口である**（`Design.md` 6.8.6）。パスキーはパスワード無しで入れる鍵なので、
乗っ取った人が登録した1本は、6.6 のリセットも 6.7 の失効も 6.9 の解除も消さない。
**乗っ取りを直すなら、6.6（パスワード）・6.7（セッション）と組み合わせて使う。** 本口は3つをまとめない
——締め出しの原因ごとに口を分けた 6.9 と同じ判断である。

**セッションは切らない**（6.9 と同じ）。切るなら 6.7 を併せて使う。

**自分自身に対しても許す**（6.9 と同じ理由）。

**端末から叩く口は作らない。** パスキーを失ってもパスワードで入れるので、管理者が1人だけの構成でも
締め出されない（`Design.md` 6.8.6）。

監査は `passkey.reset`（`target_type = "app_user"`、`detail` に消した本数）。

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

開放しても渡る情報はほとんど増えない。プロジェクトロールのキーとその実効権限は、`POST /auth/login` と `GET /me` の `projects[]` で**既に本人へ渡っている**（3.1 / 4.1）。新たに渡るのは、本人が就いていないプロジェクトロールの権限セットだけである。`POST /projects/:key/members`（6.8。未実装）が入れば、プロジェクト管理者はそれらを割り当てる側になる。

**これは暫定である。** `project.edit` を要求して「プロジェクト管理者であること」を確認する案がある。`scope` 別の必要権限を含めて、権限の全体像を再整理するときに決める（`Design.md` 付録A）。

### `scope` の値域を閉じる理由

**プロジェクトIDやユーザIDは受け付けない。** `role` テーブルは `project_id` を持たず（`DbDesign.md` 6.3）、カスタムロール（7.3。構想）も `is_builtin = 0` の行として同じグローバルな表に入るため、**プロジェクトIDで絞っても結果が変わらない**。

**「そのユーザに与えられたロール」は 6.3 の `GET /admin/users/:id` が返す**（`system_role` と `project_memberships[]`）。`?user=` を足すと同じ状態への読み取り経路が2本できる。6.8 が書き込み側について「同一の状態を2経路で変更することになるため、内部実装は共通の1関数に集約する」と警告しているのと同じ問題である。

**リストAPIのフィルタは、そのリソース自身の属性で絞るものに限る。** 他のリソースとの関係で絞りたくなったら、それは関係の側のエンドポイントである（`scope` は `role.scope` 列そのもので、リソース自身の属性）。

## 7.2 `GET /api/v1/permissions`

**必要権限**：**不要**（認証済みであればよい）

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

### 認証済みなら誰でも読める理由

**消費者は2つある。** `GuiDesign.md` 5.6.3 の権限マトリクス（`user.manage` を必要とする画面）と、`GuiDesign.md` 5.8.2 のエージェント用トークンの発行結果である。後者は`scopes[]` の権限キーを**本人に読める言葉で**出す必要がある。あの画面の必要権限は「本人」であり、`user.manage` を持たない。

**開放しても渡る情報は増えない。** 権限カタログは `Design.md` 6.4.2 に全文があり、**本人の実効権限は `GET /me` が既に返している**（4.1）。新たに渡るのは「PB にどういう権限キーが定義されているか」だけで、これは秘密ではない。**むしろ隠すと、本人が自分のエージェントに何ができるのかを読めなくなる。**

**代替案2つを退けた。** ①画面に日本語の対応表を焼き込む——**必ず腐る** ②権限キーをそのまま並べる——腐らないが**本人が読めない**。`description` はこの用途のために既にカタログが持っている列である。

**7.1 の `scope` 別の必要権限は変えない。** あちらはロールの表示名を渡すためのもので、開放の理由も範囲も違う。

`GuiDesign.md` 5.6.3 の権限マトリクス表は、7.1 と 7.2 の2レスポンスから組み立てる。**マトリクス専用のエンドポイントは作らない**（データが重複し、片方だけ更新される事故を招くため）。

## 7.3 構想：カスタムロール

`POST/PATCH/DELETE /api/v1/roles` によるカスタムロール（`role.is_builtin = 0`）。いまは読み取り専用（`GuiDesign.md` 5.6.3）。

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
| **エージェント** | `GET|POST /me/agents`<br>`PATCH /me/agents/:id`<br>`POST /me/agents/:id/tokens`<br>`DELETE /me/agents/:id/tokens/:token_id`<br>`GET /agent-client-kinds` |
| プロジェクト設定（タグタブ） | `GET|POST /projects/:key/tags`<br>`PATCH|DELETE /projects/:key/tags/:id` |
| プロジェクト設定（スプリントタブ） | `GET|POST /projects/:key/sprints`<br>`PATCH|DELETE /projects/:key/sprints/:id` |
| **エージェント連携セットアップ** | `GET /agent-client-kinds`<br>`GET /projects/:key/agent-setup`<br>`GET /projects/:key/agent-setup.zip`（ダウンロード） |
| **アプリケーション設定** | `GET /admin/settings`<br>`PUT /admin/settings`（保存） |
| **TLS証明書** | `GET /admin/tls/certificates`<br>`POST /admin/tls/certificates`（登録）<br>`DELETE /admin/tls/certificates/:id` |
| **DB** | `GET /admin/database`（タブを開いたときと再読み込み）<br>`GET /admin/backup.tar.gz`（書き出し）<br>`POST /admin/restore`（取り込み） |
| **Docs** | `GET /projects/:key/docs`（目次）<br>`GET /projects/:key/docs/*path`（本文）<br>`PATCH|DELETE /projects/:key/docs/*path`・`POST /projects/:key/docs`<br>`GET /projects/:key/docs/*path/_revisions`（履歴） |

**各画面が起動時に呼ぶAPIは1〜2本に収まっている。** 設計方針3が満たされていることの確認になる。

**唯一の例外がプロジェクトダッシュボードで、4本を呼ぶ。** 4本は互いに独立で並列に投げられ、いずれも小さい。「自分の担当」「期限が近い」を `stats` に畳み込まないのは、それがチケットの応答形をもう1つ作ることになるためである（`GuiDesign.md` 5.3）。設計方針3が避けたいのは N+1 の往復であって、独立した4本ではない。

---

# 9. チケットAPI

チケットは PB の中心にある資源であり、**同じデータをバックログ・カンバン・ガントのどの形式でも描ける**ことがデータモデルの前提である（`Requirements.md` 2章）。本章はその前提を API の形に落とす。

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

1. 親もリンク先も**同一プロジェクト内に限る**。プロジェクトが URL で決まっているため、`seq` だけで一意に定まる
2. `GuiDesign.md` 3.2 が既にルーティングを `seq` で決めている。画面が ULID を別に持ち回らずに済む
3. MCP 経由でエージェントが扱う識別子も `my-app-31` の形になる（`Requirements.md` 10.3）。人が読める番号のまま API を組み立てられる

`id` を応答に残すのは、`activity.entity_id`（`DbDesign.md` 6.8）との突き合わせと、エージェント連携（`task_lease.ticket_id` 等）が ULID を使うためである。

**完全形 `my-app-31` はサーバが組み立てない。** プロジェクトキーは URL に含まれており、フロントが `${key}-${seq}` を組める。応答に冗長な文字列を載せない。

**画面は一覧でも完全形 `my-app-31` を出す**（`GuiDesign.md` 5.4）。接尾だけ（`-31`）では負の数に見えるためである。サーバ側の規約は変わらない。

**チケット以外の子資源（コメント・DoD項目・リンク・タグ・スプリント）は ULID で指す。** これらは `seq` に相当する連番を持たない。パスは `/tickets/:seq/comments/:id` のように、チケットまでを `seq`、その先を ULID とする。

### 9.1.1 監査ログではなく `activity` に記録する

チケットの作成・更新・遷移・削除、およびコメント・DoD・リンク・**外部参照**の変更は **`activity`（`DbDesign.md` 6.8）に記録し、`audit_log` には書かない。**

2.10 が `audit_log` の対象としているのは認証・権限・トークン・ユーザー管理であり、いずれも**インスタンス管理者が追うべき事象**である。チケットの変更は業務履歴であり、読み手はプロジェクトのメンバー（`GuiDesign.md` 5.5 の「変更履歴」）である。両者を混ぜると、監査ログがチケット更新で埋まって本来の用途に使えなくなる。

**タグとスプリントの定義変更（9.11 / 9.12）は、どちらにも記録しない。** `audit_log` の対象ではなく（上記のカタログに入らない）、`activity` の読み手はチケットの変更履歴であって、9.13.2 の `entity` も `ticket:31` の形しか受け付けない。**記録しても読む画面が無い。**

ただし**タグの削除は、`ticket_tag` を `CASCADE` で消して全チケットからそのタグを外す**（`DbDesign.md` 6.10）。「使用中だったタグを誰が消したか」を後から追えない状態であり、運用に載せてから困ることがありうる。**その時点で `activity` に `entity_type='tag'` / `'sprint'` を足す**（11.2）。先に入れないのは、読む画面の無い記録が形だけ固まるのを避けるためである。

## 9.2 `GET /api/v1/projects/:key/tickets`

**必要権限**：`ticket.view`（メンバーでない場合はプロジェクトごと `404`。1.2-5）

バックログ画面（`GuiDesign.md` 5.4）とチケット検索（`GuiDesign.md` 5.13）のデータ源であり、カンバン・ガント（未実装）も同じエンドポイントを使う。

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
| `retired` | `false` | **`true` で「棚に戻ったもの」も返す**（下記「棚に戻ったものを既定で外す」）。既定では返さない |
| `staged` | — | **`true` でオンステージの行とその全子孫に限る**（下記「オンステージで絞る」）。`true` 以外は `422` |
| `due_within` | — | `7d` 形式。**今日から N 日以内に期限があるもの（期限超過を含む）**。`due_date IS NULL` は除外 |
| `overdue` | — | `true` で**期限を過ぎた未完了のもの**（`due_date < 今日` かつ `closed_at IS NULL`）。9.13.1 の `overdue` と同じ条件 |
| `planned_from` | — | 予定期間の下限（`YYYY-MM-DD`、含む）。チケットの予定期間と1日でも重なるもの。片方だけの日付を持つチケットはその日1日として扱い、両日未設定は除外 |
| `planned_to` | — | 予定期間の上限（`YYYY-MM-DD`、含む）。`planned_from` と片方だけでもよい。両方あるとき `planned_from <= planned_to` |
| `stale` | — | `14d` 形式。**その日数より前から更新されていない未完了のもの**（`updated_at < now() - N日` かつ `closed_at IS NULL`）。9.13.1 の `stale` と同じ条件 |
| `parent` | — | `seq` を指定すると、そのチケットとその全子孫（部分木）に限る。**カンマ区切りで複数指定は OR**（いずれかの部分木に含まれるもの） |
| `q` | — | **キーワード**。以下は `search_mode=fulltext` の仕様。空白で区切った語を**すべて含む**もの。各語はタイトル・本文・コメント（削除済みを除く）の**いずれかに部分一致**すればよい。大文字小文字を区別しない（**全角の英字なども畳むのは、DB の `LC_CTYPE` が `C.UTF-8` のとき**。`DbDesign.md` 4.5）。`%` と `_` は文字として扱う。200文字まで（下記「検索の条件」） |
| `seq_from` / `seq_to` | — | **チケット番号の範囲**。**両端を含む**。片方だけでもよい |
| `started_since` / `started_before` | — | **実際に着手した日時の範囲**。`since` 以上・`before` 未満。ISO8601。着手の定義は下記「着手日時を導く」。**着手していないものは外れる** |
| `closed_since` / `closed_before` | — | **完了した日時（`closed_at`）の範囲**。`since` 以上・`before` 未満。ISO8601。**未完了は外れる** |
| `sort` | `sort_key` | `sort_key` / `seq` / `title` / `status` / `priority` / `due_date` / `created_at` / `updated_at` / `closed_at` |
| `order` | `asc` | `asc` / `desc` |
| `page` | `1` | 2.6 |
| `per_page` | **`200`** | 2.6。上限は 2.6 と同じ 200 |

**異なる種類の条件どうしは AND、同じ条件の複数指定は OR** とする（`?type=task&priority=high,highest` は「タスク、かつ優先度が高以上」）。

**バックログのエピックフィルタは `parent` を使う**（`GuiDesign.md` 5.4）。エピックは行として出さず、複数選択できるフィルタになるが、**絞り込みの実体は部分木であって種別ではない**（`DbDesign.md` 6.10）。`?parent=12,30` は「12 の部分木または 30 の部分木」で、エピック自身も部分木に含まれる（画面が行として捨てる）。**`epic` という専用パラメータを作らない**——作ると API が種別に依存し、グルーピングの実体が `parent_id` であるという定義と食い違う。

**`overdue` / `stale` はダッシュボード（`GuiDesign.md` 5.3）の「要対応」から来る導線のために足した。**どちらも 9.13.1 の同名の集計とまったく同じ条件で数えるものであり、**ダッシュボードが出した件数と、押した先の一覧の件数が一致することが要件**である。`due_within=0d` で代用しない——あちらは「今日以前」で**今日が期限のもの**を含み、`overdue`（`due_date < 今日`）と1日ぶんずれる。

**`planned_from` / `planned_to` は予定日の重なりを見る。** チケット側の有効な開始を
`COALESCE(start_date, due_date)`、有効な終了を `COALESCE(due_date, start_date)` とし、
`有効な開始 <= planned_to AND 有効な終了 >= planned_from` で判定する。これにより片方だけの
日付はその日1日の点になり、両日未設定は除外される。`started_since` / `started_before` は
状態遷移から導く**実際の着手日時**なので、予定日の検索に流用しない。

**`stale` が日数を取るのは、閾値の正本がサーバにあるからである**（9.13.1 の `threshold_days`）。ダッシュボードは `stats` の応答に載る値をそのままリンクへ載せ、**画面側に 14 を書かない**。`due_within` と同じ `<N>d` 形式にしてあるので、上限も同じ 3650 日である。

#### バックログの入力検索

`q` と `search_mode=backlog` を送ると、同じプロジェクトの全件から番号（数字・プロジェクトキー付き）、タイトル、本文、祖先エピックのタイトル、タグ名を部分一致で検索する。語の分割・AND・大文字小文字・記号の扱いと200文字上限は通常の `q` と同じ。数字だけの語をIDへ照合するときはチケット番号部分だけに当て、プロジェクトキー内の数字には当てない。コメントは対象に含めない。`search_mode` は省略時 `fulltext`、`fulltext` / `backlog` 以外は422。

既存のフィルタを満たす一致チケットを選び、その祖先を検索結果へ補完する。補完する祖先にも既存のフィルタは適用する（エピックを除く種別指定も維持）。補完した親を含む重複のない結果を `total` とし、ソート・ページングはその後に適用する。**検索対象に200件の制限はなく、返す結果が最大200件である。** 上限では親も含めて打ち切る。`q` が空なら通常の一覧と同じで、補完しない。通常の全文検索の検索対象・親を補完しない動作は変わらない。

#### 検索の条件

**チケット検索（`GuiDesign.md` 5.13）のために足した。** `q` / `seq_from` / `seq_to` / `started_since` / `started_before` / `closed_since` / `closed_before` の7つと、並べ替えの `closed_at` である。**専用のエンドポイントを作らない**——検索画面の一覧はバックログと同じ行の形であり、増えるのは条件だけだからである。

| 論点 | 決めたこと |
|---|---|
| キーワードの対象 | **タイトル・本文・コメント**（削除済みを除く）。trigram インデックスが張ってある3列である（`DbDesign.md` 4.5）。完了レポートもコメントなので当たる |
| 複数の語 | 空白（全角を含む）で区切り、**すべてを含むもの**（AND）。語ごとに、3列のどれかに当たればよい |
| 長さ | `q` 全体で200文字まで。超えたら `422`（`out_of_range`）。空白だけの `q` は指定なしと同じ |
| `%` と `_` | **文字として扱う**（エスケープする）。打った記号がワイルドカードとして効くと、利用者の意図と違う行が出る |
| 実装の置き場 | **`store/search/` に隔離する**（`Design.md` 4.6）。語の分解とエスケープ、一致するチケットの抽出はそこで行い、一覧のクエリには一致した ID を渡す。**日本語検索の方式を `pg_bigm` へ替えるとき、影響をこの層に閉じ込めるため**である |
| 範囲の向き | **番号は両端を含み、日時は半開区間**（`since` 以上・`before` 未満）。日時を半開にすると、画面は「9/1〜9/15」を「9/1 の0時以上・9/16 の0時未満」として送れ、境界の瞬間を二重に数えない |
| 日の境界 | **サーバは日付を解釈しない。** 画面が利用者のタイムゾーン（`app_user.timezone`）で日の境界を作り、ISO8601 の瞬間として送る。画面は `closed_at` を同じタイムゾーンで表示しているので（`GuiDesign.md` 7.5）、**見えている日付と絞り込みの日付が一致する** |
| 範囲が逆 | `seq_from > seq_to`、`since >= before` は `422`（`invalid`）。**黙って空の結果を返さない**——入力の誤りが「該当なし」に見える |
| 片側だけの指定 | 受け付ける（`seq_from=100` は100番以降） |
| 並べ替えの `closed_at` | 未完了（`NULL`）は昇順・降順とも**末尾**（`due_date` と同じ扱い） |

##### 着手日時を導く

**「着手」は、状態が `todo` 区分から初めて出た遷移である**。`activity` の `action='transition'` の行（9.6）のうち、遷移前の状態の区分が `todo` で遷移後が `todo` 以外のものを探し、**最も早い `occurred_at`** を着手日時とする。

- **列を足さない。** `start_date` は予定の開始日で、人やエージェントが手で入れる欄であり、埋まっていないことが多い。実際の着手は遷移の履歴が持っている
- **完了を取り消して着手し直しても、最初の着手を採る。** 「いつから手を付けたか」を探す用途では、最初の着手が答えになる
- **状態の区分は、いまのワークフローで引く。** 過去のキーがいまのワークフローに無ければ、その遷移は着手として数えない
- **作成時の状態は遷移ではないので数えない。** `pb dev seed` が `activity` を書くのは `history: true` を付けたチケットだけ（`DbDesign.md` 7.6.4）なので、**それ以外のデモデータの進行中チケットは着手日時を持たない**

#### 棚に戻ったものを既定で外す

**次の3つをすべて満たす行を、既定で一覧から外す**。

| # | 条件 |
|---|---|
| 1 | `closed_at IS NOT NULL`（完了している） |
| 2 | **最後に属したスプリントが `completed`**（`DbDesign.md` 6.9.1 の `ticket_sprint` を `added_at` 降順で1件見る） |
| 3 | **未完了の祖先を持たない**（根までたどる） |

**スプリントを終了した時点で、消化し終えたものがバックログから消える**——これが「バックログ＝プロジェクトが行うべき仕事すべての保管庫」（`GuiDesign.md` 5.4）を保つ手当てである。**終わった仕事が積み上がると、保管庫は「やるべきこと」の一覧として読めなくなる。**

**条件2 が「完了」だけで外さない理由。** 完了した直後に消えると、**スプリント中に何が終わったかを振り返る面が無くなる**。オンステージは期間の作業台であり、期間が閉じるまでは終わったものも載っている。

**条件3 が要るのは、子が親より先に完了するからである**。子だけ消えると、親を開いたときに配下が歯抜けになる。**親が完了すれば、9.6 の検証7 により子はすべて完了している**ので、部分木は丸ごと同時に消える。

**直下の親だけを見る形では足りない。** 「祖父が完了しているなら親も完了しており、規則が段ごとに効く」とは限らない。親→子→孫で子と孫だけを完了させると、孫の直下の親（子）は完了しているので**孫が消える**。ところが子は、その親が未完了なので**残る**。結果は「子は見えるのに孫だけ消えた」という、**条件3 が防ごうとしていた歯抜けそのもの**である。**祖先を根までたどる。**

**通常の流れでは「根が完了した＝部分木が全部完了した」と一致する**（検証7 があるため）。それでも祖先をたどるのは、**0026 の再オープン（`done → in_progress`）が、完了した親の下に未完了の子が居る状態を作れる**ためである。

**エピックは祖先に数えない**（実データで判明）。エピックはグルーピング専用で**行として出ない**ので（`GuiDesign.md` 5.4）、完了しないまま残っていても歯抜けを作らない。数えると、**エピック配下のチケットが永久に棚へ戻らなくなる**——実運用のバックログはたいていエピックで束ねられているので、**この規則そのものが効かなくなる。** たどるのは**表示上のトップレベル**（親が無いか、親がエピック。9.4.1）から下だけである。

**列を足さず導出する**。`retired_at` のような列を立てる案は採らなかった——**再オープン（0026 で `done → in_progress` を開けた）のたびに列を落とす手当てが要り、落とし忘れると「完了していないのに消えている」行が生まれる。** 導出なら、条件1 が崩れた瞬間に自動で戻る。

**サーバで外す。** 画面側で捨てると、`GuiDesign.md` 5.4 の「**下部の総件数と上下の件数の合計は一致する**」が崩れる（9.2.4 はフィルタを行単位で適用する）。

**`retired=true` は、検索画面ができた後も残す**。棚に戻ったチケットを探す本来の面はチケット検索（`GuiDesign.md` 5.13）で、あちらは常にこれを送る。**バックログの状態フィルタで完了を明示的に選んだときも、今までどおり送る**（`GuiDesign.md` 5.4）——外すとバックログの挙動が変わり、検索画面の追加がバックログに及ぶ。**見直すのは、バックログで完了を選んだときに出る過去の行が邪魔だと言われたとき**である。

**バックログ画面は `staged` を使わない。** 画面は**フィルタ後の全件を1回で取り切り、二段を手元で分ける**（9.2.3、`GuiDesign.md` 5.4）。2本に分けると、件数表示・`ETag`・並べ替え後の再取得がすべて2本になる。段は応答の `staged_at` で判別できる。`staged=true` は MCP のための条件である（下記「オンステージで絞る」）。

**`per_page` の既定が他の一覧（25）と違う。** バックログはページャを持たず、フィルタ後の全件を1回で取り切る画面だからである（9.2.3）。**`sort` の既定が `sort_key` であることも本エンドポイント固有**で、これは人が手で並べた順序（9.4）を既定の見え方にするためである。

**`status` と `status_category` の使い分け。** 画面のフィルタは `status`（プロジェクトのワークフローに定義されたキー）を使う。`status_category` は**ワークフローが違うプロジェクトを跨いでも意味が変わらない4値**であり、ダッシュボードの集計（9.13）とカンバンの列（未実装）が使う。

#### オンステージで絞る

**`staged=true` は MCP の `pb_list_tasks` のためにある。** 「オンステージのチケットに着手して」と頼まれたエージェントが、未完了の全件を取ってから `staged_at` で拾わずに済む。

| 論点 | 決めたこと |
|---|---|
| どこで絞るか | **REST に任意の条件として足す。** MCP 層で絞ると、親子をたどる規則を MCP に持つことになり（`Design.md` 8.1 の「同じ規則を2か所に書かない」）、200件を超えるとページをまたいで取り直すことになる |
| 何をオンステージとみなすか | **`staged_at` を持つ行とその全子孫。エピックを除く。** 画面の段（`GuiDesign.md` 5.4）とスプリントの開始（9.12.1）と同じ定義である——**段を決めるのは親で、子は親と一緒に運ばれる**（9.4.1） |
| 棚に戻ったものを含めるか | **`retired` の既定どおり外す。** `retired=true` を一緒に送れば含まれる。条件は種類ごとに独立に効く（AND） |
| `false` を受けるか | **受けない**（`overdue` と同じ）。バックログ段だけを取る用途が無い |

**バックログ画面は使わない**（上記）。任意の条件を足しても、画面の取り方は変わらない。

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
      "working_agent": { "id": "01K2...", "kind": "agent", "display_name": "claude-code" },
      "parent_seq": null,
      "has_children": true,
      "sort_key": "0|hzzzzz:",
      "staged_at": null,
      "tags": [ { "id": "01K2...", "name": "設計" } ],
      "sprint": { "id": "01K2...", "name": "Sprint 3" },
      "estimate_point": 5,
      "estimate_hours": null,
      "actual_hours": 3.5,
      "actual_point": 5,
      "actual_point_version": "actual-v0",
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

**`execution_mode` / `readiness` / `readiness_note` / `scope` / `custom_fields` も含めない。** **前の4つは詳細（9.5.1）にだけ載せ、一覧には含めない**——読む相手（コンテキストパックと `/pb-implement` の分岐）はどちらも**チケット1件を指して呼ぶ**ものであり、200件ぶんの `scope` を運ぶ理由が無い。`custom_fields` は詳細にも足していない（9.5.1）。

`assignee` / `reporter` は担当者不在のとき `null`。`kind` は `user` / `agent` / `system` で、**画面はこれを見てエージェントに 🤖 バッジを付ける**（`GuiDesign.md` 5.4、設計原則5）。

**`working_agent` は「誰が実際に処理しているか」である**（`DbDesign.md` 6.6 の `working_agent_id`）。`assignee` が**誰の仕事か**を表すのに対し、こちらは**実行者**を表す。**エージェントが遷移したときに自分で立てる**（9.6）ので、人が手で埋める必要はない。未設定なら `null`。

**担当と実行者を別の欄にするのは、1欄では「田中の担当だが claude が処理している」を表せないためである。** `assignee` にエージェントを入れる形は採らない——`Design.md` 8.5 が「担当は人が持つ」と定め、MCP の `assignee=me` を所有者へ写している。**両者の食い違いは欄が1本しかなかったことに由来していた**（`DbDesign.md` 6.6）。

**`working_agent` はチケットを消化しても消えない。** 「このチケットは誰が処理したか」は完了後にこそ読みたい情報である。人が `PATCH`（9.5.2）で消すか差し替える。

### 9.2.3 ページャを画面に出さない

2.6 の `page` / `per_page` / `total` / `total_pages` は**規約どおり返す**。画面がページャを出さないだけである（`GuiDesign.md` 5.4）。

**理由は、グループ化・階層のインデント・ドラッグ&ドロップの並べ替えがいずれもページ境界をまたげないことにある。** 25件目と26件目の間で親子が切れると、子だけが孤立して2ページ目の先頭に現れる。これは `GuiDesign.md` 11章に「チケット一覧の階層表示とページングの相性」として未解決事項に挙げられていた問題であり、**バックログについてはページングを持たないことで解決する**。

`total > per_page` になったとき、画面は件数とともに「フィルタで絞り込んでください」を表示する。**サーバは 200 件で打ち切るだけで、エラーにはしない。**

**全件を返す専用のモード（`per_page=all` 等）は設けない。** 上限を外すと、応答サイズが利用者の入力ではなくデータ量で決まるようになり、性能の予測が立たなくなる。想定する規模で 200 件を超えるプロジェクトは、フィルタを使うか、ビューを分ける（スプリント・タグ）べき段階にある。

### 9.2.4 フィルタで親が落ちた子の扱い

**通常のフィルタは行単位で適用し、サーバは親を補完しない。** `q` と `search_mode=backlog` を併用した場合だけ、9.2.1 の入力検索の規則で祖先を補完する。 親がフィルタに合致しない場合、その子は `parent_seq` を保ったまま返る。画面は「親が結果に含まれていない子」をトップレベルに並べる（`GuiDesign.md` 5.4）。

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
| `execution_mode` | **常に `agent_draft`**（`DbDesign.md` 6.6 の列の既定。`INSERT` は値を送らない）。変えるのは 9.5.2 の `PATCH` である |
| `version` | `1` |

`201 Created`（`Location: /api/v1/projects/my-app/tickets/31`）。応答は 9.5 の `GET` と同形式。

**採番・ワークフロー解決・タグ付与・`activity` 記録は単一トランザクションで行う。** 5.3 の `POST /projects` と同じ方針である。

**作成したチケットは必ずバックログに入る。** オンステージは「いま仕掛り中で、直近のスプリントで消化すべきもの」（`DbDesign.md` 6.6）であり、**上げる操作は人が段へドラッグしたときだけ**にする。作成時に指定できると、新規チケットが黙って仕掛りに混ざる。

**`execution_mode` の既定を `agent_draft` にしている**（`DbDesign.md` 6.6）。**エージェントは `human_only` のチケットに着手しない**ので、既定が `human_only` だと**人が明示的に変えるまでエージェントが一切動けない**——PB は「人とエージェントが同じプロジェクトを進める」ための道具であり、**協業できないほうを既定にしない**。`agent_draft`（エージェントが下書きし、人が仕上げる）は協業を許容しつつ、**仕上げを人に残す**。

**作成時に指定できるようにはしない。** 9.5.2 の `PATCH` で変えられ、画面にも入力欄がある（`GuiDesign.md` 5.5）。作成の必須項目を増やさないほうが、起票の手数が軽い。

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
| `{"parent_seq": null, "after_seq": 44}` | **ルートにして**、44 の直後に置く（9.4.2） |

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

**`If-Match` は要求しない。** 2.8 の archive / unarchive と同じく、競合しても失われる編集内容が無い（`sort_key` はフォームで編集する項目ではない）。ただし、**`version` は他の更新と同じく +1 する**。並べ替えの直後に詳細画面が `409` を返す可能性があるが、規約を1本に保つことを優先する。実運用で不都合が出たら 2.8 ごと見直す（11.2）。

**並び順はプロジェクト内で1本である。** グループ化（親・タグ・スプリント）は表示上の区切りにすぎず、グループを切り替えても `sort_key` は変わらない。グループごとに別の順序を持たせると、軸を変えるたびに順序が失われる。

**二段（バックログ／オンステージ）も同じ1本を共有する。** 2つの表は同じ「消化順」の部分集合であり、順序キーを段ごとに持つと、**段を行き来するたびにどちらを更新するかを決めることになり、戻したときの位置が失われる**（`DbDesign.md` 6.6）。

### 9.4.2 `parent_seq` — ルートにする

**`null` だけを受け取る**（`parent_seq: null` = 親を外してルートにする）。省略すると親は変わらない。**数値を送ると `422`**（`details[].code = "unsupported"`）。

**なぜ `move` が親を触るのか。** 9.4.1 の `staged` とまったく同じ理由である——**ドラッグ&ドロップの1操作で、親と位置が同時に決まる**。バックログで子チケットを根の並びへ落とす操作（`GuiDesign.md` 5.4）がそれで、2本のエンドポイントに分けると「**ルートにはなったが位置は元のまま**」という中途半端な状態が残る。

**`move` が受け取るのは「位置と同時に決まるもの」だけである。** `staged` と `parent_seq` がこれに当たる。**位置を伴わない親の変更は `PATCH` のままである**（9.5.2）——行の中央へ落として「その行の子にする」操作は、新しい親の下での位置を決めないので `parent_seq` だけを送る。

**数値を受け取らないのは、それを決めるドロップが無いからである。** 「別の親の下の、この位置へ」を1回のドラッグで表す落とし先を 5.4 は持たない。**そういう操作を作った日に、ここへ足す。**

**`parent_seq: null` と `staged` は同時に送ってよい。** 同じトランザクションで確定するので、片方だけ成功する状態は生まれない。**段に置けるかの判定（9.4.1 の `not_stageable`）は、親を外した後の状態で行う**——外せばトップレベルになるので、外す前の親を見て弾いてはならない。

**`ticket.edit` で足りる。** 親の付け替えは 9.5.2 も `ticket.edit` なので、権限の要求は変わらない。

## 9.5 `GET | PATCH | DELETE /api/v1/projects/:key/tickets/:seq`

### 9.5.1 `GET`

**必要権限**：`ticket.view`

チケット詳細画面（`GuiDesign.md` 5.5）のデータ源。9.2 の `items[]` に以下を加えたものを返す。

| 追加項目 | 内容 |
|---|---|
| `body_md` | 本文（Markdown ソース） |
| `parent` | 親の `{seq, title, type, status}`。無ければ `null` |
| `epic` | **祖先をたどって最初に見つかるエピック**。形は `parent` と同じ。親がエピックなら親そのもの。**自分自身は数えない。** 無ければ `null` |
| `children` | 直下の子の `[{seq, title, type, status, assignee}]`（孫は含めない） |
| `dod` | 完了条件の配列（9.9） |
| `links` | 関連リンクの配列（9.10.1） |
| `references` | 外部参照の配列（9.10.2）。`kind` の昇順、同じ `kind` の中は `sort_order` → `created_at` の昇順 |
| `comment_count` | コメント件数（本文は含めない）。`deleted_at IS NULL` のものを数える |
| `execution_mode` | `human_only` / `agent_only` / `agent_draft` |
| `readiness` | `red` / `yellow` / `green`。未判定は `null` |
| `readiness_note` | Readiness の理由。未設定は `null` |
| `scope` | スコープ境界のオブジェクト。未設定は `{}` |

**`execution_mode` / `readiness` / `readiness_note` / `scope` は詳細にだけ載せ、
一覧（9.2.2）には含めない**——本文と同じく、**一覧は使わない項目を件数ぶん
掛け算する場所ではない**。

**足した理由は、`pb_get_task` が果たせていない約束があったためである。**
`Requirements.md` 10.3.2 は `pb_get_task` の戻り値に「スコープ境界、実行主体属性、
readinessスコア」を挙げ、`Requirements.md` 10.8.6 の `/pb-implement` は手順1 で
**「実行主体属性が `human-only` の場合、実装せずユーザーに報告して終了する」**
「readiness が赤の場合、不足点を提示し、実装に進んでよいかユーザーに確認する」と
定めている。`Design.md` 8.5.2 のとおり `pb_get_task` は本節の応答をそのまま返すので、
**本節が返さない限り、この2つの分岐はどちらも起こりえなかった。**

**`epic` は、画面が所属するエピックを親とは別の欄で出すためにある**（`GuiDesign.md` 5.5「エピック欄」）。`parent` は1段しか返さないので、**孫のチケットではどのエピックに属するかが `parent` からは読めない**——一覧（9.2）の手持ちから祖先をたどると、フィルタで途中の親が落ちたときに答えが欠ける。**最も近いエピックを返す**（エピックが入れ子になっていても1つ）。たどる深さは 32 段で打ち切る（`GetDisplayRootForStaging` と同じ）。**一覧（9.2.2）には足さない**——行ごとに再帰を掛けることになり、一覧の画面はこの値を使わない。

**`custom_fields` は載せない。** 読む相手が
まだ居ない——画面も MCP のツールも使わない（`GuiDesign.md` 5.5）。**在ることは、
要ることの根拠にならない。**

**コメント本体と変更履歴は含めない。** コメントはページングを持ち（9.8）、履歴は既定で畳まれている（`GuiDesign.md` 5.5）。画面は起動時に本エンドポイントと `GET .../comments` の**2本**を呼ぶ。履歴は開いたときに3本目を遅延で呼ぶ。8章の「起動時1〜2本」に収まる。

**`dod` / `links` / `references` / `comment_count` はいずれも実数を返す**（9.9 / 9.10.1 / 9.10.2）。`comment_count` は 9.6 の遷移が `kind='progress'` のコメントを作るため、0 を固定で返すと事実と食い違う。

**`references` を別の `GET` に切らず詳細応答へ入れるのは、`dod` / `links` と同じ理由である**（8章の「起動時1〜2本」）。件数は1チケットあたり数件で、遷移先の一覧（9.7）のように**開いたときだけ要るもの**ではない——画面を開いた時点で見えている（`GuiDesign.md` 5.5）。

### 9.5.2 `PATCH`

**必要権限**：`ticket.edit`。ただし `assignee_id` / `working_agent_id` を変える場合は `ticket.assign` も必要

**`ticket.self_edit` でも通る**（0029）。ただし**開けるのは下の絞り込んだ集合だけ**で、それ以外の項目を送ると `403` になる。**エージェントに渡す権限であり、自分の縛りを緩められては意味がない**（`DbDesign.md` 6.13）。

| `ticket.self_edit` で変えられる | 変えられない |
|---|---|
| `title` `body_md` `priority` `parent_seq` `assignee_id` `tag_ids` `estimate_point` `estimate_hours` `start_date` `due_date` | **`type`** `execution_mode` `readiness` `readiness_note` `scope` `working_agent_id` `actual_hours` |

**`type` は `ticket.edit` を持つ人だけが変えられる**。**種別の切り替えは盤面の見え方を変える**——タスクをエピックへ変えると、その行はバックログから消えてフィルタの選択肢になる（`GuiDesign.md` 5.4）。

**判定はハンドラで行う。** `Design.md` 6.4.4 の宣言では表せない——**どの項目を送ったか**で可否が決まるためである。**9.6 の検証6・9.8 の `comment.edit_own` に続いて3例目**であり、`Design.md` 付録A 論点①（権限の全体像の再整理）をここで行う。

**`assignee_id` を変えるときに `ticket.assign` も要るのは `ticket.edit` のときと同じ**である（エージェントの既定スコープは両方を持つ）。

`If-Match: "3"` による楽観ロック（2.8）。**省略時は `422`**。成功すると `version` が +1 される。送られたフィールドだけを更新する。

**`actual_point` と `actual_point_version` は対で送り、追加の `ticket.actual_point.edit` 権限が要る。** どちらか一方だけ、または値と null の混在は 422 とする。

変更可能：`type` `title` `body_md` `priority` `assignee_id` `working_agent_id` `parent_seq` `tag_ids` `estimate_point` `estimate_hours` `actual_hours` `actual_point` `actual_point_version` `start_date` `due_date` `execution_mode` `readiness` `readiness_note` `scope`

**含められないフィールド**

| フィールド | `details[].code` | 理由 |
|---|---|---|
| `id` `seq` `version` `created_at` `updated_at` `reporter_id` | `immutable_field` | サーバが決める（5.5 と同じ扱い） |
| `sort_key` `staged_at` | `use_move_endpoint` | 9.4（段の出し入れも `move` が行う） |
| `sprint_id` | `use_sprint_endpoint` | 9.12（スプリントの開始・終了が動かす。`DbDesign.md` 6.9.1） |
| `status_key` `closed_at` | `use_transition_endpoint` | 9.6 |

`immutable_field` / `use_move_endpoint` / `use_sprint_endpoint` / `use_transition_endpoint` はいずれも **`details[].code` の値**であって 2.5.1 の `error.code` ではない（`error.code` は `validation_failed`）。5.5 と同じ規約である。

**`tag_ids` は丸ごと置き換える**（部分更新ではない）。`settings` と同じ方針（5.5）。空配列でタグを全て外す。

**`sprint_id` は 0028 で書けなくなった**。**スプリントは「チケットにあらかじめ付ける属性」ではなく「いまどの期間で消化しようとしているか**」であり、付け替えはチケット1件ずつではなくオンステージ全体に対して1回起きる（9.12 の `start` / `finish`）。**9.3 の `POST` も同じ理由で受け取らない**——作成時にだけ設定できて後から変えられないのは、どちらの規則としても読めない中途半端な状態になる。

**`working_agent_id` を書けるのは、実行者を消すか差し替えるためである**。**エージェント自身は 9.6 の遷移で自動的に立てる**ので、この経路は人が使う。`null` を送ると外れる。

| 検証 | 失敗時 |
|---|---|
| 参照先が存在し、`actor.kind` が `agent` であること | `422 validation_failed`、`details[].code = "not_found"` |
| そのエージェントの所有者が、このプロジェクトのメンバーであること | `422 validation_failed`、`details[].code = "not_a_member"` |

**メンバーであることを所有者で見るのは、`Design.md` 6.5 の委譲に従うためである。** エージェントは `project_member` の行を持たない（持たせると所有者のロールと二重になる）ので、`assignee_id` と同じ検証をエージェント自身に対して行うと必ず落ちる。

**`ticket.assign` を要求するのは、これが「誰がやるか」を決める操作だからである。** `assignee_id` と同じ扱いにする。

**`execution_mode` / `readiness` / `readiness_note` / `scope` を書ける**

**エージェントの逸脱防止を構造データで行う**（`Requirements.md` 10.5.3）以上、**その構造データを
書く経路が要る。** 列は `DbDesign.md` 6.6 にある。

| フィールド | 検証 | 失敗時の `details[].code` |
|---|---|---|
| `execution_mode` | `human_only` / `agent_only` / `agent_draft` のいずれか。**`null` は不可**（列が NOT NULL） | `invalid` |
| `readiness` | `red` / `yellow` / `green`、または **`null`（未判定へ戻す）** | `invalid` |
| `readiness_note` | 文字列、または `null`。**長さの上限を置かない**（`body_md` と同じ扱い） | `invalid` |
| `scope` | **オブジェクト。** 既知の4キー（`allow` / `deny` / `repositories` / `external_apis`）は**文字列の配列**であること。`{}` で空に戻す。**`null` は不可**（列が NOT NULL DEFAULT `'{}'`） | `invalid` |

**`scope` の未知のキーは拒まず、そのまま保存する。** 9.15 の完了レポートと同じ判断である
——`Requirements.md` 10.5.3 の例は `allow` / `deny` / `repositories` / `external_apis` の4つを
挙げるが、**境界の表し方はプロジェクトごとに育つ**（触ってよい API、触ってよい環境、承認が要る
操作）。**知らないキーを1つ付けただけで更新が丸ごと落ちると、往復が増えるだけで誰も得をしない。**
**形（配列かオブジェクトか）だけを見る**のも 9.15 と同じである。

**`ticket.assign` は要求しない。** 追加の権限が要るのは `assignee_id` / `working_agent_id` ——
すなわち、**「誰がやるか」を決める操作**だけである（本節冒頭）。**スコープ境界と実行モードは
「何をしてよいか」であり、担当の割り当てではない。** `ticket.edit` を持つ人が本文や完了条件を
書けることと同じ重さに置く。

**新しい `details[].code` を発明しない。** 4項目とも既存の `invalid` で表せる（9.14 の表に
加わるものは無い）。

**`activity` には4項目とも `old_value` / `new_value` を記録する**（`body_md` のように
`NULL` にしない）。**`scope` の変更は「誰が縛りを緩めたか」であり、履歴として読む価値が
本文より高い。** 実際の値は数行に収まり、9.13.2 の `per_page=20` を圧迫しない。

**`POST /tickets`（9.3）には足さない**。**スコープ境界は縛る側が書くものである**
——起票時に受けると、`pb_create_ticket`（`Design.md` 8.5.1）を通じて**実装するエージェントが
自分の境界を書ける経路**になる。PM のエージェントに書かせる形は、`Design.md` 8.2 が
「エージェントが自分の所有者へ担当を割り当ててから遷移できる」問題を扱うときに、**同じ
論点として1回で決める。** 人はチケット詳細の「実行モード」「Readiness」「スコープ境界」の欄から書く（`GuiDesign.md` 5.5）。

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

**`body_md` だけは `old_value` / `new_value` を `NULL` にする**（`field` は記録する）。本文は長く、9.13.2 は `per_page=20` の一覧APIなので、20件ぶんの Markdown を載せると 8章の「応答を軽く保つ」方針に反する。**「前の本文に戻す」は履歴一覧の仕事ではない**——本文の版管理が要件になったら別の仕組みとして設計する（11.2 に起票）。

値の送信はあったが内容が現在値と同じだった項目は、**変更が生じていないので記録しない。** `version` は 2.8 の規約どおり +1 する。

応答は 9.5.1 と同形式。

### 9.5.3 `DELETE`

**必要権限**：`ticket.delete`

物理削除（`DbDesign.md` 4.6 の既定）。`204 No Content`。

**子チケットは削除しない。** `ticket.parent_id` は `ON DELETE SET NULL` であり、子は親を失ってトップレベルへ上がる。**この挙動を確認ダイアログに明示する**（`GuiDesign.md` 6.3。「3件の子チケットは削除されず、親のないチケットになります」）。

コメント・DoD・リンク・タグ付けは `ON DELETE CASCADE` で消える。`activity` は `entity_id` で残るが、参照先のチケットは存在しなくなる。

**`activity` は消さず、`action='delete'` を1行足す。** `activity.entity_id` は多相参照で FK を持てないため（`DbDesign.md` 6.8）、チケットを消しても行は追従しない。**これは設計どおりの挙動である**——9.13.2 は「削除されたチケットの行は `entity_seq` / `entity_title` が `null` になる。画面は『削除されたチケット』と表示する」と定めており、**残る前提で組まれている**。消してしまうと、ダッシュボード（`GuiDesign.md` 5.3）の「最近の動き」から「誰がどのチケットを消したか」が読めなくなる。

削除の行も `field` / `old_value` / `new_value` は持たない（作成と同じ）。何が消えたかは、同じ `entity_id` を持つ既存の行が答える。

**本節を定義したのは、`ticket.delete` 権限が `DbDesign.md` 7.2 の権限カタログに存在するのに、対応するエンドポイントがどこにも無かったためである。**

## 9.6 `POST /api/v1/projects/:key/tickets/:seq/transition`

**必要権限**：`ticket.transition`。加えて `workflow_transition.required_permission` が設定されていればその権限も必要

```json
{ "to": "in_review", "comment": "レビューをお願いします" }
```

**本体の検査を先に行う。** `to` は必須（`required`）。`comment` は任意で、**前後の空白を除いて20000字以内**（9.8 のコメントと同じ上限）。超えると `422 validation_failed`、`details[].field = "comment"`、`code = "too_long"`。**どちらもワークフローを読む前に返すので、弾かれたときに遷移は起きない**——遷移だけ通ってコメントが落ちる状態を作らない。

**検証の順序**（`DbDesign.md` 6.5）

| # | 検証 | 失敗時 |
|---|---|---|
| 1 | `to` がプロジェクトのワークフローに存在するステータスか | `422 validation_failed`（`details[].code = "unknown_status"`） |
| 2 | 現在のステータスから `to` への `workflow_transition` が定義されているか | `409 invalid_transition` |
| 3 | 呼び出し元の `actor.kind` が `allowed_actor_kinds` に含まれるか | `403 forbidden` |
| 4 | 遷移先の `is_agent_reachable` が `false` で、呼び出し元がエージェントか | `403 forbidden` |
| 5 | `required_permission` を呼び出し元が持つか | `403 forbidden` |
| 6 | **呼び出し元がエージェントのとき、`assignee_id` が自分の所有者か** | `403 forbidden` |
| 7 | **遷移先が完了カテゴリのとき、未完了の子チケットが残っていないか** | `409 children_not_closed` |

3〜6 が `Requirements.md` 10.10.4「承認ゲートをAPIレベルで強制する」の実体である。**画面側の制御に依存しない。**

#### 検証6 — エージェントは所有者の担当だけを進められる

**エージェントが状態を変えてよいのは、`assignee_id` が自分の所有者であるチケットに限る**。担当が付いていないチケット（`assignee_id IS NULL`）も進められない。

**「所有者が引き受けている」ことが、そのエージェントが動かしてよい根拠になる。** `Design.md` 6.5 は「権限の根拠は所有者」と定めた（委譲）。**そこに「作業の根拠も所有者」が加わる**——人がチケットを引き受けていないのに、その人のエージェントがボードの状態を動かすことはない。**人がループに残る。**

**呼び出し元が人（`actor.kind='user'`）のときは適用しない。** 全員に掛けると画面が壊れる——`ticket.transition` を持つ人が他人の担当を進められなくなり、`GuiDesign.md` 5.5 の状態ドロップダウンが自分の担当でしか使えなくなる。**MCP 経由の人にだけ掛ける形も採らない**——同じ操作の可否が経路で変わり、`Design.md` 8.1 の「同じ規則を2か所に書かない」に反する。**種別で分けることで、検証3・4 と同じ土俵に乗る。**

**この判定は `Design.md` 6.4.4 の宣言では表せない。** ミドルウェアはプロジェクトまでしか知らず、「どのチケットか」を見ないためである。**行を読んでから決まる判定をハンドラに置くのは、9.8 のコメント（`comment.edit_own`）に続いて2例目である。** 3例目が現れたら、`Design.md` 付録A 論点①（権限の全体像の再整理）をそこで行う。

**検証6 を最後に置くのは、これが行に依存する唯一の検証だからである。** 行を読まずに決まる障害（順路が無い・種別が違う・権限が無い）を先に返したほうが、利用者が取り除く順序と一致する。

**`working_agent_id` は判定に使わない。** あれは実行者の自己申告で、人がいつでも消せる（`DbDesign.md` 6.6）。**消しただけで作業が止まる列を認可に使わない。**

#### 遷移に成功したとき、エージェントは自分を `working_agent_id` に立てる

**呼び出し元がエージェントなら、同じトランザクションで `working_agent_id` を自分にする**（既に自分なら何もしない。別のエージェントが入っていれば上書きする）。「着手した」と「宣言した」が別々に起こる状態を作らないためで、**専用の操作を持たない。**

**上書きを許すのは、途中でエージェントを替えるのが通常の運用だからである**（`DbDesign.md` 6.6）。**排他ではない**——同じ所有者の2つのエージェントが同じチケットを進めた場合、後から進めたほうが立つ。PB の並行制御は一貫して「検出」であって「排他」ではなく、この欄も例外ではない。

**`activity` には `working_agent_id` の変更を記録しない。** 遷移の行（`action='transition'`）が「誰が進めたか」を `actor_id` で既に持っており、同じ事実が2行になる。人が `PATCH`（9.5.2）で変えたときは通常どおり1行記録する。

#### 検証7 — 未完了の子が残っている親は完了にできない

**遷移先の `category` が `done` のとき、直下の子に `closed_at IS NULL` のものが1件でもあれば拒否する**。

**直下の子だけを見る。** 孫まで数えないのは、同じ規則が子にも掛かるためである——孫が未完了なら子も完了にできず、子が完了していなければ親はここで止まる。**規則が段ごとに効くので、再帰は要らない。**

**`closed_at IS NULL` で数える。** 本節の「`closed_at` は遷移の副作用としてのみ動く」により、これが「完了していない」と一致することが保証されている（9.2 の `?open=true` と同じ判定である）。ステータスのカテゴリで数え直す案は採らない——**同じ事実を2通りに数えると、片方だけ直した日にずれる。**

**`409 children_not_closed` を返す**（9.14）。403 ではないのは**権限の問題ではない**からである——**同じ人が、子を完了させたあとなら通る。** 検証2 と同じ 409 に置くのは、どちらも「盤面がその遷移を許さない」であり、利用者が次に取る行動が「別の何かを先に済ませる」で共通するためである。

**検証6 の後に置く。** 行に依存する検証を後ろに並べる方針（検証6 の項）に従う。6 と 7 はどちらも行を読むが、**6 は「あなたが動かしてよいか」、7 は「いま動かしてよいか**」であり、前者を先に返すほうが利用者が取り除く順序と一致する。

**人にもエージェントにも等しく掛ける。** 検証6 と違って種別で分けないのは、これが**盤面の整合**についての規則だからである——「未完了の子を抱えた親が完了している」状態は、誰が作っても同じように壊れている。

#### 子が動いたら、親を進行中にする

**遷移に成功した結果そのチケットが未着手カテゴリを出たなら、同じトランザクションで祖先を進行中にする**。

| 条件 | 動き |
|---|---|
| 遷移前の `category` が `todo` で、遷移後が `todo` 以外 | 親をたどる |
| 親の `category` が `todo` | ワークフローの `in_progress` カテゴリのうち `sort_order` 最小のステータスへ移し、さらに上の祖先へ進む |
| 親の `category` が `todo` 以外 | **そこで打ち切る**（その上の祖先も見ない） |

**打ち切ってよいのは、親が既に動いているなら祖先も動いているからである。** この規則自体が親を進めるとき同じ経路を通るので、**`todo` でない親の上に `todo` の祖先は残らない。**

**掛ける検証は2（順路の定義）だけである。** 検証3〜7 は掛けない——**連動はすでに認可された操作の帰結であって、新しい操作ではない。** ここで検証5（権限）や検証6（担当が所有者か）を掛けると、**親の担当が別人であるという理由で子の着手が失敗する**ことになる。

**順路が定義されていなければ黙って飛ばす。** 親のワークフローに `todo → in_progress` が無いことを理由に子の遷移を 409 にすると、**関係のないチケットが着手できなくなる。** 連動は付随的な整合であって、子の遷移の成否を左右しない。

**親に `working_agent_id` は立てない。** あれは「自分がこのチケットを処理している」という自己申告であり（`DbDesign.md` 6.6）、**エージェントは親を処理していない。**

**`activity` に `action='transition'` を1行残す。** `actor_id` は子を進めた本人になる——連動を起こした責任はそこにあり、システムアクターを立てると「誰の操作でこうなったか」が辿れなくなる。**コメント（`kind='progress'`）は作らない**——添える本文が無い。

**`closed_at` は動かない**（遷移先が `done` ではない）。**`version` は動かした祖先のぶんだけ +1 される。**

**応答は子のチケットである。** 連動で動いた親を応答に混ぜない——呼び出しは子に対するものであり、9.5.1 の形を変えない。**画面は親の行を読み直す**（`GuiDesign.md` 5.4 のバックログは遷移後に一覧を引き直す）。

#### 着手したら、オンステージへ上げる

**遷移の結果そのチケットが未着手カテゴリを出たなら、同じトランザクションで、表示上のトップレベルの祖先を `staged_at` に立てる**。

| 条件 | 動き |
|---|---|
| 遷移前の `category` が `todo` で、遷移後が `todo` 以外 | 表示上のトップレベル（親が無い、または親がエピック）まで祖先をたどる |
| その行の `staged_at` が `NULL` | `now()` を入れ、**バックログ段の末尾ではなく現在の位置のまま**オンステージ段に出る（`sort_key` は動かさない） |
| その行が既にオンステージ | 何もしない |

**上げるのは自分ではなく「表示上のトップレベルの祖先」である。** 段に置けるのはトップレベルだけで、**配下は親と一緒に運ばれる**（9.4.1 の `not_stageable`）。子タスクに着手したとき、動かすべきなのは**その子を含む部分木の根**である。

**`sort_key` を動かさない。** 二段は同じ順序キーを1本共有しており（9.4）、段が変わっても位置は保たれる。**着手のたびに順序が変わると、人が手で組んだ消化順が壊れる。**

**この連動は「子が動いたら、親を進行中にする」連動と同じ経路を通り、同じ規則で掛ける。**

- **掛ける検証は2（順路の定義）だけである。** 段の移動は**すでに認可された遷移の帰結**であって、新しい操作ではない。ここで検証5（権限）や検証6（担当が所有者か）を掛けると、**祖先の担当が別人であるという理由で子の着手が失敗する**
- **`activity` には書かない。** 遷移の行が「誰が進めたか」を既に持っており、**`staged_at` は 9.5.2 で `PATCH` できない**ので、人が手で動かす経路（9.4）と混ざることもない
- **`version` は動かさない。** `staged_at` は 9.5.2 で `PATCH` できない欄なので、開いている詳細ペインの `If-Match` が古くなっても**失われる編集が無い**。逆に上げると、着手のたびに祖先を開いている画面が `409` になる（9.12.1 が `sprint_id` を書くときと同じ理由）

**逆向きの連動は持たない**——**未着手へ戻してもオンステージから降ろさない**。降ろす操作は2つある。**手で戻す**（9.4.1 の `staged: false`）か、**スプリントを終える**（9.12.2）かである。**「未着手だがオンステージ」は段が表せなければならない状態**であり（`DbDesign.md` 6.6、`GuiDesign.md` 5.4）、状態を戻したという理由だけで降ろすと、**やり直しのために状態を戻した行が仕掛りから消える。**

**手で上げる操作は残る。** 「まだ着手していないが、今スプリントで消化する」を表すのが段の役割であり、本連動が置き換えるものではない——**取りこぼしを拾うだけである。**

**`closed_at` の規則**

| 遷移先の `category` | `closed_at` |
|---|---|
| `done` | `now()` を設定 |
| `done` 以外 | `NULL` へ戻す |

**`closed_at` は遷移の副作用としてのみ動く。** これにより 9.2 の `?open=true`（`closed_at IS NULL`）が「完了していないもの」と一致することが保証される。`PATCH` で直接書けないようにしているのは（9.5.2）、両者がずれると一覧と集計が食い違うためである。

`comment` が付いていれば、**同じトランザクションで `kind='progress'` のコメントを作る**（`DbDesign.md` 6.7）。`activity` には `action='transition'` で記録する。

応答は 9.5.1 と同形式。**`version` は +1 される。**

**完了レポートの提出（9.15）はここを通らない。** 26b で遷移が独立した以上、
**レポートの提出と状態遷移を1つの操作に混ぜない**。

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
| 6 | 呼び出し元がエージェントで、`assignee_id` が所有者でない | `このチケットの担当者があなたの所有者ではないため、エージェントからは変更できません` |
| 7 | 遷移先が完了カテゴリで、未完了の子チケットが残っている | `未完了の子チケットが残っているため完了にできません` |

**検証7 も同じ関数を通る**。画面のステータスドロップダウンは `reason` をそのまま出すので（`GuiDesign.md` 5.5）、**未完了の子を抱えた親では「完了」の行に理由が付いて押せなくなる**——この一文のために画面を直す必要はない。

**検証6 はチケット単位の条件なので、`items[]` の全行が同時に `allowed: false` になる。** 遷移先ごとに違う理由が並ぶ他の検証とは性質が異なるが、**行ごとに理由を付ける形は変えない**——エージェントは「この1件はどうか」を見て次の一手を決めるので、表の形が揃っているほうが読み違えない。

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
| `body_md` | 必須。1文字以上、**20000字以内**（前後の空白を除いて数える）。超えると `too_long` |
| `kind` | `discussion`（既定） / `decision` / `artifact` / `caveat` / `reference` / `progress` |
| `in_reply_to` | 任意。同じチケットのコメントの ULID |

**20000字の上限は、コメントを作るすべての口に掛かる**——遷移に添える `comment`（9.6）と、完了レポートから組み立てるコメント（9.15）も同じである。**どの口から入った本文も、`PATCH` で保存し直せる長さに収める。** 上限はチケットの説明欄と同じ値で、1件で応答を膨らませる本文を弾くためにある。

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

**`ETag` を持つ子資源はコメントだけである。** 2.7 が「ポーリングは実装していないが
応答ヘッダだけ先に用意する」と定めており、**エージェントが「新しいコメントが
付いたか」を安く見る口がここになる**（`Requirements.md` 1章）。DoD とリンクは画面を
開いた時点で詳細応答（9.5.1）に入っており、単独で追う対象にならない。

**`If-Match` は持たない**（2.8 の楽観ロックの対象は `project` と `app_user` に限られる）。
**親チケットの `version` と `updated_at` も動かさない**——コメントの増減は `ticket` の
列を変えないためで、外部参照（9.10.2）と同じ扱いである。

### コメントの変更を `activity` に記録する

投稿・編集・削除のいずれも **`activity` に1行書く**（9.1.1）。

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
履歴に「何が書かれたか」が1行も残らないと、ダッシュボードの「最近の動き」が
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

**`ticket.self_edit` でも通る。ただし `is_satisfied` は送れない**（0029）。送ると `403` になる。**`pb_submit_result` が「盤面を動かさない」と決めた判断と正面からぶつかる**ためで（9.15）、**完了の判定は人が行う。** エージェントに開けるのは `body` の追加・編集・削除までである。

**`sort_order` は開ける。** 並べ替えは記述の整理の一部であって、盤面の判定ではない。

| フィールド | 検証 |
|---|---|
| `type` | **`manual` のみ**。他の値は `422`（`details[].code = "unsupported_type"`） |
| `body` | 必須。完了条件の文 |
| `is_satisfied` | 真偽値。`PATCH` でチェックを付け外しする |
| `sort_order` | 並び順。省略時は末尾 |

`is_satisfied` を `true` にしたとき、サーバが `satisfied_at` と `satisfied_by`（呼び出し元）を設定する。`false` に戻すと両方 `NULL` へ戻す。

`assertion`（コマンド実行）・`artifact`（成果物の存在確認）・`review`・`task_ref` は未実装（`Requirements.md` 10.5.2、`GuiDesign.md` 5.5）。**表とその列は `DbDesign.md` 6.11 の形で作ってあり、API が受け付ける `type` だけを絞る。** 後から列を足すより、使わない列を持つほうが安い。

**`type` は `POST` で省略できる**（既定 `manual`）。**`PATCH` で送ると `422`、
`details[].code = "immutable_field"`**——取りうる値が1つしかない以上、
変更を受け付けても何も起こせない。他の型を開けるときに、
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
いずれも `manual` 以外の型と AI提案のためのもので、**API が受け付けない値を
応答に並べると「使える」ように見える。** 他の `type` を開けるときに、
同じ改訂でこの3つも応答へ足す。

**`sort_order` を省略したときは末尾（現在の最大値 + 10）。** 9.10.2 の外部参照と同じ
採番で、**10 刻みにするのは間に挿し込む余地を残すため**である。

**ページネーション・`ETag`・`If-Match` はいずれも持たない**（9.10.2 と同じ）。
1チケットあたり数件に収まり、詳細応答（9.5.1）の `dod` に同じ一覧が入る。
**親チケットの `version` と `updated_at` も動かさない。**

**エージェントの完了レポート（9.15）も `is_satisfied` を動かさない。** `manual` の定義が
「人間がチェックを入れる」である以上（`Requirements.md` 10.5.2）、**エージェントの自己申告で
チェックが立つと型の定義に反する。** レポートは未充足の項目を応答で返すだけで、盤面は
人が動かす。

**完了条件を満たしていなくても、`done` への遷移は止めない**。9.6 の
検証の順序（`DbDesign.md` 6.5）に DoD は含まれず、**チェックリストは人が読む道具**である。
自動判定に基づいて遷移を止めるのは、`assertion` / `artifact` が入ってから
（`Requirements.md` 10.5.2「AIが完了と言ったから完了」の回避）——**判定できない型で
遷移を止めると、人が自分のチェック漏れで進めなくなるだけになる。**

### 完了条件の変更を `activity` に記録する

追加・更新・削除のいずれも **`activity` に1行書く**（9.1.1）。
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
——順序を1回入れ替えただけで変更履歴が埋まり、
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
**それを禁じるのは DB でもこの API でもない**——依存の循環検出はガント（未実装）で扱う（10.2）。

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
`lag_days` だけのために1本増やす利得も無い——**`lag_days` を読む画面が無い**（下記）。

**ページネーション・`ETag`・`If-Match` はいずれも持たない**（9.10.2 と同じ）。
**親チケットの `version` と `updated_at` も動かさず、相手側のチケットも動かさない**
——リンクの増減はどちらの `ticket` の列も変えないためである。

**プロジェクトを跨ぐリンクは作れない。** `DbDesign.md` 6.6 の `ticket_link` に制約は無いが、API が `target_seq` で受ける以上、同一プロジェクトに閉じる（9.1）。跨ぐ必要が出た時点で `target` の指定方法ごと設計する（10.2）。

`origin` は `human` / `ai_suggested`。**`human` のみ作られる**（AI提案の採用・却下は未実装。`GuiDesign.md` 5.5）。

**画面が出す `link_type` は `relates` / `duplicates` / `blocks` の3つだけである**
（`GuiDesign.md` 5.5）。`FS` / `SS` / `FF` / `SF` と `lag_days` は
**ガントの依存線**のためのもので、ガントは未実装（`GuiDesign.md` 3.2）。
**読む画面が無い値を人に選ばせても、入れた本人が結果を確かめられない。**
**API は7種すべて受け続ける**——MCP とエージェントがガント用の依存を先に積むことは
妨げない（`Requirements.md` 10.5）。

#### リンクの変更を `activity` に記録する

追加・削除のいずれも **`activity` に1行書く**（9.1.1）。
列の使い方は 9.10.2 と同じで、**`field` は `link`、`action` は常に `update`** である。

| 変更 | `old_value` | `new_value` |
|---|---|---|
| 追加 | `NULL` | `blocks my-app-12` |
| 削除 | `blocks my-app-12` | `NULL` |

**要約は `<link_type> <相手の完全形ID>`**（9.1 の `<key>-<seq>`）。
**操作したチケット側に1行だけ書き、相手のチケットの履歴には書かない**——1回の操作で
2行増えると、ダッシュボードの「最近の動き」で同じ出来事が二重に見える。
`direction` を要約に含めないのも同じ理由で、**読み手はそのチケットの履歴を見ている。**

### 9.10.2 外部参照

```
GET|POST     /api/v1/projects/:key/tickets/:seq/references
PATCH|DELETE /api/v1/projects/:key/tickets/:seq/references/:id
```

**必要権限**：`GET` は `ticket.view`、更新系は **`ticket.reference.edit`**（0027）

**更新系を `ticket.edit` から切り出した**。`DbDesign.md` 6.12 が「`kind='code'` の書き手は**エージェント**」と定める一方、エージェントのトークンに載せられる権限の許可リスト（4.5.3）は `ticket.edit` を含まず、**設計文書が定めた書き手が書けなかった**。許可リストへ `ticket.edit` を足すと `PATCH /tickets/:seq`・`move`・DoD（9.9）・チケット間リンク（9.10.1）まで同時に開く（棄却の理由は `DbDesign.md` 6.12.1）。**`ticket.edit` を持つロールにはすべて配るので、人から見た可否は変わらない。**

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

**`created_by` は返すが、画面は使わない**。
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
（`Requirements.md` 10.6.1 の `artifacts` にあたる）。

**書き手は、MCP の `pb_add_reference` を使うエージェントと、`/me/tokens` の API トークンを持つ
クライアントである**（4.4）。画面は `code` の追加を持たず、表示と削除だけを行う
（`GuiDesign.md` 5.5）——誤って積まれた行を人が始末できる必要があるためである。

#### 変更を `activity` に記録する

追加・更新・削除のいずれも **`activity` に1行書く**（9.1.1）。
変更履歴（`GuiDesign.md` 5.5）が「誰がいつどのコミットを紐づけたか」を読めるようにするためで、
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
**「そのチケットが消された」を意味しており**（9.5.3）、ダッシュボード
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

**権限を増やさない。** タグの定義は「プロジェクトの分類軸を決める」行為であり `project.edit`、チケットへの付与は「チケットの属性を変える」行為であり `ticket.edit`（9.5.2 の `tag_ids`）で足りる。**既存権限の内側に収まるものでカタログを増やさない。** 10章の `doc.view` / `doc.edit` は逆に足した側だが、**あちらは新しい資源そのもの**であり、タグのように既存資源の属性ではない。この線引きが `Design.md` 付録A の「粒度は確定」の意味である。

**`ticket_count` を一覧に含める。** プロジェクト設定のタグタブ（`GuiDesign.md` 5.9）が削除時に「12件のチケットで使われています」を出す（設計原則：破壊的操作の確認、6.3）。

`DELETE` は `ticket_tag` の行を `CASCADE` で消す。**使用中でも削除できる**（確認ダイアログで件数を示したうえで）。使用中の削除を禁止すると、要らなくなった分類を消すために全チケットを手で外すことになる。

**色を持たせない。** `GuiDesign.md` 8.6 が「ラベルに任意色を許さない」と定めている。ユーザーごとに色の意味が食い違い、一覧が虹色になって輝度による階層が崩れるためである。

## 9.12 スプリントAPI

```
GET|POST     /api/v1/projects/:key/sprints
PATCH|DELETE /api/v1/projects/:key/sprints/:id
POST         /api/v1/projects/:key/sprints/start
POST         /api/v1/projects/:key/sprints/:id/finish
```

| メソッド | 必要権限 |
|---|---|
| `GET` | `ticket.view` |
| `POST` / `PATCH` / `DELETE` / `start` / `finish` | **`project.edit`** |

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

**スプリントの CRUD を定義する理由。** `GuiDesign.md` 5.5 のチケット詳細がスプリント欄をメタ情報として並べている（`sprint` 表は 0009）。**作る手段が無いまま選択欄だけを置くと、常に空のドロップダウンになる。** **定義はプロジェクト設定のスプリントタブで行う**（`GuiDesign.md` 5.9）。バーンダウン・ベロシティは進捗分析（構想。`GuiDesign.md` 10章）が持つ。

**運用（開始・終了）は 9.12.1 / 9.12.2 である。** **定義はスプリントタブ、運用はバックログのオンステージ段が持つ**（`GuiDesign.md` 5.4）。**スプリントは「チケットにあらかじめ付ける属性」ではなく「いまどの期間で消化しようとしているか」**であり、対象は1件ずつ選ぶものではなく**オンステージに載っているもの全部**である。

### 9.12.1 `POST /projects/:key/sprints/start` — 始める

```json
{ "name": "Sprint 4", "goal": "スプリント運用を通す",
  "start_date": "2026-09-08", "end_date": "2026-09-21" }
```

**スプリントを新しく作り、`active` にし、オンステージに載っているものを対象に入れる**——この3つを1つのトランザクションで行う。

| 段階 | 動き |
|---|---|
| 1 | `sprint` を1行作る。`status` は **`active`**（`planned` を経由しない） |
| 2 | **オンステージの行とその部分木**を `ticket_sprint` に入れる（`added_at = now()`） |
| 3 | 同じ集合の `ticket.sprint_id` を、作ったスプリントへ更新する |

**対象は「オンステージ段に出ている行」である。** `staged_at IS NOT NULL` の行**とその子孫**であり、`staged_at` だけで決めてはならない——**段を決めるのは親で、子は親と一緒に運ばれる**（9.4.1、`GuiDesign.md` 5.4）。**エピックは除く**（どちらの段にも行として出ない）。

**棚に戻ったものも起点から外す**（9.2.1 の条件1・2。起点は表示上のトップレベルなので条件3 は自動的に満たされる）。9.12.2 が段を降ろすのは終了の一度きりで、**そのあとに完了した根はオンステージに残る**ためである。

| フィールド | 検証 |
|---|---|
| `name` | 必須。1〜50文字。前後の空白を取り除いてから検証する（9.12 と同じ） |
| `goal` | 任意 |
| `start_date` / `end_date` | 任意。両方あるとき `start_date <= end_date` |

**進行中のスプリントが既にあれば `409 active_sprint_exists`**。**`active` は同時に1本だけである**——オンステージは1つしかなく、「いまどの期間で消化しようとしているか」の答えが2つあると、開始のたびにどちらへ入れるかを選ぶことになる。**複数チームが並行してスプリントを回す運用は、プロジェクトを分ける形で表す。**

**オンステージが空でも通す。** 期間を先に切ってから積む進め方があるためで、**開始できない理由にしない。**

**既にある `planned` のスプリントを開始する形にはしない**。チケットの本文が「新規スプリント開始のダイアログ」と定めており、**始めるたびに名前と期間を決めるほうが、先に作った定義を探して選ぶより短い。** スプリントタブで作った `planned` の行は定義として残り、**開始の選択肢には出ない**。**この使い分けが実運用で邪魔になったら、`start` に `sprint_id` を受ける形へ広げる**（11.2）。

**応答は 9.12 の一覧と同じ1件の形に、`ticket_count` / `closed_count` を載せて返す。**

### 9.12.2 `POST /projects/:key/sprints/:id/finish` — 終える

**本文を取らない。**

| 段階 | 動き |
|---|---|
| 1 | `sprint.status` を **`completed`** にする。`end_date` が空なら `CURRENT_DATE` を入れる |
| 2 | そのスプリントの `ticket_sprint` すべてに `removed_at = now()` を立てる |
| 3 | **完了しているオンステージの根を `staged_at = NULL` にする**（オンステージから降ろす） |
| 4 | **未完了のものは `staged_at` を触らない**（オンステージに残り、次の `start` でそちらへ入る） |

**終了したあとに完了した根は、オンステージに残る。** 段を降ろすのは終了の一度きりだからである。**そのままでは次の `start` の対象に入ってしまう**ので、9.12.1 が棚に戻ったものを起点から外す（実データで判明）。**画面からは既に消えている行が、次のスプリントの `ticket_count` に混ざる**という形で表に出た。

**`status` が `active` でなければ `409 sprint_not_active`。**

**降ろすのは「完了しているオンステージの根」だけである。** 子は `staged_at` を持たないので触るものが無く、親と一緒に降りる。**根が未完了なら、完了した子も一緒にオンステージへ残る**——9.2.1 の条件3 と同じ考え方で、部分木は丸ごと動く。

**`ticket.sprint_id` は消さない。** 終わったあとも「最後に属したスプリント」を指し続ける。9.2.1 の条件2 がこれを読んで「棚に戻ったか」を決めるので、**ここで `NULL` にすると判定の材料が消える。**

**降ろした行は、バックログ段にも出ない**（9.2.1 の3条件を満たすため）。**`staged_at` を `NULL` に戻すこと自体は「バックログへ戻す」だが、棚に戻ったものは一覧から外れるので、結果として画面から消える。** 親が未完了で残っている子は条件3 で外れないため、バックログ段に親と一緒に出続ける。

**`sprint.status` を `PATCH` でも `completed` にできる**（9.12 の `PATCH`）。**あちらは定義の修正で、`ticket_sprint` も `staged_at` も動かさない。** 運用として終えるときは本節を使う。**同じ結果を2通りで作れる状態は望ましくないが、`PATCH` から `status` を落とすと 9.12 のスプリントタブが状態を直せなくなる**ので残した（11.2 で見直す）。

### 9.12.3 スプリント中にオンステージへ入ったもの

**進行中のスプリントがあるとき、オンステージへ入った部分木は、その場でスプリントに所属させる**。所属を書くのが 9.12.1 の開始だけだと、**スプリント中に配下へ作ったチケットは所属を持たず**、9.2.1 の条件2（最後に属したスプリントが `completed`）を満たせない。根と一緒に完了しても、**終了後に根だけが棚に戻り、配下がバックログに残る。**

| 入口 | 所属させるもの |
|---|---|
| 作成（9.3） | 表示上の根がオンステージに居るとき、作ったチケット |
| 親の付け替え（9.5.2） | 新しい親の表示上の根がオンステージに居るとき、付け替えたチケットとその配下 |
| 段の移動（9.4.1） | オンステージに置いたチケットとその配下 |
| 着手による段上げ（9.6） | 上がった根とその配下 |

- **9.12.1 の開始と同じ書き方をする。** `ticket_sprint` に行を足し、`ticket.sprint_id` を立てる。既に所属していれば何もしない
- **棚に戻った根（完了し、最後のスプリントが `completed`）の配下は所属させない。** 9.12.1 が開始の対象から外すのと同じ判定である
- **出ていく方向は扱わない。** スプリント中にオンステージから下ろしても所属は残る。完了していなければ一覧の見え方は変わらず、完了していれば終了の時点で棚に戻る
- **スプリントの件数（9.12 の `ticket_count`）には、途中で加わったものも入る**

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

**エピック（`type='epic'`）は数えない。** 本エンドポイントのすべての項目に掛かる（`by_category` / `total` / `open` / `overdue` / `stale` / `unassigned`）。理由は2つある。

1. **`GuiDesign.md` 5.4 が「エピックは行として並ぶと『やるべき仕事』の数に混ざる」と定めており、同じ理由が集計にも効く。** エピックはグルーピング専用であり（`DbDesign.md` 6.10）、`unassigned` に混ざると「担当者が居ない5件」に見えるが、エピックに担当者を置く運用は無い
2. **4枚のカードは押すとバックログをそのカテゴリで絞って開く**（`GuiDesign.md` 5.3）。バックログはエピックを行として出さないので、除かないと**カードの数と押した先の件数が一致しない**（demo の seed で 15 対 12）

**カードに導線を付けるので、数の一致が要件になる。**

**`by_category` は常に4つのキーを持つ。** そのカテゴリのステータスがワークフローに1つも無くても `0` を返す。`simple` テンプレート（`DbDesign.md` 7.4）は `review` を持たないが、**キーが消えると画面のカードが3枚になり、「4つのカードの意味が変わらない」という上の目的が崩れる。**

`overdue` は `due_date < 今日` かつ `closed_at IS NULL`。`stale` は `updated_at` が `threshold_days` 日より前で `closed_at IS NULL`。**閾値はサーバが持ち、応答に含めて返す**（画面に「14日以上」と出すため。文言をフロントで組み立てない）。

**`threshold_days` は 14 で固定する**。5.3 のワイヤーフレームの文言と一致させたもので、プロジェクトごとの設定にはしない——**放置の基準を変えたくなるのは運用に載せてからであり、いま設定項目を作ると使われないまま形が固まる。**

**`unassigned` にも `closed_at IS NULL` が掛かる。** `assignee_id IS NULL` かつ未完了の件数である。完了したチケットに担当者が無いのは要対応ではなく、`overdue` / `stale` と条件が揃う。

**「今日」は DB の `CURRENT_DATE` で決める**（9.2.1 の `due_within` と同じ）。`app_user.timezone` は混ぜない——混ぜると同じプロジェクトの集計が読み手ごとに変わり、「要対応が3件」という会話が成り立たなくなる。

**`ETag`（2.7）は返さない。** 2.7 が対象とするのは一覧系 GET であり、本エンドポイントはページャを持たない。加えて **ETag の材料（件数と `MAX(updated_at)`）を採る走査が本体の集計とほぼ同じ**なので、付けても DB の仕事は減らない。

### 9.13.2 `GET /api/v1/projects/:key/activity`

```
GET /api/v1/projects/:key/activity?entity=ticket:31&page=1&per_page=20
```

| パラメータ | 説明 |
|---|---|
| `entity` | `ticket:31` の形。省略時はプロジェクト全体（ダッシュボードの「最近の動き」） |
| `action` | `create` / `update` / `delete` / `transition`。**単一値のみ** |
| `page` / `per_page` | 2.6。既定 `per_page=20` |

**`action` はカンマ区切りの OR を受け付けない。** 9.2.1 のフィルタ群と違う扱いだが、**本エンドポイントの消費者は2つとも「全件を時系列で読む」**（`GuiDesign.md` 5.3 の最近の動きと 5.5 の履歴）であり、複数選択を要する画面が無い。要るようになった時点で 9.2.1 と同じ形へ広げる。

**`entity` の書式違反は 422**（`error.code` は `validation_failed`、`details[].code` は `invalid`）。`ticket:abc` のように `seq` が整数でないもの、`foo:1` のように存在しない `entity_type`、区切りを欠くものが該当する。

**存在しない `seq` を指した場合は、空の一覧を `200` で返す**（`404` にしない）。`entity` は**資源の指定ではなくフィルタ**であり、9.2.1 の `assignee` や `tag` に存在しない ULID を渡したときと同じ挙動になる。**同じ理由で、削除されたチケットの履歴には `entity` で到達できない**——`seq` からチケットの ULID を引く経路が消えるためで、その行はプロジェクト全体の一覧（`entity` 省略）にだけ現れる。

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

**`actor` は `null` になりうる**（`activity.actor_id` は `ON DELETE SET NULL`。`DbDesign.md` 6.8）。9.2.2 の `assignee` / `reporter` と同じ扱いで、画面が「削除されたユーザー」と表示する。

**並び順は `occurred_at DESC, id DESC` で固定する。** クエリパラメータでは変えられない。`occurred_at` だけでは足りないのは、**1回の `PATCH` が変更した項目ごとに複数行を書く**ためである（9.5.2）——タイトルと期限を同時に変えると2行が同じ時刻になり、tie-break が無いと履歴の並びが実行ごとに変わる。`id` は ULID で単調増加なので、`id DESC` は「最後に書いた項目が上」になる。

**`ETag`（2.7）を返す。** ページャを持つ一覧であり、9.8 のコメント一覧と同じ扱いである。材料は**件数と結果の `MAX(occurred_at)`**、および 9.2.5 と同じくフィルタ条件（`entity` / `action`）を正規化した文字列である。

```
ETag: W/"act-a3f19c2b-142-1723372992000000000"
```

**`field` の値域は実装が定める。** 次の17種類が入る（`create` / `delete` は `field` が `null`）。

| 由来 | `field` |
|---|---|
| 遷移（9.6） | `status_key` |
| 本体の更新（9.5.2） | `type` / `title` / `body_md` / `priority` / `assignee_id` / `parent_id` / `estimate_point` / `estimate_hours` / `actual_hours` / `start_date` / `due_date` |
| 子資源の更新（9.8 / 9.9 / 9.10） | `comment` / `dod` / `link` / `reference.code` / `reference.doc` |

**`assignee_id` の値は ULID がそのまま入る。** 上の「表示名への変換は画面が行う」は `status_key` については成り立つ（ワークフローが 5.4 で手元にある）が、**これは解決先を持たない画面がありうる**——ダッシュボード（`GuiDesign.md` 5.3）はメンバー表を読まない。**画面がこの値をどう出すかは `GuiDesign.md` 5.3 / 5.5 の側で決める。**

**`sprint_id` は本表から外した**。9.5.2 で書けなくなり、動くのはスプリントの開始・終了のときだけになったためである。**その2つは `activity` に書かない**——1回の操作でオンステージの全チケットが動くので、**行ごとに1件記録すると、実際には1つの出来事である履歴が数十行に膨らむ**。スプリント自体を主体とする記録（`entity_type='sprint'`）は、9.1.1 が既に予告しているとおり 11.2 のままである。

**`body_md` の行は値を載せない**（9.5.2）。`old_value` / `new_value` はどちらも `null` で、「説明が変わった」ことだけが残る。

## 9.14 チケット固有のエラーコード

2.5.1 の表に加わるもの。

| Status | `error.code` | 意味 |
|---|---|---|
| 409 | `invalid_transition` | 現在のステータスから要求された遷移が `workflow_transition` に定義されていない（9.6） |
| 409 | `children_not_closed` | 遷移先が完了カテゴリだが、未完了の子チケットが残っている（9.6 の検証7） |
| 409 | `active_sprint_exists` | 進行中のスプリントがあるのに、もう1本始めようとした（9.12.1） |
| 409 | `sprint_not_active` | 進行中でないスプリントを終了しようとした（9.12.2） |

`details[].code`（`error.code` は `validation_failed`）として加わるもの。

| `details[].code` | 意味 |
|---|---|
| `parent_cycle` | 自分自身または自分の子孫を親に指定した（9.5.2） |
| `unknown_status` | 遷移先がプロジェクトのワークフローに存在しない（9.6） |
| `not_a_member` | 担当者に指定したアクターがプロジェクトのメンバーでない（9.3） |
| `not_found` | `parent_seq` / `tag_ids` の参照先がこのプロジェクトに無い（9.3）。`target_seq` の相手がこのプロジェクトに無い（9.10.1）。`in_reply_to` の相手がこのチケットに無い、または削除済み（9.8） |
| `self_link` | 自分自身へのリンクを作ろうとした（9.10.1） |
| `immutable_field` | サーバが決める項目、または作成後に変えられない項目を送った（9.5.2 / 9.8 / 9.9 / 9.10.2） |
| `use_move_endpoint` | `sort_key` / `staged_at` を `PATCH` で変えようとした（9.5.2） |
| `use_sprint_endpoint` | `sprint_id` を `PATCH` / `POST` で書こうとした（9.5.2 / 9.3） |
| `not_stageable` | 表示上のトップレベルでないチケットを `staged: true` で上げようとした（9.4.1）。**オンステージのチケットを、段に置けなくなる `type` / `parent_seq` へ変えようとした場合も同じ**（9.5.2） |
| `use_transition_endpoint` | `status_key` / `closed_at` を `PATCH` で変えようとした（9.5.2） |
| `unsupported_type` | まだ受け付けていない値を指定した（DoD の `type` など。9.9） |

## 9.15 完了レポート

```
POST /api/v1/projects/:key/tickets/:seq/reports
```

**必要権限**：`ticket.transition`

**エージェントが1回の作業の結果を構造化して提出する口である**（`Requirements.md` 10.6.1）。
`DbDesign.md` 8.2.4 の `agent_run` と `agent_report` に1行ずつ書き、**同じトランザクションで
完了レポートのコメントを1件作る。**

**状態を進めない。** 26b で遷移が `pb_transition_task`（9.6）として独立したので、
**完了レポートの提出と状態遷移を1つの操作に混ぜない**。
**チケットもクローズしない**——`done` への遷移は `is_agent_reachable = false` かつ
`allowed_actor_kinds = ["user"]` で、DB とワークフローが拒む（`DbDesign.md` 7.4）。

**必要権限に `ticket.transition` を選んだ**のは、`Design.md` 8.2 の表がそう定めていることと、
レポートが「作業の完了を申告する」操作で**盤面を動かす権限と同じ重さにある**ためである。
専用の権限キーを足さない——`DbDesign.md` 7.2 は権限キーを削除しないと定めており、
**使い分ける必要が現れる前にカタログを増やさない。**

### 要求

```json
{ "status": "completed",
  "artifacts": [{ "type": "pull_request", "url": "https://github.com/example/app/pull/45" }],
  "dod_results": [{ "id": "01K2...", "passed": true, "evidence": "pytest tests/auth/ → 24 passed" }],
  "findings": [{ "kind": "decision", "body": "リフレッシュトークンの保存先を X ではなく Y にした。理由は…" }],
  "failures": [{ "approach": "ライブラリZのミドルウェアを使う", "reason": "非同期ランタイムの版が競合" }],
  "proposed_subtasks": [{ "title": "ローテーションの実装", "rationale": "本タスクのスコープ外だが必要" }],
  "knowledge_impact": "minor",
  "cost": { "tokens": 128000, "turns": 34, "wall_clock_min": 42 } }
```

**本体は `Requirements.md` 10.6.1 のレポートから `task_id` を抜いたものである**（チケットは
URL が指す。9.1 が「URL とチケット番号を一致させる」と定めているのと同じ理由）。

| フィールド | 検証 |
|---|---|
| `status` | **必須**。`completed` / `blocked` / `partial` のいずれか。無ければ `required`、他の値は `invalid` |
| `knowledge_impact` | 省略可。`none` / `minor` / `major` のいずれか。省略時は `null`。他の値は `invalid` |
| `cost.tokens` / `cost.turns` / `cost.wall_clock_min` | 省略可。**0 以上の整数**。負なら `out_of_range` |
| `dod_results[].id` | このチケットの完了条件の `id`。**このチケットに無い `id` は `not_found`** |
| `dod_results[].passed` | 真偽値。他の型は `invalid` |
| その他のキー | **形（配列かオブジェクトか）だけを見て、そのまま保存する** |
| 整形したコメントの本文 | **20000字以内**（9.8 と同じ上限）。超えると `details[].field = "report"`、`code = "too_long"` |

**`details[].code` は既存の語彙だけを使う**（`required` / `invalid` / `out_of_range` /
`not_found` / `too_long`）。**新しいコードを発明しない**——9.14 の表に加わるものは無い。

**検証を「列に出す値」だけ厳しくする。** `status` と `knowledge_impact` は `agent_report` の
列に、`cost.tokens` / `cost.turns` は `agent_run.tokens_used` / `turns` に展開されるので
（`DbDesign.md` 8.2.4）、**綴りや値域の誤りが静かに落ちると集計が壊れる。** 残りは `report`
jsonb にそのまま入るだけなので、**知らないキーも拒まず保存する**——`Design.md` 8.4 が
「モデルが型を取り違えても受けて通す」を採っているのと同じ判断で、余分なキーを1つ付けた
だけで提出が丸ごと落ちると、往復が増えるだけで誰も得をしない。

**`dod_results[].id` だけは厳しくする。** 存在しない `id` を黙って捨てると、
**エージェントは報告したつもりの項目が未充足として返ってくる**ことになり、`/pb-implement`
の手順7（`Requirements.md` 10.8.6）が「修正して再提出」を繰り返す。

### コミット ID は `artifacts` に入れない

**コミット・ブランチの識別子は、9.10.2 の外部参照（`kind='code'`）に積む**。MCP では `pb_add_reference` で、`repository` / `branch` / `commit_sha` を**それぞれの列**に持つ。`artifacts` はプルリクエストの URL や変更したファイルのパスを並べる欄のままにし、**識別子の欄（`ref` など）を足さない**。

- **置き場を2つにしない。** 外部参照は「作業の跡」を積む口であり、チケット詳細の「コード」に出る（`GuiDesign.md` 5.5）。`artifacts` にも欄を足すと、同じコミットが2か所に書かれ、どちらを読めばよいかが決まらない。`artifacts` は `agent_report.report` の jsonb の中にあり、**画面から読む面も無い**
- **`path` や `url` にコミット ID を押し込まない。** 欄の意味が崩れ、後から機械で読めなくなる
- **`type` の語彙は固定しない**（自由文字列のまま。例は `pull_request` / `file`）。列挙にすると、知らないキーを拒まず保存する本節の方針とぶつかる
- **`custom_fields`（11.2）とは切り離す。** あちらは「プロジェクトがどんな欄を持つか」の設計であり、作業の跡の置き場とは別の問いである

**再検討のきっかけは、完了レポートを画面に出すか集計に使い始めたとき**である（`[画面に完了レポートのセクションを作る手順]` と同じ事象）。そのとき `artifacts` から外部参照を引けるようにするか、`artifacts` に識別子を持たせるかを、レポートの面と一緒に決める。

### 完了条件は書き換えない

**`dod_item.is_satisfied` を動かさない**。いま API が受け付ける
DoD の型は `manual` ひとつで（9.9）、その定義は「**人間がチェックを入れる**」である
（`Requirements.md` 10.5.2 の表）。**エージェントが立てると型の定義そのものに反する。**

`Requirements.md` 10.5.2 が「『AIが完了と言ったから完了』という状態を回避できる」と書いて
いるのは、`assertion`（検証コマンドと期待結果）・`artifact`（成果物の存在確認）による
**客観判定が入ってはじめて成り立つ**話である。その型を開けるのは別の手順であり、
**判定できない型のまま盤面を書き換えると、回避したかったものがそのまま起きる。**

`satisfied_by` / `evidence` も動かさない。**証跡は `agent_report.report` の
`dod_results[].evidence` に残り、完了レポートのコメントに描かれる**（下記）ので、
人はそれを読んでから画面でチェックを付ける。

### 応答

```json
// 201 Created
{ "id": "01K5...", "agent_run_id": "01K5...", "seq": 31,
  "status": "completed", "knowledge_impact": "minor",
  "submitted_at": "2026-09-05T12:00:00Z",
  "submitted_by": { "id": "01K4...", "kind": "agent", "display_name": "claude-code" },
  "comment_id": "01K5...",
  "unsatisfied_dod": [
    { "id": "01K2...", "type": "manual", "body": "設計文書を更新すること" }
  ] }
```

**`unsatisfied_dod` は「レポートの `dod_results` に `passed: true` として現れなかった
完了条件」である。** `is_satisfied = false` のものではない——**上記のとおりこの API は
`is_satisfied` を動かさないので、それを返すと作業直後は必ず全件になり**、
`/pb-implement` の手順7 が終わらなくなる。

**つまりこれは自己申告どうしの突き合わせである。** サーバはチケットの完了条件を数え上げ、
**エージェントが触れなかった項目と、`passed: false` と申告した項目**を返す。盤面は動かない
まま、報告の漏れだけがその場で分かる。

**`submitted_by` は 9.5.1 の `assignee` と同じ形である**（`{id, kind, display_name}`）。

**9.6 の検証6（エージェントは所有者の担当だけを進められる）は適用しない。あの規則は「ボードの状態を動かすこと」への制約**で、`ApiDesign.md` 9.6 が
「人がループに残る」と書いた根拠がそこにある。**レポートは状態を動かさない**うえ、
提出を拒んでも**作業した事実だけが消えて盤面は変わらない**。誰が提出したかは
`agent_run.actor_id` と コメントの投稿者に残る。

| 状況 | 応答 |
|---|---|
| 成功 | `201`。上の本体 |
| `status` が無い・値域外／`dod_results[].id` が不明／整形後の本文が長すぎる | `422 validation_failed` |
| `ticket.transition` を持たない | `403 forbidden` |
| チケットが無い・他プロジェクト | `404 not_found` |

**`GET .../reports` を置かない。** 人が読むのは**完了レポートのコメント**であり
（下記）、`agent_report` の行そのものを読む面が無い。**置くのは、チケット詳細に完了レポートの
セクションを作るときである**（`GuiDesign.md` 5.5。レポートが時系列に埋もれたとき、または
`agent_report` を集計に使い始めたときが、その再検討の条件）。

### 完了レポートのコメントを同じトランザクションで作る

**レポートを Markdown へ整形し、`kind='progress'` のコメントを1件作る。** チケットシステムでは
**コメントスレッドの中に成果を順次書き込む**使い方が多く、人とエージェントの共同作業にも合う。

**これが「人がレポートを読む面」である。** チケット詳細のコメント欄は、遷移（9.6 の
`kind='progress'`）・作業中のノート（`pb_post_note`）・人の議論が時系列に並ぶ場所であり、
**完了の報告もそこに並ぶのが読む順序として自然である。**

| | 決めたこと |
|---|---|
| `kind` | **`progress`（経過）。** 9.6 の遷移コメントと同じで、「作業の経過をサーバが1件書く」型に乗る。`artifact` は 6.5 で**成果物への参照**を指す型であり、レポートは参照ではなく報告である |
| `origin` | `agent`（呼び出し元の `actor.kind` から決まる。9.8 と同じ） |
| `author_id` | 呼び出し元。**エージェントが自分の名前で書く** |
| `agent_run_id` | 作った `agent_run` の `id`。**0007 が空けていた列がここで埋まる**（`DbDesign.md` 6.7） |
| 長さ | **整形後が20000字（9.8）を超えたら弾く**。切り詰めない——**切った部分を人が読む手段が無い**（`agent_report` に全文が残るが、読む API も画面も無い）。そのまま入れもしない——`PATCH` で保存し直せないコメントが生まれる。**弾くのはトランザクションの前**で、実行記録・レポート・コメントのどれも作らない |

**整形は REST 層が行う。** MCP 層に置くと、同じ整形の規則が2か所に生まれる
（`Design.md` 8.1）。**人が画面から提出する経路は無いが、規則の置き場は経路の数で決めない。**

**完了条件の表は2列にする**（判定を条件のセルへ前置する）。判定を独立の列にすると、
中身が記号1つなので**列が最小幅まで縮み、見出しの「判定」が縦に折り返される**——
詳細ペインは 750px 前後しかない（`GuiDesign.md` 5.5）。**行はチケットの完了条件を主にして
並べる**（レポート側を主にすると、報告されなかった項目が表から消え、`unsatisfied_dod` と
見え方が食い違う）。報告のない行は判定が `—`、証跡が「報告なし」になる。

本文の形（空の節は出さない）:

```markdown
## 完了レポート（完了）

**成果物**

- pull_request: https://github.com/example/app/pull/45

**完了条件**

| 完了条件 | 証跡 |
|---|---|
| ✅ ユニットテストが通ること | pytest tests/auth/ → 24 passed |
| ❌ 設計文書を更新すること | 人間のレビュー待ち |
| — 監査ログに残ること | 報告なし |

**判明したこと**

- 決定：リフレッシュトークンの保存先を X ではなく Y にした。理由は…

**試して駄目だったこと**

- ライブラリZのミドルウェアを使う — 非同期ランタイムの版が競合

**分割の提案**

- ローテーションの実装 — 本タスクのスコープ外だが必要

**コスト** 128,000 トークン / 34 ターン / 42 分　**知識への影響** minor
```

**`proposed_subtasks` はここに書くだけで、チケットを作らない**。
承認キュー（`proposal`）は構想である（`DbDesign.md` 8.3.2）。
**人が読んで要ると判断すれば、その場で `pb_create_ticket` を呼ばせれば済む。承認なしに盤面が増える経路を作らない。**

**コメントの削除はレポートを消さない。** 人がこのコメントを消しても `agent_report` の行は
残る（`comment.agent_run_id` は `ON DELETE SET NULL` の向きが逆で、コメント側が参照している）。
**読む面が消えるだけで、集計の材料は残る。**

### `activity` への記録

**1行書く**（9.1.1）。`field` は `report`、`action` は `create`、`new_value` は
`agent_report.status`（`completed` / `blocked` / `partial`）である。

**コメントの作成は別に記録しない。** 9.6 が遷移コメントについて同じ扱いをしている——
同じトランザクションで作られた1件が2行になると、履歴が同じ事実を二度言う。

**親チケットの `version` と `updated_at` は動かさない**（9.9 / 9.10.2 と同じ）。
レポートはチケットの列を1つも変えない。

---

# 10. プロジェクト文書API

`DbDesign.md` 8.1 の `document` / `document_revision` を扱う。`Requirements.md` 10.6.2 の
プロジェクト文書（憲章）——**規約・価値観・判断の基準を1か所に置き、全参加者のエージェントが
同じものを読む**——の供給経路である。

```
GET|POST      /api/v1/projects/:key/docs
GET|PATCH|DELETE /api/v1/projects/:key/docs/*path
GET           /api/v1/projects/:key/docs/*path/_revisions
GET           /api/v1/projects/:key/docs/*path/_revisions/:no
```

| メソッド | 必要権限 |
|---|---|
| `GET` | `doc.view` |
| `POST` / `PATCH` / `DELETE` | **`doc.edit`** |

**`doc.edit` は `operator` と `project_member` が持たない**（`DbDesign.md` 8.1.4）。憲章は
全参加者を縛るため、更新できる人を絞る。**「その操作ができない人」が実在しない
という問題（`Design.md` 付録A）に対する、最初の実例でもある。**

## 10.1 パスによる指定

**文書はパスで指す。** `slug` を根から連ねたもので、`vision`、`rules/naming` のようになる。

**ULID も返すが、指定には使わない。** 9.1 のチケットが `seq` を使うのと同じ理由——共有できる
URL になり、画面の URL（`/p/:key/docs/rules/naming`）とそのまま一致する。

**`_revisions` はサブ資源の予約語である。** `slug` の検証は `^[a-z0-9][a-z0-9-]{0,63}$`
（`DbDesign.md` 8.1.1）で **`_` を含められない**ため、`.../docs/a/b/_revisions` が
「`a/b` のリビジョン一覧」なのか「`a/b/_revisions` という文書」なのかで迷うことがない。
**ワイルドカードの末尾セグメントを見て分岐する**実装になる。

**`_revisions` に対する `GET` 以外のメソッドは `405 method_not_allowed`。** `slug` の
検証が `_` を弾く以上「`_revisions` という名の文書」は存在しえないので、**実在する
サブ資源に対する未定義のメソッド**として扱うのが正しい。「文書が無い」（404）に
寄せると、書き込み先を間違えた呼び出し側が原因に辿り着けない。

## 10.2 `GET /api/v1/projects/:key/docs` — 目次

```json
{
  "items": [
    { "id": "01K2...", "path": "vision", "slug": "vision",
      "title": "価値観・世界観", "pack_mode": "full", "sort_order": 10, "version": 1,
      "updated_at": "2026-08-29T04:12:00Z", "children": [] },
    { "id": "01K2...", "path": "rules", "slug": "rules",
      "title": "規約", "pack_mode": "full", "sort_order": 20, "version": 3,
      "updated_at": "2026-08-29T05:00:00Z",
      "children": [
        { "id": "01K2...", "path": "rules/naming", "slug": "naming",
          "title": "命名", "pack_mode": "outline", "sort_order": 10, "version": 1,
          "updated_at": "2026-08-29T05:00:00Z", "children": [] }
      ] }
  ]
}
```

**`body_md` を含めない。** 目次は「どこに何があるか」を答えるものであり、本文は 10.3 が返す。
**全文を一度に返す設計にすると、リポジトリの md ファイルより劣る**（ファイルなら部分読みができる）。

`items[]` は各階層で **`sort_order` 昇順、同値は `slug` 昇順**。第2キーを置く理由は 9.11 と同じ。

**`version` を含める。** 本文（10.3）にもあるが、目次にも要る——**`GuiDesign.md` 5.10 の木の
ドラッグ&ドロップは、目次だけを持って複数行を `PATCH` する**（9.11.1 のタグと同じく、新しい
並びへ `10, 20, 30, …` を割り当てて値が変わった行だけ送る）。10.4 が `PATCH` に `If-Match` を
必須とする以上、**目次が `version` を返さないと、動かした行の数だけ `GET` が増える**。
目次は木の唯一の供給源であり、`updated_at` という鮮度の値を既に運んでいる器でもある。

**`sort_order` だけの更新を `If-Match` の対象外にする案は採らない。** 2.8 の「更新には
`If-Match`」という規約を資源ごとに割ると、どの `PATCH` にヘッダが要るかを呼び出し側が
覚えることになる。

**ページネーションを持たない**（`items` のみ）。9.11 のタグと同じく、**全件が同時に要る**
——目次は木であり、途中で切ると子が親から外れる。

### `?outline=1`

各文書の見出し一覧を足す。**エージェントが「どの章を読むか」を決めるために使う**。

```json
{ "id": "01K2...", "path": "rules", "title": "規約", "sort_order": 20, "version": 3,
  "updated_at": "2026-08-29T05:00:00Z",
  "outline": [
    { "section": "命名", "level": 2 },
    { "section": "ブランチ", "level": 2 },
    { "section": "接頭辞", "level": 3 }
  ],
  "children": [] }
```

**`section` は見出しテキストそのものである。** スラッグ化も番号付けもしない
（`DbDesign.md` 8.1.3）。**この値はどこにも保存されず、常に現在の本文から作られる**ため、
見出しを改名しても壊れる参照が生まれない。

**同じ文書に同名の見出しが2つあるときは、2つ目以降に `#2` を付ける**（`命名`、`命名#2`）。
`level` は Markdown の見出しレベル（`##` が 2）。

## 10.3 `GET /api/v1/projects/:key/docs/*path` — 本文

```json
{
  "id": "01K2...",
  "path": "rules",
  "slug": "rules",
  "parent_path": null,
  "title": "規約",
  "body_md": "本書はこのプロジェクトの規約である。\n\n## 命名\n…",
  "pack_mode": "full",
  "outline": [ { "section": "命名", "level": 2 } ],
  "sort_order": 20,
  "version": 3,
  "created_by": { "id": "01K2...", "kind": "user", "display_name": "田中" },
  "updated_by": { "id": "01K2...", "kind": "agent", "display_name": "claude-code" },
  "created_at": "2026-08-29T04:00:00Z",
  "updated_at": "2026-08-29T05:00:00Z"
}
```

**`created_by` / `updated_by` は `null` になりうる**（`ON DELETE SET NULL`。`DbDesign.md` 8.1.1）。
9.8 の `author` が `null` にならないのと異なる——コメントは書き手が消えたら意味を失うが、
**文書は書いた人が退職しても内容が生き続ける**。

`kind` が `agent` のとき、画面はアバターを角丸四角にする（`GuiDesign.md` 8.4.2）。
**憲章に「エージェントが最後に更新した」と出ることは正常である**——`pb_put_doc` は
権限を持つ人の指示で呼ばれる（`Requirements.md` 10.7.5）。

### `?section=<見出し>`

その章だけを返す。**`body_md` は見出し行から、同じか上のレベルの次の見出しの直前までを含む。**

```
GET /api/v1/projects/my-app/docs/rules?section=命名
```

```json
{ "id": "01K2...", "path": "rules", "title": "規約",
  "section": "命名", "body_md": "## 命名\n\n- テーブルは単数形…",
  "version": 3, "updated_at": "2026-08-29T05:00:00Z" }
```

**見つからないときは `404 not_found` を返し、本体に `available_sections` を添える。**

```json
{ "error": {
    "code": "not_found",
    "message": "指定された章が見つかりません",
    "available_sections": ["命名", "ブランチ", "接頭辞"],
    "request_id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S" } }
```

**`details` ではなく本体の任意フィールドに置く。** `details` は `{field, code, message}` の
配列で、入力欄に紐づかない配列を載せる場所がない——2.5 が `retry_after_sec` について
述べているのと同じ理由である。**呼び出し側（多くはエージェント）が、もう一度目次を
取りに行かずに次の一手を選べる。**

## 10.4 `POST` / `PATCH` / `DELETE`

`POST` の本体：

| フィールド | 検証 |
|---|---|
| `slug` | 必須。`^[a-z0-9][a-z0-9-]{0,63}$`。**同じ親の下で一意**（重複は `409 already_exists`） |
| `title` | 必須。1〜200文字。前後の空白を取り除いてから検証する |
| `parent_path` | 任意。省略・`null` でトップレベル。存在しないパスは `422`、`details[].code = "not_found"` |
| `body_md` | 任意。既定は空文字 |
| `pack_mode` | 任意。`full` / `outline` / `none`。既定は `outline` |
| `sort_order` | 任意。省略時は同じ親の中の末尾（現在の最大値 + 10） |

`PATCH` は `title` / `body_md` / `slug` / `parent_path` / `sort_order` / `pack_mode` を任意の組み合わせで受け、
**加えて `change_reason`（任意、200文字以内）を受ける**。

**`If-Match` を要求する**（2.8）。`document` は `version` 列を持つ。**人とエージェントが
同じ文書を触るため、プロジェクト設定より競合が起きやすい。** 不一致は `409 conflict`。

### リビジョンを作る条件

**`POST` は `revision_no = 1` を作る。** 作成時の `title` / `body_md` をそのまま置き、
`change_reason` は `null`。**リビジョンは「その変更のあとの本文」を持つ**ので（10.5 の
`change_reason` が、その版の中身を説明していることに対応する）、作成時の1件が無いと
**最初の編集で「作ったときの本文」がどの版にも残らず、戻せなくなる。**

`PATCH` は **`title` か `body_md` が実際に変わったときだけ `document_revision` に1行足す。**
`sort_order` の変更や、同じ本文の送り直しでは作らない。並べ替えのたびに履歴が伸びると、
「いつ内容が変わったか」が読めなくなる。**`version` はどの更新でも +1 する**（2.8 の規約を
1本に保つため。9.4 の `move` と同じ扱い）。

`change_reason` は `document_revision.change_reason` に入る。**リビジョンを作らない更新で
`change_reason` を送っても捨てる**（`422` にはしない）。

### 移動と改名

`parent_path` と `slug` の変更が移動・改名である。**部分木ごと移動する**（`path` は
子孫の分も付け替わる）。**自分自身または自分の子孫を `parent_path` に指定すると
`422 validation_failed`、`details[].code = "cycle"`。**

**`path` が変わると、既に共有された URL は切れる。** チケットの `seq` を不変にした
（5.2.1 / 9.1）のと違い、**文書のパスは変えられる**——文書は整理し直されるものであり、
名前を固定すると構造を直せなくなる。**壊れて困る参照を作らないために、保存する参照には
`id` を使う**（`DbDesign.md` 8.1.3）。

### 削除

`DELETE` は **`204 No Content`**。**物理削除で、部分木ごと消える**（`parent_id` の
`CASCADE`。`DbDesign.md` 4.6 / 8.1.1）。`document_revision` も一緒に消える。

**子を持つ文書の削除は、画面が件数を示して確認する**（`GuiDesign.md` 6.3）。API は止めない
——使用中のタグを消せるようにしたのと同じ判断で（9.11）、消せないと構造を直せなくなる。

## 10.5 `GET .../docs/*path/_revisions` — 履歴

```json
{
  "items": [
    { "revision_no": 3, "title": "規約",
      "changed_by": { "id": "01K2...", "kind": "agent", "display_name": "claude-code" },
      "change_reason": "ブランチ命名にチケット番号を入れる",
      "created_at": "2026-08-29T05:00:00Z" },
    { "revision_no": 2, "title": "規約",
      "changed_by": { "id": "01K2...", "kind": "user", "display_name": "田中" },
      "change_reason": null,
      "created_at": "2026-08-29T04:30:00Z" }
  ],
  "page": 1, "per_page": 20, "total": 3, "total_pages": 1
}
```

**一覧に `body_md` を含めない。** 9.13.2 の履歴が本文を `null` にしているのと同じ理由で、
20件ぶんの Markdown を載せると応答が重くなる。本文が要るときは
`GET .../\_revisions/:no` を呼ぶ。

```
GET /api/v1/projects/my-app/docs/rules/_revisions/2
```

```json
{
  "revision_no": 2,
  "title": "規約",
  "body_md": "本書はこのプロジェクトの規約である。\n\n## 命名\n…",
  "changed_by": { "id": "01K2...", "kind": "user", "display_name": "田中" },
  "change_reason": null,
  "created_at": "2026-08-29T04:30:00Z"
}
```

**一覧の1件に `body_md` を足した形である。** `document_revision`（`DbDesign.md` 8.1.1）の
列とそのまま対応する。**`version` も `outline` も持たない**——`version` は現在の文書の
楽観ロック値であって過去の版に属さず、`outline` は現在の本文から作るものである（10.2）。
無い `revision_no` を指すと `404 not_found`。

**2.6 のページネーションを持つ**（既定 `per_page=20`、上限 100）。`revision_no` の降順に固定。

**「前の版に戻す」は、取得した `title` と `body_md` を `PATCH` で書き戻して行う。**
専用のエンドポイントを置かない。**履歴は消さない**——書き戻しも新しいリビジョンとして積む
（`DbDesign.md` 8.3.1 の `knowledge_revision` と同じ扱い）。

**`title` も戻す**。10.4 がリビジョンを積む条件を
「**`title` か `body_md` が実際に変わったとき**」と定めている以上、**版の中身はこの2つで
1組**である。本節の一覧も1件ごとに `title` を返しており、**利用者は「その版のときの
タイトル」を見た上で戻す**——`body_md` だけを書き戻すと、一覧に出ている版の姿と
戻したあとの文書が食い違う。`slug` と `parent_path` は戻さない（リビジョンが持たない。
文書の場所は内容ではなく構造であり、10.4 の「移動と改名」が扱う）。

**11.2 の「チケット本文（`body_md`）の版管理」とは別件である。** あちらは `ticket.body_md` の
話で、`activity` が「いつ誰が変えたか」までしか持たないという積み残し（9.5.2）。**今回
版管理を入れたのは `document` だけで、チケット本文には依然として無い。** ただし
`document_revision` は「本文の版を別テーブルで持ち、一覧は本文を返さない」という形を先に
作ることになるので、**チケット側を作るときの下敷きになる。**

## 10.6 文書固有のエラーコード

| Status | code | 意味 |
|---|---|---|
| 404 | `not_found` | 文書が無い、`?section=` の章が無い（`available_sections` を伴う）、`_revisions/:no` の版が無い、閲覧権限が無い |
| 405 | `method_not_allowed` | `.../_revisions` を `GET` 以外で叩いた（10.1） |
| 409 | `already_exists` | 同じ親の下に同じ `slug` がある（作成・改名・移動のいずれでも） |
| 409 | `conflict` | `If-Match` 不一致 |
| 422 | `validation_failed` | `details[].code` に `not_found`（`parent_path`）、`cycle`（自分の子孫へ移動） |

# 11. アプリケーション設定API

**サーバ全体の設定を、アドミニストレータが確認・変更する**（`GuiDesign.md` 5.12、`Design.md` 10.3）。設定は3層に分かれており、**本章が扱うのは「何が実効値で、それがどこから来たか」である。**

**11.4 以降は TLS 証明書を扱う**。あれは第3層（`Design.md` 6.6.1）で、**値が秘密である点と有効期間で選ばれる点**が第2層と違うため、`app_setting` とは別の表・別のエンドポイントになっている。

**11.10 は DB の接続状態と統計を返す**。設定ではなく**状態**であり、変更の口を持たない。

**必要権限はいずれも `system.settings`。** この権限は `DbDesign.md` 7.2 のシードにある。

## 11.1 `GET /api/v1/admin/settings`

**必要権限**：`system.settings`

```json
{
  "items": [
    { "key": "log_level", "display_name": "ログレベル",
      "description": "構造化ログの最低レベル。debug では /healthcheck のアクセスログも出る",
      "layer": 2, "value": "info", "value_type": "enum",
      "allowed": ["debug", "info", "warn", "error"], "default_value": "info",
      "source": "default", "editable": true, "restart_required": false,
      "secret": false, "env_key": "PB_LOG_LEVEL",
      "updated_at": null, "updated_by": null },

    { "key": "cookie_secure", "display_name": "Cookie に Secure を付ける",
      "description": "HTTPS で公開する環境では有効にする。http で有効にするとログインできなくなる",
      "layer": 2, "value": "false", "value_type": "bool",
      "allowed": null, "default_value": "false",
      "source": "database", "editable": true, "restart_required": false,
      "secret": false, "env_key": "PB_COOKIE_SECURE",
      "updated_at": "2026-09-11T04:10:00Z",
      "updated_by": { "id": "01K2...", "display_name": "田中" } },

    { "key": "database_url", "display_name": "DB接続文字列",
      "description": "起動時に接続プールを張る。変更には再起動が要る",
      "layer": 1, "value": null, "value_type": "string",
      "allowed": null, "default_value": null,
      "source": "secret_file", "editable": false, "restart_required": true,
      "secret": true, "env_key": "PB_DATABASE_URL",
      "updated_at": null, "updated_by": null }
  ]
}
```

`items[]` は設定レジストリの並び順（層の昇順、層の中は定義順）。**ページネーションも `ETag` も持たない。** 件数は設定レジストリで固定されており、絞り込みも差分取得も意味を持たない（7.1 と同じ理由）。

| 項目 | 内容 |
|---|---|
| `key` | 設定キー。`app_setting.key` と同じ（`DbDesign.md` 6.14）。**`PB_` 接頭辞の無い小文字**である |
| `layer` | `1`（起動前）／ `2`（実行時の共有設定）／ `3`（共有される秘密）。`Design.md` 10.3 の層 |
| `value` | **実効値を文字列で返す。** `bool` も `"true"` / `"false"` の文字列である（下記）。**`secret` が `true` のものは常に `null`** |
| `value_type` | `string` / `bool` / `enum` |
| `allowed` | `value_type` が `enum` のときの値域。それ以外は `null`。**キーは常に返す**（省略しない） |
| `default_value` | 設定レジストリが持つ既定値。**必須の設定は `null`** |
| `source` | `secret_file` / `config_file` / `env` / `database` / `default`。**実効値がどこから来たか**（`Design.md` 10.3 の優先順と同じ並び） |
| `editable` | 画面から変更できるか。**`layer` が 2 で、かつ `source` が `database` か `default` のときだけ `true`** |
| `config_file_key` | `pb.yaml` に書くときのキー。`key` と同じ値を返す（画面が説明文を組み立てるために持つ） |
| `restart_required` | 変更が効くまでに再起動が要るか |
| `secret` | `true` なら `value` を返さない |
| `env_key` | この設定を環境変数で与えるときの名前（`PB_` 付き）。**`key` は `PB_` の無い平らな名前**で、`pb.yaml` と `app_setting` はそちらを使う |
| `updated_at` `updated_by` | `app_setting` の行があるときだけ埋まる。`source` が `database` 以外なら両方 `null` |

### `value` を型付きの JSON にせず、常に文字列で返す

**`value_type` と対で読む前提にする。** 真偽値を JSON の `true` にすると、`value` の型が設定ごとに変わり、**生成した型が共用体になる**（3種類の `value` を持つ配列要素になる）。画面は `value_type` を見て解釈すればよく、**入力欄も文字列で扱える。**

**`env_key` を返すのは、第1層を画面から変更できないからである。** 変更できない値について「ではどこで変えるのか」を画面が答えられないと、`editable: false` は行き止まりになる。**`source` が何であっても常に返す**——いま環境変数で与えられていない設定についても、環境変数で上書きする道を画面が示せるようにする。

### `editable` を `layer` と `source` から導く理由

**「編集できない」には2つの理由があり、利用者への説明が違う。**

| 状況 | `layer` | `source` | 画面に出す説明 |
|---|---|---|---|
| 構造的に画面で扱えない | `1` | 何でも | 「起動前に要る設定です。`pb.yaml` か `env_key` で与えてください」 |
| 設定ファイルで固定されている | `2` | `config_file` | 「`pb.yaml` の `log_format` で固定されています」 |
| 環境変数で固定されている | `2` | `env` | 「`PB_LOG_FORMAT` で固定されています」 |
| 秘密のファイルで与えられている | `2` | `secret_file` | 「`PB_LOG_FORMAT_FILE` が指すファイルで固定されています」 |
| 変更できる | `2` | `database` / `default` | （編集欄を出す） |

**`editable` だけを返すと、この2つが同じ見た目になる。** 後者は環境変数を外せば編集できるようになるが、前者はならない。

## 11.2 `PUT /api/v1/admin/settings`

**必要権限**：`system.settings`

```json
{ "items": [ { "key": "log_level", "value": "debug" },
             { "key": "health_show_version", "value": null } ] }
```

- 成功 → `200`。**本文は 11.1 と同じ形**を返す（変更後の実効値と `source` を画面が描き直せるようにする）
- 設定レジストリに無い `key` → `422 validation_failed`
- `value_type` / `allowed` に合わない `value` → `422 validation_failed`
- `editable` が `false` の `key` → `409 conflict`
- `items` が空 → `422 validation_failed`

**`value` を `null` にすると行を消し、既定値へ戻す。** 「既定に戻す」ための別のエンドポイントを作らない——**設定を消すことと既定へ戻すことは同じ状態**であり（`DbDesign.md` 6.14「行が無いことが既定値である」）、経路を2本持つと片方だけが監査に残る事故を生む。

**`PUT` は追加と変更を兼ねる（冪等）。** 6.9 の権限の `PUT` と同じ扱いである。**同じ本文を2回送っても結果は変わらない。**

**送られた `items` だけを変更する。** 本文に現れないキーは触らない——全件を送らせると、画面が古い値を握ったまま保存したときに**他人の変更を巻き戻す。**

### 1件ずつではなく配列で受ける

**設定画面の保存ボタンは1回である。** 1件ずつの `PUT` にすると、3件変えたときに3回の往復と3行の監査ログが出て、**途中で失敗したときに画面と DB がずれる。** 配列で受けて**1トランザクションで書き、監査ログも1行**にする（`detail` に変更したキーと新旧の値を入れる）。

**`detail` に秘密を入れない。** 第1層と第3層は `editable: false` なので本エンドポイントを通らず、**通るのは第2層だけ**である。第2層に秘密は無い（`DbDesign.md` 6.14）。

### 監査ログの action は `setting.update`

2.10 の列挙に加わる。**1回の保存＝1行**で、`detail` は次の形にする。

```json
{ "changes": [ { "key": "log_level", "from": "info", "to": "debug" },
               { "key": "health_show_version", "from": "true", "to": null } ] }
```

`from` は変更前の**実効値**、`to` は書いた値（`null` は既定へ戻したこと）。**変更が無かったキーは `changes` に入れない。**

### `pending_confirmation`

**確認を待っている変更があれば載せる**（無ければ `null`）。11.1 と 11.2 の両方に出る。

```json
{
  "items": [ … ],
  "config_file_path": null,
  "pending_confirmation": {
    "keys": ["tls_enabled"],
    "expires_at": "2026-09-12T12:05:00Z",
    "changed_by": { "id": "01K2...", "kind": "user", "display_name": "田中" }
  }
}
```

**画面は残り時間をここから出す**（`GuiDesign.md` 5.12）。`expires_at` を過ぎると元の値へ戻る。

**`changed_by` は文言を分けるために要る**。「あなたが変えました」と「田中 が変えました」では、**押す前に確かめることが違う。** 分からなければ `null`。

**引けなくても応答を落とさない。** 未確認の有無は設定一覧の付随情報であり、ここで `500` にすると**締め出しの手当てが設定画面自体を壊す**ことになる。

## 11.3 反映の範囲

| 層 | 反映 | 画面の表示 |
|---|---|---|
| 第2層 | **即時。** 次のリクエストから効く | `restart_required: false` |
| 第1層 | **再起動が要る** | `restart_required: true` |

**第2層は、リクエストごとに DB を引かない。** 設定はプロセス内に持ち、`PUT` が成功した時点で入れ替える。**複数のレプリカでは、他のレプリカが次に読み直すまで古い値が残る**——読み直しの間隔は実装側の既定とし、`Design.md` 10.3 に従って**設定項目にはしない**（設定の反映を設定で決めると、その設定自身の反映が説明できなくなる）。

## 11.4 `GET /api/v1/admin/tls/certificates`

**必要権限**：`system.settings`

登録済みの TLS 証明書を返す（`Design.md` 6.6.1、`DbDesign.md` 6.15）。

```json
{
  "items": [
    { "id": "01K2...", "common_name": "pb.example.com",
      "dns_names": ["pb.example.com", "www.pb.example.com"],
      "ip_addresses": ["127.0.0.1"],
      "not_before": "2026-09-01T00:00:00Z", "not_after": "2026-12-01T00:00:00Z",
      "serial_number": "0a1b2c3d", "fingerprint": "ab:cd:…",
      "is_self_signed": false, "status": "active", "decryptable": true,
      "uploaded_at": "2026-09-11T04:10:00Z",
      "uploaded_by": { "id": "01K2...", "kind": "user", "display_name": "田中" } }
  ],
  "tls_enabled": true,
  "listen_url": "https://0.0.0.0:8443",
  "listen_host": null,
  "listen_host_match": "unspecific",
  "secret_key_present": true,
  "secret_key_origin": "generated"
}
```

**`private_key` は返さない。** 暗号化して保持しており（`DbDesign.md` 6.15）、**この応答にも
他のどの応答にも現れない。** 2.5 の設計方針6「秘密は一度しか返さない」より強く、**一度も返さない。**

| 項目 | 内容 |
|---|---|
| `status` | `active`（**いま出している1枚**）／ `pending`（`not_before` が未来）／ `expired`（`not_after` を過ぎた）／ `superseded`（有効だが、より新しい有効なものがある） |
| `tls_enabled` | いま TLS で待ち受けているか。**設定 `tls_enabled` の実効値ではなく、実際の待受の状態である** |
| `listen_url` | **いま待ち受けているスキームとアドレス**（`https://0.0.0.0:8443`）。画面の先頭にそのまま出す（`GuiDesign.md` 5.12.1） |
| `decryptable` | **いまの鍵で秘密鍵を復号できるか**。偽なら**その証明書は出せない**——鍵の出どころが変わっている |
| `ip_addresses` | **IP の SAN**。**列には無く、`cert_pem` から採る**（下記）。画面は `dns_names` と並べて出す |
| `listen_host` | **接続に使うホスト名。** `listen_url` のホスト部だが、**`0.0.0.0` と `::` のときは `null`** である（下記） |
| `listen_host_match` | いま出す証明書が `listen_host` を覆っているか。`covered` ／ `uncovered` ／ `unspecific`（`listen_host` が `null`）／ `no_certificate`（いま出す1枚が無い） |
| `secret_key_present` | 鍵が使える状態か。**PB が無ければ作るので通常は真である**（`Design.md` 6.6.1） |
| `secret_key_origin` | `env`（`PB_SECRET_KEY` で与えられた）／ `generated`（PB が作って DB に保存した）。**鍵そのものは返さない。** 画面が代償を出すために要る——**生成した鍵は DB にあるので、`pg_dump` に鍵と暗号文の両方が入る** |

`items[]` は `not_before` の降順。**ページネーションも `ETag` も持たない**——証明書は数枚である。

### `listen_url` をサーバが組み立てる理由

**画面が `bind` の設定値から組み立てない。** 待受の変更には再起動が要るので（`Design.md` 10.3）、
**設定の値と実際の待受は再起動をまたぐとずれる。** 画面が設定値から組み立てると、
ずれている間ずっと嘘を表示することになる。**サーバは自分が何で待ち受けているかを
知っている唯一の者である。**

**スキームも同じ理由でサーバが決める。** `tls_enabled` の実効値ではなく、実際に
TLS で待ち受けているかを見る。

### `decryptable` は復号を試して決める

**`key_id` の突き合わせでは検出できない。**行の `key_id` は常に `v1` で、
**鍵の出どころ（`env` / `generated`）を記録していない。** `PB_SECRET_KEY` を別の値へ
差し替えても `key_id` は変わらないので、**「同じ識別子だが中身が違う」を判別できない。**

**行ごとに復号を試す。** 「実際に使えるか」を直接見るので代理指標にならず、**列を足さずに
済み、登録済みの行にもそのまま効く。**

**`status` とは別の軸である。** `status` は日付で決まり（6.6.1 の選定）、**復号の可否は
鍵で決まる。** 混ぜると、**`active` なのに出せない**という状態を表せない。

**行ごとに返すのは、混在しうるためである。** 鍵を変えたあとに登録した証明書は読める。
集計だけでは、**どれを消せばよいか**が分からない。

### `ip_addresses` を列に持たない

**`dns_names` は `DNS:` の SAN だけを持つ**（`DbDesign.md` 6.15）。IP の SAN は
**`cert_pem` を解析して返す**。

**列を足さないのは、登録済みの行にもそのまま効かせるためである。** 列にすると
**既に入っている証明書は登録し直すまで IP を持たない。**

**これは表示のためだけの値ではない。** 判定（`listen_host_match`）と**同じ出どころから
採る**ので、**画面に出る根拠と判定が食い違わない。** `SAN: localhost` と出しながら
`127.0.0.1 を覆っています` と言う状態を作らない。

### 証明書の名前との突き合わせもサーバが行う

**判定の結果だけを返し、画面が証明書の名前を照合しない。** 画面が `dns_names` と `listen_url` を
突き合わせる形では、IP アドレスを扱えない。
**`dns_names` は `DNS:` の SAN だけであり、`IP:` の SAN は別の欄にある**（`DbDesign.md` 6.15
は前者しか列に持たない）。**`https://127.0.0.1:8443` で繋ぐ構成が「覆っていない」と判定
できず、黙って通ってしまう。**

**サーバは `cert_pem` を持っているので、標準ライブラリの `VerifyHostname` に照合させられる。**
自分で書いた照合は、ワイルドカードと IP の規則を2度実装することになる。**列を足さずに
済み、登録済みの行もそのまま新しい判定に乗る。**

**`CN` へのフォールバックはしない。** `VerifyHostname` は SAN だけを見る（Go 1.15 以降）。
**現代のブラウザも同じである**ので、SAN の無い証明書は `uncovered` になる。これは
`Development.md` 14.2 の「`CN` だけでは最近のブラウザが受けない」と同じ判定である。

### `listen_host` が `null` になるとき

**`0.0.0.0` と `::` は待受の表記であって、接続先のホスト名ではない。**
**すべてのアドレスで待ち受ける、という意味しか持たない**ので、**何と突き合わせるべきかを
サーバは知らない。** ここで `0.0.0.0` を突き合わせると、**利用者は `0.0.0.0` を SAN に
入れた証明書を作ってしまう**——その証明書はどのクライアントからも一致しない。

**`null` を返し、画面は「アクセスに使うホスト名を自分で確かめてください」と出す**
（`GuiDesign.md` 5.12.1）。

### `status` をサーバが決める理由

**選定の規則は `Design.md` 6.6.1 の1か所にある。** 画面が `not_before` と `not_after` と
現在時刻から組み立てると、**「いまどれが出ているか」の判定がサーバと画面の2か所に分かれる。**
`active` が必ず1枚以下であることも、サーバが決めるから保証できる。

**`superseded` を `active` と分けるのは、消してよいものが分かるようにするため**である。
期限が切れていなくても、より新しいものが出ているなら消して差し支えない。

## 11.5 `POST /api/v1/admin/tls/certificates`

**必要権限**：`system.settings`

```json
{ "cert_pem": "-----BEGIN CERTIFICATE-----\n…", "key_pem": "-----BEGIN PRIVATE KEY-----\n…" }
```

- 成功 → `201`。本文は 11.4 の `items[]` の要素1件
- PEM として読めない／証明書と鍵が対応しない → `422 validation_failed`
- 同じ指紋の証明書が既にある → `409 conflict`
- 期限が切れている証明書 → `422 validation_failed`

**鍵が無いことを理由に断らない。** 証明書を1枚登録するために環境変数の設定と再起動を
要求する形は、「設定は WebGUI を第一の口とする」方針と矛盾する。**無ければ PB が作る**（`Design.md` 6.6.1）。

**証明書と鍵が対応することを登録時に確かめる**（`tls.X509KeyPair` と同じ検証）。
**ここで弾かないと、ハンドシェイクの時刻まで誤りが見つからない**——そのときにはもう
画面も API も TLS の向こう側にあり、直す手段が無い。

**期限切れを受けない。** 受けても `active` になれず、**利用者は「登録したのに効かない」
としか読めない。** 一方で `not_before` が未来のものは受ける——**新旧2枚を並べる**という
本チケットの目的そのものである。

**鍵の形式は PKCS#8 / PKCS#1 / SEC1 を受ける。** どれも `-----BEGIN … PRIVATE KEY-----`
で始まる PEM であり、**利用者が発行元から受け取った形をそのまま貼れるようにする。**

**暗号化されたままの秘密鍵（パスフレーズ付き）は受けない**（`422`）。パスフレーズを
どこに置くかという問いが増え、**第1層の鍵が2つになる。** 復号してから貼ってもらう。

### `multipart/form-data` ではなく JSON で受ける

PEM は**テキスト**であり、画面は貼り付け欄で受ける（`GuiDesign.md` 5.12.1）。
ファイル選択にすると、**鍵をファイルとして持っていない利用者**（発行元の画面から
コピーしただけ）が詰まる。2.2 の JSON 一本化からも外れない。

## 11.6 `DELETE /api/v1/admin/tls/certificates/:id`

**必要権限**：`system.settings`

- 成功 → `204`
- 存在しない → `404`
- **消すと有効な証明書が1枚も残らない、かつ TLS で待ち受けている** → `409 conflict`

**最後の有効な証明書を消させない。** 消せてしまうと、その瞬間から TLS ハンドシェイクが
失敗し（`Design.md` 6.6.1）、**画面から復旧できなくなる。** 平文へ戻したいなら、
先に `tls_enabled` を `false` にする。

**`expired` と `superseded` はいつでも消せる。** どちらも出していないので、消しても
振る舞いが変わらない。

### 監査ログ

`tls.certificate.upload` と `tls.certificate.delete` を 2.10 の列挙に加える。
**`detail` には指紋・`common_name`・有効期間を入れ、PEM と鍵は入れない。**

## 11.7 `GET /api/v1/admin/tls/certificates/:id/certificate.zip`

**必要権限**：`system.settings`

登録済みの証明書を zip に包んで返す。

- 成功 → `200`、`Content-Type: application/zip`
- 存在しない → `404`

```
Content-Disposition: attachment; filename="pb-cert-pb.example.com.zip"
```

**中身は証明書1枚だけである。** 手引きの類は入れない——**入れると、渡すべきものが
どれかを受け取った側が選ぶことになる。**

```
pb-cert-pb.example.com.zip
  └ pb.example.com.crt
```

**秘密鍵は返さない。** 返すのは `cert_pem` だけで、11.4 と同じく**鍵はどの応答にも現れない。**

### なぜ取り出す口が要るか

**自己署名証明書では、その証明書を持っていないクライアントが PB へ繋げない**
（`Development.md` 14.5）。証明書は PB の DB にあって画面から登録するが、**それを
クライアントへ渡すまでエージェントは PB へ繋げない**ので、**繋げない相手から取って
こなければならない**という循環になる。

**この口は循環を断つためにある。** HTTPS にする前に取っておける。

### なぜ zip で包むか

**`.crt` をそのまま返すと、ブラウザが保存させない。**Chrome は危険な形式に準じるものとして扱い、**「不審なファイル」として拒む。**

**PB 側は 200 を返しているので、画面にもログにも失敗が残らない。** 利用者から見えるのは
「押しても何も起きない」だけで、**壊れていることが誰にも分からない**見えない失敗である。
**循環を断つための口が、断てなくなる。**

**zip はブラウザが素通しする。** エージェントの手引きを同じ理由で zip にしている（5.7.2）。

### PEM のまま返す口は残さない

**同じ中身を返す口を2つ持たない。** 画面の導線は zip 一本になり、**PEM を直に欲しい
ときは接続先から取れる**（`Development.md` 14.5 の `openssl s_client`）。**残しても、
ブラウザからは落とせないままである。**

### なぜ一覧に含めず、別の口にしたか

**11.4 は画面を開くたびに引かれる。** `cert_pem` を含めると、**使いもしない PEM を
毎回全枚数ぶん運ぶ。** 取り出しは稀にしか起きない。

**`Content-Disposition` を付けられるのも理由である。** ブラウザの保存にそのまま乗り、
画面が Blob を組み立てなくて済む。

### ファイル名

**zip は `pb-cert-<common_name>.zip`、中の1件は `<common_name>.crt`。**
**`common_name` は任意の文字列なので、英数字・ハイフン・ドット・
アンダースコア以外は `_` に置き換える**——パスの区切りが入ると保存先がずれる。
**先頭と末尾の `_` と `.` は落とす**——先頭が `.` だと隠しファイルになる。
**置き換えた結果が空になったら `certificate.crt` / `pb-cert-certificate.zip` にする。**

### 監査ログを残さない

**証明書は接続してきた誰にでも提示されるもの**であり、**取り出せること自体は秘密の
漏洩にあたらない。** `system.settings` を要求するのは**口を無用に広げないため**であって、
秘匿のためではない。11.4 の一覧も同じ理由で残していない。

## 11.8 `POST /api/v1/admin/settings/confirm`

**必要権限**：`system.settings`

**締め出されうる設定の変更を確定する**（`Design.md` 10.3、`DbDesign.md` 6.17）。

- 成功 → `204`。**未確認の記録が消え、以後は元へ戻らない**
- 確認を待っている変更が無い → `404`
- **新しい設定を通って届いていない** → `409 conflict`
- 期限と競合して既に戻っていた → `409 conflict`

### 確認の条件はキーごとに違う

| 設定 | 条件 | なぜ |
|---|---|---|
| `tls_enabled` | **接続が設定どおりであること**（有効なら `r.TLS != nil`、無効なら `nil`） | **平文で届いた確認を受け取ると、切り替えが失敗していても確定してしまう** |
| `cookie_secure` | **この口に届いたこと自体で足りる** | 認証が要る口であり、**`cookie_secure` が有効なのに Cookie が届いているなら、ブラウザは HTTPS で繋いでいる** |
| `bind` | **実際の待受が設定値と一致していること** | 待受は1つなので届いた時点で新しい待受だが、**張り替えに失敗して古いままのことがある**（新しいポートが使用中だったなど）。そこへ届いた確認を受け取ると、**繋がらない設定を確定してしまう** |

**前段にプロキシを置く構成でも成立する。** `cookie_secure` の確認は PB に平文で届いても成立し、`tls_enabled` はプロキシ配下では使わない設定である。**仕組みを無効にする設定は要らない。**

### 1回の保存が1件である

**確認すれば全部確定し、期限が切れれば全部戻る**。**利用者から見て「さっきの変更」は1つ**であり、1つだけ確認できたときに残りをどうするかという問いを作らない。

**未確認が残っている間は、次の危険な変更を `409` で断る**（11.2）。重なると「どれを戻すか」が利用者にも追えなくなる。

### 再起動でも戻る

**起動時は期限を待たずに未確認を戻す**（`Design.md` 10.3 の ③''）。**再起動が
復旧手段になる**ようにするためで、そうしないと**その設定では起動に失敗する場合に
永遠に戻らない。**

### 期限は 300 秒で、設定にしない

**設定にすると、その設定自身を誤ったときに戻せない**（`Design.md` 10.3 の「反映の間隔を設定にしない」と同じ形）。

## 11.9 `GET /api/v1/admin/settings/pending`

**必要権限**：`system.settings`

**確認を待っている変更だけを返す**。無ければ `pending_confirmation` は `null`。

```json
{ "pending_confirmation": { "keys": ["tls_enabled"], "expires_at": "…", "changed_by": { … } } }
```

### 設定一覧と分けて軽い口にする

**画面はこれを定期的に引く。** どの画面にいても未確認を出すためで（`GuiDesign.md` 2.6）、**全設定の一覧をポーリングで運ぶのは無駄である。**

**間隔は30秒**。300秒の窓に対して十分で、他の管理者の変更に最悪30秒遅れて気づく。**設定にしない**（`Design.md` 10.3）。

### 一般利用者には見せない

**権限は設定一覧と同じ `system.settings`。** 一般利用者に「いま管理者が設定を変えている」を見せる必要はなく、**確認を押す権限も無い**（11.8）。画面は権限を持つ人だけがこの口を引く。

## 11.10 `GET /api/v1/admin/database`

**必要権限**：`system.settings`

**PB が繋いでいる DB の接続状態と統計を返す**（`GuiDesign.md` 5.12.2）。**読み取り専用で、
変更の口は持たない。** バックアップと復元は 11.11〜11.13 にある。

```json
{
  "fetched_at": "2026-09-17T05:03:12Z",
  "connection": { "host": "127.0.0.1", "port": 5432, "database": "pb", "user": "pb_app", "tls": false },
  "server": { "version": "17.10 (Debian 17.10-1.pgdg12+1)",
              "started_at": "2026-09-17T02:17:59Z", "max_connections": 50 },
  "migration_version": 38,
  "sessions": { "database": 7, "pb": 5 },
  "pool": { "total": 5, "acquired": 1, "idle": 4, "max": 10 },
  "size_bytes": 79712256,
  "tables": [
    { "name": "comment", "rows": 7, "size_bytes": 43778048 },
    { "name": "ticket", "rows": 15, "size_bytes": 24100864 }
  ]
}
```

| 項目 | 内容 |
|---|---|
| `connection.host` `port` | **PB に与えられた接続先**（`PB_DATABASE_URL` のホストとポート）。Unix ソケットならディレクトリのパスが入る |
| `connection.database` `user` | **実際に繋いでいる DB とロール**（`current_database()` / `current_user`） |
| `connection.tls` | **この要求が使った接続が TLS か。** 接続そのものを見て決める（下記） |
| `server.version` | `server_version` の値をそのまま返す |
| `server.max_connections` | **サーバ全体の上限である**（DB ごとではない） |
| `migration_version` | `goose_db_version` で適用済みの最大の番号 |
| `sessions.database` | `pg_stat_activity` のうち、**この DB に繋いでいるもの**の数 |
| `sessions.pb` | そのうち `application_name` が PB の値（既定 `pb`。`DbDesign.md` 3.5）のもの。**PB のプロセスが複数あれば全部を含む** |
| `pool` | **この要求を受けたプロセスの接続プール**（`total` / `acquired` / `idle` / `max`）。**問い合わせを始める前の値**で、この要求自身の接続を含まない |
| `size_bytes` | `pg_database_size(current_database())` |
| `tables[]` | `public` スキーマの表。`rows` は `count(*)` の**正確な数**、`size_bytes` は索引と TOAST を含む大きさ（`pg_total_relation_size`）。**大きさの降順、同じなら名前の昇順** |
| `tables[].rows` | **`pb_app` が `SELECT` できない表では `null`**（下記） |

**秘密は返さない。** パスワードは応答のどこにも入らない。接続文字列そのものは
11.1 の `database_url` で、これまでどおり `secret: true` として値を返さない。

**ページネーションも `ETag` も持たない**——表は数十である。

### 件数を推定値ではなく `count(*)` で数える

**推定値（`pg_stat_user_tables.n_live_tup`）は実数とずれる**。自動の VACUUM / ANALYZE が走るまで追いつかない。

**正確な数は、バックアップと復元の突き合わせに使う**。推定値では、戻したあとに
件数が合っているかを確かめられない。

**全表を1つの文で数える**（`UNION ALL`）。**1つの文は1つのスナップショットで読む**ので、
数えている途中に書き込みがあっても、表どうしの件数が同じ時点のものになる。
**代償は行数に比例する時間である**——`pb_app` の `statement_timeout`（15秒。`DbDesign.md` 3.5）を
超えれば 500 になる。

### `connection.tls` を接続そのものから決める

**`pg_stat_ssl` を引かず、PB 側の接続（ドライバが握っているソケット）を見る。** 問い合わせが
要らず、DB 側の view の見え方にも依らない。

**`pg_stat_ssl` でも自分の接続の行は読める**（他のセッションの行は返らない）。
**ただし `SET ROLE pb_app` で確かめると、自分の行も返らない**——行を見せるかの判定が
接続したロールで行われるためである。
**実行時のロールで何が見えるかは、そのロールで接続して確かめる。**

### 読めない表を落とさず `null` で返す

**`pb_app` は既定の権限で `public` の全表を読める**（`DbDesign.md` 3.4 の `ALTER DEFAULT PRIVILEGES`）。
それでも権限の外に表ができたとき、**`count(*)` が失敗して応答全体が 500 になる**のを避け、
**一覧から黙って落とすこともしない。** 落とすと、表があることに誰も気づかない。

### 他のセッションの状態（active / idle）を返さない

**`pb_app` からは見えない。** `pg_stat_activity` は、見る側のロールが権限を持たない
セッションの `state` と `query` を隠す（`pb_owner` の
セッションは `state` が `NULL`、`query` が `<insufficient privilege>` になる。`pb_app` 自身のセッションは見える）。見せるには
`pg_read_all_stats` を `pb_app` に与える必要があり、**実行時のロールに他のセッションの
問い合わせ文を読ませることになる。**

---

## 11.11 `GET /api/v1/admin/backup.tar.gz`

**必要権限**：`system.settings`

**PB 全体を1つの書庫に書き出して返す**（形式は `DbDesign.md` 9.1.1、画面は `GuiDesign.md` 5.12.2）。

- 成功 → `200`、`Content-Type: application/gzip`
- 保守モード中 → `503` / `maintenance`（11.13）

```
Content-Disposition: attachment; filename="pb-backup-20260918-150405.tar.gz"
```

**ファイル名の時刻は UTC である。** 画面の日時は利用者のタイムゾーンで出すが（`GuiDesign.md` 7.5）、
**ファイル名は端末をまたいで並ぶ**ので、並べたときに時系列になる UTC で固定する。

### 全表を1つのトランザクションで読む

**`REPEATABLE READ` の1トランザクションで全表を読む。** 表ごとに別のトランザクションで読むと、
**読んでいる途中の書き込みが表のあいだで食い違い**、戻したときに外部キーが通らない書庫ができる。

**書庫は取り込みのときに制約を効かせたまま入れる**（`DbDesign.md` 9.1.1）ので、
**整合していない書庫は取り込みで初めて失敗する。** 書き出す側で揃えておく。

### `statement_timeout` を外す

**`pb_app` の既定は 15 秒である**（`DbDesign.md` 3.5）。書き出しは**行数に比例して長くなる**ので、
このトランザクションのあいだだけ `SET LOCAL statement_timeout = 0` で外す。

**`SET LOCAL` にする。** 接続はプールに戻るので、**外した設定を他の要求へ持ち越さない。**

### 保守モードには入らない

**書き出しは読むだけである。** 1つのスナップショットで読むので、**書き込みを止める必要が無い。**
**止めるのは取り込みのときだけ**（11.12）。

### 大きさの上限を持たない

**流しながら書く**ので、応答全体をメモリに載せない。**`Content-Length` は返せない**
（gzip したあとの大きさが書き終わるまで分からない）——ブラウザは進捗を出さずに落とし続ける。

### この2つの口だけ、応答と受信の期限を外す

**サーバ全体の期限では足りない**（`ReadTimeout` 30秒 / `WriteTimeout` 60秒）。
**行が増えれば、書き出しも取り込みもこれを超える。** 超えると**接続が途中で切れ、
壊れた書庫が落ちてくる**——しかもブラウザからは、途中まで落ちたファイルと区別が付かない。

**この2つの口では、その要求のあいだだけ期限を外す**（`http.ResponseController`）。
**サーバ全体の既定は変えない**——他の口が長く占有できるようにする理由が無い。

**代わりの歯止めは保守モードである**（11.13）。取り込みのあいだは他の要求が入らないので、
期限を外しても**占有しているのは取り込みだけ**である。書き出しは読むだけで、
DB 側は `statement_timeout` を外した1つのトランザクションに閉じている。

### 監査ログ（2.10）

| action | 対象 |
|---|---|
| `database.backup` | `resource_type = "database"` |

**秘密は入らないが、持ち出した事実は残す。** 書庫には `app_secret` の鍵と暗号文の両方が入る
（`DbDesign.md` 9.1.1）ので、**証明書の取り出し（11.7）とは扱いが違う。**

## 11.12 `POST /api/v1/admin/restore`

**必要権限**：`system.settings`

**書庫を取り込み、PB 全体をその時点へ戻す**。**段取りは `DbDesign.md` 9.1.1 にある**——
表と、マイグレーションが作った他のもの（関数など）を落とし、書庫の版までマイグレーションし、**マイグレーションが入れた行を `TRUNCATE` で払い**、
**外部キーを遅延させた1つのトランザクションで**書庫の行を入れ、数えてから最新の版まで進める。

### 要求

**`multipart/form-data` で受ける。** 書庫はテキストではなく、**流しながら読む**必要がある
（K8s の Pod は `readOnlyRootFilesystem: true` で一時ファイルを書けない。`DbDesign.md` 9.1.1）。
**11.5 が JSON なのは PEM がテキストだから**であり、ここは事情が違う。

| パート | 内容 |
|---|---|
| `owner_user` | `pb_owner` にあたるロール名（既定 `pb_owner`） |
| `owner_password` | そのロールのパスワード |
| `archive` | 書庫（`application/gzip`） |

**この順で送る。** サーバはパートを**先頭から順に**読むので、**`archive` が最後でないと
資格情報を読む前に書庫が流れ込む。** 画面はこの順で組み立てる。

**資格情報を保存しない。** 取り込みのあいだだけ `pb_owner` の接続を張り、終わったら捨てる
（`DbDesign.md` 3.4）。**応答にも構造化ログにも監査ログにも、ロール名だけを残しパスワードは残さない。**

**権限は資格情報を読む前に確かめる。** `system.settings` を持たない要求は `403` で拒む——
**持っていない人に入力させない**。

### 応答

```json
{
  "restored_at": "2026-09-18T15:04:05Z",
  "backup": {
    "format_version": 1,
    "migration_version": 36,
    "created_at": "2026-09-15T02:00:00Z",
    "pb_version": "2.43.140"
  },
  "migration_version": 38,
  "session_kept": true,
  "tables": [
    { "name": "ticket",       "expected": 15, "rows": 15 },
    { "name": "comment",      "expected": 7,  "rows": 7 },
    { "name": "access_token", "expected": 3,  "rows": 3 }
  ],
  "mismatched": []
}
```

| 項目 | 内容 |
|---|---|
| `backup` | **書庫の `meta.json` をそのまま返す。** 何を戻したのかを画面が言える |
| `migration_version` | **取り込みが終わったあとの版。** `backup.migration_version` より進んでいることがある（`DbDesign.md` 9.1.1 の⑧） |
| `session_kept` | **操作者のセッションを維持できたか**（下記） |
| `tables[].expected` | `meta.json` の件数 |
| `tables[].rows` | **行を入れた直後に数えた `count(*)`**（`DbDesign.md` 9.1.1 の⑦） |
| `mismatched` | `expected` と `rows` が食い違った表の名前。**空なら全表が一致した** |

**`expected` と `rows` を両方返す。** 片方だけでは、**戻せたのか、戻したつもりなのかが分からない。**
画面は `mismatched` が空でないときに警告を出す（`GuiDesign.md` 5.12.2）。

**数えるのは、最新の版へ進める前である**（`DbDesign.md` 9.1.1 の⑦）。**突き合わせが答えるのは**
「書庫を忠実に入れられたか」であって、いまの件数ではない。**あとから走るマイグレーションが
行を足す表は、数えるのを遅らせると `meta.json` より多くなり、正しく入ったものまで食い違いとして出る。**

**操作者のセッション1行も、数えたあとに足す**（下記）。**`rows` には現れない。**

**いまの件数は 11.10 で見る。** `rows` と一致しないことがあるのは、**そのあとのマイグレーションと
セッション1行の分**である。

### 操作者のセッションだけは維持する

**復元すると `access_token` ごと入れ替わるので、全員がログアウトする。** 操作した本人まで
締め出されると、**戻ったかどうかを確かめに行けない**。

**取り込みの前にこの要求のセッション行を控え、終わったあとに入れ直す。** `token_hash` は
SHA-256 であり `app_secret` に依存しない（`DbDesign.md` 6.2）ので、**そのまま書き戻せば
同じクッキーで続けられる。**

**`session_kept` が答えるのは「このあとも同じクッキーで続けられるか」である。** 真になるのは2つ。

- **書庫に自分のセッション行が入っていた**——自分で取った書庫を戻したときの、いちばん普通の場合。
  **入れ直す必要が無かっただけ**で、行は在る
- **書庫に無かったが、入れ直せた**——復元後のデータに本人の `actor` 行が在った

**偽になるのは、参照先の `actor` 行が無いときだけである**（別の PB の書庫を入れたときなど）。
外部キーが通らないので入れ直せない。**そのときだけ、画面はログイン画面へ送る。**

**「入れ直せたか」で答えない。** 書庫に同じ行があると入れ直しは一意制約で弾かれるが、
**セッションは生きている**——これを偽と答えると、画面が不要にログイン画面へ飛ばす。

**権限は復元後のデータで決まる。** 戻したデータで `system.settings` を持たなければ、
**繋がったまま権限だけ失う。** これは隠さず、そのまま起こしてよい——**戻したデータが
正しい姿**である。

### 失敗したとき

| 状況 | 応答 |
|---|---|
| 権限が無い | `403` / `forbidden`（**資格情報を読む前に**） |
| 書庫が壊れている・`format_version` が読めない | `422` / `validation_failed` |
| `owner_password` が違う | `422` / `validation_failed`（`details[].field = "owner_password"`） |
| 書庫の版がいまの PB より新しい | `409` / `backup_too_new` |
| 既に別の取り込みが走っている | `503` / `maintenance` |
| 取り込みの途中で落ちた | `500` / `internal_error` |

**`422` までは DB を触っていない。** 書庫の検査と資格情報の確認は、**表を落とす前に済ませる。**

**`500` のとき、DB は中途半端なまま残る。** 表が落ちたあと、行が入る前で止まりうる。
**`message` にそう書き、同じ書庫でもう一度取り込むよう案内する**——段取りは表を落とすところから
始まるので（`DbDesign.md` 9.1.1 の③）、**やり直せば回復する。**

### 監査ログ（2.10）

| action | 対象 |
|---|---|
| `database.restore` | `resource_type = "database"` |

**取り込みが終わったあと、別のトランザクションで書く。** `audit_log` 自身が入れ替わるので、
**同じトランザクションで書くと、書いた行ごと消える。**

**`detail` に書庫の `created_at` と `migration_version`、`mismatched` の有無を残す。**
**資格情報は残さない**（ロール名だけ）。

**失敗したときも残す。** 表を落としたあとで落ちた場合、**この記録だけが何が起きたかを伝える。**

## 11.13 保守モード中の応答

**取り込み（11.12）のあいだ、PB は要求を受け付けない**（`Design.md` 10.4）。

| 宛先 | 応答 |
|---|---|
| `/api` と `/mcp` | `503` と 2.5 のエラー形式。`code` は `maintenance` |
| それ以外のパス | `503` と、**単一の静的な HTML**。SPA（`index.html`）を返さない |
| `/healthcheck` | **対象外。`200` のまま**（2.11） |
| `POST /api/v1/admin/restore` | **対象外**（自分を止めることになる） |

```json
{
  "error": {
    "code": "maintenance",
    "message": "バックアップの取り込み中です。終わるまでお待ちください",
    "request_id": "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
  }
}
```

**`Retry-After` を返さない。** かかる時間は書庫の大きさで決まり、**PB は見積もれない。**
`account_locked` や `rate_limited`（2.5）とは違い、**返せる数字が無い。**

**ステータスは人向けの画面でも `503` にする。** ブラウザはステータスに関わらず本文を描くので
画面は成立し、**`200` にするとエージェントと監視が成功と読む。**


# 12. 未解決の検討事項

## 12.1 実装順序 → `Design.md` 11章

**実装順序は本書に持たない。** `Design.md` 11章の手順一覧が正本である。同じ順序を2か所に持つと片方だけ古くなる。

## 12.2 未解決の検討事項

- **`GET /me` のキャッシュ戦略**。ロール変更が他セッションへ反映されるまでの許容遅延をどう決めるか（毎リクエスト検証はコスト、長期キャッシュは権限剥奪が効かない）
- 一覧APIの `total` を返し続けるコストが問題になる規模の見極め（チケットが増えたときに再検討）
- **`POST /tickets/:seq/move` が `version` を +1 することの是非**（9.4）。並べ替えの直後に詳細画面の `PATCH` が `409` を返す。2.8 の規約を1本に保つことを優先したが、ドラッグ&ドロップの頻度によっては 2.8 ごと「順序の変更は `version` を動かさない」へ見直す
- **バックログの 200 件上限に達したときのフィルタ誘導が実運用で足りるか**（9.2.3）。足りなければ、スプリント・タグによるビューの分割か、`sort_key` に沿った範囲取得を検討する
- **チケット本文（`body_md`）の版管理**（9.5.2）。`activity` は「いつ誰が本文を変えたか」までを記録し、本文そのものは持たない（一覧APIの応答が重くなるため）。「前の説明に戻したい」が要件になったら、`activity` の拡張ではなく別の仕組みとして設計する
- **プロジェクトを跨ぐチケットリンク**（9.10.1）。`target_seq` は同一プロジェクトに閉じている。跨ぐ必要が出たときの指定方法（`{project_key, seq}` か ULID か）
- **`ticket.custom_fields` を API でどう開けるか**（`DbDesign.md` 6.6）。列はあるが応答に含めていない。カスタムフィールドの定義（どのキーが存在するか）をプロジェクト設定に持たせるかどうかから決める必要がある
- **タグの並べ替えに原子性が要るか**（9.11.1）。いまは `PATCH /tags/:id` を複数回送る形で、途中で失敗すると順序が中途半端に残る。必要になったら ID の配列を受ける一括更新を足す
- **タグ・スプリントの定義変更を `activity` に残すか**（9.1.1）。読む画面が無いため記録しない。タグ削除の追跡が運用上必要になった時点で `entity_type` を足す
- **スプリントを「終える」経路が2つある**（9.12.2）。`PATCH` で `status` を `completed` にしても所属も段も動かない。**同じ結果を2通りで作れる状態は望ましくない**が、`PATCH` から `status` を落とすと 5.9.5 のスプリントタブが状態を直せなくなる。運用が固まったらどちらかへ寄せる
- **進行中のスプリントを1本に限る制約が実運用で足りるか**（9.12.1）。複数チームが並行して回す場合はプロジェクトを分ける前提だが、**分けると横断のボードが無くなる**。要求が出たら、オンステージ段を複数持つ形から設計し直す
- エラーメッセージの多言語化。いまは日本語固定とするが、`code` を機械可読にしてあるためフロント側での差し替えは可能
- **`project.settings`（jsonb）の中身をサーバは検証しない**（5.5。JSONオブジェクトであることのみ）。`repositories` の必須・上限（10件／URL 1000文字／説明 200文字）は画面だけが持つ。**設定項目が増えるなら、サーバ側の検証をどこに置くかを決める必要がある**
- **`GET /admin/users/:id` は `kind='user'` しか返さない**（6.3。エージェント・システムアクターは 404）。**0019 で実データが入ったので、この行の条件は満たされた**——一覧（6.1）はエージェントを返すのに、行から詳細へ飛べない。**詳細への導線を種別で分けるか、エージェントを 6.3 が返せるようにするかを決める**（`GuiDesign.md` 5.6 のエージェントタブを作る手順で）
- **一覧の検索（`q`）はロールの表示名に当たるが、「エージェント」には当たらない**（6.1）。この文字列は `system_role` が `null` のときに画面が作っている代替表示でDBに無い。**0019 以降は `agent.client_kind` / `model_name` / 所有者の表示名が実在する**ので、種別で探せる形をそれらから選ぶ
- **`project_memberships[]` にプロジェクトの `status` を入れていない**（6.3）。アーカイブ済みのプロジェクトも含まれるが、画面がそれを区別できない。必要になったら 6.3 の改訂を先に出す
- **2.5.1 の11コードの既定文言が実装側（`apierr.messages`）にしかない。** 文書に持たせるなら 2.5.1 に message 列を足す
- 削除操作の監査における個人情報の保持期間（`audit_log.detail` と `actor_label` に残した表示名・メールをいつ消すか）
