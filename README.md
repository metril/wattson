# Wattson

Go CLI to probe and drive a Merach exercise bike (S29R2) over BLE, locally via BlueZ or through an ESPHome Bluetooth proxy.

```
wattson scan | probe | drive | version
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

Append `/random` to the address when `scan` shows TYPE `random`. Every command has `-h`.

Docs: [hardware runbook](docs/hardware-runbook.md), [ESPHome proxy](docs/esphome-proxy.md), [S29R2 report](docs/s29r2.md). The proxy config is in [`esphome/`](esphome/).

## Safety

- Level 0 is never sent; the lowest level is 1.
- Ctrl-C returns the bike to its minimum level before wattson exits.
- Sweeps change real pedal resistance. Be on the bike or next to it.

## Development

```
make build     # build ./wattson
make go-test   # go vet + go test -race ./...
make lint      # gofmt check + go vet
```
