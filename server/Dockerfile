# syntax=docker/dockerfile:1.7

FROM golang:1.26.5-alpine AS builder

WORKDIR /src

ARG VERSION=0.1.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -p 1 \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/seaworld008/chronodesk/server/internal/version.Version=${VERSION} \
      -X github.com/seaworld008/chronodesk/server/internal/version.Commit=${COMMIT} \
      -X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/chronodesk \
    ./cmd/chronodesk
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -p 1 \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/seaworld008/chronodesk/server/internal/version.Version=${VERSION} \
      -X github.com/seaworld008/chronodesk/server/internal/version.Commit=${COMMIT} \
      -X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/chronodesk-migrate \
    ./cmd/migrate
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -p 1 \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/seaworld008/chronodesk/server/internal/version.Version=${VERSION} \
      -X github.com/seaworld008/chronodesk/server/internal/version.Commit=${COMMIT} \
      -X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/chronodesk-credential-maintain \
    ./cmd/credential-maintain

FROM alpine:3.24.1

ARG VERSION=0.1.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="ChronoDesk API" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/seaworld008/chronodesk"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S chronodesk \
    && adduser -S -G chronodesk chronodesk \
    && mkdir -p /app/data \
    && chown -R chronodesk:chronodesk /app

WORKDIR /app
COPY --from=builder --chown=chronodesk:chronodesk /out/chronodesk /usr/local/bin/chronodesk
COPY --from=builder --chown=chronodesk:chronodesk /out/chronodesk-migrate /usr/local/bin/chronodesk-migrate
COPY --from=builder --chown=chronodesk:chronodesk /out/chronodesk-credential-maintain /usr/local/bin/chronodesk-credential-maintain

USER chronodesk
EXPOSE 8081

CMD ["chronodesk"]
