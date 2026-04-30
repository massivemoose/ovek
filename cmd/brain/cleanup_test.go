package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
)

func TestManagedProjectCleanerRemovesManagedResourcesInOrderAndClearsRuntimeState(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-old",
		AppContainerName:        "ovek-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-08T23:55:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-current",
					ProjectName:             "demo-app",
					AppContainerName:        "ovek-demo-app-app-dep-current",
					ImageRef:                "ovek-demo-app:dep-current",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "ovek-demo-app-pb",
				},
				{
					DeploymentID:            "dep-old",
					ProjectName:             "demo-app",
					AppContainerName:        "ovek-demo-app-app-dep-old",
					ImageRef:                "ovek-demo-app:dep-old",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "ovek-demo-app-pb",
				},
			},
		},
	}

	err := newManagedProjectCleaner(db, runtime, defaultDataDir).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	wantSequence := []string{
		"app:ovek-demo-app-app-dep-current",
		"app:ovek-demo-app-app-dep-old",
		"pocketbase:demo-app",
		"network:demo-app",
	}
	if !reflect.DeepEqual(runtime.sequence, wantSequence) {
		t.Fatalf("expected cleanup sequence %#v, got %#v", wantSequence, runtime.sequence)
	}
	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getDeploymentRecord(t, db, "dep-current").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected current deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
	if got := getDeploymentRecord(t, db, "dep-old").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected old deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusIdle {
		t.Fatalf("expected project status %q, got %q", projectStatusIdle, got)
	}
}

func TestManagedProjectCleanerLeavesDatabaseStateUntouchedWhenAppRemovalFails(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-current",
					ProjectName:             "demo-app",
					AppContainerName:        "ovek-demo-app-app-dep-current",
					ImageRef:                "ovek-demo-app:dep-current",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "ovek-demo-app-pb",
				},
			},
		},
		removeAppErr: errors.New("stop failed"),
	}

	err := newManagedProjectCleaner(db, runtime, defaultDataDir).Cleanup(context.Background(), "demo-app")
	if err == nil {
		t.Fatal("expected cleanup to fail")
	}
	if got := err.Error(); got != `remove project app "ovek-demo-app-app-dep-current": stop failed` {
		t.Fatalf("expected app removal error, got %q", got)
	}
	if !reflect.DeepEqual(runtime.sequence, []string{"app:ovek-demo-app-app-dep-current"}) {
		t.Fatalf("expected cleanup to stop after app failure, got %#v", runtime.sequence)
	}
	if got := getProjectCurrentDeploymentID(t, db, "demo-app"); got != "dep-current" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-current", got)
	}
	if got := getDeploymentRecord(t, db, "dep-current").Status; got != deploymentStatusSucceeded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSucceeded, got)
	}
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestManagedProjectCleanerRetriesTransientRuntimeTeardownFailures(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	originalSleep := sleepForRuntimeTeardownRetry
	var retryDelays []time.Duration
	sleepForRuntimeTeardownRetry = func(_ context.Context, delay time.Duration) error {
		retryDelays = append(retryDelays, delay)
		return nil
	}
	t.Cleanup(func() {
		sleepForRuntimeTeardownRetry = originalSleep
	})

	runtime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-current",
					ProjectName:             "demo-app",
					AppContainerName:        "ovek-demo-app-app-dep-current",
					ImageRef:                "ovek-demo-app:dep-current",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "ovek-demo-app-pb",
				},
			},
		},
		removeAppErrs: []error{fmt.Errorf("podman app remove 500: %w", cerrdefs.ErrInternal), nil},
		removePBErrs:  []error{fmt.Errorf("podman PocketBase remove unavailable: %w", cerrdefs.ErrUnavailable), nil},
		removeNetErrs: []error{fmt.Errorf("podman network still has endpoint: %w", cerrdefs.ErrConflict), nil},
	}

	if err := newManagedProjectCleaner(db, runtime, defaultDataDir).Cleanup(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected cleanup to retry transient runtime failures and succeed, got error: %v", err)
	}

	wantSequence := []string{
		"app:ovek-demo-app-app-dep-current",
		"app:ovek-demo-app-app-dep-current",
		"pocketbase:demo-app",
		"pocketbase:demo-app",
		"network:demo-app",
		"network:demo-app",
	}
	if !reflect.DeepEqual(runtime.sequence, wantSequence) {
		t.Fatalf("expected cleanup sequence %#v, got %#v", wantSequence, runtime.sequence)
	}
	wantRetryDelays := []time.Duration{
		150 * time.Millisecond,
		150 * time.Millisecond,
		150 * time.Millisecond,
	}
	if !reflect.DeepEqual(retryDelays, wantRetryDelays) {
		t.Fatalf("expected retry delays %#v, got %#v", wantRetryDelays, retryDelays)
	}
	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusIdle {
		t.Fatalf("expected project status %q, got %q", projectStatusIdle, got)
	}
}

