package ridectl

import (
	"errors"
	"strings"

	"github.com/metril/wattson/internal/blue"
)

// hintFor turns a connection error into advice the rider can act on.
func hintFor(err error) string {
	switch {
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
