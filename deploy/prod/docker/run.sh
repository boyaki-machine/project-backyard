#!/usr/bin/env bash
#
# PB をコンテナで起動する（make release TARGET=docker が同梱した）。使い方は MANUAL.md の4章。
#
#   ./run.sh                   背景で起動する（コンテナ名 pb。止めるのは docker stop pb）
#   ./run.sh admin create      初期管理者を作る（その場で終わるコンテナで動かす）
#
# **用意済みの PostgreSQL へ繋ぐ。** 接続文字列は secrets/app_database_url に置き、
# コンテナへはファイルとして渡す。パスワードは環境変数の値にも引数にも出ない。
#
# 環境変数で変えられるもの
#   PB_CONTAINER_NAME   コンテナ名（既定 pb）
#   PB_DOCKER_NETWORK   参加させる docker のネットワーク。PostgreSQL もコンテナで動いているときに使う
#
# **macOS 標準の bash 3.2 でも動くように書く。** set -u の下で空の配列を展開すると
# bash 3.2 は「未定義」で止まるので、空になりうる配列を使わない。
set -euo pipefail

# 自分のあるディレクトリへ移る。secrets/ の位置を一式からの相対で決めるため。
cd "$(dirname "${BASH_SOURCE[0]}")"

image="__PB_IMAGE__"
name=${PB_CONTAINER_NAME:-pb}

if [ ! -f ./secrets/app_database_url ]; then
	echo "エラー: secrets/app_database_url が無い。MANUAL.md「4.6 docker 単体で動かす」を読むこと。" >&2
	exit 1
fi

# 共通の引数。
#   --add-host：同じ端末の PostgreSQL へ host.docker.internal で繋げるようにする（Linux では既定で引けない）
#   -v：秘密をファイル1つずつ渡す。**ディレクトリごと渡すと、700 のディレクトリをコンテナの利用者が開けない**
args=(
	--add-host=host.docker.internal:host-gateway
	-v "${PWD}/secrets/app_database_url:/run/secrets/app_database_url:ro"
	-e PB_DATABASE_URL_FILE=/run/secrets/app_database_url
)
if [ -n "${PB_DOCKER_NETWORK:-}" ]; then
	args+=(--network "${PB_DOCKER_NETWORK}")
fi

if [ "$#" -eq 0 ]; then
	# **公開は 127.0.0.1 だけ。** 外へ出すなら、先に TLS を有効にする（MANUAL.md 3.10）。
	exec docker run -d --name "${name}" --restart unless-stopped \
		-p 127.0.0.1:8080:8080 "${args[@]}" "${image}"
fi

# 端末から叩いたときだけ -t を付ける。パイプで入力を流すときに付けると docker が失敗する。
if [ -t 0 ]; then
	exec docker run --rm -i -t "${args[@]}" "${image}" "$@"
fi
exec docker run --rm -i "${args[@]}" "${image}" "$@"
