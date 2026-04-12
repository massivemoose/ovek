package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListProjectsReturnsProjectSummaries(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedProjectRecord(t, db, "beta-app")
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

	request := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var projects []projectSummary
	if err := json.NewDecoder(recorder.Body).Decode(&projects); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "alpha-app" {
		t.Fatalf("expected first project %q, got %q", "alpha-app", projects[0].Name)
	}
	if projects[0].CurrentDeploymentID == nil || *projects[0].CurrentDeploymentID != "dep-alpha" {
		t.Fatalf("expected alpha-app current deployment %q, got %#v", "dep-alpha", projects[0].CurrentDeploymentID)
	}
	if projects[0].Status != projectStatusRunning {
		t.Fatalf("expected alpha-app status %q, got %q", projectStatusRunning, projects[0].Status)
	}
	if projects[1].Name != "beta-app" {
		t.Fatalf("expected second project %q, got %q", "beta-app", projects[1].Name)
	}
	if projects[1].CurrentDeploymentID != nil {
		t.Fatalf("expected beta-app current deployment to be nil, got %#v", projects[1].CurrentDeploymentID)
	}
	if projects[1].Status != projectStatusIdle {
		t.Fatalf("expected beta-app status %q, got %q", projectStatusIdle, projects[1].Status)
	}
}

func TestListProjectsReturnsEmptyArrayWhenNoProjectsExist(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if recorder.Body.String() != "[]\n" {
		t.Fatalf("expected empty JSON array body, got %q", recorder.Body.String())
	}
}

func TestListProjectsHonorsLimit(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedProjectRecord(t, db, "alpha-app")
	seedProjectRecord(t, db, "beta-app")

	request := httptest.NewRequest(http.MethodGet, "/v1/projects?limit=1", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var projects []projectSummary
	if err := json.NewDecoder(recorder.Body).Decode(&projects); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Name != "alpha-app" {
		t.Fatalf("expected limited project %q, got %q", "alpha-app", projects[0].Name)
	}
}

func TestListProjectsRejectsInvalidLimit(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects?limit=0", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
}

func TestGetProjectReturnsProjectSummary(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

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

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var project projectSummary
	if err := json.NewDecoder(recorder.Body).Decode(&project); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if project.Name != "alpha-app" {
		t.Fatalf("expected project name %q, got %q", "alpha-app", project.Name)
	}
	if project.Status != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, project.Status)
	}
	if project.CurrentDeploymentID == nil || *project.CurrentDeploymentID != "dep-alpha" {
		t.Fatalf("expected current deployment %q, got %#v", "dep-alpha", project.CurrentDeploymentID)
	}
	if project.CreatedAt != "2026-04-10T00:00:00Z" {
		t.Fatalf("expected createdAt %q, got %q", "2026-04-10T00:00:00Z", project.CreatedAt)
	}
}

func TestGetProjectRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProject(nil)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectReturnsNotFoundForUnknownProject(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}
