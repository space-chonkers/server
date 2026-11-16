# Reference bot

`cmd/bot` is a small reference client and smoke-test driver. It uses the same
`internal/protocol` contract as the server: no duplicate wire shapes and no
separate gameplay rules.

## Run

```sh
go run ./cmd/bot -players 1 -duration 5s
go run ./cmd/bot -players 4 -duration 60s
```

Against the Compose server published on host port 8081:

```sh
go run ./cmd/bot -url ws://localhost:8081/ws -players 4 -duration 60s
```

Join an existing lobby instead of creating one:

```sh
go run ./cmd/bot -match AB12
```

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `-url` | `ws://localhost:8080/ws` | WebSocket endpoint. |
| `-players` | `1` | Number of bots, `1..4`. |
| `-duration` | `60s` | Run time; the process exits cleanly when it elapses. |
| `-match` | empty | Join this match code instead of creating a lobby. |

Without `-match`, the first bot creates the lobby, the others join by code, and
the first bot starts the match once everyone has joined.

## Behavior

- Reads `welcome` first, checks the protocol version, and sends `player.hello`.
- Moves with `match.input`, using the latest authoritative frame for position and
  facing. It turns by pushing toward the target before building.
- Expands the base greedily toward the nearest uncovered investigation cell.
- Collects at the stash when a build cost is short, and fixes an active
  phenomenon at the stash when energy allows.
- Applies `match.state`, `match.frame`, `match.build`, `match.phenomenon`, and
  `match.ended`; protocol errors keep the socket open.

It does not reconnect, render, or guarantee a win in 60 seconds. Bot behavior
against live collect/build/fix handlers is covered by server integration tests;
the fake-server test separately exercises the bot side of that contract.

## Test

```sh
go test ./cmd/bot
```
