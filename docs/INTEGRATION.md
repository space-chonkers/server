# Client integration — protocol v1

**Status:** draft for M0 sign-off
**Schema:** [protocol/v1.schema.json](./protocol/v1.schema.json) · **Types:** [protocol/v1.ts](./protocol/v1.ts) · **Spec:** [PROTOCOL.md](./PROTOCOL.md)

The JSON Schema is the authoritative shape of every message. `PROTOCOL.md`
explains behavior; this file is the short handoff guide.

## 1. Artifacts

| Artifact | Use |
| --- | --- |
| `protocol/v1.schema.json` | Runtime/CI validation of requests and messages |
| `protocol/v1.ts` | TypeScript types for the client |
| `PROTOCOL.md` | Semantics, rules, errors, examples |
| `SCOPE.md` | Product scope and tuning defaults |

The schema is strict for v1 (`additionalProperties: false`). Runtime clients
should still ignore unknown fields for forward compatibility.

## 2. Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/` | Service info |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness |
| `GET` | `/ws` | WebSocket upgrade; all game traffic |
| `GET` | `/debug/matches` | P1 debug; disabled by default |

All game messages are JSON text frames over `/ws`.

## 3. Envelope

```json
{ "id": "req-1", "type": "base.build", "payload": { "cellX": 25, "cellY": 23 } }
```

- `id` required on requests except `match.input`; responses echo it. Events omit it.
- `type` required; `payload` per type; `error` on errors.
- `match.input` has no response; `lastInputSeq` acknowledges it.
- Unknown request type → `error: "unsupported_type"`; socket stays open.
- Unknown event/field → ignore.

## 4. Message catalog

### Client → server

| Type | Schema | Response |
| --- | --- | --- |
| `ping` | `PingRequest` | `PongResponse` |
| `player.hello` | `PlayerHelloRequest` | `PlayerHelloResponse` |
| `match.join` | `MatchJoinRequest` | `MatchJoinResponse` |
| `match.leave` | `MatchLeaveRequest` | `MatchLeaveResponse` |
| `match.start` | `MatchStartRequest` | `MatchStartResponse` |
| `match.input` | `MatchInputRequest` | none (ack in frame) |
| `stash.collect` | `StashCollectRequest` | `StashCollectResponse` |
| `base.build` | `BaseBuildRequest` | `BaseBuildResponse` |
| `phenomenon.fix` | `PhenomenonFixRequest` | `PhenomenonFixResponse` |

### Server → client

| Type | Schema | When |
| --- | --- | --- |
| `welcome` | `WelcomeEvent` | First frame |
| `match.state` | `MatchStateEvent` | Join response, start, phase change |
| `match.frame` | `MatchFrameEvent` | 15 Hz while playing |
| `match.build` | `MatchBuildEvent` | After another player builds |
| `match.phenomenon` | `MatchPhenomenonEvent` | Event starts/resolves/is instant |
| `match.ended` | `MatchEndedEvent` | Win/loss/abandon |
| `error` | `ErrorMessage` | Request/event failure |

Schema paths are `#/$defs/<Name>`. Validate incoming client messages with
`#/$defs/ServerMessage` and incoming server requests with
`#/$defs/ClientRequest`.

## 5. Required fields

| Schema | Required |
| --- | --- |
| `PlayerState` | `id`, `slot`, `name`, `facing`, `x`, `y`, `vx`, `vy`, `lastInputSeq` |
| `Construction` | `id`, `kind`, `cellX`, `cellY` |
| `ResourceAmounts` | `water`, `oxygen`, `energy`, `food` |
| `Resources` | `water`, `oxygen`, `energy`, `food` |
| `ResourceState` | `pool {current,max}`, `stash {current,max,gps}`, `cps` |
| `MatchState` | `matchId`, `schema`, `tick`, `serverTimeMs`, `phase`, `phaseEndsAtMs`, `hostId`, `grid`, `baseCellCount`, `teamSize`, `players`, `constructions`, `investigation`, `resources`, `phenomenon` |
| `MatchFrame` | `matchId`, `tick`, `serverTimeMs`, `hostId`, `baseCellCount`, `teamSize`, `players`, `resources`, `phenomenon` |
| `MatchBuild` | `matchId`, `construction`, `resources` |
| `MatchPhenomenon` | `matchId`, `action`, `phenomenon` |
| `MatchStats` | `elapsedMs`, `baseCells`, `investigationCovered`, `investigationTotal`, `remaining` |
| `MatchEnded` | `matchId`, `endedAtMs`, `outcome`, `reason`, `stats` |
| `Welcome` | `playerId`, `protocolVersion`, `serverTimeMs`, `tickRate`, `frameRate`, `limits`, `rules` |
| `Rules` | `grid`, `investigationCount`, `buildCost`, `playerSpeed`, `phenomenonRollSeconds` |

