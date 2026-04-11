package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateDeploymentReturnsQueuedJob(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	handler, db := newTestHandler(t, enqueuer)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/demo.git"}`),
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
	if got := recorder.Header().Get("Location"); got != jobPath(job.ID) {
		t.Fatalf("expected Location header %q, got %q", jobPath(job.ID), got)
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
	if job.Type != jobTypeDeployment {
		t.Fatalf("expected type %q, got %q", jobTypeDeployment, job.Type)
	}
	assertJobLinks(t, job)

	persistedJob, err := getJob(db, job.ID)
	if err != nil {
		t.Fatalf("expected job to persist, got error: %v", err)
	}
	if persistedJob.ID != job.ID {
		t.Fatalf("expected persisted job ID %q, got %q", job.ID, persistedJob.ID)
	}
	if len(enqueuer.jobIDs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(enqueuer.jobIDs))
	}
	if enqueuer.jobIDs[0] != job.ID {
		t.Fatalf("expected enqueued job ID %q, got %q", job.ID, enqueuer.jobIDs[0])
	}
}

func TestCreateDeploymentRejectsInvalidProjectName(t *testing.T) {
	handler := handleCreateDeployment(nil, noopEnqueuer{})

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/demo.git"}`),
	)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestCreateDeploymentRejectsUnknownFields(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"name":"demo-app","repoUrl":"https://example.com/demo.git"}`),
	)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
}

func TestGlobalCreateDeploymentRouteReturnsNotFound(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/deployments",
		strings.NewReader(`{"name":"demo-app","repoUrl":"https://example.com/demo.git"}`),
	)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestGetJobReturnsPersistedJob(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

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
	if job.Type != jobTypeDeployment {
		t.Fatalf("expected type %q, got %q", jobTypeDeployment, job.Type)
	}
	assertJobLinks(t, job)
}

func TestGetJobReturnsNotFoundForUnknownJob(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/unknown-job", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeJobNotFound, "job not found")
}

func newTestHandler(t *testing.T, enqueuer deploymentEnqueuer) (http.Handler, *sql.DB) {
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
	}, db, enqueuer, noopProjectCleaner{})

	return handler, db
}

type noopEnqueuer struct{}

func (noopEnqueuer) Enqueue(string) {}

type noopProjectCleaner struct{}

func (noopProjectCleaner) Cleanup(context.Context, string) error { return nil }

type recordingEnqueuer struct {
	jobIDs []string
}

func (enqueuer *recordingEnqueuer) Enqueue(jobID string) {
	enqueuer.jobIDs = append(enqueuer.jobIDs, jobID)
}

func assertJobLinks(t *testing.T, job job) {
	t.Helper()

	if job.Links.Self != jobPath(job.ID) {
		t.Fatalf("expected self link %q, got %q", jobPath(job.ID), job.Links.Self)
	}
	if job.Links.Logs != jobLogsPath(job.ID) {
		t.Fatalf("expected logs link %q, got %q", jobLogsPath(job.ID), job.Links.Logs)
	}
	if job.Links.LogsStream != jobLogsStreamPath(job.ID) {
		t.Fatalf("expected logsStream link %q, got %q", jobLogsStreamPath(job.ID), job.Links.LogsStream)
	}
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string, wantMessage string) {
	t.Helper()

	if recorder.Code != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected Content-Type %q, got %q", "application/json", got)
	}

	var response apiError
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("expected error response body to decode, got error: %v", err)
	}
	if response.Code != wantCode {
		t.Fatalf("expected error code %q, got %q", wantCode, response.Code)
	}
	if response.Message != wantMessage {
		t.Fatalf("expected error message %q, got %q", wantMessage, response.Message)
	}
}
