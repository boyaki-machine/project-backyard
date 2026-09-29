# Project Backyard (PB) 設計書

> 本書は `Requirements.md`（要件・構想）を受けた**全体設計書**である。
> システム全体の構造と、領域別設計書への振り分けを担う。
>
> - 対象読者：実装者（人間およびAIエージェント）
> - 状態：確定（本文で「未実装」「構想」と書いたものを除き、実装済み）。第6章（認証・認可）を本書の正本として保持し、DB・API・画面は領域別設計書へ委譲

## 文書体系

```
Requirements.md   要件・構想（何を作るか・なぜ作るか）
      │
      ▼
Design.md         全体設計（本書）── システム構成・技術選定・認証認可・開発の進め方
      │
      ├── DbDesign.md    データベース設計   ← スキーマ・マイグレーション・DB実行環境の正本
      ├── ApiDesign.md   REST API 設計     ← エンドポイント仕様の正本
      └── GuiDesign.md   GUI 設計          ← 画面・遷移・配色の正本
```

| 領域 | 正本 | 本書での扱い |
|---|---|---|
| 要件・AI駆動開発の構想 | `Requirements.md` | 参照元 |
| システム構成・技術選定 | **本書 2〜4章** | — |
| データベース | `DbDesign.md` | 5章で概要のみ |
| 認証・認可 | **本書 6章** | — |
| REST API | `ApiDesign.md` | 7章で概要のみ |
| MCPサーバ | 本書 8章（未着手） | — |
| 画面・UI | `GuiDesign.md` | 9章で概要のみ |
| 開発の進め方 | **本書 11章** | — |

**記述が食い違った場合は、各領域の正本を優先する。**

---

## 目次

| 章 | 内容 | 状態 |
|---|---|---|
| 1 | 本書の位置づけと設計原則 | 記述済 |
| 2 | システム構成 | 記述済 |
| 3 | 技術スタックと実行環境 | 記述済 |
| 4 | リポジトリ・ディレクトリ構成 | 記述済 |
| 5 | データベース設計 | → `DbDesign.md` |
| **6** | **認証・認可設計** | **記述済（本書が正本）** |
| 7 | REST API 設計 | → `ApiDesign.md` |
| 8 | MCPサーバ設計 | 記述済 |
| 9 | 画面設計 | → `GuiDesign.md` |
| 10 | 非機能・運用設計 | ログ・ヘルスチェック・**設定管理**・**保守モード**は確定。メトリクス等は未着手 |
| 11 | 開発の進め方と実装順序 | 記述済 |

---

# 1. 本書の位置づけと設計原則

## 1.1 設計原則

| # | 原則 | 具体的な帰結 |
|---|---|---|
| 1 | **段階的に育てられる構造** | 一度作ったテーブルを後から作り直さない。拡張は列追加とテーブル追加のみで行う |
| 2 | **スキーマを設計資産として扱う** | DDL は人が読んで判断できる形で管理する。ORM の自動マイグレーション生成に委ねない（`DbDesign.md` 5.1） |
| 3 | **人間とエージェントを同じ型で扱う** | 担当者・作成者・承認者はすべて `actor` を参照する。エージェント追加時に既存テーブルを変更しない |
| 4 | **認証方式を差し替え可能にする** | ローカルID/PW・OIDC・SAML を同一の `user_identity` 抽象の下に置く（6.2） |
| 5 | **権限はデータで定義する** | 画面・機能の可否をコードに埋め込まず、permission カタログとして DB に持つ（6.4） |
| 6 | **AIは提案し、確定は人間が行う** | エージェントの出力は `proposal` テーブルを必ず経由し、本体テーブルを直接更新しない |
| 7 | **秘密をモデルのコンテキストに入れない** | エージェント向けの経路は MCP に一本化し、資格情報を含むコマンドを組み立てさせない（`Requirements.md` 10.10.1） |
| 8 | **単独開発で足りることを PB に作らない** | 開発者が1人なら、リポジトリの md ファイルとエージェント側の工夫で足りる。**機能を足す前に「md ファイル1枚では足りない理由は何か」を問う**（`Requirements.md` 10.0） |

**原則8 だけは「作らない理由」を与える。** 原則1〜7 はいずれも「どう作るか」を定めるものである。PB が価値を持つのは参加者が複数になってからであり（`Requirements.md` 10.0）、単独開発で足りる範囲まで作り込むと、原則1（段階的に育てられる構造）で足したものが誰にも使われないまま残る。

## 1.2 本書と要件定義の対応

| 本書 | `Requirements.md` の該当箇所 |
|---|---|
| 1.1 設計原則（原則8） | 10.0（PB が解く問題） |
| 2〜4. 構成・技術選定 | 1章（コンセプト・技術スタック）、8章（軽量性維持） |
| 6. 認証・認可設計 | 10.10（セキュリティとガードレール） |
| 8. MCPサーバ設計 | 10.3（MCPサーバ仕様）、10.4（コンテキストパック） |
| 11. 開発の進め方 | 10.12（MVPスコープ） |

---

# 2. システム構成

## 2.1 コンポーネント

```
┌─ ローカル端末（開発マシン） ─────────────────────────────┐
│                                                          │
│  ブラウザ ──HTTP──▶ ┌──────────────────────────────┐      │
│                     │  PB Server (コンテナ)          │      │
│  Claude Code ──MCP─▶│  ├ 静的配信 (Vue SPA)         │      │
│  VS Code    ──MCP─▶ │  ├ REST API  /api/v1/*        │      │
│                     │  ├ MCP       /mcp/<projectKey>│      │
│                     │  └ 認証・認可ミドルウェア        │      │
│                     └───────────┬──────────────────┘      │
│                                 │                          │
│                     ┌───────────▼──────────────────┐      │
│                     │  PostgreSQL 17 (コンテナ)      │      │
│                     │  + pgcrypto/citext/pg_trgm    │      │
│                     └──────────────────────────────┘      │
│                                                            │
│  docker compose もしくは Kubernetes 上で動作               │
└──────────────────────────────────────────────────────────┘
```

構成の詳細（compose定義、DBロール、K8sマニフェスト）は `DbDesign.md` 3章に記載する。

## 2.2 到達点

いま以下が成立している。

- `docker compose up` で PB と PostgreSQL が起動し、マイグレーションが適用される
- ローカルID/PW（第2要素・パスキーを含む）でログインでき、セッションが維持される
- オペレータ／アドミニストレータで見える画面・使える機能が変わる
- プロジェクトとチケットを作成・編集・一覧表示・検索できる
- ユーザーの追加・権限設定が管理画面から行える
- プロジェクト文書（憲章）を画面と MCP で読み書きできる
- エージェントを登録し、MCP でチケットを読み・起票し・状態を進め・完了レポートを出せる
- アプリケーション設定・TLS 証明書・バックアップを画面から扱える

カンバンは未実装、ガントは閲覧だけを実装済み（編集は未実装）、AI機能・知識還流・OIDC/SAML は構想である（テーブル・列のみ先行定義したものは `DbDesign.md` 6章・8章を参照）。

---

# 3. 技術スタックと実行環境

## 3.1 採用技術

| 層 | 採用 | 備考 |
|---|---|---|
| **サーバ言語** | **Go 1.26 以上** | `Requirements.md` 1章の候補（Rust / Go）から確定。ビルドの速さ、単一バイナリ配布、学習コストの低さを優先。**1.26 はパスキーの検証に使う go-webauthn の要求である**（6.8.5） |
| HTTPルータ | **chi v5** | `net/http` 互換。ミドルウェア連鎖とルートグループのみを足す薄い層 |
| DBドライバ | **pgx v5**（`database/sql` を経由しない） | `timestamptz` `jsonb` `inet` をネイティブに扱えるため。**非経由なのはアプリのデータ経路である**——マイグレーションの実行だけは例外（下の goose） |
| クエリ | **sqlc**（pgx/v5 モード） | SQLを書くとGoの型付き関数が生成される。設計原則2と一致 |
| マイグレーション | **goose v3** | SQLファイルベース。アドバイザリロック対応（`DbDesign.md` 5.3）。**サーバ本体の依存でもある**（書庫の取り込みが版を動かすため。`DbDesign.md` 9.1.1）。**goose の API は `*sql.DB` しか取らない**ので、`database/sql` と `pgx/v5/stdlib` が**マイグレーションを走らせる箇所だけ**に入る |
| パスワード | `golang.org/x/crypto/argon2` | PHC文字列の入出力は `alexedwards/argon2id` を利用 |
| 対話入力 | `golang.org/x/term` | `pb admin create` のパスワードを非表示で読む（`DbDesign.md` 7.5） |
| YAML | `gopkg.in/yaml.v3` | **設定ファイル（`PB_CONFIG_FILE` が指す `pb.yaml`。10.3）と `pb dev seed` の定義ファイル**（`DbDesign.md` 7.6.4）。実行時の依存である |
| パスキー | `go-webauthn/webauthn` v0.18.1 | WebAuthn の登録とログインの検証（6.8）。**CBOR の解析と署名の照合を自前で持たない**——攻撃者が形を決められるバイナリの解析器であるため（6.8.5）。attestation の検証と MDS は使わない |
| ID生成 | `oklog/ulid/v2` | ULID（`DbDesign.md` 4.2） |
| ログ | **`log/slog`**（標準ライブラリ、JSONハンドラ） | 外部ライブラリを増やさない |
| 設定 | YAML の設定ファイル＋環境変数＋`*_FILE` 展開（自前）と DB の `app_setting` | **3層に分け、優先順は5段。10.3 が正本**（`DbDesign.md` 3.2 / 6.14） |
| 入力検証 | `go-playground/validator` v10 | `ApiDesign.md` 2.5 のエラー形式へ変換する層を挟む |
| テスト | 標準 `testing` ＋ compose のDBに対する統合テスト | testcontainers は導入しない（起動が重く原則と衝突） |
| DB | **PostgreSQL 17** | `DbDesign.md` 2章。SQLite 先行案は廃止 |
| DB拡張 | `pgcrypto` / `citext` / `pg_trgm`、`vector`（構想） | `DbDesign.md` 3.1 |
| フロント | Vue 3 + TypeScript + Vite | SPA。サーバから静的配信 |
| 国際化 | **Vue I18n v11**（Composition API） | `client/src/locales/<locale>/` に言語別メッセージを置く。利用者の `app_user.locale` を正とし、未認証時と未対応値は `ja` へフォールバックする |
| UIコンポーネント | 未確定（自前の軽量実装で開始） | `GuiDesign.md` 1.1 |
| Markdownエディタ | **CodeMirror 6**（`codemirror` ＋ `@codemirror/lang-markdown`） | `GuiDesign.md` 5.5 の説明欄。`Requirements.md` 7章が「フルスクラッチのリッチエディタは避ける」と定める |
| Markdown描画 | `markdown-it` ＋ `dompurify` | 同じ欄のライブプレビュー側 |
| 実行形態 | docker compose（既定）／ Kubernetes | `DbDesign.md` 3.2〜3.3 |

**Markdown の2つは説明欄のために入れる**（依存の一覧は `Development.md` 10.4）。**チケットの説明欄が、PB で最も打鍵回数の多い入力欄になる**——人とエージェントが読み書きする一次資料であり（`Requirements.md` 10章）、素の `textarea` との差が毎日効く。

**`dompurify` を併せて入れるのは、本文がエージェントからも入るためである。** `markdown-it` は既定で安全側に倒れている（`html: false` と、`javascript:` 等を弾くリンク検証）が、**書き手が人だけではない**以上、描画の直前にもう一段通す。**MCP 経由の入力を信用しない**方針は `Requirements.md` 10.10.6 と同じ根である。

## 3.2 sqlc と goose を組み合わせる理由

**sqlc は `migrations/` のDDLを読んでスキーマを推論する。** したがって、

- `DbDesign.md` のDDLをマイグレーションに落とせば、**Goの構造体と型が自動的にスキーマと一致する**
- 手書きしたSQLは `sqlc generate` の時点で検証され、列名の誤りやスキーマとのずれがコンパイル前に露見する
- ORM を使わないため、実行されるSQLが常に目に見える（設計原則2）

**マイグレーションが唯一のスキーマ定義**になり、モデル定義とDDLの二重管理が発生しない。この点が Go を選んだ場合の最大の利点になる。

## 3.3 OpenAPI の扱い

`docs/design/openapi.yaml` は**生成器を通さずに保守する**。コード生成は**TypeScriptクライアントのみ**（`openapi-typescript`）に限定する。

| 採らない方式 | 理由 |
|---|---|
| yaml → サーバコード（`oapi-codegen` / `ogen` 等） | 生成物の制約に設計が引きずられる。ハンドラのシグネチャとエラーの返し方が生成器の流儀に固定され、`ApiDesign.md` 2.5 の独自エラー形式を全応答に通す作りと相性が悪い |
| 注釈 → yaml（`swaggo/swag` / `go-swagger` 等） | 注釈という別の記述を書くことになり、yaml を直接書くのと本質的な差が小さい。出力が Swagger 2.0 中心で OAS3 への変換も要る |
| 型付き登録から導出（`huma` / `goa` 等） | 真の自動化に最も近いが、ハンドラの書き方がフレームワークの登録形式に変わる。2.5 のエラー形式・2.6 のページネーション・2.9 のレート制限ヘッダといった既存の共通規約を載せ替える対価に見合わない |

**書き手は人でもエージェントでもよい。** 「手を動かして書く」ことに意味があるのではなく、生成器を挟まないことに意味がある。APIを追加・変更したステップの成果物に `openapi.yaml` の更新を含める（`.claude/commands/pb-step.md`）。

**記述の対象は実装済みの範囲に限る**（`ApiDesign.md` 1.3）。更新漏れは、実装のルート一覧（`chi.Walk`）と yaml の `paths` を突き合わせるテストで検出する。**「実装済みの現状」を名乗る以上、名乗りと実態のずれは仕組みで捕まえる。**

## 3.4 Webクライアントの配信とビルド

**Vue のビルド成果物を Go バイナリに埋め込み（`embed`）、単一プロセスで配信する。** Nginx 等の別サーバを立てない。

| 場面 | 構成 |
|---|---|
| 開発時 | Vite 開発サーバ（`:5173`）＋ `/api` `/mcp` を `:8080` へプロキシ。HMR が効く |
| 配布時 | `client/dist` を `server/internal/webui/dist/` へコピー → `//go:embed` で取り込み、単一バイナリから配信 |

**実装上の注意**

- `//go:embed` は**自パッケージのディレクトリ配下しか参照できない**。`../../client/dist` は書けないため、ビルド前にコピーする手順を Makefile に入れる
- 埋め込み対象のディレクトリが存在しないとコンパイルが通らない。`server/internal/webui/dist/placeholder.html` を1つコミットしておく
- **プレースホルダは `index.html` という名前にしない。** 実ビルドが同じ名前を出力するため、`make build` のたびに追跡対象が書き換わり、作業ツリーが汚れる
- **プレースホルダは自己完結でなければならない**（外部のスクリプトやスタイルを参照しない）。参照すると、そのファイルは埋め込まれていないので SPA フォールバックが HTML を返し、`<script type="module">` が HTML を受け取って画面が真っ白になる
- `index.html` が無いとき（client が未ビルド）は、このプレースホルダを **`503 Service Unavailable`** で返す。`200` にすると監視や自動確認から「画面が出ている」と区別が付かない
- SPA のため、`/api` `/mcp` `/healthcheck` 以外で未知のパスは `index.html` を返す（フォールバック）。`/healthcheck` は `/api/v1` の外に置く唯一のエンドポイントであり、フォールバックの例外になる（`ApiDesign.md` 2.11）
- キャッシュ制御：ハッシュ付きアセットは `immutable`、`index.html` は `no-cache`
- **第三者のライセンス表示（`THIRD_PARTY_NOTICES.txt`）も embed 対象へ置き、`/THIRD_PARTY_NOTICES.txt` で配信する**（4.5「第三者のライセンス表示」）。既存の静的配信に載るだけで、専用のハンドラを持たない。Vite の開発サーバには無い

**バイナリは `CGO_ENABLED=0` で静的リンクできる。** pgx が pure Go 実装であるため C ライブラリに依存せず、distroless / scratch イメージで動作する。クロスコンパイルも `GOOS` / `GOARCH` の指定だけで済む（`Design.md` 4.2）。

## 3.5 PostgreSQL を初期から使う理由

`DbDesign.md` 2.1 に記載する。要点は、①移行が確実に来るなら最初から移行後の姿で作る方が安い、②エージェントが並行書き込みするため単一ライタ制約が問題になる、③pgvector と `LISTEN/NOTIFY` が使える、の3点。

---

# 4. リポジトリ・ディレクトリ構成

## 4.1 全体

```
ProjectBackyard/
├── README.md                      ← プロジェクト概要（初見の人向け）
├── CLAUDE.md                      ← エージェント向け常時コンテキスト（ポインタのみ）
├── LEARNINGS.md                   ← 進め方の教訓（開始時に読む。**追跡対象外**。11.0）
├── Makefile                       ← 開発・ビルドの入口
├── .dockerignore                  ← docker build に送る材料を VERSION・client/・server/ に絞る（4.5）
├── .claude/commands/              ← 実装ステップ用のスラッシュコマンド
│
├── docs/                          ← 設計・運用に関する文書はすべてここ
│   ├── README.md                  ← 文書索引と主要な設計判断
│   ├── design/                    ← 設計文書
│   │   ├── Requirements.md        ← 要件・構想
│   │   ├── Design.md              ← 本書（全体設計）
│   │   ├── DbDesign.md            ← データベース設計
│   │   ├── ApiDesign.md           ← REST API 設計
│   │   ├── GuiDesign.md           ← GUI 設計
│   │   └── openapi.yaml           ← 実装済みAPIの現状（ApiDesign.md 1.3）
│   ├── Development.md             ← 開発環境の立ち上げ・デバッグ手順
│   ├── Testing.md                 ← 試験の設計と完了の基準
│   ├── images/                    ← README などに載せる画像
│   └── history/                   ← 完了した手順の記録（decisions.md / steps.md）
│
├── server/                        ← Go（APIサーバ + MCPサーバ + 静的配信）
│   ├── go.mod                     ← アプリの依存のみ
│   ├── sqlc.yaml                  ← migrations/ をスキーマ源として参照
│   ├── tools/go.mod               ← goose / sqlc のバージョン固定（DbDesign 5.1）
│   ├── cmd/pb/main.go             ← serve / admin create などのサブコマンド
│   ├── migrations/                ← goose。唯一のスキーマ定義（3.2）
│   │   ├── 0001_extensions_and_functions.sql
│   │   └── …                      ← 一覧は DbDesign 5.2 と 8章
│   ├── internal/
│   │   ├── config/                ← 設定の3層（環境変数・*_FILE・DB）。10.3
│   │   ├── httpapi/               ← REST（ApiDesign.md）
│   │   │   ├── middleware/        ← 認証・認可・CSRF・レート制限・request_id
│   │   │   ├── apierr/            ← ApiDesign 2.5 のエラー形式
│   │   │   └── v1/                ← エンドポイント実装
│   │   ├── auth/                  ← 認証・認可のドメインロジック（本書6章）
│   │   ├── audit/                 ← audit_log への記録（ApiDesign 2.10）
│   │   ├── store/
│   │   │   ├── queries/*.sql      ← 手書きSQL（sqlc の入力）
│   │   │   ├── gen/               ← sqlc 生成物（コミットする）
│   │   │   └── search/            ← 全文検索の実装を隔離（DbDesign 4.5）
│   │   ├── dbstat/                ← DB の接続状態と統計。sqlc を通さずカタログを読む（ApiDesign 11.10）
│   │   ├── webui/                 ← 静的配信（3.4）
│   │   │   ├── embed.go           ← //go:embed all:dist
│   │   │   └── dist/              ← client のビルド成果物（.gitignore、雛形のみコミット）
│   │   ├── mcp/                   ← MCPサーバ（8章）
│   │   └── …                      ← ほかに activity / agentsetup / backup / lexorank /
│   │                                  maintenance / mcpbridge / mfa / passkey / project / tlscert / ulidgen
│
├── client/                        ← Vue 3 + TypeScript + Vite
│   ├── package.json
│   ├── vite.config.ts             ← /api /mcp を server へプロキシ（開発時）
│   ├── src/
│   │   ├── pages/ components/ stores/
│   │   └── api/                   ← openapi.yaml から生成する型付きクライアント
│   └── dist/                      ← .gitignore
│
└── deploy/                        ← 環境別の実行設定
    ├── Dockerfile                 ← 配布用イメージ。client → pb と goose → 実行（distroless nonroot）の3段（4.5）
    ├── base/                      ← 全環境で共通のもの（4.3）
    │   ├── compose.yaml
    │   ├── initdb/01_roles.sql    ← DBロール分離（DbDesign 3.4）
    │   └── env.example
    ├── dev/                       ← 開発検証環境（compose は持たず base をそのまま使う。4.3）
    │   ├── reset.sh               ← DBを作り直してデモを投入（DbDesign 7.6.6）
    │   ├── seed/dev-data.yaml     ← デモデータの定義（DbDesign 7.6.4。コミットする）
    │   └── secrets/               ← .gitignore（.example のみコミット）
    ├── stg/                       ← PB 自身を管理するインスタンス（4.4）
    │   ├── compose.yaml           ← base への上書き（db のみ。pb-stg / :5433）
    │   ├── init.sh                ← 初回：秘密を乱数で生成し、DBを起動して migrate
    │   ├── build.sh               ← 動作に必要な一式を out/ へ出力する
    │   ├── pb.env.example         ← 出力に同梱する設定のテンプレート
    │   ├── out/                   ← .gitignore（build.sh の出力）
    │   └── secrets/               ← .gitignore（init.sh が生成。コミットしない）
    └── prod/                      ← 配布用（4.5）
        ├── build-release.sh       ← make release の本体。TARGET / OS / ARCH を受けて一式を出力する
        ├── MANUAL.md              ← 配布物の使い方。**一式に同梱する**（旧 docs/Deploy.md の予定を置き換えた）
        ├── native/                ← TARGET=native の雛形（起動・migrate・設定・ロール作成・常駐）
        ├── docker/                ← TARGET=docker の雛形（docker run で起動・migrate する例）
        ├── compose/               ← TARGET=compose の雛形（compose.yaml と、秘密を作る init.sh）
        └── k8s/                   ← TARGET=k8s の雛形（PB 本体の app.yaml・試すための db.yaml・Secret の雛形）
```

## 4.2 client と server を分けたまま単一プロセスで動かす

**フォルダは分離したまま、成果物だけを統合する。** 開発時の関心事（依存管理、ビルドツール、テスト）が Go と Node で全く異なるため、ソースツリーを混ぜる利点がない。一方、実行時は 3.4 のとおり `embed` で1バイナリにまとめるため、Nginx を別に立てる必要はない。

```
開発時                              配布時
┌──────────┐  /api  ┌──────────┐   ┌────────────────────────┐
│ Vite     │───────▶│ Go       │   │ Go バイナリ              │
│ :5173    │        │ :8080    │   │  ├ /api/v1/*           │
│ (HMR)    │◀───────│          │   │  ├ /mcp/*              │
└──────────┘  HTML  └──────────┘   │  └ /* → embed した dist │
                                    └────────────────────────┘
```

**Makefile が両者を繋ぐ。**

```make
build-client:                       # client/dist を生成
	cd client && npm ci && npm run build

sync-webui: build-client licenses-check   # embed 対象へコピー（//go:embed は親を辿れない）
	rm -rf server/internal/webui/dist && mkdir -p server/internal/webui/dist
	cp -R client/dist/. server/internal/webui/dist/
	cp THIRD_PARTY_NOTICES.txt server/internal/webui/dist/

build: sync-webui                   # 単一バイナリ
	cd server && CGO_ENABLED=0 go build -trimpath -o ../bin/pb ./cmd/pb
```

## 4.3 deploy/base に置くもの

「全環境で同じ」ものを `base/` に集約し、環境ごとの差分を重ねる。docker compose は複数ファイルの重ね合わせに対応している。**いま差分を持っているのは `stg/` だけで、dev は base をそのまま使う**（下記）。**配布用の `prod/compose/` は base に重ねず、単体で動く**——受け取った人は base を持たない（4.5）。

