package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	jobStatusRunning           = "running"
	jobStatusSucceeded         = "succeeded"
	jobStatusFailed            = "failed"
	deploymentStatusSucceeded  = "succeeded"
	deploymentStatusSuperseded = "superseded"

	defaultJobQueueSize        = 64
	interruptedJobErrorMessage = "job interrupted by brain restart"
)

type deploymentResult struct {
	LogPath                 string
	ImageRef                string
	AppContainerName        string
	NetworkName             string
	PocketBaseContainerName string
	SupersededDeploymentID  string
	LogScrubber             secretScrubber
}

type deploymentProcessor interface {
	Process(ctx context.Context, job job) (deploymentResult, error)
}

type jobManager struct {
	db              *sql.DB
	processor       deploymentProcessor
	artifactCleaner registryArtifactCleaner
	ingress         projectIngressManager
	queue           chan string
}

func newJobManager(db *sql.DB, processor deploymentProcessor, artifactCleaners ...registryArtifactCleaner) *jobManager {
	var artifactCleaner registryArtifactCleaner
	if len(artifactCleaners) > 0 {
		artifactCleaner = artifactCleaners[0]
	}

	return &jobManager{
		db:              db,
		processor:       processor,
		artifactCleaner: artifactCleaner,
		queue:           make(chan string, defaultJobQueueSize),
	}
}

func (manager *jobManager) Start(ctx context.Context) error {
	if err := recoverInterruptedJobs(manager.db); err != nil {
		return err
	}

	jobIDs, err := listQueuedJobIDs(manager.db)
	if err != nil {
		return err
	}

	go manager.run(ctx)

	for _, jobID := range jobIDs {
		manager.Enqueue(jobID)
	}

	return nil
}

func (manager *jobManager) Enqueue(jobID string) {
	manager.queue <- jobID
}

func (manager *jobManager) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case jobID := <-manager.queue:
			manager.processJob(ctx, jobID)
		}
	}
}

func (manager *jobManager) processJob(ctx context.Context, jobID string) {
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := claimQueuedJob(manager.db, jobID, startedAt)
	if err != nil {
		log.Printf("failed to claim job %s: %v", jobID, err)
		return
	}
	if !claimed {
		return
	}

	job, err := getJob(manager.db, jobID)
	if err != nil {
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if updateErr := markJobFailed(manager.db, jobID, finishedAt, "failed to load claimed job", deploymentResult{}); updateErr != nil {
			log.Printf("failed to mark job %s as failed after load error: %v", jobID, updateErr)
		}
		return
	}

	result, err := manager.processor.Process(ctx, job)
	if err != nil {
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if updateErr := markJobFailed(manager.db, jobID, finishedAt, err.Error(), result); updateErr != nil {
			log.Printf("failed to mark job %s as failed: %v", jobID, updateErr)
		}
		return
	}

	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := markJobSucceeded(manager.db, job, finishedAt, result); err != nil {
		log.Printf("failed to mark job %s as succeeded: %v", jobID, err)
		promotionErr := "promotion state update failed: " + err.Error()
		appendJobLogError(result.LogPath, promotionErr, result.LogScrubber)
		if updateErr := markJobFailed(manager.db, jobID, finishedAt, promotionErr, result); updateErr != nil {
			log.Printf("failed to mark job %s as failed after promotion update error: %v", jobID, updateErr)
		}
		return
	}
	appendJobLogLine(result.LogPath, "lifecycle: deployment promoted", result.LogScrubber)
	if manager.ingress != nil {
		if err := manager.ingress.SyncProject(ctx, job.ProjectName); err != nil {
			log.Printf("warning: failed to sync ingress for project %q after job %s: %v", job.ProjectName, jobID, err)
		}
	}

	manager.cleanupSupersededDeploymentImage(ctx, job.ProjectName, result.SupersededDeploymentID)
}

func listQueuedJobIDs(db *sql.DB) ([]string, error) {
	return listJobIDsByStatus(db, jobStatusQueued)
}

func listRunningJobIDs(db *sql.DB) ([]string, error) {
	return listJobIDsByStatus(db, jobStatusRunning)
}

