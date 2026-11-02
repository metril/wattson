# API quick start

Wattson serves a REST API under `/api` and a WebSocket at `/ws`. The complete reference is generated from the code:

- REST: [openapi.json](openapi.json), or the live reference at `/docs` and `/openapi.json` on the server.
- WebSocket messages: [asyncapi.yaml](asyncapi.yaml).
- Authentication, scopes and tokens: [auth.md](auth.md).

All examples use a token created with `POST /api/tokens` (see [auth.md](auth.md)); replace the host and token.

```
export W=http://localhost:8080
export TOKEN=wat_…
```

Errors are always `{"error":{"code":"snake_case","message":"…"}}`.

A state-changing request authenticated by the session cookie (browser use) is refused with `403 csrf` when its `Origin` names a foreign host, or when the browser sends `Sec-Fetch-Site: cross-site` with no `Origin`. This applies to every such route, including those that document no scope (for example logout). Requests with a bearer token are exempt.

## Bike and ride

```
# State of the bike: idle, connecting, ready, riding, paused or finished
curl -sS $W/api/bike -H "Authorization: Bearer $TOKEN"

# Your riders (one is created on first use), then connect one to the bike (needs the control scope)
curl -sS $W/api/riders -H "Authorization: Bearer $TOKEN"
curl -sS -X POST $W/api/bike/connect -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"rider_id":1}'
# 202 with state "connecting"; poll GET /api/bike until "ready" (or read last_error and hint)

# Ride commands: start, pause, resume, stop, set_level (value = level), level_delta (value = change)
curl -sS -X POST $W/api/ride/command -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"command":"start"}'
curl -sS -X POST $W/api/ride/command -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"command":"set_level","value":8}'

# Latest ride snapshot: speed, power, cadence, distance, level
curl -sS $W/api/ride -H "Authorization: Bearer $TOKEN"

# Return the bike to its minimum level and free it
curl -sS -X POST $W/api/bike/release -H "Authorization: Bearer $TOKEN"
```

Status codes worth knowing: `409 bike_busy` (already connected), `409 no_bike`, `409 bad_state` (command does not fit the ride state), `409 ride_active` (disconnect while riding; use release), `424 floor_unconfirmed` (the bike was released but could not be confirmed back at its minimum level: lower the resistance at the console), `503 no_bike_manager`.

Only one bike is controlled, by one rider at a time. The server never sends resistance level 0, and returns the bike to the rider's minimum level on stop, release, idle timeout and shutdown.

## WebSocket

`/ws` streams the bike status and a ride tick about once per second, and accepts the same commands as `POST /api/ride/command`. Offer the subprotocol `wattson.v1`; authenticate with the session cookie (same-origin pages) or by also offering `bearer.<token>`:

```
websocat --protocol "wattson.v1, bearer.$TOKEN" ws://localhost:8080/ws
```

You receive `{"type":"state",…}` first, then `{"type":"tick",…}` and `{"type":"bike",…}` messages. Send:

```
{"type":"command","id":"c1","command":"level_delta","value":1}
```

and get `{"type":"ack","id":"c1"}` or `{"type":"error","id":"c1","error":{"code":"bad_state","message":"…"}}`. A malformed message gets an error and the connection stays open; a message over 4 KiB closes it. A revoked token closes the socket with code 4401. At most 32 sockets are open in total and 8 per user (503 beyond that).

In a browser:

```js
const ws = new WebSocket(`ws://${location.host}/ws`, ["wattson.v1", `bearer.${token}`]);
ws.onmessage = (e) => console.log(JSON.parse(e.data));
```

## Running the server

```
wattson serve [--data-dir /data] [--listen :8080]   # env: WATTSON_DATA_DIR, WATTSON_LISTEN
wattson healthcheck [--listen :8080]                # exit 0 when GET /healthz answers 200
```

Docker: `HEALTHCHECK CMD ["wattson", "healthcheck"]`, and give the container a `stop_grace_period` of at least 25 s so that a stop can finish returning the bike to its minimum level before the process is killed. `GOMEMLIMIT` overrides the built-in 24 MiB soft memory limit.
