package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
)

func TestNewAppContainerSpec(t *testing.T) {
	spec := newAppContainerSpec(appSpec{
		ProjectName:  "demo-app",
		DeploymentID: "dep-123",
		JobID:        "job-123",
		ImageRef:     "ovek-demo-app:dep-123",
		Network: projectNetwork{
			ID:   "network-123",
			Name: "demo-app-net",
		},
	})

	if spec.Name != "ovek-demo-app-app-dep-123" {
		t.Fatalf("expected container name %q, got %q", "ovek-demo-app-app-dep-123", spec.Name)
	}
	if spec.ProjectNetworkName != "demo-app-net" {
		t.Fatalf("expected project network name %q, got %q", "demo-app-net", spec.ProjectNetworkName)
	}
	if spec.Config.Image != "ovek-demo-app:dep-123" {
		t.Fatalf("expected image %q, got %q", "ovek-demo-app:dep-123", spec.Config.Image)
	}
	if !reflect.DeepEqual(spec.Config.Env, []string{appPortEnv, appPocketBaseURLEnv}) {
		t.Fatalf("expected env %#v, got %#v", []string{appPortEnv, appPocketBaseURLEnv}, spec.Config.Env)
	}

	wantLabels := map[string]string{
		managedLabelKey:    managedLabelValue,
		projectLabelKey:    "demo-app",
		roleLabelKey:       resourceRoleApp,
		deploymentLabelKey: "dep-123",
		jobLabelKey:        "job-123",
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

func TestNewAppContainerSpecIncludesProjectEnvironmentAfterBuiltins(t *testing.T) {
	spec := newAppContainerSpec(appSpec{
		ProjectName:  "demo-app",
		DeploymentID: "dep-123",
		JobID:        "job-123",
		ImageRef:     "ovek-demo-app:dep-123",
		Network: projectNetwork{
			ID:   "network-123",
			Name: "demo-app-net",
		},
		Env: []string{"PB_SUPERUSER_EMAIL=admin@example.com", "PB_SUPERUSER_PASSWORD=secret-pass"},
	})

	wantEnv := []string{
		appPortEnv,
		appPocketBaseURLEnv,
		"PB_SUPERUSER_EMAIL=admin@example.com",
		"PB_SUPERUSER_PASSWORD=secret-pass",
	}
	if !reflect.DeepEqual(spec.Config.Env, wantEnv) {
		t.Fatalf("expected env %#v, got %#v", wantEnv, spec.Config.Env)
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
	}, "ovek-demo-app:dep-123", nil)
	if err != nil {
		t.Fatalf("expected app provisioning to succeed, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerInspectName != "ovek-demo-app-app-dep-123" {
		t.Fatalf("expected inspect name %q, got %q", "ovek-demo-app-app-dep-123", client.containerInspectName)
	}
	if client.containerCreateName != "ovek-demo-app-app-dep-123" {
		t.Fatalf("expected create name %q, got %q", "ovek-demo-app-app-dep-123", client.containerCreateName)
	}
	if client.imagePullRef != "ovek-demo-app:dep-123" {
		t.Fatalf("expected image pull ref %q, got %q", "ovek-demo-app:dep-123", client.imagePullRef)
	}
	if client.networkConnectNetwork != ovekEdgeNetworkName {
		t.Fatalf("expected edge network %q, got %q", ovekEdgeNetworkName, client.networkConnectNetwork)
	}
	if client.networkConnectID != "container-123" {
		t.Fatalf("expected connected container ID %q, got %q", "container-123", client.networkConnectID)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected started container ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestPodmanRuntimeEnsureProjectAppUsesPodmanPuller(t *testing.T) {
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
	puller := &fakePodmanImagePuller{}
	runtime := &podmanRuntime{
		dockerRuntime:    newDockerRuntime(client),
		puller:           puller,
		registryInsecure: true,
	}

	containerID, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "localhost:5001/ovek-demo-app:dep-123", nil)
	if err != nil {
		t.Fatalf("expected podman app provisioning to succeed, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected docker image pull not to be used, got %q", client.imagePullRef)
	}
	if puller.imageRef != "localhost:5001/ovek-demo-app:dep-123" {
		t.Fatalf("expected podman puller image ref %q, got %q", "localhost:5001/ovek-demo-app:dep-123", puller.imageRef)
	}
	if !puller.registryInsecure {
		t.Fatal("expected podman puller to receive registryInsecure=true")
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
				Image: "ovek-demo-app:dep-123",
				Env:   []string{appPortEnv, appPocketBaseURLEnv},
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-123",
					jobLabelKey:        "dep-123",
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net":      {},
					ovekEdgeNetworkName: {},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "ovek-demo-app:dep-123", nil)
	if err != nil {
		t.Fatalf("expected existing app container to be reused, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.containerCreateName != "" {
		t.Fatalf("expected create not to be called, got %q", client.containerCreateName)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected image pull not to be called, got %q", client.imagePullRef)
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
				Image: "ovek-demo-app:dep-123",
				Env:   []string{appPortEnv, appPocketBaseURLEnv},
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-123",
					jobLabelKey:        "dep-123",
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
	}, "ovek-demo-app:dep-123", nil)
	if err != nil {
		t.Fatalf("expected stopped app container to be started, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.networkConnectNetwork != ovekEdgeNetworkName {
		t.Fatalf("expected edge network connect %q, got %q", ovekEdgeNetworkName, client.networkConnectNetwork)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected image pull not to be called for an existing container, got %q", client.imagePullRef)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected start ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectAppReturnsImagePullFailure(t *testing.T) {
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
		imagePullErr:        errors.New("pull failed"),
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectApp(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	}, "ovek-demo-app:dep-123", nil)
	if err == nil {
		t.Fatal("expected image pull failure")
	}
	if err.Error() != `pull image "ovek-demo-app:dep-123": pull failed` {
		t.Fatalf("expected image pull error, got %q", err.Error())
	}
	if client.containerCreateName != "" {
		t.Fatalf("expected create not to be called after pull failure, got %q", client.containerCreateName)
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
				Image:  "ovek-demo-app:dep-123",
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
	}, "ovek-demo-app:dep-123", nil)
	if err == nil {
		t.Fatal("expected unmanaged app container to be rejected")
	}
	if got := err.Error(); got != "ovek-demo-app-app-dep-123 already exists but is not managed by ovek" {
		t.Fatalf("expected unmanaged container error, got %q", got)
	}
}

func TestDockerRuntimeRemoveProjectAppStopsAndRemovesRunningManagedContainer(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-old",
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectApp(context.Background(), deploymentRecord{
		ID:               "dep-old",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-old",
	})
	if err != nil {
		t.Fatalf("expected app removal to succeed, got error: %v", err)
	}
	if client.containerInspectName != "ovek-demo-app-app-dep-old" {
		t.Fatalf("expected inspect name %q, got %q", "ovek-demo-app-app-dep-old", client.containerInspectName)
	}
	if client.containerStopID != "container-123" {
		t.Fatalf("expected stop ID %q, got %q", "container-123", client.containerStopID)
	}
	if client.containerStopOptions.Timeout == nil || *client.containerStopOptions.Timeout != appStopTimeoutSeconds {
		t.Fatalf("expected stop timeout %d, got %#v", appStopTimeoutSeconds, client.containerStopOptions.Timeout)
	}
	if client.containerRemoveID != "container-123" {
		t.Fatalf("expected remove ID %q, got %q", "container-123", client.containerRemoveID)
	}
}

func TestDockerRuntimeRemoveProjectAppIgnoresMissingContainer(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectApp(context.Background(), deploymentRecord{
		ID:               "dep-old",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-old",
	})
	if err != nil {
		t.Fatalf("expected missing app removal to be ignored, got error: %v", err)
	}
	if client.containerStopID != "" {
		t.Fatalf("expected stop not to be called, got %q", client.containerStopID)
	}
	if client.containerRemoveID != "" {
		t.Fatalf("expected remove not to be called, got %q", client.containerRemoveID)
	}
}

func TestDockerRuntimeRemoveProjectNetworkRemovesManagedNetwork(t *testing.T) {
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
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected network removal to succeed, got error: %v", err)
	}
	if client.networkInspectName != "demo-app-net" {
		t.Fatalf("expected inspect name %q, got %q", "demo-app-net", client.networkInspectName)
	}
	if client.networkRemoveID != "network-123" {
		t.Fatalf("expected removed network ID %q, got %q", "network-123", client.networkRemoveID)
	}
}

func TestDockerRuntimeRemoveProjectNetworkIgnoresMissingNetwork(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected missing network removal to be ignored, got error: %v", err)
	}
	if client.networkRemoveID != "" {
		t.Fatalf("expected remove not to be called, got %q", client.networkRemoveID)
	}
}

func TestDockerRuntimeListProjectAppsReturnsManagedProjectApps(t *testing.T) {
	client := &fakeDockerClient{
		containerListResponse: []dockercontainer.Summary{
			{
				Names:   []string{"/ovek-demo-app-app-dep-123"},
				Image:   "ovek-demo-app:dep-123",
				Created: 1_744_070_400,
				Labels: map[string]string{
					deploymentLabelKey: "dep-123",
				},
				State: "running",
			},
			{
				Names:   []string{"/ovek-demo-app-app-dep-456"},
				Image:   "ovek-demo-app:dep-456",
				Created: 1_744_070_500,
				Labels: map[string]string{
					deploymentLabelKey: "dep-456",
				},
				State: "exited",
			},
		},
	}
	runtime := newDockerRuntime(client)

	apps, err := runtime.ListProjectApps(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected project app listing to succeed, got error: %v", err)
	}

	want := []projectAppRuntime{
		{
			DeploymentID:            "dep-123",
			ProjectName:             "demo-app",
			AppContainerName:        "ovek-demo-app-app-dep-123",
			ImageRef:                "ovek-demo-app:dep-123",
			NetworkName:             "demo-app-net",
			PocketBaseContainerName: "ovek-demo-app-pb",
			CreatedAt:               "2025-04-08T00:00:00Z",
			Running:                 true,
		},
		{
			DeploymentID:            "dep-456",
			ProjectName:             "demo-app",
			AppContainerName:        "ovek-demo-app-app-dep-456",
			ImageRef:                "ovek-demo-app:dep-456",
			NetworkName:             "demo-app-net",
			PocketBaseContainerName: "ovek-demo-app-pb",
			CreatedAt:               "2025-04-08T00:01:40Z",
			Running:                 false,
		},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Fatalf("expected apps %#v, got %#v", want, apps)
	}
	if !client.containerListOptions.All {
		t.Fatal("expected container listing to include stopped containers")
	}
}

func TestDockerRuntimeReadProjectAppLogsReturnsCombinedManagedContainerLogs(t *testing.T) {
	var rawLogs bytes.Buffer
	if _, err := stdcopy.NewStdWriter(&rawLogs, stdcopy.Stdout).Write([]byte("app line\n")); err != nil {
		t.Fatalf("expected stdout frame write to succeed, got error: %v", err)
	}
	if _, err := stdcopy.NewStdWriter(&rawLogs, stdcopy.Stderr).Write([]byte("warn line\n")); err != nil {
		t.Fatalf("expected stderr frame write to succeed, got error: %v", err)
	}

	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-current",
				},
			},
		},
		containerLogsResponse: io.NopCloser(bytes.NewReader(rawLogs.Bytes())),
	}
	runtime := newDockerRuntime(client)

	logs, err := runtime.ReadProjectAppLogs(context.Background(), deploymentRecord{
		ID:               "dep-current",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-current",
	}, runtimeLogOptions{})
	if err != nil {
		t.Fatalf("expected app log read to succeed, got error: %v", err)
	}
	defer logs.Close()

	logBytes, err := io.ReadAll(logs)
	if err != nil {
		t.Fatalf("expected app log stream read to succeed, got error: %v", err)
	}

	if string(logBytes) != "app line\nwarn line\n" {
		t.Fatalf("expected combined log output %q, got %q", "app line\nwarn line\n", string(logBytes))
	}
	if client.containerInspectName != "ovek-demo-app-app-dep-current" {
		t.Fatalf("expected inspect name %q, got %q", "ovek-demo-app-app-dep-current", client.containerInspectName)
	}
	if client.containerLogsName != "ovek-demo-app-app-dep-current" {
		t.Fatalf("expected logs name %q, got %q", "ovek-demo-app-app-dep-current", client.containerLogsName)
	}
	if !client.containerLogsOptions.ShowStdout || !client.containerLogsOptions.ShowStderr {
		t.Fatalf("expected stdout and stderr logs to be enabled, got %#v", client.containerLogsOptions)
	}
	if client.containerLogsOptions.Follow {
		t.Fatalf("expected follow false by default, got %#v", client.containerLogsOptions)
	}
	if client.containerLogsOptions.Tail != "all" {
		t.Fatalf("expected tail %q, got %q", "all", client.containerLogsOptions.Tail)
	}
}

