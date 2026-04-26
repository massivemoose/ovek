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
	assertTableExists(t, db, "admin_users")
	assertTableExists(t, db, "api_keys")
	assertTableExists(t, db, "reauth_tokens")
	assertTableExists(t, db, "audit_logs")
	assertTableExists(t, db, "project_config_revisions")
	assertTableExists(t, db, "project_config_entries")
	assertMigrationRecorded(t, db, 1)
	assertMigrationRecorded(t, db, 2)
	assertMigrationRecorded(t, db, 3)
	assertColumnExists(t, db, "jobs", "config_revision_id")
	assertColumnExists(t, db, "deployments", "config_revision_id")
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
	assertMigrationRecorded(t, db, 2)
	assertMigrationRecorded(t, db, 3)
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

func assertColumnExists(t *testing.T, db *sql.DB, tableName string, columnName string) {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		t.Fatalf("expected table info for %q to succeed, got error: %v", tableName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("expected column scan to succeed, got error: %v", err)
		}
		if name == columnName {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("expected column iteration to succeed, got error: %v", err)
	}

	t.Fatalf("expected table %q to have column %q", tableName, columnName)
}
