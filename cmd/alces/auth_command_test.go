package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/config"
)

func TestAuthBootstrapSavesReturnedAPIKeyToRequestedProfile(t *testing.T) {
	server := newBootstrapTestBrainServer(t)
	store := config.NewStore(t.TempDir())

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(
		context.Background(),
		[]string{"auth", "bootstrap", "--profile", "prod", "--host", server.URL},
		"admin\nsecret-pass\nsecret-pass\n",
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	profileName, profile, err := store.LoadProfile("")
	if err != nil {
		t.Fatalf("expected active profile to load, got error: %v", err)
	}
	if profileName != "prod" {
		t.Fatalf("expected active profile %q, got %q", "prod", profileName)
	}
	if profile.Host != server.URL || profile.APIKey != "bootstrap-key" {
		t.Fatalf("expected saved bootstrap profile, got %#v", profile)
	}
	if strings.Contains(stdout.String(), "bootstrap-key") {
		t.Fatalf("expected bootstrap output to omit API key, got %q", stdout.String())
	}
}

func TestAuthLoginSavesVerifiedProfile(t *testing.T) {
	server := newTestBrainServer(t)
	store := config.NewStore(t.TempDir())

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(
		context.Background(),
		[]string{"auth", "login", "--profile", "dev", "--host", server.URL, "--api-key", "test-key"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	profileName, profile, err := store.LoadProfile("")
	if err != nil {
		t.Fatalf("expected active profile to load, got error: %v", err)
	}
	if profileName != "dev" {
		t.Fatalf("expected active profile %q, got %q", "dev", profileName)
	}
	if profile.Host != server.URL || profile.APIKey != "test-key" {
		t.Fatalf("expected saved login profile, got %#v", profile)
	}
}

func TestAuthStatusReportsConfiguredProfileWithoutAPIKey(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("prod", config.Profile{Host: "https://brain.example.com", APIKey: "secret-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "status"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{"Authenticated", "prod", "https://brain.example.com"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
	if strings.Contains(output, "secret-key") {
		t.Fatalf("expected status output to omit API key, got %q", output)
	}
}

func TestAuthUseSwitchesActiveProfile(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.Save(config.Config{
		ActiveProfile: "dev",
		Profiles: map[string]config.Profile{
			"dev":  {Host: config.DefaultHost, APIKey: "dev-key"},
			"prod": {Host: "https://brain.example.com", APIKey: "prod-key"},
		},
	}); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "use", "prod"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected config load to succeed, got error: %v", err)
	}
	if cfg.ActiveProfile != "prod" {
		t.Fatalf("expected active profile %q, got %q", "prod", cfg.ActiveProfile)
	}
}

func TestAuthLogoutRemovesProfile(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("prod", config.Profile{Host: "https://brain.example.com", APIKey: "secret-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "logout", "--profile", "prod"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	_, err := store.Load()
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("expected all profiles to be removed, got %v", err)
	}
}

func TestAuthProfilesListsConfiguredProfiles(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.Save(config.Config{
		ActiveProfile: "prod",
		Profiles: map[string]config.Profile{
			"dev":  {Host: config.DefaultHost, APIKey: "dev-key"},
			"prod": {Host: "https://brain.example.com", APIKey: "prod-key"},
		},
	}); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "profiles"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{"Profiles", "dev", "prod", "https://brain.example.com"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func newBootstrapTestBrainServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/bootstrap" {
			http.NotFound(w, r)
			return
		}

		var request brainapi.BootstrapAuthRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected bootstrap request to decode, got error: %v", err)
		}
		if request.Username != "admin" || request.Password != "secret-pass" {
			t.Fatalf("expected bootstrap request values, got %#v", request)
		}

		_ = json.NewEncoder(w).Encode(brainapi.BootstrapAuthResponse{
			Username: "admin",
			APIKey:   "bootstrap-key",
		})
	}))
}
