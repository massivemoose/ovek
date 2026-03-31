package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateDeploymentReturnsQueuedJob(t *testing.T) {
	handler, db := newTestHandler(t)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/deployments",
		strings.NewReader(`{"name":"demo-app","repoUrl":"https://example.com/demo.git"}`),
	)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, recorder.Code)
	}

	var job job
	if err := json.NewDecoder(recorder.Body).Decode(&job); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if job.ProjectName != "demo-app" {
		t.Fatalf("expected project name %q, got %q", "demo-app", job.ProjectName)
	}
	if job.RepoURL != "https://example.com/demo.git" {
		t.Fatalf("expected repo URL %q, got %q", "https://example.com/demo.git", job.RepoURL)
	}
	if job.Status != jobStatusQueued {
		t.Fatalf("expected status %q, got %q", jobStatusQueued, job.Status)
	}

	persistedJob, err := getJob(db, job.ID)
	if err != nil {
		t.Fatalf("expected job to persist, got error: %v", err)
	}
	if persistedJob.ID != job.ID {
		t.Fatalf("expected persisted job ID %q, got %q", job.ID, persistedJob.ID)
	}
}

func TestCreateDeploymentRejectsInvalidProjectName(t *testing.T) {
	handler, _ := newTestHandler(t)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/deployments",
		strings.NewReader(`{"name":"Demo App","repoUrl":"https://example.com/demo.git"}`),
	)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
}

func TestGetJobReturnsPersistedJob(t *testing.T) {
	handler, db := newTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+createdJob.ID, nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var job job
	if err := json.NewDecoder(recorder.Body).Decode(&job); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if job.ID != createdJob.ID {
		t.Fatalf("expected job ID %q, got %q", createdJob.ID, job.ID)
	}
	if job.Status != jobStatusQueued {
		t.Fatalf("expected status %q, got %q", jobStatusQueued, job.Status)
	}
}

func TestGetJobReturnsNotFoundForUnknownJob(t *testing.T) {
	handler, _ := newTestHandler(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/unknown-job", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func newTestHandler(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()

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
	}, db)

	return handler, db
}
