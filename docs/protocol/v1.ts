/**
 * jam-server protocol v1 — reference types.
 *
 * Client-side mirror of docs/PROTOCOL.md and docs/protocol/v1.schema.json.
 * No runtime dependencies.
 *
 * Game: 1–4 players cooperate on a 50×50 grid. The base is the union of
 * construction cells. Players move inside the base, collect four resources at
 * the stash, and build base tiles outward. Server phenomena modify generation
 * or drain pools. Win by covering all investigation cells; lose when any
 * resource pool reaches zero.
 */

export const PROTOCOL_VERSION = 1 as const

export const DEFAULT_GRID_WIDTH = 50
export const DEFAULT_GRID_HEIGHT = 50
export const DEFAULT_TICK_RATE = 60
export const DEFAULT_FRAME_RATE = 60
export const DEFAULT_COUNTDOWN_MS = 3_000
export const DEFAULT_INVESTIGATION_COUNT = 10
export const DEFAULT_PLAYER_SPEED = 4
export const DEFAULT_INTERPOLATION_MS = 100
export const DEFAULT_PHENOMENON_ROLL_SECONDS = 30

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

export type ResourceKey = 'water' | 'oxygen' | 'energy' | 'food'
export type SolarResource = 'water' | 'oxygen' | 'energy'

/** One value per resource. Used for collected/lost/remaining. */
export interface ResourceAmounts {
  water: number
  oxygen: number
  energy: number
  food: number
}

export interface ResourcePool {
  current: number
  max: number
}

export interface ResourceStash {
  current: number
  max: number
  /** Effective generation per second after phenomena. */
  gps: number
}

export interface ResourceState {
  pool: ResourcePool
  stash: ResourceStash
  /** Effective pool drain per second after phenomena. */
  cps: number
}

export interface Resources {
  water: ResourceState
  oxygen: ResourceState
  energy: ResourceState
  food: ResourceState
}

// ---------------------------------------------------------------------------
// Grid and players
// ---------------------------------------------------------------------------

/** Movement vector. Server clamps length to 1; magnitude scales speed. */
export interface Vec2 {
  x: number
  y: number
}

/** Integer cell coordinates; bounds are validated against grid.width/height. */
export interface Cell {
  cellX: number
  cellY: number
}

export interface GridSize {
  width: number
  height: number
}

/** `match.ended` is terminal; there is no "ended" phase in state. */
export type Phase = 'lobby' | 'countdown' | 'playing'

export type Facing = 'north' | 'east' | 'south' | 'west'

export interface PlayerState {
  /** Stable for the life of the match. */
  id: string
  /** 0..3, stable while in the match, reused after a player leaves. */
  slot: number
  /** Canonical nickname; display only. */
  name: string
  /** Cardinal direction; derived from the last nonzero move. */
  facing: Facing
  /** Continuous position in grid units. */
  x: number
  y: number
  /** Actual velocity in cells/s after base clamp. */
  vx: number
  vy: number
  /** Highest accepted input seq; -1 if none. */
  lastInputSeq: number
}

export type ConstructionKind = 'base_tile' | 'stash'

export interface Construction {
  id: string
  kind: ConstructionKind
  cellX: number
  cellY: number
}

export interface InvestigationCell {
  cellX: number
  cellY: number
}

// ---------------------------------------------------------------------------
// Phenomena
// ---------------------------------------------------------------------------

export interface PhenomenonNone {
  type: 0
  startedAtMs: 0
  fixCostEnergy: 0
  effects: Record<string, never>
}

export interface PhenomenonSolar {
  type: 1
  startedAtMs: number
  fixCostEnergy: number
  effects: {
    /** Absolute fraction of base GPS lost per second; 0.10 reaches 0 at 10 s. */
    gpsReductionPerSecond: number
    resources: SolarResource[]
  }
}

export interface PhenomenonWaterLeak {
  type: 2
  startedAtMs: number
  fixCostEnergy: number
  effects: {
    cpsMultiplier: number
    resource: 'water'
  }
}

export interface PhenomenonOxygenLoss {
  type: 3
  startedAtMs: number
  fixCostEnergy: number
  effects: {
    cpsMultiplier: number
    resource: 'oxygen'
  }
}

export interface PhenomenonRottenFood {
  type: 4
  startedAtMs: number
  fixCostEnergy: 0
  effects: {
    foodLoss: number
  }
}

/** Active in match.state / match.frame (0–3). */
export type ActivePhenomenon =
  | PhenomenonNone
  | PhenomenonSolar
  | PhenomenonWaterLeak
  | PhenomenonOxygenLoss

/** Also includes the instant rotten-food event (4). */
export type EventPhenomenon = ActivePhenomenon | PhenomenonRottenFood

// ---------------------------------------------------------------------------
// State payloads
// ---------------------------------------------------------------------------

