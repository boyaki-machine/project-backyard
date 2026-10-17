#!/usr/bin/env bash
# DB初期化の途中を固定し、配布定義の実プローブが早まって成功しないことを確認する。
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
image=${1:-project-backyard-db:dev}
fixture=$(mktemp -d)
name="pb-db-readiness-test-$$"
volume="${name}-data"
cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker volume rm "$volume" >/dev/null 2>&1 || true
    rm -rf "$fixture"
}
trap cleanup EXIT
cat > "$fixture/00_hold.sh" <<'SH'
#!/bin/bash
set -eu
touch /tmp/pb-init-held
while [ ! -f /tmp/pb-init-release ]; do sleep 0.1; done
SH
chmod 755 "$fixture/00_hold.sh"
docker volume create "$volume" >/dev/null
docker run -d --name "$name" --network none \
    -e POSTGRES_USER=pb_owner -e POSTGRES_DB=pb -e POSTGRES_HOST_AUTH_METHOD=trust \
    -v "$volume:/var/lib/postgresql/data" \
    -v "$fixture/00_hold.sh:/docker-entrypoint-initdb.d/00_hold.sh:ro" "$image" >/dev/null
for ((attempt=0; attempt<90; attempt++)); do
    if docker exec "$name" test -f /tmp/pb-init-held >/dev/null 2>&1; then break; fi
    sleep 1
done
docker exec "$name" test -f /tmp/pb-init-held
probe() {
    python3 - "$repo_root" "$name" "$1" <<'PY'
import json, pathlib, subprocess, sys
root, name, phase = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
paths = ['deploy/base/compose.yaml', 'deploy/prod/compose/compose.yaml', 'deploy/prod/k8s/db.yaml']
for rel in paths:
    commands = [json.loads(line[line.index('['):line.rindex(']')+1]) for line in (root/rel).read_text().splitlines() if 'pg_isready' in line and '[' in line]
    assert commands, rel
    for cmd in commands:
        if cmd[0] == 'CMD-SHELL': cmd = ['sh', '-c', cmd[1]]
        elif cmd[0] == 'CMD': cmd = cmd[1:]
        result = subprocess.run(['docker', 'exec', name, *cmd], capture_output=True)
        expected = result.returncode != 0 if phase == 'initializing' else result.returncode == 0
        assert expected, f'{rel}: phase={phase}, probe exit={result.returncode}'
print('DB readiness:', phase, 'passed for every distribution probe.')
PY
}
probe initializing
docker exec "$name" touch /tmp/pb-init-release
for ((attempt=0; attempt<90; attempt++)); do
    if docker exec "$name" pg_isready -h 127.0.0.1 -U pb_owner -d pb >/dev/null 2>&1; then break; fi
    sleep 1
done
probe ready