func TestCleanupRuntimeTeardownRetryDelayBacksOffAndCaps(t *testing.T) {
	got := []time.Duration{
		cleanupRuntimeTeardownRetryDelay(1),
		cleanupRuntimeTeardownRetryDelay(2),
		cleanupRuntimeTeardownRetryDelay(3),
		cleanupRuntimeTeardownRetryDelay(4),
	}
	want := []time.Duration{
		150 * time.Millisecond,
		300 * time.Millisecond,
		600 * time.Millisecond,
		600 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected retry delay sequence %#v, got %#v", want, got)
	}
}

func TestManagedProjectCleanerRemovesProjectIngressAfterClearingRuntimeState(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	ingress := &recordingProjectIngressManager{}
	cleaner := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, defaultDataDir)
	cleaner.ingress = ingress

	if err := cleaner.Cleanup(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	if !reflect.DeepEqual(ingress.removedProjects, []string{"demo-app"}) {
		t.Fatalf("expected removed ingress projects %#v, got %#v", []string{"demo-app"}, ingress.removedProjects)
	}
}

func TestManagedProjectCleanerIsIdempotentWhenRuntimeResourcesAreAlreadyGone(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{}
	cleaner := newManagedProjectCleaner(db, runtime, defaultDataDir)

	if err := cleaner.Cleanup(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected first cleanup to succeed, got error: %v", err)
	}
	if err := cleaner.Cleanup(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected repeated cleanup to succeed, got error: %v", err)
	}

	wantSequence := []string{
		"pocketbase:demo-app",
		"network:demo-app",
		"pocketbase:demo-app",
		"network:demo-app",
	}
	if !reflect.DeepEqual(runtime.sequence, wantSequence) {
		t.Fatalf("expected repeated cleanup sequence %#v, got %#v", wantSequence, runtime.sequence)
	}
	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getDeploymentRecord(t, db, "dep-current").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusIdle {
		t.Fatalf("expected project status %q, got %q", projectStatusIdle, got)
	}
}

func TestManagedProjectCleanerCleansUpDeploymentImagesAfterRuntimeTeardown(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-old",
		AppContainerName:        "ovek-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSuperseded,
		CreatedAt:               "2026-04-08T23:55:00Z",
	})

	artifactCleaner := &fakeRegistryArtifactCleaner{}
	err := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, defaultDataDir, artifactCleaner).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	wantImageRefs := []string{
		"localhost:5001/ovek-demo-app:dep-old",
		"localhost:5001/ovek-demo-app:dep-current",
	}
	if !reflect.DeepEqual(artifactCleaner.cleanedRefs, wantImageRefs) {
		t.Fatalf("expected cleaned refs %#v, got %#v", wantImageRefs, artifactCleaner.cleanedRefs)
	}
}

func TestManagedProjectCleanerLogsArtifactCleanupFailuresButStillSucceeds(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	artifactCleaner := &fakeRegistryArtifactCleaner{err: errors.New("registry delete failed")}
	logs := captureTestLogs(t, func() {
		err := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, defaultDataDir, artifactCleaner).Cleanup(context.Background(), "demo-app")
		if err != nil {
			t.Fatalf("expected cleanup to succeed, got error: %v", err)
		}
	})

	if !strings.Contains(logs, `warning: failed to clean up project "demo-app" image "localhost:5001/ovek-demo-app:dep-current": registry delete failed`) {
		t.Fatalf("expected cleanup warning log, got %q", logs)
	}
}

