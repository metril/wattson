package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/ridectl"
)

const (
	// haListTimeout bounds the Home Assistant call behind the entity pickers.
	haListTimeout = 15 * time.Second
	// haReadLimit is the largest Home Assistant message accepted: get_states of
	// a large installation is several MiB.
	haReadLimit = 16 << 20
	// haEntityCap bounds the picker list.
	haEntityCap = 1000
)

type haEntitiesIn struct {
	Domain      string `query:"domain" enum:"number,sensor" required:"true" doc:"Entity domain to list: number or sensor."`
	DeviceClass string `query:"device_class" doc:"Only entities with this device_class attribute, such as power or heart_rate. Empty lists all."`
}

// HAEntity is one Home Assistant entity for a picker.
type HAEntity struct {
	EntityID    string `json:"entity_id" doc:"Entity id, such as sensor.bike_power."`
	Name        string `json:"name" doc:"Friendly name, or the entity id when it has none."`
	State       string `json:"state" doc:"Current state as Home Assistant reports it, such as 150 or unavailable."`
	Unit        string `json:"unit" doc:"Unit of measurement, empty when none."`
	DeviceClass string `json:"device_class" doc:"Device class, empty when none."`
}

// HAEntities is the entity list for a picker.
type HAEntities struct {
	Entities  []HAEntity `json:"entities" doc:"Matching entities sorted by entity_id, at most 1000."`
	Truncated bool       `json:"truncated" doc:"True when more than 1000 entities matched and the list was cut; narrow it with device_class."`
}

type haEntitiesOut struct {
	Body HAEntities
}

func (d Deps) registerHA(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/ha/entities", ID: "listHAEntities", Tag: "settings",
		Summary: "List Home Assistant entities",
		Description: "Lists the number or sensor entities of the saved Home Assistant, sorted by entity id and capped at 1000, " +
			"for the settings pickers. It always uses the saved URL and token, never ones from the request. " +
			"Returns 400 `ha_not_configured` without a saved URL and token and 502 `ha_unavailable` when Home Assistant cannot be reached " +
			"or rejects the token (the message says which).",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusBadGateway}},
		func(ctx context.Context, in *haEntitiesIn) (*haEntitiesOut, error) {
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			base, token := s.HA.URL, s.HA.Token
			if strings.TrimSpace(base) == "" || token == "" {
				return nil, apiErr(http.StatusBadRequest, "ha_not_configured", "Home Assistant URL and token are not configured")
			}
			ctx, cancel := context.WithTimeout(ctx, haListTimeout)
			defer cancel()
			states, err := listHAStates(ctx, base, token)
			if err != nil {
				if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, err // the client went away
				}
				if errors.Is(err, hass.ErrAuth) {
					return nil, apiErrPublic(http.StatusBadGateway, "ha_unavailable", ridectl.Hint(hass.ErrAuth))
				}
				msg := err.Error()
				if len(token) >= 8 { // a short token would mangle unrelated text
					msg = strings.ReplaceAll(msg, token, "[redacted]")
				}
				return nil, apiErrPublic(http.StatusBadGateway, "ha_unavailable", msg)
			}
			return &haEntitiesOut{Body: pickEntities(states, in.Domain, in.DeviceClass)}, nil
		})
}

func listHAStates(ctx context.Context, base, token string) ([]hass.State, error) {
	cl, err := hass.Dial(ctx, base, token, hass.Options{ReadLimit: haReadLimit})
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	return cl.States(ctx)
}

// pickEntities keeps the entities of domain (and deviceClass when set), sorted
// by id and capped.
func pickEntities(states []hass.State, domain, deviceClass string) HAEntities {
	out := HAEntities{Entities: []HAEntity{}}
	for _, st := range states {
		if !strings.HasPrefix(st.EntityID, domain+".") {
			continue
		}
		dc, _ := st.Attributes["device_class"].(string)
		if deviceClass != "" && dc != deviceClass {
			continue
		}
		name, _ := st.Attributes["friendly_name"].(string)
		if name == "" {
			name = st.EntityID
		}
		unit, _ := st.Attributes["unit_of_measurement"].(string)
		out.Entities = append(out.Entities, HAEntity{EntityID: st.EntityID, Name: name, State: st.State, Unit: unit, DeviceClass: dc})
	}
	sort.Slice(out.Entities, func(i, j int) bool { return out.Entities[i].EntityID < out.Entities[j].EntityID })
	if len(out.Entities) > haEntityCap {
		out.Entities, out.Truncated = out.Entities[:haEntityCap], true
	}
	return out
}
