package api

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
)

type wsMessageDoc struct {
	name, title, summary, description string
	typ                               reflect.Type
}

var wsMessages = []wsMessageDoc{
	{"state", "State", "Full state, sent once on connect",
		"The first message after the handshake: the current bike status and the latest ride snapshot.", reflect.TypeFor[WSState]()},
	{"tick", "Tick", "Ride snapshot",
		"A ride snapshot, sent about once per second while a bike is connected.", reflect.TypeFor[WSTick]()},
	{"bike", "Bike", "Bike status changed",
		"The bike connection changed state, rider, protocol or error.", reflect.TypeFor[WSBike]()},
	{"ack", "Ack", "Command accepted",
		"The command with this id was accepted by the ride engine.", reflect.TypeFor[WSAck]()},
	{"error", "Error", "Command or message rejected",
		"A command failed or a message could not be understood. The connection stays open.", reflect.TypeFor[WSError]()},
	{"command", "Command", "Ride command",
		"Starts, pauses, resumes or stops the ride, or changes the resistance level. Needs the control scope.", reflect.TypeFor[WSCommand]()},
}

// AsyncAPIDoc renders the AsyncAPI 3.0.0 description of the /ws WebSocket
// as indented JSON (which is valid YAML) with a trailing newline. Payload
// schemas come from the same huma registry and doc tags as the OpenAPI
// document.
func AsyncAPIDoc() ([]byte, error) {
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	messages := map[string]any{}
	chanMessages := map[string]any{}
	var sendRefs []any
	var recvRefs []any
	for _, m := range wsMessages {
		messages[m.name] = map[string]any{
			"name": m.name, "title": m.title, "summary": m.summary, "description": m.description,
			"contentType": "application/json",
			"payload":     reg.Schema(m.typ, true, ""),
		}
		chanMessages[m.name] = map[string]any{"$ref": "#/components/messages/" + m.name}
		ref := map[string]any{"$ref": "#/channels/ride/messages/" + m.name}
		if m.name == "command" {
			recvRefs = append(recvRefs, ref)
		} else {
			sendRefs = append(sendRefs, ref)
		}
	}
	doc := map[string]any{
		"asyncapi": "3.0.0",
		"info": map[string]any{
			"title":   "Wattson ride WebSocket",
			"version": "1.0.0",
			"description": "Live ride feed and commands for the single bike a Wattson server controls. " +
				"Open `/ws` offering the `" + auth.WSProtocol + "` subprotocol. The server sends the full state first, " +
				"then ride ticks and bike status changes. Commands need the `control` scope; the `read` scope only watches. " +
				"The server pings every 30 seconds and re-checks the credential about once a minute, closing with " +
				"status 4401 `unauthorized` when it is revoked or expired. Inbound messages are limited to 4096 bytes.",
		},
		"servers": map[string]any{
			"local": map[string]any{
				"host": "localhost:8080", "pathname": "/ws", "protocol": "ws",
				"description": "A Wattson server; use wss when it is served over TLS.",
				"security": []any{
					map[string]any{"$ref": "#/components/securitySchemes/bearer"},
					map[string]any{"$ref": "#/components/securitySchemes/cookie"},
				},
			},
		},
		"channels": map[string]any{
			"ride": map[string]any{
				"address":     "/ws",
				"title":       "Ride",
				"description": "The one channel: ride events flow to the client, commands flow to the server.",
				"messages":    chanMessages,
				"bindings":    map[string]any{"ws": map[string]any{"method": "GET", "bindingVersion": "0.1.0"}},
			},
		},
		"operations": map[string]any{
			"publishRideEvents": map[string]any{
				"action": "send", "channel": map[string]any{"$ref": "#/channels/ride"},
				"title":    "Receive ride events",
				"summary":  "The server sends state, ticks, bike changes, acks and errors",
				"messages": sendRefs,
			},
			"handleCommand": map[string]any{
				"action": "receive", "channel": map[string]any{"$ref": "#/channels/ride"},
				"title":    "Send a ride command",
				"summary":  "The client sends commands",
				"messages": recvRefs,
			},
		},
		"components": map[string]any{
			"messages": messages,
			"schemas":  reg.Map(),
			"securitySchemes": map[string]any{
				"bearer": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "An API token. A browser cannot set the Authorization header on a WebSocket, so offer it as a second " +
						"subprotocol next to `" + auth.WSProtocol + "`: `Sec-WebSocket-Protocol: " + auth.WSProtocol + ", bearer.<token>`.",
				},
				"cookie": map[string]any{
					"type": "httpApiKey", "name": auth.SessionCookie, "in": "cookie",
					"description": "The session cookie set by the login flow. The Origin header must name this server or the configured public URL.",
				},
			},
		},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
