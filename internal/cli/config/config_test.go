package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreLoadMissingReturnsNotFound(t *testing.T) {
	store := NewStore(t.TempDir())

	_, err := store.Load()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreSaveAndLoadRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())

	err := store.Save(Config{
		ActiveProfile: "prod",
		Profiles: map[string]Profile{
			"prod": {
				Host:   "https://brain.example.com/",
				APIKey: "test-key",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected save to succeed, got error: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected load to succeed, got error: %v", err)
	}
	if cfg.ActiveProfile != "prod" {
		t.Fatalf("expected active profile %q, got %q", "prod", cfg.ActiveProfile)
	}
	if cfg.Profiles["prod"].Host != "https://brain.example.com" {
		t.Fatalf("expected normalized host %q, got %q", "https://brain.example.com", cfg.Profiles["prod"].Host)
	}
	if cfg.Profiles["prod"].APIKey != "test-key" {
		t.Fatalf("expected api key %q, got %q", "test-key", cfg.Profiles["prod"].APIKey)
	}
}

func TestStoreMigratesLegacySingleProfileConfig(t *testing.T) {
	rootDir := t.TempDir()
	store := NewStore(rootDir)
	configPath, err := store.Path()
	if err != nil {
		t.Fatalf("expected config path, got error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("expected config dir create to succeed, got error: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{\"host\":\"http://brain.localhost\",\"apiKey\":\"legacy-key\"}\n"), 0o600); err != nil {
		t.Fatalf("expected legacy config write to succeed, got error: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected legacy config load to succeed, got error: %v", err)
	}
	if cfg.ActiveProfile != DefaultProfile {
		t.Fatalf("expected active profile %q, got %q", DefaultProfile, cfg.ActiveProfile)
	}
	if cfg.Profiles[DefaultProfile].APIKey != "legacy-key" {
		t.Fatalf("expected migrated API key %q, got %q", "legacy-key", cfg.Profiles[DefaultProfile].APIKey)
	}
}

func TestStoreSaveProfileSetsActiveProfile(t *testing.T) {
	store := NewStore(t.TempDir())

	if err := store.SaveProfile("prod", Profile{Host: "https://brain.example.com", APIKey: "prod-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	name, profile, err := store.LoadProfile("")
	if err != nil {
		t.Fatalf("expected active profile load to succeed, got error: %v", err)
	}
	if name != "prod" {
		t.Fatalf("expected active profile name %q, got %q", "prod", name)
	}
	if profile.APIKey != "prod-key" {
		t.Fatalf("expected profile api key %q, got %q", "prod-key", profile.APIKey)
	}
}

func TestStoreSetActiveProfileSwitchesProfiles(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Config{
		ActiveProfile: "dev",
		Profiles: map[string]Profile{
			"dev":  {Host: DefaultHost, APIKey: "dev-key"},
			"prod": {Host: "https://brain.example.com", APIKey: "prod-key"},
		},
	}); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	if err := store.SetActiveProfile("prod"); err != nil {
		t.Fatalf("expected set active profile to succeed, got error: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected config load to succeed, got error: %v", err)
	}
	if cfg.ActiveProfile != "prod" {
		t.Fatalf("expected active profile %q, got %q", "prod", cfg.ActiveProfile)
	}
}

func TestStoreRemoveProfileRemovesConfigWhenLastProfileDeleted(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.SaveProfile("dev", Profile{APIKey: "dev-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	if err := store.RemoveProfile("dev"); err != nil {
		t.Fatalf("expected remove profile to succeed, got error: %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after removing last profile, got %v", err)
	}
}

func TestStoreSaveSetsSecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not portable on Windows")
	}

	rootDir := t.TempDir()
	store := NewStore(rootDir)
	if err := store.SaveProfile("dev", Profile{APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected profile save to succeed, got error: %v", err)
	}

	configPath, err := store.Path()
	if err != nil {
		t.Fatalf("expected config path, got error: %v", err)
	}

	dirInfo, err := os.Stat(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("expected config dir stat to succeed, got error: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != dirPermissions {
		t.Fatalf("expected dir permissions %o, got %o", dirPermissions, got)
	}

	fileInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected config file stat to succeed, got error: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != filePermissions {
		t.Fatalf("expected file permissions %o, got %o", filePermissions, got)
	}
}
