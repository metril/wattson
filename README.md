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

Docs: [authentication and OIDC](docs/auth.md), [Strava](docs/strava.md), [API quick start](docs/api.md), [openapi.json](docs/openapi.json), [asyncapi.yaml](docs/asyncapi.yaml).

## Docker

```
docker compose up -d --build        # http://localhost:8080 shows the setup wizard
```

The image is distroless (about 28 MB, runs as uid 65532) and needs no environment variables: state lives in the `wattson-data` volume, mounted at `/data`. The built-in simulator needs nothing else, so the web UI works at once with no bike.

For a bike on this host's Bluetooth adapter, the container talks to the host's BlueZ over the D-Bus socket that `compose.yml` mounts, and needs the host's numeric `bluetooth` group id; see [Docker in the hardware runbook](docs/hardware-runbook.md#docker). An ESPHome proxy needs neither. The container never uses host networking.

Stopping the container returns the bike to its minimum level first, so compose gives it a 25 s grace period. The `brouter` service in `compose.yml` (pinned by tag and digest) starts with it and reads the map regions that Wattson downloads into the shared `brouter-segments` volume (Settings, Map regions). The pinned BRouter image is amd64 only, so `brouter` is pinned to `platform: linux/amd64`: on an arm64 host (Raspberry Pi) it runs under emulation, which needs `qemu-user-static`/binfmt and is slow; alternatively delete the `brouter` service and point Settings, BRouter URL at a BRouter elsewhere, leaving the segments directory empty.

## Ride screen (web)

Open the web app, pick a rider and tap Connect, then Start. Use the on-screen − / + buttons to change the level, and Pause, Resume and Stop (two taps) to control the ride.

TV remote: the up and down arrows change the level and Enter starts, pauses or resumes. Stop and Release need touch or mouse, or the idle auto-release. Remote focus navigation for those arrives in M7.

## Integrations and hardware

