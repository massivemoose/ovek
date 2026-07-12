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

func TestWorkflowTokenCreatePrintsOneTimeToken(t *testing.T) {
	var gotRequest brainapi.CreateWorkflowTriggerTokenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/workflow-demo/workflows/digest/tokens" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("expected token request to decode, got error: %v", err)
		}
		_ = json.NewEncoder(w).Encode(brainapi.CreateWorkflowTriggerTokenResponse{
			ID:           "tok_123",
			ProjectName:  "workflow-demo",
			WorkflowName: "digest",
			Label:        gotRequest.Label,
			Token:        "wft_tok_123_secret",
			CreatedAt:    "2026-05-30T00:00:00Z",
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
		"workflow", "token", "create", "workflow-demo", "digest", "--label", "app",
	}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.Label != "app" {
		t.Fatalf("expected label request, got %#v", gotRequest)
	}
	output := stdout.String()
	if !strings.Contains(output, "wft_tok_123_secret") || !strings.Contains(output, "shown once") {
		t.Fatalf("expected one-time token output, got %q", output)
	}
}

func TestWorkflowTokenListDoesNotPrintSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/projects/workflow-demo/workflows/digest/tokens" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]brainapi.WorkflowTriggerTokenSummary{{
			ID:           "tok_123",
			ProjectName:  "workflow-demo",
			WorkflowName: "digest",
			Label:        "app",
			CreatedAt:    "2026-05-30T00:00:00Z",
		}})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"workflow", "token", "list", "workflow-demo", "digest"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "tok_123") || !strings.Contains(output, "app") {
		t.Fatalf("expected token metadata output, got %q", output)
	}
	if strings.Contains(output, "wft_") || strings.Contains(output, "secret") {
		t.Fatalf("expected token list not to print secrets, got %q", output)
	}
}

func TestWorkflowTokenRemoveSendsDelete(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/projects/workflow-demo/workflows/digest/tokens/tok_123" {
			http.NotFound(w, r)
			return
		}
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"workflow", "token", "rm", "workflow-demo", "digest", "tok_123"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !deleted {
		t.Fatal("expected delete request")
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Fatalf("expected remove success output, got %q", stdout.String())
	}
}
