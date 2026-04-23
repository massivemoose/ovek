package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedProjectRuntimeServiceReturnsCurrentRuntime(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
		appsByProject: map[string][]projectAppRuntime{
			"alpha-app": {
				{
					DeploymentID:            "dep-alpha",
					ProjectName:             "alpha-app",
					AppContainerName:        "alces-alpha-app-app-dep-alpha",
					ImageRef:                "alces-alpha-app:dep-alpha",
					NetworkName:             "alpha-app-net",
					PocketBaseContainerName: "alces-alpha-app-pb",
					Running:                 true,
				},
			},
		},
		pocketBaseByProject: map[string]projectRuntimeContainer{
			"alpha-app": {
				ContainerName: "alces-alpha-app-pb",
				Running:       true,
			},
		},
		networkByProject: map[string]projectRuntimeNetwork{
			"alpha-app": {
				Name: "alpha-app-net",
			},
		},
	})

	runtimeView, err := service.GetRuntime(context.Background(), "alpha-app")
	if err != nil {
		t.Fatalf("expected runtime lookup to succeed, got error: %v", err)
	}

	if runtimeView.ProjectName != "alpha-app" {
		t.Fatalf("expected project name %q, got %q", "alpha-app", runtimeView.ProjectName)
	}
	if runtimeView.CurrentDeploymentID == nil || *runtimeView.CurrentDeploymentID != "dep-alpha" {
		t.Fatalf("expected current deployment %q, got %#v", "dep-alpha", runtimeView.CurrentDeploymentID)
	}
	if runtimeView.App == nil || runtimeView.App.ContainerName != "alces-alpha-app-app-dep-alpha" || !runtimeView.App.Running {
		t.Fatalf("expected running app runtime, got %#v", runtimeView.App)
	}
	if runtimeView.PocketBase == nil || runtimeView.PocketBase.ContainerName != "alces-alpha-app-pb" || !runtimeView.PocketBase.Running {
		t.Fatalf("expected running PocketBase runtime, got %#v", runtimeView.PocketBase)
	}
	if runtimeView.Network == nil || runtimeView.Network.Name != "alpha-app-net" {
		t.Fatalf("expected project network %q, got %#v", "alpha-app-net", runtimeView.Network)
	}
}

func TestManagedProjectRuntimeServiceReturnsProjectNotFound(t *testing.T) {
	db := newTestDB(t)
	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{})

	_, err := service.GetRuntime(context.Background(), "missing-app")
	if err != errProjectNotFound {
		t.Fatalf("expected project not found error, got %v", err)
	}
}

func TestManagedProjectRuntimeServiceReadsCurrentRuntimeLogs(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
		logsByDeploymentID: map[string]string{
			"dep-alpha": "hello from app\n",
		},
	})

	logs, err := service.ReadRuntimeLogs(context.Background(), "alpha-app")
	if err != nil {
		t.Fatalf("expected runtime log read to succeed, got error: %v", err)
	}
	defer logs.Close()

	body, err := io.ReadAll(logs)
	if err != nil {
		t.Fatalf("expected runtime log stream to read, got error: %v", err)
	}

	if string(body) != "hello from app\n" {
		t.Fatalf("expected body %q, got %q", "hello from app\n", string(body))
	}
}

func TestManagedProjectRuntimeServiceStreamsCurrentRuntimeLogsWithFollow(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	var lastOptions runtimeLogOptions
	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
		logsByDeploymentID: map[string]string{
			"dep-alpha": "hello from app\n",
		},
		lastLogsOptions: &lastOptions,
	})

	logs, err := service.StreamRuntimeLogs(context.Background(), "alpha-app")
	if err != nil {
		t.Fatalf("expected runtime log stream open to succeed, got error: %v", err)
	}
	defer logs.Close()

	if !lastOptions.Follow {
		t.Fatalf("expected follow true, got %#v", lastOptions)
	}
}

func TestManagedProjectRuntimeServiceReturnsRuntimeNotFoundWithoutCurrentDeployment(t *testing.T) {
	db := newTestDB(t)
	seedProjectRecord(t, db, "alpha-app")
	service := newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{})

	_, err := service.ReadRuntimeLogs(context.Background(), "alpha-app")
	if !errors.Is(err, errProjectRuntimeNotFound) {
		t.Fatalf("expected runtime not found error, got %v", err)
	}
}

