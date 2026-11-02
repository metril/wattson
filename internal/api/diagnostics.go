package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/ridectl"
)

type diagnosticsIn struct {
	ScanS int `query:"scan_s" minimum:"0" maximum:"10" default:"0" doc:"Seconds to scan for the configured bike and heart rate monitor to read their signal strength, 0 to 10. 0 skips the scan."`
}

type diagnosticsOut struct {
	Body ridectl.Diagnostics
}

func (d Deps) registerDiagnostics(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/diagnostics/bluetooth", ID: "getBluetoothDiagnostics", Tag: "diagnostics",
		Summary: "Check the Bluetooth path",
		Description: "Checks the host's Bluetooth adapter or the ESPHome Bluetooth proxy that the bike and heart rate monitor use, " +
			"with the exact fix for each problem found, and lists the configured devices. " +
			"While a bike is connected the check reuses its live transport; when idle it opens a temporary one and closes it again, " +
			"and a bike connect or scan arriving meanwhile gets 409 `bike_busy`. It never returns 409 itself: a running scan is reported as code `busy`. " +
			"With `scan_s` above 0 it scans for that long and reports each device's signal strength (`rssi`), absent when the device was not seen. " +
			"It briefly subscribes to an idle ESPHome proxy, which takes the proxy from another client (such as Home Assistant) until it closes. " +
			"No secret appears in the result.",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusServiceUnavailable}},
		func(ctx context.Context, in *diagnosticsIn) (*diagnosticsOut, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			return &diagnosticsOut{Body: m.Diagnose(ctx, time.Duration(in.ScanS)*time.Second)}, nil
		})
}
