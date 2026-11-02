BINARY  := wattson
VERSION ?= dev
LDFLAGS := -s -w -X github.com/metril/wattson/internal/cli.Version=$(VERSION)

.PHONY: all build web-build web-deps go-build test go-test web-test lint clean openapi serve e2e perf

all: build

## build: build the web UI into internal/web/dist, then the Go binary
build: web-build go-build

web-build: web-deps
	cd web && npm run build && npm run size

web-deps: web/node_modules/.package-lock.json

web/node_modules/.package-lock.json: web/package-lock.json
	cd web && npm ci

go-build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/wattson

## serve: build and run the server on :8080 with the data in ./data
serve: build
	./$(BINARY) serve --data-dir ./data --listen :8080

## test: run the Go and web test suites
test: go-test web-test

web-test: web-deps
	cd web && npm test

go-test:
	go vet ./...
	go test ./... -race

## openapi: regenerate docs/openapi.json, docs/asyncapi.yaml and the TypeScript types
openapi: web-deps
	go run ./cmd/wattson openapi > docs/openapi.json
	go run ./cmd/wattson asyncapi > docs/asyncapi.yaml
	cd web && npm run openapi

## e2e: Playwright smoke over the simulator (needs `npx playwright install chromium` once)
e2e: build
	cd web && npm run e2e

## perf: Lighthouse budgets (mobile) against the built binary; reports in web/lighthouse-report
perf: build
	cd web && npm run perf

## lint: gofmt check, go vet, the street photo selection budget and the web lint
lint: web-deps
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -run Budget -count=1 ./internal/mapillary/
	cd web && npm run lint

clean:
	rm -f $(BINARY)
	rm -rf dist internal/web/dist/assets internal/web/dist/index.html
