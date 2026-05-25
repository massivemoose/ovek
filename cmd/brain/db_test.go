package main

import (
	"database/sql"
	"fmt"
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
	assertTableExists(t, db, "project_pocketbase_credentials")
	assertTableExists(t, db, "workflows")
	assertTableExists(t, db, "workflow_runs")
	assertMigrationRecorded(t, db, 1)
	assertMigrationRecorded(t, db, 2)
	assertMigrationRecorded(t, db, 3)
	assertMigrationRecorded(t, db, 4)
	assertMigrationRecorded(t, db, 5)
	assertMigrationRecorded(t, db, 6)
	assertMigrationRecorded(t, db, 7)
	assertMigrationRecorded(t, db, 8)
	assertColumnExists(t, db, "jobs", "phase")
	assertColumnExists(t, db, "jobs", "config_revision_id")
	assertColumnExists(t, db, "jobs", "source_type")
	assertColumnExists(t, db, "jobs", "source_ref")
	assertColumnExists(t, db, "deployments", "config_revision_id")
	assertColumnExists(t, db, "deployments", "source_type")
	assertColumnExists(t, db, "deployments", "source_ref")
	assertColumnExists(t, db, "workflows", "source_image_ref")
	assertColumnExists(t, db, "workflows", "resolved_repo_digest")
	assertColumnExists(t, db, "workflows", "runtime_image_id")
	assertColumnExists(t, db, "workflows", "schedule")
	assertColumnExists(t, db, "workflows", "queue_cap")
	assertColumnExists(t, db, "workflows", "enabled")
	assertColumnExists(t, db, "workflow_runs", "trigger_type")
	assertColumnExists(t, db, "workflow_runs", "config_revision_id")
	assertColumnExists(t, db, "workflow_runs", "log_path")
	assertColumnExists(t, db, "workflow_runs", "exit_code")
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
	assertMigrationRecorded(t, db, 4)
	assertMigrationRecorded(t, db, 5)
	assertMigrationRecorded(t, db, 6)
	assertMigrationRecorded(t, db, 7)
	assertMigrationRecorded(t, db, 8)
}

func TestSourceMetadataMigrationBackfillsExistingJobsAndDeployments(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("expected data dir create to succeed, got error: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("expected database open to succeed, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("expected foreign keys pragma to succeed, got error: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)
`); err != nil {
		t.Fatalf("expected schema migration table create to succeed, got error: %v", err)
	}
	for _, migration := range migrations {
		if migration.version >= 6 {
			continue
		}
		if _, err := db.Exec(migration.upSQL); err != nil {
			t.Fatalf("expected migration %d setup to succeed, got error: %v", migration.version, err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name) VALUES(?, ?)",
			migration.version,
			migration.name,
		); err != nil {
			t.Fatalf("expected migration %d record setup to succeed, got error: %v", migration.version, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO projects(name, status, created_at) VALUES(?, ?, ?)`,
		"demo-app",
		projectStatusRunning,
		"2026-05-01T00:00:00Z",
	); err != nil {
		t.Fatalf("expected project seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO jobs(id, project_name, repo_url, status, created_at) VALUES(?, ?, ?, ?, ?)`,
		"job-123",
		"demo-app",
		"https://example.com/demo.git",
		jobStatusSucceeded,
		"2026-05-01T00:00:00Z",
	); err != nil {
		t.Fatalf("expected job seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO deployments(id, project_name, image_ref, app_container_name, network_name, pb_container_name, status, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		"job-123",
		"demo-app",
		"localhost:5001/ovek-demo-app:job-123",
		"ovek-demo-app-app-job-123",
		"demo-app-net",
		"ovek-demo-app-pb",
		deploymentStatusSucceeded,
		"2026-05-01T00:00:00Z",
	); err != nil {
		t.Fatalf("expected deployment seed to succeed, got error: %v", err)
	}

	if err := ensureSchema(db); err != nil {
		t.Fatalf("expected schema migration to succeed, got error: %v", err)
	}

	assertMigrationRecorded(t, db, 6)
	assertTextValue(t, db, "jobs", "source_type", "id = 'job-123'", jobSourceTypeRepo)
	assertTextValue(t, db, "jobs", "source_ref", "id = 'job-123'", "https://example.com/demo.git")
	assertTextValue(t, db, "deployments", "source_type", "id = 'job-123'", jobSourceTypeRepo)
	assertTextValue(t, db, "deployments", "source_ref", "id = 'job-123'", "https://example.com/demo.git")
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

func assertTextValue(t *testing.T, db *sql.DB, tableName string, columnName string, whereClause string, want string) {
	t.Helper()

	var got string
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", columnName, tableName, whereClause)
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("expected query %q to succeed, got error: %v", query, err)
	}
	if got != want {
		t.Fatalf("expected %s.%s to be %q, got %q", tableName, columnName, want, got)
	}
}
