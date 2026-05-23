package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestCreateQueuedWorkflowRunSnapshotsWorkflowAndConfig(t *testing.T) {
	db := newTestDB(t)
	configStore := newTestProjectConfigStore(t, db)
	mutation, err := configStore.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "test")
	if err != nil {
		t.Fatalf("expected env mutation to succeed, got error: %v", err)
	}
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:        "demo-app",
		Name:               "digest",
		SourceImageRef:     "ghcr.io/example/digest:latest",
		ResolvedRepoDigest: "ghcr.io/example/digest@sha256:repo-digest",
		RuntimeImageID:     "sha256:image-id",
		QueueCap:           2,
		Enabled:            true,
	})

	run, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual)
	if err != nil {
		t.Fatalf("expected queued workflow run to be created, got error: %v", err)
	}

	if run.Status != workflowRunStatusQueued || run.TriggerType != workflowRunTriggerManual {
		t.Fatalf("expected queued manual run, got %#v", run)
	}
	if run.ConfigRevisionID != mutation.RevisionID {
		t.Fatalf("expected config revision %q, got %q", mutation.RevisionID, run.ConfigRevisionID)
	}
	if run.SourceImageRef != "ghcr.io/example/digest:latest" || run.ResolvedRepoDigest != "ghcr.io/example/digest@sha256:repo-digest" || run.RuntimeImageID != "sha256:image-id" {
		t.Fatalf("expected workflow image metadata snapshot, got %#v", run)
	}
}

func TestCreateQueuedWorkflowRunHonorsQueueCap(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       1,
		Enabled:        true,
	})

	if _, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerAPI); err != nil {
		t.Fatalf("expected first queued workflow run to be created, got error: %v", err)
	}

	_, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual)
	if !errors.Is(err, errWorkflowQueueFull) {
		t.Fatalf("expected queue full error, got %v", err)
	}
}

func TestCreateScheduledWorkflowRunSkipsOverlap(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       1,
		Enabled:        true,
	})
	if _, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual); err != nil {
		t.Fatalf("expected active workflow run to be created, got error: %v", err)
	}

	skipped, err := createScheduledWorkflowRun(context.Background(), db, "demo-app", "digest")
	if err != nil {
		t.Fatalf("expected scheduled overlap to create skipped run, got error: %v", err)
	}

	if skipped.Status != workflowRunStatusSkipped || skipped.TriggerType != workflowRunTriggerSchedule {
		t.Fatalf("expected skipped scheduled run, got %#v", skipped)
	}
	if skipped.FinishedAt == "" || skipped.ErrorMessage == "" {
		t.Fatalf("expected skipped run to have terminal metadata, got %#v", skipped)
	}
}

func TestWorkflowManagerProcessesRunsSerially(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       4,
		Enabled:        true,
	})
	first, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual)
	if err != nil {
		t.Fatalf("expected first run creation to succeed, got error: %v", err)
	}
	second, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual)
	if err != nil {
		t.Fatalf("expected second run creation to succeed, got error: %v", err)
	}

	processor := newBlockingWorkflowProcessor()
	manager := newWorkflowManager(db, processor)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager start to succeed, got error: %v", err)
	}

	if got := processor.waitStarted(t); got != first.ID {
		t.Fatalf("expected first run to start first, got %q", got)
	}
	processor.assertNoAdditionalStart(t)
	processor.release()
	if got := processor.waitStarted(t); got != second.ID {
		t.Fatalf("expected second run to start second, got %q", got)
	}
	processor.release()
	processor.waitFinished(t, 2)

	firstRun, err := getWorkflowRun(context.Background(), db, "demo-app", first.ID)
	if err != nil {
		t.Fatalf("expected first run reload to succeed, got error: %v", err)
	}
	secondRun, err := getWorkflowRun(context.Background(), db, "demo-app", second.ID)
	if err != nil {
		t.Fatalf("expected second run reload to succeed, got error: %v", err)
	}
	if firstRun.Status != workflowRunStatusSucceeded || secondRun.Status != workflowRunStatusSucceeded {
		t.Fatalf("expected both runs to succeed, got first=%#v second=%#v", firstRun, secondRun)
	}
}