func TestDockerRuntimeReadProjectAppLogsSupportsFollow(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-current",
				},
			},
		},
		containerLogsResponse: io.NopCloser(strings.NewReader("")),
	}
	runtime := newDockerRuntime(client)

	logs, err := runtime.ReadProjectAppLogs(context.Background(), deploymentRecord{
		ID:               "dep-current",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-current",
	}, runtimeLogOptions{Follow: true})
	if err != nil {
		t.Fatalf("expected app log read to succeed, got error: %v", err)
	}
	_ = logs.Close()

	if !client.containerLogsOptions.Follow {
		t.Fatalf("expected follow true, got %#v", client.containerLogsOptions)
	}
}

func TestDockerRuntimeReadProjectAppLogsRejectsUnmanagedContainer(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{},
			},
		},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.ReadProjectAppLogs(context.Background(), deploymentRecord{
		ID:               "dep-current",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-current",
	}, runtimeLogOptions{})
	if err == nil {
		t.Fatal("expected unmanaged app log read to fail")
	}
	if got := err.Error(); got != "ovek-demo-app-app-dep-current already exists but is not managed by ovek" {
		t.Fatalf("expected unmanaged container error, got %q", got)
	}
	if client.containerLogsName != "" {
		t.Fatalf("expected container logs not to be called, got %q", client.containerLogsName)
	}
}

