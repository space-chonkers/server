package server

import "sync"

// Hub keeps track of connected clients. Match rooms replace the global registry
// once the match manager lands.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{
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
		client.GoAway()
	}
}
