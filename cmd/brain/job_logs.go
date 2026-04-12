package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

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

func getJobLogs(db *sql.DB, dataDir string, jobID string) ([]byte, error) {
	job, err := getJob(db, jobID)
	if err != nil {
		return nil, err
	}

	logPath := strings.TrimSpace(job.LogPath)
	if logPath == "" {
		logPath = jobLogPath(dataDir, job.ID)
	}

	logs, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read job log file %q: %w", logPath, err)
	}

	return logs, nil
}
