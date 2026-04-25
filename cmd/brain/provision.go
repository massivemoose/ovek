package main

import (
	"context"
	"database/sql"
	"fmt"
)

type projectProvisioner interface {
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	EnsureProjectApp(ctx context.Context, job job, imageRef string) (string, error)
	WaitForProjectAppReady(ctx context.Context, job job) error
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
}

type managedDeploymentProcessor struct {
	db                  *sql.DB
	builder             deploymentProcessor
	provisioner         projectProvisioner
	projectsHostDataDir string
	pocketBaseImage     string
}

func newManagedDeploymentProcessor(db *sql.DB, builder deploymentProcessor, provisioner projectProvisioner, projectsHostDataDir string, pocketBaseImage string) managedDeploymentProcessor {
	return managedDeploymentProcessor{
		db:                  db,
		builder:             builder,
		provisioner:         provisioner,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
	}
}

func (processor managedDeploymentProcessor) Process(ctx context.Context, job job) (result deploymentResult, err error) {
	result, err = processor.builder.Process(ctx, job)
	if err != nil {
		return result, err
	}

	appendJobLogLine(result.LogPath, "lifecycle: build succeeded")
	defer func() {
		if err != nil {
			appendJobLogError(result.LogPath, err.Error())
		}
	}()

	appendJobLogLine(result.LogPath, "lifecycle: provisioning PocketBase")
	if _, err := processor.provisioner.EnsureProjectPocketBase(ctx, job.ProjectName, processor.pocketBaseImage, processor.projectsHostDataDir); err != nil {
		return result, fmt.Errorf("ensure PocketBase: %w", err)
	}
	appendJobLogLine(result.LogPath, "lifecycle: starting app container")
	if _, err := processor.provisioner.EnsureProjectApp(ctx, job, result.ImageRef); err != nil {
		return result, fmt.Errorf("ensure app container: %w", err)
	}
	appendJobLogLine(result.LogPath, "lifecycle: waiting for app readiness")
	if err := processor.provisioner.WaitForProjectAppReady(ctx, job); err != nil {
		return result, fmt.Errorf("wait for app readiness: %w", err)
	}
	appendJobLogLine(result.LogPath, "lifecycle: app ready")

	result.AppContainerName = appContainerName(job.ProjectName, job.ID)
	result.NetworkName = projectNetworkName(job.ProjectName)
	result.PocketBaseContainerName = pocketBaseContainerName(job.ProjectName)

	appendJobLogLine(result.LogPath, "lifecycle: checking current deployment")
	currentDeployment, found, err := getProjectCurrentDeployment(processor.db, job.ProjectName)
	if err != nil {
		return result, fmt.Errorf("load current deployment: %w", err)
	}
	if !found || currentDeployment.ID == job.ID {
		appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared")
		return result, nil
	}
	if currentDeployment.AppContainerName == result.AppContainerName {
		result.SupersededDeploymentID = currentDeployment.ID
		appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared")
		return result, nil
	}
	appendJobLogLine(result.LogPath, "lifecycle: removing superseded app "+currentDeployment.ID)
	if err := processor.provisioner.RemoveProjectApp(ctx, currentDeployment); err != nil {
		if rollbackErr := processor.removeCurrentJobApp(ctx, job, result); rollbackErr != nil {
			return result, fmt.Errorf(
				"remove superseded app container %q: %w; rollback new app container %q: %v",
				currentDeployment.AppContainerName,
				err,
				result.AppContainerName,
				rollbackErr,
			)
		}

		return result, fmt.Errorf("remove superseded app container %q: %w", currentDeployment.AppContainerName, err)
	}

	result.SupersededDeploymentID = currentDeployment.ID
	appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared")
	return result, nil
}

func (processor managedDeploymentProcessor) removeCurrentJobApp(ctx context.Context, job job, result deploymentResult) error {
	if result.AppContainerName == "" {
		return nil
	}

	return processor.provisioner.RemoveProjectApp(ctx, deploymentRecord{
		ID:               job.ID,
		ProjectName:      job.ProjectName,
		AppContainerName: result.AppContainerName,
	})
}
