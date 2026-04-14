package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

var errProjectNotFound = errors.New("project not found")

type projectCleanupService interface {
	Cleanup(ctx context.Context, projectName string) error
}

type projectCleanupRuntime interface {
	ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error)
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
	RemoveProjectPocketBase(ctx context.Context, projectName string) error
	RemoveProjectNetwork(ctx context.Context, projectName string) error
}

type managedProjectCleaner struct {
	db              *sql.DB
	runtime         projectCleanupRuntime
	artifactCleaner registryArtifactCleaner
}

func newManagedProjectCleaner(db *sql.DB, runtime projectCleanupRuntime, artifactCleaners ...registryArtifactCleaner) managedProjectCleaner {
	var artifactCleaner registryArtifactCleaner
	if len(artifactCleaners) > 0 {
		artifactCleaner = artifactCleaners[0]
	}

	return managedProjectCleaner{
		db:              db,
		runtime:         runtime,
		artifactCleaner: artifactCleaner,
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

	apps, err := cleaner.runtime.ListProjectApps(ctx, projectName)
	if err != nil {
		return fmt.Errorf("list project apps: %w", err)
	}
	for _, app := range apps {
		if err := cleaner.runtime.RemoveProjectApp(ctx, app.deploymentRecord()); err != nil {
			return fmt.Errorf("remove project app %q: %w", app.AppContainerName, err)
		}
	}

	if err := cleaner.runtime.RemoveProjectPocketBase(ctx, projectName); err != nil {
		return fmt.Errorf("remove PocketBase: %w", err)
	}
	if err := cleaner.runtime.RemoveProjectNetwork(ctx, projectName); err != nil {
		return fmt.Errorf("remove project network: %w", err)
	}
	if err := clearProjectRuntimeState(cleaner.db, projectName); err != nil {
		return err
	}
	cleaner.cleanupProjectImages(ctx, projectName, imageRefs)

	return nil
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
