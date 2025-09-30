package wsx

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu    sync.RWMutex
	peers map[string]*Connection
}

func NewHub() *Hub {
	return &Hub{peers: make(map[string]*Connection)}
}

var DefaultUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(*http.Request) bool {
		return true
	},
}

func (h *Hub) WSHandler(buildID func(*http.Request) string, register func(*Connection)) http.HandlerFunc {
	if buildID == nil {
		panic("buildID must not be nil")
	}
	if register == nil {
		register = func(*Connection) {}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := buildID(r)
		if id == "" {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		conn, err := DefaultUpgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, fmt.Sprintf("upgrade failed: %v", err), http.StatusBadRequest)
			return
		}
		c := NewConnection(conn)

		h.register(id, c)
		register(c)
		c.Start(r.Context())
	}
}

func (h *Hub) register(id string, c *Connection) {
	h.mu.Lock()
	if old, ok := h.peers[id]; ok {
		h.mu.Unlock()
		old.Close()
		h.mu.Lock()
	}
	h.peers[id] = c
	h.mu.Unlock()
	c.id = id
	c.OnClose(func() {
		h.mu.Lock()
		if current, ok := h.peers[id]; ok && current == c {
			delete(h.peers, id)
		}
		h.mu.Unlock()
	})
}

func (h *Hub) SendTo(id string, env Envelope) error {
	h.mu.RLock()
	conn, ok := h.peers[id]
	h.mu.RUnlock()
	if !ok {
		return http.ErrNoLocation
	}
	return conn.SendEnvelope(env)
}

func (h *Hub) Broadcast(env Envelope) {
	h.mu.RLock()
	peers := make([]*Connection, 0, len(h.peers))
	for _, conn := range h.peers {
		peers = append(peers, conn)
	}
	h.mu.RUnlock()

	for _, conn := range peers {
		_ = conn.SendEnvelope(env)
	}
}
