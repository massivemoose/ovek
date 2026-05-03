package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
			ImageRef:                "ovek-demo-app:" + currentJob.ID,
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
	if job.ImageRef != "ovek-demo-app:"+job.ID {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:"+job.ID, job.ImageRef)
	}
	if job.StartedAt == "" {
		t.Fatal("expected startedAt to be set")
	}
	if job.FinishedAt == "" {
		t.Fatal("expected finishedAt to be set")
	}
	assertDeploymentMissing(t, db, createdJob.ID)
	assertCurrentDeploymentUnset(t, db, createdJob.ProjectName)
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusFailed {
		t.Fatalf("expected project status %q, got %q", projectStatusFailed, got)
	}
}

func TestJobManagerMarksFailedJobWhenPromotionStateUpdateFails(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(logPath, []byte("build output\n"), 0o644); err != nil {
		t.Fatalf("expected log file seed to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  logPath,
			ImageRef: "ovek-demo-app:" + currentJob.ID,
		}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)

	job := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if !strings.HasPrefix(job.ErrorMessage, "promotion state update failed: deployment result is missing app container name") {
		t.Fatalf("expected promotion update error, got %q", job.ErrorMessage)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected log file read to succeed, got error: %v", err)
	}
	if !strings.Contains(string(logBytes), "error: "+job.ErrorMessage) {
		t.Fatalf("expected job logs to contain promotion error %q, got %q", job.ErrorMessage, string(logBytes))
	}
	assertDeploymentMissing(t, db, createdJob.ID)
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusFailed {
		t.Fatalf("expected project status %q, got %q", projectStatusFailed, got)
	}
}

func TestNormalizeJobFailureMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		errorMessage string
		want         string
	}{
		{
			name:         "source fetch",
			errorMessage: "git clone: repository not found",
			want:         "source fetch failed: repository not found",
		},
		{
			name:         "build planning",
			errorMessage: "railpack prepare: no provider detected",
			want:         "build planning failed: no provider detected",
		},
		{
			name:         "image build",
			errorMessage: "buildctl build: build exited 1",
			want:         "image build failed: build exited 1",
		},
		{
			name:         "pocketbase provisioning",
			errorMessage: "ensure PocketBase: container start failed",
			want:         "PocketBase provisioning failed: container start failed",
		},
		{
			name:         "app provisioning",
			errorMessage: "ensure app container: image pull failed",
			want:         "app container provisioning failed: image pull failed",
		},
		{
			name:         "app readiness",
			errorMessage: "wait for app readiness: timed out waiting for port",
			want:         "app readiness failed: timed out waiting for port",
		},
		{
			name:         "promotion state",
			errorMessage: "load current deployment: database is locked",
			want:         "promotion state load failed: database is locked",
		},
		{
			name:         "promotion state update",
			errorMessage: "promotion state update failed: deployment result is missing app container name",
			want:         "promotion state update failed: deployment result is missing app container name",
		},
		{
			name:         "promotion cleanup",
			errorMessage: `remove superseded app container "ovek-demo-app-app-dep-old": stop failed`,
			want:         `promotion cleanup failed: remove superseded app container "ovek-demo-app-app-dep-old": stop failed`,
		},
		{
			name:         "job state load",
			errorMessage: "failed to load claimed job",
			want:         "job state load failed",
		},
		{
			name:         "interrupted restart",
			errorMessage: interruptedJobErrorMessage,
			want:         interruptedJobErrorMessage,
		},
		{
			name:         "already normalized",
			errorMessage: "source fetch failed: repository not found",
			want:         "source fetch failed: repository not found",
		},
		{
			name:         "unknown message",
			errorMessage: "something unexpected happened",
			want:         "something unexpected happened",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := normalizeJobFailureMessage(test.errorMessage)
			if got != test.want {
				t.Fatalf("expected normalized error %q, got %q", test.want, got)
			}
		})
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
	if got := getProjectStatus(t, db, firstJob.ProjectName); got != projectStatusDeploying {
		t.Fatalf("expected project status %q while first job is running, got %q", projectStatusDeploying, got)
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
	if firstFinishedJob.ImageRef != "ovek-demo-app:"+firstJob.ID {
		t.Fatalf("expected first image ref %q, got %q", "ovek-demo-app:"+firstJob.ID, firstFinishedJob.ImageRef)
	}
	secondFinishedJob := waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)
	if secondFinishedJob.ImageRef != "ovek-demo-app:"+secondJob.ID {
		t.Fatalf("expected second image ref %q, got %q", "ovek-demo-app:"+secondJob.ID, secondFinishedJob.ImageRef)
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
	if got := getProjectStatus(t, db, secondJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
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
			ImageRef:                "ovek-demo-app:" + currentJob.ID,
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
	if deployment.SourceType != jobSourceTypeRepo {
		t.Fatalf("expected deployment source type %q, got %q", jobSourceTypeRepo, deployment.SourceType)
	}
	if deployment.SourceRef != "https://example.com/demo.git" {
		t.Fatalf("expected deployment source ref %q, got %q", "https://example.com/demo.git", deployment.SourceRef)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != createdJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", createdJob.ID, got)
	}
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestJobManagerPromotesImageSourceJob(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJobWithSource(db, "demo-app", jobSourceTypeImage, "ghcr.io/example/demo:2026.05.01", false)
	if err != nil {
		t.Fatalf("expected image job creation to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		if currentJob.SourceType != jobSourceTypeImage {
			t.Fatalf("expected source type %q, got %q", jobSourceTypeImage, currentJob.SourceType)
		}
		if currentJob.SourceRef != "ghcr.io/example/demo:2026.05.01" {
			t.Fatalf("expected source ref %q, got %q", "ghcr.io/example/demo:2026.05.01", currentJob.SourceRef)
		}
		if currentJob.RepoURL != "" {
			t.Fatalf("expected image job repo URL to be empty, got %q", currentJob.RepoURL)
		}

		return deploymentResult{
			LogPath:                 "/tmp/image-run.log",
			ImageRef:                currentJob.SourceRef,
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

	manager.Enqueue(createdJob.ID)

	finishedJob := waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)
	if finishedJob.ImageRef != "ghcr.io/example/demo:2026.05.01" {
		t.Fatalf("expected finished job image ref %q, got %q", "ghcr.io/example/demo:2026.05.01", finishedJob.ImageRef)
	}
	if finishedJob.SourceType != jobSourceTypeImage {
		t.Fatalf("expected finished job source type %q, got %q", jobSourceTypeImage, finishedJob.SourceType)
	}

	deployment := getDeploymentRecord(t, db, createdJob.ID)
	if deployment.ImageRef != "ghcr.io/example/demo:2026.05.01" {
		t.Fatalf("expected deployment image ref %q, got %q", "ghcr.io/example/demo:2026.05.01", deployment.ImageRef)
	}
	if deployment.SourceType != jobSourceTypeImage {
		t.Fatalf("expected deployment source type %q, got %q", jobSourceTypeImage, deployment.SourceType)
	}
	if deployment.SourceRef != "ghcr.io/example/demo:2026.05.01" {
		t.Fatalf("expected deployment source ref %q, got %q", "ghcr.io/example/demo:2026.05.01", deployment.SourceRef)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != createdJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", createdJob.ID, got)
	}
}

func TestJobManagerRecoversInterruptedRunningJobsOnStart(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, started_at = ?
		 WHERE id = ?`,
		jobStatusRunning,
		"2026-04-12T00:00:00Z",
		createdJob.ID,
	); err != nil {
		t.Fatalf("expected job running-state seed to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		t.Fatalf("expected interrupted running job %q not to be reprocessed", currentJob.ID)
		return deploymentResult{}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	job := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if job.ErrorMessage != interruptedJobErrorMessage {
		t.Fatalf("expected interruption error %q, got %q", interruptedJobErrorMessage, job.ErrorMessage)
	}
	if job.StartedAt != "2026-04-12T00:00:00Z" {
		t.Fatalf("expected startedAt to remain %q, got %q", "2026-04-12T00:00:00Z", job.StartedAt)
	}
	if job.FinishedAt == "" {
		t.Fatal("expected finishedAt to be set")
	}
	assertCurrentDeploymentUnset(t, db, createdJob.ProjectName)
	assertDeploymentMissing(t, db, createdJob.ID)
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusFailed {
		t.Fatalf("expected project status %q, got %q", projectStatusFailed, got)
	}
}

func TestJobManagerKeepsProjectRunningWhenRecoveringInterruptedJobOverExistingRuntime(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, started_at = ?
		 WHERE id = ?`,
		jobStatusRunning,
		"2026-04-12T00:00:00Z",
		createdJob.ID,
	); err != nil {
		t.Fatalf("expected job running-state seed to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		t.Fatalf("expected interrupted running job %q not to be reprocessed", currentJob.ID)
		return deploymentResult{}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	job := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if job.ErrorMessage != interruptedJobErrorMessage {
		t.Fatalf("expected interruption error %q, got %q", interruptedJobErrorMessage, job.ErrorMessage)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != "dep-current" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-current", got)
	}
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestJobManagerKeepsProjectRunningWhenNewDeploymentFailsOverExistingRuntime(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                "ovek-demo-app:" + currentJob.ID,
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
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != "dep-current" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-current", got)
	}
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestJobManagerCleansUpSupersededDeploymentImageAfterSuccessfulPromotion(t *testing.T) {
	db := newTestDB(t)
	firstJob, err := createQueuedJob(db, "demo-app", "https://example.com/first.git")
	if err != nil {
		t.Fatalf("expected first job creation to succeed, got error: %v", err)
	}
	secondJob, err := createQueuedJob(db, "demo-app", "https://example.com/second.git")
	if err != nil {
		t.Fatalf("expected second job creation to succeed, got error: %v", err)
	}

	artifactCleaner := &fakeRegistryArtifactCleaner{}
	manager := newJobManager(db, processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		result := deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                "localhost:5001/ovek-demo-app:" + job.ID,
			AppContainerName:        appContainerName(job.ProjectName, job.ID),
			NetworkName:             projectNetworkName(job.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(job.ProjectName),
		}
		if job.ID == secondJob.ID {
			result.SupersededDeploymentID = firstJob.ID
		}

		return result, nil
	}), artifactCleaner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(firstJob.ID)
	manager.Enqueue(secondJob.ID)

	waitForJobStatus(t, db, firstJob.ID, jobStatusSucceeded)
	waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)
	waitForArtifactCleanup(t, artifactCleaner, 1)

	wantImageRefs := []string{"localhost:5001/ovek-demo-app:" + firstJob.ID}
	if !reflect.DeepEqual(artifactCleaner.cleanedRefs, wantImageRefs) {
		t.Fatalf("expected cleaned refs %#v, got %#v", wantImageRefs, artifactCleaner.cleanedRefs)
	}
}

func TestJobManagerLogsArtifactCleanupFailuresButKeepsSuccessfulJobState(t *testing.T) {
	db := newTestDB(t)
	firstJob, err := createQueuedJob(db, "demo-app", "https://example.com/first.git")
	if err != nil {
		t.Fatalf("expected first job creation to succeed, got error: %v", err)
	}
	secondJob, err := createQueuedJob(db, "demo-app", "https://example.com/second.git")
	if err != nil {
		t.Fatalf("expected second job creation to succeed, got error: %v", err)
	}

	artifactCleaner := &fakeRegistryArtifactCleaner{err: errors.New("registry delete failed")}
	manager := newJobManager(db, processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		result := deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                "localhost:5001/ovek-demo-app:" + job.ID,
			AppContainerName:        appContainerName(job.ProjectName, job.ID),
			NetworkName:             projectNetworkName(job.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(job.ProjectName),
		}
		if job.ID == secondJob.ID {
			result.SupersededDeploymentID = firstJob.ID
		}

		return result, nil
	}), artifactCleaner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	logs := captureWorkerLogs(t, func() {
		manager.Enqueue(firstJob.ID)
		manager.Enqueue(secondJob.ID)
		waitForJobStatus(t, db, firstJob.ID, jobStatusSucceeded)
		waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)
		waitForArtifactCleanup(t, artifactCleaner, 1)
	})

	if !strings.Contains(logs, `warning: failed to clean up superseded deployment "`) {
		t.Fatalf("expected cleanup warning log, got %q", logs)
	}
	if got := getProjectCurrentDeploymentID(t, db, secondJob.ProjectName); got != secondJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", secondJob.ID, got)
	}
	if got := getProjectStatus(t, db, secondJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestJobManagerSkipsArtifactCleanupForSupersededImageDeployment(t *testing.T) {
	db := newTestDB(t)
	firstJob, err := createQueuedJobWithSource(db, "demo-app", jobSourceTypeImage, "ghcr.io/example/demo:first", false)
	if err != nil {
		t.Fatalf("expected first image job creation to succeed, got error: %v", err)
	}
	secondJob, err := createQueuedJob(db, "demo-app", "https://example.com/second.git")
	if err != nil {
		t.Fatalf("expected second job creation to succeed, got error: %v", err)
	}

	artifactCleaner := &fakeRegistryArtifactCleaner{}
	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		imageRef := "localhost:5001/ovek-demo-app:" + currentJob.ID
		if currentJob.SourceType == jobSourceTypeImage {
			imageRef = currentJob.SourceRef
		}
		result := deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                imageRef,
			AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
			NetworkName:             projectNetworkName(currentJob.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
		}
		if currentJob.ID == secondJob.ID {
			result.SupersededDeploymentID = firstJob.ID
		}

		return result, nil
	}), artifactCleaner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(firstJob.ID)
	manager.Enqueue(secondJob.ID)

	waitForJobStatus(t, db, firstJob.ID, jobStatusSucceeded)
	waitForJobStatus(t, db, secondJob.ID, jobStatusSucceeded)

	if len(artifactCleaner.cleanedRefs) != 0 {
		t.Fatalf("expected no artifact cleanup for superseded image deployment, got %#v", artifactCleaner.cleanedRefs)
	}
}

