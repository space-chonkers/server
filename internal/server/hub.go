package server

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// Hub keeps track of connected clients and fans events out to them.
type Hub struct {
	log     *slog.Logger
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:     log,
		clients: make(map[*Client]struct{}),
	}
}

func (h *Hub) Add(client *Client) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) Remove(client *Client) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
	client.Close()
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends an event to every connected client except the sender.
func (h *Hub) Broadcast(eventType string, payload any, except *Client) {
	message, err := json.Marshal(Envelope{Type: eventType, Payload: marshalPayload(payload)})
	if err != nil {
		h.log.Error("failed to encode broadcast", "error", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		if client == except {
			continue
		}
		client.enqueue(message)
	}
}

// CloseAll asks every client to disconnect. It is used during shutdown because
// WebSocket connections are hijacked and are not tracked by http.Server.
func (h *Hub) CloseAll() {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	for _, client := range clients {
		client.Close()
	}
}
