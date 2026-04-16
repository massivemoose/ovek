package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/config"
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
	if err := store.Save(config.Config{Host: server.URL, APIKey: "test-key"}); err != nil {
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
					ContainerName: "alces-demo-app-app",
					ImageRef:      "localhost:5001/alces-demo-app:dep_123",
					Running:       true,
				},
			})
		case "/v1/projects/demo-app/jobs":
			_ = json.NewEncoder(w).Encode([]brainapi.Job{{
				ID:        "job_123",
				Status:    "succeeded",
				RepoURL:   "https://example.com/demo.git",
				CreatedAt: "2026-04-15T00:01:00Z",
			}})
		case "/v1/projects/demo-app/deployments":
			_ = json.NewEncoder(w).Encode([]brainapi.Deployment{{
				ID:        "dep_123",
				Status:    "succeeded",
				ImageRef:  "localhost:5001/alces-demo-app:dep_123",
				CreatedAt: "2026-04-15T00:02:00Z",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.Save(config.Config{Host: server.URL, APIKey: "test-key"}); err != nil {
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
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, output)
		}
	}
}
