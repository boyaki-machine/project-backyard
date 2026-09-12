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
11. ドッグフーディング用インスタンス（stg）  ← PB 自身を PB で管理する器
12. エージェントを MCP でつなぐ  ← /pb-onboard を通すまで、手で叩く手順、起票（12.5）
13. 設定を変える              ← 3層の変え方、設定ファイル、設定を1件足す（13.4）
14. HTTPS で公開する（TLS）   ← 3ステップで始める、openssl で自己署名（14.2）、認証局発行（14.3）
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
make up        # docker compose up -d db（db のみ。app は Phase 2 では起動しない）
make migrate   # goose で 0001〜 を適用する。前進のみ（DbDesign.md 5.3）
```

`make up` が起動するのは **db だけ**である。`deploy/Dockerfile` が未作成のため、
app サービスは compose に定義してあっても起動対象から外してある。**`Design.md` 4.4 により
Phase 2 では Dockerfile を作らない**ので、この状態は当面続く（PB 本体は `make run` か
`make build` の単一バイナリで動かす）。

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
| `make stg-*` | ドッグフーディング用インスタンス（11章）。`dev` とは別の器で、`make dev-reset` の影響を受けない |

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

**stg を立てているなら、そちらは止めない**（11章）。stg の DB は別の compose プロジェクト
（`pb-stg`）で `restart: always` なので、`make down` でも `make dev-reset` でも落ちない。
容量を空けるために畳むときだけ `make stg-down` を使う。

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

**`dev-data.yaml` に書ける列は `server/cmd/pb/dev_seed.go` の構造体が決める。** `yaml.Decoder` を
`KnownFields(true)` で回しているので、**構造体に無いキーを書くと seed が丸ごと落ちる。**
たとえばチケットの `estimate_hours` / `actual_hours` は `devTicket` に列が無いため書けない
（画面からは編集できるが、seed の初期値は `—` になる）。増やすときは Go 側の構造体を先に直す。

## 4.1 デモアカウント

共通パスワードは `dev-data.yaml` の `password`。**権限による画面の出し分けを検証できる組み合わせ**にしてある（`DbDesign.md` 7.6.5）。

| アカウント | システムロール | `demo` での役割 | 確認できること |
|---|---|---|---|
| `admin@example.com` | administrator | — | 「管理」セクションが出る。ユーザー管理・監査ログに入れる |
| `pm@example.com` | operator | project_admin | 管理セクションが出ない。プロジェクト設定が触れる |
| `member@example.com` | operator | project_member | プロジェクト設定が触れない |
| `viewer@example.com` | operator | project_viewer | 閲覧のみ |

**`admin@example.com` は `demo` のメンバーではない。** `pb dev seed` は**プロジェクトの作成者をメンバーとして登録しない**（`server/cmd/pb/dev_seed.go`）。登録するのは `POST /projects`（画面から作ったとき）だけで、そちらは作成者が `project_admin` になる（`ApiDesign.md` 5.2）。**この非対称を忘れると「管理者だが非メンバー」の前提を取り違える**——手順24b の検証で実際に4件が偽の FAIL になった。**`make dev-info` は定義ファイルを読むので、DB を引いた結果とは別物である。**

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

### 遅延読み込みが効いているかはビルド出力で見る

**`defineAsyncComponent` は、その部品を*すべての*利用者が動的に取り込んで初めて効く。**
1つでも `import X from './X.vue'` が残っていると、チャンクは分かれない。

```
[INEFFECTIVE_DYNAMIC_IMPORT] src/components/MarkdownEditor.vue is dynamically imported by
… but also statically imported by src/components/TicketComments.vue…
```

**この警告が利用者を名指しする**ので、`grep` で数えるより速い。手順22b では
`MarkdownEditor` の利用者を2つと数えて実際は3つあり、**警告で3つ目に気づいた**。
効いていれば出力にチャンクが増える（`dist/assets/MarkdownEditor-*.js`）。

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

**`websocket-client` はこの端末に入っていない**（2026-09-06 に実測）。**WebSocket は
標準ライブラリだけで書く**——CDP のフレームはテキスト1種類しか来ないので、
**クライアント側のマスク（必須）・断片化・ping/pong の読み飛ばし**の3つを扱えば足りる。

**注意点が2つある。**

- **ウィンドウ幅を必ず指定する**（`--window-size=1440,900`）。既定のままでは 768px 未満と判定され、
  メニューがオーバーレイになる（`GuiDesign.md` 2.4）。「メニューが出ない」と誤読しやすい
- **セレクタは DOM を1回出してから書く**（手順26c で2回外した）。ログイン画面の入力欄は
  `id` を持たず `name` だけを持ち、`form button` は**パスワードの表示切替（`👁`）を先に拾う**
  ——`document.querySelectorAll('input')` と `button[type=submit]` が確実である。
  **外したときの症状は「ログインできない」ではなく「ログイン画面のまま先へ進む」ことである**。
  後続の検証が全部 FAIL になるため、原因が遠くに見える
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
- **フォーカスの出入りで保存する画面は `Page.bringToFront` を先に呼ぶ。**
  ヘッドレスの窓は既定でフォーカスを持たず（`document.hasFocus()` が `false`）、
  その状態では `el.blur()` が **blur イベントを発火しない**。
  `GuiDesign.md` 5.5 のインライン編集（「別の領域をクリックしてフォーカスを外す」＝保存）は
  これに当たり、呼ばないと**実装は正しいのに「保存されない」という FAIL**になる
  （手順17b で実際に踏んだ）。`activeElement` は正しく入るので、そちらだけ見ても気づけない
- **値を入れる `Runtime.evaluate` と、押す `Runtime.evaluate` を分ける。**
  Vue は `:disabled` の DOM 反映を **nextTick で行う**ので、**同じ evaluate の中で
  入力してすぐ送信ボタンを押すと、まだ `disabled` のボタンを押す**ことになり
  何も起きない。症状は「追加されない」で、**実装は正しいのに FAIL になる**
  （手順18b で実際に踏んだ）。押す前に `!btn.disabled` を待つとさらに固い。

  ```python
  c.js("pbset(document.querySelector('#x'), '値'); return 1;")   # 入れる
  c.wait_for("!document.querySelector('#save').disabled")         # 反映を待つ
  c.js("document.querySelector('#save').click(); return 1;")      # 押す
  ```

- **`Input.insertText` は「いまのカーソル位置」へ入る。** CodeMirror を開いた直後の
  カーソルは**先頭**なので、そのまま打つと本文の**前**に付く（手順18b で、追記した
  つもりの文字列が前に付き、印による後片付けが空振りした）。**置き換えたいときは
  全選択してから入れる**——`Input.dispatchKeyEvent` に `commands: ["selectAll"]` を添える。

- **`captureBeyondViewport: true` は「画面の下が切れている」を隠す。** 文書全体を1枚に
  収めるモードなので、**ビューポートに収まらない部分も描画される**——スクロールできない
  作りでも、スクリーンショットには全部写る。**スクロールは座標ではなく実測で確かめる。**

  ```js
  const el = document.querySelector('.page-body')
  JSON.stringify({ scrollH: el.scrollHeight, clientH: el.clientHeight,
                   canScroll: el.scrollHeight > el.clientHeight,
                   overflow: getComputedStyle(el).overflowY })
  ```

  **`AppShell` の `.content` は `overflow: hidden` で、各ページが自前のスクロール枠を
  持つ約束である**（`UsersPage` の `.page-body`）。置き忘れると下が切れて操作できない。
  **pb-3 の設定画面でこれを踏み、自動検証は全 PASS のまま stg で利用者が見つけた**
  （2026-09-12）。**下までスクロールして最下部の要素が見えることまで測る。**

- **`Page.captureScreenshot` の `captureBeyondViewport: true` では `position: fixed` の要素が写らない。**
  文書全体を1枚に収めるモードなので、画面に固定した要素（`<Teleport>` で body へ出した
  ドロップダウンやメニュー）が抜ける。**開いたパネルを撮るときは `false` にする**
  （手順16d-b で、パネルが出ているのにスクリーンショットに無いという形で踏んだ）
- **`Page.captureScreenshot` は PNG では返ってこない。JPEG で撮る**（Chrome 152 /
  `--headless=new`。pb-6 で実測、2026-09-09）。`fromSurface` を足しても `clip` を
  足しても同じで、**`{format: 'jpeg', quality: 92, fromSurface: true}` なら返る**
  （`fromSurface: false` も返らない）。**症状はエラーではなく無応答**なので、
  待ち時間を延ばす方向へ探しに行くと当たらない。**形式を変えて1回試すのが早い。**
- **撮る前に落ち着くのを待ち、駄目なら撮り直す。** 描画が安定する前に要求すると
  応答が来ないことがあり、一度きりで諦めると「この画面は撮れない」に見える
  （pb-6 で実測）。**`setDeviceMetricsOverride` の直後に1秒待ち、失敗したら
  間隔を延ばして3回まで**で足りた。
- **画面を切り替えた直後の DOM を測らない。** マスター・ディテール（`GuiDesign.md`
  2.2.1）は開閉の途中で**行も列も見出しも揃っていない中間の DOM** を返す
  ——pb-6 では、3列になっているはずの一覧で `thead th` が 2、`.section-head` が 0
  だった。**同じ瞬間に撮ったスクリーンショットは正しかった**ので、
  「実装が壊れている」と読み違える。**目当ての要素が現れるまで待つ形にする。**
- **ドラッグ&ドロップを合成した `DragEvent` で確かめてはいけない。**
  `dispatchEvent(new DragEvent('dragstart'))` はハンドラを呼ぶだけで、**Chrome の
  ドラッグ機構を一度も通らない**。ドラッグが実際に始まるか、途中で取り消されないかを
  測れず、**手順16d-b では「32件 PASS」のままバックログの行が一度も掴めない欠陥を見逃した**
  （利用者の実機確認で判明、2026-08-24）。**測っていたのは自分のハンドラであって、画面ではない。**
  本物で回すには `Input` ドメインを使う。

  ```
  Input.setInterceptDrags {enabled: true}
  Input.dispatchMouseEvent {type: 'mousePressed' / 'mouseMoved' …}   ← ここでドラッグが始まる
  ← Input.dragIntercepted が来れば「始まった」。**来なければ取り消されている**
  Input.dispatchDragEvent {type: 'dragEnter' / 'dragOver' / 'drop', data: <intercepted の data>}
  ```

- **`dragOver` は同じ落とし先の中で位置を変えるとき、1回では届かないことがある。**
  **x を 1px ずらして2回送る**のを既定にする。手順22c で、行を3つに割る落とし先
  （上1/4・下1/4・中央1/2）のうち**中央だけがアプリに届かず、直前のゾーンの目印が
  残ったまま**になった。**実装は3ゾーンとも正しかった。**
  切り分けは `document` に素の listener を張って `clientY` を数えるだけで済む
  ——**送った3件のうち2件しか届いていない**ことが1回で分かる。

  ```js
  window.__log = []
  document.addEventListener('dragover', (e) => window.__log.push(e.clientY), true)
  ```

- **`Input.dispatchDragEvent` の `data` は `dragIntercepted` が返したものをそのまま渡す。**
  `dragOperationsMask` が必須で、`{items: []}` のような手書きの値は
  `Invalid parameters` になる（`dragCancel` でも同じ）。
- **CDP のコマンド応答を待つあいだに来たイベントを読み捨てない。** 1本の
  WebSocket に応答とイベントが混ざって流れるので、`id` が一致する行だけを拾って
  残りを捨てる作りにすると、**`Input.dragIntercepted` のような1回きりのイベントが消える**。
  待つ側で溜めておき、あとから取り出す。
- **検証端末に `websocket-client` は入っていない。** CDP を叩くには
  **WebSocket を標準ライブラリで書く**（RFC 6455。ハンドシェイク、テキストフレーム、
  **クライアント側のマスクは必須**、継続フレームの連結で足りる）。
  **`Sec-WebSocket-Accept` の magic GUID は `258EAFA5-E914-47DA-95CA-C5AB0DC85B11`**
  （36文字。手順23 で末尾の区切りを取り違えて書き、**ハンドシェイクは 101 で成功するのに
  検算だけが合わない**という形で 15 分溶かした）。**書いたら RFC 6455 の例で1回検算する**
  ——`dGhlIHNhbXBsZSBub25jZQ==` → `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=`。
- **継続フレームを連結しないと、大きな応答が「永久に届かない」ように見える。**
  上の「継続フレームの連結で足りる」を落とすと起きる（pb-6 で実測、2026-09-09）。
  Chrome は**スクリーンショットの画像のような大きな応答を複数フレームに割る**ので、
  FIN ビットを見ずに最初のフレームだけを1メッセージとして返すと、`id` が一致する
  行がいつまでも現れない。**症状は「そのコマンドだけが時間切れになる」**で、
  小さな応答は全部通るため**コマンド側の問題に見える。**
  **FIN が立つまで payload を溜めて連結する**（opcode `0x0` が継続フレーム）。
- **ソケットに時間切れを持たせる。** `recv` がブロックしたままだと、
  待ち合わせのループに書いた期限が一度も評価されない
  ——**「応答が来ない」ではなく「スクリプトが返らない」**という形になり、
  どのコマンドで止まったのかも分からなくなる。
- **`Runtime.evaluate` の包みを2種類持つ。** `(function(){…})()` の中では `await` が
  使えない（`SyntaxError: await is only valid in async functions`）。**ページ内 `fetch` で
  APIを叩く検証**は必ず `await` を要るので、`(async function(){…})()` で包む版を別に用意する。
- **ページ内 `fetch` で書き込み系APIを叩くと、Cookie も CSRF もそのまま乗る。**
  トークンは `document.cookie` の `pb_csrf` から取り、ヘッダ名は **`X-PB-CSRF`**
  （`client/src/api/client.ts`）。**画面を操作するより速く、権限や応答の形をそのまま測れる**
  ——手順23 では憲章の本文を4件 `PATCH` して `?outline=1` の章立てまでを1本で確かめた。
  `If-Match` は `"<version>"`（引用符ごと）。
- **画面を測る前に `make build` を通す。** dev のサーバは **client を embed している**ので
  （`Design.md` 3.4）、`make run` で起動し直すだけでは**古い画面が出続ける**。症状は
  「押しても何も起きない」で、**実装のバグに見える**——pb-69 では、`pb-55` で廃止したはずの
  確認モーダルが開いていて、選択肢を押しても `POST` が飛ばなかった。**測っていたのは
  数世代前の画面である。** 1コマンドで済ませるなら `make restart`（停止→ビルド→DB起動→起動）。
  疑ったときは `/healthcheck` の `version` を見る（`PB_HEALTH_SHOW_VERSION=true` のとき出る）。
- **`dragstart` を機に落とし場所を描き足すと、Chrome がドラッグを取り消す。**
  掴んだ行の位置が直後にずれるためで、症状は「`dragstart` の 1〜2ms 後に `dragend` が来て、
  `dragover` が一度も起きない」。**落とし場所は掴む前から画面にあるものに限る**
  （`GuiDesign.md` 5.4「掴んだ瞬間に要素を差し込まない」）

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

## 8.5 検証そのものが誤りやすいところ

| 規則 | 理由 |
|---|---|
| **「動いたこと」を先に確かめてから「動かないこと」を確かめる** | ガードの検証は、ガードに当たらない操作が成功することを先に見る。順序を逆にすると**ガードを検証していないまま緑になる**——手順16c で `⠿` のドロップが一度も効かない実装を書いたが、「元に戻る」も「別の親へは動かない」も**周囲3件は PASS のまま**だった。何も動かないので両方通る |
| **「戻る」「消える」「ゼロになる」を測るなら、先に始点を作る** | 終わりの値だけを書くと、**実装が何もしなくても通る。** 手順13a で `failed_attempts` のリセットを測ったとき、**リセット前が0でないことを一度も確かめていなかった**（そのうえ測る位置も、故意に失敗させたログインの後だった）——「0になった」は最初から0でも成り立つ。**始点が意味のある値であることを1件測ってから、終点を測る** |
| **一覧の並びは「APIの応答と同じ順か」で見る** | 並び順の正本はサーバで、`display_name` は `COLLATE "ja-JP-x-icu"`（`DbDesign.md` 4.4）で比較される。**検証側で並べ直して突き合わせると、日本語の読み順と食い違って誤検知する** |

## 8.6 seed に無い状態を作る（放置チケット・古い更新日時）

**`updated_at` は通常の `UPDATE` では過去へ置けない。** `0006` の
`trg_ticket_updated`（BEFORE UPDATE）が `now()` を書くためである。
`ApiDesign.md` 9.13.1 の `stale`（14日以上更新のないチケット）や、
`GuiDesign.md` 5.3 の「要対応」の放置の行は、**seed のデータでは
一度も画面に出ない**（`pb dev seed` の投入時刻がそのまま入る）。

**トリガを一時停止して振る。**

```
make psql <<'SQL'
ALTER TABLE ticket DISABLE TRIGGER trg_ticket_updated;
UPDATE ticket SET updated_at = now() - interval '20 days'
 WHERE seq = <検証用の seq> AND project_id = (SELECT id FROM project WHERE key='demo');
