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
	if projects[1].Name != "beta-app" {
		t.Fatalf("expected second project %q, got %q", "beta-app", projects[1].Name)
	}
	if projects[1].CurrentDeploymentID != nil {
		t.Fatalf("expected beta-app current deployment to be nil, got %#v", projects[1].CurrentDeploymentID)
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
