#!/usr/bin/env bash
#
# PB のスキーマを進める（make release TARGET=docker が同梱した）。使い方は MANUAL.md の4章。
#
#   ./migrate.sh           未適用のマイグレーションをすべて適用する（goose up）
#   ./migrate.sh status    適用状況を表示する
#
# イメージの中の goose を、その場で終わるコンテナで動かす。接続先は migrate.conf に、
# pb_owner のパスワードは secrets/pgpass（PostgreSQL 標準の passfile）に置く。
# **パスワードを環境変数の値にもコマンドライン引数にも出さない。** コンテナへ渡すのは
# passfile そのものと、その位置（PGPASSFILE）だけで、中身は goose（が使う pgx）が読む。
#
# 環境変数 PB_DOCKER_NETWORK は run.sh と同じ（PostgreSQL もコンテナで動いているときに使う）。
set -euo pipefail

# 自分のあるディレクトリへ移る（run.sh と同じ理由）。
cd "$(dirname "${BASH_SOURCE[0]}")"

image="__PB_IMAGE__"

if [ ! -f ./migrate.conf ]; then
	echo "エラー: migrate.conf が無い。" >&2
	exit 1
fi

# コメントと空行を除いた最初の行を、接続先として読む。
url=$(awk '!/^[[:space:]]*(#|$)/ { gsub(/[[:space:]]/, ""); print; exit }' ./migrate.conf)
if [ -z "${url}" ]; then
	echo "エラー: migrate.conf に接続先が書かれていない。" >&2
	exit 1
fi

# **接続先にパスワードを書かせない。** 書くと docker の引数に載り、ps で他のユーザから見える。
password_in_url='://[^/@]*:[^/@]*@'
if [[ ${url} =~ ${password_in_url} ]]; then
	echo "エラー: migrate.conf の接続先にパスワードが含まれている。パスワードは secrets/pgpass に書くこと（MANUAL.md「4.6 docker 単体で動かす」）。" >&2
	exit 1
fi

if [ ! -f ./secrets/pgpass ]; then
	echo "エラー: secrets/pgpass が無い。MANUAL.md「4.6 docker 単体で動かす」を読むこと。" >&2
	exit 1
fi

args=(
	--add-host=host.docker.internal:host-gateway
	-v "${PWD}/secrets/pgpass:/run/secrets/pgpass:ro"
	-e PGPASSFILE=/run/secrets/pgpass
)
if [ -n "${PB_DOCKER_NETWORK:-}" ]; then
	args+=(--network "${PB_DOCKER_NETWORK}")
fi

if [ "$#" -eq 0 ]; then
	set -- up
fi
exec docker run --rm "${args[@]}" --entrypoint /goose "${image}" -dir /migrations postgres "${url}" "$@"
