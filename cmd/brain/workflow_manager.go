package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

const interruptedWorkflowRunErrorMessage = "workflow run interrupted by brain restart"

var errWorkflowRunTimedOut = errors.New("workflow run timed out")

type workflowRunResult struct {
	LogPath  string
	ExitCode *int
}

type workflowProcessor interface {
	Process(ctx context.Context, run workflowRun) (workflowRunResult, error)
}

type workflowManager struct {
	db        *sql.DB
	processor workflowProcessor
	queue     chan string
}

func newWorkflowManager(db *sql.DB, processor workflowProcessor) *workflowManager {
	return &workflowManager{
		db:        db,
		processor: processor,
		queue:     make(chan string, workflowQueueSize),
	}
}

func (manager *workflowManager) Start(ctx context.Context) error {
	if err := recoverInterruptedWorkflowRuns(manager.db); err != nil {
		return err
	}

	runIDs, err := listQueuedWorkflowRunIDs(manager.db)
	if err != nil {
		return err
	}

	go manager.run(ctx)

	for _, runID := range runIDs {
		manager.Enqueue(runID)
	}

	return nil
}

func (manager *workflowManager) Enqueue(runID string) {
	manager.queue <- runID
}

func (manager *workflowManager) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case runID := <-manager.queue:
			manager.processRun(ctx, runID)
		}
	}
}

func (manager *workflowManager) processRun(ctx context.Context, runID string) {
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := claimQueuedWorkflowRun(manager.db, runID, startedAt)
	if err != nil {
		log.Printf("failed to claim workflow run %s: %v", runID, err)
		return
	}
	if !claimed {
		return
	}

	run, err := getWorkflowRunByID(ctx, manager.db, runID)
	if err != nil {
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if updateErr := markWorkflowRunFailed(manager.db, runID, finishedAt, "failed to load claimed workflow run", workflowRunResult{}); updateErr != nil {
			log.Printf("failed to mark workflow run %s failed after load error: %v", runID, updateErr)
		}
		return
	}

	result, err := manager.processor.Process(ctx, run)
	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		if errors.Is(err, errWorkflowRunTimedOut) {
			if updateErr := markWorkflowRunTimedOut(manager.db, runID, finishedAt, err.Error(), result); updateErr != nil {
				log.Printf("failed to mark workflow run %s timed out: %v", runID, updateErr)
			}
			return
		}
		if updateErr := markWorkflowRunFailed(manager.db, runID, finishedAt, err.Error(), result); updateErr != nil {
			log.Printf("failed to mark workflow run %s failed: %v", runID, updateErr)
		}
		return
	}

	if err := markWorkflowRunSucceeded(manager.db, runID, finishedAt, result); err != nil {
		log.Printf("failed to mark workflow run %s succeeded: %v", runID, err)
	}
}

func listQueuedWorkflowRunIDs(db *sql.DB) ([]string, error) {
	return listWorkflowRunIDsByStatus(db, workflowRunStatusQueued)
}

func listInterruptedWorkflowRunIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(
		`SELECT id
		 FROM workflow_runs
		 WHERE status IN (?, ?)
		 ORDER BY created_at ASC`,
		workflowRunStatusPreparing,
		workflowRunStatusRunning,
	)
	if err != nil {
		return nil, fmt.Errorf("list interrupted workflow runs: %w", err)
	}
	defer rows.Close()

	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan interrupted workflow run ID: %w", err)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted workflow runs: %w", err)
	}

	return runIDs, nil
}

func listWorkflowRunIDsByStatus(db *sql.DB, status string) ([]string, error) {
	rows, err := db.Query(
		`SELECT id
		 FROM workflow_runs
		 WHERE status = ?
		 ORDER BY created_at ASC`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("list %s workflow runs: %w", status, err)
	}
	defer rows.Close()

	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan %s workflow run ID: %w", status, err)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s workflow runs: %w", status, err)
	}

	return runIDs, nil
}

func recoverInterruptedWorkflowRuns(db *sql.DB) error {
	runIDs, err := listInterruptedWorkflowRunIDs(db)
	if err != nil {
		return err
	}

	for _, runID := range runIDs {
		finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := markWorkflowRunFailed(db, runID, finishedAt, interruptedWorkflowRunErrorMessage, workflowRunResult{}); err != nil {
			return fmt.Errorf("recover interrupted workflow run %q: %w", runID, err)
		}
		log.Printf("recovered interrupted workflow run %q as failed after Brain restart", runID)
	}

	return nil
}

func claimQueuedWorkflowRun(db *sql.DB, runID string, startedAt string) (bool, error) {
	result, err := db.Exec(
		`UPDATE workflow_runs
		 SET status = ?, started_at = ?, error_message = NULL
		 WHERE id = ? AND status = ?`,
		workflowRunStatusRunning,
		startedAt,
		runID,
		workflowRunStatusQueued,
	)
	if err != nil {
		return false, fmt.Errorf("claim queued workflow run: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read claimed workflow run rows affected: %w", err)
	}

	return rowsAffected == 1, nil
}

func markWorkflowRunSucceeded(db *sql.DB, runID string, finishedAt string, result workflowRunResult) error {
	updateResult, err := db.Exec(
		`UPDATE workflow_runs
		 SET status = ?, finished_at = ?, error_message = NULL, log_path = ?, exit_code = ?
		 WHERE id = ? AND status = ?`,
		workflowRunStatusSucceeded,
		finishedAt,
		nullableString(result.LogPath),
		nullableInt(result.ExitCode),
		runID,
		workflowRunStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("mark workflow run succeeded: %w", err)
	}

	return requireUpdatedRow(updateResult, "mark succeeded workflow run")
}

func markWorkflowRunFailed(db *sql.DB, runID string, finishedAt string, errorMessage string, result workflowRunResult) error {
	return markWorkflowRunTerminal(db, runID, workflowRunStatusFailed, finishedAt, errorMessage, result)
}

func markWorkflowRunTimedOut(db *sql.DB, runID string, finishedAt string, errorMessage string, result workflowRunResult) error {
	return markWorkflowRunTerminal(db, runID, workflowRunStatusTimedOut, finishedAt, errorMessage, result)
}

func markWorkflowRunTerminal(db *sql.DB, runID string, status string, finishedAt string, errorMessage string, result workflowRunResult) error {
	updateResult, err := db.Exec(
		`UPDATE workflow_runs
		 SET status = ?, finished_at = ?, error_message = ?, log_path = ?, exit_code = ?
		 WHERE id = ? AND status IN (?, ?)`,
		status,
		finishedAt,
		errorMessage,
		nullableString(result.LogPath),
		nullableInt(result.ExitCode),
		runID,
		workflowRunStatusPreparing,
		workflowRunStatusRunning,
	)
	if err != nil {
		return fmt.Errorf("mark workflow run terminal: %w", err)
	}

	return requireUpdatedRow(updateResult, "mark terminal workflow run")
}
