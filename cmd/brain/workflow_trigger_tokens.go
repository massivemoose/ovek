package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const credentialPrefixWorkflowTriggerToken = "wft"

var (
	errWorkflowTriggerTokenNotFound = errors.New("workflow trigger token not found")
	errInvalidWorkflowTriggerToken  = errors.New("invalid workflow trigger token")
)

func createWorkflowTriggerToken(ctx context.Context, db *sql.DB, projectName string, workflowName string, label string, actor string) (brainapi.CreateWorkflowTriggerTokenResponse, error) {
	projectName = strings.TrimSpace(projectName)
	workflowName = strings.TrimSpace(workflowName)
	label = strings.TrimSpace(label)
	if label == "" {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, errors.New("workflow trigger token label is required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, fmt.Errorf("begin workflow trigger token transaction: %w", err)
	}
	defer tx.Rollback()

	if _, found, err := getWorkflowDefinitionTx(ctx, tx, projectName, workflowName); err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, err
	} else if !found {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, errWorkflowNotFound
	}

	token, tokenID, tokenHash, err := newOpaqueCredential(credentialPrefixWorkflowTriggerToken)
	if err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO workflow_trigger_tokens(id, project_name, workflow_name, label, token_hash, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		tokenID,
		projectName,
		workflowName,
		label,
		tokenHash,
		now,
	); err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, fmt.Errorf("insert workflow trigger token: %w", err)
	}
	if err := insertAuditLogTx(tx, auditLogRecord{
		EventType:   "workflow.trigger_token.created",
		ProjectName: sql.NullString{String: projectName, Valid: true},
		DetailsJSON: mustDetailsJSON(map[string]string{
			"workflow": workflowName,
			"token_id": tokenID,
			"label":    label,
			"actor":    actor,
		}),
		CreatedAt: now,
	}); err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return brainapi.CreateWorkflowTriggerTokenResponse{}, fmt.Errorf("commit workflow trigger token transaction: %w", err)
	}

	return brainapi.CreateWorkflowTriggerTokenResponse{
		ID:           tokenID,
		ProjectName:  projectName,
		WorkflowName: workflowName,
		Label:        label,
		Token:        token,
		CreatedAt:    now,
	}, nil
}

func listWorkflowTriggerTokens(ctx context.Context, db *sql.DB, projectName string, workflowName string) ([]brainapi.WorkflowTriggerTokenSummary, error) {
	if _, err := getWorkflowDefinition(ctx, db, projectName, workflowName); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT id, project_name, workflow_name, label, created_at, last_used_at, revoked_at
		 FROM workflow_trigger_tokens
		 WHERE project_name = ? AND workflow_name = ?
		 ORDER BY created_at DESC`,
		projectName,
		workflowName,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflow trigger tokens: %w", err)
	}
	defer rows.Close()

	var tokens []brainapi.WorkflowTriggerTokenSummary
	for rows.Next() {
		token, err := scanWorkflowTriggerTokenSummary(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow trigger tokens: %w", err)
	}
	return tokens, nil
}

func revokeWorkflowTriggerToken(ctx context.Context, db *sql.DB, projectName string, workflowName string, tokenID string, actor string) error {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return errWorkflowTriggerTokenNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workflow trigger token revoke transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(
		ctx,
		`UPDATE workflow_trigger_tokens
		 SET revoked_at = ?
		 WHERE id = ? AND project_name = ? AND workflow_name = ? AND revoked_at IS NULL`,
		now,
		tokenID,
		projectName,
		workflowName,
	)
	if err != nil {
		return fmt.Errorf("revoke workflow trigger token: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count revoked workflow trigger tokens: %w", err)
	}
	if rowsAffected == 0 {
		return errWorkflowTriggerTokenNotFound
	}
	if err := insertAuditLogTx(tx, auditLogRecord{
		EventType:   "workflow.trigger_token.revoked",
		ProjectName: sql.NullString{String: projectName, Valid: true},
		DetailsJSON: mustDetailsJSON(map[string]string{
			"workflow": workflowName,
			"token_id": tokenID,
			"actor":    actor,
		}),
		CreatedAt: now,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow trigger token revoke transaction: %w", err)
	}
	return nil
}

func validateWorkflowTriggerToken(ctx context.Context, db *sql.DB, projectName string, workflowName string, plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", errInvalidWorkflowTriggerToken
	}
	tokenID, tokenHash, err := parseOpaqueCredential(credentialPrefixWorkflowTriggerToken, plaintext)
	if err != nil {
		return "", errInvalidWorkflowTriggerToken
	}

	var (
		recordedHash string
		revokedAt    sql.NullString
	)
	err = db.QueryRowContext(
		ctx,
		`SELECT token_hash, revoked_at
		 FROM workflow_trigger_tokens
		 WHERE id = ? AND project_name = ? AND workflow_name = ?`,
		tokenID,
		projectName,
		workflowName,
	).Scan(&recordedHash, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errInvalidWorkflowTriggerToken
	}
	if err != nil {
		return "", fmt.Errorf("query workflow trigger token: %w", err)
	}
	if revokedAt.Valid {
		return "", errInvalidWorkflowTriggerToken
	}
	if subtle.ConstantTimeCompare([]byte(recordedHash), []byte(tokenHash)) != 1 {
		return "", errInvalidWorkflowTriggerToken
	}
	if _, err := db.ExecContext(ctx, `UPDATE workflow_trigger_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), tokenID); err != nil {
		return "", fmt.Errorf("update workflow trigger token last used: %w", err)
	}
	return tokenID, nil
}

func scanWorkflowTriggerTokenSummary(scanner workflowScanner) (brainapi.WorkflowTriggerTokenSummary, error) {
	var token brainapi.WorkflowTriggerTokenSummary
	var lastUsedAt sql.NullString
	var revokedAt sql.NullString
	if err := scanner.Scan(
		&token.ID,
		&token.ProjectName,
		&token.WorkflowName,
		&token.Label,
		&token.CreatedAt,
		&lastUsedAt,
		&revokedAt,
	); err != nil {
		return brainapi.WorkflowTriggerTokenSummary{}, err
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = lastUsedAt.String
	}
	if revokedAt.Valid {
		token.RevokedAt = revokedAt.String
	}
	return token, nil
}
