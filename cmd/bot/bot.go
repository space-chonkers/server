package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/protocol"
)

var errMatchEnded = errors.New("match ended")

type cell struct {
	x int
	y int
}

type direction struct {
	facing protocol.Facing
	dx     int
	dy     int
}

type taskKind int

const (
	taskCollect taskKind = iota + 1
	taskFix
	taskBuild
)

type task struct {
	kind   taskKind
	target cell
	stand  cell
	dir    protocol.Facing
}

var directions = []direction{
	{facing: protocol.FacingNorth, dx: 0, dy: -1},
	{facing: protocol.FacingEast, dx: 1, dy: 0},
	{facing: protocol.FacingSouth, dx: 0, dy: 1},
	{facing: protocol.FacingWest, dx: -1, dy: 0},
}

type bot struct {
	index    int
	url      string
	nickname string

	conn     *websocket.Conn
	playerID string

	rules    protocol.Rules
	tickRate float64

	grid          protocol.GridSize
	base          map[cell]struct{}
	goals         []cell
	stash         cell
	hasStash      bool
	resources     protocol.Resources
	phenomenon    protocol.Phenomenon
	player        protocol.PlayerState
	playing       bool
	ended         bool
	seq           int64
	actionReadyAt time.Time
	lastErrorCode protocol.ErrorCode
	lastErrorAt   time.Time

	needCollect bool
	buildTask   *task
}

func newBot(index int, url, nickname string) *bot {
	return &bot{
		index:    index,
		url:      url,
		nickname: nickname,
		base:     make(map[cell]struct{}),
		tickRate: 30,
	}
}

func (b *bot) connect(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, b.url, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", b.url, err)
	}
	b.conn = conn

	envelope, err := b.readEnvelope(ctx, 5*time.Second)
	if err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	if envelope.Type != protocol.TypeWelcome {
		return fmt.Errorf("expected welcome, got %q", envelope.Type)
	}
	var welcome protocol.Welcome
	if err := json.Unmarshal(envelope.Payload, &welcome); err != nil {
		return fmt.Errorf("decode welcome: %w", err)
	}
	if welcome.ProtocolVersion != protocol.ProtocolVersion {
		return fmt.Errorf("protocol version %d, want %d",
			welcome.ProtocolVersion, protocol.ProtocolVersion)
	}
	b.rules = welcome.Rules
	if welcome.TickRate > 0 &&
		!math.IsNaN(welcome.TickRate) && !math.IsInf(welcome.TickRate, 0) {
		b.tickRate = welcome.TickRate
	}
	if b.rules.PlayerSpeed <= 0 {
		b.rules.PlayerSpeed = 4
	}

	hello := &protocol.PlayerHelloRequest{
		ProtocolVersion: welcome.ProtocolVersion,
		Nickname:        b.nickname,
	}
	envelope, err = b.request(ctx, "hello-1", hello, protocol.TypePlayerHello)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	var result protocol.HelloResult
	if err := json.Unmarshal(envelope.Payload, &result); err != nil {
		return fmt.Errorf("decode hello: %w", err)
	}
	b.playerID = result.PlayerID
	return nil
}

func (b *bot) createMatch(ctx context.Context) (string, error) {
	create := true
	envelope, err := b.request(ctx, "join-1",
		&protocol.MatchJoinRequest{Create: &create}, protocol.TypeMatchJoin)
	if err != nil {
		return "", fmt.Errorf("create match: %w", err)
	}
	var result protocol.MatchJoinResult
	if err := json.Unmarshal(envelope.Payload, &result); err != nil {
		return "", fmt.Errorf("decode join: %w", err)
	}
	b.applyState(result.State)
	return result.MatchID, nil
}

func (b *bot) joinMatch(ctx context.Context, matchID string) error {
	envelope, err := b.request(ctx, "join-1",
		&protocol.MatchJoinRequest{MatchID: &matchID}, protocol.TypeMatchJoin)
	if err != nil {
		return fmt.Errorf("join match: %w", err)
	}
	var result protocol.MatchJoinResult
	if err := json.Unmarshal(envelope.Payload, &result); err != nil {
		return fmt.Errorf("decode join: %w", err)
	}
	b.applyState(result.State)
	return nil
}

