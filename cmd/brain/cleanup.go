package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
)

var errProjectNotFound = errors.New("project not found")

const (
	cleanupRuntimeTeardownAttempts          = 5
	cleanupRuntimeTeardownInitialRetryDelay = 150 * time.Millisecond
	cleanupRuntimeTeardownMaxRetryDelay     = 600 * time.Millisecond
)

type projectCleanupService interface {
	Cleanup(ctx context.Context, projectName string) error
	RemoveRuntime(ctx context.Context, projectName string, options projectRuntimeRemovalOptions) error
}

type projectCleanupRuntime interface {
	ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error)
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
	RemoveProjectWorkflowContainers(ctx context.Context, projectName string) error
	RemoveProjectPocketBase(ctx context.Context, projectName string) error
	RemoveProjectNetwork(ctx context.Context, projectName string) error
}

type managedProjectCleaner struct {
	dataDir             string
	projectsHostDataDir string
	db                  *sql.DB
	runtime             projectCleanupRuntime
	artifactCleaner     registryArtifactCleaner
	ingress             projectIngressManager
}

type projectRuntimeRemovalOptions struct {
	RemoveDatabase     bool
	DeleteDatabaseData bool
}

func newManagedProjectCleaner(db *sql.DB, runtime projectCleanupRuntime, dataDir string, artifactCleaners ...registryArtifactCleaner) managedProjectCleaner {
	var artifactCleaner registryArtifactCleaner
	if len(artifactCleaners) > 0 {
		artifactCleaner = artifactCleaners[0]
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = defaultDataDir
	}

	return managedProjectCleaner{
		dataDir:             dataDir,
		projectsHostDataDir: defaultProjectsHostDataDir,
		db:                  db,
		runtime:             runtime,
		artifactCleaner:     artifactCleaner,
	}
}

func (cleaner managedProjectCleaner) Cleanup(ctx context.Context, projectName string) error {
	exists, err := projectExists(cleaner.db, projectName)
	if err != nil {
		return err
	}
	if !exists {
		return errProjectNotFound
	}

	imageRefs, err := listProjectDeploymentImageRefs(cleaner.db, projectName)
	if err != nil {
		log.Printf("warning: failed to list deployment images for project %q cleanup: %v", projectName, err)
	}
	logPaths, err := listProjectJobLogPaths(cleaner.db, projectName)
	if err != nil {
		log.Printf("warning: failed to list job logs for project %q cleanup: %v", projectName, err)
	}
	workflowLogPaths, err := listProjectWorkflowLogPaths(cleaner.db, projectName)
	if err != nil {
		log.Printf("warning: failed to list workflow logs for project %q cleanup: %v", projectName, err)
	}
	logPaths = append(logPaths, workflowLogPaths...)
	workflowPayloadPaths, err := listProjectWorkflowPayloadPaths(cleaner.db, cleaner.dataDir, projectName)
	if err != nil {
		log.Printf("warning: failed to list workflow payloads for project %q cleanup: %v", projectName, err)
	}
	logPaths = append(logPaths, workflowPayloadPaths...)

	apps, err := cleaner.runtime.ListProjectApps(ctx, projectName)
	if err != nil {
		return fmt.Errorf("list project apps: %w", err)
	}
	for _, app := range apps {
		if err := retryRuntimeTeardown(ctx, "remove project app "+app.AppContainerName, func(ctx context.Context) error {
			return cleaner.runtime.RemoveProjectApp(ctx, app.deploymentRecord())
		}); err != nil {
			return fmt.Errorf("remove project app %q: %w", app.AppContainerName, err)
		}
	}
	if err := retryRuntimeTeardown(ctx, "remove workflow containers for project "+projectName, func(ctx context.Context) error {
		return cleaner.runtime.RemoveProjectWorkflowContainers(ctx, projectName)
	}); err != nil {
		return fmt.Errorf("remove workflow containers: %w", err)
	}

	if err := retryRuntimeTeardown(ctx, "remove PocketBase for project "+projectName, func(ctx context.Context) error {
		return cleaner.runtime.RemoveProjectPocketBase(ctx, projectName)
	}); err != nil {
		return fmt.Errorf("remove PocketBase: %w", err)
	}
	if err := retryRuntimeTeardown(ctx, "remove network for project "+projectName, func(ctx context.Context) error {
		return cleaner.runtime.RemoveProjectNetwork(ctx, projectName)
	}); err != nil {
		return fmt.Errorf("remove project network: %w", err)
	}
	if err := clearProjectRuntimeState(cleaner.db, projectName); err != nil {
		return err
	}
	if err := clearProjectWorkflowState(cleaner.db, projectName); err != nil {
		return err
	}
	if cleaner.ingress != nil {
		if err := cleaner.ingress.RemoveProject(projectName); err != nil {
			log.Printf("warning: failed to remove ingress for project %q during cleanup: %v", projectName, err)
		}
	}
	cleaner.cleanupProjectImages(ctx, projectName, imageRefs)
	cleaner.cleanupProjectLogFiles(projectName, logPaths)

	return nil
}

