package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
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
	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusDeploying {
		t.Fatalf("expected project status %q, got %q", projectStatusDeploying, got)
	}
}

func TestCreateDeploymentRejectsDuplicateActiveJob(t *testing.T) {
	enqueuer := &recordingEnqueuer{}
	handler, db := newTestHandler(t, enqueuer)

	activeJob, err := createQueuedJob(db, "demo-app", "https://example.com/first.git")
	if err != nil {
		t.Fatalf("expected active job seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET status = ? WHERE id = ?", jobStatusRunning, activeJob.ID); err != nil {
		t.Fatalf("expected active job update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/second.git"}`),
	)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d with body %q", http.StatusConflict, recorder.Code, recorder.Body.String())
	}

	var response apiError
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}
	if response.Code != errorCodeActiveDeploymentExists {
		t.Fatalf("expected error code %q, got %q", errorCodeActiveDeploymentExists, response.Code)
	}
	if !strings.Contains(response.Message, activeJob.ID) {
		t.Fatalf("expected error message to include active job ID %q, got %q", activeJob.ID, response.Message)
	}
	if len(enqueuer.jobIDs) != 0 {
		t.Fatalf("expected no enqueued jobs, got %d", len(enqueuer.jobIDs))
	}

	count := queryCount(t, db, `SELECT COUNT(1) FROM jobs WHERE project_name = ?`, "demo-app")
	if count != 1 {
		t.Fatalf("expected only the active job to remain, got %d jobs", count)
	}
}

func TestListProjectJobsReturnsNewestFirst(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	oldJob, err := createQueuedJob(db, "alpha-app", "https://example.com/old.git")
	if err != nil {
		t.Fatalf("expected old job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE jobs
		 SET created_at = ?, status = ?, finished_at = ?, error_message = ?
		 WHERE id = ?`,
		"2026-04-09T00:00:00Z",
		jobStatusFailed,
		"2026-04-09T00:01:00Z",
		"build failed",
		oldJob.ID,
	); err != nil {
		t.Fatalf("expected old job update to succeed, got error: %v", err)
	}

	currentJob, err := createQueuedJob(db, "alpha-app", "https://example.com/current.git")
	if err != nil {
		t.Fatalf("expected current job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE jobs
		 SET created_at = ?, status = ?, started_at = ?, finished_at = ?, log_path = ?, image_ref = ?
		 WHERE id = ?`,
		"2026-04-10T00:00:00Z",
		jobStatusSucceeded,
		"2026-04-10T00:00:05Z",
		"2026-04-10T00:01:00Z",
		"/tmp/current.log",
		"localhost:5001/ovek-alpha-app:dep-current",
		currentJob.ID,
	); err != nil {
		t.Fatalf("expected current job update to succeed, got error: %v", err)
	}

	otherJob, err := createQueuedJob(db, "beta-app", "https://example.com/other.git")
	if err != nil {
		t.Fatalf("expected other job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET created_at = ? WHERE id = ?", "2026-04-11T00:00:00Z", otherJob.ID); err != nil {
		t.Fatalf("expected other job update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/jobs", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var jobs []job
	if err := json.NewDecoder(recorder.Body).Decode(&jobs); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].ID != currentJob.ID {
		t.Fatalf("expected newest job %q, got %q", currentJob.ID, jobs[0].ID)
	}
	if jobs[0].Status != jobStatusSucceeded {
		t.Fatalf("expected current job status %q, got %q", jobStatusSucceeded, jobs[0].Status)
	}
	assertJobLinks(t, jobs[0])
	if jobs[1].ID != oldJob.ID {
		t.Fatalf("expected older job %q, got %q", oldJob.ID, jobs[1].ID)
	}
	if jobs[1].Status != jobStatusFailed {
		t.Fatalf("expected older job status %q, got %q", jobStatusFailed, jobs[1].Status)
	}
	assertJobLinks(t, jobs[1])
}

func TestListProjectJobsReturnsEmptyArrayForKnownProjectWithoutJobs(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})
	seedProjectRecord(t, db, "alpha-app")

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/jobs", nil)
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

func TestListProjectJobsHonorsLimit(t *testing.T) {
	handler, db := newTestHandler(t, noopEnqueuer{})

	oldJob, err := createQueuedJob(db, "alpha-app", "https://example.com/old.git")
	if err != nil {
		t.Fatalf("expected old job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET created_at = ? WHERE id = ?", "2026-04-09T00:00:00Z", oldJob.ID); err != nil {
		t.Fatalf("expected old job update to succeed, got error: %v", err)
	}

	currentJob, err := createQueuedJob(db, "alpha-app", "https://example.com/current.git")
	if err != nil {
		t.Fatalf("expected current job creation to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET created_at = ? WHERE id = ?", "2026-04-10T00:00:00Z", currentJob.ID); err != nil {
		t.Fatalf("expected current job update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/jobs?limit=1", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var jobs []job
	if err := json.NewDecoder(recorder.Body).Decode(&jobs); err != nil {
		t.Fatalf("expected response body to decode, got error: %v", err)
	}

	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].ID != currentJob.ID {
		t.Fatalf("expected limited job %q, got %q", currentJob.ID, jobs[0].ID)
	}
}

func TestListProjectJobsRejectsInvalidProjectName(t *testing.T) {
	handler := handleListProjectJobs(nil)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/jobs", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestListProjectJobsRejectsInvalidLimit(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/jobs?limit=0", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
}

func TestListProjectJobsReturnsNotFoundForUnknownProject(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/jobs", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
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
	}, db, enqueuer, noopProjectCleaner{}, noopProjectRuntimeService{}, managedProjectPocketBaseService{})

	return handler, db
}

type noopEnqueuer struct{}

func (noopEnqueuer) Enqueue(string) {}

type noopProjectCleaner struct{}

func (noopProjectCleaner) Cleanup(context.Context, string) error { return nil }

type noopProjectRuntimeService struct{}

func (noopProjectRuntimeService) GetRuntime(context.Context, string) (projectRuntimeView, error) {
	return projectRuntimeView{}, nil
}

func (noopProjectRuntimeService) ReadRuntimeLogs(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (noopProjectRuntimeService) StreamRuntimeLogs(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

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
