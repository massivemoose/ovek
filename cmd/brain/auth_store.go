package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"
)

const (
	authModeDev                 = "dev"
	authModeProd                = "prod"
	credentialPrefixAPIKey      = "ak"
	credentialPrefixReauth      = "rt"
	reauthScopeCriticalMutation = "critical_mutation"
	passwordHashIterations      = 120000
	passwordHashKeyLength       = 32
	reauthTokenTTL              = 10 * time.Minute
)

var errAuthBootstrapDisabled = errors.New("auth bootstrap disabled")
var errInvalidAPIKey = errors.New("invalid api key")
var errInvalidPassword = errors.New("invalid password")
var errInvalidReauthToken = errors.New("invalid reauth token")
var errReauthRequired = errors.New("reauth required")

type authPrincipal struct {
	UserID   string
	Username string
	APIKeyID string
}

type adminUserRecord struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    string
	DisabledAt   sql.NullString
	LastReauthAt sql.NullString
}

type reauthTokenRecord struct {
	ID        string
	UserID    string
	APIKeyID  string
	TokenHash string
	Scope     string
	CreatedAt string
	ExpiresAt string
	UsedAt    sql.NullString
	RevokedAt sql.NullString
}

func countAdminUsers(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(1) FROM admin_users WHERE disabled_at IS NULL").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count admin users: %w", err)
	}

	return count, nil
}

func bootstrapAdminUser(db *sql.DB, username string, password string) (string, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return "", errInvalidPassword
	}

	userID, err := newID()
	if err != nil {
		return "", fmt.Errorf("create user ID: %w", err)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return "", err
	}

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer tx.Rollback()

	count, err := countAdminUsersTx(tx)
	if err != nil {
		return "", err
	}
	if count > 0 {
		return "", errAuthBootstrapDisabled
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO admin_users(id, username, password_hash, created_at) VALUES(?, ?, ?, ?)`,
		userID,
		username,
		passwordHash,
		now,
	); err != nil {
		return "", fmt.Errorf("insert admin user: %w", err)
	}

	apiKey, apiKeyID, err := createAPIKeyTx(tx, userID, "bootstrap", now)
	if err != nil {
		return "", err
	}

	if err := insertAuditLogTx(tx, auditLogRecord{
		UserID:      sql.NullString{String: userID, Valid: true},
		APIKeyID:    sql.NullString{String: apiKeyID, Valid: true},
		EventType:   "auth.bootstrap_completed",
		DetailsJSON: mustDetailsJSON(map[string]string{"username": username}),
		CreatedAt:   now,
	}); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit bootstrap transaction: %w", err)
	}

	return apiKey, nil
}

func createAPIKey(ctx context.Context, db *sql.DB, userID string, label string) (string, string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return "", "", fmt.Errorf("begin api key transaction: %w", err)
	}
	defer tx.Rollback()

	apiKey, apiKeyID, err := createAPIKeyTx(tx, userID, label, now)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("commit api key transaction: %w", err)
	}

	return apiKey, apiKeyID, nil
}

func countAdminUsersTx(tx *sql.Tx) (int, error) {
	var count int
	err := tx.QueryRow("SELECT COUNT(1) FROM admin_users WHERE disabled_at IS NULL").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count admin users: %w", err)
	}

	return count, nil
}

func createAPIKeyTx(tx *sql.Tx, userID string, label string, now string) (string, string, error) {
	apiKey, apiKeyID, apiKeyHash, err := newOpaqueCredential(credentialPrefixAPIKey)
	if err != nil {
		return "", "", fmt.Errorf("create api key: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO api_keys(id, user_id, label, key_hash, created_at) VALUES(?, ?, ?, ?, ?)`,
		apiKeyID,
		userID,
		label,
		apiKeyHash,
		now,
	); err != nil {
		return "", "", fmt.Errorf("insert api key: %w", err)
	}

	return apiKey, apiKeyID, nil
}

