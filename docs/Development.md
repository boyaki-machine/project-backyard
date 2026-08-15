# Project Backyard (PB) 開発環境ガイド

**開発端末で PB を動かし、直し、確かめるための手順書。**

- 設計の正本は `Design.md` / `DbDesign.md` / `ApiDesign.md` / `GuiDesign.md` にある。本書は**それらを実際に動かす手順**だけを扱う
- **実装済みの範囲だけを書く。** 未実装の手順は書かない（`docs/PROGRESS.md` の進捗と揃える）
- 実装の進捗そのものと「いつ何を判断したか」は `docs/PROGRESS.md` にある

> **本書のコマンドは、断りがない限りすべてリポジトリ直下で実行する。**
> `make` はカレントディレクトリの Makefile しか見ないため、下位ディレクトリで叩くと
> `No rule to make target` や `no makefile found` になる。どこからでも叩きたい場合は
> `make -C <リポジトリのパス> <ターゲット>` を使う。
> 例外は `cd client && npm ...` のように、明示的に `cd` を書いてあるものだけ。

## 目次

```
1. 前提（必要なもの）
2. 初回セットアップ
3. 日々の開発
4. 開発用デモデータ
5. コード生成（sqlc / openapi-typescript）
6. テスト
7. ビルドとバージョン
8. 画面の動作確認
9. つまずいたとき
```

---

# 1. 前提（必要なもの）

| 必要なもの | 最低要件 | 検証した版 |
|---|---|---|
| コンテナランタイム | `docker compose` が使えること | Rancher Desktop（`docker` は `~/.rd/bin/docker`） |
| Go | **1.24 以上**（`Design.md` 3.1） | 1.26.5 |
| Node.js / npm | client のビルドに必要 | v24.14.0 / npm 11.9.0 |
| Google Chrome | 画面の動作確認（8章）。任意 | — |

`goose`（マイグレーション）と `sqlc`（クエリ生成）は**別途インストールしない**。`server/tools/go.mod` の `tool` ディレクティブでバージョンを固定してあり、`make migrate` / `make sqlc` が `go tool` 経由で呼ぶ（`DbDesign.md` 5.1）。

**コンテナランタイムを起動しておくこと。** 停止していると `make up` が
`failed to connect to the docker API` で落ちる。Rancher Desktop なら `open -a "Rancher Desktop"`
のあと `docker info` が通るまで待つ（実測で約30秒）。

---

# 2. 初回セットアップ

## 2.1 秘密ファイルを置く

**秘密は環境変数ではなくファイルで渡す**（`docker inspect` や `ps` から見えないようにするため。`DbDesign.md` 3.2）。`deploy/dev/secrets/` に3つ置く。`.example` だけがコミットされている。

| ファイル | 中身 |
|---|---|
| `db_password` | `pb_owner`（スキーマ所有者。DDLを実行する）のパスワード |
| `app_db_password` | `pb_app`（実行時ロール）のパスワード。initdb の `01_roles.sh` がロール作成時に読む |
| `app_database_url` | `pb_app` での接続文字列。**`app_db_password` と同じ値を埋め込む** |

```
# リポジトリ直下で実行する（cd しない。次の 2.2 も直下で叩く）
for f in db_password app_db_password app_database_url; do
  cp "deploy/dev/secrets/$f.example" "deploy/dev/secrets/$f"
done
# それぞれ中身を実際の値に書き換える
```

**`app_db_password` と `app_database_url` のパスワードは必ず一致させる。** ずれると
`pb_app` でだけ認証に失敗し、DBは起動しているのにアプリが繋がらない状態になる。

`app_database_url` が指すホストは**コンテナ内から見た `db:5432`** である。ホストで直接動かす
ターゲット（`make run` / `make migrate` / `make admin-create` / `make dev-seed`）は、
Makefile が `app_db_password` から `127.0.0.1:5432` 向けの接続文字列を組み立て直すため、
このファイルを書き換える必要はない。

## 2.2 DBを起動してスキーマを作る

```
make up        # docker compose up -d db（現状は db のみ。app は手順13以降）
make migrate   # goose で 0001〜 を適用する。前進のみ（DbDesign.md 5.3）
```

`make up` が起動するのは **db だけ**である。`deploy/Dockerfile` が未作成のため、
app サービスは compose に定義してあっても起動対象から外してある（手順13以降で戻す）。

