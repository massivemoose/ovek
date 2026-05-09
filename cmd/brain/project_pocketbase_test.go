package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestProjectPocketBaseInitCreatesCredentialAndAppSecrets(t *testing.T) {
	db := newTestDB(t)
	service, credentialStore, configStore, runtime := newTestProjectPocketBaseService(t, db)

	status, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{AppSecrets: true}, "dev")
	if err != nil {
		t.Fatalf("expected PocketBase init to succeed, got error: %v", err)
	}

	if !status.Initialized || !status.Running || status.SuperuserEmail == nil || *status.SuperuserEmail != "admin@demo-app.ovek.local" {
		t.Fatalf("expected initialized running status with default email, got %#v", status)
	}
	if !status.AppSecretsConfigured || status.AppSecretsRevisionID == "" {
		t.Fatalf("expected app secrets revision to be reported, got %#v", status)
	}
	if runtime.ensureProjectName != "demo-app" || runtime.ensureImage != defaultPocketBaseImage || runtime.ensureDataDir != "/srv/ovek/projects" {
		t.Fatalf("expected PocketBase sidecar provisioning, got runtime %#v", runtime)
	}
	if len(runtime.upserts) != 1 || runtime.upserts[0].email != "admin@demo-app.ovek.local" || runtime.upserts[0].password == "" {
		t.Fatalf("expected one superuser upsert, got %#v", runtime.upserts)
	}

	var encryptedPassword string
	if err := db.QueryRow(`SELECT encrypted_password FROM project_pocketbase_credentials WHERE project_name = ?`, "demo-app").Scan(&encryptedPassword); err != nil {
		t.Fatalf("expected stored credential, got error: %v", err)
	}
	if encryptedPassword == runtime.upserts[0].password || !strings.HasPrefix(encryptedPassword, secretCipherPrefix) {
		t.Fatalf("expected encrypted password at rest, got %q", encryptedPassword)
	}
	credential, found, err := credentialStore.Get(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected credential decrypt to succeed, got error: %v", err)
	}
	if !found || credential.Password != runtime.upserts[0].password {
		t.Fatalf("expected stored credential to decrypt to runtime password, got found=%v credential=%#v", found, credential)
	}

	entries, err := configStore.ListEnvironment(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected project environment list to succeed, got error: %v", err)
	}
	assertPocketBaseAppSecretsConfigured(t, entries, "admin@demo-app.ovek.local")
	if count := queryCount(t, db, `SELECT COUNT(1) FROM project_config_revisions WHERE project_name = ?`, "demo-app"); count != 1 {
		t.Fatalf("expected one app secrets revision, got %d", count)
	}
}

func TestProjectPocketBaseInitIsIdempotentAndReusesCredentials(t *testing.T) {
	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)

	firstStatus, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err != nil {
		t.Fatalf("expected first PocketBase init to succeed, got error: %v", err)
	}
	firstPassword := runtime.upserts[0].password
	secondStatus, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err != nil {
		t.Fatalf("expected second PocketBase init to succeed, got error: %v", err)
	}

	if firstStatus.SuperuserEmail == nil || secondStatus.SuperuserEmail == nil || *firstStatus.SuperuserEmail != *secondStatus.SuperuserEmail {
		t.Fatalf("expected repeated init to reuse email, got first=%#v second=%#v", firstStatus, secondStatus)
	}
	if len(runtime.upserts) != 2 || runtime.upserts[1].password != firstPassword {
		t.Fatalf("expected repeated init to reuse password, got %#v", runtime.upserts)
	}
	if count := queryCount(t, db, `SELECT COUNT(1) FROM project_pocketbase_credentials WHERE project_name = ?`, "demo-app"); count != 1 {
		t.Fatalf("expected one stored credential, got %d", count)
	}
}

func TestProjectPocketBaseInitRejectsEmailChangeAfterInitialization(t *testing.T) {
	db := newTestDB(t)
	service, _, _, _ := newTestProjectPocketBaseService(t, db)

	if _, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{Email: "admin@example.com"}, "dev"); err != nil {
		t.Fatalf("expected first PocketBase init to succeed, got error: %v", err)
	}
	_, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{Email: "other@example.com"}, "dev")
	if !errors.Is(err, errProjectPocketBaseAlreadyInitialized) {
		t.Fatalf("expected email change conflict, got %v", err)
	}
}

func TestProjectPocketBaseInitDeletesNewCredentialWhenUpsertFails(t *testing.T) {
	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)
	runtime.upsertErr = errors.New("upsert failed")

	_, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err == nil {
		t.Fatal("expected PocketBase init to fail")
	}
	if count := queryCount(t, db, `SELECT COUNT(1) FROM project_pocketbase_credentials WHERE project_name = ?`, "demo-app"); count != 0 {
		t.Fatalf("expected failed new credential to be deleted, got %d rows", count)
	}
}

