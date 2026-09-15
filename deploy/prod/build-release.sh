#!/usr/bin/env bash
#
# リリース用の一式を出力する（Design.md 4.5）。使い方は deploy/prod/MANUAL.md の2章。
#
#   make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]
#   make release TARGET=<docker|compose|k8s> ARCH=<amd64|arm64> [OUT=<出力先>] [PUSH=<レジストリ>/<名前>:<タグ>]
#   deploy/prod/build-release.sh --target <…> [--os <…>] --arch <…> [--out <出力先>] [--push <…>]
#
# **出力は「配置すれば動く一式」である**（stg の build.sh と同じ考え方。Design.md 4.4）。
# 同梱のスクリプトは自分の位置へ移ってから動くので、一式を丸ごと任意のパスへ置ける。
#
# TARGET は native（pb-122）と docker / compose（pb-123）と k8s（pb-124）。
# **CPU は 64bit の2種だけを受ける**（利用者の判断、2026-09-13。pb-4）。
#
# **macOS 標準の bash 3.2 でも動くように書く。** ${var,,} や連想配列を使わない。
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
prod_dir="${repo_root}/deploy/prod"
native_dir="${prod_dir}/native"
docker_dir="${prod_dir}/docker"
compose_dir="${prod_dir}/compose"
k8s_dir="${prod_dir}/k8s"

# **goose には postgres のドライバだけを入れる。** 全ドライバ入りは約 38MB、絞ると
# 11〜12MB になる（pb-4 で実測）。版は server/tools/go.mod が固定しているので
# （DbDesign.md 5.1）、ここには書かない。**コンテナイメージにも --build-arg で渡す。**
goose_tags="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb"

usage() {
	cat <<'USAGE'
使い方:
  make release TARGET=native OS=<darwin|windows|linux> ARCH=<amd64|arm64> [OUT=<出力先>]
  make release TARGET=<docker|compose|k8s> ARCH=<amd64|arm64> [OUT=<出力先>] [PUSH=<レジストリ>/<名前>:<タグ>]

  TARGET  native（実行ファイルと起動スクリプト）/ docker（イメージと docker run の例）/
          compose（イメージと compose 一式）/ k8s（イメージとマニフェスト一式）
  OS      darwin（mac と書いてもよい）/ windows / linux。docker / compose / k8s は linux だけで、省いてよい
  ARCH    amd64（x64 / x86_64 / x86）/ arm64（m1 / arm / aarch64）。32bit 向けには出力しない
  OUT     出力先。省略すると dist/pb-v<版>-<TARGET>-<OS>-<ARCH>。空でないディレクトリには書かない
  PUSH    docker / compose / k8s だけ。イメージを tar に出す代わりに、このレジストリへ送る
USAGE
}

# **使い方の誤りは 2 で終える。** ビルドそのものの失敗（1）と区別するため。
die() {
	printf 'エラー: %s\n' "$1" >&2
	printf '使い方は deploy/prod/MANUAL.md の2章（--help でも出る）\n' >&2
	exit 2
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

target="" os="" arch="" out="" push=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--target | --os | --arch | --out | --push)
		[ "$#" -ge 2 ] || die "$1 に値を指定すること"
		case "$1" in
		--target) target=$2 ;;
		--os) os=$2 ;;
		--arch) arch=$2 ;;
		--out) out=$2 ;;
		--push) push=$2 ;;
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
docker) target=docker ;;
compose) target=compose ;;
k8s) target=k8s ;;
"") die "TARGET を指定すること（native / docker / compose / k8s）" ;;
*) die "知らない TARGET: ${target}（native / docker / compose / k8s を指定できる）" ;;
esac

container=false
case "${target}" in
docker | compose | k8s) container=true ;;
esac

case "$(lower "${os}")" in
darwin | mac) os=darwin ;;
windows) os=windows ;;
linux) os=linux ;;
"")
	if [ "${container}" = true ]; then
		os=linux
	else
		die "OS を指定すること（darwin / windows / linux）"
	fi
	;;
