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

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                "alces-demo-app:" + currentJob.ID,
			AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
			NetworkName:             projectNetworkName(currentJob.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
		}, errors.New("build failed")
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
	if job.LogPath != "/tmp/build.log" {
		t.Fatalf("expected log path %q, got %q", "/tmp/build.log", job.LogPath)
	}
	if job.ImageRef != "alces-demo-app:"+job.ID {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:"+job.ID, job.ImageRef)
	}
	if job.StartedAt == "" {
		t.Fatal("expected startedAt to be set")
	}
	if job.FinishedAt == "" {
		t.Fatal("expected finishedAt to be set")
	}
	assertDeploymentMissing(t, db, createdJob.ID)
	assertCurrentDeploymentUnset(t, db, createdJob.ProjectName)
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
	manager := newJobManager(db, processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		if job.ID == firstJob.ID {
			close(firstJobStarted)
			<-releaseFirstJob
		}
		result := successfulDeploymentResult(job)
		if job.ID == secondJob.ID {
			result.SupersededDeploymentID = firstJob.ID
		}

		return result, nil
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

	firstFinishedJob := waitForJobStatus(t, db, firstJob.ID, jobStatusSucceeded)
	if firstFinishedJob.ImageRef != "alces-demo-app:"+firstJob.ID {
		t.Fatalf("expected first image ref %q, got %q", "alces-demo-app:"+firstJob.ID, firstFinishedJob.ImageRef)
	}
	secondFinishedJob := waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)
	if secondFinishedJob.ImageRef != "alces-demo-app:"+secondJob.ID {
		t.Fatalf("expected second image ref %q, got %q", "alces-demo-app:"+secondJob.ID, secondFinishedJob.ImageRef)
	}

	firstDeployment := getDeploymentRecord(t, db, firstJob.ID)
	if firstDeployment.AppContainerName != appContainerName(firstJob.ProjectName, firstJob.ID) {
		t.Fatalf("expected first app container name %q, got %q", appContainerName(firstJob.ProjectName, firstJob.ID), firstDeployment.AppContainerName)
	}
	if firstDeployment.Status != deploymentStatusSuperseded {
		t.Fatalf("expected first deployment status %q, got %q", deploymentStatusSuperseded, firstDeployment.Status)
	}
	secondDeployment := getDeploymentRecord(t, db, secondJob.ID)
	if secondDeployment.AppContainerName != appContainerName(secondJob.ProjectName, secondJob.ID) {
		t.Fatalf("expected second app container name %q, got %q", appContainerName(secondJob.ProjectName, secondJob.ID), secondDeployment.AppContainerName)
	}
	if secondDeployment.Status != deploymentStatusSucceeded {
		t.Fatalf("expected second deployment status %q, got %q", deploymentStatusSucceeded, secondDeployment.Status)
	}
	if got := getProjectCurrentDeploymentID(t, db, secondJob.ProjectName); got != secondJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", secondJob.ID, got)
	}
}

func TestJobManagerRequeuesQueuedJobsOnStart(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:                 "/tmp/requeued.log",
			ImageRef:                "alces-demo-app:" + currentJob.ID,
			AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
			NetworkName:             projectNetworkName(currentJob.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
		}, nil
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
	if job.LogPath != "/tmp/requeued.log" {
		t.Fatalf("expected log path %q, got %q", "/tmp/requeued.log", job.LogPath)
	}

	deployment := getDeploymentRecord(t, db, createdJob.ID)
	if deployment.ProjectName != createdJob.ProjectName {
		t.Fatalf("expected deployment project name %q, got %q", createdJob.ProjectName, deployment.ProjectName)
	}
	if deployment.Status != deploymentStatusSucceeded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSucceeded, deployment.Status)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != createdJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", createdJob.ID, got)
	}
}

type processorFunc func(ctx context.Context, job job) (deploymentResult, error)

func (process processorFunc) Process(ctx context.Context, job job) (deploymentResult, error) {
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

func successfulDeploymentResult(currentJob job) deploymentResult {
	return deploymentResult{
		ImageRef:                "alces-" + currentJob.ProjectName + ":" + currentJob.ID,
		AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
		NetworkName:             projectNetworkName(currentJob.ProjectName),
		PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
	}
}

func getDeploymentRecord(t *testing.T, db *sql.DB, deploymentID string) deploymentRecord {
	t.Helper()

	var record deploymentRecord
	err := db.QueryRow(
		`SELECT id, project_name, image_ref, app_container_name, network_name, pb_container_name, status, created_at
		 FROM deployments
		 WHERE id = ?`,
		deploymentID,
	).Scan(
		&record.ID,
		&record.ProjectName,
		&record.ImageRef,
		&record.AppContainerName,
		&record.NetworkName,
		&record.PocketBaseContainerName,
		&record.Status,
		&record.CreatedAt,
	)
	if err != nil {
		t.Fatalf("expected deployment %q lookup to succeed, got error: %v", deploymentID, err)
	}

	return record
}

func assertDeploymentMissing(t *testing.T, db *sql.DB, deploymentID string) {
	t.Helper()

	var count int
	if err := db.QueryRow("SELECT COUNT(1) FROM deployments WHERE id = ?", deploymentID).Scan(&count); err != nil {
		t.Fatalf("expected deployment missing lookup to succeed, got error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected deployment %q to be absent, got count %d", deploymentID, count)
	}
}

func getProjectCurrentDeploymentID(t *testing.T, db *sql.DB, projectName string) string {
	t.Helper()

	var currentDeploymentID sql.NullString
	err := db.QueryRow(
		"SELECT current_deployment_id FROM projects WHERE name = ?",
		projectName,
	).Scan(&currentDeploymentID)
	if err != nil {
		t.Fatalf("expected project %q lookup to succeed, got error: %v", projectName, err)
	}
	if !currentDeploymentID.Valid {
		return ""
	}

	return currentDeploymentID.String
}

func assertCurrentDeploymentUnset(t *testing.T, db *sql.DB, projectName string) {
	t.Helper()

	if got := getProjectCurrentDeploymentID(t, db, projectName); got != "" {
		t.Fatalf("expected project %q current deployment to be unset, got %q", projectName, got)
	}
}