export interface MatchState {
  matchId: string
  /** Game-state schema. Additive changes do not bump it. */
  schema: 1
  /** Completed simulation steps; 0 before `playing`. */
  tick: number
  /** Unix epoch milliseconds when the message was created. */
  serverTimeMs: number
  phase: Phase
  /** Epoch ms when `countdown` ends; 0 otherwise. */
  phaseEndsAtMs: number
  hostId: string
  grid: GridSize
  /** Unique base cells, including the stash cell. */
  baseCellCount: number
  /** Starting team size, frozen at match.start; drives caps/CPS. */
  teamSize: number
  /** Sorted by slot ascending. */
  players: PlayerState[]
  /** Sorted by cell; unique cells. */
  constructions: Construction[]
  /** Goal cells; sorted; all revealed at start. */
  investigation: InvestigationCell[]
  resources: Resources
  phenomenon: ActivePhenomenon
}

export interface MatchFrame {
  matchId: string
  tick: number
  serverTimeMs: number
  hostId: string
  baseCellCount: number
  teamSize: number
  players: PlayerState[]
  resources: Resources
  phenomenon: ActivePhenomenon
}

export interface MatchBuild {
  matchId: string
  construction: Construction
  resources: Resources
}

export type MatchPhenomenonAction = 'started' | 'resolved' | 'instant'

export interface MatchPhenomenon {
  matchId: string
  action: MatchPhenomenonAction
  phenomenon: EventPhenomenon
}

export type MatchOutcome = 'win' | 'loss' | 'abandoned'

export type MatchEndReason =
  | 'all_investigated'
  | 'resource_depleted'
  | 'abandoned'

export interface MatchStats {
  elapsedMs: number
  baseCells: number
  investigationCovered: number
  investigationTotal: number
  remaining: ResourceAmounts
  /** Present on resource-depletion losses. */
  depletedResource?: ResourceKey
}

export interface MatchEnded {
  matchId: string
  endedAtMs: number
  outcome: MatchOutcome
  reason: MatchEndReason
  stats: MatchStats
}

export interface BuildCost {
  water: number
  oxygen: number
  energy: number
  food: number
}

export interface Rules {
  grid: GridSize
  investigationCount: number
  buildCost: BuildCost
  playerSpeed: number
  phenomenonRollSeconds: number
}

export interface Limits {
  maxMessageBytes: number
  maxPlayersPerMatch: number
  maxNicknameLength: number
  maxMatches: number
}

export interface Welcome {
  playerId: string
  protocolVersion: 1
  serverTimeMs: number
  tickRate: number
  frameRate: number
  limits: Limits
  rules: Rules
}

// ---------------------------------------------------------------------------
// Client -> server (v1)
// ---------------------------------------------------------------------------

export interface PingRequest {
  id: string
  type: 'ping'
  payload?: { clientTimeMs?: number }
}

export interface PlayerHelloRequest {
  id: string
  type: 'player.hello'
  payload: {
    protocolVersion: 1
    nickname: string
  }
}

export interface MatchJoinRequest {
  id: string
  type: 'match.join'
  /**
   * - `{ matchId }`: join that code.
   * - `{ create: true }`: always create a fresh lobby.
   * - `{}`: quick-join a lobby with space, creating one if needed.
   */
  payload?: { matchId?: string; create?: boolean }
}

export interface MatchLeaveRequest {
  id: string
  type: 'match.leave'
}

export interface MatchStartRequest {
  id: string
  type: 'match.start'
}

/** Fire-and-forget movement; no id, no direct response. */
export interface MatchInputRequest {
  type: 'match.input'
  payload: {
    /** Monotonic per player, starts at 0. */
    seq: number
    /** Full intent for this tick; omitted = zero. */
    move?: Vec2
  }
}

/** Requires the player's current cell to be the stash cell. */
export interface StashCollectRequest {
  id: string
  type: 'stash.collect'
}

/** Requires the player on a base cell; target orthogonal to the player's cell. */
export interface BaseBuildRequest {
  id: string
  type: 'base.build'
  payload: {
    cellX: number
    cellY: number
  }
}

/** Requires the player's current cell to be the stash cell. */
export interface PhenomenonFixRequest {
  id: string
  type: 'phenomenon.fix'
}

export type ClientRequest =
  | PingRequest
  | PlayerHelloRequest
  | MatchJoinRequest
  | MatchLeaveRequest
  | MatchStartRequest
  | MatchInputRequest
  | StashCollectRequest
  | BaseBuildRequest
  | PhenomenonFixRequest

// ---------------------------------------------------------------------------
// Server -> client responses (v1)
// ---------------------------------------------------------------------------

export interface PongResponse {
  id: string
  type: 'pong'
  payload: {
    clientTimeMs?: number
    serverTimeMs: number
    tick: number
  }
}

export interface PlayerHelloResponse {
  id: string
  type: 'player.hello'
  payload: {
    playerId: string
    nickname: string
  }
}