func TestGetProjectRuntimeReturnsRuntimeSummary(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	handler := newHandler(
		config{
			BrainAPIKey: "test-key",
			DataDir:     dataDir,
		},
		db,
		noopEnqueuer{},
		noopProjectCleaner{},
		newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
			appsByProject: map[string][]projectAppRuntime{
				"alpha-app": {
					{
						DeploymentID:            "dep-alpha",
						ProjectName:             "alpha-app",
						AppContainerName:        "alces-alpha-app-app-dep-alpha",
						ImageRef:                "alces-alpha-app:dep-alpha",
						NetworkName:             "alpha-app-net",
						PocketBaseContainerName: "alces-alpha-app-pb",
						Running:                 true,
					},
				},
			},
			pocketBaseByProject: map[string]projectRuntimeContainer{
				"alpha-app": {
					ContainerName: "alces-alpha-app-pb",
					Running:       true,
				},
			},
			networkByProject: map[string]projectRuntimeNetwork{
				"alpha-app": {
					Name: "alpha-app-net",
				},
			},
		}),
	)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	assertJSONContains(t, recorder.Body.String(), `"projectName":"alpha-app"`)
	assertJSONContains(t, recorder.Body.String(), `"currentDeploymentId":"dep-alpha"`)
	assertJSONContains(t, recorder.Body.String(), `"containerName":"alces-alpha-app-app-dep-alpha"`)
	assertJSONContains(t, recorder.Body.String(), `"pocketBase":{"containerName":"alces-alpha-app-pb","running":true}`)
	assertJSONContains(t, recorder.Body.String(), `"network":{"name":"alpha-app-net"}`)
}

func TestGetProjectRuntimeLogsReturnsPlainTextLogs(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	handler := newHandler(
		config{
			BrainAPIKey: "test-key",
			DataDir:     dataDir,
		},
		db,
		noopEnqueuer{},
		noopProjectCleaner{},
		newManagedProjectRuntimeService(db, fakeProjectRuntimeReader{
			logsByDeploymentID: map[string]string{
				"dep-alpha": "hello from app\n",
			},
		}),
	)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertTextResponse(t, recorder, http.StatusOK, "hello from app\n")
}

func TestGetProjectRuntimeLogsStreamReturnsSSELogs(t *testing.T) {
	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-alpha",
		ProjectName:             "alpha-app",
		ImageRef:                "alces-alpha-app:dep-alpha",
		AppContainerName:        "alces-alpha-app-app-dep-alpha",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-10T00:00:00Z",
	})

	handler := newHandler(
		config{
			BrainAPIKey: "test-key",
			DataDir:     dataDir,
		},
		db,
		noopEnqueuer{},
		noopProjectCleaner{},
		fakeProjectRuntimeService{
			streamLogs: "first line\nsecond line\n",
		},
	)

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertSSEResponse(t, recorder, http.StatusOK, "data: first line\n\ndata: second line\n\n")
}