func TestJobManagerDoesNotCleanUpArtifactsForFailedJobs(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	artifactCleaner := &fakeRegistryArtifactCleaner{}
	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:                "/tmp/build.log",
			ImageRef:               "localhost:5001/ovek-demo-app:" + currentJob.ID,
			SupersededDeploymentID: "dep-current",
		}, errors.New("build failed")
	}), artifactCleaner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)
	waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)

	if len(artifactCleaner.cleanedRefs) != 0 {
		t.Fatalf("expected no artifact cleanup on failure, got %#v", artifactCleaner.cleanedRefs)
	}
}

func TestJobManagerSyncsIngressAfterSuccessfulJob(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	ingress := &recordingProjectIngressManager{}
	manager := newJobManager(db, processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:                 "/tmp/build.log",
			ImageRef:                "localhost:5001/ovek-demo-app:" + currentJob.ID,
			AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
			NetworkName:             projectNetworkName(currentJob.ProjectName),
			PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
		}, nil
	}))
	manager.ingress = ingress

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)
	waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)

	if !reflect.DeepEqual(ingress.syncedProjects, []string{"demo-app"}) {
		t.Fatalf("expected synced projects %#v, got %#v", []string{"demo-app"}, ingress.syncedProjects)
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
		ImageRef:                "ovek-" + currentJob.ProjectName + ":" + currentJob.ID,
		AppContainerName:        appContainerName(currentJob.ProjectName, currentJob.ID),
		NetworkName:             projectNetworkName(currentJob.ProjectName),
		PocketBaseContainerName: pocketBaseContainerName(currentJob.ProjectName),
	}
}