**`make migrate` は `pb_owner` で接続する**（`db_password` から組み立てる）。実行時ロールの
`pb_app` は DDL を実行できず、それがロール分離の目的である（`DbDesign.md` 3.4）。
また **initdb（ロール作成）が走るのは `pgdata` ボリュームが空の初回起動時だけ**なので、
ロール定義や秘密を変えたら `make dev-reset`（ボリュームごと作り直す）が要る。

## 2.3 ログインできる状態にする

開発端末では**デモデータを入れるのが早い**（4章）。

```
make dev-reset   # DBを作り直して migrate → デモデータ投入（確認を求める）
make dev-info    # URL と4アカウントを表示する
```

デモを使わず管理者を1人だけ作る場合は `make admin-create`（対話。パスワードは2回入力する）。
これは `DbDesign.md` 7.5 の初期管理者作成で、**本番環境で使うのもこの経路**である。

---

# 3. 日々の開発

## 3.1 サーバだけ動かす（画面は embed 済みのものを使う）

```
make up
make run       # :8080。PB_HEALTH_SHOW_VERSION=true で起動する
```

`http://127.0.0.1:8080/` を開くと、**バイナリに埋め込まれた** client が返る。
埋め込みの中身は最後に `make build` した時点のものなので、画面を直したときは
`make build` し直すか、次の 3.2 を使う。

## 3.2 画面を直す（HMR を効かせる）

**2つ立てる。** Vite（:5173）が画面を配信し、`/api` と `/mcp` だけを Go（:8080）へ中継する。

```
make run          # 別の端末で。API は :8080
make dev-client   # :5173。ブラウザで開くのはこちら
```

`/healthcheck` は中継していない（監視用であり画面からは呼ばないため。`ApiDesign.md` 2.11）。

## 3.3 よく使うもの

| コマンド | 用途 |
|---|---|
| `make psql` | DBコンソール（`pb_owner` で接続） |
| `make dev-info` | URL とデモアカウントの一覧。**パスワードを探す時間をなくすためのもの** |
| `make test` | Go のテスト |
| `make down` | コンテナを停止する。**`pgdata` ボリュームは残る**ので、次の `make up` でデータは戻る |

---

# 4. 開発用デモデータ

仕様は `DbDesign.md` 7.6。定義は `deploy/dev/seed/dev-data.yaml`（コミットされている）にあり、
**Go を触らずにユーザーやプロジェクトを増やせる。**

| コマンド | 動作 |
|---|---|
| `make dev-reset` | `docker compose down -v`（**ボリュームごと破棄**）→ 起動 → `migrate` → `dev-seed`。実行前に確認を求める |
| `make dev-seed` | デモデータのみ投入。**冪等**（既存のメール／プロジェクトキーはスキップして件数を報告する） |
| `make dev-info` | URL とアカウント一覧を表示。DBには接続しない |

`make dev-reset` は非対話（スクリプトから呼ぶ場合）では止まる。`PB_YES=1` を渡すと確認を省略する。

## 4.1 デモアカウント

共通パスワードは `dev-data.yaml` の `password`。**権限による画面の出し分けを検証できる組み合わせ**にしてある（`DbDesign.md` 7.6.5）。

| アカウント | システムロール | `demo` での役割 | 確認できること |
|---|---|---|---|
| `admin@example.com` | administrator | — | 「管理」セクションが出る。ユーザー管理・監査ログに入れる |
| `pm@example.com` | operator | project_admin | 管理セクションが出ない。プロジェクト設定が触れる |
| `member@example.com` | operator | project_member | プロジェクト設定が触れない |
| `viewer@example.com` | operator | project_viewer | 閲覧のみ |

## 4.2 本番DBへ流れない仕組み

二重のガードがある（`DbDesign.md` 7.6.3）。いずれかに掛かったら何もせず終了する。

1. 環境変数 `PB_ALLOW_DEV_SEED=1` があること（**`make dev-seed` は自動で与える**）
2. 接続先ホストが `localhost` / `127.0.0.1` / `db` のいずれかであること

加えて `app_user` が50件を超えていたら中断する。**`make` は開発端末専用の入口**なので 1 を自動で
与えているが、配布したバイナリを直接叩く経路では環境変数が無いので止まり、あっても 2 が残る。

---

# 5. コード生成（sqlc / openapi-typescript）

**生成物はコミットする**（`Design.md` 4.6）。生成器が無い環境でもビルドできるようにするため。

| コマンド | 入力 → 出力 |
|---|---|
| `make sqlc` | `server/internal/store/queries/*.sql`（＋スキーマ源の `server/migrations/`）→ `server/internal/store/gen/` |
| `make gen-api` | `docs/openapi.yaml` → `client/src/api/schema.d.ts` |

