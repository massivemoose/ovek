package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestBootstrapAuthCreatesFirstAdminAndAPIKey(t *testing.T) {
	handler, db := newProdTestHandler(t)

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/bootstrap", strings.NewReader(`{"username":"admin","password":"secret-pass"}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, recorder.Code)
	}

	var response brainapi.BootstrapAuthResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("expected bootstrap response to decode, got error: %v", err)
	}
	if response.Username != "admin" || response.APIKey == "" {
		t.Fatalf("expected bootstrap response to include username and api key, got %#v", response)
	}

	if count := queryCount(t, db, "SELECT COUNT(1) FROM admin_users"); count != 1 {
		t.Fatalf("expected 1 admin user, got %d", count)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM api_keys"); count != 1 {
		t.Fatalf("expected 1 api key, got %d", count)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.bootstrap_completed'"); count != 1 {
		t.Fatalf("expected bootstrap audit log, got %d rows", count)
	}
}

func TestBootstrapAuthIsDisabledAfterFirstAdminExists(t *testing.T) {
	handler, db := newProdTestHandler(t)
	if _, err := bootstrapAdminUser(db, "admin", "secret-pass"); err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/bootstrap", strings.NewReader(`{"username":"second","password":"secret-pass"}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusForbidden, errorCodeAuthBootstrapDisabled, "auth bootstrap disabled")
}

func TestProdPingRequiresDBBackedAPIKey(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "pong" {
		t.Fatalf("expected prod ping to succeed, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestProdDeployRequiresReauth(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/demo.git"}`),
	)
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestProdRunRequiresReauth(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/runs",
		strings.NewReader(`{"capsuleRef":"ghcr.io/example/demo:2026.05.01"}`),
	)
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestProdReauthAllowsCriticalDeployAndWritesAuditLog(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	reauthRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/reauth", strings.NewReader(`{"password":"secret-pass"}`))
	reauthRequest.Header.Set(headerAPIKey, apiKey)
	reauthRecorder := httptest.NewRecorder()

	handler.ServeHTTP(reauthRecorder, reauthRequest)

	if reauthRecorder.Code != http.StatusOK {
		t.Fatalf("expected reauth status %d, got %d", http.StatusOK, reauthRecorder.Code)
	}

	var reauthResponse brainapi.ReauthResponse
	if err := json.NewDecoder(reauthRecorder.Body).Decode(&reauthResponse); err != nil {
		t.Fatalf("expected reauth response to decode, got error: %v", err)
	}
	if reauthResponse.ReauthToken == "" {
		t.Fatal("expected reauth token to be returned")
	}

	deployRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/demo.git"}`),
	)
	deployRequest.Header.Set(headerAPIKey, apiKey)
	deployRequest.Header.Set(headerReauthToken, reauthResponse.ReauthToken)
	deployRecorder := httptest.NewRecorder()

	handler.ServeHTTP(deployRecorder, deployRequest)

	if deployRecorder.Code != http.StatusAccepted {
		t.Fatalf("expected deploy status %d, got %d", http.StatusAccepted, deployRecorder.Code)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.reauth_succeeded'"); count != 1 {
		t.Fatalf("expected reauth audit log, got %d rows", count)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'deploy.authorized' AND project_name = 'demo-app'"); count != 1 {
		t.Fatalf("expected deploy audit log, got %d rows", count)
	}
}

func TestProdReauthAllowsCriticalRunAndWritesAuditLog(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	reauthRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/reauth", strings.NewReader(`{"password":"secret-pass"}`))
	reauthRequest.Header.Set(headerAPIKey, apiKey)
	reauthRecorder := httptest.NewRecorder()

	handler.ServeHTTP(reauthRecorder, reauthRequest)

	if reauthRecorder.Code != http.StatusOK {
		t.Fatalf("expected reauth status %d, got %d", http.StatusOK, reauthRecorder.Code)
	}

	var reauthResponse brainapi.ReauthResponse
	if err := json.NewDecoder(reauthRecorder.Body).Decode(&reauthResponse); err != nil {
		t.Fatalf("expected reauth response to decode, got error: %v", err)
	}
	if reauthResponse.ReauthToken == "" {
		t.Fatal("expected reauth token to be returned")
	}

	runRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/runs",
		strings.NewReader(`{"capsuleRef":"ghcr.io/example/demo:2026.05.01"}`),
	)
	runRequest.Header.Set(headerAPIKey, apiKey)
	runRequest.Header.Set(headerReauthToken, reauthResponse.ReauthToken)
	runRecorder := httptest.NewRecorder()

	handler.ServeHTTP(runRecorder, runRequest)

	if runRecorder.Code != http.StatusAccepted {
		t.Fatalf("expected run status %d, got %d", http.StatusAccepted, runRecorder.Code)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'run.authorized' AND project_name = 'demo-app'"); count != 1 {
		t.Fatalf("expected run audit log, got %d rows", count)
	}
}

