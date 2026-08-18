# 実装進捗

`Design.md` 11章の手順に対応。**ステップを完了したら必ずこの表を更新すること。**

**この文書は現況だけを持つ。** 完了した手順の詳しい記録（判断の経緯・作った
ファイル・検証結果）は `docs/history/` にある。毎回読む必要はない → 「過去の記録」（末尾）

## Phase 1

| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 完了 | 2026-08-11 | DB起動と `pb_app` ロール |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 完了 | 2026-08-11 | テーブル23個・権限28件・ロール5件 |
| 3 | `pb admin create` による初期管理者作成 | 完了 | 2026-08-12 | `system_role='administrator'` で入る |
| 4a | 共通基盤その1（sqlc導入・エラー形式・ページネーション・request_id・アクセスログ・ヘルスチェック・serve骨格） | 完了 | 2026-08-12 | `go test` ＋ `/healthcheck` と 2.5 形式の 404 |
| 4b | 共通基盤その2（認証ミドルウェア・監査ログ） | 完了 | 2026-08-12 | `go test` ＋ 実DBの認証結合テスト |
| 5a | `POST /auth/login`・`/auth/logout`・`GET /me`・実効権限の計算 | 完了 | 2026-08-12 | Set-Cookie 2種・`GET /me` が権限28件 |
| 5b | CSRF ミドルウェア（2.4）・レート制限（2.9） | 完了 | 2026-08-12 | `X-PB-CSRF` 無しで 403、11回目で 429 |
| 6a | 認可ミドルウェア（`RequirePermission` / `RequireProjectPermission`） | 完了 | 2026-08-13 | operator→403 / 非メンバー→404（**実サーバでの実地確認は手順9b で完了**） |
| 6b | 実効権限のセッションキャッシュ（マイグレーション 0012、`Design.md` 6.4.5） | 完了 | 2026-08-13 | 昇格しても12件のまま→破棄後28件 |
| 7 | client 雛形（Vite + Pinia + router + デザイントークン）と embed 疎通 | 完了 | 2026-08-13 | 単一バイナリで Vue が起動・SPA フォールバック |
| 7.5 | 開発用デモデータ（`pb dev seed`）・`make dev-reset` / `dev-seed` / `dev-info` | 完了 | 2026-08-13 | 4アカウント＋デモPJ・2回目は冪等 |
| 8a | `openapi.yaml` 初版・ドリフト検出テスト・API層・auth ストア・ログイン画面・ルーターガード | 完了 | 2026-08-15 | ブラウザ16件＋ドリフト検出テスト |
| 8b | AppShell・メインメニュー・権限による出し分け・ユーザーメニュー | 完了 | 2026-08-15 | ブラウザ39件（出し分け・テーマ・折りたたみ） |
| 9a | `GET /projects`（件数・進捗・ページネーション・ETag）と `check-key` | 完了 | 2026-08-15 | 実サーバで一覧・ETag・422・`check-key` 4判定 |
| 9b | `POST /projects`（テンプレート複製・単一トランザクション・5.4 形式の応答） | 完了 | 2026-08-15 | 201＋複製＋監査ログ、重複で 409、operator は 403 |
| 10a | プロジェクト一覧画面（表・ソート・ページング・4状態・アーカイブ切替） | 完了 | 2026-08-15 | ブラウザ53件（ソート・ページャ・4状態） |
| 10b | 新規プロジェクト作成モーダル（`check-key` の即時検証・作成・409） | 完了 | 2026-08-18 | ブラウザ32件（即時検証・debounce・409・3テンプレート） |
| 11 | `GET/PATCH /projects/:key`、archive、プロジェクト設定画面 | 未着手 | | `If-Match` 不一致で 409。設定画面から名前を変更できる |
| 12 | `GET/POST /admin/users` | 未着手 | | ユーザーを作成し、初期パスワードが1回だけ返る |
| 13 | ユーザー管理画面（一覧・追加モーダル） | 未着手 | | ブラウザでユーザーを追加でき、初期パスワードが1回だけ表示される |
| 14 | `GET/PATCH/DELETE /admin/users/:id`、password-reset、memberships | 未着手 | | 自分自身のロール変更が 409、最後の管理者の降格が 409 |
| 15 | ユーザー詳細・編集画面 | 未着手 | | ロール変更・無効化・パスワードリセット・プロジェクト権限の付与ができる |
| 16 | `GET /roles`、`GET /permissions` とロールと権限タブ | 未着手 | | 権限28件・ロール5件が返り、マトリクスが表示される |
| 17 | `/me` 系 API と自分の設定・トークン管理画面 | 未着手 | | パスワード変更後に他セッションが失効する。テーマと色相を切り替えられる |
| 18 | チケット API と画面 | 未着手 | | `ApiDesign.md` 9章の確定後に着手。プレースホルダを実画面へ差し替える |

