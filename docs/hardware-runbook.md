# Hardware runbook (M0)

Goal: confirm the S29R2 protocol and the 1..16 level sweep on the real bike, then record it in `docs/s29r2.md`.

You must be on the bike or next to it. Pedal to wake it.

## Safety

- Level 0 is never sent. The lowest level is 1.
- Ctrl-C (SIGINT/SIGTERM) stops the sweep and wattson then tries to return the bike to its minimum level. It has returned only when it prints `bike returned to level N`. If it prints `failed to return bike to level N` (or the bike disconnected), lower the resistance manually at the bike console.
- A second Ctrl-C exits at once with `forced exit; bike may still be at level N`: lower the resistance at the console. Ctrl-C before the link is up exits 130 with `interrupted before connecting` (no level was set).
- `drive` treats 5 s with no telemetry as a disconnect (`no telemetry for 5s — treating bike as disconnected at level N`, exit 1). It still tries the minimum-level write on the way out, but reports it as sent, not confirmed. Check the console.
- Exit codes: 0 complete, 130 interrupted, 1 bike disconnected or command failure, 2 usage error.
- The sweep goes min..max..min; resistance changes every 5 s, so keep a steady, light cadence.

## Prerequisites

### Local adapter (BlueZ)

- Linux host with `bluetoothd` running and a powered adapter: `systemctl status bluetooth`, `bluetoothctl show` (Powered: yes).
- The user needs D-Bus access to BlueZ. If you see `access denied` / `permission denied ... add the container user to the host bluetooth group`, add the user to the `bluetooth` group and re-login: `sudo usermod -aG bluetooth $USER`.
- Close other BLE clients (phone app, `bluetoothctl connect`) so the bike has no other connection.

### Docker

Host Bluetooth is opt-in through `compose.bluetooth.yml`; the simulator and the ESPHome proxy need none of it. Enable it with a `.env` next to `compose.yml`:

```
COMPOSE_FILE=compose.yml:compose.bluetooth.yml
BLUETOOTH_GID=112   # getent group bluetooth | cut -d: -f3
```

The override adds:

- The system D-Bus socket, mounted read-only: `/run/dbus/system_bus_socket:/run/dbus/system_bus_socket:ro`. The image sets `DBUS_SYSTEM_BUS_ADDRESS` to that path, so there is nothing to configure.
- The host's `bluetooth` group, by number (`BLUETOOTH_GID`): the image has no group names to resolve. With plain Docker: `--group-add "$(getent group bluetooth | cut -d: -f3)"`.

The host must run `bluetoothd` (`systemctl status bluetooth`); otherwise Docker creates a directory at the socket path. If the logs say `access denied` or `permission denied` for BlueZ, the gid is wrong or the socket is missing.

No host networking and no `--privileged`.

### ESPHome proxy (alternative)

See [esphome-proxy.md](esphome-proxy.md). Use `--via esphome://<host> --key <key>` on every command below.

## Steps

Build: `make build`. Add `--via esphome://HOST --key "$KEY"` to each command for the proxy.

1. **Scan.** Find the bike and note its address and address type.
   ```
   ./wattson scan --name-prefix MRK- --duration 15s
   ```
   Output columns: `ADDR TYPE RSSI NAME SERVICES`. Pass the ADDR column to `--addr` exactly as shown (it already includes `/random` for random addresses).

2. **Probe with selftest** while pedalling gently.
   ```
   ./wattson probe --addr <ADDR> --selftest --duration 30s --out s29r2.hexlog
   ```
   Expect: `protocol: fitplus` (or `ftms`), the GATT tree, then `selftest: PASS (... answered on ... in N ms)`, followed by `t_ms char hex` notification lines. `s29r2.hexlog` is gitignored; keep it for decoding.

3. **Drive the sweep.**
   ```
   ./wattson drive --addr <ADDR> --max 16 --dwell 5s --report docs/s29r2.md
   ```
   Takes about 31 steps x 5 s. Expect `confirmed 31/31 levels`. Options if autodetect is wrong: `--proto fitplus|ftms`, `--ftms-enc u8|u16`.
   The report **overwrites** `docs/s29r2.md`; copy the skeleton's notes aside first or re-apply them afterwards.