func getDeploymentRecord(t *testing.T, db *sql.DB, deploymentID string) deploymentRecord {
	t.Helper()

	var record deploymentRecord
	var sourceType sql.NullString
	var sourceRef sql.NullString
	var configRevisionID sql.NullString
	err := db.QueryRow(
		`SELECT id, project_name, image_ref, source_type, source_ref, app_container_name, network_name, pb_container_name, status, created_at, config_revision_id
		 FROM deployments
		 WHERE id = ?`,
		deploymentID,
	).Scan(
		&record.ID,
		&record.ProjectName,
		&record.ImageRef,
		&sourceType,
		&sourceRef,
		&record.AppContainerName,
		&record.NetworkName,
		&record.PocketBaseContainerName,
		&record.Status,
		&record.CreatedAt,
		&configRevisionID,
	)
	if err != nil {
		t.Fatalf("expected deployment %q lookup to succeed, got error: %v", deploymentID, err)
	}
	if sourceType.Valid {
		record.SourceType = sourceType.String
	}
	if sourceRef.Valid {
		record.SourceRef = sourceRef.String
	}
	if configRevisionID.Valid {
		record.ConfigRevisionID = configRevisionID.String
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

func getProjectStatus(t *testing.T, db *sql.DB, projectName string) string {
	t.Helper()

	var status string
	if err := db.QueryRow(
		"SELECT status FROM projects WHERE name = ?",
		projectName,
	).Scan(&status); err != nil {
		t.Fatalf("expected project %q status lookup to succeed, got error: %v", projectName, err)
	}

	return status
}

func captureWorkerLogs(t *testing.T, run func()) string {
	t.Helper()

	var buffer bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
	})

	run()
	log.SetOutput(originalWriter)

	return buffer.String()
}

func waitForArtifactCleanup(t *testing.T, cleaner *fakeRegistryArtifactCleaner, wantCount int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(cleaner.cleanedRefs) >= wantCount {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected at least %d cleaned refs, got %#v", wantCount, cleaner.cleanedRefs)
}
