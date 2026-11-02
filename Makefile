BINARY  := wattson
VERSION ?= dev
LDFLAGS := -s -w -X github.com/metril/wattson/internal/cli.Version=$(VERSION)

.PHONY: all build go-build test go-test lint clean openapi serve

all: build

## build: build the Go binary
build: go-build

go-build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/wattson

## serve: build and run the server on :8080 with the data in ./data
serve: build
	./$(BINARY) serve --data-dir ./data --listen :8080

## test: run the Go test suite
test: go-test

go-test:
	go vet ./...
	go test ./... -race

## openapi: regenerate docs/openapi.json and docs/asyncapi.yaml from the Go API
openapi:
	go run ./cmd/wattson openapi > docs/openapi.json
	go run ./cmd/wattson asyncapi > docs/asyncapi.yaml

## lint: gofmt check plus go vet
lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf dist
