#!/usr/bin/env bash
#
# stg（ドッグフーディング用インスタンス）の初回セットアップ（Design.md 4.4）。
#
#   秘密を乱数で生成 → 設定を用意 → DB を起動 → migrate
#
# **何度実行しても壊れない。** 既にあるものは作り直さず、その旨を報告して次へ進む。
# 秘密を作り直すと DB のロールと食い違って接続できなくなるため、上書きは行わない。
#
# 処理を Makefile ではなくここに置いているのは、生成・待ち合わせ・条件分岐を
# 含む手続きだからである（deploy/dev/reset.sh と同じ理由）。
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
stg_dir="${repo_root}/deploy/stg"
secrets_dir="${stg_dir}/secrets"

# Makefile から渡される。単体で叩かれた場合に備えて既定値を持つ。
COMPOSE=${COMPOSE:-"docker compose -f ${repo_root}/deploy/base/compose.yaml -f ${repo_root}/deploy/stg/compose.yaml"}

# 32文字の16進。openssl は macOS / Linux のどちらにも入っている。
gen_password() { openssl rand -hex 16; }

echo "make stg-init — ドッグフーディング用インスタンスの初回セットアップ"
echo

# ── 1. 秘密 ───────────────────────────────────────────────
mkdir -p "${secrets_dir}"
chmod 700 "${secrets_dir}"

if [ -f "${secrets_dir}/db_password" ]; then
	echo "==> 秘密は既にある（作り直さない）"
else
	echo "==> 秘密を乱数で生成する"
	owner_password=$(gen_password)
	app_password=$(gen_password)

	printf '%s\n' "${owner_password}" >"${secrets_dir}/db_password"
	printf '%s\n' "${app_password}" >"${secrets_dir}/app_db_password"
	# **接続文字列はホストから見た 127.0.0.1:5433 を指す。** dev の
	# app_database_url がコンテナ内から見た db:5432 なのと違い、stg の PB は
	# コンテナではなくネイティブに動く（Design.md 4.4）。
	printf '%s\n' \
		"postgres://pb_app:${app_password}@127.0.0.1:5433/pb?sslmode=disable&application_name=pb" \
		>"${secrets_dir}/app_database_url"

	chmod 600 "${secrets_dir}"/*
	echo "    deploy/stg/secrets/ に3件（db_password / app_db_password / app_database_url）"
fi

# ── 2. 設定 ───────────────────────────────────────────────
if [ -f "${stg_dir}/pb.env" ]; then
	echo "==> pb.env は既にある（上書きしない）"
else
	echo "==> pb.env をテンプレートから作る"
	cp "${stg_dir}/pb.env.example" "${stg_dir}/pb.env"
fi

# ── 3. DB ─────────────────────────────────────────────────
echo
echo "==> DB を起動して healthy になるまで待つ（compose プロジェクト pb-stg / :5433）"
${COMPOSE} up -d --wait db

# ── 4. スキーマ ───────────────────────────────────────────
echo
echo "==> マイグレーションを適用する"
make -C "${repo_root}" stg-migrate

echo
echo "完了。次にやること:"
echo "    make stg-admin-create   # 初期管理者を対話的に作る（DbDesign.md 7.5）"
echo "    make stg-build          # 動作に必要な一式を deploy/stg/out/ へ出力する"
echo "    make stg-run            # 起動（http://localhost:8081）"