func TestManagedProjectCleanerRemovesManagedProjectJobLogFiles(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	demoJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected demo job creation to succeed, got error: %v", err)
	}
	otherJob, err := createQueuedJob(db, "other-app", "https://example.com/other.git")
	if err != nil {
		t.Fatalf("expected other job creation to succeed, got error: %v", err)
	}

	demoLogPath := jobLogPath(dataDir, demoJob.ID)
	otherLogPath := jobLogPath(dataDir, otherJob.ID)
	if err := os.MkdirAll(filepath.Dir(demoLogPath), 0o755); err != nil {
		t.Fatalf("expected demo log dir creation to succeed, got error: %v", err)
	}
	if err := os.WriteFile(demoLogPath, []byte("demo logs\n"), 0o644); err != nil {
		t.Fatalf("expected demo log write to succeed, got error: %v", err)
	}
	if err := os.WriteFile(otherLogPath, []byte("other logs\n"), 0o644); err != nil {
		t.Fatalf("expected other log write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ? WHERE id = ?", demoLogPath, demoJob.ID); err != nil {
		t.Fatalf("expected demo log path update to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ? WHERE id = ?", otherLogPath, otherJob.ID); err != nil {
		t.Fatalf("expected other log path update to succeed, got error: %v", err)
	}

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	err = newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, dataDir).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	if _, err := os.Stat(demoLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected demo log file %q to be removed, got error %v", demoLogPath, err)
	}
	if _, err := os.Stat(otherLogPath); err != nil {
		t.Fatalf("expected other log file %q to remain, got error %v", otherLogPath, err)
	}
}

func TestManagedProjectCleanerSkipsJobLogFilesOutsideManagedDirectory(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	outsideLogPath := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outsideLogPath, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("expected outside log write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ? WHERE id = ?", outsideLogPath, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	err = newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, dataDir).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	if _, err := os.Stat(outsideLogPath); err != nil {
		t.Fatalf("expected outside log file %q to remain, got error %v", outsideLogPath, err)
	}
}

func TestManagedProjectCleanerLogsJobLogCleanupFailuresButStillSucceeds(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatalf("expected failing log path setup to succeed, got error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logPath, "child.log"), []byte("child\n"), 0o644); err != nil {
		t.Fatalf("expected child log write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ? WHERE id = ?", logPath, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	logs := captureTestLogs(t, func() {
		err := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, dataDir).Cleanup(context.Background(), "demo-app")
		if err != nil {
			t.Fatalf("expected cleanup to succeed, got error: %v", err)
		}
	})

	if !strings.Contains(logs, `warning: failed to clean up project "demo-app" log file "`) {
		t.Fatalf("expected log cleanup warning, got %q", logs)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected failing log path %q to remain, got error %v", logPath, err)
	}
}

func TestManagedProjectCleanerReturnsNotFoundForUnknownProject(t *testing.T) {
	db := newTestDB(t)
	runtime := &fakeProjectCleanupRuntime{}

	err := newManagedProjectCleaner(db, runtime, defaultDataDir).Cleanup(context.Background(), "demo-app")
	if !errors.Is(err, errProjectNotFound) {
		t.Fatalf("expected project not found error, got %v", err)
	}
	if len(runtime.sequence) != 0 {
		t.Fatalf("expected no runtime calls, got %#v", runtime.sequence)
	}
}

func TestDeleteProjectRuntimeEndpointReturnsNoContent(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	handler := newHandler(config{
		BrainAPIKey: "test-key",
		DataDir:     dataDir,
	}, db, noopEnqueuer{}, newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, dataDir), noopProjectRuntimeService{}, managedProjectPocketBaseService{})

	request := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/runtime", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %q", recorder.Body.String())
	}
	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusIdle {
		t.Fatalf("expected project status %q, got %q", projectStatusIdle, got)
	}
}

func TestDeleteProjectRuntimeEndpointRejectsInvalidProjectName(t *testing.T) {
	handler := handleDeleteProjectRuntime(noopProjectCleaner{})
	request := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/runtime", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestDeleteProjectRuntimeEndpointReturnsNotFoundForUnknownProject(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	handler := newHandler(config{
		BrainAPIKey: "test-key",
		DataDir:     dataDir,
	}, db, noopEnqueuer{}, newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, dataDir), noopProjectRuntimeService{}, managedProjectPocketBaseService{})

	request := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/runtime", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestDeleteProjectRuntimeEndpointReturnsInternalServerErrorOnCleanupFailure(t *testing.T) {
	handler := handleDeleteProjectRuntime(failingProjectCleaner{err: errors.New("boom")})
	request := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/runtime", nil)
	request.SetPathValue("projectName", "demo-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusInternalServerError, errorCodeProjectCleanupFailed, "failed to clean up project")
}

