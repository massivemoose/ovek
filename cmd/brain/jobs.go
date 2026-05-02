package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const (
	jobStatusQueued   = "queued"
	jobTypeDeployment = "deployment"
	jobSourceTypeRepo = brainapi.JobSourceTypeRepo

	jobPhaseQueued              = "queued"
	jobPhaseStarting            = "starting"
	jobPhaseBuildingImage       = "building image"
	jobPhaseProvisioningPB      = "provisioning PocketBase"
	jobPhaseStartingApp         = "starting app"
	jobPhaseWaitingForReadiness = "waiting for readiness"
	jobPhasePromoting           = "promoting"
)

var projectNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type deploymentEnqueuer interface {
	Enqueue(jobID string)
}

type activeDeploymentJobError struct {
	job job
}

func (err activeDeploymentJobError) Error() string {
	return fmt.Sprintf("project %q already has active deployment job %q", err.job.ProjectName, err.job.ID)
}

func handleListProjectJobs(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		limit, err := parseListLimit(r, defaultListLimit)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
			return
		}

		exists, err := projectExists(db, projectName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeListJobsFailed, "failed to list jobs")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}

		jobs, err := listProjectJobs(db, projectName, limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeListJobsFailed, "failed to list jobs")
			return
		}

		writeJSON(w, http.StatusOK, jobs)
	}
}

func handleCreateDeployment(db *sql.DB, enqueuer deploymentEnqueuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		var request createDeploymentRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		createDeploymentJob(w, db, enqueuer, projectName, request.RepoURL)
	}
}

func handleGetJob(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimSpace(r.PathValue("jobID"))
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodeJobIDRequired, "job ID is required")
			return
		}

		job, err := getJob(db, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeJobNotFound, "job not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchJobFailed, "failed to fetch job")
			return
		}

		writeJSON(w, http.StatusOK, job)
	}
}

func createDeploymentJob(w http.ResponseWriter, db *sql.DB, enqueuer deploymentEnqueuer, projectName string, repoURL string) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeRepoURLRequired, "repoUrl is required")
		return
	}

	job, err := createQueuedDeploymentJob(db, projectName, repoURL)
	if err != nil {
		var activeErr activeDeploymentJobError
		if errors.As(err, &activeErr) {
			writeJSONError(w, http.StatusConflict, errorCodeActiveDeploymentExists, activeDeploymentJobMessage(activeErr.job))
			return
		}
		writeJSONError(w, http.StatusInternalServerError, errorCodeCreateJobFailed, "failed to create deployment job")
		return
	}

	if enqueuer != nil {
		enqueuer.Enqueue(job.ID)
	}

	w.Header().Set("Location", jobPath(job.ID))
	writeJSON(w, http.StatusAccepted, job)
}

func createQueuedJob(db *sql.DB, projectName string, repoURL string) (job, error) {
	return createQueuedJobWithActiveCheck(db, projectName, repoURL, false)
}

func createQueuedDeploymentJob(db *sql.DB, projectName string, repoURL string) (job, error) {
	return createQueuedJobWithActiveCheck(db, projectName, repoURL, true)
}

func createQueuedJobWithActiveCheck(db *sql.DB, projectName string, repoURL string, rejectActive bool) (job, error) {
	return createQueuedJobWithSource(db, projectName, jobSourceTypeRepo, repoURL, rejectActive)
}

func createQueuedJobWithSource(db *sql.DB, projectName string, sourceType string, sourceRef string, rejectActive bool) (job, error) {
	jobID, err := newID()
	if err != nil {
		return job{}, err
	}
	sourceType = strings.TrimSpace(sourceType)
	sourceRef = strings.TrimSpace(sourceRef)
	repoURL := ""
	if sourceType == jobSourceTypeRepo {
		repoURL = sourceRef
	}

	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return job{}, err
	}

	if _, err := tx.Exec(
		"INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)",
		projectName,
		projectStatusIdle,
		createdAt,
	); err != nil {
		_ = tx.Rollback()
		return job{}, err
	}

	if rejectActive {
		activeJob, found, err := findActiveDeploymentJob(context.Background(), tx, projectName)
		if err != nil {
			_ = tx.Rollback()
			return job{}, err
		}
		if found {
			_ = tx.Rollback()
			return job{}, activeDeploymentJobError{job: activeJob}
		}
	}

	configRevisionID, configRevisionFound, err := latestProjectConfigRevisionID(context.Background(), tx, projectName)
	if err != nil {
		_ = tx.Rollback()
		return job{}, err
	}

	if _, err := tx.Exec(
		"INSERT INTO jobs(id, project_name, repo_url, source_type, source_ref, status, phase, created_at, config_revision_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)",
		jobID,
		projectName,
		repoURL,
		sourceType,
		sourceRef,
		jobStatusQueued,
		jobPhaseQueued,
		createdAt,
		nullableString(configRevisionID),
	); err != nil {
		_ = tx.Rollback()
		return job{}, err
	}
	if err := syncProjectStatus(tx, projectName); err != nil {
		_ = tx.Rollback()
		return job{}, err
	}

	if err := tx.Commit(); err != nil {
		return job{}, err
	}

	return decorateJob(job{
		ID:               jobID,
		ProjectName:      projectName,
		RepoURL:          repoURL,
		SourceType:       sourceType,
		SourceRef:        sourceRef,
		Status:           jobStatusQueued,
		CreatedAt:        createdAt,
		ConfigRevisionID: optionalRevisionID(configRevisionID, configRevisionFound),
	}), nil
}

