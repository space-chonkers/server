# jam-server

Baseline Go WebSocket server for the game. It is intentionally small: one
service, one WebSocket endpoint, JSON request/response envelopes, health
checks, and graceful shutdown. Docker is the only local prerequisite; Go does
not need to be installed on the host.

Event bus, federation, leaderboard, and autoscaling from the architecture
sketch are not implemented yet. The hub is in-memory and can be replaced later
without changing the transport contract.

## Layout

```text
cmd/server/main.go          process startup and shutdown
internal/server/config.go   environment configuration
internal/server/server.go   HTTP routes and WebSocket upgrade
internal/server/hub.go      connected-client registry and broadcast
internal/server/client.go   read/write pumps and request handlers
internal/server/protocol.go JSON envelope
Dockerfile                  multi-stage build (Go builder, Alpine runtime)
compose.yaml                local server container
```

## Run locally

```sh
docker compose up --build
```

- Server: <http://localhost:8080>
- WebSocket: `ws://localhost:8080/ws`
- Health: <http://localhost:8080/healthz>

Stop it with `docker compose down`. The client container is not wired in this
repo yet; connect a client to the WebSocket URL when ready.

## WebSocket protocol

Every frame is a JSON envelope. Requests carry an `id`; responses echo it so the
client can correlate them. Server-initiated events omit `id`.

```json
{ "id": "req-1", "type": "ping" }
```

```json
{ "id": "req-1", "type": "pong", "payload": { "time": "2026-11-14T18:00:00Z" } }
```

Built-in request types:

| Type        | Response  | Behavior                                      |
| ----------- | --------- | --------------------------------------------- |
| `ping`      | `pong`    | Liveness round trip.                          |
| `echo`      | `echo`    | Returns the request payload unchanged.        |
| `broadcast` | `ack`     | Sends the payload to every other client.      |

Server event types:

| Type        | When                                      |
| ----------- | ----------------------------------------- |
| `welcome`   | On connect, with `clientId` and `clients`.|
| `broadcast` | When another client sends `broadcast`.    |
| `error`     | Invalid JSON or unknown request type.     |

Minimal client wiring:

```ts
const socket = new WebSocket('ws://localhost:8080/ws')

socket.addEventListener('open', () => {
  socket.send(JSON.stringify({ id: crypto.randomUUID(), type: 'ping' }))
})

socket.addEventListener('message', (event) => {
  console.log(JSON.parse(event.data))
})
```

When the client is served behind a reverse proxy in production, use a `wss://`
URL on the same origin and set `ALLOWED_ORIGINS` to that origin.

## Configuration

All settings are environment variables and have defaults:

| Variable                   | Default | Purpose                                      |
| -------------------------- | ------- | -------------------------------------------- |
| `PORT`                     | `8080`  | HTTP listen port.                            |
| `WS_PATH`                  | `/ws`   | WebSocket route.                             |
| `ALLOWED_ORIGINS`          | `*`     | Comma-separated origins; `*` allows all.     |
| `MAX_MESSAGE_BYTES`        | `32768` | Maximum incoming WebSocket frame size.       |
| `SHUTDOWN_TIMEOUT_SECONDS` | `10`    | Graceful shutdown deadline.                  |

`ALLOWED_ORIGINS=*` is convenient locally. Set a real origin in production.

## Production container

The Dockerfile has a production stage with a non-root user and a container
health check. Build and run the same image through Compose:

```sh
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose build
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose up -d
```

The image is tagged `space-chonkers/server:${VERSION:-dev}`. Override
`SERVER_PORT` when the host port differs from `8080`.

## Tests

Tests run inside the Go builder image, so the host does not need Go:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.25-alpine go test ./...
```
