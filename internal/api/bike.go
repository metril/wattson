package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/store"
)

type bikeOut struct {
	Body ridectl.Status
}

type connectIn struct {
	Body struct {
		RiderID int64 `json:"rider_id" minimum:"1" doc:"ID of one of your riders; the bike is bound to this rider until released."`
	}
}

// ScanOut is the result of a bike scan.
type ScanOut struct {
	Devices []ridectl.Device `json:"devices" doc:"Devices seen while scanning, supported bikes first, then by signal strength."`
}

type scanIn struct {
	Body struct {
		Transport string `json:"transport,omitempty" enum:"local,esphome" doc:"local (this host's adapter) or esphome (a Bluetooth proxy); empty uses the saved setting."`
		Host      string `json:"host,omitempty" doc:"ESPHome proxy as host or host:port; empty uses the saved setting."`
		Key       string `json:"key,omitempty" doc:"ESPHome API encryption key (base64); empty uses the saved setting."`
		DurationS int    `json:"duration_s,omitempty" minimum:"1" maximum:"30" default:"5" doc:"How long to scan, in seconds, 1 to 30."`
	}
}

type scanOut struct {
	Body ScanOut
}

// bikeFailure maps a manager error onto the API envelope. Errors it does not
// know stay as-is (logged, then reported as a generic 500).
func bikeFailure(err error) error {
	switch {
	case errors.Is(err, ridectl.ErrBusy):
		return apiErr(http.StatusConflict, "bike_busy", "a bike is already connected or a scan is running")
	case errors.Is(err, ridectl.ErrRiding):
		return apiErr(http.StatusConflict, "ride_active", "a ride is in progress; stop it or release the bike")
	case errors.Is(err, ridectl.ErrNoAddress):
		return apiErr(http.StatusBadRequest, "no_bike_address", "no bike address is configured in the settings")
	case errors.Is(err, ridectl.ErrNoHost):
		return apiErr(http.StatusBadRequest, "no_esphome_host", "no ESPHome host is configured")
	case errors.Is(err, ridectl.ErrClosed):
		return apiErr(http.StatusConflict, "shutting_down", "the server is shutting down")
	}
	return err
}

// releaseFailure reports the outcome of a release. The bike is released
// either way, so a bike not confirmed at its minimum level is a 424 whose
// message tells the rider to lower the resistance by hand.
func releaseFailure(err error) error {
	if err == nil {
		return nil
	}
	if e := bikeFailure(err); e != err {
		return e
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return apiErr(http.StatusFailedDependency, "floor_unconfirmed",
		"the bike was released but could not be confirmed back at its minimum level; lower the resistance at the console ("+err.Error()+")")
}

const noBikeMsg = "this server has no bike manager"

// bikeMgr returns the bike manager, or a 503 no_bike_manager when the server
// was built without one.
func (d Deps) bikeMgr() (*ridectl.Manager, error) {
	if d.Bike == nil {
		return nil, apiErrPublic(http.StatusServiceUnavailable, "no_bike_manager", noBikeMsg)
	}
	return d.Bike, nil
}

func (d Deps) registerBike(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/bike", ID: "getBike", Tag: "bike",
		Summary:     "Get the bike connection",
		Description: "Returns the state of the single bike this server controls: idle, connecting, ready, riding, paused or finished, the bound rider, the live protocol and any connection problem with advice to fix it.",
		Scope:       auth.ScopeRead, Errors: []int{http.StatusServiceUnavailable}},
		func(_ context.Context, _ *noIn) (*bikeOut, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			return &bikeOut{Body: m.Status()}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/bike/connect", ID: "connectBike", Tag: "bike",
		Summary: "Connect the bike",
		Description: "Binds one of the caller's riders to the bike (or the simulator) and returns at once with state `connecting`; " +
			"watch `GET /api/bike` or the WebSocket for `ready` or a connection problem. Any user with control may drive the single bike. " +
			"Returns 409 `bike_busy` unless the bike is idle, 400 `no_bike_address` when the settings name no bike, " +
			"and 404 for a rider that is not the caller's.",
		Scope: auth.ScopeControl, Status: http.StatusAccepted, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *connectIn) (*bikeOut, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			id, _ := auth.FromContext(ctx)
			r, err := d.Store.Rider(ctx, id.User.ID, in.Body.RiderID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRider()
			}
			if err != nil {
				return nil, err
			}
			st, err := m.Connect(ctx, r)
			if err != nil {
				return nil, bikeFailure(err)
			}
			return &bikeOut{Body: st}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/bike/disconnect", ID: "disconnectBike", Tag: "bike",
		Summary: "Disconnect the bike",
		Description: "Returns the bike to the rider's minimum level and closes the link. Does nothing when idle. " +
			"Refused with 409 `ride_active` while a ride is riding or paused; use release to end the ride too. " +
			"Returns 424 `floor_unconfirmed` when the bike was released but not confirmed back at its minimum level.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict, http.StatusFailedDependency, http.StatusServiceUnavailable}},
		func(ctx context.Context, _ *noIn) (*noContent, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			if err := m.Disconnect(ctx); err != nil {
				return nil, releaseFailure(err)
			}
			return &noContent{}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/bike/release", ID: "releaseBike", Tag: "bike",
		Summary: "Release the bike",
		Description: "Ends any ride, returns the bike to the rider's minimum level and closes the link. Idempotent: releasing an idle bike succeeds. " +
			"Returns 424 `floor_unconfirmed` when the bike was released but could not be confirmed back at its minimum level; " +
			"the rider must then lower the resistance at the console.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusFailedDependency, http.StatusServiceUnavailable}},
		func(ctx context.Context, _ *noIn) (*noContent, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			if err := m.Release(ctx); err != nil {
				return nil, releaseFailure(err)
			}
			return &noContent{}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/bike/scan", ID: "scanBikes", Tag: "bike",
		Summary: "Scan for bikes",
		Description: "Scans for Bluetooth devices for `duration_s` seconds (default 5) and lists them, supported bikes first. " +
			"Empty transport fields use the saved settings. Refused with 409 `bike_busy` while a bike is connected or another scan runs; " +
			"424 `scan_failed` when the adapter or proxy cannot be used.",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusConflict, http.StatusFailedDependency, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *scanIn) (*scanOut, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			devs, err := m.Scan(ctx, ridectl.ScanRequest{Transport: in.Body.Transport, Host: in.Body.Host,
				Key: in.Body.Key, DurationS: in.Body.DurationS})
			if err != nil {
				if e := bikeFailure(err); e != err {
					return nil, e
				}
				if ctx.Err() != nil {
					return nil, err
				}
				return nil, apiErr(http.StatusFailedDependency, "scan_failed", err.Error()+". "+ridectl.Hint(err))
			}
			if devs == nil {
				devs = []ridectl.Device{}
			}
			return &scanOut{Body: ScanOut{Devices: devs}}, nil
		})
}
