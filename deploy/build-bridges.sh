#!/usr/bin/env bash
# Build every client-side bridge supported by the release matrix. The PB
# server's own OS and CPU do not determine the platform of a downloading agent.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out_dir=${1:?出力先を指定してください}
mkdir -p "${out_dir}"
out_dir=$(cd "${out_dir}" && pwd)

# A staging rebuild may reuse an output directory produced by the old matrix.
rm -f "${out_dir}/darwin-amd64/pb-mcp-bridge"
rmdir "${out_dir}/darwin-amd64" 2>/dev/null || true

for target in darwin-arm64 windows-amd64 windows-arm64 linux-amd64 linux-arm64; do
	os_name=${target%-*}
	arch=${target#*-}
	dir="${out_dir}/${target}"
	mkdir -p "${dir}"
	name=pb-mcp-bridge
	if [ "${os_name}" = windows ]; then name=pb-mcp-bridge.exe; fi
	echo "==> ${os_name}/${arch} の ${name} を作る"
	CGO_ENABLED=0 GOOS="${os_name}" GOARCH="${arch}" go -C "${repo_root}/server" build \
		-trimpath -ldflags "-s -w" -o "${dir}/${name}" ./cmd/pb-mcp-bridge
	if [ "${os_name}" != windows ]; then chmod +x "${dir}/${name}"; fi
done
