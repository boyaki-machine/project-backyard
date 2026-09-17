# Project Backyard（PB）配布物マニュアル

`make release` が出力する一式の使い方。**この文書は一式に同梱される**
（リポジトリの `deploy/prod/MANUAL.md` が正本）。

```
1. どの形で動かすか
2. 一式を作る（リポジトリで）
3. native（mac / Windows / Linux）
   3.1 一式の中身          3.6 初期管理者を作る       3.11 新しい版へ入れ替える
   3.2 必要なもの          3.7 起動する・止める       3.12 Windows で使う
   3.3 DB を用意する       3.8 常駐させる             3.13 つまずいたとき
   3.4 秘密を置く          3.9 設定を変える
   3.5 スキーマを作る      3.10 端末の外へ出す
4. コンテナ（docker / compose）
   4.1 イメージと一式の中身              4.5 compose で外部の PostgreSQL へ繋ぐ
   4.2 イメージを取り込む・登録する      4.6 docker 単体で動かす
   4.3 秘密ファイルの権限                4.7 新しい版へ入れ替える
   4.4 compose で動かす                  4.8 つまずいたとき
5. Kubernetes
   5.1 一式の中身                      5.6 初期管理者を作る
   5.2 イメージをクラスタへ届ける      5.7 外部の PostgreSQL へ繋ぐ
   5.3 秘密を作る                      5.8 クラスタの外へ出す
   5.4 apply する                      5.9 新しい版へ入れ替える・片付ける
   5.5 画面を開く（port-forward）      5.10 つまずいたとき
```

---

## 1. どの形で動かすか

| TARGET | 出力されるもの | DB | この版 |
|---|---|---|---|
| `native` | 実行ファイルと起動スクリプト（mac / Windows / Linux） | **用意済みの PostgreSQL に繋ぐ** | 使える |
| `docker` | コンテナイメージと、`docker run` で動かすスクリプト | **用意済みの PostgreSQL に繋ぐ** | 使える |
| `compose` | コンテナイメージと compose 一式 | **DB のコンテナも一緒に立てる**（外部の PostgreSQL へ繋ぐこともできる） | 使える |
| `k8s` | コンテナイメージとマニフェスト一式 | **試すための DB のサンプル付き**（運用では CloudNativePG か外部の PostgreSQL） | 使える |

**どの形でも、PB の配布物に DB は入っていない。** compose と k8s のサンプルが立てる DB は、公開イメージ
`pgvector/pgvector:pg17` を起動時に取りに行く。

---

## 2. 一式を作る（リポジトリで）

リポジトリの直下で実行する。

```
make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]
make release TARGET=<docker|compose|k8s> ARCH=<amd64|arm64> [OUT=<出力先>] [PUSH=<レジストリ>/<名前>:<タグ>]
```

| 引数 | 指定できる値 | 同じ意味に読む表記 |
|---|---|---|
| `TARGET` | `native` / `docker` / `compose` / `k8s` | — |
| `OS` | `darwin` / `windows` / `linux`。**docker / compose / k8s は `linux` だけで、省いてよい** | `mac` → `darwin` |
| `ARCH` | `amd64` / `arm64` | `x64`・`x86_64`・`x86` → `amd64`、`m1`・`arm`・`aarch64` → `arm64` |
| `OUT` | 出力先のディレクトリ | 省略すると `dist/pb-v<版>-<TARGET>-<OS>-<ARCH>` |
| `PUSH` | docker / compose / k8s だけ。イメージを tar に出す代わりに、このレジストリへ送る（4.2） | — |

- **要るもの**：native は Go と Node.js（リポジトリの `docs/Development.md` 1章）。
  **docker / compose / k8s は Docker（buildx）だけ**——ビルドはイメージの中で行う
- **CPU は 64bit の2種だけ。** `386` や `armv7` など 32bit を指定すると、理由を表示して止まる
- **空でない出力先には書かない。** 作り直すときは、出力先を消してから実行する
- 指定の誤りで止まったときの終了コードは 2、ビルドそのものが失敗したときは 1

---

## 3. native（mac / Windows / Linux）

**手順の流れ**：3.3 DB を用意する → 3.4 秘密を置く → 3.5 スキーマを作る → 3.6 初期管理者を作る → 3.7 起動する。
**3.3〜3.6 は最初の1回だけ**である。

以下の例は mac / Linux のもの。**Windows では `./run.sh` を `.\run.ps1` に、`./migrate.sh` を
`.\migrate.ps1` に読み替え**、先に 3.12 を読む。

### 3.1 一式の中身

```
<一式>/
├── pb                 PB 本体（Windows は pb.exe）。画面も入った単一の実行ファイル
├── goose              スキーマを進める道具（Windows は goose.exe）
├── migrations/        スキーマの定義（goose が読む）
├── run.sh             起動の入口（Windows は run.ps1）
├── migrate.sh         スキーマを進める入口（Windows は migrate.ps1）
├── migrate.conf       goose の接続先（パスワードは書かない）
├── pb.yaml            PB の設定ファイル（何も書かなくても動く）
├── create-roles.sql   PostgreSQL に DB とロールを作る SQL
├── secrets/
│   ├── app_database_url.example   PB 本体の接続文字列の見本
│   └── pgpass.example             goose のパスワードファイルの見本
├── launchd/           常駐の雛形（mac の一式だけ）
├── systemd/           常駐の雛形（Linux の一式だけ）
└── MANUAL.md          この文書
```

**一式は丸ごと好きな場所へ置いてよい。** スクリプトは自分の位置へ移ってから動く。
**起動は必ず `run.sh` から行う。** `pb` を直に実行すると、設定ファイル・秘密・待受が渡らない。