func TestProjectExistsReturnsExpectedState(t *testing.T) {
	db := newTestDB(t)

	exists, err := projectExists(db, "missing-app")
	if err != nil {
		t.Fatalf("expected missing project existence check to succeed, got error: %v", err)
	}
	if exists {
		t.Fatal("expected missing project to be absent")
	}

	seedProjectRecord(t, db, "demo-app")
	exists, err = projectExists(db, "demo-app")
	if err != nil {
		t.Fatalf("expected present project existence check to succeed, got error: %v", err)
	}
	if !exists {
		t.Fatal("expected present project to exist")
	}
}

func TestClearProjectRuntimeStateSupersedesSucceededDeploymentsAndClearsCurrentPointer(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-current",
		AppContainerName:        "ovek-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "ovek-demo-app:dep-old",
		AppContainerName:        "ovek-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "ovek-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-08T23:55:00Z",
	})

	if err := clearProjectRuntimeState(db, "demo-app"); err != nil {
		t.Fatalf("expected runtime state clear to succeed, got error: %v", err)
	}

	assertCurrentDeploymentUnset(t, db, "demo-app")
	if got := getDeploymentRecord(t, db, "dep-current").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected current deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
	if got := getDeploymentRecord(t, db, "dep-old").Status; got != deploymentStatusSuperseded {
		t.Fatalf("expected old deployment status %q, got %q", deploymentStatusSuperseded, got)
	}
}

type fakeProjectCleanupRuntime struct {
	appsByProject map[string][]projectAppRuntime
	listErr       error
	removeAppErr  error
	removeAppErrs []error
	removePBErr   error
	removePBErrs  []error
	removeNetErr  error
	removeNetErrs []error
	sequence      []string
}

type fakeRegistryArtifactCleaner struct {
	cleanedRefs []string
	err         error
	errByRef    map[string]error
}

func (runtime *fakeProjectCleanupRuntime) ListProjectApps(_ context.Context, projectName string) ([]projectAppRuntime, error) {
	if runtime.listErr != nil {
		return nil, runtime.listErr
	}

	return append([]projectAppRuntime(nil), runtime.appsByProject[projectName]...), nil
}

func (runtime *fakeProjectCleanupRuntime) RemoveProjectApp(_ context.Context, deployment deploymentRecord) error {
	runtime.sequence = append(runtime.sequence, "app:"+deployment.AppContainerName)
	if len(runtime.removeAppErrs) > 0 {
		err := runtime.removeAppErrs[0]
		runtime.removeAppErrs = runtime.removeAppErrs[1:]
		return err
	}
	return runtime.removeAppErr
}

func (runtime *fakeProjectCleanupRuntime) RemoveProjectPocketBase(_ context.Context, projectName string) error {
	runtime.sequence = append(runtime.sequence, "pocketbase:"+projectName)
	if len(runtime.removePBErrs) > 0 {
		err := runtime.removePBErrs[0]
		runtime.removePBErrs = runtime.removePBErrs[1:]
		return err
	}
	return runtime.removePBErr
}

func (runtime *fakeProjectCleanupRuntime) RemoveProjectNetwork(_ context.Context, projectName string) error {
	runtime.sequence = append(runtime.sequence, "network:"+projectName)
	if len(runtime.removeNetErrs) > 0 {
		err := runtime.removeNetErrs[0]
		runtime.removeNetErrs = runtime.removeNetErrs[1:]
		return err
	}
	return runtime.removeNetErr
}

func (cleaner *fakeRegistryArtifactCleaner) CleanupImage(_ context.Context, imageRef string) error {
	cleaner.cleanedRefs = append(cleaner.cleanedRefs, imageRef)
	if cleaner.errByRef != nil {
		if err, ok := cleaner.errByRef[imageRef]; ok {
			return err
		}
	}

	return cleaner.err
}

type failingProjectCleaner struct {
	err error
}

func (cleaner failingProjectCleaner) Cleanup(context.Context, string) error {
	return cleaner.err
}

func captureTestLogs(t *testing.T, run func()) string {
	t.Helper()

	var buffer bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
	})

	run()
	log.SetOutput(originalWriter)

	return buffer.String()
}
