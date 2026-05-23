package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestWorkflowDefinitionAPISetListGetAndDelete(t *testing.T) {
	imageResolver := &recordingWorkflowImageResolver{
		metadataByRef: map[string]workflowImageMetadata{
			"ghcr.io/example/digest:latest": {
				ResolvedRepoDigest: "ghcr.io/example/digest@sha256:first",
				RuntimeImageID:     "sha256:first-image",
			},
			"ghcr.io/example/digest:v2": {
				ResolvedRepoDigest: "ghcr.io/example/digest@sha256:second",
				RuntimeImageID:     "sha256:second-image",
			},
		},
	}
	handler, db := newTestHandlerWithWorkflowImageResolver(t, imageResolver)

	setRequest := httptest.NewRequest(
		http.MethodPut,
		"/v1/projects/demo-app/workflows/digest",
		strings.NewReader(`{"imageRef":"ghcr.io/example/digest:latest","schedule":"*/5 * * * *"}`),
	)
	setRequest.Header.Set(headerAPIKey, "test-key")
	setRecorder := httptest.NewRecorder()
	handler.ServeHTTP(setRecorder, setRequest)
	if setRecorder.Code != http.StatusOK {
		t.Fatalf("expected set status %d, got %d with body %q", http.StatusOK, setRecorder.Code, setRecorder.Body.String())
	}

	var created brainapi.Workflow
	if err := json.NewDecoder(setRecorder.Body).Decode(&created); err != nil {
		t.Fatalf("expected workflow response to decode, got error: %v", err)
	}
	if created.ProjectName != "demo-app" || created.Name != "digest" || created.SourceImageRef != "ghcr.io/example/digest:latest" {
		t.Fatalf("expected workflow identity and image ref, got %#v", created)
	}
	if created.Schedule != "*/5 * * * *" || created.QueueCap != defaultWorkflowQueueCap || !created.Enabled {
		t.Fatalf("expected default queue cap, enabled workflow, and schedule, got %#v", created)
	}
	if created.RuntimeImageID != "sha256:first-image" || created.ResolvedRepoDigest != "ghcr.io/example/digest@sha256:first" {
		t.Fatalf("expected resolved image metadata, got %#v", created)
	}
	if created.Links.Self != brainapi.ProjectWorkflowPath("demo-app", "digest") {
		t.Fatalf("expected self link, got %#v", created.Links)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/workflows", nil)
	listRequest.Header.Set(headerAPIKey, "test-key")
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %q", http.StatusOK, listRecorder.Code, listRecorder.Body.String())
	}
	var workflows []brainapi.Workflow
	if err := json.NewDecoder(listRecorder.Body).Decode(&workflows); err != nil {
		t.Fatalf("expected list response to decode, got error: %v", err)
	}
	if len(workflows) != 1 || workflows[0].Name != "digest" {
		t.Fatalf("expected one workflow in list, got %#v", workflows)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/workflows/digest", nil)
	getRequest.Header.Set(headerAPIKey, "test-key")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("expected get status %d, got %d with body %q", http.StatusOK, getRecorder.Code, getRecorder.Body.String())
	}

	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/v1/projects/demo-app/workflows/digest",
		strings.NewReader(`{"imageRef":"ghcr.io/example/digest:v2","queueCap":7,"enabled":false}`),
	)
	updateRequest.Header.Set(headerAPIKey, "test-key")
	updateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("expected update status %d, got %d with body %q", http.StatusOK, updateRecorder.Code, updateRecorder.Body.String())
	}
	var updated brainapi.Workflow
	if err := json.NewDecoder(updateRecorder.Body).Decode(&updated); err != nil {
		t.Fatalf("expected update response to decode, got error: %v", err)
	}
	if updated.SourceImageRef != "ghcr.io/example/digest:v2" || updated.ResolvedRepoDigest != "ghcr.io/example/digest@sha256:second" || updated.RuntimeImageID != "sha256:second-image" {
		t.Fatalf("expected same-name update to refresh image metadata, got %#v", updated)
	}
	if updated.Schedule != "" || updated.QueueCap != 7 || updated.Enabled {
		t.Fatalf("expected full replacement to update image, clear schedule, and preserve requested flags, got %#v", updated)
	}
	if len(imageResolver.calls) != 2 {
		t.Fatalf("expected two image resolution calls, got %#v", imageResolver.calls)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/workflows/digest", nil)
	deleteRequest.Header.Set(headerAPIKey, "test-key")
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected delete status %d, got %d with body %q", http.StatusNoContent, deleteRecorder.Code, deleteRecorder.Body.String())
	}

	if count := queryCount(t, db, "SELECT COUNT(1) FROM workflows WHERE project_name = 'demo-app'"); count != 0 {
		t.Fatalf("expected workflow row to be deleted, got %d", count)
	}
}

