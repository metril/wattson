package ridectl

import (
	"errors"
	"strings"

	"github.com/metril/wattson/internal/bike/habike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/hass"
)

// hintFor turns a connection error into advice the rider can act on.
func hintFor(err error) string {
	switch {
	case errors.Is(err, blue.ErrProxyHeld):
		return "Another client (usually Home Assistant) holds this ESPHome proxy's Bluetooth. Remove this ESP32 from Home Assistant's ESPHome integration so it is dedicated to Wattson, then reconnect."
	case errors.Is(err, hass.ErrAuth):
		return "Home Assistant rejected the token. Create a long-lived access token in your HA profile and save it in Settings → Integrations."
	case errors.Is(err, habike.ErrRange):
		return "The Home Assistant resistance entity's min/max leaves no level in the rider's range. Change the entity's min/max in Home Assistant, or the rider's minimum and maximum level, then reconnect."
	case errors.Is(err, ErrNoHAURL):
		return "No Home Assistant URL is set. Enter it in Settings → Integrations, then reconnect."
	case errors.Is(err, ErrNoHAToken):
		return "No Home Assistant token is set. Create a long-lived access token in your HA profile and save it in Settings → Integrations, then reconnect."
	case errors.Is(err, habike.ErrEntityUnavailable):
		return "Home Assistant reports the bike entity unavailable. Its FTMS integration must be connected to the bike (FTMS bikes only), and nothing else may hold the bike's Bluetooth."
	case errors.Is(err, blue.ErrNotFound):
		return "Bike not found. Pedal to wake it, check the address, and close the Merach app or the Home Assistant integration that may be holding the connection."
	case errors.Is(err, blue.ErrNoSlots):
		return "The ESPHome proxy has no free Bluetooth connection slots. Set connection_slots: 3 and keep this proxy out of Home Assistant's Bluetooth."
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "access denied") || strings.Contains(msg, "permission denied") {
		return "Wattson cannot reach BlueZ over D-Bus. Mount /run/dbus/system_bus_socket and add the host's bluetooth group id to group_add (see compose.yml)."
	}
	return "Bike busy or unreachable. Close the Merach app or the Home Assistant integration, then retry."
}

// Hint returns advice for fixing err, the same text Status.Hint carries.
func Hint(err error) string { return hintFor(err) }
