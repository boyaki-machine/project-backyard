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
1. 前提（必要なもの）        ← 確認コマンド。足りなければ付録A へ
2. 初回セットアップ
3. 日々の開発                ← 起動と止め方、make build が要る場面、終わり方（3.4）
4. 開発用デモデータ
5. コード生成（sqlc / openapi-typescript）
6. テスト
7. ビルドとバージョン
8. 画面の動作確認
9. つまずいたとき            ← 症状から引く
10. 依存とツールのバージョン  ← 固定しているものと、その理由
付録A. 環境の構築            ← 端末に一度だけ入れるもの
```

---

# 1. 前提（必要なもの）

**まず4つを確認する。** どれかが無ければ**付録A**へ。

| 必要なもの | 確認コマンド | 期待する結果 | 無いとき |
|---|---|---|---|
| コンテナランタイム | `docker info` | エラーにならない（起動している） | 付録 A.1 |
| Go | `go version` | **`go1.24` 以上**（`Design.md` 3.1） | 付録 A.2 |
| Node.js / npm | `node -v && npm -v` | 表示される（client のビルドに必要） | 付録 A.3 |
| Google Chrome | `ls "/Applications/Google Chrome.app"` | 存在する（**画面の動作確認（8章）に使うだけ。任意**） | 付録 A.4 |

まとめて確認するなら:

```
docker info > /dev/null 2>&1 && echo "docker: ok" || echo "docker: NG（付録 A.1）"
go version
node -v && npm -v
```

**検証した組み合わせ**（この版で動くことを確認済み。より新しい版でも動く可能性は高い）

| | 版 | 導入方法 |
|---|---|---|
| コンテナランタイム | Rancher Desktop（`docker` は `~/.rd/bin/docker`） | アプリとして導入 |
| Go | 1.26.5 | Homebrew |
| Node / npm | v24.14.0 / 11.9.0 | nvm |

`goose`（マイグレーション）と `sqlc`（クエリ生成）は**別途インストールしない**。`server/tools/go.mod` の `tool` ディレクティブでバージョンを固定してあり、`make migrate` / `make sqlc` が `go tool` 経由で呼ぶ（`DbDesign.md` 5.1）。同様に、client の依存は `make build-client` が `npm ci` で入れるので、手で `npm install` する必要はない。

**コンテナランタイムは起動しておくこと。** 停止していると `make up` が
`failed to connect to the docker API` で落ちる（付録 A.1）。

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
make up        # docker compose up -d db（現状は db のみ。app は手順12b以降）
make migrate   # goose で 0001〜 を適用する。前進のみ（DbDesign.md 5.3）
```

`make up` が起動するのは **db だけ**である。`deploy/Dockerfile` が未作成のため、
app サービスは compose に定義してあっても起動対象から外してある（手順12b以降で戻す）。

**`make migrate` は `pb_owner` で接続する**（`db_password` から組み立てる）。実行時ロールの
`pb_app` は DDL を実行できず、それがロール分離の目的である（`DbDesign.md` 3.4）。
また **initdb（ロール作成）が走るのは `pgdata` ボリュームが空の初回起動時だけ**なので、
ロール定義や秘密を変えたら `make dev-reset`（ボリュームごと作り直す）が要る。

**`deploy/base/initdb/01_roles.sh` は実行ビットを立てておくこと。** `:ro` でマウントしても
ホスト側のファイルモードがそのまま使われるため、ビットが落ちていると initdb が
このスクリプトを実行せず、`pb_app` ロールが作られないまま起動してしまう
（`ls -l deploy/base/initdb/` で確認できる）。

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

## 3.1 1プロセスで動かす（画面は embed 済みのものを使う）

```
make up
make build     # client を作り直してバイナリへ埋め込む（画面を直したときは必須）
make run       # :8080。PB_HEALTH_SHOW_VERSION=true で起動する
```

`http://127.0.0.1:8080/` を開くと、**バイナリに埋め込まれた** client が返る。

**`make build` が要るのは client を直したときだけである。**

