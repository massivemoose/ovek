package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/config"
)

func TestDeployCreatesJobAndPrintsSummary(t *testing.T) {
	currentDeploymentID := "dep_123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/deployments":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_123",
				Status:      "queued",
				ProjectName: "demo-app",
				RepoURL:     "https://example.com/demo.git",
			})
		case "/v1/jobs/job_123/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: build line\n\n")
		case "/v1/jobs/job_123":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:           "job_123",
				Status:       "succeeded",
				ImageRef:     "localhost:5001/alces-demo-app:dep_123",
				FinishedAt:   "2026-04-16T00:20:00Z",
				ProjectName:  "demo-app",
				RepoURL:      "https://example.com/demo.git",
				ErrorMessage: "",
			})
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-04-16T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "alces-demo-app-app",
					ImageRef:      "localhost:5001/alces-demo-app:dep_123",
					Running:       true,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(
		context.Background(),
		[]string{"deploy", "demo-app", "https://example.com/demo.git"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{
		"Deployment",
		"Build Logs",
		"Result",
		"Project",
		"build line",
		"job_123",
		"dep_123",
		"running",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestDeployRetriesAfterReauthRequired(t *testing.T) {
	currentDeploymentID := "dep_456"
	deployAttempts := 0
	reauthCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/deployments":
			deployAttempts++
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", r.Method)
			}
			if deployAttempts == 1 {
				if got := r.Header.Get("X-Alces-Reauth-Token"); got != "" {
					t.Fatalf("expected first deploy attempt without reauth token, got %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{
					Code:    "reauth_required",
					Message: "critical mutation requires reauthentication",
				})
				return
			}
			if got := r.Header.Get("X-Alces-Reauth-Token"); got != "rt-test" {
				t.Fatalf("expected retried deploy request to include reauth token, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_456",
				Status:      "queued",
				ProjectName: "demo-app",
				RepoURL:     "https://example.com/secure.git",
			})
		case "/v1/auth/reauth":
			reauthCalls++
			if got := r.Header.Get("X-API-Key"); got != "test-key" {
				t.Fatalf("expected reauth request to include API key, got %q", got)
			}
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "secret-pass" {
				t.Fatalf("expected reauth password, got %q", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt-test"})
		case "/v1/jobs/job_456/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: secure build line\n\n")
		case "/v1/jobs/job_456":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_456",
				Status:      "succeeded",
				ImageRef:    "localhost:5001/alces-demo-app:dep_456",
				FinishedAt:  "2026-04-16T00:30:00Z",
				ProjectName: "demo-app",
				RepoURL:     "https://example.com/secure.git",
			})
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-04-16T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "alces-demo-app-app",
					ImageRef:      "localhost:5001/alces-demo-app:dep_456",
					Running:       true,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("prod", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(
		context.Background(),
		[]string{"deploy", "demo-app", "https://example.com/secure.git"},
		"secret-pass\n",
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if deployAttempts != 2 {
		t.Fatalf("expected deploy to be attempted twice, got %d", deployAttempts)
	}
	if reauthCalls != 1 {
		t.Fatalf("expected one reauth request, got %d", reauthCalls)
	}
	if !strings.Contains(stdout.String(), "secure build line") {
		t.Fatalf("expected deploy output to contain build logs, got %q", stdout.String())
	}
}

func TestDeployFailurePrintsSummaryAndLogHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/deployments":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_fail",
				Status:      "queued",
				ProjectName: "demo-app",
				RepoURL:     "https://example.com/broken.git",
			})
		case "/v1/jobs/job_fail/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: clone failed\n\n")
		case "/v1/jobs/job_fail":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:           "job_fail",
				Status:       "failed",
				FinishedAt:   "2026-04-19T00:20:00Z",
				ProjectName:  "demo-app",
				RepoURL:      "https://example.com/broken.git",
				ErrorMessage: "source fetch failed: repository not found",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(
		context.Background(),
		[]string{"deploy", "demo-app", "https://example.com/broken.git"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{
		"Deployment",
		"Build Logs",
		"Result",
		"Next Step",
		"job_fail",
		"source fetch failed: repository not found",
		"alces logs --job job_fail --no-follow",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
	if !strings.Contains(stderr.String(), "deployment failed: source fetch failed: repository not found") {
		t.Fatalf("expected stderr to contain the deploy failure, got %q", stderr.String())
	}
}
