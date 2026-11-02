package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const wellKnown = "/.well-known/openid-configuration"

// ResolveIssuer fetches the discovery document at discoveryURL (the
// ".well-known/openid-configuration" suffix is appended when missing) and
// returns its "issuer" exactly as published. Providers differ in whether
// the issuer carries a trailing slash (Authentik's does), and ID token
// verification compares the strings exactly, so the issuer must come from
// the document rather than from the URL the operator typed.
func ResolveIssuer(ctx context.Context, client *http.Client, discoveryURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(discoveryURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("oidcauth: discovery URL must be an absolute http(s) URL")
	}
	u.RawQuery, u.Fragment = "", ""
	if !strings.HasSuffix(u.Path, wellKnown) {
		u.Path = strings.TrimRight(u.Path, "/") + wellKnown
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("oidcauth: discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidcauth: fetch discovery document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidcauth: discovery document: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", fmt.Errorf("oidcauth: decode discovery document: %w", err)
	}
	if doc.Issuer == "" {
		return "", errors.New("oidcauth: discovery document has no issuer")
	}
	return doc.Issuer, nil
}
