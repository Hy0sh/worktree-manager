#!/usr/bin/env bash
# Drives the real binary through the cycle the unit suite cannot: refresh,
# create, the stack's own bindings, remove, and what docker keeps afterwards.
# For CI: it initialises a git repository here and leaves docker state behind,
# so it is not to be run from inside a worktree.
set -euo pipefail
WTM=${WTM:-./wtm}
HERE=$(cd "$(dirname "$0")" && pwd)
export WTM_CONFIG_DIR=$(mktemp -d)
create_two_out=""
trap 'rm -rf "$WTM_CONFIG_DIR" "$create_two_out"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

cd "$HERE" && git init -q . 2>/dev/null || true
git -C "$HERE" add -A && git -C "$HERE" -c user.email=ci@x -c user.name=ci commit -qm fixture --allow-empty
git -C "$HERE" branch -M main

$WTM project create fx --dir "$HERE" --base main --dump --app-service api \
  --migrate 'sh migrate.sh' --env 'DATABASE_URL=postgresql://postgres:fixture@db:5432/{{database}}' --no-input
$WTM backup refresh fx
docker compose -f "$HERE/compose.yaml" ps --services --status running | grep -q . && fail "refresh left the main stack running"
docker volume ls -q | grep -q '^integration_pgdata$' || fail "refresh removed the main stack's named volume"

before=$(docker volume ls -q | wc -l)
$WTM create fx it/one --ignore-memory
create_two_out=$(mktemp)
$WTM create fx it/two --ignore-memory 2>&1 | tee "$create_two_out"   # index 2 must skip the 5432/5433 clash with index 1
grep -q "index 2 skipped" "$create_two_out" || fail "create did not report skipping index 2 for its port clash"
$WTM list fx
$WTM list fx | grep -q "^3 *it/two" || fail "index 2 must have been skipped for its port clash: it/two should be at index 3"

db1_cid=$(docker ps -q --filter label=com.docker.compose.project=integration-wt-1-it-one --filter label=com.docker.compose.service=db)
[[ -n "$db1_cid" ]] || fail "no db container for it/one (project integration-wt-1-it-one)"
port1=$(docker port "$db1_cid" 5432 | head -1)
[[ "$port1" == 127.0.0.1:* ]] || fail "db must bind on 127.0.0.1, got $port1"

api1_cid=$(docker ps -q --filter label=com.docker.compose.project=integration-wt-1-it-one --filter label=com.docker.compose.service=api)
[[ -n "$api1_cid" ]] || fail "no api container for it/one (project integration-wt-1-it-one)"
api1=$(docker port "$api1_cid" 8099 | head -1 | cut -d: -f2)
for i in $(seq 1 30); do curl -sf "http://localhost:$api1/widgets" | grep -q from-migration && break; sleep 2; done
if ! curl -sf "http://localhost:$api1/widgets" | grep -q from-migration; then
  docker compose -p integration-wt-1-it-one logs --tail=50 api
  fail "the worktree did not come up on the restored dump"
fi

$WTM remove fx it/one
$WTM remove fx it/two
after=$(docker volume ls -q | wc -l)
[[ "$after" -le "$before" ]] || fail "remove leaked $((after - before)) volume(s)"
$WTM doctor
echo "integration: OK"
