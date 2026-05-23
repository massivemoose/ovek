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
	{
		version: 2,
		name:    "create control plane auth tables",
		upSQL: `
CREATE TABLE admin_users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	disabled_at TEXT,
	last_reauth_at TEXT
);

CREATE TABLE api_keys (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	label TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_used_at TEXT,
	revoked_at TEXT,
	FOREIGN KEY (user_id) REFERENCES admin_users(id)
);

CREATE TABLE reauth_tokens (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	api_key_id TEXT NOT NULL,
	token_hash TEXT NOT NULL,
	scope TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	used_at TEXT,
	revoked_at TEXT,
	FOREIGN KEY (user_id) REFERENCES admin_users(id),
	FOREIGN KEY (api_key_id) REFERENCES api_keys(id)
);

CREATE TABLE audit_logs (
	id TEXT PRIMARY KEY,
	user_id TEXT,
	api_key_id TEXT,
	event_type TEXT NOT NULL,
	project_name TEXT,
	details_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (user_id) REFERENCES admin_users(id),
	FOREIGN KEY (api_key_id) REFERENCES api_keys(id)
);
`,
	},
	{
		version: 3,
		name:    "create project environment config tables",
		upSQL: `
CREATE TABLE project_config_revisions (
	id TEXT PRIMARY KEY,
	project_name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	created_by TEXT,
	reason TEXT NOT NULL,
	FOREIGN KEY (project_name) REFERENCES projects(name)
);

CREATE TABLE project_config_entries (
	project_name TEXT NOT NULL,
	revision_id TEXT NOT NULL,
	name TEXT NOT NULL,
	value TEXT NOT NULL,
	is_secret INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (revision_id, name),
	FOREIGN KEY (project_name) REFERENCES projects(name),
	FOREIGN KEY (revision_id) REFERENCES project_config_revisions(id)
);

CREATE INDEX idx_project_config_revisions_project_created
	ON project_config_revisions(project_name, created_at DESC);

CREATE INDEX idx_project_config_entries_project_revision
	ON project_config_entries(project_name, revision_id);

ALTER TABLE jobs ADD COLUMN config_revision_id TEXT;
ALTER TABLE deployments ADD COLUMN config_revision_id TEXT;
`,
	},
	{
		version: 4,
		name:    "create project pocketbase credential table",
		upSQL: `
CREATE TABLE project_pocketbase_credentials (
	project_name TEXT PRIMARY KEY,
	superuser_email TEXT NOT NULL,
	encrypted_password TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY (project_name) REFERENCES projects(name)
);
`,
	},
	{
		version: 5,
		name:    "add job phase",
		upSQL: `
ALTER TABLE jobs ADD COLUMN phase TEXT;
`,
	},
	{
		version: 6,
		name:    "add deployment source metadata",
		upSQL: `
ALTER TABLE jobs ADD COLUMN source_type TEXT;
ALTER TABLE jobs ADD COLUMN source_ref TEXT;

UPDATE jobs
   SET source_type = 'repo'
 WHERE source_type IS NULL OR source_type = '';

UPDATE jobs
   SET source_ref = repo_url
 WHERE source_ref IS NULL OR source_ref = '';

ALTER TABLE deployments ADD COLUMN source_type TEXT;
ALTER TABLE deployments ADD COLUMN source_ref TEXT;

UPDATE deployments
   SET source_type = COALESCE((SELECT jobs.source_type FROM jobs WHERE jobs.id = deployments.id), 'repo')
 WHERE source_type IS NULL OR source_type = '';

UPDATE deployments
   SET source_ref = COALESCE((SELECT jobs.source_ref FROM jobs WHERE jobs.id = deployments.id), '')
 WHERE source_ref IS NULL OR source_ref = '';
`,
	},
	{
		version: 7,
		name:    "create registry credential table",
		upSQL: `
CREATE TABLE registry_credentials (
	registry_host TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	encrypted_password TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
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
