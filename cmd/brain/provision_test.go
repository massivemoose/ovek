package main

import (
	"context"
	"errors"
	"testing"
)

func TestManagedDeploymentProcessorEnsuresPocketBaseAfterBuild(t *testing.T) {
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "alces-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{}
	processor := newManagedDeploymentProcessor(builder, provisioner, "/srv/alces/projects", defaultPocketBaseImage)

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
	if result.ImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-123", result.ImageRef)
	}
	if result.AppContainerName != "alces-demo-app-app-job-123" {
		t.Fatalf("expected app container name %q, got %q", "alces-demo-app-app-job-123", result.AppContainerName)
	}
	if result.NetworkName != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", result.NetworkName)
	}
	if result.PocketBaseContainerName != "alces-demo-app-pb" {
		t.Fatalf("expected PocketBase container name %q, got %q", "alces-demo-app-pb", result.PocketBaseContainerName)
	}
	if provisioner.projectName != "demo-app" {
		t.Fatalf("expected provisioned project %q, got %q", "demo-app", provisioner.projectName)
	}
	if provisioner.image != defaultPocketBaseImage {
		t.Fatalf("expected provisioned image %q, got %q", defaultPocketBaseImage, provisioner.image)
	}
	if provisioner.projectsHostDataDir != "/srv/alces/projects" {
		t.Fatalf("expected projects host data dir %q, got %q", "/srv/alces/projects", provisioner.projectsHostDataDir)
	}
	if provisioner.appJob.ID != "job-123" {
		t.Fatalf("expected app job ID %q, got %q", "job-123", provisioner.appJob.ID)
	}
	if provisioner.appImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected app image ref %q, got %q", "alces-demo-app:job-123", provisioner.appImageRef)
	}
	if provisioner.readyCalls != 1 {
		t.Fatalf("expected readiness to be checked once, got %d", provisioner.readyCalls)
	}
	if provisioner.readyJob.ID != "job-123" {
		t.Fatalf("expected readiness job ID %q, got %q", "job-123", provisioner.readyJob.ID)
	}
	if got := provisioner.sequence; len(got) != 3 || got[0] != "pocketbase" || got[1] != "app" || got[2] != "ready" {
		t.Fatalf("expected provisioner order [pocketbase app ready], got %#v", got)
	}
}

func TestManagedDeploymentProcessorReturnsBuildFailureWithoutProvisioning(t *testing.T) {
	builder := processorFunc(func(_ context.Context, _ job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "alces-demo-app:job-123",
		}, errors.New("build failed")
	})
	provisioner := &fakeProjectProvisioner{}
	processor := newManagedDeploymentProcessor(builder, provisioner, "/srv/alces/projects", defaultPocketBaseImage)

	_, err := processor.Process(context.Background(), job{ProjectName: "demo-app"})
	if err == nil {
		t.Fatal("expected build failure")
	}
	if provisioner.calls != 0 {
		t.Fatalf("expected provisioner not to be called, got %d calls", provisioner.calls)
	}
}

func TestManagedDeploymentProcessorReturnsProvisioningFailure(t *testing.T) {
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "alces-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		err: errors.New("provisioning failed"),
	}
	processor := newManagedDeploymentProcessor(builder, provisioner, "/srv/alces/projects", defaultPocketBaseImage)

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
	if result.ImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-123", result.ImageRef)
	}
}

func TestManagedDeploymentProcessorReturnsAppProvisioningFailure(t *testing.T) {
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "alces-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		appErr: errors.New("app provisioning failed"),
	}
	processor := newManagedDeploymentProcessor(builder, provisioner, "/srv/alces/projects", defaultPocketBaseImage)

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
	if result.ImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-123", result.ImageRef)
	}
}

func TestManagedDeploymentProcessorReturnsReadinessFailure(t *testing.T) {
	builder := processorFunc(func(_ context.Context, job job) (deploymentResult, error) {
		return deploymentResult{
			LogPath:  "/tmp/job.log",
			ImageRef: "alces-demo-app:" + job.ID,
		}, nil
	})
	provisioner := &fakeProjectProvisioner{
		readyErr: errors.New("timed out waiting for port"),
	}
	processor := newManagedDeploymentProcessor(builder, provisioner, "/srv/alces/projects", defaultPocketBaseImage)

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
	if result.ImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-123", result.ImageRef)
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
