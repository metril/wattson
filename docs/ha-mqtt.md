# Home Assistant over MQTT

Wattson publishes the ride to an MQTT broker and registers itself with Home Assistant through MQTT discovery. You get nine sensors and seven buttons on one "Wattson" device. This is separate from the [Home Assistant bike driver](home-assistant.md), which lets Wattson drive a bike through Home Assistant.

## Set up

1. Run an MQTT 5 broker that Home Assistant also uses (for example Mosquitto 2) and enable the MQTT integration in Home Assistant.
2. In Wattson open Settings, MQTT and enter:
   - **URL**: `mqtt://broker:1883`, or `mqtts://broker:8883` for TLS. No `user:pass@` in the URL; the port defaults to 1883 and 8883.
   - **Username** and **password** if the broker needs them. The password is stored sealed and never shown again.
   - **Discovery prefix**: the one Home Assistant listens on, `homeassistant` by default.
   - **Skip TLS certificate check** (`mqtt.tls_insecure_skip_verify`, off by default): for an `mqtts://` broker with a self-signed certificate on your LAN. It turns certificate checks off, so anyone on the network path can impersonate the broker and read the password. Prefer adding a proper certificate. The Test button honours it (and an explicit value in its body).
3. Press **Test** (it calls `POST /api/settings/test/mqtt`). It makes one MQTT 5 connection and disconnects without publishing. A refused login reports the broker's reason, for example `not authorized`. Empty fields use the stored values; the stored username and password are only used when the URL is the stored one.
4. Save. Wattson connects at once, and again whenever you change these settings. With no URL the bridge is off. A broker that is down at boot is retried every 5 seconds; the server starts anyway.

Status: `GET /api/mqtt/status` (admin) reports `enabled`, `connected` and the last `error`.

## Entities

| Entity | Type | Unit | State field |
|---|---|---|---|
| Power | sensor | W | `power_w` |
| Cadence | sensor | rpm | `cadence_rpm` |
| Heart rate | sensor | bpm | `hr_bpm` |
| Speed | sensor | km/h | `speed_kmh` |
| Grade | sensor | % | `grade_pct` |
| Resistance level | sensor | | `level` |
| Distance | sensor | km | `distance_km` |
| Route progress | sensor | % | `progress_pct` |
| Ride state | sensor (enum: idle, connecting, ready, riding, paused, finished) | | `state` |
| Start ride | button | | |
| Pause | button | | |
| Resume | button | | |
| Stop ride | button | | |
| Harder | button | | level +1 |
| Easier | button | | level -1 |
| Release bike | button | | |

Entity ids are `sensor.wattson_<key>` and `button.wattson_<key>`. Wattson sets them with the discovery option `default_entity_id` (the full `sensor.wattson_power`); Home Assistant 2025.10 deprecated `object_id` and 2026.4 stopped honouring it. They are fixed when the entity is first created, so renaming one in Home Assistant sticks. The keys are `power`, `cadence`, `heart_rate`, `speed`, `grade`, `level`, `distance`, `progress`, `state`, and for buttons `start`, `pause`, `resume`, `stop`, `level_up`, `level_down`, `release_bike`.

- **Start ride** begins a free ride on the bike that is already connected and ready. It does not connect a bike; connect one in the web app first.
- **Harder** and **Easier** change the resistance level by one. In a route ride they shift the live bias instead (up to 10 levels either way). The level never goes below 1.
- **Release bike** ends any ride and returns the bike to its minimum level.
- Heart rate shows 0 when there is no heart rate source or no reading in the last 5 seconds.

## Topics

The base topic is fixed to `wattson`; only the discovery prefix is configurable.

| Topic | Direction | Retained | Payload |
|---|---|---|---|
| `wattson/status` | Wattson publishes | yes | `online` or `offline`, QoS 1. `offline` is also the MQTT last will, so the sensors go unavailable when the connection drops without a goodbye. |
| `wattson/state` | Wattson publishes | yes | JSON, below. At most once per second, and only when something changed. |
| `wattson/command/<key>` | Wattson subscribes | no | `PRESS` (any payload works), QoS 1. |
| `<prefix>/sensor/wattson_<key>/config`, `<prefix>/button/wattson_<key>/config` | Wattson publishes | yes | Home Assistant discovery config. |

```json
{"state":"riding","mode":"free","power_w":201,"cadence_rpm":84,"hr_bpm":142,"level":8,
 "speed_kmh":31.4,"grade_pct":0,"distance_km":4.512,"progress_pct":0,"connected":true}
```

`progress_pct` is the share of the route ridden and 0 outside a route ride. Values are rounded: watts and rpm to whole numbers, km/h and percent to 0.1, km to 0.001.

Changing the credentials or the TLS option reconnects and keeps the discovery configs. Changing the discovery prefix empties the configs under the old prefix and publishes them under the new one. Clearing the URL, or pointing at another broker, empties the configs on the old one, publishes `offline` and disconnects. A normal shutdown publishes `offline` and leaves the configs in place.

## Commands and trust

- A **retained** message on a command topic is ignored, so a button press stored by the broker is never replayed after a restart. Wattson also asks the broker not to send retained messages when it subscribes.
- An unknown key, or a command that fails (for example Pause with no bike connected), is only logged as a warning with the key and the reason. The connection stays up.
- **The broker is trusted.** Commands carry no user identity: anyone who can publish to `wattson/command/#` can start, stop and release the bike. Keep the broker private, give Wattson and Home Assistant their own accounts, and restrict who may publish to `wattson/#` with broker ACLs. Wattson never sends resistance level 0 whatever is published.

## Dashboard card

```yaml
type: vertical-stack
cards:
  - type: glance
    title: Wattson
    entities:
      - entity: sensor.wattson_power
        name: Power
      - entity: sensor.wattson_cadence
        name: Cadence
      - entity: sensor.wattson_heart_rate
        name: Heart rate
      - entity: sensor.wattson_level
        name: Level
  - type: entities
    entities:
      - entity: sensor.wattson_state
      - entity: sensor.wattson_speed
      - entity: sensor.wattson_distance
  - type: grid
    columns: 3
    square: false
    cards:
      - {type: button, entity: button.wattson_start, name: Start}
      - {type: button, entity: button.wattson_pause, name: Pause}
      - {type: button, entity: button.wattson_resume, name: Resume}
      - {type: button, entity: button.wattson_level_down, name: Easier}
      - {type: button, entity: button.wattson_level_up, name: Harder}
      - {type: button, entity: button.wattson_stop, name: Stop}
```

## Keep the recorder small

The sensors change about once a second while you ride. If you do not want that history in the Home Assistant database, exclude them in `configuration.yaml`:

```yaml
recorder:
  exclude:
    entity_globs:
      - sensor.wattson_*
```

Add `- button.wattson_*` too if you do not want the press history. Wattson keeps your rides itself.

## AsyncAPI

The topics and payload schema are in [`asyncapi.yaml`](asyncapi.yaml) next to the WebSocket description.
