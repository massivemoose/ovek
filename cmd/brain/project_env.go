package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func handleListProjectEnvironment(store projectConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		entries, err := store.ListEnvironment(r.Context(), projectName)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeProjectEnvFailed, "failed to list project environment")
			return
		}

		writeJSON(w, http.StatusOK, entries)
	}
}

func handleSetProjectEnvironment(store projectConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		name := strings.TrimSpace(r.PathValue("name"))
		var request brainapi.SetProjectEnvironmentRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		mutation, err := store.SetEnvironmentEntry(r.Context(), projectName, name, request.Value, request.Secret, requestActor(r))
		if err != nil {
			writeProjectConfigError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, mutation)
	}
}

func handleDeleteProjectEnvironment(store projectConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		name := strings.TrimSpace(r.PathValue("name"))
		mutation, err := store.DeleteEnvironmentEntry(r.Context(), projectName, name, requestActor(r))
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if errors.Is(err, errProjectEnvNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectEnvNotFound, "project environment entry not found")
			return
		}
		if err != nil {
			writeProjectConfigError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, mutation)
	}
}

func writeProjectConfigError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "environment variable"):
		writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectEnv, message)
	default:
		writeJSONError(w, http.StatusInternalServerError, errorCodeProjectEnvFailed, "failed to update project environment")
	}
}

func requestActor(r *http.Request) string {
	if principal, ok := authPrincipalFromContext(r.Context()); ok {
		return principal.Username
	}

	return "dev"
}
