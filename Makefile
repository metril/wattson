BINARY  := wattson
VERSION ?= dev
LDFLAGS := -s -w -X github.com/metril/wattson/internal/cli.Version=$(VERSION)

.PHONY: all build go-build test go-test lint clean openapi

all: build

## build: build the Go binary
build: go-build

go-build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/wattson

## test: run the Go test suite
test: go-test

go-test:
	go vet ./...
	go test ./... -race

## openapi: regenerate docs/openapi.json from the Go API
openapi:
	go run ./cmd/wattson openapi > docs/openapi.json

## lint: gofmt check plus go vet
lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf dist
