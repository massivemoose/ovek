package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/config"
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

func TestAuthKeyCreatePrintsOneTimeKeyAndRetriesAfterReauth(t *testing.T) {
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/api-keys":
			createCalls++
			if createCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_key" {
				t.Fatalf("expected create retry to include reauth token, got %q", got)
			}
			var request brainapi.CreateAPIKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected create request to decode, got error: %v", err)
			}
			if request.Label != "ci" {
				t.Fatalf("expected label %q, got %q", "ci", request.Label)
			}
			_ = json.NewEncoder(w).Encode(brainapi.CreateAPIKeyResponse{
				ID:     "key_ci",
				Label:  "ci",
				APIKey: "ak_key_ci_secret",
			})
		case "/v1/auth/reauth":
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "brain-pass" {
				t.Fatalf("expected reauth password %q, got %q", "brain-pass", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_key"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"auth", "key", "create", "--label", "ci"}, "brain-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if createCalls != 2 {
		t.Fatalf("expected two create attempts, got %d", createCalls)
	}
	if !strings.Contains(stdout.String(), "ak_key_ci_secret") {
		t.Fatalf("expected one-time API key in output, got %q", stdout.String())
	}
}

func TestAuthKeysListsMetadataWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/api-keys" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]brainapi.APIKeySummary{{
			ID:        "key_bootstrap",
			Label:     "bootstrap",
			CreatedAt: "2026-05-09T00:00:00Z",
		}})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "keys"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "key_bootstrap") || !strings.Contains(stdout.String(), "bootstrap") {
		t.Fatalf("expected API key metadata output, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "ak_") {
		t.Fatalf("expected auth keys not to print key secrets, got %q", stdout.String())
	}
}

func TestAuthKeyRemoveRetriesAfterReauthRequired(t *testing.T) {
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/api-keys/key_123":
			deleteCalls++
			if deleteCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_key_rm" {
				t.Fatalf("expected revoke retry to include reauth token, got %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/auth/reauth":
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_key_rm"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"auth", "key", "rm", "key_123"}, "brain-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if deleteCalls != 2 {
		t.Fatalf("expected two delete calls, got %d", deleteCalls)
	}
	if !strings.Contains(stdout.String(), "Revoked API key key_123") {
		t.Fatalf("expected revoke output, got %q", stdout.String())
	}
}

func TestAuthPasswordUsesCurrentPasswordForReauth(t *testing.T) {
	changeCalls := 0
	reauthCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/password":
			changeCalls++
			if changeCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{Code: "reauth_required", Message: "reauth required"})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt_password" {
				t.Fatalf("expected password retry to include reauth token, got %q", got)
			}
			var request brainapi.ChangePasswordRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected password request to decode, got error: %v", err)
			}
			if request.CurrentPassword != "old-pass" || request.NewPassword != "new-pass" {
				t.Fatalf("expected password change request, got %#v", request)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/auth/reauth":
			reauthCalls++
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "old-pass" {
				t.Fatalf("expected current password to be reused for reauth, got %q", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt_password"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"auth", "password"}, "old-pass\nnew-pass\nnew-pass\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if changeCalls != 2 || reauthCalls != 1 {
		t.Fatalf("expected password change retry with one reauth, got change=%d reauth=%d", changeCalls, reauthCalls)
	}
	if !strings.Contains(stdout.String(), "Password changed.") {
		t.Fatalf("expected password change output, got %q", stdout.String())
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