func TestReauthTokenIsBoundToSpecificAPIKey(t *testing.T) {
	handler, db := newProdTestHandler(t)
	firstAPIKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	principal, err := validateAPIKey(context.Background(), db, firstAPIKey)
	if err != nil {
		t.Fatalf("expected first api key validation to succeed, got error: %v", err)
	}
	secondAPIKey, _, err := createAPIKey(context.Background(), db, principal.UserID, "second")
	if err != nil {
		t.Fatalf("expected second api key creation to succeed, got error: %v", err)
	}

	reauthRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/reauth", strings.NewReader(`{"password":"secret-pass"}`))
	reauthRequest.Header.Set(headerAPIKey, firstAPIKey)
	reauthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(reauthRecorder, reauthRequest)

	var reauthResponse brainapi.ReauthResponse
	if err := json.NewDecoder(reauthRecorder.Body).Decode(&reauthResponse); err != nil {
		t.Fatalf("expected reauth response to decode, got error: %v", err)
	}

	deployRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/projects/demo-app/deployments",
		strings.NewReader(`{"repoUrl":"https://example.com/demo.git"}`),
	)
	deployRequest.Header.Set(headerAPIKey, secondAPIKey)
	deployRequest.Header.Set(headerReauthToken, reauthResponse.ReauthToken)
	deployRecorder := httptest.NewRecorder()

	handler.ServeHTTP(deployRecorder, deployRequest)

	assertAPIError(t, deployRecorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestAPIKeyLifecycleRequiresReauthAndRedactsSecrets(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys", strings.NewReader(`{"label":"ci"}`))
	createRequest.Header.Set(headerAPIKey, apiKey)
	createRecorder := httptest.NewRecorder()

	handler.ServeHTTP(createRecorder, createRequest)

	assertAPIError(t, createRecorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")

	reauthToken, _, err := issueReauthToken(context.Background(), db, mustValidateAPIKey(t, db, apiKey), "secret-pass")
	if err != nil {
		t.Fatalf("expected reauth token to be issued, got error: %v", err)
	}

	createRequest = httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys", strings.NewReader(`{"label":"ci"}`))
	createRequest.Header.Set(headerAPIKey, apiKey)
	createRequest.Header.Set(headerReauthToken, reauthToken)
	createRecorder = httptest.NewRecorder()

	handler.ServeHTTP(createRecorder, createRequest)

	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d with body %q", http.StatusCreated, createRecorder.Code, createRecorder.Body.String())
	}
	var createResponse brainapi.CreateAPIKeyResponse
	if err := json.NewDecoder(createRecorder.Body).Decode(&createResponse); err != nil {
		t.Fatalf("expected create API key response to decode, got error: %v", err)
	}
	if createResponse.APIKey == "" || createResponse.ID == "" || createResponse.Label != "ci" {
		t.Fatalf("expected create response with one-time api key, got %#v", createResponse)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/auth/api-keys", nil)
	listRequest.Header.Set(headerAPIKey, apiKey)
	listRecorder := httptest.NewRecorder()

	handler.ServeHTTP(listRecorder, listRequest)

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %q", http.StatusOK, listRecorder.Code, listRecorder.Body.String())
	}
	if strings.Contains(listRecorder.Body.String(), createResponse.APIKey) {
		t.Fatalf("expected list response to redact api key secret, got %q", listRecorder.Body.String())
	}
	var summaries []brainapi.APIKeySummary
	if err := json.NewDecoder(listRecorder.Body).Decode(&summaries); err != nil {
		t.Fatalf("expected list response to decode, got error: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected bootstrap and ci keys, got %#v", summaries)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/auth/api-keys/"+createResponse.ID, nil)
	deleteRequest.Header.Set(headerAPIKey, apiKey)
	deleteRequest.Header.Set(headerReauthToken, reauthToken)
	deleteRecorder := httptest.NewRecorder()

	handler.ServeHTTP(deleteRecorder, deleteRequest)

	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected delete status %d, got %d with body %q", http.StatusNoContent, deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if _, err := validateAPIKey(context.Background(), db, createResponse.APIKey); !errors.Is(err, errInvalidAPIKey) {
		t.Fatalf("expected revoked api key to stop validating, got %v", err)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.api_key_revoked'"); count != 1 {
		t.Fatalf("expected api key revoke audit log, got %d rows", count)
	}
}

func TestPasswordChangeRequiresReauthAndCurrentPassword(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	changeRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/password", strings.NewReader(`{"currentPassword":"secret-pass","newPassword":"new-secret-pass"}`))
	changeRequest.Header.Set(headerAPIKey, apiKey)
	changeRecorder := httptest.NewRecorder()

	handler.ServeHTTP(changeRecorder, changeRequest)

	assertAPIError(t, changeRecorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")

	reauthToken, _, err := issueReauthToken(context.Background(), db, mustValidateAPIKey(t, db, apiKey), "secret-pass")
	if err != nil {
		t.Fatalf("expected reauth token to be issued, got error: %v", err)
	}

	wrongCurrentRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/password", strings.NewReader(`{"currentPassword":"wrong-pass","newPassword":"new-secret-pass"}`))
	wrongCurrentRequest.Header.Set(headerAPIKey, apiKey)
	wrongCurrentRequest.Header.Set(headerReauthToken, reauthToken)
	wrongCurrentRecorder := httptest.NewRecorder()

	handler.ServeHTTP(wrongCurrentRecorder, wrongCurrentRequest)

	assertAPIError(t, wrongCurrentRecorder, http.StatusUnauthorized, errorCodeAuthPasswordFailed, "password change failed")

	changeRequest = httptest.NewRequest(http.MethodPost, "/v1/auth/password", strings.NewReader(`{"currentPassword":"secret-pass","newPassword":"new-secret-pass"}`))
	changeRequest.Header.Set(headerAPIKey, apiKey)
	changeRequest.Header.Set(headerReauthToken, reauthToken)
	changeRecorder = httptest.NewRecorder()

	handler.ServeHTTP(changeRecorder, changeRequest)

	if changeRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected password change status %d, got %d with body %q", http.StatusNoContent, changeRecorder.Code, changeRecorder.Body.String())
	}
	if _, _, err := issueReauthToken(context.Background(), db, mustValidateAPIKey(t, db, apiKey), "secret-pass"); !errors.Is(err, errInvalidPassword) {
		t.Fatalf("expected old password to stop working, got %v", err)
	}
	if _, _, err := issueReauthToken(context.Background(), db, mustValidateAPIKey(t, db, apiKey), "new-secret-pass"); err != nil {
		t.Fatalf("expected new password to work, got error: %v", err)
	}
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.password_changed'"); count != 1 {
		t.Fatalf("expected password change audit log, got %d rows", count)
	}
}

