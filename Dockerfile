# syntax=docker/dockerfile:1.7

FROM golang:1.26.2-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/omo ./cmd/omo

FROM alpine:3.23

ARG NVM_VERSION=v0.40.7
ARG PNPM_VERSION=12.3.4

RUN apk add --no-cache \
        bash \
        build-base \
        ca-certificates \
        coreutils \
        curl \
        docker-cli \
        docker-cli-buildx \
        docker-cli-compose \
        findutils \
        git \
        jq \
        less \
        nodejs \
        npm \
        openssh-client \
        procps \
        py3-pip \
        python3 \
        ripgrep \
        rsync \
        su-exec \
        tar \
        unzip \
        wget \
        zip \
    && npm install --global "pnpm@$PNPM_VERSION" \
    && mkdir -p /usr/local/share/nvm \
    && curl --proto '=https' --tlsv1.2 -fsSL \
        "https://github.com/nvm-sh/nvm/archive/refs/tags/$NVM_VERSION.tar.gz" \
        | tar -xz --strip-components=1 -C /usr/local/share/nvm \
    && npm cache clean --force

COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/omo /usr/local/bin/omo
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint

ENV PATH="/usr/local/go/bin:/home/omo/.local/bin:/home/omo/.local/share/pnpm:$PATH"

WORKDIR /workspace
EXPOSE 8090

USER root
ENTRYPOINT ["/usr/local/bin/docker-entrypoint"]
