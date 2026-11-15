package match

import "github.com/space-chonkers/server/internal/protocol"

// publishState sends the full state to every member. The caller holds
// publishMu.
func publishState(members []Member, state protocol.MatchState) error {
	event, err := protocol.NewEvent(protocol.TypeMatchState, state)
	if err != nil {
		return err
	}
	for _, member := range members {
		member.Sink.Send(event)
	}
	return nil
}

// publishJoin sends the join response to the joiner and the lobby state to
// every other member. The caller holds publishMu.
func publishJoin(members []Member, requestID, joinerID string, state protocol.MatchState) error {
	response, err := protocol.NewResponse(requestID, protocol.TypeMatchJoin, protocol.MatchJoinResult{
		MatchID: state.MatchID,
		State:   state,
	})
	if err != nil {
		return err
	}
	event, err := protocol.NewEvent(protocol.TypeMatchState, state)
	if err != nil {
		return err
	}
	for _, member := range members {
		if member.PlayerID == joinerID {
			member.Sink.Send(response)
			continue
		}
		member.Sink.Send(event)
	}
	return nil
}
