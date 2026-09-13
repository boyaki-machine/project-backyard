# Project Backyard (PB)

人間とAIエージェントが共同で使うプロジェクト管理ツール。
サーバは Go、DBは PostgreSQL、フロントは Vue 3。ローカル端末で docker compose により動作させる。

**本書はこのリポジトリの取扱説明である。規約ではない。**

## 規約の正本は PB にある

**このプロジェクトの規約は、PB の文書「規約」が正本である**（`Requirements.md` 10.6.2）。
`pb_list_docs` で目次を取り、`pb_get_doc` で読む。**着手する前に必ず読むこと。**

**本書に規約を書き戻さない。** `CLAUDE.md` は Claude にしか効かず、エージェントの種別は増えていく。
**種別によらず MCP で取れる場所に正本を置く**、というのがこの分担の理由である。

| 正本 | 何を |
|---|---|
| **PB の「規約」** | 進め方の規約——止まる条件、作業の単位、ブランチとバージョン、命名と形式、振り返り、規約の増やし方 |
| **PB の「エージェントの参画情報」** | 作業材料の取り方、**種別ごとの入口**、資格情報の要否 |
| **リポジトリ**（本書と `docs/`） | 設計文書とコマンド文書（いずれもプロジェクトの成果物）、`make` のターゲット、技術スタック、構成 |
| **各自の手元**（`LEARNINGS.md`） | エージェント個体の学習。共有しない |

**MCP に繋がらないときは、そこで止めて利用者に伝えること。** 規約を読まずに進めない。

## 設計文書（実装前に該当箇所を読むこと）

**設計文書はすべて `docs/` 配下にある。**

| 領域 | 正本 |
|---|---|
| 全体設計・技術選定・開発フェーズ | `docs/Design.md` |
| 認証・認可 | `docs/Design.md` 6章 |
| DBスキーマ・マイグレーション・compose構成 | `docs/DbDesign.md` |
| REST API | `docs/ApiDesign.md` |
| 画面・遷移・配色 | `docs/GuiDesign.md` |
| 要件・背景・AI駆動開発の構想 | `docs/Requirements.md` |
| 文書の索引と主要な設計判断 | `docs/README.md` |
| 開発環境の立ち上げ・検証手順（**設計ではなく手順**） | `docs/Development.md` |
| 試験の設計と完了の基準（**何をどこまで検証したら終わりか**） | `docs/Testing.md` |

**全文を読み込まないこと。** 各文書は数百〜1400行ある。目次から必要な章を特定して、その章だけを読む。

文書どうしの相互参照は `DbDesign.md 6.2` のようにファイル名のみで書かれている。いずれも `docs/` 配下を指す。

## 技術スタック

| 用途 | 採用 | 備考 |
|---|---|---|
| 言語 | Go 1.26+ | |
| ルータ | chi v5 | 権限はミドルウェアとしてルート定義に宣言する（`docs/Design.md` 6.4.4） |
| DB | pgx v5（`database/sql` 非経由） | |
| クエリ | sqlc（pgx/v5 モード） | `migrations/` をスキーマ源として読む |
| マイグレーション | goose v3 | 前進のみ。`down` を書かない（`docs/DbDesign.md` 5.3） |
| ログ | `log/slog`（JSON） | |
| フロント | Vue 3 + TypeScript + Vite + Pinia | |

詳細と選定理由は `docs/Design.md` 3章。

## リポジトリ構成

```
docs/     設計・運用文書        server/   Go（API + MCP + 静的配信）
client/   Vue 3                deploy/   環境別の実行設定（base/dev/stg/prod）
```

詳細は `docs/Design.md` 4章。**client のビルド成果物は Go バイナリに embed する**（別のWebサーバを立てない）。

## 開発コマンド

