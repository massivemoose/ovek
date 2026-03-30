package main

import (
	"errors"
	"log"
	"net/http"
)

const listenAddr = ":8081"

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: newHandler(cfg),
	}

	log.Printf("brain listening on %s", listenAddr)

	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("brain server failed: %v", err)
	}
}

func newHandler(cfg config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	mux.Handle("/v1/", apiKeyMiddleware(cfg.BrainAPIKey, apiMux))

	return mux
}
