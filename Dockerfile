# Copyright (c) 2026 Meizon Inc.
#
# Multi-stage build producing a minimal, static registryd image. The bootstrap
# and CLI binaries ship alongside so the entrypoint can render config on start
# and operators can run registryctl in the same image.

# --- console ---
# Built first and embedded into the Go binary, so one artifact serves both the
# API and the UI from one origin (no CORS, no second web server to operate).
FROM node:22-alpine AS console
WORKDIR /web

COPY apps/registry/package.json apps/registry/package-lock.json* ./
RUN npm ci || npm install

COPY apps/registry/ ./
RUN npm run build

# --- build ---
FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Replace the committed placeholder with the real bundle before compiling, so
# go:embed picks it up.
RUN rm -rf pkg/server/console/dist
COPY --from=console /web/dist ./pkg/server/console/dist

ARG VERSION=dev
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/registryd            ./cmd/registryd \
 && go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/registryd-bootstrap  ./cmd/registryd-bootstrap \
 && go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/registryctl          ./cmd/registryctl

# --- runtime ---
FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/registryd           /usr/local/bin/registryd
COPY --from=build /out/registryd-bootstrap /usr/local/bin/registryd-bootstrap
COPY --from=build /out/registryctl         /usr/local/bin/registryctl

USER nonroot:nonroot
EXPOSE 8080 8081

# The image ships all three binaries. registryd expects a rendered config via
# -cfg-file; an init step (compose one-shot service / Kubernetes init container)
# runs registryd-bootstrap first to render REGISTRYD_* env into a shared volume.
# See compose.prod.yaml and helm/ for the wiring.
ENTRYPOINT ["/usr/local/bin/registryd"]
CMD ["-cfg-file", "/etc/registryd/config.yml"]
