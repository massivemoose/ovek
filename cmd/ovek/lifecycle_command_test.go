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

func TestStopCommandCallsRuntimeStopEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo-app/runtime/stop" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
			ProjectName:         "demo-app",
			CurrentDeploymentID: stringPointer("dep_123"),
			App: &brainapi.ProjectRuntimeApp{
				ContainerName: "ovek-demo-app-app-dep_123",
				ImageRef:      "ghcr.io/example/demo:latest",
				Running:       false,
			},
			PocketBase: &brainapi.ProjectRuntimeContainer{ContainerName: "ovek-demo-app-pb", Running: true},
			Network:    &brainapi.ProjectRuntimeNetwork{Name: "ovek-demo-app-net"},
		})
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"stop", "demo-app"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	for _, fragment := range []string{"Stopped", "demo-app", "running=false", "Database"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, stdout.String())
		}
	}
}

func TestRemoveCommandPassesDatabaseRemovalFlags(t *testing.T) {
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runtime":
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET runtime request, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName: "demo-app",
				App:         &brainapi.ProjectRuntimeApp{ContainerName: "ovek-demo-app-app-dep_123"},
				PocketBase:  &brainapi.ProjectRuntimeContainer{ContainerName: "ovek-demo-app-pb", Running: true},
				Network:     &brainapi.ProjectRuntimeNetwork{Name: "ovek-demo-app-net"},
			})
		case "/v1/projects/demo-app/runtime/app":
			deleteCalled = true
			if r.Method != http.MethodDelete {
				t.Fatalf("expected DELETE request, got %s", r.Method)
			}
			if got := r.URL.Query().Get("removeDatabase"); got != "true" {
				t.Fatalf("expected removeDatabase=true, got %q", got)
			}
			if got := r.URL.Query().Get("deleteDatabaseData"); got != "true" {
				t.Fatalf("expected deleteDatabaseData=true, got %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	store := config.NewStore(t.TempDir())
	if err := store.SaveProfile("default", config.Profile{Host: server.URL, APIKey: "test-key"}, true); err != nil {
		t.Fatalf("expected config save to succeed, got error: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStoreAndInput(context.Background(), []string{"rm", "demo-app", "--remove-database", "--delete-database-data"}, "demo-app\n", &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected delete endpoint to be called")
	}
	if !strings.Contains(stdout.String(), "Type \"demo-app\"") {
		t.Fatalf("expected confirmation prompt, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "removed, data deleted") {
		t.Fatalf("expected removal summary, got %q", stdout.String())
	}
}

func TestRemoveCommandReportsNothingToRemoveWithoutCallingDelete(t *testing.T) {
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runtime":
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET runtime request, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{ProjectName: "demo-app"})
		case "/v1/projects/demo-app/runtime/app":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
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
	exitCode := runWithStore(context.Background(), []string{"rm", "demo-app", "--remove-database", "--delete-database-data"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if deleteCalled {
		t.Fatal("expected delete endpoint not to be called")
	}
	for _, fragment := range []string{"Nothing To Remove", "App Runtime  none", "Database     none"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, stdout.String())
		}
	}
}

func TestRemoveCommandAcceptsFalseDatabaseFlagValues(t *testing.T) {
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName: "demo-app",
				App:         &brainapi.ProjectRuntimeApp{ContainerName: "ovek-demo-app-app-dep_123"},
				PocketBase:  &brainapi.ProjectRuntimeContainer{ContainerName: "ovek-demo-app-pb", Running: true},
			})
		case "/v1/projects/demo-app/runtime/app":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
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
	exitCode := runWithStore(context.Background(), []string{
		"rm", "--remove-database=false", "--delete-database-data=false", "demo-app",
	}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected app runtime delete endpoint to be called")
	}
	if strings.Contains(stdout.String(), "Type \"demo-app\"") {
		t.Fatalf("expected no database removal prompt, got %q", stdout.String())
	}
}

func TestRemoveCommandRequiresProjectNameConfirmationForDatabaseRemoval(t *testing.T) {
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/demo-app/runtime":
			_ = json.NewEncoder(w).Encode(brainapi.ProjectRuntime{
				ProjectName: "demo-app",
				PocketBase:  &brainapi.ProjectRuntimeContainer{ContainerName: "ovek-demo-app-pb", Running: true},
			})
		case "/v1/projects/demo-app/runtime/app":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
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
	exitCode := runWithStoreAndInput(context.Background(), []string{"rm", "demo-app", "--remove-database"}, "wrong\n", &stdout, &stderr, store)
	if exitCode == 0 {
		t.Fatalf("expected confirmation failure, got stdout %q", stdout.String())
	}
	if deleteCalled {
		t.Fatal("expected delete endpoint not to be called")
	}
	if !strings.Contains(stderr.String(), "database removal not confirmed") {
		t.Fatalf("expected confirmation error, got %q", stderr.String())
	}
}

func TestRemoveHelpPrintsRemoveUsage(t *testing.T) {
	store := config.NewStore(t.TempDir())

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := runWithStore(context.Background(), []string{"rm", "--help"}, &stdout, &stderr, store)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with stderr %q", exitCode, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "ovek rm <project> [--remove-database] [--delete-database-data]") {
		t.Fatalf("expected rm usage, got %q", output)
	}
	if strings.Contains(output, "Commands:") {
		t.Fatalf("expected command-specific usage, got root usage %q", output)
	}
}
