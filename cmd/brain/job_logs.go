package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultJobLogStreamPollInterval = 100 * time.Millisecond

func handleGetJobLogs(db *sql.DB, dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimSpace(r.PathValue("jobID"))
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodeJobIDRequired, "job ID is required")
			return
		}

		logs, err := getJobLogs(db, dataDir, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeJobNotFound, "job not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchJobLogsFailed, "failed to fetch job logs")
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(logs)
	}
}

func handleGetJobLogsStream(db *sql.DB, dataDir string) http.HandlerFunc {
	return handleGetJobLogsStreamWithPollInterval(db, dataDir, defaultJobLogStreamPollInterval)
}

func handleGetJobLogsStreamWithPollInterval(db *sql.DB, dataDir string, pollInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimSpace(r.PathValue("jobID"))
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodeJobIDRequired, "job ID is required")
			return
		}

		job, initialLogs, offset, err := prepareJobLogStream(db, dataDir, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeJobNotFound, "job not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchJobLogsFailed, "failed to fetch job logs")
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchJobLogsFailed, "failed to fetch job logs")
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

		if isTerminalJobStatus(job.Status) {
			return
		}

		if err := followJobLogStream(r.Context(), w, flusher, db, dataDir, job, offset, pollInterval); err != nil {
			_ = writeSSEEvent(w, "error", "failed to fetch job logs")
			flusher.Flush()
		}
	}
}

func getJobLogs(db *sql.DB, dataDir string, jobID string) ([]byte, error) {
	job, err := getJob(db, jobID)
	if err != nil {
		return nil, err
	}

	logPath := jobLogPathForJob(dataDir, job)
	logs, _, err := readJobLogDelta(logPath, 0)
	if err != nil {
		return nil, err
	}

	return logs, nil
}

func prepareJobLogStream(db *sql.DB, dataDir string, jobID string) (job, []byte, int, error) {
	job, err := getJob(db, jobID)
	if err != nil {
		return job, nil, 0, err
	}

	logPath := jobLogPathForJob(dataDir, job)
	initialLogs, offset, err := readJobLogDelta(logPath, 0)
	if err != nil {
		return job, nil, 0, err
	}

	return job, initialLogs, offset, nil
}

func followJobLogStream(ctx context.Context, w io.Writer, flusher http.Flusher, db *sql.DB, dataDir string, job job, offset int, pollInterval time.Duration) error {
	logPath := jobLogPathForJob(dataDir, job)
	if pollInterval <= 0 {
		pollInterval = defaultJobLogStreamPollInterval
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval):
		}

		nextJob, err := getJob(db, job.ID)
		if err != nil {
			return err
		}

		nextLogPath := jobLogPathForJob(dataDir, nextJob)
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

		if isTerminalJobStatus(nextJob.Status) {
			return nil
		}

		job = nextJob
	}
}

func jobLogPathForJob(dataDir string, job job) string {
	logPath := strings.TrimSpace(job.LogPath)
	if logPath == "" {
		return jobLogPath(dataDir, job.ID)
	}

	return logPath
}

func readJobLogDelta(logPath string, offset int) ([]byte, int, error) {
	logs, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, offset, nil
	}
	if err != nil {
		return nil, offset, fmt.Errorf("read job log file %q: %w", logPath, err)
	}

	if offset > len(logs) {
		offset = 0
	}

	return logs[offset:], len(logs), nil
}

func writeSSELogEvents(w io.Writer, logs []byte) error {
	if len(logs) == 0 {
		return nil
	}

	for len(logs) > 0 {
		line, rest, foundNewline := bytes.Cut(logs, []byte{'\n'})
		if err := writeSSEEvent(w, "", string(line)); err != nil {
			return err
		}
		if !foundNewline {
			return nil
		}
		logs = rest
	}

	return nil
}

func writeSSEEvent(w io.Writer, event string, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}

	return nil
}

func isTerminalJobStatus(status string) bool {
	return status == jobStatusSucceeded || status == jobStatusFailed
}
