# Project Backyard (PB)

人間とAIエージェントが共同で使うプロジェクト管理ツール。
サーバは Go、DBは PostgreSQL、フロントは Vue 3。ローカル端末で docker compose により動作させる。

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

**全文を読み込まないこと。** 各文書は数百〜1400行ある。目次から必要な章を特定して、その章だけを読む。

文書どうしの相互参照は `DbDesign.md 6.2` のようにファイル名のみで書かれている。いずれも `docs/` 配下を指す。

## 絶対規則

1. **推測で実装しない。** 仕様が不明な点は設計文書を読むか、ユーザーに質問する
2. **設計文書に書かれていない設計判断を勝手に行わない。** 判断が必要になったら、選択肢と推奨を提示して承認を得る
3. **設計を変える必要が生じたら、先に設計文書の修正案を出す。** コードだけ先に変えない
4. **1セッション = 1ステップ。** スコープ外のファイルを変更しない。ついでのリファクタをしない
5. **DDLを勝手に変更しない。** `docs/DbDesign.md` 6〜7章の記述が正本。変更が必要なら 3 に従う
6. **秘密（パスワード・トークン・接続文字列）をコードやドキュメントに書かない。** 環境変数と `deploy/<env>/secrets/` を使う

## 技術スタック

| 用途 | 採用 | 備考 |
|---|---|---|
| 言語 | Go 1.24+ | |
| ルータ | chi v5 | 権限はミドルウェアとしてルート定義に宣言する（`docs/Design.md` 6.4.4） |
| DB | pgx v5（`database/sql` 非経由） | |
| クエリ | sqlc（pgx/v5 モード） | `migrations/` をスキーマ源として読む |
| マイグレーション | goose v3 | 前進のみ。`down` を書かない（`docs/DbDesign.md` 5.3） |
| ログ | `log/slog`（JSON） | |
| フロント | Vue 3 + TypeScript + Vite + Pinia | |

詳細と選定理由は `docs/Design.md` 3章。

## 命名・形式の規約

- API のフィールドは **snake_case**（`docs/ApiDesign.md` 2.2）。フロントも変換せずそのまま使う
- ID は **ULID**（`char(26)`）。アプリ側で生成する。DBの自動採番を使わない
- 日時は `timestamptz`、API では ISO8601 UTC
- `jsonb` 列に `_json` 接尾辞を付けない
- エラー応答は `docs/ApiDesign.md` 2.5 の形式に統一する。`message` はそのまま画面に出せる日本語

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
make dev-client  # Vite 開発サーバ（:5173、/api を :8080 へプロキシ）
make build       # client をビルドして embed し、単一バイナリを作る
make test
make psql        # DBコンソール
```

## 進捗

現在 **Phase 1**（`docs/Design.md` 11章）。どこまで完了したかは `docs/PROGRESS.md` を見ること。
ステップを完了したら `docs/PROGRESS.md` を必ず更新する。

実装は `/pb-step <手順番号>` で開始する。