**手順7以降は `Design.md` 11章 rev.6 で再編した**（API と画面の交互実装）。旧番号との対応は `Design.md` 11.4 を参照。

### プレースホルダで開始する画面

`GuiDesign.md` 6.5 の規約に従い、以下は手順7の時点でプレースホルダを置く。実装時にルート定義を差し替える。

| ルート | 画面 | 実装予定 |
|---|---|---|
| `/p/:key` | プロジェクトダッシュボード | Phase 1 手順18以降（統計APIが必要） |
| `/p/:key/tickets` | チケット一覧 | 手順18 |
| `/p/:key/tickets/:seq` | チケット詳細 | 手順18 |
| `/admin/audit` | 監査ログ | Phase 1 内で未定 |
| Phase 2/3 の全画面 | ボード・ガント・承認キュー等 | `GuiDesign.md` 3.2 参照 |

**状態の記法**：未着手 / 進行中 / 完了 / 保留

## 手順外の作業

`Design.md` 11章の手順に属さない作業はここに記録する。

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
| ダッシュボードのページヘッダを「プロジェクト名 ＋ 画面名」にする（`GuiDesign.md` 5.3。利用者からの指摘） | 完了 | 2026-08-15 | `feature/step-10-projects-page` | ブラウザで5件：メンバーは `デモプロジェクト ダッシュボード`、demo に未所属の管理者は `demo ダッシュボード`（キーで代替）、チケット一覧は `チケット一覧` のまま、プレースホルダのカード内は `プロジェクトダッシュボードのページ予定` のまま（6.5 の規約を保つ） |

## バージョンの現況

| | 値 |
|---|---|
| 現在 | **v1.12.15**（手順10b のマージ前に `make bump-minor`。マージ後に `make version-check` すること） |
| 内訳 | メジャー1 / マイナー12 / ビルド15 |
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

手順10 は当初 10a と 10b を1回のマージにする予定だったが、**10a の完了時点でマージすることを
ユーザーが指示した**（2026-08-15）。したがって `make bump-minor` は 10a で実行し（`1.10.13` →
`1.11.14`）、**10b は同じブランチで続けて、完了時にもう一度マージする**（ビルド15）。
分割した手順を2回に分けてマージした最初の回である（4a/4b 以降はいずれも1回だった）。

10b でも `make bump-minor`（`1.11.14` → `1.12.15`）を実行した（ユーザーの選択、2026-08-18）。
`Design.md` 11.1 の条件表が「マイナーは `feature/*` のマージで上がる」と機械的に定めており、
10b も `feature/*` のマージであるため。**1つの手順を2回に分けてマージすると、マイナーが
2つ進む**ことになるが、11.1 の規約はマージ回数を数えるものであって手順を数えるものではない。

規約は `Design.md` 11.1。**マージ前に feature ブランチ上で `make bump-minor`（`fix/*`・`docs/*` は `make bump-build`）を実行し、`VERSION` の更新を同じブランチに含める。**

## 次の手順への引き継ぎ

**過去の判断のうち、まだ消化していない約束だけをここに置く。** 決着済みの記録は
`history/decisions.md` にある（下記「過去の記録」）。**手順に着手するときは、この表に
その手順の行が無いかを必ず見ること。** 消化したらその行を消し、結果を
`history/decisions.md` の表の末尾に書く。