func (b *bot) startMatch(ctx context.Context) error {
	if _, err := b.request(ctx, "start-1",
		&protocol.MatchStartRequest{}, protocol.TypeMatchStart); err != nil {
		return fmt.Errorf("start match: %w", err)
	}
	return nil
}

func (b *bot) request(
	ctx context.Context,
	id string,
	request protocol.Request,
	typ protocol.MessageType,
) (protocol.Envelope, error) {
	if err := b.sendRequest(ctx, id, request); err != nil {
		return protocol.Envelope{}, err
	}
	return b.await(ctx, id, typ)
}

func (b *bot) await(
	ctx context.Context,
	id string,
	typ protocol.MessageType,
) (protocol.Envelope, error) {
	for {
		envelope, err := b.readEnvelope(ctx, 5*time.Second)
		if err != nil {
			return envelope, err
		}
		if envelope.ID == id && envelope.Type == typ {
			return envelope, nil
		}
		if envelope.Type == protocol.TypeError && (envelope.ID == id || envelope.ID == "") {
			b.handleError(envelope)
			return envelope, fmt.Errorf("request %s failed: %s", id, envelope.Error)
		}
		if envelope.Type == protocol.TypeMatchEnded {
			_, _ = b.handleMessage(envelope)
			return envelope, errMatchEnded
		}
		if _, err := b.handleMessage(envelope); err != nil {
			return envelope, err
		}
	}
}

