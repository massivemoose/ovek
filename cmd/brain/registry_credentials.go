package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

var errRegistryCredentialNotFound = errors.New("registry credential not found")

type registryCredentialStore struct {
	db     *sql.DB
	cipher secretCipher
}

type registryCredentialSecret struct {
	Host     string
	Username string
	Password string
}

func newRegistryCredentialStore(db *sql.DB, cipher secretCipher) registryCredentialStore {
	return registryCredentialStore{db: db, cipher: cipher}
}

func (store registryCredentialStore) List(ctx context.Context) ([]brainapi.RegistryCredential, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT registry_host, username, created_at, updated_at
  FROM registry_credentials
 ORDER BY registry_host ASC`)
	if err != nil {
		return nil, fmt.Errorf("list registry credentials: %w", err)
	}
	defer rows.Close()

	var credentials []brainapi.RegistryCredential
	for rows.Next() {
		var credential brainapi.RegistryCredential
		if err := rows.Scan(&credential.Host, &credential.Username, &credential.CreatedAt, &credential.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan registry credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registry credentials: %w", err)
	}

	return credentials, nil
}

func (store registryCredentialStore) Upsert(ctx context.Context, host string, username string, password string, actor string) (brainapi.RegistryCredential, error) {
	host, err := canonicalRegistryHost(host)
	if err != nil {
		return brainapi.RegistryCredential{}, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return brainapi.RegistryCredential{}, errors.New("registry username is required")
	}
	if password == "" {
		return brainapi.RegistryCredential{}, errors.New("registry password is required")
	}

	encryptedPassword, err := store.cipher.Encrypt(password)
	if err != nil {
		return brainapi.RegistryCredential{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return brainapi.RegistryCredential{}, fmt.Errorf("begin registry credential transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO registry_credentials(registry_host, username, encrypted_password, created_at, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(registry_host) DO UPDATE SET
	username = excluded.username,
	encrypted_password = excluded.encrypted_password,
	updated_at = excluded.updated_at`,
		host,
		username,
		encryptedPassword,
		now,
		now,
	); err != nil {
		return brainapi.RegistryCredential{}, fmt.Errorf("upsert registry credential: %w", err)
	}
	if err := insertAuditLogTx(tx, auditLogRecord{
		EventType:   "registry_credential.upserted",
		DetailsJSON: mustDetailsJSON(map[string]string{"host": host, "actor": actor}),
		CreatedAt:   now,
	}); err != nil {
		return brainapi.RegistryCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return brainapi.RegistryCredential{}, fmt.Errorf("commit registry credential transaction: %w", err)
	}

	return brainapi.RegistryCredential{
		Host:      host,
		Username:  username,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (store registryCredentialStore) Delete(ctx context.Context, host string, actor string) error {
	host, err := canonicalRegistryHost(host)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin registry credential transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM registry_credentials WHERE registry_host = ?`, host)
	if err != nil {
		return fmt.Errorf("delete registry credential: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted registry credentials: %w", err)
	}
	if rowsAffected == 0 {
		return errRegistryCredentialNotFound
	}
	if err := insertAuditLogTx(tx, auditLogRecord{
		EventType:   "registry_credential.deleted",
		DetailsJSON: mustDetailsJSON(map[string]string{"host": host, "actor": actor}),
		CreatedAt:   now,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registry credential transaction: %w", err)
	}

	return nil
}

func (store registryCredentialStore) ResolveForImage(ctx context.Context, imageRef string) (registryCredentialSecret, bool, error) {
	host, found := registryHostFromImageRef(imageRef)
	if !found {
		return registryCredentialSecret{}, false, nil
	}

	var credential registryCredentialSecret
	var encryptedPassword string
	err := store.db.QueryRowContext(ctx, `
SELECT registry_host, username, encrypted_password
  FROM registry_credentials
 WHERE registry_host = ?`,
		host,
	).Scan(&credential.Host, &credential.Username, &encryptedPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return registryCredentialSecret{}, false, nil
	}
	if err != nil {
		return registryCredentialSecret{}, false, fmt.Errorf("load registry credential: %w", err)
	}

	password, err := store.cipher.Decrypt(encryptedPassword)
	if err != nil {
		return registryCredentialSecret{}, false, fmt.Errorf("decrypt registry credential: %w", err)
	}
	credential.Password = password

	return credential, true, nil
}

func canonicalRegistryHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", errors.New("registry host is required")
	}
	if strings.Contains(host, "://") || strings.Contains(host, "/") || strings.ContainsAny(host, " \t\r\n") {
		return "", errors.New("registry host must be a host name without scheme or path")
	}
	return host, nil
}

func registryHostFromImageRef(imageRef string) (string, bool) {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return "", false
	}
	imageRef = strings.TrimPrefix(imageRef, "docker://")

	firstSlash := strings.Index(imageRef, "/")
	if firstSlash <= 0 {
		return "", false
	}

	host := strings.ToLower(imageRef[:firstSlash])
	if strings.Contains(host, ".") || strings.Contains(host, ":") || host == "localhost" {
		return host, true
	}

	return "", false
}

func registryCredentialHosts(credentials []brainapi.RegistryCredential) []string {
	hosts := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		hosts = append(hosts, credential.Host)
	}
	sort.Strings(hosts)
	return hosts
}
