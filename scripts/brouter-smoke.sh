#!/usr/bin/env bash
# Smoke-test a built wattson-brouter image: starts, answers HTTP on 17777, runs non-root.
#   usage: scripts/brouter-smoke.sh <image> [extra docker run args...]
set -euo pipefail

image="${1:?usage: $0 <image> [docker run args...]}"
shift || true

cid=""
cleanup() { [ -n "$cid" ] && docker rm -f "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; [ -z "$cid" ] || docker logs "$cid" >&2 || true; exit 1; }

cid="$(docker run -d -p 127.0.0.1::17777 "$@" "$image")"
port="$(docker port "$cid" 17777/tcp | head -n1 | sed 's/.*://')"
url="http://127.0.0.1:$port/brouter?lonlats=8.0,50.0%7C8.01,50.01&profile=trekking&alternativeidx=0&format=geojson"
echo "container ${cid:0:12} on 127.0.0.1:$port"

# Empty segments dir: any HTTP response (error body included) proves the server is up.
code=000
for _ in $(seq 1 100); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || true)"
  [ "$code" != 000 ] && break
  [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = true ] || fail "container exited"
  sleep 0.3
done
[ "$code" != 000 ] || fail "no HTTP response from BRouter"
echo "http: $code"

cfg_user="$(docker inspect -f '{{.Config.User}}' "$cid")"
proc_uid="$(docker top "$cid" -eo pid,uid | awk 'NR==2 {print $2}')"
echo "user: config=$cfg_user process uid=$proc_uid"
[ "${cfg_user%%:*}" = 65532 ] || fail "image user is '$cfg_user', want 65532"
[ "$proc_uid" = 65532 ] || fail "server runs as uid '$proc_uid', want 65532"

echo "PASS: brouter answers, user 65532"
