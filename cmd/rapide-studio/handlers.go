package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	gorapide "github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/studio"
)

// listArchitectures returns a JSON array of {id, name} objects.
func (s *Session) listArchitectures(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := make([]item, 0, len(s.schemas))
	for id, schema := range s.schemas {
		list = append(list, item{ID: id, Name: schema.Name})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// createArchitecture validates the incoming schema, assigns an ID, and stores it.
func (s *Session) createArchitecture(w http.ResponseWriter, r *http.Request) {
	var schema studio.ArchitectureSchema
	if err := json.NewDecoder(r.Body).Decode(&schema); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err := schema.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("validation error: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	id := fmt.Sprintf("%d", s.nextID)
	s.nextID++
	s.schemas[id] = &schema
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// getArchitecture returns the full schema for a single architecture.
func (s *Session) getArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.RLock()
	schema, ok := s.schemas[id]
	s.mu.RUnlock()

	if !ok {
		http.Error(w, "architecture not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schema)
}

// updateArchitecture replaces the schema for an existing architecture.
func (s *Session) updateArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var schema studio.ArchitectureSchema
	if err := json.NewDecoder(r.Body).Decode(&schema); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err := schema.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("validation error: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if _, ok := s.schemas[id]; !ok {
		s.mu.Unlock()
		http.Error(w, "architecture not found", http.StatusNotFound)
		return
	}
	s.schemas[id] = &schema
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// deleteArchitecture removes a stored architecture schema.
func (s *Session) deleteArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	if _, ok := s.schemas[id]; !ok {
		s.mu.Unlock()
		http.Error(w, "architecture not found", http.StatusNotFound)
		return
	}
	delete(s.schemas, id)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// startSimulation stops any running simulation, reconstructs the architecture
// from the given schema, and starts it with an event observer that broadcasts
// events over WebSocket.
func (s *Session) startSimulation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.RLock()
	schema, ok := s.schemas[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "architecture not found", http.StatusNotFound)
		return
	}

	// Stop any running simulation first.
	s.doStopSimulation()

	rec := studio.NewRecorder()
	hub := s.hub

	observerFn := func(e *gorapide.Event) {
		rec.Observer()(e) // record the event
		data, _ := json.Marshal(e)
		msg, _ := json.Marshal(WSMessage{Type: "event", Data: data})
		hub.broadcast <- msg
	}

	a, err := studio.ReconstructWithObserver(schema, observerFn)
	if err != nil {
		http.Error(w, fmt.Sprintf("reconstruction error: %v", err), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		cancel()
		http.Error(w, fmt.Sprintf("start error: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.architecture = a
	s.recorder = rec
	s.simCancel = cancel
	s.mu.Unlock()

	log.Printf("simulation started for architecture %q (id=%s)", schema.Name, id)

	// Broadcast sim_started message.
	startMsg, _ := json.Marshal(WSMessage{
		Type: "sim_started",
		Data: json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q}`, id, schema.Name)),
	})
	hub.broadcast <- startMsg

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// stopSimulation stops the currently running simulation.
func (s *Session) stopSimulation(w http.ResponseWriter, r *http.Request) {
	s.doStopSimulation()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// doStopSimulation is the internal helper that stops a running simulation.
func (s *Session) doStopSimulation() {
	s.mu.Lock()
	a := s.architecture
	cancel := s.simCancel
	s.architecture = nil
	s.recorder = nil
	s.simCancel = nil
	s.mu.Unlock()

	if a != nil {
		a.Stop()
		a.Wait()
		if cancel != nil {
			cancel()
		}
		log.Printf("simulation stopped")

		// Broadcast sim_stopped message.
		stopMsg, _ := json.Marshal(WSMessage{
			Type: "sim_stopped",
			Data: json.RawMessage(`{}`),
		})
		s.hub.broadcast <- stopMsg
	}
}

// injectEvent injects an event into the running simulation.
func (s *Session) injectEvent(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	a := s.architecture
	s.mu.RUnlock()

	if a == nil {
		http.Error(w, "no simulation running", http.StatusConflict)
		return
	}

	e := a.Inject(payload.Name, payload.Params)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "injected",
		"event_id": string(e.ID),
	})
}
