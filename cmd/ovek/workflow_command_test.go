package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/config"
)

func TestWorkflowSetAcceptsFlagsAfterPositionals(t *testing.T) {
	var gotRequest brainapi.UpsertWorkflowRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/projects/workflow-demo/workflows/digest" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("expected workflow request to decode, got error: %v", err)
		}
		_ = json.NewEncoder(w).Encode(brainapi.Workflow{
			ProjectName:    "workflow-demo",
			Name:           "digest",
			SourceImageRef: gotRequest.ImageRef,
			Schedule:       gotRequest.Schedule,
			Enabled:        true,
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{
		"workflow", "set", "workflow-demo", "digest",
		"--image", "ghcr.io/example/digest:latest",
		"--schedule", "@hourly",
	}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.ImageRef != "ghcr.io/example/digest:latest" {
		t.Fatalf("expected image ref, got %#v", gotRequest)
	}
	if gotRequest.Schedule != "@hourly" {
		t.Fatalf("expected schedule, got %#v", gotRequest)
	}
}

func TestWorkflowSetStillAcceptsFlagsBeforePositionals(t *testing.T) {
	var gotRequest brainapi.UpsertWorkflowRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/projects/workflow-demo/workflows/digest" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("expected workflow request to decode, got error: %v", err)
		}
		_ = json.NewEncoder(w).Encode(brainapi.Workflow{
			ProjectName:    "workflow-demo",
			Name:           "digest",
			SourceImageRef: gotRequest.ImageRef,
			Enabled:        true,
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{
		"workflow", "set",
		"--image", "ghcr.io/example/digest:latest",
		"workflow-demo", "digest",
	}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.ImageRef != "ghcr.io/example/digest:latest" {
		t.Fatalf("expected image ref, got %#v", gotRequest)
	}
}

func TestWorkflowSetRejectsUnknownFlagAfterPositionals(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: "http://127.0.0.1", APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{
		"workflow", "set", "workflow-demo", "digest",
		"--image", "ghcr.io/example/digest:latest",
		"--wat",
	}, &stdout, &stderr, store)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), `unknown workflow set flag "--wat"`) {
		t.Fatalf("expected unknown flag error, got %q", stderr.String())
	}
}
