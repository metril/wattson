package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const healthTimeout = 3 * time.Second

// cmdHealthcheck is the Docker HEALTHCHECK: GET /healthz on the loopback
// address of the configured listen port; exit 0 on 200, otherwise 1.
func cmdHealthcheck(ctx context.Context, d deps, args []string, _ io.Writer, stderr io.Writer) int {
	cfg, rest, code, ok := resolveCmdConfig(d, "healthcheck", args, stderr)
	if !ok {
		return code
	}
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "wattson healthcheck: unexpected argument %q\n", rest[0])
		return 2
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "wattson healthcheck: bad listen address %q: %v\n", cfg.Listen, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
	if err != nil {
		fmt.Fprintf(stderr, "wattson healthcheck: %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "wattson healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "wattson healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
