package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/ovek/internal/brainapi"
)

func TestPingSendsAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("expected API key header %q, got %q", "test-key", got)
		}
		if got := r.URL.Path; got != "/v1/ping" {
			t.Fatalf("expected ping path %q, got %q", "/v1/ping", got)
		}
		_, _ = w.Write([]byte("pong"))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("expected ping to succeed, got error: %v", err)
	}
}

func TestGetProjectsDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RawQuery; got != "limit=20" {
			t.Fatalf("expected query %q, got %q", "limit=20", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]brainapi.ProjectSummary{{Name: "demo-app", Status: "running"}})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	projects, err := client.GetProjects(context.Background(), 20)
	if err != nil {
		t.Fatalf("expected projects request to succeed, got error: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "demo-app" {
		t.Fatalf("expected decoded project, got %+v", projects)
	}
}

func TestCreateDeploymentReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(brainapi.APIError{
			Code:    "repo_url_required",
			Message: "repoUrl is required",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	_, err = client.CreateDeployment(context.Background(), "demo-app", brainapi.CreateDeploymentRequest{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Code != "repo_url_required" {
		t.Fatalf("expected API error code %q, got %q", "repo_url_required", apiErr.Code)
	}
}

func TestCreateRunPostsCapsuleRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("expected method %q, got %q", http.MethodPost, got)
		}
		if got := r.URL.Path; got != "/v1/projects/demo-app/runs" {
			t.Fatalf("expected path %q, got %q", "/v1/projects/demo-app/runs", got)
		}
		var request brainapi.CreateRunRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected request to decode, got error: %v", err)
		}
		if request.CapsuleRef != "ghcr.io/example/demo:2026.05.01" {
			t.Fatalf("expected capsule ref %q, got %q", "ghcr.io/example/demo:2026.05.01", request.CapsuleRef)
		}
		_ = json.NewEncoder(w).Encode(brainapi.Job{
			ID:         "job_run",
			Status:     "queued",
			SourceType: brainapi.JobSourceTypeImage,
			SourceRef:  request.CapsuleRef,
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	job, err := client.CreateRun(context.Background(), "demo-app", brainapi.CreateRunRequest{
		CapsuleRef: "ghcr.io/example/demo:2026.05.01",
	})
	if err != nil {
		t.Fatalf("expected run request to succeed, got error: %v", err)
	}
	if job.ID != "job_run" || job.SourceType != brainapi.JobSourceTypeImage {
		t.Fatalf("expected run job response to decode, got %#v", job)
	}
}

func TestWorkflowTriggerTokenClientMethods(t *testing.T) {
	var created bool
	var listed bool
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/demo-app/workflows/digest/tokens":
			created = true
			var request brainapi.CreateWorkflowTriggerTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("expected create request to decode, got error: %v", err)
			}
			if request.Label != "app" {
				t.Fatalf("expected label %q, got %q", "app", request.Label)
			}
			_ = json.NewEncoder(w).Encode(brainapi.CreateWorkflowTriggerTokenResponse{ID: "tok_123", Token: "wft_tok_123_secret", Label: request.Label})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/demo-app/workflows/digest/tokens":
			listed = true
			_ = json.NewEncoder(w).Encode([]brainapi.WorkflowTriggerTokenSummary{{ID: "tok_123", Label: "app"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/demo-app/workflows/digest/tokens/tok_123":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	token, err := client.CreateWorkflowTriggerToken(context.Background(), "demo-app", "digest", brainapi.CreateWorkflowTriggerTokenRequest{Label: "app"})
	if err != nil {
		t.Fatalf("expected token create to succeed, got error: %v", err)
	}
	if token.Token != "wft_tok_123_secret" {
		t.Fatalf("expected plaintext token response, got %#v", token)
	}
	tokens, err := client.ListWorkflowTriggerTokens(context.Background(), "demo-app", "digest")
	if err != nil {
		t.Fatalf("expected token list to succeed, got error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != "tok_123" {
		t.Fatalf("expected token metadata, got %#v", tokens)
	}
	if err := client.DeleteWorkflowTriggerToken(context.Background(), "demo-app", "digest", "tok_123"); err != nil {
		t.Fatalf("expected token delete to succeed, got error: %v", err)
	}
	if !created || !listed || !deleted {
		t.Fatalf("expected create/list/delete calls, got create=%t list=%t delete=%t", created, listed, deleted)
	}
}

func TestBootstrapDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(brainapi.BootstrapAuthResponse{
			Username: "admin",
			APIKey:   "ak_test",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	response, err := client.Bootstrap(context.Background(), brainapi.BootstrapAuthRequest{
		Username: "admin",
		Password: "secret-pass",
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed, got error: %v", err)
	}
	if response.APIKey != "ak_test" || response.Username != "admin" {
		t.Fatalf("expected bootstrap response to decode, got %#v", response)
	}
}

func TestReauthSendsBaselineAPIKeyAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("expected api key header %q, got %q", "test-key", got)
		}
		_ = json.NewEncoder(w).Encode(brainapi.ReauthResponse{
			ReauthToken: "rt_test",
			ExpiresAt:   "2026-04-16T00:00:00Z",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	response, err := client.Reauth(context.Background(), "secret-pass")
	if err != nil {
		t.Fatalf("expected reauth to succeed, got error: %v", err)
	}
	if response.ReauthToken != "rt_test" {
		t.Fatalf("expected reauth token %q, got %q", "rt_test", response.ReauthToken)
	}
}

func TestUpsertRegistryCredentialSendsCredentialRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPut {
			t.Fatalf("expected method %q, got %q", http.MethodPut, got)
		}
		if got := r.URL.Path; got != "/v1/registry/credentials/ghcr.io" {
			t.Fatalf("expected registry credential path, got %q", got)
		}
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("expected api key header %q, got %q", "test-key", got)
		}

		var request brainapi.UpsertRegistryCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected request to decode, got error: %v", err)
		}
		if request.Username != "octo" || request.Password != "secret-token" {
			t.Fatalf("expected credential request, got %#v", request)
		}

		_ = json.NewEncoder(w).Encode(brainapi.RegistryCredential{
			Host:      "ghcr.io",
			Username:  "octo",
			UpdatedAt: "2026-05-09T00:00:00Z",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	credential, err := client.UpsertRegistryCredential(context.Background(), "ghcr.io", brainapi.UpsertRegistryCredentialRequest{
		Username: "octo",
		Password: "secret-token",
	})
	if err != nil {
		t.Fatalf("expected registry credential upsert to succeed, got error: %v", err)
	}
	if credential.Host != "ghcr.io" || credential.Username != "octo" {
		t.Fatalf("expected redacted registry credential response, got %#v", credential)
	}
}

func TestListRegistryCredentialsDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("expected method %q, got %q", http.MethodGet, got)
		}
		if got := r.URL.Path; got != "/v1/registry/credentials" {
			t.Fatalf("expected registry credential list path, got %q", got)
		}
		_ = json.NewEncoder(w).Encode([]brainapi.RegistryCredential{{
			Host:      "ghcr.io",
			Username:  "octo",
			UpdatedAt: "2026-05-09T00:00:00Z",
		}})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	credentials, err := client.ListRegistryCredentials(context.Background())
	if err != nil {
		t.Fatalf("expected registry credential list to succeed, got error: %v", err)
	}
	if len(credentials) != 1 || credentials[0].Host != "ghcr.io" {
		t.Fatalf("expected decoded registry credential list, got %#v", credentials)
	}
}

func TestDeleteRegistryCredentialSendsDeleteRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodDelete {
			t.Fatalf("expected method %q, got %q", http.MethodDelete, got)
		}
		if got := r.URL.Path; got != "/v1/registry/credentials/ghcr.io" {
			t.Fatalf("expected registry credential delete path, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	if err := client.DeleteRegistryCredential(context.Background(), "ghcr.io"); err != nil {
		t.Fatalf("expected registry credential delete to succeed, got error: %v", err)
	}
}

func TestCreateAPIKeyDecodesOneTimeKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("expected method %q, got %q", http.MethodPost, got)
		}
		if got := r.URL.Path; got != "/v1/auth/api-keys" {
			t.Fatalf("expected API key create path, got %q", got)
		}
		var request brainapi.CreateAPIKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected request to decode, got error: %v", err)
		}
		if request.Label != "ci" {
			t.Fatalf("expected label %q, got %q", "ci", request.Label)
		}
		_ = json.NewEncoder(w).Encode(brainapi.CreateAPIKeyResponse{
			ID:     "key_123",
			Label:  "ci",
			APIKey: "ak_key_123_secret",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	response, err := client.CreateAPIKey(context.Background(), brainapi.CreateAPIKeyRequest{Label: "ci"})
	if err != nil {
		t.Fatalf("expected API key create to succeed, got error: %v", err)
	}
	if response.APIKey != "ak_key_123_secret" || response.Label != "ci" {
		t.Fatalf("expected one-time API key response, got %#v", response)
	}
}

func TestListAPIKeysDecodesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("expected method %q, got %q", http.MethodGet, got)
		}
		if got := r.URL.Path; got != "/v1/auth/api-keys" {
			t.Fatalf("expected API key list path, got %q", got)
		}
		_ = json.NewEncoder(w).Encode([]brainapi.APIKeySummary{{
			ID:        "key_123",
			Label:     "bootstrap",
			CreatedAt: "2026-05-09T00:00:00Z",
		}})
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	keys, err := client.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("expected API key list to succeed, got error: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != "key_123" {
		t.Fatalf("expected decoded API key metadata, got %#v", keys)
	}
}

func TestRevokeAPIKeySendsDeleteRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodDelete {
			t.Fatalf("expected method %q, got %q", http.MethodDelete, got)
		}
		if got := r.URL.Path; got != "/v1/auth/api-keys/key_123" {
			t.Fatalf("expected API key revoke path, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	if err := client.RevokeAPIKey(context.Background(), "key_123"); err != nil {
		t.Fatalf("expected API key revoke to succeed, got error: %v", err)
	}
}

func TestChangePasswordSendsPasswordRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("expected method %q, got %q", http.MethodPost, got)
		}
		if got := r.URL.Path; got != "/v1/auth/password" {
			t.Fatalf("expected password change path, got %q", got)
		}
		var request brainapi.ChangePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("expected request to decode, got error: %v", err)
		}
		if request.CurrentPassword != "old-pass" || request.NewPassword != "new-pass" {
			t.Fatalf("expected password change request, got %#v", request)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatalf("expected client to construct, got error: %v", err)
	}

	if err := client.ChangePassword(context.Background(), brainapi.ChangePasswordRequest{CurrentPassword: "old-pass", NewPassword: "new-pass"}); err != nil {
		t.Fatalf("expected password change to succeed, got error: %v", err)
	}
}

func TestReadSSEDataEmitsOnlyDataLines(t *testing.T) {
	stream := strings.NewReader("event: message\ndata: first\n\ndata: second\n\n")
	var lines []string

	err := ReadSSEData(stream, func(line string) error {
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("expected SSE read to succeed, got error: %v", err)
	}

	if got, want := strings.Join(lines, ","), "first,second"; got != want {
		t.Fatalf("expected lines %q, got %q", want, got)
	}
}
