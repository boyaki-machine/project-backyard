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
5. Kubernetes
```

---

## 1. どの形で動かすか

| TARGET | 出力されるもの | DB | この版 |
|---|---|---|---|
| `native` | 実行ファイルと起動スクリプト（mac / Windows / Linux） | **用意済みの PostgreSQL に繋ぐ** | 使える |
| `docker` | コンテナイメージ | 用意済みの PostgreSQL に繋ぐ | まだ無い |
| `compose` | コンテナイメージと compose 一式 | DB コンテナを立てるサンプル付き | まだ無い |
| `k8s` | コンテナイメージとマニフェスト | DB を立てるサンプル付き | まだ無い |

**どの形でも、PB の配布物に DB は入っていない。**

---

## 2. 一式を作る（リポジトリで）

リポジトリの直下で実行する。Go と Node.js が要る（リポジトリの `docs/Development.md` 1章）。

```
make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]
```

| 引数 | 指定できる値 | 同じ意味に読む表記 |
|---|---|---|
| `TARGET` | `native` | — |
| `OS` | `darwin` / `windows` / `linux` | `mac` → `darwin` |
| `ARCH` | `amd64` / `arm64` | `x64`・`x86_64`・`x86` → `amd64`、`m1`・`arm`・`aarch64` → `arm64` |
| `OUT` | 出力先のディレクトリ | 省略すると `dist/pb-v<版>-<TARGET>-<OS>-<ARCH>` |

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
  （PB 本体が使う。テーブルを作れない・消せない）、DB `pb`（UTF8、照合順序 C）、拡張3つ
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
3. `run.sh`（Windows は `run.ps1`）の `PB_BIND=127.0.0.1:8080` を、外から届くアドレス
   （例：`0.0.0.0:8443`）に直して、起動し直す
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
#    run.sh の PB_BIND を直していたなら、新しい run.sh にも同じ変更を入れる

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
| `bind: address already in use` | 8080 番を別のプロセスが使っている。止めるか、`run.sh` の `PB_BIND` のポートを変える |
| ログインしても入れない（http で開いている） | 「Cookie に Secure を付ける」を http のまま有効にした。`PB_COOKIE_SECURE=false` を付けて起動し、画面で無効に戻す |
| 画面の「待受アドレス」を変えられない | `run.sh` が固定しているため（3.10） |

---

## 4. コンテナ（docker / compose）

**まだ用意していない。**

---

## 5. Kubernetes

**まだ用意していない。**