| 直した対象 | 必要な操作 |
|---|---|
| **client**（`client/` 配下） | `make build` → `make run` を起動し直す。埋め込みの中身は最後に `make build` した時点のもので、`make run` では更新されない |
| **server**（`server/` 配下） | `make run` を起動し直すだけでよい。`make run` は `go run` なので**毎回ソースからコンパイルし直す** |

迷ったら **`make restart`**（下記）で確実に最新になる。画面を続けて直すなら、毎回ビルドを
待たずに済む 3.2 のほうが速い。

**`make build` の後は `make clean-webui`（7.1）を忘れないこと。** ビルド成果物が
追跡対象のファイルを上書きしたままコミットしてしまう。

### 止め方

**`Ctrl + C`（`make run` を実行した端末で）。** これが正しい止め方である。
`SIGINT` / `SIGTERM` を受けると**処理中のリクエストの完了を待ってから**終了する
（graceful shutdown。猶予15秒）。ログに次の2行が出れば正常に落ちている。

```
{"level":"INFO","msg":"停止信号を受け取った。処理中のリクエストの完了を待つ"}
{"level":"INFO","msg":"サーバを停止した"}
```

そのあと `make` が `Interrupt` や `Error 1` を表示することがあるが、**上の2行が出ていれば
異常ではない**。`go run` が「シグナルで終了した子プロセス」を失敗として扱うためである。

止まったかどうかは待受ポートで確かめる。**`make run` は `go run` → 実バイナリの親子構成**
なので、親だけを殺すと子が :8080 を掴んだまま残ることがある。

```
lsof -nP -iTCP:8080 -sTCP:LISTEN     # 何も出なければ停止できている
```

残っていたら、その PID を `kill <PID>`（`-9` は不要。graceful に落ちる）。
**`make stop-server` が同じことをする**（`lsof` で PID を引いて `kill` する）。

### 作り直して起動し直す（`make restart`）

**画面を直したら `make restart` を使う。** 停止 → ビルド → DB起動 → サーバ起動を1コマンドで行う。

```
make restart
```

```
stop-server → down → build → up → run
```

**`make run` を止めて起動し直すだけでは、client の変更は反映されない。** 画面は
Go バイナリに埋め込まれており（`Design.md` 3.4）、`make build` を通さない限り
古いものが出続ける。`make restart` はこれを手順として含んでいる。

先頭の `stop-server` が要るのは、**`make run` のサーバがコンテナではない**ためである。
`make down` はコンテナしか落とさないので、ポートを掴んだままのプロセスが残っていると
`run` が `address already in use` で即座に終わる。

## 3.2 画面を直す（HMR を効かせる）

> **HMR**（Hot Module Replacement）とは、**ソースを保存した瞬間に、変更した部分だけを
> 動いているページに差し替える**仕組み。ページ全体を再読み込みしないので、
> **入力途中の値や開いているメニューといった画面の状態が保たれたまま**見た目が変わる。
> Vite の機能で、`make build` を待つ必要がなくなる（保存から反映までおよそ1秒未満）。

**2つ立てる。** Vite（:5173）が画面を配信し、`/api` と `/mcp` だけを Go（:8080）へ中継する。

```
make run          # 別の端末で。API は :8080
make dev-client   # :5173。ブラウザで開くのはこちら
```

**ブラウザで開くのは :5173 のほう。** :8080 を開くと embed 済みの古い画面が出る（3.1）。
この構成では **client の変更に `make build` は要らない**。server を直したときだけ
`make run` を起動し直す。

`make dev-client` の止め方も `Ctrl + C`。`/healthcheck` は中継していない（監視用であり
画面からは呼ばないため。`ApiDesign.md` 2.11）。

## 3.3 よく使うもの

| コマンド | 用途 |
|---|---|
| `make psql` | DBコンソール（`pb_owner` で接続） |
| `make dev-info` | URL とデモアカウントの一覧。**パスワードを探す時間をなくすためのもの** |
| `make test` | Go のテスト |
| **`make restart`** | **停止 → ビルド → DB起動 → サーバ起動をまとめて行う**（3.1）。画面を直したあとはこれ1つでよい |
| `make stop-server` | :8080 を掴んでいるサーバを PID で止める（3.1「止め方」） |
| `make down` | コンテナを停止する。**`pgdata` ボリュームは残る**ので、次の `make up` でデータは戻る（3.4） |