| 効いてくる手順 | 内容 | 出典（`history/decisions.md`） |
|---|---|---|
| 11 | **`ApiDesign.md` 2.7 の ETag の例 `"W/proj-…"` が RFC 9110 違反**（`W/` は引用符の外）。9a の実装は `W/"proj-<件数>-<ナノ秒>"` で正しい。**例の修正提案が未提出**。`If-Match` を扱う手順11でまとめて出す | 2026-08-15 / 9 |
| 11 | `project` ストアは一覧の状態しか持っていない。`GuiDesign.md` 7.1 の「選択中プロジェクト」は、実画面が要求した時点で足す | 2026-08-15 / 10a |
| 12・13 | **`DataTable`（`GuiDesign.md` 6.1）の抽出を提案する。** 10a は消費者が1つのため `ProjectsPage` に直接書いた。ユーザー一覧が2つ目の消費者になる | 2026-08-15 / 10a |
| 13 | 日時整形（`formatDateTime`）も同じ理由で `ProjectsPage` 内にある。2つ目の消費者が出たら共通化を提案する | 2026-08-15 / 10a |
| 13 | **トースト通知（`GuiDesign.md` 6.4）はまだ作っていない。** 削除・パスワードリセットのように画面遷移を伴わない操作が出る手順13以降で作る | 2026-08-15 / 10a |
| 13 | `Modal.vue` はユーザー追加モーダル（`GuiDesign.md` 5.6.1）で再利用する前提で汎用にしてある | 2026-08-18 / 10b |
| 13以降 | **`deploy/Dockerfile` が未作成のため `make up` の対象を `db` のみにしている**（Makefile に TODO）。全サービスへ戻すときに、compose の `app` に `healthcheck` を足すか（`pb healthcheck` サブコマンド案）も同時に決める。**`/healthcheck` は SPA フォールバックの例外**なので embed 経路で取りこぼさないこと | 2026-08-11 / 1、2026-08-12 / 4a |
| 17 | テーマ・色相の保存先を `localStorage`（`pb.theme` / `pb.hue`）から `app_user` へ移す（`GuiDesign.md` 8.11 は両方に保存すると定める） | 2026-08-13 / 7 |
| 17 | 日時は端末のローカル時刻で出している。`app_user.timezone` の反映は自分の設定で扱う | 2026-08-15 / 10a |
| 17 | `must_change_password: true` の誘導先（パスワード変更画面）が未実装。8a はストアに持つだけで遷移を変えていない | 2026-08-15 / 8a |
| 18 | チケットの未完了件数バッジ（`GuiDesign.md` 4.1 の `[12]`）は、供給するAPIが無いため出していない | 2026-08-15 / 8b |
| Phase 2 | **トークンスコープ（`ticket:read` 等）と権限キー（`ticket.view` 等）の対応表が未定義。** 5a は完全一致で絞っており、Phase 1 のセッションは `scopes=[]` なので実害はない | 2026-08-12 / 5a |
| Phase 2 | エージェントは `system_role` を持たないため現状は権限0件で 403。権限の持たせ方は Phase 2 で設計する | 2026-08-13 / 6a |
| Phase 2/3 | マイグレーションの採番が 0013〜0020 へずれている（`DbDesign.md` 8章は構成案でありファイル名の予約ではない） | 2026-08-13 / 6b |
| API を足す手順 | **`docs/openapi.yaml` を更新したら同じ手順の中で `make gen-api` まで走らせる。** 手順9→10a で持ち越しが起きた | 2026-08-15 / 10a |
| 未定 | `ApiDesign.md` 2.5.1 の11コードの既定文言が実装側（`apierr.messages`）にしかない。文書に持たせるなら 2.5.1 に message 列を足す | 2026-08-12 / 4a |
| 未定 | HTTPサーバのタイムアウト値が実装（`serve.go`）にしかない。`Design.md` 10章を扱うときに文書化を提案する | 2026-08-12 / 4a |
| 未定 | OKLCH の hex フォールバック（`GuiDesign.md` 8.5）が未実装。PostCSS の採用可否が 11章の未解決事項のまま | 2026-08-13 / 7 |
| 未定 | DBを使うテストの作法（`PB_TEST_DATABASE_URL`）を `Design.md` に書くかは要判断 | 2026-08-12 / 4b |
| 随時 | **プロジェクト作成の手順が `cmd/pb/dev_seed.go` と `httpapi/v1/projects_create.go` の2か所にある。** 片方だけ直すと差が開く。共通化は見送っている | 2026-08-15 / 9b |
| 次に閾値を超えたとき | **本文書は 230行 / 28KB で、振り返りの閾値（250行 / 30KB）に近い。** 次に超えたら、残る最大の塊である「環境メモ」から**再現手順になるものを `Development.md` へ寄せる**（制約だけを残す） | 2026-08-18 / 手順外 |
| Phase 1 完了時 | `LEARNINGS.md` の棚卸し（一度も再発していないエントリの退役、昇格候補の判断） | 2026-08-18 / 手順外 |
| 保留中の検討 | **`CLAUDE.md` の規約強化**を今回は見送った——①自律の線引き（止まる／進めて報告／黙って進める）②実現したい価値・世界観の明文化 ③手順番号ではなく目的で指示を受ける入口。**2回ルールに従い、必要性が再度現れたときに判断する** | 2026-08-18 / 手順外 |

