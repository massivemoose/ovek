package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const (
	maxProjectEnvNameLength  = 255
	maxProjectEnvValueLength = 8192
)

var projectEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

var errProjectEnvNotFound = errors.New("project environment entry not found")

type projectConfigStore struct {
	db     *sql.DB
	cipher secretCipher
}

type projectConfigEntryRecord struct {
	ProjectName string
	RevisionID  string
	Name        string
	Value       string
	Secret      bool
	CreatedAt   string
}

type resolvedProjectConfig struct {
	RevisionID     string
	Env            []string
	SecretValues   []string
	SecretScrubber secretScrubber
}

func newProjectConfigStore(db *sql.DB, cipher secretCipher) projectConfigStore {
	return projectConfigStore{
		db:     db,
		cipher: cipher,
	}
}

func validateProjectEnvName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return errors.New("environment variable name is required")
	}
	if len(name) > maxProjectEnvNameLength {
		return fmt.Errorf("environment variable name must be at most %d characters", maxProjectEnvNameLength)
	}
	if !projectEnvNamePattern.MatchString(name) {
		return errors.New("environment variable name must match ^[A-Z_][A-Z0-9_]*$")
	}
	if name == "PORT" || name == "POCKETBASE_URL" || strings.HasPrefix(name, "OVEK_") {
		return fmt.Errorf("environment variable name %q is reserved", name)
	}

	return nil
}

func validateProjectEnvValue(value string) error {
	if len([]byte(value)) > maxProjectEnvValueLength {
		return fmt.Errorf("environment variable value must be at most %d bytes", maxProjectEnvValueLength)
	}

	return nil
}

func (store projectConfigStore) ListEnvironment(ctx context.Context, projectName string) ([]brainapi.ProjectEnvironmentEntry, error) {
	latestRevisionID, found, err := latestProjectConfigRevisionID(ctx, store.db, projectName)
	if err != nil {
		return nil, err
	}
	if !found {
		exists, err := projectExists(store.db, projectName)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, sql.ErrNoRows
		}
		return []brainapi.ProjectEnvironmentEntry{}, nil
	}

	records, err := listProjectConfigEntryRecords(ctx, store.db, projectName, latestRevisionID)
	if err != nil {
		return nil, err
	}

	entries := make([]brainapi.ProjectEnvironmentEntry, 0, len(records))
	for _, record := range records {
		entry := brainapi.ProjectEnvironmentEntry{
			Name:      record.Name,
			Secret:    record.Secret,
			UpdatedAt: record.CreatedAt,
		}
		if !record.Secret {
			value := record.Value
			entry.Value = &value
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

func (store projectConfigStore) SetEnvironmentEntry(ctx context.Context, projectName string, name string, value string, secret bool, createdBy string) (brainapi.ProjectEnvironmentMutation, error) {
	name = strings.TrimSpace(name)
	if err := validateProjectEnvName(name); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if err := validateProjectEnvValue(value); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("begin project config transaction: %w", err)
	}
	defer tx.Rollback()

	if err := ensureProjectTx(tx, projectName, now); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}

	entries, err := latestProjectConfigEntryMap(ctx, tx, projectName)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	storedValue := value
	if secret {
		storedValue, err = store.cipher.Encrypt(value)
		if err != nil {
			return brainapi.ProjectEnvironmentMutation{}, err
		}
	}
	entries[name] = projectConfigEntryRecord{
		ProjectName: projectName,
		Name:        name,
		Value:       storedValue,
		Secret:      secret,
		CreatedAt:   now,
	}

	revisionID, err := insertProjectConfigRevision(ctx, tx, projectName, createdBy, projectConfigMutationReason(name, secret, "set"), now, entries)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if err := insertAuditLogTx(tx, projectConfigAuditRecord(projectName, createdBy, name, secret, "set", now)); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("commit project config transaction: %w", err)
	}

	entry := brainapi.ProjectEnvironmentEntry{
		Name:      name,
		Secret:    secret,
		UpdatedAt: now,
	}
	if !secret {
		entry.Value = &value
	}

	return brainapi.ProjectEnvironmentMutation{
		RevisionID: revisionID,
		Entry:      &entry,
	}, nil
}

