package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

	wantLogs := "$ railpack build --name ovek-demo-app:" + createdJob.ID + " /tmp/workspace\n"
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

func TestGetJobLogsStreamReturnsSSEForCompletedJob(t *testing.T) {
	handler, db, dataDir := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("expected log directory creation to succeed, got error: %v", err)
	}

	wantBody := "data: first line\n\ndata: second line\n\n"
	if err := os.WriteFile(logPath, []byte("first line\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("expected log file write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ?, status = ? WHERE id = ?", logPath, jobStatusSucceeded, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsStreamPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertSSEResponse(t, recorder, http.StatusOK, wantBody)
}

func TestGetJobLogsStreamFollowsRunningJobUntilCompletion(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("expected log directory creation to succeed, got error: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("first line\n"), 0o644); err != nil {
		t.Fatalf("expected initial log file write to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET status = ? WHERE id = ?", jobStatusRunning, createdJob.ID); err != nil {
		t.Fatalf("expected job status update to succeed, got error: %v", err)
	}

	handler := handleGetJobLogsStreamWithPollInterval(db, dataDir, time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, jobLogsStreamPath(createdJob.ID), nil)
	request.SetPathValue("jobID", createdJob.ID)

	ctx, cancel := context.WithTimeout(request.Context(), time.Second)
	defer cancel()
	request = request.WithContext(ctx)

	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("expected log file append open to succeed, got error: %v", err)
	}
	if _, err := file.WriteString("second line\n"); err != nil {
		_ = file.Close()
		t.Fatalf("expected log file append to succeed, got error: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("expected log file close to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET status = ?, log_path = ? WHERE id = ?", jobStatusSucceeded, logPath, createdJob.ID); err != nil {
		t.Fatalf("expected terminal job update to succeed, got error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected stream handler to complete after job reached terminal status")
	}

	assertSSEResponse(t, recorder, http.StatusOK, recorder.Body.String())
	if !strings.Contains(recorder.Body.String(), "data: first line\n\n") {
		t.Fatalf("expected first streamed log line in body %q", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "data: second line\n\n") {
		t.Fatalf("expected second streamed log line in body %q", recorder.Body.String())
	}
}

func TestGetJobLogsStreamReturnsNotFoundForUnknownJob(t *testing.T) {
	handler, _, _ := newJobLogsTestHandler(t)

	request := httptest.NewRequest(http.MethodGet, jobLogsStreamPath("missing-job"), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeJobNotFound, "job not found")
}

func TestGetJobLogsStreamReturnsInternalServerErrorWhenInitialLogFileCannotBeRead(t *testing.T) {
	handler, db, dataDir := newJobLogsTestHandler(t)

	createdJob, err := createQueuedJob(db, "demo-app", "https://example.com/demo.git")
	if err != nil {
		t.Fatalf("expected job creation to succeed, got error: %v", err)
	}

	logPath := jobLogPath(dataDir, createdJob.ID)
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatalf("expected unreadable log path setup to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE jobs SET log_path = ?, status = ? WHERE id = ?", logPath, jobStatusSucceeded, createdJob.ID); err != nil {
		t.Fatalf("expected log path update to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, jobLogsStreamPath(createdJob.ID), nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusInternalServerError, errorCodeFetchJobLogsFailed, "failed to fetch job logs")
}

func TestStreamSSELogReaderBuffersSplitReadsUntilCompleteLine(t *testing.T) {
	var body bytes.Buffer
	flusher := &countingFlusher{}

	logs := newScriptedReadCloser([]scriptedRead{
		{data: "first "},
		{data: "line\nsecond line\n", err: io.EOF},
	})

	err := streamSSELogReader(context.Background(), &body, flusher, logs)
	if err != nil {
		t.Fatalf("expected stream to succeed, got error: %v", err)
	}

	if got := body.String(); got != "data: first line\n\ndata: second line\n\n" {
		t.Fatalf("expected combined SSE body, got %q", got)
	}
	if flusher.flushes != 1 {
		t.Fatalf("expected 1 flush for complete lines, got %d", flusher.flushes)
	}
}

func TestStreamSSELogReaderEmitsFinalUnterminatedLineAtEOF(t *testing.T) {
	var body bytes.Buffer
	flusher := &countingFlusher{}

	logs := newScriptedReadCloser([]scriptedRead{
		{data: "last line", err: io.EOF},
	})

	err := streamSSELogReader(context.Background(), &body, flusher, logs)
	if err != nil {
		t.Fatalf("expected stream to succeed, got error: %v", err)
	}

	if got := body.String(); got != "data: last line\n\n" {
		t.Fatalf("expected final unterminated line to be emitted, got %q", got)
	}
	if flusher.flushes != 1 {
		t.Fatalf("expected 1 flush at EOF, got %d", flusher.flushes)
	}
}

func TestStreamSSELogReaderReturnsNilOnContextCancellation(t *testing.T) {
	var body bytes.Buffer
	flusher := &countingFlusher{}

	logs := newBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- streamSSELogReader(ctx, &body, flusher, logs)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected cancellation to end cleanly, got error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected stream reader to exit after context cancellation")
	}

	if !logs.closed {
		t.Fatal("expected blocking reader to be closed on cancellation")
	}
	if got := body.String(); got != "" {
		t.Fatalf("expected no SSE body on cancellation, got %q", got)
	}
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

func assertSSEResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()

	if recorder.Code != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected Content-Type %q, got %q", "text/event-stream", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected Cache-Control %q, got %q", "no-cache", got)
	}
	if got := recorder.Header().Get("Connection"); got != "keep-alive" {
		t.Fatalf("expected Connection %q, got %q", "keep-alive", got)
	}
	if got := recorder.Body.String(); got != wantBody {
		t.Fatalf("expected body %q, got %q", wantBody, got)
	}
}

type countingFlusher struct {
	flushes int
}

func (flusher *countingFlusher) Flush() {
	flusher.flushes++
}

type scriptedRead struct {
	data string
	err  error
}

type scriptedReadCloser struct {
	reads  []scriptedRead
	closed bool
}

func newScriptedReadCloser(reads []scriptedRead) *scriptedReadCloser {
	return &scriptedReadCloser{
		reads: append([]scriptedRead(nil), reads...),
	}
}

func (reader *scriptedReadCloser) Read(p []byte) (int, error) {
	if len(reader.reads) == 0 {
		return 0, io.EOF
	}

	next := reader.reads[0]
	reader.reads = reader.reads[1:]
	count := copy(p, next.data)
	if count != len(next.data) {
		panic("scripted read larger than destination buffer")
	}

	return count, next.err
}

func (reader *scriptedReadCloser) Close() error {
	reader.closed = true
	return nil
}

var errBlockingReadClosed = errors.New("blocking read closed")

type blockingReadCloser struct {
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{done: make(chan struct{})}
}

func (reader *blockingReadCloser) Read(_ []byte) (int, error) {
	<-reader.done
	return 0, errBlockingReadClosed
}

func (reader *blockingReadCloser) Close() error {
	reader.closeOnce.Do(func() {
		reader.closed = true
		close(reader.done)
	})

	return nil
}
