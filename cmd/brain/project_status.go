package main

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	projectStatusIdle      = "idle"
	projectStatusDeploying = "deploying"
	projectStatusRunning   = "running"
	projectStatusStopped   = "stopped"
	projectStatusFailed    = "failed"
)

type projectStatusStore interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func reconcileAllProjectStatuses(db *sql.DB) error {
	projectNames, err := listProjectNames(db)
	if err != nil {
		return err
	}

	for _, projectName := range projectNames {
		if err := syncProjectStatus(db, projectName); err != nil {
			return err
		}
	}

	return nil
}

func syncProjectStatus(store projectStatusStore, projectName string) error {
	status, err := deriveProjectStatus(store, projectName)
	if err != nil {
		return err
	}

	return setProjectStatus(store, projectName, status)
}

func deriveProjectStatus(store projectStatusStore, projectName string) (string, error) {
	var currentStatus string
	var currentDeploymentID sql.NullString
	var activeJobCount int
	err := store.QueryRow(
		`SELECT status,
		        current_deployment_id,
		        (
		          SELECT COUNT(1)
		          FROM jobs
		          WHERE project_name = p.name AND status IN (?, ?)
		        )
		 FROM projects p
		 WHERE name = ?`,
		jobStatusQueued,
		jobStatusRunning,
		projectName,
	).Scan(&currentStatus, &currentDeploymentID, &activeJobCount)
	if err != nil {
		return "", fmt.Errorf("load project %q status inputs: %w", projectName, err)
	}

	var latestJobStatus sql.NullString
	err = store.QueryRow(
		`SELECT status
		 FROM jobs
		 WHERE project_name = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		projectName,
	).Scan(&latestJobStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load latest job status for project %q: %w", projectName, err)
	}

	switch {
	case activeJobCount > 0:
		return projectStatusDeploying, nil
	case currentDeploymentID.Valid && currentStatus == projectStatusStopped:
		return projectStatusStopped, nil
	case currentDeploymentID.Valid:
		return projectStatusRunning, nil
	case latestJobStatus.Valid && latestJobStatus.String == jobStatusFailed:
		return projectStatusFailed, nil
	default:
		return projectStatusIdle, nil
	}
}

func setProjectStatus(store projectStatusStore, projectName string, status string) error {
	updateResult, err := store.Exec(
		`UPDATE projects
		 SET status = ?
		 WHERE name = ?`,
		status,
		projectName,
	)
	if err != nil {
		return fmt.Errorf("set project %q status to %q: %w", projectName, status, err)
	}

	return requireUpdatedRow(updateResult, "set project status")
}
