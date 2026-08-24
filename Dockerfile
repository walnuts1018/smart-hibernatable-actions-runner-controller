# syntax=docker/dockerfile:1

# =============================================================================
# Go dependencies
# =============================================================================
FROM docker.io/golang:1.27.0-trixie AS go-deps

ENV GOTOOLCHAIN=local

WORKDIR /src

RUN rm -f /etc/apt/apt.conf.d/docker-clean \
    && printf '%s\n' \
    'Binary::apt::APT::Keep-Downloaded-Packages "true";' \
    > /etc/apt/apt.conf.d/keep-cache \
    && printf '%s\n' \
    'Acquire::Languages "none";' \
    > /etc/apt/apt.conf.d/99no-translations

RUN --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=cache,id=gha-baremetal-go-mod,target=/go/pkg/mod,sharing=shared \
    go mod download -x

# =============================================================================
# Static Go builder
# =============================================================================
FROM go-deps AS static-go-builder

RUN --mount=type=bind,source=.,target=/src \
    --mount=type=cache,id=gha-baremetal-go-mod,target=/go/pkg/mod,sharing=shared \
    --mount=type=cache,id=gha-baremetal-go-build,target=/root/.cache/go-build,sharing=shared \
    <<'EOF'
set -eu

mkdir -p /out

tags="netgo,osusergo"

CGO_ENABLED=0 \
    go build \
        -buildvcs=false \
        -trimpath \
        -mod=readonly \
        -tags "${tags}" \
        -ldflags="-s -w" \
        -o /out/manager \
        ./cmd/main.go

CGO_ENABLED=0 \
    go build \
        -buildvcs=false \
        -trimpath \
        -mod=readonly \
        -tags "${tags}" \
        -ldflags="-s -w" \
        -o /out/listener \
        ./cmd/listener/main.go

CGO_ENABLED=0 \
    go build \
        -buildvcs=false \
        -trimpath \
        -mod=readonly \
        -tags "${tags}" \
        -ldflags="-s -w" \
        -o /out/runner-hook \
        ./cmd/runner-hook/main.go
EOF

# =============================================================================
# Static runtime
# =============================================================================
FROM gcr.io/distroless/static-debian13:nonroot AS static-runtime

WORKDIR /

USER 65532:65532

# =============================================================================
# Manager image
# =============================================================================
FROM static-runtime AS manager

COPY --link \
    --from=static-go-builder \
    --chmod=0555 \
    /out/manager \
    /manager

ENTRYPOINT ["/manager"]

# =============================================================================
# Listener image
# =============================================================================
FROM static-runtime AS listener

COPY --link \
    --from=static-go-builder \
    --chmod=0555 \
    /out/listener \
    /listener

ENTRYPOINT ["/listener"]

# =============================================================================
# Runner Hook image
# =============================================================================
FROM static-runtime AS runner-hook

COPY --link \
    --from=static-go-builder \
    --chmod=0555 \
    /out/runner-hook \
    /runner-hook

ENTRYPOINT ["/runner-hook"]

