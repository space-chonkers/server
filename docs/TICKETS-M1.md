# M1 tickets — grid survival loop

**Milestone:** M1, day 1 · **Window:** ~10–11 h with two people (~22 person-hours)
**Spec:** [PROTOCOL.md](./PROTOCOL.md) · **Schema:** [protocol/v1.schema.json](./protocol/v1.schema.json) · **Integration:** [INTEGRATION.md](./INTEGRATION.md)

## Exit criteria

- Two bots spawn in a 3×3 base, move inside it, collect four resources, build
  adjacent base tiles, and trigger/fix a phenomenon.
- Any resource pool at 0 ends the match in a loss; covering all goals wins.
- Base cells, resource rates/caps, and active phenomenon stay identical on all
  clients.
- `match.build` and `match.phenomenon` are reliable; frames are lossy.
- A second match is isolated; a slow client cannot block the tick.
- `go test -race ./...` green; `docker compose up --build` serves `/ws`.

**Not in M1:** countdown UI, `match.ended` results, real client integration,
leaderboard, reconnect, combat/units, removal, RabbitMQ/Redis.

## Board

| ID | Lane | Ticket | Est. | Depends | Files |
| --- | --- | --- | --- | --- | --- |
| M1-00 | A | Commit docs/schema baseline | 0.25 h | — | `docs/`, `README.md` |
| M1-01 | A | Contract freeze + schema validation | 1.5 h | M1-00 | docs, schema, `v1.ts` |
| M1-02 | B | `internal/protocol` package | 1.5 h | M1-01 | `internal/protocol/*` |
| M1-03 | B | Handshake + dispatch | 1 h | M1-02 | `internal/server/{client,server}.go` |
| M1-04 | B | Manager, membership, `Sink` | 2 h | M1-02 | `internal/match/{manager,match}.go` |
| M1-05 | B | Lobby join/leave/host/start | 1.25 h | M1-03, M1-04 | `internal/match/join.go` |
| M1-06 | C | Map/start state + initial resources | 2 h | M1-02 | `internal/game/{map,state}.go` |
| M1-07 | C | Four-resource economy | 2 h | M1-06 | `internal/game/economy.go` |
| M1-08 | C | Base-only movement | 1.5 h | M1-06 | `internal/game/movement.go`, `internal/match/input.go` |
| M1-09 | C | Build/collect actions | 2 h | M1-07, M1-08 | `internal/game/actions.go`, `internal/match/actions.go` |
| M1-10 | C | Phenomena roll/effects/fix | 2 h | M1-07, M1-09 | `internal/game/phenomena.go`, `internal/match/actions.go` |
| M1-11 | B | State, frames, build/phenomenon events, queues | 1.25 h | M1-04, M1-06, M1-09, M1-10 | `internal/match/broadcast.go`, `internal/server/client.go` |
| M1-12 | D | `cmd/bot` reference client | 1.25 h | M1-05, M1-10 | `cmd/bot/*` |
| M1-13 | D | Unit/economy/phenomena/isolation tests | 2 h | M1-05…M1-12 | `*_test.go` |
| M1-14 | B | Config, wiring, shutdown, docs | 0.75 h | M1-05, M1-11 | `cmd/server/main.go`, `internal/server/config.go`, `README.md` |

**Critical path:** M1-02 → M1-04 → M1-05 → M1-06 → M1-07 → M1-09 → M1-10 →
M1-11 → M1-13.

### Ownership

| Owner | Files |
| --- | --- |
| Server dev | `internal/protocol/**`, `internal/match/{manager,match,join,broadcast}.go`, `internal/server/**`, `cmd/server/**` |
| Game/QA dev | `internal/game/**`, `internal/match/{tick,input,actions}.go`, `cmd/bot/**`, integration tests |
| Shared, then frozen | `docs/**`, `README.md` |

`internal/match` is one package split by file; agree on `Match`, `Sink`, member,
and game-state types before editing. Do not call `Sink` methods under locks.

## Tickets

### M1-00 — Commit docs/schema baseline