```
# dev（base だけ。Makefile の COMPOSE がこれである）
docker compose -f deploy/base/compose.yaml up -d

# stg（base に stg を重ねる。Makefile の STG_COMPOSE）
docker compose -f deploy/base/compose.yaml -f deploy/stg/compose.yaml up -d
```

| 置き場所 | 内容 |
|---|---|
| `base/compose.yaml` | `db` と `app` のサービス定義、ボリューム、ヘルスチェック、依存関係 |
| `base/initdb/` | DBロール作成（`DbDesign.md` 3.4）。環境によらず同一 |
| `base/env.example` | 必要な環境変数の一覧と説明。**第1層と第2層に分けてある**（10.3） |
| `base/pb.yaml.example` | **設定ファイル（YAML）の雛形。** `PB_CONFIG_FILE` で位置を渡す（10.3）。任意——置かなくても環境変数と既定値で動く |
| `stg/compose.yaml` | **stg の差分。** DB だけを別の compose プロジェクト（`pb-stg`）と別のポート（`:5433`）で立てる（4.4） |
| `prod/compose/compose.yaml` | **配布用。base に重ねず単体で動く。** イメージの参照は `make release` が埋め、DB のポートは外へ出さない。秘密は同梱の `init.sh` が作る（4.5） |

**dev は差分を持たず、base をそのまま使う。** `deploy/dev/` にあるのは `reset.sh`・`seed/`・`secrets/` だけで、**compose のファイルは無い**——ポートを `127.0.0.1` に公開することもログの設定も base が持っており、重ねるべき差分が出ていないためである。**差分が出た時点で `dev/compose.yaml` を足す**（そのとき Makefile の `COMPOSE` も base＋dev に変える）。

## 4.4 stg の扱い

**`stg/` は、PB 自身のプロジェクト管理に使うインスタンスである。** PB の開発を PB で管理するため、**開発中に壊れる `dev` とは別に、壊れないインスタンス**が要る。

| | `dev` | `stg` |
|---|---|---|
| 用途 | 開発中のコードを動かす | **PB 自身のプロジェクト管理**（チケット・憲章） |
| PB 本体 | `make run`（`go run`） | **`deploy/stg/build.sh` が出力したネイティブバイナリ一式** |
| 待受 | `127.0.0.1:8080` | `127.0.0.1:8081` |
| DB | compose プロジェクト `project-backyard`（`:5432`） | **compose プロジェクト `pb-stg`（`:5433`）** |
| 作り直し | `make dev-reset` | **しない**（データが本番相当） |

**分離は compose プロジェクトの単位で行う。** `deploy/dev/reset.sh` は `docker compose down -v` を実行して pgdata ボリュームごと破棄するため、**同じコンテナ内で DB 名を分けても `make dev-reset` 1回で消える**。compose プロジェクト名を分けると、コンテナ・ネットワーク・ボリュームが名前空間ごと分かれる。

**stg の PB 本体はコンテナにしない。** client を embed した単一バイナリを作れる構成（3.4）であり、`stg` に必要なのは「壊れず動き続けること」だけで、コンテナの利点（再現性・隔離）は開発端末上では効きが薄い。配布用のコンテナイメージ（`deploy/Dockerfile`。4.5）はあるが、stg はネイティブの一式で動かす。

**データの置き場も分ける。** compose プロジェクトを分けた帰結として、pgdata は `dev` の `project-backyard_pgdata` とは**別の名前付きボリューム `pb-stg_pgdata`** になる。同じボリュームを共有しない以上、`dev` 側の `down -v` は `stg` に届かない。

**stg の DB は `restart: always` にする。** base の既定は `unless-stopped` だが、それは**手で止めた後はコンテナランタイムを起動し直しても戻らない**。`stg` に求めるのは「端末を再起動した後も、意識せずに上がっていること」なので `always` を上書きする。**`docker compose down` はコンテナ自体を消すため、この設定でも戻らない**——`stg` を畳むのは容量を空けるときだけにする。

**アプリは常駐を保証しない。** データが `pb-stg_pgdata` に残っている限り、PB のプロセスは落として上げ直せば同じ状態に戻る。したがって停止・再起動は利用者の手（ターミナルから起動し、必要なら `nohup`）に任せ、監視や自動再起動の仕組みを持たない。**「壊れないインスタンス」の保証は DB 側にある。**

**ブラウザでは `http://localhost:8081` を開く。** Cookie はポートを区別しない（RFC 6265 8.5）。PB のセッション Cookie は `Domain` 属性を持たないホスト限定 Cookie なので、`dev`（`127.0.0.1:8080`）と同じホスト名で `stg` を開くと**双方のログインセッションが上書きし合う**。`localhost` と `127.0.0.1` は Cookie 上は別ホストであり、待受を `127.0.0.1:8081` に固定したままでも `localhost` から到達できる。

**`deploy/stg/` に置くもの**

| 置くもの | 内容 |
|---|---|
| `compose.yaml` | base への上書き。`name` / ポート / `restart` / secrets の置き場だけを差し替える |
| `init.sh` | 初回だけ実行する。**秘密を乱数で生成し**、DB を起動して migrate まで進める |
| `build.sh` | **動作に必要な一式を1つの出力ディレクトリへ吐く。** 出力を丸ごと任意のパスへ置けば、そこで動く |
| `pb.env.example` | 出力に同梱する設定のテンプレート。実体の `pb.env` は `.gitignore` |
| `secrets/` | `.gitignore`。**`.example` を置かず、`init.sh` が乱数で作る**——`dev` は `CHANGE_ME` の写しを手で書き換える形だが、`stg` は作り直さない器なので、初回に一度だけ強い値を機械に決めさせるほうがよい |
| `out/` | `build.sh` の出力。`.gitignore` |

**`build.sh` の出力は「配置すれば動く一式」にする。**

```
out/
├── pb                      client を embed した単一バイナリ
├── pb.env                  動作を規定する設定（pb.env.example の写し）
├── run.sh                  pb.env を読んで pb serve を起動する。自身の位置へ cd してから動く
├── secrets/
│   └── app_database_url    pb_app での接続文字列（0600）
└── README.txt              起動・停止・URL・ログの見方
```

**設定ファイル内のパスは出力ディレクトリからの相対で書く。** `pb.env` は接続文字列そのものではなく `PB_DATABASE_URL_FILE=./secrets/app_database_url` を持ち、**パスワードは設定ファイルにも環境変数の値にも現れない**。相対パスが解けるよう、`run.sh` は自身のあるディレクトリへ移ってから `pb` を起動する。

**起動するのは `run.sh` であって `pb` ではない。** `pb.env` は**シェルが読んで環境変数へ export するファイル**であり（`run.sh` の `set -a` + source）、**バイナリは `pb.env` を開かない。** `pb` を直に叩いても `pb.env` は効かない。

**バイナリが直接読む設定ファイルは、`PB_CONFIG_FILE` が指す YAML である**（10.3）。**`pb.env` は残る**——環境変数の層に値を流し込む道具としてで、YAML とは別の段である。

**stg のマイグレーションはリポジトリ側から適用する。** goose は `server/tools/` のツールモジュールにあり、**stg の出力一式には含まれない**（`DbDesign.md` 5.1）。stg のスキーマを進めるのは開発端末での作業であって、配置した一式の仕事ではない。**配布用の一式（4.5）は事情が違い、goose を同梱する**——受け取った人はリポジトリを持っていない。

## 4.5 ビルドとクロスコンパイル

| 目的 | 方法 |
|---|---|
| 開発端末で動かす | `make run`（`go run`）または `make build` |
| **配布用の一式を作る** | **`make release TARGET=… OS=… ARCH=… [OUT=…] [PUSH=…]`** → `deploy/prod/build-release.sh`。配布物の使い方は `deploy/prod/MANUAL.md`（一式に同梱する） |
| コンテナで動かす | `deploy/Dockerfile`（3段）。**ビルドもコンテナの中で行うため、この端末の Go と Node を使わない。** `make release TARGET=docker\|compose\|k8s` が呼ぶ |
| 他アーキテクチャ向けイメージ | `make release … ARCH=amd64\|arm64`。**1回に1つの CPU。** ビルドの段を `$BUILDPLATFORM` で動かしてクロスコンパイルするので、エミュレーションは要らない |

### `make release`

**`TARGET` で形を、`OS` と `ARCH` で行き先を選ぶ。** `native`（実行ファイルと起動スクリプト）、`docker` / `compose`（コンテナイメージと一式）、`k8s`（コンテナイメージとマニフェスト一式）の4つ。

| 引数 | 受ける値 | 同じ意味に読む表記 |
|---|---|---|
| `TARGET` | `native` / `docker` / `compose` / `k8s` | — |
| `OS` | `darwin` / `windows` / `linux`。**docker / compose / k8s は `linux` だけで、省いてよい** | `mac` → `darwin` |
| `ARCH` | `amd64` / `arm64` | `x64`・`x86_64`・`x86` → `amd64`、`m1`・`arm`・`aarch64` → `arm64` |
| `OUT` | 出力先 | 省略すると `dist/pb-v<版>-<TARGET>-<OS>-<ARCH>` |
| `PUSH` | docker / compose / k8s だけ。イメージを tar に出す代わりに、このレジストリへ送る | — |

**CPU は 64bit の2種だけを受ける**。`386`・`armv7` など 32bit を名指しする値は、理由を出して止まる。DB の公開イメージ（`pgvector/pgvector:pg17`）も amd64 / arm64 しか無い。

**PB が動く OS / CPU と、ZIP を受け取るエージェントの端末は別である。** `make build`、stg と
native の一式、コンテナイメージには `darwin/arm64`、`windows/amd64`、`windows/arm64`、
`linux/amd64`、`linux/arm64` の5種類の `pb-mcp-bridge` を
`bridges/<os>-<arch>/pb-mcp-bridge[.exe]` に置く。macOS Intel 向けブリッジは配布しない。
`deploy/build-bridges.sh` が共通のクロスビルドを担い、PB は利用者の選んだ1種類を ZIP に入れる。
バイナリをリポジトリへはコミットしない。`make run` の `go run` は一時パスから実行するため、
ブリッジZIPを試す際は `make build` で作った `bin/pb` を使う。

**受けない指定は終了コード 2 で止まる。** ビルドそのものの失敗（1）と区別するため。

**空でない出力先には書かない。** stg の `build.sh` は上書きするが、配布物では許さない——利用者が書き換えた起動スクリプトや設定が黙って元に戻り、動いている一式のバイナリを同じパスへ書き直すと実行中のプロセスが落ちうる。

**Makefile はコマンドラインで渡された値だけを使う**（`$(origin …)`）。`OUT` は `stg-build` と共有の変数で既定値が `deploy/stg/out` なので、渡さずに叩いて stg の一式を上書きしないため。`OS` や `ARCH` は環境変数として定義されている端末があり、それを黙って拾わないため。

#### native の一式

```
<OUT>/
├── pb（pb.exe）              client を embed した単一バイナリ
├── goose（goose.exe）        postgres のドライバだけに絞った goose（DbDesign.md 5.1）
├── bridges/                 5種類の pb-mcp-bridge（Codex の接続ZIP用）
├── migrations/               server/migrations/ の写し
├── run.sh（run.ps1）          起動の入口。設定ファイル・秘密の位置・待受を渡して pb を起動する
├── migrate.sh（migrate.ps1）  goose で migrate する
├── migrate.conf              goose の接続先（パスワードを含まない）
├── pb.yaml                   設定ファイル（PB_CONFIG_FILE。何も書かなくても動く）
├── create-roles.sql          既存の PostgreSQL に DB とロールを作る（psql で1回）
├── secrets/                  app_database_url.example / pgpass.example
├── launchd/（darwin）・systemd/（linux）   常駐の雛形
├── LICENSE・NOTICE・THIRD_PARTY_NOTICES.txt  PB のライセンスと著作権表示、第三者のライセンス表示（全 TARGET に置く）
└── MANUAL.md                 deploy/prod/MANUAL.md の写し
```

| 決めたこと | 理由 |
|---|---|
| **DB は用意済みの PostgreSQL が前提で、配布物に DB を含めない** | native は「実行ファイルと起動スクリプト」であり、DB の立て方は利用者の環境が決める。**要るのは PostgreSQL 17・contrib・ICU で、pgvector はまだ要らない**（`DbDesign.md` 3.1 の `vector` は構想） |
| **goose は別の実行ファイルとして同梱し、`pb` 本体に入れない**（同） | `server/go.mod` にツールの依存を持ち込まない（`DbDesign.md` 5.1） |
| **`pb_owner` のパスワードは passfile（`secrets/pgpass`）で渡す**（同） | goose はパスワードに `_FILE` の口を持たない。**`PGPASSFILE` にはファイルの位置だけを渡し、中身は pgx が読む**ので、パスワードが環境変数の値にも引数にも出ない |
| **ロール作成の SQL にパスワードを書かない** | psql の `\password` が入力を画面に出さず、ハッシュにしてから送る。中身の権限の分け方は `deploy/base/initdb/01_roles.sh` と同じ（`DbDesign.md` 3.4） |
| **起動スクリプトで待受を固定しない** | **PB の既定値そのものが `127.0.0.1:8080`** なので、固定しなくても平文ですべてのアドレスには出ない。**固定しないので、画面の「待受アドレス」が使える**（固定すると画面が「環境変数で固定」になる） |
| **ps1 には出力するときに UTF-8 の BOM を付ける** | Windows PowerShell 5.1 は BOM の無い `.ps1` を ANSI として読み、日本語が化ける。リポジトリ側には BOM を持たせない（差分と grep を素直に保つ）。**Windows の実機では確かめていない**（この端末に Windows も pwsh も無い） |

#### docker / compose の一式

**イメージ**は `deploy/Dockerfile` の3段（client → pb と goose → 実行）で作る。中身は `/pb`（入口。既定の引数は `serve`）・`/goose`（postgres のドライバだけ）・`/bridges`（5種類のブリッジ）・`/migrations`・`/LICENSE`・`/NOTICE`・`/THIRD_PARTY_NOTICES.txt`。実行の段は `gcr.io/distroless/static-debian12:nonroot` で、利用者は uid 65532。

| | compose | docker |
|---|---|---|
| イメージ | `project-backyard-<版>-linux-<CPU>.tar`（`PUSH` のときは無し） | 同じ |
| 起動 | `compose.yaml`（db＝公開イメージ `pgvector/pgvector:pg17`・migrate・app） | `run.sh`（`docker run` の例） |
| スキーマ | compose の migrate サービス（同じイメージの `/goose`。起動のたびに走る） | `migrate.sh`（同じイメージの `/goose`） |
| 秘密 | `init.sh` が乱数で作る | 見本を複製して手で書く |
| DB | 同梱の DB サービス。**外部の PostgreSQL へ繋ぐ手順を `compose.yaml` にコメントで併記** | 用意済みの PostgreSQL（`create-roles.sql`） |

| 決めたこと | 理由 |
|---|---|
| **nonroot（uid 65532）で動かし、秘密ファイルはディレクトリ 700・ファイル 644 で置く** | コンテナの利用者に読ませるには、ファイルの権限で開けるしかない——**compose は secrets の `uid`・`mode` を無視する**（compose v5.3.1 で確認）。**700 のディレクトリごと渡すと 644 でも読めない**ので、ファイルを1つずつ渡す。**mac の共有パスは所有者が書き換わって見えるので、Linux の権限は VM の中で確かめる** |
| **コンテナの healthcheck を持たない**（同） | distroless に `/healthcheck` を叩くコマンドが無く、叩き役（`pb healthcheck`）も足さない。健全かは外から見る。再検討の条件は、**compose に「app が健全になってから」を待つサービスを足すとき** |
| **tar は buildx の `type=docker`** | `docker load` のほか nerdctl や podman も読める。取り込める相手の広さで選ぶ |
| **`pb_owner` のパスワードは passfile で渡す** | native と揃える。compose では秘密の1つとして渡し、`PGPASSFILE` にその位置を入れる |
| **`.dockerignore` は送るものを名指しで許す**（`VERSION`・`LICENSE`・`NOTICE`・`THIRD_PARTY_NOTICES.txt`・`client/`・`server/`） | 除く形だと、あとから増えた秘密（`deploy/*/secrets/` など）を送り漏らしうる |
| **compose のプロジェクト名は `pb-prod` で、DB のポートは外へ出さない** | dev の compose（`project-backyard`）と同じ名前だと、同じ端末で DB のボリュームを共有する。DB へは compose の中からだけ繋ぐ |
| **イメージに `ENV PB_BIND=0.0.0.0:8080` を持たせる** | **PB の既定値は `127.0.0.1:8080` である**。コンテナの中で 127.0.0.1 に閉じると、公開範囲を決めるはずの `-p` や `ports` を通っても外から届かない。**画面の「待受アドレス」は環境変数で固定になる** |
| **`make up` は db だけのまま** | dev は PB 本体を `make run` でホストから動かす。app のコンテナまで上げると 8080 番でぶつかる（`Development.md` 2.2） |

#### k8s の一式

**イメージは docker / compose と同じもの**で、マニフェストと Secret の雛形を添える。**DB は試すためのサンプルで、運用では CloudNativePG か外部の PostgreSQL を使う**（`DbDesign.md` 3.3）。

```
<OUT>/
├── project-backyard-<版>-linux-<CPU>.tar   イメージ（PUSH のときは無し）
├── k8s/
│   ├── app.yaml          ConfigMap（pb.yaml）・Deployment（initContainer で migrate）・Service
│   └── db.yaml           試すための DB：ConfigMap（ロール作成）・headless Service・StatefulSet（PVC 付き）
├── secret.example.yaml   Secret の雛形（値は CHANGE_ME）
├── create-roles.sql      外部の PostgreSQL 用（native と同じもの）
├── LICENSE・NOTICE・THIRD_PARTY_NOTICES.txt
└── MANUAL.md
```

| 決めたこと | 理由 |
|---|---|
| **DB は素の StatefulSet のサンプルとし、外部の PostgreSQL へ繋ぐ設定をコメントアウトで併記する** | 試すためのもの。CloudNativePG の `Cluster` はサンプルにしない |
| **秘密は Secret から環境変数で渡す**（推奨はファイルでのマウントだった） | PB 本体は `PB_DATABASE_URL`、goose は `PGPASSWORD`（接続先にパスワードを書かない）。**Pod の定義に出るのは参照だけ**である。**DB のサンプルのロール作成だけはファイルで読む**——dev・stg・compose と共有する正本（`deploy/base/initdb/01_roles.sh`）を変えないため |
| **Secret は `kubectl create` で入れ、`apply` しない** | `apply` は入れた内容を注釈 `last-applied-configuration` に平文で残す。`create` と `replace` は残さない |
| **migrate は initContainer、Deployment はレプリカ1・`strategy: Recreate`** | goose の CLI に同時実行を防ぐロックが無い。並べて入れ替えると、古い版の Pod が新しいスキーマの上で動く。**代わりに入れ替えの間は止まる**（`DbDesign.md` 3.3） |
| **livenessProbe だけを置き、`/healthcheck` を見る** | `/healthcheck` は DB を見ない固定の応答で、liveness だけを表す（10.2）。**画面で TLS を有効にしたら `scheme: HTTPS` へ直す**——PB は平文と TLS を同時に待ち受けない（6.6.1） |
| **`enableServiceLinks: false`** | 既定では Service `pb` の分として `PB_PORT` や `PB_SERVICE_HOST` が注入される（headless の `pb-db` の分は入らない）。PB は `PB_<キー>` を設定として読むので、キーを足したときに黙って拾いうる |
| **nonroot（65532）・`readOnlyRootFilesystem`・capability をすべて外す** | PB と goose はファイルを書かない。読み取り専用のまま migrate・起動・ログインまで通した |
| **Secret の雛形は `k8s/` の外に置く** | `kubectl apply -f k8s/` で、値の入っていない Secret まで入れないため |
| **ロール作成のスクリプトは、ビルドのときに ConfigMap へ埋め込む** | 正本を `deploy/base/initdb/01_roles.sh` の1つに保ったまま、一式を `kubectl` だけで入れられる |
| **マニフェストに namespace を書かない** | 入れる先は apply するときの `-n` で決める |

#### 第三者のライセンス表示

**配布物に入る第三者のソフトウェアの著作権表示とライセンスの本文を、`THIRD_PARTY_NOTICES.txt` の1枚にまとめてリポジトリ直下に置く。** MIT・BSD・Apache-2.0 は、バイナリで再配布するときにこれらの同梱を求める。**`make licenses` が生成し、手で編集しない**（文書の本文にはこの旨を書かない。読むのは配布物と画面の利用者であるため）。

| 対象 | 拾い方 |
|---|---|
| Go のモジュール | 配布する実行ファイル（`pb`・`pb-mcp-bridge`・`goose`）ごとに `go list -deps` で、**リンクされるもの**を拾う。OS で依存が変わりうるので、配布する3つの OS の和を取る。本文はモジュールの直下の LICENSE・NOTICE・PATENTS（`server/tools/notices`） |
| Go の標準ライブラリ | ツールチェーンの `LICENSE`・`PATENTS` |
| npm のパッケージ | **バンドルに実際に入ったものだけ**を、Vite 8 の `build.license` で拾う（`client/scripts/npm-licenses.mjs`） |
| ビルドの道具が注入するコード | `build.license` が数えない仮想モジュール（ID が `\0` で始まる）を集め、持ち主（vite・`@vitejs/plugin-vue`・rolldown）の LICENSE を足す。**持ち主を対応づけていない仮想モジュールが現れたら失敗する** |

| 決めたこと | 理由 |
|---|---|
| **goose の依存も対象にする** | native の一式とイメージに同梱しており、`pb` と依存が違う（`godotenv`・`xflag` と、`pgx`・`go-retry` の別の版） |
| **npm は `package.json` の依存ではなく、バンドルに入ったものを拾う** | vite・typescript などビルドにしか使わないものは配布物に入らない。**依存を足さずに拾える**ので、Vite に組み込みの機能を使う |
| **出力に時刻もツールチェーンの版も入れず、並びを固定する** | 同じ依存からは毎回同じ内容になるので、作り直した結果と比べるだけで鮮度が分かる |
| **`make licenses-check` を `make test` と `make sync-webui`（＝`make build`・`make stg-build`・native の `make release`）の前に置き、Dockerfile でも同じ検査を通す** | 依存を足したり上げたりして作り直さないまま、古い表示を配らない。**このため `make test` にも `client/node_modules`（`npm ci`）が要る** |
| **Vite の LICENSE.md は先頭の節（Vite 自身の MIT）だけを載せる** | 後ろに続くのは Vite が内部に抱える依存のライセンスで、ビルド時に動くコードでありバンドルに入らない（約11万字） |
| **PB 自身の著作権表示は `NOTICE` の2行（`Project Backyard` と `Copyright 2026 boyaki-machine`）に置き、`LICENSE` と同じく全 TARGET とイメージに同梱する** | Apache-2.0 の 4(d) により、`NOTICE` は再配布する人が残す対象になる。**残させるものなので短く保つ**（第三者の表示は `THIRD_PARTY_NOTICES.txt` に分ける）。README 8章にも同じ1行を書く |
| **goose のビルドタグは `build-release.sh` から読み出す** | タグで goose の依存が変わる。正本を増やさない |
| **画面からはユーザーメニューの版の行のリンクで読む**（`GuiDesign.md` 4.2） | 静的なテキストを別タブで開くだけで、画面を作らない |

**`CGO_ENABLED=0` で静的バイナリになる。** pgx が pure Go 実装であるため C ライブラリに依存せず、`scratch` や distroless イメージで動作する。開発端末（arm64 macOS）から Linux/amd64 向けを出すのもフラグ指定のみで済む。

## 4.6 その他の規約

- **`internal/` に置くことで外部からの import を禁止する。** 単一アプリケーションであり、パッケージを公開する予定がないため
- **`store/gen/` はコミットする。** 生成物だが、`sqlc generate` を実行しなくてもビルドが通る状態を保ち、レビュー時に生成結果の差分が見えるようにする
- **`store/search/` に全文検索を隔離する。** 日本語検索の方式（`pg_trgm` → `pg_bigm`）を将来変更した際、影響範囲をこの層に閉じ込めるため（`DbDesign.md` 4.5）
- **秘密は `deploy/<env>/secrets/` に置き、`.gitignore` する。** `.example` ファイルのみコミットする

---

# 5. データベース設計

