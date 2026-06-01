package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const workflowLogsDirName = "workflow-logs"
const workflowPayloadsDirName = "workflow-payloads"

func workflowLogPath(dataDir string, runID string) string {
	return filepath.Join(dataDir, workflowLogsDirName, runID+".log")
}

func workflowPayloadPath(dataDir string, runID string) string {
	return filepath.Join(dataDir, workflowPayloadsDirName, runID+".json")
}

func handleGetWorkflowRunLogs(db *sql.DB, dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, runID, ok := validateWorkflowRunPath(w, r)
		if !ok {
			return
		}

		logs, err := getWorkflowRunLogs(r.Context(), db, dataDir, projectName, runID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowRunNotFound, "workflow run not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchWorkflowLogsFailed, "failed to fetch workflow logs")
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(logs)
	}
}

func handleGetWorkflowRunLogsStream(db *sql.DB, dataDir string) http.HandlerFunc {
	return handleGetWorkflowRunLogsStreamWithPollInterval(db, dataDir, defaultJobLogStreamPollInterval)
}

func handleGetWorkflowRunLogsStreamWithPollInterval(db *sql.DB, dataDir string, pollInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, runID, ok := validateWorkflowRunPath(w, r)
		if !ok {
			return
		}

		run, initialLogs, offset, err := prepareWorkflowRunLogStream(r.Context(), db, dataDir, projectName, runID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowRunNotFound, "workflow run not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchWorkflowLogsFailed, "failed to fetch workflow logs")
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchWorkflowLogsFailed, "failed to fetch workflow logs")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if err := writeSSELogEvents(w, initialLogs); err != nil {
			return
		}
		flusher.Flush()

		if isTerminalWorkflowRunStatus(run.Status) {
			return
		}

		if err := followWorkflowRunLogStream(r.Context(), w, flusher, db, dataDir, run, offset, pollInterval); err != nil {
			_ = writeSSEEvent(w, "error", "failed to fetch workflow logs")
			flusher.Flush()
		}
	}
}

func getWorkflowRunLogs(ctx context.Context, db *sql.DB, dataDir string, projectName string, runID string) ([]byte, error) {
	run, err := getWorkflowRun(ctx, db, projectName, runID)
	if err != nil {
		return nil, err
	}

	logs, _, err := readJobLogDelta(workflowLogPathForRun(dataDir, run), 0)
	if err != nil {
		return nil, err
	}

	return logs, nil
}

func prepareWorkflowRunLogStream(ctx context.Context, db *sql.DB, dataDir string, projectName string, runID string) (workflowRun, []byte, int, error) {
	run, err := getWorkflowRun(ctx, db, projectName, runID)
	if err != nil {
		return workflowRun{}, nil, 0, err
	}

	initialLogs, offset, err := readJobLogDelta(workflowLogPathForRun(dataDir, run), 0)
	if err != nil {
		return workflowRun{}, nil, 0, err
	}

	return run, initialLogs, offset, nil
}

func followWorkflowRunLogStream(ctx context.Context, w io.Writer, flusher http.Flusher, db *sql.DB, dataDir string, run workflowRun, offset int, pollInterval time.Duration) error {
	logPath := workflowLogPathForRun(dataDir, run)
	if pollInterval <= 0 {
		pollInterval = defaultJobLogStreamPollInterval
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval):
		}

		nextRun, err := getWorkflowRun(ctx, db, run.ProjectName, run.ID)
		if err != nil {
			return err
		}

		nextLogPath := workflowLogPathForRun(dataDir, nextRun)
		if nextLogPath != logPath {
			logPath = nextLogPath
			offset = 0
		}

		logs, nextOffset, err := readJobLogDelta(logPath, offset)
		if err != nil {
			return err
		}
		offset = nextOffset

		if err := writeSSELogEvents(w, logs); err != nil {
			return err
		}
		if len(logs) > 0 {
			flusher.Flush()
		}

		if isTerminalWorkflowRunStatus(nextRun.Status) {
			return nil
		}

		run = nextRun
	}
}

func workflowLogPathForRun(dataDir string, run workflowRun) string {
	logPath := strings.TrimSpace(run.LogPath)
	if logPath == "" {
		return workflowLogPath(dataDir, run.ID)
	}

	return logPath
}

func isTerminalWorkflowRunStatus(status string) bool {
	return status == workflowRunStatusSucceeded ||
		status == workflowRunStatusFailed ||
		status == workflowRunStatusSkipped ||
		status == workflowRunStatusCanceled ||
		status == workflowRunStatusTimedOut
}
