# Scope — cooperative grid survival MVP

**Status:** proposed for M0 sign-off
**Related:** [PROTOCOL.md](./PROTOCOL.md) · [INTEGRATION.md](./INTEGRATION.md) · [TICKETS-M1.md](./TICKETS-M1.md) · [protocol/v1.schema.json](./protocol/v1.schema.json) · [protocol/v1.ts](./protocol/v1.ts)

## 1. Goal

Ship a 1–4 player cooperative game on a 50×50 grid. Players start in a random
3×3 base, collect four resources from a shared stash, spend them to expand the
base, and cover all N investigation cells before **any** resource pool reaches
zero.

The server owns the grid, base footprint, resources, phenomena, build/collect
validation, and win/loss. The client owns rendering, movement interpolation,
build/collect/fix UI, and art. There is no combat and no units. Resources, base
cells, and the outcome are shared; there is no individual score.

### Game loop at a glance

- **Move** continuously inside the base; players overlap, while closed corridor
  walls and corner posts block movement.
- **Collect** all four resources at the stash; overflow beyond pool capacity is
  lost.
- **Build** a base tile on the single empty cell directly in front of the
  character; costs water/O2/energy and raises water/O2/energy income and upkeep.
- **Survive** server phenomena: solar storm (GPS down), water leak/oxygen loss
  (CPS up), or rotten food (one-time food loss). Fix the persistent ones at the
  stash with energy.
- **Win** by covering all N investigation cells with the base; **lose** when any
  resource pool hits zero.

## 2. Rules

### Grid, base, movement

- Grid: 50×50 cells, `cellX/cellY` in `0..49`. Positions are continuous in grid
  units; cell `(x,y)` spans `[x,x+1) × [y,y+1)`.
- Base = union of cells occupied by `base_tile` or the unique `stash`.
- Start: random 3×3 base fully inside the grid; stash at its center; the other
  8 cells are `base_tile`. `baseCellCount = 9`.
- Constructions are 1×1, no rotation, no removal. The stash is pre-placed and
  cannot be built or removed.
- Players spawn at the four cardinal edge cells (N/E/S/W by slot), facing
  radially outward, may overlap, and move continuously **only inside the base**.
  Closed corridor walls and corner posts block movement; shared sides between
  adjacent base cells remain open.
- Each player has a cardinal `facing` (`north/east/south/west`). It follows the
  dominant axis of the last nonzero movement input; exact ties keep the current
  facing; zero input leaves it unchanged. Facing updates even if movement is
  clamped by the base edge, so pushing toward a target turns the character.
- Build: player stands on a base cell; target must be the **single cell directly
  in front** (`current cell + facing`), in bounds, empty, and affordable.
  Instant; no refund. No other adjacent cell is valid.
- No combat, projectiles, NPCs, or worker units.

### Resources

Four resources: `water`, `oxygen`, `energy`, `food`. Each has a **pool** and a
**stash**:

| Container | Fields | Scaling |
| --- | --- | --- |
| Pool | `current`, `max` | `max = POOL_PER_PLAYER × P` |
| Stash | `current`, `max`, `gps` | `max = STASH_BASE + STASH_PER_TILE × B + STASH_PER_PLAYER × P` |

`B` is `baseCellCount` including the stash. `P` is the **starting team size**,
frozen at `match.start`; a player leaving does not change caps or upkeep. At
start, `pool.current = pool.max × STARTING_POOL_FRACTION` and `stash.current = 0`.

### Economy

For each resource, on each server simulation step (`dt` is measured from the
previous step; the server loop runs at 60 Hz by default):

```text
stash.current = min(stash.current + gps * dt, stash.max)   // overflow lost
pool.current  = max(pool.current - cps * dt, 0)
if any pool.current <= 0 and no win -> loss
```

Base rates before phenomena:

| Resource | GPS | CPS |
| --- | --- | --- |
| water | `GPS_PER_PLAYER.water × P + GPS_PER_TILE.water × B` | `CPS_PER_PLAYER.water × P + CPS_PER_TILE.water × B` |
| oxygen | `GPS_PER_PLAYER.oxygen × P + GPS_PER_TILE.oxygen × B` | `CPS_PER_PLAYER.oxygen × P + CPS_PER_TILE.oxygen × B` |
| energy | `GPS_PER_PLAYER.energy × P + GPS_PER_TILE.energy × B` | `CPS_PER_PLAYER.energy × P + CPS_PER_TILE.energy × B` |
| food | `FOOD_GPS_PER_PLAYER × P` | `FOOD_CPS_PER_PLAYER × P` |

The state exposes **effective** `gps`/`cps` after active phenomena. Tick order:
movement/actions → win/loss check → phenomenon roll/instant → economy → loss
check. Config requires `cps >= gps` for every supported team size and base size.
Current defaults make both rates equal, provided players collect before the stash fills.

