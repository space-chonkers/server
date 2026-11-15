# jam-server — architecture handoff

> Historical baseline handoff. Current requirements and the client contract live
> in [SCOPE.md](./SCOPE.md), [PROTOCOL.md](./PROTOCOL.md), and
> [INTEGRATION.md](./INTEGRATION.md).

Scoping document for the next session. It describes the architecture sketched in
`../archfinaljuegonasa.png` and how the current baseline in this repo maps onto
it. The baseline is a scaffold, not the finished system: transport, container
pipeline, and lifecycle exist; the game domain does not.

This file is part of the tracked `docs/` set alongside `SCOPE.md`,
`PROTOCOL.md`, `INTEGRATION.md`, `TICKETS-M1.md`, and `protocol/`. Do not add
`docs/` to `.gitignore`; the protocol contract is a deliverable, not scratch
notes.

---

## 1. What the sketch says

The sketch is a two-column "do this / don't do this" comparison, plus two
component close-ups.

### "Que no hacer" (anti-pattern)

```text
Jugador 1   Jugador 2   Jugador 3   Jugador 4
    \          |          |          /
     \         |          |         /
      +--------+----------+--------+
                       |
                    [SERVER]        <-- one process owns everything
                       ^
                       |
                  [Task update]     <-- polling/updating tasks against it
```

Every player opens a direct bidirectional link to a single server, and the
server is also responsible for task updates. Consequences the sketch is warning
about:

- The server is a single choke point and a single point of failure.
- Player count is bounded by one process's memory and CPU.
- Game state, matchmaking, and task bookkeeping all compete in one event loop.
- No way to scale out without duplicating or sharding that monolith by hand.

### "Que hacer" (target pattern)

```text
                    +-------------------+
                    | FEDERATED SERVER  |   <-- one entry point, routes by match
                    +---------+---------+
                              |
                    +---------v---------+
                    |  EventBus (Rabbit)|   <-- broker between components
                    +----+----+----+----+
                         |    |    |
                    +----v-+ +v----+ +v----+
                    |Queue | |Queue | |Queue |   <-- one stream per match/room
                    +--+---+ +--+---+ +--+---+
                       |        |        |
                    +--v--+  +--v--+  +--v--+
                    |CLIENT| |CLIENT| |CLIENT|
                    +-----+  +-----+  +-----+

      +------------------------------------------------------+
      | SERVER (si Sebastian no usa HashiCorp Nomad,          |
      |         quitenle la offer)                           |
      |                                                      |
      |  MATCHMAKING                 LEADERBOARD             |
      |  +-------------+             +-------------+         |
      |  | MatchBroker |             | SortedSet   |         |
      |  | 3/4 party   |             |  top()      |         |
      |  | 4/4 party   |             +------+------+         |
      |  | ...         |                    |                |
      |  +------+------+             FinishedMatchState       |
      |         |                           |                |
      |      CLIENT                       CLIENT             |
      +------------------------------------------------------+

      +------------------------------------------------------+
      | MATCH                                                |
      |  +------------+      +------------------+             |
      |  | MatchState |----->| PushEventBroker  |             |
      |  +------------+      +---+--+--+--+------+             |
      |                          |  |  |  |                    |
      |                       Queue Queue Queue Queue          |
      |                          |  |  |  |                    |
      |                       CLIENT CLIENT CLIENT CLIENT      |
      +------------------------------------------------------+
```

Read it as four cooperating concerns:

1. **Federation / edge.** A "federated server" is the only address clients
   need. It does not hold match state; it authenticates, places players, and
   routes them to the match that owns them.
2. **Event bus.** RabbitMQ decouples producers (match runtime, matchmaking,
   leaderboard) from consumers. Each match owns a queue; a client's socket is
   fed from that queue, not by direct calls into the game loop.
3. **Matchmaking + leaderboard.** A separate server-side service group.
   Matchmaking keeps party buckets (`3/4`, `4/4`, ...) and a `MatchBroker` that
   promotes a full bucket into a match. Leaderboard is a `SortedSet` (Redis
   sorted-set semantics) with `top()` reads and writes from finished matches.
4. **Match runtime.** `MatchState` is the authoritative game state for one
   match. `PushEventBroker` fans state deltas out to the match's queues. This is
   the piece the current repo actually implements, in miniature.

The sketch's running joke — "if Sebastian doesn't use HashiCorp Nomad, take away
the offer" — marks the orchestration decision as open. Nomad is the implied
scheduler for the "cloud" stack; nothing in the baseline depends on it yet.

---

## 2. Baseline in this repo

```text
cmd/server/main.go              process startup, signals, graceful shutdown
internal/server/config.go       env configuration
internal/server/server.go       HTTP routes, WebSocket upgrade, origin check
internal/server/hub.go          connected-client registry
internal/server/client.go       per-connection pumps, welcome/hello handshake
internal/server/server_test.go  HTTP and shared test helpers
internal/server/client_test.go  handshake, close, and error tests
internal/server/config_test.go  default welcome projection test
Dockerfile                      multi-stage build, non-root runtime, healthcheck
compose.yaml                    single server service, local and prod-shaped
```

### What works today

- `GET /ws` upgrades to WebSocket; `GET /healthz`, `/readyz`, `/` return JSON.
- JSON envelope in both directions: `{id, type, payload, error}`. Requests carry
  an `id`; responses echo it. Server events omit it.
- Contract handshake: `welcome` first, then `player.hello`; `ping` works before
  and after identification. Unknown types and invalid payloads keep the socket
  open; binary frames close `1003`; version mismatch closes `1008`.
- The typed v1 contract lives in `internal/protocol`; other contract requests are
  parsed and validated but return `internal` until match handlers land.