func TestRecoverInterruptedWorkflowRunsFailsPreparingAndRunningRuns(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       4,
		Enabled:        true,
	})
	queued, err := createWorkflowRunRecord(context.Background(), db, workflowRun{
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		TriggerType:    workflowRunTriggerManual,
		Status:         workflowRunStatusQueued,
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
	})
	if err != nil {
		t.Fatalf("expected queued run creation to succeed, got error: %v", err)
	}
	preparing, err := createWorkflowRunRecord(context.Background(), db, workflowRun{
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		TriggerType:    workflowRunTriggerManual,
		Status:         workflowRunStatusPreparing,
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
	})
	if err != nil {
		t.Fatalf("expected preparing run creation to succeed, got error: %v", err)
	}
	running, err := createWorkflowRunRecord(context.Background(), db, workflowRun{
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		TriggerType:    workflowRunTriggerManual,
		Status:         workflowRunStatusRunning,
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
	})
	if err != nil {
		t.Fatalf("expected running run creation to succeed, got error: %v", err)
	}

	if err := recoverInterruptedWorkflowRuns(db); err != nil {
		t.Fatalf("expected recovery to succeed, got error: %v", err)
	}

	queuedRun, err := getWorkflowRun(context.Background(), db, "demo-app", queued.ID)
	if err != nil {
		t.Fatalf("expected queued run reload to succeed, got error: %v", err)
	}
	preparingRun, err := getWorkflowRun(context.Background(), db, "demo-app", preparing.ID)
	if err != nil {
		t.Fatalf("expected preparing run reload to succeed, got error: %v", err)
	}
	runningRun, err := getWorkflowRun(context.Background(), db, "demo-app", running.ID)
	if err != nil {
		t.Fatalf("expected running run reload to succeed, got error: %v", err)
	}

	if queuedRun.Status != workflowRunStatusQueued {
		t.Fatalf("expected queued run to remain queued, got %#v", queuedRun)
	}
	if preparingRun.Status != workflowRunStatusFailed || runningRun.Status != workflowRunStatusFailed {
		t.Fatalf("expected interrupted runs to fail, got preparing=%#v running=%#v", preparingRun, runningRun)
	}
	if preparingRun.ErrorMessage != interruptedWorkflowRunErrorMessage || runningRun.ErrorMessage != interruptedWorkflowRunErrorMessage {
		t.Fatalf("expected interrupted error message, got preparing=%q running=%q", preparingRun.ErrorMessage, runningRun.ErrorMessage)
	}
}

func seedWorkflowDefinition(t *testing.T, db *sql.DB, workflow workflowDefinition) workflowDefinition {
	t.Helper()
	created, err := upsertWorkflowDefinition(context.Background(), db, workflow)
	if err != nil {
		t.Fatalf("expected workflow seed to succeed, got error: %v", err)
	}
	return created
}

type blockingWorkflowProcessor struct {
	started  chan string
	releases chan struct{}
	done     chan struct{}
}

func newBlockingWorkflowProcessor() *blockingWorkflowProcessor {
	return &blockingWorkflowProcessor{
		started:  make(chan string, 4),
		releases: make(chan struct{}),
		done:     make(chan struct{}, 4),
	}
}

func (processor *blockingWorkflowProcessor) Process(_ context.Context, run workflowRun) (workflowRunResult, error) {
	processor.started <- run.ID
	<-processor.releases
	processor.done <- struct{}{}
	exitCode := 0
	return workflowRunResult{ExitCode: &exitCode}, nil
}

func (processor *blockingWorkflowProcessor) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case runID := <-processor.started:
		return runID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow run to start")
	}
	return ""
}

func (processor *blockingWorkflowProcessor) assertNoAdditionalStart(t *testing.T) {
	t.Helper()
	select {
	case runID := <-processor.started:
		t.Fatalf("expected no additional workflow run to start yet, got %q", runID)
	case <-time.After(50 * time.Millisecond):
	}
}

func (processor *blockingWorkflowProcessor) release() {
	processor.releases <- struct{}{}
}

func (processor *blockingWorkflowProcessor) waitFinished(t *testing.T, count int) {
	t.Helper()
	var finished []struct{}
	for len(finished) < count {
		select {
		case item := <-processor.done:
			finished = append(finished, item)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for processor completions, got %d", len(finished))
		}
	}
	if len(finished) != count {
		t.Fatalf("expected %d completions, got %d", count, len(finished))
	}
}