## 3.4 作業を終える／再開する（端末のリソースを解放する）

**開発端末を他の用途にも使うなら、作業の終わりに解放しておく。** PB が端末に残すものは
次の3つで、上から順に止める。

| 動いているもの | 止め方 | 残るもの |
|---|---|---|
| `make run` のサーバ（:8080） | `Ctrl + C`（3.1「止め方」） | 何も残らない |
| `make dev-client` の Vite（:5173） | `Ctrl + C` | 何も残らない |
| DB コンテナ | **`make down`** | **`pgdata` ボリューム（＝データ）は残る** |

```
make down     # docker compose down。コンテナとネットワークを破棄する
```

**再開はこれだけでよい。**

```
make up       # 同じデータで戻る。migrate も dev-seed も要らない
```

`make down` は **`-v` を付けない**ので、ボリューム `project-backyard_pgdata` は消えない。
デモデータもログイン済みのアカウントもそのまま戻る（実測：`make down` → `make up` の後も
ユーザー4件・プロジェクト1件が残っている）。

**データごと捨てたいときだけ `make dev-reset`** を使う（`down -v` を含む。4章）。
`make down` と `make dev-reset` の違いはここだけである。

### どこまで解放されるか

| 操作 | 解放されるもの | 実測の目安 |
|---|---|---|
| `make down` | PB の DB コンテナ | 約 36 MiB |
| コンテナランタイム自体の終了（Rancher Desktop を終了する等） | ランタイムの常駐プロセスと VM | 数百 MB 規模 |

**`make down` だけではランタイムの VM は動いたままである。** Rancher Desktop のように
Kubernetes を同梱する製品では、PB と無関係なコンテナ（coredns / traefik など）も
動き続ける。**端末のメモリをしっかり空けたいなら、ランタイムのアプリごと終了する。**
次に開発するときは、A.1 のとおり起動して `docker info` が通るのを待ってから `make up` する。

数値は実測の一例で、環境によって変わる。自分の環境で見るには:

```
docker stats --no-stream        # コンテナごとの使用量
docker compose -f deploy/base/compose.yaml ps    # PB のコンテナが動いているか
lsof -nP -iTCP:8080 -sTCP:LISTEN                 # サーバが残っていないか
```

---

# 4. 開発用デモデータ

仕様は `DbDesign.md` 7.6。定義は `deploy/dev/seed/dev-data.yaml`（コミットされている）にあり、
**Go を触らずにユーザーやプロジェクトを増やせる。**