func TestWorkflowDefinitionAPIValidation(t *testing.T) {
	handler, _ := newTestHandlerWithWorkflowImageResolver(t, &recordingWorkflowImageResolver{})

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid project",
			method:      http.MethodPut,
			path:        "/v1/projects/Bad_Project/workflows/digest",
			body:        `{"imageRef":"ghcr.io/example/digest:latest"}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeInvalidProjectName,
			wantMessage: "invalid project name",
		},
		{
			name:        "invalid workflow",
			method:      http.MethodPut,
			path:        "/v1/projects/demo-app/workflows/Bad_Workflow",
			body:        `{"imageRef":"ghcr.io/example/digest:latest"}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeInvalidWorkflowName,
			wantMessage: "invalid workflow name",
		},
		{
			name:        "missing image",
			method:      http.MethodPut,
			path:        "/v1/projects/demo-app/workflows/digest",
			body:        `{}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeCapsuleRefRequired,
			wantMessage: "imageRef is required",
		},
		{
			name:        "invalid image",
			method:      http.MethodPut,
			path:        "/v1/projects/demo-app/workflows/digest",
			body:        `{"imageRef":"ghcr.io/example/digest latest"}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeInvalidCapsuleRef,
			wantMessage: "imageRef must not contain whitespace or control characters",
		},
		{
			name:        "invalid schedule",
			method:      http.MethodPut,
			path:        "/v1/projects/demo-app/workflows/digest",
			body:        `{"imageRef":"ghcr.io/example/digest:latest","schedule":"not cron"}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeInvalidWorkflow,
			wantMessage: "invalid schedule: expected exactly 5 fields, found 2: [not cron]",
		},
		{
			name:        "negative queue cap",
			method:      http.MethodPut,
			path:        "/v1/projects/demo-app/workflows/digest",
			body:        `{"imageRef":"ghcr.io/example/digest:latest","queueCap":-1}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    errorCodeInvalidWorkflow,
			wantMessage: "queueCap must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set(headerAPIKey, "test-key")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			assertAPIError(t, recorder, tt.wantStatus, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestWorkflowDefinitionAPIReportsImagePullFailures(t *testing.T) {
	imageResolver := &recordingWorkflowImageResolver{
		err: errString("pull image \"ghcr.io/example/private:latest\": denied; if this is a private image, run: ovek registry login ghcr.io"),
	}
	handler, _ := newTestHandlerWithWorkflowImageResolver(t, imageResolver)

	request := httptest.NewRequest(
		http.MethodPut,
		"/v1/projects/demo-app/workflows/digest",
		strings.NewReader(`{"imageRef":"ghcr.io/example/private:latest"}`),
	)
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(
		t,
		recorder,
		http.StatusBadGateway,
		errorCodeWorkflowFailed,
		"pull image \"ghcr.io/example/private:latest\": denied; if this is a private image, run: ovek registry login ghcr.io",
	)
	if len(imageResolver.calls) != 1 || imageResolver.calls[0] != "ghcr.io/example/private:latest" {
		t.Fatalf("expected image resolver to be called with private ref, got %#v", imageResolver.calls)
	}
}

func TestWorkflowDefinitionAPIListRequiresExistingProject(t *testing.T) {
	handler, _ := newTestHandler(t, noopEnqueuer{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/workflows", nil)
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func newTestHandlerWithWorkflowImageResolver(t *testing.T, imageResolver workflowImageResolver) (http.Handler, *sql.DB) {
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
	}, db, noopEnqueuer{}, noopProjectCleaner{}, noopProjectRuntimeService{}, managedProjectPocketBaseService{}, imageResolver, nil, registryCredentialStore{})

	return handler, db
}

type recordingWorkflowImageResolver struct {
	calls         []string
	metadataByRef map[string]workflowImageMetadata
	err           error
}

func (resolver *recordingWorkflowImageResolver) ResolveWorkflowImage(_ context.Context, imageRef string) (workflowImageMetadata, error) {
	resolver.calls = append(resolver.calls, imageRef)
	if resolver.err != nil {
		return workflowImageMetadata{}, resolver.err
	}
	metadata := resolver.metadataByRef[imageRef]
	if metadata.SourceImageRef == "" {
		metadata.SourceImageRef = imageRef
	}
	if metadata.RuntimeImageID == "" {
		metadata.RuntimeImageID = "sha256:test-image"
	}
	return metadata, nil
}

type errString string

func (err errString) Error() string {
	return string(err)
}

func TestWorkflowDefinitionAPIRequiresReauthInProdMode(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPut,
		"/v1/projects/demo-app/workflows/digest",
		strings.NewReader(`{"imageRef":"ghcr.io/example/digest:latest"}`),
	)
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestWorkflowDefinitionAPIAllowsMutationsWithProdReauthAndAudits(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got error: %v", err)
	}
	principal, err := validateAPIKey(context.Background(), db, apiKey)
	if err != nil {
		t.Fatalf("expected API key validation to succeed, got error: %v", err)
	}
	reauthToken, _, err := issueReauthToken(context.Background(), db, principal, "secret-pass")
	if err != nil {
		t.Fatalf("expected reauth token issue to succeed, got error: %v", err)
	}

	setRequest := httptest.NewRequest(
		http.MethodPut,
		"/v1/projects/demo-app/workflows/digest",
		strings.NewReader(`{"imageRef":"ghcr.io/example/digest:latest"}`),
	)
	setRequest.Header.Set(headerAPIKey, apiKey)
	setRequest.Header.Set(headerReauthToken, reauthToken)
	setRecorder := httptest.NewRecorder()
	handler.ServeHTTP(setRecorder, setRequest)
	if setRecorder.Code != http.StatusOK {
		t.Fatalf("expected set status %d, got %d with body %q", http.StatusOK, setRecorder.Code, setRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/workflows/digest", nil)
	deleteRequest.Header.Set(headerAPIKey, apiKey)
	deleteRequest.Header.Set(headerReauthToken, reauthToken)
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected delete status %d, got %d with body %q", http.StatusNoContent, deleteRecorder.Code, deleteRecorder.Body.String())
	}

	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'workflow.set.authorized' AND project_name = 'demo-app'"); count != 1 {
		t.Fatalf("expected workflow set audit log, got %d rows", count)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'workflow.delete.authorized' AND project_name = 'demo-app'"); count != 1 {
		t.Fatalf("expected workflow delete audit log, got %d rows", count)
	}
}
