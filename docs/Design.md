# Project Backyard (PB) 設計書

> 本書は `Requirements.md`（要件・構想）を受けた**全体設計書**である。
> システム全体の構造と、領域別設計書への振り分けを担う。
>
> - 対象読者：実装者（人間およびAIエージェント）
> - 状態：Phase 1 の設計を確定。第6章（認証・認可）を本書の正本として保持し、DB・API・画面は領域別設計書へ委譲

## 文書体系

```
Requirements.md   要件・構想（何を作るか・なぜ作るか）
      │
      ▼
Design.md         全体設計（本書）── システム構成・技術選定・認証認可・開発フェーズ
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
| 開発フェーズ | **本書 11章** | — |

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
| 8 | MCPサーバ設計 | 記述済（手順25 で read 系を実装） |
| 9 | 画面設計 | → `GuiDesign.md` |
| 10 | 非機能・運用設計 | ログ・ヘルスチェック・**設定管理**は確定。メトリクス等は未着手 |
| 11 | 開発フェーズと実装順序 | 記述済 |

---

# 1. 本書の位置づけと設計原則

## 1.1 設計原則

| # | 原則 | 具体的な帰結 |
|---|---|---|
| 1 | **段階的に育てられる構造** | Phase 1 で作るテーブルを Phase 2/3 で作り直さない。拡張は列追加とテーブル追加のみで行う |
| 2 | **スキーマを設計資産として扱う** | DDL は人が読んで判断できる形で管理する。ORM の自動マイグレーション生成に委ねない（`DbDesign.md` 5.1） |
| 3 | **人間とエージェントを同じ型で扱う** | 担当者・作成者・承認者はすべて `actor` を参照する。エージェント追加時に既存テーブルを変更しない |
| 4 | **認証方式を差し替え可能にする** | ローカルID/PW・OIDC・SAML を同一の `user_identity` 抽象の下に置く（6.2） |
| 5 | **権限はデータで定義する** | 画面・機能の可否をコードに埋め込まず、permission カタログとして DB に持つ（6.4） |
| 6 | **AIは提案し、確定は人間が行う** | エージェントの出力は `proposal` テーブルを必ず経由し、本体テーブルを直接更新しない |
| 7 | **秘密をモデルのコンテキストに入れない** | エージェント向けの経路は MCP に一本化し、資格情報を含むコマンドを組み立てさせない（`Requirements.md` 10.10.1） |
| 8 | **単独開発で足りることを PB に作らない** | 開発者が1人なら、リポジトリの md ファイルとエージェント側の工夫で足りる。**機能を足す前に「md ファイル1枚では足りない理由は何か」を問う**（`Requirements.md` 10.0） |

**原則2 は rev.1 の「DB依存を薄く保つ（SQLite/PostgreSQL両対応）」を置き換えたものである。** PostgreSQL 前提へ変更したことで移植性の制約が不要になり、代わりに「スキーマそのものを設計資産として管理する」ことを原則に据えた。経緯は `DbDesign.md` 2章に記録している。

**原則8 は本改訂（2026-08-29）で足した。** 原則1〜7 はいずれも「どう作るか」を定めるもので、**作らない理由を与える原則が1つも無かった**。PB が価値を持つのは参加者が複数になってからであり（`Requirements.md` 10.0）、単独開発で足りる範囲まで作り込むと、原則1（段階的に育てられる構造）で足したものが誰にも使われないまま残る。

## 1.2 本書と要件定義の対応

| 本書 | `Requirements.md` の該当箇所 |
|---|---|
| 1.1 設計原則（原則8） | 10.0（PB が解く問題） |
| 2〜4. 構成・技術選定 | 1章（コンセプト・技術スタック）、8章（軽量性維持） |
| 6. 認証・認可設計 | 10.10（セキュリティとガードレール） |
| 8. MCPサーバ設計 | 10.3（MCPサーバ仕様）、10.4（コンテキストパック） |
| 11. 開発フェーズ | 10.12（MVPスコープ） |

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

## 2.2 Phase 1 の到達点

Phase 1 は 2026-08-28 に完了し、以下が成立している。

- `docker compose up` で PB と PostgreSQL が起動し、マイグレーションが適用される
- ローカルID/PW でログインでき、セッションが維持される
- オペレータ／アドミニストレータで見える画面・使える機能が変わる
- プロジェクトとチケットを作成・編集・一覧表示できる
- ユーザーの追加・権限設定が管理画面から行える

MCP サーバ、AI機能、ガント描画は Phase 1 の対象外である（テーブル・列のみ先行定義したものは `DbDesign.md` 6章・8章を参照）。

---

# 3. 技術スタックと実行環境

## 3.1 採用技術

| 層 | 採用 | 備考 |
|---|---|---|
| **サーバ言語** | **Go 1.24 以上** | `Requirements.md` 1章の候補（Rust / Go）から確定。ビルドの速さ、単一バイナリ配布、学習コストの低さを優先 |
| HTTPルータ | **chi v5** | `net/http` 互換。ミドルウェア連鎖とルートグループのみを足す薄い層 |
| DBドライバ | **pgx v5**（`database/sql` を経由しない） | `timestamptz` `jsonb` `inet` をネイティブに扱えるため |
| クエリ | **sqlc**（pgx/v5 モード） | SQLを書くとGoの型付き関数が生成される。設計原則2と一致 |
| マイグレーション | **goose v3** | SQLファイルベース。アドバイザリロック対応（`DbDesign.md` 5.3） |
| パスワード | `golang.org/x/crypto/argon2` | PHC文字列の入出力は `alexedwards/argon2id` を利用 |
| 対話入力 | `golang.org/x/term` | `pb admin create` のパスワードを非表示で読む（`DbDesign.md` 7.5） |
| YAML | `gopkg.in/yaml.v3` | **設定ファイル（`PB_CONFIG_FILE` が指す `pb.yaml`。10.3）と `pb dev seed` の定義ファイル**（`DbDesign.md` 7.6.4）。**pb-2 で実行時の依存になった**——それまでは `pb dev seed` だけで、実行時の推移依存を持たなかった |
| ID生成 | `oklog/ulid/v2` | ULID（`DbDesign.md` 4.2） |
| ログ | **`log/slog`**（標準ライブラリ、JSONハンドラ） | 外部ライブラリを増やさない |
| 設定 | YAML の設定ファイル＋環境変数＋`*_FILE` 展開（自前）と DB の `app_setting` | **3層に分け、優先順は5段。10.3 が正本**（`DbDesign.md` 3.2 / 6.14） |
| 入力検証 | `go-playground/validator` v10 | `ApiDesign.md` 2.5 のエラー形式へ変換する層を挟む |
| テスト | 標準 `testing` ＋ compose のDBに対する統合テスト | testcontainers は導入しない（起動が重く原則と衝突） |
| DB | **PostgreSQL 17** | `DbDesign.md` 2章。SQLite 先行案は廃止 |
| DB拡張 | `pgcrypto` / `citext` / `pg_trgm`（Phase 1）、`vector`（Phase 3） | `DbDesign.md` 3.1 |
| フロント | Vue 3 + TypeScript + Vite | SPA。サーバから静的配信 |
| UIコンポーネント | 未確定（自前の軽量実装で開始） | `GuiDesign.md` 1.1 |
| Markdownエディタ | **CodeMirror 6**（`codemirror` ＋ `@codemirror/lang-markdown`） | `GuiDesign.md` 5.5 の説明欄。`Requirements.md` 7章が「フルスクラッチのリッチエディタは避ける」と定める |
| Markdown描画 | `markdown-it` ＋ `dompurify` | 同じ欄のライブプレビュー側 |
| 実行形態 | docker compose（既定）／ Kubernetes | `DbDesign.md` 3.2〜3.3 |

**Markdown の2つは手順17b で足す。** それまでの client の実行時依存は `vue` / `vue-router` / `pinia` の3つだけだった（`Development.md` 10.4）。**チケットの説明欄が、PB で最も打鍵回数の多い入力欄になる**——人とエージェントが読み書きする一次資料であり（`Requirements.md` 10章）、素の `textarea` との差が毎日効く。

**`dompurify` を併せて入れるのは、本文がエージェントからも入るためである。** `markdown-it` は既定で安全側に倒れている（`html: false` と、`javascript:` 等を弾くリンク検証）が、**書き手が人だけではない**以上、描画の直前にもう一段通す。**MCP 経由の入力を信用しない**方針は `Requirements.md` 10.10.6 と同じ根である。

## 3.2 sqlc と goose を組み合わせる理由

**sqlc は `migrations/` のDDLを読んでスキーマを推論する。** したがって、

- `DbDesign.md` のDDLをマイグレーションに落とせば、**Goの構造体と型が自動的にスキーマと一致する**
- 手書きしたSQLは `sqlc generate` の時点で検証され、列名の誤りやスキーマとのずれがコンパイル前に露見する
- ORM を使わないため、実行されるSQLが常に目に見える（設計原則2）

**マイグレーションが唯一のスキーマ定義**になり、モデル定義とDDLの二重管理が発生しない。この点が Go を選んだ場合の最大の利点になる。

## 3.3 OpenAPI の扱い

`docs/openapi.yaml` は**生成器を通さずに保守する**。コード生成は**TypeScriptクライアントのみ**（`openapi-typescript`）に限定する。

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
- **プレースホルダは `index.html` という名前にしない。** 実ビルドが同じ名前を出力するため、`make build` のたびに追跡対象が書き換わり、作業ツリーが汚れる（2026-08-22 に名前を分けた）
- **プレースホルダは自己完結でなければならない**（外部のスクリプトやスタイルを参照しない）。参照すると、そのファイルは埋め込まれていないので SPA フォールバックが HTML を返し、`<script type="module">` が HTML を受け取って画面が真っ白になる
- `index.html` が無いとき（client が未ビルド）は、このプレースホルダを **`503 Service Unavailable`** で返す。`200` にすると監視や自動確認から「画面が出ている」と区別が付かない
- SPA のため、`/api` `/mcp` `/healthcheck` 以外で未知のパスは `index.html` を返す（フォールバック）。`/healthcheck` は `/api/v1` の外に置く唯一のエンドポイントであり、フォールバックの例外になる（`ApiDesign.md` 2.11）
- キャッシュ制御：ハッシュ付きアセットは `immutable`、`index.html` は `no-cache`

**バイナリは `CGO_ENABLED=0` で静的リンクできる。** pgx が pure Go 実装であるため C ライブラリに依存せず、distroless / scratch イメージで動作する。クロスコンパイルも `GOOS` / `GOARCH` の指定だけで済む（`Design.md` 4.2）。

## 3.5 PostgreSQL を初期から使う理由

`DbDesign.md` 2.1 に記載する。要点は、①移行が確実に来るなら最初から移行後の姿で作る方が安い、②Phase 2 でエージェントが並行書き込みするため単一ライタ制約が問題になる、③pgvector と `LISTEN/NOTIFY` が使える、の3点。

---

# 4. リポジトリ・ディレクトリ構成

## 4.1 全体

```
ProjectBackyard/
├── README.md                      ← プロジェクト概要（初見の人向け）
├── CLAUDE.md                      ← エージェント向け常時コンテキスト（ポインタのみ）
├── LEARNINGS.md                   ← 進め方の教訓（開始時に読む。**追跡対象外**。11.0）
├── Makefile                       ← 開発・ビルドの入口
├── .claude/commands/              ← 実装ステップ用のスラッシュコマンド
│
├── docs/                          ← 設計・運用に関する文書はすべてここ
│   ├── README.md                  ← 文書索引と主要な設計判断
│   ├── Requirements.md            ← 要件・構想
│   ├── Design.md                  ← 本書（全体設計）
│   ├── DbDesign.md                ← データベース設計
│   ├── ApiDesign.md               ← REST API 設計
│   ├── GuiDesign.md               ← GUI 設計
│   ├── Development.md             ← 開発環境の立ち上げ・デバッグ手順
│   ├── Deploy.md                  ← 環境別のデプロイ手順
│   ├── PROGRESS.md                ← 実装進捗（現況のみ）
│   ├── history/                   ← 完了した手順の記録（decisions.md / steps.md）
│   ├── openapi.yaml               ← 実装済みAPIの現状（ApiDesign.md 1.3）
│   └── adr/                       ← 個別の設計判断の記録
│
├── server/                        ← Go（APIサーバ + MCPサーバ + 静的配信）
│   ├── go.mod                     ← アプリの依存のみ
│   ├── sqlc.yaml                  ← migrations/ をスキーマ源として参照
│   ├── tools/go.mod               ← goose / sqlc のバージョン固定（DbDesign 5.1）
│   ├── cmd/pb/main.go             ← serve / admin create などのサブコマンド
│   ├── migrations/                ← goose。唯一のスキーマ定義（3.2）
│   │   ├── 0001_extensions_and_functions.sql
│   │   └── …                      ← Phase 1 は 0010 まで（DbDesign 5.2）
│   ├── internal/
│   │   ├── config/                ← 設定の3層（環境変数・*_FILE・DB）。10.3
│   │   ├── httpapi/               ← REST（ApiDesign.md）
│   │   │   ├── middleware/        ← 認証・認可・CSRF・レート制限・request_id
│   │   │   ├── apierr/            ← ApiDesign 2.5 のエラー形式
│   │   │   └── v1/                ← エンドポイント実装
│   │   ├── auth/                  ← 認証・認可のドメインロジック（本書6章）
│   │   ├── audit/                 ← audit_log への記録（ApiDesign 2.10）
│   │   ├── domain/                ← エンティティとビジネスルール
│   │   ├── store/
│   │   │   ├── queries/*.sql      ← 手書きSQL（sqlc の入力）
│   │   │   ├── gen/               ← sqlc 生成物（コミットする）
│   │   │   └── search/            ← 全文検索の実装を隔離（DbDesign 4.5）
│   │   ├── webui/                 ← 静的配信（3.4）
│   │   │   ├── embed.go           ← //go:embed all:dist
│   │   │   └── dist/              ← client のビルド成果物（.gitignore、雛形のみコミット）
│   │   ├── mcpsrv/                ← MCPサーバ（Phase 2）
│   │   └── ulidgen/
│   └── testdata/
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
    ├── Dockerfile                 ← マルチステージ（client build → server build → 実行）
    ├── base/                      ← 全環境で共通のもの（4.3）
    │   ├── compose.yaml
    │   ├── initdb/01_roles.sql    ← DBロール分離（DbDesign 3.4）
    │   └── env.example
    ├── dev/                       ← 開発検証環境
    │   ├── compose.yaml           ← base への上書き
    │   ├── reset.sh               ← DBを作り直してデモを投入（DbDesign 7.6.6）
    │   ├── seed/dev-data.yaml     ← デモデータの定義（DbDesign 7.6.4。コミットする）
    │   └── secrets/               ← .gitignore（.example のみコミット）
    ├── stg/                       ← ドッグフーディング用インスタンス（4.4）
    │   ├── compose.yaml           ← base への上書き（db のみ。pb-stg / :5433）
    │   ├── init.sh                ← 初回：秘密を乱数で生成し、DBを起動して migrate
    │   ├── build.sh               ← 動作に必要な一式を out/ へ出力する
    │   ├── pb.env.example         ← 出力に同梱する設定のテンプレート
    │   ├── out/                   ← .gitignore（build.sh の出力）
    │   └── secrets/               ← .gitignore（init.sh が生成。コミットしない）
    └── prod/                      ← 配布用
        ├── compose.yaml
        └── build-release.sh       ← クロスコンパイル／マルチアーキイメージ
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

sync-webui: build-client            # embed 対象へコピー（//go:embed は親を辿れない）
	rm -rf server/internal/webui/dist && mkdir -p server/internal/webui/dist
	cp -R client/dist/. server/internal/webui/dist/

build: sync-webui                   # 単一バイナリ
	cd server && CGO_ENABLED=0 go build -trimpath -o ../bin/pb ./cmd/pb
```

## 4.3 deploy/base に置くもの

「全環境で同じ」ものを `base/` に集約し、環境ごとの差分のみを `dev/` `stg/` `prod/` に置く。docker compose は複数ファイルの重ね合わせに対応している。

```
docker compose -f deploy/base/compose.yaml -f deploy/dev/compose.yaml up -d
```

| 置き場所 | 内容 |
|---|---|
| `base/compose.yaml` | `db` と `app` のサービス定義、ボリューム、ヘルスチェック、依存関係 |
| `base/initdb/` | DBロール作成（`DbDesign.md` 3.4）。環境によらず同一 |
| `base/env.example` | 必要な環境変数の一覧と説明 |
| `dev/compose.yaml` | ポートを `127.0.0.1` に公開、ログ詳細化、ソースのバインドマウント、開発用シード |
| `prod/compose.yaml` | イメージタグ固定、バインドマウントなし、`restart: always`、リソース制限 |

**`base/` の中身が育つまでは、`dev/compose.yaml` 単体で始めてよい。** 環境が1つしかない段階で共通化を先取りすると、共通部分の判断材料がないまま構造だけが増える。

## 4.4 stg の扱い

**Phase 2 で `stg/` を使い始める。** PB 自身の開発を PB で行うため（ドッグフーディング）、**開発中に壊れる `dev` とは別に、壊れないインスタンス**が要る。

| | `dev` | `stg` |
|---|---|---|
| 用途 | 開発中のコードを動かす | **PB 自身のプロジェクト管理**（チケット・憲章） |
| PB 本体 | `make run`（`go run`） | **`deploy/stg/build.sh` が出力したネイティブバイナリ一式** |
| 待受 | `127.0.0.1:8080` | `127.0.0.1:8081` |
| DB | compose プロジェクト `project-backyard`（`:5432`） | **compose プロジェクト `pb-stg`（`:5433`）** |
| 作り直し | `make dev-reset` | **しない**（データが本番相当） |

**分離は compose プロジェクトの単位で行う。** `deploy/dev/reset.sh` は `docker compose down -v` を実行して pgdata ボリュームごと破棄するため、**同じコンテナ内で DB 名を分けても `make dev-reset` 1回で消える**。compose プロジェクト名を分けると、コンテナ・ネットワーク・ボリュームが名前空間ごと分かれる。

**PB 本体をコンテナにしない。** client を embed した単一バイナリを作れる構成（3.4）であり、`stg` に必要なのは「壊れず動き続けること」だけで、コンテナの利点（再現性・隔離）は開発端末上では効きが薄い。`deploy/Dockerfile` の作成は Phase 2 の前提から外れた。

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

**「バイナリ自身に設定ファイルを読ませるのは設定機構そのものの変更であり、必要になった時点で別途扱う」と先送りしていたが、pb-2 でその時点が来た**（利用者の希望、2026-09-11）。**いまは `PB_CONFIG_FILE` が指す YAML をバイナリが直接読む**（10.3）。**`pb.env` は残る**——環境変数の層に値を流し込む道具としてで、YAML とは別の段である。

**マイグレーションはリポジトリ側から適用する。** goose は `server/tools/` のツールモジュールにあり、出力一式には含まれない（`DbDesign.md` 5.1）。スキーマを進めるのは開発端末での作業であって、配置した一式の仕事ではない。

## 4.5 ビルドとクロスコンパイル

| 目的 | 方法 |
|---|---|
| 開発端末で動かす | `make run`（`go run`）または `make build` |
| コンテナで動かす | `deploy/Dockerfile`（マルチステージ）。**ビルドもコンテナ内で行うため、ホストのアーキテクチャに依存しない** |
| 他アーキテクチャ向けイメージ | `docker buildx build --platform linux/amd64,linux/arm64` |
| ネイティブバイナリ配布 | `deploy/prod/build-release.sh` で `GOOS`/`GOARCH` を回す |

```bash
# build-release.sh の骨子
for target in darwin/arm64 linux/amd64 linux/arm64; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "dist/pb_${GOOS}_${GOARCH}" ./cmd/pb
done
```

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

## 5.2 テーブル一覧とPhase

| Phase | 領域 | テーブル | 詳細 |
|---|---|---|---|
| **1** | アクター・認証 | `actor` `app_user` `auth_provider` `user_identity` `local_credential` `access_token` | `DbDesign.md` 6.2 |
| **1** | 認可 | `permission` `role` `role_permission` `project_member` | 6.3 |
| **1** | プロジェクト | `project` `project_counter` | 6.4 |
| **1** | ワークフロー | `workflow` `workflow_status` `workflow_transition` | 6.5 |
| **1** | チケット | `ticket` `ticket_link` | 6.6 |
| **1** | コメント・添付 | `comment` `attachment` | 6.7 |
| **1** | 履歴 | `activity` `audit_log` | 6.8 |
| **1** | アジャイル | `sprint` | 6.9 |
| **1** | タグ | `tag` `ticket_tag` | 6.10 |
| **1** | 完了条件 | `dod_item` | 6.11 |
| **2** | **プロジェクト文書** | `document` `document_revision` | 8.1 |
| **2** | エージェント連携 | `agent` `task_lease` | 8.2 |
| **2** | **アプリケーション設定** | `app_setting` | `DbDesign.md` 6.14（**10.3 の第2層の置き場**） |
| **3** | エージェントの実行記録 | `agent_run` `agent_report` `context_pack_log` | 8.2 |
| **3** | 知識還流 | `knowledge` `knowledge_revision` `proposal` | 8.3 |
| **3** | AI・分析 | `comment_signal` `embedding` `project_event` `estimate_record` `contribution` | 8.4 |

**`document` は Phase 1 のテーブルを1つも変更しない**（原則1）。`ticket_reference`（6.12）から
PB 内の文書を指せるようにするかは、必要性が運用で確認できてから決める（`ApiDesign.md` 10章）。

## 5.3 設計上の要点（3点のみ）

本書の設計原則と直結する3点を挙げる。それ以外は `DbDesign.md` を参照すること。

1. **`actor` が人間とエージェントの共通基底になっている**（原則3）。`ticket.assignee_id` などはすべて `actor(id)` を参照するため、Phase 2 でエージェントを導入しても既存テーブルの変更が不要
2. **`app_user` と `user_identity` を分離している**（原則4）。Phase 3 で OIDC/SAML を追加する際、既存ユーザーを作り直さずに行の追加だけで済む
3. **`proposal` がAIの提案の唯一の入口**（原則6）。エージェントは本体テーブルを直接更新せず、承認を経て反映される

---

# 6. 認証・認可設計

**本章が認証・認可の正本である。** 対応するテーブル定義は `DbDesign.md` 6.2〜6.3、APIは `ApiDesign.md` 3〜4章・6〜7章を参照。

## 6.1 全体方針

| 項目 | Phase 1 | Phase 3（将来） |
|---|---|---|
| 認証方式 | ローカル ID（メール）/ パスワード | + OIDC / SAML |
| セッション | HttpOnly Cookie + 不透明トークン | 同左（IdP はログイン時のみ） |
| API アクセス | Bearer トークン | 同左 |
| 権限 | システムロール2種＋プロジェクトロール | + IdP グループからのロールマッピング |

**将来の IdP 連携でコードの大部分を変えないための鍵は、`user_identity` 抽象である。** 認証処理を「① 認証手段が subject を特定する → ② subject から `user_identity` を引く → ③ `app_user` を得てセッションを発行する」の3段に分離しておけば、OIDC / SAML の追加は①のアダプタ実装のみで済む。

## 6.2 認証フロー

### 6.2.1 ローカル ID/PW ログイン（Phase 1）

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

### 6.2.3 OIDC / SAML（Phase 3 の差し込み点）

Phase 1 の実装時点で、以下の**インターフェースだけ**を定義しておく。

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

## 6.3 パスワードとアカウント保護（Phase 1）

| 項目 | 方針 |
|---|---|
| ハッシュ | Argon2id、PHC 文字列で保存。パラメータは `m=64MiB, t=3, p=4` を初期値とする |
| 最小長 | 12文字。複雑性要件（記号必須等）は課さず長さを優先する |
| 自動生成パスワード | 語句連結方式（`quiet-harbor-4172-mint`）。口頭・チャットでの伝達誤りを減らす（`ApiDesign.md` 6.2） |
| 既知の漏洩パスワード | Phase 1 では未対応（オフライン動作を優先） |
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

**スコープの語彙は権限カタログのキーそのものである**（6.4.2。Phase 1 は28件、0017 で30件。`ApiDesign.md` 4.4.2）。上の積は権限キーどうしの完全一致で取るため、別の語彙を混ぜると、絞ったつもりのトークンが権限0件になるか、解釈できない語彙を通して逆に広がるかのどちらかになる。**空配列は「絞り込みなし」であって「権限0件」ではない。**

**エージェントの既定スコープもこの語彙で書く**（6.5）。改訂前の 6.5 は `ticket:read` / `ticket:claim` 等の別語彙を挙げていたが、**その語彙で発行すると実効権限が0件になる**ため、権限キーへ置き換えた（2026-08-30、手順24a）。対応表は 6.5 にある。

**エージェントは左辺の2つの層を所有者から借りる。** `actor(kind='agent')` は `app_user` の行を持たないためシステムロールの層が必ず空になり、そのままでは権限0件になる。`agent.owner_actor_id` が指す人のロールを両層に用いる（6.5、`DbDesign.md` 8.2.1）。**式そのものは変わらない**——変わるのは「誰のロールを読むか」だけである。

### 6.4.2 権限カタログ

権限をコードのif文ではなく**データとして定義**する（原則5）。カタログは Phase 1 が28件、Phase 2 の 0017 で `doc.view` / `doc.edit` を足して30件になる。`DbDesign.md` 7.2（0010）と 8.1.4（0017）のシードが正本。

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
| **operator** | system | プロジェクト・チケットの閲覧と編集、コメント、知識の提案、Excel出力 |
| **administrator** | system | **全権限**。ユーザー管理・認証設定・監査ログ・プロジェクト作成を含む |
| project_admin | project | 当該プロジェクトの全操作＋承認（`knowledge.approve` `proposal.review`）＋エージェント管理 |
| project_member | project | 当該プロジェクトのチケット作成・編集・遷移、コメント |
| project_viewer | project | 閲覧のみ |

**Phase 1 ではプロジェクトロールもUIで扱う。** ユーザー詳細のメンバーシップ欄（`GuiDesign.md` 5.6.2）で付与・変更・剥奪ができ、権限マトリクス（同 5.6.3）に5ロールすべてが並ぶ。**カスタムロールの作成と権限の編集だけが Phase 3 に残る**（`ApiDesign.md` 7.3）。

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
| チケットの状態遷移 | 呼び出し元がエージェントのとき、担当が自分の所有者か | `ApiDesign.md` 9.6 の検証6（手順26b） |

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
- エージェントからの操作は、権限に加えて①ワークフローの `is_agent_reachable`、②サーキットブレーカーの状態、③**担当が自分の所有者であること**（状態遷移のみ。`ApiDesign.md` 9.6 の検証6。手順26b）、を追加で検証する
  - **③は当初「リースの保有」だった。** リースを Phase 3 へ送ったため置き換えた（8.2）。判定の材料が `task_lease` の行から `ticket.assignee_id` に変わっただけで、**「人が引き受けていないものをエージェントが動かさない」という意図は同じ**である

## 6.5 エージェントの認証（Phase 2）

人間ユーザーとは別系統として設計する。

| 項目 | 方針 |
|---|---|
| principal | `actor(kind='agent')` + `agent` テーブル。人間アカウントの借用をしない |
| **所有者** | **`agent.owner_actor_id`（必須）。エージェントはプロジェクトメンバの誰かに紐づく**（`DbDesign.md` 8.2.1） |
| **権限** | **所有者から導く（委譲）。** 下の式を参照 |
| トークン | `access_token(token_type='agent')`。プロジェクトスコープ必須、有効期限必須。接頭辞は `pb_agt_` |
| 発行 | **本人が自分の設定から**（`/me/agents`。`ApiDesign.md` 4.5、`Requirements.md` 10.9.1 系統B）。**発行時に一度だけ全文表示** |
| スコープ既定 | `project.view` `ticket.view` `ticket.create` `ticket.transition` `ticket.assign` `comment.create` `doc.view` `agent.run` `ticket.reference.edit` `ticket.self_edit`。**語彙は権限カタログのキーそのものである**（6.4.1）。**発行時に `doc.edit` だけを足せる**（`ApiDesign.md` 4.5.3 の許可リスト。手順26a） |
| 禁止 | `ticket.close`、`doc.edit`、`knowledge` の直接更新、他プロジェクトへのアクセス |
| 信頼度 | `agent.trust_level` に応じて既定スコープを段階的に拡大（`Requirements.md` 10.10.3）。**実績の供給源が Phase 3 のため、Phase 2 では既定値のまま使わない** |
| 失効 | 本人と管理画面から即時失効。サーキットブレーカー作動時は自動失効も選択可 |

**権限は所有者から導く。** エージェントは `app_user` の行を持たないため、6.4.1 の式のうちシステムロールの層が必ず空になる。そこで**所有者の層をそのまま使う**。

```
実効権限 = ( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ トークンのスコープ
             └─ その人の持つ権限がベース              └─ エージェント独自の権限の整理
```

**これは「人間アカウントの借用」ではない。** principal もトークンも監査の `actor_id` も別のままで、**借りるのは資格情報ではなく権限の根拠**である。事故の追跡は「どのモデルが」ではなく「誰の環境で」から始められる（`Requirements.md` 10.10.3）。副次的に、**所有者を無効化するとその人のエージェントも同時に効かなくなる。** 詳細と自立エージェントへの広げ方は `DbDesign.md` 8.2.1。

**スコープ既定は権限カタログのキーで書く。** 改訂前の本節は `ticket:read` / `context:read` / `ticket:claim` / `note:write` / `result:submit` / `proposal:create` という別語彙を挙げていたが、**この語彙で発行すると実効権限が0件になる**（6.4.1 の積は権限キーどうしの完全一致で取る）。2026-08-22 に `/me/tokens` について「権限カタログのキーそのものを語彙にする」と決めてあり（`ApiDesign.md` 4.4.2）、エージェントだけ別語彙を持つ理由が無い。対応は次のとおり。

| 旧語彙 | 権限キー |
|---|---|
| `ticket:read` | `ticket.view` |
| `context:read` | `project.view` `doc.view` |
| `ticket:claim` | `ticket.transition` `ticket.assign` |
| `note:write` | `comment.create` |
| `result:submit` | `ticket.transition` |
| `proposal:create` | `ticket.create`（`proposal` は `DbDesign.md` 8.3 で Phase 3 へ送った） |

**`ticket.self_edit` は既定に入れた**（利用者の判断、2026-09-09。pb-75）。`ticket.reference.edit` と同じ理由で、**起票したチケットを直すのは実装エージェントの通常の仕事**である。**`ticket.edit` は許可リストにも入れない**——9.5.2 の全項目を開けるので、**エージェントが `execution_mode` や `scope` を自分で緩められる**（`DbDesign.md` 6.13）。

**`doc.edit` は既定に入れないが、発行時に足せる。** `pb_put_doc` にこの権限が要る（8.2）が、載せるかは**そのエージェントが誰に付いているか**で決まる——PM のエージェントは持ち、実装だけを行うエージェントは持たない。**そもそも所有者が `doc.edit` を持たなければ、スコープに書いても積で消える**（持つのは `project_admin` だけである。`DbDesign.md` 8.1.4）。

**手順26a まで、これは実行できなかった。** `ApiDesign.md` 4.5.3 が `scopes` を受け取らず既定を固定していたため、**`pb_put_doc` は誰が呼んでも必ず 403 になる**状態だった。26a で 4.5.3 に許可リスト（既定8件 ∪ `doc.edit`）を入れ、本節の「誰に付いているかで決まる」を発行の口で表せるようにした。**`ticket.close` は許可リストにも入れない**——本節の禁止のうち、`doc.edit` だけが「決まる」と書かれている。

**`agent.run` を既定に含める。** `DbDesign.md` 8.2.6 で `operator` / `project_member` / `project_viewer` へ配り直しており、所有者が持つ権限になった。

**`ticket.reference.edit` を既定に含める**（pb-68。利用者の判断、2026-09-08）。`pb_add_reference` が要求する権限で（8.2）、**作業の跡を残すのは実装エージェントの通常の仕事**だから既定に置く——`doc.edit` のように「誰に付いているか」で変わらない。**`ticket.edit` を既定にも許可リストにも入れない**：外部参照だけでなく本文・担当・期日の書き換えや並べ替えまで開いてしまうためで、そこを切り出すために 0027 で権限を新設した（`DbDesign.md` 6.12.1）。

**エージェントによるクローズ禁止はDBレベルでも担保する。** ワークフローの `done` ステータスは `is_agent_reachable = false`、遷移の `allowed_actor_kinds` は `["user"]`（`DbDesign.md` 7.4）。

## 6.6 ネットワークと転送（Phase 1）

- アプリは既定で `127.0.0.1` にのみ公開する。コンテナ内は `0.0.0.0:8080` で待ち受け、公開範囲は compose の `ports` で制御する（`DbDesign.md` 3.2）
- Cookie は `HttpOnly` `SameSite=Lax`。HTTPS 提供時は `Secure` を付与
- CSRF：Cookie 認証の状態変更系リクエストに CSRF トークンを要求する。Bearer トークン認証の場合は不要（`ApiDesign.md` 2.4）
- CORS：既定で同一オリジンのみ許可
- **DBはアプリ実行時ロール `pb_app`（DML のみ）で接続する。** DDL権限を持つ `pb_owner` と分離し、実行時のSQLインジェクションでテーブルを落とせないようにする（`DbDesign.md` 3.4）

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
| チケット | `/projects/:key/tickets` 系、`/tags`、`/sprints`、`/stats`、`/activity` | 確定（`ApiDesign.md` 9章。実装は手順16〜19） |

**本表の「確定」は設計が確定した意味であり、実装済みという意味ではない。** `docs/openapi.yaml` に載るのは実装が済んだものだけである（役割と食い違い時の扱いは `ApiDesign.md` 1.3）。

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

**正本は `Requirements.md` 10.3.2**（Phase 列つきの一覧）。ここでは REST 側の対応と必要権限だけを示す。

| ツール | REST | 必要権限 |
|---|---|---|
| `pb_get_project` | `GET /projects/:key` | `project.view` |
| `pb_list_docs` | `GET /projects/:key/docs?outline=1` | `doc.view` |
| `pb_get_doc` | `GET /projects/:key/docs/*path?section=` | `doc.view` |
| `pb_get_task` / `pb_list_tasks` | `GET /projects/:key/tickets(/:seq)` | `ticket.view` |
| `pb_get_context` | **合成**（`GET /tickets/:seq` ＋ `GET /docs` ＋ `GET /docs/*path`。8.5.5） | `ticket.view` `doc.view` |
| `pb_create_ticket` | `POST /projects/:key/tickets` | `ticket.create` |
| `pb_update_ticket` | `PATCH /projects/:key/tickets/:seq` | **`ticket.self_edit`**（pb-75） |
| `pb_put_dod` | `GET|POST|PATCH|DELETE /projects/:key/tickets/:seq/dod` | **`ticket.self_edit`**（pb-75） |
| `pb_list_tags` | `GET /projects/:key/tags` | `ticket.view`（pb-76） |
| `pb_put_doc` | `PATCH /projects/:key/docs/*path` | **`doc.edit`** |
| `pb_post_note` | `POST /projects/:key/tickets/:seq/comments` | `comment.create` |
| `pb_add_reference` | `POST /projects/:key/tickets/:seq/references` | **`ticket.reference.edit`** |
| `pb_transition_task` | `POST /projects/:key/tickets/:seq/transition` | `ticket.transition` |
| `pb_list_transitions` | `GET /projects/:key/tickets/:seq/transitions` | `ticket.view` |
| `pb_submit_result` | `POST /projects/:key/tickets/:seq/reports`（`ApiDesign.md` 9.15） | `ticket.transition` |
| `pb_claim_task` / `pb_release_task`（**Phase 3**） | リース（`DbDesign.md` 8.2.2） | `ticket.transition` |

**手順26 は3つに分かれる**（利用者の判断、2026-09-05）。

| | ツール | 状態 |
|---|---|---|
| **26a** | `pb_create_ticket` / `pb_put_doc` / `pb_post_note` | **叩く REST が実装済み**なので、MCP 層だけで足りる |
| **26b** | `pb_transition_task` / `pb_list_transitions` | **叩く REST（9.6 / 9.7）は Phase 1 から在る。** 足すのは MCP の口と、`ticket.working_agent_id`（`DbDesign.md` 6.6）と、9.6 の検証6 |
| **26c** | `pb_submit_result` | **格納先の `agent_run` / `agent_report` を Phase 3 から戻した**（11章）。0022 を足し、**REST 側も新設した**（`ApiDesign.md` 9.15。遷移API を叩く形にはならなかった） |

**分けたのは、26b と 26c が新しい設計を約20件要求するためである**（リースのステータス遷移先・TTL の延長点・`stale` の判定・完了レポートの検証範囲・`proposed_subtasks` の格納先など）。11.2.1 のとおり、**26a の時点で検証・記録・コミットまで終える。**

### 26b はリースをやめ、状態遷移を開けた（2026-09-05）

**当初の 26b は `pb_claim_task` / `pb_release_task` だった。** 実装前の一括確認で、利用者から「**汎用的なプロジェクト管理から見ると claim / リースに違和感がある**」という指摘があり、設計を組み直した。

**きっかけは、リースが何を排他するのかを数えたことである。** `task_lease` に言及する設計文書の全16か所を調べたところ、**「リースを保持している間、他者の◯◯を拒む」と書かれた箇所が1つも無かった。** `Requirements.md` 10.3.3 自身が「**第一の目的は、いま誰が触っているかを他の参加者に見せること**」「少人数運用では**緩やかな整合**で実害はない」と書いている。**設計上のリースは掲示であって錠ではなく、`lease_token`（能力トークン）という名前だけが錠の語彙を持ち込んでいた。**

そのうえでリースが解こうとしていた3つを分解した（詳細は `DbDesign.md` 6.6）。

| 解こうとしていたもの | Phase 2 での判定 |
|---|---|
| 可視性 | **`assignee_id` ＋ `working_agent_id` ＋ `status_key` で足りる。** TTL 30分はエージェントのセッションの時間尺度で、PB が目指す分野横断のプロジェクト管理には合わない |
| 排他 | **Phase 2 では発生しない。** `/pb-implement <seq>` は人が番号を指定して走らせる。エージェントが自律的に拾うのは `pb_next_task`（Phase 3） |
| 詰まり防止 | 占有しないので詰まらない |

**代わりに見えたのが、本当の穴だった**——**設計原則7 が「エージェントから見える面は MCP のみ」と定めているのに、状態遷移（9.6 / 9.7）は Phase 1 から REST に在って MCP に無かった。** `Requirements.md` 10.3.2 が `pb_claim_task` の説明に「着手宣言。**ステータスを「実装中」へ**」と書いていたため、**「状態を動かす機能」がリースの中に埋まって見えなくなっていた。** 26b はそれを取り出す手順になった。

**副次的に、担当欄をめぐる食い違いが1つ解消した**（`DbDesign.md` 6.6）。

**`pb_claim_task` / `pb_release_task` は Phase 3 へ送った**（`Requirements.md` 10.3.2）。**再検討の条件は自律取得（`pb_next_task`）の実装である**——そのときは `working_agent_id` を「宣言」から「条件」へ格上げすれば足り、テーブルを足さずに済む。TTL による失効（`stale` の検知）が要ると分かった時点で `task_lease` の器を起こす。

**`pb_put_doc` は `doc.edit` を要求する。** エージェントのトークンにこの権限を載せるかは、**そのエージェントが誰に付いているか**で決まる（`Requirements.md` 10.10.3）。PM のエージェントは持ち、実装だけを行うエージェントは持たない。**発行時に許可リストから選ぶ**（`ApiDesign.md` 4.5.3。手順26a で 6.5 とあわせて改訂した）。

**トークンや接続情報を返すツールを一切持たない**（`Requirements.md` 10.3.1）。

## 8.3 エンドポイントとスコープ

```
/mcp/<project_key>
```

**URL パスにプロジェクトキーを含める**（`Requirements.md` 10.8.8）。これにより手順ファイルがプロジェクト非依存になり、すべてのリポジトリで同じ雛形を使い回せる。

**トークンのプロジェクトスコープと URL の整合はサーバが検証する。** 食い違えば `404`——6.4.5 が定める「トークンが特定のプロジェクトに紐づく場合、他プロジェクトは 404」の実施点がここである。

**エンドポイント自体の必要権限は `agent.run`。** 0019 がこのキーの意味を「自分に紐づくエージェントを MCP から走らせてよい」と定め、`operator` / `project_member` / `project_viewer` へ配り直した（`DbDesign.md` 8.2.6）。個々のツールの権限（8.2）はその内側で REST 層が判定する。

**Cookie では通さない。Bearer トークンだけを受ける。** `ApiDesign.md` 2.4 は「CSRF トークンを要求するのは Cookie 認証のときだけ」と定めており、MCP の口を Cookie に開けると、CSRF の検証を持たない `POST` が1本増える。読み取りだけの手順25 では実害が小さいが、**手順26 で write 系ツールが入った時点で穴になる**ため、最初から閉じておく。MCP クライアントが PB の Cookie を持つことはない。

## 8.4 プロトコル（手順25）

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

## 8.5 ツールの引数と応答（手順25・26a・26b）

### 8.5.1 write 系（手順26a）

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_create_ticket` | `type`, `title`, `body_md?`, `priority?`, `parent_seq?`, `assignee_id?`, `tag_ids?`, `estimate_point?`, `estimate_hours?`, `start_date?`, `due_date?` | `POST /projects/:key/tickets` | 9.5.1 の応答をそのまま |
| `pb_update_ticket` | `seq`, ＋ 上の任意引数から **`type` を除いたもの**（**送ったものだけ更新**） | `PATCH /projects/:key/tickets/:seq` | 9.5.1 の応答をそのまま |
| `pb_put_dod` | `seq`, `add?[]`, `update?[]`, `delete?[]` | 9.9 の `POST` / `PATCH` / `DELETE` | 9.9 の一覧をそのまま |
| `pb_list_tags` | （なし） | `GET /projects/:key/tags` | 9.11 の一覧をそのまま |
| `pb_put_doc` | `path`, `body_md`, `change_reason?` | `GET` してから `PATCH /projects/:key/docs/*path` | 10.3 の応答をそのまま |
| `pb_post_note` | `seq`, `body_md`, `kind?` | `POST /projects/:key/tickets/:seq/comments` | 9.8 の1件をそのまま |
| `pb_add_reference` | `seq`, `repository`, `branch?`, `commit_sha?`, `url?`, `label?`, `note?`, `kind?` | `POST /projects/:key/tickets/:seq/references` | 9.10.2 の1件をそのまま |

**引数の名前は `ApiDesign.md` の本体フィールドに揃える**（`body` ではなく `body_md`、`parent` ではなく `parent_seq`）。8.5 の冒頭が述べるとおり、名前が一致していればエージェントは迷ったときに設計文書を引ける。`Requirements.md` 10.3.2 は `body` / `parent` / `task_id` と書いていたが、**あちらを実装に合わせて改訂した**。

**`assignee_id` は `me` を受ける。** read 系の `assignee` と同じ写し方をする（下記）——**エージェントはアクターの ULID を知らない**ため、`me` を通さないと担当を付ける経路が実質無い。ULID をそのまま渡すこともできる。

**開けない引数がある。** `pb_post_note` は `in_reply_to` を渡せない——**コメントの ULID を得る経路が無い**ためである。**増やすときは本表を先に直す。**

**改訂前は、見積2種・日付2種・`tag_ids` / `sprint_id` も閉じていた**（理由は「いずれも ULID か画面の文脈が要り、エージェントが持たない」）。**pb-76 で3つに分けて見直した**（利用者の判断、2026-09-09）。

| | 判定 |
|---|---|
| 見積2種・日付2種 | **開けた。** 数値と日付であって ULID ではない。**この理由は最初から当てはまっていなかった**——編集の口を作る前提が無かった時期に、まとめて閉じたものと見られる |
| `tag_ids` | **開けた。** `pb_list_tags` を足したので**列挙できる**。ULID が要るという理由はここで解消した |
| `sprint_id` | **開けない。** 理由が変わった——**0028 以降どの経路からも書けない**（`ApiDesign.md` 9.5.2 の `use_sprint_endpoint`）。所属を動かすのはスプリントの開始・終了だけである（9.12.1 / 9.12.2） |

**`actual_hours` も開けない。** `pb_submit_result` の `cost.wall_clock_min` と二重になり、**どちらが正本か決まらない**（pb-76 の判断②）。

**`pb_list_sprints` は足さない。** 設定できない以上、列挙する用途が無い——**読むだけなら `pb_get_task` の応答が `sprint: {id, name}` を返している**（9.5.1）。

#### `pb_update_ticket` — 起票したあと直す（pb-75）

**「作れるものは直せる。ただし `type` を除く」を線にした。** `pb_create_ticket` が受ける集合と揃えてあり、**起票時に選べる項目を直せないのは筋が通らない**——が、**種別の切り替えは人が行う**（利用者の判断、2026-09-09）。タスクをエピックへ変えると、その行はバックログから消えてフィルタの選択肢になる（`GuiDesign.md` 5.4）ので、**記述を整えるつもりで盤面の見え方を変えてしまう。**

**塞いでいるのは REST 側である**（9.5.2 の `ticket.self_edit` が `type` を受け付けない）。**MCP 層で引数を落としているのではない**——8.1 の「MCP に独自の規則を置かない」を保つ。

**部分更新である**（送った項目だけ）。`PATCH`（9.5.2）と同じ形にした。**`pb_put_doc` が全置換なのは `If-Match` の都合**であり、本文も同じにする理由は無い。

**`If-Match` は MCP 層が内部で取る。** 9.5.2 は `If-Match` を必須とするが、`pb_get_task` は `version` を返すので**エージェントが渡すこともできる**——それでも内部で取るのは、`pb_put_doc` と同じ形に揃えるためと、**読んでから書くまでの間に人が直したときに競合を検出できる**ようにするためである。**競合（`409`）は `isError` のツール結果**として返し、モデルが読み直してやり直せるようにする。

**`execution_mode` / `readiness` / `readiness_note` / `scope` は引数に無い。** REST 側も `ticket.self_edit` では受け付けない（9.5.2）ので、**MCP 層で塞いでいるのではない**——8.1 の「MCP に独自の規則を置かない」を保つ。

#### `pb_put_dod` — 完了条件を整える（pb-75）

**いまある DoD に対する追加・編集・削除を、まとめて1回で受ける。** 9.9 は3本のエンドポイントに分かれているが、**DoD は「一覧をあるべき形にする」操作**であり、1件ずつ往復させると n 回の呼び出しになる。

**一覧の全置換にはしない。** 全置換だと送る側が全項目の ULID を持つ必要があり、**読んでから書くまでの間に他の人が足した項目を黙って消す。** 差分で受ければ、触っていない項目は残る。

**`is_satisfied` は引数に無い。** REST 側も `ticket.self_edit` では受け付けない（9.9）。**`pb_submit_result` が「盤面を動かさない」と決めた判断と正面からぶつかる**ためで、**完了の判定は人が行う。**

**応答は 9.9 の一覧をそのまま返す。** 何件足して何件消したかを MCP 層で組み立てない（8.1）。

**`pb_put_doc` は内部で2往復する。** 10.4 が `PATCH` に `If-Match` を必須とする一方、`pb_get_doc` は本文の Markdown しか返さないので（8.5 の表）**エージェントは `version` を持てない**。MCP 層が `GET` で読んで `If-Match` に載せる。**`409 conflict` は `isError` のツール結果**として返す——「他の人が更新したので読み直してやり直す」はモデルが判断できることであり、8.4 が定める「ツール呼び出しの結果」に当たる。

**これは MCP 層が独自のルールを持つことにはならない**（8.1）。楽観ロックの判定は REST 側のままで、MCP がしているのは**エージェントが渡せない値を、同じ REST から取ってくる**ことだけである。

**`pb_put_doc` は本文を全置換する。** 10.4 の `PATCH` がそうであり、章だけを差し替える口は無い。`?section=` は読む側（10.3）にしかない。**エージェントは `pb_get_doc` で全文を読み、直した全文を渡す。**

**write 系も応答は REST の JSON をそのままである。** `Requirements.md` 10.3.2 は戻り値を「チケットID・`seq`」「リビジョン番号」と書いていたが、**絞ると 8.1 の「整形の規則を MCP 層に置かない」に反する**うえ、`PATCH .../docs` の応答は `revision_no` を持たない（10.3 の形）。**あちらを改訂した。**

**冪等キー（`idempotency_key`）は受けない**（`Requirements.md` 10.3.4 の改訂。再検討の条件は 8.6）。

#### `pb_add_reference` — 作業の跡を積む（pb-68）

**表を新設していない。** `ticket_reference`（`DbDesign.md` 6.12）が 0016 から `repository` / `branch` / `commit_sha` を持ち、**6.12 自身が「`kind='code'` の書き手はエージェント」「作業の経過として追記されて積み上がる」と定めていた**。REST も 9.10.2 として実装済みで、**欠けていたのは MCP の口と権限だけだった。**

**`kind` の既定は `code` である。** 省略できるのは、このツールを呼ぶ動機がほぼ `code` だからで、**`doc` も渡せる**——塞ぐと 8.1 の「MCP 層に独自の規則を置かない」に反する。**既定を MCP 層に置くことは、本節に書いてある限り隠れた規則にならない。**

**必須は `seq` だけにしてある。** `repository`（`code` のとき）と `url`（`doc` のとき）の出し分けは 9.10.2 の検証がそのまま返す。**スキーマ側で条件付き必須を組むと、判定が REST と MCP の2か所に分かれる。**

**`sort_order` は開けない。** 9.10.2 が省略時に末尾（現在の最大値 + 10）へ置き、6.12 が「並びは `sort_order` ではなく `created_at` が実質の軸」と述べている。**積む順がそのまま並びになるので、エージェントが決める値が無い。**

**更新と削除の口は作らない。** 6.12 が「画面が持つのは**表示と削除**だけで、誤って積まれた行を人が始末できるようにする」と定めており、**エージェント側は追記専用**にする。積み間違いを人が消せる形を保つほうが、エージェントが自分の跡を消せることより価値がある。

**読む口も作らない。** `pb_get_task` の応答（9.5.1）が `references` を実数で含むため、**既に読めている**。

### 8.5.3 遷移系（手順26b）

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_list_transitions` | `seq` | `GET /projects/:key/tickets/:seq/transitions` | 9.7 の応答をそのまま |
| `pb_transition_task` | `seq`, `to`, `comment?` | `POST /projects/:key/tickets/:seq/transition` | 9.5.1 の応答をそのまま |

**`pb_list_transitions` を別のツールとして出す。** 9.7 は**遷移できない先も `allowed: false` と日本語の理由を付けて返す**ので、エージェントが盲目的に `pb_transition_task` を試して失敗を繰り返すのを防げる。9.6 の検証6（担当が所有者でない）もここに現れるため、**「なぜ進められないか」を1往復で知れる。**

**`pb_get_task` の応答に畳まない。** 9.7 がエンドポイントを分けている理由がそのまま効く——遷移先は `PATCH` のたびに再計算が要り、詳細を読むだけの呼び出しにその計算を載せない。

**`to` はステータスキーである**（`in_progress` などの英字キー。表示名の「進行中」ではない）。`pb_get_project` の `workflow.statuses[]` と `pb_list_transitions` の `items[].key` がその語彙を返す。

**`comment` を開けている。** 9.6 が「同じトランザクションで `kind='progress'` のコメントを作る」と定めており、**遷移だけ通って経緯が残らない状態を作らない**ためである。`pb_post_note` を別に呼ばせると2往復になり、途中で落ちると遷移だけが残る。

**`done` への遷移は開けなくてよい。** 3つのワークフローテンプレートすべてで `done` は `is_agent_reachable = false` かつ遷移の `allowed_actor_kinds` が `["user"]` であり（`DbDesign.md` 7.4）、**DB とワークフローが拒む**（`Requirements.md` 10.8.6 の禁止事項）。MCP 層に `if` を置かない（8.1）。

**遷移に成功すると `ticket.working_agent_id` が呼び出し元のエージェントになる**（`ApiDesign.md` 9.6）。**MCP 層は何もしない**——REST 側の副作用であり、人が画面から遷移したときと同じ経路を通る。

### 8.5.4 完了レポート系（手順26c）

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_submit_result` | `seq`, `status`, `artifacts?`, `dod_results?`, `findings?`, `failures?`, `proposed_subtasks?`, `knowledge_impact?`, `cost?` | `POST /projects/:key/tickets/:seq/reports` | 9.15 の応答をそのまま |

**引数を平らにする。** `Requirements.md` 10.3.2 は `task_id` と `report`（10.6.1 のオブジェクト）
と書いていたが、**あちらを実装に合わせて改訂した**。理由は 8.5.1 と同じ——引数の名前が REST の
本体フィールドに一致していれば、エージェントは迷ったときに設計文書を引ける。`task_id` を
`seq` にするのも `pb_get_task` と同じ理由である（8.5.2）。

**`pb_submit_result` は状態を進めない**（利用者の判断、2026-09-05）。26b で遷移が
`pb_transition_task` として独立したので、**完了レポートの提出と状態遷移を1つのツールに
混ぜない**。**チケットもクローズしない**——`done` は `is_agent_reachable = false` かつ
`allowed_actor_kinds = ["user"]` で、DB とワークフローが拒む（`DbDesign.md` 7.4）。
`Requirements.md` 10.8.6 の禁止事項が、MCP 層の `if` ではなくワークフローで守られている
（8.1）。

**応答の `unsatisfied_dod` が、エージェントの次の一手を決める。** サーバはチケットの完了条件を
数え上げ、**レポートの `dod_results` に `passed: true` として現れなかった項目**を返す
（9.15）。`/pb-implement` の手順7 が「未充足の完了条件が返されたら修正して再提出する」と
定めており（`Requirements.md` 10.8.6）、その判断材料がこれである。

**完了条件のチェック（`dod_item.is_satisfied`）は動かない。** いま API が開けている DoD の型は
`manual` だけで、その定義は「人間がチェックを入れる」である（`Requirements.md` 10.5.2）。
**エージェントが立てると型の定義に反する**ので、盤面は人が動かす（9.15）。

**提出は完了レポートのコメントを1件作る**（`kind='progress'`）。**人がレポートを読む面が
コメント欄である**——チケット詳細のコメント欄は遷移・作業中のノート・人の議論が時系列に
並ぶ場所で、**完了の報告もそこに並ぶのが読む順序として自然である**（利用者の判断、
2026-09-05）。**整形は REST 層が行う**（8.1。MCP 層に置くと同じ規則が2か所に生まれる）。

**`proposed_subtasks` はレポートに残るだけで、チケットにならない。** 承認キュー（`proposal`）は
Phase 3 であり、人が読んで要ると判断すれば `pb_create_ticket`（26a）を呼ばせれば済む。
**承認なしに盤面が増える経路を作らない。**

### 8.5.2 read 系（手順25）

**応答は REST の JSON をそのまま `content[0].text` に載せる**（`pb_get_doc` の本文だけは Markdown 生）。整形の規則を MCP 層に置くと、同じ規則が REST と2か所に生まれる（8.1）。フィールド名が `ApiDesign.md` と一致していれば、エージェントは迷ったときに設計文書を引ける。

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_get_project` | — | `GET /projects/:key` | 5.4 の応答をそのまま |
| `pb_list_docs` | — | `GET /projects/:key/docs?outline=1` | 10.2 の応答をそのまま（**目次と見出しだけ。本文は含まない**） |
| `pb_get_doc` | `path`, `section?` | `GET /projects/:key/docs/*path` | **本文の Markdown**。`section` を指定すればその章だけ |
| `pb_get_task` | `seq` | `GET /projects/:key/tickets/:seq` | 9.5.1 の応答をそのまま |
| `pb_list_tasks` | `status?`, `status_category?`, `assignee?`, `open?`, `parent?`, `per_page?` | `GET /projects/:key/tickets` | **軽量な部分集合**（下記） |

**`pb_get_task` の引数は `seq` である**（`Requirements.md` 10.3.2 は `id` と書いていた）。9.1 が「URL とチケット番号を一致させる」と定めており、人が画面で見る番号も `/pb-implement <id>` に渡す値も `seq` である。`id`（ULID）を名乗ると、ULID を渡す呼び出しが必ず出る。

**`pb_list_tasks` は軽量にする**（`Requirements.md` 10.3.2 の「チケット一覧（軽量）」）。9.2.2 の応答から次の11項目だけを残す。

```
seq / type / title / status / priority / assignee / working_agent
  / parent_seq / staged_at / due_date / updated_at
```

**`working_agent` は手順26b で11項目目にした。** 排他が無いため（`ApiDesign.md` 9.6 は上書きを許す）、**同じ所有者の別のエージェントが既に触ったチケットを、それと知らずにもう一度進めることが起こりうる。** `/pb-onboard` の `pb_list_tasks(assignee=me)` で見えていれば、モデルが気づける。

落とすのは `id`（`seq` で足りる）、`sort_key`（画面の並べ替え用）、`tags` `sprint` `has_children` `reporter`、見積3種、`start_date` `closed_at` `version` `created_at` である。**ボードの状況把握に要らない項目を、一覧の件数ぶん掛け算しない。** 1件の詳細が要るときは `pb_get_task` が全項目を返す。

**`assignee` に `me` を渡したときは、エージェントの所有者を指す。** エージェントのアクターに担当は付かない（担当は人が持つ）ため、`me` を文字どおり解釈すると `/pb-onboard` の「自分の担当を知る」が必ず0件になる。**6.5 の委譲が「権限の根拠は所有者」と定めているのと同じ理由で、担当の視点も所有者に置く**。引数の値を書き換えるだけなので、MCP 層が独自のルールを持つことにはならない（8.1）。人のトークン（`token_type='api'`）で叩いたときは、従来どおりその人自身を指す。

### 8.5.5 コンテキストパック（手順27）

| ツール | 引数 | 叩く REST | 応答 |
|---|---|---|---|
| `pb_get_context` | `seq` | `GET /projects/:key/tickets/:seq` ＋ `GET /projects/:key/docs?outline=1` ＋ 文書ごとに `GET /projects/:key/docs/*path` | **Markdown 1枚** |

**引数は `seq` である**（`Requirements.md` 10.3.2 は `task_id` と書いていた。**あちらを実装に
合わせて改訂した**）。理由は 8.5.2 の `pb_get_task` と同じで、人が画面で見る番号も
`/pb-implement <id>` に渡す値も `seq` である。

#### REST に専用のエンドポイントを作らない

**合成は MCP 層で行う。** 8.1 の表が**両方向から**この置き場を指している——MCP が持つものは
「**応答の整形、トークン予算**」であり、REST が**持たない**ものは「**エージェント向けの
言い換え**」である。コンテキストパックはその3つそのものである。

**必要権限が2つ（`ticket.view` `doc.view`）であることも、内側で2種類の REST を叩く形と
一致する**（8.2）。専用のエンドポイントを1本置くと、ルート定義に権限を AND で2段重ねる
ことになり、**どちらが何のための権限かがルート定義から読めなくなる**（6.4.4）。**読む画面が
無いまま公開 API が1本増える**ことでもある。

**代償は内部呼び出しの本数である**（チケット1 ＋ 目次1 ＋ 本文の数）。同一プロセス内の
呼び出しなので、憲章が4文書のうちは測れる差にならない。**文書の数に比例する**ので、
8.6 の再検討条件（`PROGRESS.md` と `history/` の移譲）が来たときに、選定と一緒に見直す。

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
## 3. 憲章                        ← 同 優先度2（Phase 2 は文書がメモリの代わり）
## 4. 依存・関連するチケット      ← 同 優先度5
## 5. 足りないときの調べ方        ← 10.4.3 の 4
```

**本文・完了条件・コメントを入れない。** 10.4.2 の優先度表が構成要素に挙げていないためであり、
**`/pb-implement` は手順1 で `pb_get_task` を先に呼んでいる**（`Requirements.md` 10.8.6）。
**同じ本文を2回運ぶと、押し付けたいものが薄まる。**

**スコープ境界が未設定のときは、空欄にせず文を出す**——「このチケットに境界は設定されて
いない。境界の外かもしれない変更が要ると判断したら、実装せず利用者に相談すること」。
`ApiDesign.md` 9.5.2 が書く経路を開けたのは手順27 だが、**既存のチケットは当分すべて空**で
あり、空欄を見たモデルが「制約が無い」と読むのは、境界が無いことより悪い。

**憲章は全文を載せる**（8.6）。**該当章を選ばない。**

**ただし「エージェントの参画情報」（`agent-onboarding`）だけを除く**（手順28c）。これは
**参画時に一度読む手順**であって、判断の拠りどころではない（`Requirements.md` 10.6.2）。
**チケットごとのパックに毎回運ぶと、押し付けたいもの（スコープ境界・規約）が薄まる**
——本文と完了条件を入れない理由と同じである。

**除外は `path` の完全一致で見る。** 木のどこにあっても効く規則にすると、**たまたま同じ
`slug` を付けた別の文書まで黙って落ちる。** この文書を他の文書の下へ移すと憲章に戻るが、
**落としたことは応答に1行出るので、移した人が気づける**（下記）。

**落としたことを1行書く。** 10.4.3 の 4「切り詰めた事実を応答に明記する」がそのまま当たる。
`doc.view` を持たないトークンで憲章ごと省くときに理由の1行を出しているのと同じ形で、
**実際に落ちたときだけ出す**（文書が無いプロジェクトでは何も出ない）。

**埋め込むとき、本文の見出しを2段下げる。** パックは `#`（表題）→ `##`（節）→
`###`（文書）を使うので、文書の中の見出しは4段目から始まる。**実サーバで描画して
初めて見えた問題である**——stg の憲章は本文が `##` で始まるため、`## 3. 憲章` の次に
`## PB とは何か` が並び、**後続の `## 4. 依存・関連するチケット` が憲章の中にあるのか
外にあるのかが読めなくなっていた。** 木の形が壊れると、モデルは「どこまでが押し付けか」を
取り違える。**本文そのものは書き換えず、`#` の数だけを変える**（上限の6段で止め、
コードブロックの中は触らない）。

**依存・関連は 9.5.1 の `parent` / `children` / `links` から作る。** 相手の `status` を
含んだ形で返ってくるので、**追加の往復が要らない**（10.4.2 優先度5 の「前提タスクの成果物、
影響範囲」に当たるのがこれである）。

**5節目に深掘りの入口を書く**（`pb_get_doc` / `pb_list_transitions` / `pb_get_task`）。
10.4.3 の 4 が「切り詰めた事実と、深掘り用のクエリ例を応答に明記する」と定めており、
**いまは切り詰めが起きないが、入口だけは先に出しておく**——パックに無いものを探す手段が
書かれていないと、モデルは推測で埋める。

#### `budget` を受けない

**`Requirements.md` 10.3.2 は `budget?` を挙げていたが、あちらを改訂した**（10.4.3 も）。
憲章が全文で 3,601 文字である以上、**予算が効く場面が無い。** 26a が `idempotency_key` に
ついて下した判断と同じ形で、**器を先に作らない**（8.6 に再検討の条件を書いた）。

**渡す側が居ないことも理由である。** `budget` を決めるのは MCP クライアントだが、**モデルが
概算トークン数を選ぶ根拠を持たない**——数字を1つ発明させることになる。切り詰めが要る規模に
なったら、**予算は呼び出し側の引数ではなくサーバ側の上限として置くほうが、値の根拠を
PB が持てる。**

#### `context_pack_log` に書かない

**`DbDesign.md` 8.2.5 の器は 0022 で在るが、手順27 でも書かない**（利用者の判断、2026-09-05）。

**結び先が無いためである。** `agent_run` は `pb_submit_result` のときにしか作られない
（8.5.4、`DbDesign.md` 8.2.4）ので、パックを返す時点では **`agent_run_id` が必ず `NULL` に
なる。** `Requirements.md` 10.4.4 の効果計測は **`agent_report.status` との突き合わせ**が
本体であり、**結べない行を貯めても計測にならない。** 10.10.7 の監査（何を見せたか）だけなら
成り立つが、**そのために書き手を1つ置くと、Phase 3 で結べる形へ変えるときに既存行の
扱いが要る。**

**再検討の条件は、run の開始を告げる口ができたときである**（`agent_run.status` の
`running` を立てる経路。`docs/PROGRESS.md` の引き継ぎ `[Phase 3 の入口]` ①と同じ）。
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

- **公式 Go SDK（`github.com/modelcontextprotocol/go-sdk`）へ乗り換えるか。** 手順25 は自前実装を採った——話す必要があるのが4メソッドだけで、8.1 が MCP 層を薄く保つと定めているためである（`Design.md` 3.1 がログと設定で下している判断と同じ形）。**次のいずれかが起きたら再検討する**
  - **PB が話す面が増えたとき。** `resources` / `prompts` / サーバ発の通知 / SSE ストリーム——とくに**長く走るツールの進捗をクライアントへ返したくなったとき**（手順26 以降の write 系で起こりうる）。8.4 が「SSE が要らない」と言えているのは read 系が1往復で終わるからで、その前提が消えたら判断ごと変わる
  - **認可が MCP の Authorization 仕様（OAuth 2.1）へ寄ったとき。** いまは静的な Bearer トークン1本（6.5）だが、メタデータの配布・動的クライアント登録・トークン検証まで自前で持つのは割に合わない
  - **SDK が v1 に達し、破壊的変更が収まったとき。** v0.x のあいだ依存に入れると、追随の手間が手順26〜28 に乗る
  - **判断の材料は `server/internal/mcp` の行数である。** 4メソッドで数百行なら自前が安い。**仕様への追随のために膨らみ始めたら、それは SDK が引き受けている仕事を書き写している**という合図であり、そこが乗り換え時である
- **write 系の冪等キー（`idempotency_key`）を受けるか。** `Requirements.md` 10.3.4 は「write 系ツールは `idempotency_key` を受け付ける」と定めていたが、**手順26a では受けない**ことにし、あちらを改訂した。**本当の冪等性には「キー → 結果」を持つ器が要り**、8.1 が「MCP に独自のビジネスルールを置かない」と定める以上、置き場は REST 層＝全クライアントに効く変更になる。26a の3ツールは**再送が安全側に倒れる**——`pb_put_doc` は `If-Match` があるので古い版での再送が `409`、`pb_create_ticket` と `pb_post_note` の重複は画面で見えて人が消せる。**次のいずれかが起きたら再検討する**
  - ~~**リースが入ったとき（手順26b）。**~~ **消化した**（2026-09-05）。26b はリースを採らず状態遷移を開けたので、条件そのものが立たなくなった。**`pb_transition_task` の再送は安全側に倒れる**——2度目は「進行中 → 進行中」を要求することになり、`ck_workflow_transition_diff` により定義が存在しないため `409 invalid_transition` で弾かれる（`ApiDesign.md` 9.6 の検証2）。**リースを Phase 3 で起こすときに、この条件も一緒に戻す**
  - **無人実行に踏み込んだとき**（`Requirements.md` 10.11 の将来対応）。人が同席していれば重複は目で拾えるが、同席しないなら拾えない
  - **判断の材料は「再送で何が二重になるか」を1つ言えるかである。** 言えないうちは器を作らない

- **ツール description の文面設計**（`Requirements.md` 10.13）。**実質的にこれがエージェントの行動を規定する**ため、プロンプトエンジニアリングの対象になる。**手順25 では日本語で書いた**——憲章・チケット・文書がすべて日本語であり、description が指示する語彙と、エージェントが読む対象の語彙を揃えるためである（英語より毎セッション数百トークン多く消費する）
- ~~**コンテキストパックの生成アルゴリズムとトークン予算配分**~~／~~**応答形式**~~／~~**どの章を「該当章」と判定するか**~~ **決着した**（手順27。8.5.5）。**憲章は選定せず全文を載せ、応答は Markdown 1枚とし、`budget` は受けない。**（手順28c で「エージェントの参画情報」1件だけを除いた。8.5.5） 決め手は実測である——stg の `pb` の憲章は**4文書で 3,601 文字**しかなく、**選定の機構を挟むほうが「外したことに誰も気づけない」危険だけを持ち込む。** 10.4.1 の「検索させず、押し付ける」に最も忠実な形でもある。**次のいずれかが起きたら、選定と切り詰めを入れる**
  - **`docs/PROGRESS.md` と `docs/history/` が PB の文書へ移ったとき**（利用者の構想、2026-09-05）。**プロジェクトに紐づく内容は PB へ移譲し、環境と利用者との関係だけをローカルの `CLAUDE.md` / `LEARNINGS.md` に残す**——`Requirements.md` 10.8.1 の三層分離をリポジトリ側から詰める動きである。移れば憲章は**いまの1桁上**（`PROGRESS.md` だけで 24KB）になり、全文送出は成り立たない
  - **Phase 3 の `knowledge`（プロジェクトメモリ）が入ったとき**（`DbDesign.md` 8.3）。10.4.2 の優先度2 が文書から粒の細かい行へ移り、**選定の母数が桁で増える**
  - **進め方の規約が PB の憲章へ移ったとき**（利用者の判断、2026-09-06。`Requirements.md` 10.6.2）。**`CLAUDE.md` が Claude にしか効かない**ため、種別によらず MCP で取れる場所へ正本を移す。移送のぶんは実測で**約 5,600 バイト**（絶対規則・ブランチ運用・命名と形式・進め方）である。**パックの憲章節は移送前 3,936字、移送後 6,846字**（いずれも 2026-09-06 実測。測り方は `Testing.md` 7.6）。**下の材料に照らすとまだ「数千文字」の側**なので、この移送だけでは選定を入れない。**ただし見積りは 約5,800字で、1,000字ぶん外した**——文字数は本文のバイト数から素直には出ず、表と見出しの構造が効く。**次に移すときは、見積りではなく移送後の実測で判断する。**
  - **判断の材料は「1回のパックが何文字になるか」である。** 全文で数千文字なら選定は要らない。**万を超えたら、そこが切り替え時である**
  - **ただし、誰もそれを測っていない。** `make docs-size` が測るのは**毎セッション読むローカル4文書の 80KB** であって、**PB 側の憲章は対象外である**。**憲章へ何かを移したら、そのたびに `pb_get_context` を1回叩いて文字数を数える**——歯止めの無い置き場は必ず育ち、**育ったことに誰も気づけないのが最も悪い**。数え方は `Testing.md` に置く

---

# 9. 画面設計

**正本は `GuiDesign.md`。** 本章は Phase 1 の画面と必要権限の一覧に留める。

| 画面 | パス | 必要権限 |
|---|---|---|
| ログイン | `/login` | 不要 |
| プロジェクト一覧（ログイン後の初期画面） | `/projects` | `project.view` |
| プロジェクトダッシュボード | `/p/:key` | `project.view` |
| バックログ | `/p/:key/backlog` | `ticket.view` |
| チケット詳細 | `/p/:key/tickets/:seq` | `ticket.view` |
| プロジェクト設定（一般／メンバー／タグ／スプリント） | `/p/:key/settings` | `project.edit` |
| **プロジェクト文書（Docs、Phase 2）** | `/p/:key/docs` | **`doc.view`**（編集は `doc.edit`） |
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
| `ip` | `audit_log.ip` と同じ値。プロキシ経由の実IP解決は Phase 1 では行わない |
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

**liveness と readiness を分ける。** Phase 1 で提供するのは liveness（プロセスが応答するか）のみ。DB断はプロセス再起動では復旧しないため、liveness に含めると再起動ループを招く。

## 10.3 設定管理

**設定は3層に分ける。層を決めるのは「その値がいつ必要か」と「複数のレプリカが一致していなければならないか」の2点である。**

| 層 | 既定の置き場 | 何を置くか | 画面 |
|---|---|---|---|
| **1. 起動前** | `PB_CONFIG_FILE` の YAML / 環境変数 / `<KEY>_FILE` | 接続文字列（`database_url`）、待受（`bind`）、第3層の暗号鍵 | **表示のみ** |
| **2. 実行時の共有設定** | DB の `app_setting`（`DbDesign.md` 6.14） | ログ形式・ログレベル・ヘルスチェックのバージョン表示・Cookie の `Secure` | 確認と変更 |
| **3. 共有される秘密** | DB（第1層の鍵で暗号化） | TLS 証明書と秘密鍵（**未実装**。pb-3 の範囲） | 確認と変更 |

**「既定の置き場」と書いたのは、第2層もファイルや環境変数で与えられるからである。** 与えた時点でその設定は画面から変更できなくなり、「固定」として表示される（下記の優先順）。**逆に第1層を DB に置くことはできない**——接続文字列は DB の中にあり得ない。

### 第1層に残すのは、DB に到れないものだけである

**接続文字列は DB の中にあり得ない。** これは選択ではなく循環であり、手当ての余地が無い。待受も同じ理由で第1層に残る——待受を張る時点でまだ DB へ問い合わせていない。

**K8s では、この層に新しい仕組みを作る必要が無い。** ConfigMap と Secret が**すでに複数のコンテナ間で共有される**ため、第1層の値は全レプリカへ同じものが届く。**`pb.yaml` は ConfigMap として、秘密は Secret として配る**——後者はボリュームにマウントすれば `<KEY>_FILE` の形になり、環境変数として渡せば `${…}` で引ける。

**第1層を画面から編集させないのは、編集させても反映先が無いからである。**

| 書き戻し先 | なぜ無理か |
|---|---|
| **環境変数** | **プロセスは自分の環境変数を次回の起動に残せない。** 他のコンテナの環境変数にも触れない。K8s では ConfigMap 由来の環境変数は**コンテナ起動時にだけ注入される** |
| **ConfigMap のファイル** | **常に読み取り専用でマウントされる** |
| **コンテナ内のローカルファイル** | 書けるが **Pod ごとに分かれる**（`deploy/stg/pb.env` が端末ごとに分岐するのと同じ問題が Pod ごとに起きる） |

**この制約は PB 固有ではない。** 初回アクセスで設定画面を出して自分の設定ファイルを書き換える方式（Gitea・WordPress・Nextcloud）は**単一ノードの方式**であり、**当の OSS はどれも K8s ではこの画面を封じている**（Gitea の Helm chart は `INSTALL_LOCK` を常に真にする）。調査の詳細は pb-1 のコメント（2026-09-11）。**PB でこの方式を採るなら、同じ封じ手を最初から持たせること。**

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

**再検討の条件は、PB を単一ノードのみで配布すると決めたとき**である。判断の材料は本節と pb-2 のコメント。

### 設定ファイルは YAML の1枚で、キーは平らに並べる

**`PB_CONFIG_FILE` が指す YAML ファイルに、設定をキーと値で書く**（利用者の希望、2026-09-11）。

```yaml
# pb.yaml — PB_CONFIG_FILE が指すファイル
log_level: info
log_format: json
health_show_version: false
cookie_secure: false
bind: 0.0.0.0:8080

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

### この節が畳んだ構想（2026-09-06〜2026-09-11）

**本節の前身は「アプリケーション設定（利用者の構想）」という節で、着手の条件を「公開エンドポイントが `Host` から導けなくなったとき」としていた。** その条件は**発火していない**。先に発火したのは別の事象で、**pb-1（DB接続先の画面設定）と pb-3（TLS）がどちらもこの仕組みを前提とすること**が実装を読んで確定したためである（pb-2 のコメント、2026-09-11）。

構想が挙げていた3項目は、いずれも**この仕組みの上に載る設定**になった。

| 項目 | いまどうなっているか |
|---|---|
| **公開エンドポイント** | **まだ設定として存在しない。** 手順28a・28b は暫定としてリクエストの `Host` ヘッダから組み立てている（`ApiDesign.md` 5.7.1 / 4.5.8）。**着手の条件は変わらず「`Host` から導けなくなったとき」**——リバースプロキシの背後に置く、あるいは複数の参加者が別々のアドレスで同じインスタンスを見るようになったとき（`Requirements.md` 10.13） |
| アクセスを許可・拒否するアクセス元 | 無い。第2層に載る形になる |
| TLS | pb-3 の範囲。**証明書と秘密鍵は第3層**（DB に置き、第1層の鍵で暗号化する）。スケールアウトでは全レプリカが同じ証明書を出す必要があり、更新が全台に届かなければならないため、pod ごとのファイル配布を採らない |

**「env を先に足さない」という 2026-09-06 の判断は、本節で解けた。** 当時の理由は「DB へ移す日に env と DB のどちらが正本かを解く仕事が増える」ことだったが、**その順序を上の優先順として決めたので、以後は env を足しても正本が曖昧にならない。**

## 10.4 今後扱うもの

- メトリクス（Prometheus 形式のエクスポート可否を含む）
- コンテナイメージのビルドと配布
- `Requirements.md` 8章の「KEDAでスケール0」構成の可否（PostgreSQL 常駐との兼ね合い）

---

# 11. 開発フェーズと実装順序

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

**`<番号>` は PB のチケット番号（`ticket.seq`）である。** ドッグフーディングを始めると作業は
チケットから来るので、**`git branch` を見るだけでどのチケットの作業か分かる**ようにする。
**手順の実装は既に番号を持っている**（`step-20`）ため書式を変えない——**その番号がそのまま
チケット番号になる**。チケットに紐づかない作業では `pb-<番号>-` を省いてよい。

**接頭辞を残すことが要である。** `Requirements.md` 10.7.3 は当初ブランチを `pb/<チケットID>` に
統一する案だったが、**マイナーを上げるかビルドを上げるかが接頭辞で決まる**（11.1）ため、
接頭辞を落とすと `make bump-*` のどちらを打つかを git から判断できなくなる。
**チケット番号は接頭辞の後ろに置く**ことで両立する。

**`develop` に直接コミットしない。** 手順ごとに feature ブランチを切り、完了後に `--no-ff` で `develop` へマージする。`--no-ff` を使うのは、**手順の区切りをマージコミットとして履歴に残す**ためである。後から「どの手順でどこまで入ったか」を `git log --first-parent develop` で辿れる。

```bash
git switch develop && git pull
git switch -c feature/step-02-migrations
# 実装・検証・振り返り・記録の更新（PROGRESS.md と docs/history/）
git commit -m "step 2: マイグレーション 0001〜0010 を追加"
git switch develop && git merge --no-ff feature/step-02-migrations
```

**`docs/PROGRESS.md` の更新は実装と同じブランチに含める。** 別コミットにすると、マージ前のブランチだけを見たときに「完了したのか途中なのか」が判断できなくなる。

**マージを提案する前に2種類の振り返りを行う。** どちらもコミットの前に行い、**結果をコミットに含める**。

| 振り返り | 対象 | 反映先 | 手順 |
|---|---|---|---|
| **成果物** | 消化した引き継ぎ・生まれた約束・判断と検証の記録・検証で作った資源の後始末 | `docs/PROGRESS.md`、`docs/history/` | `pb-step.md` 手順7 |
| **進め方** | 手戻り・往復の多かった論点・指示の解釈違い・空振り | `LEARNINGS.md`（開始時に読む文書） | `pb-step.md` 手順8 |

進め方の教訓は**同じことが2回起きてから規約へ昇格させる**。一度きりの事象で規約を増やすと、細かくなるほど守られなくなる。

**`LEARNINGS.md` は `.gitignore` の対象**であり、リポジトリには入らない。**個人のセッション履歴（`~/.claude/projects/`）から抽出した内容を含む**ためで、規約へ昇格した分だけが `CLAUDE.md` と `pb-step.md` に残る。文書が存在しない環境では、見出しと空の表だけ作って始める。

**マージ・push・ブランチ削除はエージェントに独断で行わせない**（PB の規約「人の承認が要ること」、`.claude/commands/pb-step.md` の手順10）。

## 11.1 バージョン番号とリリースタグ

リリースタグは **`vX1.X2.X3`** 形式とする（例：`v1.2.4`）。

| 桁 | 名称 | 意味 | 上がる条件 |
|---|---|---|---|
| X1 | メジャー | 機能のまとまり | **開発者が判断して上げる。** Phase 1 = 1、**Phase 2 = 2**（下記「Phase の区切りでメジャーを上げる」） |
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
# 実装・検証・振り返り・記録の更新（PROGRESS.md と docs/history/）

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

### Phase の区切りでメジャーを上げる

**Phase の完了をメジャー番号の区切りとする**（利用者の判断、2026-08-29）。

| Phase | メジャー | 到達点 |
|---|---|---|
| 1 | **1** | **`v1.36.55`**——認証・認可、プロジェクト、チケットの基礎と、Phase 2 の設計 |
| 2 | **2** | 未定（**`v2.1.56` から始まった**。手順20、2026-08-29） |

**Phase 2 の最初のマージは `2.1.56` になる。** メジャーを上げるとマイナーは 0 に戻るが、
**その作業自体が `feature/*` のマージなのでマイナーが 1 に上がる**——規約どおりの結果である。
最初の Phase 2 ブランチ上で2つ続けて実行する。

```bash
git switch -c feature/step-20-stg-instance
make bump-major          # 1.36.55 → 2.0.56
make bump-minor          # 2.0.56  → 2.1.56
```

**`bump-*` はどちらもビルド番号に `NEXT_BUILD`（現在のマージ回数 + 1）を書く**ため、
続けて実行してもビルド番号は二重に進まない。`make version-check` はマージ後に通る。

**ただし `make bump-major bump-minor` と1回の呼び出しにまとめてはならない。** `VERSION` は
`VERSION := $(shell cat …)` で**パース時に一度だけ展開される**ので、まとめると `bump-minor` が
`bump-major` の書いた値ではなく元の値を読み、`1.37.56` になる。**`make` を2回に分けて叩く**
（手順20 で実測。`1.36.55` → `2.0.56` → `2.1.56`）。

| Phase 1 の到達点 | Phase 2 の最初 |
|---|---|
| `v1.36.55`（`main` にタグ済み） | **`v2.1.56`**（手順20、2026-08-29。約束どおりに着地した） |

**`fix/*` や `docs/*` で Phase 2 を始める場合は `make bump-major` と `make bump-build`** に
なり、`2.0.56` から始まる。**マイナーは「そのメジャー内での機能実装数」であり**（上の表）、
機能を足していないマージで 1 にはしない。

### 起点

| ビルド | 内容 |
|---|---|
| 1 | 手順2（`feature/step-02-migrations`）のマージ。**`--no-ff` マージの1回目** |
| 2 | 本節の導入（`feature/versioning`）のマージ |

**手順1以前の2コミットはマージではないため数えない。** 手順1は 11.0 の規約が固まる前に `develop` へ直接コミットされており、feature ブランチを経ていない。

## Phase 1 — 認証とチケットの基礎（ローカル動作確認まで）

**手順1〜19 は 2026-08-28 に完了した。** 各手順の完了日・検証内容は `docs/history/steps.md`「Phase 1 完了一覧」にある。

```
 1. deploy/base/compose.yaml と initdb（DBロール分離）  ← DbDesign 3.2, 3.4
 2. server/migrations/ 0001〜0010 の作成と適用       ← DbDesign 6, 7
 3. pb admin create による初期管理者作成            ← DbDesign 7.5
 4. 共通基盤：エラー形式、ページネーション、認証ミドルウェア、監査ログ
 5. POST /auth/login、/auth/logout、GET /me         ← ここでログインが通る
 6. 認可ミドルウェア（RequirePermission）
 7. client 雛形（Vite + Pinia + router + デザイントークン）と embed 疎通   [TS]
7.5 開発用デモデータ（pb dev seed）と dev-reset / dev-info            [Go+sh]
      ← 権限の違う4アカウントが揃い、手順8の出し分けを検証できる
 8. ログイン画面・auth ストア・ルーターガード・メニュー出し分け             [TS]
      ← ブラウザでログインでき、権限でメニューが変わる
 9. GET/POST /projects、check-key                                    [Go]
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

**完了した手順を一覧から消さない。** 本一覧は**手順番号の定義の正本**であり（`docs/README.md`「開発フェーズ・実装順序 = 本書11章」）、番号を引く先が要る。

| 参照元 | 手順番号の出現 |
|---|---|
| `server/` と `client/` のコメント | **250件超**（`手順5` 29件、`手順13a` 28件、`手順2` 22件 …） |
| `docs/history/decisions.md` / `steps.md` | 300件超 |
| `docs/PROGRESS.md` | 71件 |

`login.go` を読んだ人が「手順5a とは何か」を引ける必要がある。**本一覧は「何から成るか」を示し、いつ何をどう検証して完了したかは `docs/history/steps.md`** が持つ。役割が違うので両方に置く。あわせて、Phase 2/3 と並べたときに**フェーズの大きさを比べられる**ことが、次のフェーズの見積りの土台になる。

**手順17〜19 は a / b / c に分けて実施した**（17a/17b/17c、18a/18b、19a/19b）。いずれも 11.2.1 に従って着手時に分量を見積もって分けたもので、判断の経緯は `docs/history/decisions.md`「手順の分割の経緯」にある。

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

Phase 1 の一覧の手順7〜9 に残る `[TS]` `[Go]` の印は、**この規約より前の「1ステップ = 1言語」の下で完了した実績**である。当時の理由（読む設計文書が `ApiDesign.md` と `GuiDesign.md` で切り替わり、混ぜるとコンテキストが膨らむ）自体は正しいが、**手順の区切りに使うと弊害のほうが大きい。**

| 弊害 | 具体 |
|---|---|
| API の手順が curl でしか検証できない | 11.1 が挙げたとおり、Cookie・CSRF・応答形状の過不足はブラウザを通さないと分からない |
| 応答の設計ミスが次の手順まで顕在化しない | 画面を書いて初めて「この項目が足りない」と気づく |
| 手順の完了が利用者に見えない | 「動くようになったもの」が増えないまま手順番号だけ進む |

**コンテキストの膨張は、手順の分割ではなくセッションの分割で扱う**（11.2.1）。

#### Phase 2 では尺度を1つ足す

**1ステップ = ドッグフーディングの経路が1本増える単位。**

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
——それは 11.1 が Phase 1 で退けたのと同じ誤りで、エージェントが実際に使えるかは
クライアントを通さないと分からない。

#### 11.2.1 セッションを分けるとき

1つの手順が1セッションに収まらないときは、**a / b に分けてよい**。ただし次を守る。

- **分割は手順一覧で先に決めない。** 着手時に分量を見積もって判断する。設計文書に書いた見積もりは、書いた時点の推定であって実装直前の実測ではない
- **分けたら、a の完了時に検証・記録・コミットまで行って止まる。** そこまでやらないなら分けない。「a で作りかけ、b で仕上げる」は、a の時点で何が動くのかが誰にも分からなくなる
- **手順の完了（上の `←` 行）は b で満たす。** a は途中経過であり、単独では手順を完了させない

実績では、a/b に分けた7組のうち5組が結局1回のマージになっている。**分けること自体に手間がかかる（記録・検証・コミットが2回要る）ため、既定は「分けない」である**。

### 11.3 未実装画面はプレースホルダを置く

**遷移先が未実装・設計未確定の画面には、`GuiDesign.md` 6.5 のプレースホルダページを表示する。** 空白や 404 にしない。

- 実装済みの画面から遷移して動作確認できる
- **プレースホルダ自体が「この画面をどう作るか」を議論するときの参照点になる**。画面名・設計文書の章番号・予定内容が画面上に出ているため、それを見ながら会話できる
- 権限（`meta.permission`）は実画面と同じにする。プレースホルダのうちにルーターガードとメニュー出し分けを検証できる

現在プレースホルダなのは**監査ログ（`/admin/audit`）と Phase 2/3 の全画面**である（`GuiDesign.md` 3.2）。

## Phase 2 — 複数人とエージェントが同じプロジェクトを進められるようにする

**目的は「MCP サーバができること」ではない。** `Requirements.md` 10.0 のとおり、PB が価値を持つのは参加者が複数になってからである。Phase 2 は**その最小構成を通しで動かす**ところまでを担う。

```
20. ドッグフーディング用インスタンス（deploy/stg）        ← Design 4.4
      ← make dev-reset を実行しても stg のデータが残る
21. マイグレーション 0017（document / document_revision / doc 権限） ← DbDesign 8.1
      ← make migrate と make test-db が通る
22. 文書API と Docs 画面（目次・本文・編集・履歴）        ← ApiDesign 10, GuiDesign 5.10
      ← ブラウザで文書を作り、階層に置き、編集して履歴が残る
23. 文書テンプレートの複製（プロジェクト作成時）           ← DbDesign 8.1.2
      ← 新規プロジェクトに型の4文書が並ぶ。stg に PB 自身の憲章を書く
      （プロジェクト作成の手順を internal/project へ1本化し、
        初期本文の直しを 0018 で入れたため、以降の採番が1つずれた）
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
        8.2 の表が内訳。26b は当初リースだったが 2026-09-05 に組み直した）
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

**手順20 を先頭に置く。** 以降の手順が「PB の開発を PB で管理する」を積み上げるので**器が先に要る**。またマイグレーションを足すたびに stg へ適用する運用が、ここから始まる。

**手順21〜23 の前に、プロジェクト作成手順の二重化を片付けることが望ましい**（`docs/PROGRESS.md`「手順外の作業」）。`dev_seed.go` と `projects_create.go` がワークフローテンプレートの複製を別々に持っており、**手順23 で文書テンプレートの複製が加わると差が開く。**

### Phase 2 の完了条件

**受け入れは通しで測る。**

> **新しく clone した作業ディレクトリで `/pb-onboard` → `/pb-implement <id>` が通しで走り、
> チケットが1件消化される。これを Claude Code と VS Code のエージェントの両方で行う。**

両方で行うのは、**セッションどうしが記憶を共有しないため**である。**文脈を共有しない参加者が
同じプロジェクトを触るという構造は、開発者が1人でも成立する**（`LEARNINGS.md` が存在するのが
その証拠である）。内訳として測るのは4点。

| # | 測ること |
|---|---|
| 1 | 議論の結果を PB のチケットとして起票できる（`pb_create_ticket`） |
| 2 | 着手時にコンテキストパックが前提を押し付ける（`pb_get_context`） |
| 3 | 憲章を MCP 経由で読める（`pb_list_docs` / `pb_get_doc`） |
| 4 | clone からセッション開始まで、**PB の画面以外の口頭指示を要しない** |

**Phase 2 で検証されないことを明記しておく。** 意見の対立と合意形成、承認者が別人であることの
負荷、個人の学びが N 人へ広がるか——**いずれも参加者が1人では起きない**。`Requirements.md` 10.0.2 が
挙げた4つのうち、Phase 2 が実地に確かめられるのは「学びの置き場ができたこと」までである。

### Phase 3 へ送ったもの

| 項目 | 理由 |
|---|---|
| `knowledge`（プロジェクトメモリ） | まず 8.1 の文書として運用し、押し付けたい粒度が実測で見えてから切り出す（`DbDesign.md` 8.3） |
| **`proposal` と承認キューUI** | 承認対象だった `knowledge` と文書差分の両方が Phase 3 へ移ると、**Phase 2 に残る対象がサブタスク提案だけになり、画面を作る理由が薄い** |
| DoD の machine 型、`agent_run` / `agent_report` / `context_pack_log` | 人が同席する前提では、`pb_post_note` を既存の `comment.kind` に載せれば足りる（`Requirements.md` 10.3.2）。**うち `agent_run` / `agent_report` は手順26c で Phase 2 へ戻した**（利用者の判断、2026-09-05。0022 で適用済みで、Phase 3 の採番は 0023〜0027 へずれた）。**`context_pack_log` は 0022 に器だけ作り、書き手は Phase 3 のまま**（`DbDesign.md` 8.2.5） |

**旧手順21 は「`dod_item` と `agent_report`」だった。** `dod_item` は Phase 1（手順18）へ前倒し済みで、`agent_report` は上記により Phase 3 へ送った（`DbDesign.md` 6.11 / 8.2.4）。

## Phase 3 — AI機能・分析と、知識の還流

```
29. マイグレーション 0023〜0027                        ← DbDesign 8.3, 8.4
30. DoD の machine 型                                  ← DbDesign 6.11
      （agent_run / agent_report は手順26c で実装済み。0022。
        context_pack_log は 0022 に器だけ在り、書き手は本 Phase）
31. proposal と承認キューUI                            ← Requirements 10.6.3
32. プロジェクトメモリ（knowledge）とコンテキストパックへの供給 ← DbDesign 8.3
33. Readiness 判定、DoD ドラフト生成                    ← Requirements 10.5.1
34. コメント分類・重要度スコアリング
35. ベクトル検索、プロジェクトヒストリー
36. 進捗分析画面（消化状況・残存チケット・ベロシティ）    ← GuiDesign 10章
37. OIDC / SAML 連携                                   ← Design 6.2.3
38. カスタムロールの編集UI                              ← GuiDesign 5.6.3
```

**Phase 2 の再構成（2026-08-29）で 27〜33 から 29〜38 へずれた。** Phase 2 が7手順から9手順へ増え、Phase 2 から3項目が移ってきたためである。

**振り直しで、Phase 2 への前方参照6か所を追随させた。** 「エージェント用のアクターも MCP も無い（手順21・22）」と書いていた箇所が、`ApiDesign.md` 9.10.2 / `DbDesign.md` 6.12 / `GuiDesign.md` 5.5 / `docs/openapi.yaml` / `server/.../routes.go` / `client/src/api/schema.d.ts` にあり、**新しい番号では手順24（エージェント登録）と手順25（MCP の read 系）にあたる。** `docs/history/` は完了した手順（1〜19）しか参照していないため触れていない。

---

## 付録A. 本書に関する未解決の検討事項

各領域固有の検討事項は、それぞれの設計書の末尾に記載している（`DbDesign.md` 10章、`ApiDesign.md` 12.2、`GuiDesign.md` 11章）。本書に残るのは以下。

- `Requirements.md` 8章の「KEDAでスケール0」を PostgreSQL 常駐構成でどう扱うか
- アプリケーションログと `audit_log` の使い分け（何を両方に書き、何を片方に留めるか）
- **権限の全体像を再整理する**（利用者の判断、2026-08-22）。**見直しの条件だった「プロジェクトメンバー向けの画面（`GuiDesign.md` 5.3 / 5.4 / 5.5）の実装」は Phase 1 の完了で満たされた。** 論点は3つ——①`GET /roles?scope=project` を**権限不要**としたのは暫定である（`project.edit` を要求する案と `role.view` を新設する案は検討して見送った）②**Phase 1 に「チケットを作れない人」は実在しない**（実測）。実効権限はシステムロール ∪ プロジェクトロール（6.4.1）で operator が `ticket.create` / `ticket.edit` / `ticket.close` を持つため、`project_viewer` を与えても到達できるプロジェクトではチケットを作れる。ルート定義の宣言は正しいが、効き始めるのは operator の持ち物を減らしてからである ③②の帰結として、画面側の権限による出し分けの**負の側を Phase 1 のアカウントでは検証できない**。**②③は 2026-08-29 に部分解消した**——`doc.edit` を `operator` と `project_member` に与えないと決めたため（`DbDesign.md` 8.1.4）、**「その操作ができない人」が手順22 の Docs 画面で初めて実在する。** 残るのは論点①である
- **HTTPサーバのタイムアウト値が実装（`serve.go`）にしかない。** 10章を扱うときに文書化を提案する
- **DBを使うテストの作法を本書に書くか**。手順は `Development.md` 6.1 / 6.2 にあるが、設計として持つかは未判断
- **ブランチ運用をどこまで厳密にするか**（11.0。利用者の方針、2026-08-18）。`fix/` の分離は 2026-08-22 に一度実施したが、初期の作りかけの部分ではまだ1ブランチにまとめている。目安としていた「**PB 自身を MCP 経由で Claude Code が使い、PB の開発項目をチケット管理できる段階**」は、**Phase 2 の完了条件そのものになった**（11章）。命名規約は 2026-08-29 に `pb-<番号>` を含む形へ改めてあり、残るのは運用の厳密さの判断だけである

### 解決済みとして削除した項目（rev.1 から）

| 項目 | 結論 |
|---|---|
| SQLite の単一ライタ制約下でのエージェント並行アクセス | PostgreSQL 前提化により解消（`DbDesign.md` 2.1） |
| PostgreSQL 移行時のデータ移送手順とダウンタイム | 移行そのものが不要になった |
| `permission` カタログの粒度 | 0010 の時点で28件（`DbDesign.md` 7.2）。**0017 で `doc.view` / `doc.edit` を足して30件になる**（同 8.1.4）。**「確定」は粒度の考え方であって件数ではない**——新しい資源が増えれば足す |
| プロジェクトロールのUIをどの段階で入れるか | Phase 3（`GuiDesign.md` 5.6.3） |
| サーバ言語の確定（Rust / Go） | **Go** に確定（3.1） |

### 解決済みとして削除した項目（Phase 2 の再構成、2026-08-29）

| 項目 | 結論 |
|---|---|
| MCPサーバとREST APIの責務分担 | **MCP は REST の薄いラッパ。** ビジネスルール・権限判定・検証は REST 層に置き、MCP は入出力の形を変えるだけにする（8.1） |
| PB自身の開発に PB を使う時期 | **Phase 2 の完了条件そのものにした**（11章）。器（`deploy/stg`）は手順20 で作る |

### 解決済みとして削除した項目（手順24a、2026-08-30）

| 項目 | 結論 |
|---|---|
| **エージェントの権限の持たせ方**（6.5） | **所有者から導く（委譲）。** エージェントは `agent.owner_actor_id` でプロジェクトメンバの誰かに紐づき、その人のシステムロールとプロジェクトロールを両層に用いる（利用者の判断、2026-08-30——「現状のAIエージェントは人の支援を行う形態であるため、PJメンバの誰かに紐づいて登録するのが妥当」）。**エージェントに `project_member` の行は作らない**（二重に持つと片方が古くなる）。人に紐づかない自立エージェントは `owner_actor_id` を NULL 可にして受ける（`DbDesign.md` 8.2.1） |
| **6.5 のスコープ語彙が権限カタログのキーに対応していない** | **権限キーで書き直した**（6.5 に対応表）。`ticket:read` 等の旧語彙は**発行しても実効権限が0件になる**ため残せない。2026-08-22 に `/me/tokens` について決めた方針（`ApiDesign.md` 4.4.2）へ揃えた |
| **`agent.run` を誰が持つか** | 意味を「**自分に紐づくエージェントを MCP から走らせてよい**」と定め、0019 で `operator` / `project_member` / `project_viewer` へ配り直した（`DbDesign.md` 8.2.6）。委譲のため、所有者が持たなければエージェントも持てない |