### 3.2 必要なもの

| もの | 条件 |
|---|---|
| **PostgreSQL 17** | 拡張 `pgcrypto`・`citext`・`pg_trgm`（contrib）と、**ICU**（日本語の並び順に `ja-JP-x-icu` を使う）が要る |
| **スーパーユーザ** | 3.3 の SQL を流すのに要る（DB とロールを作るため） |
| **psql** | 3.3 で1回だけ使う。PostgreSQL に付いてくる |

**ICU が使えるかは、PostgreSQL に繋いで次を実行すれば分かる。** 1行返れば使える。

```sql
SELECT collname FROM pg_collation WHERE collname = 'ja-JP-x-icu';
```

公式のコンテナイメージ `postgres:17` で動くことを確かめてある。**pgvector は要らない。**

### 3.3 DB を用意する

一式のディレクトリで、スーパーユーザとして `create-roles.sql` を1回だけ流す。

```
psql -h 127.0.0.1 -p 5432 -U postgres -d postgres -f create-roles.sql
```

- **`pb_owner` と `pb_app` のパスワードを、2回ずつ尋ねられる。** 入力は画面に出ず、
  SQL ファイルにも psql の履歴にも残らない。**入力した値は 3.4 で使うので控えておく**
- **パスワードは英数字だけにすると、3.4 で書き方に気を遣わずに済む。** 作り方の例：
  - mac / Linux：`openssl rand -hex 16`
  - Windows（PowerShell）：`$b = New-Object byte[] 16; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); ($b | % { $_.ToString('x2') }) -join ''`
- **作られるもの**：ロール `pb_owner`（スキーマの持ち主。goose が使う）と `pb_app`
  （PB 本体が使う。テーブルを作れない・消せない）、DB `pb`（UTF8、照合順序 C、文字の種類 C.UTF-8）、拡張3つ
- **2回目は最初の `CREATE ROLE` で止まる。** やり直すときは、先に
  `DROP DATABASE pb; DROP ROLE pb_app; DROP ROLE pb_owner;` を流す（**中身は消える**）
- **スーパーユーザを持てない PostgreSQL**（クラウドのサービスなど）では、そのサービスの管理用ロールで流す。
  `ALTER DEFAULT PRIVILEGES FOR ROLE pb_owner` が権限の不足で失敗したら、そのロールを `pb_owner` の
  メンバーにしてから流し直す。**この場合の手順は確かめていない**

### 3.4 秘密を置く

**3つのファイルを書く。** うち2つは見本を複製して作る。

```
cp secrets/app_database_url.example secrets/app_database_url
cp secrets/pgpass.example secrets/pgpass
chmod 600 secrets/app_database_url secrets/pgpass
```

| ファイル | 書くこと | 使うもの |
|---|---|---|
| `secrets/app_database_url` | `CHANGE_ME` を **`pb_app` のパスワード**に置き換える。ホストとポートを実際の PostgreSQL に合わせる | PB 本体 |
| `secrets/pgpass` | `CHANGE_ME` を **`pb_owner` のパスワード**に置き換える | goose |
| `migrate.conf` | ホストとポートを実際の PostgreSQL に合わせる。**パスワードは書かない** | goose |

- **パスワードが渡る経路**：`run.sh` はファイルの位置だけを `pb` に渡し、`migrate.sh` も
  ファイルの位置だけを goose に渡す。**パスワードは環境変数の値にもコマンドの引数にも現れない**
- `secrets/app_database_url` のパスワードに記号を含むときは、URL の書き方で書く（`@` → `%40`、`:` → `%3A`）
- `secrets/pgpass` のパスワードに `:` や `\` を含むときは、`\:` と `\\` と書く
- **別のマシンの PostgreSQL に繋ぐなら、`sslmode=disable` を `require` 以上にする**（両方のファイルで）
- **`migrate.conf` にパスワードを書くと、`migrate.sh` はエラーで止まる**（引数に載って他のユーザから見えるため）

### 3.5 スキーマを作る（migrate）

```
./migrate.sh          # 未適用のものをすべて適用する
./migrate.sh status   # 適用状況を表示する
```

**新しい版へ入れ替えたときも、起動の前に毎回実行する**（3.11）。適用済みなら何も変えずに終わる。
スキーマは前へ進めるだけで、戻す手順は無い。**戻したくなったときに備え、版を入れ替える前に
`pg_dump` でバックアップを取る。**

### 3.6 初期管理者を作る

```
./run.sh admin create
```

表示名・メールアドレス・パスワード（2回）を尋ねられる。**最初の1回だけ**でよい。
PB 本体と同じ設定で DB へ繋ぐので、3.4 が済んでいれば追加の準備は要らない。

### 3.7 起動する・止める

```
./run.sh                          # 前景で起動する。Ctrl+C で止まる
nohup ./run.sh > pb.log 2>&1 &    # 背景で起動する。ログは pb.log
```

- **画面は `http://localhost:8080` で開く。** `127.0.0.1` ではなく `localhost` と書く——
  パスキーは `localhost` でしか使えない
- **動いているかは `curl -s http://localhost:8080/healthcheck`** で分かる（`{"status":"OK"}` が返る）
- **背景で起動したものを止める**：`kill <PID>`。PID は `lsof -ti tcp:8080 -sTCP:LISTEN` で分かる
- **待受は `127.0.0.1:8080` に固定してある**（この端末からだけ届く）。変えるなら 3.10

### 3.8 常駐させる

**雛形の `__PB_HOME__` を、一式を置いたディレクトリの絶対パスに置き換えて使う。**
一式のディレクトリで次を実行する。

**mac（launchd）**