func (cleaner managedProjectCleaner) RemoveRuntime(ctx context.Context, projectName string, options projectRuntimeRemovalOptions) error {
	if options.DeleteDatabaseData && !options.RemoveDatabase {
		return errors.New("delete database data requires database removal")
	}
	exists, err := projectExists(cleaner.db, projectName)
	if err != nil {
		return err
	}
	if !exists {
		return errProjectNotFound
	}

	apps, err := cleaner.runtime.ListProjectApps(ctx, projectName)
	if err != nil {
		return fmt.Errorf("list project apps: %w", err)
	}
	for _, app := range apps {
		if err := retryRuntimeTeardown(ctx, "remove project app "+app.AppContainerName, func(ctx context.Context) error {
			return cleaner.runtime.RemoveProjectApp(ctx, app.deploymentRecord())
		}); err != nil {
			return fmt.Errorf("remove project app %q: %w", app.AppContainerName, err)
		}
	}
	if cleaner.ingress != nil {
		if err := cleaner.ingress.RemoveProject(projectName); err != nil {
			return err
		}
	}
	if err := clearProjectRuntimeState(cleaner.db, projectName); err != nil {
		return err
	}

	if !options.RemoveDatabase {
		return nil
	}
	if err := retryRuntimeTeardown(ctx, "remove database for project "+projectName, func(ctx context.Context) error {
		return cleaner.runtime.RemoveProjectPocketBase(ctx, projectName)
	}); err != nil {
		return fmt.Errorf("remove database: %w", err)
	}
	if options.DeleteDatabaseData {
		if err := os.RemoveAll(pocketBaseDataDir(cleaner.projectsHostDataDir, projectName)); err != nil {
			return fmt.Errorf("delete database data: %w", err)
		}
	}
	if err := retryRuntimeTeardown(ctx, "remove network for project "+projectName, func(ctx context.Context) error {
		return cleaner.runtime.RemoveProjectNetwork(ctx, projectName)
	}); err != nil {
		return fmt.Errorf("remove project network: %w", err)
	}

	return nil
}

func retryRuntimeTeardown(ctx context.Context, description string, teardown func(context.Context) error) error {
	var lastErr error
	for attempt := 1; attempt <= cleanupRuntimeTeardownAttempts; attempt++ {
		err := teardown(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryableRuntimeTeardownError(err) || attempt == cleanupRuntimeTeardownAttempts {
			return err
		}

		delay := cleanupRuntimeTeardownRetryDelay(attempt)
		log.Printf("warning: transient runtime cleanup failure while trying to %s; retrying in %s (%d/%d): %v", description, delay, attempt, cleanupRuntimeTeardownAttempts, err)
		if err := sleepForRuntimeTeardownRetry(ctx, delay); err != nil {
			return fmt.Errorf("%w after retryable cleanup error: %v", err, lastErr)
		}
	}

	return lastErr
}

func isRetryableRuntimeTeardownError(err error) bool {
	return cerrdefs.IsConflict(err) ||
		cerrdefs.IsInternal(err) ||
		cerrdefs.IsUnknown(err) ||
		cerrdefs.IsUnavailable(err)
}

func cleanupRuntimeTeardownRetryDelay(attempt int) time.Duration {
	delay := cleanupRuntimeTeardownInitialRetryDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= cleanupRuntimeTeardownMaxRetryDelay {
			return cleanupRuntimeTeardownMaxRetryDelay
		}
	}

	return delay
}

var sleepForRuntimeTeardownRetry = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (cleaner managedProjectCleaner) cleanupProjectImages(ctx context.Context, projectName string, imageRefs []string) {
	if cleaner.artifactCleaner == nil {
		return
	}

	for _, imageRef := range dedupeStrings(imageRefs) {
		if err := cleaner.artifactCleaner.CleanupImage(ctx, imageRef); err != nil {
			log.Printf("warning: failed to clean up project %q image %q: %v", projectName, imageRef, err)
		}
	}
}

func (cleaner managedProjectCleaner) cleanupProjectLogFiles(projectName string, logPaths []string) {
	for _, logPath := range dedupeStrings(logPaths) {
		managedLogPath, ok := managedJobLogPath(cleaner.dataDir, logPath)
		if !ok {
			continue
		}
		if err := os.Remove(managedLogPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			log.Printf("warning: failed to clean up project %q log file %q: %v", projectName, managedLogPath, err)
		}
	}
}

func managedJobLogPath(dataDir string, logPath string) (string, bool) {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return "", false
	}

	candidatePath, err := filepath.Abs(logPath)
	if err != nil {
		return "", false
	}
	for _, logsDirName := range []string{jobLogsDirName, workflowLogsDirName, workflowPayloadsDirName} {
		baseDir, err := filepath.Abs(filepath.Join(dataDir, logsDirName))
		if err != nil {
			return "", false
		}
		relativePath, err := filepath.Rel(baseDir, candidatePath)
		if err != nil {
			return "", false
		}
		if relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return candidatePath, true
		}
	}

	return "", false
}

