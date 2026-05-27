package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/cli/config"
)

func TestLogsReadsRuntimeSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runtime/logs":
			_, _ = fmt.Fprint(w, "runtime line\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"logs", "demo-app", "--no-follow"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "runtime line\n" {
		t.Fatalf("expected runtime log output %q, got %q", "runtime line\n", got)
	}
}

func TestLogsStreamsJobLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/jobs/job_123/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: first\n\ndata: second\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"logs", "--job", "job_123"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	if got := stdout.String(); got != "first\nsecond\n" {
		t.Fatalf("expected streamed log output %q, got %q", "first\nsecond\n", got)
	}
}

func TestLogsAcceptsEqualsJobFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/jobs/job_123/logs":
			_, _ = fmt.Fprint(w, "job line\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"logs", "--job=job_123", "--no-follow"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "job line\n" {
		t.Fatalf("expected job log output %q, got %q", "job line\n", got)
	}
}