- One reader goroutine and one writer goroutine per connection, bounded send
  queue, drop-with-warning for slow consumers, ping/pong keepalive, read limit.
- Graceful shutdown closes sockets explicitly because hijacked connections are
  not tracked by `http.Server`.
- Origin allow-list via `ALLOWED_ORIGINS`, `*` for local.
- Docker: Go builder stage, Alpine runtime, non-root user, container
  healthcheck. Compose runs the server only; no client container is wired.

### What is deliberately absent

- No match concept: the hub is global, not per-match.
- No persistence: no Redis, no Postgres, no RabbitMQ.
- No matchmaking, no party buckets, no leaderboard.
- No auth or player identity beyond a random connection id.
- No full schema validation at runtime; `internal/protocol` validates requests,
  and clients validate server messages.
- No metrics/tracing endpoints; logs are `slog` text.

The hub is the seam. Replacing `Hub` with a per-match room registry and pointing
its fan-out at a broker consumer should not change the transport contract.

---

## 3. Component mapping

| Sketch component | Baseline today | Next step in Go |
| --- | --- | --- |
| Federated server | `internal/server` HTTP/WS edge | Add auth + "join match" handshake; keep stateless |
| EventBus (RabbitMQ) | none | `internal/bus` publisher/consumer interface, AMQP impl |
| Queue (per match) | none | Routing key per match id; one consumer group per match |
| MatchBroker | none | `internal/matchmaking`: party buckets, promotion rule |
| SortedSet / leaderboard | none | `internal/leaderboard` behind a Redis sorted-set interface |
| FinishedMatchState | none | Event emitted when a match closes; leaderboard consumer |
| MatchState | hub state only | `internal/match`: authoritative state machine |
| PushEventBroker | none (hub registry only) | Per-match fan-out publishing to the bus |
| Client | `internal/server/client.go` | Unchanged transport; add match subscription |

Suggested package layout for the next session (not created yet):

```text
internal/bus/           publisher + consumer interfaces, AMQP adapter
internal/match/         MatchState, tick loop, rules
internal/matchmaking/   party buckets, MatchBroker
internal/leaderboard/   SortedSet interface, Redis adapter
internal/session/       auth and player identity
```

---

## 4. Protocol direction

Keep the envelope; extend the type set rather than changing the frame.

```json
{ "id": "req-1", "type": "match.join", "payload": { "matchId": "m-42" } }
```

Planned request types: `match.join`, `match.leave`, `match.input`,
`matchmaking.enqueue`, `matchmaking.cancel`, `leaderboard.top`.
Planned server events: `match.state`, `match.event`, `match.ended`,
`matchmaking.matched`, `leaderboard.updated`, `error`.

Rules to keep:

- Every client request has an `id`; every direct response echoes it.
- Events never carry an `id`.
- Unknown types return `error` rather than closing the socket.
- Match-scoped events carry the match id in the payload, so one socket can only
  ever be subscribed to one match.

---

## 5. Deployment and local runs

Local and production use the same image and the same Compose file. The only
difference is configuration.

```sh
# local
docker compose up --build            # server on http://localhost:8080
docker compose down

# production-shaped
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose build
VERSION=1.0.0 ALLOWED_ORIGINS=https://game.example.com docker compose up -d
```

- Image tag: `space-chonkers/server:${VERSION:-dev}`.
- Host port override: `SERVER_PORT=8090 docker compose up --build`.
- Health: `GET /healthz`; readiness: `GET /readyz`.
- WebSocket: `ws://localhost:8080/ws` locally, `wss://<host>/ws` in production.

When the bus, Redis, and matchmaking land, they become additional Compose
services in the same file (`rabbitmq`, `redis`, `matchmaker`). The server stays
the only service exposed to the browser; everything else is internal.

The "cloud" stack in the sketch implies horizontal scaling behind a load
balancer with sticky WebSocket sessions. That is a load-balancer concern, not a
code concern, as long as match state lives in the bus/match runtime and not in
process memory. Nomad is the candidate scheduler; Compose is the local stand-in.

---

## 6. Open decisions for the next session

1. **Orchestration.** Nomad vs. plain Compose + a managed load balancer. The
   sketch assumes Nomad; the baseline does not require it.
2. **Bus technology.** RabbitMQ is named in the sketch. Confirm it before
   writing the adapter; the interface should stay transport-agnostic.
3. **State ownership.** Does the match runtime own state in memory and publish
   deltas (sketch's `PushEventBroker`), or does Redis hold state and the runtime
   only compute? The sketch leans toward runtime-owned state.
4. **Matchmaking scope.** Party buckets only (`3/4`, `4/4`), or also skill/region
   keys? The sketch shows only party size.
5. **Leaderboard semantics.** `top()` only, or per-player rank lookups too?
   `SortedSet` gives both cheaply; decide what the client needs.
6. **Auth.** Anonymous connection ids are fine for the baseline. Real identity
   changes the handshake and the federation layer.
7. **Tick model.** Fixed tick in the match runtime, or event-driven only? This
   affects whether the hub needs a timer loop.

---

## 7. Definition of done for the next increment

- `internal/match` owns a `MatchState` with an explicit tick or event loop.
- `internal/bus` has a publisher and consumer interface with one working
  adapter, plus an in-memory adapter for tests.
- A client can `match.join` a match id and receive `match.state` events only for
  that match.
- Two matches run concurrently without leaking events between them.
- `docker compose up --build` still starts one server; bus/Redis are optional
  profiles until the adapters exist.
- Tests cover envelope round-trip, per-match isolation, and slow-consumer drop.
