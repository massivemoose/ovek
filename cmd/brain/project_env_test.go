package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestProjectEnvironmentAPISetListAndDelete(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)
	handler := newHandler(config{AuthMode: authModeDev, BrainAPIKey: "test-key"}, db, nil, nil, nil, managedProjectPocketBaseService{}, store)

	setRequest := httptest.NewRequest(http.MethodPut, "/v1/projects/demo-app/env/PB_SUPERUSER_PASSWORD", strings.NewReader(`{"value":"secret-pass","secret":true}`))
	setRequest.Header.Set(headerAPIKey, "test-key")
	setRecorder := httptest.NewRecorder()
	handler.ServeHTTP(setRecorder, setRequest)
	if setRecorder.Code != http.StatusOK {
		t.Fatalf("expected set status %d, got %d with body %q", http.StatusOK, setRecorder.Code, setRecorder.Body.String())
	}

	var mutation brainapi.ProjectEnvironmentMutation
	if err := json.Unmarshal(setRecorder.Body.Bytes(), &mutation); err != nil {
		t.Fatalf("expected set response decode to succeed, got error: %v", err)
	}
	if mutation.RevisionID == "" {
		t.Fatal("expected mutation revision ID")
	}
	if mutation.Entry == nil || !mutation.Entry.Secret || mutation.Entry.Value != nil {
		t.Fatalf("expected masked secret entry in mutation response, got %#v", mutation.Entry)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/projects/demo-app/env", nil)
	listRequest.Header.Set(headerAPIKey, "test-key")
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %q", http.StatusOK, listRecorder.Code, listRecorder.Body.String())
	}

	var entries []brainapi.ProjectEnvironmentEntry
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &entries); err != nil {
		t.Fatalf("expected list response decode to succeed, got error: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "PB_SUPERUSER_PASSWORD" || !entries[0].Secret || entries[0].Value != nil {
		t.Fatalf("expected masked secret entry, got %#v", entries)
	}
	if strings.Contains(listRecorder.Body.String(), "secret-pass") {
		t.Fatal("expected list response not to leak secret")
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/projects/demo-app/env/PB_SUPERUSER_PASSWORD", nil)
	deleteRequest.Header.Set(headerAPIKey, "test-key")
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("expected delete status %d, got %d with body %q", http.StatusOK, deleteRecorder.Code, deleteRecorder.Body.String())
	}
}

func TestProjectEnvironmentAPIRejectsInvalidEnvName(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)
	handler := newHandler(config{AuthMode: authModeDev, BrainAPIKey: "test-key"}, db, nil, nil, nil, managedProjectPocketBaseService{}, store)

	request := httptest.NewRequest(http.MethodPut, "/v1/projects/demo-app/env/PORT", strings.NewReader(`{"value":"8081","secret":false}`))
	request.Header.Set(headerAPIKey, "test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusBadRequest, errorCodeInvalidProjectEnv, `environment variable name "PORT" is reserved`)
}

func TestProjectEnvironmentAPIRequiresReauthInProdMode(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)
	apiKey, err := bootstrapAdminUser(db, "admin", "secret-pass")
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got error: %v", err)
	}
	handler := newHandler(config{AuthMode: authModeProd}, db, nil, nil, nil, managedProjectPocketBaseService{}, store)

	request := httptest.NewRequest(http.MethodPut, "/v1/projects/demo-app/env/PUBLIC_SITE_URL", strings.NewReader(`{"value":"https://example.com","secret":false}`))
	request.Header.Set(headerAPIKey, apiKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assertAPIError(t, recorder, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
}

func TestProjectEnvironmentAPIAllowsMutationWithProdReauth(t *testing.T) {
	db := newTestDB(t)
	store := newTestProjectConfigStore(t, db)
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
	handler := newHandler(config{AuthMode: authModeProd}, db, nil, nil, nil, managedProjectPocketBaseService{}, store)

	request := httptest.NewRequest(http.MethodPut, "/v1/projects/demo-app/env/PUBLIC_SITE_URL", strings.NewReader(`{"value":"https://example.com","secret":false}`))
	request.Header.Set(headerAPIKey, apiKey)
	request.Header.Set(headerReauthToken, reauthToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected set status %d, got %d with body %q", http.StatusOK, recorder.Code, recorder.Body.String())
	}
}
