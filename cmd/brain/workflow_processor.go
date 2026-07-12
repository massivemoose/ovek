package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const defaultWorkflowRunTimeout = 10 * time.Minute

type managedWorkflowProcessor struct {
	db                  *sql.DB
	runtime             workflowExecutionRuntime
	dataDir             string
	projectsHostDataDir string
	pocketBaseImage     string
	configStore         projectConfigStore
	timeout             time.Duration
}

type workflowExecutionRuntime interface {
	EnsureProjectNetwork(ctx context.Context, projectName string) (projectNetwork, error)
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	WaitForProjectPocketBaseReady(ctx context.Context, projectName string) error
	CreateWorkflowContainer(ctx context.Context, run workflowRun, imageRef string, env []string, payloadHostPath string) (string, error)
	StartWorkflowContainer(ctx context.Context, containerID string) error
	WaitWorkflowContainer(ctx context.Context, containerID string) (int, error)
	StreamWorkflowContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	StopWorkflowContainer(ctx context.Context, containerID string) error
	RemoveWorkflowContainer(ctx context.Context, containerID string) error
}

func newManagedWorkflowProcessor(db *sql.DB, runtime workflowExecutionRuntime, dataDir string, projectsHostDataDir string, pocketBaseImage string, configStore projectConfigStore, timeoutOverrides ...time.Duration) managedWorkflowProcessor {
	timeout := defaultWorkflowRunTimeout
	if len(timeoutOverrides) > 0 && timeoutOverrides[0] > 0 {
		timeout = timeoutOverrides[0]
	}
	return managedWorkflowProcessor{
		db:                  db,
		runtime:             runtime,
		dataDir:             dataDir,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
		configStore:         configStore,
		timeout:             timeout,
	}
}

func (processor managedWorkflowProcessor) Process(ctx context.Context, run workflowRun) (result workflowRunResult, err error) {
	result = workflowRunResult{
		LogPath: workflowLogPath(processor.dataDir, run.ID),
	}
	if processor.timeout <= 0 {
		processor.timeout = defaultWorkflowRunTimeout
	}
	if processor.db != nil {
		exists, err := projectExists(processor.db, run.ProjectName)
		if err != nil {
			return workflowRunResult{}, fmt.Errorf("check project exists: %w", err)
		}
		if !exists {
			return result, fmt.Errorf("project %q not found", run.ProjectName)
		}
	}

	logFile, err := createManagedJobLogFile(result.LogPath)
	if err != nil {
		return workflowRunResult{}, fmt.Errorf("create workflow log file: %w", err)
	}
	defer logFile.Close()

	if _, err := processor.runtime.EnsureProjectNetwork(ctx, run.ProjectName); err != nil {
		return result, fmt.Errorf("ensure project network: %w", err)
	}
	if _, err := processor.runtime.EnsureProjectPocketBase(ctx, run.ProjectName, processor.pocketBaseImage, processor.projectsHostDataDir); err != nil {
		return result, fmt.Errorf("ensure PocketBase: %w", err)
	}
	if err := processor.runtime.WaitForProjectPocketBaseReady(ctx, run.ProjectName); err != nil {
		return result, fmt.Errorf("wait for PocketBase readiness: %w", err)
	}

	runtimeConfig, err := processor.configStore.ResolveRuntimeConfig(ctx, run.ProjectName, run.ConfigRevisionID)
	if err != nil {
		return result, fmt.Errorf("resolve project config: %w", err)
	}
	logWriter := runtimeConfig.SecretScrubber.Writer(logFile)
	defer func() {
		if flushErr := logWriter.Flush(); flushErr != nil {
			err = errors.Join(err, fmt.Errorf("flush workflow log: %w", flushErr))
		}
	}()

	payloadHostPath, err := writeWorkflowPayloadFile(processor.dataDir, run)
	if err != nil {
		return result, err
	}

	imageRef := run.RuntimeImageID
	if imageRef == "" {
		imageRef = run.SourceImageRef
	}
	containerID, err := processor.runtime.CreateWorkflowContainer(ctx, run, imageRef, runtimeConfig.Env, payloadHostPath)
	if err != nil {
		return result, fmt.Errorf("create workflow container: %w", err)
	}
	defer func() {
		_ = processor.runtime.RemoveWorkflowContainer(context.Background(), containerID)
	}()

	if err := processor.runtime.StartWorkflowContainer(ctx, containerID); err != nil {
		return result, fmt.Errorf("start workflow container: %w", err)
	}

	logs, err := processor.runtime.StreamWorkflowContainerLogs(ctx, containerID)
	if err != nil {
		return result, fmt.Errorf("stream workflow container logs: %w", err)
	}
	logCopyDone := make(chan error, 1)
	go func() {
		defer logs.Close()
		_, copyErr := io.Copy(logWriter, logs)
		logCopyDone <- copyErr
	}()

	runCtx, cancel := context.WithTimeout(ctx, processor.timeout)
	defer cancel()

	exitCode, err := processor.runtime.WaitWorkflowContainer(runCtx, containerID)
	result.ExitCode = &exitCode
	if errors.Is(err, context.DeadlineExceeded) {
		_ = processor.runtime.StopWorkflowContainer(context.Background(), containerID)
		<-logCopyDone
		return result, fmt.Errorf("%w after %s", errWorkflowRunTimedOut, processor.timeout)
	}
	logCopyErr := <-logCopyDone
	if err != nil {
		return result, fmt.Errorf("wait for workflow container: %w", err)
	}
	if logCopyErr != nil {
		return result, fmt.Errorf("copy workflow container logs: %w", logCopyErr)
	}
	if exitCode != 0 {
		return result, fmt.Errorf("workflow container exited with code %d", exitCode)
	}

	return result, nil
}

func writeWorkflowPayloadFile(dataDir string, run workflowRun) (string, error) {
	payloadPath := workflowPayloadPath(dataDir, run.ID)
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o755); err != nil {
		return "", fmt.Errorf("create workflow payload directory: %w", err)
	}
	payload := normalizeWorkflowRunPayload(run.Payload)
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		return "", fmt.Errorf("write workflow payload file: %w", err)
	}
	return payloadPath, nil
}
