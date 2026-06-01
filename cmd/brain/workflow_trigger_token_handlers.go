package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func handleListWorkflowTriggerTokens(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}
		tokens, err := listWorkflowTriggerTokens(r.Context(), db, projectName, workflowName)
		if errors.Is(err, errWorkflowNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowNotFound, "workflow not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowTriggerTokenFailed, "failed to list workflow trigger tokens")
			return
		}
		writeJSON(w, http.StatusOK, tokens)
	}
}

func handleCreateWorkflowTriggerToken(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}
		var request brainapi.CreateWorkflowTriggerTokenRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		response, err := createWorkflowTriggerToken(r.Context(), db, projectName, workflowName, request.Label, requestActor(r))
		if errors.Is(err, errWorkflowNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowNotFound, "workflow not found")
			return
		}
		if err != nil && strings.Contains(err.Error(), "label") {
			writeJSONError(w, http.StatusBadRequest, errorCodeWorkflowTriggerTokenFailed, err.Error())
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowTriggerTokenFailed, "failed to create workflow trigger token")
			return
		}
		writeJSON(w, http.StatusCreated, response)
	}
}

func handleRevokeWorkflowTriggerToken(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName, workflowName, ok := validateWorkflowDefinitionPath(w, r)
		if !ok {
			return
		}
		tokenID := strings.TrimSpace(r.PathValue("tokenID"))
		if err := revokeWorkflowTriggerToken(r.Context(), db, projectName, workflowName, tokenID, requestActor(r)); errors.Is(err, errWorkflowTriggerTokenNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkflowTriggerTokenNotFound, "workflow trigger token not found")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeWorkflowTriggerTokenFailed, "failed to revoke workflow trigger token")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