- **Home Assistant bike driver**: Settings, Bike, driver "Home Assistant (FTMS)" drives the bike through `number`/`sensor` entities. **FTMS bridges only**: a FitPlus bike has no Home Assistant path and needs the local adapter or an ESPHome proxy. Details in [docs/home-assistant.md](docs/home-assistant.md).
- **Heart rate** (optional): Settings, Bike, Heart rate. Use a Bluetooth strap, an Apple Watch through the free tier of the **HeartCast** iPhone app, or a Home Assistant sensor. The ride screen shows bpm and the source state; a reading older than 5 s shows `—`. Setup, range and address rotation: [hardware runbook](docs/hardware-runbook.md#heart-rate-m5).
- **MQTT / Home Assistant entities**: Settings, Integrations, MQTT (broker URL, credentials, discovery prefix, Test MQTT). Sensors and buttons appear in Home Assistant by discovery; see [docs/ha-mqtt.md](docs/ha-mqtt.md).
- **ESPHome proxy**: one proxy serves one Bluetooth subscriber. If Home Assistant adopts it, Wattson loses its Bluetooth data ("Held by another client"); remove it from Home Assistant's ESPHome integration. See [docs/esphome-proxy.md](docs/esphome-proxy.md#held-by-another-client).
- **Diagnostics**: Settings, Diagnostics (admin) checks the local adapter or the ESPHome proxy, prints the fix for what it finds, and scans for the signal strength of your bike and heart rate monitor. A check can take up to about 20 s.

## Routes

Routes turn the bike into a gradient trainer: Wattson raises the resistance a few seconds before a climb and eases it on descents. Open **Routes** in the web app.

- **Import**: upload a GPX, TCX or FIT file (up to 20 MiB, 50 m to 500 km). The route needs elevation data; a file with more than 10 % of its points missing it is rejected unless the OpenTopoData fallback is on (below).
- **Draw**: click waypoints on the map (up to 30). BRouter follows the roads between them and the preview shows distance, ascent and the elevation profile before you save.
- **Synthetic**: build a route from segments of a given length and grade, for intervals and tests. It has no map.
- **Preview and tuning**: a route's page shows the resistance each point will ask for. Gain, flat-road level, downhill relief and lead time come from the rider and apply from the next connect; the preview is a guide (the live ride adds smoothing).
- **Ride**: choose it on the ride screen (or tap "Ride this route"), then Start. The map marker and profile cursor follow you. The up and down arrows (or Harder and Easier) shift the level by a bias on top of the route's own level. The ride ends at the finish line.

`docker compose up -d` now also starts BRouter. Drawing needs its map data: open **Settings, Map regions**, find the 5 degree squares you want and download them; progress shows there. A draw over a missing region says which one to download. The regions live in the `brouter-segments` volume and are shared with BRouter.

To use a BRouter running elsewhere, set **Settings, BRouter URL** and leave the segments directory empty; Wattson then skips the local missing-region check. On arm64 (Raspberry Pi) the pinned BRouter image is amd64 only, so it runs under emulation (see Docker above) or on another machine through that URL.

Privacy: importing and riding use only your own server. The map background is loaded by your browser from `tiles.openfreemap.org`. With **OpenTopoData** on, the coordinates of a track are sent to `api.opentopodata.org` when more than 10 % of its points are missing elevation (an import without elevation, or a BRouter track that comes back without it) and for any track above 60 degrees latitude. With **public fallback** on, the waypoints of a drawn route are sent to `brouter.de` only when the route needs map regions you have not downloaded; it is never used when BRouter itself is down. Both are off by default.

## Workouts and ERG

Open **Workouts** in the web app to build a workout from blocks, with a preview chart for the rider you pick. Reorder blocks with Move up and Move down.

- **Interval workouts** set a resistance level per block: an absolute **Level**, or an **Offset** from the rider's flat-road level (so one workout fits every rider). Levels are clamped to the rider's range.
- **ERG workouts** hold a target power per block, in **Watts** or as **% FTP** of the rider's FTP (set on the Riders page). A % FTP target outside 50 to 1000 W is raised or capped when ridden; the editor warns about it. Wattson picks the resistance level that gives the target at your current cadence and trims it with a small feedback loop. It holds within about 5 % on the simulator; between two levels it can only be as close as the level steps allow.
- **Blocks** last 5 s to 2 h each (up to 200 blocks and 6 h), with an optional label and cadence hint shown on the ride screen. Blocks are steps; build a ramp from several short blocks.
- **Ride**: choose a workout on the ride screen (or tap "Ride this workout"), then Start. The screen shows the block number, its target, the next block, a countdown and the chart with a cursor. Pausing freezes the block clock. The ride ends after the last block. In an interval workout Easier and Harder shift every block's level (the bias); in ERG, -10 W and +10 W shift the target (up to 100 W). Editing a workout never changes a ride in progress, and a workout being ridden cannot be deleted.
- **Free ERG**: choose "Free ERG" on the ride screen, enter a power (default 150 W) and Start. Change it with -10 W and +10 W or by typing a new power and tapping Set.
- **Power multiplier**: each rider has a power multiplier (Riders page) that scales the bike's reported power, for a bike that reads high or low. The multiplied power is what you see, what is recorded, what drives the speed, and what ERG holds.
- **Learned power model**: every bike gets its own model of power against cadence and level, starting from a built-in one. Wattson refines it from steady stretches of your rides (level unchanged for 3 s, 40 to 160 rpm, implausible readings ignored) and saves it per bike. **Settings, Bike, Learned power model** shows the number of samples and the modelled watts at 80 rpm for levels 1, 8 and 16. **Reset** (tap twice, admin) discards it and starts again from the built-in model, which is worth doing after changing the bike's calibration.

## History & Strava

Finished rides are recorded automatically: one sample per second of riding, kept in the same database. Open **History** (also on a paired kiosk display) for the list, newest first, with a rider filter and "Load more". Open a ride for its stats (duration, distance, ascent, average and normalized power, energy, cadence, speed, heart rate when available), charts of power, cadence, speed and elevation over distance, and the map trace for route rides. Rides shorter than about 10 seconds are discarded. Delete removes the ride only after a second tap.

- **FIT download**: every ride has a Download FIT button (a standard FIT activity with power, cadence, speed, distance and, for route rides, position and altitude). It works from a browser session and from a paired display.
- **Strava**: connect each rider on the Riders page, then use Upload to Strava on a ride, or switch on automatic upload per rider. Wattson never uploads without a connected rider, and rides are uploaded once. Setup (your own Strava API app, callback domain, the one-athlete capacity of new apps, privacy and troubleshooting) is in [docs/strava.md](docs/strava.md).

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

Docs: [hardware runbook](docs/hardware-runbook.md), [ESPHome proxy](docs/esphome-proxy.md), [S29R2 report](docs/s29r2.md), [Home Assistant bike driver](docs/home-assistant.md), [Home Assistant over MQTT](docs/ha-mqtt.md). The proxy config is in [`esphome/`](esphome/).

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