- Commit `docs/` (scope, protocol, integration, tickets, schema, TS).
- Link docs from README; confirm `docs/` is not ignored.
- Acceptance: `git status` has no untracked `docs/`; links resolve.

### M1-01 — Contract freeze + schema validation

- Walk `PROTOCOL.md` and `INTEGRATION.md` with the client; resolve open defaults.
- Validate `protocol/v1.schema.json` as JSON Schema 2020-12.
- Validate every `json` example in `PROTOCOL.md`/`INTEGRATION.md` against
  `ClientRequest`/`ServerMessage`/reserved refs.
- Confirm `v1.ts` matches the schema.
- Acceptance: no TBD in normative sections; schema/examples pass; build/collect/
  fix and phenomenon frames are understood by the client owner.

### M1-02 — `internal/protocol` package

- Move `Envelope` out of `internal/server/protocol.go`.
- Types: resources/pool/stash, players, constructions, active/event phenomena,
  state/frame/build/phenomenon/ended, requests/responses, error payloads.
- `DecodeRequest`, `DecodePayload`, `ClampMove`, `SanitizeNickname`, cell helpers.
- Acceptance: stdlib only; table tests + fuzz; byte-compatible envelope.

### M1-03 — Handshake + dispatch

- Send `welcome` first (version, rates, limits, rules), then start reader.
- `player.hello` version/nickname checks; duplicate rejected.
- Text only; binary `1003`; unknown type `unsupported_type`; invalid JSON stays open.
- Preserve read limit/deadlines and WebSocket control ping/pong.
- Acceptance: welcome first even if client sends immediately; version `2` closes
  `1008`; nickname sanitized; ping before/after hello.

### M1-04 — Manager, membership, `Sink`

- `Manager`: create/join/leave/get/count/close-all; max matches; unique 4-char codes.
- `Match`: members, host, phase, slots, join order, close.
- `Sink`: `SendState`, `SendFrame`, `SendControl`, `Close` implemented by
  `server.Client`; `match` never imports `server`.
- `Hub` becomes a connection registry only.
- Acceptance: two matches isolated; capacity/slot reuse/host migration; no
  ticker/goroutine after close; `-race` clean.

### M1-05 — Lobby join/leave/host/start

- `match.join` by code, `create:true`, or quick-join; atomic find-or-create.
- Leave removes; host migrates; empty match destroyed.
- `match.start`: host, lobby, 1–4; call `game.NewMatch` (M1-06); send full state
  and begin playing. M1 may skip countdown (M2 adds it).
- Acceptance: 5th player `match_full`; join playing `match_in_progress`;
  `matchId`+`create` `invalid_payload`; state has 9 constructions + N goals.

### M1-06 — Map/start state + initial resources

- 50×50 grid; random 3×3 fully inside; stash center; 8 base tiles.
- N=10 random investigation cells outside start; no duplicates; sorted.
- Spawns at N/E/S/W edge cells; slots 0–3.
- Per-resource state: pool current = max × fraction, stash 0; max/rates from
  formulas using `teamSize` frozen at start; phenomenon none; baseCellCount 9.
- Injectable RNG; stable ordering.
- Acceptance: exact 9 constructions; goals/exclusions; seed determinism; rates
  match formulas.

### M1-07 — Four-resource economy

- Per-resource pool/stash; capsule formulas from `SCOPE.md` §7.
- Tick: stash += gps·dt capped; pool -= cps·dt clamped; loss if any pool ≤0.
- Effective gps/cps include phenomena (M1-10).
- Starting values; stash overflow lost.
- Acceptance: rates at 1/2/4 players and 9/50 base cells; `teamSize` frozen at
  start and unchanged on leave; cap/overflow; loss on any resource; deterministic
  with fixed dt; `cps > gps` config validation.

### M1-08 — Base-only movement

- Clamp `move` to 1; scale by `playerSpeed`; one input per tick; `lastInputSeq`.
- Axis-separated clamp/slide against base cells; players overlap; no
  player/construction collision.
