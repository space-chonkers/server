# B1 — starting world and resources

**Status:** implemented; `go test ./...` and `go vet ./...` pass  
**Ticket:** [TICKETS-M1.md](./TICKETS-M1.md#L76)  
**Contracts:** [SCOPE.md](./SCOPE.md), [PROTOCOL.md](./PROTOCOL.md), and
`internal/game/api.go` (the expected C2 API contract was absent from this
checkout, so B1 adds a minimal world/state surface for this ticket)

## Goal

When a match starts, construct one valid deterministic-from-seed world on a
50×50 grid: a randomly placed 3×3 starting base, a centered stash, four
cardinal spawn positions, N distinct investigation cells outside the starting
base, and correctly initialized four-resource state.

“50×50 base” in the ticket is interpreted as a **50×50 grid** containing a
3×3 starting base, consistent with the product scope and protocol.

## Required outcomes

- Grid dimensions are 50×50; all generated cell coordinates are within
  `0..49`.
- The initial base is a contiguous 3×3 square fully inside the grid. It has
  exactly 9 cells: one stash at its center and 8 `base_tile` constructions.
- The four spawn points occupy the four cardinal edge cells of the 3×3 base,
  using the established slot-to-direction mapping, and face outward.
- Create exactly `INVESTIGATION_COUNT` distinct goal cells (default N=10), none
  inside the initial base.
- Given the same seed and inputs, generation returns the same start position,
  constructions, goals, spawns, and initial resources. Different seeds may
  produce different valid worlds. Generation must not depend on global random
  state.
- Initialize the four resource pools and stashes using B=9 and the frozen
  starting team size P:
  - `pool.max = POOL_PER_PLAYER × P`
  - `pool.current = pool.max × STARTING_POOL_FRACTION`
  - `stash.max = STASH_BASE + STASH_PER_TILE × B + STASH_PER_PLAYER × P`
  - `stash.current = 0`
  - Water/oxygen/energy GPS = `GPS_PER_TILE × B`; food GPS =
    `FOOD_GPS_PER_PLAYER × P`
  - Water/oxygen/energy CPS = `CPS_PER_PLAYER × P + CPS_PER_TILE × B`; food
    CPS = `FOOD_CPS_PER_PLAYER × P`
- Default values come from the placeholder constants in `SCOPE.md`; calculations
  must use the supplied game configuration rather than duplicate those defaults.
- The frozen C2 API was absent, so add only the minimal game API required for
  B1. Keep implementation within `internal/game/{api,map,state,random}.go` and
  related tests; reconcile the API if the original contract is restored. Do not
  edit Path A transport, protocol, lifecycle, or manager files.

## Implementation steps

1. **Unblock on the shared API.** The expected C2 `internal/game/api.go`
   contract was absent from this checkout. Add a minimal B1-compatible
   world/state surface, then reconcile it with the original C2 contract if
   that contract is restored.
2. **Implement seeded map generation.** Use a local seeded PRNG. Select a valid
   3×3 top-left coordinate, derive its stash and eight tile coordinates, and
   assign the four cardinal spawn cells and outward facings according to the
   API’s slot convention.
3. **Place investigation cells.** Select N unique cells from the 50×50 cells
   outside the start footprint, using the same local PRNG so the complete map
   is repeatable for a given seed.
4. **Initialize match state and resources.** Set grid/base counts, constructions,
   goals, spawns, team size, and initial resource pools/stashes/rates from the
   supplied configuration and formulas above. Keep the requested starting team
   size as the value used for initialization.
5. **Add focused tests.** Cover exact structure/counts, bounds, stash center,
   all four edge spawns/facings, goals outside the base and without duplicates,
   valid resource formulas for team sizes 1–4, and same-seed reproducibility.
   Include a range of seeds to catch boundary placement errors; avoid asserting
   statistical randomness.
6. **Validate.** Run the focused game tests, then `go test ./...` once the game
   package and its C2 dependencies are present. Do not take on B2 economy ticking,
   B3 movement, actions, or any wire-schema changes as part of B1.

## Out of scope

- Resource ticking, caps changing after builds, depletion/loss, and economy
  configuration validation beyond what is needed to initialize valid state
  (B2).
- Movement simulation/clamping, action handling, build/collect, and phenomena
  (B3–B5).
- Match lifecycle, network messages, protocol/schema changes, and integration
  wiring owned by Path A.

## Decisions provided by user

- Proceed despite the missing C2 API with a minimal B1 game API; reconcile it
  with the original contract if restored.
- Accept the generation seed internally; do not add it to the wire contract.
  The match layer can later derive it from the client.
- Map player slots to cardinal spawns in N/E/S/W order (0/1/2/3).
- Keep the 3×3 start fully inside the grid with no extra edge margin.
- Use seeded Poisson-disk goal sampling outside the base, with a temporary
  minimum distance of 5 cells.
- Leave `cps > gps` validation to B2; current rates are placeholders.

## Original scoping questions

1. **C2 API is missing here (blocking).** The ticket marks C2 complete and says
   `internal/game/api.go` is frozen, but the current checkout contains no
   `internal/game` files. Which commit/branch supplies that API? B1 should wait
   for it rather than silently recreating the contract. -> Do it anyways
2. **Seed ownership and persistence.** The protocol has no seed field. Is the
   seed supplied internally by the match layer, and does it need to be retained
   in game state for diagnostics/replay? Recommendation: accept it as an
   internal generation input and do not add it to the wire contract for B1. -> LGTM, seed will be generated based on client so lets use an interna input for now
3. **Spawn mapping.** The docs say N/E/S/W “by slot” but do not spell out
   whether slots 0–3 map in that exact order. Confirm the frozen API’s mapping;
   recommendation if absent: slot 0=N, 1=E, 2=S, 3=W. -> LGTM
4. **Random start distribution.** The docs require a random valid 3×3 start but
   do not define its distribution or a minimum margin from the world edge.
   Recommendation: choose each legal top-left coordinate uniformly from
   `0..47` independently for X and Y; no extra margin is required. -> Yes, let's keep the base generated inside the limits of the map
5. **Goal selection distribution.** The docs require N distinct goals outside
   the start but do not specify a sampling method. Recommendation: uniform
   sampling without replacement from all remaining cells, using the same
   seeded PRNG. -> We can use a uniform replacement using the poisson disk sampling algorithm to distribute them at a minimum separated distance
6. **Config validation boundary.** B1 needs valid formulas and a valid initial
   state, while `cps > gps` validation is explicitly B2. Confirm that B1 should
   not own the broader config validation; recommendation: leave it to B2. -> Add a placeholder value for now, B2 will solve the issue

## Definition of done

- Every generated start satisfies the required structure, bounds, goals, spawns,
  and resource values.
- A fixed seed and input configuration produce identical output on repeated
  runs.
- Focused game tests, `go test ./...`, and `go vet ./...` pass. The race check
  could not run because cgo/a C compiler is unavailable in this environment.
- No Path A files or unrelated functionality are changed; reconcile the
  temporary B1 API with C2 if restored.