*) die "知らない OS: ${os}（darwin / windows / linux を指定できる）" ;;
esac
if [ "${container}" = true ] && [ "${os}" != linux ]; then
	die "TARGET=${target} の行き先は linux だけ（コンテナは Linux で動く）。OS は省くか linux を指定すること"
fi

# **x86 は amd64 と読む**（利用者の判断。「x64 と x86 を amd64 として読む」）。
# 32bit を名指しする表記だけを拒む。
case "$(lower "${arch}")" in
amd64 | x64 | x86_64 | x86) arch=amd64 ;;
arm64 | m1 | arm | aarch64) arch=arm64 ;;
386 | i386 | i686 | armv6 | armv7 | armhf) die "32bit の CPU（${arch}）向けには出力しない。amd64 か arm64 を指定すること" ;;
"") die "ARCH を指定すること（amd64 / arm64）" ;;
*) die "知らない ARCH: ${arch}（amd64 / arm64 を指定できる）" ;;
esac

if [ -n "${push}" ]; then
	if [ "${container}" != true ]; then
		die "PUSH は TARGET=docker / compose / k8s でだけ使える"
	fi
	case "${push}" in
	*[[:space:]]*) die "PUSH に空白を含めない: ${push}" ;;
	esac
fi

version=$(cat "${repo_root}/VERSION")
out=${out:-"${repo_root}/dist/pb-v${version}-${target}-${os}-${arch}"}

# **空でない出力先には書かない。** 利用者が書き換えた設定や起動スクリプトが黙って元に
# 戻るうえ、動いている一式のバイナリを同じパスへ書き直すと、実行中のプロセスが
# まだ読み込んでいないページが入れ替わって落ちうる。
if [ -e "${out}" ] && [ ! -d "${out}" ]; then
	die "出力先がディレクトリではない: ${out}"
fi
if [ -d "${out}" ] && [ -n "$(ls -A "${out}")" ]; then
	die "出力先が空でない: ${out}（別の出力先を指定するか、消してから作り直すこと）"
fi

if [ "${container}" = true ] && ! docker buildx version >/dev/null 2>&1; then
	echo "エラー: docker buildx が使えない。コンテナランタイムが起動しているか確かめること。" >&2
	exit 1
fi

# ── native（pb-122）─────────────────────────────────────────
build_native() {
	local exe=""
	if [ "${os}" = windows ]; then
		exe=".exe"
	fi

	echo "==> client をビルドして embed 対象へ同期する"
	make -C "${repo_root}" sync-webui

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
}

# ── docker / compose（pb-123）・k8s（pb-124）─────────────────
# build_image はイメージを作り、一式の雛形に埋める参照を image_ref に入れる。
image_ref=""
build_image() {
	echo "==> コンテナイメージを作る（v${version}、linux/${arch}）"
	# **ビルドはイメージの中で行う**（deploy/Dockerfile）。この端末に Go と Node は要らない。
	if [ -n "${push}" ]; then
		image_ref=${push}
		docker buildx build --platform "linux/${arch}" -f "${repo_root}/deploy/Dockerfile" \
			--build-arg "VERSION=${version}" --build-arg "GOOSE_TAGS=${goose_tags}" \
			--tag "${push}" --push "${repo_root}"
	else
		image_ref="project-backyard:${version}"
		# **type=docker の tar にする**（利用者の判断に異論なし、2026-09-15）。docker load のほか、
		# nerdctl や podman も読める。
		docker buildx build --platform "linux/${arch}" -f "${repo_root}/deploy/Dockerfile" \
			--build-arg "VERSION=${version}" --build-arg "GOOSE_TAGS=${goose_tags}" \
			--output "type=docker,dest=${out}/project-backyard-${version}-linux-${arch}.tar,name=${image_ref}" \
			"${repo_root}"
	fi
}

