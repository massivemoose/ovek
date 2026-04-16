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
