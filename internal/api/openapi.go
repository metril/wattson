package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/metril/wattson/internal/auth"
)

// Typed handlers are registered through huma, which derives the OpenAPI
// document from their Input/Output structs. opSpec describes the few
// operations that stay on raw chi handlers (healthz, OIDC redirects, the
// WebSocket): they are documented only.

// p describes one path or query parameter.
type p struct {
	Name, Doc, Type string // Type: integer (default) or string
	Query           bool
	Required        bool
	Enum            []any
}

// opSpec documents one raw HTTP operation.
type opSpec struct {
	Method, Path, ID, Tag, Summary, Description string
	Params                                      []p
	Body                                        any // request body type (JSON), nil for none
	OptionalBody                                bool
	Scope                                       auth.Scope // "" = public
	Status                                      int        // success status (default 200, or 204 without Out)
	Out                                         any        // response body type, nil for no body
	OutType                                     string     // response media type override
	Errors                                      []int      // extra error statuses
	NoStdErrors                                 bool       // skip the 400/404 implied by Params (redirect-only routes)
}

// specGroups collects docs-only operations; each file adds its own.
var specGroups []func() []opSpec

func registerSpec(f func() []opSpec) { specGroups = append(specGroups, f) }

func (s opSpec) operation(reg huma.Registry) *huma.Operation {
	if s.ID == "" || s.Summary == "" || s.Description == "" || s.Tag == "" {
		panic(fmt.Sprintf("api: %s %s must have ID, Summary, Description and Tag", s.Method, s.Path))
	}
	op := &huma.Operation{
		OperationID: s.ID,
		Method:      s.Method,
		Path:        s.Path,
		Summary:     s.Summary,
		Description: s.Description,
		Tags:        []string{s.Tag},
		Responses:   map[string]*huma.Response{},
	}
	for _, q := range s.Params {
		typ := q.Type
		if typ == "" {
			typ = "integer"
		}
		in := "path"
		if q.Query {
			in = "query"
		}
		op.Parameters = append(op.Parameters, &huma.Param{Name: q.Name, In: in, Description: q.Doc,
			Required: q.Required || !q.Query,
			Schema:   &huma.Schema{Type: typ, Enum: q.Enum}})
	}
	if s.Body != nil {
		op.RequestBody = &huma.RequestBody{
			Required: !s.OptionalBody,
			Content: map[string]*huma.MediaType{"application/json": {
				Schema: reg.Schema(reflect.TypeOf(s.Body), true, ""),
			}},
		}
	}
	status := s.Status
	if status == 0 {
		status = http.StatusOK
		if s.Out == nil {
			status = http.StatusNoContent
		}
	}
	ok := &huma.Response{Description: http.StatusText(status)}
	if s.Out != nil {
		mt := s.OutType
		if mt == "" {
			mt = "application/json"
		}
		ok.Content = map[string]*huma.MediaType{mt: {
			Schema: reg.Schema(reflect.TypeOf(s.Out), true, ""),
		}}
	}
	op.Responses[strconv.Itoa(status)] = ok
	errs := map[int]bool{}
	if s.Scope != "" {
		errs[http.StatusUnauthorized] = true
		errs[http.StatusForbidden] = true
	}
	if !s.NoStdErrors && (s.Body != nil || len(s.Params) > 0) {
		errs[http.StatusBadRequest] = true
	}
	for _, q := range s.Params {
		if !q.Query && !s.NoStdErrors {
			errs[http.StatusNotFound] = true
		}
	}
	for _, e := range s.Errors {
		errs[e] = true
	}
	errSchema := reg.Schema(reflect.TypeOf(APIError{}), true, "")
	codes := make([]int, 0, len(errs))
	for c := range errs {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	for _, c := range codes {
		op.Responses[strconv.Itoa(c)] = &huma.Response{
			Description: http.StatusText(c),
			Content:     map[string]*huma.MediaType{"application/json": {Schema: errSchema}},
		}
	}
	if s.Scope == "" {
		op.Security = []map[string][]string{}
	} else {
		op.Security = []map[string][]string{{"bearer": {}}, {"cookie": {}}}
		op.Description += " Requires the `" + string(s.Scope) + "` scope."
	}
	return op
}

// addDocsOnly documents the routes that stay on raw chi handlers.
func addDocsOnly(api huma.API) {
	oapi := api.OpenAPI()
	reg := oapi.Components.Schemas
	for _, g := range specGroups {
		for _, s := range g() {
			oapi.AddOperation(s.operation(reg))
		}
	}
}

// registerAPI registers every typed operation. Later tasks add their
// register* calls here.
func (d Deps) registerAPI(api huma.API) {
	d.registerSetup(api)
	d.registerAuthn(api)
	d.registerUsers(api)
	d.registerTokens(api)
	d.registerSettings(api)
	d.registerRiders(api)
	d.registerRoutes(api)
	d.registerRouteStats(api)
	d.registerWorkouts(api)
	d.registerDraw(api)
	d.registerMaps(api)
	d.registerMapillary(api)
	d.registerOIDCProviders(api)
	d.registerBike(api)
	d.registerRide(api)
	d.registerRides(api)
	d.registerStats(api)
	d.registerRideCurve(api)
	d.registerStrava(api)
	d.registerDiagnostics(api)
	d.registerHA(api)
	d.registerMQTT(api)
}

// finalizeSpec removes the responses huma adds on its own that this API
// never produces (validation failures report 400) and the duplicate error
// schema they referenced.
func finalizeSpec(api huma.API) {
	oapi := api.OpenAPI()
	for _, item := range oapi.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op != nil {
				if _, keep := op.Extensions[keep422]; keep {
					delete(op.Extensions, keep422)
				} else {
					delete(op.Responses, "422")
				}
				delete(op.Responses, "default")
			}
		}
	}
}

// OpenAPIJSON renders the API description as indented JSON. It registers
// every operation regardless of which dependencies a running server has.
func OpenAPIJSON() ([]byte, error) {
	api := newHumaAPI(chi.NewMux())
	Deps{}.registerAPI(api)
	addDocsOnly(api)
	finalizeSpec(api)
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