```
sed "s|__PB_HOME__|$(pwd)|g" launchd/local.projectbackyard.pb.plist \
  > ~/Library/LaunchAgents/local.projectbackyard.pb.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/local.projectbackyard.pb.plist

# 止めて外す
launchctl bootout gui/$(id -u)/local.projectbackyard.pb
```

ログインしたときに起動し、落ちたら上げ直す。ログは一式の中の `pb.log`。

**Linux（systemd）**

```
sed -e "s|__PB_HOME__|$(pwd)|g" -e "s|__PB_USER__|$(id -un)|g" systemd/pb.service \
  | sudo tee /etc/systemd/system/pb.service > /dev/null
sudo systemctl daemon-reload
sudo systemctl enable --now pb
journalctl -u pb -f     # ログを見る
```

**Windows（タスクスケジューラ）**

管理者の PowerShell で、`C:\pb` を一式の場所に置き換えて実行する。

```
schtasks /Create /TN "Project Backyard" /SC ONSTART /RU SYSTEM `
  /TR "powershell -NoProfile -ExecutionPolicy Bypass -Command \"& 'C:\pb\run.ps1' *> 'C:\pb\pb.log'\""
```

**3つとも、雛形の形式を確かめただけで、実際に登録して常駐させることは確かめていない。**

### 3.9 設定を変える

- **ほとんどの設定は、画面の「管理 → 設定」から変える。** 反映が即時か、再起動が要るかは画面に出る
- **画面から変えられないようにしたい設定は `pb.yaml` に書く。** 書いたものは画面で「固定」と表示される
- **TLS の証明書も画面から登録する**（3.10）
- **待受（アドレスとポート）だけは `run.sh` が決めている。** 画面では「環境変数で固定」と表示される

### 3.10 端末の外へ出す

**平文（http）のまま外へ出さない。** 次の順で進める。

1. 画面の「管理 → 設定」で証明書を登録し、TLS を有効にする。**有効にしたあと、期限内に確認しないと元に戻る**
2. `https://localhost:8080` で開けることを確かめる
3. 画面の「管理 → 設定」で「待受アドレス」を、外から届くアドレス（例：`0.0.0.0:8443`）に
   変える。**切り替えは即時で、期限内に確認しないと元に戻る**（再起動は要らない）
4. 画面で「Cookie に Secure を付ける」を有効にする

証明書の作り方は、リポジトリの `docs/Development.md` 14章にある。

### 3.11 新しい版へ入れ替える

**動いている一式の上に、新しい一式を上書きしない。** 実行中のプロセスが落ちることがある。
別の場所に作ってから入れ替える。

```
# ① 新しい一式を別の場所に作る（リポジトリで make release … OUT=<新しい一式>）

# ② 設定と秘密を引き継ぐ
cp -p <今の一式>/secrets/app_database_url <今の一式>/secrets/pgpass <新しい一式>/secrets/
cp -p <今の一式>/migrate.conf <今の一式>/pb.yaml <新しい一式>/
#    待受アドレスを画面で変えていたなら、その値は DB にあるので引き継ぎは要らない

# ③ バックアップを取ってから止める（3.5）

# ④ 新しい一式でスキーマを進める
<新しい一式>/migrate.sh

# ⑤ 置き場所を入れ替えて起動する
mv <今の一式> <今の一式>.old
mv <新しい一式> <今の一式>
<今の一式>/run.sh
```

**⑤ の `mv` は順番が意味を持つ。** 移し先のディレクトリが既にあると、中へ移されてしまう。

### 3.12 Windows で使う

> **この版の Windows 向け一式は、ビルドと中身の検査だけを行った。Windows の実機では動かしていない。**
> `run.ps1` と `migrate.ps1` は一度も実行されていない。動かして気づいたことがあれば、PB のプロジェクトへ知らせてほしい。

- **実行ポリシーで止められたら**：`powershell -ExecutionPolicy Bypass -File .\run.ps1`
- **`run.ps1` と `migrate.ps1` は BOM 付きの UTF-8 で保存してある。** Windows PowerShell 5.1 は BOM の無い
  スクリプトを日本語として正しく読めない。**編集して保存するときも文字コードを変えない**
- 秘密ファイルは、エクスプローラーのプロパティ → セキュリティで、PB を動かすユーザだけが読めるようにする
- コンソールで日本語が化けるときは、先に `chcp 65001` を実行する

### 3.13 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| `secrets/app_database_url が無い` ／ `secrets/pgpass が無い` | 3.4 を済ませる |
| `migrate.conf の接続先にパスワードが含まれている` | `migrate.conf` の URL から `:パスワード` を消し、パスワードは `secrets/pgpass` に書く |
| migrate が `password authentication failed for user "pb_owner"` | `secrets/pgpass` のパスワードが違う。または行のユーザ名が `pb_owner` になっていない |
| 起動が `password authentication failed for user "pb_app"` | `secrets/app_database_url` のパスワードが違う。記号は URL の書き方にする（3.4） |
| migrate が `extension "…" is not available` | PostgreSQL に contrib が入っていない（3.2） |
| 一覧を開くと `collation "ja-JP-x-icu" … does not exist` | PostgreSQL が ICU なしでビルドされている（3.2） |
| `bind: address already in use` | 8080 番を別のプロセスが使っている。止めるか、`PB_BIND=127.0.0.1:<空いている番号>` を付けて起動し直す（起動できたら画面の「待受アドレス」で変えられる） |
| ログインしても入れない（http で開いている） | 「Cookie に Secure を付ける」を http のまま有効にした。`PB_COOKIE_SECURE=false` を付けて起動し、画面で無効に戻す |
| 画面の「待受アドレス」を変えられない | `pb.yaml` に `bind:` を書いている（3.9）か、`PB_BIND` を付けて起動している。**どちらもしていなければ画面から変えられる**（既定は `127.0.0.1:8080`） |