func TestProjectPocketBaseInitWaitsForRuntimeReadyBeforeCredentialUpsert(t *testing.T) {
	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)
	runtime.waitReadyErr = errors.New("not ready yet")

	_, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err == nil || !strings.Contains(err.Error(), "wait for PocketBase readiness") {
		t.Fatalf("expected readiness failure, got %v", err)
	}
	if runtime.waitReadyProjectName != "demo-app" {
		t.Fatalf("expected readiness wait for project, got %q", runtime.waitReadyProjectName)
	}
	if len(runtime.upserts) != 0 {
		t.Fatalf("expected no superuser upsert before readiness, got %#v", runtime.upserts)
	}
	if count := queryCount(t, db, `SELECT COUNT(1) FROM project_pocketbase_credentials WHERE project_name = ?`, "demo-app"); count != 0 {
		t.Fatalf("expected no stored credential before readiness, got %d rows", count)
	}
}

func TestProjectPocketBaseInitRetriesTransientLockedDatabaseUpsert(t *testing.T) {
	originalSleep := sleepForPocketBaseUpsertRetry
	sleepForPocketBaseUpsertRetry = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() {
		sleepForPocketBaseUpsertRetry = originalSleep
	})

	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)
	runtime.upsertErrs = []error{
		errors.New("PocketBase superuser command failed: 2026/05/06 database is locked (5) (SQLITE_BUSY)"),
		nil,
	}

	status, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err != nil {
		t.Fatalf("expected transient locked database upsert to retry successfully, got error: %v", err)
	}
	if !status.Initialized {
		t.Fatalf("expected initialized status, got %#v", status)
	}
	if len(runtime.upserts) != 2 {
		t.Fatalf("expected two upsert attempts, got %#v", runtime.upserts)
	}
}

func TestProjectPocketBaseInitRetriesStartupKilledUpsert(t *testing.T) {
	originalSleep := sleepForPocketBaseUpsertRetry
	sleepForPocketBaseUpsertRetry = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() {
		sleepForPocketBaseUpsertRetry = originalSleep
	})

	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)
	runtime.upsertErrs = []error{
		errors.New("PocketBase superuser command failed: exit code 137"),
		nil,
	}

	status, err := service.Init(context.Background(), "demo-app", brainapi.InitProjectPocketBaseRequest{}, "dev")
	if err != nil {
		t.Fatalf("expected startup-killed superuser upsert to retry successfully, got error: %v", err)
	}
	if !status.Initialized {
		t.Fatalf("expected initialized status, got %#v", status)
	}
	if len(runtime.upserts) != 2 {
		t.Fatalf("expected two upsert attempts, got %#v", runtime.upserts)
	}
}

func TestProjectPocketBaseAPIRequiresReauthInProdMode(t *testing.T) {
	db := newTestDB(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got error: %v", err)
	}
	service, _, _, _ := newTestProjectPocketBaseService(t, db)
	handler := newHandler(config{AuthMode: authModeProd}, db, nil, nil, nil, service)

	request := httptest.NewRequest(http.MethodPost, "/v1/projects/demo-app/pocketbase/init", strings.NewReader(`{"appSecrets":true}`))
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestProjectPocketBaseAPINeverReturnsPassword(t *testing.T) {
	db := newTestDB(t)
	service, _, _, runtime := newTestProjectPocketBaseService(t, db)
	handler := newHandler(config{AuthMode: authModeDev, BrainAPIKey: "test-key"}, db, nil, nil, nil, service)

	request := httptest.NewRequest(http.MethodPost, "/v1/projects/demo-app/pocketbase/init", strings.NewReader(`{"appSecrets":true}`))
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected init status %d, got %d with body %q", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), runtime.upserts[0].password) {
		t.Fatalf("expected init response not to leak password, got %q", recorder.Body.String())
	}

	var status brainapi.ProjectPocketBaseStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("expected init response decode to succeed, got error: %v", err)
	}
	if status.SuperuserEmail == nil || *status.SuperuserEmail != "admin@demo-app.ovek.local" {
		t.Fatalf("expected superuser email in response, got %#v", status)
	}
}

