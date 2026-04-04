package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/studio"
)

// Session holds all server state for the rapide-studio application.
type Session struct {
	schemas      map[string]*studio.ArchitectureSchema
	nextID       int
	architecture *arch.Architecture
	recorder     *studio.Recorder
	simCancel    context.CancelFunc
	hub          *Hub
	mu           sync.RWMutex
}

// newSession creates a new Session with empty state.
func newSession(hub *Hub) *Session {
	return &Session{
		schemas: make(map[string]*studio.ArchitectureSchema),
		nextID:  1,
		hub:     hub,
	}
}

// registerRoutes registers all HTTP endpoints on the given ServeMux.
func (s *Session) registerRoutes(mux *http.ServeMux) {
	// Architecture CRUD
	mux.HandleFunc("GET /api/architectures", s.listArchitectures)
	mux.HandleFunc("POST /api/architectures", s.createArchitecture)
	mux.HandleFunc("GET /api/architectures/{id}", s.getArchitecture)
	mux.HandleFunc("PUT /api/architectures/{id}", s.updateArchitecture)
	mux.HandleFunc("DELETE /api/architectures/{id}", s.deleteArchitecture)

	// Simulation control
	mux.HandleFunc("POST /api/simulate/start/{id}", s.startSimulation)
	mux.HandleFunc("POST /api/simulate/stop", s.stopSimulation)
	mux.HandleFunc("POST /api/simulate/inject", s.injectEvent)

	// WebSocket
	mux.HandleFunc("GET /ws", s.handleWS)
}
