package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestManagedProjectCleanerRemovesManagedResourcesInOrderAndClearsRuntimeState(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-old",
		AppContainerName:        "alces-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-08T23:55:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-current",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-current",
					ImageRef:                "alces-demo-app:dep-current",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
				},
				{
					DeploymentID:            "dep-old",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-old",
					ImageRef:                "alces-demo-app:dep-old",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
				},
			},
		},
	}

	err := newManagedProjectCleaner(db, runtime).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	wantSequence := []string{
		"app:alces-demo-app-app-dep-current",
		"app:alces-demo-app-app-dep-old",
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
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-current",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-current",
					ImageRef:                "alces-demo-app:dep-current",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
				},
			},
		},
		removeAppErr: errors.New("stop failed"),
	}

	err := newManagedProjectCleaner(db, runtime).Cleanup(context.Background(), "demo-app")
	if err == nil {
		t.Fatal("expected cleanup to fail")
	}
	if got := err.Error(); got != `remove project app "alces-demo-app-app-dep-current": stop failed` {
		t.Fatalf("expected app removal error, got %q", got)
	}
	if !reflect.DeepEqual(runtime.sequence, []string{"app:alces-demo-app-app-dep-current"}) {
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

func TestManagedProjectCleanerIsIdempotentWhenRuntimeResourcesAreAlreadyGone(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	runtime := &fakeProjectCleanupRuntime{}
	cleaner := newManagedProjectCleaner(db, runtime)

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
		ImageRef:                "localhost:5001/alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/alces-demo-app:dep-old",
		AppContainerName:        "alces-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSuperseded,
		CreatedAt:               "2026-04-08T23:55:00Z",
	})

	artifactCleaner := &fakeRegistryArtifactCleaner{}
	err := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, artifactCleaner).Cleanup(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	wantImageRefs := []string{
		"localhost:5001/alces-demo-app:dep-old",
		"localhost:5001/alces-demo-app:dep-current",
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
		ImageRef:                "localhost:5001/alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	artifactCleaner := &fakeRegistryArtifactCleaner{err: errors.New("registry delete failed")}
	logs := captureTestLogs(t, func() {
		err := newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}, artifactCleaner).Cleanup(context.Background(), "demo-app")
		if err != nil {
			t.Fatalf("expected cleanup to succeed, got error: %v", err)
		}
	})

	if !strings.Contains(logs, `warning: failed to clean up project "demo-app" image "localhost:5001/alces-demo-app:dep-current": registry delete failed`) {
		t.Fatalf("expected cleanup warning log, got %q", logs)
	}
}

func TestManagedProjectCleanerReturnsNotFoundForUnknownProject(t *testing.T) {
	db := newTestDB(t)
	runtime := &fakeProjectCleanupRuntime{}

	err := newManagedProjectCleaner(db, runtime).Cleanup(context.Background(), "demo-app")
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
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	handler := newHandler(config{
		BrainAPIKey: "test-key",
		DataDir:     dataDir,
	}, db, noopEnqueuer{}, newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}), noopProjectRuntimeService{})

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
	}, db, noopEnqueuer{}, newManagedProjectCleaner(db, &fakeProjectCleanupRuntime{}), noopProjectRuntimeService{})

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
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-old",
		AppContainerName:        "alces-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
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
	removePBErr   error
	removeNetErr  error
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
	return runtime.removeAppErr
}

func (runtime *fakeProjectCleanupRuntime) RemoveProjectPocketBase(_ context.Context, projectName string) error {
	runtime.sequence = append(runtime.sequence, "pocketbase:"+projectName)
	return runtime.removePBErr
}

func (runtime *fakeProjectCleanupRuntime) RemoveProjectNetwork(_ context.Context, projectName string) error {
	runtime.sequence = append(runtime.sequence, "network:"+projectName)
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