---

## 4. コンテナ（docker / compose）

**手順の流れ（compose）**：4.2 イメージを取り込む → 4.4 秘密を作って起動する。
**手順の流れ（docker 単体）**：4.2 イメージを取り込む → 4.6 用意済みの PostgreSQL に繋いで起動する。
**先に 4.3（秘密ファイルの権限）を読む。**

### 4.1 イメージと一式の中身

**イメージ `project-backyard:<版>`**

| 中身 | 役目 |
|---|---|
| `/pb` | PB 本体（画面も入った単一の実行ファイル）。コンテナの入口で、既定の引数は `serve` |
| `/goose` | スキーマを進める道具（postgres のドライバだけ） |
| `/migrations/` | スキーマの定義 |

- **利用者は uid 65532（root ではない）。** シェルも `curl` も入っていない
- **コンテナの中の待受は `0.0.0.0:8080`** で、イメージが環境変数 `PB_BIND` で決めている（画面では
  「環境変数で固定」と表示される）。**外への公開範囲は `-p` や compose の `ports` で決める**
- **CPU ごとに別のイメージである。** amd64 の端末には amd64 の、arm64 の端末には arm64 の一式を使う
- **コンテナの healthcheck は持たない。** 健全かは外から `curl -s http://localhost:8080/healthcheck` で見る
  （`{"status":"OK"}` が返る）

**compose の一式**

```
<一式>/
├── project-backyard-<版>-linux-<CPU>.tar   イメージ（PUSH で作った一式には無い）
├── compose.yaml         db・migrate・app の3サービス
├── init.sh              秘密を乱数で作る（最初の1回だけ）
├── initdb/01_roles.sh   DB の初回起動で pb_app のロールを作る
├── create-roles.sql     外部の PostgreSQL へ繋ぐときに使う（4.5）
└── MANUAL.md            この文書
```

**docker の一式**

```
<一式>/
├── project-backyard-<版>-linux-<CPU>.tar   イメージ（PUSH で作った一式には無い）
├── run.sh               docker run で PB を起動する
├── migrate.sh           docker run で goose を流す
├── migrate.conf         goose の接続先（パスワードは書かない）
├── create-roles.sql     用意済みの PostgreSQL に DB とロールを作る（3.3）
├── secrets/             app_database_url.example / pgpass.example
└── MANUAL.md            この文書
```

### 4.2 イメージを取り込む・登録する

**tar は `docker load` で取り込める形式である**（buildx の `type=docker` で書き出したもの）。

```
docker load -i project-backyard-<版>-linux-<CPU>.tar     # project-backyard:<版> として取り込まれる
```

**自分のレジストリへ登録する**には、取り込んだイメージに名前を付けて送る。

```
docker tag  project-backyard:<版>  registry.example.com/pb:<版>
docker push registry.example.com/pb:<版>
```

**一式を作るときに、tar を出さずに直接送る**こともできる（`PUSH`）。

```
make release TARGET=compose ARCH=amd64 PUSH=registry.example.com/pb:<版>
```

- **送る先には、先に `docker login` しておく**
- **`PUSH` で作った一式には tar が入らない。** `compose.yaml`（docker の一式なら `run.sh` と `migrate.sh`）が
  そのレジストリのイメージを指す。起動するときにそこから取りに行く
- **CPU ごとに1つずつ送る。** amd64 と arm64 を1つの名前にまとめるには、docker-container ドライバの
  builder で `docker buildx build --platform linux/amd64,linux/arm64 --push` を実行する。**この手順は確かめていない**

### 4.3 秘密ファイルの権限

**`secrets/` ディレクトリは 700、中のファイルは 644 にする。** compose の `init.sh` はこの形で作る。

| 権限 | 理由 |
|---|---|
| **ファイル 644** | コンテナの中の利用者（PB は uid 65532、DB は postgres）が読めるようにする。**docker compose は secrets の `uid`・`mode` の指定を無視する**ので、ファイルの権限で開けるしかない |
| **ディレクトリ 700** | この端末の他のユーザを閉め出す。コンテナへはファイルを1つずつ渡すので、ディレクトリが 700 でもコンテナからは読める |

- **ディレクトリを 700 より緩めない。** ファイルは 644 なので、ディレクトリが開くと他のユーザから秘密が読める
- **`secrets/` をディレクトリごとコンテナへ渡さない。** 700 のディレクトリを、コンテナの中の利用者は開けない
- **mac と Windows の Docker Desktop・Rancher Desktop では、権限が違っていても動いてしまう**
  （共有したファイルの所有者が、コンテナの利用者に書き換わって見える）。**Linux のサーバへ移したときに
  初めて `permission denied` になる**ので、最初からこの形で置く

### 4.4 compose で動かす

一式のディレクトリで、最初の1回は次の順に実行する。

```
docker load -i project-backyard-<版>-linux-<CPU>.tar   # 4.2（PUSH で作った一式なら不要）
./init.sh                                            # 秘密を乱数で作る
docker compose up -d                                 # db → migrate → app の順に起動する
docker compose run --rm app admin create             # 初期管理者を作る
```

- **画面は `http://localhost:8080`**（`localhost` で開く。3.7）
- **migrate は起動のたびに走り、適用済みなら何も変えずに終わる。** app は migrate の成功を待って起動する
- **DB のポートは外へ出していない。** DB に入るなら `docker compose exec db psql -U pb_owner -d pb`
- **compose のプロジェクト名は `pb-prod`** で、コンテナとデータのボリューム（`pb-prod_pgdata`）の名前の頭に付く

