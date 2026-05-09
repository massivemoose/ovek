package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/config"
)

func TestRunCreatesJobAndPrintsSummary(t *testing.T) {
	currentDeploymentID := "dep_run"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runs":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", r.Method)
			}
			var request brainapi.CreateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected run request to decode, got error: %v", err)
			}
			if request.CapsuleRef != "ghcr.io/example/demo:2026.05.01" {
				t.Fatalf("expected capsule ref %q, got %q", "ghcr.io/example/demo:2026.05.01", request.CapsuleRef)
			}
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_run",
				Status:      "queued",
				ProjectName: "demo-app",
				SourceType:  brainapi.JobSourceTypeImage,
				SourceRef:   request.CapsuleRef,
			})
		case "/v1/jobs/job_run/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: lifecycle: using prebuilt image ghcr.io/example/demo:2026.05.01\n\n")
		case "/v1/jobs/job_run":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_run",
				Status:      "succeeded",
				ImageRef:    "ghcr.io/example/demo:2026.05.01",
				FinishedAt:  "2026-05-01T00:20:00Z",
				ProjectName: "demo-app",
				SourceType:  brainapi.JobSourceTypeImage,
				SourceRef:   "ghcr.io/example/demo:2026.05.01",
			})
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-05-01T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "ovek-demo-app-app",
					ImageRef:      "ghcr.io/example/demo:2026.05.01",
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
		[]string{"run", "demo-app", "ghcr.io/example/demo:2026.05.01"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{
		"Run",
		"Run Logs",
		"Result",
		"Project",
		"job_run",
		"ghcr.io/example/demo:2026.05.01",
		"lifecycle: using prebuilt image",
		"running",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestRunRetriesAfterReauthRequired(t *testing.T) {
	currentDeploymentID := "dep_run_secure"
	runAttempts := 0
	reauthCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runs":
			runAttempts++
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", r.Method)
			}
			if runAttempts == 1 {
				if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "" {
					t.Fatalf("expected first run attempt without reauth token, got %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(brainapi.APIError{
					Code:    "reauth_required",
					Message: "critical mutation requires reauthentication",
				})
				return
			}
			if got := r.Header.Get("X-Ovek-Reauth-Token"); got != "rt-test" {
				t.Fatalf("expected retried run request to include reauth token, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_secure_run",
				Status:      "queued",
				ProjectName: "demo-app",
				SourceType:  brainapi.JobSourceTypeImage,
				SourceRef:   "ghcr.io/example/secure:latest",
			})
		case "/v1/auth/reauth":
			reauthCalls++
			var request brainapi.ReauthRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected reauth request to decode, got error: %v", err)
			}
			if request.Password != "secret-pass" {
				t.Fatalf("expected reauth password, got %q", request.Password)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{ReauthToken: "rt-test"})
		case "/v1/jobs/job_secure_run/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: secure run line\n\n")
		case "/v1/jobs/job_secure_run":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_secure_run",
				Status:      "succeeded",
				ImageRef:    "ghcr.io/example/secure:latest",
				FinishedAt:  "2026-05-01T00:30:00Z",
				ProjectName: "demo-app",
			})
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-05-01T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "ovek-demo-app-app",
					ImageRef:      "ghcr.io/example/secure:latest",
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
		[]string{"run", "demo-app", "ghcr.io/example/secure:latest"},
		"secret-pass\n",
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if runAttempts != 2 {
		t.Fatalf("expected run to be attempted twice, got %d", runAttempts)
	}
	if reauthCalls != 1 {
		t.Fatalf("expected one reauth request, got %d", reauthCalls)
	}
	if !strings.Contains(stdout.String(), "secure run line") {
		t.Fatalf("expected run output to contain logs, got %q", stdout.String())
	}
}

func TestRunReportsActiveDeploymentConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runs":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(brainapi.APIError{
				Code:    "active_deployment_exists",
				Message: "project \"demo-app\" already has an active deployment job \"job_active\" with status \"running\"",
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
		[]string{"run", "demo-app", "ghcr.io/example/demo:latest"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout, got %q", stdout.String())
	}
	for _, fragment := range []string{
		"already has an active deployment job",
		"job_active",
		"ovek status demo-app",
	} {
		if !strings.Contains(stderr.String(), fragment) {
			t.Fatalf("expected stderr to contain %q, got %q", fragment, stderr.String())
		}
	}
}

func TestRunFailurePrintsSummaryAndLogHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runs":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:          "job_fail_run",
				Status:      "queued",
				ProjectName: "demo-app",
				SourceType:  brainapi.JobSourceTypeImage,
				SourceRef:   "ghcr.io/example/broken:latest",
			})
		case "/v1/jobs/job_fail_run/logs/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: image pull failed\n\n")
		case "/v1/jobs/job_fail_run":
			_ = json.NewEncoder(w).Encode(brainapi.Job{
				ID:           "job_fail_run",
				Status:       "failed",
				FinishedAt:   "2026-05-01T00:20:00Z",
				ProjectName:  "demo-app",
				ErrorMessage: "app container provisioning failed: image pull failed",
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
		[]string{"run", "demo-app", "ghcr.io/example/broken:latest"},
		&stdout,
		&stderr,
		store,
	)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{
		"Run",
		"Run Logs",
		"Result",
		"Next Step",
		"job_fail_run",
		"app container provisioning failed: image pull failed",
		"ovek logs --job job_fail_run --no-follow",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
	if !strings.Contains(stderr.String(), "run failed: app container provisioning failed: image pull failed") {
		t.Fatalf("expected stderr to contain the run failure, got %q", stderr.String())
	}
}
