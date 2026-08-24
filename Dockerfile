# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM docker.io/golang:1.27.0-trixie AS go-deps

ENV GOTOOLCHAIN=local

WORKDIR /src

RUN --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    go mod download

FROM go-deps AS builder

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}


FROM builder AS build-manager

RUN --mount=type=bind,source=.,target=/src \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=go-build-${TARGETARCH},target=/root/.cache/go-build \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/manager \
    ./cmd/main.go

FROM builder AS build-listener

RUN --mount=type=bind,source=.,target=/src \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=go-build-${TARGETARCH},target=/root/.cache/go-build \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/listener \
    ./cmd/listener/main.go

FROM builder AS build-runner-hook

RUN --mount=type=bind,source=.,target=/src \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=go-build-${TARGETARCH},target=/root/.cache/go-build \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/runner-hook \
    ./cmd/runner-hook/main.go

FROM gcr.io/distroless/static-debian13:nonroot AS static-runtime

WORKDIR /

USER 65532:65532

FROM static-runtime AS manager

COPY --link \
    --from=build-manager \
    --chmod=0555 \
    /out/manager \
    /manager

ENTRYPOINT ["/manager"]

FROM static-runtime AS listener

COPY --link \
    --from=build-listener \
    --chmod=0555 \
    /out/listener \
    /listener

ENTRYPOINT ["/listener"]

FROM static-runtime AS runner-hook

COPY --link \
    --from=build-runner-hook \
    --chmod=0555 \
    /out/runner-hook \
    /runner-hook

ENTRYPOINT ["/runner-hook"]

