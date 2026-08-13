#!/usr/bin/env bash
#
# 開発用：DBを作り直してデモデータを投入する（DbDesign.md 7.6.6）。
#
#   コンテナとボリュームを破棄 → 起動 → migrate → dev seed
#
# **docker compose down -v を含む。** pgdata ボリュームごと消えるため、
# 実行前に確認を求める。PB_YES=1 を渡すと確認を省略する（非対話の検証用）。
#
# 処理を Makefile ではなくここに置いているのは、確認と待ち合わせを含む
# 手続きだからである。migrate / dev-seed は Makefile 側の定義を呼ぶだけにして、
# 接続文字列の組み立てを二重に持たない。
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

# Makefile から渡される。単体で叩かれた場合に備えて既定値を持つ。
COMPOSE=${COMPOSE:-"docker compose -f deploy/base/compose.yaml"}

echo "make dev-reset — 開発用DBの作り直し"
echo
echo "  次の操作を行います。**DBの中身はすべて消えます。**"
echo "    1. ${COMPOSE} down -v（pgdata ボリュームを破棄）"
echo "    2. DBを起動して healthy になるまで待つ"
echo "    3. make migrate"
echo "    4. make dev-seed"
echo

if [ "${PB_YES:-}" != "1" ]; then
	if [ ! -t 0 ]; then
		echo "中止しました（非対話で実行する場合は PB_YES=1 を指定してください）" >&2
		exit 1
	fi
	read -r -p "続行しますか？ [y/N]: " answer
	case "$answer" in
	y | Y | yes | YES) ;;
	*)
		echo "中止しました。"
		exit 1
		;;
	esac
fi

echo
echo "==> コンテナとボリュームを破棄する"
${COMPOSE} down -v

echo
echo "==> DBを起動して healthy になるまで待つ"
${COMPOSE} up -d --wait db

echo
echo "==> マイグレーションを適用する"
make migrate

echo
echo "==> デモデータを投入する"
make dev-seed