func TestDockerRuntimeReadProjectAppLogsReturnsContainerLogFailure(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{
					managedLabelKey:    managedLabelValue,
					projectLabelKey:    "demo-app",
					roleLabelKey:       resourceRoleApp,
					deploymentLabelKey: "dep-current",
				},
			},
		},
		containerLogsErr: errors.New("logs failed"),
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.ReadProjectAppLogs(context.Background(), deploymentRecord{
		ID:               "dep-current",
		ProjectName:      "demo-app",
		AppContainerName: "ovek-demo-app-app-dep-current",
	}, runtimeLogOptions{})
	if err == nil {
		t.Fatal("expected app log read to fail")
	}
	if got := err.Error(); got != `read app container "ovek-demo-app-app-dep-current" logs: logs failed` {
		t.Fatalf("expected container log error, got %q", got)
	}
}

func TestDockerRuntimeWaitForProjectAppReadySucceedsAfterRetry(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					ovekEdgeNetworkName: {
						IPAddress: "172.20.0.10",
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)
	attempts := 0
	sleeps := 0
	runtime.dialContext = func(_ context.Context, network string, address string) (net.Conn, error) {
		attempts++
		if network != "tcp" {
			t.Fatalf("expected network %q, got %q", "tcp", network)
		}
		if address != "172.20.0.10:8080" {
			t.Fatalf("expected address %q, got %q", "172.20.0.10:8080", address)
		}
		if attempts < 2 {
			return nil, errors.New("connection refused")
		}

		return fakeConn{}, nil
	}
	runtime.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps++
		if delay != appReadinessInterval {
			t.Fatalf("expected sleep delay %s, got %s", appReadinessInterval, delay)
		}

		return nil
	}

	err := runtime.WaitForProjectAppReady(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	})
	if err != nil {
		t.Fatalf("expected readiness wait to succeed, got error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 probe attempts, got %d", attempts)
	}
	if sleeps != 1 {
		t.Fatalf("expected 1 sleep between probes, got %d", sleeps)
	}
	if client.containerInspectName != "ovek-demo-app-app-dep-123" {
		t.Fatalf("expected inspect name %q, got %q", "ovek-demo-app-app-dep-123", client.containerInspectName)
	}
	if client.containerInspectCalls != 2 {
		t.Fatalf("expected 2 inspect attempts, got %d", client.containerInspectCalls)
	}
}

