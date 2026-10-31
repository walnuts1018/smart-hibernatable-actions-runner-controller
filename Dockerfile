# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

FROM --platform=$BUILDPLATFORM docker.io/golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385 AS go-deps

ENV GOTOOLCHAIN=local

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

FROM go-deps AS builder

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}


FROM builder AS build-binaries

RUN --mount=type=bind,source=.,target=/src \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/manager \
    ./cmd/main.go && \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/listener \
    ./cmd/listener/main.go && \
    go build \
    -buildvcs=false \
    -trimpath \
    -mod=readonly \
    -tags "netgo,osusergo" \
    -ldflags="-s -w" \
    -o /out/runner-hook \
    ./cmd/runner-hook/main.go

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS static-runtime

WORKDIR /

USER 65532:65532

FROM static-runtime AS manager

COPY --link \
    --from=build-binaries \
    --chmod=0555 \
    /out/manager \
    /manager

ENTRYPOINT ["/manager"]

FROM static-runtime AS listener

COPY --link \
    --from=build-binaries \
    --chmod=0555 \
    /out/listener \
    /listener

ENTRYPOINT ["/listener"]

FROM static-runtime AS runner-hook

COPY --link \
    --from=build-binaries \
    --chmod=0555 \
    /out/runner-hook \
    /runner-hook

ENTRYPOINT ["/runner-hook"]

