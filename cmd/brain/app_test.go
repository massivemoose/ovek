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

func TestNewAppContainerSpec(t *testing.T) {
	spec := newAppContainerSpec(appSpec{
		ProjectName:  "demo-app",
		DeploymentID: "dep-123",
		JobID:        "job-123",
		ImageRef:     "alces-demo-app:dep-123",
		Network: projectNetwork{
			ID:   "network-123",
			Name: "demo-app-net",
		},
	})

	if spec.Name != "alces-demo-app-app-dep-123" {
		t.Fatalf("expected container name %q, got %q", "alces-demo-app-app-dep-123", spec.Name)
	}
	if spec.ProjectNetworkName != "demo-app-net" {
		t.Fatalf("expected project network name %q, got %q", "demo-app-net", spec.ProjectNetworkName)
	}
	if spec.Config.Image != "alces-demo-app:dep-123" {
		t.Fatalf("expected image %q, got %q", "alces-demo-app:dep-123", spec.Config.Image)
	}
	if !reflect.DeepEqual(spec.Config.Env, []string{appPortEnv, appPocketBaseURLEnv}) {
		t.Fatalf("expected env %#v, got %#v", []string{appPortEnv, appPocketBaseURLEnv}, spec.Config.Env)
	}

	wantLabels := map[string]string{
		managedLabelKey:       managedLabelValue,
		projectLabelKey:       "demo-app",
		roleLabelKey:          resourceRoleApp,
		deploymentLabelKey:    "dep-123",
		jobLabelKey:           "job-123",
		traefikEnableLabelKey: "true",
		"traefik.http.routers.app-demo-app-dep-123.entrypoints":               traefikWebEntrypoint,
		"traefik.http.routers.app-demo-app-dep-123.rule":                      "Host(`demo-app.localhost`)",
		"traefik.http.routers.app-demo-app-dep-123.service":                   "app-demo-app-dep-123",
		"traefik.http.services.app-demo-app-dep-123.loadbalancer.server.port": appRuntimePort,
		"traefik.docker.network":                                              alcesEdgeNetworkName,
	}
	if !reflect.DeepEqual(spec.Config.Labels, wantLabels) {
		t.Fatalf("expected labels %#v, got %#v", wantLabels, spec.Config.Labels)
	}
	if spec.HostConfig.NetworkMode != dockercontainer.NetworkMode("demo-app-net") {
		t.Fatalf("expected network mode %q, got %q", dockercontainer.NetworkMode("demo-app-net"), spec.HostConfig.NetworkMode)
	}
	if !spec.HostConfig.RestartPolicy.IsUnlessStopped() {
		t.Fatalf("expected restart policy unless-stopped, got %#v", spec.HostConfig.RestartPolicy)
	}
	if endpoint := spec.NetworkingConfig.EndpointsConfig["demo-app-net"]; endpoint == nil {
		t.Fatal("expected project network endpoint config")
	}
	if spec.EdgeEndpointConfig == nil {
		t.Fatal("expected edge network endpoint config")
	}
}

func TestDockerRuntimeEnsureProjectAppCreatesConnectsAndStartsManagedContainer(t *testing.T) {
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
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		containerCreateResponse: dockercontainer.CreateResponse{
			ID: "container-123",
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "alces-demo-app:dep-123")
	if err != nil {
		t.Fatalf("expected app provisioning to succeed, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerInspectName != "alces-demo-app-app-dep-123" {
		t.Fatalf("expected inspect name %q, got %q", "alces-demo-app-app-dep-123", client.containerInspectName)
	}
	if client.containerCreateName != "alces-demo-app-app-dep-123" {
		t.Fatalf("expected create name %q, got %q", "alces-demo-app-app-dep-123", client.containerCreateName)
	}
	if client.networkConnectNetwork != alcesEdgeNetworkName {
		t.Fatalf("expected edge network %q, got %q", alcesEdgeNetworkName, client.networkConnectNetwork)
	}
	if client.networkConnectID != "container-123" {
		t.Fatalf("expected connected container ID %q, got %q", "container-123", client.networkConnectID)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected started container ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectAppReusesRunningManagedContainer(t *testing.T) {
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Image: "alces-demo-app:dep-123",
				Env:   []string{appPortEnv, appPocketBaseURLEnv},
				Labels: map[string]string{
					managedLabelKey:       managedLabelValue,
					projectLabelKey:       "demo-app",
					roleLabelKey:          resourceRoleApp,
					deploymentLabelKey:    "dep-123",
					jobLabelKey:           "dep-123",
					traefikEnableLabelKey: "true",
					"traefik.http.routers.app-demo-app-dep-123.entrypoints":               traefikWebEntrypoint,
					"traefik.http.routers.app-demo-app-dep-123.rule":                      "Host(`demo-app.localhost`)",
					"traefik.http.routers.app-demo-app-dep-123.service":                   "app-demo-app-dep-123",
					"traefik.http.services.app-demo-app-dep-123.loadbalancer.server.port": appRuntimePort,
					"traefik.docker.network":                                              alcesEdgeNetworkName,
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net":       {},
					alcesEdgeNetworkName: {},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "alces-demo-app:dep-123")
	if err != nil {
		t.Fatalf("expected existing app container to be reused, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerCreateName != "" {
		t.Fatalf("expected create not to be called, got %q", client.containerCreateName)
	}
	if client.networkConnectNetwork != "" {
		t.Fatalf("expected edge connect not to be called, got %q", client.networkConnectNetwork)
	}
	if client.containerStartID != "" {
		t.Fatalf("expected running container not to be started again, got %q", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectAppConnectsStoppedContainerToEdgeNetworkAndStartsIt(t *testing.T) {
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: false,
				},
			},
			Config: &dockercontainer.Config{
				Image: "alces-demo-app:dep-123",
				Env:   []string{appPortEnv, appPocketBaseURLEnv},
				Labels: map[string]string{
					managedLabelKey:       managedLabelValue,
					projectLabelKey:       "demo-app",
					roleLabelKey:          resourceRoleApp,
					deploymentLabelKey:    "dep-123",
					jobLabelKey:           "dep-123",
					traefikEnableLabelKey: "true",
					"traefik.http.routers.app-demo-app-dep-123.entrypoints":               traefikWebEntrypoint,
					"traefik.http.routers.app-demo-app-dep-123.rule":                      "Host(`demo-app.localhost`)",
					"traefik.http.routers.app-demo-app-dep-123.service":                   "app-demo-app-dep-123",
					"traefik.http.services.app-demo-app-dep-123.loadbalancer.server.port": appRuntimePort,
					"traefik.docker.network":                                              alcesEdgeNetworkName,
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net": {},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "alces-demo-app:dep-123")
	if err != nil {
		t.Fatalf("expected stopped app container to be started, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.networkConnectNetwork != alcesEdgeNetworkName {
		t.Fatalf("expected edge network connect %q, got %q", alcesEdgeNetworkName, client.networkConnectNetwork)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected start ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectAppRejectsUnmanagedContainer(t *testing.T) {
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Image:  "alces-demo-app:dep-123",
				Env:    []string{appPortEnv, appPocketBaseURLEnv},
				Labels: map[string]string{},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net": {},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "alces-demo-app:dep-123")
	if err == nil {
		t.Fatal("expected unmanaged app container to be rejected")
	}
	if got := err.Error(); got != "alces-demo-app-app-dep-123 already exists but is not managed by alces" {
		t.Fatalf("expected unmanaged container error, got %q", got)
	}
}
