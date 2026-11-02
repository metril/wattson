package cli

import (
	"fmt"
	"io"

	"github.com/metril/wattson/internal/api"
)

// cmdOpenAPI prints the OpenAPI document (what `make openapi` stores in
// docs/openapi.json).
func cmdOpenAPI(stdout, stderr io.Writer) int {
	b, err := api.OpenAPIJSON()
	if err != nil {
		fmt.Fprintf(stderr, "wattson openapi: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(b); err != nil {
		fmt.Fprintf(stderr, "wattson openapi: %v\n", err)
		return 1
	}
	return 0
}

// cmdAsyncAPI prints the AsyncAPI document of the WebSocket (what
// `make openapi` stores in docs/asyncapi.yaml).
func cmdAsyncAPI(stdout, stderr io.Writer) int {
	b, err := api.AsyncAPIDoc()
	if err != nil {
		fmt.Fprintf(stderr, "wattson asyncapi: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(b); err != nil {
		fmt.Fprintf(stderr, "wattson asyncapi: %v\n", err)
		return 1
	}
	return 0
}