func TestDockerRuntimeWaitForProjectAppReadyTimesOut(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					ovekEdgeNetworkName: {
						IPAddress: "172.20.0.10",
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)
	runtime.dialContext = func(_ context.Context, _ string, _ string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	runtime.sleep = func(_ context.Context, _ time.Duration) error {
		return context.DeadlineExceeded
	}

	err := runtime.WaitForProjectAppReady(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	if got := err.Error(); !strings.Contains(got, "timed out waiting for app container") {
		t.Fatalf("expected timeout error, got %q", got)
	}
}

func TestDockerRuntimeWaitForProjectAppReadyReturnsSleepFailure(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					ovekEdgeNetworkName: {
						IPAddress: "172.20.0.10",
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)
	runtime.dialContext = func(_ context.Context, _ string, _ string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	runtime.sleep = func(_ context.Context, _ time.Duration) error {
		return errors.New("sleep failed")
	}

	err := runtime.WaitForProjectAppReady(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected readiness wait to fail")
	}
	if got := err.Error(); got != "wait for next readiness probe: sleep failed" {
		t.Fatalf("expected sleep failure error, got %q", got)
	}
}

func TestDockerRuntimeWaitForProjectAppReadyTimesOutWhenEdgeIPAddressIsMissing(t *testing.T) {
	runtime := newDockerRuntime(&fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					ovekEdgeNetworkName: {},
				},
			},
		},
	})
	runtime.sleep = func(_ context.Context, _ time.Duration) error {
		return context.DeadlineExceeded
	}

	err := runtime.WaitForProjectAppReady(context.Background(), job{
		ID:          "dep-123",
		ProjectName: "demo-app",
	})
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	if got := err.Error(); !strings.Contains(got, "has no IP address on network") {
		t.Fatalf("expected missing IP address error, got %q", got)
	}
}

type fakeConn struct{}

func (fakeConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (fakeConn) Write(buffer []byte) (int, error)   { return len(buffer), nil }
func (fakeConn) Close() error                       { return nil }
func (fakeConn) LocalAddr() net.Addr                { return fakeAddr("local") }
func (fakeConn) RemoteAddr() net.Addr               { return fakeAddr("remote") }
func (fakeConn) SetDeadline(_ time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(_ time.Time) error { return nil }

type fakeAddr string

func (addr fakeAddr) Network() string { return "tcp" }
func (addr fakeAddr) String() string  { return string(addr) }
