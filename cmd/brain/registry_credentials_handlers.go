package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func handleListRegistryCredentials(store registryCredentialStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credentials, err := store.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeRegistryCredentialFailed, "failed to list registry credentials")
			return
		}

		writeJSON(w, http.StatusOK, credentials)
	}
}

func handleUpsertRegistryCredential(store registryCredentialStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		host := strings.TrimSpace(r.PathValue("host"))
		var request brainapi.UpsertRegistryCredentialRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		credential, err := store.Upsert(r.Context(), host, request.Username, request.Password, requestActor(r))
		if err != nil {
			writeRegistryCredentialError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, credential)
	}
}

func handleDeleteRegistryCredential(store registryCredentialStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSpace(r.PathValue("host"))
		err := store.Delete(r.Context(), host, requestActor(r))
		if errors.Is(err, errRegistryCredentialNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeRegistryCredentialNotFound, "registry credential not found")
			return
		}
		if err != nil {
			writeRegistryCredentialError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func writeRegistryCredentialError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "registry host") ||
		strings.Contains(message, "registry username") ||
		strings.Contains(message, "registry password"):
		writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRegistryCredential, message)
	default:
		writeJSONError(w, http.StatusInternalServerError, errorCodeRegistryCredentialFailed, "failed to update registry credential")
	}
}
