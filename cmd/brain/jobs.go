package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	jobStatusQueued   = "queued"
	projectStatusIdle = "idle"
)

var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type job struct {
	ID           string `json:"id"`
	ProjectName  string `json:"projectName"`
	RepoURL      string `json:"repoUrl"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

type createDeploymentRequest struct {
	Name    string `json:"name"`
	RepoURL string `json:"repoUrl"`
}

func handleCreateDeployment(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var request createDeploymentRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		projectName := strings.TrimSpace(request.Name)
		repoURL := strings.TrimSpace(request.RepoURL)
		if !isValidProjectName(projectName) {
			http.Error(w, "invalid project name", http.StatusBadRequest)
			return
		}
		if repoURL == "" {
			http.Error(w, "repoUrl is required", http.StatusBadRequest)
			return
		}

		job, err := createQueuedJob(db, projectName, repoURL)
		if err != nil {
			http.Error(w, "failed to create deployment job", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusAccepted, job)
	}
}

func handleGetJob(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimSpace(r.PathValue("jobID"))
		if jobID == "" {
			http.Error(w, "job ID is required", http.StatusBadRequest)
			return
		}

		job, err := getJob(db, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to fetch job", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, job)
	}
}

func createQueuedJob(db *sql.DB, projectName string, repoURL string) (job, error) {
	jobID, err := newID()
	if err != nil {
		return job{}, err
	}

	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return job{}, err
	}

	if _, err := tx.Exec(
		"INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)",
		projectName,
		projectStatusIdle,
		createdAt,
	); err != nil {
		_ = tx.Rollback()
		return job{}, err
	}

	if _, err := tx.Exec(
		"INSERT INTO jobs(id, project_name, repo_url, status, created_at) VALUES(?, ?, ?, ?, ?)",
		jobID,
		projectName,
		repoURL,
		jobStatusQueued,
		createdAt,
	); err != nil {
		_ = tx.Rollback()
		return job{}, err
	}

	if err := tx.Commit(); err != nil {
		return job{}, err
	}

	return job{
		ID:          jobID,
		ProjectName: projectName,
		RepoURL:     repoURL,
		Status:      jobStatusQueued,
		CreatedAt:   createdAt,
	}, nil
}

func getJob(db *sql.DB, jobID string) (job, error) {
	var job job
	var errorMessage sql.NullString

	err := db.QueryRow(
		`SELECT id, project_name, repo_url, status, error_message, created_at
		 FROM jobs
		 WHERE id = ?`,
		jobID,
	).Scan(
		&job.ID,
		&job.ProjectName,
		&job.RepoURL,
		&job.Status,
		&errorMessage,
		&job.CreatedAt,
	)
	if err != nil {
		return job, err
	}

	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}

	return job, nil
}

func isValidProjectName(name string) bool {
	return projectNamePattern.MatchString(name)
}

func newID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(buffer[:]), nil
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
