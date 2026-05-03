package main

import (
	"encoding/json"
	"net/http"
)

const (
	errorCodeUnauthorized              = "unauthorized"
	errorCodeReauthRequired            = "reauth_required"
	errorCodeAuthBootstrapDisabled     = "auth_bootstrap_disabled"
	errorCodeAuthBootstrapFailed       = "auth_bootstrap_failed"
	errorCodeAuthReauthFailed          = "auth_reauth_failed"
	errorCodeInvalidRequestBody        = "invalid_request_body"
	errorCodeInvalidProjectName        = "invalid_project_name"
	errorCodeInvalidLimit              = "invalid_limit"
	errorCodeRepoURLRequired           = "repo_url_required"
	errorCodeActiveDeploymentExists    = "active_deployment_exists"
	errorCodeCreateJobFailed           = "create_deployment_job_failed"
	errorCodeListProjectsFailed        = "list_projects_failed"
	errorCodeListDeploymentsFailed     = "list_deployments_failed"
	errorCodeListJobsFailed            = "list_jobs_failed"
	errorCodeDeploymentNotFound        = "deployment_not_found"
	errorCodeFetchDeploymentFailed     = "fetch_deployment_failed"
	errorCodeFetchProjectFailed        = "fetch_project_failed"
	errorCodeFetchProjectRuntimeFailed = "fetch_project_runtime_failed"
	errorCodeProjectRuntimeNotFound    = "project_runtime_not_found"
	errorCodeFetchRuntimeLogsFailed    = "fetch_runtime_logs_failed"
	errorCodeJobIDRequired             = "job_id_required"
	errorCodeJobNotFound               = "job_not_found"
	errorCodeFetchJobFailed            = "fetch_job_failed"
	errorCodeFetchJobLogsFailed        = "fetch_job_logs_failed"
	errorCodeProjectNotFound           = "project_not_found"
	errorCodeProjectCleanupFailed      = "project_cleanup_failed"
	errorCodeInvalidProjectEnv         = "invalid_project_environment"
	errorCodeProjectEnvNotFound        = "project_environment_not_found"
	errorCodeProjectEnvFailed          = "project_environment_failed"
	errorCodeInvalidPocketBase         = "invalid_pocketbase"
	errorCodePocketBaseNotFound        = "pocketbase_not_found"
	errorCodePocketBaseFailed          = "pocketbase_failed"
)

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
