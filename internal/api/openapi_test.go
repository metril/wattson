package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

type specDoc struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
	Comp  struct {
		Schemas map[string]schemaDoc `json:"schemas"`
	} `json:"components"`
}

type schemaDoc struct {
	Description string               `json:"description"`
	Properties  map[string]schemaDoc `json:"properties"`
	Items       *schemaDoc           `json:"items"`
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}

func TestEveryOperationDocumented(t *testing.T) {
	t.Parallel()
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec specDoc
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var bad []string
	for path, item := range spec.Paths {
		for method, rawOp := range item {
			if !httpMethods[method] {
				continue
			}
			var op struct {
				ID, Summary, Description string
				Tags                     []string
			}
			var m map[string]json.RawMessage
			_ = json.Unmarshal(rawOp, &m)
			_ = json.Unmarshal(m["operationId"], &op.ID)
			_ = json.Unmarshal(m["summary"], &op.Summary)
			_ = json.Unmarshal(m["description"], &op.Description)
			_ = json.Unmarshal(m["tags"], &op.Tags)
			if op.ID == "" || op.Summary == "" || op.Description == "" || len(op.Tags) != 1 {
				bad = append(bad, fmt.Sprintf("%s %s (id=%q summary=%q description=%q tags=%d)",
					method, path, op.ID, op.Summary, op.Description, len(op.Tags)))
			}
		}
	}
	for name, s := range spec.Comp.Schemas {
		walkSchema(name, s, &bad)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("undocumented: %s", b)
	}
	if len(spec.Paths) == 0 {
		t.Error("spec has no paths")
	}
}

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
