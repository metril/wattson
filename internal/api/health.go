package api

import (
	"encoding/json"
	"net/http"
)

// HealthResponse is the body of GET /healthz.
type HealthResponse struct {
	Status  string `json:"status" doc:"Always \"ok\" while the process is serving."`
	Version string `json:"version" doc:"Server build version."`
}

func init() {
	registerSpec(func() []opSpec {
		return []opSpec{{
			Method: http.MethodGet, Path: "/healthz", ID: "getHealth", Tag: "system",
			Summary:     "Liveness probe",
			Description: "Returns 200 while the server is running. No authentication required.",
			Out:         HealthResponse{},
		}}
	})
}

func (d Deps) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok", Version: d.Version})
}
