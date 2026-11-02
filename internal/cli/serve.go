package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/metril/wattson/internal/api"
	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/maptiles"
	"github.com/metril/wattson/internal/oidcauth"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const (
	defaultDataDir = "/data"
	defaultListen  = ":8080"
	dbFile         = "wattson.db"

	// softMemoryLimit keeps the idle RSS inside the 30 MB budget.
	softMemoryLimit = 24 << 20

	releaseTimeout  = 10 * time.Second // returning the bike to its floor at shutdown
	shutdownTimeout = 10 * time.Second // draining HTTP requests at shutdown
	sessionSweep    = time.Hour
)

// serveHooks are test seams for runServer, keyed by data directory (so
// parallel tests never share one): a recording simulator and fast clocks.
type serveHooks struct {
	simDial   func(sim.Config) bike.Dial
	tickEvery time.Duration
}

var serveTestHooks sync.Map // data dir -> serveHooks

// serveConfig is where the server keeps its data and listens.
type serveConfig struct{ DataDir, Listen string }

// resolveServeConfig applies --data-dir/--listen over WATTSON_DATA_DIR /
// WATTSON_LISTEN over the defaults and returns the arguments left over.
func resolveServeConfig(args []string, getenv func(string) string) (serveConfig, []string, error) {
	cfg := serveConfig{DataDir: defaultDataDir, Listen: defaultListen}
	if v := getenv("WATTSON_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("WATTSON_LISTEN"); v != "" {
		cfg.Listen = v
	}
	rest, err := parseServeFlags(&cfg, "serve", args, io.Discard)
	return cfg, rest, err
}

// parseServeFlags parses --data-dir and --listen into cfg, accepting them
// before, between and after positional arguments, and returns the positionals.
func parseServeFlags(cfg *serveConfig, name string, args []string, out io.Writer) ([]string, error) {
	fs := flag.NewFlagSet("wattson "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "directory holding wattson.db and secret.key (env WATTSON_DATA_DIR)")
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "address to listen on (env WATTSON_LISTEN)")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (d deps) env(k string) string {
	if d.getenv != nil {
		return d.getenv(k)
	}
	return os.Getenv(k)
}

// resolveCmdConfig is resolveServeConfig for a subcommand: flag errors are
// printed and turned into exit code 2 (0 for -h).
func resolveCmdConfig(d deps, name string, args []string, stderr io.Writer) (cfg serveConfig, rest []string, code int, ok bool) {
	cfg, rest, err := resolveServeConfig(nil, d.env)
	if err == nil {
		rest, err = parseServeFlags(&cfg, name, args, stderr)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, nil, 0, false
		}
		return cfg, nil, 2, false
	}
	return cfg, rest, 0, true
}

func cmdServe(ctx context.Context, d deps, args []string, _ io.Writer, stderr io.Writer) int {
	cfg, rest, code, ok := resolveCmdConfig(d, "serve", args, stderr)
	if !ok {
		return code
	}
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "wattson serve: unexpected argument %q\n", rest[0])
		return 2
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); !set {
		debug.SetMemoryLimit(softMemoryLimit)
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	lastLevel.Store(serveLevel) // a forced exit then says the bike may still be raised
	defer lastLevel.Store(0)
	if err := runServer(ctx, cfg, logger, nil); err != nil {
		logger.Error("server failed", "err", err)
		return 1
	}
	return 0
}

