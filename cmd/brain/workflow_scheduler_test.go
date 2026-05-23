package main

import (
	"context"
	"testing"
)

func TestWorkflowSchedulerRebuildsEnabledSchedulesFromDB(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "scheduled",
		SourceImageRef: "ghcr.io/example/scheduled:latest",
		RuntimeImageID: "sha256:scheduled",
		Schedule:       "@hourly",
		QueueCap:       1,
		Enabled:        true,
	})
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "manual",
		SourceImageRef: "ghcr.io/example/manual:latest",
		RuntimeImageID: "sha256:manual",
		QueueCap:       1,
		Enabled:        true,
	})
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "disabled",
		SourceImageRef: "ghcr.io/example/disabled:latest",
		RuntimeImageID: "sha256:disabled",
		Schedule:       "@hourly",
		QueueCap:       1,
		Enabled:        false,
	})

	scheduler := newWorkflowScheduler(db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("expected scheduler start to succeed, got error: %v", err)
	}

	if len(scheduler.entries) != 1 {
		t.Fatalf("expected one scheduled entry, got %#v", scheduler.entries)
	}
	if _, ok := scheduler.entries[workflowScheduleKey("demo-app", "scheduled")]; !ok {
		t.Fatalf("expected scheduled workflow entry, got %#v", scheduler.entries)
	}
}

func TestWorkflowSchedulerReplaceAndRemoveEntries(t *testing.T) {
	db := newTestDB(t)
	scheduler := newWorkflowScheduler(db, nil)

	if err := scheduler.Replace(workflowDefinition{
		ProjectName: "demo-app",
		Name:        "digest",
		Schedule:    "@hourly",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("expected replace to succeed, got error: %v", err)
	}
	if len(scheduler.entries) != 1 {
		t.Fatalf("expected schedule entry, got %#v", scheduler.entries)
	}

	if err := scheduler.Replace(workflowDefinition{
		ProjectName: "demo-app",
		Name:        "digest",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("expected manual-only replace to succeed, got error: %v", err)
	}
	if len(scheduler.entries) != 0 {
		t.Fatalf("expected schedule entry removal, got %#v", scheduler.entries)
	}

	if err := scheduler.Replace(workflowDefinition{
		ProjectName: "demo-app",
		Name:        "digest",
		Schedule:    "@daily",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("expected second replace to succeed, got error: %v", err)
	}
	scheduler.Remove("demo-app", "digest")
	if len(scheduler.entries) != 0 {
		t.Fatalf("expected remove to clear schedule entry, got %#v", scheduler.entries)
	}
}
