package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type workflowRunEnqueuer interface {
	Enqueue(runID string)
}

func handleCreateWorkflowRun(db *sql.DB, enqueuer workflowRunEnqueuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}

		request := createWorkflowRunRequest{TriggerType: workflowRunTriggerAPI}
		if r.Body != nil {
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
				writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
				return
			}
		}
		if request.TriggerType == "" {
			request.TriggerType = workflowRunTriggerAPI
		}
		if request.TriggerType != workflowRunTriggerAPI && request.TriggerType != workflowRunTriggerManual {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, "triggerType must be api or manual")
			return
		}

		run, err := createQueuedWorkflowRun(r.Context(), db, projectName, workflowName, request.TriggerType)
		if errors.Is(err, errWorkflowNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowNotFound, "workflow not found")
			return
		}
		if errors.Is(err, errWorkflowQueueFull) {
			writeJSONError(w, http.StatusTooManyRequests, errorCodeWorkflowQueueFull, "workflow queue full")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to create workflow run")
			return
		}

		_ = insertAuditLog(r.Context(), db, auditLogRecord{
			EventType:   "workflow.run.created",
			ProjectName: sql.NullString{String: projectName, Valid: true},
			DetailsJSON: mustDetailsJSON(map[string]string{
				"workflow": run.WorkflowName,
				"run_id":   run.ID,
				"trigger":  run.TriggerType,
				"actor":    requestActor(r),
			}),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})

		if enqueuer != nil {
			enqueuer.Enqueue(run.ID)
		}

		w.Header().Set("Location", run.Links.Self)
		writeJSON(w, http.StatusAccepted, run)
	}
}

func handleListWorkflowRuns(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, ok := validateWorkflowProjectPath(w, r)
		if !ok {
			return
		}
		limit, err := parseListLimit(r, defaultListLimit)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
			return
		}

		exists, err := projectExists(db, projectName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to list workflow runs")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}

		runs, err := listWorkflowRuns(r.Context(), db, projectName, limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to list workflow runs")
			return
		}

		writeJSON(w, http.StatusOK, runs)
	}
}

func handleGetWorkflowRun(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, runID, ok := validateWorkflowRunPath(w, r)
		if !ok {
			return
		}

		run, err := getWorkflowRun(r.Context(), db, projectName, runID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowRunNotFound, "workflow run not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to fetch workflow run")
			return
		}

		writeJSON(w, http.StatusOK, run)
	}
}

func validateWorkflowRunPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	projectName, ok := validateWorkflowProjectPath(w, r)
	if !ok {
		return "", "", false
	}

	runID := strings.TrimSpace(r.PathValue("runID"))
	if runID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, "workflow run ID is required")
		return "", "", false
	}

	return projectName, runID, true
}