// runServer opens the data directory, serves HTTP on cfg.Listen and, once ctx
// is cancelled, returns the bike to its floor level and shuts down. ready, if
// set, is called with the bound address once the server accepts connections.
// A bike that could not be confirmed at its floor is logged as an error, not
// returned: the shutdown itself still succeeded.
func runServer(ctx context.Context, cfg serveConfig, logger *slog.Logger, ready func(addr string)) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	box, err := secretbox.LoadOrCreateKey(cfg.DataDir)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, dbFile))
	if err != nil {
		return err
	}
	defer st.Close()

	svc := settings.NewService(st, box)
	au := auth.NewAuthenticator(st, func(ctx context.Context) string {
		s, err := svc.Get(ctx)
		if err != nil {
			return ""
		}
		return s.General.PublicURL
	}, logger)
	client := &http.Client{Timeout: 15 * time.Second}
	rd := ridectl.Deps{Settings: svc.Get, LoadRoute: ridectl.StoreRoutes(st), Logger: logger}
	if v, ok := serveTestHooks.Load(cfg.DataDir); ok {
		h := v.(serveHooks)
		rd.SimDial, rd.TickEvery, rd.SimPeriod = h.simDial, h.tickEvery, h.tickEvery
	}
	// Settle rides a crash left open before a new one can start.
	if fin, del, err := history.Recover(ctx, st, minRideSamples); err != nil {
		logger.Warn("ride recovery incomplete", "finished", fin, "deleted", del, "err", err)
	} else if fin+del > 0 {
		logger.Info("recovered unfinished rides", "finished", fin, "deleted", del)
	}
	mgr := ridectl.New(rd)
	events, unsub := mgr.Subscribe(256)
	recCtx, stopRec := context.WithCancel(context.Background())
	recDone := make(chan struct{})
	rec := history.NewRecorder(history.RecorderDeps{
		Store:      st,
		RouteInfo:  storeRouteInfo(st),
		MinSamples: minRideSamples,
		Logger:     logger,
	})
	go func() {
		defer close(recDone)
		_ = rec.Run(recCtx, events)
	}()
	var closeBike sync.Once
	releaseBike := func() {
		closeBike.Do(func() {
			// Closing the manager ends the final ride; the recorder then
			// finalizes it before the store closes.
			releaseAtShutdown(mgr, logger)
			unsub()
			stopRec()
			<-recDone
		})
	}
	// Tile downloads run for minutes: no overall client timeout (the manager
	// aborts a stalled transfer itself) and cancelled on shutdown below.
	tiles := maptiles.New(svc.Get, &http.Client{})
	defer tiles.Close()
	h, err := api.NewServer(api.Deps{Store: st, Settings: svc, Box: box, Auth: au,
		OIDC: oidcauth.NewRegistry(client), HTTPClient: client, Logger: logger, Version: Version, Bike: mgr, Tiles: tiles})
	if err != nil {
		releaseBike()
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		releaseBike()
		return fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	addr := ln.Addr().String()
	logger.Info("wattson listening", "addr", addr, "data_dir", cfg.DataDir, "version", Version)
	if n, err := st.CountUsers(ctx); err == nil && n == 0 {
		logger.Warn("setup required: open the URL to create the first admin", "url", setupURL(ln.Addr()))
	}
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	go sweepSessions(sweepCtx, st, logger, sessionSweep)
	if ready != nil {
		ready(addr)
	}

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-serveErr:
		releaseBike()
		return fmt.Errorf("serve: %w", err)
	}

	releaseBike()
	tiles.Close() // cancel any map download so its requests end before HTTP drains
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		logger.Warn("http shutdown incomplete; closing connections", "err", err)
		_ = srv.Close()
	}
	logger.Info("stopped")
	return nil
}

// minRideSamples is the shortest ride (seconds at 1 Hz) worth keeping.
const minRideSamples = 10

// storeRouteInfo names and decodes a stored route for the ride recorder.
func storeRouteInfo(st *store.Store) func(context.Context, int64) (string, *route.Profile, error) {
	return func(ctx context.Context, id int64) (string, *route.Profile, error) {
		r, err := st.Route(ctx, id)
		if err != nil {
			return "", nil, err
		}
		p, err := route.Decode(r.Points)
		if err != nil {
			return "", nil, err
		}
		return r.Name, p, nil
	}
}

// releaseAtShutdown ends any ride and returns the bike to its floor level,
// then reports honestly whether that was confirmed.
func releaseAtShutdown(mgr *ridectl.Manager, logger *slog.Logger) {
	held := mgr.Status().State != "idle"
	rctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	switch err := mgr.Close(rctx); {
	case err != nil:
		logger.Error("bike NOT confirmed back at its minimum level; lower the resistance at the console", "err", err)
	case held:
		logger.Info("bike released at its minimum level")
	}
}

// setupURL is where to open the setup wizard, given the bound address.
func setupURL(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return "http://localhost/"
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	p, _ := strconv.Atoi(port)
	return "http://" + net.JoinHostPort(host, strconv.Itoa(p)) + "/"
}

// sweepSessions deletes expired sessions every period until ctx ends.
func sweepSessions(ctx context.Context, st *store.Store, logger *slog.Logger, period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.DeleteExpiredSessions(ctx, time.Now()); err != nil {
				if ctx.Err() == nil {
					logger.Warn("delete expired sessions", "err", err)
				}
			} else if n > 0 {
				logger.Info("deleted expired sessions", "count", n)
			}
		}
	}
}
