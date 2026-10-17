#!/usr/bin/env bash
# 完成イメージの利用者・起動と、実際のtzdataの内容を確認する。サービスは起動しない。
set -euo pipefail
image=${1:-project-backyard:dev}
fixture=$(mktemp -d)
name="pb-runtime-image-test-$$"
cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
    rm -rf "$fixture"
}
trap cleanup EXIT
[ "$(docker image inspect --format '{{.Config.User}}' "$image")" = '65532:65532' ]
docker run --rm --network none "$image" version
docker create --name "$name" "$image" version >/dev/null
docker cp "$name:/var/lib/dpkg/status.d/tzdata" "$fixture/status"
docker cp "$name:/usr/share/zoneinfo" "$fixture/zoneinfo"
# メタデータの書換えだけでは通らない。勧告DLA-4792-1の対象地域でデータも検証する。
python3 - "$fixture" <<'PY'
import datetime, pathlib, sys, zoneinfo
root = pathlib.Path(sys.argv[1])
status = dict(line.split(': ', 1) for line in (root/'status').read_text().splitlines() if ': ' in line and not line.startswith(' '))
assert status['Package'] == 'tzdata'
assert status['Architecture'] == 'all'
assert (root/'zoneinfo/tzdata.zi').read_text().splitlines()[0].startswith('# version ')
for name, date, offset in [('America/Edmonton', (2026, 11, 2), -6), ('Africa/Casablanca', (2026, 10, 1), 0), ('Asia/Tokyo', (2026, 10, 1), 9)]:
    with (root/'zoneinfo'/name).open('rb') as stream:
        tz = zoneinfo.ZoneInfo.from_file(stream, key=name)
    actual = datetime.datetime(*date, tzinfo=tz).utcoffset().total_seconds()/3600
    assert actual == offset, (name, actual, offset)
print('Runtime image: nonroot, startup and timezone rules passed (' + status['Version'] + ').')
PY
