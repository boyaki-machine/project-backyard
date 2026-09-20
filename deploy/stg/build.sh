#!/usr/bin/env bash
#
# stg（ドッグフーディング用インスタンス）の実行一式を出力する（Design.md 4.4）。
#
#   deploy/stg/build.sh [出力先]        既定の出力先は deploy/stg/out
#
# **出力は「配置すれば動く一式」である。** 丸ごと別のパスへコピーして、その中の
# run.sh を実行すれば動く。設定ファイル内のパスはすべて出力ディレクトリからの
# 相対で書き、リポジトリの位置に依存させない。
#
#   out/
#   ├── pb                   client を embed した単一バイナリ
#   ├── pb.env               動作を規定する設定（deploy/stg/pb.env の写し）
#   ├── run.sh               pb.env を読んで pb serve を起動する
#   ├── secrets/
#   │   └── app_database_url pb_app での接続文字列（0600）
#   └── README.txt           起動・停止・URL・ログの見方
#
# **マイグレーションは含まない。** goose は server/tools/ のツールモジュールに
# あり（DbDesign.md 5.1）、スキーマを進めるのはリポジトリ側の作業である。
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
stg_dir="${repo_root}/deploy/stg"

out_dir=${1:-"${stg_dir}/out"}
# 相対で渡されても絶対パスに直す。以降のコマンドを cwd に依存させないため。
mkdir -p "${out_dir}"
out_dir=$(cd "${out_dir}" && pwd)

version=$(cat "${repo_root}/VERSION")

if [ ! -f "${stg_dir}/pb.env" ]; then
	echo "deploy/stg/pb.env が無い。先に make stg-init を実行すること。" >&2
	exit 1
fi
if [ ! -f "${stg_dir}/secrets/app_database_url" ]; then
	echo "deploy/stg/secrets/app_database_url が無い。先に make stg-init を実行すること。" >&2
	exit 1
fi

echo "==> client をビルドして embed 対象へ同期する"
make -C "${repo_root}" sync-webui

echo
echo "==> バイナリを作る（v${version} → ${out_dir}/pb）"
# CGO_ENABLED=0 で静的バイナリになる（Design.md 4.5）。
CGO_ENABLED=0 go -C "${repo_root}/server" build \
	-trimpath -ldflags "-s -w -X main.version=${version}" \
	-o "${out_dir}/pb" ./cmd/pb

echo "==> pb-mcp-bridge を作る"
CGO_ENABLED=0 go -C "${repo_root}/server" build \
	-trimpath -ldflags "-s -w" \
	-o "${out_dir}/pb-mcp-bridge" ./cmd/pb-mcp-bridge
chmod +x "${out_dir}/pb-mcp-bridge"

echo
echo "==> 設定と秘密を同梱する"
cp "${stg_dir}/pb.env" "${out_dir}/pb.env"
mkdir -p "${out_dir}/secrets"
chmod 700 "${out_dir}/secrets"
cp "${stg_dir}/secrets/app_database_url" "${out_dir}/secrets/app_database_url"
chmod 600 "${out_dir}/secrets/app_database_url"

echo "==> run.sh を生成する"
cat >"${out_dir}/run.sh" <<'RUNSH'
#!/usr/bin/env bash
#
# PB を起動する（deploy/stg/build.sh が生成した。直接編集しない）。
#
#   ./run.sh                             前景で起動する
#   nohup ./run.sh > pb.log 2>&1 &       背景で起動し、ログをファイルへ
#
# **pb を直に叩いても動かない。** pb.env は**シェルが読んで環境変数へ export する
# ファイル**であり、バイナリは開かない（Design.md 4.4）。pb.env を読み込む
# このスクリプトが起動の入口である。
#
# **設定ファイル（PB_CONFIG_FILE の YAML）はバイナリが直接読む**が、stg では
# 使っていない——第2層は画面から変える（Design.md 10.3）。
set -euo pipefail

# **自身のあるディレクトリへ移る。** pb.env の PB_DATABASE_URL_FILE は
# 出力ディレクトリからの相対パスなので、cwd が違うと解決できない。
cd "$(dirname "${BASH_SOURCE[0]}")"

set -a
# shellcheck disable=SC1091
. ./pb.env
set +a

exec ./pb serve
RUNSH
chmod +x "${out_dir}/run.sh"

echo "==> README.txt を生成する"
cat >"${out_dir}/README.txt" <<READMESH
PB ドッグフーディング用インスタンス（stg）— v${version}

deploy/stg/build.sh が出力した一式である。**丸ごと任意のパスへ置いて動く。**

起動
    ./run.sh                            前景
    nohup ./run.sh > pb.log 2>&1 &      背景（ログは pb.log へ）

停止
    前景なら Ctrl + C
    背景なら :8081 を掴んでいるプロセスを止める
        kill \$(lsof -ti tcp:8081 -sTCP:LISTEN)
    リポジトリからは make stg-stop でも止まる。

画面
    http://localhost:8081

    **127.0.0.1:8081 では開かないこと。** Cookie はポートを区別しないので、
    dev（127.0.0.1:8080）と同じホスト名で開くとログインセッションが
    上書きし合う（Design.md 4.4）。

動いているか
    curl -s http://localhost:8081/healthcheck
    → バージョンが出る（PB_HEALTH_SHOW_VERSION=true）

DB
    compose プロジェクト pb-stg（127.0.0.1:5433）。データは名前付きボリューム
    pb-stg_pgdata にあり、dev の make dev-reset では消えない。
    コンテナは restart: always なので、コンテナランタイムの起動時に自動で上がる。

設定
    pb.env を書き換えて起動し直せば効く。接続文字列は secrets/app_database_url に
    あり、pb.env にも環境変数の値にも現れない。

スキーマを進めるとき
    リポジトリ側で make stg-migrate を実行し、そのあと make stg-build で
    この一式を作り直す。goose はこの一式に含まれていない。
READMESH

echo
echo "完了: ${out_dir}"
ls -la "${out_dir}"
