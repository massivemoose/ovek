package main

import (
	"errors"
	"log"
	"net/http"
)

const listenAddr = ":8081"

func main() {
	server := &http.Server{
		Addr:    listenAddr,
		Handler: newHandler(),
	}

	log.Printf("brain listening on %s", listenAddr)

	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("brain server failed: %v", err)
	}
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}
