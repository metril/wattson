package hass

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// WSURL derives the WebSocket API endpoint from the configured Home Assistant
// base URL: http becomes ws, https becomes wss, and /api/websocket is appended
// to the base path. Userinfo, query and fragment are dropped.
func WSURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("home assistant url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("home assistant url: scheme %q must be http or https", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("home assistant url: no host")
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.Path = strings.TrimRight(u.Path, "/") + "/api/websocket"
	u.RawPath = ""
	return u.String(), nil
}
