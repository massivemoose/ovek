package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedProjectRuntimeServiceReturnsCurrentRuntime(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
		appsByProject: map[string][]projectAppRuntime{
			"alpha-app": {
				{
					DeploymentID:            "dep-alpha",
					ProjectName:             "alpha-app",
					AppContainerName:        "alces-alpha-app-app-dep-alpha",
					ImageRef:                "alces-alpha-app:dep-alpha",
					NetworkName:             "alpha-app-net",
					PocketBaseContainerName: "alces-alpha-app-pb",
					Running:                 true,
				},
			},
		},
		pocketBaseByProject: map[string]projectRuntimeContainer{
			"alpha-app": {
				ContainerName: "alces-alpha-app-pb",
				Running:       true,
			},
		},
		networkByProject: map[string]projectRuntimeNetwork{
			"alpha-app": {
				Name: "alpha-app-net",
			},
		},
	})

	runtimeView, err := service.GetRuntime(context.Background(), "alpha-app")
	if err != nil {
		t.Fatalf("expected runtime lookup to succeed, got error: %v", err)
	}

	if runtimeView.ProjectName != "alpha-app" {
		t.Fatalf("expected project name %q, got %q", "alpha-app", runtimeView.ProjectName)
	}
	if runtimeView.CurrentDeploymentID == nil || *runtimeView.CurrentDeploymentID != "dep-alpha" {
		t.Fatalf("expected current deployment %q, got %#v", "dep-alpha", runtimeView.CurrentDeploymentID)
	}
	if runtimeView.App == nil || runtimeView.App.ContainerName != "alces-alpha-app-app-dep-alpha" || !runtimeView.App.Running {
		t.Fatalf("expected running app runtime, got %#v", runtimeView.App)
	}
	if runtimeView.PocketBase == nil || runtimeView.PocketBase.ContainerName != "alces-alpha-app-pb" || !runtimeView.PocketBase.Running {
		t.Fatalf("expected running PocketBase runtime, got %#v", runtimeView.PocketBase)
	}
	if runtimeView.Network == nil || runtimeView.Network.Name != "alpha-app-net" {
		t.Fatalf("expected project network %q, got %#v", "alpha-app-net", runtimeView.Network)
	}
}

func TestManagedProjectRuntimeServiceReturnsProjectNotFound(t *testing.T) {
	db := newTestDB(t)
	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{})

	_, err := service.GetRuntime(context.Background(), "missing-app")
	if err != errProjectNotFound {
		t.Fatalf("expected project not found error, got %v", err)
	}
}

func TestGetProjectRuntimeReturnsRuntimeSummary(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	handler := newHandler(
		config{
			BrainAPIKey: "test-key",
			DataDir:     dataDir,
		},
		db,
		noopEnqueuer{},
		noopProjectCleaner{},
		newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
			appsByProject: map[string][]projectAppRuntime{
				"alpha-app": {
					{
						DeploymentID:            "dep-alpha",
						ProjectName:             "alpha-app",
						AppContainerName:        "alces-alpha-app-app-dep-alpha",
						ImageRef:                "alces-alpha-app:dep-alpha",
						NetworkName:             "alpha-app-net",
						PocketBaseContainerName: "alces-alpha-app-pb",
						Running:                 true,
					},
				},
			},
			pocketBaseByProject: map[string]projectRuntimeContainer{
				"alpha-app": {
					ContainerName: "alces-alpha-app-pb",
					Running:       true,
				},
			},
			networkByProject: map[string]projectRuntimeNetwork{
				"alpha-app": {
					Name: "alpha-app-net",
				},
			},
		}),
	)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	assertJSONContains(t, recorder.Body.String(), `"projectName":"alpha-app"`)
	assertJSONContains(t, recorder.Body.String(), `"currentDeploymentId":"dep-alpha"`)
	assertJSONContains(t, recorder.Body.String(), `"containerName":"alces-alpha-app-app-dep-alpha"`)
	assertJSONContains(t, recorder.Body.String(), `"pocketBase":{"containerName":"alces-alpha-app-pb","running":true}`)
	assertJSONContains(t, recorder.Body.String(), `"network":{"name":"alpha-app-net"}`)
}

func TestGetProjectRuntimeRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProjectRuntime(noopProjectRuntimeService{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/runtime", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectRuntimeReturnsNotFoundForUnknownProject(t *testing.T) {
	handler := handleGetProjectRuntime(fakeProjectRuntimeService{err: errProjectNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/runtime", nil)
	recorder := httptest.NewRecorder()
	request.SetPathValue("projectName", "missing-app")

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

type fakeProjectRuntimeReader struct {
	appsByProject       map[string][]projectAppRuntime
	pocketBaseByProject map[string]projectRuntimeContainer
	networkByProject    map[string]projectRuntimeNetwork
	listErr             error
	pocketBaseErr       error
	networkErr          error
}

func (runtime fakeProjectRuntimeReader) ListProjectApps(_ context.Context, projectName string) ([]projectAppRuntime, error) {
	if runtime.listErr != nil {
		return nil, runtime.listErr
	}

	return append([]projectAppRuntime(nil), runtime.appsByProject[projectName]...), nil
}

func (runtime fakeProjectRuntimeReader) GetProjectPocketBaseRuntime(_ context.Context, projectName string) (projectRuntimeContainer, bool, error) {
	if runtime.pocketBaseErr != nil {
		return projectRuntimeContainer{}, false, runtime.pocketBaseErr
	}

	pocketBase, ok := runtime.pocketBaseByProject[projectName]
	return pocketBase, ok, nil
}

func (runtime fakeProjectRuntimeReader) GetProjectNetworkRuntime(_ context.Context, projectName string) (projectRuntimeNetwork, bool, error) {
	if runtime.networkErr != nil {
		return projectRuntimeNetwork{}, false, runtime.networkErr
	}

	network, ok := runtime.networkByProject[projectName]
	return network, ok, nil
}

type fakeProjectRuntimeService struct {
	runtimeView projectRuntimeView
	err         error
}

func (service fakeProjectRuntimeService) GetRuntime(context.Context, string) (projectRuntimeView, error) {
	return service.runtimeView, service.err
}

func assertJSONContains(t *testing.T, body string, want string) {
	t.Helper()

	if !strings.Contains(body, want) {
		t.Fatalf("expected body to contain %q, got %q", want, body)
	}
}