func TestProjectPocketBaseProxyForwardsAuthenticatedRequestToPocketBase(t *testing.T) {
	db := newTestDB(t)
	runtime := &fakeProjectPocketBaseRuntime{proxyTarget: ""}
	service, _, _, _ := newTestProjectPocketBaseServiceWithRuntime(t, db, runtime)
	pocketBaseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/collections?perPage=1" {
			t.Fatalf("expected proxied target path and query, got %q", r.URL.RequestURI())
		}
		if got := r.Header.Get(headerAPIKey); got != "" {
			t.Fatalf("expected API key header to be stripped, got %q", got)
		}
		w.Header().Set("X-Pocketbase-Test", "yes")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer pocketBaseServer.Close()
	runtime.proxyTarget = pocketBaseServer.URL

	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/pocketbase/proxy?target=/api/collections%3FperPage%3D1", nil)
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()

	if err := service.Proxy(recorder, request, "demo-app"); err != nil {
		t.Fatalf("expected proxy to succeed, got error: %v", err)
	}
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"ok":true}` {
		t.Fatalf("expected proxied response, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Pocketbase-Test") != "yes" {
		t.Fatalf("expected proxy response headers to be copied, got %#v", recorder.Header())
	}
}

func TestProjectPocketBaseProxyRejectsInvalidTarget(t *testing.T) {
	db := newTestDB(t)
	service, _, _, _ := newTestProjectPocketBaseService(t, db)
	request := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/pocketbase/proxy?target=//evil.example", nil)
	recorder := httptest.NewRecorder()

	err := service.Proxy(recorder, request, "demo-app")
	if err == nil || !strings.Contains(err.Error(), "target must be an absolute path") {
		t.Fatalf("expected invalid target error, got %v", err)
	}
}

func newTestProjectPocketBaseService(t *testing.T, db *sql.DB) (managedProjectPocketBaseService, projectPocketBaseStore, projectConfigStore, *fakeProjectPocketBaseRuntime) {
	t.Helper()

	runtime := &fakeProjectPocketBaseRuntime{}
	service, credentialStore, configStore, _ := newTestProjectPocketBaseServiceWithRuntime(t, db, runtime)
	return service, credentialStore, configStore, runtime
}

func newTestProjectPocketBaseServiceWithRuntime(t *testing.T, db *sql.DB, runtime *fakeProjectPocketBaseRuntime) (managedProjectPocketBaseService, projectPocketBaseStore, projectConfigStore, *fakeProjectPocketBaseRuntime) {
	t.Helper()

	cipherBox := secretCipher{key: []byte(strings.Repeat("p", secretKeyLength))}
	credentialStore := newProjectPocketBaseStore(db, cipherBox)
	configStore := newProjectConfigStore(db, cipherBox)
	service := newManagedProjectPocketBaseService(db, runtime, "/srv/ovek/projects", defaultPocketBaseImage, credentialStore, configStore)
	return service, credentialStore, configStore, runtime
}

func assertPocketBaseAppSecretsConfigured(t *testing.T, entries []brainapi.ProjectEnvironmentEntry, wantEmail string) {
	t.Helper()

	foundEmail := false
	foundPassword := false
	for _, entry := range entries {
		switch entry.Name {
		case projectPocketBaseSuperuserEmailEnv:
			foundEmail = true
			if entry.Secret || entry.Value == nil || *entry.Value != wantEmail {
				t.Fatalf("expected plain PocketBase email entry, got %#v", entry)
			}
		case projectPocketBaseSuperuserPasswordEnv:
			foundPassword = true
			if !entry.Secret || entry.Value != nil {
				t.Fatalf("expected masked PocketBase password secret entry, got %#v", entry)
			}
		}
	}
	if !foundEmail || !foundPassword {
		t.Fatalf("expected PocketBase app secret entries, got %#v", entries)
	}
}

type fakeProjectPocketBaseRuntime struct {
	ensureProjectName    string
	ensureImage          string
	ensureDataDir        string
	ensureErr            error
	waitReadyProjectName string
	waitReadyErr         error
	upserts              []fakePocketBaseUpsert
	upsertErr            error
	upsertErrs           []error
	proxyTarget          string
	proxyErr             error
	runtimeFound         bool
	runtimeView          projectRuntimeContainer
	runtimeErr           error
}

type fakePocketBaseUpsert struct {
	email    string
	password string
}

func (runtime *fakeProjectPocketBaseRuntime) EnsureProjectPocketBase(_ context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	runtime.ensureProjectName = projectName
	runtime.ensureImage = image
	runtime.ensureDataDir = projectsHostDataDir
	if runtime.ensureErr != nil {
		return "", runtime.ensureErr
	}
	runtime.runtimeFound = true
	runtime.runtimeView = projectRuntimeContainer{
		ContainerName: pocketBaseContainerName(projectName),
		Running:       true,
	}
	return "pb-container-123", nil
}

func (runtime *fakeProjectPocketBaseRuntime) WaitForProjectPocketBaseReady(_ context.Context, projectName string) error {
	runtime.waitReadyProjectName = projectName
	return runtime.waitReadyErr
}

func (runtime *fakeProjectPocketBaseRuntime) UpsertProjectPocketBaseSuperuser(_ context.Context, _ string, email string, password string) error {
	runtime.upserts = append(runtime.upserts, fakePocketBaseUpsert{email: email, password: password})
	if len(runtime.upsertErrs) > 0 {
		err := runtime.upsertErrs[0]
		runtime.upsertErrs = runtime.upsertErrs[1:]
		return err
	}
	return runtime.upsertErr
}

func (runtime *fakeProjectPocketBaseRuntime) ProjectPocketBaseProxyTarget(_ context.Context, _ string) (string, error) {
	if runtime.proxyErr != nil {
		return "", runtime.proxyErr
	}
	return runtime.proxyTarget, nil
}

func (runtime *fakeProjectPocketBaseRuntime) GetProjectPocketBaseRuntime(_ context.Context, projectName string) (projectRuntimeContainer, bool, error) {
	if runtime.runtimeErr != nil {
		return projectRuntimeContainer{}, false, runtime.runtimeErr
	}
	if runtime.runtimeFound {
		return runtime.runtimeView, true, nil
	}
	return projectRuntimeContainer{ContainerName: pocketBaseContainerName(projectName)}, false, nil
}
