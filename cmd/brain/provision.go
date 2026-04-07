package main

import (
	"context"
	"fmt"
)

type projectProvisioner interface {
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	EnsureProjectApp(ctx context.Context, job job, imageRef string) (string, error)
}

type managedDeploymentProcessor struct {
	builder             deploymentProcessor
	provisioner         projectProvisioner
	projectsHostDataDir string
	pocketBaseImage     string
}

func newManagedDeploymentProcessor(builder deploymentProcessor, provisioner projectProvisioner, projectsHostDataDir string, pocketBaseImage string) managedDeploymentProcessor {
	return managedDeploymentProcessor{
		builder:             builder,
		provisioner:         provisioner,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
	}
}

func (processor managedDeploymentProcessor) Process(ctx context.Context, job job) (deploymentResult, error) {
	result, err := processor.builder.Process(ctx, job)
	if err != nil {
		return result, err
	}

	if _, err := processor.provisioner.EnsureProjectPocketBase(ctx, job.ProjectName, processor.pocketBaseImage, processor.projectsHostDataDir); err != nil {
		return result, fmt.Errorf("ensure PocketBase: %w", err)
	}
	if _, err := processor.provisioner.EnsureProjectApp(ctx, job, result.ImageRef); err != nil {
		return result, fmt.Errorf("ensure app container: %w", err)
	}

	return result, nil
}