### Build and collect

- Base tile cost: `BUILD_COST.water`, `BUILD_COST.oxygen`,
  `BUILD_COST.energy` (no food), paid once from the pool. If any is short, the
  build is rejected.
- A build adds one base cell, then:
  - increases water/oxygen/energy GPS and CPS by their per-tile constants;
  - increases all stash caps through `B`;
  - does **not** change pool caps or food GPS/CPS.
- Collect requires standing on the stash cell. It moves all four resources
  together: `moved = min(stash.current, pool.max - pool.current)`; overflow is
  lost. The response reports `collected` and `lost` per resource.

### Phenomena

At most one phenomenon is active. The server rolls only while playing and only
when `type == 0`; the first roll happens `PHENOMENON_FIRST_ROLL_SECONDS` after
play starts, then every `PHENOMENON_ROLL_SECONDS`. Each roll is a
solar storm with `p_solar = 1 − (1 − SOLAR_STORM_CHANCE_PER_DAY)^(PHENOMENON_ROLL_SECONDS / SECONDS_PER_DAY)`
(0.169 by default), then strong with `STRONG_SHARE`, otherwise mild; the
remaining `1 − p_solar` is split by config weights over 0, 2, 3, and 4 (default
uniform, ≈ 0.208 each). Every roll—including a 0 roll—and every event
resolution resets the timer.

- 1 day = 60 s = 1,800 ticks.

| Type | Name | Effect | Fix | Duration |
| --- | --- | --- | --- | --- |
| 0 | none | No effect | — | — |
| 1 | solar storm | Water/oxygen/energy GPS −`SOLAR_GPS_REDUCTION_PER_SECOND` (strong 15 percentage points/s, 0 after ≈ 6.7 s; mild 5 points/s, 0 after 20 s) | Energy | Until fixed |
| 2 | water leak | Water CPS × `WATER_LEAK_CPS_MULTIPLIER` (1.5) | Energy | Until fixed |
| 3 | oxygen loss | Oxygen CPS × `OXYGEN_LOSS_CPS_MULTIPLIER` (1.5) | Energy | Until fixed |
| 4 | rotten food | Food pool − `ROTTEN_FOOD_PER_PLAYER × P`, once | — | One tick |

- Solar storm affects stash generation only; leak/loss affect the pool only.
  Solar reduction is absolute (percentage points of base GPS per second), not a
  compounding multiplier; it reaches 0 after `1 / SOLAR_GPS_REDUCTION_PER_SECOND`
  seconds and stays there until fixed.
- Rotten food subtracts from the pool once, then the active type returns to 0.
- Fix: stand on the stash cell, pay `fixCostEnergy`, clear immediately. Fixing
  before the economy tick prevents that tick's effect. Insufficient energy is an
  error; spending down to 0 can cause a loss.
- Active phenomena are in every state/frame; start/resolve/instant transitions
  are also sent as reliable `match.phenomenon` events.
- Events never overlap and never run during lobby/countdown.

### Win, loss, lifecycle

- **Win:** base covers every investigation cell. A build that completes the
  last goal wins even if its cost leaves a pool at 0.
- **Loss:** any pool `current <= 0` after actions/economy. Rotten food, leaks,
  upkeep, build costs, and fix costs can all cause it.
- **Abandon:** no players remain.
- 1–4 players; host starts; lobby-only join; leave continues; host migrates.
- No timer; no reconnect; no persistence; restart loses the match.

## 3. In and out of scope

**P0:** handshake, lobby/start, random map + stash + goals, base-only movement,
four-resource economy, build/collect/fix, phenomena, win/loss, full state +
build/phenomenon events + frames, bot, tests, Compose.

**P1:** leaderboard (fastest clear), debug endpoint, rate limiting, resync
request.

**P2 / post-jam:** combat/units, removal/undo, multi-cell/rotation, more
construction types, matchmaking/party, RabbitMQ, Redis, federation, Nomad, auth,
spectators, replay, persistence, horizontal scaling.

## 4. Architecture

```text
client ── /ws JSON ──> internal/server ──> internal/match ──> internal/game
                         transport         lifecycle/actions    grid/economy
                         queues            fan-out              phenomena/rules
```

| Package | Owns |
| --- | --- |
| `internal/protocol` | Wire types, decode/validate, error codes |
| `internal/game` | Grid, map, base, resources, phenomena, movement, win/loss |
| `internal/match` | Manager, membership, phases, tick, action dispatch, fan-out |
| `internal/server` | HTTP/WS, handshake, per-client priority queues, shutdown |
| `cmd/bot` | Reference client / smoke test |
| `internal/bus`, `internal/leaderboard` | P1 seams only |

## 5. Milestones