func findActiveDeploymentJob(ctx context.Context, tx *sql.Tx, projectName string) (job, bool, error) {
	activeJob, err := scanJob(
		tx.QueryRowContext(
			ctx,
			`SELECT id, project_name, repo_url, source_type, source_ref, status, phase, log_path, image_ref, error_message, created_at, started_at, finished_at, config_revision_id
			 FROM jobs
			 WHERE project_name = ?
			   AND status IN (?, ?)
			 ORDER BY created_at ASC
			 LIMIT 1`,
			projectName,
			jobStatusQueued,
			jobStatusRunning,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return job{}, false, nil
	}
	if err != nil {
		return job{}, false, err
	}

	return decorateJob(activeJob), true, nil
}

func activeDeploymentJobMessage(job job) string {
	return fmt.Sprintf("project %q already has an active deployment job %q with status %q; wait for it to finish before starting another deploy", job.ProjectName, job.ID, job.Status)
}

func listProjectJobs(db *sql.DB, projectName string, limit int) ([]job, error) {
	rows, err := db.Query(
		`SELECT id, project_name, repo_url, source_type, source_ref, status, phase, log_path, image_ref, error_message, created_at, started_at, finished_at, config_revision_id
		 FROM jobs
		 WHERE project_name = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectName,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, decorateJob(job))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return jobs, nil
}

func getJob(db *sql.DB, jobID string) (job, error) {
	job, err := scanJob(
		db.QueryRow(
			`SELECT id, project_name, repo_url, source_type, source_ref, status, phase, log_path, image_ref, error_message, created_at, started_at, finished_at, config_revision_id
			 FROM jobs
			 WHERE id = ?`,
			jobID,
		),
	)
	if err != nil {
		return job, err
	}

	return decorateJob(job), nil
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(scanner jobScanner) (job, error) {
	var job job
	var sourceType sql.NullString
	var sourceRef sql.NullString
	var phase sql.NullString
	var logPath sql.NullString
	var imageRef sql.NullString
	var errorMessage sql.NullString
	var startedAt sql.NullString
	var finishedAt sql.NullString
	var configRevisionID sql.NullString

	err := scanner.Scan(
		&job.ID,
		&job.ProjectName,
		&job.RepoURL,
		&sourceType,
		&sourceRef,
		&job.Status,
		&phase,
		&logPath,
		&imageRef,
		&errorMessage,
		&job.CreatedAt,
		&startedAt,
		&finishedAt,
		&configRevisionID,
	)
	if err != nil {
		return job, err
	}

	if sourceType.Valid {
		job.SourceType = sourceType.String
	}
	if sourceRef.Valid {
		job.SourceRef = sourceRef.String
	}
	if logPath.Valid {
		job.LogPath = logPath.String
	}
	if phase.Valid {
		job.Phase = phase.String
	}
	if imageRef.Valid {
		job.ImageRef = imageRef.String
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.String
	}
	if finishedAt.Valid {
		job.FinishedAt = finishedAt.String
	}
	if configRevisionID.Valid {
		job.ConfigRevisionID = configRevisionID.String
	}

	return job, nil
}

func optionalRevisionID(revisionID string, found bool) string {
	if !found {
		return ""
	}
	return revisionID
}

func isValidProjectName(name string) bool {
	return projectNamePattern.MatchString(name)
}

func newID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(buffer[:]), nil
}

func decorateJob(job job) job {
	job.Type = brainapi.JobTypeDeployment
	if job.SourceType == "" {
		job.SourceType = jobSourceTypeRepo
	}
	if job.SourceRef == "" && job.RepoURL != "" {
		job.SourceRef = job.RepoURL
	}
	job.Links = jobLinks{
		Self:       brainapi.JobPath(job.ID),
		Logs:       brainapi.JobLogsPath(job.ID),
		LogsStream: brainapi.JobLogsStreamPath(job.ID),
	}

	return job
}

func jobPath(jobID string) string {
	return brainapi.JobPath(jobID)
}

func jobLogsPath(jobID string) string {
	return brainapi.JobLogsPath(jobID)
}

func jobLogsStreamPath(jobID string) string {
	return brainapi.JobLogsStreamPath(jobID)
}
