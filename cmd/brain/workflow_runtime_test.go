package main

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
)

func TestNewWorkflowContainerSpec(t *testing.T) {
	run := workflowRun{
		ID:           "run-123",
		ProjectName:  "demo-app",
		WorkflowName: "digest",
	}
	spec := newWorkflowContainerSpec(run, "sha256:image-id", projectNetwork{Name: "demo-app-net"}, []string{"PUBLIC_SITE_URL=https://example.com"})

	if spec.Name != "ovek-demo-app-workflow-digest-run-123" {
		t.Fatalf("expected workflow container name, got %q", spec.Name)
	}
	if spec.Config.Image != "sha256:image-id" {
		t.Fatalf("expected image ID, got %q", spec.Config.Image)
	}
	wantEnv := []string{
		appPortEnv,
		appPocketBaseURLEnv,
		"OVEK_PROJECT=demo-app",
		"OVEK_WORKFLOW=digest",
		"OVEK_WORKFLOW_RUN_ID=run-123",
		"PUBLIC_SITE_URL=https://example.com",
	}
	if !reflect.DeepEqual(spec.Config.Env, wantEnv) {
		t.Fatalf("expected env %#v, got %#v", wantEnv, spec.Config.Env)
	}
	wantLabels := map[string]string{
		managedLabelKey:     managedLabelValue,
		projectLabelKey:     "demo-app",
		roleLabelKey:        resourceRoleWorkflow,
		workflowLabelKey:    "digest",
		workflowRunLabelKey: "run-123",
	}
	if !reflect.DeepEqual(spec.Config.Labels, wantLabels) {
		t.Fatalf("expected labels %#v, got %#v", wantLabels, spec.Config.Labels)
	}
	if spec.HostConfig.NetworkMode != dockercontainer.NetworkMode("demo-app-net") {
		t.Fatalf("expected project network mode, got %#v", spec.HostConfig.NetworkMode)
	}
	if !spec.HostConfig.RestartPolicy.IsNone() {
		t.Fatalf("expected restart policy disabled, got %#v", spec.HostConfig.RestartPolicy)
	}
	if endpoint := spec.NetworkingConfig.EndpointsConfig["demo-app-net"]; endpoint == nil {
		t.Fatal("expected project network endpoint")
	}
}

func TestDockerRuntimeWorkflowContainerLifecycle(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectResponse: dockernetwork.Inspect{
			ID:   "network-123",
			Name: "demo-app-net",
			Labels: map[string]string{
				managedLabelKey: managedLabelValue,
				projectLabelKey: "demo-app",
				roleLabelKey:    resourceRoleProjectNetwork,
			},
		},
		containerCreateResponse: dockercontainer.CreateResponse{ID: "container-123"},
		containerWaitResponse:   dockercontainer.WaitResponse{StatusCode: 0},
	}
	runtime := newDockerRuntime(client)
	run := workflowRun{
		ID:             "run-123",
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		RuntimeImageID: "sha256:image-id",
	}

	containerID, err := runtime.CreateWorkflowContainer(context.Background(), run, "", []string{"PUBLIC_SITE_URL=https://example.com"})
	if err != nil {
		t.Fatalf("expected workflow container creation to succeed, got error: %v", err)
	}
	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerCreateName != "ovek-demo-app-workflow-digest-run-123" {
		t.Fatalf("expected create name, got %q", client.containerCreateName)
	}
	if client.containerCreateConfig.Image != "sha256:image-id" {
		t.Fatalf("expected runtime image ID, got %#v", client.containerCreateConfig)
	}

	if err := runtime.StartWorkflowContainer(context.Background(), containerID); err != nil {
		t.Fatalf("expected start to succeed, got error: %v", err)
	}
	exitCode, err := runtime.WaitWorkflowContainer(context.Background(), containerID)
	if err != nil {
		t.Fatalf("expected wait to succeed, got error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if err := runtime.RemoveWorkflowContainer(context.Background(), containerID); err != nil {
		t.Fatalf("expected remove to succeed, got error: %v", err)
	}
	if client.containerStartID != "container-123" || client.containerWaitID != "container-123" || client.containerRemoveID != "container-123" {
		t.Fatalf("expected lifecycle to use container ID, got start=%q wait=%q remove=%q", client.containerStartID, client.containerWaitID, client.containerRemoveID)
	}
}

func TestDockerRuntimeCreateWorkflowContainerEnsuresProjectNetwork(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		networkCreateResponse: dockernetwork.CreateResponse{
			ID: "network-123",
		},
		containerCreateResponse: dockercontainer.CreateResponse{ID: "container-123"},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.CreateWorkflowContainer(context.Background(), workflowRun{
		ID:             "run-123",
		ProjectName:    "demo-app",
		WorkflowName:   "digest",
		SourceImageRef: "ghcr.io/example/digest:latest",
	}, "", nil)
	if err != nil {
		t.Fatalf("expected workflow container creation to succeed, got error: %v", err)
	}
	if client.networkCreateName != "demo-app-net" {
		t.Fatalf("expected project network to be created, got %q", client.networkCreateName)
	}
	if client.containerCreateConfig.Image != "ghcr.io/example/digest:latest" {
		t.Fatalf("expected source image fallback, got %q", client.containerCreateConfig.Image)
	}
}
