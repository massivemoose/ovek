package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

const (
	jobStatusRunning   = "running"
	jobStatusSucceeded = "succeeded"
	jobStatusFailed    = "failed"

	defaultJobQueueSize = 64
)

type deploymentResult struct {
	LogPath  string
	ImageRef string
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
	if err := markJobSucceeded(manager.db, jobID, finishedAt, result); err != nil {
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

func markJobSucceeded(db *sql.DB, jobID string, finishedAt string, result deploymentResult) error {
	updateResult, err := db.Exec(
		`UPDATE jobs
		 SET status = ?, finished_at = ?, error_message = NULL, log_path = ?, image_ref = ?
		 WHERE id = ? AND status = ?`,
		jobStatusSucceeded,
		finishedAt,
		nullableString(result.LogPath),
		nullableString(result.ImageRef),
		jobID,
		jobStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("mark job succeeded: %w", err)
	}

	return requireUpdatedRow(updateResult, "mark succeeded job")
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