`PhenomenonNone`, `PhenomenonSolar`, `PhenomenonWaterLeak`,
`PhenomenonOxygenLoss` are active states. `PhenomenonRottenFood` only appears in
a `match.phenomenon` event with `action: "instant"`. `MatchStats.depletedResource`
is optional and present on resource-depletion losses. Solar
`effects.gpsReductionPerSecond` is absolute (0.10 = 10 percentage points/s);
`resources.*.gps` is already the effective value.

## 6. Validation

### TypeScript / Ajv 2020

```ts
import Ajv2020 from 'ajv/dist/2020'
import schema from './protocol/v1.schema.json'

const schemaId = 'https://github.com/space-chonkers/server/docs/protocol/v1.schema.json'
const ajv = new Ajv2020({ allErrors: true })
ajv.addSchema(schema, schemaId)

const validateServer = ajv.getSchema(`${schemaId}#/$defs/ServerMessage`)!
const validateClient = ajv.getSchema(`${schemaId}#/$defs/ClientRequest`)!

const message: unknown = JSON.parse(event.data)
if (!validateServer(message)) {
  console.error(validateServer.errors)
  return
}
```

### Python

```python
import json
from jsonschema import Draft202012Validator

schema = json.load(open("protocol/v1.schema.json"))
validator = Draft202012Validator({
    "$schema": schema["$schema"],
    "$defs": schema["$defs"],
    "$ref": "#/$defs/ServerMessage",  # or ClientRequest
})
validator.validate(message)
```

Notes:

- `match.input` has no `id`.
- `match.join` rejects `matchId` + `create: true`; the schema allows both fields,
  so keep the cross-field check in logic.
- `cellX/cellY` are non-negative; the server checks `grid.width/height`.
- `protocolVersion` is `const: 1`; mismatch closes `1008`.

## 7. Handoff checklist

- [ ] Validate every incoming frame against `ServerMessage`.
- [ ] Validate every incoming request against `ClientRequest`.
- [ ] Check `welcome.protocolVersion`; store `welcome.rules`.
- [ ] Replace full state on `match.state`; apply build/phenomenon deltas.
- [ ] Derive current cell with `floor(x)`, `floor(y)`.
- [ ] Build UI: only the single cell in front (`current cell + facing`); turn
      by pushing a small `move` toward the target (facing updates even when
      blocked); show `rules.buildCost`; disable if any pool is short.
- [ ] Collect UI: stash cell only; discard overflow; show `collected`/`lost`.
- [ ] Fix UI: stash cell only; show `phenomenon.fixCostEnergy`; disable if
      energy is short.
- [ ] Show four resource bars: pool current/max, stash current/max, GPS, CPS.
- [ ] Use `teamSize` (frozen at start), not the current player list length, for
      cap/upkeep labels.
- [ ] Render active phenomenon from frames; use `match.phenomenon` for instant
      rotten-food toasts and start/resolve transitions.
- [ ] Handle every `ErrorCode` without closing the socket.
- [ ] Show shared outcome + `stats.remaining` on `match.ended`.
- [ ] No reconnect/removal/combat in v1.

## 8. Client repo submodule

The client repo consumes this repository as a git submodule at
`vendor/jam-server`. It is pinned to a server commit; update it intentionally:

```sh
git -C vendor/jam-server fetch
git -C vendor/jam-server checkout <tag-or-commit>
git add vendor/jam-server .gitmodules
```

- CI: clone with `--recurse-submodules` or run `git submodule update --init`.
- Docker: ensure the submodule is checked out before building the client image.
- A cross-repo symlink is not portable across CI, Docker, Windows, or GitHub’s
  file view; the submodule is the source of truth.

## 9. Compatibility

- `protocolVersion` bumps for breaking envelope/lifecycle changes.
- `state.schema` bumps for breaking gameplay payload changes.
- Additive fields/events do not bump; update the schema and types together.
- When a message changes, update `PROTOCOL.md`, the schema, `v1.ts`, and
  examples; validate all `json` examples before merging.
- Bus/Redis/federation must not change socket bytes.