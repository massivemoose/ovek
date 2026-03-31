package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenBrainDBCreatesDatabaseAndSchema(t *testing.T) {
	dataDir := t.TempDir()

	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	databasePath := filepath.Join(dataDir, databaseFileName)
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("expected database file to exist, got error: %v", err)
	}

	assertTableExists(t, db, "schema_migrations")
	assertTableExists(t, db, "projects")
	assertTableExists(t, db, "deployments")
	assertTableExists(t, db, "jobs")
	assertMigrationRecorded(t, db, 1)
}

func TestOpenBrainDBIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()

	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected first database open to succeed, got error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected first database close to succeed, got error: %v", err)
	}

	db, err = openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected second database open to succeed, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	assertMigrationRecorded(t, db, 1)
}

func assertTableExists(t *testing.T, db *sql.DB, tableName string) {
	t.Helper()

	var count int
	err := db.QueryRow(
		"SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?",
		tableName,
	).Scan(&count)
	if err != nil {
		t.Fatalf("expected table lookup for %q to succeed, got error: %v", tableName, err)
	}

	if count != 1 {
		t.Fatalf("expected table %q to exist, got count %d", tableName, count)
	}
}

func assertMigrationRecorded(t *testing.T, db *sql.DB, version int) {
	t.Helper()

	var count int
	err := db.QueryRow(
		"SELECT COUNT(1) FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&count)
	if err != nil {
		t.Fatalf("expected migration lookup for %d to succeed, got error: %v", version, err)
	}

	if count != 1 {
		t.Fatalf("expected migration %d to be recorded once, got count %d", version, count)
	}
}