| したいこと | コマンド |
|---|---|
| 状態を見る | `docker compose ps` |
| ログを見る | `docker compose logs -f app`（migrate の結果は `docker compose logs migrate`） |
| 止める（コンテナは残る） | `docker compose stop` |
| コンテナを消す（**データは残る**） | `docker compose down` |
| **データごと消す** | `docker compose down -v` |

- **秘密を作り直すのは、データごと消すときだけ**：`docker compose down -v` → `rm -r secrets` → `./init.sh`。
  DB のパスワードは、データのボリュームが空の初回起動でしか決まらない
- **端末の外へ出すとき**は、3.10 の1・2・4を行い、3 の代わりに `compose.yaml` の `ports` の左側
  （`127.0.0.1:8080`）を外から届くアドレスに直して `docker compose up -d` する。**平文のまま外へ出さない**

### 4.5 compose で外部の PostgreSQL へ繋ぐ

**`compose.yaml` の末尾にある「外部の PostgreSQL へ繋ぐとき」の4段に従う。** 要点は次のとおり。

- **外部の PostgreSQL に `create-roles.sql` を1回流す**（3.3）。`initdb/01_roles.sh` は compose の db の
  初回起動でしか走らないので、**外部の PostgreSQL ではロールを自分で作る必要がある**
- db サービスを消し、migrate の接続先を外部用の行に差し替える
- `init.sh` は使わず、`secrets/` を 700、中のファイルを 644 で手で作る（4.3）
- **この組み合わせは確かめていない**

### 4.6 docker 単体で動かす

**用意済みの PostgreSQL に繋ぐ。** 一式のディレクトリで、最初の1回は次の順に実行する。

```
docker load -i project-backyard-<版>-linux-<CPU>.tar     # 4.2（PUSH で作った一式なら不要）
psql -h <ホスト> -U postgres -d postgres -f create-roles.sql   # 3.3（1回だけ）

cp secrets/app_database_url.example secrets/app_database_url
cp secrets/pgpass.example secrets/pgpass
chmod 700 secrets
chmod 644 secrets/app_database_url secrets/pgpass
#   secrets/app_database_url・secrets/pgpass・migrate.conf を書き換える（3.4 と同じ。ホストは下の表）

./migrate.sh              # スキーマを作る
./run.sh admin create     # 初期管理者を作る
./run.sh                  # 背景で起動する（コンテナ名 pb）
```

**ホストは「コンテナの中から見た」名前で書く。** `127.0.0.1` はコンテナ自身を指す。

| PostgreSQL の場所 | ホストに書くもの |
|---|---|
| 同じ端末 | `host.docker.internal`（`run.sh` と `migrate.sh` がこの名前を引けるようにしている）。**Linux では、PostgreSQL が docker のブリッジのアドレスでも待ち受け、そこからの接続を `pg_hba.conf` で許している必要がある。この場合は確かめていない** |
| コンテナで動いている | そのコンテナ名。**`PB_DOCKER_NETWORK=<ネットワーク名>` を付けて**、`migrate.sh` と `run.sh` を同じネットワークに入れる |
| 別のマシン | そのホスト名。`sslmode=disable` を `require` 以上にする |

| したいこと | コマンド |
|---|---|
| ログを見る | `docker logs -f pb` |
| 止める・消す | `docker stop pb` → `docker rm pb` |
| コンテナ名を変える | `PB_CONTAINER_NAME=<名前> ./run.sh` |

- **パスワードが渡る経路**：`run.sh` と `migrate.sh` は秘密ファイルを1つずつコンテナへ渡し、
  **パスワードは環境変数の値にも docker の引数にも現れない**
- **公開は `127.0.0.1:8080` だけ。** 外へ出すときは 3.10 に従い、`run.sh` の `-p 127.0.0.1:8080:8080` を直す

### 4.7 新しい版へ入れ替える

**compose**：新しい一式を別の場所に作り、秘密を引き継いで、そこで起動し直す。**プロジェクト名が同じ
（`pb-prod`）なので、同じデータのボリュームを使う。** migrate は起動のときに走る。

```
docker load -i <新しい一式>/project-backyard-<新しい版>-linux-<CPU>.tar
cp -Rp <今の一式>/secrets <新しい一式>/
#    compose.yaml を書き換えていたなら（ports・外部の PostgreSQL など）、新しい compose.yaml にも同じ変更を入れる
docker compose -f <今の一式>/compose.yaml exec db pg_dump -U pb_owner pb > pb-backup.sql   # バックアップ
cd <新しい一式> && docker compose up -d
```

**docker 単体**：

```
docker load -i <新しい一式>/project-backyard-<新しい版>-linux-<CPU>.tar
cp -Rp <今の一式>/secrets <今の一式>/migrate.conf <新しい一式>/
<新しい一式>/migrate.sh
docker stop pb && docker rm pb
<新しい一式>/run.sh
```

### 4.8 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| `docker compose up` が `bind source path does not exist: …/secrets/db_password` で止まる | `./init.sh` を実行していない（4.4） |
| `pull access denied for project-backyard` など、イメージを取りに行って失敗する | イメージを取り込んでいない（4.2） |
| app が起動せず、`docker compose ps` で migrate が失敗している | `docker compose logs migrate` を見る。`password authentication failed` なら、`secrets/` と DB のパスワードが食い違っている（DB のパスワードは初回起動でしか決まらない。4.4） |
| ログに `permission denied`（`/run/secrets/…`） | 秘密ファイルの権限を 4.3 の形にする |
| `port is already allocated`（8080） | 8080 番を別のプロセスが使っている。compose は `ports` の左側、docker は `run.sh` の `-p` を変える |
| docker 単体で `connection refused` や名前が引けない | 接続先のホストの書き方を 4.6 の表で確かめる |
| `exec format error` | CPU の違う一式を使っている（4.1） |

