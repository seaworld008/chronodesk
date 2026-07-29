FROM golang:1.26.5-alpine AS builder

WORKDIR /src

ARG VERSION=0.1.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -p 1 \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/seaworld008/chronodesk/server/internal/version.Version=${VERSION} \
      -X github.com/seaworld008/chronodesk/server/internal/version.Commit=${COMMIT} \
      -X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/chronodesk \
    ./cmd/chronodesk
RUN CGO_ENABLED=0 GOOS=linux go build \
    -p 1 \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/seaworld008/chronodesk/server/internal/version.Version=${VERSION} \
      -X github.com/seaworld008/chronodesk/server/internal/version.Commit=${COMMIT} \
      -X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/chronodesk-migrate \
    ./cmd/migrate

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S chronodesk \
    && adduser -S -G chronodesk chronodesk \
    && mkdir -p /app/data \
    && chown -R chronodesk:chronodesk /app

WORKDIR /app
COPY --from=builder --chown=chronodesk:chronodesk /out/chronodesk /usr/local/bin/chronodesk
COPY --from=builder --chown=chronodesk:chronodesk /out/chronodesk-migrate /usr/local/bin/chronodesk-migrate

USER chronodesk
EXPOSE 8081

CMD ["chronodesk"]
