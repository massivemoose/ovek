package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/config"
)

func runWithStore(ctx context.Context, args []string, stdout, stderr io.Writer, store *config.Store) int {
	return runApp(ctx, args, bytes.NewBuffer(nil), stdout, stderr, store)
}

func runWithStoreAndInput(ctx context.Context, args []string, input string, stdout, stderr io.Writer, store *config.Store) int {
	return runApp(ctx, args, strings.NewReader(input), stdout, stderr, store)
}

func newTestBrainServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ping" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(brainapi.APIError{
				Code:    "unauthorized",
				Message: "unauthorized",
			})
			return
		}
		_, _ = w.Write([]byte("pong"))
	}))
}
