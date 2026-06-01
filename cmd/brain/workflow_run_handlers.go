package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	headerWorkflowTriggerToken = "X-Ovek-Workflow-Token"
	headerIdempotencyKey       = "Idempotency-Key"
	maxWorkflowRunPayloadBytes = 64 * 1024
	maxWorkflowRunRequestBytes = maxWorkflowRunPayloadBytes + 1024
)

type workflowRunEnqueuer interface {
	Enqueue(runID string)
}

func handleCreateWorkflowRun(cfg config, db *sql.DB, enqueuer workflowRunEnqueuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}

		auth, ok := authenticateWorkflowRunCreateRequest(w, r, cfg, db, projectName, workflowName)
		if !ok {
			return
		}

		request := createWorkflowRunRequest{TriggerType: auth.defaultTriggerType}
		if r.Body != nil {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkflowRunRequestBytes))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
				if strings.Contains(err.Error(), "http: request body too large") {
					writeJSONError(w, http.StatusBadRequest, errorCodeWorkflowPayloadTooLarge, "workflow payload too large")
					return
				}
				writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
				return
			}
		}
		if request.TriggerType == "" {
			request.TriggerType = auth.defaultTriggerType
		}
		if auth.triggerTokenID != "" && request.TriggerType != workflowRunTriggerAPI {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, "triggerType must be api")
			return
		}
		if request.TriggerType != workflowRunTriggerAPI && request.TriggerType != workflowRunTriggerManual {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, "triggerType must be api or manual")
			return
		}
		payload := normalizeWorkflowRunPayload(request.Payload)
		if len(payload) > maxWorkflowRunPayloadBytes {
			writeJSONError(w, http.StatusBadRequest, errorCodeWorkflowPayloadTooLarge, "workflow payload too large")
			return
		}
		if !json.Valid(payload) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflowPayload, "invalid workflow payload")
			return
		}

		run, created, err := createQueuedWorkflowRunWithOptions(r.Context(), db, projectName, workflowName, createWorkflowRunOptions{
			TriggerType:    request.TriggerType,
			Payload:        payload,
			IdempotencyKey: strings.TrimSpace(r.Header.Get(headerIdempotencyKey)),
			TriggerTokenID: auth.triggerTokenID,
		})
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
				"actor":    auth.actor,
			}),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})

		if created && enqueuer != nil {
			enqueuer.Enqueue(run.ID)
		}

		w.Header().Set("Location", run.Links.Self)
		writeJSON(w, http.StatusAccepted, run)
	}
}

type workflowRunCreateAuth struct {
	actor              string
	defaultTriggerType string
	triggerTokenID     string
}

func authenticateWorkflowRunCreateRequest(w http.ResponseWriter, r *http.Request, cfg config, db *sql.DB, projectName string, workflowName string) (workflowRunCreateAuth, bool) {
	if token := strings.TrimSpace(r.Header.Get(headerWorkflowTriggerToken)); token != "" {
		tokenID, err := validateWorkflowTriggerToken(r.Context(), db, projectName, workflowName, token)
		if errors.Is(err, errInvalidWorkflowTriggerToken) {
			writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
			return workflowRunCreateAuth{}, false
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowTriggerTokenFailed, "failed to validate workflow trigger token")
			return workflowRunCreateAuth{}, false
		}
		return workflowRunCreateAuth{
			actor:              "workflow-trigger-token",
			defaultTriggerType: workflowRunTriggerAPI,
			triggerTokenID:     tokenID,
		}, true
	}

	if cfg.AuthMode == authModeProd {
		principal, err := validateAPIKey(r.Context(), db, r.Header.Get(headerAPIKey))
		if errors.Is(err, errInvalidAPIKey) {
			reason := authFailureReason(err)
			log.Printf("auth api key rejected: reason=%s path=%s", reason, r.URL.Path)
			_ = insertAuditLog(r.Context(), db, auditLogRecord{
				EventType:   "auth.api_key_rejected",
				DetailsJSON: mustDetailsJSON(map[string]string{"reason": reason, "path": r.URL.Path}),
				CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
			})
			writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
			return workflowRunCreateAuth{}, false
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeUnauthorized, "unauthorized")
			return workflowRunCreateAuth{}, false
		}
		return workflowRunCreateAuth{actor: principal.Username, defaultTriggerType: workflowRunTriggerAPI}, true
	}

	if subtle.ConstantTimeCompare([]byte(r.Header.Get(headerAPIKey)), []byte(cfg.BrainAPIKey)) != 1 {
		writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
		return workflowRunCreateAuth{}, false
	}
	return workflowRunCreateAuth{actor: "dev", defaultTriggerType: workflowRunTriggerAPI}, true
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