func TestProdAuthFailuresAreAuditedByReason(t *testing.T) {
	handler, db := newProdTestHandler(t)

	missingRequest := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	missingRecorder := httptest.NewRecorder()

	handler.ServeHTTP(missingRecorder, missingRequest)

	assertAPIError(t, missingRecorder, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.api_key_rejected' AND details_json LIKE '%missing%'"); count != 1 {
		t.Fatalf("expected missing api key audit log, got %d rows", count)
	}

	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}
	principal := mustValidateAPIKey(t, db, apiKey)
	if _, err := db.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ?`, "2026-05-09T00:00:00Z", principal.APIKeyID); err != nil {
		t.Fatalf("expected api key revoke fixture update to succeed, got error: %v", err)
	}

	revokedRequest := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	revokedRequest.Header.Set(headerAPIKey, apiKey)
	revokedRecorder := httptest.NewRecorder()

	handler.ServeHTTP(revokedRecorder, revokedRequest)

	assertAPIError(t, revokedRecorder, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
	if count := queryCount(t, db, "SELECT COUNT(1) FROM audit_logs WHERE event_type = 'auth.api_key_rejected' AND details_json LIKE '%revoked%'"); count != 1 {
		t.Fatalf("expected revoked api key audit log, got %d rows", count)
	}
}

func newProdTestHandler(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()

	dataDir := t.TempDir()
	db, err := openBrainDB(dataDir)
	if err != nil {
		t.Fatalf("expected test database to open, got error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	cipherBox, err := loadSecretCipher(dataDir)
	if err != nil {
		t.Fatalf("expected test secret cipher to load, got error: %v", err)
	}
	handler := newHandlerWithRegistryStore(config{
		AuthMode: authModeProd,
		DataDir:  dataDir,
	}, db, noopEnqueuer{}, noopProjectCleaner{}, noopProjectRuntimeService{}, managedProjectPocketBaseService{}, newRegistryCredentialStore(db, cipherBox))

	return handler, db
}

func queryCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("expected query %q to succeed, got error: %v", query, err)
	}

	return count
}
