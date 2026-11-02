# Wattson

Go CLI to probe and drive a Merach exercise bike (S29R2) over BLE, locally via BlueZ or through an ESPHome Bluetooth proxy.

```
wattson serve | admin | healthcheck | scan | probe | drive | ride | version
```

## Server

`wattson serve` runs the web/API server that controls the bike (simulator or real), with users, OIDC sign-in, API tokens and a ride WebSocket. All state lives in one data directory (`wattson.db` and `secret.key`).

```
make build
./wattson serve --data-dir ./data --listen :8080     # or WATTSON_DATA_DIR / WATTSON_LISTEN (defaults /data, :8080)
# create the first admin with POST /api/setup (the web UI wraps it); the log prints the URL
```

Everything else is configured through the API and stored in SQLite. Stop it with Ctrl-C or SIGTERM: the bike is returned to its minimum level first, and the log says plainly if that could not be confirmed.

```
./wattson admin reset-password <user>    # locked out: prints a new password once
./wattson admin disable-oidc             # broken IdP: disable providers, re-enable local login
./wattson healthcheck                    # for Docker HEALTHCHECK
```

Docs: [authentication and OIDC](docs/auth.md), [API quick start](docs/api.md), [openapi.json](docs/openapi.json), [asyncapi.yaml](docs/asyncapi.yaml).

## Docker

```
docker compose up -d --build        # http://localhost:8080 shows the setup wizard
```

The image is distroless (about 28 MB, runs as uid 65532) and needs no environment variables: state lives in the `wattson-data` volume, mounted at `/data`. The built-in simulator needs nothing else, so the web UI works at once with no bike.

For a bike on this host's Bluetooth adapter, the container talks to the host's BlueZ over the D-Bus socket that `compose.yml` mounts, and needs the host's numeric `bluetooth` group id; see [Docker in the hardware runbook](docs/hardware-runbook.md#docker). An ESPHome proxy needs neither. The container never uses host networking.

Stopping the container returns the bike to its minimum level first, so compose gives it a 25 s grace period. The `brouter` service in `compose.yml` sits behind the `routing` profile and is not started by default.

## Ride screen (web)

Open the web app, pick a rider and tap Connect, then Start. Use the on-screen − / + buttons to change the level, and Pause, Resume and Stop (two taps) to control the ride.

TV remote: the up and down arrows change the level and Enter starts, pauses or resumes. Stop and Release need touch or mouse, or the idle auto-release. Remote focus navigation for those arrives in M7.

## Quick start (CLI)

```
make build                    # builds ./wattson
```

Scan for the bike, probe the protocol, then sweep resistance levels:

```
# local adapter (BlueZ, Linux)
./wattson scan --name-prefix MRK- --duration 15s
./wattson probe --addr AA:BB:CC:DD:EE:FF --selftest --duration 30s --out s29r2.hexlog
./wattson drive --addr AA:BB:CC:DD:EE:FF --max 16 --dwell 5s --report docs/s29r2.md

# through an ESPHome Bluetooth proxy
./wattson scan --via esphome://192.0.2.10 --key "$KEY" --name-prefix MRK-
./wattson drive --via esphome://192.0.2.10 --key "$KEY" --addr AA:BB:CC:DD:EE:FF --report docs/s29r2.md
```

Ride headless in Free mode (1 Hz lines for time, level, cadence, power, speed, distance; Ctrl-C returns the bike to its minimum level):

```
./wattson ride --sim --duration 5m --level 8 --cadence 85
./wattson ride --addr AA:BB:CC:DD:EE:FF --level 6 --duration 20m        # add --via esphome://... --key ... for a proxy
```

Flags: `--level` start level (5), `--min`/`--max` (1/16), `--mass` rider+bike kg (75), `--proto auto|fitplus|ftms`, `--duration` riding time (0 = until Ctrl-C), `--cadence` simulated rpm (80). Exit 0 on finish, 130 on Ctrl-C, 1 on error.

Pass the ADDR column from `scan` to `--addr` exactly as shown (random addresses already include `/random`). Every command has `-h`.

Docs: [hardware runbook](docs/hardware-runbook.md), [ESPHome proxy](docs/esphome-proxy.md), [S29R2 report](docs/s29r2.md). The proxy config is in [`esphome/`](esphome/).

## Safety

- Level 0 is never sent; the lowest level is 1.
- Ctrl-C stops the sweep and wattson tries to return the bike to its minimum level. It is back only when wattson prints `bike returned to level N`; on `failed to return bike to level N` lower the resistance manually at the console.
- A second Ctrl-C exits immediately (`forced exit; bike may still be at level N`): lower resistance at the console. Ctrl-C before the connection is up exits 130 with `interrupted before connecting` (no level was set).
- `drive` also treats 5 s without bike telemetry as a disconnect (`no telemetry for 5s — treating bike as disconnected at level N`, exit 1); a powered-off bike over an ESPHome proxy is otherwise only noticed after 8-10 s.
- Exit codes: 130 interrupted, 1 bike disconnected or failure.
- Sweeps change real pedal resistance. Be on the bike or next to it.

## Development

```
make build     # build ./wattson
make go-test   # go vet + go test -race ./...
make serve     # build and run the server on :8080 with ./data
make lint      # gofmt check + go vet + web type-check
make e2e       # Playwright smoke over the simulator (npx playwright install chromium once)
```
