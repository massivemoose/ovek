package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/config"
)

func TestRegistryLoginUsesPasswordStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/registry/credentials/ghcr.io" {
			http.NotFound(w, r)
			return
		}
		if got := r.Method; got != http.MethodPut {
			t.Fatalf("expected method %q, got %q", http.MethodPut, got)
		}

		var request brainapi.UpsertRegistryCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected request to decode, got error: %v", err)
		}
		if request.Username != "octo" || request.Password != "registry-token" {
			t.Fatalf("expected registry login payload, got %#v", request)
		}

		_ = json.NewEncoder(w).Encode(brainapi.RegistryCredential{
			Host:      "ghcr.io",
			Username:  "octo",
			UpdatedAt: "2026-05-09T00:00:00Z",
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithStoreAndInput(
		context.Background(),
		[]string{"registry", "login", "ghcr.io", "--username", "octo", "--password-stdin"},
		"registry-token\n",
		&stdout,
		&stderr,
		store,
	)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Saved registry credentials for ghcr.io") {
		t.Fatalf("expected success output, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "registry-token") || strings.Contains(stderr.String(), "registry-token") {
		t.Fatal("expected registry password not to be printed")
	}
}

func TestRegistryLoginRetriesAfterReauthRequired(t *testing.T) {
	upsertCalls := 0
	reauthCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/registry/credentials/ghcr.io":
			upsertCalls++
			if upsertCalls == 1 {
				if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "" {
					t.Fatalf("expected first registry login without reauth token, got %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_registry" {
				t.Fatalf("expected retried registry login to include reauth token, got %q", got)
			}
			var request brainapi.UpsertRegistryCredentialRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected request to decode, got error: %v", err)
			}
			if request.Password != "registry-token" {
				t.Fatalf("expected registry password %q, got %q", "registry-token", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.RegistryCredential{Host: "ghcr.io", Username: request.Username})
		case "/v1/auth/reauth":
			reauthCalls++
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "brain-pass" {
				t.Fatalf("expected Brain password %q, got %q", "brain-pass", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_registry"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithStoreAndInput(
		context.Background(),
		[]string{"registry", "login", "ghcr.io", "--username", "octo"},
		"registry-token\nbrain-pass\n",
		&stdout,
		&stderr,
		store,
	)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if upsertCalls != 2 {
		t.Fatalf("expected two registry login attempts, got %d", upsertCalls)
	}
	if reauthCalls != 1 {
		t.Fatalf("expected one reauth request, got %d", reauthCalls)
	}
	if strings.Contains(stdout.String(), "registry-token") || strings.Contains(stderr.String(), "registry-token") {
		t.Fatal("expected registry password not to be printed")
	}
}

func TestRegistryListRendersCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/registry/credentials" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]brainapi.RegistryCredential{{
			Host:      "ghcr.io",
			Username:  "octo",
			UpdatedAt: "2026-05-09T00:00:00Z",
		}})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithStore(context.Background(), []string{"registry", "list"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ghcr.io") || !strings.Contains(stdout.String(), "octo") {
		t.Fatalf("expected registry list output, got %q", stdout.String())
	}
}

func TestRegistryRemoveRetriesAfterReauthRequired(t *testing.T) {
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/registry/credentials/ghcr.io":
			deleteCalls++
			if got := r.Method; got != http.MethodDelete {
				t.Fatalf("expected method %q, got %q", http.MethodDelete, got)
			}
			if deleteCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_registry_rm" {
				t.Fatalf("expected delete retry to include reauth token, got %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/auth/reauth":
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "brain-pass" {
				t.Fatalf("expected Brain password %q, got %q", "brain-pass", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_registry_rm"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithStoreAndInput(context.Background(), []string{"registry", "rm", "ghcr.io"}, "brain-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if deleteCalls != 2 {
		t.Fatalf("expected two delete attempts, got %d", deleteCalls)
	}
	if !strings.Contains(stdout.String(), "Removed registry credentials for ghcr.io") {
		t.Fatalf("expected remove output, got %q", stdout.String())
	}
}
