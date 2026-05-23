package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestWorkflowRunAPICreatesListsGetsAndEnqueuesRun(t *testing.T) {
	enqueuer := &recordingWorkflowRunEnqueuer{}
	handler, db, _ := newTestWorkflowRunHandler(t, enqueuer)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:        "demo-app",
		Name:               "digest",
		SourceImageRef:     "ghcr.io/example/digest:latest",
		ResolvedRepoDigest: "ghcr.io/example/digest@sha256:repo-digest",
		RuntimeImageID:     "sha256:image-id",
		QueueCap:           2,
		Enabled:            true,
	})

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/workflows/digest/runs",
		strings.NewReader(`{"triggerType":"manual"}`),
	)
	createRequest.Header.Set(headerAPIKey, "test-key")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusAccepted {
		t.Fatalf("expected create status %d, got %d with body %q", http.StatusAccepted, createRecorder.Code, createRecorder.Body.String())
	}

	var run brainapi.WorkflowRun
	if err := json.NewDecoder(createRecorder.Body).Decode(&run); err != nil {
		t.Fatalf("expected workflow run response to decode, got error: %v", err)
	}
	if run.Status != workflowRunStatusQueued || run.TriggerType != workflowRunTriggerManual {
		t.Fatalf("expected queued manual run, got %#v", run)
	}
	if len(enqueuer.runIDs) != 1 || enqueuer.runIDs[0] != run.ID {
		t.Fatalf("expected run to be enqueued, got %#v", enqueuer.runIDs)
	}
	if createRecorder.Header().Get("Location") != run.Links.Self {
		t.Fatalf("expected location header %q, got %q", run.Links.Self, createRecorder.Header().Get("Location"))
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/workflow-runs", nil)
	listRequest.Header.Set(headerAPIKey, "test-key")
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %q", http.StatusOK, listRecorder.Code, listRecorder.Body.String())
	}
	var runs []brainapi.WorkflowRun
	if err := json.NewDecoder(listRecorder.Body).Decode(&runs); err != nil {
		t.Fatalf("expected run list to decode, got error: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("expected created run in list, got %#v", runs)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/workflow-runs/"+run.ID, nil)
	getRequest.Header.Set(headerAPIKey, "test-key")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("expected get status %d, got %d with body %q", http.StatusOK, getRecorder.Code, getRecorder.Body.String())
	}
}

func TestWorkflowRunAPIReturnsQueueFull(t *testing.T) {
	handler, db, _ := newTestWorkflowRunHandler(t, nil)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       1,
		Enabled:        true,
	})
	if _, err := createQueuedWorkflowRun(context.Background(), db, "demo-app", "digest", workflowRunTriggerManual); err != nil {
		t.Fatalf("expected seed run creation to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/projects/demo-app/workflows/digest/runs", strings.NewReader(`{}`))
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusTooManyRequests, errorCodeWorkflowQueueFull, "workflow queue full")
}

func TestWorkflowRunLogsAPIReadsPersistedLogs(t *testing.T) {
	handler, db, dataDir := newTestWorkflowRunHandler(t, nil)
	seedWorkflowDefinition(t, db, workflowDefinition{
		ProjectName:    "demo-app",
		Name:           "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		QueueCap:       1,
		Enabled:        true,
	})
	run, err := createWorkflowRunRecord(context.Background(), db, workflowRun{
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		TriggerType:    workflowRunTriggerManual,
		Status:         workflowRunStatusSucceeded,
		SourceImageRef: "ghcr.io/example/digest:latest",
		RuntimeImageID: "sha256:image-id",
		LogPath:        workflowLogPath(dataDir, "run-123"),
	})
	if err != nil {
		t.Fatalf("expected workflow run creation to succeed, got error: %v", err)
	}
	logPath := workflowLogPath(dataDir, "run-123")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("expected log dir creation to succeed, got error: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("expected log write to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/workflow-runs/"+run.ID+"/logs", nil)
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected logs status %d, got %d with body %q", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "hello\n" {
		t.Fatalf("expected log body, got %q", recorder.Body.String())
	}
}

func newTestWorkflowRunHandler(t *testing.T, enqueuer workflowRunEnqueuer) (http.Handler, *sql.DB, string) {
	t.Helper()

	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	handler := newHandlerWithRegistryStore(config{
		BrainAPIKey: "test-key",
		DataDir:     dataDir,
	}, db, noopEnqueuer{}, noopProjectCleaner{}, noopProjectRuntimeService{}, managedProjectPocketBaseService{}, passthroughWorkflowImageResolver{}, enqueuer, nil, registryCredentialStore{})

	return handler, db, dataDir
}

type recordingWorkflowRunEnqueuer struct {
	runIDs []string
}

func (enqueuer *recordingWorkflowRunEnqueuer) Enqueue(runID string) {
	enqueuer.runIDs = append(enqueuer.runIDs, runID)
}
