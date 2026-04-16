package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/massivemoose/alces/internal/brainapi"
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
