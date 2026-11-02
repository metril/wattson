package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/metril/wattson/internal/settings"
)

// trustedProxies parses auth.trusted_proxies. Validation keeps the list
// well-formed; an entry that still fails to parse is skipped.
func trustedProxies(s settings.Settings) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(s.Auth.TrustedProxies))
	for _, v := range s.Auth.TrustedProxies {
		if p, err := settings.ParseProxy(v); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// proxies returns the configured trusted proxies; none when the settings
// cannot be read (forwarded headers are then ignored).
func (d Deps) proxies(ctx context.Context) []netip.Prefix {
	s, err := d.Settings.Get(ctx)
	if err != nil {
		return nil
	}
	return trustedProxies(s)
}

func inAny(a netip.Addr, nets []netip.Prefix) bool {
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// peerAddr is the address of the TCP peer, if RemoteAddr holds one.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// fromTrustedPeer reports whether the TCP peer is one of the trusted proxies.
func fromTrustedPeer(r *http.Request, trusted []netip.Prefix) bool {
	if len(trusted) == 0 {
		return false
	}
	a, ok := peerAddr(r)
	return ok && inAny(a, trusted)
}

// clientIP is the address of the remote client. It is the host of
// r.RemoteAddr unless that peer is a trusted proxy, in which case the
// X-Forwarded-For chain is walked from the right (the hop each proxy
// appended itself) and the first address that is not itself a trusted proxy
// wins. Entries further left are client-supplied and never consulted. A chain
// made only of trusted proxies yields its leftmost address; an unparseable
// entry or an empty header falls back to the peer.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, ok := peerAddr(r)
	if !ok || !fromTrustedPeer(r, trusted) {
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return h
		}
		return r.RemoteAddr
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, h := range strings.Split(v, ",") {
			hops = append(hops, strings.TrimSpace(h))
		}
	}
	leftmost := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHop(hops[i])
		if !ok {
			return peer.String()
		}
		if !inAny(a, trusted) {
			return a.String()
		}
		leftmost = a
	}
	return leftmost.String()
}

// parseHop reads one X-Forwarded-For entry: an IP, optionally with a port
// (or in brackets for IPv6).
func parseHop(h string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap().WithZone(""), true
	}
	if ap, err := netip.ParseAddrPort(h); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		if a, err := netip.ParseAddr(h[1 : len(h)-1]); err == nil {
			return a.Unmap().WithZone(""), true
		}
	}
	return netip.Addr{}, false
}

// requestIsHTTPS reports whether the client reached the server over TLS:
// this connection is TLS, or a trusted proxy says so in X-Forwarded-Proto
// (the value its nearest proxy set, the last one when several are listed).
func requestIsHTTPS(r *http.Request, trusted []netip.Prefix) bool {
	if r.TLS != nil {
		return true
	}
	if !fromTrustedPeer(r, trusted) {
		return false
	}
	vals := r.Header.Values("X-Forwarded-Proto")
	if len(vals) == 0 {
		return false
	}
	parts := strings.Split(vals[len(vals)-1], ",")
	return strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "https")
}
