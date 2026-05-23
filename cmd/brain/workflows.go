package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const (
	defaultWorkflowQueueCap = 64
	workflowQueueSize       = 64

	workflowRunStatusQueued    = brainapi.WorkflowRunStatusQueued
	workflowRunStatusPreparing = brainapi.WorkflowRunStatusPreparing
	workflowRunStatusRunning   = brainapi.WorkflowRunStatusRunning
	workflowRunStatusSucceeded = brainapi.WorkflowRunStatusSucceeded
	workflowRunStatusFailed    = brainapi.WorkflowRunStatusFailed
	workflowRunStatusSkipped   = brainapi.WorkflowRunStatusSkipped
	workflowRunStatusCanceled  = brainapi.WorkflowRunStatusCanceled
	workflowRunStatusTimedOut  = brainapi.WorkflowRunStatusTimedOut

	workflowRunTriggerManual   = brainapi.WorkflowRunTriggerManual
	workflowRunTriggerSchedule = brainapi.WorkflowRunTriggerSchedule
	workflowRunTriggerAPI      = brainapi.WorkflowRunTriggerAPI
)

var (
	errWorkflowNotFound  = errors.New("workflow not found")
	errWorkflowQueueFull = errors.New("workflow queue full")
)

func isValidWorkflowName(name string) bool {
	return isValidProjectName(name)
}

func upsertWorkflowDefinition(ctx context.Context, db *sql.DB, workflow workflowDefinition) (workflowDefinition, error) {
	now := strings.TrimSpace(workflow.UpdatedAt)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339Nano)
	}
	createdAt := strings.TrimSpace(workflow.CreatedAt)
	if createdAt == "" {
		createdAt = now
	}
	if workflow.QueueCap <= 0 {
		workflow.QueueCap = defaultWorkflowQueueCap
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return workflowDefinition{}, fmt.Errorf("begin workflow upsert transaction: %w", err)
	}
	defer tx.Rollback()

	if err := ensureProjectTx(tx, workflow.ProjectName, now); err != nil {
		return workflowDefinition{}, err
	}

	existing, found, err := getWorkflowDefinitionTx(ctx, tx, workflow.ProjectName, workflow.Name)
	if err != nil {
		return workflowDefinition{}, err
	}
	if found {
		createdAt = existing.CreatedAt
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO workflows(project_name, name, source_image_ref, resolved_repo_digest, runtime_image_id, schedule, queue_cap, enabled, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_name, name) DO UPDATE SET
			source_image_ref = excluded.source_image_ref,
			resolved_repo_digest = excluded.resolved_repo_digest,
			runtime_image_id = excluded.runtime_image_id,
			schedule = excluded.schedule,
			queue_cap = excluded.queue_cap,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at`,
		workflow.ProjectName,
		workflow.Name,
		workflow.SourceImageRef,
		nullableString(workflow.ResolvedRepoDigest),
		nullableString(workflow.RuntimeImageID),
		nullableString(workflow.Schedule),
		workflow.QueueCap,
		boolInt(workflow.Enabled),
		createdAt,
		now,
	); err != nil {
		return workflowDefinition{}, fmt.Errorf("upsert workflow: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return workflowDefinition{}, fmt.Errorf("commit workflow upsert transaction: %w", err)
	}

	return decorateWorkflowDefinition(workflowDefinition{
		ProjectName:        workflow.ProjectName,
		Name:               workflow.Name,
		SourceImageRef:     workflow.SourceImageRef,
		ResolvedRepoDigest: workflow.ResolvedRepoDigest,
		RuntimeImageID:     workflow.RuntimeImageID,
		Schedule:           workflow.Schedule,
		QueueCap:           workflow.QueueCap,
		Enabled:            workflow.Enabled,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
	}), nil
}

func listWorkflowDefinitions(ctx context.Context, db *sql.DB, projectName string) ([]workflowDefinition, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_name, name, source_image_ref, resolved_repo_digest, runtime_image_id, schedule, queue_cap, enabled, created_at, updated_at
		 FROM workflows
		 WHERE project_name = ?
		 ORDER BY name ASC`,
		projectName,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	var workflows []workflowDefinition
	for rows.Next() {
		workflow, err := scanWorkflowDefinition(rows)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, decorateWorkflowDefinition(workflow))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflows: %w", err)
	}

	return workflows, nil
}

func getWorkflowDefinition(ctx context.Context, db *sql.DB, projectName string, workflowName string) (workflowDefinition, error) {
	workflow, found, err := getWorkflowDefinitionQuery(ctx, db, projectName, workflowName)
	if err != nil {
		return workflowDefinition{}, err
	}
	if !found {
		return workflowDefinition{}, errWorkflowNotFound
	}

	return decorateWorkflowDefinition(workflow), nil
}

