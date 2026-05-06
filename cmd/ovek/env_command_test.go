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

func TestEnvSetSendsEnvironmentMutationAndPrintsApplyHint(t *testing.T) {
	var gotRequest brainapi.SetProjectEnvironmentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/env/PUBLIC_SITE_URL" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotRequest)
		_ = json.NewEncoder(w).Encode(brainapi.ProjectEnvironmentMutation{RevisionID: "rev_123"})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"env", "set", "demo-app", "PUBLIC_SITE_URL=https://example.com"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.Value != "https://example.com" || gotRequest.Secret {
		t.Fatalf("expected plain env request, got %#v", gotRequest)
	}
	output := stdout.String()
	for _, fragment := range []string{
		"Environment updated. Run 'ovek run <project> <capsule-ref>' to apply changes.",
		"Revision  rev_123",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestEnvListMasksSecrets(t *testing.T) {
	plainValue := "https://example.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/env" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]brainapi.ProjectEnvironmentEntry{
			{Name: "PB_SUPERUSER_PASSWORD", Secret: true, UpdatedAt: "2026-04-26T00:00:00Z"},
			{Name: "PUBLIC_SITE_URL", Secret: false, Value: &plainValue, UpdatedAt: "2026-04-26T00:01:00Z"},
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"env", "list", "demo-app"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "PB_SUPERUSER_PASSWORD") || !strings.Contains(output, "****") {
		t.Fatalf("expected masked secret in output, got %q", output)
	}
	if !strings.Contains(output, "PUBLIC_SITE_URL") || !strings.Contains(output, "https://example.com") {
		t.Fatalf("expected plain env in output, got %q", output)
	}
}

func TestSecretSetPromptsAndDoesNotPrintSecretValue(t *testing.T) {
	var gotRequest brainapi.SetProjectEnvironmentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/env/PB_SUPERUSER_PASSWORD" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotRequest)
		_ = json.NewEncoder(w).Encode(brainapi.ProjectEnvironmentMutation{RevisionID: "rev_secret"})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"secret", "set", "demo-app", "PB_SUPERUSER_PASSWORD"}, "secret-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.Value != "secret-pass" || !gotRequest.Secret {
		t.Fatalf("expected secret request, got %#v", gotRequest)
	}
	if strings.Contains(stdout.String(), "secret-pass") || strings.Contains(stderr.String(), "secret-pass") {
		t.Fatal("expected secret value not to be printed")
	}
}

func TestEnvSetRetriesAfterProdReauthRequired(t *testing.T) {
	mutationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/env/PUBLIC_SITE_URL":
			mutationCalls++
			if r.Header.Get("X-Ovek-Reauth-Token") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			_ = json.NewEncoder(w).Encode(brainapi.ProjectEnvironmentMutation{RevisionID: "rev_456"})
		case "/v1/auth/reauth":
			var request brainapi.ReauthRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Password != "secret-pass" {
				t.Fatalf("expected reauth password %q, got %q", "secret-pass", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_123", ExpiresAt: "2026-04-26T00:00:00Z"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("prod", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"env", "set", "demo-app", "PUBLIC_SITE_URL=https://example.com"}, "secret-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if mutationCalls != 2 {
		t.Fatalf("expected mutation to be attempted twice, got %d", mutationCalls)
	}
}
