package main

import (
	"context"
	"testing"
)

func TestWorkflowTriggerTokenCreateListAndRevokeStoresHashOnly(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       2,
		Enabled:        true,
	})

	response, err := createWorkflowTriggerToken(context.Background(), db, "demo-app", "digest", "app", "test")
	if err != nil {
		t.Fatalf("expected trigger token creation to succeed, got error: %v", err)
	}
	if response.Token == "" || response.ID == "" {
		t.Fatalf("expected one-time plaintext token and ID, got %#v", response)
	}
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM workflow_trigger_tokens WHERE id = ?`, response.ID).Scan(&storedHash); err != nil {
		t.Fatalf("expected stored hash lookup to succeed, got error: %v", err)
	}
	if storedHash == response.Token {
		t.Fatal("expected token table to store hash instead of plaintext token")
	}

	tokens, err := listWorkflowTriggerTokens(context.Background(), db, "demo-app", "digest")
	if err != nil {
		t.Fatalf("expected trigger token list to succeed, got error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != response.ID || tokens[0].Label != "app" {
		t.Fatalf("expected token metadata, got %#v", tokens)
	}

	if _, err := validateWorkflowTriggerToken(context.Background(), db, "demo-app", "digest", response.Token); err != nil {
		t.Fatalf("expected token validation to succeed, got error: %v", err)
	}
	if err := revokeWorkflowTriggerToken(context.Background(), db, "demo-app", "digest", response.ID, "test"); err != nil {
		t.Fatalf("expected trigger token revoke to succeed, got error: %v", err)
	}
	if _, err := validateWorkflowTriggerToken(context.Background(), db, "demo-app", "digest", response.Token); err == nil {
		t.Fatal("expected revoked token validation to fail")
	}
}
