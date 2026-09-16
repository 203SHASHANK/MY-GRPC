# Dockerfile for notification-service.
#
# Same shape as user-service/Dockerfile: the build context is the repo root
# (grpc-demo/), not this folder, because go.mod carries
# `replace github.com/demo/grpc-demo/proto => ../proto` and that shared module
# must be inside the context. All COPY paths below are relative to grpc-demo/.

# ---- Stage 1: build ----
#
# NOTE: go.mod declares `go 1.25.0` but this base image is 1.22, so Go will try
# to auto-download the newer toolchain during the build. Bump to
# golang:1.25-alpine to avoid it.
FROM golang:1.22-alpine AS builder
WORKDIR /app

# The shared contract module.
COPY proto/ ./proto/

WORKDIR /app/notification-service

# Manifests first, source second — keeps the dependency download in a cached
# layer that ordinary code edits do not invalidate.
COPY notification-service/go.mod notification-service/go.sum ./
RUN go mod download

COPY notification-service/ .
RUN go build -o notification-service .

# ---- Stage 2: run ----
#
# Ship only the compiled binary: ~10MB instead of the ~600MB toolchain image.
FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/notification-service/notification-service .

# gRPC only — this service has no HTTP port because nothing outside the cluster
# calls it. docker-compose.yml uses `expose` rather than `ports` for 9090, so it
# stays reachable between containers and unreachable from your machine.
EXPOSE 9090
CMD ["./notification-service"]
