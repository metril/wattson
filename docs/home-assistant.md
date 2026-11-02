# Home Assistant bike driver

Wattson can drive the bike through Home Assistant instead of BLE (`bike.driver = ha`).

- Resistance is written with `number.set_value` on the configured `number.*` entity. The level is clamped to the rider range intersected with the entity's `min`/`max` attributes and is never below 1 (level 0 is never sent).
- Power and cadence come from the configured `sensor.*` entities. The merged sample (level, cadence, power) is re-emitted every second; `unavailable`/`unknown` values read as 0.
- If the Home Assistant connection drops, the bike supervisor reconnects and re-asserts the target level.

**FTMS only.** This driver needs a bridge that exposes the bike's FTMS resistance as a Home Assistant `number` entity (for example an ESPHome Bluetooth proxy plus an FTMS bridge). The FitPlus protocol used by Merach bikes natively has no Home Assistant path: bikes that speak only FitPlus must use the BLE driver.