---

## 5. Kubernetes

**手順の流れ**：5.2 イメージを届ける → 5.3 秘密を作る → 5.4 apply する → 5.6 初期管理者を作る → 5.5 画面を開く。
**5.3 と 5.6 は最初の1回だけ**である。

**`k8s/db.yaml` の DB は、試すためのサンプルである。** バックアップもフェイルオーバも持たない。
運用では CloudNativePG か外部の PostgreSQL を使う（5.7）。

### 5.1 一式の中身

```
<一式>/
├── project-backyard-<版>-linux-<CPU>.tar   イメージ（PUSH で作った一式には無い）
├── k8s/
│   ├── app.yaml         PB 本体：ConfigMap（pb.yaml）・Deployment・Service
│   └── db.yaml          試すための DB：ConfigMap（ロール作成）・Service・StatefulSet（PVC 付き）
├── secret.example.yaml  Secret の雛形（値は入っていない。5.3）
├── create-roles.sql     外部の PostgreSQL へ繋ぐときに使う（5.7）
└── MANUAL.md            この文書
```

| 名前 | 種類 | 役目 |
|---|---|---|
| `pb` | Deployment・Service | PB 本体。Service は 8080 番 |
| `pb` | Secret | パスワードと接続文字列。**各コンテナへ環境変数で渡る** |
| `pb-config` | ConfigMap | 設定ファイル `pb.yaml`（3.9 と同じ書き方） |
| `pb-db` | StatefulSet・Service | 試すための DB。Service は 5432 番 |
| `pb-db-initdb` | ConfigMap | DB の初回起動で `pb_app` のロールを作る |
| `data-pb-db-0` | PVC | DB のデータ（StatefulSet が作る） |

- **Pod が起動するたびに、initContainer の `migrate` がスキーマを進める。** 適用済みなら何も変えずに終わる。
  失敗すると PB 本体は起動しない
- **レプリカは1のままにする。** migrate の同時実行を防ぐロックが無い。版の入れ替えは「止めてから上げる」
  （`strategy: Recreate`）ので、**入れ替えの間は PB が止まる**
- **マニフェストに namespace は書いていない。** apply するときに `-n` で決める
- 利用者は uid 65532（root ではない）で、ファイルシステムを読み取り専用にして動かす
- **CPU ごとに別のイメージである**（4.1）。ノードの CPU に合わせた一式を使う

### 5.2 イメージをクラスタへ届ける

**ノードがイメージを引ける場所へ置く。** クラスタによって届け方が違う。

| クラスタ | 届け方 |
|---|---|
| **レジストリから引く**（一般のクラスタ） | 4.2 のとおりレジストリへ送り、`k8s/app.yaml` の `image:`（2か所）をその名前に直す。**`PUSH` で作った一式なら、はじめからその名前が入っている** |
| **Rancher Desktop（コンテナエンジンが dockerd）** | `docker load -i project-backyard-<版>-linux-<CPU>.tar`。**k3s が同じ docker を使うので、取り込んだイメージがそのまま見える** |
| **Rancher Desktop（コンテナエンジンが containerd）** | `nerdctl -n k8s.io load -i project-backyard-<版>-linux-<CPU>.tar`。**この手順は確かめていない** |

- **どのエンジンで動いているか**は `kubectl get nodes -o wide` の `CONTAINER-RUNTIME` 列で分かる（`docker://…` か `containerd://…`）
- **`imagePullPolicy` は `IfNotPresent`**：ノードに同じ名前のイメージがあれば取りに行かない。**tar で届けたときは、名前（`project-backyard:<版>`）を変えない**
- **非公開のレジストリ**から引くなら、docker-registry 型の Secret を作り、`k8s/app.yaml` の `imagePullSecrets` のコメントを外す。
  作り方は Kubernetes の文書「Pull an Image from a Private Registry」にある。**この手順は確かめていない**

### 5.3 秘密を作る

**`secret.example.yaml` を複製して、`CHANGE_ME` を3か所書き換える。** 雛形そのものには値を書かない。

```
cp secret.example.yaml secret.yaml
chmod 600 secret.yaml
openssl rand -hex 16     # 1つ目の値
openssl rand -hex 16     # 2つ目の値
```

| キー | 書くもの |
|---|---|
| `owner_password` | 1つ目の値（`pb_owner` のパスワード） |
| `app_password` | 2つ目の値（`pb_app` のパスワード） |
| `database_url` | `pb_app:CHANGE_ME@` の `CHANGE_ME` を、**2つ目と同じ値**にする |

- **DB のパスワードは、PVC が空の初回起動でしか決まらない。** あとから Secret の値を変えても DB の側は変わらず、
  認証エラーになる（5.10）
- **パスワードは英数字だけにする。** 記号を含むと、`database_url` の書き方に気を遣う必要がある（`@` → `%40`、`:` → `%3A`）
- **値は各コンテナへ環境変数で渡る。** Pod の定義（`kubectl get pod -o yaml`）に出るのは Secret の参照だけで、値は出ない。
  **Secret を読める権限を持つ人は値を読める**ので、namespace の権限で絞る
- **Secret は `kubectl apply` ではなく `kubectl create` で入れる。** `apply` は、入れた内容を Secret の注釈
  （`kubectl.kubernetes.io/last-applied-configuration`）に**平文のまま**残し、`kubectl get secret pb -o yaml` で値がそのまま読める
- **値を変えるときは `kubectl replace -n <ns> -f secret.yaml`**（これも注釈を残さない）。環境変数は Pod の起動時にしか
  読まれないので、続けて `kubectl rollout restart -n <ns> deployment/pb` を実行する。**DB の側のパスワードは変わらない**（上の1つ目）
