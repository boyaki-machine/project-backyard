#!/usr/bin/env bash
# Docker自身の除外処理で、秘密がビルド先へ転送されないことを確かめる。
# 実在の秘密は読まず、作業用ディレクトリのダミーファイルだけを送る。
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "${fixture}"' EXIT
mkdir -p "${fixture}/context" "${fixture}/out"
cp "${repo_root}/.dockerignore" "${fixture}/context/.dockerignore"
allowed=(VERSION LICENSE NOTICE THIRD_PARTY_NOTICES.txt client/package.json client/src/main.ts server/go.mod server/internal/webui/dist/placeholder.html deploy/build-bridges.sh)
blocked=(client/.npmrc client/.netrc server/.aws/credentials client/.ssh/id_ed25519 client/.docker/config.json server/store.pfx client/store.jks .env .codex/config.toml LEARNINGS_CODEX.md client/.env client/.env.local client/.envrc client/secrets/token client/private.key server/test.pem server/.mcp.json client/node_modules/library.js client/dist/index.html server/internal/webui/dist/index.html deploy/dev/secrets/db_password deploy/stg/pb.env deploy/prod/native/secrets/pgpass)
for name in "${allowed[@]}" "${blocked[@]}"; do
    mkdir -p "$(dirname "${fixture}/context/${name}")"
    printf '%s\n' 'synthetic test fixture' > "${fixture}/context/${name}"
done
printf 'FROM scratch\nCOPY . /\n' > "${fixture}/Dockerfile"
docker build --quiet --file "${fixture}/Dockerfile" --output "type=local,dest=${fixture}/out" "${fixture}/context"
for name in "${allowed[@]}"; do
    if [ ! -f "${fixture}/out/${name}" ]; then
        echo "NG: ビルドに必要なファイルが除外された: ${name}" >&2
        exit 1
    fi
done
for name in "${blocked[@]}"; do
    if [ -e "${fixture}/out/${name}" ]; then
        echo "NG: ビルドへ送ってはいけないファイルが入った: ${name}" >&2
        exit 1
    fi
done
echo "OK: 必要なソースを含み、秘密と生成物は除外されている"
