package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"golang.org/x/net/websocket"
)

// WSMessage is the envelope for all WebSocket messages.
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Hub manages WebSocket client connections and broadcasts messages.
type Hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.RWMutex
}

// newHub creates a new Hub ready to run.
func newHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

// run is the main loop for the hub, managing client registration and broadcasts.
// It should be started as a goroutine.
func (h *Hub) run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()
			log.Printf("ws: client connected (%d total)", len(h.clients))

		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				conn.Close()
			}
			h.mu.Unlock()
			log.Printf("ws: client disconnected (%d total)", len(h.clients))

		case msg := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.clients {
				if _, err := conn.Write(msg); err != nil {
					// Will be cleaned up when the read loop exits.
					log.Printf("ws: write error: %v", err)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// handleWS returns an http.HandlerFunc that upgrades to WebSocket.
func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	wsHandler := websocket.Handler(func(conn *websocket.Conn) {
		s.hub.register <- conn
		defer func() {
			s.hub.unregister <- conn
		}()

		for {
			var raw []byte
			if err := websocket.Message.Receive(conn, &raw); err != nil {
				break
			}
			s.handleWSMessage(conn, raw)
		}
	})
	wsHandler.ServeHTTP(w, r)
}

// handleWSMessage processes an incoming WebSocket message from a client.
func (s *Session) handleWSMessage(conn *websocket.Conn, raw []byte) {
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("ws: invalid message: %v", err)
		return
	}

	switch msg.Type {
	case "inject":
		var payload struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			log.Printf("ws: inject: invalid data: %v", err)
			return
		}
		s.mu.RLock()
		a := s.architecture
		s.mu.RUnlock()
		if a == nil {
			log.Printf("ws: inject: no simulation running")
			return
		}
		a.Inject(payload.Name, payload.Params)

	default:
		log.Printf("ws: unknown message type: %q", msg.Type)
	}
}
