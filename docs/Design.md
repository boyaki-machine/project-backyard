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
| 8 | MCPサーバ設計 | 未着手 |
| 9 | 画面設計 | → `GuiDesign.md` |
| 10 | 非機能・運用設計 | ログ・ヘルスチェックは確定。メトリクス等は未着手 |
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

**原則2 は rev.1 の「DB依存を薄く保つ（SQLite/PostgreSQL両対応）」を置き換えたものである。** PostgreSQL 前提へ変更したことで移植性の制約が不要になり、代わりに「スキーマそのものを設計資産として管理する」ことを原則に据えた。経緯は `DbDesign.md` 2章に記録している。

## 1.2 本書と要件定義の対応

| 本書 | `Requirements.md` の該当箇所 |
|---|---|
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

Phase 1（当面の実装対象）は以下を成立させる。

- `docker compose up` で PB と PostgreSQL が起動し、マイグレーションが適用される
- ローカルID/PW でログインでき、セッションが維持される
- オペレータ／アドミニストレータで見える画面・使える機能が変わる
- プロジェクトとチケットを作成・編集・一覧表示できる
- ユーザーの追加・権限設定が管理画面から行える

MCP サーバ、AI機能、ガント描画は Phase 1 では実装しない（テーブル・列のみ先行定義するものは `DbDesign.md` 6章・8章を参照）。

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
| YAML | `gopkg.in/yaml.v3` | `pb dev seed` の定義ファイルを読むためだけに使う（`DbDesign.md` 7.6.4）。実行時の推移依存を持たない。**設定ファイルの形式として一般化しない**（設定は環境変数＋`*_FILE`） |
| ID生成 | `oklog/ulid/v2` | ULID（`DbDesign.md` 4.2） |
| ログ | **`log/slog`**（標準ライブラリ、JSONハンドラ） | 外部ライブラリを増やさない |
| 設定 | 環境変数＋`*_FILE` 展開（自前、数十行） | `DbDesign.md` 3.2 |
| 入力検証 | `go-playground/validator` v10 | `ApiDesign.md` 2.5 のエラー形式へ変換する層を挟む |
| テスト | 標準 `testing` ＋ compose のDBに対する統合テスト | testcontainers は導入しない（起動が重く原則と衝突） |
| DB | **PostgreSQL 17** | `DbDesign.md` 2章。SQLite 先行案は廃止 |
| DB拡張 | `pgcrypto` / `citext` / `pg_trgm`（Phase 1）、`vector`（Phase 3） | `DbDesign.md` 3.1 |
| フロント | Vue 3 + TypeScript + Vite | SPA。サーバから静的配信 |
| UIコンポーネント | 未確定（自前の軽量実装で開始） | `GuiDesign.md` 1.1 |
| 実行形態 | docker compose（既定）／ Kubernetes | `DbDesign.md` 3.2〜3.3 |

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
│   │   ├── config/                ← 環境変数と *_FILE の読み込み
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
    ├── stg/                       ← ステージング（当面は空でよい。4.4）
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

Phase 1〜2 は開発端末での動作が中心であり、**`stg/` は当面 `.gitkeep` のみで構わない**。ステージングが実際に必要になる（他者に触ってもらう、外部公開する）段階で、`dev` と `prod` の差分を見てから内容を決める方が無駄がない。

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
| **2** | エージェント連携 | `agent` `task_lease` `agent_run` `agent_report` `context_pack_log` | 8.1 |
| **2** | 知識還流 | `knowledge` `knowledge_revision` `proposal` | 8.2 |
| **3** | AI・分析 | `comment_signal` `embedding` `project_event` `estimate_record` `contribution` | 8.3 |

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

**スコープの語彙は権限カタログのキーそのものである**（6.4.2 の28件。`ApiDesign.md` 4.4.2）。上の積は権限キーどうしの完全一致で取るため、別の語彙を混ぜると、絞ったつもりのトークンが権限0件になるか、解釈できない語彙を通して逆に広がるかのどちらかになる。**空配列は「絞り込みなし」であって「権限0件」ではない。**