| コマンド | 動作 |
|---|---|
| `make dev-reset` | `docker compose down -v`（**ボリュームごと破棄**）→ 起動 → `migrate` → `dev-seed`。実行前に確認を求める。**日々の停止は `make down`**（データが残る。3.4） |
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
make up          # DB が動いていること
make test-db     # 結合テスト（-run Integration）をすべて走らせる
```

**接続文字列を手で書かない。** `make test-db` が `deploy/dev/secrets/app_db_password` から
recipe 内で組み立てる（`make run` と同じ作法）。秘密は argv にも Makefile にも残らない。

1つに絞りたいときは `RUN=` を渡す。

```
make test-db RUN=TestMeTokensIntegration
```

フェイクで差し替えたテストでは `queries/*.sql` が一度も実行されないため、
列名・JOIN の向き・条件の取りこぼしが検出できない。それを埋めるためのものである。

## 6.2 テストを書くときの落とし穴

実際に踏んだものだけを挙げる。

| 落とし穴 | 対処 |
|---|---|
| **`t.Cleanup` は `defer` より後に走る。** 結合テストで `defer pool.Close()` と `t.Cleanup(削除)` を併用すると、後片付けの時点でプールが閉じていて `closed pool` になる | プールの close も `t.Cleanup` で登録し、LIFO の順序を使う |
| **可変長引数を渡さないと `nil` スライスになる**（`[]string{}` ではない）。実効権限のキャッシュは `nil`（キャッシュ不在）と長さ0（権限0件）を区別するため、ヘルパで `f()` と書くと意図せず「不在」になる | `append([]string{}, xs...)` のように空スライスを明示する |
| **プロジェクトキーには CHECK 制約がある**（`DbDesign.md` 6.4）。`^[a-z0-9][a-z0-9-]{1,19}$` で**2〜20文字**。ULID をそのまま使うと長さ超過で INSERT が落ちる | ULID の末尾6〜8文字を小文字化して使う |
| **レート制限のカウンタはプロセス内メモリにある。** `make run` を再起動すると消える | 429 を再現する検証は**サーバを起動したまま**続けて叩く。ログインは IPあたり 10回/分 |
| **ログインは IP あたり 10回/分**（`ApiDesign.md` 2.9）。`httptest.NewRequest` は固定のアドレスを入れるため、1つの結合テストがログインを11回すると自分で 429 を踏む | ログインは `loginAs` / `loginAsWith` を通す。**呼び出しごとに違う擬似 IP** を入れてある（`nextTestClientIP`）。生の `call(..., "/api/v1/auth/login", ...)` を書かない |
| **状態を変える検証スクリプトは、途中で落ちると副作用だけが残る** | 現在値（`version` など）は毎回読み直し、本文とステータスを同じ出力へ混ぜない。まず1件だけ通してから全体を回す |
| **パスワード入力のエコー抑止には競合窓がある。** プロンプトを出してから `term.ReadPassword` が echo を切るまでの数マイクロ秒に文字が届くと、その分だけ端末に表示される（`sudo` や `ssh` も同じ） | 端末ありの検証は `expect` に `sleep 0.4` を入れる。`printf ... \| script -q /dev/null` は stdin を即座に閉じるため `EOF` になり使えない |

結合テストの接続は **`pb_app`（DML のみ）** で行う。実運用と同じ権限で通ることを確かめるためで、
DDL が要るなら `pb_owner` を使うのではなくマイグレーションを足す（`DbDesign.md` 3.4）。

## 6.3 openapi.yaml のドリフト検出

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

**`make build` は作業ツリーを汚さない。** 生成物（`index.html` と `assets/`）は
`.gitignore` の対象で、追跡しているのは `server/internal/webui/dist/placeholder.html`
だけである。`//go:embed` の対象ディレクトリが空だとコンパイルが通らないため、
**実ビルドが出力しない名前**のファイルを1つ置いてある（`Design.md` 3.4）。

`make clean-webui` はビルド成果物を消してディスクを空けるためのもので、
**コミット前の必須手順ではない**。

**client を未ビルドのまま `make run` すると、画面は `503` でプレースホルダを返す**
（「画面がまだビルドされていません」）。`/healthcheck` と `/api` は影響を受けない。

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
window.set = window.set || function (el, v) {
  const proto = Object.getPrototypeOf(el)
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
```

**`const` で宣言しない。** 同じページで2回以上 `Runtime.evaluate` に流すと
「Identifier has already been declared」で落ちる（ページを移動するまで同じスコープが続く）。
`window.<名前> = window.<名前> || …` にしておけば何度流してもよい。

- **`--remote-debugging-port` は Chrome のインスタンスごとに別の番号にする。**
  固定にすると2つ目の Chrome が bind に失敗し、**1つ目の DevTools につながる**。
  同じプロファイルの Cookie を書き換えるため、管理者のセッションで検証していたつもりが
  別の利用者のセッションに変わる（手順13b で実際に起きた）。空きポートは
  `socket.bind(('127.0.0.1', 0))` で取る
- **`Runtime.evaluate` から返すのは値だけにする**（真偽・数・文字列）。DOM 要素は `{}` に
  直列化され、Python 側では偽になる
- **`Page.captureScreenshot` の `captureBeyondViewport: true` では `position: fixed` の要素が写らない。**
  文書全体を1枚に収めるモードなので、画面に固定した要素（`<Teleport>` で body へ出した
  ドロップダウンやメニュー）が抜ける。**開いたパネルを撮るときは `false` にする**
  （手順16d-b で、パネルが出ているのにスクリーンショットに無いという形で踏んだ）
- **ドラッグ&ドロップは `DragEvent` を自分で発火して確かめられる**（`dragstart` → `dragover` → `drop`）。
  ただし**「落とせるか」は `drop` を投げて判定しない**——合成イベントには「既定を止めた要素だけが
  ドロップ先になる」という規約が効かず、落とせないはずの相手でもハンドラが動く。
  **`dragover` を投げて `defaultPrevented` を見る**のが、実際のブラウザと同じ判定である

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
| 画面に「画面がまだビルドされていません」と出る（503） | **client が未ビルド。** `make build` するか、:5173（`make dev-client`）で見る（3.1 / 3.2） |
| 画面が「メニューが出ない」ように見える | ヘッドレスのウィンドウ幅が 768px 未満（8.2）。または未認証（ログイン画面はメニューを出さない。`GuiDesign.md` 5.1） |
| `make dev-reset` が「中止しました」で終わる | 非対話で実行している。`PB_YES=1` を付ける（4章） |
| Vite が :5173 以外で起動しない | `strictPort` にしてある。**ポートが空いていなければ黙ってずらさずに失敗する**（Cookie の送り先が変わるのを防ぐため） |
| `Ctrl + C` の後に `make: *** [run] Error 1` が出る | **異常ではない**（3.1 の「止め方」）。`go run` がシグナル終了を失敗として扱うため。`停止信号を受け取った` → `サーバを停止した` の2行が出ていれば正常 |
| 画面を直したのに反映されない | :8080 は embed 済みの画面を返す。`make build` し直すか、:5173（`make dev-client`）で見る（3.1 / 3.2） |
| `go version` が 1.24 未満 | 付録 A.2。goose / sqlc も Go 経由で動くため、ここが古いとマイグレーションから先に進めない |
| `make down` したらデータも消えたのでは、と不安になる | 消えていない。`-v` を付けていないのでボリュームは残る（3.4）。`docker volume ls \| grep backyard` に `project-backyard_pgdata` があれば無事 |
| 端末が重い。PB を止めたのにメモリが空かない | `make down` はコンテナだけ。**コンテナランタイムの VM は動いたまま**（3.4）。アプリごと終了する |

---

# 10. 依存とツールのバージョン

**ここに挙げたものは意図して固定してある。** 上げると `Design.md` 3.1 の
「Go 1.24 以上」と衝突する、あるいはビルドが壊れる。**上げる場合は 3.1 の
最低バージョンとセットで見直すこと。**

## 10.1 固定しているもの

| 対象 | 版 | 上げない理由 |
|---|---|---|
| goose | **v3.26.0**（`server/tools/go.mod`） | v3.27.3 以降は `go 1.25.7` を要求する |
| sqlc | **v1.30.0**（同上） | v1.31.1 は `go 1.26.0` を要求する（v1.30.0 自体は `go 1.23.0` 要求） |
| `golang.org/x/term` / `x/sys` | `v0.33.0` 系 | 最新版は go 1.25 を要求し、`go get` が go ディレクティブを勝手に `1.25.0` へ引き上げる |
| `typescript`（client） | **`^5`** | vue-tsc 3.3.9 が TS 7 の `typescript/lib/tsc` を require できない（症状は9章） |

**`go get` の後は `head -3 server/go.mod` と `head -3 server/tools/go.mod` を見て、
go ディレクティブが `1.24` のままか確認する。**

## 10.2 ツールは server/tools/go.mod に隔離してある

`make migrate` / `make sqlc` は `cd server/tools` してから `go tool` を呼ぶ。
**`server/go.mod` にツールを足さないこと**（indirect が80件超に膨らみ、
go ディレクティブも 1.25 へ上がる）。

**`go get -tool` は実行順で結果が変わる。** `tools/go.mod` に sqlc → goose の順で
入れると go ディレクティブが 1.24 のまま保たれるが、goose → sqlc の順だと `x/*` が
最新へ上がって `1.25.0` に書き換えられる。**ツールを足したら必ず
`head -3 server/tools/go.mod` を見る。**

## 10.3 sqlc の型の写し方

`server/sqlc.yaml` の `overrides` にある。

- `citext` は sqlc が既定の対応を持たないため **`string`** に写している
- `inet` は **`*netip.Addr`**
- NULL 許容列は **`pgtype.*`**

## 10.4 実行時の依存

**Go（`server/go.mod` の直接依存）**：`jackc/pgx/v5` / `oklog/ulid/v2` /
`alexedwards/argon2id` / `golang.org/x/term` / `go-chi/chi/v5` の5つ。
トークンのハッシュと乱数は標準ライブラリ（`crypto/sha256` / `crypto/rand`）で足りる。

**client（`client/package.json`）**：`vue` / `vue-router` / `pinia` の3つ。
dev に `vite` / `@vitejs/plugin-vue` / `typescript` / `vue-tsc` / `openapi-typescript`。
**`openapi-typescript` は型生成のみで実行時には入らない。**
`npm run build` は型検査（`vue-tsc --noEmit`）を通してから `vite build` する
（型エラーはビルドを止める）。`make build-client` は `npm ci` を使うため
`client/package-lock.json` をコミットしている。

**依存を足す・置き換えるのはユーザーの承認が要る**（`CLAUDE.md` 絶対規則2）。

---

# 付録A. 環境の構築

1章の確認で足りなかったものを入れる。**PB のリポジトリ側の設定ではなく、端末に一度だけ入れるもの。**

## A.1 コンテナランタイム

DB（PostgreSQL）を compose で動かすために要る（`DbDesign.md` 3.2）。**`docker compose` が使えれば
実装は問わない**（Rancher Desktop / Docker Desktop / colima など）。検証は Rancher Desktop で行っている。

```
docker info      # エラーにならなければ導入・起動できている
```

- **入っているのに落ちる場合は、起動していないだけのことが多い。** Rancher Desktop なら
  `open -a "Rancher Desktop"` のあと `docker info` が通るまで待つ（実測で約30秒）
- 入っていない場合は各製品の配布ページから導入する。導入後、`docker compose version` も確認する
  （**Compose V2 が要る**。`docker-compose`（V1、ハイフンあり）ではない）

## A.2 Go

**1.24 以上**（`Design.md` 3.1）。サーバ本体に加えて、`goose`（マイグレーション）と
`sqlc`（コード生成）も `go tool` 経由で動くため、これが無いと 2.2 から先へ進めない。

```
go version       # go1.24 以上であること
```

macOS なら Homebrew（検証環境もこれ。`brew install go`）。公式配布の pkg でもよい。

**上げるときは注意する。** ライブラリの都合で go ディレクティブが勝手に上がる問題を避けるため、
`go.mod` は `1.24` に固定してある（10.1）。Go 本体を新しくするのは構わないが、
`go get` の後は `head -3 server/go.mod` で `1.24` のままか確認すること。

## A.3 Node.js / npm

client のビルド（`make build` / `make dev-client`）に要る。**サーバだけ触るなら無くても
`make run` は動く**（embed 済みの画面が返るため）。

```
node -v && npm -v
```

検証環境は nvm で入れた v24.14.0 / npm 11.9.0。公式インストーラでも Homebrew でもよい。
**client の依存を手で入れる必要はない**（`make build-client` が `npm ci` で入れる）。

## A.4 Google Chrome

**任意。** 8章のヘッドレスでの画面確認に使うだけで、開発そのものには要らない。
普段使いのブラウザで画面を見るぶんには何でもよい。

```
ls "/Applications/Google Chrome.app"
```

## A.5 このリポジトリ側の準備

端末側が揃ったら 2章へ戻る（秘密ファイルの配置 → `make up` → `make migrate` → `make dev-reset`）。
**リポジトリ側で追加インストールするものは無い。**

---

## 関連文書

| 知りたいこと | 文書 |
|---|---|
| なぜその構成なのか（技術選定・ディレクトリ・配信方式） | `Design.md` 3〜4章 |
| DBの実行環境・スキーマ・初期データ・デモデータの仕様 | `DbDesign.md` 3章・5〜7章 |
| API の規約（エラー形式・CSRF・レート制限） | `ApiDesign.md` 2章 |
| 画面の構造・配色・ルーティング | `GuiDesign.md` |
| どこまで実装したか・次の手順への引き継ぎ・環境メモ | `docs/PROGRESS.md` |
| 過去の判断の経緯 | `docs/history/decisions.md` |
| どの手順で何を作ったか・当時の検証内容 | `docs/history/steps.md` |
