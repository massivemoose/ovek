package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/robfig/cron/v3"
)

var workflowCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

type passthroughWorkflowImageResolver struct{}

func (passthroughWorkflowImageResolver) ResolveWorkflowImage(_ context.Context, imageRef string) (workflowImageMetadata, error) {
	return workflowImageMetadata{
		SourceImageRef: imageRef,
		RuntimeImageID: imageRef,
	}, nil
}

func handleListWorkflowDefinitions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, ok := validateWorkflowProjectPath(w, r)
		if !ok {
			return
		}

		exists, err := projectExists(db, projectName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to list workflows")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}

		workflows, err := listWorkflowDefinitions(r.Context(), db, projectName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to list workflows")
			return
		}

		writeJSON(w, http.StatusOK, workflows)
	}
}

func handleGetWorkflowDefinition(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}

		workflow, err := getWorkflowDefinition(r.Context(), db, projectName, workflowName)
		if errors.Is(err, errWorkflowNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowNotFound, "workflow not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to fetch workflow")
			return
		}

		writeJSON(w, http.StatusOK, workflow)
	}
}

func handleUpsertWorkflowDefinition(db *sql.DB, imageResolver workflowImageResolver, schedules workflowScheduleController) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}

		var request upsertWorkflowRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		imageRef, err := normalizeCapsuleRef(request.ImageRef)
		if errors.Is(err, errCapsuleRefRequired) {
			writeJSONError(w, http.StatusBadRequest, errorCodeCapsuleRefRequired, "imageRef is required")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidCapsuleRef, strings.ReplaceAll(err.Error(), "capsuleRef", "imageRef"))
			return
		}

		schedule, err := normalizeWorkflowSchedule(request.Schedule)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, err.Error())
			return
		}

		if request.QueueCap < 0 {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflow, "queueCap must be positive")
			return
		}

		resolver := imageResolver
		if resolver == nil {
			resolver = passthroughWorkflowImageResolver{}
		}
		imageMetadata, err := resolver.ResolveWorkflowImage(r.Context(), imageRef)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, errorCodeWorkflowFailed, err.Error())
			return
		}
		if imageMetadata.SourceImageRef == "" {
			imageMetadata.SourceImageRef = imageRef
		}

		enabled := true
		if request.Enabled != nil {
			enabled = *request.Enabled
		}

		workflow, err := upsertWorkflowDefinition(r.Context(), db, workflowDefinition{
			ProjectName:        projectName,
			Name:               workflowName,
			SourceImageRef:     imageMetadata.SourceImageRef,
			ResolvedRepoDigest: imageMetadata.ResolvedRepoDigest,
			RuntimeImageID:     imageMetadata.RuntimeImageID,
			Schedule:           schedule,
			QueueCap:           request.QueueCap,
			Enabled:            enabled,
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to save workflow")
			return
		}
		if schedules != nil {
			if err := schedules.Replace(workflow); err != nil {
				writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to schedule workflow")
				return
			}
		}

		writeJSON(w, http.StatusOK, workflow)
	}
}

func handleDeleteWorkflowDefinition(db *sql.DB, schedules workflowScheduleController) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}

		if err := deleteWorkflowDefinition(r.Context(), db, projectName, workflowName); errors.Is(err, errWorkflowNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowNotFound, "workflow not found")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowFailed, "failed to delete workflow")
			return
		}
		if schedules != nil {
			schedules.Remove(projectName, workflowName)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func validateWorkflowProjectPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	projectName := strings.TrimSpace(r.PathValue("projectName"))
	if !isValidProjectName(projectName) {
		writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
		return "", false
	}

	return projectName, true
}

func validateWorkflowDefinitionPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	projectName, ok := validateWorkflowProjectPath(w, r)
	if !ok {
		return "", "", false
	}

	workflowName := strings.TrimSpace(r.PathValue("workflowName"))
	if !isValidWorkflowName(workflowName) {
		writeJSONError(w, http.StatusBadRequest, errorCodeInvalidWorkflowName, "invalid workflow name")
		return "", "", false
	}

	return projectName, workflowName, true
}

func normalizeWorkflowSchedule(schedule string) (string, error) {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return "", nil
	}
	if _, err := workflowCronParser.Parse(schedule); err != nil {
		return "", fmt.Errorf("invalid schedule: %w", err)
	}

	return schedule, nil
}
