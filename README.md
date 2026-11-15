# jam-server

Baseline Go WebSocket server for the game. It is intentionally small: one
service, one WebSocket endpoint, JSON request/response envelopes, health
checks, and graceful shutdown. Docker is the only local prerequisite; Go does
not need to be installed on the host.

The MVP target is a **1–4 player cooperative 50×50 grid survival builder**:
players spawn in a 3×3 base, manage shared water/oxygen/energy/food pools, expand
the base to cover N investigation cells, and respond to server-controlled
phenomena before any resource pool drains.

Event bus, federation, leaderboard, and autoscaling from the architecture
sketch are not implemented yet. The hub is in-memory and can be replaced later
without changing the transport contract.

## Game loop at a glance

1–4 players share a 50×50 grid. A random 3×3 base starts with a stash at its
center; players spawn on the base edge and move only inside the base.

- **Resources:** water, oxygen, energy, food. Each has a pool (spendable,
  capacity scales with starting team size) and a stash (passively generated,
  capacity scales with base cells and team size).
- **Survival:** GPS fills the stash; CPS drains the pool. CPS > GPS by design,
  so the team always loses ground. Collect at the stash to refill the pool.
- **Expansion:** build a base tile on the single empty cell directly in front of
  the character. Costs water/O2/energy; raises water/O2/energy income and
  upkeep; expands the base. No removal.
- **Phenomena:** none, solar storm (water/O2/energy GPS −10 percentage points/s,
  0 after 10 s), water leak
  (water CPS ×1.5), oxygen loss (oxygen CPS ×1.5), rotten food (one-time food
  loss). One at a time; solar/leaks persist until fixed with energy at the
  stash; rotten food is instant.
- **Win/loss:** cover all N investigation cells (default 10) with the base to
  win; lose when any resource pool hits 0. Shared outcome; no combat or units.

## Docs

- [Co-op grid MVP scope and 2-day plan](docs/SCOPE.md) — what ships now vs. later.
- [WebSocket protocol v1](docs/PROTOCOL.md) — the client contract and message map.
- [Client integration guide](docs/INTEGRATION.md) — schemas, validation, handoff checklist.
- [JSON Schema](docs/protocol/v1.schema.json) — machine-readable message schemas.
- [M1 ticket board](docs/TICKETS-M1.md) — parallelizable implementation tasks.
- [Protocol TypeScript types](docs/protocol/v1.ts) — client-side reference types.
- [Architecture handoff](docs/DESCRIPTION.md) — sketch-to-code mapping.
- [Mars terrain](internal/terrain/README.md) — real HiRISE terrain at `GET /v1/mars-terrain/{seed}`.

## Layout

```text
cmd/server/main.go          process startup and shutdown
internal/server/config.go   environment configuration
internal/server/server.go   HTTP routes and WebSocket upgrade
internal/server/hub.go      connected-client registry
internal/server/client.go   read/write pumps, welcome/hello handshake
internal/terrain/           Mars heightmap regions and the terrain HTTP route
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
- Readiness: <http://localhost:8080/readyz>
- Debug matches (P1, disabled by default): `/debug/matches`

Stop it with `docker compose down`. The client container is not wired in this
repo yet; connect a client to the WebSocket URL when ready.

## WebSocket protocol

Every frame is a JSON text envelope from the v1 contract in
[docs/PROTOCOL.md](docs/PROTOCOL.md). Requests carry an `id`; responses echo it so
the client can correlate them. Server-initiated events omit `id`.

```json
{ "id": "req-1", "type": "ping", "payload": { "clientTimeMs": 1763222400000 } }
```

```json
{ "id": "req-1", "type": "pong", "payload": { "serverTimeMs": 1763222400012, "tick": 0 } }
```

On connect the server sends `welcome` first, with the protocol version, server
time, limits, and gameplay rules. The client then identifies once:

```json
{ "id": "req-2", "type": "player.hello", "payload": { "protocolVersion": 1, "nickname": "ana" } }
```

| Type | Status |
| --- | --- |
| `ping` | Any phase; answered with `pong`. |
| `player.hello` | Once per connection; answered with `player.hello`. |
| `match.join`, `match.leave`, `match.start`, `match.input`, `stash.collect`, `base.build`, `phenomenon.fix` | Validated by the contract; match handlers land in A3/A4. |

Unknown types return `error: "unsupported_type"` and keep the socket open.
Errors use a stable `error` code plus a human-readable `payload.message`.
Binary frames close with `1003`; a protocol version mismatch closes with `1008`.

Minimal client wiring:

```ts
const socket = new WebSocket('ws://localhost:8080/ws')

socket.addEventListener('message', (event) => {
  const message = JSON.parse(event.data)
  if (message.type === 'welcome') {
    socket.send(JSON.stringify({
      id: crypto.randomUUID(),
      type: 'player.hello',
      payload: { protocolVersion: message.payload.protocolVersion, nickname: 'ana' },
    }))
  }
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
