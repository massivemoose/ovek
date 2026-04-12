package main

import (
	"encoding/json"
	"net/http"
)

const (
	errorCodeUnauthorized              = "unauthorized"
	errorCodeInvalidRequestBody        = "invalid_request_body"
	errorCodeInvalidProjectName        = "invalid_project_name"
	errorCodeInvalidLimit              = "invalid_limit"
	errorCodeRepoURLRequired           = "repo_url_required"
	errorCodeCreateJobFailed           = "create_deployment_job_failed"
	errorCodeListProjectsFailed        = "list_projects_failed"
	errorCodeFetchProjectFailed        = "fetch_project_failed"
	errorCodeFetchProjectRuntimeFailed = "fetch_project_runtime_failed"
	errorCodeJobIDRequired             = "job_id_required"
	errorCodeJobNotFound               = "job_not_found"
	errorCodeFetchJobFailed            = "fetch_job_failed"
	errorCodeFetchJobLogsFailed        = "fetch_job_logs_failed"
	errorCodeProjectNotFound           = "project_not_found"
	errorCodeProjectCleanupFailed      = "project_cleanup_failed"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, statusCode int, code string, message string) {
	writeJSON(w, statusCode, apiError{
		Code:    code,
		Message: message,
	})
}
