# WebSocket protocol — v1

**Status:** draft for M0 sign-off · **Protocol:** `1` · **Schema:** [protocol/v1.schema.json](./protocol/v1.schema.json) · **Types:** [protocol/v1.ts](./protocol/v1.ts) · **Integration:** [INTEGRATION.md](./INTEGRATION.md)

Game shape: 1–4 players cooperate on a 50×50 grid. The base is the union of
construction cells. Players move continuously inside the base, collect four
resources at the stash, and build base tiles outward. Server-controlled
phenomena modify generation or drain pools. Win by covering all investigation
cells; lose when any resource pool reaches zero.

## 1. Endpoints and message map

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/` | Service info |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness |
| `GET` | `/ws` | WebSocket upgrade; all game traffic |
| `GET` | `/debug/matches` | P1 debug; disabled by default |

### Client → server

| Type | Response | Phase | Purpose |
| --- | --- | --- | --- |
| `ping` | `pong` | any | Liveness / clock offset |
| `player.hello` | `player.hello` | connected | Version + nickname |
| `match.join` | `match.join` | identified | Join/create/quick-join lobby |
| `match.leave` | `match.leave` | in match | Leave |
| `match.start` | `match.start` | lobby, host | Generate map and start |
| `match.input` | none (ack in frame) | playing | Movement vector |
| `stash.collect` | `stash.collect` | playing, on stash | Collect all four resources |
| `base.build` | `base.build` | playing, on base | Build one base tile |
| `phenomenon.fix` | `phenomenon.fix` | playing, on stash | Pay energy to clear event |
| `leaderboard.top` | `leaderboard.top` | identified | P1 / reserved |

### Server → client

| Type | When | Purpose |
| --- | --- | --- |
| `welcome` | First frame | Player id, version, limits, rules |
| `match.state` | Join response, start, phase change | Full lobby/map state |
| `match.frame` | 15 Hz while playing | Players, resources, phenomenon, host |
| `match.build` | Another player builds | Reliable construction delta |
| `match.phenomenon` | Event starts/resolves/is instant | Reliable phenomenon transition |
| `match.ended` | Win/loss/abandon | Shared outcome + stats |
| `error` | Request/event failure | Stable error code |

## 2. Envelope and lifecycle

```json
{ "id": "req-1", "type": "base.build", "payload": { "cellX": 25, "cellY": 23 } }
```

- `id`: required on requests except `match.input`; responses echo it. Events omit it.
- `type`: message type. `payload`: type-specific. `error`: stable code.
- Unknown request type → `error: "unsupported_type"`; socket stays open.
- Unknown event/field → ignore.
- `match.input` has no direct response; `lastInputSeq` acknowledges it.

```text
connect → welcome → player.hello → match.join (lobby)
host: match.start → match.state (countdown) → match.state (playing)
loop: match.input / stash.collect / base.build / phenomenon.fix
      ← match.frame, match.build, match.phenomenon
end: match.ended
```

Close codes: `1000` normal, `1001` shutdown, `1003` binary frame, `1008` policy
(version), `1009` too large, `1011` internal/backpressure, `1013` capacity.

## 3. Grid, base, movement

- 50×50 cells; `cellX/cellY` integers `0..49`; origin top-left, `+x` right,
  `+y` down. Continuous positions are grid units: cell `(x,y)` spans
  `[x,x+1) × [y,y+1)`; center is `(x+0.5, y+0.5)`.
- Base = union of cells with `base_tile` or `stash`. Start is a random 3×3 base
  with 8 `base_tile` and a unique `stash` at the center; `baseCellCount = 9`.
- Players spawn at N/E/S/W edge cells by slot, facing radially outward, move
  continuously, stay inside the base, may overlap, and ignore other players and
  constructions for collision.
- Each player has a cardinal `facing`: `north|east|south|west`. It is derived
  from the last nonzero `move`: use the axis with the larger absolute component;
  exact ties keep the current facing; zero/omitted input leaves it unchanged.
  Facing updates even when movement is blocked by the base edge. There is no
  separate facing input. Facing controls build direction and visuals only.
- Build: stand on a base cell; the target must be the **single cell directly in
  front** of the player (`current cell + facing`), in bounds, empty, and
  affordable. Instant, no removal.
- Facing offsets: north `(0,-1)`, east `(+1,0)`, south `(0,+1)`, west `(-1,0)`.
- No combat, units, projectiles, or NPCs.

## 4. Resources and economy

Four resources: `water`, `oxygen`, `energy`, `food`. Each resource state:

```text
{
  "pool": { "current": 40.0, "max": 100.0 },
  "stash": { "current": 12.0, "max": 29.0, "gps": 0.36 },
  "cps": 1.32
}
```

- `pool.max` scales with team size; `stash.max` scales with base cells and team
  size; `gps`/`cps` are **effective** after phenomena.
- `B = baseCellCount` (includes stash), `P = teamSize` (starting team size,
  frozen at `match.start`; leaving does not change caps or upkeep).

```text
water/oxygen/energy GPS = GPS_PER_TILE[r] * B
food GPS               = FOOD_GPS_PER_PLAYER * P
water/oxygen/energy CPS = CPS_PER_PLAYER[r] * P + CPS_PER_TILE[r] * B
food CPS               = FOOD_CPS_PER_PLAYER * P

