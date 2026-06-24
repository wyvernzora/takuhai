## Build stage
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

# Pre-fetch root-module dependencies for layer caching. The crawler module
# (sources/dmhy) resolves the root via its `replace … => ../..`, so it shares
# the same module cache once the full tree is copied below.
COPY go.mod go.sum ./
RUN go mod download

# Copy the whole workspace. The crawler build needs the root tree present
# (its replace points at ../..); the service build ignores sources/. One
# parametric Dockerfile serves both images.
COPY . .

# BUILD_DIR is the module root to build from (`.` for the service,
# `sources/dmhy` for the crawler). CMD_PKG is the main package within it.
ARG BUILD_DIR=.
ARG CMD_PKG=./cmd/takuhai
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN cd "${BUILD_DIR}" \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/app \
    "${CMD_PKG}"

## Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/app /usr/local/bin/app

# Binary-specific runtime config comes from env vars, NOT a shared CMD: the two
# images run different binaries with different flag sets. Both addr vars are pinned
# to :8080 (matching EXPOSE 8080) so each binary binds the published port from its
# own env var; the var the other binary ignores is harmless.
ENV TAKUHAI_ADDR=:8080
ENV TAKUHAI_DMHY_ADDR=:8080

# The shared CMD supplies the crawler's required `serve` subcommand. The service binary
# has no subcommands, so it ignores the trailing positional and configures from env.
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/app"]
CMD ["serve"]
