package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockernetwork "github.com/docker/docker/api/types/network"
)

func TestProjectResourceNames(t *testing.T) {
	projectName := "demo-app"

	if got := projectNetworkName(projectName); got != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", got)
	}
	if got := pocketBaseContainerName(projectName); got != "alces-demo-app-pb" {
		t.Fatalf("expected PocketBase container name %q, got %q", "alces-demo-app-pb", got)
	}
	if got := appContainerName(projectName, "dep-123"); got != "alces-demo-app-app-dep-123" {
		t.Fatalf("expected app container name %q, got %q", "alces-demo-app-app-dep-123", got)
	}
}

func TestManagedLabelsIncludesOptionalFields(t *testing.T) {
	labels := managedLabels(managedResourceMetadata{
		ProjectName:  "demo-app",
		Role:         resourceRoleApp,
		DeploymentID: "dep-123",
		JobID:        "job-456",
	})

	want := map[string]string{
		managedLabelKey:    managedLabelValue,
		projectLabelKey:    "demo-app",
		roleLabelKey:       resourceRoleApp,
		deploymentLabelKey: "dep-123",
		jobLabelKey:        "job-456",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("expected labels %#v, got %#v", want, labels)
	}
}

func TestDockerRuntimeEnsureProjectNetworkCreatesManagedNetworkWhenMissing(t *testing.T) {
	client := &fakeDockerClient{
		inspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		createResponse: dockernetwork.CreateResponse{
			ID: "network-123",
		},
	}
	runtime := newDockerRuntime(client)

	network, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected network creation to succeed, got error: %v", err)
	}

	if network.Name != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", network.Name)
	}
	if network.ID != "network-123" {
		t.Fatalf("expected network ID %q, got %q", "network-123", network.ID)
	}
	if client.inspectName != "demo-app-net" {
		t.Fatalf("expected inspect name %q, got %q", "demo-app-net", client.inspectName)
	}
	if client.createName != "demo-app-net" {
		t.Fatalf("expected created network name %q, got %q", "demo-app-net", client.createName)
	}
	if client.createOptions.Driver != projectNetworkDriver {
		t.Fatalf("expected network driver %q, got %q", projectNetworkDriver, client.createOptions.Driver)
	}

	wantLabels := map[string]string{
		managedLabelKey: managedLabelValue,
		projectLabelKey: "demo-app",
		roleLabelKey:    resourceRoleProjectNetwork,
	}
	if !reflect.DeepEqual(client.createOptions.Labels, wantLabels) {
		t.Fatalf("expected labels %#v, got %#v", wantLabels, client.createOptions.Labels)
	}
}

func TestDockerRuntimeEnsureProjectNetworkReusesExistingManagedNetwork(t *testing.T) {
	client := &fakeDockerClient{
		inspectResponse: dockernetwork.Inspect{
			ID:   "network-123",
			Name: "demo-app-net",
			Labels: map[string]string{
				managedLabelKey: managedLabelValue,
				projectLabelKey: "demo-app",
				roleLabelKey:    resourceRoleProjectNetwork,
			},
		},
	}
	runtime := newDockerRuntime(client)

	network, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected existing managed network to be reused, got error: %v", err)
	}

	if network.ID != "network-123" {
		t.Fatalf("expected network ID %q, got %q", "network-123", network.ID)
	}
	if network.Name != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", network.Name)
	}
	if client.createCalls != 0 {
		t.Fatalf("expected create not to be called, got %d calls", client.createCalls)
	}
}

func TestDockerRuntimeEnsureProjectNetworkRejectsUnmanagedExistingNetwork(t *testing.T) {
	client := &fakeDockerClient{
		inspectResponse: dockernetwork.Inspect{
			ID:     "network-123",
			Name:   "demo-app-net",
			Labels: map[string]string{},
		},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err == nil {
		t.Fatal("expected unmanaged network to be rejected")
	}
	if !strings.Contains(err.Error(), "already exists but is not managed by alces") {
		t.Fatalf("expected unmanaged network error, got %v", err)
	}
	if client.createCalls != 0 {
		t.Fatalf("expected create not to be called, got %d calls", client.createCalls)
	}
}

type fakeDockerClient struct {
	inspectName     string
	inspectResponse dockernetwork.Inspect
	inspectErr      error
	createCalls     int
	createName      string
	createOptions   dockernetwork.CreateOptions
	createResponse  dockernetwork.CreateResponse
	createErr       error
}

func (client *fakeDockerClient) NetworkInspect(_ context.Context, networkID string, _ dockernetwork.InspectOptions) (dockernetwork.Inspect, error) {
	client.inspectName = networkID
	return client.inspectResponse, client.inspectErr
}

func (client *fakeDockerClient) NetworkCreate(_ context.Context, name string, options dockernetwork.CreateOptions) (dockernetwork.CreateResponse, error) {
	client.createCalls++
	client.createName = name
	client.createOptions = options
	return client.createResponse, client.createErr
}
