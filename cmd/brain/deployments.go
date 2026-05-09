package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func handleListProjectDeployments(db *sql.DB) http.HandlerFunc {
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
			writeJSONError(w, http.StatusInternalServerError, errorCodeListDeploymentsFailed, "failed to list deployments")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}

		deployments, err := listProjectDeployments(db, projectName, limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeListDeploymentsFailed, "failed to list deployments")
			return
		}

		writeJSON(w, http.StatusOK, deployments)
	}
}

func handleGetProjectDeployment(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		exists, err := projectExists(db, projectName)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchDeploymentFailed, "failed to fetch deployment")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}

		deploymentID := strings.TrimSpace(r.PathValue("deploymentID"))
		deployment, err := getProjectDeployment(db, projectName, deploymentID)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeDeploymentNotFound, "deployment not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchDeploymentFailed, "failed to fetch deployment")
			return
		}

		writeJSON(w, http.StatusOK, deployment)
	}
}

func listProjectDeployments(db *sql.DB, projectName string, limit int) ([]deploymentRecord, error) {
	rows, err := db.Query(
		`SELECT id, project_name, image_ref, source_type, source_ref, app_container_name, network_name, pb_container_name, status, created_at, config_revision_id
		 FROM deployments
		 WHERE project_name = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectName,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query deployments for project %q: %w", projectName, err)
	}
	defer rows.Close()

	deployments := make([]deploymentRecord, 0)
	for rows.Next() {
		deployment, err := scanDeploymentRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment for project %q: %w", projectName, err)
		}
		deployments = append(deployments, deployment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployments for project %q: %w", projectName, err)
	}

	return deployments, nil
}

func getProjectDeployment(db *sql.DB, projectName string, deploymentID string) (deploymentRecord, error) {
	return scanDeploymentRecord(
		db.QueryRow(
			`SELECT id, project_name, image_ref, source_type, source_ref, app_container_name, network_name, pb_container_name, status, created_at, config_revision_id
			 FROM deployments
			 WHERE project_name = ? AND id = ?`,
			projectName,
			deploymentID,
		),
	)
}

type deploymentRecordScanner interface {
	Scan(dest ...any) error
}

func scanDeploymentRecord(scanner deploymentRecordScanner) (deploymentRecord, error) {
	var deployment deploymentRecord
	var sourceType sql.NullString
	var sourceRef sql.NullString
	var configRevisionID sql.NullString
	if err := scanner.Scan(
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
	); err != nil {
		return deployment, err
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

	return deployment, nil
}
