package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type specDoc struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
	Comp  struct {
		Schemas map[string]schemaDoc `json:"schemas"`
	} `json:"components"`
}

type schemaDoc struct {
	Description          string               `json:"description"`
	Properties           map[string]schemaDoc `json:"properties"`
	Items                *schemaDoc           `json:"items"`
	AdditionalProperties json.RawMessage      `json:"additionalProperties"`
	OneOf                []schemaDoc          `json:"oneOf"`
	AnyOf                []schemaDoc          `json:"anyOf"`
	AllOf                []schemaDoc          `json:"allOf"`
}

type opDoc struct {
	ID          string `json:"operationId"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Tags        []string
	Parameters  []struct {
		Name, In, Description string
	}
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}

// docGaps lists every undocumented operation (as "METHOD path"), parameter
// ("METHOD path param name") and schema property ("Schema.field").
func docGaps(raw []byte) ([]string, error) {
	var spec specDoc
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	var bad []string
	for path, item := range spec.Paths {
		for method, rawOp := range item {
			if !httpMethods[method] {
				continue
			}
			var op opDoc
			if err := json.Unmarshal(rawOp, &op); err != nil {
				return nil, err
			}
			if op.ID == "" || op.Summary == "" || op.Description == "" || len(op.Tags) != 1 {
				bad = append(bad, fmt.Sprintf("%s %s", strings.ToUpper(method), path))
			}
			for _, p := range op.Parameters {
				if p.Description == "" {
					bad = append(bad, fmt.Sprintf("%s %s param %s", strings.ToUpper(method), path, p.Name))
				}
			}
		}
	}
	for name, s := range spec.Comp.Schemas {
		walkSchema(name, s, &bad)
	}
	sort.Strings(bad)
	return bad, nil
}

// walkSchema requires a description on every property reachable from s,
// through nested objects, array items, additionalProperties and
// oneOf/anyOf/allOf branches.
func walkSchema(prefix string, s schemaDoc, bad *[]string) {
	for name, p := range s.Properties {
		full := prefix + "." + name
		if p.Description == "" {
			*bad = append(*bad, full)
		}
		walkSchema(full, p, bad)
	}
	if s.Items != nil {
		walkSchema(prefix+"[]", *s.Items, bad)
	}
	if len(s.AdditionalProperties) > 0 && s.AdditionalProperties[0] == '{' {
		var ap schemaDoc
		if json.Unmarshal(s.AdditionalProperties, &ap) == nil {
			walkSchema(prefix+"{}", ap, bad)
		}
	}
	for i, group := range [][]schemaDoc{s.OneOf, s.AnyOf, s.AllOf} {
		for j, b := range group {
			walkSchema(fmt.Sprintf("%s<%s%d>", prefix, []string{"oneOf", "anyOf", "allOf"}[i], j), b, bad)
		}
	}
}

func TestEveryOperationDocumented(t *testing.T) {
	t.Parallel()
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	bad, err := docGaps(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("undocumented: %s", b)
	}
	if !bytes.Contains(raw, []byte(`"/healthz"`)) {
		t.Error("spec has no paths")
	}
}

func TestDocGateListsOffenders(t *testing.T) {
	t.Parallel()
	const spec = `{
	  "paths": {
	    "/ok":  {"get": {"operationId":"ok","summary":"s","description":"d","tags":["t"]}},
	    "/bad": {"post": {"operationId":"bad","summary":"s","tags":["t"],
	      "parameters":[{"name":"id","in":"path"}]}}
	  },
	  "components": {"schemas": {
	    "Thing": {"properties": {
	      "good": {"description":"x"},
	      "naked": {"type":"string"},
	      "list": {"description":"l","items":{"properties":{"inner":{"type":"integer"}}}},
	      "map": {"description":"m","additionalProperties":{"properties":{"v":{"type":"integer"}}}},
	      "either": {"description":"e","oneOf":[{"properties":{"o":{"type":"integer"}}}],
	                 "allOf":[{"properties":{"a":{"type":"integer"}}}]}
	    }}
	  }}
	}`
	got, err := docGaps([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /bad",
		"POST /bad param id",
		"Thing.either<allOf0>.a",
		"Thing.either<oneOf0>.o",
		"Thing.list[].inner",
		"Thing.map{}.v",
		"Thing.naked",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gaps:\n got  %q\n want %q", got, want)
	}
}

func TestRegisterRejectsUndocumentedOperation(t *testing.T) {
	t.Parallel()
	base := route{Method: "GET", Path: "/api/x", ID: "x", Tag: "t", Summary: "s", Description: "d", Scope: "read"}
	for name, mut := range map[string]func(*route){
		"id":          func(r *route) { r.ID = "" },
		"summary":     func(r *route) { r.Summary = "" },
		"description": func(r *route) { r.Description = "" },
		"tag":         func(r *route) { r.Tag = "" },
	} {
		rt := base
		mut(&rt)
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("register accepted an empty %s", name)
				}
			}()
			register(newHumaAPI(chi.NewMux()), rt, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
		}()
	}
	op := opSpec{Method: "GET", Path: "/raw", ID: "raw", Tag: "t", Summary: "s", Scope: "read"}
	defer func() {
		if recover() == nil {
			t.Error("opSpec accepted an empty description")
		}
	}()
	op.operation(newHumaAPI(chi.NewMux()).OpenAPI().Components.Schemas)
}

func TestOpenAPIUpToDate(t *testing.T) {
	t.Parallel()
	got, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatalf("read docs/openapi.json: %v; run: make openapi", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("docs/openapi.json is out of date; run: make openapi")
	}
}