func validateAPIKey(ctx context.Context, db *sql.DB, plaintext string) (authPrincipal, error) {
	apiKeyID, apiKeyHash, err := parseOpaqueCredential(credentialPrefixAPIKey, plaintext)
	if err != nil {
		return authPrincipal{}, errInvalidAPIKey
	}

	var (
		recordedHash string
		userID       string
		username     string
		disabledAt   sql.NullString
		revokedAt    sql.NullString
	)
	err = db.QueryRowContext(
		ctx,
		`SELECT api_keys.key_hash, api_keys.user_id, admin_users.username, admin_users.disabled_at, api_keys.revoked_at
		 FROM api_keys
		 JOIN admin_users ON admin_users.id = api_keys.user_id
		 WHERE api_keys.id = ?`,
		apiKeyID,
	).Scan(&recordedHash, &userID, &username, &disabledAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authPrincipal{}, errInvalidAPIKey
	}
	if err != nil {
		return authPrincipal{}, fmt.Errorf("query api key: %w", err)
	}
	if disabledAt.Valid || revokedAt.Valid {
		return authPrincipal{}, errInvalidAPIKey
	}
	if subtle.ConstantTimeCompare([]byte(recordedHash), []byte(apiKeyHash)) != 1 {
		return authPrincipal{}, errInvalidAPIKey
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, apiKeyID); err != nil {
		return authPrincipal{}, fmt.Errorf("update api key last used: %w", err)
	}

	return authPrincipal{
		UserID:   userID,
		Username: username,
		APIKeyID: apiKeyID,
	}, nil
}

