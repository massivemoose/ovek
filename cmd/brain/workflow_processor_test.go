package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagedWorkflowProcessorRunsContainerWithProjectServicesAndEnv(t *testing.T) {
	db := newTestDB(t)
	configStore := newTestProjectConfigStore(t, db)
	mutation, err := configStore.SetEnvironmentEntry(context.Background(), "demo-app", "PUBLIC_SITE_URL", "https://example.com", false, "test")
	if err != nil {
		t.Fatalf("expected env mutation to succeed, got error: %v", err)
	}
	mutation, err = configStore.SetEnvironmentEntry(context.Background(), "demo-app", "API_TOKEN", "secret-token", true, "test")
	if err != nil {
		t.Fatalf("expected secret mutation to succeed, got error: %v", err)
	}
	runtime := &recordingWorkflowExecutionRuntime{
		waitExitCode: 0,
		logs:         "prefix-prefix-prefix secret-token",
	}
	processor := newManagedWorkflowProcessor(db, runtime, t.TempDir(), "/srv/ovek/projects", defaultPocketBaseImage, configStore)
	run := workflowRun{
		ID:               "run-123",
		ProjectName:      "demo-app",
		WorkflowName:     "digest",
		ConfigRevisionID: mutation.RevisionID,
		Payload:          []byte(`{"signupId":"rec_123"}`),
		SourceImageRef:   "ghcr.io/example/digest:latest",
		RuntimeImageID:   "sha256:image-id",
	}

	result, err := processor.Process(context.Background(), run)
	if err != nil {
		t.Fatalf("expected workflow processing to succeed, got error: %v", err)
	}

	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %#v", result.ExitCode)
	}
	if runtime.ensureNetworkProject != "demo-app" {
		t.Fatalf("expected project network ensure, got %q", runtime.ensureNetworkProject)
	}
	if runtime.ensurePocketBaseProject != "demo-app" || runtime.ensurePocketBaseImage != defaultPocketBaseImage || runtime.ensurePocketBaseDataDir != "/srv/ovek/projects" {
		t.Fatalf("expected PocketBase ensure metadata, got runtime=%#v", runtime)
	}
	if runtime.createRun.ID != run.ID || runtime.createImageRef != "sha256:image-id" {
		t.Fatalf("expected workflow container to use runtime image ID, got runtime=%#v", runtime)
	}
	if runtime.createPayloadHostPath == "" {
		t.Fatal("expected workflow payload host path")
	}
	payloadBytes, err := os.ReadFile(runtime.createPayloadHostPath)
	if err != nil {
		t.Fatalf("expected payload file to be readable, got error: %v", err)
	}
	if string(payloadBytes) != `{"signupId":"rec_123"}` {
		t.Fatalf("expected payload file contents, got %q", string(payloadBytes))
	}
	wantEnv := []string{"API_TOKEN=secret-token", "PUBLIC_SITE_URL=https://example.com"}
	if !reflect.DeepEqual(runtime.createEnv, wantEnv) {
		t.Fatalf("expected project config env, got %#v", runtime.createEnv)
	}
	if runtime.startContainerID != "workflow-container-123" || runtime.waitContainerID != "workflow-container-123" || runtime.removeContainerID != "workflow-container-123" {
		t.Fatalf("expected lifecycle to target created container, got runtime=%#v", runtime)
	}
	logBytes, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("expected workflow logs to be readable, got error: %v", err)
	}
	if got := string(logBytes); got != "prefix-prefix-prefix [redacted]" {
		t.Fatalf("expected workflow logs to be captured, got %q", got)
	}
}

func TestManagedWorkflowProcessorFailsOnNonzeroExit(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowProject(t, db, "demo-app")
	runtime := &recordingWorkflowExecutionRuntime{
		waitExitCode: 2,
		logs:         "failed\n",
	}
	processor := newManagedWorkflowProcessor(db, runtime, t.TempDir(), "/srv/ovek/projects", defaultPocketBaseImage, projectConfigStore{})

	result, err := processor.Process(context.Background(), workflowRun{
		ID:             "run-123",
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
	})
	if err == nil {
		t.Fatal("expected non-zero exit to fail workflow processing")
	}
	if result.ExitCode == nil || *result.ExitCode != 2 {
		t.Fatalf("expected exit code 2, got %#v", result.ExitCode)
	}
	if runtime.removeContainerID != "workflow-container-123" {
		t.Fatalf("expected terminal container removal, got %q", runtime.removeContainerID)
	}
}

