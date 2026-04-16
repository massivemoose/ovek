package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/cli/config"
)

func TestAuthLoginSavesVerifiedConfig(t *testing.T) {
	server := newTestBrainServer(t)
	store := config.NewStore(t.TempDir())

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(
		context.Background(),
		[]string{"auth", "login", "--host", server.URL, "--api-key", "test-key"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}
	if cfg.Host != server.URL {
		t.Fatalf("expected stored host %q, got %q", server.URL, cfg.Host)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("expected stored API key %q, got %q", "test-key", cfg.APIKey)
	}
	if strings.Contains(stdout.String(), "test-key") {
		t.Fatalf("expected login output to redact API key, got %q", stdout.String())
	}
}

func TestAuthStatusReportsConfiguredHostWithoutAPIKey(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.Save(config.Config{Host: "http://brain.localhost", APIKey: "secret-key"}); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "status"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{"Authenticated: yes", "Host: http://brain.localhost"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
	if strings.Contains(output, "secret-key") {
		t.Fatalf("expected status output to omit API key, got %q", output)
	}
}

func TestAuthLogoutRemovesConfig(t *testing.T) {
	store := config.NewStore(t.TempDir())
	if err := store.Save(config.Config{APIKey: "secret-key"}); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"auth", "logout"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	configPath, err := store.Path()
	if err != nil {
		t.Fatalf("expected config path, got error: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected config file to be removed, stat error: %v", err)
	}
}
