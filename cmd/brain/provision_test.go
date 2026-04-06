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
	if provisioner.projectName != "demo-app" {
		t.Fatalf("expected provisioned project %q, got %q", "demo-app", provisioner.projectName)
	}
	if provisioner.image != defaultPocketBaseImage {
		t.Fatalf("expected provisioned image %q, got %q", defaultPocketBaseImage, provisioner.image)
	}
	if provisioner.projectsHostDataDir != "/srv/alces/projects" {
		t.Fatalf("expected projects host data dir %q, got %q", "/srv/alces/projects", provisioner.projectsHostDataDir)
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

type fakeProjectProvisioner struct {
	calls               int
	projectName         string
	image               string
	projectsHostDataDir string
	err                 error
}

func (provisioner *fakeProjectProvisioner) EnsureProjectPocketBase(_ context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	provisioner.calls++
	provisioner.projectName = projectName
	provisioner.image = image
	provisioner.projectsHostDataDir = projectsHostDataDir

	if provisioner.err != nil {
		return "", provisioner.err
	}

	return "container-123", nil
}