```
make up          # docker compose up -d（DB + アプリ）
make down
make migrate     # goose によるマイグレーション適用
make sqlc        # sqlc generate（server/migrations/ からスキーマを推論）
make run         # サーバをローカル起動（:8080）
make restart     # 停止→ビルド→DB起動→サーバ起動をまとめて行う（画面を直したとき）
make dev-client  # Vite 開発サーバ（:5173、/api を :8080 へプロキシ）
make gen-api     # docs/openapi.yaml から client の型を生成（Design.md 3.3）
make build       # client をビルドして embed し、単一バイナリを作る
make version     # 現在のバージョンと develop へのマージ回数を表示
make bump-minor  # 機能追加のマージ前に実行（fix/ docs/ は bump-build）
make test
make psql        # DBコンソール
make dev-reset   # 開発用：DBを作り直してデモデータを投入
make dev-seed    # 開発用：デモデータのみ投入（冪等）
make dev-info    # 開発用：URL とデモアカウント一覧を表示
make admin-mfa-reset EMAIL=…  # 第2要素を外す（認証アプリを失ったとき。Design.md 6.7.5）

make stg-init    # stg（ドッグフーディング用）：初回セットアップ
make stg-build   # stg：動作に必要な一式を deploy/stg/out/ へ出力
make stg-run     # stg：起動（http://localhost:8081）
make stg-migrate # stg：マイグレーション適用（migrate と同時に打つ）
```

**stg は PB 自身のプロジェクト管理に使う、壊れないインスタンスである**（`Design.md` 4.4、
手順は `docs/Development.md` 11章）。`dev` とは compose プロジェクトごと分かれており、
**`make dev-reset` の影響を受けない**。**画面は `http://localhost:8081` で開く**——
`127.0.0.1:8081` で開くと dev とログインセッションが上書きし合う。

**セットアップ・検証・つまずいたときの対処は `docs/Development.md`。** 秘密ファイルの配置、
結合テストの走らせ方、ヘッドレス Chrome での画面確認、`make build` 後の `make clean-webui` など。

## 作業の入口

**現況は `docs/PROGRESS.md` を見ること。** 本書に進捗を書かない（写した状態は必ず腐る）。

| コマンド | 用途 |
|---|---|
| `/pb-implement <番号>` | PB のチケットを1件実装する（契約の取得→前提の取得→計画→実装→検証→報告） |
| `/pb-step <手順番号>` | `Design.md` 11章の手順を1つ実装する |
| `/pb-review <手順番号\|領域>` | 実装と設計文書の差異を点検する（修正はせず報告のみ） |
| `/pb-refine <番号>` | チケットの記述を、エージェントが自律実行できる水準まで引き上げる |
| `/pb-onboard` | PB のプロジェクトに参画する（規約と担当を読む） |

**これらは Claude Code 向けの入口である。** 他の種別の入口は PB の「エージェントの参画情報」にある。
**手順の中身はこのリポジトリの成果物**であり、規約は PB にある——`.claude/commands/` を直すときは、
規約に当たる記述を書き込まないこと。

**完了した手順の詳しい記録は `docs/history/` にある**（`decisions.md` = 判断の経緯、
`steps.md` = 作ったファイルと検証結果）。**毎セッションで読む文書ではない。**
**各ファイルの先頭に索引がある**ので、そこで見出しを特定してから該当節だけを引く。

**セッションを始めるとき、`LEARNINGS.md` を読む。** 過去のセッションで得た**進め方**の教訓
（設計や進捗ではない）。短い文書なので毎回読む。**個人のセッション履歴から抽出した内容を含むため
`.gitignore` の対象**で、リポジトリには入らない。無ければ見出しと空の表だけ作って始める。

## 文書の分量

**毎セッション読む4文書の合計 80KB が予算である**（`CLAUDE.md` / `LEARNINGS.md` /
`docs/PROGRESS.md` / `.claude/commands/pb-step.md`）。**`make docs-size` で測る。**
個別の閾値は持たない——片方を減らして片方を増やすのを許してしまうため。

- **4ステップごとに必ず棚卸しする**（`pb-step.md` 手順7）。超えてから動くと毎回大手術になる
- **閾値の引き上げを提案しない。** 引き上げるかどうかは利用者だけが判断する
  （過去3回、掃除ではなく閾値のほうが動いた。8KB→40KB→80KB）

**PB 側の憲章は別に測る。** `make docs-size` の対象ではなく、**規約・価値観・学びと知見は全文がコンテキストパックに乗る**
（判断の記録は目次だけ。`Design.md` 8.5.5）。**PB の文書へ何かを移したら、`Testing.md` 7.6 の手順で1回数える。**
材料は `Design.md` 8.6——**3文書の合計が1万字を超えたら切り替え時**である。
