package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/config"
)

func TestStatusListsProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			_ = json.NewEncoder(w).Encode([]brainapi.ProjectSummary{
				{Name: "alpha-app", Status: "running", CreatedAt: "2026-04-15T00:00:00Z"},
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
	exitCode := runWithStore(context.Background(), []string{"status"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{"Projects", "alpha-app", "running"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestStatusShowsProjectDetail(t *testing.T) {
	currentDeploymentID := "dep_123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-04-15T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "ovek-demo-app-app",
					ImageRef:      "localhost:5001/ovek-demo-app:dep_123",
					Running:       true,
				},
			})
		case "/v1/projects/demo-app/jobs":
			_ = json.NewEncoder(w).Encode([]brainapi.Job{
				{
					ID:           "job_124",
					Status:       "failed",
					RepoURL:      "https://example.com/demo.git",
					CreatedAt:    "2026-04-15T00:03:00Z",
					ErrorMessage: "app readiness failed: timed out waiting for port",
				},
				{
					ID:        "job_123",
					Status:    "running",
					Phase:     "building image",
					RepoURL:   "https://example.com/demo.git",
					CreatedAt: "2026-04-15T00:01:00Z",
				},
			})
		case "/v1/projects/demo-app/deployments":
			_ = json.NewEncoder(w).Encode([]brainapi.Deployment{{
				ID:        "dep_123",
				Status:    "succeeded",
				ImageRef:  "localhost:5001/ovek-demo-app:dep_123",
				CreatedAt: "2026-04-15T00:02:00Z",
			}})
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
	exitCode := runWithStore(context.Background(), []string{"status", "demo-app"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	for _, fragment := range []string{
		"Project",
		"Runtime",
		"Recent Jobs",
		"Recent Deployments",
		"demo-app",
		"dep_123",
		"job_123",
		"job_124",
		"Phase",
		"building image",
		"Error",
		"app readiness failed: timed out waiting for port",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}

func TestStatusShowsStoppedWhenRuntimeAppIsStopped(t *testing.T) {
	currentDeploymentID := "dep_123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectSummary{
				Name:                "demo-app",
				Status:              "running",
				CurrentDeploymentID: &currentDeploymentID,
				CreatedAt:           "2026-04-15T00:00:00Z",
			})
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName:         "demo-app",
				CurrentDeploymentID: &currentDeploymentID,
				App: &brainapi.ProjectRuntimeApp{
					ContainerName: "ovek-demo-app-app",
					ImageRef:      "ghcr.io/example/demo:latest",
					Running:       false,
				},
			})
		case "/v1/projects/demo-app/jobs":
			_ = json.NewEncoder(w).Encode([]brainapi.Job{})
		case "/v1/projects/demo-app/deployments":
			_ = json.NewEncoder(w).Encode([]brainapi.Deployment{})
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
	exitCode := runWithStore(context.Background(), []string{"status", "demo-app"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "stopped") {
		t.Fatalf("expected stopped status, got %q", output)
	}
	if !strings.Contains(output, "running=false") {
		t.Fatalf("expected stopped runtime detail, got %q", output)
	}
}