ALTER TABLE ticket ENABLE TRIGGER trg_ticket_updated;
SQL
```

**チケット自体は API で作る**（`POST /projects/demo/tickets`）。`seq` の採番を
サーバに任せるためで、直接 INSERT すると次の作成と番号がぶつかりうる。

**振った後にそのチケットを触ると元へ戻る。** `PATCH` も `transition` も
トリガを起こして `updated_at` が `now()` になるので、**他の検証を全部
終えてから最後に振る**（手順19b で、振った直後に `PATCH` して
「放置0件」に戻り、4件の FAIL を出した）。

後始末は 8.4 に従い、チケットを消し、**積まれた `activity` も消す**——
ULID は単調増加なので、検証前に `SELECT max(id) FROM activity` を控えておけば
`DELETE FROM activity WHERE id > '<控えたID>'` で落とせる。

---

---

# 9. つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| `make: *** No rule to make target 'migrate'` / `no makefile found` | **リポジトリ直下以外で実行している。** `make` は親ディレクトリを探しに行かない。`pwd` を確認して直下へ戻るか、`make -C <リポジトリのパス> migrate` と書く |
| `make up` が `failed to connect to the docker API` | コンテナランタイムが停止している。Rancher Desktop を起動し、`docker info` が通るまで待つ（約30秒） |
| `make run` が `bind: address already in use` | 前のセッションの `pb` が :8080 を掴んでいる。`lsof -nP -iTCP:8080 -sTCP:LISTEN` で確認する。`make stop-server` で落とすか、他人のプロセスを残すなら `PB_BIND=127.0.0.1:8099` のように待受を変えて起動する |
| **バックグラウンドで起動したら、`/healthcheck` の `version` を `make version` と突き合わせる** | `make run &` は失敗しても画面に出ない。**古いプロセスが :8080 を掴んでいると、healthcheck は 200 を返し続ける**ので「起動した」と誤読する（手順26c で実際に踏み、足したばかりの MCP ツールが `tools/list` に出ないことで初めて気づいた）。**版が一致しなければ、動いているのは自分のビルドではない** |
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

**一覧クエリを足すときの3点**（`ticket.sql` の `ListTickets` が型を持つ）。

1. **総件数と最終更新は窓関数で同じクエリから返す**（`count(*) OVER ()`）。条件が十数種類あると、
   `user.sql` 方式の「WHERE を2本そろえる」は必ずずれる
2. **`CASE` 式の結果を SELECT の列に出さない。** `::int` を付けると sqlc が NOT NULL と推論し、
   値が NULL の行で 500 になる。**単体テストはフェイクを返すので通ってしまい、実サーバで初めて出る**
3. **`sort_key` の比較には `COLLATE "C"` を明示する。** DBの既定は `ja-JP-x-icu` で、ICU は句読点の
   重みを言語規則で決める。`internal/lexorank` が仮定しているのは**バイト順**である（4.2 / 4.4）

**生成器の推論を説得しようとせず、2〜3手で当たらなければ「型」ではなく「値」で表す。**
sqlc は `LEFT JOIN LATERAL` の右辺が NULL になることを推論できず、NOT NULL 列を `string` と出す
（実行時に `cannot scan NULL into *string` で落ちる）。`::text` → スカラ副問い合わせ →
ELSE 無しの `CASE` → `NULLIF` と4手外したあと、`(COALESCE(t.id::text, ''))::text` で決着した
——ULID は空文字になりえないので、**値で「無い」を表せる**。
**判断の境目は「推論を変えたいのか、NULL を扱いたいのか」。**
あわせて、**次の手を選ぶ前に生成物を1回読む**（`grep 'TokenID '` 1回で済む）。

## 10.4 実行時の依存

**Go（`server/go.mod` の直接依存）**：`jackc/pgx/v5` / `oklog/ulid/v2` /
`alexedwards/argon2id` / `golang.org/x/term` / `go-chi/chi/v5` の5つ。
トークンのハッシュと乱数は標準ライブラリ（`crypto/sha256` / `crypto/rand`）で足りる。

**client（`client/package.json`）**：`vue` / `vue-router` / `pinia` に、
**後から足した4つ**——`codemirror` / `@codemirror/lang-markdown` / `markdown-it` /
`dompurify` を加えた**7つ**。後半4つは**チケット詳細の説明欄だけが使う**
（`GuiDesign.md` 5.5「説明欄」。Markdownソース＋ライブプレビュー）。
`dompurify` は `markdown-it` の出力を描画の直前に通すためのもので、
**本文の書き手がエージェントでもありうる**ことによる（`Design.md` 3.1）。

dev に `vite` / `@vitejs/plugin-vue` / `typescript` / `vue-tsc` / `openapi-typescript`、
および `markdown-it` の型（`@types/markdown-it`）。
**`openapi-typescript` と `@types/*` は型のためだけで実行時には入らない。**
`npm run build` は型検査（`vue-tsc --noEmit`）を通してから `vite build` する
（型エラーはビルドを止める）。`make build-client` は `npm ci` を使うため
`client/package-lock.json` をコミットしている。

**依存を足す・置き換えるのはユーザーの承認が要る**（PB の規約「実装の前に」）。

---

# 11. ドッグフーディング用インスタンス（stg）

**設計は `Design.md` 4.4。** ここには手順だけを置く。

**PB 自身のプロジェクト管理に使う、壊れないインスタンス**である。開発中に壊れる `dev` とは
**compose プロジェクトごと分かれている**ので、`make dev-reset` を実行しても stg のデータは消えない。

| | `dev` | `stg` |
|---|---|---|
| 画面 | `http://127.0.0.1:8080` | **`http://localhost:8081`** |
| DB | `127.0.0.1:5432`（`project-backyard`） | `127.0.0.1:5433`（`pb-stg`） |
| データの実体 | ボリューム `project-backyard_pgdata` | ボリューム `pb-stg_pgdata` |
| PB 本体 | `make run`（`go run`） | `deploy/stg/build.sh` が出力した一式 |
| 作り直し | `make dev-reset` | **しない** |

**`http://localhost:8081` で開くこと。`127.0.0.1:8081` で開いてはならない。**
Cookie はポートを区別しないため、`dev` と同じホスト名で開くと**双方のログインセッションが
上書きし合う**（`Design.md` 4.4）。`localhost` と `127.0.0.1` は Cookie 上は別ホストである。

## 11.1 初回セットアップ

```
make stg-init            # 秘密を乱数で生成 → 設定 → DB起動 → migrate
make stg-admin-create    # 初期管理者を対話的に作る（DbDesign.md 7.5）
make stg-build           # 動作に必要な一式を deploy/stg/out/ へ出力する
make stg-run             # 前景で起動する
```

**`make stg-init` は何度実行しても壊れない。** 既にある秘密と `pb.env` は作り直さない
（秘密を作り直すと DB のロールと食い違って接続できなくなるため）。

**`dev` と違い、秘密を手で書き換える必要はない。** `init.sh` が `openssl rand` で生成する。
`stg` は作り直さない器なので、初回に一度だけ強い値を機械に決めさせる。

**デモデータは入れない**（`make dev-seed` に相当するものを用意していない）。stg のデータは
本番相当である。**`pb dev seed` のガード（`DbDesign.md` 7.6.3）は接続先ホストしか見ないので
stg を止めない**——`make` に入口を作らないことで防いでいる。

## 11.2 日々の使い方

```
make stg-build           # コードを進めたら作り直す
make stg-stop            # :8081 を掴んでいるサーバを止める
make stg-run             # 起動し直す
```

**背景で動かすなら出力先で直接叩く。**

```
nohup deploy/stg/out/run.sh > deploy/stg/out/pb.log 2>&1 &
```

**アプリの常駐は保証していない。** データが `pb-stg_pgdata` に残っている限り、
落として上げ直せば同じ状態に戻る（`Design.md` 4.4）。**「壊れない」の保証は DB 側にある。**

**DB コンテナは `restart: always`** なので、コンテナランタイムを起動し直すと自動で上がる。
**ただし `make stg-down` はコンテナ自体を消す**ので、その後は `make stg-up` が要る。
通常は止めない。

動いているかを見る:

```
curl -s http://localhost:8081/healthcheck        # バージョンが出る
docker compose ls | grep pb-stg                  # DB が動いているか
lsof -nP -iTCP:8081 -sTCP:LISTEN                 # サーバが動いているか
```

## 11.3 マイグレーションを足したとき

**`stg` にも適用する。** これは手順20 以降ずっと続く運用である（`Design.md` 11章）。

```
make migrate       # dev
make stg-migrate   # stg
make stg-build     # バイナリを作り直す
```

**goose は出力一式に含まれない**（`server/tools/` のツールモジュールにある。`DbDesign.md` 5.1）。
スキーマを進めるのはリポジトリ側の作業である。

## 11.4 出力一式を別のパスへ置く

`deploy/stg/build.sh` の出力は**丸ごとコピーすれば、そこで動く**。

```
make stg-build OUT=/path/to/dir
```

設定ファイル内のパスはすべて出力ディレクトリからの相対で書かれており、`run.sh` は
自身のあるディレクトリへ移ってから `pb` を起動する。使い方は出力に同梱の `README.txt` にある。

**起動するのは `run.sh` であって `pb` ではない。** `pb.env` は**シェルが読んで環境変数へ
export するファイル**であり、バイナリは開かない（`Design.md` 4.4）。`pb` を直に叩いても
`pb.env` は効かない。**バイナリが直接読む設定ファイルは `PB_CONFIG_FILE` の YAML だけ**で、
stg では使っていない（第2層は画面から変える。12章）。

## 11.5 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| `make stg-build` が「先に make stg-init を実行すること」と言う | `pb.env` か `secrets/app_database_url` が無い。`make stg-init` を実行する |
| stg にログインすると dev からログアウトされる | `127.0.0.1:8081` で開いている。**`localhost:8081`** で開き直す |
| `bind: address already in use` | 前のサーバが残っている。`make stg-stop` |
| 起動して即座に落ちる | `deploy/stg/secrets/app_database_url` のパスワードが DB のロールと食い違っている。**秘密を作り直したなら DB も作り直す**（initdb はボリュームが空のときしか走らない） |
| `docker compose ls` に `pb-stg` が出ない | `make stg-up` |

---

# 12. エージェントを MCP でつなぐ

**設計は `Design.md` 8章。** ここは手順だけを書く。

## 12.1 このリポジトリの配置ファイル

**手順28a から、手順ファイルは PB が生成したものである**（`/p/<key>/settings/agents`。
`GuiDesign.md` 5.11）。`.claude/commands/pb-onboard.md` / `pb-implement.md` /
`pb-refine.md` と `.claude/settings.json` の許可がそれで、**直したいときは
テンプレート（`server/internal/agentsetup/templates/body/`）を直して取り直す。**
手で直すと、次に取り直したときに消える。

**`.mcp.json` は履歴管理の対象外である**（`Requirements.md` 10.8.1。2026-09-06 に
`.gitignore` へ移した）。各人のネットワーク事情で書き換えるファイルなので、コミットすると
**個人環境が履歴に残り、参加者どうしで上書き合戦になる**。**clone した人は自分で用意する**
——**手で書かない。`/me/agents` の接続パネルから落とす**（手順28b。下の 12.2）。

**stg（`:8081`）を指すエージェントを登録する。** PB 自身の管理に PB を使うためで
（`Design.md` 4.4）、**dev（`:8080`）ではない**——`make dev-reset` で消えるインスタンスを
憲章の置き場にはできない。**画面を開いたアドレスがそのまま `url` に入る**ので、
**`localhost:8081` で開いて落とすこと**（`127.0.0.1:8081` で開くと dev と
ログインセッションが上書きし合う。11.5）。

**環境変数名は本人が決める**（`ApiDesign.md` 4.5.2 の `token_env_suffix`）。
**0023 より前に登録したエージェントは接尾が未設定**で、その場合は
`PB_TOKEN_<エージェントの id>` になる（4.5.1 のフォールバック）。**決めた名前へ揃えるなら、
`/me/agents` の編集で接尾を入れてから接続設定を落とし直し、`~/.zshrc` の変数名も変える**
——**片方だけ変えると「繋がらない」になる**（症状は 12.4 の 401）。

> **2026-09-06 の実測**：この作業ディレクトリに `.mcp.json` は無い。改訂前の本節は
> 「`${PB_TOKEN}` のまま置いてある」と書いていたが、**実物は存在しなかった**。

## 12.2 つなぐ（初回）

```
make stg-build                 # MCP を持つバイナリを作る
make stg-stop && make stg-run  # 起動し直す（前景。背景は 11.2）
```

1. `http://localhost:8081` を開いてログインする（**`127.0.0.1:8081` で開かない**。11.5）
2. `/me/agents` でエージェントを登録する（プロジェクトは `pb`、クライアント種別は使う道具、
   **環境変数名の接尾は端末が分かる名前**にする）
3. トークンを発行し、**一度だけ表示される全文**を控える
4. **同じカードの `[ 接続の手順を開く ]` を押し、接続設定をコピーするか zip で落とす**
   （手順28b）。zip の中は**別名**なので、展開してから元の名前へ戻す

   | クライアント | 置き場 | zip の中の名前 |
   |---|---|---|
   | Claude Code | `.mcp.json` | `.mcp.pb-block.json` |
   | GitHub Copilot | `.vscode/mcp.json` | `.vscode/mcp.pb-block.json` |
   | OpenAI Codex | `.codex/config.toml` | `.codex/config.pb-block.toml` |

5. 同じパネルの `export` 行をコピーして端末の環境変数に置き、3 の全文を入れる。
   **リポジトリには書かない**（`Requirements.md` 10.8.1）

```
export PB_TOKEN_<接尾>=pb_agt_...     # ~/.zshrc か direnv
```

6. Claude Code を開き直し、`/pb-onboard` を実行する

**うまくいけば、憲章の要約と自分の担当チケットが返る。** 読み取りしかしないコマンドなので、
ツール許可を read 系だけ自動承認にしておくと一息に走る（`.claude/settings.json`。
**Codex では同じ許可が `.codex/config.toml` に入っている**——`Requirements.md` 10.8.4.1）。

**繋がったかどうかは PB の画面で分かる。** `/me/agents` のトークンの箱に
**`✓ 接続済み（最終利用 …）`** が付く（`access_token.last_used_at`）。**付かないときは
一度も届いていない**ので、URL・環境変数名・シェルの開き直しを 12.4 の順に見る。

## 12.3 手で叩く（サーバだけを確かめたい）

**MCP クライアントを立てずに、`curl` で JSON-RPC を1往復できる。**
実装を直したときの当たりを取るのはこちらが速い。

```
TOKEN=pb_agt_...   # 履歴に残したくないなら read -s TOKEN

# ツールの一覧
curl -s http://127.0.0.1:8080/mcp/demo \
  -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | python3 -m json.tool

# ツールを1つ呼ぶ
curl -s http://127.0.0.1:8080/mcp/demo \
  -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"pb_get_doc","arguments":{"path":"rules"}}}' | python3 -m json.tool
```

**dev のトークンは画面から作れる**（`http://127.0.0.1:8080` → `/me/agents`）。
デモアカウントは 4.1。

## 12.4 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| クライアントが「接続できない」と言う | サーバが起きていない。`curl -s http://localhost:8081/healthcheck` で確かめる |
| `401 unauthenticated` | `PB_TOKEN` が空か、失効しているか、**所有者が無効化されている**（`Design.md` 6.5）。`/me/agents` で発行し直す |
| `404 not_found` | URL のプロジェクトキーが、トークンのプロジェクトと違う（同 8.3）。`.mcp.json` の URL を見る |
| `403 forbidden` がツールの結果に出る | トークンのスコープか所有者の権限が足りない（同 6.4.1）。`/me/agents` で発行時のスコープを見る |
| `405 method_not_allowed` | `GET` で叩いている。**PB は `POST` だけを受ける**（SSE ストリームを持たない。同 8.4） |
| ツールが1つも見えない | クライアントは接続時に一覧をキャッシュする。**クライアントを開き直す** |

## 12.5 手順外で気づいた問題を起票する

**手順の範囲外で見つけた不具合・改善候補は、stg の PB にチケットとして起票する**
（`CLAUDE.md`「進捗と作業の進め方」）。**`docs/PROGRESS.md` には番号と1行要約だけを置く。**

```
# 12.2 でつないだ MCP から
pb_create_ticket(project="pb", type=..., title=..., body_md=...)
```

- **プロジェクトは stg の `pb`。** dev は `make dev-reset` で消えるので起票先にしない
- **必要なのは `ticket.create` を含むトークン**（既定スコープ8件に入っている。`ApiDesign.md` 4.5.3）
- **その場で実装しない。** 起票して手順の作業へ戻る（`pb-step.md` 手順7）
- **stg のバイナリが `pb_create_ticket` を持たないときは起票できない**——
  write 系3件は手順26a（2026-09-05）で入ったので、それ以前のビルドで動いている stg では
  `tools/list` に出ない。`make stg-build` → 再起動 → **クライアントの MCP 接続を張り直す**

---

# 13. 設定を変える

**設計の正本は `Design.md` 10.3。** ここに置くのは操作の手順である。

設定は3層に分かれており、**どの層にあるかで変え方が違う。**

| 層 | 変え方 | 反映 |
|---|---|---|
| **1. 起動前**（接続文字列・待受） | 環境変数か設定ファイル | **再起動が要る** |
| **2. 実行時の共有設定**（ログ・ヘルスチェック・Cookie） | **画面**（`/admin/settings`）／設定ファイル／環境変数 | 即時 |

## 13.1 画面から変える（第2層）

`http://localhost:8080/admin/settings` を開く（stg は `:8081`）。**必要権限は
`system.settings`** で、アドミニストレータが持つ。

**各項目に出どころの札が付く。**

| 札 | 意味 | 編集できるか |
|---|---|---|
| `[既定]` | まだ誰も与えていない | できる |
| `[DB]` | 画面から変えたもの | できる |
| `[設定ファイル]` | `PB_CONFIG_FILE` の YAML に書いてある | **できない** |
| `[環境変数]` | 環境変数で与えてある | **できない** |
| `[秘密ファイル]` | `<KEY>_FILE` が指すファイルにある | **できない** |

**編集できない項目は、外し方が画面に出る。** 「`PB_LOG_FORMAT` で固定されています」の
ように環境変数名か設定ファイルのキーが添えられるので、そこを外せば画面から変えられる
ようになる。

**`[DB]` の項目には `既定に戻す` が出る。** 押すと行が消え、`[既定]` に戻る。

## 13.2 設定ファイルを使う（任意）

雛形は `deploy/base/pb.yaml.example` にある。任意のパスへ複製し、位置を渡す。

```
PB_CONFIG_FILE=/etc/pb/pb.yaml ./pb serve
```

```yaml
# キーは平らに並べる。PB_ を付けない
log_level: info

# ${環境変数名} でその環境変数を引く。秘密をこのファイルに書かないための口
database_url: ${PB_DATABASE_URL}
```

**起動が失敗する条件を先に知っておくこと。** どれも「書いたのに効いていない」を
黙って作らないためである。

| 条件 | 出るもの |
|---|---|
| 知らないキーがある | `PB_CONFIG_FILE に知らないキーがある: [log_levle]` |
| キーが入れ子になっている | `… は入れ子になっている。設定ファイルのキーは平らに並べること` |
| `${…}` が参照する環境変数が未設定 | `… が参照する環境変数 X が未設定である` |
| 値が値域に合わない | `ログレベル（PB_CONFIG_FILE の log_level）が正しくない: …` |
| ファイルが読めない | `PB_CONFIG_FILE が指すファイルを読めない` |

**`$` そのものを書くときは `$$`** とする（接続文字列のパスワードなどで要る）。

## 13.3 環境変数で1件だけ上書きする

設定ファイルより弱く、DB より強い。**1件だけ差し替えたいときに使う。**

```
PB_LOG_LEVEL=debug ./pb serve
```

**与えると画面から変更できなくなる**ので、恒久的に固定したいものにだけ使う。
`deploy/base/compose.yaml` と `deploy/stg/pb.env.example` が第2層の既定を持たないのは
この理由である——**書くと画面が読み取り専用になる。**

## 13.4 設定を1件足す（開発者向け）

**触るのは `server/internal/config/registry.go` の `definitions` だけである。**
マイグレーションは要らない（`app_setting` はキーと値の表で、許可リストを
CHECK に書いていない。`DbDesign.md` 6.14）。

足すときに決めるのは5点。

1. `Layer`——**起動前に要るなら第1層**（DB から読めない）
2. `Type` と `Allowed`——`string` / `bool` / `enum`
3. `Default` か `Required`
4. `Secret`——真なら API が値を返さない
5. `RestartRequired`

**`DisplayName` と `Description` はそのまま画面に出る日本語で書く。**

## 13.5 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| 設定画面で項目が編集できない | ファイルか環境変数で固定されている。画面に出ている環境変数名かキーを外す。**第1層は外しても編集できない**（起動前に要る設定のため） |
| 画面で変えたのに効かない | **上の層が勝っている。** 札が `[DB]` になっているか見る。`[環境変数]` のままなら、そちらを外す |
| `cookie_secure` を有効にしたらログインできなくなった | http で提供しているため（`Design.md` 6.2.1）。**画面から直せない**ので、`PB_COOKIE_SECURE=false` を環境変数で与えて起動し直す（環境変数が DB に勝つ。この復旧経路のためにこの順にしてある） |
| 起動時に「設定の行を読めなかった」と出る | DB に繋がっていないか `app_setting` が無い。**起動は続く**（ファイルと環境変数と既定値で動く）。`make migrate` を当てる |
| `make docs-size` が予算を超えた | 本書は対象外（毎セッション読む4文書のみ）。`CLAUDE.md` 「文書の分量」を見る |

---

---

# 14. HTTPS で公開する（TLS）

**設計の正本は `Design.md` 6.6.1。** ここに置くのは操作の手順である。

**PB は自分で TLS を終端できる。** 前段にリバースプロキシを置く構成も従来どおり使える。

## 14.1 3ステップで始める

### ① 暗号鍵を作る

**秘密鍵を暗号化して保存するための鍵である**（`Design.md` 10.3 の第1層）。
証明書を登録しないなら要らない。

```
openssl rand -base64 32
```

出た値を `PB_SECRET_KEY` に与える。**32バイトを base64 で与える**のが形式で、
他の長さは起動時に弾かれる。

```
PB_SECRET_KEY=<上で出た値> ./pb serve
```

**秘密なので、設定ファイル（`pb.yaml`）に直接書かない。** 書くなら `${PB_SECRET_KEY}`
として環境変数を引く形にする（13.2）。**ファイルで渡すなら `PB_SECRET_KEY_FILE`** に
パスを置く（`deploy/<env>/secrets/` に実ファイルを置く形。`DbDesign.md` 3.2）。

**この鍵を失うと、登録済みの証明書を復号できなくなる。** そのときは証明書を
消して登録し直す。**鍵を変えるときも同じ**——登録し直しが要る。

### ② 証明書を登録する

`/admin/settings` の「TLS 証明書」タブで、証明書と秘密鍵の PEM を貼る
（`GuiDesign.md` 5.12.1）。**必要権限は `system.settings`。**

**貼った秘密鍵は二度と表示されない。** 暗号化して保存され、どの応答にも現れない。
**手元の鍵を捨てないこと。**

### ③ TLS を有効にして再起動する

同じ画面の「一般」タブで「TLS で待ち受ける」を有効にし、**再起動する**。
待受の切り替えには再起動が要る（第1層ではないが、待受を張り直すため）。

起動ログの `scheme` で確かめられる。

```
msg=サーバを起動した bind=127.0.0.1:8443 scheme=https
```

## 14.2 自己署名証明書を作る（openssl）

**開発端末や LAN の中で試すとき**に使う。ブラウザは警告を出すが、PB の動作は
認証局発行の証明書と変わらない（`Design.md` 6.6.1）。

```
openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
  -keyout pb.key -out pb.crt \
  -subj "/CN=pb.example.com" \
  -addext "subjectAltName=DNS:pb.example.com,DNS:localhost"
```

| 指定 | 意味 |
|---|---|
| `-nodes` | **秘密鍵にパスフレーズを付けない。** PB はパスフレーズ付きを受けない（`ApiDesign.md` 11.5） |
| `-subj "/CN=…"` | 画面の一覧に出る名前 |
| `-addext "subjectAltName=DNS:…"` | **ブラウザが見るのはこちらである。** `CN` だけでは最近のブラウザが受けない。**アクセスに使うホスト名を必ず入れる** |
| `IP:` | **IP アドレスで繋ぐなら `DNS:` ではなく `IP:` で入れる**（`subjectAltName=DNS:localhost,IP:127.0.0.1`）。`DNS:127.0.0.1` は一致しない（14.5） |
| `-days` | 有効日数。**切れると画面が見えなくなる**ので、更新の予定と合わせる |

**`pb.crt` を「証明書」、`pb.key` を「秘密鍵」の欄に貼る。**

### 中身を確かめる

```
openssl x509 -in pb.crt -noout -subject -dates -ext subjectAltName
openssl x509 -in pb.crt -noout -fingerprint -sha256
```

**指紋は画面に出るものと一致する。** 貼り間違いに気づく手がかりになる。

### curl で試す

自己署名なので、CA として証明書そのものを渡す。

```
curl --cacert pb.crt https://pb.example.com:8443/healthcheck
```

**`-k` で検証を飛ばさない。** 飛ばすと「証明書が正しく出ているか」を確かめられない。

## 14.3 認証局が発行した証明書を登録する

サイバートラスト・DigiCert・GlobalSign・Let's Encrypt など、**発行元によらず手順は同じ**である。
PB は検証の連鎖を辿らないので、**フォーマル証明書と自己署名証明書を区別しない**
（`Design.md` 6.6.1）。

### ① 鍵と CSR を作る

```
openssl req -new -newkey rsa:2048 -nodes \
  -keyout pb.key -out pb.csr \
  -subj "/C=JP/ST=Tokyo/O=Example Inc./CN=pb.example.com"
```

**`pb.key` は手元に残す。** これが秘密鍵で、発行元には渡さない。
**`pb.csr` を発行元の申込画面へ提出する。**

発行元が組織の実在確認を求める形式（OV / EV）では、この後に審査が入る。

### ② 受け取ったものを1つにまとめる

発行元からは**サーバ証明書と中間証明書が別々に届くことが多い**。

**証明書の欄には、サーバ証明書 → 中間証明書 の順で続けて貼る。**

```
-----BEGIN CERTIFICATE-----
（サーバ証明書。CN が自分のホスト名のもの）
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
（中間証明書。発行元から渡されるもの）
-----END CERTIFICATE-----
```

**順序を守ること。** 逆にすると、クライアントが連鎖を辿れず「証明書が信頼できない」と出る。
**ルート証明書は貼らなくてよい**（クライアントが持っている）。

コマンドでまとめるなら次のようにする。

```
cat server.crt intermediate.crt > pb-chain.crt
```

### ③ 連鎖が正しいか確かめる

```
openssl verify -untrusted intermediate.crt server.crt
```

`server.crt: OK` と出れば連鎖が繋がっている。

**登録したあとは外から確かめる。** PB は連鎖を検証しないので、**繋がっていない連鎖でも
登録できてしまう**。

```
openssl s_client -connect pb.example.com:8443 -servername pb.example.com
```

`Verify return code: 0 (ok)` を確かめる。**`Verify return code: 21` は中間証明書が
足りていない**（②の順序か有無を見直す）。

### ④ 更新のとき

**古いものを消さずに、新しいものを登録する。** PB は**有効なもののうち
`notBefore` が最も新しいものを出す**ので（`Design.md` 6.6.1）、新しい証明書が
有効になった時点で自動的に切り替わる。**再起動は要らない。**

画面では新しい行が `[ 待機中 ]` になり、「いつから使われるか」が出る。
切り替わったあと、古い行は `[ 世代交代 ]` になるので消してよい。

**期限が切れるまで待たない。** 切り替えは `notBefore` で起きるので、
**更新を早めに登録しておくほど安全**である。

## 14.4 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| 「PB_SECRET_KEY の設定が要ります」と出て登録できない | 鍵を与えていない。14.1 ① |
| 起動が「1枚も読めなかった」で落ちる | **`PB_SECRET_KEY` が登録時と違う。** 同じ鍵を与えるか、証明書を消して登録し直す |
| 起動が「証明書が1枚も登録されていない」で落ちる | `tls_enabled` が有効なのに証明書が無い。登録するか `PB_TLS_ENABLED=false` |
| HTTPS で繋がらず、ログに「ハンドシェイクを拒否した」 | **有効な証明書が無い**（全部期限切れか、まだ有効でない）。`PB_TLS_ENABLED=false` で平文に戻してから登録し直す |
| ブラウザが「証明書が無効」と言う | 自己署名なら想定どおり。認証局発行なら**中間証明書が足りない**（14.3 ③）か、**SAN にアクセス先のホスト名が無い**（14.2） |
| 「これを消すと有効な証明書が無くなります」で削除できない | 最後の有効な証明書である。平文へ戻すなら先に「TLS で待ち受ける」を無効にする |
| 証明書を登録したのに `[ 待機中 ]` のまま | `notBefore` がまだ来ていない。画面に「いつから使われるか」が出ている |
| 平文に戻したい | **`PB_TLS_ENABLED=false` を与えて起動し直す。** 環境変数が DB に勝つので、画面を開けなくても戻せる（13.3） |
| **TLS にしたらエージェントが MCP で繋がらなくなった** | **クライアントがこの証明書を信頼していない。** ブラウザと違って続行の選択肢が無い。14.5 |
| クライアントが `DEPTH_ZERO_SELF_SIGNED_CERT` と言う | 同上。**自己署名である**という意味で、PB 側は正常に動いている。14.5 |
| 画面は「覆っています」と出るのにクライアントが繋がらない | **覆っているのは待受のホスト名であって、クライアントが使う名前とは限らない。** `.mcp.json` の URL と突き合わせる（14.5） |


## 14.5 クライアントに証明書を信頼させる

**TLS を有効にすると、エージェントが MCP で PB へ繋げなくなることがある**（pb-100。
2026-09-12 に stg で実際に起きた）。

```
pb (DEPTH_ZERO_SELF_SIGNED_CERT): "self signed certificate"
```

**ブラウザとエージェントで振る舞いが違う。** ブラウザは警告を出したうえで**続行の
選択肢を与える**が、**エージェントは黙って失敗する。** 画面は成功して見えるので、
**壊れているのはエージェントの側だけで、画面を見ている人には分からない。**

**自己署名だけの問題ではない。** 社内の認証局が発行した証明書でも、**その認証局を
信頼していないクライアントは同じく落ちる。**

### 落とし穴は3つある

**pb-3 の動作確認で順に踏んだ。**

| # | 落とし穴 | 症状と対処 |
|---|---|---|
| 1 | **`0.0.0.0` を証明書に入れても意味がない** | `PB_BIND` の値をそのまま `-subj` や `-addext` に書くと踏む。**`0.0.0.0` は待受の表記であって接続先のホスト名ではない。** クライアントが使う名前（`localhost` や `127.0.0.1`）を入れる |
| 2 | **IP で繋ぐなら `IP:` が要る** | `DNS:127.0.0.1` では一致しない。`.mcp.json` が `https://127.0.0.1:…` なら `IP:127.0.0.1` を入れる（14.2） |
| 3 | **クライアント側に証明書を渡す手当てが別に要る** | PB を直しても解決しない。下記の手順 |

**1 と 2 は画面で気づける**——TLS を有効にする前に、証明書が待受のホスト名を覆って
いるかが出る（`GuiDesign.md` 5.12.1）。**3 は画面から案内する。**

### 手順

#### ① 証明書を取り出す

**`/admin/settings` の「TLS 証明書」タブで「保存」を押す**（`ApiDesign.md` 11.7）。
**HTTPS にする前に取っておける**のがこの導線の要点である。

**既に HTTPS にしてしまい、画面も開けないときは接続先から直接取る。**

```
openssl s_client -connect 127.0.0.1:8443 -showcerts </dev/null 2>/dev/null \
  | openssl x509 -outform PEM > pb.crt
```

#### ② クライアントに渡す

**Node.js で動くクライアント（Claude Code など）は `NODE_EXTRA_CA_CERTS` を読む。**

```
export NODE_EXTRA_CA_CERTS=/absolute/path/to/pb.crt
```

**絶対パスで与える。** クライアントの作業ディレクトリは自分の端末と同じとは限らない。

#### ③ クライアントを再起動する

**これをしないと効かない。** 環境変数は起動時にしか読まれない。

**GUI から起動するクライアントでは、シェルで `export` しても届かないことがある**
（macOS の launchd 配下など）。**本書で実機確認したのは Claude Code（CLI）だけである。**

### 切り分け

**PB 側とクライアント側のどちらが悪いかを1回で分ける。**

```
curl --cacert pb.crt https://127.0.0.1:8443/healthcheck
```

| 結果 | 意味 |
|---|---|
| 通る | **PB は正しく出している。** クライアント側の設定（②③）の問題である |
| `unable to get local issuer certificate` | 取り出した `pb.crt` が、いま出している証明書と違う。①からやり直す |
| `certificate is not valid for` … | **SAN が繋ぎ先の名前を覆っていない**（落とし穴1・2）。証明書を作り直す（14.2） |

**`-k` で検証を飛ばして確かめない。** 飛ばすと**この切り分けが成立しない**——
`-k` はどちらの原因でも通ってしまう。

**証明書そのものを見る。**

```
openssl x509 -in pb.crt -noout -subject -ext subjectAltName
```


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