func (store projectConfigStore) DeleteEnvironmentEntry(ctx context.Context, projectName string, name string, createdBy string) (brainapi.ProjectEnvironmentMutation, error) {
	name = strings.TrimSpace(name)
	if err := validateProjectEnvName(name); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("begin project config transaction: %w", err)
	}
	defer tx.Rollback()

	exists, err := projectExistsTx(tx, projectName)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if !exists {
		return brainapi.ProjectEnvironmentMutation{}, sql.ErrNoRows
	}

	entries, err := latestProjectConfigEntryMap(ctx, tx, projectName)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	record, found := entries[name]
	if !found {
		return brainapi.ProjectEnvironmentMutation{}, errProjectEnvNotFound
	}
	delete(entries, name)

	revisionID, err := insertProjectConfigRevision(ctx, tx, projectName, createdBy, projectConfigMutationReason(name, record.Secret, "unset"), now, entries)
	if err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if err := insertAuditLogTx(tx, projectConfigAuditRecord(projectName, createdBy, name, record.Secret, "unset", now)); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("commit project config transaction: %w", err)
	}

	return brainapi.ProjectEnvironmentMutation{RevisionID: revisionID}, nil
}

func (store projectConfigStore) ResolveRuntimeConfig(ctx context.Context, projectName string, revisionID string) (resolvedProjectConfig, error) {
	revisionID = strings.TrimSpace(revisionID)
	if revisionID == "" || store.db == nil {
		return resolvedProjectConfig{}, nil
	}
	exists, err := projectConfigRevisionExists(ctx, store.db, projectName, revisionID)
	if err != nil {
		return resolvedProjectConfig{}, err
	}
	if !exists {
		return resolvedProjectConfig{}, sql.ErrNoRows
	}

	records, err := listProjectConfigEntryRecords(ctx, store.db, projectName, revisionID)
	if err != nil {
		return resolvedProjectConfig{}, err
	}

	config := resolvedProjectConfig{RevisionID: revisionID}
	for _, record := range records {
		value := record.Value
		if record.Secret {
			value, err = store.cipher.Decrypt(record.Value)
			if err != nil {
				return resolvedProjectConfig{}, fmt.Errorf("decrypt project secret %q: %w", record.Name, err)
			}
			config.SecretValues = append(config.SecretValues, value)
		}
		config.Env = append(config.Env, record.Name+"="+value)
	}
	config.SecretScrubber = newSecretScrubber(config.SecretValues)

	return config, nil
}

func (store projectConfigStore) SecretScrubberForJob(ctx context.Context, currentJob job) (secretScrubber, error) {
	config, err := store.ResolveRuntimeConfig(ctx, currentJob.ProjectName, currentJob.ConfigRevisionID)
	if err != nil {
		return secretScrubber{}, err
	}

	return config.SecretScrubber, nil
}

func projectConfigMutationReason(name string, secret bool, action string) string {
	prefix := "env"
	if secret {
		prefix = "secret"
	}
	return prefix + "." + action + ":" + name
}

func projectConfigAuditRecord(projectName string, actor string, name string, secret bool, action string, createdAt string) auditLogRecord {
	eventType := "project_env." + action
	if secret {
		eventType = "project_secret." + action
	}

	return auditLogRecord{
		EventType:   eventType,
		ProjectName: sql.NullString{String: projectName, Valid: true},
		DetailsJSON: mustDetailsJSON(map[string]string{
			"name":  name,
			"actor": actor,
		}),
		CreatedAt: createdAt,
	}
}

