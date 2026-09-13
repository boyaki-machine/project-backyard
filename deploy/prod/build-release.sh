#!/usr/bin/env bash
#
# リリース用の一式を出力する（Design.md 4.5）。使い方は deploy/prod/MANUAL.md の2章。
#
#   make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]
#   deploy/prod/build-release.sh --target native --os <…> --arch <…> [--out <出力先>]
#
# **出力は「配置すれば動く一式」である**（stg の build.sh と同じ考え方。Design.md 4.4）。
# 同梱のスクリプトは自分の位置へ移ってから動くので、一式を丸ごと任意のパスへ置ける。
#
# **いま実装している TARGET は native だけである**（docker / compose は pb-123、k8s は pb-124）。
# **CPU は 64bit の2種だけを受ける**（利用者の判断、2026-09-13。pb-4）。
#
# **macOS 標準の bash 3.2 でも動くように書く。** ${var,,} や連想配列を使わない。
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
prod_dir="${repo_root}/deploy/prod"
native_dir="${prod_dir}/native"

# **goose には postgres のドライバだけを入れる。** 全ドライバ入りは約 38MB、絞ると
# 11〜12MB になる（pb-4 で実測）。版は server/tools/go.mod が固定しているので
# （DbDesign.md 5.1）、ここには書かない。
goose_tags="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb"

usage() {
	cat <<'USAGE'
使い方:
  make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]

  TARGET  native（実行ファイルと起動スクリプト）。docker / compose / k8s はまだ無い
  OS      darwin（mac と書いてもよい）/ windows / linux
  ARCH    amd64（x64 / x86_64 / x86）/ arm64（m1 / arm / aarch64）。32bit 向けには出力しない
  OUT     出力先。省略すると dist/pb-v<版>-<TARGET>-<OS>-<ARCH>。空でないディレクトリには書かない
USAGE
}

# **使い方の誤りは 2 で終える。** ビルドそのものの失敗（1）と区別するため。
die() {
	printf 'エラー: %s\n' "$1" >&2
	printf '使い方は deploy/prod/MANUAL.md の2章（--help でも出る）\n' >&2
	exit 2
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

target="" os="" arch="" out=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--target | --os | --arch | --out)
		[ "$#" -ge 2 ] || die "$1 に値を指定すること"
		case "$1" in
		--target) target=$2 ;;
		--os) os=$2 ;;
		--arch) arch=$2 ;;
		--out) out=$2 ;;
		esac
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "知らない引数: $1" ;;
	esac
done

case "$(lower "${target}")" in
native) target=native ;;
docker | compose | k8s) die "TARGET=${target} はまだ実装していない（いま指定できるのは native）" ;;
"") die "TARGET を指定すること（いま指定できるのは native）" ;;
*) die "知らない TARGET: ${target}（いま指定できるのは native）" ;;
esac

case "$(lower "${os}")" in
darwin | mac) os=darwin ;;
windows) os=windows ;;
linux) os=linux ;;
"") die "OS を指定すること（darwin / windows / linux）" ;;
*) die "知らない OS: ${os}（darwin / windows / linux を指定できる）" ;;
esac

# **x86 は amd64 と読む**（利用者の判断。「x64 と x86 を amd64 として読む」）。
# 32bit を名指しする表記だけを拒む。
case "$(lower "${arch}")" in
amd64 | x64 | x86_64 | x86) arch=amd64 ;;
arm64 | m1 | arm | aarch64) arch=arm64 ;;
386 | i386 | i686 | armv6 | armv7 | armhf) die "32bit の CPU（${arch}）向けには出力しない。amd64 か arm64 を指定すること" ;;
"") die "ARCH を指定すること（amd64 / arm64）" ;;
*) die "知らない ARCH: ${arch}（amd64 / arm64 を指定できる）" ;;
esac

version=$(cat "${repo_root}/VERSION")
out=${out:-"${repo_root}/dist/pb-v${version}-${target}-${os}-${arch}"}

# **空でない出力先には書かない。** 利用者が書き換えた pb.yaml や run.sh が黙って元に
# 戻るうえ、動いている一式のバイナリを同じパスへ書き直すと、実行中のプロセスが
# まだ読み込んでいないページが入れ替わって落ちうる。
if [ -e "${out}" ] && [ ! -d "${out}" ]; then
	die "出力先がディレクトリではない: ${out}"
fi
if [ -d "${out}" ] && [ -n "$(ls -A "${out}")" ]; then
	die "出力先が空でない: ${out}（別の出力先を指定するか、消してから作り直すこと）"
fi

exe=""
if [ "${os}" = windows ]; then
	exe=".exe"
fi

echo "==> client をビルドして embed 対象へ同期する"
make -C "${repo_root}" sync-webui

mkdir -p "${out}"
# 相対で渡されても絶対パスに直す。以降のコマンドを cwd に依存させないため。
out=$(cd "${out}" && pwd)

echo
echo "==> pb を作る（v${version}、${os}/${arch}）"
# CGO_ENABLED=0 で静的バイナリになる（Design.md 4.5）。ldflags は Makefile の LDFLAGS と揃える。
GOOS=${os} GOARCH=${arch} CGO_ENABLED=0 go -C "${repo_root}/server" build \
	-trimpath -ldflags "-s -w -X main.version=${version}" \
	-o "${out}/pb${exe}" ./cmd/pb

echo "==> goose を作る（postgres のドライバだけ）"
GOOS=${os} GOARCH=${arch} CGO_ENABLED=0 go -C "${repo_root}/server/tools" build \
	-tags "${goose_tags}" -trimpath -ldflags "-s -w" \
	-o "${out}/goose${exe}" github.com/pressly/goose/v3/cmd/goose

echo "==> マイグレーションと雛形を置く"
mkdir -p "${out}/migrations"
cp "${repo_root}"/server/migrations/*.sql "${out}/migrations/"

cp "${prod_dir}/MANUAL.md" "${out}/MANUAL.md"
cp "${native_dir}/pb.yaml" "${native_dir}/migrate.conf" "${native_dir}/create-roles.sql" "${out}/"

mkdir -p "${out}/secrets"
chmod 700 "${out}/secrets"
cp "${native_dir}"/secrets/*.example "${out}/secrets/"

case "${os}" in
windows)
	# **Windows PowerShell 5.1 は BOM の無い .ps1 を ANSI（日本語環境では Shift_JIS）として読む。**
	# 日本語のコメントと文言が化けて構文が壊れうるので、UTF-8 の BOM を付けて置く。
	# リポジトリ側に BOM を持たせないのは、差分と grep を素直に保つため。
	for f in run.ps1 migrate.ps1; do
		{
			printf '\357\273\277'
			cat "${native_dir}/${f}"
		} >"${out}/${f}"
	done
	;;
*)
	cp "${native_dir}/run.sh" "${native_dir}/migrate.sh" "${out}/"
	chmod +x "${out}/run.sh" "${out}/migrate.sh" "${out}/pb" "${out}/goose"
	;;
esac

# 常駐の雛形。Windows はファイルを持たず、MANUAL.md にタスクスケジューラの手順を書く。
case "${os}" in
darwin)
	mkdir -p "${out}/launchd"
	cp "${native_dir}"/launchd/*.plist "${out}/launchd/"
	;;
linux)
	mkdir -p "${out}/systemd"
	cp "${native_dir}"/systemd/*.service "${out}/systemd/"
	;;
esac

echo
echo "完了: ${out}"
(cd "${out}" && find . -type f | LC_ALL=C sort)
echo
echo "次は ${out}/MANUAL.md の3章（native）を読む。"
