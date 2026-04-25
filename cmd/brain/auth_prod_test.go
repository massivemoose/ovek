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

	handler := newHandler(config{
		AuthMode: authModeProd,
		DataDir:  dataDir,
	}, db, noopEnqueuer{}, noopProjectCleaner{}, noopProjectRuntimeService{})

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
