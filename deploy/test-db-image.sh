#!/usr/bin/env bash
# 自分で作る隔離コンテナとボリュームだけで、初期化と17系のデータ引継ぎを確かめる。
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
image=${1:-project-backyard-db:dev}
base=${PB_DB_TEST_BASE_IMAGE:-pgvector/pgvector:pg17}
fixture=$(mktemp -d)
name="pb-db-image-test-$$"
volume="${name}-data"
cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker volume rm "$volume" >/dev/null 2>&1 || true
    rm -rf "$fixture"
}
trap cleanup EXIT
printf '%s' 'isolated-test-password' > "$fixture/app-password"
chmod 644 "$fixture/app-password"
docker volume create "$volume" >/dev/null
start() {
    docker run -d --name "$name" --network none \
        -e POSTGRES_USER=pb_owner -e POSTGRES_DB=pb -e POSTGRES_HOST_AUTH_METHOD=trust \
        -e 'POSTGRES_INITDB_ARGS=--encoding=UTF8 --lc-collate=C --lc-ctype=C.UTF-8' \
        -v "$volume:/var/lib/postgresql/data" \
        -v "$fixture/app-password:/run/secrets/app_db_password:ro" \
        -v "$repo_root/deploy/base/initdb/01_roles.sh:/docker-entrypoint-initdb.d/01_roles.sh:ro" \
        "$1" >/dev/null
    for ((attempt=0; attempt<90; attempt++)); do
        # initdbの一時サーバではなく、最終的なPID 1のpostgresを待つ。
        if docker exec "$name" sh -c '[ "$(cat /proc/1/comm)" = postgres ] && pg_isready -U pb_owner -d pb' >/dev/null 2>&1; then return; fi
        sleep 1
    done
    docker logs "$name" >&2
    return 1
}
query() { docker exec -i "$name" psql -X -qAt -v ON_ERROR_STOP=1 -U pb_owner -d pb; }
seed() {
    query <<'SQL'
CREATE EXTENSION pgcrypto;
CREATE EXTENSION citext;
CREATE EXTENSION pg_trgm;
CREATE EXTENSION vector;
CREATE TABLE image_upgrade_probe (id int PRIMARY KEY, name text, embedding vector(3));
INSERT INTO image_upgrade_probe VALUES (1, '日本語の保存確認', '[1,2,3]');
SQL
}
verify() {
    result=$(query <<'SQL'
SELECT current_setting('server_version_num')::int / 10000;
SELECT name || ':' || embedding::text FROM image_upgrade_probe WHERE id=1;
SELECT count(*) FROM pg_extension WHERE extname IN ('pgcrypto','citext','pg_trgm','vector');
SELECT has_table_privilege('pb_app','image_upgrade_probe','SELECT') AND NOT has_schema_privilege('pb_app','public','CREATE');
SELECT '日本語' ~ '[[:alpha:]]';
SQL
)
    expected=$(printf '17\n日本語の保存確認:[1,2,3]\n4\nt\nt')
    [ "$result" = "$expected" ] || { printf '%s\n' "$result" >&2; return 1; }
    [ "$(docker exec "$name" gosu postgres id -u)" = "$(docker exec "$name" id -u postgres)" ]
    docker exec "$name" test -s /usr/share/doc/pb-db/github.com/tianon/gosu/LICENSE
    docker exec "$name" sh -c 'if dpkg-query -s libsqlite3-0 2>/dev/null | grep -q "^Status: install ok installed$"; then echo "DB image still contains SQLite" >&2; exit 1; fi; test -x /usr/bin/gpgv'
}
# 上流イメージで作ったデータを、PB用イメージで引き継ぐ。
start "$base"
seed
docker stop "$name" >/dev/null
docker rm "$name" >/dev/null
start "$image"
verify
docker stop "$name" >/dev/null
docker rm "$name" >/dev/null
docker volume rm "$volume" >/dev/null
# 空のボリュームでも同じロール・拡張・文字設定を利用できる。
docker volume create "$volume" >/dev/null
start "$image"
seed
verify
printf '%s\n' 'DB image: PostgreSQL17 data preservation and fresh initialization passed.'