func TestGetProjectRuntimeLogsStreamReturnsInternalServerErrorWhenWriterCannotFlush(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{
		streamReader: newScriptedReadCloser([]scriptedRead{{data: "hello\n", err: io.EOF}}),
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "alpha-app")
	writer := newNonFlushingResponseWriter()

	handler.ServeHTTP(writer, request)

	assertGenericAPIError(t, writer.status, writer.header, writer.body.String(), http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
}

func TestGetProjectRuntimeLogsStreamWritesSSEErrorEventAfterReadFailure(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{
		streamReader: newScriptedReadCloser([]scriptedRead{
			{data: "first line\n"},
			{err: errors.New("boom")},
		}),
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertSSEResponse(t, recorder, http.StatusOK, "data: first line\n\nevent: error\ndata: failed to fetch runtime logs\n\n")
}

func TestGetProjectRuntimeLogsStreamBuffersChunkedReadsIntoCompleteLines(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{
		streamReader: newScriptedReadCloser([]scriptedRead{
			{data: "first "},
			{data: "line\nsecond"},
			{data: " line", err: io.EOF},
		}),
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertSSEResponse(t, recorder, http.StatusOK, "data: first line\n\ndata: second line\n\n")
}

func TestGetProjectRuntimeRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProjectRuntime(noopProjectRuntimeService{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/runtime", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectRuntimeLogsRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProjectRuntimeLogs(noopProjectRuntimeService{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/runtime/logs", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectRuntimeLogsStreamRejectsInvalidProjectName(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(noopProjectRuntimeService{})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "Demo App")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
}

func TestGetProjectRuntimeReturnsNotFoundForUnknownProject(t *testing.T) {
	handler := handleGetProjectRuntime(fakeProjectRuntimeService{err: errProjectNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/runtime", nil)
	recorder := httptest.NewRecorder()
	request.SetPathValue("projectName", "missing-app")

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestGetProjectRuntimeLogsReturnsNotFoundForUnknownProject(t *testing.T) {
	handler := handleGetProjectRuntimeLogs(fakeProjectRuntimeService{logsErr: errProjectNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/runtime/logs", nil)
	request.SetPathValue("projectName", "missing-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestGetProjectRuntimeLogsReturnsNotFoundWhenProjectHasNoCurrentRuntime(t *testing.T) {
	handler := handleGetProjectRuntimeLogs(fakeProjectRuntimeService{logsErr: errProjectRuntimeNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectRuntimeNotFound, "project runtime not found")
}

func TestGetProjectRuntimeLogsStreamReturnsNotFoundForUnknownProject(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{streamLogsErr: errProjectNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/missing-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "missing-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
}

func TestGetProjectRuntimeLogsStreamReturnsNotFoundWhenProjectHasNoCurrentRuntime(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{streamLogsErr: errProjectRuntimeNotFound})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusNotFound, errorCodeProjectRuntimeNotFound, "project runtime not found")
}

func TestGetProjectRuntimeLogsReturnsInternalServerErrorOnReadFailure(t *testing.T) {
	handler := handleGetProjectRuntimeLogs(fakeProjectRuntimeService{logsErr: errors.New("boom")})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
}

func TestGetProjectRuntimeLogsStreamReturnsInternalServerErrorOnReadFailure(t *testing.T) {
	handler := handleGetProjectRuntimeLogsStream(fakeProjectRuntimeService{streamLogsErr: errors.New("boom")})

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/alpha-app/runtime/logs/stream", nil)
	request.SetPathValue("projectName", "alpha-app")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
}

type fakeProjectRuntimeReader struct {
	appsByProject       map[string][]projectAppRuntime
	pocketBaseByProject map[string]projectRuntimeContainer
	networkByProject    map[string]projectRuntimeNetwork
	logsByDeploymentID  map[string]string
	lastLogsOptions     *runtimeLogOptions
	listErr             error
	pocketBaseErr       error
	networkErr          error
	logsErr             error
}

func (runtime fakeProjectRuntimeReader) ListProjectApps(_ context.Context, projectName string) ([]projectAppRuntime, error) {
	if runtime.listErr != nil {
		return nil, runtime.listErr
	}

	return append([]projectAppRuntime(nil), runtime.appsByProject[projectName]...), nil
}

func (runtime fakeProjectRuntimeReader) GetProjectPocketBaseRuntime(_ context.Context, projectName string) (projectRuntimeContainer, bool, error) {
	if runtime.pocketBaseErr != nil {
		return projectRuntimeContainer{}, false, runtime.pocketBaseErr
	}

	pocketBase, ok := runtime.pocketBaseByProject[projectName]
	return pocketBase, ok, nil
}

func (runtime fakeProjectRuntimeReader) GetProjectNetworkRuntime(_ context.Context, projectName string) (projectRuntimeNetwork, bool, error) {
	if runtime.networkErr != nil {
		return projectRuntimeNetwork{}, false, runtime.networkErr
	}

	network, ok := runtime.networkByProject[projectName]
	return network, ok, nil
}

func (runtime fakeProjectRuntimeReader) ReadProjectAppLogs(_ context.Context, deployment deploymentRecord, options runtimeLogOptions) (io.ReadCloser, error) {
	if runtime.logsErr != nil {
		return nil, runtime.logsErr
	}

	if runtime.lastLogsOptions != nil {
		*runtime.lastLogsOptions = options
	}

	return io.NopCloser(strings.NewReader(runtime.logsByDeploymentID[deployment.ID])), nil
}

type fakeProjectRuntimeService struct {
	runtimeView   projectRuntimeView
	err           error
	logs          string
	logsErr       error
	streamLogs    string
	streamLogsErr error
	streamReader  io.ReadCloser
}

func (service fakeProjectRuntimeService) GetRuntime(context.Context, string) (projectRuntimeView, error) {
	return service.runtimeView, service.err
}

func (service fakeProjectRuntimeService) ReadRuntimeLogs(context.Context, string) (io.ReadCloser, error) {
	if service.logsErr != nil {
		return nil, service.logsErr
	}

	return io.NopCloser(bytes.NewBufferString(service.logs)), nil
}

func (service fakeProjectRuntimeService) StreamRuntimeLogs(context.Context, string) (io.ReadCloser, error) {
	if service.streamLogsErr != nil {
		return nil, service.streamLogsErr
	}
	if service.streamReader != nil {
		return service.streamReader, nil
	}

	return io.NopCloser(bytes.NewBufferString(service.streamLogs)), nil
}

func assertJSONContains(t *testing.T, body string, want string) {
	t.Helper()

	if !strings.Contains(body, want) {
		t.Fatalf("expected body to contain %q, got %q", want, body)
	}
}

type nonFlushingResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newNonFlushingResponseWriter() *nonFlushingResponseWriter {
	return &nonFlushingResponseWriter{
		header: make(http.Header),
	}
}

func (writer *nonFlushingResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *nonFlushingResponseWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}

	return writer.body.Write(data)
}

func (writer *nonFlushingResponseWriter) WriteHeader(statusCode int) {
	writer.status = statusCode
}

func assertGenericAPIError(t *testing.T, gotStatus int, headers http.Header, body string, wantStatus int, wantCode string, wantMessage string) {
	t.Helper()

	if gotStatus != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, gotStatus)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected Content-Type %q, got %q", "application/json", got)
	}

	var response apiError
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("expected error response body to decode, got error: %v", err)
	}
	if response.Code != wantCode {
		t.Fatalf("expected error code %q, got %q", wantCode, response.Code)
	}
	if response.Message != wantMessage {
		t.Fatalf("expected error message %q, got %q", wantMessage, response.Message)
	}
}