func getWorkflowDefinitionTx(ctx context.Context, tx *sql.Tx, projectName string, workflowName string) (workflowDefinition, bool, error) {
	return getWorkflowDefinitionQuery(ctx, tx, projectName, workflowName)
}

func getWorkflowDefinitionQuery(ctx context.Context, queryer workflowQueryer, projectName string, workflowName string) (workflowDefinition, bool, error) {
	workflow, err := scanWorkflowDefinition(
		queryer.QueryRowContext(
			ctx,
			`SELECT project_name, name, source_image_ref, resolved_repo_digest, runtime_image_id, schedule, queue_cap, enabled, created_at, updated_at
			 FROM workflows
			 WHERE project_name = ? AND name = ?`,
			projectName,
			workflowName,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowDefinition{}, false, nil
	}
	if err != nil {
		return workflowDefinition{}, false, err
	}

	return workflow, true, nil
}

func deleteWorkflowDefinition(ctx context.Context, db *sql.DB, projectName string, workflowName string) error {
	result, err := db.ExecContext(
		ctx,
		`DELETE FROM workflows WHERE project_name = ? AND name = ?`,
		projectName,
		workflowName,
	)
	if err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	if err := requireUpdatedRow(result, "delete workflow"); err != nil {
		return errWorkflowNotFound
	}

	return nil
}

func createWorkflowRunRecord(ctx context.Context, db *sql.DB, run workflowRun) (workflowRun, error) {
	runID := strings.TrimSpace(run.ID)
	var err error
	if runID == "" {
		runID, err = newID()
		if err != nil {
			return workflowRun{}, err
		}
	}
	now := strings.TrimSpace(run.CreatedAt)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if run.Status == "" {
		run.Status = workflowRunStatusQueued
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO workflow_runs(id, project_name, workflow_name, trigger_type, status, config_revision_id, log_path, source_image_ref, resolved_repo_digest, runtime_image_id, exit_code, error_message, created_at, started_at, finished_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID,
		run.ProjectName,
		run.WorkflowName,
		run.TriggerType,
		run.Status,
		nullableString(run.ConfigRevisionID),
		nullableString(run.LogPath),
		run.SourceImageRef,
		nullableString(run.ResolvedRepoDigest),
		nullableString(run.RuntimeImageID),
		nullableInt(run.ExitCode),
		nullableString(run.ErrorMessage),
		now,
		nullableString(run.StartedAt),
		nullableString(run.FinishedAt),
	); err != nil {
		return workflowRun{}, fmt.Errorf("create workflow run: %w", err)
	}

	run.ID = runID
	run.CreatedAt = now
	return decorateWorkflowRun(run), nil
}

func createQueuedWorkflowRun(ctx context.Context, db *sql.DB, projectName string, workflowName string, triggerType string) (workflowRun, error) {
	if triggerType == "" {
		triggerType = workflowRunTriggerAPI
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return workflowRun{}, fmt.Errorf("begin workflow run transaction: %w", err)
	}
	defer tx.Rollback()

	workflow, found, err := getWorkflowDefinitionTx(ctx, tx, projectName, workflowName)
	if err != nil {
		return workflowRun{}, err
	}
	if !found {
		return workflowRun{}, errWorkflowNotFound
	}
	if workflow.QueueCap <= 0 {
		workflow.QueueCap = defaultWorkflowQueueCap
	}

	activeCount, err := countActiveWorkflowRuns(ctx, tx, projectName, workflowName)
	if err != nil {
		return workflowRun{}, err
	}
	if activeCount >= workflow.QueueCap {
		return workflowRun{}, errWorkflowQueueFull
	}

	configRevisionID, configRevisionFound, err := latestProjectConfigRevisionID(ctx, tx, projectName)
	if err != nil {
		return workflowRun{}, err
	}

	run, err := insertWorkflowRunTx(ctx, tx, workflowRun{
		ProjectName:        projectName,
		WorkflowName:       workflowName,
		TriggerType:        triggerType,
		Status:             workflowRunStatusQueued,
		ConfigRevisionID:   optionalRevisionID(configRevisionID, configRevisionFound),
		SourceImageRef:     workflow.SourceImageRef,
		ResolvedRepoDigest: workflow.ResolvedRepoDigest,
		RuntimeImageID:     workflow.RuntimeImageID,
	})
	if err != nil {
		return workflowRun{}, err
	}

	if err := tx.Commit(); err != nil {
		return workflowRun{}, fmt.Errorf("commit workflow run transaction: %w", err)
	}

	return decorateWorkflowRun(run), nil
}

func createScheduledWorkflowRun(ctx context.Context, db *sql.DB, projectName string, workflowName string) (workflowRun, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return workflowRun{}, fmt.Errorf("begin scheduled workflow run transaction: %w", err)
	}
	defer tx.Rollback()

	workflow, found, err := getWorkflowDefinitionTx(ctx, tx, projectName, workflowName)
	if err != nil {
		return workflowRun{}, err
	}
	if !found {
		return workflowRun{}, errWorkflowNotFound
	}

	activeCount, err := countActiveWorkflowRuns(ctx, tx, projectName, workflowName)
	if err != nil {
		return workflowRun{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if activeCount > 0 {
		run, err := insertWorkflowRunTx(ctx, tx, workflowRun{
			ProjectName:        projectName,
			WorkflowName:       workflowName,
			TriggerType:        workflowRunTriggerSchedule,
			Status:             workflowRunStatusSkipped,
			SourceImageRef:     workflow.SourceImageRef,
			ResolvedRepoDigest: workflow.ResolvedRepoDigest,
			RuntimeImageID:     workflow.RuntimeImageID,
			ErrorMessage:       "scheduled tick skipped because workflow already has queued or running work",
			CreatedAt:          now,
			FinishedAt:         now,
		})
		if err != nil {
			return workflowRun{}, err
		}
		if err := tx.Commit(); err != nil {
			return workflowRun{}, fmt.Errorf("commit skipped workflow run transaction: %w", err)
		}
		return decorateWorkflowRun(run), nil
	}

	configRevisionID, configRevisionFound, err := latestProjectConfigRevisionID(ctx, tx, projectName)
	if err != nil {
		return workflowRun{}, err
	}

	run, err := insertWorkflowRunTx(ctx, tx, workflowRun{
		ProjectName:        projectName,
		WorkflowName:       workflowName,
		TriggerType:        workflowRunTriggerSchedule,
		Status:             workflowRunStatusQueued,
		ConfigRevisionID:   optionalRevisionID(configRevisionID, configRevisionFound),
		SourceImageRef:     workflow.SourceImageRef,
		ResolvedRepoDigest: workflow.ResolvedRepoDigest,
		RuntimeImageID:     workflow.RuntimeImageID,
		CreatedAt:          now,
	})
	if err != nil {
		return workflowRun{}, err
	}

	if err := tx.Commit(); err != nil {
		return workflowRun{}, fmt.Errorf("commit scheduled workflow run transaction: %w", err)
	}

	return decorateWorkflowRun(run), nil
}

func insertWorkflowRunTx(ctx context.Context, tx *sql.Tx, run workflowRun) (workflowRun, error) {
	runID := strings.TrimSpace(run.ID)
	var err error
	if runID == "" {
		runID, err = newID()
		if err != nil {
			return workflowRun{}, err
		}
	}
	now := strings.TrimSpace(run.CreatedAt)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if run.Status == "" {
		run.Status = workflowRunStatusQueued
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO workflow_runs(id, project_name, workflow_name, trigger_type, status, config_revision_id, log_path, source_image_ref, resolved_repo_digest, runtime_image_id, exit_code, error_message, created_at, started_at, finished_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID,
		run.ProjectName,
		run.WorkflowName,
		run.TriggerType,
		run.Status,
		nullableString(run.ConfigRevisionID),
		nullableString(run.LogPath),
		run.SourceImageRef,
		nullableString(run.ResolvedRepoDigest),
		nullableString(run.RuntimeImageID),
		nullableInt(run.ExitCode),
		nullableString(run.ErrorMessage),
		now,
		nullableString(run.StartedAt),
		nullableString(run.FinishedAt),
	); err != nil {
		return workflowRun{}, fmt.Errorf("create workflow run: %w", err)
	}

	run.ID = runID
	run.CreatedAt = now
	return run, nil
}

func countActiveWorkflowRuns(ctx context.Context, queryer workflowQueryer, projectName string, workflowName string) (int, error) {
	var count int
	if err := queryer.QueryRowContext(
		ctx,
		`SELECT COUNT(1)
		 FROM workflow_runs
		 WHERE project_name = ?
		   AND workflow_name = ?
		   AND status IN (?, ?, ?)`,
		projectName,
		workflowName,
		workflowRunStatusQueued,
		workflowRunStatusPreparing,
		workflowRunStatusRunning,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active workflow runs: %w", err)
	}

	return count, nil
}

func listWorkflowRuns(ctx context.Context, db *sql.DB, projectName string, limit int) ([]workflowRun, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT id, project_name, workflow_name, trigger_type, status, config_revision_id, log_path, source_image_ref, resolved_repo_digest, runtime_image_id, exit_code, error_message, created_at, started_at, finished_at
		 FROM workflow_runs
		 WHERE project_name = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectName,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflow runs: %w", err)
	}
	defer rows.Close()

	var runs []workflowRun
	for rows.Next() {
		run, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, decorateWorkflowRun(run))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow runs: %w", err)
	}

	return runs, nil
}

func getWorkflowRun(ctx context.Context, db *sql.DB, projectName string, runID string) (workflowRun, error) {
	run, err := scanWorkflowRun(
		db.QueryRowContext(
			ctx,
			`SELECT id, project_name, workflow_name, trigger_type, status, config_revision_id, log_path, source_image_ref, resolved_repo_digest, runtime_image_id, exit_code, error_message, created_at, started_at, finished_at
			 FROM workflow_runs
			 WHERE project_name = ? AND id = ?`,
			projectName,
			runID,
		),
	)
	if err != nil {
		return workflowRun{}, err
	}

	return decorateWorkflowRun(run), nil
}

func getWorkflowRunByID(ctx context.Context, db *sql.DB, runID string) (workflowRun, error) {
	run, err := scanWorkflowRun(
		db.QueryRowContext(
			ctx,
			`SELECT id, project_name, workflow_name, trigger_type, status, config_revision_id, log_path, source_image_ref, resolved_repo_digest, runtime_image_id, exit_code, error_message, created_at, started_at, finished_at
			 FROM workflow_runs
			 WHERE id = ?`,
			runID,
		),
	)
	if err != nil {
		return workflowRun{}, err
	}

	return decorateWorkflowRun(run), nil
}

type workflowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type workflowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflowDefinition(scanner workflowScanner) (workflowDefinition, error) {
	var workflow workflowDefinition
	var repoDigest sql.NullString
	var runtimeImageID sql.NullString
	var schedule sql.NullString
	var enabled int

	if err := scanner.Scan(
		&workflow.ProjectName,
		&workflow.Name,
		&workflow.SourceImageRef,
		&repoDigest,
		&runtimeImageID,
		&schedule,
		&workflow.QueueCap,
		&enabled,
		&workflow.CreatedAt,
		&workflow.UpdatedAt,
	); err != nil {
		return workflowDefinition{}, err
	}
	if repoDigest.Valid {
		workflow.ResolvedRepoDigest = repoDigest.String
	}
	if runtimeImageID.Valid {
		workflow.RuntimeImageID = runtimeImageID.String
	}
	if schedule.Valid {
		workflow.Schedule = schedule.String
	}
	workflow.Enabled = enabled != 0

	return workflow, nil
}

func scanWorkflowRun(scanner workflowScanner) (workflowRun, error) {
	var run workflowRun
	var configRevisionID sql.NullString
	var logPath sql.NullString
	var repoDigest sql.NullString
	var runtimeImageID sql.NullString
	var exitCode sql.NullInt64
	var errorMessage sql.NullString
	var startedAt sql.NullString
	var finishedAt sql.NullString

	if err := scanner.Scan(
		&run.ID,
		&run.ProjectName,
		&run.WorkflowName,
		&run.TriggerType,
		&run.Status,
		&configRevisionID,
		&logPath,
		&run.SourceImageRef,
		&repoDigest,
		&runtimeImageID,
		&exitCode,
		&errorMessage,
		&run.CreatedAt,
		&startedAt,
		&finishedAt,
	); err != nil {
		return workflowRun{}, err
	}
	if configRevisionID.Valid {
		run.ConfigRevisionID = configRevisionID.String
	}
	if logPath.Valid {
		run.LogPath = logPath.String
	}
	if repoDigest.Valid {
		run.ResolvedRepoDigest = repoDigest.String
	}
	if runtimeImageID.Valid {
		run.RuntimeImageID = runtimeImageID.String
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		run.ExitCode = &code
	}
	if errorMessage.Valid {
		run.ErrorMessage = errorMessage.String
	}
	if startedAt.Valid {
		run.StartedAt = startedAt.String
	}
	if finishedAt.Valid {
		run.FinishedAt = finishedAt.String
	}

	return run, nil
}

func decorateWorkflowDefinition(workflow workflowDefinition) workflowDefinition {
	workflow.Links = workflowLinks{
		Self: brainapi.ProjectWorkflowPath(workflow.ProjectName, workflow.Name),
		Runs: brainapi.ProjectWorkflowDefinitionRunsPath(
			workflow.ProjectName,
			workflow.Name,
		),
	}
	return workflow
}

func decorateWorkflowRun(run workflowRun) workflowRun {
	run.Links = workflowRunLinks{
		Self:       brainapi.ProjectWorkflowRunPath(run.ProjectName, run.ID),
		Logs:       brainapi.ProjectWorkflowRunLogsPath(run.ProjectName, run.ID),
		LogsStream: brainapi.ProjectWorkflowRunLogsStreamPath(run.ProjectName, run.ID),
	}
	return run
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