6.5 がエージェントの既定スコープとして挙げる `ticket:read` / `ticket:claim` / `result:submit` / `context:read` は、**この語彙ではない**。権限カタログに対応するキーを持たないものを含んでおり、対応表は Phase 2 でエージェントの操作を設計するときに決める。

### 6.4.2 権限カタログ

権限をコードのif文ではなく**データとして定義**する（原則5）。カタログは28件で、`DbDesign.md` 7.2 のシードが正本。

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

**Phase 1 ではプロジェクトロールもUIで扱う。** 手順13b がユーザー詳細のメンバーシップ欄（`GuiDesign.md` 5.6.2）で付与・変更・剥奪を実装し、手順14 が権限マトリクス（同 5.6.3）に5ロールすべてを並べた。改訂前の本節は「システムロール2種のみをUIで扱い、プロジェクトロールは画面を後回しにする」としていたが、**13b の時点で実装が先に進んでいた**（2026-08-22 に手順14 で発見）。カスタムロールの作成と権限の編集だけが Phase 3 に残る（`ApiDesign.md` 7.3）。

### 6.4.4 画面・機能の制限方式

**サーバとフロントの二重で制御する。**

**サーバ側（本体）**：全 API ハンドラに必要権限を宣言し、ミドルウェアで検証する。

```go
r.With(RequirePermission("ticket.close")).
  Post("/projects/{key}/tickets/{seq}/close", h.CloseTicket)
```

**権限は chi のミドルウェアとしてルート定義に宣言する。** ハンドラ本体に権限チェックを書くと、新しいエンドポイントで書き忘れても気づけない。ルート定義に並べれば、`routes.go` を眺めるだけで全エンドポイントの必要権限を確認でき、テストで網羅も検証できる。

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
- エージェントからの操作は、権限に加えて①ワークフローの `is_agent_reachable`、②サーキットブレーカーの状態、③リースの保有、を追加で検証する

## 6.5 エージェントの認証（Phase 2）

人間ユーザーとは別系統として設計する。

| 項目 | 方針 |
|---|---|
| principal | `actor(kind='agent')` + `agent` テーブル。人間アカウントの借用をしない |
| トークン | `access_token(token_type='agent')`。プロジェクトスコープ必須、有効期限必須 |
| 発行 | プロジェクト設定画面から。**発行時に一度だけ全文表示**（`Requirements.md` 10.9.1） |
| スコープ既定 | `ticket:read` `context:read` `ticket:claim` `note:write` `result:submit` `proposal:create`。**この語彙は権限カタログのキー（6.4.2）に対応していない。** 対応表は Phase 2 で決める（6.4.1） |
| 禁止 | `ticket.close`、`knowledge` の直接更新、他プロジェクトへのアクセス |
| 信頼度 | `agent.trust_level` に応じて既定スコープを段階的に拡大（`Requirements.md` 10.10.3） |
| 失効 | 管理画面から即時失効。サーキットブレーカー作動時は自動失効も選択可 |

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

未着手。`Requirements.md` 10.3（ツール一覧）・10.4（コンテキストパック）が入力となる。

- ツールの入出力スキーマ定義
- ツール description の文面設計（`Requirements.md` 10.13 の検討事項）
- コンテキストパックの生成アルゴリズムとトークン予算配分
- REST 層との責務分担

**エージェントから見える面は MCP のみとする**（原則7）。REST API を直接叩かせる設計は採らない。

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

## 10.3 今後扱うもの

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
| `feature/<スラッグ>` | 手順に属さない実装（不具合修正など） |
| `fix/<スラッグ>` | 機能を変えない修正（不具合修正・hotfix）。**マイナーバージョンを上げない**（11.1） |
| `docs/<スラッグ>` | 設計文書のみの修正 |

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

**マージ・push・ブランチ削除はエージェントに独断で行わせない**（`CLAUDE.md` のブランチ運用、`.claude/commands/pb-step.md` の手順10）。

## 11.1 バージョン番号とリリースタグ

リリースタグは **`vX1.X2.X3`** 形式とする（例：`v1.2.4`）。

