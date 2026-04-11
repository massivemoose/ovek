package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzReturnsOK(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})
	request := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestPingRequiresAPIKey(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
}

func TestPingRejectsWrongAPIKey(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	request.Header.Set("X-API-Key", "wrong-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
}

func TestPingReturnsOKWithCorrectAPIKey(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	if recorder.Body.String() != "pong" {
		t.Fatalf("expected body %q, got %q", "pong", recorder.Body.String())
	}
}