## 環境メモ

実際に動かして分かったこと（バージョンの相性、ハマった点、回避策）を追記する。

> **再現できる手順は `docs/Development.md` に移した**（2026-08-15、`docs/development-guide`）。
> 起動・セットアップ・検証のやり方を知りたいときはそちらを見ること。**ここに残すのは
> 「いつ・どの手順で・何を見つけたか」の記録**であり、手順の正本ではない。
> 新しく分かったことは、まずここに書き、再現手順として使えるものは `Development.md` へ写す。

- **セッションの会話は `~/.claude/projects/<リポジトリの絶対パスの / を - に置き換えたもの>/<UUID>.jsonl` に残る**
  （2026-08-18 時点で16セッション・24MB。`gitBranch` が入るので手順と対応づけられる）。
  **既定30日で自動削除される**（`~/.claude/settings.json` の `cleanupPeriodDays`。未設定なら既定値）。
  過去セッションを掘り起こすなら期限内に行う。リポジトリには含まれない
- ホストの Node: **v24.14.0** / npm **11.9.0**（手順7の時点）。`make build-client` は `npm ci` を使うため
  `client/package-lock.json` をコミットしている
- client の依存（手順8時点）: `vue` / `vue-router` / `pinia` ＋ dev に `vite` / `@vitejs/plugin-vue` /
  `typescript` / `vue-tsc` / **`openapi-typescript`**（手順8で追加。型生成のみで実行時には入らない）
  - **`typescript` は `^5` に固定すること**（手順7で判明。vue-tsc 3.3.9 が TS 7 の
    `typescript/lib/tsc` を require できない）→ 症状と対処は `Development.md` 9章
  - `npm run build` は型検査（`vue-tsc --noEmit`）を通してから `vite build` する。型エラーは
    ビルドを止める
- **`make build` の後は `make clean-webui` を実行してからコミットする**（手順7で判明）
  → `Development.md` 7.1
- **画面の描画はヘッドレス Chrome で確認できる**（手順7で導入、手順8で CDP 経由の操作まで拡張）
  → 呼び出し方と注意点は `Development.md` 8章。Playwright / Puppeteer は入れていない
  - 手順8で気づいた2点（**ウィンドウ幅を指定しないと 768px 未満と判定される** ／
    `v-model` にはネイティブの value セッター＋`input` イベントが要る）も 8章に書いた
- **コンテナランタイムは Rancher Desktop**（`docker` は `~/.rd/bin/docker`）。手順8の開始時に
  停止しており `make up` が落ちた → 起動手順は `Development.md` 1章・9章
- **Vite 開発サーバは `:5173`（`strictPort`）。** ポートが空いていなければ黙ってずらさずに失敗する。
  API は別途 `make run` で `:8080` に立てる（`/api` `/mcp` だけがプロキシされる）
- **`make run` を止め忘れると次のセッションで `bind: address already in use` になる**（手順5b で発生）
  → 確認と回避は `Development.md` 9章
- **Cookie 認証で状態変更系を叩くテストは `X-PB-CSRF` が要る**（手順5b で導入）
  → curl の手順は `Development.md` 8.3