4. Repeat steps 1 to 3 over the other transport (local and ESPHome) if you want both. For the proxy, note any `bike disconnected` during the sweep (keepalive reliability).

5. **Annotate** `docs/s29r2.md`: confirmed protocol, true max level, FTMS encoding if relevant, power/cadence behaviour, mismatches, defaults for M1.

## Abort criteria

Stop (Ctrl-C) and record in `docs/s29r2.md` if:

- The bike **reboots** or the console resets.
- The **level shown on the console differs** from the level sent, or `confirmed` is `no` for several rows (check the Mismatches section of the report).
- **No notifications** arrive (selftest FAIL, empty hexlog, or `-` in every row).
- `bike disconnected` repeatedly.

After an abort run `./wattson scan` to confirm the bike is advertising again; power-cycle the bike if not.

## Fallback: FitPlus table mismatch

If the bike answers but levels or frames do not match the built-in FitPlus table:

1. On Android enable Developer options > *Enable Bluetooth HCI snoop log*, toggle Bluetooth off/on.
2. Use the Merach app: connect, change resistance across the whole range, pedal.
3. Pull the log (`adb bugreport` or `adb pull /sdcard/btsnoop_hci.log`) and open it in Wireshark; filter `btatt`.
4. Decode the writes to the FitPlus write characteristic (`fff2`) and the notifications on `fff1` by hand against the table; record the real frames in `docs/s29r2.md`.
5. `probe --pcap` is **deferred**; build it only if this fallback is needed.

## Heart rate (M5)

Wattson reads heart rate from a Bluetooth monitor or from a Home Assistant sensor (Settings, Bike, Heart rate). It is optional and never changes the ride state, the level or alerts. A reading older than 5 seconds shows as `—` on the ride screen. Settings changes apply the next time the bike connects.

### Apple Watch with HeartCast

An Apple Watch does not broadcast heart rate by itself. Run the **HeartCast** app (the free tier is enough) on the iPhone paired with the watch, and start its Bluetooth heart rate broadcast.

1. Start the broadcast in HeartCast; keep the phone awake or the app in the foreground.
2. In Wattson, Settings, Bike, Heart rate: choose **Bluetooth**, tap **Scan** (only heart rate monitors are listed) and pick the HeartCast row. This fills the address and the name.
3. Save, then connect the bike. The ride screen shows the bpm and the source state ("Bluetooth: connected").

**Range.** The iPhone must be within Bluetooth range of the radio that Wattson uses: the host's own adapter, or the ESPHome proxy if you use one. Put the phone or the proxy near the bike, not in another room. With a proxy the heart rate monitor takes a second connection slot ([esphome-proxy.md](esphome-proxy.md#slots)).

**Address rotation.** iPhones change their private Bluetooth address from time to time, so the saved address goes stale. Wattson therefore also searches by the saved **name** (with the heart rate service) on every attempt, and finds the phone again. Keep the name filled in; rescan only if the name changed.

Chest straps and other BLE monitors work the same way (pick them in the scan). Put the strap on, and wet the electrodes, before scanning.

### Alternative: heart rate from Home Assistant

If Home Assistant already has a heart rate sensor (for example from the Apple Health or Fitbit integration, or an ESPHome device), choose **Home Assistant** and pick the `sensor.*` entity (in bpm). It needs the Home Assistant URL and token under Integrations. Wattson subscribes to the entity, so the rate is as fresh as Home Assistant's own updates; slow integrations (cloud syncs) show stale or zero values.

### Home Assistant as the bike driver

`bike.driver = ha` (Settings, Bike, driver "Home Assistant (FTMS)") is for FTMS bridges only, not for FitPlus bikes. See [home-assistant.md](home-assistant.md). Wattson needs no Bluetooth for the bike then. Diagnostics reports "No Bluetooth in use" only when the heart rate source is not Bluetooth either; with a Bluetooth heart rate monitor it checks the adapter or proxy for that.

### Diagnostics

Settings, **Diagnostics** (admin only) is the first stop when the bike or monitor will not connect: it checks the local adapter (D-Bus socket, BlueZ, adapter) or the ESPHome proxy (feature flags, slots, who holds the subscription), prints the exact fix, and shows each device's state and, after **Scan for signal strength**, its signal in dBm. Requests can take up to about 20 seconds; the page shows a spinner meanwhile.