| 桁 | 名称 | 意味 | 上がる条件 |
|---|---|---|---|
| X1 | メジャー | 機能のまとまり | **開発者が判断して上げる。** 当面 1 |
| X2 | マイナー | メジャー内での機能実装数 | `feature/*` のマージ。**メジャーを上げたとき 0 に戻る** |
| X3 | ビルド | `develop` へのマージ回数 | **すべてのマージ**。リセットしない |

**`fix/*` と `docs/*` のマージではマイナーを上げず、ビルド番号だけを上げる。** リリース後の小さな修正は、マイナーを据え置いたままビルド番号の違いで識別する。逆に言えば、**マイナーが同じでビルドが異なる版は「同じ機能セットの別ビルド」**を意味する。

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

### 起点

| ビルド | 内容 |
|---|---|
| 1 | 手順2（`feature/step-02-migrations`）のマージ。**`--no-ff` マージの1回目** |
| 2 | 本節の導入（`feature/versioning`）のマージ |

**手順1以前の2コミットはマージではないため数えない。** 手順1は 11.0 の規約が固まる前に `develop` へ直接コミットされており、feature ブランチを経ていない。

## Phase 1 — 認証とチケットの基礎（ローカル動作確認まで）

```
 1. deploy/base/compose.yaml と initdb（DBロール分離）  ← DbDesign 3.2, 3.4
 2. server/migrations/ 0001〜0010 の作成と適用       ← DbDesign 6, 7
 3. pb admin create による初期管理者作成            ← DbDesign 7.5
 4. 共通基盤：エラー形式、ページネーション、認証ミドルウェア、監査ログ
 5. POST /auth/login、/auth/logout、GET /me         ← ここでログインが通る
 6. 認可ミドルウェア（RequirePermission）
─────────────────────────── ここまで完了 ───────────────────────────
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
17. チケット詳細・更新・削除・ステータス遷移API とチケット詳細画面
      ← ブラウザでチケットを編集し、ワークフローに沿って状態を進められる
18. コメント・DoD・関連リンクのAPI と詳細画面への組み込み
      ← ブラウザでコメントを投稿し、完了条件と関連チケットを管理できる
19. stats / activity API とプロジェクトダッシュボード
      ← ブラウザでプロジェクトの現況が見える
```

**手順16〜19 は本改訂（rev.8）で、旧手順16「チケット API と画面（ApiDesign 9章の確定後）」を展開したものである。** `ApiDesign.md` 9章の確定によって範囲が見えたため、11.2 の「1ステップ = ブラウザで確認できる単位」に従って4つに割った。各手順の対応表は同 9.15 にある。

手順16 でスキーマが2つ増える（`0013_tag.sql`・`0014_dod.sql`。`DbDesign.md` 6.10 / 6.11）。**0014 が使われるのは手順18 だが、マイグレーションは手順16 でまとめて当てる**——2回に分けても手順18 の直前で `make migrate` を打つだけであり、分ける利得がない。

### 11.1 API と画面を同じ手順で進める

**手順7以降は、API と対応する画面を同じ手順で実装する。** 手順6までのように API を積み上げてから画面に取りかかる進め方は採らない。

rev.6 では「交互に実装する」（API の手順と画面の手順を隣り合わせに並べる）としていたが、rev.7 で**同じ手順に入れる**ことにした。理由は下の表と同じで、より徹底したものである（11.2）。

理由は動機づけではなく**フィードバックの遅れ**にある。以下はブラウザを通さないと検証できず、curl では確認できない。

| 対象 | ブラウザでしか分からないこと |
|---|---|
| HttpOnly Cookie | 実際に保存され、次のリクエストで送られるか |
| CSRF 二重送信 | JS が `pb_csrf` を読めて送信できるか |
| Vite プロキシ（:5173 → :8080） | Cookie とプロキシの相性 |
| 実効権限のキャッシュ | ストアに載せた権限で画面が正しく出し分くか |
| `GET /me` の応答形状 | **フロントが必要とする情報に過不足がないか** |

特に最後の項目は、`GET /me` の唯一の消費者が Pinia の `auth` ストアであるため、**ルーターガードとメニュー出し分けを実際に書くまで過不足が判明しない**。API を先に積み上げると、判明した時点で後続の実装がすべて同じ前提の上に載っている。

