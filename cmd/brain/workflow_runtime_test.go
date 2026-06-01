package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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
	spec := newWorkflowContainerSpec(run, "sha256:image-id", projectNetwork{Name: "demo-app-net"}, []string{"PUBLIC_SITE_URL=https://example.com"}, "/tmp/payload.json")

	if spec.Name != "ovek-demo-app-wf-run-123" {
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
		"OVEK_WORKFLOW_PAYLOAD_FILE=/var/run/ovek/workflow-payload.json",
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
	if len(spec.HostConfig.Mounts) != 1 {
		t.Fatalf("expected payload file mount, got %#v", spec.HostConfig.Mounts)
	}
	if spec.HostConfig.Mounts[0].Source != "/tmp/payload.json" || spec.HostConfig.Mounts[0].Target != workflowPayloadContainerPath || !spec.HostConfig.Mounts[0].ReadOnly {
		t.Fatalf("expected readonly payload file mount, got %#v", spec.HostConfig.Mounts[0])
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

	containerID, err := runtime.CreateWorkflowContainer(context.Background(), run, "", []string{"PUBLIC_SITE_URL=https://example.com"}, "/tmp/payload.json")
	if err != nil {
		t.Fatalf("expected workflow container creation to succeed, got error: %v", err)
	}
	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerCreateName != "ovek-demo-app-wf-run-123" {
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

func TestWorkflowContainerNameStaysShortForMaxLengthNames(t *testing.T) {
	projectName := "project-" + strings.Repeat("a", 55)
	workflowName := "workflow-" + strings.Repeat("b", 54)
	runID := strings.Repeat("c", 32)
	name := workflowContainerName(projectName, workflowName, runID)

	if name != "ovek-"+projectName+"-wf-"+runID {
		t.Fatalf("expected shortened workflow container name, got %q", name)
	}
	if len(name) > len("ovek-"+projectName+"-app-"+runID) {
		t.Fatalf("expected workflow container name %q to stay within app container name length class", name)
	}

	run := workflowRun{
		ID:           runID,
		ProjectName:  projectName,
		WorkflowName: workflowName,
	}
	spec := newWorkflowContainerSpec(run, "sha256:image-id", projectNetwork{Name: projectName + "-net"}, nil, "")
	if spec.Config.Labels[workflowLabelKey] != workflowName {
		t.Fatalf("expected full workflow name in labels, got %q", spec.Config.Labels[workflowLabelKey])
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
	}, "", nil, "")
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