**`make gen-api` が生成するのは型だけである。** API の呼び出しは手書きの薄いラッパ
（`client/src/api/client.ts`）が持つ。CSRF ヘッダ・Cookie の送出・`ApiDesign.md` 2.5 の
エラー形式といった共通規約を1か所に集めるためで、生成器にハンドラや呼び出しを作らせない
方針は `Design.md` 3.3 にある。

**`docs/openapi.yaml` は設計の写しではなく「実装済みAPIの現状」である**（`ApiDesign.md` 1.3）。
APIを足したステップの成果物に、この yaml の更新と `make gen-api` の結果を含める。

---

# 6. テスト

```
make test      # cd server && go test ./...
```

## 6.1 DBを使う結合テスト

**`PB_TEST_DATABASE_URL` が設定されているときだけ走る。** 未設定ならスキップするので、
DBが無くても `make test` は通る。

```
cd server
PB_TEST_DATABASE_URL='postgres://pb_app:<password>@127.0.0.1:5432/pb?sslmode=disable' \
  go test ./internal/httpapi/ -run Integration -v
```

フェイクで差し替えたテストでは `queries/*.sql` が一度も実行されないため、
列名・JOIN の向き・条件の取りこぼしが検出できない。それを埋めるためのものである。

## 6.2 openapi.yaml のドリフト検出

`make test` に含まれる（`server/internal/httpapi/openapi_drift_test.go`）。
`chi.Walk` で得た実装のルート一覧と `docs/openapi.yaml` の `paths` を突き合わせ、
**実装にあって yaml に無い／yaml にあって実装に無い**の両方向を報告する。

エンドポイントを足して yaml を忘れると、ここで落ちる。

---

# 7. ビルドとバージョン

## 7.1 単一バイナリを作る

```
make build        # client をビルド → embed 対象へコピー → bin/pb
make clean-webui  # ← コミット前に必ず実行する
```

**`make build` は追跡対象の `server/internal/webui/dist/index.html` を実成果物で上書きする。**
`//go:embed` の対象ディレクトリが空だとコンパイルが通らないため、プレースホルダを1つ
コミットしてある構造上、必ず起きる（`Design.md` 3.4）。`make clean-webui` は
**コミット済みの内容**へ戻すので、プレースホルダ自体を書き換えたときは先に `git add` すること。

## 7.2 バージョン

正本は `VERSION`。ビルド番号は **`develop` へのマージ回数**と一致する（`Design.md` 11.1）。

```
make version         # 現在の値と、git 実測のマージ回数を表示
make version-check   # 両者が一致するか検証する（マージ後に通ること）
make bump-minor      # feature/* をマージする前に。マイナーとビルドを +1
make bump-build      # fix/* docs/* をマージする前に。ビルドのみ +1
```

**`bump-*` は feature ブランチ上で実行し、`VERSION` の更新を同じブランチに含める。**
`develop` 上で直接コミットしないため（`Design.md` 11.0）。

---

# 8. 画面の動作確認

Playwright / Puppeteer は入れていない（`Design.md` 3.1 の採用技術表にない）。
**ヘッドレス Chrome を直接叩く。**

## 8.1 描画だけ見る

```
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --disable-gpu \
  --virtual-time-budget=3000 --user-data-dir=<一時ディレクトリ> --dump-dom http://127.0.0.1:8080/
```

- `--user-data-dir` は**呼び出しごとに別のディレクトリにする**。使い回すとプロファイルのロックで2回目以降が固まる
- `--virtual-time-budget` は SPA のマウントを待つため

## 8.2 ログインやクリックを伴う確認

`--dump-dom` では足りない。`--headless=new --remote-debugging-port=9222` で起動し、
**CDP（Chrome DevTools Protocol）を WebSocket で叩く使い捨てスクリプト**を書く
（手順8では Python の標準ライブラリだけで書いた。`Runtime.evaluate` / `Page.navigate` /
`Page.captureScreenshot` / `Emulation.setDeviceMetricsOverride`）。

**注意点が2つある。**

- **ウィンドウ幅を必ず指定する**（`--window-size=1440,900`）。既定のままでは 768px 未満と判定され、
  メニューがオーバーレイになる（`GuiDesign.md` 2.4）。「メニューが出ない」と誤読しやすい
