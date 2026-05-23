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

func TestRegistryCredentialStoreEncryptsAndRedactsCredentials(t *testing.T) {
	db := newTestDB(t)
	store := newRegistryCredentialStore(db, testSecretCipher())

	credential, err := store.Upsert(context.Background(), "GHCR.IO", "octo", "secret-token", "tester")
	if err != nil {
		t.Fatalf("expected credential upsert to succeed, got error: %v", err)
	}
	if credential.Host != "ghcr.io" {
		t.Fatalf("expected canonical host %q, got %q", "ghcr.io", credential.Host)
	}

	credentials, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("expected credential list to succeed, got error: %v", err)
	}
	if len(credentials) != 1 {
		t.Fatalf("expected one credential, got %d", len(credentials))
	}
	if credentials[0].Host != "ghcr.io" || credentials[0].Username != "octo" {
		t.Fatalf("expected redacted credential metadata, got %#v", credentials[0])
	}

	var encryptedPassword string
	if err := db.QueryRow(`SELECT encrypted_password FROM registry_credentials WHERE registry_host = ?`, "ghcr.io").Scan(&encryptedPassword); err != nil {
		t.Fatalf("expected stored credential row, got error: %v", err)
	}
	if strings.Contains(encryptedPassword, "secret-token") {
		t.Fatalf("expected password to be encrypted, got %q", encryptedPassword)
	}

	resolved, found, err := store.ResolveForImage(context.Background(), "ghcr.io/example/private:latest")
	if err != nil {
		t.Fatalf("expected credential resolve to succeed, got error: %v", err)
	}
	if !found {
		t.Fatal("expected credential to be found for ghcr.io image")
	}
	if resolved.Username != "octo" || resolved.Password != "secret-token" {
		t.Fatalf("expected decrypted credential, got %#v", resolved)
	}
}

func TestRegistryCredentialStoreRejectsInvalidHost(t *testing.T) {
	store := newRegistryCredentialStore(newTestDB(t), testSecretCipher())

	_, err := store.Upsert(context.Background(), "https://ghcr.io", "octo", "secret-token", "tester")
	if err == nil {
		t.Fatal("expected invalid registry host to fail")
	}
}

func TestRegistryCredentialAPIRequiresReauthForMutation(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPut,
		"/v1/registry/credentials/ghcr.io",
		strings.NewReader(`{"username":"octo","password":"secret-token"}`),
	)
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestRegistryCredentialAPIUpsertsAndListsRedactedCredentials(t *testing.T) {
	handler, db := newProdTestHandler(t)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap helper to succeed, got error: %v", err)
	}
	reauthToken, _, err := issueReauthToken(context.Background(), db, mustValidateAPIKey(t, db, apiKey), "secret-pass")
	if err != nil {
		t.Fatalf("expected reauth token to be issued, got error: %v", err)
	}

	upsertRequest := httptest.NewRequest(
		http.MethodPut,
		"/v1/registry/credentials/ghcr.io",
		strings.NewReader(`{"username":"octo","password":"secret-token"}`),
	)
	upsertRequest.Header.Set(headerAPIKey, apiKey)
	upsertRequest.Header.Set(headerReauthToken, reauthToken)
	upsertRecorder := httptest.NewRecorder()

	handler.ServeHTTP(upsertRecorder, upsertRequest)

	if upsertRecorder.Code != http.StatusOK {
		t.Fatalf("expected upsert status %d, got %d with body %q", http.StatusOK, upsertRecorder.Code, upsertRecorder.Body.String())
	}
	if strings.Contains(upsertRecorder.Body.String(), "secret-token") {
		t.Fatalf("expected upsert response to omit password, got %q", upsertRecorder.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/registry/credentials", nil)
	listRequest.Header.Set(headerAPIKey, apiKey)
	listRecorder := httptest.NewRecorder()

	handler.ServeHTTP(listRecorder, listRequest)

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d", http.StatusOK, listRecorder.Code)
	}
	if strings.Contains(listRecorder.Body.String(), "secret-token") {
		t.Fatalf("expected list response to omit password, got %q", listRecorder.Body.String())
	}
	var credentials []brainapi.RegistryCredential
	if err := json.NewDecoder(listRecorder.Body).Decode(&credentials); err != nil {
		t.Fatalf("expected list response to decode, got error: %v", err)
	}
	if len(credentials) != 1 || credentials[0].Host != "ghcr.io" || credentials[0].Username != "octo" {
		t.Fatalf("expected redacted credential metadata, got %#v", credentials)
	}
}

func testSecretCipher() secretCipher {
	return secretCipher{key: []byte("12345678901234567890123456789012")}
}

func mustValidateAPIKey(t *testing.T, db *sql.DB, apiKey string) authPrincipal {
	t.Helper()

	principal, err := validateAPIKey(context.Background(), db, apiKey)
	if err != nil {
		t.Fatalf("expected api key to validate, got error: %v", err)
	}
	return principal
}
