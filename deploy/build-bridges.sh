#!/usr/bin/env bash
# Build every client-side bridge supported by the release matrix. The PB
# server's own OS and CPU do not determine the platform of a downloading agent.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out_dir=${1:?出力先を指定してください}
mkdir -p "${out_dir}"
out_dir=$(cd "${out_dir}" && pwd)

for os_name in darwin windows linux; do
	for arch in amd64 arm64; do
		dir="${out_dir}/${os_name}-${arch}"
		mkdir -p "${dir}"
		name=pb-mcp-bridge
		if [ "${os_name}" = windows ]; then name=pb-mcp-bridge.exe; fi
		echo "==> ${os_name}/${arch} の ${name} を作る"
		CGO_ENABLED=0 GOOS="${os_name}" GOARCH="${arch}" go -C "${repo_root}/server" build \
			-trimpath -ldflags "-s -w" -o "${dir}/${name}" ./cmd/pb-mcp-bridge
		if [ "${os_name}" != windows ]; then chmod +x "${dir}/${name}"; fi
	done
done