func handleDeleteProjectRuntime(cleaner projectCleanupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		if err := cleaner.Cleanup(r.Context(), projectName); errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeProjectCleanupFailed, "failed to clean up project")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteProjectAppRuntime(cleaner projectCleanupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		options, err := parseProjectRuntimeRemovalOptions(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, err.Error())
			return
		}

		if err := cleaner.RemoveRuntime(r.Context(), projectName, options); errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeProjectCleanupFailed, "failed to remove project runtime")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func parseProjectRuntimeRemovalOptions(r *http.Request) (projectRuntimeRemovalOptions, error) {
	query := r.URL.Query()
	options := projectRuntimeRemovalOptions{
		RemoveDatabase:     query.Get("removeDatabase") == "true",
		DeleteDatabaseData: query.Get("deleteDatabaseData") == "true",
	}
	if options.DeleteDatabaseData && !options.RemoveDatabase {
		return projectRuntimeRemovalOptions{}, errors.New("deleteDatabaseData requires removeDatabase")
	}
	return options, nil
}

func projectExists(db *sql.DB, projectName string) (bool, error) {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(1)
		 FROM projects
		 WHERE name = ?`,
		projectName,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("query project %q: %w", projectName, err)
	}

	return count == 1, nil
}

func clearProjectRuntimeState(db *sql.DB, projectName string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin cleanup transaction: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE deployments
		 SET status = ?
		 WHERE project_name = ? AND status = ?`,
		deploymentStatusSuperseded,
		projectName,
		deploymentStatusSucceeded,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("supersede project deployments for %q: %w", projectName, err)
	}

	if _, err := tx.Exec(
		`UPDATE projects
		 SET current_deployment_id = NULL
		 WHERE name = ?`,
		projectName,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear current deployment for project %q: %w", projectName, err)
	}
	if err := syncProjectStatus(tx, projectName); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cleanup transaction: %w", err)
	}

	return nil
}

func clearProjectWorkflowState(db *sql.DB, projectName string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin workflow cleanup transaction: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM workflow_trigger_tokens WHERE project_name = ?`, projectName); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete workflow trigger tokens for project %q: %w", projectName, err)
	}
	if _, err := tx.Exec(`DELETE FROM workflow_runs WHERE project_name = ?`, projectName); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete workflow runs for project %q: %w", projectName, err)
	}
	if _, err := tx.Exec(`DELETE FROM workflows WHERE project_name = ?`, projectName); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete workflows for project %q: %w", projectName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow cleanup transaction: %w", err)
	}
	return nil
}

func listProjectJobLogPaths(db *sql.DB, projectName string) ([]string, error) {
	rows, err := db.Query(
		`SELECT log_path
		 FROM jobs
		 WHERE project_name = ? AND log_path IS NOT NULL
		 ORDER BY created_at ASC`,
		projectName,
	)
	if err != nil {
		return nil, fmt.Errorf("list job log paths for project %q: %w", projectName, err)
	}
	defer rows.Close()

	var logPaths []string
	for rows.Next() {
		var logPath sql.NullString
		if err := rows.Scan(&logPath); err != nil {
			return nil, fmt.Errorf("scan job log path for project %q: %w", projectName, err)
		}
		if logPath.Valid {
			logPaths = append(logPaths, logPath.String)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job log paths for project %q: %w", projectName, err)
	}

	return logPaths, nil
}

func listProjectWorkflowLogPaths(db *sql.DB, projectName string) ([]string, error) {
	rows, err := db.Query(
		`SELECT log_path
		 FROM workflow_runs
		 WHERE project_name = ? AND log_path IS NOT NULL
		 ORDER BY created_at ASC`,
		projectName,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflow log paths for project %q: %w", projectName, err)
	}
	defer rows.Close()

	var logPaths []string
	for rows.Next() {
		var logPath sql.NullString
		if err := rows.Scan(&logPath); err != nil {
			return nil, fmt.Errorf("scan workflow log path for project %q: %w", projectName, err)
		}
		if logPath.Valid {
			logPaths = append(logPaths, logPath.String)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow log paths for project %q: %w", projectName, err)
	}

	return logPaths, nil
}

func listProjectWorkflowPayloadPaths(db *sql.DB, dataDir string, projectName string) ([]string, error) {
	rows, err := db.Query(
		`SELECT id
		 FROM workflow_runs
		 WHERE project_name = ?
		 ORDER BY created_at ASC`,
		projectName,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflow payload paths for project %q: %w", projectName, err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan workflow payload run ID for project %q: %w", projectName, err)
		}
		paths = append(paths, workflowPayloadPath(dataDir, runID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow payload paths for project %q: %w", projectName, err)
	}
	return paths, nil
}