func listJobIDsByStatus(db *sql.DB, status string) ([]string, error) {
	rows, err := db.Query(
		`SELECT id
		 FROM jobs
		 WHERE status = ?
		 ORDER BY created_at ASC`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("list %s jobs: %w", status, err)
	}
	defer rows.Close()

	var jobIDs []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, fmt.Errorf("scan %s job ID: %w", status, err)
		}
		jobIDs = append(jobIDs, jobID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s jobs: %w", status, err)
	}

	return jobIDs, nil
}

func recoverInterruptedJobs(db *sql.DB) error {
	jobIDs, err := listRunningJobIDs(db)
	if err != nil {
		return err
	}

	for _, jobID := range jobIDs {
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := markJobFailed(db, jobID, finishedAt, interruptedJobErrorMessage, deploymentResult{}); err != nil {
			return fmt.Errorf("recover interrupted job %q: %w", jobID, err)
		}
		log.Printf("recovered interrupted job %q as failed after Brain restart", jobID)
	}

	return nil
}

func claimQueuedJob(db *sql.DB, jobID string, startedAt string) (bool, error) {
	result, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, phase = ?, started_at = ?, error_message = NULL
		 WHERE id = ? AND status = ?`,
		jobStatusRunning,
		jobPhaseStarting,
		startedAt,
		jobID,
		jobStatusQueued,
	)
	if err != nil {
		return false, fmt.Errorf("claim queued job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read claimed job rows affected: %w", err)
	}

	return rowsAffected == 1, nil
}

func markJobFailed(db *sql.DB, jobID string, finishedAt string, errorMessage string, result deploymentResult) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin failed job transaction: %w", err)
	}

	errorMessage = normalizeJobFailureMessage(errorMessage)

	projectName, err := getJobProjectName(tx, jobID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}

	updateResult, err := tx.Exec(
		`UPDATE jobs
		 SET status = ?, phase = NULL, finished_at = ?, error_message = ?, log_path = ?, image_ref = ?
		 WHERE id = ? AND status = ?`,
		jobStatusFailed,
		finishedAt,
		errorMessage,
		nullableString(result.LogPath),
		nullableString(result.ImageRef),
		jobID,
		jobStatusRunning,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark job failed: %w", err)
	}
	if err := requireUpdatedRow(updateResult, "mark failed job"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := syncProjectStatus(tx, projectName); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed job transaction: %w", err)
	}

	return nil
}

func normalizeJobFailureMessage(errorMessage string) string {
	errorMessage = strings.TrimSpace(errorMessage)
	if errorMessage == "" {
		return ""
	}

	switch {
	case errorMessage == interruptedJobErrorMessage:
		return errorMessage
	case strings.HasPrefix(errorMessage, "source fetch failed:"),
		strings.HasPrefix(errorMessage, "build planning failed:"),
		strings.HasPrefix(errorMessage, "image build failed:"),
		strings.HasPrefix(errorMessage, "PocketBase provisioning failed:"),
		strings.HasPrefix(errorMessage, "app container provisioning failed:"),
		strings.HasPrefix(errorMessage, "app readiness failed:"),
		strings.HasPrefix(errorMessage, "promotion state load failed:"),
		strings.HasPrefix(errorMessage, "promotion state update failed:"),
		strings.HasPrefix(errorMessage, "promotion cleanup failed:"),
		strings.HasPrefix(errorMessage, "job state load failed:"):
		return errorMessage
	case strings.HasPrefix(errorMessage, "git clone:"):
		return "source fetch failed: " + trimFailurePrefix(errorMessage, "git clone:")
	case strings.HasPrefix(errorMessage, "railpack prepare:"):
		return "build planning failed: " + trimFailurePrefix(errorMessage, "railpack prepare:")
	case strings.HasPrefix(errorMessage, "buildctl build:"):
		return "image build failed: " + trimFailurePrefix(errorMessage, "buildctl build:")
	case strings.HasPrefix(errorMessage, "ensure PocketBase:"):
		return "PocketBase provisioning failed: " + trimFailurePrefix(errorMessage, "ensure PocketBase:")
	case strings.HasPrefix(errorMessage, "ensure app container:"):
		return "app container provisioning failed: " + trimFailurePrefix(errorMessage, "ensure app container:")
	case strings.HasPrefix(errorMessage, "wait for app readiness:"):
		return "app readiness failed: " + trimFailurePrefix(errorMessage, "wait for app readiness:")
	case strings.HasPrefix(errorMessage, "load current deployment:"):
		return "promotion state load failed: " + trimFailurePrefix(errorMessage, "load current deployment:")
	case strings.HasPrefix(errorMessage, "promotion state update failed:"):
		return errorMessage
	case strings.HasPrefix(errorMessage, "remove superseded app container"):
		return "promotion cleanup failed: " + errorMessage
	case errorMessage == "failed to load claimed job":
		return "job state load failed"
	default:
		return errorMessage
	}
}

func trimFailurePrefix(errorMessage string, prefix string) string {
	return strings.TrimSpace(strings.TrimPrefix(errorMessage, prefix))
}

func markJobSucceeded(db *sql.DB, currentJob job, finishedAt string, result deploymentResult) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin succeeded job transaction: %w", err)
	}

	updateResult, err := tx.Exec(
		`UPDATE jobs
		 SET status = ?, phase = NULL, finished_at = ?, error_message = NULL, log_path = ?, image_ref = ?
		 WHERE id = ? AND status = ?`,
		jobStatusSucceeded,
		finishedAt,
		nullableString(result.LogPath),
		nullableString(result.ImageRef),
		currentJob.ID,
		jobStatusRunning,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark job succeeded: %w", err)
	}
	if err := requireUpdatedRow(updateResult, "mark succeeded job"); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := markSupersededDeployment(tx, currentJob.ProjectName, result.SupersededDeploymentID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := insertSucceededDeployment(tx, currentJob, result); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := setProjectCurrentDeployment(tx, currentJob.ProjectName, currentJob.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit succeeded job transaction: %w", err)
	}

	return nil
}

func insertSucceededDeployment(tx *sql.Tx, currentJob job, result deploymentResult) error {
	if err := requireDeploymentMetadata(result); err != nil {
		return err
	}

	insertResult, err := tx.Exec(
		`INSERT INTO deployments(
			id,
			project_name,
			image_ref,
			source_type,
			source_ref,
			app_container_name,
			network_name,
			pb_container_name,
			status,
			created_at,
			config_revision_id
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		currentJob.ID,
		currentJob.ProjectName,
		result.ImageRef,
		currentJob.SourceType,
		currentJob.SourceRef,
		result.AppContainerName,
		result.NetworkName,
		result.PocketBaseContainerName,
		deploymentStatusSucceeded,
		currentJob.CreatedAt,
		nullableString(currentJob.ConfigRevisionID),
	)
	if err != nil {
		return fmt.Errorf("insert deployment %q: %w", currentJob.ID, err)
	}

	return requireUpdatedRow(insertResult, "insert deployment")
}

