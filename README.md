# jam-server

Baseline Go WebSocket server for the game. It is intentionally small: one
service, one WebSocket endpoint, JSON request/response envelopes, health
checks, and graceful shutdown. Docker is the only prerequisite to run the
server; the reference bot needs Go 1.25+ (or the Go builder image).

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
- [Reference bot](cmd/bot/README.md) — contract-faithful smoke client.
- [Architecture handoff](docs/DESCRIPTION.md) — sketch-to-code mapping.
- [Mars terrain](internal/terrain/README.md) — real HiRISE terrain at `GET /v1/mars-terrain/{seed}`.

## Layout

```text
cmd/server/main.go          process startup and shutdown
cmd/bot/                    reference client and smoke-test driver
internal/server/config.go   environment configuration and validation
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

- Server: <http://localhost:8081>
- WebSocket: `ws://localhost:8081/ws`
- Health: <http://localhost:8081/healthz>
- Readiness: <http://localhost:8081/readyz>
- Debug matches (P1, disabled by default): `/debug/matches`

Docker Compose publishes host port `8081` to the server's container port `8080`.
When running the binary directly, the default address remains `:8080`. Stop the
container with `docker compose down`. The server handles `SIGTERM`, closes
connected sockets with `1001`, and exits within `SHUTDOWN_TIMEOUT_SECONDS`.

The reference bot is a separate Go command:

```sh
go run ./cmd/bot -url ws://localhost:8081/ws -players 4 -duration 60s
```

See [cmd/bot/README.md](cmd/bot/README.md) for flags and limitations. The client
lives in a separate repository; connect it to the WebSocket URL above.

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
| `match.join`, `match.leave`, `match.start`, `match.input` | Wired through the match lifecycle and movement handlers. |
| `stash.collect`, `base.build` | Wired to the authoritative match actions. |
| `phenomenon.fix` | Pays the configured energy cost to resolve an active phenomenon. |

Unknown types return `error: "unsupported_type"` and keep the socket open.
Errors use a stable `error` code plus a human-readable `payload.message`.
Binary frames close with `1003`; a protocol version mismatch closes with `1008`.

Minimal client wiring:

```ts
const socket = new WebSocket('ws://localhost:8081/ws')

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

Server settings are environment variables and have defaults:

| Variable                   | Default | Purpose                                      |
| -------------------------- | ------- | -------------------------------------------- |
| `PORT`                     | `8080`  | HTTP listen port (`1..65535`).               |
| `WS_PATH`                  | `/ws`   | Exact WebSocket route; a leading `/` is added if omitted. |
| `ALLOWED_ORIGINS`          | `*`     | Comma-separated `http(s)://host[:port]` origins; `*` allows all. |
| `MAX_MESSAGE_BYTES`        | `32768` | Maximum incoming WebSocket frame size.       |
| `SHUTDOWN_TIMEOUT_SECONDS` | `10`    | Graceful shutdown deadline.                  |

Invalid values fail startup with an `invalid configuration` log and exit code
`1`; they never silently fall back to a default. `ALLOWED_ORIGINS=*` is
convenient locally. Set a real origin in production.

Gameplay rules are compiled defaults published in `welcome.rules`:

| Rule | Default |
| --- | --- |
| Grid | `50×50` |
| Investigation cells | `10` |
| Build cost | water `5`, oxygen `5`, energy `5`, food `0` |
| Player speed | `4` cells/s |
| Phenomenon roll | every 30 seconds |
| Tick / frame rate | `30` / `15` Hz |

Changing these requires a code change and restart; clients read the current
values from `welcome.rules`.

## Production container

The Dockerfile has a production stage with a non-root user and a container
health check. Build and run the same image through Compose:

```sh
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose build
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose up -d
```

The image is tagged `space-chonkers/server:${VERSION:-dev}`. Override
`SERVER_PORT` when the host port differs from `8080`. Compose gives the server a
15-second stop grace period, leaving room for the default 10-second shutdown
timeout.

## Tests

Tests run inside the Go builder image, so the host does not need Go:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.25-alpine go test ./...
```
