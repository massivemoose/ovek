package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseTaggedImageRef(t *testing.T) {
	ref, err := parseTaggedImageRef("localhost:5001/alces/demo-app:job-123")
	if err != nil {
		t.Fatalf("expected image ref to parse, got error: %v", err)
	}

	if ref.Host != "localhost:5001" {
		t.Fatalf("expected host %q, got %q", "localhost:5001", ref.Host)
	}
	if ref.Repository != "alces/demo-app" {
		t.Fatalf("expected repository %q, got %q", "alces/demo-app", ref.Repository)
	}
	if ref.Tag != "job-123" {
		t.Fatalf("expected tag %q, got %q", "job-123", ref.Tag)
	}
}

func TestParseTaggedImageRefRequiresTag(t *testing.T) {
	_, err := parseTaggedImageRef("localhost:5001/alces-demo-app")
	if err == nil {
		t.Fatal("expected missing tag to fail")
	}
}

func TestRegistryArtifactCleanerSkipsUnmanagedImageRefs(t *testing.T) {
	client := registryHTTPClientFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("expected unmanaged image ref not to make registry requests")
		return nil, nil
	})

	err := newRegistryArtifactCleaner("localhost:5001", "http://registry:5000", client).CleanupImage(context.Background(), "example.com/alces-demo-app:job-123")
	if !errors.Is(err, errUnmanagedImageRef) {
		t.Fatalf("expected unmanaged image error, got %v", err)
	}
}

func TestRegistryArtifactCleanerDeletesManifestByDigest(t *testing.T) {
	var requests []string
	client := registryHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.String())

		switch len(requests) {
		case 1:
			if got := request.Header.Get("Accept"); got != registryManifestAcceptHeader {
				t.Fatalf("expected accept header %q, got %q", registryManifestAcceptHeader, got)
			}

			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("manifest")),
			}
			response.Header.Set("Docker-Content-Digest", "sha256:abc123")

			return response, nil
		case 2:
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		default:
			t.Fatalf("unexpected request count %d", len(requests))
			return nil, nil
		}
	})

	err := newRegistryArtifactCleaner("localhost:5001", "http://registry:5000", client).CleanupImage(context.Background(), "localhost:5001/alces/demo-app:job-123")
	if err != nil {
		t.Fatalf("expected cleanup to succeed, got error: %v", err)
	}

	wantRequests := []string{
		"GET http://registry:5000/v2/alces/demo-app/manifests/job-123",
		"DELETE http://registry:5000/v2/alces/demo-app/manifests/sha256:abc123",
	}
	if len(requests) != len(wantRequests) {
		t.Fatalf("expected %d requests, got %d", len(wantRequests), len(requests))
	}
	for i := range wantRequests {
		if requests[i] != wantRequests[i] {
			t.Fatalf("expected request %d to be %q, got %q", i, wantRequests[i], requests[i])
		}
	}
}

func TestRegistryArtifactCleanerReturnsManifestNotFound(t *testing.T) {
	client := registryHTTPClientFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("missing")),
		}, nil
	})

	err := newRegistryArtifactCleaner("localhost:5001", "http://registry:5000", client).CleanupImage(context.Background(), "localhost:5001/alces-demo-app:job-123")
	if !errors.Is(err, errRegistryManifestNotFound) {
		t.Fatalf("expected manifest not found error, got %v", err)
	}
}

type registryHTTPClientFunc func(req *http.Request) (*http.Response, error)

func (client registryHTTPClientFunc) Do(req *http.Request) (*http.Response, error) {
	return client(req)
}
