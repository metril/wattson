package api

import (
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/ridectl"
)

// The WebSocket messages. The AsyncAPI document is generated from these
// types, so every field carries a doc tag.

// WSState is the first message on a connection: the full current picture.
type WSState struct {
	Type string         `json:"type" enum:"state" doc:"Message kind: always state."`
	Bike ridectl.Status `json:"bike" doc:"Bike connection status right now."`
	Tick engine.Tick    `json:"tick" doc:"Latest ride snapshot right now."`
}

// WSTick carries one ride snapshot.
type WSTick struct {
	Type string      `json:"type" enum:"tick" doc:"Message kind: always tick."`
	Tick engine.Tick `json:"tick" doc:"Ride snapshot; sent about once per second while a bike is connected."`
}

// WSBike announces a change of the bike connection.
type WSBike struct {
	Type string         `json:"type" enum:"bike" doc:"Message kind: always bike."`
	Bike ridectl.Status `json:"bike" doc:"New bike connection status."`
}

// WSAck confirms that a command was accepted.
type WSAck struct {
	Type string `json:"type" enum:"ack" doc:"Message kind: always ack."`
	ID   string `json:"id" doc:"The id of the command being confirmed, as the client sent it."`
}

// WSError reports a failed command or a malformed message.
type WSError struct {
	Type  string    `json:"type" enum:"error" doc:"Message kind: always error."`
	ID    string    `json:"id,omitempty" doc:"The id of the failed command; absent when the message could not be parsed."`
	Error ErrorBody `json:"error" doc:"What went wrong. Codes: invalid_request, forbidden, unknown_command, unsupported_command, no_bike, bad_state."`
}

// WSCommand is the one message a client sends.
type WSCommand struct {
	Type    string `json:"type" enum:"command" doc:"Message kind: always command."`
	ID      string `json:"id" doc:"Client-chosen identifier, echoed in the ack or error that answers this command."`
	Command string `json:"command" enum:"start,pause,resume,stop,set_level,level_delta,set_target_power" doc:"What to do: start, pause, resume or stop the ride, set_level (to value), level_delta (by value) or set_target_power (not supported yet)."`
	Value   int    `json:"value,omitempty" doc:"Resistance level for set_level, change in levels for level_delta; ignored by the other commands."`
}
