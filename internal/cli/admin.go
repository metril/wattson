package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const adminUsage = "usage: wattson admin [--data-dir DIR] <reset-password USER | disable-oidc>\n"

// cmdAdmin runs the break-glass commands straight against the data directory.
// They are safe while the server runs (SQLite WAL, busy timeout) and take
// effect at once because the server reads providers, settings and sessions
// from the database on every request.
func cmdAdmin(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	cfg, rest, code, ok := resolveCmdConfig(d, "admin", args, stderr)
	if !ok {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	sub, rest := rest[0], rest[1:]
	switch {
	case sub == "reset-password" && len(rest) == 1:
		return withStore(ctx, cfg, stderr, func(st *store.Store) int { return resetPassword(ctx, st, rest[0], stdout, stderr) })
	case sub == "disable-oidc" && len(rest) == 0:
		return withStore(ctx, cfg, stderr, func(st *store.Store) int { return disableOIDC(ctx, st, stdout, stderr) })
	}
	fmt.Fprint(stderr, adminUsage)
	return 2
}

// withStore opens an existing database (never creating one by mistake).
func withStore(_ context.Context, cfg serveConfig, stderr io.Writer, f func(*store.Store) int) int {
	path := filepath.Join(cfg.DataDir, dbFile)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "wattson admin: no database at %s (is --data-dir right?)\n", path)
		return 1
	}
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "wattson admin: open database: %v\n", err)
		return 1
	}
	defer st.Close()
	return f(st)
}

func resetPassword(ctx context.Context, st *store.Store, user string, stdout, stderr io.Writer) int {
	u, err := st.UserByUsername(ctx, user)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "no such user: %s\n", user)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "wattson admin: %v\n", err)
		return 1
	}
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		fmt.Fprintf(stderr, "wattson admin: generate password: %v\n", err)
		return 1
	}
	pw := base64.RawURLEncoding.EncodeToString(raw) // 24 characters
	hash, err := auth.HashPassword(ctx, pw)
	if err != nil {
		fmt.Fprintf(stderr, "wattson admin: hash password: %v\n", err)
		return 1
	}
	if err := st.SetPassword(ctx, u.ID, hash); err != nil {
		fmt.Fprintf(stderr, "wattson admin: %v\n", err)
		return 1
	}
	if _, err := st.DeleteUserSessions(ctx, u.ID); err != nil {
		fmt.Fprintf(stderr, "wattson admin: password set but sessions not revoked: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "password for %s: %s\n", user, pw)
	if u.Disabled {
		fmt.Fprintf(stderr, "warning: %s is disabled and cannot sign in until re-enabled\n", user)
	}
	if !u.IsBootstrap {
		if on, err := settings.LocalLoginEnabled(ctx, st); err == nil && !on {
			fmt.Fprintf(stderr, "warning: password sign-in is turned off, so %s cannot use this password; "+
				"run `wattson admin disable-oidc` to turn it back on, or sign in as the bootstrap admin\n", user)
		}
	}
	return 0
}

func disableOIDC(ctx context.Context, st *store.Store, stdout, stderr io.Writer) int {
	n, err := st.DisableAllOIDCProviders(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "wattson admin: %v\n", err)
		return 1
	}
	if err := settings.EnableLocalLogin(ctx, st); err != nil {
		fmt.Fprintf(stderr, "wattson admin: providers disabled but local login not enabled: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "disabled %d OIDC providers; local login enabled\n", n)
	return 0
}