- **レート制限のカウンタはプロセス内メモリにある**（手順5b）。`make run` を再起動すると消えるため、
  429 を再現する検証は**サーバを起動したまま**続けて叩くこと。ログインは IPあたり 10回/分
- **パスワードを知らないアカウントでログインを試すと `failed_attempts` が増える**（手順6a の疎通確認で
  `tanaka@example.com` に1回記録した）。5回で15分ロックされ、次のセッションの検証を妨げる
  → 戻し方は `Development.md` 8.4
- **プロジェクトキーには CHECK 制約がある**（`DbDesign.md` 6.4）。`^[a-z0-9][a-z0-9-]{1,19}$` で
  **2〜20文字**。テストで ULID をそのまま使うと長さ超過で INSERT が落ちる（末尾6文字を小文字化して使った）
- **`.claude/settings.json` の deny は Read ツールにしか効かない。** `Read(./deploy/*/secrets/**)` を deny していても、`allow` にある `Bash(cat:*)` 経由では読めてしまう（手順4b の検証で `app_db_password` を実際にそう読んだ）。秘密を機械的に守りたい場合は Bash 側にも `deny` を足す必要がある
- **DBを使うテストは `PB_TEST_DATABASE_URL` で切り替える**（手順4b で導入）。未設定ならスキップするので
  `make test` は DB 無しでも通る → 実行例は `Development.md` 6.1。
  接続は `pb_app`（DML のみ）で行う。実運用と同じ権限で通ることを確かめるため
- **`t.Cleanup` は `defer` より後に走る。** 結合テストで `defer pool.Close()` と `t.Cleanup(削除)` を併用すると、後片付けの時点でプールが閉じていて `closed pool` になる。プールの close も `t.Cleanup` で登録し、LIFO の順序を使うこと
- **可変長引数を渡さないと `nil` スライスになる**（`[]string{}` ではない）。実効権限のキャッシュは
  `nil`（キャッシュ不在）と長さ0（権限0件）を区別するため、テストヘルパで `f()` と書くと
  意図せず「不在」になる。手順6b で `append([]string{}, xs...)` に直して気づいた
- **実サーバでのログイン検証にはパスワードの分かるアカウントが要る。** 手順3で作った
  `tanaka@example.com` のパスワードは記録されていない。手順6b では Argon2id ハッシュを
  スクラッチパッドの小さなモジュールで生成し、`actor` → `app_user` → `user_identity` →
  `local_credential` を直接 INSERT して検証用ユーザーを作った（検証後に削除）。
  ハッシュのパラメータは `server/internal/auth/password.go` の `hashParams`
  （m=65536, t=3, p=4, salt=16, key=32）に合わせること
- **検証で `login.failure` を1件でも出したら消しておく。** `audit_log` に残り、次のセッションの
  件数の検証を狂わせる → あとしまつの一覧は `Development.md` 8.4
- Go の直接依存（手順4b時点）: `jackc/pgx/v5 v5.7.5` / `oklog/ulid/v2 v2.1.2` / `alexedwards/argon2id v1.0.0` / `golang.org/x/term v0.33.0` / `go-chi/chi/v5 v5.3.1`（手順4a から**増えていない**。トークンのハッシュと乱数は標準ライブラリの `crypto/sha256` / `crypto/rand` で足りる）
  - **`x/term` と `x/sys` はバージョンを上げないこと。** 最新版は go 1.25 を要求し、`go get` が go ディレクティブを勝手に 1.25.0 へ引き上げる（`Design.md` 3.1 と衝突）。上げる際は 3.1 の最低バージョンとセットで見直す
  - `go get` 後は `head -3 server/go.mod` で go ディレクティブが `1.24` のままか確認する
- **パスワード入力のエコー抑止には競合窓がある。** プロンプトを出してから `term.ReadPassword` が echo を切るまでの数マイクロ秒に文字が届くと、その分だけ端末に表示される。`expect` から遅延なしで送ると再現するが、人間の入力では起こらない（`sudo` や `ssh` も同じ挙動）
  - 端末ありの検証は `expect` に `sleep 0.4` を入れて行う。`printf ... | script -q /dev/null` は stdin を即座に閉じるため `EOF` になり検証に使えない