- 入れたあと、`secret.yaml` は消してよい（値はクラスタの Secret に残る）

### 5.4 apply する

`<ns>` は PB を入れる namespace（例：`pb`）。一式のディレクトリで実行する。

```
kubectl create namespace <ns>
kubectl create -n <ns> -f secret.yaml     # Secret だけは apply ではなく create（5.3）
kubectl apply -n <ns> -f k8s/db.yaml
kubectl rollout status -n <ns> statefulset/pb-db --timeout=180s     # DB が Ready になるまで待つ
kubectl apply -n <ns> -f k8s/app.yaml
kubectl rollout status -n <ns> deployment/pb --timeout=180s
```

- **DB を先に上げる。** `kubectl apply -n <ns> -f k8s/` でまとめて入れても最後には上がるが、DB が Ready になるまで
  migrate が失敗と再試行を繰り返し、待ち時間が延びる
- **入れる前に、通るかだけを見る**なら `kubectl apply -n <ns> -f k8s/ --dry-run=server`
- migrate の結果は `kubectl logs -n <ns> deployment/pb -c migrate`、PB 本体のログは `kubectl logs -n <ns> deployment/pb -c pb -f`

### 5.5 画面を開く（port-forward）

```
kubectl port-forward -n <ns> svc/pb 8080:8080
```

- **画面は `http://localhost:8080` で開く**（`localhost` と書く。3.7）。port-forward を止めると届かなくなる
- **動いているかは `curl -s http://localhost:8080/healthcheck`** で分かる（`{"status":"OK"}` が返る）。
  Deployment の livenessProbe も同じ `/healthcheck` を見ている

### 5.6 初期管理者を作る

```
kubectl exec -it -n <ns> deployment/pb -c pb -- /pb admin create
```

表示名・メールアドレス・パスワード（2回）を尋ねられる。**最初の1回だけ**でよい。
**PB 本体のコンテナの中で動くので、接続先を渡す必要は無い。** イメージにシェルは無いが、`/pb` は直に実行できる。

### 5.7 外部の PostgreSQL へ繋ぐ

**`k8s/app.yaml` の末尾にある「外部の PostgreSQL へ繋ぐとき」の5段に従う。** 要点は次のとおり。

- **外部の PostgreSQL に `create-roles.sql` を1回流す**（3.2・3.3）。`k8s/db.yaml` の DB と違い、**ロールは自分で作る**
- `k8s/db.yaml` を apply しない
- `k8s/app.yaml` の migrate の接続先と、`secret.yaml` の `database_url` を、コメントアウトしてある外部用の行に差し替える
- **クラスタの Pod から外部の PostgreSQL へ届く**必要がある（ファイアウォールと `pg_hba.conf`）。別のマシンなら `sslmode` を `require` 以上にする
- **この組み合わせは確かめていない**

### 5.8 クラスタの外へ出す

**平文（http）のまま外へ出さない。** 次の順で進める。

1. port-forward で開いた画面の「管理 → 設定」で証明書を登録し、TLS を有効にする（3.10 の1・2）。以後の画面は `https://localhost:8080`
2. **`k8s/app.yaml` の livenessProbe に `scheme: HTTPS` を足して apply し直す**（コメントアウトしてある行）。
   **PB は平文と TLS を同時に待ち受けない**ので、http のままだと probe が失敗し続けて Pod が再起動を繰り返す
3. Ingress か `type: LoadBalancer` の Service で外へ出す
4. 画面で「Cookie に Secure を付ける」を有効にする

**この手順は確かめていない。**

### 5.9 新しい版へ入れ替える・片付ける

**入れ替える**

```
kubectl exec -n <ns> pb-db-0 -- pg_dump -U pb_owner -Fc pb > pb-backup.dump   # バックアップ（サンプルの DB のとき）
#   新しい一式のイメージを届ける（5.2）
kubectl apply -n <ns> -f <新しい一式>/k8s/app.yaml
kubectl rollout status -n <ns> deployment/pb --timeout=180s
```

- `k8s/app.yaml` を書き換えていたなら（外部の PostgreSQL・`imagePullSecrets`・`scheme: HTTPS` など）、新しい `app.yaml` にも同じ変更を入れる
- **古い Pod が止まってから、新しい Pod の migrate が走る**（`strategy: Recreate`）。migrate が失敗すると PB は止まったままになる——
  ログを見て直すか、バックアップから戻す
- Secret と `k8s/db.yaml` は作り直さない

**片付ける**

```
kubectl delete namespace <ns>
```

- **namespace ごと消すと PVC も消える。** その先のデータまで消えるかは StorageClass の `reclaimPolicy` による
  （`kubectl get storageclass` の `RECLAIMPOLICY` 列。`Delete` なら消える）
- PB を消してデータを残すなら、`kubectl delete -n <ns> -f k8s/app.yaml -f k8s/db.yaml`（PVC `data-pb-db-0` は残る）

### 5.10 つまずいたとき

