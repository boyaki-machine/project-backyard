# Project Backyard (PB) — 設計文書一覧

人間とAIエージェントが共同で使うプロジェクト管理ツール **Project Backyard** の設計文書群。

## 文書構成

```
design/            設計文書
  Requirements.md   要件・構想（何を作るか・なぜ作るか）
        │
        ▼
  Design.md         全体設計 ── システム構成・技術選定・認証認可・開発フェーズ
        │
        ├── DbDesign.md    データベース設計   ← スキーマ・マイグレーション・DB実行環境
        ├── ApiDesign.md   REST API 設計     ← エンドポイント仕様（実装の現状は openapi.yaml）
        └── GuiDesign.md   GUI 設計          ← 画面・遷移・配色

Development.md     開発環境ガイド ── 上記を実際に動かす手順（設計ではない）
Testing.md         試験の設計と完了の基準（何をどこまで検証したら終わりか）

history/           完了した手順の記録（毎セッションでは読まない。必要なときに grep する）
images/            README などに載せる画像
```

**リポジトリ直下の `LEARNINGS.md` は設計文書ではない。** セッションから得た**進め方**の教訓を
ためる場所で、`CLAUDE.md` と同じくセッションの開始時に読む。**`.gitignore` の対象**なので
clone 直後には存在しない（規約は `Design.md` 11.0）。

| 文書 | 内容 | 状態 |
|---|---|---|
| [Requirements.md](design/Requirements.md) | 要件・構想。1〜9章がチケット管理ツールとしての仕様、**10章がAI駆動開発への拡張** | 記述済 |
| [Design.md](design/Design.md) | 全体設計。**6章（認証・認可）が本書の正本**。他領域は各設計書へ委譲 | Phase 1 実装完了。8章（MCP）は Phase 2 の設計を記述済 |
| [DbDesign.md](design/DbDesign.md) | Phase 1 の完全なDDL・マイグレーション・初期データ・docker compose 構成 | Phase 1 実装完了（0001〜0016 適用済み） |
| [ApiDesign.md](design/ApiDesign.md) | 認証・プロジェクト・ユーザー管理API、**チケットAPI（9章）**、**プロジェクト文書API（10章）** | Phase 1 実装完了。10章は Phase 2 |
| [GuiDesign.md](design/GuiDesign.md) | 画面遷移・ワイヤーフレーム・モノクロマティック配色体系 | Phase 1 実装完了（監査ログのみプレースホルダ） |
| [Development.md](Development.md) | **開発環境の立ち上げ・デバッグ手順。** 設計ではなく、実装済みの範囲を動かす手順 | 実装に追従 |
| [Testing.md](Testing.md) | **試験の設計と完了の基準。** 層の使い分け・期待値の作り方・切り分け・あとしまつ。実行手順は `Development.md` | 2026-09-05 新設 |
| [history/decisions.md](history/decisions.md) [history/steps.md](history/steps.md) | 完了した手順の記録（判断の経緯／作ったファイルと検証結果）。**参照専用。先頭に索引がある** | 追記のみ |

**記述が食い違った場合は、各領域の正本を優先する。**

**`Development.md` は設計文書ではない。** 設計と食い違ったら設計側（上の4文書）が正しく、
`Development.md` の側を直す。手順が実装に追いつかなくなるのが唯一の失敗の形なので、
コマンドやターゲットを増やしたステップの成果物にこの文書の更新を含める。

## 領域と正本の対応

| 領域 | 正本 |
|---|---|
| 要件・AI駆動開発の構想 | `Requirements.md` |
| システム構成・技術選定 | `Design.md` 2〜4章 |
| データベース（スキーマ・DDL・実行環境） | `DbDesign.md` |
| 認証・認可 | `Design.md` 6章 |
| REST API | `ApiDesign.md` |
| MCPサーバ | `Design.md` 8章 |
| プロジェクト文書（Docs） | `DbDesign.md` 8.1、`ApiDesign.md` 10章、`GuiDesign.md` 5.10 |
| **アプリケーション設定（設定の3層）** | **`Design.md` 10.3**、`DbDesign.md` 6.14、`ApiDesign.md` 11.1〜11.3、`GuiDesign.md` 5.12 |
| **TLS 終端と証明書** | **`Design.md` 6.6.1**、`DbDesign.md` 6.15、`ApiDesign.md` 11.4〜11.6、`GuiDesign.md` 5.12.1 |
| **バックアップと復元** | **`DbDesign.md` 9.1**、`ApiDesign.md` 11.11〜11.13、`GuiDesign.md` 5.12.2、`Design.md` 10.4（保守モード） |
| 画面・UI・配色 | `GuiDesign.md` |
| 開発フェーズ・実装順序 | `Design.md` 11章 |
| ブランチ運用・バージョン番号 | `Design.md` 11.0〜11.1 |
| 開発環境の立ち上げ・デバッグ手順 | `Development.md` |
| 試験の設計・完了の基準 | `Testing.md` |