func markSupersededDeployment(tx *sql.Tx, projectName string, deploymentID string) error {
	if deploymentID == "" {
		return nil
	}

	updateResult, err := tx.Exec(
		`UPDATE deployments
		 SET status = ?
		 WHERE id = ? AND project_name = ? AND status = ?`,
		deploymentStatusSuperseded,
		deploymentID,
		projectName,
		deploymentStatusSucceeded,
	)
	if err != nil {
		return fmt.Errorf("mark deployment %q superseded: %w", deploymentID, err)
	}

	return requireUpdatedRow(updateResult, "mark superseded deployment")
}

func setProjectCurrentDeployment(tx *sql.Tx, projectName string, deploymentID string) error {
	updateResult, err := tx.Exec(
		`UPDATE projects
		 SET current_deployment_id = ?
		 WHERE name = ?`,
		deploymentID,
		projectName,
	)
	if err != nil {
		return fmt.Errorf("set current deployment for project %q: %w", projectName, err)
	}
	if err := requireUpdatedRow(updateResult, "set current deployment"); err != nil {
		return err
	}

	return syncProjectStatus(tx, projectName)
}

func getJobProjectName(store projectStatusStore, jobID string) (string, error) {
	var projectName string
	if err := store.QueryRow(
		`SELECT project_name
		 FROM jobs
		 WHERE id = ?`,
		jobID,
	).Scan(&projectName); err != nil {
		return "", fmt.Errorf("get project name for job %q: %w", jobID, err)
	}

	return projectName, nil
}

