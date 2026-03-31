package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const databaseFileName = "brain.db"

type migration struct {
	version int
	name    string
	upSQL   string
}

var migrations = []migration{
	{
		version: 1,
		name:    "create core state tables",
		upSQL: `
CREATE TABLE projects (
	name TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	current_deployment_id TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE deployments (
	id TEXT PRIMARY KEY,
	project_name TEXT NOT NULL,
	image_ref TEXT NOT NULL,
	app_container_name TEXT NOT NULL,
	network_name TEXT NOT NULL,
	pb_container_name TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (project_name) REFERENCES projects(name)
);

CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	project_name TEXT NOT NULL,
	repo_url TEXT NOT NULL,
	status TEXT NOT NULL,
	log_path TEXT,
	image_ref TEXT,
	error_message TEXT,
	created_at TEXT NOT NULL,
	started_at TEXT,
	finished_at TEXT,
	FOREIGN KEY (project_name) REFERENCES projects(name)
);
`,
	},
}

func openBrainDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, databaseFileName)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	if err := ensureSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	return db, nil
}

func ensureSchema(db *sql.DB) error {
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)
`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	for _, migration := range migrations {
		applied, err := hasMigration(db, migration.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}

		if _, err := tx.Exec(migration.upSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version, name) VALUES(?, ?)",
			migration.version,
			migration.name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}

	return nil
}

func hasMigration(db *sql.DB, version int) (bool, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(1) FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("query migration %d: %w", version, err)
	}

	return count > 0, nil
}