**正本は `DbDesign.md`。** 本章は全体像の把握のための要約に留める。スキーマ定義・マイグレーション・DB実行環境・初期データはすべて `DbDesign.md` を参照すること。

## 5.1 主要エンティティの関係

```
        actor ──┬── app_user ── user_identity ── local_credential
                │                    │
                │                    └── auth_provider
                └── agent
                  │
                  │ (assignee / author / created_by として全テーブルから参照)
                  ▼
project ──┬── ticket ──┬── comment
          │            ├── dod_item
          │            ├── ticket_link (self join)
          │            ├── ticket_tag ── tag
          │            ├── task_lease
          │            └── agent_run ── agent_report
          ├── sprint
          ├── tag
          ├── workflow ── workflow_status ── workflow_transition
          ├── knowledge ── knowledge_revision
          ├── proposal          （すべての「AIの提案」がここを通る）
          ├── project_event
          └── project_member
```

## 5.2 テーブル一覧

| 状態 | 領域 | テーブル | 詳細 |
|---|---|---|---|
| 作成済み | アクター・認証 | `actor` `app_user` `auth_provider` `user_identity` `local_credential` `access_token` | `DbDesign.md` 6.2 |
| 作成済み | 多要素認証・パスキー | `user_mfa_credential` `mfa_login_challenge` `mfa_recovery_code` `user_passkey` `webauthn_challenge` | 6.18 / 6.19 |
| 作成済み | 認可 | `permission` `role` `role_permission` `project_member` | 6.3 |
| 作成済み | プロジェクト | `project` `project_counter` | 6.4 |
| 作成済み | ワークフロー | `workflow` `workflow_status` `workflow_transition` | 6.5 |
| 作成済み | チケット | `ticket` `ticket_link` `ticket_reference` | 6.6 / 6.12 |
| 作成済み | コメント・添付 | `comment` `attachment` | 6.7 |
| 作成済み | 履歴 | `activity` `audit_log` | 6.8 |
| 作成済み | アジャイル | `sprint` `ticket_sprint` | 6.9 |
| 作成済み | タグ | `tag` `ticket_tag` | 6.10 |
| 作成済み | 完了条件 | `dod_item` | 6.11 |
| 作成済み | **プロジェクト文書** | `document` `document_revision` | 8.1 |
| 作成済み | エージェント連携 | `agent` `agent_client_kind` `task_lease` | 8.2 |
| 作成済み | エージェントの実行記録 | `agent_run` `agent_report` `context_pack_log` | 8.2 |
| 作成済み | **アプリケーション設定** | `app_setting` `pending_setting_change` `tls_certificate` `app_secret` | `DbDesign.md` 6.14〜6.17（**10.3 の第2層・第3層の置き場**） |
| 構想 | 知識還流 | `knowledge` `knowledge_revision` `proposal` | 8.3 |
| 構想 | AI・分析 | `comment_signal` `embedding` `project_event` `estimate_record` `contribution` | 8.4 |

**`document` は既存のテーブルを1つも変更しない**（原則1）。`ticket_reference`（6.12）から
PB 内の文書を指せるようにするかは、必要性が運用で確認できてから決める（`ApiDesign.md` 10章）。

## 5.3 設計上の要点（3点のみ）

本書の設計原則と直結する3点を挙げる。それ以外は `DbDesign.md` を参照すること。

1. **`actor` が人間とエージェントの共通基底になっている**（原則3）。`ticket.assignee_id` などはすべて `actor(id)` を参照するため、エージェントを導入しても既存テーブルの変更が不要だった
2. **`app_user` と `user_identity` を分離している**（原則4）。OIDC/SAML（構想）を追加する際、既存ユーザーを作り直さずに行の追加だけで済む
3. **`proposal` がAIの提案の唯一の入口**（原則6）。エージェントは本体テーブルを直接更新せず、承認を経て反映される

---

# 6. 認証・認可設計

**本章が認証・認可の正本である。** 対応するテーブル定義は `DbDesign.md` 6.2〜6.3、APIは `ApiDesign.md` 3〜4章・6〜7章を参照。

## 6.1 全体方針

| 項目 | いま | 構想 |
|---|---|---|
| 認証方式 | ローカル ID（メール）/ パスワードと、パスキー（6.8） | + OIDC / SAML |
| セッション | HttpOnly Cookie + 不透明トークン | 同左（IdP はログイン時のみ） |
| API アクセス | Bearer トークン | 同左 |
| 権限 | システムロール2種＋プロジェクトロール | + IdP グループからのロールマッピング |

**第2要素は本表に載せない。** 認証方式（誰であるかを特定する手段）とは層が違うためで、
**TOTP は 6.7 にある。**
**パスキーは第2要素ではなく、パスワードの代わりに本人を特定する手段である**（6.8）。
**ただし `user_identity` には載せない**——理由は 6.8.4 にある。

**将来の IdP 連携でコードの大部分を変えないための鍵は、`user_identity` 抽象である。** 認証処理を「① 認証手段が subject を特定する → ② subject から `user_identity` を引く → ③ `app_user` を得てセッションを発行する」の3段に分離しておけば、OIDC / SAML の追加は①のアダプタ実装のみで済む。

## 6.2 認証フロー

### 6.2.1 ローカル ID/PW ログイン

```
1. POST /api/v1/auth/login  { email, password }
2. app_user を email で検索（citext のため大文字小文字を区別しない）
3. user_identity を (provider_key='local', subject=email) で検索
4. local_credential.locked_until を確認 → ロック中なら 423 を返す
5. Argon2id で password_hash を検証
   ├─ 失敗 → failed_attempts++ 、閾値超過で locked_until を設定
   │          audit_log('login.failure') を記録し 401 を返す
   └─ 成功 → failed_attempts=0
             ハッシュパラメータが旧世代なら再ハッシュして更新
5.5 確定済みの第2要素が登録されていれば、ここで分岐する（6.7.4）
   └─ セッションを発行せず、挑戦を返して手順6〜8 を次の要求へ持ち越す
6. access_token を発行（token_type='session'）
   ├─ 平文トークン = "pb_sess_" + base64url(random 32 bytes)
   └─ DB には SHA-256 ハッシュのみ保存
7. Set-Cookie: pb_session=<平文>; HttpOnly; SameSite=Lax; Secure(本番); Path=/
8. audit_log('login.success') を記録
```

**平文トークンは Cookie にのみ存在し、DB にもログにも残さない。**

ユーザーが存在しない場合もダミーハッシュを検証し、応答時間を揃える（タイミング攻撃対策）。

### 6.2.2 リクエスト時の認証

```
Cookie(pb_session) または Authorization: Bearer <token>
  → SHA-256 でハッシュ化
  → access_token を token_hash（UNIQUE索引）で検索
  → revoked_at IS NULL かつ expires_at > now() を確認
  → last_used_at を更新（書き込み負荷軽減のため1分粒度で間引く）
  → actor をロードしてリクエストコンテキストに載せる
```

**JWT を採用しない理由**は、①即時失効が必要（エージェントの暴走時にトークンを止めたい）、②スコープ変更を即時反映したい、の2点。

### 6.2.3 OIDC / SAML（構想の差し込み点）

以下の**インターフェースだけ**を定義してある。

```go
type AuthProvider interface {
    Key() string
    Kind() ProviderKind // Local | OIDC | SAML
    // ブラウザをIdPへ飛ばす（Local では利用しない）
    Start(state string) (redirectURL string, ok bool)
    // コールバックを検証し、subject と属性を返す
    Verify(ctx context.Context, in VerifyInput) (VerifiedIdentity, error)
}

type VerifiedIdentity struct {
    Subject       string            // OIDC: sub / SAML: NameID / Local: email
    Email         string
    DisplayName   string
    Groups        []string          // ロールマッピングの入力
    RawAttributes map[string]any
}
```

`verify()` 以降（`user_identity` 検索 → セッション発行）は**全プロバイダで共通**にする。この共通化ができていれば、OIDC 追加時の作業は以下に限定される。

1. `AuthProvider` の OIDC 実装を追加する
2. `auth_provider` テーブルに1行 INSERT する
3. ログイン画面のボタンは `GET /api/v1/auth/providers` の結果から自動生成される（`ApiDesign.md` 3.3）ため**フロントの改修が不要**

**JIT プロビジョニング**は `auth_provider.is_jit_provisioning` で制御する。有効時は `default_system_role` で `app_user` を作成し、`role_mapping` に従って `project_member` を付与する。

**アカウントリンク**：既存のローカルユーザーが後から IdP を使う場合、同一 `user_id` に対して `user_identity` を追加する。メールアドレス一致による自動リンクは**なりすましのリスクがあるため既定で無効**とし、管理者による明示的リンクか、ログイン中ユーザーによる自己リンクのみを許す。

## 6.3 パスワードとアカウント保護

| 項目 | 方針 |
|---|---|
| ハッシュ | Argon2id、PHC 文字列で保存。パラメータは `m=64MiB, t=3, p=4` を初期値とする |
| 最小長 | 12文字。複雑性要件（記号必須等）は課さず長さを優先する |
| 自動生成パスワード | 語句連結方式（`quiet-harbor-4172-mint`）。口頭・チャットでの伝達誤りを減らす（`ApiDesign.md` 6.2） |
| 既知の漏洩パスワード | 未対応（オフライン動作を優先） |
| ログイン失敗 | 5回連続で15分ロック。`failed_attempts` / `locked_until` で管理 |
| レート制限 | IP単位・アカウント単位の両方（`ApiDesign.md` 2.9） |
| エラーメッセージ | 「メールアドレスまたはパスワードが正しくありません」で統一し、アカウント存在を漏らさない |
| 初期管理者 | 初回起動時に `pb admin create` で対話的に作成。**既定パスワードをシードに埋め込まない**（`DbDesign.md` 7.5） |
| パスワード変更 | 変更時に当該ユーザーのセッションを全失効（現在のセッションを除く） |

## 6.4 認可モデル

### 6.4.1 三層構造

権限は**3つの層の積**として決まる。

```
実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ トークンのスコープ
```

| 層 | 保持場所 | 役割 |
|---|---|---|
| システムロール | `app_user.system_role` | インスタンス全体に対する役割。**オペレータ／アドミニストレータ** |
| プロジェクトロール | `project_member.role_key` | 個別プロジェクトに対する役割。PM／メンバー／閲覧者 |
| トークンスコープ | `access_token.scopes` | **権限の上限**。ロールが持つ権限を超えることはできず、縮小のみ可能 |

**トークンスコープを「縮小のみ」と定義することが重要である。** エージェント用トークンに読み取りだけを与えれば、そのトークンで実行される限り、たとえ紐づくアクターが管理者であっても他の操作はできない（`Requirements.md` 10.10.3）。

**非管理者のシステムロールにはプロジェクトの書き込み権限を与えない。** 和集合で計算するため、`operator` に書き込み権限があると `project_viewer` の「閲覧のみ」が成立しない。`administrator` はインスタンス全体を管理する例外として全権限を持つ。現在の `operator` の割り当ては `DbDesign.md` 6.20 のマイグレーションで閲覧権限に絞る。

**スコープの語彙は権限カタログのキーそのものである**（6.4.2。32件。`ApiDesign.md` 4.4.2）。上の積は権限キーどうしの完全一致で取るため、別の語彙を混ぜると、絞ったつもりのトークンが権限0件になるか、解釈できない語彙を通して逆に広がるかのどちらかになる。**空配列は「絞り込みなし」であって「権限0件」ではない。**

**エージェントの既定スコープもこの語彙で書く**（6.5）。別の語彙で発行すると、実効権限が0件になる。

**エージェントは左辺の2つの層を所有者から借りる。** `actor(kind='agent')` は `app_user` の行を持たないためシステムロールの層が必ず空になり、そのままでは権限0件になる。`agent.owner_actor_id` が指す人のロールを両層に用いる（6.5、`DbDesign.md` 8.2.1）。**式そのものは変わらない**——変わるのは「誰のロールを読むか」だけである。

### 6.4.2 権限カタログ

権限をコードのif文ではなく**データとして定義**する（原則5）。カタログは33件（0010 の28件に、0017 の `doc.view` / `doc.edit`、0027 の `ticket.reference.edit`、0029 の `ticket.self_edit`、0044 の `ticket.actual_point.edit` を足したもの）。`DbDesign.md` 7.2（0010）・8.1.4（0017）・6.12.1（0027）・6.13（0029）のシードが正本。

| カテゴリ | 権限キー |
|---|---|
| project | `project.view` `project.create` `project.edit` `project.archive` |
| ticket | `ticket.view` `ticket.create` `ticket.edit` `ticket.transition` `ticket.close` `ticket.assign` `ticket.delete` |
| comment | `comment.create` `comment.edit_own` `comment.delete_any` |
| knowledge | `knowledge.view` `knowledge.propose` `knowledge.approve` |
| proposal | `proposal.review` |
| agent | `agent.register` `agent.token.issue` `agent.run` |
| admin | `user.manage` `role.manage` `authprovider.manage` `auditlog.view` `system.settings` |
| export | `export.excel` `share.publiclink` |

### 6.4.3 組み込みロール

5種類（システム2＋プロジェクト3）。**割り当ての正本は `DbDesign.md` 7.3 のシード**であり、以下は要約。

| ロール | scope | 概要 |
|---|---|---|
| **operator** | system | プロジェクト・チケット・文書・知識の閲覧とExcel出力。書き込みはプロジェクトロールから得る |
| **administrator** | system | **全権限**。ユーザー管理・認証設定・監査ログ・プロジェクト作成を含む |
| project_admin | project | 当該プロジェクトの全操作＋承認（`knowledge.approve` `proposal.review`）＋エージェント管理 |
| project_member | project | 当該プロジェクトのチケット作成・編集・遷移、コメント |
| project_viewer | project | 閲覧のみ |

**プロジェクトロールもUIで扱う。** ユーザー詳細のメンバーシップ欄（`GuiDesign.md` 5.6.2）で付与・変更・剥奪ができ、権限マトリクス（同 5.6.3）に5ロールすべてが並ぶ。**カスタムロールの作成と権限の編集だけが構想として残る**（`ApiDesign.md` 7.3）。

### 6.4.4 画面・機能の制限方式

**サーバとフロントの二重で制御する。**

**サーバ側（本体）**：全 API ハンドラに必要権限を宣言し、ミドルウェアで検証する。

```go
r.With(RequirePermission("ticket.close")).
  Post("/projects/{key}/tickets/{seq}/close", h.CloseTicket)
```

**権限は chi のミドルウェアとしてルート定義に宣言する。** ハンドラ本体に権限チェックを書くと、新しいエンドポイントで書き忘れても気づけない。ルート定義に並べれば、`routes.go` を眺めるだけで全エンドポイントの必要権限を確認でき、テストで網羅も検証できる。

**「行を読まないと決まらない判定」だけはハンドラに置く。** ミドルウェアはプロジェクトまでしか知らず、どの行を触るかを見ないためである。**現在2例ある。**

| 例 | 判定 | 出典 |
|---|---|---|
| コメントの編集・削除 | 自分が書いたものか（`comment.edit_own`） | `ApiDesign.md` 9.8 |
| チケットの状態遷移 | 呼び出し元がエージェントのとき、担当が自分の所有者か | `ApiDesign.md` 9.6 の検証6 |

**3例目が現れたら、付録A 論点①（権限の全体像の再整理）をそこで行う。** 2例までは「宣言でほぼ足りる」と言えるが、3例あるものは規則である。

**1本のルートでクエリの値により必要権限が変わる場合は `RequirePermissionUnlessQuery`（`middleware/authz.go`）を使う。** 宣言をルート定義に残すためのもので、**読み取り専用で、素通しする部分集合を意図して公開しているものにだけ使う。** 認可が値の検証より先に走るため、権限の無い呼び出し元には `422` ではなく `403` が返る。

**フロント側（表示制御）**：ログイン時に実効権限の一覧を返し、ストアに保持する（`ApiDesign.md` 4.1）。

```
GET /api/v1/me
→ { actor: {...}, permissions: [...], projects: [{ key, role, permissions }] }
```

- **ルーターガード**：ページごとに必要権限を定義し、不足時は 403 ページへ
- **コンポーネント**：`v-if="can('ticket.close')"` でボタンを出し分ける

フロントの制御は**利便性のためのものであり、セキュリティ境界ではない**。権限判定の正本は常にサーバ側に置く。

### 6.4.5 権限判定の実装上の注意

- ログインごとに実効権限を計算し、セッションにキャッシュする。ロール変更時は当該ユーザーのキャッシュを無効化する
  - 置き場は `access_token.cached_permissions` / `permissions_cached_at`（`DbDesign.md` 6.2）。キャッシュするのは**システムロールの層のみ**で、プロジェクトロールの層は毎回引く
  - 無効化は**アクター単位で全トークンを対象に**行う。複数のセッションやAPIトークンのうち1本だけ消しても、残りから旧権限で通れてしまう
  - 併せて **TTL 5分**を設ける。ロールの割り当てそのものを変えた場合（`role_permission` のシードをマイグレーションで書き換えた場合）は「当該ユーザーの無効化」では届かないためである。無効化が主、TTL は取りこぼしの安全弁という関係にする
- 権限不足は `403` を返し、`audit_log('permission.denied')` に記録する。**存在を隠したい資源（他プロジェクト）は `404` を返す**
  - **トークンが特定のプロジェクトに紐づく場合（`access_token.project_id`）、他プロジェクトは `404`。** 当人がそのプロジェクトのメンバーであっても、アドミニストレータであっても通さない。6.5 がエージェントトークンに禁じる「他プロジェクトへのアクセス」の実施点はここである。ロール・権限とは独立した軸で、トークンスコープが「何をしてよいか」を絞るのに対し、こちらは「どのプロジェクトに対してか」を絞る
- **不変条件をAPI側で守る**：自分自身のロール変更・無効化・削除の禁止、最後の**有効な**アドミニストレータの降格禁止（`ApiDesign.md` 6.4 / 6.5）。UIだけで防ぐと、直接APIを叩いた際に誰もログインできないインスタンスが生まれうる
- エージェントからの操作は、権限に加えて①ワークフローの `is_agent_reachable`、②サーキットブレーカーの状態、③**担当が自分の所有者であること**（状態遷移のみ。`ApiDesign.md` 9.6 の検証6）、を追加で検証する
  - ③の意図は、**人が引き受けていないものをエージェントが動かさない**ことである。判定の材料は `ticket.assignee_id`（リースは構想。8.2）

## 6.5 エージェントの認証

人間ユーザーとは別系統として設計する。

| 項目 | 方針 |
|---|---|
| principal | `actor(kind='agent')` + `agent` テーブル。人間アカウントの借用をしない |
| **所有者** | **`agent.owner_actor_id`（必須）。エージェントはプロジェクトメンバの誰かに紐づく**（`DbDesign.md` 8.2.1） |
| **権限** | **所有者から導く（委譲）。** 下の式を参照 |
| トークン | `access_token(token_type='agent')`。プロジェクトスコープ必須、有効期限必須。接頭辞は `pb_agt_` |
| 発行 | **本人が自分の設定から**（`/me/agents`。`ApiDesign.md` 4.5、`Requirements.md` 10.9.1 系統B）。**発行時に一度だけ全文表示** |
| スコープ既定 | `project.view` `ticket.view` `ticket.create` `ticket.transition` `ticket.assign` `comment.create` `doc.view` `agent.run` `ticket.reference.edit` `ticket.self_edit`。**語彙は権限カタログのキーそのものである**（6.4.1）。**発行時に `doc.edit` と `ticket.actual_point.edit` を足せる**（`ApiDesign.md` 4.5.3 の許可リスト） |
| 禁止 | `ticket.close`、`knowledge` の直接更新、他プロジェクトへのアクセス。`doc.edit` は既定では与えず、発行時に追加できる |
| 信頼度 | `agent.trust_level` に応じて既定スコープを段階的に拡大（`Requirements.md` 10.10.3）。**実績の供給源が構想のため、既定値のまま使わない** |
| 失効 | 本人と管理画面から即時失効。サーキットブレーカー作動時は自動失効も選択可 |

**権限は所有者から導く。** エージェントは `app_user` の行を持たないため、6.4.1 の式のうちシステムロールの層が必ず空になる。そこで**所有者の層をそのまま使う**。

**認証時にもアクター種別とトークン種別を照合する。** エージェントは `token_type='agent'` だけを使い、人間は `session` または `api` を使う。旧 `/me/tokens` からエージェント名義の API トークンを発行できた経路があったため、発行時の制約だけに依存せず既存トークンも拒否する（`DbDesign.md` 6.21）。

```
実効権限 = ( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ トークンのスコープ
             └─ その人の持つ権限がベース              └─ エージェント独自の権限の整理
```

**これは「人間アカウントの借用」ではない。** principal もトークンも監査の `actor_id` も別のままで、**借りるのは資格情報ではなく権限の根拠**である。事故の追跡は「どのモデルが」ではなく「誰の環境で」から始められる（`Requirements.md` 10.10.3）。副次的に、**所有者を無効化するとその人のエージェントも同時に効かなくなる。** 詳細と自立エージェントへの広げ方は `DbDesign.md` 8.2.1。

**スコープ既定は権限カタログのキーで書く**（6.4.1 の積は権限キーどうしの完全一致で取る。`ApiDesign.md` 4.4.2）。
既定は `agent.run` / `comment.create` / `doc.view` / `project.view` / `ticket.assign` / `ticket.create` /
`ticket.reference.edit` / `ticket.self_edit` / `ticket.transition` / `ticket.view` の10件である。

**`ticket.self_edit` は既定に入れた**。`ticket.reference.edit` と同じ理由で、**起票したチケットを直すのは実装エージェントの通常の仕事**である。**`ticket.edit` は許可リストにも入れない**——9.5.2 の全項目を開けるので、**エージェントが `execution_mode` や `scope` を自分で緩められる**（`DbDesign.md` 6.13）。

**`doc.edit` は既定に入れないが、発行時に足せる。** `pb_put_doc` にこの権限が要る（8.2）が、載せるかは**そのエージェントが誰に付いているか**で決まる——PM のエージェントは持ち、実装だけを行うエージェントは持たない。**そもそも所有者が `doc.edit` を持たなければ、スコープに書いても積で消える**（持つのは `project_admin` だけである。`DbDesign.md` 8.1.4）。

**発行の口は許可リスト（既定 ∪ `doc.edit` ∪ `ticket.actual_point.edit`）を受ける**（`ApiDesign.md` 4.5.3）。本節の「誰に付いているかで決まる」を、発行時に表せる。**`ticket.close` は許可リストにも入れない**——本節の禁止のうち、`doc.edit` だけが「決まる」と書かれている。

**`agent.run` を既定に含める。** `DbDesign.md` 8.2.6 で `operator` / `project_member` / `project_viewer` へ配り直しており、所有者が持つ権限になった。

**`ticket.reference.edit` を既定に含める**。`pb_add_reference` が要求する権限で（8.2）、**作業の跡を残すのは実装エージェントの通常の仕事**だから既定に置く——`doc.edit` のように「誰に付いているか」で変わらない。**`ticket.edit` を既定にも許可リストにも入れない**：外部参照だけでなく本文・担当・期日の書き換えや並べ替えまで開いてしまうためで、そこを切り出すために 0027 で権限を新設した（`DbDesign.md` 6.12.1）。

**エージェントによるクローズ禁止はDBレベルでも担保する。** ワークフローの `done` ステータスは `is_agent_reachable = false`、遷移の `allowed_actor_kinds` は `["user"]`（`DbDesign.md` 7.4）。

## 6.6 ネットワークと転送

- アプリは既定で `127.0.0.1` にのみ公開する。コンテナ内は `0.0.0.0:8080` で待ち受け、公開範囲は compose の `ports` で制御する（`DbDesign.md` 3.2）
- Cookie は `HttpOnly` `SameSite=Lax`。HTTPS 提供時は `Secure` を付与
- CSRF：Cookie 認証の状態変更系リクエストに CSRF トークンを要求する。Bearer トークン認証の場合は不要（`ApiDesign.md` 2.4）
- CORS：既定で同一オリジンのみ許可
- **DBはアプリ実行時ロール `pb_app`（DML のみ）で接続する。** DDL権限を持つ `pb_owner` と分離し、実行時のSQLインジェクションでテーブルを落とせないようにする（`DbDesign.md` 3.4）

### 6.6.1 PB 自身で TLS を終端する

