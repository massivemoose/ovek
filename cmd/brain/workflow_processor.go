package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const defaultWorkflowRunTimeout = time.Hour

type managedWorkflowProcessor struct {
	db                  *sql.DB
	runtime             workflowExecutionRuntime
	projectsHostDataDir string
	pocketBaseImage     string
	configStore         projectConfigStore
	timeout             time.Duration
}

type workflowExecutionRuntime interface {
	EnsureProjectNetwork(ctx context.Context, projectName string) (projectNetwork, error)
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	WaitForProjectPocketBaseReady(ctx context.Context, projectName string) error
	CreateWorkflowContainer(ctx context.Context, run workflowRun, imageRef string, env []string) (string, error)
	StartWorkflowContainer(ctx context.Context, containerID string) error
	WaitWorkflowContainer(ctx context.Context, containerID string) (int, error)
	StopWorkflowContainer(ctx context.Context, containerID string) error
	RemoveWorkflowContainer(ctx context.Context, containerID string) error
}

func newManagedWorkflowProcessor(db *sql.DB, runtime workflowExecutionRuntime, projectsHostDataDir string, pocketBaseImage string, configStore projectConfigStore) managedWorkflowProcessor {
	return managedWorkflowProcessor{
		db:                  db,
		runtime:             runtime,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
		configStore:         configStore,
		timeout:             defaultWorkflowRunTimeout,
	}
}

func (processor managedWorkflowProcessor) Process(ctx context.Context, run workflowRun) (workflowRunResult, error) {
	if processor.timeout <= 0 {
		processor.timeout = defaultWorkflowRunTimeout
	}
	if processor.db != nil {
		exists, err := projectExists(processor.db, run.ProjectName)
		if err != nil {
			return workflowRunResult{}, fmt.Errorf("check project exists: %w", err)
		}
		if !exists {
			return workflowRunResult{}, fmt.Errorf("project %q not found", run.ProjectName)
		}
	}

	if _, err := processor.runtime.EnsureProjectNetwork(ctx, run.ProjectName); err != nil {
		return workflowRunResult{}, fmt.Errorf("ensure project network: %w", err)
	}
	if _, err := processor.runtime.EnsureProjectPocketBase(ctx, run.ProjectName, processor.pocketBaseImage, processor.projectsHostDataDir); err != nil {
		return workflowRunResult{}, fmt.Errorf("ensure PocketBase: %w", err)
	}
	if err := processor.runtime.WaitForProjectPocketBaseReady(ctx, run.ProjectName); err != nil {
		return workflowRunResult{}, fmt.Errorf("wait for PocketBase readiness: %w", err)
	}

	runtimeConfig, err := processor.configStore.ResolveRuntimeConfig(ctx, run.ProjectName, run.ConfigRevisionID)
	if err != nil {
		return workflowRunResult{}, fmt.Errorf("resolve project config: %w", err)
	}

	imageRef := run.RuntimeImageID
	if imageRef == "" {
		imageRef = run.SourceImageRef
	}
	containerID, err := processor.runtime.CreateWorkflowContainer(ctx, run, imageRef, runtimeConfig.Env)
	if err != nil {
		return workflowRunResult{}, fmt.Errorf("create workflow container: %w", err)
	}
	defer func() {
		_ = processor.runtime.RemoveWorkflowContainer(context.Background(), containerID)
	}()

	if err := processor.runtime.StartWorkflowContainer(ctx, containerID); err != nil {
		return workflowRunResult{}, fmt.Errorf("start workflow container: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, processor.timeout)
	defer cancel()

	exitCode, err := processor.runtime.WaitWorkflowContainer(runCtx, containerID)
	result := workflowRunResult{ExitCode: &exitCode}
	if errors.Is(err, context.DeadlineExceeded) {
		_ = processor.runtime.StopWorkflowContainer(context.Background(), containerID)
		return result, fmt.Errorf("%w after %s", errWorkflowRunTimedOut, processor.timeout)
	}
	if err != nil {
		return result, fmt.Errorf("wait for workflow container: %w", err)
	}
	if exitCode != 0 {
		return result, fmt.Errorf("workflow container exited with code %d", exitCode)
	}

	return result, nil
}
