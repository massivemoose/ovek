package main

import "testing"

func TestLoadConfigRequiresAPIKey(t *testing.T) {
	t.Setenv("BRAIN_API_KEY", "")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected an error when BRAIN_API_KEY is missing")
	}
}

func TestLoadConfigReadsAPIKey(t *testing.T) {
	t.Setenv("BRAIN_API_KEY", "test-key")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.BrainAPIKey != "test-key" {
		t.Fatalf("expected API key %q, got %q", "test-key", cfg.BrainAPIKey)
	}

	if cfg.DataDir != defaultDataDir {
		t.Fatalf("expected data dir %q, got %q", defaultDataDir, cfg.DataDir)
	}
	if cfg.BuildKitHost != defaultBuildKitHost {
		t.Fatalf("expected BuildKit host %q, got %q", defaultBuildKitHost, cfg.BuildKitHost)
	}
}

func TestLoadConfigReadsBuildKitHostOverride(t *testing.T) {
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("BUILDKIT_HOST", "tcp://custom-buildkit:2345")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.BuildKitHost != "tcp://custom-buildkit:2345" {
		t.Fatalf("expected BuildKit host %q, got %q", "tcp://custom-buildkit:2345", cfg.BuildKitHost)
	}
}
