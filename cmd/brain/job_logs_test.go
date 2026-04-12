package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGetJobLogsReturnsPersistedLogContents(t *testing.T) {
	handler, db, dataDir := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("expected log directory creation to succeed, got error: %v", err)
	}

	wantLogs := "$ git clone --depth 1 https://example.com/demo.git\nbuild ok\n"
	if err := os.WriteFile(logPath, []byte(wantLogs), 0o644); err != nil {
		t.Fatalf("expected log file write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ?, status = ? WHERE id = ?", logPath, jobStatusSucceeded, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertTextResponse(t, recorder, http.StatusOK, wantLogs)
}

func TestGetJobLogsReturnsInProgressLogContentsWithoutPersistedLogPath(t *testing.T) {
	handler, db, dataDir := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("expected log directory creation to succeed, got error: %v", err)
	}

	wantLogs := "$ railpack build --name alces-demo-app:" + createdJob.ID + " /tmp/workspace\n"
	if err := os.WriteFile(logPath, []byte(wantLogs), 0o644); err != nil {
		t.Fatalf("expected log file write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET status = ? WHERE id = ?", jobStatusRunning, createdJob.ID); err != nil {
		t.Fatalf("expected job status update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertTextResponse(t, recorder, http.StatusOK, wantLogs)
}

func TestGetJobLogsReturnsEmptyBodyWhenLogFileDoesNotExistYet(t *testing.T) {
	handler, db, _ := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertTextResponse(t, recorder, http.StatusOK, "")
}

func TestGetJobLogsReturnsNotFoundForUnknownJob(t *testing.T) {
	handler, _, _ := newJobLogsTestHandler(t)

	request := httptest.NewRequest(http.MethodGet, jobLogsPath("missing-job"), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeJobNotFound, "job not found")
}

func TestGetJobLogsReturnsInternalServerErrorWhenLogFileCannotBeRead(t *testing.T) {
	handler, db, dataDir := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatalf("expected unreadable log path setup to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ? WHERE id = ?", logPath, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusInternalServerError, errorCodeFetchJobLogsFailed, "failed to fetch job logs")
}

func newJobLogsTestHandler(t *testing.T) (http.Handler, *sql.DB, string) {
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
	}, db, noopEnqueuer{}, noopProjectCleaner{}, noopProjectRuntimeService{})

	return handler, db, dataDir
}

func assertTextResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()

	if recorder.Code != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("expected Content-Type %q, got %q", "text/plain; charset=utf-8", got)
	}
	if got := recorder.Body.String(); got != wantBody {
		t.Fatalf("expected body %q, got %q", wantBody, got)
	}
}