## 主要な設計判断

| 判断 | 記録場所 |
|---|---|
| PBはエージェントを起動せず、エージェントがPBに接続しに来る（pull型） | `Requirements.md` 10.2.2 |
| 静的ファイルには手順のみ、内容は実行時にMCPで取得 | `Requirements.md` 10.1.2、10.8 |
| PostgreSQL を初期から使う（SQLite先行案の廃止） | `DbDesign.md` 2章 |
| サーバは Go（chi + pgx + sqlc + goose）。マイグレーションが唯一のスキーマ定義になる | `Design.md` 3.1〜3.2 |
| `actor` を人間とエージェントの共通基底にする | `Design.md` 5.3、`DbDesign.md` 6.2 |
| `app_user` と `user_identity` を分離しOIDC/SAMLに備える | `Design.md` 6.2.3 |
| 権限をコードではなくデータ（permissionカタログ）で定義する | `Design.md` 6.4、`DbDesign.md` 7.2 |
| AIの提案はすべて `proposal` テーブルを経由させる | `DbDesign.md` 8.3.2 |
| **チケットのグルーピングは2軸**——分解は親子階層、分類はタグ。1軸に混ぜない | `DbDesign.md` 6.10 |
| **設定を3層に分ける**（起動前＝環境変数と設定ファイル／実行時＝DB／共有される秘密＝DB で暗号化） | `Design.md` 10.3 |
| **サーバが自分の設定ファイルを書き換える案を採らない**（スケールアウトでレプリカごとに分岐する） | `Design.md` 10.3 |
| **TLS の証明書は DB に置く。** 出すのは「有効なもののうち `notBefore` が最も新しいもの」 | `Design.md` 6.6.1 |
| **チケットは「視点」（バックログ／カンバン／ガント）で見る。** 同一データを別の描き方で出し、グループ化軸を共有する | `GuiDesign.md` 4.1.1 |
| **バックログはページングしない。** グループ化・階層・並べ替えがページ境界をまたげないため | `ApiDesign.md` 9.2.3 |
| チケットは API でも `seq`（プロジェクト内連番）で指す。ULID は返すが指定には使わない | `ApiDesign.md` 9.1 |
| アプリ共通ヘッダを持たない（縦方向の可用領域を優先） | `GuiDesign.md` 2.1 |
| モノクロマティック配色。有彩色は危険・警告・AIの3つのみ | `GuiDesign.md` 8章 |
| 紫は「AI由来」ではなく「未確認のAI出力」を意味する | `GuiDesign.md` 8.4.2 |
| ビルド番号は `develop` へのマージ回数。ブランチ接頭辞でマイナーを上げるか決まる | `Design.md` 11.1 |
| **単独開発で足りることを PB に作らない。** PB が要るのは参加者が複数になってから | `Design.md` 1.1 原則8、`Requirements.md` 10.0 |
| **文書の型はテンプレートで配り、データモデルに語彙を持たせない**（ワークフローテンプレートと同じ機構） | `DbDesign.md` 8.1 |
| **文書の章は永続化しない。** 保存する参照は `document.id` のみ | `DbDesign.md` 8.1.3 |
| **バックアップは PB 独自の形式と `pg_dump` を併用する。** 前者は配置に依らず打てるがスキーマを持たず、後者はその逆 | `DbDesign.md` 9.1 |
| **取り込みは、行を消して入れ直すのではなく、表を落として作り直す。** 書き出した時点のスキーマまで戻してから行を入れる | `DbDesign.md` 9.1.1 |
| **PB は `pb_owner` の資格情報を持たない。** 取り込みのときだけ画面で受け取り、終わったら捨てる | `DbDesign.md` 3.4 |

## 開発フェーズ

| Phase | 内容 | 状態 | 詳細 |
|---|---|---|---|
| **1** | 認証・認可、プロジェクト、チケットの基礎。ローカルでの動作確認まで | **完了**（2026-08-28） | `Design.md` 11章 |
| 2 | **複数人とエージェントが同じプロジェクトを進められるようにする**——プロジェクト文書（Docs）、MCPサーバ、コンテキストパック（手順20〜28） | 手順は完了（2026-09-06）。受け入れ待ち（pb-163） | 同上 |
| 3 | AI機能（Readiness判定・要約・ベクトル検索）、**承認キュー・プロジェクトメモリ**、OIDC/SAML連携（手順29〜38） | 未着手 | 同上 |

## 現在の着手ポイント

**stg の PB（プロジェクト `pb`）のチケットが正本である。** 本書には写さない——
実装が進むたびに2か所を直すことになり、片方が必ず古くなるため。