**PB は TLS を自分で終端できる。** 証明書を1枚も登録していなければ平文の HTTP で待ち受け、
**登録されていれば TLS で待ち受ける。** 前段にリバースプロキシを置く構成も従来どおり使える。

**この節は `Requirements.md` 10.10.2 の前提を変える。** あの要件は「`127.0.0.1` にのみバインドする」
と定めていたが、**localhost だけで使うなら TLS は要らない。** TLS が必要になるのは PB を端末の
外へ出すときで、それは「**OSS として小さな PJ を手助けする**」（憲章 `vision`）ために避けられない。
**10.10.2 を「既定は `127.0.0.1`、外へ出すなら TLS の終端を必須とする」へ改める。**

#### 証明書と秘密鍵の置き場は DB である（10.3 の第3層）

| 置き場 | 採否 | 理由 |
|---|---|---|
| **DB の `tls_certificate`（暗号化）** | **採る** | **スケールアウトでは全レプリカが同じ証明書を出す必要があり、更新が全台に届かなければならない** |
| Pod ごとのファイル | 採らない | 更新の瞬間に pod ごとで証明書がずれる。10.3 で `pb.env` の書き換えを落としたのと同じ理由 |

**秘密鍵は平文で DB に置かない。** `pg_dump` がそのまま鍵を運ぶためで、規約「秘密をコードや
文書に書かない」と同じ理由による。**DB の外にある鍵で列を暗号化する**（封筒暗号）。

#### 暗号鍵は PB が自動生成し、環境変数で上書きできる

**利用者の決定。** **既定では PB が初回に鍵を作って DB に保存する。**
利用者の操作は要らず、**画面だけで証明書を登録できる。**

**`PB_SECRET_KEY` を必須にしない。** 必須にすると、証明書を1枚登録するために環境変数の設定と
再起動を利用者へ要求することになり、「設定は WebGUI を第一の口とする」という方針と矛盾する。

| 項目 | 決めたこと |
|---|---|
| 既定 | **PB が初回に32バイトを生成し、`app_secret` へ保存する**（`DbDesign.md` 6.16）。**利用者の操作は無い** |
| 上書き | `PB_SECRET_KEY`（32バイトの base64）を与えたときはそちらを使う。**与えた側が強い** |
| 形式 | 32バイト。AES-256-GCM の鍵長そのもので、長さの検証が1行で済む |
| **代償** | **既定では `pg_dump` に鍵と暗号文の両方が入る。** これは隠さず**画面に表示する**——「バックアップの持ち出しから秘密鍵を守るには `PB_SECRET_KEY` を与えてください」 |
| 取り違えたとき | **行があるのに1枚も復号できなければ起動を失敗させる。** 鍵の取り違えは**時間が経っても直らない設定の誤り**であり、起動してしまうと利用者には「繋がらない」としか見えない。**日付のせいで有効なものが無い場合とは区別する**——あちらは時刻で変わるので、警告を出して起動する |
| 鍵の交換 | 行に `key_id` を持たせ、**復号に使った鍵を識別できるようにする。** 交換の手順はまだ作らない（再検討の条件は、鍵の漏洩が疑われたとき） |

**「DB の中身を復号する鍵を DB に置く」ことの意味を隠さない。** 暗号化の目的は
**バックアップやレプリカの持ち出しから守ること**であり、既定ではその保護が効かない。
**効かないことを画面に出すのが、この判断を成立させる条件である**——黙って既定で
上がると、利用者は守られているつもりになる。

**生成した鍵を画面にも API にも返さない。** 表示しても利用者に使い道がなく、
**返す経路を作ると漏れる経路が増える**だけである。

#### 有効にする前に、どこで待ち受けることになるかを示す

**TLS を有効にする前に、有効化後の待受を出す。** 加えて**証明書の名前（SAN）と、
アクセスに使うホスト名が合っているか**を突き合わせる。**ここがずれているとブラウザが
警告を出す**ので、押す前に気づける面が要る。詳細は `GuiDesign.md` 5.12.1。

#### 出す証明書は「有効なもののうち `notBefore` が最も新しいもの」

**利用者の決定。** チケットの本文は「旧証明書期限で切り替え」だったが、
**その形は有効な証明書が1枚も無い窓を作りうる**——新証明書の `notBefore` が旧証明書の
`notAfter` より後だと、その間どちらも出せない。

```
選定：now が [notBefore, notAfter] に入る行のうち、notBefore が最大のもの
```

| 状況 | 出すもの |
|---|---|
| 新旧2枚が重なって有効 | **新しいほう**（`notBefore` が大きい） |
| 新証明書がまだ有効でない | 旧証明書 |
| 旧証明書が切れ、新証明書が有効 | 新証明書 |
| **有効なものが1枚も無い** | **TLS ハンドシェイクを失敗させる**（下記） |

**`crypto/tls` の `GetCertificate` で毎ハンドシェイクごとに選ぶ。** 再起動もリロードの合図も
要らず、**画面から登録した瞬間から次の接続で効く。** 期限による切り替えも時刻の比較だけで
起きるので、切り替えのための仕掛けを持たない。

#### 有効な証明書が無いときに平文へ落ちない

**TLS で待ち受けている間に有効な証明書が無くなったら、ハンドシェイクを失敗させる。**
平文へ落とす案は採らない——**利用者は HTTPS で公開しているつもりなので、黙って平文に
なるのが最悪の結果である。**

**このとき画面も API も見えなくなるので、復旧の口を2つ用意する。**

| 口 | 使い方 |
|---|---|
| 画面から `tls_enabled` を `false` にする | **再起動は要らない**（10.3 の待受の張り替え）。ただし**画面が見えている必要がある** |
| `PB_TLS_ENABLED=false` を与えて起動し直す | 第2層の設定に**環境変数が勝つ**ので、DB を触らずに平文へ戻せる（10.3 の優先順）。**画面へ到達できないときの口である** |
| 期限切れの証明書を `psql` で消す | 最後の手段 |

**`cookie_secure` の復旧経路と同じ考え方である**（10.3）。画面から直せなくなる設定には、
環境変数で上から押さえる口を必ず残す。

#### 平文と TLS を同時に待ち受けない

**待受は1つだけである。** 2つ開くと「どちらのポートで来たか」で `Secure` 属性や
公開エンドポイントの組み立てが分岐し、**同じ設定が2つの意味を持つ。**

**HTTP から HTTPS への転送は作らない。** 転送のためだけに平文の待受を
開くことになり、上の判断と矛盾する。**再検討の条件は、利用者が「http で来た人を
救いたい」と言ったとき**である。

#### 自己署名証明書は持ち込みだけを受ける

**利用者の決定。** PEM のアップロードだけを受け、**PB では生成しない。**
手順は `Development.md` 13章に書く。**フォーマル証明書と自己署名証明書を区別しない**
——どちらも PEM の証明書と秘密鍵であり、**PB は検証の連鎖を辿らない**（それをするのは
接続するクライアントである）。

**`cookie_secure` は自動判定しない**（従来どおり）。TLS を自分で終端していても、
**前段にプロキシを置く構成では平文で届く**ため、リクエストの TLS 有無から決めると
「HTTPS で公開しているのに `Secure` が付かない」を招く。

### 6.6.2 セキュリティヘッダ

**全応答にミドルウェアで付ける。** 値の一覧は `ApiDesign.md` 2.12。

**入れる理由は「いまの防御が崩れた日の受け皿」である。** Markdown は `lib/markdown.ts` 一本
（`html: false` ＋ `dompurify`）を通り、`:href` にユーザー入力が入る箇所はすべて `isWebUrl`
（`^https?://`）で守っている。**そのガードを誰かが外した日、あるいは `v-html` を1箇所素通しで
足した日に、止めるものが要る。**

**値はビルドの実物から決める。** ビルド後の `index.html` はインラインスクリプトを持たず（Vite が
外部ファイルを参照する）、CSS は外部リソースを読まず、client のソースに外部ドメインの
参照も無い。**したがって `script-src 'self'` で足りる。**

**`style-src` にだけ `'unsafe-inline'` を許す。** Vue の `:style` 束縛と CodeMirror が
実行時にスタイルを当てるためである。**これを外すには `:style` を全部 class へ書き換える
必要があるうえ、CodeMirror が注入する分は直せない**ので、費用に見合わないと判断した
。**スクリプトの実行を止めるのは `script-src` であり、そちらは
緩めていない。**

#### `Strict-Transport-Security` は出さない

**PB は http でも動く設計である**（`tls_enabled` の既定は false）。HSTS は
**一度ブラウザが受け取ると、その後 http で開けなくなる**——開発端末や、TLS を
無効に戻した環境で締め出しが起きる。これは 10.3 が「締め出されうる設定」に
払っている注意と同じ性質の事故である。

**再検討の条件**：**PB を TLS 必須で公開する構成を正式に支持したとき**
（いまは `tls_enabled` を切り替えられる形になっている）。そのとき、
TLS が有効なときだけ出す形にできるかを併せて見る。

#### 実機で確かめた

**画面を回して CSP 違反が1件も出ないことを見た。** DevTools の Console を
`Refused` で絞って **0 件**である（CSP 違反はこの語で始まる）。

| 見たもの | 結果 |
|---|---|
| ログイン画面 | Vue がマウントする。**`script-src 'self'` が JS の実行を妨げていない** |
| チケット詳細・Docs（Markdown と CodeMirror） | 描画・編集ともに違反なし |
| TOTP の登録 | **QR が出る**（`img-src data:` が効いている） |
| **リカバリコードの保存** | **ファイルが落ちる**（後述） |
| 画面 / 静的アセット / API の 401 / 未ビルド時の 503 | 4つのヘッダがすべて付く |

**`blob:` は許さなくてよい。** リカバリコードの保存は `URL.createObjectURL` で
`blob:` の URL を作るが（`RecoveryCodesDialog.vue`）、**`default-src 'self'` のまま
保存できた**——**CSP の `a[download]` によるダウンロードはフェッチディレクティブの
対象外**である、という仕様どおりの挙動である。**実機で確かめた事実**として残す。

**コンソールのノイズに注意する。** 検証時、Chrome 拡張機能由来のエラー
（`FrameDoesNotExistError` など、`background.js` から出るもの）が70件近く出ていた。
**PB とは無関係なので、`Refused` で絞ってから数えること。**

## 6.7 多要素認証（TOTP）

**本節が MFA の正本である。** 表定義は `DbDesign.md` 6.18、API は `ApiDesign.md` 3.4・4.6・6.9、
画面は `GuiDesign.md` 5.8 を参照。

**節を6章の末尾に置くのは、既存の節番号を動かさないためである。** 内容は 6.2〜6.3 の続きだが、
`6.4` 以降を繰り下げると、設計文書とコードの相互参照が全面的に動く。

### 6.7.1 何を第2要素にするか

| 項目 | いま | 将来 |
|---|---|---|
| 方式 | TOTP（RFC 6238） | —（**パスキーは第2要素にしなかった**。6.8.1） |
| 単位 | 利用者1人につき最大5件の認証器 | 同左 |
| 締め出しの手当て | リカバリコード10本 ＋ 管理者による解除 ＋ `pb admin mfa-reset` | 同左 |
| 適用範囲 | **ログインのときだけ** | 同左 |

**`user_identity` の抽象（6.1）には載せない。** MFA は「subject を特定する手段」ではなく、
**特定できたあとに重ねる関門**である。載せると、プロバイダを増やすたびに MFA の実装が増える。
**利用者に1つ結び付ける**ので、OIDC（構想）を足しても TOTP の実装は動かない。

**既に発行済みの API トークン・エージェントトークンには第2要素を要求しない。**
Bearer は対話的な認証を伴わない経路であり（6.2.2）、そこで6桁のコードを求める先が無い。
**トークンを持つことがそのまま権限である**という前提を、ここでは変えない。
**再検討の条件は、利用者が「トークンの発行そのものに第2要素を要求したい」と言ったとき**である。

### 6.7.2 TOTP のパラメータ

| 項目 | 値 | 根拠 |
|---|---|---|
| アルゴリズム | HMAC-SHA1 | RFC 6238 §1.2 の既定。認証アプリの事実上の共通解である |
| 桁数 | 6桁 | 同上 |
| 刻み | 30秒 | 同上 |
| 許容する窓 | 前後1刻み（計90秒） | RFC 6238 §5.2 が推奨する時計ずれの吸収。2刻み以上は総当たりの的を広げる |
| 共有秘密 | 20バイトの乱数を Base32（パディング無し） | RFC 4226 §4 の推奨（160ビット）。Base32 は手入力とQRの両方で使える |

**この5項目を列に持たない。** 秘密を作るのは PB だけなので、行ごとに違う値が入る経路が無い。
**他所で作られた TOTP を取り込めるようにする日が来たら、そのとき列を足す**——それが
再検討の条件である。

**同じ刻みのコードを2回受け付けない。** 照合が通った刻みの番号を `last_used_step` に残し、
それ以下を拒否する。**画面の後ろで盗み見たコードが30秒間使い回せる**のを防ぐ。

### 6.7.3 共有秘密は暗号化して保存する

**`app_secret` の鍵で AES-256-GCM で封じる**（6.6.1 と同じ鍵、同じ方式）。

**平文で持たない理由は、TLS の秘密鍵と同じである**（`DbDesign.md` 6.15）——
共有秘密が読めれば、その人のコードを何度でも作れる。**パスワードのハッシュと違い、
TOTP の共有秘密は検証のために平文が必要**なので、ハッシュではなく可逆の暗号を使う。

**代償も同じである**（`DbDesign.md` 6.16「代償を隠さない」）。既定では `pg_dump` に鍵と
暗号文の両方が入る。**`PB_SECRET_KEY` を与えたときだけ、その保護が効く。**

**鍵が無ければその場で作る。** 解決は `ResolveKey`（6.6.1）に委ね、MFA のために別の鍵を
持たない。**利用者の操作を要らなくする**という方針（10.3）と揃える。

### 6.7.4 ログインは2段になる

```
1. POST /api/v1/auth/login  { email, password }
     ↓ 6.2.1 の手順2〜5（ロック確認・Argon2id 照合）は変わらない
2. 確定済みの認証器が1件も無ければ、従来どおり手順6〜8（セッション発行）へ進む
3. 1件以上あれば、セッションを発行せず挑戦を1件作る
     ├─ 平文の挑戦トークン = "pb_mfa_" + base64url(random 32 bytes)
     ├─ DB には SHA-256 ハッシュのみ保存（5分で失効、試行5回で失効）
     └─ 200 { mfa_required: true, mfa_token, methods, expires_at }  ← Cookie は出さない
4. POST /api/v1/auth/login/mfa  { mfa_token, code }（または { mfa_token, recovery_code }）
     ├─ 挑戦を引く → 期限・試行回数・消費済みを確認
     ├─ TOTP を照合（前後1刻み）、またはリカバリコードを1本消費
     ├─ 失敗 → attempts++、audit_log('login.mfa_failure')、401
     └─ 成功 → 挑戦を消費済みにし、6.2.1 の手順6〜8 をそのまま行う
```

**挑戦を `access_token` に置かない。** 認証ミドルウェア（6.2.2）は `token_hash` で引いた行を
`token_type` で絞らずアクターを載せる。**`access_token` に中間状態を置けば、挑戦トークンが
そのまま API 全体を通る資格情報になる**——パスワードだけで第2要素を飛ばせてしまう。
**専用の表なら、その経路が構造上存在しない。** 棄却したのは `token_type` に `'mfa'` を足し、
ミドルウェア側で弾く案である。**弾き忘れが即座に認証の穴になる**形は採らない。

**パスワードを2回送らせる案も棄却した**（`POST /auth/login` に `totp_code` を足す形）。
表は増えないが、**1回のログインでパスワードが2往復する**。

**第2要素の失敗で `failed_attempts` を増やさない。** パスワードは既に正しいので、
ここで数えるとコードを打ち間違えた本人がアカウントごとロックされる。**総当たりは
挑戦ごとの5回と、IP 単位のレート制限（`ApiDesign.md` 2.9）で抑える。**

### 6.7.5 締め出しの手当てを3つ持つ

**TOTP を登録して認証アプリを失うと、手当てが無ければそのアカウントは永久に入れない。**

| 口 | 誰が | いつ効くか |
|---|---|---|
| リカバリコード10本 | 本人 | **認証器を失ったとき。** MFA を最初に有効にした瞬間に1回だけ表示する |
| 管理者による解除 | `user.manage` を持つ人 | 本人がコードも失ったとき。`ApiDesign.md` 6.9 |
| `pb admin mfa-reset --email` | 端末を触れる人 | **管理者が1人しかおらず、その人が締め出されたとき。** 画面側の口が成立しない |

**3つ目が要るのは、PB が小さなプロジェクトを対象にしているためである**（価値観）。
「他の管理者に頼む」が常に選べるとは限らない。**環境変数で上から押さえる復旧経路と
同じ位置づけ**であり（10.3）、端末と DB への到達を要求する最後の手段である。

**リカバリコードは Crockford Base32 で10文字（50ビット）を10本。**
`I` `L` `O` `U` を除く32文字を使うのは、**紙に書き写す値だから**である——
`I` と `1`、`O` と `0` は手書きで区別できない。 `access_token` と同じく
SHA-256 のハッシュだけを保存し、**平文は発行の1回しか出さない**（`ApiDesign.md` 4.4.1 と
同じ作法）。**Argon2id を使わないのは、コードが利用者の記憶に由来しないためである**
——50ビットの乱数に辞書攻撃は効かない。

**1本使ったら消費済みにする。** 残数を画面に出し、**0本になったら再発行を促す。**
**最後の認証器を削除したら、リカバリコードも消す**——MFA が無効な状態で残っていても
入口が無く、次に有効化したときに古いコードが通るのは筋が悪い。

## 6.8 パスキー（WebAuthn）

**本節がパスキーの正本である。** 表定義は `DbDesign.md` 6.19、API は `ApiDesign.md` 3.5・3.6・4.7・6.10、
画面は `GuiDesign.md` 5.1・5.6.2・5.8 を参照。

### 6.8.1 パスワードの代わりであって、第2要素ではない

**パスキーは、メールアドレスもパスワードも入力せずにログインする手段である**。
ログイン画面の「パスキーでログイン」を押すと、端末がパスキーを選ばせて生体認証か PIN を求め、
**通ればそのままセッションを発行する。**

| 項目 | 値 |
|---|---|
| 位置づけ | **パスワードの代わり**（6.2.1 の手順2〜5.5 を置き換える） |
| 入力 | なし（discoverable credential。メールアドレスを入力させない） |
| 単位 | 利用者1人につき最大5件 |
| 本人の確認 | **UV（生体認証か端末の PIN）を必須にする** |
| attestation | `none`（機種を検証しない。MDS も使わない） |
| 適用範囲 | ログインのときだけ（6.7.1 と同じ） |

**第2要素（6.7）には使わない**。棄却したのは、パスワードのあとで
TOTP と並べる案である。**チケットのゴールは
「生体認証を使ってログインできる」であり、パスワードを打ったあとに生体認証を足しても手間は減らない。**
**再検討の条件は、利用者が「パスキーを第2要素としても使いたい」と言ったとき**である。
登録は discoverable で行っているので、そのとき登録し直しは要らない。

**パスキー1回で多要素を満たす。** 端末を持っていること（所持）と、生体認証か PIN（生体または知識）を、
認証器が1回の操作で確かめる（NIST SP 800-63B の多要素暗号認証器に当たる）。
**UV を必須にするのはこのためである**——UV の無いパスキーは所持の1要素しか示さない。

**メールアドレスを入力させないので、アカウントの有無が漏れない。** 挑戦は誰に対しても同じ形で作り
（`allowCredentials` を空にする）、失敗は理由によらず同じ `401` を返す。

**パスワードの無いアカウントは作らない。** パスキーは既存の利用者が追加で登録するものであり、
`local_credential` を持たない利用者を生む経路は無い。**パスキーを失ってもパスワードで入れる**ので、
6.7.5 のような締め出しの手当ては要らない。

### 6.8.2 ログインの流れ

```
1. POST /api/v1/auth/passkey/options       ← 認証不要。IP 単位のレート制限（ApiDesign.md 2.9）
     ├─ 挑戦を作り、webauthn_challenge に purpose='login' で保存する（5分で失効）
     └─ 200 { options: { publicKey: { challenge, rpId, userVerification: "required", ... } }, expires_at }
2. ブラウザ：navigator.credentials.get(options) → 端末がパスキーを選ばせ、生体認証か PIN を求める
3. POST /api/v1/auth/login/passkey  { credential }
     ├─ clientDataJSON の challenge で挑戦を引く → 期限・消費済みを確認
     ├─ 挑戦を消費済みにする（検証より先に行う）
     ├─ credential_id で user_passkey を引く → actor.is_active を確認
     ├─ go-webauthn で検証（origin・rpIdHash・UV・署名・user handle・BE フラグ・sign count）
     ├─ 失敗 → audit_log('login.passkey_failure')、401
     └─ 成功 → sign_count・フラグ・last_used_at を更新し、6.2.1 の手順6〜8 をそのまま行う
```

**TOTP を登録している人にも、パスキーのあとで第2要素を求めない**。
6.8.1 の「パスキー1回で多要素を満たす」が根拠である。求めると、TOTP を持つ人にとって
パスキーの利点（入力が減る）がほとんど残らない。

**パスワード失敗のロック（`local_credential.locked_until`）はパスキーのログインを止めない。**
ロックはパスワードの総当たりへの手当てであり、パスキーは総当たりできない。**メールアドレスを
知っていれば他人でもロックを掛けられる**ので、止めると本人の入口が1つ減るだけである。
**`actor.is_active = false` は止める**——無効化は、管理者が入口を全部閉じる操作だからである。

**`must_change_password` はそのまま応答に載せる。** パスキーで入った人にも、画面は変更を促す
（`GuiDesign.md` 5.8「要パスワード変更の誘導」）。

**挑戦は検証より先に消費する。** 同じ応答を並行して2回送られても、通るのは1回だけになる。
検証に失敗した挑戦は捨てられ、やり直しは手順1から始まる。

**sign count が逆行したら拒否する。** 保存値と今回の値のどちらかが 0 でなく、今回が保存値以下なら、
認証器が複製された疑いがある（go-webauthn の `CloneWarning`）。**同期されるパスキーは常に 0 を返す**ので、
この判定に掛かるのは物理的な認証器だけである。

### 6.8.3 RP ID と origin は Host ヘッダから導く

**パスキーは RP ID（ホスト名）に結び付く。** `localhost:8081` で登録したパスキーは、
`pb.localhost:8081` で開いた画面では使えない——ブラウザが候補に出さない。

**RP ID は要求の Host からポートを除いた値、origin は `publicBaseURL`（`ApiDesign.md` 5.7.1）と
同じ組み立てにする**。28a・28b が公開 URL を Host から組み立てているのと
同じ暫定であり、**設定項目は足さない。**

**Host を信じてもフィッシングの穴にはならない。** RP ID と開いているページの origin の対応はブラウザが
強制するので、別のホストで開いた偽の画面では、PB の RP ID に結び付いたパスキーを使えない。

**登録時の RP ID を `user_passkey.rp_id` に残す。** 画面はいま開いているホスト名と比べ、違うものに
「このアドレスでは使えません」と出す（`GuiDesign.md` 5.8）。**残しておかないと、あとで RP ID を
設定にしたとき、どのパスキーが使えなくなるかを数えられない。**

**IP アドレスは RP ID になれない。** WebAuthn の RP ID は有効なドメインでなければならず、
go-webauthn v0.18 も拒否する。**`http://127.0.0.1:8080` で開いた画面ではパスキーを使えない**ので、
画面は理由を出してボタンを押せなくする。dev で試すときは `http://localhost:8080` で開く（`Development.md` 8章）。

**再検討の条件は、Host から導けなくなったとき**である（リバースプロキシの背後に置いたとき、または
参加者が別々のアドレスで同じインスタンスを見るようになったとき）。そのときは RP ID を公開エンドポイントと
一緒に設定にし、`rp_id` が設定値と違うパスキーの件数を画面に出す。
**RP ID を変えると、それまでのパスキーは全部使えなくなる。**

### 6.8.4 `user_identity` には載せず、`app_user` に吊る

**パスキーは本人を特定する手段である**（6.1 の①に当たる）。**ただし結び付く先は `user_identity` ではなく
`app_user` にする**。