func issueReauthToken(ctx context.Context, db *sql.DB, principal authPrincipal, password string) (string, string, error) {
	user, err := getAdminUserByID(ctx, db, principal.UserID)
	if err != nil {
		return "", "", err
	}
	if !verifyPassword(user.PasswordHash, password) {
		_ = insertAuditLog(ctx, db, auditLogRecord{
			UserID:      sql.NullString{String: principal.UserID, Valid: true},
			APIKeyID:    sql.NullString{String: principal.APIKeyID, Valid: true},
			EventType:   "auth.reauth_failed",
			DetailsJSON: mustDetailsJSON(map[string]string{"reason": "invalid_password"}),
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		})
		return "", "", errInvalidPassword
	}

	token, tokenID, tokenHash, err := newOpaqueCredential(credentialPrefixReauth)
	if err != nil {
		return "", "", fmt.Errorf("create reauth token: %w", err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(reauthTokenTTL).Format(time.RFC3339Nano)
	nowText := now.Format(time.RFC3339Nano)

	tx, err := db.Begin()
	if err != nil {
		return "", "", fmt.Errorf("begin reauth transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO reauth_tokens(id, user_id, api_key_id, token_hash, scope, created_at, expires_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		tokenID,
		principal.UserID,
		principal.APIKeyID,
		tokenHash,
		reauthScopeCriticalMutation,
		nowText,
		expiresAt,
	); err != nil {
		return "", "", fmt.Errorf("insert reauth token: %w", err)
	}
	if _, err := tx.Exec(`UPDATE admin_users SET last_reauth_at = ? WHERE id = ?`, nowText, principal.UserID); err != nil {
		return "", "", fmt.Errorf("update admin user last reauth: %w", err)
	}
	if err := insertAuditLogTx(tx, auditLogRecord{
		UserID:      sql.NullString{String: principal.UserID, Valid: true},
		APIKeyID:    sql.NullString{String: principal.APIKeyID, Valid: true},
		EventType:   "auth.reauth_succeeded",
		DetailsJSON: mustDetailsJSON(map[string]string{"scope": reauthScopeCriticalMutation}),
		CreatedAt:   nowText,
	}); err != nil {
		return "", "", err
	}

	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("commit reauth transaction: %w", err)
	}

	return token, expiresAt, nil
}

func requireReauthToken(ctx context.Context, db *sql.DB, principal authPrincipal, token string, scope string) error {
	if strings.TrimSpace(token) == "" {
		return errReauthRequired
	}

	tokenID, tokenHash, err := parseOpaqueCredential(credentialPrefixReauth, token)
	if err != nil {
		return errReauthRequired
	}

	record, err := getReauthTokenByID(ctx, db, tokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return errReauthRequired
	}
	if err != nil {
		return err
	}

	if record.UserID != principal.UserID || record.APIKeyID != principal.APIKeyID || record.Scope != scope {
		return errReauthRequired
	}
	if record.RevokedAt.Valid {
		return errReauthRequired
	}
	if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(tokenHash)) != 1 {
		return errReauthRequired
	}

	expiresAt, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil || time.Now().UTC().After(expiresAt) {
		return errReauthRequired
	}

	if _, err := db.ExecContext(ctx, `UPDATE reauth_tokens SET used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), record.ID); err != nil {
		return fmt.Errorf("update reauth token used at: %w", err)
	}

	return nil
}

func getAdminUserByID(ctx context.Context, db *sql.DB, userID string) (adminUserRecord, error) {
	var user adminUserRecord
	err := db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash, created_at, disabled_at, last_reauth_at
		 FROM admin_users
		 WHERE id = ?`,
		userID,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.DisabledAt, &user.LastReauthAt)
	if err != nil {
		return adminUserRecord{}, err
	}

	return user, nil
}

func getReauthTokenByID(ctx context.Context, db *sql.DB, tokenID string) (reauthTokenRecord, error) {
	var token reauthTokenRecord
	err := db.QueryRowContext(
		ctx,
		`SELECT id, user_id, api_key_id, token_hash, scope, created_at, expires_at, used_at, revoked_at
		 FROM reauth_tokens
		 WHERE id = ?`,
		tokenID,
	).Scan(
		&token.ID,
		&token.UserID,
		&token.APIKeyID,
		&token.TokenHash,
		&token.Scope,
		&token.CreatedAt,
		&token.ExpiresAt,
		&token.UsedAt,
		&token.RevokedAt,
	)
	if err != nil {
		return reauthTokenRecord{}, err
	}

	return token, nil
}

type auditLogRecord struct {
	ID          string
	UserID      sql.NullString
	APIKeyID    sql.NullString
	EventType   string
	ProjectName sql.NullString
	DetailsJSON string
	CreatedAt   string
}

func insertAuditLog(ctx context.Context, db *sql.DB, record auditLogRecord) error {
	return insertAuditLogQueryer(ctx, db, record)
}

func insertAuditLogTx(tx *sql.Tx, record auditLogRecord) error {
	return insertAuditLogQueryer(context.Background(), tx, record)
}

type auditLogQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertAuditLogQueryer(ctx context.Context, queryer auditLogQueryer, record auditLogRecord) error {
	if record.ID == "" {
		recordID, err := newID()
		if err != nil {
			return fmt.Errorf("create audit log id: %w", err)
		}
		record.ID = recordID
	}
	if record.DetailsJSON == "" {
		record.DetailsJSON = "{}"
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if _, err := queryer.ExecContext(
		ctx,
		`INSERT INTO audit_logs(id, user_id, api_key_id, event_type, project_name, details_json, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		nullStringValue(record.UserID),
		nullStringValue(record.APIKeyID),
		record.EventType,
		nullStringValue(record.ProjectName),
		record.DetailsJSON,
		record.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key, err := pbkdf2.Key(func() hash.Hash { return sha256.New() }, password, salt, passwordHashIterations, passwordHashKeyLength)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}

	return fmt.Sprintf(
		"pbkdf2-sha256$%d$%s$%s",
		passwordHashIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(encodedHash string, password string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	derivedHash, err := pbkdf2.Key(func() hash.Hash { return sha256.New() }, password, salt, iterations, len(expectedHash))
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(expectedHash, derivedHash) == 1
}

func newOpaqueCredential(prefix string) (string, string, string, error) {
	credentialID, err := newID()
	if err != nil {
		return "", "", "", err
	}

	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return "", "", "", err
	}
	plaintext := prefix + "_" + credentialID + "_" + hex.EncodeToString(secret)
	return plaintext, credentialID, hashOpaqueCredential(plaintext), nil
}

func parseOpaqueCredential(prefix string, plaintext string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(plaintext), "_")
	if len(parts) != 3 || parts[0] != prefix || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", errInvalidAPIKey
	}

	return parts[1], hashOpaqueCredential(plaintext), nil
}

func hashOpaqueCredential(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func mustDetailsJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func nullStringValue(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}