- Inputs only in playing; ack in frames.
- Acceptance: speed, diagonal clamp, half-speed analog, cannot leave base, slides
  on edges, can enter newly built tile, duplicate/out-of-order `seq` dropped.

### M1-09 — Build/collect actions

- Build: playing, on base, target orthogonal to player's cell, in bounds, empty,
  enough water/O2/energy. Deduct once; add tile; update base count, stash caps,
  water/O2/energy GPS/CPS; check win, then loss.
- Collect: only on stash cell; all four; `moved = min(stash, pool.max-current)`;
  overflow lost; response `{collected,lost,resources}`.
- Builder gets response; others get `match.build`.
- Acceptance: every error code; no partial build; exact cost/deltas; collect
  clamp/overflow; two builds same cell → one success, one `cell_occupied`.

### M1-10 — Phenomena roll/effects/fix

- Enum 0–4; one active; roll every `phenomenonRollSeconds` only when type 0;
  uniform weights; every roll (including 0) and resolve/instant resets timer.
- Solar: water/O2/energy GPS ×0.9. Water leak: water CPS ×1.5. Oxygen loss:
  oxygen CPS ×1.5. Rotten food: food pool −`5×P` once.
- Fix: stash cell, pay energy, clear immediately; before economy; errors
  `insufficient_energy`, `no_phenomenon`, `not_at_stash`.
- Reliable `match.phenomenon` started/resolved/instant; active state in
  state/frame; food instant does not occupy active slot.
- Acceptance: no overlap; effects last until fixed; fix restores rates; fix
  before tick avoids effect; food can cause loss; event state/effects exact.

### M1-11 — State, frames, events, queues

- `match.state`: full lobby/start state (resources, phenomenon, 9 constructions,
  goals). Control queue.
- `match.frame`: 15 Hz players/resources/phenomenon/baseCellCount/teamSize/host; lossy.
- `match.build`/`match.phenomenon`: reliable; builder/fixer gets response, others
  event.
- Two queues: frames cap 4 (drop oldest), control cap 32 (close `1011`).
- No periodic full state; no removal.
- Acceptance: ~15 frames/s; no constructions in frames; start/state exact; build/
  phenomenon events not duplicated/lost; slow reader does not block; isolation.

### M1-12 — `cmd/bot`

- Flags: url, players, match, hz, duration, seed, verbose.
- Connect/hello/join/start; stay in base; collect at stash; build toward nearest
  goal; fix active events when at stash and energy allows; print resources/base/
  goals/phenomenon.
- Acceptance: 4 bots grow base and cover at least one goal; collect/fix exercised;
  no illegal builds; second match isolated; clean exit.

### M1-13 — Tests

- Unit: protocol/fuzz, map, economy, movement, actions, phenomena, manager.
- Integration: two matches × two clients; start → state → collect → build →
  phenomenon → frame/event; win (small N) and loss (drain a resource); join/leave/
  5th/join-in-progress; slow consumer.
- Schema/examples validation (M1-01) can be a CI script.
- Acceptance: `-count=3` pass; `-race` and `vet` clean; isolation test fails if
  cross-wired.

### M1-14 — Config, wiring, shutdown, docs

- Config: grid, N, per-resource pool/stash/GPS/CPS, build costs, starting pool
  fraction, event roll/weights, multipliers, rotten food, fix costs, max
  players/matches/clients, rates.
- Validate `cps > gps` for supported player counts and representative base sizes;
  fail fast on bad config.
- Shutdown: stop accepting → close matches/tickers → clients `1001` → HTTP.
- README: layout, config, links, bot usage, endpoints.
- Acceptance: defaults start; invalid config fails; Compose serves; `SIGTERM`
  clean; no new deps.

## Cut line

If behind at hour 7: cut leaderboard/debug/rate-limit; cut bot goal-seeking
(keep random build/collect/fix); reduce N to 8; skip countdown (M2). Never cut:
base-only clamp, resource loss, build validation, phenomenon state/events,
reliable control events, isolation test, bot.