| 症状 | 原因と対処 |
|---|---|
| Pod が `ErrImagePull` ／ `ImagePullBackOff` | イメージがノードに無い（5.2）。`kubectl describe pod -n <ns> -l app.kubernetes.io/component=app` の Events に、取りに行った名前が出る |
| Pod が `CreateContainerConfigError` | Secret `pb` が無いか、キーが足りない（5.3）。`kubectl describe pod` に足りないものが出る |
| Pod が `Init:Error` ／ `Init:CrashLoopBackOff` | migrate が失敗している。`kubectl logs -n <ns> deployment/pb -c migrate` を見る。DB が Ready になる前なら、待てば上がる |
| migrate が `password authentication failed for user "pb_owner"` | Secret の `owner_password` が、DB を初めて起動したときの値と違う（5.3）。サンプルの DB を作り直すなら `kubectl delete -n <ns> -f k8s/db.yaml` → `kubectl delete pvc -n <ns> data-pb-db-0` → 5.4（**データは消える**） |
| PB が `password authentication failed for user "pb_app"` | `database_url` の中のパスワードが `app_password` と違う（5.3） |
| Pod が再起動を繰り返し、Events に `Liveness probe failed` | 画面で TLS を有効にしたのに、probe が http のまま（5.8 の2） |
| port-forward の画面でログインしても入れない（http で開いている） | 「Cookie に Secure を付ける」を http のまま有効にした。`kubectl set env -n <ns> deployment/pb PB_COOKIE_SECURE=false` で入り、画面で無効に戻してから `kubectl set env -n <ns> deployment/pb PB_COOKIE_SECURE-` で外す |
| `exec format error` | ノードの CPU と違う一式を使っている（5.1） |

## 6. DB の文字の種類を C.UTF-8 へ移す（以前の一式で作った DB）

**起動時のログに `DB の LC_CTYPE が C のため、日本語のキーワード検索でインデックスが効かない` と出たら、この章の対象である。** 2.44.147 までの一式は、DB の文字の種類（`LC_CTYPE`）を `C` で作っていた。`C` では PostgreSQL が日本語を「語の文字」として数えないため、キーワード検索のインデックスが日本語に効かない（`DbDesign.md` 4.5）。

- **移さなくても PB は動く。** 日本語の検索が、チケットやコメントが増えるほど遅くなるだけである
- **文字の種類は DB を作るときに決まり、あとから変えられない。** そこで、別名の DB を `C.UTF-8` で作って中身を移し、名前を入れ替える。**元の DB は別名で残る**ので、名前を戻せば元どおりになる
- **移している間は PB を止める。** かかる時間は DB の大きさによる
- **ダンプにはパスワードのハッシュや暗号化した秘密が入る。** 終わったら消すか、秘密ファイルと同じ扱いで保管する

### 6.1 compose（サンプルの DB）

一式のディレクトリで打つ。**名前の入れ替えは、DB につながっているものがあると失敗する**（`database "pb" is being accessed by other users`）ので、先に PB を止める。

```
docker compose stop app
docker compose exec -T db pg_dump -U pb_owner -Fc pb > pb-before-ctype.dump
docker compose exec -T db psql -U pb_owner -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE pb_ctype OWNER pb_owner ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C.UTF-8' TEMPLATE template0"
docker compose exec -T db pg_restore -U pb_owner -d pb_ctype --exit-on-error < pb-before-ctype.dump
docker compose exec -T db psql -U pb_owner -d postgres -v ON_ERROR_STOP=1 \
  -c "GRANT CONNECT ON DATABASE pb_ctype TO pb_app" \
  -c "ALTER DATABASE pb RENAME TO pb_before_ctype" \
  -c "ALTER DATABASE pb_ctype RENAME TO pb"
docker compose up -d
```

**`GRANT CONNECT` を忘れない。** DB 単位の接続権限はダンプに入らないので、付けないと PB がつながらない。

### 6.2 Kubernetes（`k8s/db.yaml` のサンプルの DB）

```
kubectl scale -n <ns> deployment/pb --replicas=0
kubectl exec -n <ns> pb-db-0 -- pg_dump -U pb_owner -Fc pb > pb-before-ctype.dump
kubectl exec -n <ns> pb-db-0 -- psql -U pb_owner -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE pb_ctype OWNER pb_owner ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C.UTF-8' TEMPLATE template0"
kubectl exec -i -n <ns> pb-db-0 -- pg_restore -U pb_owner -d pb_ctype --exit-on-error < pb-before-ctype.dump
kubectl exec -n <ns> pb-db-0 -- psql -U pb_owner -d postgres -v ON_ERROR_STOP=1 \
  -c "GRANT CONNECT ON DATABASE pb_ctype TO pb_app" \
  -c "ALTER DATABASE pb RENAME TO pb_before_ctype" \
  -c "ALTER DATABASE pb_ctype RENAME TO pb"
kubectl scale -n <ns> deployment/pb --replicas=1
```

**この手順は Kubernetes では確かめていない**（同じ SQL を compose の DB で通した）。

### 6.3 native・外部の PostgreSQL

6.1 と同じ SQL を、`psql -h <ホスト> -U pb_owner -d postgres` で流す（ダンプと復元は `pg_dump -h …` / `pg_restore -h …`）。**`C.UTF-8` が無い OS の PostgreSQL では `CREATE DATABASE` が失敗する。** その場合は、サーバにある UTF-8 のロケール（`locale -a` で探す。例：`ja_JP.UTF-8`、`en_US.UTF-8`）を `LC_CTYPE` に指定する。`LC_COLLATE` は `C` のままにする。**mac の PostgreSQL とクラウドの PostgreSQL では確かめていない。**

### 6.4 確かめる・戻す・片付ける

**確かめる**

```
SELECT datname, datcollate, datctype FROM pg_database WHERE datname LIKE 'pb%';
```

`pb` が `C` / `C.UTF-8`、`pb_before_ctype` が `C` / `C` になっていればよい。PB を起動して、**ログに 6章冒頭の警告が出ないこと**、ログインしてチケットとコメントが見えることを確かめる。

**戻す**（PB を止めてから）

```
ALTER DATABASE pb RENAME TO pb_ctype;
ALTER DATABASE pb_before_ctype RENAME TO pb;
```

**片付ける**：確かめ終えたら `DROP DATABASE pb_before_ctype;` とダンプを消す。

