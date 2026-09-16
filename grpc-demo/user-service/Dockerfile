# Dockerfile for user-service.
#
# IMPORTANT: the build context is the repo root (grpc-demo/), not this folder,
# because go.mod says `replace github.com/demo/grpc-demo/proto => ../proto` and
# that shared module must be inside the context. Every COPY path below is
# therefore relative to grpc-demo/. docker-compose.yml sets this up with:
#
#   build:
#     context: .
#     dockerfile: user-service/Dockerfile
#
# Building from inside user-service/ fails with "cannot find module ../proto".

# ---- Stage 1: build ----
#
# NOTE: go.mod declares `go 1.25.0` but this base image is 1.22. Go 1.21+ will
# try to auto-download the newer toolchain mid-build — slow, and a hard failure
# in a network-restricted builder. Fix by bumping this line to golang:1.25-alpine.
# Keep the base image at or above the `go` directive in go.mod.
FROM golang:1.22-alpine AS builder
WORKDIR /app

# The shared contract module, resolved by the `replace` directive above.
COPY proto/ ./proto/

WORKDIR /app/user-service

# Copy the dependency manifests BEFORE the source. Docker caches layers and
# invalidates everything after the first change; dependencies change rarely
# while source changes constantly, so this keeps `go mod download` in a layer
# that survives ordinary code edits. Copy everything at once instead and every
# build re-downloads the world.
COPY user-service/go.mod user-service/go.sum ./
RUN go mod download

COPY user-service/ .
RUN go build -o user-service .

# ---- Stage 2: run ----
#
# Two stages because you need a compiler to build and not to run. The Go
# toolchain image is ~600MB; this one is ~10MB. Only the binary crosses over,
# so the final image deploys faster, costs less to store, and carries far less
# attack surface — there is no shell full of tools sitting in production.
FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/user-service/user-service .

# 8080 is the HTTP port you curl; 50051 is the gRPC port other services use.
# EXPOSE is documentation — docker-compose.yml decides what is actually published.
EXPOSE 8080 50051
CMD ["./user-service"]
