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
# fill wifi_ssid, wifi_password, ap_password, wattson_proxy_static_ip
esphome config wattson-proxy.yaml           # validate
esphome run wattson-proxy.yaml              # first flash over USB, later OTA
```

Edit `esp32.board` in the YAML if your board is not an ESP32-S3 devkit. The static IP is yours to choose (reserve it on your router); `gateway`, `dns`, `ntp_server` and `syslog_*` default to the author's network and live in `substitutions:`; change them for yours (or remove the `time`, `udp` and `syslog` blocks).

OTA is encrypted with the API key (`ota: encryption: {}`).

## Key

The API key is the 32-byte base64 string in `secrets.yaml` (`wattson_proxy_api_key`). Generate it once with `openssl rand -base64 32`. Pass the same string to wattson:

```
wattson scan --via esphome://192.0.2.10 --key "$KEY" --name-prefix MRK-
```

Port defaults to 6053 (`esphome://host:6053`). Avoid putting the key in shell history: `KEY=$(grep wattson_proxy_api_key esphome/secrets.yaml | cut -d'"' -f2)`.

## Slots

`bluetooth_proxy: connection_slots: 3`. Wattson needs one for the bike. A slot stays taken until the link is dropped; if a previous run crashed, wait a bit or reboot the proxy.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `invalid encryption key` (device says "Handshake MAC failure") | Wrong `--key`. Use the exact `wattson_proxy_api_key` the firmware was built with; re-flash if you changed it. |
| `--key ... is required with --via esphome://` | Pass `--key`. |
| `proxy has no free bluetooth connection slots` (`ErrNoSlots`) | Another client holds the slots. Disconnect HA/other tools, wait, or reboot the proxy. Check HA did not adopt it. |
| `device is using the plaintext protocol` | Firmware has no `api: encryption`; re-flash this config. |
| Connection refused / timeout | Wrong host/IP, proxy offline, firewall on 6053. Check the proxy's logs (`esphome logs wattson-proxy.yaml`). |
| Bike not in `scan` | Bike asleep (pedal to wake), out of range, or the proxy is not near it. Remove `--name-prefix` to see all devices. |
| `bike disconnected` mid-sweep | Weak link between proxy and bike: move the proxy closer, then retry. Record it in `docs/s29r2.md`. |