export interface MatchJoinResponse {
  id: string
  type: 'match.join'
  payload: {
    matchId: string
    state: MatchState
  }
}

export interface MatchLeaveResponse {
  id: string
  type: 'match.leave'
  payload: { ok: true }
}

export interface MatchStartResponse {
  id: string
  type: 'match.start'
  payload: { ok: true }
}

export interface StashCollectResponse {
  id: string
  type: 'stash.collect'
  payload: {
    collected: ResourceAmounts
    lost: ResourceAmounts
    resources: Resources
  }
}

export interface BaseBuildResponse {
  id: string
  type: 'base.build'
  payload: {
    construction: Construction
    resources: Resources
  }
}

export interface PhenomenonFixResponse {
  id: string
  type: 'phenomenon.fix'
  payload: {
    phenomenon: ActivePhenomenon
    resources: Resources
  }
}

export interface ErrorResponse {
  id: string
  type: 'error'
  error: ErrorCode
  payload?: ErrorPayload
}

export type ErrorCode =
  | 'invalid_json'
  | 'invalid_payload'
  | 'unsupported_type'
  | 'protocol_version_mismatch'
  | 'not_identified'
  | 'already_identified'
  | 'already_in_match'
  | 'not_in_match'
  | 'match_not_found'
  | 'match_full'
  | 'match_in_progress'
  | 'not_host'
  | 'wrong_phase'
  | 'not_on_base'
  | 'not_in_front'
  | 'cell_occupied'
  | 'out_of_bounds'
  | 'insufficient_resources'
  | 'insufficient_energy'
  | 'not_at_stash'
  | 'no_phenomenon'
  | 'match_over'
  | 'invalid_cell'
  | 'server_full'
  | 'rate_limited'
  | 'internal'

export interface ErrorPayload {
  message?: string
  /** For insufficient_resources: which build costs are short. */
  missing?: ResourceKey[]
}

export type ServerResponse =
  | PongResponse
  | PlayerHelloResponse
  | MatchJoinResponse
  | MatchLeaveResponse
  | MatchStartResponse
  | StashCollectResponse
  | BaseBuildResponse
  | PhenomenonFixResponse
  | ErrorResponse

// ---------------------------------------------------------------------------
// Server -> client events (v1)
// ---------------------------------------------------------------------------

export interface WelcomeEvent {
  type: 'welcome'
  payload: Welcome
}

export interface MatchStateEvent {
  type: 'match.state'
  payload: MatchState
}

export interface MatchFrameEvent {
  type: 'match.frame'
  payload: MatchFrame
}

export interface MatchBuildEvent {
  type: 'match.build'
  payload: MatchBuild
}

export interface MatchPhenomenonEvent {
  type: 'match.phenomenon'
  payload: MatchPhenomenon
}

export interface MatchEndedEvent {
  type: 'match.ended'
  payload: MatchEnded
}

/** Error events have no id; errors answering a request use `ErrorResponse`. */
export interface ErrorEvent {
  id?: never
  type: 'error'
  error: ErrorCode
  payload?: ErrorPayload
}

export type ServerEvent =
  | WelcomeEvent
  | MatchStateEvent
  | MatchFrameEvent
  | MatchBuildEvent
  | MatchPhenomenonEvent
  | MatchEndedEvent
  | ErrorEvent

export type ServerMessage = ServerResponse | ServerEvent

// ---------------------------------------------------------------------------
// Reserved for post-jam additions (not emitted/implemented in v1)
// ---------------------------------------------------------------------------

export interface LeaderboardEntry {
  rank: number
  playerId: string
  name: string
  score: number
  achievedAtMs: number
}

export interface LeaderboardTopRequest {
  id: string
  type: 'leaderboard.top'
  payload?: { limit?: number }
}

export interface LeaderboardTopResponse {
  id: string
  type: 'leaderboard.top'
  payload: { entries: LeaderboardEntry[] }
}

export interface MatchmakingEnqueueRequest {
  id: string
  type: 'matchmaking.enqueue'
  payload?: { partySize?: number }
}

export interface MatchmakingCancelRequest {
  id: string
  type: 'matchmaking.cancel'
}

export interface MatchmakingMatchedEvent {
  type: 'matchmaking.matched'
  payload: { matchId: string }
}

export interface LeaderboardUpdatedEvent {
  type: 'leaderboard.updated'
  payload: { entries: LeaderboardEntry[] }
}

export interface MatchEventEvent {
  type: 'match.event'
  payload: {
    matchId: string
    kind: string
    [key: string]: unknown
  }
}

export type ReservedClientRequest =
  | LeaderboardTopRequest
  | MatchmakingEnqueueRequest
  | MatchmakingCancelRequest

export type ReservedServerEvent =
  | LeaderboardTopResponse
  | MatchmakingMatchedEvent
  | LeaderboardUpdatedEvent
  | MatchEventEvent