func latestProjectConfigEntryMap(ctx context.Context, tx *sql.Tx, projectName string) (map[string]projectConfigEntryRecord, error) {
	revisionID, found, err := latestProjectConfigRevisionID(ctx, tx, projectName)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]projectConfigEntryRecord{}, nil
	}

	records, err := listProjectConfigEntryRecords(ctx, tx, projectName, revisionID)
	if err != nil {
		return nil, err
	}

	entries := make(map[string]projectConfigEntryRecord, len(records))
	for _, record := range records {
		entries[record.Name] = record
	}

	return entries, nil
}

func projectConfigRevisionExists(ctx context.Context, queryer projectConfigQueryer, projectName string, revisionID string) (bool, error) {
	var count int
	if err := queryer.QueryRowContext(
		ctx,
		`SELECT COUNT(1)
		 FROM project_config_revisions
		 WHERE project_name = ? AND id = ?`,
		projectName,
		revisionID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check project config revision: %w", err)
	}

	return count > 0, nil
}

func latestProjectConfigRevisionID(ctx context.Context, queryer projectConfigQueryer, projectName string) (string, bool, error) {
	var revisionID string
	err := queryer.QueryRowContext(
		ctx,
		`SELECT id
		 FROM project_config_revisions
		 WHERE project_name = ?
		 ORDER BY created_at DESC
		 LIMIT 1`,
		projectName,
	).Scan(&revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load latest project config revision: %w", err)
	}

	return revisionID, true, nil
}

func listProjectConfigEntryRecords(ctx context.Context, queryer projectConfigQueryer, projectName string, revisionID string) ([]projectConfigEntryRecord, error) {
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT project_name, revision_id, name, value, is_secret, created_at
		 FROM project_config_entries
		 WHERE project_name = ? AND revision_id = ?
		 ORDER BY name ASC`,
		projectName,
		revisionID,
	)
	if err != nil {
		return nil, fmt.Errorf("query project config entries: %w", err)
	}
	defer rows.Close()

	var records []projectConfigEntryRecord
	for rows.Next() {
		var record projectConfigEntryRecord
		var isSecret int
		if err := rows.Scan(
			&record.ProjectName,
			&record.RevisionID,
			&record.Name,
			&record.Value,
			&isSecret,
			&record.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project config entry: %w", err)
		}
		record.Secret = isSecret != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project config entries: %w", err)
	}

	return records, nil
}

func insertProjectConfigRevision(ctx context.Context, tx *sql.Tx, projectName string, createdBy string, reason string, createdAt string, entries map[string]projectConfigEntryRecord) (string, error) {
	revisionID, err := newID()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO project_config_revisions(id, project_name, created_at, created_by, reason)
		 VALUES(?, ?, ?, ?, ?)`,
		revisionID,
		projectName,
		createdAt,
		nullableString(createdBy),
		reason,
	); err != nil {
		return "", fmt.Errorf("insert project config revision: %w", err)
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := entries[name]
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO project_config_entries(project_name, revision_id, name, value, is_secret, created_at)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			projectName,
			revisionID,
			entry.Name,
			entry.Value,
			boolInt(entry.Secret),
			entry.CreatedAt,
		); err != nil {
			return "", fmt.Errorf("insert project config entry %q: %w", entry.Name, err)
		}
	}

	return revisionID, nil
}

func ensureProjectTx(tx *sql.Tx, projectName string, createdAt string) error {
	if _, err := tx.Exec(
		"INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)",
		projectName,
		projectStatusIdle,
		createdAt,
	); err != nil {
		return fmt.Errorf("ensure project %q: %w", projectName, err)
	}

	return nil
}

func projectExistsTx(tx *sql.Tx, projectName string) (bool, error) {
	var count int
	if err := tx.QueryRow("SELECT COUNT(1) FROM projects WHERE name = ?", projectName).Scan(&count); err != nil {
		return false, fmt.Errorf("check project existence: %w", err)
	}

	return count > 0, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type projectConfigQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
