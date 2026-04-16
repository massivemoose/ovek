package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const defaultListLimit = 20

type projectSummary struct {
	Name                string  `json:"name"`
	Status              string  `json:"status"`
	CurrentDeploymentID *string `json:"currentDeploymentId"`
	CreatedAt           string  `json:"createdAt"`
}

func handleListProjects(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseListLimit(r, defaultListLimit)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidLimit, "limit must be a positive integer")
			return
		}

		projects, err := listProjects(db, limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeListProjectsFailed, "failed to list projects")
			return
		}

		writeJSON(w, http.StatusOK, projects)
	}
}

func handleGetProject(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		project, err := getProject(db, projectName)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchProjectFailed, "failed to fetch project")
			return
		}

		writeJSON(w, http.StatusOK, project)
	}
}

func parseListLimit(r *http.Request, defaultLimit int) (int, error) {
	limitValue := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limitValue == "" {
		return defaultLimit, nil
	}

	limit, err := strconv.Atoi(limitValue)
	if err != nil {
		return 0, fmt.Errorf("parse limit %q: %w", limitValue, err)
	}
	if limit <= 0 {
		return 0, errors.New("limit must be positive")
	}

	return limit, nil
}

func listProjects(db *sql.DB, limit int) ([]projectSummary, error) {
	rows, err := db.Query(
		`SELECT name, status, current_deployment_id, created_at
		 FROM projects
		 ORDER BY name ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	projects := make([]projectSummary, 0)
	for rows.Next() {
		project, err := scanProjectSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, project)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}

	return projects, nil
}

func getProject(db *sql.DB, projectName string) (projectSummary, error) {
	return scanProjectSummary(
		db.QueryRow(
			`SELECT name, status, current_deployment_id, created_at
			 FROM projects
			 WHERE name = ?`,
			projectName,
		),
	)
}

type projectSummaryScanner interface {
	Scan(dest ...any) error
}

func scanProjectSummary(scanner projectSummaryScanner) (projectSummary, error) {
	var project projectSummary
	var currentDeploymentID sql.NullString
	if err := scanner.Scan(
		&project.Name,
		&project.Status,
		&currentDeploymentID,
		&project.CreatedAt,
	); err != nil {
		return project, err
	}
	if currentDeploymentID.Valid {
		deploymentID := currentDeploymentID.String
		project.CurrentDeploymentID = &deploymentID
	}

	return project, nil
}
