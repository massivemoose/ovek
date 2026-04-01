package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestJobManagerMarksFailedJobWhenProcessorReturnsError(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(context.Context, job) error {
		return errors.New("build failed")
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)

	job := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if job.ErrorMessage != "build failed" {
		t.Fatalf("expected error message %q, got %q", "build failed", job.ErrorMessage)
	}
	if job.StartedAt == "" {
		t.Fatal("expected startedAt to be set")
	}
	if job.FinishedAt == "" {
		t.Fatal("expected finishedAt to be set")
	}
}

func TestJobManagerProcessesJobsSequentially(t *testing.T) {
	db := newTestDB(t)
	firstJob, err := createQueuedJob(db, "demo-app", "https://example.com/first.git")
	if err != nil {
		t.Fatalf("expected first job creation to succeed, got error: %v", err)
	}
	secondJob, err := createQueuedJob(db, "demo-app", "https://example.com/second.git")
	if err != nil {
		t.Fatalf("expected second job creation to succeed, got error: %v", err)
	}

	firstJobStarted := make(chan struct{})
	releaseFirstJob := make(chan struct{})
	manager := newJobManager(db, processorFunc(func(_ context.Context, job job) error {
		if job.ID == firstJob.ID {
			close(firstJobStarted)
			<-releaseFirstJob
		}
		return nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(firstJob.ID)
	manager.Enqueue(secondJob.ID)

	select {
	case <-firstJobStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first job to start")
	}

	firstJobState := waitForJobStatus(t, db, firstJob.ID, jobStatusRunning)
	if firstJobState.StartedAt == "" {
		t.Fatal("expected first job startedAt to be set")
	}

	secondJobState, err := getJob(db, secondJob.ID)
	if err != nil {
		t.Fatalf("expected second job lookup to succeed, got error: %v", err)
	}
	if secondJobState.Status != jobStatusQueued {
		t.Fatalf("expected second job to remain queued while first is running, got %q", secondJobState.Status)
	}

	close(releaseFirstJob)

	waitForJobStatus(t, db, firstJob.ID, jobStatusSucceeded)
	waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)
}

func TestJobManagerRequeuesQueuedJobsOnStart(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(context.Context, job) error {
		return nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	job := waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)
	if job.FinishedAt == "" {
		t.Fatal("expected finishedAt to be set")
	}
}

type processorFunc func(ctx context.Context, job job) error

func (process processorFunc) Process(ctx context.Context, job job) error {
	return process(ctx, job)
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func waitForJobStatus(t *testing.T, db *sql.DB, jobID string, wantStatus string) job {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := getJob(db, jobID)
		if err == nil && job.Status == wantStatus {
			return job
		}

		time.Sleep(10 * time.Millisecond)
	}

	job, err := getJob(db, jobID)
	if err != nil {
		t.Fatalf("expected final job lookup to succeed, got error: %v", err)
	}

	t.Fatalf("expected job %q status %q, got %q", jobID, wantStatus, job.Status)
	return job
}
