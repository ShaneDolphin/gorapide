package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8400", "HTTP listen address")
	flag.Parse()

	hub := newHub()
	go hub.run()

	session := newSession(hub)

	mux := http.NewServeMux()
	session.registerRoutes(mux)

	// Serve static files from the static/ directory.
	mux.Handle("GET /", http.FileServer(http.Dir("static")))

	log.Printf("rapide-studio listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
