#!/usr/bin/env bash
# Smoke-test a built wattson image: size budget, non-root user, a simulated
# ride through the API, idle RSS budget and the admin CLI.
#   usage: scripts/docker-smoke.sh <image>
set -euo pipefail

image="${1:?usage: $0 <image>}"
MAX_SIZE=31457280 # 30 MiB
MAX_MEM_MIB=30
RIDE_SECONDS=20

cid=""
tmp="$(mktemp -d)"
cleanup() {
  [ -n "$cid" ] && docker rm -f "$cid" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# --- image size ---------------------------------------------------------------
size="$(docker image inspect -f '{{.Size}}' "$image")"
echo "image size: $size bytes ($((size / 1048576)) MiB, limit $MAX_SIZE)"
[ "$size" -le "$MAX_SIZE" ] || fail "image is $size bytes, over $MAX_SIZE"

# --- start --------------------------------------------------------------------
cid="$(docker run -d -p 127.0.0.1::8080 "$image")"
port="$(docker port "$cid" 8080/tcp | head -n1 | sed 's/.*://')"
base="http://127.0.0.1:$port"
echo "container ${cid:0:12} on $base"

for _ in $(seq 1 100); do
  curl -fsS "$base/healthz" >/dev/null 2>&1 && break
  [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = true ] || { docker logs "$cid" >&2; fail "container exited"; }
  sleep 0.2
done
curl -fsS "$base/healthz" >/dev/null || { docker logs "$cid" >&2; fail "server never became healthy"; }
docker exec "$cid" /usr/local/bin/wattson healthcheck || fail "wattson healthcheck failed inside the container"
echo "healthy"

# --- user ---------------------------------------------------------------------
cfg_user="$(docker inspect -f '{{.Config.User}}' "$cid")"
proc_uid="$(docker top "$cid" -eo pid,uid | awk 'NR==2 {print $2}')"
echo "user: config=$cfg_user process uid=$proc_uid"
[ "${cfg_user%%:*}" = 65532 ] || fail "image user is '$cfg_user', want 65532"
[ "$proc_uid" = 65532 ] || fail "server runs as uid '$proc_uid', want 65532"

# --- a simulated ride ---------------------------------------------------------
jar="$tmp/cookies"
json=(-H 'Content-Type: application/json')
curl -fsS -c "$jar" "${json[@]}" -d '{"username":"admin","password":"correct-horse-1"}' "$base/api/setup" >/dev/null \
  || fail "POST /api/setup"
token="$(curl -fsS -b "$jar" "${json[@]}" -d '{"name":"smoke","scopes":["read","control"]}' "$base/api/tokens" \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
[ -n "$token" ] || fail "no token minted"
auth=(-H "Authorization: Bearer $token")
rider="$(curl -fsS "${auth[@]}" "$base/api/riders" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')"
[ -n "$rider" ] || fail "no rider"
curl -fsS "${auth[@]}" "${json[@]}" -d "{\"rider_id\":$rider}" "$base/api/bike/connect" >/dev/null || fail "connect"
state=""
for _ in $(seq 1 50); do
  state="$(curl -fsS "${auth[@]}" "$base/api/bike" | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')"
  [ "$state" = ready ] && break
  sleep 0.2
done
[ "$state" = ready ] || fail "bike state '$state', want ready"
curl -fsS "${auth[@]}" "${json[@]}" -d '{"command":"start"}' "$base/api/ride/command" >/dev/null || fail "start"
echo "riding for ${RIDE_SECONDS}s"
sleep "$RIDE_SECONDS"
power="$(curl -fsS "${auth[@]}" "$base/api/ride" | sed -n 's/.*"power_w":\([0-9.]*\).*/\1/p')"
echo "power: ${power:-?} W"
[ -n "$power" ] && [ "${power%.*}" -gt 0 ] || fail "no power reading while riding"

# --- memory -------------------------------------------------------------------
docker stats --no-stream --format '{{.MemUsage}}' "$cid" >/dev/null # first sample can be stale
mem="$(docker stats --no-stream --format '{{.MemUsage}}' "$cid" | awk '{print $1}')"
mib="$(echo "$mem" | awk '{
  v = $0 + 0
  if ($0 ~ /GiB$/) v *= 1024; else if ($0 ~ /GB$/) v *= 953.67
  else if ($0 ~ /KiB$/) v /= 1024; else if ($0 ~ /kB$/) v /= 1048.576
  else if ($0 ~ /MB$/) v *= 0.95367; else if ($0 ~ /B$/ && $0 !~ /iB$/) v /= 1048576
  printf "%.1f", v }')"
echo "memory: $mem ($mib MiB, limit $MAX_MEM_MIB)"
awk -v m="$mib" -v max="$MAX_MEM_MIB" 'BEGIN { exit !(m < max) }' || fail "memory $mib MiB, over $MAX_MEM_MIB"

# --- admin CLI ----------------------------------------------------------------
out="$(docker exec "$cid" /usr/local/bin/wattson admin reset-password admin)"
echo "$out" | grep -q '^password for admin:' || fail "reset-password printed: $out"
echo "reset-password: ok"

echo "PASS: size ${size}B, memory ${mib}MiB, user 65532"
