package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestManagedDeploymentProcessorEnsuresPocketBaseAfterBuild(t *testing.T) {
	db := newTestDB(t)
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	})
	if err != nil {
		t.Fatalf("expected deployment processing to succeed, got error: %v", err)
	}

	if result.LogPath != "/tmp/job.log" {
		t.Fatalf("expected log path %q, got %q", "/tmp/job.log", result.LogPath)
	}
	if result.ImageRef != "ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:job-123", result.ImageRef)
	}
	if result.AppContainerName != "ovek-demo-app-app-job-123" {
		t.Fatalf("expected app container name %q, got %q", "ovek-demo-app-app-job-123", result.AppContainerName)
	}
	if result.NetworkName != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", result.NetworkName)
	}
	if result.PocketBaseContainerName != "ovek-demo-app-pb" {
		t.Fatalf("expected PocketBase container name %q, got %q", "ovek-demo-app-pb", result.PocketBaseContainerName)
	}
	if provisioner.projectName != "demo-app" {
		t.Fatalf("expected provisioned project %q, got %q", "demo-app", provisioner.projectName)
	}
	if provisioner.image != defaultPocketBaseImage {
		t.Fatalf("expected provisioned image %q, got %q", defaultPocketBaseImage, provisioner.image)
	}
	if provisioner.projectsHostDataDir != "/srv/ovek/projects" {
		t.Fatalf("expected projects host data dir %q, got %q", "/srv/ovek/projects", provisioner.projectsHostDataDir)
	}
	if provisioner.appJob.ID != "job-123" {
		t.Fatalf("expected app job ID %q, got %q", "job-123", provisioner.appJob.ID)
	}
	if provisioner.appImageRef != "ovek-demo-app:job-123" {
		t.Fatalf("expected app image ref %q, got %q", "ovek-demo-app:job-123", provisioner.appImageRef)
	}
	if provisioner.readyCalls != 1 {
		t.Fatalf("expected readiness to be checked once, got %d", provisioner.readyCalls)
	}
	if provisioner.readyJob.ID != "job-123" {
		t.Fatalf("expected readiness job ID %q, got %q", "job-123", provisioner.readyJob.ID)
	}
	if provisioner.removeCalls != 0 {
		t.Fatalf("expected superseded app removal not to be called, got %d calls", provisioner.removeCalls)
	}
	if got := provisioner.sequence; len(got) != 3 || got[0] != "pocketbase" || got[1] != "app" || got[2] != "ready" {
		t.Fatalf("expected provisioner order [pocketbase app ready], got %#v", got)
	}
}

func TestManagedDeploymentProcessorReturnsBuildFailureWithoutProvisioning(t *testing.T) {
	db := newTestDB(t)
	builder := processorFunc(func(_ context.Context, _ job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:job-123",
		}, errors.New("build failed")
	})
	provisioner := &fakeProjectProvisioner{}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	_, err := processor.Process(context.Background(), job{ProjectName: "demo-app"})
	if err == nil {
		t.Fatal("expected build failure")
	}
	if provisioner.calls != 0 {
		t.Fatalf("expected provisioner not to be called, got %d calls", provisioner.calls)
	}
}

func TestManagedDeploymentProcessorReturnsProvisioningFailure(t *testing.T) {
	db := newTestDB(t)
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		err: errors.New("provisioning failed"),
	}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected provisioning failure")
	}
	if err.Error() != "ensure PocketBase: provisioning failed" {
		t.Fatalf("expected provisioning error %q, got %q", "ensure PocketBase: provisioning failed", err.Error())
	}
	if result.ImageRef != "ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:job-123", result.ImageRef)
	}
}

func TestManagedDeploymentProcessorReturnsAppProvisioningFailure(t *testing.T) {
	db := newTestDB(t)
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		appErr: errors.New("app provisioning failed"),
	}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected app provisioning failure")
	}
	if err.Error() != "ensure app container: app provisioning failed" {
		t.Fatalf("expected app provisioning error %q, got %q", "ensure app container: app provisioning failed", err.Error())
	}
	if result.ImageRef != "ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:job-123", result.ImageRef)
	}
}

func TestManagedDeploymentProcessorReturnsReadinessFailure(t *testing.T) {
	db := newTestDB(t)
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		readyErr: errors.New("timed out waiting for port"),
	}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected readiness failure")
	}
	if err.Error() != "wait for app readiness: timed out waiting for port" {
		t.Fatalf("expected readiness error %q, got %q", "wait for app readiness: timed out waiting for port", err.Error())
	}
	if result.ImageRef != "ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:job-123", result.ImageRef)
	}
}

