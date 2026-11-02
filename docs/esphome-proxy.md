# ESPHome Bluetooth proxy

Use a dedicated ESP32 near the bike as a remote BLE radio. `wattson` talks to it over the ESPHome native API (Noise-encrypted) with `--via esphome://<host> --key <key>`.

Config: [`esphome/wattson-proxy.yaml`](../esphome/wattson-proxy.yaml). It is self-contained (no private packages) and takes values from `esphome/secrets.yaml`.

## One subscriber rule

An ESPHome Bluetooth proxy serves **one** Bluetooth subscriber at a time. Wattson must be that subscriber, so:

- Do **not** add `wattson-proxy` to Home Assistant's ESPHome integration. If it was auto-discovered, ignore or remove it. If HA already adopted it, delete the device entry.
- Do not use this device as a general HA proxy. Use a different proxy for HA.
- Do not run two wattson sessions against it at once.

The config sets `api: reboot_timeout: 0s` so the proxy does not reboot for lack of an HA client.

## Flash

Needs ESPHome >= 2026.9.

```
cd esphome
cp secrets.example.yaml secrets.yaml        # gitignored
openssl rand -base64 32                     # paste as wattson_proxy_api_key
# fill every entry (wifi, ap_password, static IP, gateway, DNS, NTP, syslog host)
esphome config wattson-proxy.yaml           # validate
esphome run wattson-proxy.yaml              # first flash over USB, later OTA
```

Edit `esp32.board` in the YAML if your board is not an ESP32-S3 devkit. The static IP is yours to choose (reserve it on your router). Gateway, DNS, NTP and syslog host are also secrets (`wattson_proxy_*`); remove the `time`, `udp` and `syslog` blocks if you do not want SNTP or remote logs.

OTA is encrypted with the API key (`ota: encryption: {}`).

## Key

The API key is the 32-byte base64 string in `secrets.yaml` (`wattson_proxy_api_key`). Generate it once with `openssl rand -base64 32`. Pass the same string to wattson:

```
wattson scan --via esphome://192.0.2.10 --key "$KEY" --name-prefix MRK-
```

Port defaults to 6053 (`esphome://host:6053`). Avoid putting the key in shell history: `KEY=$(grep wattson_proxy_api_key esphome/secrets.yaml | cut -d'"' -f2)`.

## How it talks to the proxy

`bluetooth_proxy` sends every BLE reply (connection state, GATT services, reads, acks, notifications, errors) only to the API client that subscribed to advertisements, and drops them otherwise. Wattson therefore subscribes once when the transport opens and keeps that subscription until it exits (a newer subscriber replaces an older one, so do not run two clients at once; Home Assistant adopting the proxy would take it over).

## Slots

`bluetooth_proxy: connection_slots: 3`. Wattson needs one for the bike. A slot stays taken until the link is dropped; if a previous run crashed, wait a bit or reboot the proxy.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `invalid encryption key` (device says "Handshake MAC failure") | Wrong `--key`. Use the exact `wattson_proxy_api_key` the firmware was built with; re-flash if you changed it. |
| `--key ... is required with --via esphome://` | Pass `--key`. |
| `proxy has no free bluetooth connection slots` (`ErrNoSlots`) | Another client holds the slots. Disconnect HA/other tools, wait, or reboot the proxy. Check HA did not adopt it. |
| `device is using the plaintext protocol` | Firmware has no `api: encryption`; re-flash this config. |
| `connection refused` (names `host:port`) | Wrong port, or the API is not listening. Check the proxy's logs (`esphome logs wattson-proxy.yaml`). |
| `dial timeout: host:port did not answer within 5s` | Wrong host/IP, proxy offline or unreachable, firewall on 6053. Whole transport setup is capped at 10 s. |
| BLE connect times out (hint: `pass --addr XX:..:XX/random`) | The bike advertises a random address but `--addr` lacks `/random`. Copy ADDR from `scan` exactly. |
| Bike not in `scan` | Bike asleep (pedal to wake), out of range, or the proxy is not near it. Remove `--name-prefix` to see all devices. |
| `bike disconnected` mid-sweep | Weak link between proxy and bike: move the proxy closer, then retry. Record it in `docs/s29r2.md`. |
