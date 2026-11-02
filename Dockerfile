# syntax=docker/dockerfile:1

# ---- stage 1: build the SPA into internal/web/dist -------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- stage 2: build the static Go binary ----------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X github.com/metril/wattson/internal/cli.Version=${VERSION}" \
      -o /out/wattson ./cmd/wattson \
 && mkdir /data /segments

# ---- stage 3: runtime -----------------------------------------------------
# distroless static (no shell, no package manager), uid/gid 65532.
# Pinned to the static-debian12:nonroot tag as of 2026-11-02; bump by
# re-pulling the tag and updating both the digest and this date.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# /data is created owned by 65532 so a fresh named volume is writable.
COPY --from=build --chown=65532:65532 /data /data
# /segments holds BRouter map regions; the shared compose volume inherits its
# ownership on first use, so wattson can download into it.
COPY --from=build --chown=65532:65532 /segments /segments
COPY --from=build /out/wattson /usr/local/bin/wattson

# Where BlueZ's D-Bus socket is mounted (see compose.yml). The only variable
# the image sets: the data dir and listen address default to /data and :8080.
ENV DBUS_SYSTEM_BUS_ADDRESS=unix:path=/run/dbus/system_bus_socket

USER 65532:65532
WORKDIR /data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD ["/usr/local/bin/wattson", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/wattson"]
CMD ["serve"]
