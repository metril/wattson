# Wattson

Go CLI to probe and drive a Merach exercise bike (S29R2) over BLE, locally via BlueZ or through an ESPHome Bluetooth proxy.

```
wattson scan | probe | drive | ride | version
```

## Quick start

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
make lint      # gofmt check + go vet
```
