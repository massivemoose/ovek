package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	jobStatusRunning           = "running"
	jobStatusSucceeded         = "succeeded"
	jobStatusFailed            = "failed"
	deploymentStatusSucceeded  = "succeeded"
	deploymentStatusSuperseded = "superseded"

	defaultJobQueueSize = 64
)

type deploymentResult struct {
	LogPath                 string
	ImageRef                string
	AppContainerName        string
	NetworkName             string
	PocketBaseContainerName string
	SupersededDeploymentID  string
}

type deploymentRecord struct {
	ID                      string
	ProjectName             string
	ImageRef                string
	AppContainerName        string
	NetworkName             string
	PocketBaseContainerName string
	Status                  string
	CreatedAt               string
}

type deploymentProcessor interface {
	Process(ctx context.Context, job job) (deploymentResult, error)
}

type jobManager struct {
	db        *sql.DB
	processor deploymentProcessor
	queue     chan string
}

func newJobManager(db *sql.DB, processor deploymentProcessor) *jobManager {
	return &jobManager{
		db:        db,
		processor: processor,
		queue:     make(chan string, defaultJobQueueSize),
	}
}

func (manager *jobManager) Start(ctx context.Context) error {
	go manager.run(ctx)

	jobIDs, err := listQueuedJobIDs(manager.db)
	if err != nil {
		return err
	}

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
	}
}

func listQueuedJobIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(
		`SELECT id
		 FROM jobs
		 WHERE status = ?
		 ORDER BY created_at ASC`,
		jobStatusQueued,
	)
	if err != nil {
		return nil, fmt.Errorf("list queued jobs: %w", err)
	}
	defer rows.Close()

	var jobIDs []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, fmt.Errorf("scan queued job ID: %w", err)
		}
		jobIDs = append(jobIDs, jobID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate queued jobs: %w", err)
	}

	return jobIDs, nil
}

func claimQueuedJob(db *sql.DB, jobID string, startedAt string) (bool, error) {
	result, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, started_at = ?, error_message = NULL
		 WHERE id = ? AND status = ?`,
		jobStatusRunning,
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
	updateResult, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, finished_at = ?, error_message = ?, log_path = ?, image_ref = ?
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
		return fmt.Errorf("mark job failed: %w", err)
	}

	return requireUpdatedRow(updateResult, "mark failed job")
}

func markJobSucceeded(db *sql.DB, currentJob job, finishedAt string, result deploymentResult) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin succeeded job transaction: %w", err)
	}

	updateResult, err := tx.Exec(
		`UPDATE jobs
		 SET status = ?, finished_at = ?, error_message = NULL, log_path = ?, image_ref = ?
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
			app_container_name,
			network_name,
			pb_container_name,
			status,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		currentJob.ID,
		currentJob.ProjectName,
		result.ImageRef,
		result.AppContainerName,
		result.NetworkName,
		result.PocketBaseContainerName,
		deploymentStatusSucceeded,
		currentJob.CreatedAt,
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

	return requireUpdatedRow(updateResult, "set current deployment")
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

func getProjectCurrentDeployment(db *sql.DB, projectName string) (deploymentRecord, bool, error) {
	var deployment deploymentRecord
	err := db.QueryRow(
		`SELECT d.id, d.project_name, d.image_ref, d.app_container_name, d.network_name, d.pb_container_name, d.status, d.created_at
		 FROM projects p
		 JOIN deployments d ON d.id = p.current_deployment_id
		 WHERE p.name = ?`,
		projectName,
	).Scan(
		&deployment.ID,
		&deployment.ProjectName,
		&deployment.ImageRef,
		&deployment.AppContainerName,
		&deployment.NetworkName,
		&deployment.PocketBaseContainerName,
		&deployment.Status,
		&deployment.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return deploymentRecord{}, false, nil
	}
	if err != nil {
		return deploymentRecord{}, false, fmt.Errorf("get current deployment for project %q: %w", projectName, err)
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