- `v-model` の入力欄に値を入れるときは、`el.value = v` ではなく**ネイティブの value セッターを
  呼んでから `input` イベントを発火**する。前者では Vue が変更に気づかない

```js
const set = (el, v) => {
  const proto = Object.getPrototypeOf(el)
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
```

## 8.3 Cookie 認証で状態変更系を叩く（curl）

`X-PB-CSRF` が要る（`ApiDesign.md` 2.4）。値は `pb_csrf` Cookie と同じ。

```
curl -s -c cj.txt -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"<password>"}'
CSRF=$(grep pb_csrf cj.txt | awk '{print $7}')
curl -s -b cj.txt -X POST -H "X-PB-CSRF: ${CSRF}" http://127.0.0.1:8080/api/v1/auth/logout -i
```

**検証が終わったら Cookie ファイルを消すこと。** セッショントークンの平文が残っている。

## 8.4 検証のあとしまつ

**次のセッションの検証を狂わせるものを残さない。** DBに書き込む検証をしたら、以下を戻す。

| 残るもの | 影響 | 戻し方 |
|---|---|---|
| ログイン失敗（`local_credential`） | 5回で15分ロックされ、以後ログインできない | `UPDATE local_credential SET failed_attempts = 0, locked_until = NULL;` |
| `audit_log` の `login.failure` など | 件数を数える検証がずれる。`actor_id` が NULL の行は未登録メールでの失敗で、`detail->>'email'` で特定できる | 該当行を削除する |
| 検証用に作ったユーザー・プロジェクト | 一覧や件数に混ざる | 削除する。作り直すなら `make dev-reset` が早い |
| 起動したプロセス（`make run` / `make dev-client`） | ポートを掴んだままになる | 停止する（9章） |
| Cookie jar・ヘッドレス Chrome のプロファイル | セッショントークンの平文が残る | 削除する |

DBを丸ごと作り直してよい場面では、**個別に戻すより `make dev-reset` のほうが確実**である。

---

# 9. つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| `make: *** No rule to make target 'migrate'` / `no makefile found` | **リポジトリ直下以外で実行している。** `make` は親ディレクトリを探しに行かない。`pwd` を確認して直下へ戻るか、`make -C <リポジトリのパス> migrate` と書く |
| `make up` が `failed to connect to the docker API` | コンテナランタイムが停止している。Rancher Desktop を起動し、`docker info` が通るまで待つ（約30秒） |
| `make run` が `bind: address already in use` | 前のセッションの `pb` が :8080 を掴んでいる。`lsof -nP -iTCP:8080 -sTCP:LISTEN` で確認する。他人のプロセスを落とさずに済ませるなら `PB_BIND=127.0.0.1:8099` のように待受を変えて起動する |
| `make migrate` が認証に失敗する | `deploy/dev/secrets/db_password` と DB の実際のパスワードがずれている。initdb は**初回起動時にしか走らない**ため、後から `.example` を書き換えても反映されない。`make dev-reset` で作り直す |
| アプリだけDBに繋がらない | `app_db_password` と `app_database_url` のパスワードが不一致（2.1） |
| `npm run build` が `ERR_PACKAGE_PATH_NOT_EXPORTED` | `typescript` が 7.x になっている。**`^5` に固定すること**（vue-tsc 3.3.9 が `typescript/lib/tsc` を require できない） |
| コミットしたら `server/internal/webui/dist/index.html` が汚れていた | `make build` の後に `make clean-webui` を忘れている（7.1） |
| 画面が「メニューが出ない」ように見える | ヘッドレスのウィンドウ幅が 768px 未満（8.2）。または未認証（ログイン画面はメニューを出さない。`GuiDesign.md` 5.1） |
| `make dev-reset` が「中止しました」で終わる | 非対話で実行している。`PB_YES=1` を付ける（4章） |
| Vite が :5173 以外で起動しない | `strictPort` にしてある。**ポートが空いていなければ黙ってずらさずに失敗する**（Cookie の送り先が変わるのを防ぐため） |

---

## 関連文書

| 知りたいこと | 文書 |
|---|---|
| なぜその構成なのか（技術選定・ディレクトリ・配信方式） | `Design.md` 3〜4章 |
| DBの実行環境・スキーマ・初期データ・デモデータの仕様 | `DbDesign.md` 3章・5〜7章 |
| API の規約（エラー形式・CSRF・レート制限） | `ApiDesign.md` 2章 |
| 画面の構造・配色・ルーティング | `GuiDesign.md` |
| どこまで実装したか・過去の判断 | `docs/PROGRESS.md` |
