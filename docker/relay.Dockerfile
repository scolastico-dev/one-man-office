# syntax=docker/dockerfile:1.7

FROM golang:1.26.2-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache make
COPY go.mod go.sum Makefile ./
COPY cmd/omo-relay ./cmd/omo-relay
COPY internal/relayserver ./internal/relayserver
COPY internal/relayproto ./internal/relayproto
RUN GOOS="$TARGETOS" GOARCH="$TARGETARCH" make relay VERSION="$VERSION"

FROM scratch
COPY --from=builder /src/bin/omo-relay /omo-relay
COPY LICENSE NOTICE /usr/share/doc/omo-relay/
COPY --from=builder /src/bin/RELAY_THIRD_PARTY_NOTICES /usr/share/doc/omo-relay/THIRD_PARTY_NOTICES
USER 65532:65532
EXPOSE 8091
ENTRYPOINT ["/omo-relay"]