func (b *bot) play(ctx context.Context) error {
	playCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	messages := make(chan protocol.Envelope, 64)
	readErr := make(chan error, 1)

	go func() {
		for {
			envelope, err := b.readEnvelope(playCtx, 0)
			if err != nil {
				select {
				case readErr <- err:
				case <-playCtx.Done():
				}
				return
			}
			select {
			case messages <- envelope:
			case <-playCtx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(time.Duration(float64(time.Second) / b.tickRate))
	defer ticker.Stop()

	for {
		select {
		case <-playCtx.Done():
			return nil
		case err := <-readErr:
			if contextDone(playCtx) ||
				websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		case envelope := <-messages:
			ended, err := b.handleMessage(envelope)
			if err != nil {
				return err
			}
			if ended {
				return nil
			}
		case <-ticker.C:
			if err := b.tick(playCtx); err != nil {
				if contextDone(playCtx) {
					return nil
				}
				return err
			}
		}
	}
}

func (b *bot) tick(ctx context.Context) error {
	if !b.playing || b.ended {
		return nil
	}

	b.seq++
	if err := b.sendInput(ctx); err != nil {
		return err
	}
	if time.Now().Before(b.actionReadyAt) {
		return nil
	}
	return b.act(ctx)
}

func (b *bot) sendInput(ctx context.Context) error {
	x, y := b.desiredMove()
	seq := b.seq
	request := &protocol.MatchInputRequest{Seq: &seq}
	if x != 0 || y != 0 {
		request.Move = &protocol.InputMove{X: &x, Y: &y}
	}
	message, err := protocol.EncodeRequest("", request)
	if err != nil {
		return fmt.Errorf("encode input: %w", err)
	}
	if err := b.write(ctx, message); err != nil {
		return fmt.Errorf("write input: %w", err)
	}
	return nil
}

func (b *bot) act(ctx context.Context) error {
	if b.ended || !b.playing {
		return nil
	}

	switch next := b.nextTask(); next.kind {
	case taskFix:
		if !b.atCell(next.target) {
			return nil
		}
		b.actionReadyAt = time.Now().Add(500 * time.Millisecond)
		return b.sendAction(ctx, &protocol.PhenomenonFixRequest{}, "fix")
	case taskCollect:
		if !b.atCell(next.target) {
			return nil
		}
		b.needCollect = false
		b.actionReadyAt = time.Now().Add(500 * time.Millisecond)
		return b.sendAction(ctx, &protocol.StashCollectRequest{}, "collect")
	case taskBuild:
		if !b.atCell(next.stand) || b.player.Facing != next.dir {
			return nil
		}
		b.buildTask = nil
		b.actionReadyAt = time.Now().Add(500 * time.Millisecond)
		cellX, cellY := next.target.x, next.target.y
		return b.sendAction(ctx, &protocol.BaseBuildRequest{
			CellX: &cellX,
			CellY: &cellY,
		}, "build")
	}
	return nil
}

func (b *bot) sendAction(ctx context.Context, request protocol.Request, label string) error {
	id := fmt.Sprintf("bot-%d-%s-%d", b.index, label, b.seq)
	if err := b.sendRequest(ctx, id, request); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func (b *bot) desiredMove() (float64, float64) {
	if !b.playing || b.ended || b.player.ID == "" {
		return 0, 0
	}

	switch next := b.nextTask(); next.kind {
	case taskCollect, taskFix:
		if b.atCell(next.target) {
			return 0, 0
		}
		return b.moveToward(next.target)
	case taskBuild:
		if !b.atCell(next.stand) {
			return b.moveToward(next.stand)
		}
		if b.player.Facing != next.dir {
			return facingVector(next.dir)
		}
	}
	return 0, 0
}

func (b *bot) nextTask() task {
	if b.hasPhenomenon() {
		if !b.hasStash {
			return task{}
		}
		if b.resources.Energy.Pool.Current < fixCost(b.phenomenon) {
			return task{kind: taskCollect, target: b.stash}
		}
		return task{kind: taskFix, target: b.stash}
	}
	if b.needCollect || b.shouldCollect() {
		if !b.hasStash {
			return task{}
		}
		return task{kind: taskCollect, target: b.stash}
	}
	if b.buildTask == nil {
		b.buildTask = b.chooseBuild()
	}
	if b.buildTask == nil {
		return task{}
	}
	return *b.buildTask
}

func (b *bot) moveToward(target cell) (float64, float64) {
	start := cellOf(b.player.X, b.player.Y)
	if start == target {
		return b.vectorTo(float64(target.x)+0.5, float64(target.y)+0.5)
	}
	path := b.path(start, target)
	if len(path) < 2 {
		return b.vectorTo(float64(target.x)+0.5, float64(target.y)+0.5)
	}
	next := path[1]
	return b.vectorTo(float64(next.x)+0.5, float64(next.y)+0.5)
}

func (b *bot) vectorTo(x, y float64) (float64, float64) {
	dx, dy := x-b.player.X, y-b.player.Y
	distance := math.Hypot(dx, dy)
	if distance < 1e-9 {
		return 0, 0
	}
	step := b.rules.PlayerSpeed / b.tickRate
	if step <= 0 {
		step = 0.1
	}
	if distance <= step {
		return dx / step, dy / step
	}
	return dx / distance, dy / distance
}

func (b *bot) path(from, to cell) []cell {
	if from == to {
		return []cell{from}
	}
	queue := []cell{from}
	previous := map[cell]cell{from: from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, direction := range directions {
			next := cell{current.x + direction.dx, current.y + direction.dy}
			if _, walkable := b.base[next]; !walkable {
				continue
			}
			if _, seen := previous[next]; seen {
				continue
			}
			previous[next] = current
			if next == to {
				return reconstructPath(previous, from, to)
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func reconstructPath(previous map[cell]cell, from, to cell) []cell {
	path := []cell{to}
	for current := to; current != from; {
		current = previous[current]
		path = append(path, current)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path
}

func (b *bot) atCell(target cell) bool {
	if b.player.ID == "" {
		return false
	}
	if cellOf(b.player.X, b.player.Y) != target {
		return false
	}
	return math.Abs(b.player.X-(float64(target.x)+0.5)) < 0.35 &&
		math.Abs(b.player.Y-(float64(target.y)+0.5)) < 0.35
}

func (b *bot) chooseBuild() *task {
	goals := b.uncoveredGoals()
	if len(goals) == 0 {
		return nil
	}

	var best *task
	bestScore := math.MaxInt
	for stand := range b.base {
		for _, direction := range directions {
			target := cell{stand.x + direction.dx, stand.y + direction.dy}
			if !b.inBounds(target) || b.isBase(target) {
				continue
			}
			score := nearestGoal(goals, target)
			if best == nil || score < bestScore ||
				(score == bestScore && lessCell(target, best.target)) {
				best = &task{
					kind:   taskBuild,
					target: target,
					stand:  stand,
					dir:    direction.facing,
				}
				bestScore = score
			}
		}
	}
	return best
}

func (b *bot) uncoveredGoals() []cell {
	goals := make([]cell, 0, len(b.goals))
	for _, goal := range b.goals {
		if !b.isBase(goal) {
			goals = append(goals, goal)
		}
	}
	return goals
}

func nearestGoal(goals []cell, target cell) int {
	best := math.MaxInt
	for _, goal := range goals {
		distance := abs(goal.x-target.x) + abs(goal.y-target.y)
		if distance < best {
			best = distance
		}
	}
	return best
}

func (b *bot) shouldCollect() bool {
	cost := b.rules.BuildCost
	return b.resources.Water.Pool.Current < cost.Water ||
		b.resources.Oxygen.Pool.Current < cost.Oxygen ||
		b.resources.Energy.Pool.Current < cost.Energy
}

func (b *bot) hasPhenomenon() bool {
	return b.phenomenon != nil && b.phenomenon.PhenomenonType() != protocol.PhenomenonNoneType
}

func fixCost(phenomenon protocol.Phenomenon) float64 {
	switch phenomenon := phenomenon.(type) {
	case protocol.PhenomenonSolar:
		return phenomenon.FixCostEnergy
	case protocol.PhenomenonWaterLeak:
		return phenomenon.FixCostEnergy
	case protocol.PhenomenonOxygenLoss:
		return phenomenon.FixCostEnergy
	default:
		return 0
	}
}

func (b *bot) isBase(target cell) bool {
	_, ok := b.base[target]
	return ok
}

func (b *bot) inBounds(target cell) bool {
	return target.x >= 0 && target.x < b.grid.Width &&
		target.y >= 0 && target.y < b.grid.Height
}

func (b *bot) handleMessage(envelope protocol.Envelope) (bool, error) {
	switch envelope.Type {
	case protocol.TypeMatchState:
		var state protocol.MatchState
		if err := json.Unmarshal(envelope.Payload, &state); err != nil {
			return false, fmt.Errorf("decode match.state: %w", err)
		}
		b.applyState(state)
	case protocol.TypeMatchFrame:
		var frame protocol.MatchFrame
		if err := json.Unmarshal(envelope.Payload, &frame); err != nil {
			return false, fmt.Errorf("decode match.frame: %w", err)
		}
		b.applyFrame(frame)
	case protocol.TypeMatchBuild:
		var event protocol.MatchBuild
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return false, fmt.Errorf("decode match.build: %w", err)
		}
		b.applyConstruction(event.Construction)
		b.resources = event.Resources
	case protocol.TypeMatchPhenomenon:
		var event protocol.MatchPhenomenon
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return false, fmt.Errorf("decode match.phenomenon: %w", err)
		}
		b.applyPhenomenonEvent(event)
	case protocol.TypeMatchEnded:
		var ended protocol.MatchEnded
		if err := json.Unmarshal(envelope.Payload, &ended); err != nil {
			return false, fmt.Errorf("decode match.ended: %w", err)
		}
		log.Printf("bot %d match ended: %s/%s", b.index, ended.Outcome, ended.Reason)
		b.ended = true
		return true, nil
	case protocol.TypeError:
		b.handleError(envelope)
	case protocol.TypeBaseBuild:
		var result protocol.BaseBuildResult
		if err := json.Unmarshal(envelope.Payload, &result); err != nil {
			return false, fmt.Errorf("decode base.build: %w", err)
		}
		b.applyConstruction(result.Construction)
		b.resources = result.Resources
	case protocol.TypeStashCollect:
		var result protocol.StashCollectResult
		if err := json.Unmarshal(envelope.Payload, &result); err != nil {
			return false, fmt.Errorf("decode stash.collect: %w", err)
		}
		b.resources = result.Resources
		b.needCollect = false
	case protocol.TypePhenomenonFix:
		var result protocol.PhenomenonFixResult
		if err := json.Unmarshal(envelope.Payload, &result); err != nil {
			return false, fmt.Errorf("decode phenomenon.fix: %w", err)
		}
		b.phenomenon = result.Phenomenon.Value
		b.resources = result.Resources
	}
	return false, nil
}

func (b *bot) applyState(state protocol.MatchState) {
	b.playing = state.Phase == protocol.PhasePlaying
	b.grid = state.Grid

	b.base = make(map[cell]struct{}, len(state.Constructions))
	b.hasStash = false
	for _, construction := range state.Constructions {
		b.applyConstruction(construction)
	}

	b.goals = make([]cell, 0, len(state.Investigation))
	for _, goal := range state.Investigation {
		b.goals = append(b.goals, cell{goal.CellX, goal.CellY})
	}
	b.resources = state.Resources
	b.phenomenon = state.Phenomenon.Value
	b.player = findPlayer(state.Players, b.playerID)
	b.buildTask = nil
}

func (b *bot) applyFrame(frame protocol.MatchFrame) {
	b.playing = true
	b.resources = frame.Resources
	b.phenomenon = frame.Phenomenon.Value
	b.player = findPlayer(frame.Players, b.playerID)
}

func (b *bot) applyConstruction(construction protocol.Construction) {
	position := cell{construction.CellX, construction.CellY}
	b.base[position] = struct{}{}
	if construction.Kind == protocol.ConstructionStash {
		b.stash = position
		b.hasStash = true
	}
	b.buildTask = nil
}

func (b *bot) applyPhenomenonEvent(event protocol.MatchPhenomenon) {
	switch event.Action {
	case protocol.PhenomenonActionStarted:
		b.phenomenon = event.Phenomenon
	default:
		b.phenomenon = protocol.PhenomenonNone{}
	}
}

func (b *bot) handleError(envelope protocol.Envelope) {
	var payload protocol.ErrorPayload
	_ = json.Unmarshal(envelope.Payload, &payload)
	now := time.Now()
	if envelope.Error != b.lastErrorCode || now.Sub(b.lastErrorAt) > 5*time.Second {
		log.Printf("bot %d error %s: %s", b.index, envelope.Error, payload.Message)
		b.lastErrorCode = envelope.Error
		b.lastErrorAt = now
	}

	b.actionReadyAt = now.Add(500 * time.Millisecond)
	switch envelope.Error {
	case protocol.CodeInsufficientResources, protocol.CodeInsufficientEnergy:
		b.needCollect = true
		b.buildTask = nil
	case protocol.CodeNotAtStash:
		b.needCollect = true
	case protocol.CodeNotInFront, protocol.CodeNotOnBase,
		protocol.CodeCellOccupied, protocol.CodeOutOfBounds:
		b.buildTask = nil
	case protocol.CodeNoPhenomenon:
		b.phenomenon = protocol.PhenomenonNone{}
	case protocol.CodeMatchOver:
		b.ended = true
	case protocol.CodeWrongPhase:
		b.playing = false
	case protocol.CodeInternal:
		b.actionReadyAt = now.Add(time.Second)
	}
}

func (b *bot) close() {
	_ = b.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	_ = b.conn.Close()
}

func (b *bot) sendRequest(ctx context.Context, id string, request protocol.Request) error {
	message, err := protocol.EncodeRequest(id, request)
	if err != nil {
		return err
	}
	return b.write(ctx, message)
}

func (b *bot) write(ctx context.Context, message []byte) error {
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = b.conn.SetWriteDeadline(deadline)
	return b.conn.WriteMessage(websocket.TextMessage, message)
}

func (b *bot) readEnvelope(ctx context.Context, timeout time.Duration) (protocol.Envelope, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	if contextDeadline, ok := ctx.Deadline(); ok &&
		(deadline.IsZero() || contextDeadline.Before(deadline)) {
		deadline = contextDeadline
	}
	_ = b.conn.SetReadDeadline(deadline)

	var envelope protocol.Envelope
	err := b.conn.ReadJSON(&envelope)
	return envelope, err
}

func contextDone(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func findPlayer(players []protocol.PlayerState, playerID string) protocol.PlayerState {
	for _, player := range players {
		if player.ID == playerID {
			return player
		}
	}
	return protocol.PlayerState{}
}

func cellOf(x, y float64) cell {
	return cell{x: int(math.Floor(x)), y: int(math.Floor(y))}
}

func facingVector(facing protocol.Facing) (float64, float64) {
	for _, direction := range directions {
		if direction.facing == facing {
			return float64(direction.dx), float64(direction.dy)
		}
	}
	return 0, 0
}

func lessCell(left, right cell) bool {
	if left.x == right.x {
		return left.y < right.y
	}
	return left.x < right.x
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
