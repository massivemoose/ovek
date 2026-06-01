package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type projectProvisioner interface {
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	EnsureProjectApp(ctx context.Context, job job, imageRef string, env []string) (string, error)
	WaitForProjectAppReady(ctx context.Context, job job) error
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
}

type managedDeploymentProcessor struct {
	db                  *sql.DB
	builder             deploymentProcessor
	provisioner         projectProvisioner
	projectsHostDataDir string
	pocketBaseImage     string
	configStore         projectConfigStore
	appBrainURL         string
}

func newManagedDeploymentProcessor(db *sql.DB, builder deploymentProcessor, provisioner projectProvisioner, projectsHostDataDir string, pocketBaseImage string, stores ...projectConfigStore) managedDeploymentProcessor {
	var configStore projectConfigStore
	if len(stores) > 0 {
		configStore = stores[0]
	}

	return managedDeploymentProcessor{
		db:                  db,
		builder:             builder,
		provisioner:         provisioner,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
		configStore:         configStore,
		appBrainURL:         defaultAppBrainURL,
	}
}

func (processor managedDeploymentProcessor) Process(ctx context.Context, job job) (result deploymentResult, err error) {
	if err := updateJobPhase(processor.db, job.ID, sourcePreparationPhase(job)); err != nil {
		return result, err
	}
	result, err = processor.builder.Process(ctx, job)
	if err != nil {
		return result, err
	}

	runtimeConfig, err := processor.configStore.ResolveRuntimeConfig(ctx, job.ProjectName, job.ConfigRevisionID)
	if err != nil {
		return result, fmt.Errorf("load project config: %w", err)
	}
	result.LogScrubber = runtimeConfig.SecretScrubber

	appendJobLogLine(result.LogPath, sourceReadyLogLine(job), result.LogScrubber)
	defer func() {
		if err != nil {
			appendJobLogError(result.LogPath, err.Error(), result.LogScrubber)
		}
	}()

	if err := updateJobPhase(processor.db, job.ID, jobPhaseProvisioningPB); err != nil {
		return result, err
	}
	appendJobLogLine(result.LogPath, "lifecycle: provisioning PocketBase", result.LogScrubber)
	if _, err := processor.provisioner.EnsureProjectPocketBase(ctx, job.ProjectName, processor.pocketBaseImage, processor.projectsHostDataDir); err != nil {
		return result, fmt.Errorf("ensure PocketBase: %w", err)
	}
	if err := updateJobPhase(processor.db, job.ID, jobPhaseStartingApp); err != nil {
		return result, err
	}
	appendJobLogLine(result.LogPath, "lifecycle: starting app container", result.LogScrubber)
	if _, err := processor.provisioner.EnsureProjectApp(ctx, job, result.ImageRef, processor.appRuntimeEnv(runtimeConfig.Env)); err != nil {
		return result, fmt.Errorf("ensure app container: %w", err)
	}
	if err := updateJobPhase(processor.db, job.ID, jobPhaseWaitingForReadiness); err != nil {
		return result, err
	}
	appendJobLogLine(result.LogPath, "lifecycle: waiting for app readiness", result.LogScrubber)
	if err := processor.provisioner.WaitForProjectAppReady(ctx, job); err != nil {
		return result, fmt.Errorf("wait for app readiness: %w", err)
	}
	appendJobLogLine(result.LogPath, "lifecycle: app ready", result.LogScrubber)

	result.AppContainerName = appContainerName(job.ProjectName, job.ID)
	result.NetworkName = projectNetworkName(job.ProjectName)
	result.PocketBaseContainerName = pocketBaseContainerName(job.ProjectName)

	if err := updateJobPhase(processor.db, job.ID, jobPhasePromoting); err != nil {
		return result, err
	}
	appendJobLogLine(result.LogPath, "lifecycle: checking current deployment", result.LogScrubber)
	currentDeployment, found, err := getProjectCurrentDeployment(processor.db, job.ProjectName)
	if err != nil {
		return result, fmt.Errorf("load current deployment: %w", err)
	}
	if !found || currentDeployment.ID == job.ID {
		appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared", result.LogScrubber)
		return result, nil
	}
	if currentDeployment.AppContainerName == result.AppContainerName {
		result.SupersededDeploymentID = currentDeployment.ID
		appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared", result.LogScrubber)
		return result, nil
	}
	appendJobLogLine(result.LogPath, "lifecycle: removing superseded app "+currentDeployment.ID, result.LogScrubber)
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
	appendJobLogLine(result.LogPath, "lifecycle: runtime promotion prepared", result.LogScrubber)
	return result, nil
}

func (processor managedDeploymentProcessor) appRuntimeEnv(projectEnv []string) []string {
	appBrainURL := strings.TrimSpace(processor.appBrainURL)
	if appBrainURL == "" {
		appBrainURL = defaultAppBrainURL
	}
	env := []string{"OVEK_BRAIN_URL=" + appBrainURL}
	env = append(env, projectEnv...)
	return env
}

func sourcePreparationPhase(job job) string {
	if job.SourceType == jobSourceTypeImage {
		return jobPhasePreparingImage
	}

	return jobPhaseBuildingImage
}

func sourceReadyLogLine(job job) string {
	if job.SourceType == jobSourceTypeImage {
		return "lifecycle: image ready"
	}

	return "lifecycle: build succeeded"
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
