# M1 tickets — two parallel paths

**Milestone:** M1, day 1 · **Window:** ~10–11 h with two devs (~21 person-hours)
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

---

## Phase 0 — shared contract (not parallelized)

Both devs participate in C0/C1. C2 is the branch point; after it, Path A and
Path B must not wait on each other.

Both devs:

- [x] **C0 — The contract and handoff docs are committed to the repository**
  (0.25 h, no dep). Done in server commit `322d60a`; links/schema/types present.
- [ ] **C1 — The client team can build against a frozen, schema-validated contract**
  (1.5 h, after C0). Done when the schema is valid, every `json` example passes,
  no TBD remains, and the client owner signs off.
- [ ] **C2 — Both devs can code against agreed package interfaces without waiting
  on each other** (0.75 h, after C1). Branch point. Done when `match.Sink` and
  `game` API stubs compile and both paths can run `go test ./...` with fakes.

**C2 interface contract**

- Dev A owns `internal/match/contract.go`: `Sink`, member/player types, phase.
- Dev B owns `internal/game/api.go`: grid, player facing, resources, actions,
  phenomena, state snapshot types.
- Freeze these files after C2. Wire changes go back through C1; package
  internals stay independent.

---

## Path A — Dev A: transport, match lifecycle, state delivery

Depends only on C2. Use a fake `game` implementation for all server tests.

| ID | Product request | Est. | Depends | Done when | Files |
| --- | --- | --- | --- | --- | --- |
| A1 | Every client message is defined and validated in one shared contract | 1.5 h | C2 | Encode/decode + fuzz pass; stdlib only; envelope compatible | `internal/protocol/**` |
| A2 | A new player can connect, receive a welcome, identify, and send typed requests safely | 1 h | A1 | Welcome first; version mismatch `1008`; text-only; unknown type stays open; ping before/after hello | `internal/server/{client,server}.go` |
| A3 | Matches can be created and destroyed without leaking players or events between them | 2 h | A1 | Two matches isolated; capacity/slot/host rules; no goroutine leaks; `-race` clean | `internal/match/{manager,match}.go` |
| A4 | Players can create, join, leave, and start a lobby | 1.25 h | A2, A3 | Join/create/quickjoin; 5th `match_full`; start emits full state; host migration; atomic quickjoin | `internal/match/join.go` |
| A5 | Clients can reconstruct the world from a start state, reliable deltas, and lossy frames | 1.25 h | A3, A4 | Start state exact; 15 Hz frames; build/phenomenon reliable; slow reader isolated; control queue `1011` | `internal/match/broadcast.go`, `internal/server/client.go` |
| A6 | Operators can configure, run, and shut down the match server with documented defaults | 0.75 h | A4, A5 | Defaults start; invalid config fails; `SIGTERM` clean; Compose serves; no new deps | `cmd/server/**`, `internal/server/config.go`, `README.md` |
| A7 | A bot can play the full loop as a reference client and demo fallback | 1.25 h | A2, A4 | Bot connects/joins, turns by moving, sends move/collect/build/fix frames; fake-server test passes; 4-bot 60 s smoke in convergence | `cmd/bot/**` |

**Path A critical path:** A1 → A2/A3 → A4 → A5 → A6. A7 can start after A4.

**Path A independence rule:** never import `internal/game` internals; use the C2
`game` API/fake. If a game behavior is missing, stub it locally and file a note
for Path B; do not edit `internal/game/**`.

---

## Path B — Dev B: world simulation, actions, economy, phenomena

Depends only on C2. Use standalone game tests; do not edit server/transport files.

| ID | Product request | Est. | Depends | Done when | Files |
| --- | --- | --- | --- | --- | --- |
| B1 | Starting a match creates a valid 50×50 base, stash, goal cells, spawns, and resources | 2 h | C2 | 3×3 fits; 8 tiles + stash; N goals outside start; cardinal spawns; seed deterministic; rates match formulas | `internal/game/{map,state}.go` |
| B2 | The team can watch four resource pools generate, drain, cap, and end the match | 2 h | B1 | Per-resource formulas; stash overflow lost; any pool 0 loss; `teamSize` frozen; `cps > gps` validated | `internal/game/economy.go` |
| B3 | Players can move inside the base, overlap freely, and turn to face any cardinal direction | 1.5 h | B1 | Speed/clamp; diagonal/half-speed; axis slide; cannot leave base; facing from last nonzero move; blocked pushes still turn; front cell derives from facing; new tile walkable | `internal/game/movement.go`, `internal/match/input.go` |
| B4 | Players can collect the shared stash and expand the base with a paid tile directly in front | 2 h | B2, B3 | Build only front cell (`not_in_front`); costs/deltas; all error cases; collect clamp/overflow; win/loss precedence; no partial actions | `internal/game/actions.go`, `internal/match/actions.go` |
| B5 | The server can roll a phenomenon, apply its effect, and let the team fix it at the stash | 2 h | B2, B4 | One active; 0–4 enum; effects exact; fix at stash/energy; instant food; timer reset; event payload matches C2 API | `internal/game/phenomena.go`, `internal/match/actions.go` |

**Path B critical path:** B1 → B2/B3 → B4 → B5.

**Path B independence rule:** never edit `internal/server/**`,
`internal/match/{manager,match,join,broadcast}.go`, or `internal/protocol/**`.
If a wire shape is missing, file a note for Path A; the C2 `game` API is the
only shared surface.

---

## Branch rules

1. **C2 is the gate.** Do not start parallel work until the interfaces compile.
2. **No cross-path imports.** Path A uses `game` API/fakes; Path B owns
   `internal/game` and game-side `match` files.
3. **File ownership is exclusive.** Path A owns transport/protocol/manager;
   Path B owns game/tick/input/actions.
4. **Wire changes pause both paths.** Update C1 schema/types, then resume.
5. **Merge order is free.** Both paths must compile independently with fakes;
   merge A then B or B then A.

## Convergence (after both paths)

- Run `go test -race ./...` and `go vet ./...`.
- Validate schema + every `json` example.
- Run `cmd/bot` with 4 players for 60 s.
- Add integration tests: 2 matches isolated, win, loss, slow consumer.
- `docker compose up --build`; smoke `/healthz`, `/readyz`, `/ws`.
- Demo: collect → build → phenomenon/fix → win; restart → drain → loss.

## Cut line

If Path B runs long: reduce N to 8; keep random valid build/collect/fix; drop
bot goal-seeking. If Path A runs long: keep full state + frames; drop debug/rate
limit (P1). Never cut: base-only clamp, resource loss, build validation,
phenomenon state/events, reliable control events, isolation tests.