| Milestone | Deliverable | Exit |
| --- | --- | --- |
| **M0** | Contract + schema sign-off | Client can implement connect/lobby/move/build/collect/fix |
| **M1** | Grid, economy, phenomena, actions, frames, bot | Two bots play a full loop; `go test -race` green |
| **M2** | Countdown, win/loss, client integration, leaderboard P1 | Full win and loss from the real client |
| **M3** | Compose deploy, demo, handoff | Deployed demo; code frozen |

M1 is ~22 person-hours; see [TICKETS-M1.md](./TICKETS-M1.md). If solo, cut P1,
bot goal-seeking, and reduce N.

## 6. Risks

| Risk | Mitigation |
| --- | --- |
| Economy is unwinnable/unfun | All constants config; bot-driven tuning; N=10; `cps >= gps` validation |
| Phenomena feel unfair | One at a time; fixed costs visible; roll timer resets; tune weights |
| Corridor wall traversal | Server-authoritative wall and corner-post clearance + unit tests |
| State desync on build/phenomenon | Reliable control events + full state at start; no removal |
| Client build UI targets wrong cell | Server validates; UI only offers the single front cell |
| Scope creep (combat/units/removal) | Hard P2 cut line |

## 7. Placeholder constants

All are server config; the client reads current values from state/rules. These
are **not balanced** for the jam yet.

| Constant | Water | Oxygen | Energy | Food |
| --- | --- | --- | --- | --- |
| `POOL_PER_PLAYER` | 100 | 100 | 100 | 80 |
| `STASH_BASE` | 10 | 10 | 10 | 10 |
| `STASH_PER_TILE` | 1 | 1 | 1 | 1 |
| `STASH_PER_PLAYER` | 5 | 5 | 5 | 5 |
| `GPS_PER_TILE` | 0.08 | 0.08 | 0.06 | — |
| `GPS_PER_PLAYER` | 0.25 | 0.25 | 0.20 | 0.25 |
| `CPS_PER_PLAYER` | 0.25 | 0.25 | 0.20 | 0.25 |
| `CPS_PER_TILE` | 0.08 | 0.08 | 0.06 | — |
| `BUILD_COST` | 5 | 5 | 5 | 0 |

Other defaults: `STARTING_POOL_FRACTION=0.8`,
`SOLAR_GPS_REDUCTION_PER_SECOND={strong:0.15, mild:0.05}`,
`WATER_LEAK_CPS_MULTIPLIER=1.5`, `OXYGEN_LOSS_CPS_MULTIPLIER=1.5`,
`ROTTEN_FOOD_PER_PLAYER=5`,
`FIX_COST_ENERGY={solar:{strong:30, mild:15}, water:10, oxygen:10}`,
`PHENOMENON_FIRST_ROLL_SECONDS=120`, `PHENOMENON_ROLL_SECONDS=30`,
uniform event weights, `INVESTIGATION_COUNT=10`, `PLAYER_SPEED=4`,
`TICK_RATE=60`, `FRAME_RATE=60`.

Phenomenon roll defaults:

| Constant | Default | Notes |
| --- | --- | --- |
| `SECONDS_PER_DAY` | 60 | 1 Mars day (sol) = 60 s = 1,800 ticks; one roll = 0.5 day |
| `SOLAR_STORM_CHANCE_PER_DAY` | 0.31 | NASA DONKI WSA-Enlil, Mars impacts, 2024-01-01 → 2026-11-15: 323 of 1,050 days |
| `STRONG_SHARE` | 0.38 | Direct hits among Mars storms, last 12 months (63 of 167) |
| `SOLAR_STRONG` | reduction 0.15/s (15 percentage points/s), fix 30 energy | |
| `SOLAR_MILD` | reduction 0.05/s (5 percentage points/s), fix 15 energy | |
| `PHENOMENON_WEIGHTS` | none 1, water leak 1, oxygen loss 1, rotten food 1 | Split the non-solar share |

## 8. Definition of done

- [ ] 1–4 players spawn in the 3×3 base, move only inside it, and overlap.
- [ ] Collect moves all four resources and reports overflow lost.
- [ ] Build only succeeds on the single front cell, costs water/O2/energy, adds
      one base cell, and updates GPS/CPS/caps.
- [ ] Any pool at 0 ends the match in a loss.
- [ ] All investigation cells covered ends the match in a win.
- [ ] Solar/water/oxygen phenomena persist until fixed; rotten food is instant.
- [ ] Fixing requires the stash cell and energy, and clears before the tick.
- [ ] Full state at start + reliable build/phenomenon events reconstruct the map.
- [ ] A slow client cannot block the tick.
- [ ] `go test -race ./...` green; bot runs 60 s; Compose serves `/ws`.

**Demo:** start; collect; build toward goals; show one phenomenon and fix it; show
pool drain and a loss; restart and win.