func requireDeploymentMetadata(result deploymentResult) error {
	if result.ImageRef == "" {
		return fmt.Errorf("deployment result is missing image ref")
	}
	if result.AppContainerName == "" {
		return fmt.Errorf("deployment result is missing app container name")
	}
	if result.NetworkName == "" {
		return fmt.Errorf("deployment result is missing network name")
	}
	if result.PocketBaseContainerName == "" {
		return fmt.Errorf("deployment result is missing PocketBase container name")
	}

	return nil
}

func updateJobPhase(db *sql.DB, jobID string, phase string) error {
	_, err := db.Exec(
		`UPDATE jobs
		 SET phase = ?
		 WHERE id = ? AND status = ?`,
		strings.TrimSpace(phase),
		jobID,
		jobStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("update job phase: %w", err)
	}

	return nil
}

func getProjectCurrentDeployment(db *sql.DB, projectName string) (deploymentRecord, bool, error) {
	var deployment deploymentRecord
	var sourceType sql.NullString
	var sourceRef sql.NullString
	var configRevisionID sql.NullString
	err := db.QueryRow(
		`SELECT d.id, d.project_name, d.image_ref, d.source_type, d.source_ref, d.app_container_name, d.network_name, d.pb_container_name, d.status, d.created_at, d.config_revision_id
		 FROM projects p
		 JOIN deployments d ON d.id = p.current_deployment_id
		 WHERE p.name = ?`,
		projectName,
	).Scan(
		&deployment.ID,
		&deployment.ProjectName,
		&deployment.ImageRef,
		&sourceType,
		&sourceRef,
		&deployment.AppContainerName,
		&deployment.NetworkName,
		&deployment.PocketBaseContainerName,
		&deployment.Status,
		&deployment.CreatedAt,
		&configRevisionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return deploymentRecord{}, false, nil
	}
	if err != nil {
		return deploymentRecord{}, false, fmt.Errorf("get current deployment for project %q: %w", projectName, err)
	}
	if sourceType.Valid {
		deployment.SourceType = sourceType.String
	}
	if sourceRef.Valid {
		deployment.SourceRef = sourceRef.String
	}
	if configRevisionID.Valid {
		deployment.ConfigRevisionID = configRevisionID.String
	}

	return deployment, true, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func requireUpdatedRow(result sql.Result, operation string) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("%s: expected 1 updated row, got %d", operation, rowsAffected)
	}

	return nil
}

func (manager *jobManager) cleanupSupersededDeploymentImage(ctx context.Context, projectName string, deploymentID string) {
	if manager.artifactCleaner == nil || strings.TrimSpace(deploymentID) == "" {
		return
	}

	imageRef, found, err := getDeploymentImageRef(manager.db, projectName, deploymentID)
	if err != nil {
		log.Printf("warning: failed to load superseded deployment %q image for project %q cleanup: %v", deploymentID, projectName, err)
		return
	}
	if !found {
		log.Printf("warning: superseded deployment %q image for project %q was missing during cleanup", deploymentID, projectName)
		return
	}
	if err := manager.artifactCleaner.CleanupImage(ctx, imageRef); err != nil {
		log.Printf("warning: failed to clean up superseded deployment %q image %q: %v", deploymentID, imageRef, err)
	}
}

func getDeploymentImageRef(db *sql.DB, projectName string, deploymentID string) (string, bool, error) {
	var imageRef string
	err := db.QueryRow(
		`SELECT image_ref
		 FROM deployments
		 WHERE id = ? AND project_name = ? AND (source_type IS NULL OR source_type = ?)`,
		deploymentID,
		projectName,
		jobSourceTypeRepo,
	).Scan(&imageRef)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get deployment %q image ref for project %q: %w", deploymentID, projectName, err)
	}

	return imageRef, true, nil
}
