package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetProjectDeploymentReturnsCurrentDeployment(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-current",
		AppContainerName:        "ovek-alpha-app-app-dep-current",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments/dep-current", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var deployment deploymentRecord
	if err := json.NewDecoder(recorder.Body).Decode(&deployment); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if deployment.ID != "dep-current" {
		t.Fatalf("expected deployment %q, got %q", "dep-current", deployment.ID)
	}
	if deployment.Status != deploymentStatusSucceeded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSucceeded, deployment.Status)
	}
}

func TestGetProjectDeploymentReturnsSupersededDeployment(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-old",
		AppContainerName:        "ovek-alpha-app-app-dep-old",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSuperseded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments/dep-old", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var deployment deploymentRecord
	if err := json.NewDecoder(recorder.Body).Decode(&deployment); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if deployment.ID != "dep-old" {
		t.Fatalf("expected deployment %q, got %q", "dep-old", deployment.ID)
	}
	if deployment.Status != deploymentStatusSuperseded {
		t.Fatalf("expected deployment status %q, got %q", deploymentStatusSuperseded, deployment.Status)
	}
}

func TestListProjectDeploymentsReturnsNewestFirst(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-old",
		AppContainerName:        "ovek-alpha-app-app-dep-old",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSuperseded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-current",
		AppContainerName:        "ovek-alpha-app-app-dep-current",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-other",
		ProjectName:             "beta-app",
		ImageRef:                "localhost:5001/ovek-beta-app:dep-other",
		AppContainerName:        "ovek-beta-app-app-dep-other",
		NetworkName:             "beta-app-net",
		PocketBaseContainerName: "ovek-beta-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-11T00:00:00Z",
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var deployments []deploymentRecord
	if err := json.NewDecoder(recorder.Body).Decode(&deployments); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(deployments) != 2 {
		t.Fatalf("expected 2 deployments, got %d", len(deployments))
	}
	if deployments[0].ID != "dep-current" {
		t.Fatalf("expected newest deployment %q, got %q", "dep-current", deployments[0].ID)
	}
	if deployments[0].Status != deploymentStatusSucceeded {
		t.Fatalf("expected current deployment status %q, got %q", deploymentStatusSucceeded, deployments[0].Status)
	}
	if deployments[1].ID != "dep-old" {
		t.Fatalf("expected older deployment %q, got %q", "dep-old", deployments[1].ID)
	}
	if deployments[1].Status != deploymentStatusSuperseded {
		t.Fatalf("expected old deployment status %q, got %q", deploymentStatusSuperseded, deployments[1].Status)
	}
}

func TestListProjectDeploymentsReturnsEmptyArrayForKnownProjectWithoutDeployments(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})
	seedProjectRecord(t, db, "alpha-app")

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments", nil)
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

func TestListProjectDeploymentsHonorsLimit(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-old",
		AppContainerName:        "ovek-alpha-app-app-dep-old",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSuperseded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/ovek-alpha-app:dep-current",
		AppContainerName:        "ovek-alpha-app-app-dep-current",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "ovek-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments?limit=1", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var deployments []deploymentRecord
	if err := json.NewDecoder(recorder.Body).Decode(&deployments); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(deployments) != 1 {
		t.Fatalf("expected 1 deployment, got %d", len(deployments))
	}
	if deployments[0].ID != "dep-current" {
		t.Fatalf("expected limited deployment %q, got %q", "dep-current", deployments[0].ID)
	}
}

func TestListProjectDeploymentsRejectsInvalidProjectName(t *testing.T) {
	handler := handleListProjectDeployments(nil)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/deployments", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestListProjectDeploymentsRejectsInvalidLimit(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments?limit=0", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
}

func TestListProjectDeploymentsReturnsNotFoundForUnknownProject(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/deployments", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestGetProjectDeploymentRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProjectDeployment(nil)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/deployments/dep-current", nil)
	request.SetPathValue("projectName", "Demo App")
	request.SetPathValue("deploymentID", "dep-current")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectDeploymentReturnsNotFoundForUnknownProject(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/deployments/dep-current", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestGetProjectDeploymentReturnsNotFoundForMissingDeploymentInKnownProject(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})
	seedProjectRecord(t, db, "alpha-app")

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments/dep-missing", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeDeploymentNotFound, "deployment not found")
}

func TestGetProjectDeploymentReturnsNotFoundForWrongProject(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-other",
		ProjectName:             "beta-app",
		ImageRef:                "localhost:5001/ovek-beta-app:dep-other",
		AppContainerName:        "ovek-beta-app-app-dep-other",
		NetworkName:             "beta-app-net",
		PocketBaseContainerName: "ovek-beta-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-11T00:00:00Z",
	})
	seedProjectRecord(t, db, "alpha-app")

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/deployments/dep-other", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeDeploymentNotFound, "deployment not found")
}
