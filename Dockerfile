# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/space-chonkers/server/internal/server.Version=${VERSION}" \
    -o /out/server ./cmd/server

FROM alpine:3.22 AS production

RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app

WORKDIR /app
COPY --from=build /out/server /app/server

USER app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://127.0.0.1:${PORT:-8080}/healthz || exit 1

ENTRYPOINT ["/app/server"]
