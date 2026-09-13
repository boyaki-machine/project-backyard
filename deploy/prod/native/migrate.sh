#!/usr/bin/env bash
#
# PB のスキーマを進める（make release が同梱した）。使い方は MANUAL.md の3章。
#
#   ./migrate.sh           未適用のマイグレーションをすべて適用する（goose up）
#   ./migrate.sh status    適用状況を表示する
#
# 接続先は migrate.conf に、pb_owner のパスワードは secrets/pgpass に置く。
# **パスワードを環境変数の値にもコマンドライン引数にも出さない。** 渡すのは
# passfile の位置（PGPASSFILE）だけで、中身は goose（が使う pgx）が自分で読む。
set -euo pipefail

# 自分のあるディレクトリへ移る（run.sh と同じ理由）。
cd "$(dirname "${BASH_SOURCE[0]}")"

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

# **接続先にパスワードを書かせない。** 書くと goose の引数に載り、ps で他のユーザから見える。
# ://ユーザ:パスワード@ の形（/ と @ をまたがない）だけを拾う。
password_in_url='://[^/@]*:[^/@]*@'
if [[ ${url} =~ ${password_in_url} ]]; then
	echo "エラー: migrate.conf の接続先にパスワードが含まれている。パスワードは secrets/pgpass に書くこと（MANUAL.md「3.4 秘密を置く」）。" >&2
	exit 1
fi

if [ ! -f ./secrets/pgpass ]; then
	echo "エラー: secrets/pgpass が無い。MANUAL.md「3.4 秘密を置く」を読むこと。" >&2
	exit 1
fi
# **他のユーザから読める passfile は警告する。** psql はそういう passfile を無視するが、
# goose が使う pgx は権限を見ずに読む。
if [ -n "$(find ./secrets/pgpass -perm -004 -o -perm -040)" ]; then
	echo "警告: secrets/pgpass が他のユーザから読める。chmod 600 secrets/pgpass で閉じること。" >&2
fi

export PGPASSFILE="${PWD}/secrets/pgpass"

if [ "$#" -eq 0 ]; then
	set -- up
fi
exec ./goose -dir ./migrations postgres "${url}" "$@"
