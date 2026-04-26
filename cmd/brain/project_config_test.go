package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestProjectConfigStoreCreatesRevisionsMasksSecretsAndResolvesRuntimeEnv(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)

	envMutation, err := store.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "dev")
	if err != nil {
		t.Fatalf("expected env set to succeed, got error: %v", err)
	}
	if envMutation.RevisionID == "" {
		t.Fatal("expected env mutation revision ID")
	}
	secretMutation, err := store.SetEnvironmentEntry(context.Background(), "demo-app", "PB_SUPERUSER_PASSWORD", "secret-pass", true, "dev")
	if err != nil {
		t.Fatalf("expected secret set to succeed, got error: %v", err)
	}
	if secretMutation.RevisionID == "" || secretMutation.RevisionID == envMutation.RevisionID {
		t.Fatalf("expected distinct secret mutation revision ID, got env=%q secret=%q", envMutation.RevisionID, secretMutation.RevisionID)
	}

	entries, err := store.ListEnvironment(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected list to succeed, got error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %#v", entries)
	}
	if entries[0].Name != "PB_SUPERUSER_PASSWORD" || !entries[0].Secret || entries[0].Value != nil {
		t.Fatalf("expected masked secret entry first by name, got %#v", entries[0])
	}
	if entries[1].Name != "PUBLIC_SITE_URL" || entries[1].Secret || entries[1].Value == nil || *entries[1].Value != "https://example.com" {
		t.Fatalf("expected plain env entry, got %#v", entries[1])
	}

	runtimeConfig, err := store.ResolveRuntimeConfig(context.Background(), "demo-app", secretMutation.RevisionID)
	if err != nil {
		t.Fatalf("expected runtime config resolve to succeed, got error: %v", err)
	}
	wantEnv := []string{
		"PB_SUPERUSER_PASSWORD=secret-pass",
		"PUBLIC_SITE_URL=https://example.com",
	}
	if strings.Join(runtimeConfig.Env, "\n") != strings.Join(wantEnv, "\n") {
		t.Fatalf("expected runtime env %#v, got %#v", wantEnv, runtimeConfig.Env)
	}
	if len(runtimeConfig.SecretValues) != 1 || runtimeConfig.SecretValues[0] != "secret-pass" {
		t.Fatalf("expected resolved secret values, got %#v", runtimeConfig.SecretValues)
	}
}

func TestProjectConfigStoreDeleteCreatesNewRevisionWithoutMutatingOldRevision(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)

	envMutation, err := store.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "dev")
	if err != nil {
		t.Fatalf("expected env set to succeed, got error: %v", err)
	}
	deleteMutation, err := store.DeleteEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "dev")
	if err != nil {
		t.Fatalf("expected env delete to succeed, got error: %v", err)
	}
	if deleteMutation.RevisionID == envMutation.RevisionID {
		t.Fatalf("expected delete to create a new revision, got %q", deleteMutation.RevisionID)
	}

	oldConfig, err := store.ResolveRuntimeConfig(context.Background(), "demo-app", envMutation.RevisionID)
	if err != nil {
		t.Fatalf("expected old runtime config resolve to succeed, got error: %v", err)
	}
	if len(oldConfig.Env) != 1 || oldConfig.Env[0] != "PUBLIC_SITE_URL=https://example.com" {
		t.Fatalf("expected old revision to remain immutable, got %#v", oldConfig.Env)
	}
	newConfig, err := store.ResolveRuntimeConfig(context.Background(), "demo-app", deleteMutation.RevisionID)
	if err != nil {
		t.Fatalf("expected new runtime config resolve to succeed, got error: %v", err)
	}
	if len(newConfig.Env) != 0 {
		t.Fatalf("expected deleted entry to be absent from new revision, got %#v", newConfig.Env)
	}
}

func TestProjectConfigStoreValidatesNamesAndValues(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "lowercase", key: "bad_key", value: "value"},
		{name: "reserved port", key: "PORT", value: "8081"},
		{name: "reserved pocketbase", key: "POCKETBASE_URL", value: "http://db:8090"},
		{name: "reserved prefix", key: "OVEK_TOKEN", value: "value"},
		{name: "too long name", key: strings.Repeat("A", maxProjectEnvNameLength+1), value: "value"},
		{name: "too long value", key: "BIG_VALUE", value: strings.Repeat("x", maxProjectEnvValueLength+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.SetEnvironmentEntry(context.Background(), "demo-app", test.key, test.value, false, "dev")
			if err == nil {
				t.Fatal("expected validation to fail")
			}
		})
	}
}

func TestProjectConfigStoreDeleteMissingEntry(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)
	if _, err := store.DeleteEnvironmentEntry(context.Background(), "missing-app", "MISSING", "dev"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing project error, got %v", err)
	}

	if _, err := store.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "dev"); err != nil {
		t.Fatalf("expected env set to succeed, got error: %v", err)
	}
	if _, err := store.DeleteEnvironmentEntry(context.Background(), "demo-app", "MISSING", "dev"); !errors.Is(err, errProjectEnvNotFound) {
		t.Fatalf("expected missing env entry error, got %v", err)
	}
}

func newTestProjectConfigStore(t *testing.T, db *sql.DB) projectConfigStore {
	t.Helper()

	return newProjectConfigStore(db, secretCipher{key: []byte(strings.Repeat("k", secretKeyLength))})
}