func TestManagedWorkflowProcessorStopsAndRemovesTimedOutContainer(t *testing.T) {
	db := newTestDB(t)
	seedWorkflowProject(t, db, "demo-app")
	runtime := &recordingWorkflowExecutionRuntime{
		waitErr: context.DeadlineExceeded,
		logs:    "slow\n",
	}
	processor := newManagedWorkflowProcessor(db, runtime, t.TempDir(), "/srv/ovek/projects", defaultPocketBaseImage, projectConfigStore{})
	processor.timeout = 1

	_, err := processor.Process(context.Background(), workflowRun{
		ID:             "run-123",
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
	})
	if !errors.Is(err, errWorkflowRunTimedOut) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if runtime.stopContainerID != "workflow-container-123" || runtime.removeContainerID != "workflow-container-123" {
		t.Fatalf("expected timed out container stop and removal, got runtime=%#v", runtime)
	}
}

func TestNewManagedWorkflowProcessorUsesConfiguredTimeout(t *testing.T) {
	processor := newManagedWorkflowProcessor(nil, &recordingWorkflowExecutionRuntime{}, t.TempDir(), "/srv/ovek/projects", defaultPocketBaseImage, projectConfigStore{}, 25*time.Minute)

	if processor.timeout != 25*time.Minute {
		t.Fatalf("expected configured workflow timeout %s, got %s", 25*time.Minute, processor.timeout)
	}
}

func seedWorkflowProject(t *testing.T, db *sql.DB, projectName string) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)`, projectName, projectStatusIdle, "2026-05-23T00:00:00Z"); err != nil {
		t.Fatalf("expected project seed to succeed, got error: %v", err)
	}
}

type recordingWorkflowExecutionRuntime struct {
	ensureNetworkProject    string
	ensurePocketBaseProject string
	ensurePocketBaseImage   string
	ensurePocketBaseDataDir string
	waitReadyProject        string
	createRun               workflowRun
	createImageRef          string
	createEnv               []string
	createPayloadHostPath   string
	startContainerID        string
	waitContainerID         string
	waitExitCode            int
	waitErr                 error
	logs                    string
	streamLogsContainerID   string
	stopContainerID         string
	removeContainerID       string
}

func (runtime *recordingWorkflowExecutionRuntime) EnsureProjectNetwork(_ context.Context, projectName string) (projectNetwork, error) {
	runtime.ensureNetworkProject = projectName
	return projectNetwork{ID: "network-123", Name: projectName + "-net"}, nil
}

func (runtime *recordingWorkflowExecutionRuntime) EnsureProjectPocketBase(_ context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	runtime.ensurePocketBaseProject = projectName
	runtime.ensurePocketBaseImage = image
	runtime.ensurePocketBaseDataDir = projectsHostDataDir
	return "pb-container-123", nil
}

func (runtime *recordingWorkflowExecutionRuntime) WaitForProjectPocketBaseReady(_ context.Context, projectName string) error {
	runtime.waitReadyProject = projectName
	return nil
}

func (runtime *recordingWorkflowExecutionRuntime) CreateWorkflowContainer(_ context.Context, run workflowRun, imageRef string, env []string, payloadHostPath string) (string, error) {
	runtime.createRun = run
	runtime.createImageRef = imageRef
	runtime.createEnv = env
	runtime.createPayloadHostPath = payloadHostPath
	return "workflow-container-123", nil
}

func (runtime *recordingWorkflowExecutionRuntime) StartWorkflowContainer(_ context.Context, containerID string) error {
	runtime.startContainerID = containerID
	return nil
}

func (runtime *recordingWorkflowExecutionRuntime) WaitWorkflowContainer(_ context.Context, containerID string) (int, error) {
	runtime.waitContainerID = containerID
	return runtime.waitExitCode, runtime.waitErr
}

func (runtime *recordingWorkflowExecutionRuntime) StreamWorkflowContainerLogs(_ context.Context, containerID string) (io.ReadCloser, error) {
	runtime.streamLogsContainerID = containerID
	return io.NopCloser(strings.NewReader(runtime.logs)), nil
}

func (runtime *recordingWorkflowExecutionRuntime) StopWorkflowContainer(_ context.Context, containerID string) error {
	runtime.stopContainerID = containerID
	return nil
}

func (runtime *recordingWorkflowExecutionRuntime) RemoveWorkflowContainer(_ context.Context, containerID string) error {
	runtime.removeContainerID = containerID
	return nil
}
