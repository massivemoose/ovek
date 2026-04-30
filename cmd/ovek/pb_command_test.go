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

func TestPocketBaseInitSendsRequestAndPrintsStatus(t *testing.T) {
	var gotRequest brainapi.InitProjectPocketBaseRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/pocketbase/init" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("expected API key header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("expected init request decode to succeed, got error: %v", err)
		}
		_ = json.NewEncoder(w).Encode(brainapi.ProjectPocketBaseStatus{
			ProjectName:          "demo-app",
			ContainerName:        "ovek-demo-app-pb",
			Running:              true,
			Initialized:          true,
			SuperuserEmail:       stringPointer("admin@example.com"),
			AppSecretsConfigured: true,
			AppSecretsRevisionID: "rev_pb",
			UpdatedAt:            "2026-04-26T00:00:00Z",
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"pb", "init", "demo-app", "--email", "admin@example.com", "--app-secrets"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if gotRequest.Email != "admin@example.com" || !gotRequest.AppSecrets {
		t.Fatalf("expected init request with email and app secrets, got %#v", gotRequest)
	}
	output := stdout.String()
	for _, fragment := range []string{
		"PocketBase initialized.",
		"PocketBase",
		"demo-app",
		"ovek-demo-app-pb",
		"admin@example.com",
		"Revision",
		"rev_pb",
		"Environment updated. Run 'ovek deploy <project> <repoURL>' to apply changes.",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestPocketBaseStatusPrintsStatusWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/pocketbase" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(brainapi.ProjectPocketBaseStatus{
			ProjectName:          "demo-app",
			ContainerName:        "ovek-demo-app-pb",
			Running:              true,
			Initialized:          true,
			SuperuserEmail:       stringPointer("admin@demo-app.ovek.local"),
			AppSecretsConfigured: false,
			UpdatedAt:            "2026-04-26T00:00:00Z",
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"pb", "status", "demo-app"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, fragment := range []string{"PocketBase", "admin@demo-app.ovek.local", "App Secrets", "no"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
	if strings.Contains(output, "password") || strings.Contains(stderr.String(), "password") {
		t.Fatalf("expected status output not to mention passwords, stdout=%q stderr=%q", output, stderr.String())
	}
}

func TestPocketBaseInitRetriesAfterProdReauthRequired(t *testing.T) {
	initCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/pocketbase/init":
			initCalls++
			if initCalls == 1 {
				if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "" {
					t.Fatalf("expected first init without reauth token, got %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_pb" {
				t.Fatalf("expected retried init to include reauth token, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ProjectPocketBaseStatus{
				ProjectName:    "demo-app",
				ContainerName:  "ovek-demo-app-pb",
				Running:        true,
				Initialized:    true,
				SuperuserEmail: stringPointer("admin@demo-app.ovek.local"),
			})
		case "/v1/auth/reauth":
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "secret-pass" {
				t.Fatalf("expected reauth password, got %q", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_pb"})
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
	exitCode := runWithStoreAndInput(context.Background(), []string{"pb", "init", "demo-app"}, "secret-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if initCalls != 2 {
		t.Fatalf("expected init to be attempted twice, got %d", initCalls)
	}
	if strings.Contains(stdout.String(), "secret-pass") || strings.Contains(stderr.String(), "secret-pass") {
		t.Fatal("expected reauth password not to be printed")
	}
}

func TestPocketBaseTunnelRejectsNonLoopbackListenAddress(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: "http://127.0.0.1:1", APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"pb", "tunnel", "demo-app", "--listen", "0.0.0.0:8090"}, &stdout, &stderr, store)
	if exitCode == 0 {
		t.Fatalf("expected tunnel validation to fail, got stdout %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "loopback host:port") {
		t.Fatalf("expected loopback validation error, got %q", stderr.String())
	}
}

func stringPointer(value string) *string {
	return &value
}
