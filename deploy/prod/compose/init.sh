#!/usr/bin/env bash
#
# 秘密を乱数で作る（make release TARGET=compose が同梱した）。使い方は MANUAL.md の4章。
#
#   ./init.sh
#
# **最初の1回だけ実行する。既にある秘密は作り直さない。** 作り直すと、DB に登録済みの
# パスワードと食い違って繋がらなくなる（DB のパスワードは pgdata が空の初回起動でしか決まらない）。
#
# 権限は「ディレクトリ 700・ファイル 644」にする（MANUAL.md 4.3）。
#   - ディレクトリ 700：この端末の他のユーザを閉め出す
#   - ファイル 644：コンテナの中の利用者（pb は uid 65532、DB は postgres）が読めるようにする。
#     docker compose は secrets の uid・mode の指定を無視するので、ファイルの権限で開けるしかない
set -euo pipefail

# 自分のあるディレクトリへ移る。compose.yaml は ./secrets/ を参照する。
cd "$(dirname "${BASH_SOURCE[0]}")"

names="db_password app_db_password app_database_url pgpass"

existing=""
for n in ${names}; do
	if [ -e "secrets/${n}" ]; then
		existing="${existing} ${n}"
	fi
done
if [ -n "${existing}" ]; then
	echo "秘密は既にある（作り直さない）:${existing}"
	echo "作り直すなら、DB のボリュームごと消してから secrets/ を消す（MANUAL.md 4.4。データも消える）。"
	exit 0
fi

mkdir -p secrets
chmod 700 secrets
# ファイルは作った瞬間から 644 にする。ディレクトリが先に 700 なので、他のユーザからは見えない。
umask 022

# 32文字の16進。openssl は macOS / Linux のどちらにも入っている。
owner_password=$(openssl rand -hex 16)
app_password=$(openssl rand -hex 16)

# printf はシェルの組み込みなので、パスワードがコマンドの引数として他のプロセスから見えない。
printf '%s\n' "${owner_password}" >secrets/db_password
printf '%s\n' "${app_password}" >secrets/app_db_password
printf 'postgres://pb_app:%s@db:5432/pb?sslmode=disable&application_name=pb\n' "${app_password}" >secrets/app_database_url
printf '*:*:*:pb_owner:%s\n' "${owner_password}" >secrets/pgpass
chmod 644 secrets/db_password secrets/app_db_password secrets/app_database_url secrets/pgpass

echo "秘密を作った: secrets/（db_password / app_db_password / app_database_url / pgpass）"
echo "次は docker compose up -d（MANUAL.md 4.4）。"