### 11.1.1 手順7.5 を挟む理由

手順8（メニュー出し分け）の検証には、**システムロールとプロジェクトロールが異なる複数のアカウント**が必要になる。`pb admin create` で1人ずつ対話的に作るのは現実的でなく、権限の組み合わせを毎回手で再現することになる。

デモデータの投入は Go とシェルで完結し、TypeScript を含まないため、手順8とは別ステップとする（11.2）。仕様は `DbDesign.md` 7.6。

### 11.2 1ステップ = ブラウザで確認できる単位

**手順の区切りは言語ではなく、「ブラウザで何ができるようになるか」で決める。** 上の一覧で各手順に付けた `←` の行が、その手順の完了条件である。API と対応する画面は同じ手順に入れる。

一覧の手順7〜9 に `[TS]` `[Go]` の印が残っているのは、**rev.6 の「1ステップ = 1言語」の下で完了した実績だから**である。記録として残す。手順10 以降が本節の対象になる。

rev.6 では「1ステップ = 1言語」とし、Go と TypeScript を別の手順に分けていた。読むべき設計文書が `ApiDesign.md` と `GuiDesign.md` で切り替わるため、混ぜるとセッションのコンテキストが膨らむ、という理由による。**この理由自体は正しいが、手順の区切りに使うと弊害のほうが大きい。**

| 弊害 | 具体 |
|---|---|
| API の手順が curl でしか検証できない | 11.1 が挙げたとおり、Cookie・CSRF・応答形状の過不足はブラウザを通さないと分からない |
| 応答の設計ミスが次の手順まで顕在化しない | 画面を書いて初めて「この項目が足りない」と気づく |
| 手順の完了が利用者に見えない | 「動くようになったもの」が増えないまま手順番号だけ進む |

**コンテキストの膨張は、手順の分割ではなくセッションの分割で扱う**（11.2.1）。

#### 11.2.1 セッションを分けるとき

1つの手順が1セッションに収まらないときは、**a / b に分けてよい**。ただし次を守る。

- **分割は手順一覧で先に決めない。** 着手時に分量を見積もって判断する。設計文書に書いた見積もりは、書いた時点の推定であって実装直前の実測ではない
- **分けたら、a の完了時に検証・記録・コミットまで行って止まる。** そこまでやらないなら分けない。「a で作りかけ、b で仕上げる」は、a の時点で何が動くのかが誰にも分からなくなる
- **手順の完了（上の `←` 行）は b で満たす。** a は途中経過であり、単独では手順を完了させない

実績では、a/b に分けた7組のうち5組が結局1回のマージになっている。**分けること自体に手間がかかる（記録・検証・コミットが2回要る）ため、既定は「分けない」**である。

### 11.3 未実装画面はプレースホルダを置く

**遷移先が未実装・設計未確定の画面には、`GuiDesign.md` 6.5 のプレースホルダページを表示する。** 空白や 404 にしない。

- 実装済みの画面から遷移して動作確認できる
- **プレースホルダ自体が「この画面をどう作るか」を議論するときの参照点になる**。画面名・設計文書の章番号・予定内容が画面上に出ているため、それを見ながら会話できる
- 権限（`meta.permission`）は実画面と同じにする。プレースホルダのうちにルーターガードとメニュー出し分けを検証できる

Phase 1 で最初からプレースホルダとするのは、プロジェクトダッシュボード・バックログ・チケット詳細・監査ログ、および Phase 2/3 の全画面（`GuiDesign.md` 3.2 の一覧を参照）。

### 11.4 旧番号との対応

手順一覧は3度再編している。**新しいほうから読む。**

#### rev.8（`ApiDesign.md` 9章の確定にともなう展開）

旧手順16「チケット API と画面」を、9章が定まったことで4つに割った。**手順15 までは変わらない。**

| rev.7 | rev.8 | 備考 |
|---|---|---|
| 16 | **16・17・18・19** | チケット（バックログ／詳細／コメント・DoD・リンク／ダッシュボード） |
| 17〜23 | **20〜26** | Phase 2 を6つ繰り下げ |
| 24〜29 | **27〜30・32・33** | Phase 3 を繰り下げ、31 に進捗分析画面を新設 |

