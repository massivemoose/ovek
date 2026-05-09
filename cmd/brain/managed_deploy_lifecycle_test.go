package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestManagedDeploymentLifecycleSucceeds(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	provisioner := &fakeProjectProvisioner{}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), nil
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)

	finishedJob := waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)
	if finishedJob.ErrorMessage != "" {
		t.Fatalf("expected no job error message, got %q", finishedJob.ErrorMessage)
	}
	if finishedJob.LogPath != "/tmp/build.log" {
		t.Fatalf("expected log path %q, got %q", "/tmp/build.log", finishedJob.LogPath)
	}
	if finishedJob.ImageRef != "localhost:5001/ovek-demo-app:"+createdJob.ID {
		t.Fatalf("expected image ref %q, got %q", "localhost:5001/ovek-demo-app:"+createdJob.ID, finishedJob.ImageRef)
	}

	deployment := getDeploymentRecord(t, db, createdJob.ID)
	if deployment.Status != deploymentStatusSucceeded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSucceeded, deployment.Status)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != createdJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", createdJob.ID, got)
	}
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
	if provisioner.removeCalls != 0 {
		t.Fatalf("expected no superseded deployment removal, got %d calls", provisioner.removeCalls)
	}
}

func TestManagedDeploymentLifecycleInjectsCapturedProjectConfigRevision(t *testing.T) {
	db := newTestDB(t)
	configStore := newTestProjectConfigStore(t, db)
	if _, err := configStore.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "dev"); err != nil {
		t.Fatalf("expected env set to succeed, got error: %v", err)
	}
	secretMutation, err := configStore.SetEnvironmentEntry(context.Background(), "demo-app", "PB_SUPERUSER_PASSWORD", "secret-pass", true, "dev")
	if err != nil {
		t.Fatalf("expected secret set to succeed, got error: %v", err)
	}

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}
	if createdJob.ConfigRevisionID != secretMutation.RevisionID {
		t.Fatalf("expected job config revision %q, got %q", secretMutation.RevisionID, createdJob.ConfigRevisionID)
	}

	provisioner := &fakeProjectProvisioner{}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), nil
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
		configStore,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)
	finishedJob := waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)
	if finishedJob.ConfigRevisionID != secretMutation.RevisionID {
		t.Fatalf("expected finished job config revision %q, got %q", secretMutation.RevisionID, finishedJob.ConfigRevisionID)
	}

	wantEnv := []string{
		"PB_SUPERUSER_PASSWORD=secret-pass",
		"PUBLIC_SITE_URL=https://example.com",
	}
	if len(provisioner.appEnv) != len(wantEnv) || strings.Join(provisioner.appEnv, "\n") != strings.Join(wantEnv, "\n") {
		t.Fatalf("expected provisioner app env %#v, got %#v", wantEnv, provisioner.appEnv)
	}

	deployment := getDeploymentRecord(t, db, createdJob.ID)
	if deployment.ConfigRevisionID != secretMutation.RevisionID {
		t.Fatalf("expected deployment config revision %q, got %q", secretMutation.RevisionID, deployment.ConfigRevisionID)
	}
}

func TestManagedDeploymentLifecycleFailsDuringBuild(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	provisioner := &fakeProjectProvisioner{}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), errors.New("git clone: repository not found")
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)

	finishedJob := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if finishedJob.ErrorMessage != "source fetch failed: repository not found" {
		t.Fatalf("expected normalized job error %q, got %q", "source fetch failed: repository not found", finishedJob.ErrorMessage)
	}
	if provisioner.calls != 0 || provisioner.appCalls != 0 || provisioner.readyCalls != 0 {
		t.Fatalf("expected provisioner not to be called, got calls=%d appCalls=%d readyCalls=%d", provisioner.calls, provisioner.appCalls, provisioner.readyCalls)
	}
	assertDeploymentMissing(t, db, createdJob.ID)
	assertCurrentDeploymentUnset(t, db, createdJob.ProjectName)
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusFailed {
		t.Fatalf("expected project status %q, got %q", projectStatusFailed, got)
	}
}

func TestManagedDeploymentLifecycleKeepsCurrentRuntimeWhenReadinessFails(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-18T00:00:00Z",
	})
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	provisioner := &fakeProjectProvisioner{
		readyErr: errors.New("timed out waiting for port"),
	}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), nil
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)

	finishedJob := waitForJobStatus(t, db, createdJob.ID, jobStatusFailed)
	if finishedJob.ErrorMessage != "app readiness failed: timed out waiting for port" {
		t.Fatalf("expected normalized job error %q, got %q", "app readiness failed: timed out waiting for port", finishedJob.ErrorMessage)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != "dep-current" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-current", got)
	}
	if got := getProjectStatus(t, db, createdJob.ProjectName); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
	if provisioner.removeCalls != 0 {
		t.Fatalf("expected no superseded runtime removal, got %d calls", provisioner.removeCalls)
	}
	assertDeploymentMissing(t, db, createdJob.ID)
}

func TestManagedDeploymentLifecycleSupersedesExistingRuntime(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-18T00:00:00Z",
	})
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	provisioner := &fakeProjectProvisioner{}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), nil
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)
	waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)

	if got := getDeploymentRecord(t, db, "dep-current").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected previous deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
	if got := getProjectCurrentDeploymentID(t, db, createdJob.ProjectName); got != createdJob.ID {
		t.Fatalf("expected current deployment ID %q, got %q", createdJob.ID, got)
	}
	if provisioner.removeCalls != 1 {
		t.Fatalf("expected one superseded runtime removal, got %d calls", provisioner.removeCalls)
	}
	if got := len(provisioner.removedDeployments); got != 1 || provisioner.removedDeployments[0].ID != "dep-current" {
		t.Fatalf("expected removed deployment %#v, got %#v", []string{"dep-current"}, provisioner.removedDeployments)
	}
}

func TestManagedDeploymentLifecycleCleanupReturnsProjectToIdle(t *testing.T) {
	db := newTestDB(t)
	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	provisioner := &fakeProjectProvisioner{}
	manager := newJobManager(db, newManagedDeploymentProcessor(
		db,
		processorFunc(func(_ context.Context, currentJob job) (deploymentResult, error) {
			return successfulManagedBuildResult(currentJob), nil
		}),
		provisioner,
		"/srv/ovek/projects",
		defaultPocketBaseImage,
	))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected manager to start, got error: %v", err)
	}

	manager.Enqueue(createdJob.ID)
	waitForJobStatus(t, db, createdJob.ID, jobStatusSucceeded)

	deployment := getDeploymentRecord(t, db, createdJob.ID)
	cleanupRuntime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            deployment.ID,
					ProjectName:             deployment.ProjectName,
					AppContainerName:        deployment.AppContainerName,
					ImageRef:                deployment.ImageRef,
					NetworkName:             deployment.NetworkName,
					PocketBaseContainerName: deployment.PocketBaseContainerName,
				},
			},
		},
	}

	if err := newManagedProjectCleaner(db, cleanupRuntime, defaultDataDir).Cleanup(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getDeploymentRecord(t, db, createdJob.ID).Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected deployment status %q after cleanup, got %q", deploymentStatusSuperseded, got)
	}
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusIdle {
		t.Fatalf("expected project status %q after cleanup, got %q", projectStatusIdle, got)
	}
}

func successfulManagedBuildResult(currentJob job) deploymentResult {
	return deploymentResult{
		LogPath:  "/tmp/build.log",
		ImageRef: "localhost:5001/ovek-demo-app:" + currentJob.ID,
	}
}