**`user_identity` は「どのプロバイダの誰か」を1行で表す表である。** 列の多くは IdP のためにあり
（`auth_provider` の JIT・ロールマッピング・許可ドメイン）、パスキーではどれも使われない。
パスキーは1人に複数あり、端末ごとに増えて消える。**載せると `auth_provider.type` の CHECK を広げることになり、
`GET /auth/providers`（`ApiDesign.md` 3.3）がパスキーをログイン画面の IdP ボタンとして返す形になる。**

**6.1 の3段は崩れない。** ①がパスキーの検証（credential_id と user handle で利用者を特定する）であり、
**②で引く先が `user_identity` ではなく `user_passkey` になるだけ**で、③はパスワードと同じ経路を通る。

**`user_mfa_credential`（6.7）にも載せない。** あの表は「特定したあとに重ねる関門」であり
（`DbDesign.md` 6.18）、置くとログインの分岐（6.7.4 の手順2）がパスキーを第2要素として数えてしまう。

**user handle は `actor.id`（ULID の26文字）をそのまま使う。** WebAuthn は64バイト以下の不透明な値で、
個人を特定する情報を含まないことを求める。**ULID は個人を特定する情報を持たず、すでに API で出している識別子**なので、
別の乱数を列に持たない。

### 6.8.5 検証を go-webauthn に任せる

**CBOR の解析と署名の照合を自前で書かない**。攻撃者が形を決められる
バイナリの解析器であり、TOTP（6.7。約80行）や MCP（8章）を自前にした判断とは重さが違う。

**go-webauthn v0.18.1 は go 1.26.0 を要求するので、Go の最低版を 1.24 から 1.26 へ上げた**（3.1）。
`server/tools/go.mod` は据え置く——ツールは隔離してあり（`Development.md` 10.2）、1.26 の Go で動かすことに支障は無い。

**挑戦は DB に置く**（`webauthn_challenge`。`DbDesign.md` 6.19）。プロセスを再起動しても、
ログインの途中にいる人を失敗させないためである。**中身は go-webauthn の `SessionData` を JSON にしたもの**で、
ライブラリを上げると形が変わりうる。**挑戦は5分で失効するので、上げる前の挑戦は流し切ってよい**
（ライブラリの MIGRATION.md が求める扱い）。

**attestation は `none` を求め、MDS（FIDO のメタデータサービス）を使わない。** PB は機種を選ばない。
**再検討の条件は、特定の認証器だけを許したいという要望が出たとき**である。

### 6.8.6 乗っ取りからの復旧

**パスキーはパスワード無しで入れる鍵である。** 乗っ取った人が1本登録すると、パスワードの変更・リセット、
全セッションの失効、第2要素の解除（`ApiDesign.md` 4.3・6.6・6.7・6.9）のどれもその鍵を消さない。

| 口 | 誰が | いつ効くか |
|---|---|---|
| 自分の設定から削除 | 本人 | 見覚えのないパスキーが一覧にあるとき（`GuiDesign.md` 5.8） |
| 管理者による全削除 | `user.manage` を持つ人 | 乗っ取りの疑いがあり、本人が一覧を確かめられないとき（`ApiDesign.md` 6.10） |

**パスワードのリセットでは消さない**。パスワードを忘れただけの人までパスキーを
登録し直すことになり、**パスキーで入れたはずの人の入口を減らす。** `ApiDesign.md` 6.9 が締め出しの原因ごとに
口を分けたのと同じ判断である。

**端末から叩く口（`pb admin mfa-reset` に当たるもの）は作らない。** パスキーを失ってもパスワードで入れるので、
管理者が1人だけの構成でも締め出されない。

**登録と削除に現在のパスワードを求めない**（`ApiDesign.md` 4.6.4 と同じ扱い）。**再検討の条件も同じである**
——セッションの盗用を想定した見直しを行うとき、パスワード変更・メール変更・第2要素の削除と**まとめて**
再認証の対象にする。

---

# 7. REST API 設計

**正本は `ApiDesign.md`。** 本章は方針の要約に留める。

## 7.1 方針

| # | 方針 |
|---|---|
| 1 | リソース指向。動詞は状態遷移など名詞で表せない操作のみ `POST /:id/<action>` |
| 2 | 権限判定はサーバが正本。全エンドポイントに必要権限を宣言する |
| 3 | 画面の1表示 = 1リクエストを目指す。N+1 の往復を作らない |
| 4 | エラーは `code` で機械可読に。メッセージ文字列でのマッチングを不要にする |
| 5 | 権限のないリソースは 403 ではなく 404 |
| 6 | 秘密（パスワード・トークンの平文）は発行応答に1回だけ含める |

## 7.2 定義済みの範囲

| 領域 | エンドポイント | 状態 |
|---|---|---|
| 認証・セッション | `/auth/login` `/auth/logout` `/auth/providers` | 確定 |
| 自分自身 | `/me` `/me/password` `/me/tokens` | 確定 |
| プロジェクト | `/projects` `/projects/check-key` `/projects/:key` | 確定 |
| ユーザー管理 | `/admin/users` 系 | 確定 |
| ロール・権限 | `/roles` `/permissions` | 確定 |
| チケット | `/projects/:key/tickets` 系、`/tags`、`/sprints`、`/stats`、`/activity` | 確定（`ApiDesign.md` 9章） |

**本表の「確定」は設計が確定した意味であり、実装済みという意味ではない。** `docs/design/openapi.yaml` に載るのは実装が済んだものだけである（役割と食い違い時の扱いは `ApiDesign.md` 1.3）。

---

# 8. MCPサーバ設計

**エージェントから見える面は MCP のみとする**（原則7）。REST API を直接叩かせる設計は採らない。理由は `Requirements.md` 10.10.1——`curl -H "Authorization: Bearer ..."` を組み立てさせた時点で、資格情報がモデルのコンテキストを通過する。

## 8.1 REST 層との責務分担

**MCP は REST API の薄いラッパである**（`Requirements.md` 10.3.1）。ビジネスルール・権限判定・検証は REST 層（`internal/httpapi`）に置き、**MCP 層は入出力の形を変えるだけ**にする。

| 層 | 持つもの | 持たないもの |
|---|---|---|
| REST | 権限判定、検証、状態遷移、トランザクション | エージェント向けの言い換え |
| MCP | ツール定義、description、応答の整形、トークン予算 | 独自のビジネスルール |

**同じ規則を2か所に書かない。** エージェントだけに許す・禁じることは、**権限（`doc.edit` 等）とワークフロー（`is_agent_reachable` / `allowed_actor_kinds`）で表す**——MCP 層の `if` で表さない。前者は DB に残り監査できるが、後者は経路を1つ増やすたびに漏れる。

## 8.2 ツールと必要権限

**正本は `Requirements.md` 10.3.2**（状態の列つきの一覧）。ここでは REST 側の対応と必要権限だけを示す。

| ツール | REST | 必要権限 |
|---|---|---|
| `pb_get_project` | `GET /projects/:key` | `project.view` |
| `pb_list_docs` | `GET /projects/:key/docs?outline=1` | `doc.view` |
| `pb_get_doc` | `GET /projects/:key/docs/*path?section=` | `doc.view` |
| `pb_get_task` / `pb_list_tasks` | `GET /projects/:key/tickets(/:seq)` | `ticket.view` |
| `pb_get_context` | **合成**（`GET /tickets/:seq` ＋ `GET /docs` ＋ `GET /docs/*path`。8.5.5） | `ticket.view` `doc.view` |
| `pb_create_ticket` | `POST /projects/:key/tickets` | `ticket.create` |
| `pb_update_ticket` | `PATCH /projects/:key/tickets/:seq` | **`ticket.self_edit`** |
| `pb_put_dod` | `GET|POST|PATCH|DELETE /projects/:key/tickets/:seq/dod` | **`ticket.self_edit`** |
| `pb_list_tags` | `GET /projects/:key/tags` | `ticket.view` |
| `pb_create_doc` | `POST /projects/:key/docs` | **`doc.edit`** |
| `pb_put_doc` | `PATCH /projects/:key/docs/*path` | **`doc.edit`** |
| `pb_post_note` | `POST /projects/:key/tickets/:seq/comments` | `comment.create` |
| `pb_add_reference` | `POST /projects/:key/tickets/:seq/references` | **`ticket.reference.edit`** |
| `pb_transition_task` | `POST /projects/:key/tickets/:seq/transition` | `ticket.transition` |
| `pb_list_transitions` | `GET /projects/:key/tickets/:seq/transitions` | `ticket.view` |
| `pb_submit_result` | `POST /projects/:key/tickets/:seq/reports`（`ApiDesign.md` 9.15） | `ticket.transition` |
| `pb_claim_task` / `pb_release_task`（**構想**） | リース（`DbDesign.md` 8.2.2） | `ticket.transition` |

### リースを持たず、状態遷移を開ける

**`pb_claim_task` / `pb_release_task`（リース）を持たない。** 汎用的なプロジェクト管理から見ると、
claim / リースという考え方は馴染まない。**設計上のリースは掲示であって錠ではない**——`Requirements.md` 10.3.3 自身が
「**第一の目的は、いま誰が触っているかを他の参加者に見せること**」「少人数運用では**緩やかな整合**で実害はない」と書いている。

リースが解こうとしていた3つは、次のように扱う（詳細は `DbDesign.md` 6.6）。

| 解こうとしていたもの | 扱い |
|---|---|
| 可視性 | **`assignee_id` ＋ `working_agent_id` ＋ `status_key` で足りる。** TTL 30分はエージェントのセッションの時間尺度で、PB が目指す分野横断のプロジェクト管理には合わない |
| 排他 | **発生しない。** `/pb-implement <seq>` は人が番号を指定して走らせる。エージェントが自律的に拾うのは `pb_next_task`（構想） |
| 詰まり防止 | 占有しないので詰まらない |

**代わりに、状態遷移を MCP に開ける。** 設計原則7 は「エージェントから見える面は MCP のみ」と定めるので、
REST にある状態遷移（9.6 / 9.7）にも MCP の口（`pb_transition_task` / `pb_list_transitions`）を置く。

**`pb_claim_task` / `pb_release_task` は構想である**（`Requirements.md` 10.3.2）。**再検討の条件は自律取得（`pb_next_task`）の実装である**——そのときは `working_agent_id` を「宣言」から「条件」へ格上げすれば足り、テーブルを足さずに済む。TTL による失効（`stale` の検知）が要ると分かった時点で `task_lease` の器を起こす。

**`pb_put_doc` は `doc.edit` を要求する。** エージェントのトークンにこの権限を載せるかは、**そのエージェントが誰に付いているか**で決まる（`Requirements.md` 10.10.3）。PM のエージェントは持ち、実装だけを行うエージェントは持たない。**発行時に許可リストから選ぶ**（`ApiDesign.md` 4.5.3）。

**トークンや接続情報を返すツールを一切持たない**（`Requirements.md` 10.3.1）。

## 8.3 エンドポイントとスコープ

```
/mcp/<project_key>
```

**URL パスにプロジェクトキーを含める**（`Requirements.md` 10.8.8）。これにより手順ファイルがプロジェクト非依存になり、すべてのリポジトリで同じ雛形を使い回せる。

**トークンのプロジェクトスコープと URL の整合はサーバが検証する。** 食い違えば `404`——6.4.5 が定める「トークンが特定のプロジェクトに紐づく場合、他プロジェクトは 404」の実施点がここである。

**エンドポイント自体の必要権限は `agent.run`。** 0019 がこのキーの意味を「自分に紐づくエージェントを MCP から走らせてよい」と定め、`operator` / `project_member` / `project_viewer` へ配り直した（`DbDesign.md` 8.2.6）。個々のツールの権限（8.2）はその内側で REST 層が判定する。

**Cookie では通さない。Bearer トークンだけを受ける。** `ApiDesign.md` 2.4 は「CSRF トークンを要求するのは Cookie 認証のときだけ」と定めており、MCP の口を Cookie に開けると、CSRF の検証を持たない `POST` が1本増える。**write 系ツールを持つので、それは穴になる。** MCP クライアントが PB の Cookie を持つことはない。

## 8.4 プロトコル

**自前で実装する**（公式 SDK を使わない。乗り換えを検討する条件は 8.6）。実装するのは**JSON-RPC 2.0 の4メソッドを話す HTTP ハンドラ**であって、MCP の全機能ではない。

| 項目 | 採るもの |
|---|---|
| トランスポート | Streamable HTTP。**`POST /mcp/<project_key>` の1経路だけ**を実装し、応答は常に `application/json` |
| 受けるメソッド | `initialize` / `notifications/initialized` / `tools/list` / `tools/call` / `ping` |
| 実装しないもの | SSE ストリーム（`GET` は `405`）、セッション（`Mcp-Session-Id` を発行しない）、`resources` / `prompts` / サーバ発の通知 |
| プロトコル版 | クライアントが `initialize` で送った版が PB の知る値なら**それをそのまま返す**。知らない値なら PB の最新版を返す（クライアントが切断を選べる） |
| `MCP-Protocol-Version` ヘッダ | **検証しない**（無くても通す）。版の合意は `initialize` の応答で済んでおり、ヘッダで二重に判定すると、版を送らないクライアントが繋がらなくなる |

**サーバ→クライアントの通知を持たないので、SSE が要らない。** MCP の仕様は `POST` の応答を `application/json` で返してよいと定めており、read 系ツールはすべて1往復で完結する。**進捗通知や購読が要る面が現れたら、そこが 8.6 の再検討点になる。**

**エラーは2系統に分ける。**

| 起きたこと | 返し方 |
|---|---|
| 認証できない（トークンが無い・失効・所有者が無効） | **HTTP `401`**（`ApiDesign.md` 2.5 の本文）。JSON-RPC へ入る前に落とす |
| URL のプロジェクトへ到達できない／トークンのスコープ外 | **HTTP `404`**（同上。8.3） |
| `agent.run` を持たない | **HTTP `403`**（同上） |
| 本文が JSON として壊れている | JSON-RPC エラー `-32700` |
| `jsonrpc` / `method` が無い | `-32600` |
| 知らないメソッド | `-32601` |
| 引数の型・必須が違う／知らないツール名 | `-32602` |
| **ツールが呼んだ REST が 4xx / 5xx を返した** | **`tools/call` の結果に `isError: true`** を立て、本文に 2.5 の `code` と `message` を載せる |

**最後の1行が要点である。** 権限不足（`403`）や見つからない（`404`）は**プロトコルの誤りではなく、そのツール呼び出しの結果**であり、モデルが読んで利用者へ伝えるべきものである。JSON-RPC エラーで返すとクライアントが「サーバの不具合」として扱い、文面がモデルに届かない。逆に、知らないメソッドや壊れた引数は**クライアント側の不具合**なので JSON-RPC エラーで返す。

**MCP 層は REST を内部の HTTP 呼び出しで叩く**（同一プロセス内で同じ chi ルータへ渡す。`Authorization` ヘッダを引き継ぐ）。8.1 が定める「ビジネスルール・権限判定・検証は REST 層に置く」を、**経路として強制するため**である。ハンドラを直接呼ぶ形にすると `RequireProjectPermission` を通らない経路が生まれ、権限判定が2か所になる。

**内部で読んだ応答の日時は ISO8601 UTC に戻して返す**（pb-224）。REST はエポックミリ秒（`ApiDesign.md` 2.2）だが、MCP は ISO8601 のまま残す——エージェントはエポック値の換算を誤りやすい。戻すのは名前が `_at` で終わる項目（と証明書の `not_before` / `not_after`）の整数で、実装は `mcp/rest.go` の `isoTimes` 1か所である。

## 8.5 ツールの引数と応答

