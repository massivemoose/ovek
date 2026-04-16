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
		Host:   "http://brain.localhost/",
		APIKey: "test-key",
	})
	if err != nil {
		t.Fatalf("expected save to succeed, got error: %v", err)
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected load to succeed, got error: %v", err)
	}
	if cfg.Host != DefaultHost {
		t.Fatalf("expected normalized host %q, got %q", DefaultHost, cfg.Host)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("expected API key %q, got %q", "test-key", cfg.APIKey)
	}
}

func TestStoreSaveSetsSecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not portable on Windows")
	}

	rootDir := t.TempDir()
	store := NewStore(rootDir)
	if err := store.Save(Config{APIKey: "test-key"}); err != nil {
		t.Fatalf("expected save to succeed, got error: %v", err)
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

func TestStoreClearRemovesConfig(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Config{APIKey: "test-key"}); err != nil {
		t.Fatalf("expected save to succeed, got error: %v", err)
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("expected clear to succeed, got error: %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after clear, got %v", err)
	}
}