# fill_image は雛形の __PB_IMAGE__ をイメージの参照に置き換えて書き出す。
fill_image() {
	sed "s|__PB_IMAGE__|${image_ref}|g" "$1" >"$2"
}

# fill_initdb は db.yaml の __INITDB_ROLES_SH__ の行を、ロール作成のスクリプトで置き換える。
# **正本は deploy/base/initdb/01_roles.sh**（DbDesign.md 3.4）で、ConfigMap のブロックへ4桁下げて埋める。
# 空行は字下げしない（YAML のブロックでは、空行の字下げは意味を持たない）。
fill_initdb() {
	awk -v script="${repo_root}/deploy/base/initdb/01_roles.sh" '
		/^__INITDB_ROLES_SH__$/ {
			while ((getline line < script) > 0) {
				if (line == "") print ""; else print "    " line
			}
			close(script)
			next
		}
		{ print }
	' "$1" >"$2"
}

build_container() {
	build_image

	echo
	echo "==> 雛形を置く（TARGET=${target}）"
	cp "${prod_dir}/MANUAL.md" "${out}/MANUAL.md"
	# 用意済みの PostgreSQL にロールを作る SQL。compose と k8s でも、外部の PostgreSQL へ繋ぐときに使う。
	cp "${native_dir}/create-roles.sql" "${out}/create-roles.sql"

	case "${target}" in
	compose)
		fill_image "${compose_dir}/compose.yaml" "${out}/compose.yaml"
		cp "${compose_dir}/init.sh" "${out}/init.sh"
		chmod +x "${out}/init.sh"
		# DB の初回起動で pb_app のロールを作る。**正本は deploy/base/initdb/01_roles.sh**（DbDesign.md 3.4）。
		# 実行ビットが無いと postgres の entrypoint が source する形になるので、755 で置く。
		mkdir -p "${out}/initdb"
		cp "${repo_root}/deploy/base/initdb/01_roles.sh" "${out}/initdb/01_roles.sh"
		chmod 755 "${out}/initdb" "${out}/initdb/01_roles.sh"
		;;
	docker)
		fill_image "${docker_dir}/run.sh" "${out}/run.sh"
		fill_image "${docker_dir}/migrate.sh" "${out}/migrate.sh"
		chmod +x "${out}/run.sh" "${out}/migrate.sh"
		cp "${docker_dir}/migrate.conf" "${out}/migrate.conf"
		mkdir -p "${out}/secrets"
		chmod 700 "${out}/secrets"
		cp "${docker_dir}"/secrets/*.example "${out}/secrets/"
		;;
	k8s)
		# **k8s/ には apply してよいものだけを置く。** Secret の雛形は値が入っていないので、
		# kubectl apply -f k8s/ で紛れ込まないよう一式の直下に置く。
		mkdir -p "${out}/k8s"
		fill_image "${k8s_dir}/app.yaml" "${out}/k8s/app.yaml"
		fill_initdb "${k8s_dir}/db.yaml" "${out}/k8s/db.yaml"
		cp "${k8s_dir}/secret.example.yaml" "${out}/secret.example.yaml"
		;;
	esac
}

mkdir -p "${out}"
# 相対で渡されても絶対パスに直す。以降のコマンドを cwd に依存させないため。
out=$(cd "${out}" && pwd)

case "${target}" in
native)
	build_native
	chapter="3章（native）"
	;;
docker | compose)
	build_container
	chapter="4章（コンテナ）"
	;;
k8s)
	build_container
	chapter="5章（Kubernetes）"
	;;
esac

echo
echo "完了: ${out}"
(cd "${out}" && find . -type f | LC_ALL=C sort)
if [ "${container}" = true ]; then
	echo
	echo "イメージ: ${image_ref}"
fi
echo
echo "次は ${out}/MANUAL.md の${chapter}を読む。"