### 8.5.1 write 系

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_create_ticket` | `type`, `title`, `body_md?`, `priority?`, `parent_seq?`, `assignee_id?`, `tag_ids?`, `estimate_point?`, `estimate_hours?`, `start_date?`, `due_date?`（終日。`YYYY-MM-DD`）, `start_at?`, `due_at?`（時刻付き。時差を含む ISO8601） | `POST /projects/:key/tickets` | **要点だけ**（`id` / `seq` / `status` / `version` / `parent_seq`） |
| `pb_update_ticket` | `seq`, ＋ 上の任意引数から **`type` を除いたもの**、`actual_point` と `actual_point_version` の対（**送ったものだけ更新**） | `PATCH /projects/:key/tickets/:seq` | **要点だけ**（`seq` / `status` / `version` / `updated_at`） |
| `pb_put_dod` | `seq`, `add?[]`, `update?[]`, `delete?[]` | 9.9 の `POST` / `PATCH` / `DELETE` | 9.9 の一覧をそのまま |
| `pb_list_tags` | （なし） | `GET /projects/:key/tags` | 9.11 の一覧をそのまま |
| `pb_create_doc` | `slug`, `title`, `parent_path?`, `body_md?`, `sort_order?` | `POST /projects/:key/docs` | **要点だけ**（`path` / `version` / `updated_at`） |
| `pb_put_doc` | `path`, `body_md`, `change_reason?` | `GET` してから `PATCH /projects/:key/docs/*path` | **要点だけ**（`path` / `version` / `updated_at`） |
| `pb_post_note` | `seq`, `body_md`, `kind?` | `POST /projects/:key/tickets/:seq/comments` | **要点だけ**（`id` / `kind` / `created_at`） |
| `pb_add_reference` | `seq`, `repository`, `branch?`, `commit_sha?`, `url?`, `label?`, `note?`, `kind?` | `POST /projects/:key/tickets/:seq/references` | 9.10.2 の1件をそのまま |

**引数の名前は `ApiDesign.md` の本体フィールドに揃える**（`body` ではなく `body_md`、`parent` ではなく `parent_seq`）。8.5 の冒頭が述べるとおり、名前が一致していればエージェントは迷ったときに設計文書を引ける。

**`pb_create_doc` の `slug` は `^[a-z0-9][a-z0-9-]{0,63}$` に従う。** 大文字や `.md` は付けない。同じ親の下に同名があれば REST の `409 already_exists` を返す。`doc.edit` を持つトークンだけが作成できる。配布する接続設定の自動承認一覧には入れず、書き込みのたびに確認する。

**`assignee_id` は `me` を受ける。** read 系の `assignee` と同じ写し方をする（下記）——**エージェントはアクターの ULID を知らない**ため、`me` を通さないと担当を付ける経路が実質無い。ULID をそのまま渡すこともできる。

**開けない引数がある。** `pb_post_note` は `in_reply_to` を渡せない——**コメントの ULID を得る経路が無い**ためである。**増やすときは本表を先に直す。**

| | 判定 |
|---|---|
| 見積2種・日付2種 | **開ける。** 数値と日付であって ULID ではない |
| `tag_ids` | **開ける。** `pb_list_tags` で**列挙できる** |
| `sprint_id` | **開けない。** **どの経路からも書けない**（`ApiDesign.md` 9.5.2 の `use_sprint_endpoint`）。所属を動かすのはスプリントの開始・終了だけである（9.12.1 / 9.12.2） |

**`actual_hours` も開けない。** `pb_submit_result` の `cost.wall_clock_min` と二重になり、**どちらが正本か決まらない**。

**`pb_list_sprints` は足さない。** 設定できない以上、列挙する用途が無い——**読むだけなら `pb_get_task` の応答が `sprint: {id, name}` を返している**（9.5.1）。

#### `pb_update_ticket` — 起票したあと直す

**「作れるものは直せる。ただし `type` を除く」を線にした。** `pb_create_ticket` が受ける集合と揃えてあり、**起票時に選べる項目を直せないのは筋が通らない**——が、**種別の切り替えは人が行う**。タスクをエピックへ変えると、その行はバックログから消えてフィルタの選択肢になる（`GuiDesign.md` 5.4）ので、**記述を整えるつもりで盤面の見え方を変えてしまう。**

**塞いでいるのは REST 側である**（9.5.2 の `ticket.self_edit` が `type` を受け付けない）。**MCP 層で引数を落としているのではない**——8.1 の「MCP に独自の規則を置かない」を保つ。

**部分更新である**（送った項目だけ）。`PATCH`（9.5.2）と同じ形にした。**`pb_put_doc` が全置換なのは `If-Match` の都合**であり、本文も同じにする理由は無い。

**`If-Match` は MCP 層が内部で取る。** 9.5.2 は `If-Match` を必須とするが、`pb_get_task` は `version` を返すので**エージェントが渡すこともできる**——それでも内部で取るのは、`pb_put_doc` と同じ形に揃えるためと、**読んでから書くまでの間に人が直したときに競合を検出できる**ようにするためである。**競合（`409`）は `isError` のツール結果**として返し、モデルが読み直してやり直せるようにする。

**`execution_mode` / `readiness` / `readiness_note` / `scope` は引数に無い。** REST 側も `ticket.self_edit` では受け付けない（9.5.2）ので、**MCP 層で塞いでいるのではない**——8.1 の「MCP に独自の規則を置かない」を保つ。

#### `pb_put_dod` — 完了条件を整える

**いまある DoD に対する追加・編集・削除を、まとめて1回で受ける。** 9.9 は3本のエンドポイントに分かれているが、**DoD は「一覧をあるべき形にする」操作**であり、1件ずつ往復させると n 回の呼び出しになる。

**一覧の全置換にはしない。** 全置換だと送る側が全項目の ULID を持つ必要があり、**読んでから書くまでの間に他の人が足した項目を黙って消す。** 差分で受ければ、触っていない項目は残る。

**`is_satisfied` は引数に無い。** REST 側も `ticket.self_edit` では受け付けない（9.9）。**`pb_submit_result` が「盤面を動かさない」と決めた判断と正面からぶつかる**ためで、**完了の判定は人が行う。**

**応答は 9.9 の一覧をそのまま返す。** 何件足して何件消したかを MCP 層で組み立てない（8.1）。

**`pb_put_doc` は内部で2往復する。** 10.4 が `PATCH` に `If-Match` を必須とする一方、`pb_get_doc` は本文の Markdown しか返さないので（8.5 の表）**エージェントは `version` を持てない**。MCP 層が `GET` で読んで `If-Match` に載せる。**`409 conflict` は `isError` のツール結果**として返す——「他の人が更新したので読み直してやり直す」はモデルが判断できることであり、8.4 が定める「ツール呼び出しの結果」に当たる。

**これは MCP 層が独自のルールを持つことにはならない**（8.1）。楽観ロックの判定は REST 側のままで、MCP がしているのは**エージェントが渡せない値を、同じ REST から取ってくる**ことだけである。

**`pb_put_doc` は本文を全置換する。** 10.4 の `PATCH` がそうであり、章だけを差し替える口は無い。`?section=` は読む側（10.3）にしかない。**エージェントは `pb_get_doc` で全文を読み、直した全文を渡す。**

**書いた内容を応答で返さない**。起票・更新・文書・コメントの4ツールは、REST の応答から要点だけを返す。
**本文は送った本人の手元にある**ので、そのまま返すと同じ文をもう一度運ぶ。

| ツール | 残す項目 | 使いみち |
|---|---|---|
| `pb_create_ticket` | `id` / `seq` / `status` / `version` / `parent_seq` | `seq` を続けて `parent_seq` や `pb_transition_task` に使う |
| `pb_update_ticket` | `seq` / `status` / `version` / `updated_at` | 直ったこと（版が進んだこと）を確かめる |
| `pb_create_doc` | `path` / `version` / `updated_at` | 続けて `pb_put_doc` で直すときのパスを得る |
| `pb_put_doc` | `path` / `version` / `updated_at` | 10.3 の応答は**本文の全文**を持つ。版は `pb_get_context` の `charter_versions`（8.5.5）に使える |
| `pb_post_note` | `id` / `kind` / `created_at` | 書けたことを確かめる |

- **本文が要るなら読み直す**（`pb_get_task` / `pb_get_doc`）。ツールの説明文にもそう書く
- **失敗（403 / 409 / 422）は今までどおり本文ごと返す。** 理由の文と `details[]` が次の一手を決める
- **`pb_put_dod` と `pb_add_reference` は揃えない。** 前者の一覧は、足した項目の `id` を次の `update` / `delete` に渡すための材料であり、後者の1件は短く、送った本文が長くなる欄を持たない
- **REST の応答（9.3 / 9.5.2 / 9.8 / 10.4）は変えない。** 画面が使っている

**要点を選ぶことは、8.1 の「整形の規則を MCP 層に置かない」に反しない。** 8.1 の表は「応答の整形、トークン予算」を MCP の持ち物としており、
項目の選別は `pb_list_tasks` の軽量化（8.5.2）でも行っている。選別は規則ではなく形の変換なので、同じ規則が2か所に生まれることにはならない。
なお `PATCH .../docs` の応答は `revision_no` を持たない（10.3 の形）ので、「リビジョン番号」の代わりに `version` を返す。

**冪等キー（`idempotency_key`）は受けない**（`Requirements.md` 10.3.4。再検討の条件は 8.6）。

#### `pb_add_reference` — 作業の跡を積む

**表を新設していない。** `ticket_reference`（`DbDesign.md` 6.12）が 0016 から `repository` / `branch` / `commit_sha` を持ち、**6.12 自身が「`kind='code'` の書き手はエージェント」「作業の経過として追記されて積み上がる」と定めていた**。REST も 9.10.2 として実装済みで、**欠けていたのは MCP の口と権限だけだった。**

**`kind` の既定は `code` である。** 省略できるのは、このツールを呼ぶ動機がほぼ `code` だからで、**`doc` も渡せる**——塞ぐと 8.1 の「MCP 層に独自の規則を置かない」に反する。**既定を MCP 層に置くことは、本節に書いてある限り隠れた規則にならない。**

**必須は `seq` だけにしてある。** `repository`（`code` のとき）と `url`（`doc` のとき）の出し分けは 9.10.2 の検証がそのまま返す。**スキーマ側で条件付き必須を組むと、判定が REST と MCP の2か所に分かれる。**

**`sort_order` は開けない。** 9.10.2 が省略時に末尾（現在の最大値 + 10）へ置き、6.12 が「並びは `sort_order` ではなく `created_at` が実質の軸」と述べている。**積む順がそのまま並びになるので、エージェントが決める値が無い。**

**更新と削除の口は作らない。** 6.12 が「画面が持つのは**表示と削除**だけで、誤って積まれた行を人が始末できるようにする」と定めており、**エージェント側は追記専用**にする。積み間違いを人が消せる形を保つほうが、エージェントが自分の跡を消せることより価値がある。

**読む口も作らない。** `pb_get_task` の応答（9.5.1）が `references` を実数で含むため、**既に読めている**。

### 8.5.3 遷移系

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_list_transitions` | `seq` | `GET /projects/:key/tickets/:seq/transitions` | 9.7 の応答をそのまま |
| `pb_transition_task` | `seq`, `to`, `comment?` | `POST /projects/:key/tickets/:seq/transition` | **状態の要点だけ**（9.5.1 の応答から `seq` / `status` / `version` / `working_agent` / `updated_at` / `closed_at`） |

**`pb_list_transitions` を別のツールとして出す。** 9.7 は**遷移できない先も `allowed: false` と日本語の理由を付けて返す**ので、エージェントが盲目的に `pb_transition_task` を試して失敗を繰り返すのを防げる。9.6 の検証6（担当が所有者でない）もここに現れるため、**「なぜ進められないか」を1往復で知れる。**

**ただし着手（未着手→進行中）では、先に呼ばなくてよい**。着手のたびに同じ答え（`todo` カテゴリから
`in_progress` カテゴリへは `allowed: true`）を受け取っていた。**進められないときは、`pb_transition_task` の失敗の応答に
`pb_list_transitions` の `reason` と同じ文が返る**ので、失敗してから読んでも次の一手は変わらない（dev で確かめた）。

| 進められない理由 | `pb_transition_task` の応答 |
|---|---|
| 担当が所有者でない（9.6 の検証6） | `403 forbidden`「このチケットの担当者があなたの所有者ではないため、エージェントからは変更できません」 |
| 人しか通せない順路（`allowed_actor_kinds`） | `403 forbidden`「この状態への変更はエージェントからは行えません」 |
| 定義の無い遷移 | `409 invalid_transition`「未着手から完了へは直接進められません」 |
| ワークフローに無いキー | `422 validation_failed`、`details[].code = "unknown_status"` |

- **`to` には進行中にあたるキーを渡す。** 3つのテンプレートとも `in_progress` である（`DbDesign.md` 7.4）。キーが違えば
  `422 unknown_status` が返るので、そのときに `pb_list_transitions` で確かめる
- **着手以外の遷移（レビューへ・差し戻し・完了へ）は、今どおり先に呼ぶ。** 進める先がワークフローごとに分かれており、
  キーを知らないまま送ることになる
- **ツールの説明文と `/pb-implement` の手順4 にも、同じ条件を書く**（`Requirements.md` 10.8.6）

**`pb_get_task` の応答に畳まない。** 9.7 がエンドポイントを分けている理由がそのまま効く——遷移先は `PATCH` のたびに再計算が要り、詳細を読むだけの呼び出しにその計算を載せない。

**`to` はステータスキーである**（`in_progress` などの英字キー。表示名の「進行中」ではない）。`pb_get_project` の `workflow.statuses[]` と `pb_list_transitions` の `items[].key` がその語彙を返す。

**`comment` を開けている。** 9.6 が「同じトランザクションで `kind='progress'` のコメントを作る」と定めており、**遷移だけ通って経緯が残らない状態を作らない**ためである。`pb_post_note` を別に呼ばせると2往復になり、途中で落ちると遷移だけが残る。

**`done` への遷移は開けなくてよい。** 3つのワークフローテンプレートすべてで `done` は `is_agent_reachable = false` かつ遷移の `allowed_actor_kinds` が `["user"]` であり（`DbDesign.md` 7.4）、**DB とワークフローが拒む**（`Requirements.md` 10.8.6 の禁止事項）。MCP 層に `if` を置かない（8.1）。

**応答は状態の要点だけにし、チケットの本文を返さない**。
エージェントは着手前に `pb_get_task` で本文を読んでいるので、9.5.1 をそのまま返すと**遷移のたびに同じ本文をもう一度受け取る**。

- **残すのは、遷移の結果を確かめるのに要る6項目である。** `status`（どこへ進んだか）、`working_agent`（自分が記録されたか）、
  `version`（続けて書くときの `If-Match`）、`updated_at` / `closed_at`、`seq`
- **本文・完了条件・関連リンク（`body_md` / `dod` / `links` / `references` / `children` など）は落とす。** 要るなら `pb_get_task` を呼ぶ。
  ツールの説明文にもそう書く
- **失敗（403 / 409）は今までどおり本文ごと返す**（`failed`）。理由の文（`message`）が次の一手を決めるためである
- **REST（9.6）の応答は変えない。** 画面が使っている。MCP 層で項目を選ぶのは `pb_list_tasks` の軽量化（8.5.2）と同じ形で、
  8.1 の表が MCP の持ち物とする「応答の整形、トークン予算」に当たる

**遷移に成功すると `ticket.working_agent_id` が呼び出し元のエージェントになる**（`ApiDesign.md` 9.6）。**MCP 層は何もしない**——REST 側の副作用であり、人が画面から遷移したときと同じ経路を通る。

### 8.5.4 完了レポート系

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_submit_result` | `seq`, `status`, `artifacts?`, `dod_results?`, `findings?`, `failures?`, `proposed_subtasks?`, `knowledge_impact?`, `cost?` | `POST /projects/:key/tickets/:seq/reports` | 9.15 の応答をそのまま |

**引数を平らにする**（10.6.1 のレポートを1段のオブジェクトにしない）。理由は 8.5.1 と同じ——引数の名前が REST の
本体フィールドに一致していれば、エージェントは迷ったときに設計文書を引ける。`task_id` を
`seq` にするのも `pb_get_task` と同じ理由である（8.5.2）。

**`pb_submit_result` は状態を進めない**。26b で遷移が
`pb_transition_task` として独立したので、**完了レポートの提出と状態遷移を1つのツールに
混ぜない**。**チケットもクローズしない**——`done` は `is_agent_reachable = false` かつ
`allowed_actor_kinds = ["user"]` で、DB とワークフローが拒む（`DbDesign.md` 7.4）。
`Requirements.md` 10.8.6 の禁止事項が、MCP 層の `if` ではなくワークフローで守られている
（8.1）。

**コミット ID は `artifacts` に入れず、`pb_add_reference` で積む**（`ApiDesign.md` 9.15）。`artifacts` の説明文にもそう書いてある——**エージェントが読むのは設計文書ではなくツールの説明文**なので、置き場の案内は説明文に置かないと届かない。

**応答の `unsatisfied_dod` が、エージェントの次の一手を決める。** サーバはチケットの完了条件を
数え上げ、**レポートの `dod_results` に `passed: true` として現れなかった項目**を返す
（9.15）。`/pb-implement` の手順7 が「未充足の完了条件が返されたら修正して再提出する」と
定めており（`Requirements.md` 10.8.6）、その判断材料がこれである。

**完了条件のチェック（`dod_item.is_satisfied`）は動かない。** いま API が開けている DoD の型は
`manual` だけで、その定義は「人間がチェックを入れる」である（`Requirements.md` 10.5.2）。
**エージェントが立てると型の定義に反する**ので、盤面は人が動かす（9.15）。

**提出は完了レポートのコメントを1件作る**（`kind='progress'`）。**人がレポートを読む面が
コメント欄である**——チケット詳細のコメント欄は遷移・作業中のノート・人の議論が時系列に
並ぶ場所で、**完了の報告もそこに並ぶのが読む順序として自然である**。**整形は REST 層が行う**（8.1。MCP 層に置くと同じ規則が2か所に生まれる）。

**`proposed_subtasks` はレポートに残るだけで、チケットにならない。** 承認キュー（`proposal`）は
構想であり、人が読んで要ると判断すれば `pb_create_ticket`（26a）を呼ばせれば済む。
**承認なしに盤面が増える経路を作らない。**

### 8.5.2 read 系

**応答は REST の JSON をそのまま `content[0].text` に載せる**（`pb_get_doc` の本文だけは Markdown 生）。整形の規則を MCP 層に置くと、同じ規則が REST と2か所に生まれる（8.1）。フィールド名が `ApiDesign.md` と一致していれば、エージェントは迷ったときに設計文書を引ける。

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_get_project` | — | `GET /projects/:key` | 5.4 の応答をそのまま |
| `pb_list_docs` | — | `GET /projects/:key/docs?outline=1` | 10.2 の応答をそのまま（**目次と見出しだけ。本文は含まない**） |
| `pb_get_doc` | `path`, `section?` | `GET /projects/:key/docs/*path` | **本文の Markdown**。`section` を指定すればその章だけ |
| `pb_get_task` | `seq` | `GET /projects/:key/tickets/:seq` | 9.5.1 の応答をそのまま |
| `pb_list_tasks` | `status?`, `status_category?`, `assignee?`, `open?`, `parent?`, `staged?`, `per_page?` | `GET /projects/:key/tickets` | **軽量な部分集合**（下記） |

**予定は日付でも受ける**（pb-217）。REST は `start_at` / `due_at`（エポックミリ秒・半開区間）＋ `all_day` だけを受ける（`ApiDesign.md` 9.3.1）が、
エージェントに「締切日の翌日の0時」を計算させると誤る。**MCP 層がプロジェクトの基準タイムゾーンを引いて変換する**——`due_date`
（締切日を含む）は翌日の0時の `due_at` になり、`all_day = true` を添える。時刻付きの `start_at` / `due_at` を渡したときは
`all_day = false` で送る。日付と時刻付きを同じ呼び出しで混ぜると `-32602`。**読み出し（`pb_get_task` / `pb_list_tasks`）は、
終日のチケットに `start_date` / `due_date`（締切日を含む日付）を添える**——`due_at` だけを見ると締切が1日後に読める。

**`pb_get_task` の引数は `seq` である**（`Requirements.md` 10.3.2 は `id` と書いていた）。9.1 が「URL とチケット番号を一致させる」と定めており、人が画面で見る番号も `/pb-implement <id>` に渡す値も `seq` である。`id`（ULID）を名乗ると、ULID を渡す呼び出しが必ず出る。

**`pb_list_tasks` は軽量にする**（`Requirements.md` 10.3.2 の「チケット一覧（軽量）」）。9.2.2 の応答から次の11項目だけを残す。

```
seq / type / title / status / priority / assignee / working_agent
  / parent_seq / staged_at / due_at（終日なら due_date も） / updated_at
```

**`working_agent` を返す。** 排他が無いため（`ApiDesign.md` 9.6 は上書きを許す）、**同じ所有者の別のエージェントが既に触ったチケットを、それと知らずにもう一度進めることが起こりうる。** `/pb-onboard` の `pb_list_tasks(assignee=me)` で見えていれば、モデルが気づける。

落とすのは `id`（`seq` で足りる）、`sort_key`（画面の並べ替え用）、`tags` `sprint` `has_children` `reporter`、見積3種、`start_at` `closed_at` `version` `created_at` である。**ボードの状況把握に要らない項目を、一覧の件数ぶん掛け算しない。** 1件の詳細が要るときは `pb_get_task` が全項目を返す。

**`staged` は `true` のときだけ `staged=true` を送る**（`ApiDesign.md` 9.2.1「オンステージで絞る」）。オンステージの行とその配下が返る。**`false` は指定なしと同じに扱う**——REST は `true` しか受けない（`overdue` と同じ）ので、そのまま送ると `422` になる。「オンステージのチケットに着手して」と頼まれたときに、未完了の全件を取らずに済ませるための条件である。

**`assignee` に `me` を渡したときは、エージェントの所有者を指す。** エージェントのアクターに担当は付かない（担当は人が持つ）ため、`me` を文字どおり解釈すると `/pb-onboard` の「自分の担当を知る」が必ず0件になる。**6.5 の委譲が「権限の根拠は所有者」と定めているのと同じ理由で、担当の視点も所有者に置く**。引数の値を書き換えるだけなので、MCP 層が独自のルールを持つことにはならない（8.1）。人のトークン（`token_type='api'`）で叩いたときは、従来どおりその人自身を指す。

### 8.5.5 コンテキストパック

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_get_context` | `seq`, `charter_versions?` | `GET /projects/:key/tickets/:seq` ＋ `GET /projects/:key/docs?outline=1` ＋ 文書ごとに `GET /projects/:key/docs/*path`（判断の記録と、渡された版と一致した文書を除く） | **Markdown 1枚** |

**引数は `seq` である。** 理由は 8.5.2 の `pb_get_task` と同じで、人が画面で見る番号も
`/pb-implement <id>` に渡す値も `seq` である。

#### REST に専用のエンドポイントを作らない

**合成は MCP 層で行う。** 8.1 の表が**両方向から**この置き場を指している——MCP が持つものは
「**応答の整形、トークン予算**」であり、REST が**持たない**ものは「**エージェント向けの
言い換え**」である。コンテキストパックはその3つそのものである。

**必要権限が2つ（`ticket.view` `doc.view`）であることも、内側で2種類の REST を叩く形と
一致する**（8.2）。専用のエンドポイントを1本置くと、ルート定義に権限を AND で2段重ねる
ことになり、**どちらが何のための権限かがルート定義から読めなくなる**（6.4.4）。**読む画面が
無いまま公開 API が1本増える**ことでもある。

**代償は内部呼び出しの本数である**（チケット1 ＋ 目次1 ＋ 本文の数。判断の記録の本文は引かない）。同一プロセス内の
呼び出しなので、憲章が4文書のうちは測れる差にならない。**文書の数に比例する**ので、
8.6 の再検討条件（`history/` の移譲など）が来たときに、選定と一緒に見直す。

#### 応答は Markdown 1枚である

**read 系の他のツールと異なり、REST の JSON をそのまま載せない**（8.5.2）。**そこに写す元の
JSON が無い**——パックは複数の応答を組み直したものなので、「同じ規則が2か所に生まれる」
問題（8.1）が起きない。`pb_get_doc` が本文の Markdown を生で返しているのと同じ扱いである。

**読ませたいのは構造ではなく文である。** 10.4.1 が「押し付ける」と定めるものは、モデルが
読んで従う対象であって、モデルが解析して組み立て直す材料ではない。JSON にすると、キーと
引用符とエスケープに予算を使ったうえで、**モデルが同じ文章を頭の中で再構成する**ことになる。

#### 中身は5節。チケット本文と完了条件を入れない

```
# コンテキストパック — <key>-<seq>「<表題>」

## 1. スコープ境界と制約          ← 10.4.2 優先度1（ソース：チケット）
## 2. 実行の前提                  ← 同 優先度1（実行モード・Readiness）
## 3. 憲章                        ← 同 優先度2（文書がメモリの代わり）
## 4. 依存・関連するチケット      ← 同 優先度5
## 5. 足りないときの調べ方        ← 10.4.3 の 4

憲章の版：{"vision":2,"rules":7,…}  ← 次の呼び出しで charter_versions に渡す（下記）
```

**本文・完了条件・コメントを入れない。** 10.4.2 の優先度表が構成要素に挙げていないためであり、
**`/pb-implement` は手順1 で `pb_get_task` を先に呼んでいる**（`Requirements.md` 10.8.6）。
**同じ本文を2回運ぶと、押し付けたいものが薄まる。**

**スコープ境界が未設定のときは、空欄にせず文を出す**——「このチケットに境界は設定されて
いない。境界の外かもしれない変更が要ると判断したら、実装せず利用者に相談すること」。
境界を書くのは人であり（`ApiDesign.md` 9.5.2）、**多くのチケットは空のまま**で
あり、空欄を見たモデルが「制約が無い」と読むのは、境界が無いことより悪い。

**憲章は文書ごとの `pack_mode`（`full` / `outline` / `none`）に従って載せる。** `full` は全文、`outline` は目次と `pb_get_doc` での引き方、`none` は掲載しない。新規文書の初期値は `outline`。既存文書は 0042 の移行で、従来の掲載状態を引き継ぐ。文書の場所や名前で掲載方法を判定しない。

**親の設定は子孫の上限になる。** 親が `none` なら子孫も載らず、親が `outline` なら子孫の `full` も目次だけになる。親が `full` のときは子それぞれの設定に従う。掲載しないパスと目次だけのパスを応答に明示し、必要な文書は `pb_get_doc` で読む。目次は 10.2 の `?outline=1` から取り、目次掲載の文書は本文取得を省く。見出しが無い文書も本文を代わりに載せず、全文の引き方を示す。

**`charter_versions` が一致した文書は内容を省く。** ただし、子の実効掲載方法は親の変更でも変わるため、親を持つ文書には版一致による省略を適用しない。

**埋め込むとき、本文の見出しを2段下げる。** パックは `#`（表題）→ `##`（節）→
`###`（文書）を使うので、文書の中の見出しは4段目から始まる。下げないと、本文が `##` で
始まる文書では `## 3. 憲章` の次に本文の `##` が並び、**後続の `## 4. 依存・関連するチケット` が
憲章の中にあるのか外にあるのかが読めなくなる。** 木の形が壊れると、モデルは「どこまでが押し付けか」を
取り違える。**本文そのものは書き換えず、`#` の数だけを変える**（上限の6段で止め、
コードブロックの中は触らない）。

**依存・関連は 9.5.1 の `parent` / `children` / `links` から作る。** 相手の `status` を
含んだ形で返ってくるので、**追加の往復が要らない**（10.4.2 優先度5 の「前提タスクの成果物、
影響範囲」に当たるのがこれである）。

**5節目に深掘りの入口を書く**（`pb_get_doc` / `pb_list_transitions` / `pb_get_task`）。
10.4.3 の 4 が「切り詰めた事実と、深掘り用のクエリ例を応答に明記する」と定めており、
**掲載しない文書や目次だけの文書を読む入口も出す**——パックに無いものを探す手段が
書かれていないと、モデルは推測で埋める。**2件目以降は1行に畳む**（下の「2件目以降は定型文を畳む」）。

#### 読んだ憲章の版を受け、変わっていない文書を省く

**1セッションで複数のチケットを消化すると、同じ憲章の全文をチケットの数だけ受け取る。**
10件を消化すれば、数千字の憲章を9回余分に運ぶ。`/pb-onboard` が `pb_get_doc` で同じ全文を先に読むので、
参画直後の1件目からもう二重になっていた。

**`charter_versions` は任意で、文書のパスから版への対応である**（`{"vision":2,"rules":7,"learnings":2,"decisions":5}`）。
版は文書の `version`（10.2 / 10.3）である。

| 場面 | 振る舞い |
|---|---|
| 渡さない | **今までと同じ応答**（末尾に版の1行が増えるだけ）。既存の手順は壊れない |
| 版が一致した文書 | **本文（判断の記録は目次）を載せず、「前回から変わっていない」と版を1行出す。** 10.3 も叩かない |
| 版が違う文書・渡されていない文書 | **今までどおり載せる。** 途中で誰かが規約を直しても、その文書だけは届く |
| 憲章に無いパス（参画情報・消えた文書） | 無視する |
| 値が整数として読めない | 引数の誤り（`-32602`）として返す |
| `doc.view` を持たない | **今までどおり憲章を丸ごと省き**、版の行も出さない |

- **パックの末尾に、載せた憲章の版を1行出す。** エージェントは次の呼び出しでそれをそのまま渡す。
  **1・2・4・5節は毎回出す**——チケットごとに違うため
- **比べるのは目次（10.2）の版、末尾に出すのは本文（10.3）の版である。** 目次を読んでから本文を
  読むまでの間に更新されても、載せた本文と出した版が食い違わない
- **省かれる向きには、一致したときしか倒れない。** 渡した値に誤りや漏れがあっても、起きるのは
  余分に全文が届くことだけである
- **省いたときは、節3の頭に「手元に本文が無ければ、`charter_versions` を渡さずに呼び直す」と書く。**
  クライアントは長い会話を要約し、本文が手元から消えることがある。**サーバはそれを知り得ない**
  ので、判断はエージェントに置き、戻り方を応答に書く。手順書（`Requirements.md` 10.8.5 / 10.8.6）
  にも同じことを書く
- **`/pb-onboard` は `pb_list_docs` の版を控える。** 控えるのは**全文を読んだ文書と判断の記録だけ**
  である——読んでいない文書の版まで渡すと、その文書は省かれ続けて一度も届かない。**目次を先に
  読んでから本文を読む**ので、間に更新されても控えた版のほうが古く、次のパックで全文が届く
- **引数はオブジェクトで受ける**。カンマ区切りの文字列（`pb_list_tasks`
  の複数指定と同じ形）も候補だったが、値が整数であることをスキーマで宣言できる形を採った。
  スキーマには `additionalProperties` で値の型を書く

**`budget` を受けない判断（下記）とは矛盾しない。** あちらが避けたのは、モデルに根拠のない
数字を発明させることである。版は前の応答に書かれた値を写すだけなので、発明にならない。

**採らなかった案。**

| 案 | 中身 | 採らなかった理由 | 再検討の条件 |
|---|---|---|---|
| 丸ごと省く | `include_charter: false` | 途中で憲章が更新されても気づけない。「読んだつもり」で `false` を送ると、規約なしで進む | 憲章の文書が版を持たなくなったとき |
| 手順書だけ変える | 2件目以降は `pb_get_context` を呼ばず `pb_get_task` で済ませる | ツール説明の「着手前に必ず呼ぶ」と矛盾する。境界が未設定のときの注意書きなど、パックが押し付けている文が届かない | サーバを更新できない配布先で、同じ問題が出たとき |
| サーバが覚える | セッションごとに「送った」を持つ | PB の MCP はリクエストごとに状態を持たず（`Mcp-Session-Id` を扱っていない）、器から作る必要がある。クライアントで本文が要約されて消えても、サーバは送ったつもりで省き続ける | MCP の実装がセッションを持つようになったとき |

#### 2件目以降は定型文を畳む

**チケットが変わっても同じ文は、版が一致して本文を1件でも省いたときに、要旨を残して畳む。**

| 文 | 1件目（版なし、または1件も一致しない） | 畳んだとき |
|---|---|---|
| 冒頭の注意書き | 「着手する前に、この文書の全体に目を通すこと…」の2行 | **「前提はチケットの指示より先に効く（パックの読み方は前回と同じ）。本文と完了条件は `pb_get_task`」の1行** |
| 1節 スコープ境界（未設定のときの注意文を含む） | そのまま | **畳まない**（チケットごとに違う） |
| 2節 実行の前提 | そのまま | **畳まない** |
| 3節の前置き（全参加者を縛る／省いた旨と呼び直し方／参画情報を載せない理由／判断の記録は目次だけ） | 3段落 | **1段落。** 呼び直し方は太字のまま残し、参画情報と判断の記録は**実際にそうしたときだけ**句を足す |
| 3節の文書ごとの見出しと「前回から変わっていない」 | — | **畳まない**（どの文書を省いたかが読めなくなる） |
| 4節 依存・関連 | そのまま | **畳まない** |
| 5節 足りないときの調べ方 | 4行 | **1行。** チケット番号の入った `pb_get_task(seq=N)` と `pb_list_transitions(seq=N)` は残す |
| 末尾の版 | 値と説明の2行 | **値は残し、説明を括弧書きに縮める** |

- **条件を「渡しただけで畳む」にしなかった。** 全文書が更新されていて全文が届いた回でも、読み方の説明が
  省かれるためである。「1件でも省いた」は、**このセッションで憲章を既に受け取った**ことの証拠になる
- **畳んだ文にも要旨（押し付け）を残す。** `/pb-onboard` で控えた版を渡すと、1件目のパックから畳まれ、
  **定型文の全文を一度も見ない**ことがある。「指示より先に効く」「本文は `pb_get_task`」「呼び直し方」が
  畳んだ1行に残っていれば、押し付けたいものは届く
- **節の見出し（`## 1.`〜`## 5.`）は畳んでも残す。** 木の形が変わらないので、`Testing.md` 7.6 の測り方
  （`## 3. 憲章` から `## 4.` までを取る）もそのまま使える

#### `budget` を受けない

憲章が全文で数千文字である以上、**予算が効く場面が無い。** `idempotency_key` について
下した判断（8.5.1）と同じ形で、**器を先に作らない**（8.6 に再検討の条件を書いた）。

**渡す側が居ないことも理由である。** `budget` を決めるのは MCP クライアントだが、**モデルが
概算トークン数を選ぶ根拠を持たない**——数字を1つ発明させることになる。切り詰めが要る規模に
なったら、**予算は呼び出し側の引数ではなくサーバ側の上限として置くほうが、値の根拠を
PB が持てる。**

#### `context_pack_log` に書かない

**`DbDesign.md` 8.2.5 の器は 0022 にあるが、書かない。**

**結び先が無いためである。** `agent_run` は `pb_submit_result` のときにしか作られない
（8.5.4、`DbDesign.md` 8.2.4）ので、パックを返す時点では **`agent_run_id` が必ず `NULL` に
なる。** `Requirements.md` 10.4.4 の効果計測は **`agent_report.status` との突き合わせ**が
本体であり、**結べない行を貯めても計測にならない。** 10.10.7 の監査（何を見せたか）だけなら
成り立つが、**そのために書き手を1つ置くと、結べる形へ変えるときに既存行の
扱いが要る。**

**再検討の条件は、run の開始を告げる口ができたときである**（`agent_run.status` の
`running` を立てる経路）。
そのとき `pb_get_context` は `agent_run_id` を受け取れるようになる。

#### `doc.view` を持たないときは憲章を落として続ける

| 欠けている権限 | 返し方 |
|---|---|
| `ticket.view` | **内側の 403 をそのまま `isError` のツール結果にする**（8.4）。チケットが読めなければパックは成り立たない |
| `doc.view` | **憲章の節を落とし、「権限が無いため憲章を省いた」と本文に書いて成功として返す** |

**後者を成功にするのは、これが切り詰めの一種だからである。** 10.4.3 の 4 が「切り詰めた事実を
応答に明記する」と定めており、**閲覧用に絞ったトークン**（`Requirements.md` 10.9.1 の系統B）
でも、スコープ境界と依存関係は受け取れたほうがよい。**403 で全体を落とすと、読めるはずの
ものまで届かない。**

## 8.6 まだ決めていないこと

- **公式 Go SDK（`github.com/modelcontextprotocol/go-sdk`）へ乗り換えるか。** いまは自前で実装している——話す必要があるのが4メソッドだけで、8.1 が MCP 層を薄く保つと定めているためである（`Design.md` 3.1 がログと設定で下している判断と同じ形）。**次のいずれかが起きたら再検討する**
  - **PB が話す面が増えたとき。** `resources` / `prompts` / サーバ発の通知 / SSE ストリーム——とくに**長く走るツールの進捗をクライアントへ返したくなったとき**。8.4 が「SSE が要らない」と言えているのは各ツールが1往復で終わるからで、その前提が消えたら判断ごと変わる
  - **認可が MCP の Authorization 仕様（OAuth 2.1）へ寄ったとき。** いまは静的な Bearer トークン1本（6.5）だが、メタデータの配布・動的クライアント登録・トークン検証まで自前で持つのは割に合わない
  - **SDK が v1 に達し、破壊的変更が収まったとき。** v0.x のあいだ依存に入れると、追随の手間がかかる
  - **判断の材料は `server/internal/mcp` の行数である。** 4メソッドで数百行なら自前が安い。**仕様への追随のために膨らみ始めたら、それは SDK が引き受けている仕事を書き写している**という合図であり、そこが乗り換え時である
- **write 系の冪等キー（`idempotency_key`）を受けるか。** いまは受けない。**本当の冪等性には「キー → 結果」を持つ器が要り**、8.1 が「MCP に独自のビジネスルールを置かない」と定める以上、置き場は REST 層＝全クライアントに効く変更になる。write 系ツールは**再送が安全側に倒れる**——`pb_put_doc` は `If-Match` があるので古い版での再送が `409`、`pb_create_ticket` と `pb_post_note` の重複は画面で見えて人が消せる。**`pb_transition_task` の再送も安全側に倒れる**——2度目は「進行中 → 進行中」を要求することになり、`ck_workflow_transition_diff` により定義が存在しないため `409 invalid_transition` で弾かれる（`ApiDesign.md` 9.6 の検証2）。**次のいずれかが起きたら再検討する**
  - **リース（構想）を起こしたとき**（8.2）
  - **無人実行に踏み込んだとき**（`Requirements.md` 10.11 の将来対応）。人が同席していれば重複は目で拾えるが、同席しないなら拾えない
  - **判断の材料は「再送で何が二重になるか」を1つ言えるかである。** 言えないうちは器を作らない

- **ツール description の文面設計**（`Requirements.md` 10.13）。**実質的にこれがエージェントの行動を規定する**ため、プロンプトエンジニアリングの対象になる。**いまは日本語で書いている**——憲章・チケット・文書がすべて日本語であり、description が指示する語彙と、エージェントが読む対象の語彙を揃えるためである（英語より毎セッション数百トークン多く消費する）
- **コンテキストパックをさらに自動選定・切り詰めするか。** 現在は文書ごとの掲載方法を管理者が選び（8.5.5）、応答は Markdown 1枚で `budget` を受けない。掲載しない文書と目次だけの文書は応答に明示する。**全文掲載の文書が合計1万字を超えたら再検討する。** `make docs-size` は PB の文書を測らないので、追加・掲載方法の変更時に `pb_get_context` で測る（`Testing.md` 7.6）。
---

# 9. 画面設計

**正本は `GuiDesign.md`。** 本章は主な画面と必要権限の一覧に留める。

| 画面 | パス | 必要権限 |
|---|---|---|
| ログイン | `/login` | 不要 |
| プロジェクト一覧（ログイン後の初期画面） | `/projects` | `project.view` |
| プロジェクトダッシュボード | `/p/:key` | `project.view` |
| バックログ | `/p/:key/backlog` | `ticket.view` |
| チケット詳細 | `/p/:key/tickets/:seq` | `ticket.view` |
| プロジェクト設定（一般／メンバー／タグ／スプリント） | `/p/:key/settings` | `project.edit` |
| **プロジェクト文書（Docs）** | `/p/:key/docs` | **`doc.view`**（編集は `doc.edit`） |
| アカウント / 権限管理 | `/admin/users` | `user.manage` |
| 監査ログ | `/admin/audit` | `auditlog.view` |
| 自分の設定・トークン | `/me` `/me/tokens` | 本人 |

UI の基本構成として、**アプリ共通ヘッダを持たない**（ノートPCでの縦方向の可用領域を優先）。ナビゲーションはメインメニューペインに集約する。詳細は `GuiDesign.md` 2章。

---

# 10. 非機能・運用設計

DB に関する運用（バックアップ、ログ、データ量見積り）は `DbDesign.md` 9章に記載する。

## 10.1 アプリケーションログ

**構造化JSONを標準出力へ書く。** アプリはログの保存・ローテーション・転送に関与しない（12-factor）。収集はコンテナランタイムに委ねる。

| 項目 | 内容 |
|---|---|
| 出力先 | **標準出力**。`stderr` はロガー初期化前の致命的エラーと、CLI（`pb admin create` 等）の利用者向けメッセージにのみ使う |
| 形式 | `log/slog` の JSONHandler。`PB_LOG_FORMAT=text` で開発時のみ人間向けに切り替える |
| レベル | `PB_LOG_LEVEL`（`debug` / `info` / `warn` / `error`。既定 `info`） |
| `audit_log` との関係 | `request_id` で突き合わせる。**監査ログはDBに残す正式な記録**（`ApiDesign.md` 2.10）であり、アプリログは揮発してよい運用情報とする |

**標準出力に一本化する理由。** Kubernetes / CRI は `stdout` と `stderr` の双方を同じログストリームへ集約するため、どちらでも `kubectl logs` からは見える。それでも一本化するのは、①`stderr` は「異常」の含意を持ち、正常なアクセスログを流すと収集基盤（Fluent Bit / Loki 等）で誤って error 扱いする設定を誘発しやすい、②2ストリームに分けると行の到着順が保証されない、の2点による。

### アクセスログ

**1リクエストにつき1行**を、応答を返し終えた時点で出す。

```json
{"time":"2026-08-12T13:00:10Z","level":"INFO","msg":"request",
 "request_id":"01KZT21BZHZFY55771BQ88JVSE","method":"GET",
 "path":"/api/v1/projects","status":200,"duration_ms":12.3,
 "bytes":1234,"ip":"127.0.0.1"}
```

| 項目 | 方針 |
|---|---|
| `path` | **クエリ文字列は記録しない。** 検索語やメールアドレスがログに残るため |
| `ip` | `audit_log.ip` と同じ値。プロキシ経由の実IP解決は行わない |
| レベル | 5xx は `ERROR`、それ以外は `INFO` |
| `/healthcheck` | **成功時のみ** `DEBUG` で出す。既定レベル（`info`）では出ない。probe が1日数千行のノイズになるため。**そのパスへの 4xx / 5xx は通常どおり出す**（メソッド違いや probe の設定誤りに気づけなくなるため） |
| エラーの詳細 | **別行**として出す。`request_id` で突き合わせる（下記） |

### エラーログ

`ApiDesign.md` 2.5 の形式で応答を返す際、**内部原因を持つものだけ**を別行で出す。応答本文には内部原因を含めない。

```json
{"time":"...","level":"ERROR","msg":"リクエスト処理に失敗した",
 "request_id":"01KZT21BZHZFY55771BQ88JVSE","code":"internal_error",
 "status":500,"method":"POST","path":"/api/v1/projects",
 "cause":"pool exhausted"}
```

アクセスログと分けるのは、**1行の意味を「1リクエストの結果」に保つ**ため。原因の有無で項目が増減する行にすると、収集基盤側のスキーマが安定しない。

## 10.2 ヘルスチェック

`GET /healthcheck`。認証不要・副作用なし・DB非依存の固定応答とする。定義は `ApiDesign.md` 2.11 が正本。

**liveness と readiness を分ける。** 提供するのは liveness（プロセスが応答するか）のみ。DB断はプロセス再起動では復旧しないため、liveness に含めると再起動ループを招く。

## 10.3 設定管理

**方針の正本は PB の憲章「判断の記録」の「設定の扱い」である**——
①**設定は WebGUI を第一の口とする** ②即時反映できるものは即時、再起動が要るものは
そう画面に出す ③**締め出されうるものは確認しないと元に戻す**。
**理由は運用の担い手であり、技術力を前提にしないことを設定機構にも通す。**
本節はその方針の下での**設計**——層の定義・優先順・画面の作り——を書く。

**設定は3層に分ける。層を決めるのは「その値がいつ必要か」と「複数のレプリカが一致していなければならないか」の2点である。**

| 層 | 既定の置き場 | 何を置くか | 画面 |
|---|---|---|---|
| **1. 起動前** | `PB_CONFIG_FILE` の YAML / 環境変数 / `<KEY>_FILE` | 接続文字列（`database_url`）、**第3層の暗号鍵（`secret_key`）** | **表示のみ** |
| **2. 実行時の共有設定** | DB の `app_setting`（`DbDesign.md` 6.14） | ログ形式・ログレベル・ヘルスチェックのバージョン表示・Cookie の `Secure` | 確認と変更 |
| **3. 共有される秘密** | DB（`secret_key` で暗号化） | TLS 証明書と秘密鍵（`DbDesign.md` 6.15） | 確認と変更 |

**「既定の置き場」と書いたのは、第2層もファイルや環境変数で与えられるからである。** 与えた時点でその設定は画面から変更できなくなり、「固定」として表示される（下記の優先順）。**逆に第1層を DB に置くことはできない**——接続文字列は DB の中にあり得ない。

### 第1層に残すのは、DB に到れないものだけである

**接続文字列は DB の中にあり得ない。** これは選択ではなく循環であり、手当ての余地が無い。

**待受（`bind`）は第2層に置く。** 接続文字列は**DB に到るために要る**ので原理的に第2層に置けないが、**待受は「起動の順序をそう決めている」だけ**である。サーバは DB へ繋いだあとに待受を張る。

**行が読めなくても起動する**（この節の方針どおり）。読めなければファイル・環境変数・既定値で待ち受ける。

**K8s では、この層に新しい仕組みを作る必要が無い。** ConfigMap と Secret が**すでに複数のコンテナ間で共有される**ため、第1層の値は全レプリカへ同じものが届く。**`pb.yaml` は ConfigMap として、秘密は Secret として配る**——後者はボリュームにマウントすれば `<KEY>_FILE` の形になり、環境変数として渡せば `${…}` で引ける。

**第1層を画面から編集させないのは、編集させても反映先が無いからである。**

| 書き戻し先 | なぜ無理か |
|---|---|
| **環境変数** | **プロセスは自分の環境変数を次回の起動に残せない。** 他のコンテナの環境変数にも触れない。K8s では ConfigMap 由来の環境変数は**コンテナ起動時にだけ注入される** |
| **ConfigMap のファイル** | **常に読み取り専用でマウントされる** |
| **コンテナ内のローカルファイル** | 書けるが **Pod ごとに分かれる**（`deploy/stg/pb.env` が端末ごとに分岐するのと同じ問題が Pod ごとに起きる） |

**この制約は PB 固有ではない。** 初回アクセスで設定画面を出して自分の設定ファイルを書き換える方式（Gitea・WordPress・Nextcloud）は**単一ノードの方式**であり、**当の OSS はどれも K8s ではこの画面を封じている**（Gitea の Helm chart は `INSTALL_LOCK` を常に真にする）。**PB でこの方式を採るなら、同じ封じ手を最初から持たせること。**

### 第2層を DB に置く理由は3つある

1. **複数のレプリカが一致する。** レプリカ N 台で同じ行を読むので、画面からの変更が全台に効く
2. **変更を `audit_log` に同一トランザクションで残せる。** 既存の監査の仕組み（`DbDesign.md` 6.8）にそのまま乗る。ファイルへの書き戻しでは残せない
3. **設定を1件足すのがスキーマ変更でなくなる。** キーと値の行なので、2件目以降の追加に DDL が要らない

### サーバが自分の設定ファイルを書き換える案は採らない

| 理由 | 内容 |
|---|---|
| **スケールアウトで成立しない** | レプリカ N 台で設定ファイルが N 個に分岐する。画面からの変更は、そのリクエストを受けた1台にしか効かない。**手当てが無い** |
| **読み取り専用ファイルシステムで動かない** | `readOnlyRootFilesystem: true` は堅牢なコンテナの既定である |
| **`_FILE` による秘密の分離が崩れる** | いま秘密は `/run/secrets` から**読むだけ**である（`DbDesign.md` 3.2）。書き換え案では、HTTP を捌くプロセスが接続文字列への書き込み権を持つ |

**再検討の条件は、PB を単一ノードのみで配布すると決めたとき**である。

### 設定ファイルは YAML の1枚で、キーは平らに並べる

**`PB_CONFIG_FILE` が指す YAML ファイルに、設定をキーと値で書く。**

```yaml
# pb.yaml — PB_CONFIG_FILE が指すファイル
log_level: info
log_format: json
health_show_version: false
cookie_secure: false
bind: 127.0.0.1:8080

# 秘密は書かない。環境変数を引く
database_url: ${PB_DATABASE_URL}
```

**キーは `app_setting.key` と同じ平らな名前空間である**（`PB_` 接頭辞を付けない）。**入れ子にしない**——DB の行・API の項目・レジストリのどれも平らな1つの名前空間で、ファイルだけを入れ子にすると**対応表を維持する仕事が生まれる**。構造が要る設定は `tls_cert_path` のようにキー名で表す。

**`${環境変数名}` と書くと、その環境変数を引く。** これは**秘密をこのファイルに書かないための口**である（`DbDesign.md` 3.2）。ファイルは Git やコンテナイメージに入りうるので、接続文字列やトークンを直接書ける形にしない。

| 書き方 | 解釈 |
|---|---|
| `log_level: info` | そのままの値 |
| `database_url: ${PB_DATABASE_URL}` | 環境変数 `PB_DATABASE_URL` の値 |
| `database_url: ${PB_DATABASE_URL}` で環境変数が未設定 | **起動を失敗させる。** 空文字へ倒さない——書いてあるのに効いていない状態を黙って作らないため |
| キーを書かない | 下の層へ落ちる（環境変数 → DB → 既定値） |

**書かれていないキーは固定しない。** ファイルが言及したキーだけを押さえる。これは下の罠を避けるためである。

**`$$` で `$` をそのまま書ける。** 補間を1文字も持たないと、`$` を含む値を書けなくなる。

### 優先順と、それが招く罠

**`<KEY>_FILE` ＞ `PB_CONFIG_FILE` の YAML ＞ 環境変数 `<KEY>` ＞ `app_setting` の行 ＞ 既定値。**

| 層 | 何が入るか | なぜこの順か |
|---|---|---|
| `<KEY>_FILE` | **秘密の1値ファイル**（`/run/secrets/…`） | **プラットフォームが配る秘密**である（compose の `secrets:`、K8s の Secret ボリューム）。リポジトリに入りうる設定ファイルに上書きされてはならない |
| `PB_CONFIG_FILE` の YAML | 運用者が意図して書いた設定 | **運用者の明示的な宣言**である。環境変数より強くてよい——引きたいときは `${…}` で引ける |
| 環境変数 `<KEY>` | 1件だけ差し替えたいとき | ファイルを編集せずに1件を上書きする口 |
| `app_setting` の行 | 画面から変えたもの | 上の3つが言及していないキーについて効く |
| 既定値 | レジストリが持つ | 正本は Go 側の1か所 |

**環境変数を DB より強くするのは、画面からの誤設定を DB を触らずに復旧できる口を残すためである。** `cookie_secure` を誤ると全員がログインできなくなり（6.2.1）、そのとき画面から直す手段も失われている。

**この順は罠を1つ作る。** `deploy/base/compose.yaml` が第2層の4件すべてを `environment:` で明示していると、**環境変数が常に勝つので画面から何も変えられない。** そこで**第2層の既定値を compose と `pb.env` から外す**。外した結果、既定値の正本は Go 側の設定レジストリ1か所になる。**同じ理由で、配布する `pb.yaml` の雛形にも第2層のキーを書かない**——書いた瞬間に画面が読み取り専用になる。

**設定をファイルや環境変数で与えることは禁じない。** 与えたものは「固定」として画面に出す（下記）。

### 値の出どころを画面に出す

**各設定について、実効値がどこから来たか（環境変数 / ファイル / DB / 既定値）を画面に出す。これは付け足しではなく仕組みの本体である。**

これがあることで、**1つの画面が単一ノードと K8s の両方を扱える**——単一ノードでは DB の行を編集でき、K8s では Secret と ConfigMap で固定された値が「固定」と表示される。出どころが見えないと、誤った `cookie_secure` が「設定の誤り」ではなく「ログインの不具合」として読まれる。

### 変更の反映

| 層 | 反映 |
|---|---|
| 第1層 | **再起動が要る。** 接続プールと待受は起動時に張る |
| 第2層 | **即時。** ログの2件とヘルスチェックの1件は次のリクエストから効く。`cookie_secure` は次に発行する Cookie から効き、既存のセッションは切れない |

### この仕組みに載る設定

| 項目 | いまどうなっているか |
|---|---|
| **公開エンドポイント** | **まだ設定として存在しない。** 配置ファイルと接続設定の生成は、リクエストの `Host` ヘッダから組み立てている（`ApiDesign.md` 5.7.1 / 4.5.8）。**着手の条件は「`Host` から導けなくなったとき」**——リバースプロキシの背後に置く、あるいは複数の参加者が別々のアドレスで同じインスタンスを見るようになったとき（`Requirements.md` 10.13） |
| アクセスを許可・拒否するアクセス元 | 無い。第2層に載る形になる |
| TLS | **証明書と秘密鍵は第3層**（6.6.1。DB に置き、`secret_key` で暗号化する）。スケールアウトでは全レプリカが同じ証明書を出す必要があり、更新が全台に届かなければならないため、pod ごとのファイル配布を採らない |

### 締め出されうる設定は、確認しないと元に戻す

**設定を WebGUI で変えられるようにするほど、「変えたら画面へ戻れなくなる」設定が
増える。** **環境変数で上から押さえる**復旧経路は残すが、これは**利用者に端末と再起動の手段を
要求する**ので、第一の手当てにしない。

**ネットワーク機器の `commit confirmed` と同じ形を採る。**

```
① 設定を変える            → 「未確認」として記録し、期限（既定300秒）を置く
② 新しい設定で画面へ入る   → 「アクセスできました」を押す
③ 押されたら確定          → 未確認の記録を消す。以後は戻らない
③' 期限までに押されなければ → 元の値へ戻す
③'' 再起動した            → 期限を待たずに元の値へ戻す
```

**③'' が要るのは、締め出された人が最初に試すのが
再起動だからである。** そこで戻さないと、**その設定では起動に失敗する場合に永遠に
戻らない**——プロセスが上がらないのでタイマも動かず、何度再起動しても同じ
ところで落ちる（TLS を有効にしたまま暗号鍵の出どころが変わった場合がこれに当たる）。

**ネットワーク機器の `commit confirmed` も同じである。** 再起動すると未確定の
設定は失われ、保存済みの設定で上がる。

**代償は、確認前に別の理由で再起動すると変更が失われることである。** ただし
**確認していないのだから戻って正しい**——やり直せばよい。

#### 対象は「その変更で、その画面へ戻れなくなるか」で決める

| 設定 | 対象か | 理由 |
|---|---|---|
| `tls_enabled` | **対象** | http で入っていた人が https へ移れないと締め出される |
| `cookie_secure` | **対象** | http で有効にすると Cookie が送られず、**ログインが黙って失敗する** |
| `bind` | **対象** | ポートを誤ると画面へ到達できない。**第2層へ移し、張り替えも即時にした**ので同じ仕組みに乗る |
| ログ・ヘルスチェック | 対象外 | 誤っても画面から直せる |

**判定の基準を「重要かどうか」にしない。** 重要さは人によって違う。**「直す手段が
画面の向こうへ行くか」だけを見る。**

#### 待受の差し替えは実装した

**前提だったものが先に入った。** `tls_enabled` は**画面から変えた時点で待受が変わる**
ので、戻す操作もプロセス内で完結する。**プロセスは自分を確実に再起動できない**
（監視プロセスがある保証がない）ため、この仕組みが無いと「戻す」が成立しなかった。

**サーバごと作り直す形を採った**。

```
旧リスナを閉じる → 旧 Server を Shutdown（別スレッド）→ 新リスナを開く → 新 Server で待ち受ける
```

**`Shutdown` を同期で待たない。** 切り替えを指示するのは画面からのリクエストであり、
**その処理中に待つと自分自身を待つ**（処理中のリクエストの完了を `Shutdown` が待ち、
そのリクエストは `Shutdown` の完了を待つ）。

**棄却した案：素のリスナを1つ持ち続け、`tls.Server` で包むかをフラグで決める形。**
待受を閉じないので単純だが、**HTTP/2 が使えなくなる**——`http.Server` が HTTP/2 を
有効にするのは `ServeTLS` を通ったときだけである。**再検討の条件は、切り替え中に
接続が切れることが実際に問題になったとき。**

**同じアドレスなので、旧を閉じてからでないと新しく開けない。** 開けなかったときは
元の設定で開き直す。

#### 確認は新しい設定を通って届かなければ意味がない

**確認の口は「新しい設定で到達したこと」を確かめる。** `tls_enabled` なら
`r.TLS != nil` を見る——**平文で届いた確認を受け取ると、切り替えが失敗していても
確定してしまう。**

**前段にプロキシを置く構成では `r.TLS` が偽になる**（6.6.1 の `cookie_secure` と
同じ事情）。その構成では**この仕組みを無効にできる必要がある**。

#### 確定したら二度と戻さない

確定したあとで、ある時期に突然 HTTP に戻るようなことがあってはならない。
**確定は未確認の記録を消すことで表す**ので、消えたあとに戻す経路は存在しない。

#### 決めたこと

| 論点 | 決めたこと |
|---|---|
| 記録の置き場 | **DB に表を1つ持つ**（`pending_setting_change`。`DbDesign.md` 6.17） |
| 期限を数える主体 | **DB の `expires_at` が正本。** 起動時の点検とプロセス内のタイマが同じ行を見る |
| 複数の変更 | **1回の保存を1件として扱う。** 未確認が残っている間は次を `409` で断る |
| 既定の期限 | **300秒。設定にしない**（その設定自身を誤ると戻せない） |
| プロキシ配下 | **無効化の設定は足さない。** 確認の条件をキーごとに変える（`ApiDesign.md` 11.8） |

**プロセス内に持つ案は、締め出された人の行動で壊れる。** 画面へ入れなくなった人が
まず試すのは再起動であり、**そのとき記録が消えると未確認のまま確定してしまう。**
**戻り道を消すのが復旧の試み自身になる**という、最悪の形である。

**点検の間隔（10秒）も設定にしない。** 期限から最大10秒遅れて戻ることになる。

## 10.4 保守モード

**書庫の取り込み（`DbDesign.md` 9.1.1）のあいだ、PB は要求を受け付けない。** 取り込みは
表を落として作り直すので、**そのあいだに入った書き込みは、戻したデータと辻褄が合わない。**

### フラグはプロセスの中に持つ。DB に置かない

**取り込みは DB そのものを入れ替える。** フラグを `app_setting` に置くと、**落とす表の中に
入っている**ことになり、取り込みの途中でフラグ自身が消える。

**人が切り替える口を持たない。** 保守モードは取り込みの要求が自分で立てて自分で降ろすもので、
**設定ではない**（10.3 の「設定は WebGUI を第一の口とする」の対象外）。

**プロセスが落ちればフラグも消える。** 取り込みが途中で落ちると DB は中途半端なまま残るが、
**同じ書庫をもう一度取り込めば、表を落とすところからやり直す**ので回復できる
（`DbDesign.md` 9.1.1 の段取りが③から始まるため）。

### 応答は、人と機械で分ける

| 宛先 | 応答 |
|---|---|
| `/api` と `/mcp` | **`503`** と `ApiDesign.md` 2.5 のエラー形式（`code` は `maintenance`） |
| それ以外のパス（ブラウザ） | **`503`** と、**単一の静的な HTML**（「メンテナンス中」）。SPA を返さない |
| `/healthcheck` | **対象外。`200` のまま**（2.11） |
| 取り込みの口そのもの | **対象外。** これを止めると取り込みが自分を止めることになる |

**人には読める画面を、機械には読める形を返す**。**ステータスは
どちらも `503`** にする——ブラウザはステータスに関係なく本文を描くので画面は成立し、
**エージェントと監視は `200` を成功と読んでしまう。**

**SPA（`index.html`）を返さない。** SPA を返すと、画面は動き出してから API で失敗する。
**何が起きているかは、その API のエラーからしか読めない。** 保守モードのあいだは、
**Vue を積まない単一の HTML** を返す（`Design.md` 3.4 の SPA フォールバックの例外。`/healthcheck` と同じ扱い）。

**`Retry-After` は返さない。** 取り込みにかかる時間は書庫の大きさで決まり、**PB は見積もれない。**

### 複数のプロセスでは、1つしか止まらない

**フラグはプロセスの中にあるので、同じ DB へ繋ぐ別のプロセスは止まらない。** PB は
単一プロセス・少人数利用を前提としており（`DbDesign.md` 3.5）、**この限界は塞がない。**

**複数プロセスで動かしている環境では、取り込みの前に他のプロセスを止める。**
K8s のサンプルが `replicas: 1` のままなのは、initContainer で migrate を走らせるためだが
（`deploy/prod/k8s/app.yaml`）、**この制約もそこに乗る。**

## 10.5 今後扱うもの

- メトリクス（Prometheus 形式のエクスポート可否を含む）
- コンテナイメージのビルドと配布
- `Requirements.md` 8章の「KEDAでスケール0」構成の可否（PostgreSQL 常駐との兼ね合い）

---

# 11. 開発の進め方と実装順序

**本章の手順一覧にある Phase 1〜3 は、過去の手順を時期で分けた分類である。** 機能の区分ではなく、本書の他の章とほかの設計文書は Phase を使わない（実装済みでないものは「未実装」「構想」と書く）。手順番号はコミットやチケットから参照されるので、一覧は消さない。

## 11.0 ブランチ運用

```
main ← develop ← feature/*
```

| ブランチ | 役割 |
|---|---|
| `main` | リリース可能な状態のみ |
| `develop` | 統合先。ここから feature を切り、ここへ戻す |
| `feature/step-<2桁>-<スラッグ>` | 本章の手順1つ分の作業 |
| `feature/pb-<番号>-<スラッグ>` | 手順に属さない実装 |
| `fix/pb-<番号>-<スラッグ>` | 機能を変えない修正（不具合修正・hotfix）。**マイナーバージョンを上げない**（11.1） |
| `docs/pb-<番号>-<スラッグ>` | 設計文書のみの修正 |

**`<番号>` は PB のチケット番号（`ticket.seq`）である。** 作業はチケットから来るので、
**`git branch` を見るだけでどのチケットの作業か分かる**ようにする。
チケットに紐づかない作業では `pb-<番号>-` を省いてよい。

**接頭辞を残すことが要である。** **マイナーを上げるかビルドを上げるかが接頭辞で決まる**（11.1）ため、
接頭辞を落とすと `make bump-*` のどちらを打つかを git から判断できなくなる。
**チケット番号は接頭辞の後ろに置く**ことで両立する。

**`develop` に直接コミットしない。** 作業ごとに feature ブランチを切り、完了後に `--no-ff` で `develop` へマージする。`--no-ff` を使うのは、**作業の区切りをマージコミットとして履歴に残す**ためである。後から「どの作業でどこまで入ったか」を `git log --first-parent develop` で辿れる。

```bash
git switch develop && git pull
git switch -c feature/step-02-migrations
# 実装・検証
git commit -m "step 2: マイグレーション 0001〜0010 を追加"
git switch develop && git merge --no-ff feature/step-02-migrations
```

**マージ・push・ブランチ削除はエージェントに独断で行わせない**（PB の規約「人の承認が要ること」）。

## 11.1 バージョン番号とリリースタグ

リリースタグは **`vX1.X2.X3`** 形式とする（例：`v1.2.4`）。

| 桁 | 名称 | 意味 | 上がる条件 |
|---|---|---|---|
| X1 | メジャー | 機能のまとまり | **利用者が区切りを決めたときに上げる**（11.1 下記） |
| X2 | マイナー | メジャー内での機能実装数 | `feature/*` のマージ。**メジャーを上げたとき 0 に戻る** |
| X3 | ビルド | `develop` へのマージ回数 | **すべてのマージ**。リセットしない |

**`fix/*` と `docs/*` のマージではマイナーを上げず、ビルド番号だけを上げる。** リリース後の小さな修正は、マイナーを据え置いたままビルド番号の違いで識別する。逆に言えば、**マイナーが同じでビルドが異なる版は「同じ機能セットの別ビルド」を意味する**。

### 正本と検証

**`VERSION`（リポジトリ直下）が正本。** ビルド時は `-X main.version=$(VERSION)` で単一バイナリへ埋め込む（4.5）。

これとは別に、ビルド番号は git からも導出できる。

```bash
git rev-list --count --first-parent --merges develop
```

**マージコミットのみを数える**（`--merges`）。`--first-parent` だけで全コミットを数えると、`develop` 上で `VERSION` を更新するたびにビルド番号が動いてしまい、値が自分自身を追い越して収束しない。マージ回数で数えれば、`develop` への直接コミットがあってもビルド番号は動かない。

`make version-check` がこの実測値と `VERSION` を突き合わせる。ずれていれば失敗する。

### 更新の手順

**`VERSION` の更新は feature ブランチ側で行う。** `develop` へ直接コミットしないという原則（11.0）を保つため、マージの中に含める。

```bash
git switch -c feature/<スラッグ>
# 実装・検証

make bump-minor          # 機能追加のとき。fix/* と docs/* は make bump-build
git add -A && git commit -m "..."

git switch develop && git merge --no-ff feature/<スラッグ>
make version-check       # VERSION と git 実測が一致することを確認する
```

`bump-*` は **これからマージする前提**で、現在のマージ回数に 1 を足した値をビルド番号に書く。したがって feature ブランチ上で実行し、マージ後に `version-check` が通る状態にしておく。

| コマンド | 用途 | 例 |
|---|---|---|
| `make version` | 現在値と git 実測の表示 | |
| `make version-check` | 両者の一致を検証（マージ後に実行） | |
| `make bump-build` | `fix/*`・`docs/*` のマージ | `1.2.4` → `1.2.5` |
| `make bump-minor` | `feature/*` のマージ | `1.2.4` → `1.3.5` |
| `make bump-major` | メジャーを上げる。マイナーは 0 へ | `1.2.4` → `2.0.5` |
| `make release-tag` | `develop` 上で `v<VERSION>` の注釈付きタグを作る。**push は手動** | |

### 区切りを決めたらメジャーを上げる

**メジャーは、利用者が機能のまとまりの区切りを決めたときに上げる。** 区切りの後の最初のマージが `feature/*` なら、
そのブランチ上で `make bump-major` と `make bump-minor` を続けて実行する（例：`1.36.55` → `2.0.56` → `2.1.56`）。
**`bump-*` はどちらもビルド番号に「現在のマージ回数 + 1」を書く**ため、続けて実行してもビルド番号は二重に進まない。

**ただし `make bump-major bump-minor` と1回の呼び出しにまとめてはならない。** `VERSION` は
`VERSION := $(shell cat …)` で**パース時に一度だけ展開される**ので、まとめると `bump-minor` が
`bump-major` の書いた値ではなく元の値を読む。**`make` を2回に分けて叩く。**

最初のマージが `fix/*` や `docs/*` なら `make bump-major` と `make bump-build` になり、マイナーは 0 のままである。
**マイナーは「そのメジャー内での機能実装数」であり**、機能を足していないマージで 1 にはしない。

## Phase 1 — 認証とチケットの基礎（ローカル動作確認まで）

```
 1. deploy/base/compose.yaml と initdb（DBロール分離）  ← DbDesign 3.2, 3.4
 2. server/migrations/ 0001〜0010 の作成と適用       ← DbDesign 6, 7
 3. pb admin create による初期管理者作成            ← DbDesign 7.5
 4. 共通基盤：エラー形式、ページネーション、認証ミドルウェア、監査ログ
 5. POST /auth/login、/auth/logout、GET /me         ← ここでログインが通る
 6. 認可ミドルウェア（RequirePermission）
 7. client 雛形（Vite + Pinia + router + デザイントークン）と embed 疎通
7.5 開発用デモデータ（pb dev seed）と dev-reset / dev-info
      ← 権限の違う4アカウントが揃い、手順8の出し分けを検証できる
 8. ログイン画面・auth ストア・ルーターガード・メニュー出し分け
      ← ブラウザでログインでき、権限でメニューが変わる
 9. GET/POST /projects、check-key
10. プロジェクト一覧画面・新規作成モーダル
      ← ブラウザでプロジェクトを一覧・作成できる
11. GET/PATCH /projects/:key、archive、プロジェクト設定画面
      ← ブラウザでプロジェクトを編集・アーカイブできる
12. GET/POST /admin/users とユーザー管理画面（一覧・追加モーダル）
      ← ブラウザでユーザーを追加でき、初期パスワードが1回だけ出る
13. GET/PATCH/DELETE /admin/users/:id、password-reset、memberships
    とユーザー詳細・編集画面
      ← ブラウザでロール変更・無効化・パスワードリセットができる
14. GET /roles、GET /permissions とロールと権限タブ
      ← ブラウザで権限マトリクスが見える
15. /me 系 API と自分の設定・トークン管理画面
      ← ブラウザでパスワード変更・テーマ切替とCLIトークンの発行ができる
16. チケット一覧・作成API、タグAPI、スプリントAPI とバックログ画面
      ← ブラウザでチケットを一覧・作成・並べ替え・グループ化できる
17. チケット詳細・更新・削除・ステータス遷移API とチケット詳細画面（外部参照を含む）
      ← ブラウザでチケットを編集し、状態を進め、実装したコミットや仕様書を辿れる
18. コメント・DoD・関連リンクのAPI と詳細画面への組み込み
      ← ブラウザでコメントを投稿し、完了条件と関連チケットを管理できる
19. stats / activity API とプロジェクトダッシュボード
      ← ブラウザでプロジェクトの現況が見える
```

**完了した手順も一覧から消さない。** 本一覧は**手順番号の定義の正本**であり、コードのコメントが
手順番号（「手順5a」など）で実装の由来を示しているので、番号を引く先が要る。
手順16〜19 は a〜d に分けて実施した（16a〜16d、17a〜17c、18a/18b、19a/19b。11.2.1）。

### 11.1 API と画面を同じ手順で進める

**API と対応する画面は同じ手順で実装する。** API を積み上げてから画面に取りかかる進め方は採らない。理由は動機づけではなく**フィードバックの遅れ**にある。以下はブラウザを通さないと検証できず、curl では確認できない。

| 対象 | ブラウザでしか分からないこと |
|---|---|
| HttpOnly Cookie | 実際に保存され、次のリクエストで送られるか |
| CSRF 二重送信 | JS が `pb_csrf` を読めて送信できるか |
| Vite プロキシ（:5173 → :8080） | Cookie とプロキシの相性 |
| 実効権限のキャッシュ | ストアに載せた権限で画面が正しく出し分くか |
| `GET /me` の応答形状 | **フロントが必要とする情報に過不足がないか** |

特に最後の項目は、`GET /me` の唯一の消費者が Pinia の `auth` ストアであるため、**ルーターガードとメニュー出し分けを実際に書くまで過不足が判明しない**。API を先に積み上げると、判明した時点で後続の実装がすべて同じ前提の上に載っている。

### 11.2 1ステップ = ブラウザで確認できる単位

**手順の区切りは言語ではなく、「ブラウザで何ができるようになるか」で決める。** 上の一覧で各手順に付けた `←` の行が、その手順の完了条件である。API と対応する画面は同じ手順に入れる。

**言語（API か画面か）で区切らない。** 区切ると次の弊害がある。

| 弊害 | 具体 |
|---|---|
| API の手順が curl でしか検証できない | 11.1 が挙げたとおり、Cookie・CSRF・応答形状の過不足はブラウザを通さないと分からない |
| 応答の設計ミスが次の手順まで顕在化しない | 画面を書いて初めて「この項目が足りない」と気づく |
| 手順の完了が利用者に見えない | 「動くようになったもの」が増えないまま手順番号だけ進む |

**コンテキストの膨張は、手順の分割ではなくセッションの分割で扱う**（11.2.1）。

#### Phase 2 では尺度を1つ足す

**1ステップ = 利用者が実際に使える経路が1本増える単位。**

Phase 2 の成果物には**ブラウザに出ないものがある**——MCP のツール、コンテキストパック、
設定ファイルの生成物。「ブラウザで何ができるようになるか」だけを尺度にすると、
これらの手順に完了条件を書けない。

| 手順 | ブラウザで確認できるか | 何をもって完了とするか |
|---|---|---|
| 22 Docs 画面 | **できる** | 従来どおり |
| 25 MCP の read 系 | できない | **`/pb-onboard` が通しで走る** |
| 27 コンテキストパック | できない | **チケットを指定すると憲章の該当章が返る** |

**どちらの尺度も「利用者が実際にやることが1つ増える」を測っている。** 増えた先が
ブラウザかターミナルかの違いにすぎない。**`curl` で応答が返ることを完了条件にしない**
——エージェントが実際に使えるかは、クライアントを通さないと分からない。

#### 11.2.1 セッションを分けるとき

1つの手順が1セッションに収まらないときは、**a / b に分けてよい**。ただし次を守る。

- **分割は手順一覧で先に決めない。** 着手時に分量を見積もって判断する。設計文書に書いた見積もりは、書いた時点の推定であって実装直前の実測ではない
- **分けたら、a の完了時に検証・記録・コミットまで行って止まる。** そこまでやらないなら分けない。「a で作りかけ、b で仕上げる」は、a の時点で何が動くのかが誰にも分からなくなる
- **手順の完了（上の `←` 行）は b で満たす。** a は途中経過であり、単独では手順を完了させない

**分けること自体に手間がかかる（記録・検証・コミットが2回要る）ため、既定は「分けない」である。**

### 11.3 未実装画面はプレースホルダを置く

**遷移先が未実装・設計未確定の画面には、`GuiDesign.md` 6.5 のプレースホルダページを表示する。** 空白や 404 にしない。

- 実装済みの画面から遷移して動作確認できる
- **プレースホルダ自体が「この画面をどう作るか」を議論するときの参照点になる**。画面名・設計文書の章番号・予定内容が画面上に出ているため、それを見ながら会話できる
- 権限（`meta.permission`）は実画面と同じにする。プレースホルダのうちにルーターガードとメニュー出し分けを検証できる

プレースホルダの一覧は `GuiDesign.md` 3.2 にある。

## Phase 2 — 複数人とエージェントが同じプロジェクトを進められるようにする

**目的は「MCP サーバができること」ではない。** `Requirements.md` 10.0 のとおり、PB が価値を持つのは参加者が複数になってからである。Phase 2 は**その最小構成を通しで動かす**ところまでを担う。

```
20. PB 自身を管理するインスタンス（deploy/stg）          ← Design 4.4
      ← make dev-reset を実行しても stg のデータが残る
21. マイグレーション 0017（document / document_revision / doc 権限） ← DbDesign 8.1
      ← make migrate と make test-db が通る
22. 文書API と Docs 画面（目次・本文・編集・履歴）        ← ApiDesign 10, GuiDesign 5.10
      ← ブラウザで文書を作り、階層に置き、編集して履歴が残る
23. 文書テンプレートの複製（プロジェクト作成時）           ← DbDesign 8.1.2
      ← 新規プロジェクトに型の4文書が並ぶ
24. マイグレーション 0019（agent / task_lease）と
    エージェント登録・トークン発行                       ← DbDesign 8.2, Design 6.5
      ← 自分の設定でエージェントを登録し、参加プロジェクトを選び、
        トークンを1回だけ全文表示できる
      （登録は本人が行う。エージェントは所有者に紐づき、権限を所有者から
        導く＝委譲。24a=0019 と API、24b=画面）
25. MCP サーバと read 系ツール                          ← Design 8
      ← Claude Code から /pb-onboard が通しで走る
26. MCP の write 系ツール                               ← Design 8.2
      ← 議論の結果をチケットとして起票でき、指示で文書を更新でき、
        エージェントが自分の担当ぶんの状態を進められる
      （26a=起票・文書更新・ノート＋エージェントの削除、
        26b=状態遷移＋ticket.working_agent_id、
        26c=完了レポート＋マイグレーション 0022（agent_run / agent_report）。
        8.2 の表が内訳）
27. コンテキストパック（pb_get_context）                 ← Requirements 10.4
      ← チケットを指定すると憲章の該当章とスコープ境界が返る
28. セットアップ画面と設定ファイル生成                    ← Requirements 10.9
      ← 「リポジトリの初回接続」と「メンバーの参画」の2系統が出る
      （28a=系統A：配置ファイルの生成とプロジェクト設定の画面、
        28b=系統B：/me/agents の接続設定・export 行・接続確認
             ＝「MCP が使える状態になるまで」、
        28c=作業材料の取り方を PB の文書で指定する。
        **手順の完了は 28c で満たす**）
```

**手順20 を先頭に置く。** 以降の手順が「PB の開発を PB で管理する」を積み上げるので、**器が先に要る**。

### Phase 2 の完了条件

**受け入れは通しで測る。**

> **新しく clone した作業ディレクトリで `/pb-onboard` → `/pb-implement <id>` が通しで走り、
> チケットが1件消化される。これを Claude Code と VS Code のエージェントの両方で行う。**

両方で行うのは、**セッションどうしが記憶を共有しないため**である。**文脈を共有しない参加者が
同じプロジェクトを触るという構造は、開発者が1人でも成立する。** 内訳として測るのは4点。

| # | 測ること |
|---|---|
| 1 | 議論の結果を PB のチケットとして起票できる（`pb_create_ticket`） |
| 2 | 着手時にコンテキストパックが前提を押し付ける（`pb_get_context`） |
| 3 | 憲章を MCP 経由で読める（`pb_list_docs` / `pb_get_doc`） |
| 4 | clone からセッション開始まで、**PB の画面以外の口頭指示を要しない** |

**Phase 2 で検証されないことを明記しておく。** 意見の対立と合意形成、承認者が別人であることの
負荷、個人の学びが N 人へ広がるか——**いずれも参加者が1人では起きない**。`Requirements.md` 10.0.2 が
挙げた4つのうち、Phase 2 が実地に確かめられるのは「学びの置き場ができたこと」までである。

### Phase 3 で扱うもの

| 項目 | 理由 |
|---|---|
| `knowledge`（プロジェクトメモリ） | まず 8.1 の文書として運用し、押し付けたい粒度が実測で見えてから切り出す（`DbDesign.md` 8.3） |
| **`proposal` と承認キューUI** | 承認の対象になる `knowledge` と文書差分がそろってから作る。それまでは対象がサブタスク提案だけになり、画面を作る理由が薄い |
| DoD の machine 型、`context_pack_log` の書き手 | 人が同席する前提では、`pb_post_note` を既存の `comment.kind` に載せれば足りる（`Requirements.md` 10.3.2）。**`context_pack_log` は 0022 に器だけがある**（`DbDesign.md` 8.2.5） |

## Phase 3 — AI機能・分析と、知識の還流

```
29. Phase 3 のマイグレーション（DbDesign 8章の一覧）     ← DbDesign 8.3, 8.4
30. DoD の machine 型                                  ← DbDesign 6.11
      （context_pack_log の書き手もここで置く）
31. proposal と承認キューUI                            ← Requirements 10.6.3
32. プロジェクトメモリ（knowledge）とコンテキストパックへの供給 ← DbDesign 8.3
33. Readiness 判定、DoD ドラフト生成                    ← Requirements 10.5.1
34. コメント分類・重要度スコアリング
35. ベクトル検索、プロジェクトヒストリー
36. 進捗分析画面（消化状況・残存チケット・ベロシティ）    ← GuiDesign 10章
37. OIDC / SAML 連携                                   ← Design 6.2.3
38. カスタムロールの編集UI                              ← GuiDesign 5.6.3
```

---

## 付録A. 本書に関する未解決の検討事項

各領域固有の検討事項は、それぞれの設計書の末尾に記載している（`DbDesign.md` 10章、`ApiDesign.md` 12.2、`GuiDesign.md` 11章）。本書に残るのは以下。

- `Requirements.md` 8章の「KEDAでスケール0」を PostgreSQL 常駐構成でどう扱うか
- アプリケーションログと `audit_log` の使い分け（何を両方に書き、何を片方に留めるか）
- **`GET /roles?scope=project` の必要権限を再整理する。** 認証済みなら誰でも読める現行方式は暫定である（`project.edit` を要求する案と `role.view` を新設する案がある）。
- **HTTPサーバのタイムアウト値が実装（`serve.go`）にしかない。** 10章を扱うときに文書化する
- **DBを使うテストの作法を本書に書くか**。手順は `Development.md` 6.1 / 6.2 にあるが、設計として持つかは未判断