**Phase 3 だけ単純な繰り下げになっていない。** `GuiDesign.md` 10章に進捗分析画面（`/p/:key/insights`）を足したため、31 が新規で、旧28・29（OIDC/SAML、カスタムロール）が 32・33 になる。

#### rev.7（11.2 の改訂にともなう統合）

API の手順と対応する画面の手順を1つにまとめ、以降の番号を詰めた。**手順11 までは変わらない。**

| rev.6 | rev.7 | 備考 |
|---|---|---|
| 12・13 | **12** | ユーザー管理（API＋画面） |
| 14・15 | **13** | ユーザー詳細・編集（API＋画面） |
| 16 | **14** | ロールと権限 |
| 17 | **15** | /me 系 |
| 18 | **16** | チケット |

#### rev.6（API と画面の交互実装への再編）

**rev.5 → rev.6 の対応表は本改訂で削除した**（2026-08-23）。rev.5 の番号を参照している記録は `docs/history/` にも `PROGRESS.md` にも残っておらず、rev.8 まで来て**4段の読み替え**になった時点で使えるものではなくなったためである。

rev.6 が行ったこと自体は 11.2 に残っている——手順7以降を「API の手順と画面の手順を交互に並べる」形へ再編し、`client` 雛形（旧13）を手順7へ前倒しした。この改訂の判断の経緯は `docs/history/decisions.md`（2026-08-18 / 12a）にある。

## Phase 2 — エージェント連携

```
20. マイグレーション 0015〜0017                     ← DbDesign 8.1, 8.2
21. agent / access_token(agent) / task_lease
22. MCP サーバと read 系ツール
23. コンテキストパック生成（初期は単純な選定でよい）
24. DoD の machine 型（assertion / artifact / review）と agent_report
25. proposal と承認キューUI
26. セットアップ画面と設定ファイル生成（`Requirements.md` 10.9）
```

旧手順21 は「`dod_item` と `agent_report`」だった。**`dod_item` は Phase 1（手順18）へ前倒しした**ため、Phase 2 に残るのは `manual` 以外の型を開けることだけになる（`DbDesign.md` 6.11 / 8.1.3）。

## Phase 3 — AI機能・分析

```
27. マイグレーション 0018〜0021                     ← DbDesign 8.3
28. Readiness 判定、DoD ドラフト生成
29. コメント分類・重要度スコアリング
30. ベクトル検索、プロジェクトヒストリー
31. 進捗分析画面（消化状況・残存チケット・ベロシティ）  ← GuiDesign 10章
32. OIDC / SAML 連携
33. カスタムロールの編集UI
```

---

## 付録A. 本書に関する未解決の検討事項

各領域固有の検討事項は、それぞれの設計書の末尾に記載している（`DbDesign.md` 10章、`ApiDesign.md` 10.2、`GuiDesign.md` 11章）。本書に残るのは以下。

- MCPサーバとREST APIの責務分担（8章の着手時に確定）
- `Requirements.md` 8章の「KEDAでスケール0」を PostgreSQL 常駐構成でどう扱うか
- アプリケーションログと `audit_log` の使い分け（何を両方に書き、何を片方に留めるか）
- PB自身の開発に PB を使う（ドッグフーディング）時期。Phase 2 のMCPサーバ完成が前提になる

### 解決済みとして削除した項目（rev.1 から）

| 項目 | 結論 |
|---|---|
| SQLite の単一ライタ制約下でのエージェント並行アクセス | PostgreSQL 前提化により解消（`DbDesign.md` 2.1） |
| PostgreSQL 移行時のデータ移送手順とダウンタイム | 移行そのものが不要になった |
| `permission` カタログの粒度 | 28件で確定（`DbDesign.md` 7.2） |
| プロジェクトロールのUIをどの段階で入れるか | Phase 3（`GuiDesign.md` 5.6.3） |
| サーバ言語の確定（Rust / Go） | **Go** に確定（3.1） |