- goose のバージョン: **v3.26.0**（手順4a から `server/tools/go.mod` の tool ディレクティブで固定）
  - v3.27.3 以降は `go 1.25.7` を要求し、`Design.md` 3.1 の「Go 1.24 以上」と衝突するため上げていない
- sqlc のバージョン: **v1.30.0**（`server/tools/go.mod`）
  - v1.31.1 は `go 1.26.0` を要求するため上げていない。v1.30.0 自体は `go 1.23.0` 要求
  - **`go get -tool` は実行順で結果が変わる。** `tools/go.mod` に sqlc → goose の順で入れると go ディレクティブが 1.24 のまま保たれるが、goose → sqlc の順だと `x/*` が最新へ上がって 1.25.0 に書き換えられる。ツールを足したら必ず `head -3 server/tools/go.mod` を見る
  - `citext` は sqlc が既定の対応を持たないため、`sqlc.yaml` の `overrides` で `string` に写している。`inet` は `*netip.Addr`、NULL 許容列は `pgtype.*` になる
- **ツールは `server/tools/go.mod` に隔離してある。** `make migrate` / `make sqlc` は `cd server/tools` してから `go tool` を呼ぶ。`server/go.mod` にツールを足さないこと（indirect が80件超に膨らみ、go ディレクティブも 1.25 へ上がる）
- ホストの Go: 1.26.5（Homebrew。手順2で導入）。`make migrate` / 手順3以降の `make run` / `make build` に必要
- ホストに `psql` が入っていないため、`make psql` は `docker compose exec db psql` でコンテナ内に入る
- `initdb/01_roles.sh` は**実行ビットを立てておくこと**。`:ro` マウントでもホスト側のファイルモードがそのまま使われる
- initdb スクリプトが走るのは `pgdata` ボリュームが空の初回起動時のみ。ロール定義を変えたら `docker compose -f deploy/base/compose.yaml down -v` でボリュームごと作り直す
- 秘密の実ファイルは `deploy/dev/secrets/` に3つ必要（`db_password` / `app_db_password` / `app_database_url`）。`app_database_url` に埋め込むパスワードは `app_db_password` と同じ値にする
- **マイグレーションは `pb_owner` で接続する。** `pb_app` はDDLを実行できない（それが 3.4 のロール分離の目的）。`make migrate` は `db_password` から接続文字列を組み立てている
- initdb の `ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner` により、goose が作ったテーブルにも `pb_app` の DML 権限が自動で付く。マイグレーション後に GRANT を流す必要はない（`goose_db_version` も同様）

## 過去の記録

完了した手順の記録は `docs/history/` にある（2026-08-18 に分割。`docs/progress-archive`）。
**セッションの開始時に読む文書ではない。** 必要になったときに grep して、当たった箇所だけを読む。

| 探したいもの | 引き先 |
|---|---|
| ある実装をなぜそう決めたか／設計文書と食い違ったときに何を選んだか | [history/decisions.md](history/decisions.md)（日付順の表。手順名で grep する） |
| 設計文書に反映済みの修正の一覧（手順1〜6b） | [history/decisions.md](history/decisions.md) の `### 設計文書へ反映済みの修正` |
| バージョン運用の判断（手順4a〜9 のマージの分け方） | [history/decisions.md](history/decisions.md) の `## バージョン運用の判断` |
| どの手順で何を作ったか（ファイル一覧）・どう検証したか | [history/steps.md](history/steps.md) |
| 進捗表を要約する前の検証内容の全文 | [history/steps.md](history/steps.md) の `## 進捗表から移した検証内容` |

**記録の書き分け**（これを守らないと本文書がまた肥大化する）

| 書くこと | 書き先 |
|---|---|
| 手順の状態・完了日・検証の要約（1行） | 本文書の Phase 1 表 |
| **次の手順以降に持ち越す約束** | 本文書の「次の手順への引き継ぎ」 |
| 判断の経緯・設計文書との食い違いとその対応 | `history/decisions.md` の表の末尾 |
| 作ったファイルの一覧・検証結果の全文 | `history/steps.md` の末尾 |
| いま効いている環境の制約 | 本文書の「環境メモ」 |
| 再現できる手順 | `docs/Development.md`（設計文書ではなく手順書） |
