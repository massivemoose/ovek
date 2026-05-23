package main

import (
	"context"
	"errors"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestWorkflowNameValidationMatchesProjectNames(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "digest-daily", want: true},
		{name: "a", want: true},
		{name: "DigestDaily", want: false},
		{name: "-daily", want: false},
		{name: "daily-", want: false},
		{name: "daily_digest", want: false},
	}

	for _, test := range tests {
		if got := isValidWorkflowName(test.name); got != test.want {
			t.Fatalf("expected workflow name %q validity %v, got %v", test.name, test.want, got)
		}
	}
}

func TestUpsertWorkflowDefinitionCreatesAndUpdatesWorkflow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	created, err := upsertWorkflowDefinition(ctx, db, workflowDefinition{
		ProjectName:        "demo-app",
		Name:               "digest-daily",
		SourceImageRef:     "ghcr.io/example/digest:latest",
		ResolvedRepoDigest: "ghcr.io/example/digest@sha256:abc",
		RuntimeImageID:     "sha256:abc",
		Schedule:           "0 12 * * *",
		Enabled:            true,
		UpdatedAt:          "2026-05-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("expected workflow upsert to succeed, got error: %v", err)
	}
	if created.QueueCap != defaultWorkflowQueueCap {
		t.Fatalf("expected default queue cap %d, got %d", defaultWorkflowQueueCap, created.QueueCap)
	}
	if created.Links.Self != brainapi.ProjectWorkflowPath("demo-app", "digest-daily") {
		t.Fatalf("expected self link %q, got %q", brainapi.ProjectWorkflowPath("demo-app", "digest-daily"), created.Links.Self)
	}
	if created.Links.Runs != brainapi.ProjectWorkflowDefinitionRunsPath("demo-app", "digest-daily") {
		t.Fatalf("expected runs link %q, got %q", brainapi.ProjectWorkflowDefinitionRunsPath("demo-app", "digest-daily"), created.Links.Runs)
	}

	updated, err := upsertWorkflowDefinition(ctx, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest-daily",
		SourceImageRef: "ghcr.io/example/digest:v2",
		RuntimeImageID: "sha256:def",
		QueueCap:       9,
		Enabled:        true,
		UpdatedAt:      "2026-05-02T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("expected workflow update to succeed, got error: %v", err)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatalf("expected created timestamp to remain %q, got %q", created.CreatedAt, updated.CreatedAt)
	}
	if updated.UpdatedAt == created.UpdatedAt {
		t.Fatalf("expected updated timestamp to change from %q", created.UpdatedAt)
	}
	if updated.SourceImageRef != "ghcr.io/example/digest:v2" || updated.RuntimeImageID != "sha256:def" || updated.Schedule != "" || updated.QueueCap != 9 {
		t.Fatalf("expected updated workflow fields, got %#v", updated)
	}

	workflows, err := listWorkflowDefinitions(ctx, db, "demo-app")
	if err != nil {
		t.Fatalf("expected list workflows to succeed, got error: %v", err)
	}
	if len(workflows) != 1 || workflows[0].SourceImageRef != "ghcr.io/example/digest:v2" {
		t.Fatalf("expected one updated workflow, got %#v", workflows)
	}
}

func TestCreateWorkflowRunRecordPersistsAndDecoratesRun(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	workflow, err := upsertWorkflowDefinition(ctx, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest-daily",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:abc",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("expected workflow upsert to succeed, got error: %v", err)
	}
	exitCode := 0
	run, err := createWorkflowRunRecord(ctx, db, workflowRun{
		ProjectName:        workflow.ProjectName,
		WorkflowName:       workflow.Name,
		TriggerType:        workflowRunTriggerManual,
		ConfigRevisionID:   "rev-123",
		LogPath:            "/tmp/workflow.log",
		SourceImageRef:     workflow.SourceImageRef,
		ResolvedRepoDigest: workflow.ResolvedRepoDigest,
		RuntimeImageID:     workflow.RuntimeImageID,
		ExitCode:           &exitCode,
	})
	if err != nil {
		t.Fatalf("expected workflow run create to succeed, got error: %v", err)
	}
	if run.ID == "" || run.Status != workflowRunStatusQueued || run.CreatedAt == "" {
		t.Fatalf("expected queued run with ID and timestamp, got %#v", run)
	}
	if run.Links.Self != brainapi.ProjectWorkflowRunPath("demo-app", run.ID) {
		t.Fatalf("expected self link %q, got %q", brainapi.ProjectWorkflowRunPath("demo-app", run.ID), run.Links.Self)
	}
	if run.Links.Logs != brainapi.ProjectWorkflowRunLogsPath("demo-app", run.ID) {
		t.Fatalf("expected logs link %q, got %q", brainapi.ProjectWorkflowRunLogsPath("demo-app", run.ID), run.Links.Logs)
	}
	if run.ExitCode == nil || *run.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %#v", run.ExitCode)
	}

	persisted, err := getWorkflowRun(ctx, db, "demo-app", run.ID)
	if err != nil {
		t.Fatalf("expected workflow run lookup to succeed, got error: %v", err)
	}
	if persisted.ID != run.ID || persisted.ConfigRevisionID != "rev-123" || persisted.LogPath != "/tmp/workflow.log" {
		t.Fatalf("expected persisted run fields, got %#v", persisted)
	}

	runs, err := listWorkflowRuns(ctx, db, "demo-app", 20)
	if err != nil {
		t.Fatalf("expected workflow run list to succeed, got error: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("expected one workflow run, got %#v", runs)
	}
}

func TestDeleteWorkflowDefinitionRemovesWorkflow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := upsertWorkflowDefinition(ctx, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest-daily",
		SourceImageRef: "ghcr.io/example/digest:latest",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("expected workflow upsert to succeed, got error: %v", err)
	}

	if err := deleteWorkflowDefinition(ctx, db, "demo-app", "digest-daily"); err != nil {
		t.Fatalf("expected workflow delete to succeed, got error: %v", err)
	}
	if _, err := getWorkflowDefinition(ctx, db, "demo-app", "digest-daily"); !errors.Is(err, errWorkflowNotFound) {
		t.Fatalf("expected workflow not found after delete, got %v", err)
	}
}