func TestManagedDeploymentProcessorRemovesSupersededAppAfterReadiness(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-old",
		AppContainerName:        "ovek-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-08T00:00:00Z",
	})

	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
	})
	if err != nil {
		t.Fatalf("expected deployment processing to succeed, got error: %v", err)
	}
	if result.SupersededDeploymentID != "dep-old" {
		t.Fatalf("expected superseded deployment ID %q, got %q", "dep-old", result.SupersededDeploymentID)
	}
	if provisioner.removeCalls != 1 {
		t.Fatalf("expected superseded app removal to be called once, got %d calls", provisioner.removeCalls)
	}
	if got := len(provisioner.removedDeployments); got != 1 {
		t.Fatalf("expected 1 removed deployment record, got %d", got)
	}
	if provisioner.removedDeployments[0].ID != "dep-old" {
		t.Fatalf("expected removed deployment ID %q, got %q", "dep-old", provisioner.removedDeployments[0].ID)
	}
	if got := provisioner.sequence; len(got) != 4 || got[0] != "pocketbase" || got[1] != "app" || got[2] != "ready" || got[3] != "remove" {
		t.Fatalf("expected provisioner order [pocketbase app ready remove], got %#v", got)
	}
}

func TestManagedDeploymentProcessorReturnsSupersededAppRemovalFailure(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-old",
		AppContainerName:        "ovek-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-08T00:00:00Z",
	})

	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "ovek-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		removeErrs: []error{errors.New("stop failed"), nil},
	}
	processor := newManagedDeploymentProcessor(db, builder, provisioner, "/srv/ovek/projects", defaultPocketBaseImage)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected superseded app removal failure")
	}
	if err.Error() != "remove superseded app container \"ovek-demo-app-app-dep-old\": stop failed" {
		t.Fatalf("expected superseded app removal error, got %q", err.Error())
	}
	if result.SupersededDeploymentID != "" {
		t.Fatalf("expected superseded deployment ID to remain unset on failure, got %q", result.SupersededDeploymentID)
	}
	if provisioner.removeCalls != 2 {
		t.Fatalf("expected old app removal plus rollback removal, got %d calls", provisioner.removeCalls)
	}
	if got := len(provisioner.removedDeployments); got != 2 {
		t.Fatalf("expected 2 removed deployment records, got %d", got)
	}
	if provisioner.removedDeployments[0].ID != "dep-old" {
		t.Fatalf("expected first removed deployment ID %q, got %q", "dep-old", provisioner.removedDeployments[0].ID)
	}
	if provisioner.removedDeployments[1].ID != "job-123" {
		t.Fatalf("expected rollback deployment ID %q, got %q", "job-123", provisioner.removedDeployments[1].ID)
	}
}

type fakeProjectProvisioner struct {
	calls               int
	projectName         string
	image               string
	projectsHostDataDir string
	err                 error
	appCalls            int
	appJob              job
	appImageRef         string
	appErr              error
	sequence            []string
	readyCalls          int
	readyJob            job
	readyErr            error
	removeCalls         int
	removedDeployments  []deploymentRecord
	removeErr           error
	removeErrs          []error
}

func (provisioner *fakeProjectProvisioner) EnsureProjectPocketBase(_ context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	provisioner.calls++
	provisioner.projectName = projectName
	provisioner.image = image
	provisioner.projectsHostDataDir = projectsHostDataDir
	provisioner.sequence = append(provisioner.sequence, "pocketbase")

	if provisioner.err != nil {
		return "", provisioner.err
	}

	return "container-123", nil
}

func (provisioner *fakeProjectProvisioner) EnsureProjectApp(_ context.Context, currentJob job, imageRef string) (string, error) {
	provisioner.appCalls++
	provisioner.appJob = currentJob
	provisioner.appImageRef = imageRef
	provisioner.sequence = append(provisioner.sequence, "app")

	if provisioner.appErr != nil {
		return "", provisioner.appErr
	}

	return "app-container-123", nil
}

func (provisioner *fakeProjectProvisioner) WaitForProjectAppReady(_ context.Context, currentJob job) error {
	provisioner.readyCalls++
	provisioner.readyJob = currentJob
	provisioner.sequence = append(provisioner.sequence, "ready")

	return provisioner.readyErr
}

func (provisioner *fakeProjectProvisioner) RemoveProjectApp(_ context.Context, deployment deploymentRecord) error {
	provisioner.removeCalls++
	provisioner.removedDeployments = append(provisioner.removedDeployments, deployment)
	provisioner.sequence = append(provisioner.sequence, "remove")

	if len(provisioner.removeErrs) > 0 {
		err := provisioner.removeErrs[0]
		provisioner.removeErrs = provisioner.removeErrs[1:]
		return err
	}

	return provisioner.removeErr
}

func seedCurrentDeployment(t *testing.T, db *sql.DB, deployment deploymentRecord) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)",
		deployment.ProjectName,
		projectStatusIdle,
		deployment.CreatedAt,
	); err != nil {
		t.Fatalf("expected project seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO deployments(id, project_name, image_ref, app_container_name, network_name, pb_container_name, status, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		deployment.ID,
		deployment.ProjectName,
		deployment.ImageRef,
		deployment.AppContainerName,
		deployment.NetworkName,
		deployment.PocketBaseContainerName,
		deployment.Status,
		deployment.CreatedAt,
	); err != nil {
		t.Fatalf("expected deployment seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE projects SET current_deployment_id = ? WHERE name = ?",
		deployment.ID,
		deployment.ProjectName,
	); err != nil {
		t.Fatalf("expected project current deployment seed to succeed, got error: %v", err)
	}
	if err := syncProjectStatus(db, deployment.ProjectName); err != nil {
		t.Fatalf("expected project status seed to succeed, got error: %v", err)
	}
}