per tick: stash = min(stash + gps*dt, stash.max)   // overflow lost
          pool  = max(pool - cps*dt, 0)
```

- Tick order: movement → collect/build/fix actions → win/loss check →
  phenomenon roll/instant → recompute effective rates → economy → loss check.
  A build that completes the win ends at the first check. A fix in the action
  phase prevents that tick's effect.
- Config MUST keep `cps > gps`; that is the survival pressure.
- Build costs `buildCost.water`, `.oxygen`, `.energy` (no food), paid once.
  It adds one base cell, raises water/O2/energy GPS+CPS per tile, and raises all
  stash caps through `B`; it does not change pool caps or food rates.
- Collect at the stash cell moves all four at once:
  `moved = min(stash.current, pool.max - pool.current)`; overflow is lost.
- Any pool `<= 0` after actions/economy is a loss, unless a build won.

## 5. Phenomena

At most one active. Roll every `rules.phenomenonRollSeconds` while playing, only
when `type == 0`; default weights are uniform over 0–4. Every roll—including a
0 roll—and every event resolution resets the roll timer.

| Type | Effect | Fix | Duration |
| --- | --- | --- | --- |
| 0 none | — | — | — |
| 1 solar storm | water/O2/energy GPS × 0.9 | energy | until fixed |
| 2 water leak | water CPS × 1.5 | energy | until fixed |
| 3 oxygen loss | oxygen CPS × 1.5 | energy | until fixed |
| 4 rotten food | food pool − `5 × P`, once | — | one tick |

- Solar affects stash generation; leaks affect the pool. Food is unaffected by
  solar/leaks.
- Fix: stand on stash cell, pay `fixCostEnergy`, clear immediately. A fix before
  the economy tick prevents that tick's effect. `match.phenomenon` is reliable.
- Food loss is instant and never occupies the active slot.

Active phenomenon object (`match.state`/`match.frame`):

```text
{
  "type": 1,
  "startedAtMs": 1763222410000,
  "fixCostEnergy": 20,
  "effects": { "gpsMultiplier": 0.9, "resources": ["water", "oxygen", "energy"] }
}
```

Type 0: `{ "type":0, "startedAtMs":0, "fixCostEnergy":0, "effects":{} }`.
Type 2: `{ "effects": { "cpsMultiplier": 1.5, "resource": "water" } }`.
Type 3: same with `"oxygen"`. Type 4 only appears in `match.phenomenon`:
`{ "effects": { "foodLoss": 10 } }`.

## 6. Requests

| Type | Payload |
| --- | --- |
| `ping` | `{clientTimeMs?}` |
| `player.hello` | `{protocolVersion:1, nickname}` |
| `match.join` | `{matchId?}` or `{create:true}` or omitted |
| `match.leave` | none |
| `match.start` | none |
| `match.input` | `{seq, move?: {x,y}}` |
| `stash.collect` | none |
| `base.build` | `{cellX, cellY}` |
| `phenomenon.fix` | none |

```json
{ "type": "match.input", "payload": { "seq": 42, "move": { "x": 0, "y": -1 } } }
```

- Input is a full intent snapshot; omitted `move` = zero. To stop, send zero.
  Server clamps length to 1; magnitude scales `rules.playerSpeed`. One input per
  tick; `lastInputSeq` acknowledges. Base-only movement with axis-separated
  sliding.
- `facing` is derived from the last nonzero `move` (dominant axis; exact ties
  keep the current facing); zero/omitted input keeps facing. Facing updates even
  when movement is clamped, so push toward the target to turn before building.
  There is no separate facing input.
- `base.build` response:

```text
{
  "id": "7",
  "type": "base.build",
  "payload": {
    "construction": { "id": "c_10", "kind": "base_tile", "cellX": 25, "cellY": 23 },
    "resources": { "water": {}, "oxygen": {}, "energy": {}, "food": {} }
  }
}
```

- `base.build` target MUST equal `current cell + facing`. To build elsewhere,
  push a small `move` toward that cell first; facing updates even if movement is
  clamped. No other adjacent cell is accepted.
- `stash.collect` response: `{collected: {water,oxygen,energy,food}, lost: {...}, resources}`.
- `phenomenon.fix` response: `{phenomenon: {type:0,...}, resources}`.
- Errors: `not_on_base`, `not_in_front`, `cell_occupied`, `out_of_bounds`,
  `insufficient_resources` (payload `missing`), `not_at_stash`,
  `insufficient_energy`, `no_phenomenon`, `wrong_phase`, `match_over`.

## 7. Events

Examples marked `text` are abbreviated; the JSON Schema and §4 define every
required field.

### `welcome`

```json
{
  "type": "welcome",
  "payload": {
    "playerId": "p_1a2b3c4d",
    "protocolVersion": 1,
    "serverTimeMs": 1763222400000,
    "tickRate": 30,
    "frameRate": 15,
    "limits": { "maxMessageBytes": 32768, "maxPlayersPerMatch": 4, "maxNicknameLength": 16, "maxMatches": 64 },
    "rules": {
      "grid": { "width": 50, "height": 50 },
      "investigationCount": 10,
      "buildCost": { "water": 5, "oxygen": 5, "energy": 5, "food": 0 },
      "playerSpeed": 4,
      "phenomenonRollSeconds": 30
    }
  }
}
```

### `match.state`

Full state. Sent in the `match.join` response and on start/phase change. No
periodic full state; use frames + build/phenomenon events.

Key shape:

```text
{
  "type": "match.state",
  "payload": {
    "matchId": "AB12", "schema": 1, "tick": 0, "serverTimeMs": 1763222404000,
    "phase": "playing", "phaseEndsAtMs": 0, "hostId": "p_1a2b3c4d",
    "grid": { "width": 50, "height": 50 },
    "baseCellCount": 9,
    "teamSize": 2,
    "players": [ { "id": "p_1a2b3c4d", "slot": 0, "name": "ana", "facing": "north", "x": 25.5, "y": 24.5, "vx": 0, "vy": 0, "lastInputSeq": -1 } ],
    "constructions": [ { "id": "c_1", "kind": "base_tile", "cellX": 24, "cellY": 24 }, { "id": "c_5", "kind": "stash", "cellX": 25, "cellY": 25 } ],
    "investigation": [ { "cellX": 12, "cellY": 8 } ],
    "resources": { "water": {}, "oxygen": {}, "energy": {}, "food": {} },
    "phenomenon": { "type": 0, "startedAtMs": 0, "fixCostEnergy": 0, "effects": {} }
  }
}
```

`players` sorted by slot; `constructions` sorted by cell; `investigation` sorted.
`players[].facing` is cardinal and controls build direction. `teamSize` is
frozen at `match.start` and drives pool caps, CPS, stash caps, and food GPS; it
does not decrease when a player leaves. `phase` is
`lobby|countdown|playing`; `match.ended` is terminal. In lobby,
`constructions`/`investigation` are empty, resources zero, `baseCellCount=0`,
and `teamSize` is the current lobby player count.

### `match.frame` (lossy, 15 Hz)

```text
{
  "type": "match.frame",
  "payload": {
    "matchId": "AB12", "tick": 137, "serverTimeMs": 1763222404500,
    "hostId": "p_1a2b3c4d", "baseCellCount": 9, "teamSize": 2,
    "players": [ { "id": "p_1a2b3c4d", "slot": 0, "name": "ana", "facing": "south", "x": 25.5, "y": 24.9, "vx": 0, "vy": 4, "lastInputSeq": 137 } ],
    "resources": { "water": {}, "oxygen": {}, "energy": {}, "food": {} },
    "phenomenon": { "type": 0, "startedAtMs": 0, "fixCostEnergy": 0, "effects": {} }
  }
}
```

### `match.build` (reliable, other members)

```text
{
  "type": "match.build",
  "payload": {
    "matchId": "AB12",
    "construction": { "id": "c_10", "kind": "base_tile", "cellX": 25, "cellY": 23 },
    "resources": { "water": {}, "oxygen": {}, "energy": {}, "food": {} }
  }
}
```

Builder receives the same construction in the `base.build` response.

### `match.phenomenon` (reliable)

```json
{
  "type": "match.phenomenon",
  "payload": {
    "matchId": "AB12",
    "action": "started",
    "phenomenon": { "type": 1, "startedAtMs": 1763222410000, "fixCostEnergy": 20, "effects": { "gpsMultiplier": 0.9, "resources": ["water", "oxygen", "energy"] } }
  }
}
```

`action`: `started`, `resolved`, `instant`. `resolved` carries type 0.
`instant` carries type 4 with `effects.foodLoss`. The active phenomenon is also
in frames/state. The fixer receives `phenomenon.fix` response; other members
receive the resolved event. `started`/`instant` go to all members.

### `match.ended` (terminal)

```json
{
  "type": "match.ended",
  "payload": {
    "matchId": "AB12", "endedAtMs": 1763222580000,
    "outcome": "loss", "reason": "resource_depleted",
    "stats": {
      "elapsedMs": 178000, "baseCells": 32,
      "investigationCovered": 7, "investigationTotal": 10,
      "remaining": { "water": 0, "oxygen": 12.3, "energy": 4.1, "food": 8.0 },
      "depletedResource": "water"
    }
  }
}
```

`outcome`: `win|loss|abandoned`; `reason`:
`all_investigated|resource_depleted|abandoned`. `depletedResource` is omitted
unless the loss was a resource. Match is removed immediately; show results
client-side.

## 8. Error codes

`invalid_json`, `invalid_payload`, `unsupported_type`,
`protocol_version_mismatch` (close `1008`), `not_identified`,
`already_identified`, `already_in_match`, `not_in_match`, `match_not_found`,
`match_full`, `match_in_progress`, `not_host`, `wrong_phase`, `not_on_base`,
`not_in_front`, `cell_occupied`, `out_of_bounds`, `insufficient_resources`,
`insufficient_energy`, `not_at_stash`, `no_phenomenon`, `match_over`,
`invalid_cell`, `server_full`, `rate_limited`, `internal`.

Errors keep the socket open except version mismatch and capacity refusal.
`payload.message` is human-readable; `payload.missing` may list resources for
`insufficient_resources`.

## 9. Limits and backpressure

| Limit | Default |
| --- | --- |
| Inbound frame | 32 KiB (close `1009`) |
| Players / match | 4 |
| Matches / process | 64 |
| Clients / process | 256 |
| Nickname | 16 runes |
| Input | ≤30/s recommended |
| Build/collect/fix | ≤10/s recommended; P1 rate limit |
| State queue | 4 frames; drop oldest |
| Control queue | 32 messages; close `1011` if full |

Frames are lossy. `welcome`, responses, errors, `match.state`, `match.build`,
`match.phenomenon`, and `match.ended` are reliable. The server validates all
movement, base, resources, and events; never trust client positions.

## 10. Client notes

1. Check `welcome.protocolVersion`; store `rules`.
2. Replace full state on `match.state`; apply build/phenomenon deltas.
3. Derive current cell with `floor(x)`, `floor(y)`.
4. Build UI: compute the single cell in front from the current cell + `facing`;
   enable only if it is in bounds and empty. To build elsewhere, push a small
   `move` toward that cell first; facing updates even if movement is clamped.
   Show `rules.buildCost`; disable if any cost is short.
5. Collect UI: only on stash cell; show per-resource stash and pool caps;
   collection discards overflow.
6. Fix UI: only on stash cell; show `phenomenon.fixCostEnergy`; disable if
   energy is short.
7. Show four resource bars (pool current/max, stash current/max, GPS, CPS) and
   the active phenomenon. Use `teamSize` (frozen at start), not the current
   `players.length`, for capacity/upkeep labels.
8. Movement prediction must clamp to the local base; players may overlap.
9. No reconnect in v1; no removal; no combat/facing.
10. On `match.ended`, show the shared outcome and `stats.remaining`.

## 11. Versioning

`protocolVersion` bumps for breaking envelope/lifecycle changes; `state.schema`
bumps for breaking gameplay payload changes. Additive fields/events do not bump
either; clients ignore unknown fields. Bus/Redis/federation must not change the
socket bytes. Post-jam additions use new types, not changed semantics.