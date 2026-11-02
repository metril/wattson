package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/maptiles"
	wroute "github.com/metril/wattson/internal/route"
)

// availableTimeout bounds the remote index fetch behind GET /api/maps/tiles.
const availableTimeout = 10 * time.Second

// DirOut describes the segments directory.
type DirOut struct {
	Path     string `json:"path" doc:"Absolute path of the segments directory inside the container."`
	Exists   bool   `json:"exists" doc:"Whether the directory exists."`
	Writable bool   `json:"writable" doc:"Whether Wattson can create files in it."`
}

// TileOut is one map region (a 5 degree BRouter segment file).
type TileOut struct {
	Name       string    `json:"name" doc:"Tile name from its south-west corner, such as E5_N45 (5 degrees east, 45 degrees north)."`
	Status     string    `json:"status" enum:"present,queued,downloading,error" doc:"present: on disk; queued: waiting for its turn; downloading: in progress; error: the last download failed."`
	Bytes      int64     `json:"bytes" doc:"Bytes on disk (present) or received so far (downloading)."`
	TotalBytes int64     `json:"total_bytes" doc:"Size announced by the segments server in bytes; 0 when unknown."`
	Error      string    `json:"error,omitempty" doc:"Why the last download failed, such as HTTP 404."`
	UpdatedAt  time.Time `json:"updated_at" doc:"Last change of the tile's file or download state, UTC."`
}

// TilesOut lists the map regions.
type TilesOut struct {
	Dir            DirOut    `json:"dir" doc:"The segments directory."`
	Tiles          []TileOut `json:"tiles" doc:"Tiles on disk plus queued, downloading and failed downloads, sorted by name."`
	Available      []string  `json:"available" doc:"Tile names the segments server offers; empty when the index could not be read."`
	AvailableError string    `json:"available_error,omitempty" doc:"Why the list of available tiles could not be read."`
}

func tileOut(t maptiles.Tile) TileOut {
	return TileOut{Name: t.Name, Status: string(t.Status), Bytes: t.Bytes, TotalBytes: t.TotalBytes, Error: t.Error,
		UpdatedAt: t.UpdatedAt}
}

type tilesBody struct{ Body TilesOut }
type tileBody struct{ Body TileOut }

type tileNameIn struct {
	Name string `path:"name" doc:"Tile name such as E5_N45."`
}

// tiles returns the tile manager, or a 503 when the server has none.
func (d Deps) tiles() (*maptiles.Manager, error) {
	if d.Tiles == nil {
		return nil, apiErrPublic(http.StatusServiceUnavailable, "maps_unavailable", "map region downloads are not available on this server")
	}
	return d.Tiles, nil
}

func tileNameErr(err error) error {
	switch {
	case errors.Is(err, wroute.ErrBadTileName):
		return apiErr(http.StatusBadRequest, "bad_tile_name", "Not a tile name. Use names like E5_N45 or W5_S35 (multiples of 5 degrees).")
	case errors.Is(err, maptiles.ErrDirUnavailable):
		return apiErr(http.StatusConflict, "segments_dir_unavailable",
			"The segments directory does not exist or is not writable. Check the segments directory in Settings → Integrations.")
	case errors.Is(err, maptiles.ErrNoLocalRegions):
		return apiErr(http.StatusConflict, "no_local_regions",
			"No segments directory is set, so map regions are not managed here. Set one in Settings → Integrations, or leave it empty when using a remote BRouter.")
	case errors.Is(err, maptiles.ErrClosed):
		return apiErrPublic(http.StatusServiceUnavailable, "shutting_down", "the server is shutting down")
	}
	return err
}

func (d Deps) registerMaps(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/maps/tiles", ID: "listMapTiles", Tag: "maps",
		Summary: "List map regions",
		Description: "Returns the segments directory state, the tiles on disk and in the download queue, and the tiles the " +
			"segments server offers (cached for 24 hours; `available_error` explains an unreadable index). Routing needs the " +
			"tiles a route crosses; each covers 5 by 5 degrees. 409 `no_local_regions` when the segments directory setting is empty.",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, _ *struct{}) (*tilesBody, error) {
			m, err := d.tiles()
			if err != nil {
				return nil, err
			}
			dir, ts, err := m.List(ctx)
			if err != nil {
				return nil, tileNameErr(err)
			}
			out := TilesOut{Dir: DirOut{Path: dir.Path, Exists: dir.Exists, Writable: dir.Writable},
				Tiles: make([]TileOut, 0, len(ts)), Available: []string{}}
			for _, t := range ts {
				out.Tiles = append(out.Tiles, tileOut(t))
			}
			actx, cancel := context.WithTimeout(ctx, availableTimeout)
			defer cancel()
			if av, err := m.Available(actx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				out.AvailableError = err.Error()
			} else {
				out.Available = av
			}
			return &tilesBody{Body: out}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/maps/tiles/{name}", ID: "downloadMapTile", Tag: "maps",
		Summary: "Download a map region",
		Description: "Queues the tile for download from the segments server; tiles download one at a time. A tile already on " +
			"disk or queued is returned as it is. Poll the list for progress. Errors: 400 `bad_tile_name`; " +
			"409 `segments_dir_unavailable` (the directory is missing or not writable) or `no_local_regions` (the segments directory setting is empty).",
		Scope: auth.ScopeAdmin, Status: http.StatusAccepted, Errors: []int{http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *tileNameIn) (*tileBody, error) {
			m, err := d.tiles()
			if err != nil {
				return nil, err
			}
			t, err := m.Enqueue(ctx, in.Name)
			if err != nil {
				return nil, tileNameErr(err)
			}
			return &tileBody{Body: tileOut(t)}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/maps/tiles/{name}", ID: "deleteMapTile", Tag: "maps",
		Summary: "Delete a map region",
		Description: "Cancels the tile's download if one is running and deletes the tile and any partial file from the " +
			"segments directory. Succeeds when the tile is not there. 400 `bad_tile_name` for an invalid name; 409 `no_local_regions` when the segments directory setting is empty.",
		Scope: auth.ScopeAdmin, Status: http.StatusNoContent, Errors: []int{http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *tileNameIn) (*noContent, error) {
			m, err := d.tiles()
			if err != nil {
				return nil, err
			}
			if err := m.Remove(ctx, in.Name); err != nil {
				return nil, tileNameErr(err)
			}
			return &noContent{}, nil
		})
}
